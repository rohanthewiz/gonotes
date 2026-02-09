package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// CategoryChange tracks category modifications for peer-to-peer sync.
// Mirrors the NoteChange pattern: each change records only what changed
// using delta storage via CategoryFragment.
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
// The bitmask indicates which fields are active/changed, using the same
// high-to-low bit ordering convention as NoteFragment.
type CategoryFragment struct {
	ID            int64          // Primary key
	Bitmask       int16          // Indicates which fields are active
	Name          sql.NullString // New name (if changed)
	Description   sql.NullString // New description (if changed)
	Subcategories sql.NullString // New subcategories JSON (if changed)
}

// Bitmask constants for CategoryFragment fields.
// Using same high-to-low bit ordering as note fragments for consistency.
const (
	CatFragmentName          = 0x80 // 128 - bit 7
	CatFragmentDescription   = 0x40 // 64  - bit 6
	CatFragmentSubcategories = 0x20 // 32  - bit 5
)

// CategoryChangeSyncPeer tracks which peers have received each category change.
// Parallel structure to NoteChangeSyncPeer.
type CategoryChangeSyncPeer struct {
	CategoryChangeID int64     // FK to category_changes
	PeerID           string    // Unique peer identifier
	SyncedAt         time.Time // When synced to peer
}

// SQL DDL constants for category change tracking tables

const DDLCreateCategoryFragmentsSequence = `
CREATE SEQUENCE IF NOT EXISTS category_fragments_id_seq START 1;
`

const DDLCreateCategoryFragmentsTable = `
CREATE TABLE IF NOT EXISTS category_fragments (
    id            BIGINT PRIMARY KEY DEFAULT nextval('category_fragments_id_seq'),
    bitmask       SMALLINT NOT NULL,
    name          VARCHAR,
    description   VARCHAR,
    subcategories VARCHAR
);
`

const DDLCreateCategoryChangesSequence = `
CREATE SEQUENCE IF NOT EXISTS category_changes_id_seq START 1;
`

const DDLCreateCategoryChangesTable = `
CREATE TABLE IF NOT EXISTS category_changes (
    id                   BIGINT PRIMARY KEY DEFAULT nextval('category_changes_id_seq'),
    guid                 VARCHAR NOT NULL UNIQUE,
    category_guid        VARCHAR NOT NULL,
    operation            INTEGER NOT NULL,
    category_fragment_id BIGINT,
    user                 VARCHAR,
    created_at           TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_fragment_id) REFERENCES category_fragments(id)
);
`

const DDLCreateCategoryChangesIndexCategoryGUID = `
CREATE INDEX IF NOT EXISTS idx_category_changes_category_guid ON category_changes(category_guid);
`

const DDLCreateCategoryChangesIndexCreatedAt = `
CREATE INDEX IF NOT EXISTS idx_category_changes_created_at ON category_changes(created_at);
`

const DDLCreateCategoryChangeSyncPeersTable = `
CREATE TABLE IF NOT EXISTS category_change_sync_peers (
    category_change_id BIGINT NOT NULL,
    peer_id            VARCHAR NOT NULL,
    synced_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (category_change_id, peer_id),
    FOREIGN KEY (category_change_id) REFERENCES category_changes(id)
);
`

const DDLCreateCategoryChangeSyncPeersIndexPeerID = `
CREATE INDEX IF NOT EXISTS idx_category_change_sync_peers_peer_id ON category_change_sync_peers(peer_id);
`

// computeCategoryChangeBitmask determines which fields changed between
// existing category and input. Returns 0 if nothing changed.
func computeCategoryChangeBitmask(existing Category, input CategoryInput) int16 {
	var bitmask int16 = 0

	if existing.Name != input.Name {
		bitmask |= CatFragmentName
	}

	// Compare description: sql.NullString vs *string
	if !sqlNullStringEqualsPointer(existing.Description, input.Description) {
		bitmask |= CatFragmentDescription
	}

	// Compare subcategories: need to compare JSON arrays
	existingSubcats := categorySubcatsToSlice(existing.Subcategories)
	if !stringSlicesEqual(existingSubcats, input.Subcategories) {
		bitmask |= CatFragmentSubcategories
	}

	return bitmask
}

// categorySubcatsToSlice parses the JSON subcategories column into a string slice
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

// stringSlicesEqual compares two string slices for equality (order-sensitive)
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
// Used for create operations where all fields are "changed".
func createCategoryFragmentFromInput(input CategoryInput, bitmask int16) CategoryFragment {
	fragment := CategoryFragment{
		Bitmask: bitmask,
	}

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

// createCategoryDeltaFragment creates a CategoryFragment with only changed fields.
// Used for update operations where only modified fields are stored.
func createCategoryDeltaFragment(input CategoryInput, bitmask int16) CategoryFragment {
	return createCategoryFragmentFromInput(input, bitmask)
}

// insertCategoryFragment saves a category fragment to the database.
// Returns the fragment ID or an error.
func insertCategoryFragment(fragment CategoryFragment) (int64, error) {
	query := `
		INSERT INTO category_fragments (bitmask, name, description, subcategories)
		VALUES (?, ?, ?, ?)
		RETURNING id
	`

	var fragmentID int64
	err := db.QueryRow(
		query,
		fragment.Bitmask,
		fragment.Name,
		fragment.Description,
		fragment.Subcategories,
	).Scan(&fragmentID)

	if err != nil {
		return 0, serr.Wrap(err, "failed to insert category fragment")
	}

	return fragmentID, nil
}

// insertCategoryChange records a category change to the database.
func insertCategoryChange(changeGUID, categoryGUID string, operation int32, fragmentID sql.NullInt64, user string) error {
	query := `
		INSERT INTO category_changes (guid, category_guid, operation, category_fragment_id, user)
		VALUES (?, ?, ?, ?, ?)
	`

	userVal := sql.NullString{}
	if user != "" {
		userVal = sql.NullString{String: user, Valid: true}
	}

	_, err := db.Exec(query, changeGUID, categoryGUID, operation, fragmentID, userVal)
	if err != nil {
		return serr.Wrap(err, "failed to insert category change")
	}

	return nil
}

// GetCategoryFragment retrieves a category fragment by ID.
// Returns nil if not found.
func GetCategoryFragment(id int64) (*CategoryFragment, error) {
	query := `
		SELECT id, bitmask, name, description, subcategories
		FROM category_fragments
		WHERE id = ?
	`

	fragment := &CategoryFragment{}
	err := db.QueryRow(query, id).Scan(
		&fragment.ID,
		&fragment.Bitmask,
		&fragment.Name,
		&fragment.Description,
		&fragment.Subcategories,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category fragment")
	}

	return fragment, nil
}

// CategoryChangeOutput provides a complete view of a category change for API responses
type CategoryChangeOutput struct {
	ID                 int64            `json:"id"`
	GUID               string           `json:"guid"`
	CategoryGUID       string           `json:"category_guid"`
	Operation          int32            `json:"operation"`
	CategoryFragmentID sql.NullInt64    `json:"category_fragment_id,omitempty"`
	Fragment           *CategoryFragment `json:"fragment,omitempty"`
	User               sql.NullString   `json:"user,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
}

// GetCategoryChangeWithFragment retrieves a complete category change with its fragment.
func GetCategoryChangeWithFragment(changeID int64) (*CategoryChangeOutput, error) {
	query := `
		SELECT id, guid, category_guid, operation, category_fragment_id, user, created_at
		FROM category_changes
		WHERE id = ?
	`

	change := &CategoryChangeOutput{}
	err := db.QueryRow(query, changeID).Scan(
		&change.ID,
		&change.GUID,
		&change.CategoryGUID,
		&change.Operation,
		&change.CategoryFragmentID,
		&change.User,
		&change.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category change")
	}

	// Load fragment if present
	if change.CategoryFragmentID.Valid {
		fragment, err := GetCategoryFragment(change.CategoryFragmentID.Int64)
		if err != nil {
			return nil, serr.Wrap(err, "failed to get associated category fragment")
		}
		change.Fragment = fragment
	}

	return change, nil
}

// GetUnsentCategoryChangesForPeer retrieves category changes not yet sent to a peer.
// Returns up to 'limit' changes ordered by creation time (oldest first).
func GetUnsentCategoryChangesForPeer(peerID string, limit int) ([]CategoryChange, error) {
	query := `
		SELECT cc.id, cc.guid, cc.category_guid, cc.operation, cc.category_fragment_id, cc.user, cc.created_at
		FROM category_changes cc
		WHERE cc.id NOT IN (
			SELECT category_change_id
			FROM category_change_sync_peers
			WHERE peer_id = ?
		)
		ORDER BY cc.created_at ASC
		LIMIT ?
	`

	rows, err := db.Query(query, peerID, limit)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query unsent category changes for peer")
	}
	defer rows.Close()

	var changes []CategoryChange
	for rows.Next() {
		var change CategoryChange
		err := rows.Scan(
			&change.ID,
			&change.GUID,
			&change.CategoryGUID,
			&change.Operation,
			&change.CategoryFragmentID,
			&change.User,
			&change.CreatedAt,
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

// MarkCategoryChangeSyncedToPeer records that a category change has been synced to a peer.
func MarkCategoryChangeSyncedToPeer(categoryChangeID int64, peerID string) error {
	query := `
		INSERT INTO category_change_sync_peers (category_change_id, peer_id)
		VALUES (?, ?)
	`

	_, err := db.Exec(query, categoryChangeID, peerID)
	if err != nil {
		return serr.Wrap(err, "failed to mark category change as synced to peer")
	}

	return nil
}

// recordCategoryCreateChange records a full-fragment change when a category is created.
// Non-blocking: logs errors rather than failing the create operation.
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

// recordCategoryUpdateChange records a delta-fragment change when a category is updated.
// Only fields that actually changed are stored in the fragment.
// Non-blocking: logs errors rather than failing the update operation.
func recordCategoryUpdateChange(existing Category, updated Category, input CategoryInput) {
	bitmask := computeCategoryChangeBitmask(existing, input)
	if bitmask == 0 {
		return // Nothing changed
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

// recordCategoryDeleteChange records a delete change (no fragment, null fragment ID).
// Non-blocking: logs errors rather than failing the delete operation.
func recordCategoryDeleteChange(categoryGUID string) {
	if err := insertCategoryChange(GenerateChangeGUID(), categoryGUID, OperationDelete,
		sql.NullInt64{}, ""); err != nil {
		logger.LogErr(err, "failed to record category delete change", "category_guid", categoryGUID)
	}
}

// NoteCategoryMappingSnapshot captures a note's complete category state for sync.
// Each entry represents one category-to-note relationship including subcategory selections.
// This is stored as JSON in the NoteFragment.Categories field.
type NoteCategoryMappingSnapshot struct {
	CategoryGUID          string   `json:"category_guid"`
	SelectedSubcategories []string `json:"selected_subcategories,omitempty"`
}

// recordNoteCategoryMappingChange captures the full category state of a note
// and records it as a note change with the FragmentCategories bitmask.
// This approach stores a snapshot of all category mappings, enabling the
// sync consumer to replace the entire set atomically on the receiving end.
// Non-blocking: logs errors rather than failing the operation.
func recordNoteCategoryMappingChange(noteID int64) {
	// Get the note's GUID for change tracking
	var noteGUID string
	err := cacheDB.QueryRow(`SELECT guid FROM notes WHERE id = ? AND deleted_at IS NULL`, noteID).Scan(&noteGUID)
	if err != nil {
		logger.LogErr(err, "failed to get note GUID for category mapping change", "note_id", noteID)
		return
	}

	// Query all category mappings for this note, using category GUIDs
	query := `SELECT c.guid, nc.subcategories
		FROM note_categories nc
		INNER JOIN categories c ON nc.category_id = c.id
		WHERE nc.note_id = ?
		ORDER BY c.guid`

	rows, err := cacheDB.Query(query, noteID)
	if err != nil {
		logger.LogErr(err, "failed to query note categories for mapping change", "note_id", noteID)
		return
	}
	defer rows.Close()

	var mappings []NoteCategoryMappingSnapshot
	for rows.Next() {
		var (
			catGUID     string
			subcatsJSON sql.NullString
		)
		if err := rows.Scan(&catGUID, &subcatsJSON); err != nil {
			logger.LogErr(err, "failed to scan category mapping", "note_id", noteID)
			continue
		}

		mapping := NoteCategoryMappingSnapshot{CategoryGUID: catGUID}
		if subcatsJSON.Valid && subcatsJSON.String != "" {
			var subcats []string
			if err := json.Unmarshal([]byte(subcatsJSON.String), &subcats); err == nil {
				mapping.SelectedSubcategories = subcats
			}
		}
		mappings = append(mappings, mapping)
	}

	// Serialize the full mapping set as JSON
	mappingsJSON, err := json.Marshal(mappings)
	if err != nil {
		logger.LogErr(err, "failed to marshal category mappings", "note_id", noteID)
		return
	}

	// Create a note fragment with only the Categories bitmask set
	fragment := NoteFragment{
		Bitmask:    FragmentCategories,
		Categories: sql.NullString{String: string(mappingsJSON), Valid: true},
	}

	fragmentID, err := insertNoteFragment(fragment)
	if err != nil {
		logger.LogErr(err, "failed to insert category mapping fragment", "note_guid", noteGUID)
		return
	}

	if err := insertNoteChange(GenerateChangeGUID(), noteGUID, OperationUpdate,
		sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
		logger.LogErr(err, "failed to record category mapping change", "note_guid", noteGUID)
	}
}

// GetCategoryByGUID retrieves a category by its GUID from cache.
// Used for sync operations where cross-machine identity is needed.
func GetCategoryByGUID(guid string) (*Category, error) {
	query := `SELECT id, guid, name, description, subcategories, created_at, updated_at
		FROM categories WHERE guid = ?`

	var category Category
	err := cacheDB.QueryRow(query, guid).Scan(
		&category.ID,
		&category.GUID,
		&category.Name,
		&category.Description,
		&category.Subcategories,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category by GUID")
	}

	return &category, nil
}
