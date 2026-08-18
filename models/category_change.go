package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// category_change.go tracks category modifications for peer-to-peer sync.
// Categories are a public-only table, so all category change-tracking
// (category_changes / category_fragments / category_change_sync_peers)
// lives in the public database — no fan-out needed here.
//
// The one exception is recordNoteCategoryMappingChange: a note's category
// links changing is recorded as a NOTE change, which must land in the
// note's own database, and it resolves category GUIDs against the public
// catalog.

// CategoryChange tracks one category modification.
type CategoryChange struct {
	ID                 int64          // Primary key
	GUID               string         // Unique identifier for this change
	CategoryGUID       string         // GUID of the affected category
	Operation          int32          // 1: Create, 2: Update, 3: Delete, 9: Sync
	CategoryFragmentID sql.NullInt64  // FK to category_fragments (null for deletes)
	User               sql.NullString // User who made the change
	CreatedAt          time.Time      // Immutable timestamp
}

// CategoryFragment stores delta information for category changes.
type CategoryFragment struct {
	ID            int64          // Primary key
	Bitmask       int16          // Indicates which fields are active
	Name          sql.NullString // New name (if changed)
	Description   sql.NullString // New description (if changed)
	Subcategories sql.NullString // New subcategories JSON (if changed)
}

// Bitmask constants for CategoryFragment fields.
const (
	CatFragmentName          = 0x80 // 128 - bit 7
	CatFragmentDescription   = 0x40 // 64  - bit 6
	CatFragmentSubcategories = 0x20 // 32  - bit 5
)

// CategoryChangeSyncPeer tracks which peers have received each change.
type CategoryChangeSyncPeer struct {
	CategoryChangeID int64
	PeerID           string
	SyncedAt         time.Time
}

// computeCategoryChangeBitmask determines which fields changed.
func computeCategoryChangeBitmask(existing Category, input CategoryInput) int16 {
	var bitmask int16 = 0
	if existing.Name != input.Name {
		bitmask |= CatFragmentName
	}
	if !sqlNullStringEqualsPointer(existing.Description, input.Description) {
		bitmask |= CatFragmentDescription
	}
	existingSubcats := categorySubcatsToSlice(existing.Subcategories)
	if !stringSlicesEqual(existingSubcats, input.Subcategories) {
		bitmask |= CatFragmentSubcategories
	}
	return bitmask
}

// categorySubcatsToSlice parses the JSON subcategories column into a slice.
func categorySubcatsToSlice(subcats sql.NullString) []string {
	if !subcats.Valid || subcats.String == "" {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(subcats.String), &result); err != nil {
		return nil
	}
	return result
}

// stringSlicesEqual compares two string slices (order-sensitive).
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// createCategoryFragmentFromInput creates a full CategoryFragment from input.
func createCategoryFragmentFromInput(input CategoryInput, bitmask int16) CategoryFragment {
	fragment := CategoryFragment{Bitmask: bitmask}
	if bitmask&CatFragmentName != 0 {
		fragment.Name = sql.NullString{String: input.Name, Valid: true}
	}
	if bitmask&CatFragmentDescription != 0 && input.Description != nil {
		fragment.Description = sql.NullString{String: *input.Description, Valid: true}
	}
	if bitmask&CatFragmentSubcategories != 0 && len(input.Subcategories) > 0 {
		jsonBytes, err := json.Marshal(input.Subcategories)
		if err == nil {
			fragment.Subcategories = sql.NullString{String: string(jsonBytes), Valid: true}
		}
	}
	return fragment
}

// createCategoryDeltaFragment creates a CategoryFragment with changed fields.
func createCategoryDeltaFragment(input CategoryInput, bitmask int16) CategoryFragment {
	return createCategoryFragmentFromInput(input, bitmask)
}

// insertCategoryFragment saves a category fragment to the public database.
func insertCategoryFragment(fragment CategoryFragment) (int64, error) {
	query := `
		INSERT INTO category_fragments (id, bitmask, name, description, subcategories)
		VALUES (nextval('category_fragments_id_seq'), ?, ?, ?, ?)
		RETURNING id
	`
	var fragmentID int64
	err := pubDB.QueryRow(query, fragment.Bitmask, fragment.Name, fragment.Description, fragment.Subcategories).Scan(&fragmentID)
	if err != nil {
		return 0, serr.Wrap(err, "failed to insert category fragment")
	}
	return fragmentID, nil
}

// insertCategoryChange records a category change to the public database.
func insertCategoryChange(changeGUID, categoryGUID string, operation int32, fragmentID sql.NullInt64, user string) error {
	query := `
		INSERT INTO category_changes (id, guid, category_guid, operation, category_fragment_id, change_user)
		VALUES (nextval('category_changes_id_seq'), ?, ?, ?, ?, ?)
	`
	userVal := sql.NullString{}
	if user != "" {
		userVal = sql.NullString{String: user, Valid: true}
	}
	_, err := pubDB.Exec(query, changeGUID, categoryGUID, operation, fragmentID, userVal)
	if err != nil {
		return serr.Wrap(err, "failed to insert category change")
	}
	return nil
}

// GetCategoryFragment retrieves a category fragment by id. Returns nil if
// not found.
func GetCategoryFragment(id int64) (*CategoryFragment, error) {
	query := `
		SELECT id, bitmask, name, description, subcategories
		FROM category_fragments
		WHERE id = ?
	`
	fragment := &CategoryFragment{}
	err := pubDB.QueryRow(query, id).Scan(
		&fragment.ID, &fragment.Bitmask, &fragment.Name, &fragment.Description, &fragment.Subcategories,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category fragment")
	}
	return fragment, nil
}

// CategoryChangeOutput provides a complete view of a category change.
type CategoryChangeOutput struct {
	ID                 int64             `json:"id"`
	GUID               string            `json:"guid"`
	CategoryGUID       string            `json:"category_guid"`
	Operation          int32             `json:"operation"`
	CategoryFragmentID sql.NullInt64     `json:"category_fragment_id,omitempty"`
	Fragment           *CategoryFragment `json:"fragment,omitempty"`
	User               sql.NullString    `json:"user,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
}

// GetCategoryChangeWithFragment retrieves a complete category change.
func GetCategoryChangeWithFragment(changeID int64) (*CategoryChangeOutput, error) {
	query := `
		SELECT id, guid, category_guid, operation, category_fragment_id, change_user, created_at
		FROM category_changes
		WHERE id = ?
	`
	change := &CategoryChangeOutput{}
	err := pubDB.QueryRow(query, changeID).Scan(
		&change.ID, &change.GUID, &change.CategoryGUID, &change.Operation,
		&change.CategoryFragmentID, &change.User, &change.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category change")
	}

	if change.CategoryFragmentID.Valid {
		fragment, err := GetCategoryFragment(change.CategoryFragmentID.Int64)
		if err != nil {
			return nil, serr.Wrap(err, "failed to get associated category fragment")
		}
		change.Fragment = fragment
	}
	return change, nil
}

// GetUnsentCategoryChangesForPeer retrieves category changes not yet sent
// to a peer, oldest first, capped at limit. All category change data lives
// in the public database, so this is a single-database query.
func GetUnsentCategoryChangesForPeer(peerID string, userGUID string, limit int) ([]CategoryChange, error) {
	var query string
	var args []any

	if userGUID != "" {
		query = `
			SELECT cc.id, cc.guid, cc.category_guid, cc.operation, cc.category_fragment_id, cc.change_user, cc.created_at
			FROM category_changes cc
			INNER JOIN categories c ON cc.category_guid = c.guid AND c.created_by = ?
			WHERE cc.id NOT IN (
				SELECT category_change_id FROM category_change_sync_peers WHERE peer_id = ?
			)
			ORDER BY cc.created_at ASC`
		args = []any{userGUID, peerID}
	} else {
		query = `
			SELECT cc.id, cc.guid, cc.category_guid, cc.operation, cc.category_fragment_id, cc.change_user, cc.created_at
			FROM category_changes cc
			WHERE cc.id NOT IN (
				SELECT category_change_id FROM category_change_sync_peers WHERE peer_id = ?
			)
			ORDER BY cc.created_at ASC`
		args = []any{peerID}
	}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := pubDB.Query(query, args...)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query unsent category changes for peer")
	}
	defer rows.Close()

	var changes []CategoryChange
	for rows.Next() {
		var change CategoryChange
		err := rows.Scan(
			&change.ID, &change.GUID, &change.CategoryGUID, &change.Operation,
			&change.CategoryFragmentID, &change.User, &change.CreatedAt,
		)
		if err != nil {
			logger.LogErr(err, "failed to scan category change row")
			continue
		}
		changes = append(changes, change)
	}
	if err = rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating category changes")
	}
	return changes, nil
}

// MarkCategoryChangeSyncedToPeer records that a category change has been
// synced to a peer (public database).
func MarkCategoryChangeSyncedToPeer(categoryChangeID int64, peerID string) error {
	_, err := pubDB.Exec(
		`INSERT INTO category_change_sync_peers (category_change_id, peer_id) VALUES (?, ?)`,
		categoryChangeID, peerID,
	)
	if err != nil {
		return serr.Wrap(err, "failed to mark category change as synced to peer")
	}
	return nil
}

// recordCategoryCreateChange records a full-fragment create change.
func recordCategoryCreateChange(category Category, input CategoryInput) {
	bitmask := int16(CatFragmentName | CatFragmentDescription | CatFragmentSubcategories)
	fragment := createCategoryFragmentFromInput(input, bitmask)
	fragmentID, err := insertCategoryFragment(fragment)
	if err != nil {
		logger.LogErr(err, "failed to record category create fragment", "category_guid", category.GUID)
		return
	}
	if err := insertCategoryChange(GenerateChangeGUID(), category.GUID, OperationCreate,
		sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
		logger.LogErr(err, "failed to record category create change", "category_guid", category.GUID)
	}
}

// recordCategoryUpdateChange records a delta-fragment update change.
func recordCategoryUpdateChange(existing Category, updated Category, input CategoryInput) {
	bitmask := computeCategoryChangeBitmask(existing, input)
	if bitmask == 0 {
		return
	}
	fragment := createCategoryDeltaFragment(input, bitmask)
	fragmentID, err := insertCategoryFragment(fragment)
	if err != nil {
		logger.LogErr(err, "failed to record category update fragment", "category_guid", updated.GUID)
		return
	}
	if err := insertCategoryChange(GenerateChangeGUID(), updated.GUID, OperationUpdate,
		sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
		logger.LogErr(err, "failed to record category update change", "category_guid", updated.GUID)
	}
}

// recordCategoryDeleteChange records a delete change (no fragment).
func recordCategoryDeleteChange(categoryGUID string) {
	if err := insertCategoryChange(GenerateChangeGUID(), categoryGUID, OperationDelete,
		sql.NullInt64{}, ""); err != nil {
		logger.LogErr(err, "failed to record category delete change", "category_guid", categoryGUID)
	}
}

// NoteCategoryMappingSnapshot captures a note's complete category state for
// sync, stored as JSON in NoteFragment.Categories.
type NoteCategoryMappingSnapshot struct {
	CategoryGUID          string   `json:"category_guid"`
	SelectedSubcategories []string `json:"selected_subcategories,omitempty"`
}

// noteCategoryMappingsJSON builds the portable snapshot of a note's category
// state: every link the note has, with the category resolved from a local id
// to its GUID and the link's own subcategory selection carried along.
//
// Extracted so the two writers of a categories fragment produce byte-identical
// content — recordNoteCategoryMappingChange when a link changes, and the
// compactor when it rebuilds a note's pending changes into one snapshot. A
// second, subtly different serializer here would show up as a phantom category
// diff on the hub.
func noteCategoryMappingsJSON(noteID int64) (string, error) {
	// Read the note's category links from its database, then resolve each
	// category id to its GUID via the public catalog.
	links, err := noteLinks(noteID)
	if err != nil {
		return "", serr.Wrap(err, "failed to query note categories for mapping snapshot")
	}

	var mappings []NoteCategoryMappingSnapshot
	for _, l := range links {
		cat, err := getCategoryByID(l.CategoryID)
		if err != nil || cat == nil {
			continue
		}
		mapping := NoteCategoryMappingSnapshot{CategoryGUID: cat.GUID}
		if l.Subcategories.Valid && l.Subcategories.String != "" {
			var subcats []string
			if err := json.Unmarshal([]byte(l.Subcategories.String), &subcats); err == nil {
				mapping.SelectedSubcategories = subcats
			}
		}
		mappings = append(mappings, mapping)
	}
	// Deterministic order (by category GUID), matching the old ORDER BY.
	sortMappingsByGUID(mappings)

	mappingsJSON, err := json.Marshal(mappings)
	if err != nil {
		return "", serr.Wrap(err, "failed to marshal category mappings")
	}
	return string(mappingsJSON), nil
}

// recordNoteCategoryMappingChange snapshots a note's full category state and
// records it as a NOTE change in the note's own database. Category ids in
// the link rows are resolved to GUIDs against the public catalog so the
// snapshot is portable across machines. Non-blocking.
func recordNoteCategoryMappingChange(noteID int64) {
	en := engineForNoteID(noteID)
	if en == nil {
		logger.Warn("recordNoteCategoryMappingChange: note not found", "note_id", noteID)
		return
	}

	var noteGUID string
	if err := en.QueryRow(`SELECT guid FROM notes WHERE id = ? AND deleted_at IS NULL`, noteID).Scan(&noteGUID); err != nil {
		logger.LogErr(err, "failed to get note GUID for category mapping change", "note_id", noteID)
		return
	}

	mappingsJSON, err := noteCategoryMappingsJSON(noteID)
	if err != nil {
		logger.LogErr(err, "failed to snapshot note categories for mapping change", "note_id", noteID)
		return
	}

	fragment := NoteFragment{
		Bitmask:    FragmentCategories,
		Categories: sql.NullString{String: mappingsJSON, Valid: true},
	}

	fragmentID, err := insertNoteFragment(en, fragment)
	if err != nil {
		logger.LogErr(err, "failed to insert category mapping fragment", "note_guid", noteGUID)
		return
	}
	if err := insertNoteChange(en, GenerateChangeGUID(), noteGUID, OperationUpdate,
		sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
		logger.LogErr(err, "failed to record category mapping change", "note_guid", noteGUID)
	}
}

// sortMappingsByGUID orders snapshots by category GUID for deterministic
// fragment content (parity with the old SQL ORDER BY c.guid).
func sortMappingsByGUID(m []NoteCategoryMappingSnapshot) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j-1].CategoryGUID > m[j].CategoryGUID; j-- {
			m[j-1], m[j] = m[j], m[j-1]
		}
	}
}

// GetCategoryByGUID retrieves a category by GUID from the public catalog.
// Does NOT filter by user — sync internals need any category by GUID.
func GetCategoryByGUID(guid string) (*Category, error) {
	var category Category
	err := scanCategory(pubDB.QueryRow(`SELECT `+categoryCols+` FROM categories WHERE guid = ?`, guid), &category)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category by GUID")
	}
	return &category, nil
}
