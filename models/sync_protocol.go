package models

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Unified Sync Protocol
//
// This file defines the unified sync envelope (SyncChange) that merges both
// note and category changes into a single, chronologically ordered stream.
// Spokes pull changes from the hub via GetUnifiedChangesForPeer(), and push
// changes back via ApplyIncomingSyncChange(). The protocol is designed for
// idempotent operation — replaying the same change is a no-op.
//
// Wire format uses JSON-friendly fragment types (*string instead of sql.Null*)
// to avoid serialization issues with nullable database types.
// ============================================================================

// SyncChange is the unified envelope for the sync protocol.
// Carries both note and category changes in a single, chronologically
// ordered stream. AuthoredAt preserves the source machine's authoring
// timestamp — receivers must never overwrite it with CURRENT_TIMESTAMP.
type SyncChange struct {
	ID         int64     `json:"id"`
	GUID       string    `json:"guid"`
	EntityType string    `json:"entity_type"` // "note" or "category"
	EntityGUID string    `json:"entity_guid"`
	Operation  int32     `json:"operation"`
	Fragment   any       `json:"fragment,omitempty"` // *NoteFragmentOutput or *CategoryFragmentOutput
	AuthoredAt time.Time `json:"authored_at"`
	User       string    `json:"user,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// NoteFragmentOutput is the JSON-friendly version of NoteFragment.
// Replaces sql.Null* types with pointer types for clean JSON serialization.
type NoteFragmentOutput struct {
	Bitmask     int16   `json:"bitmask"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Body        *string `json:"body,omitempty"`
	BodyIsDiff  bool    `json:"body_is_diff"`
	Tags        *string `json:"tags,omitempty"`
	IsPrivate   *bool   `json:"is_private,omitempty"`
	Categories  *string `json:"categories,omitempty"`
}

// CategoryFragmentOutput is the JSON-friendly version of CategoryFragment.
type CategoryFragmentOutput struct {
	Bitmask       int16   `json:"bitmask"`
	Name          *string `json:"name,omitempty"`
	Description   *string `json:"description,omitempty"`
	Subcategories *string `json:"subcategories,omitempty"`
}

// SyncPullResponse is the response body for GET /api/v1/sync/pull.
// HasMore indicates whether additional changes remain beyond the requested limit,
// signaling the client to issue another pull request.
type SyncPullResponse struct {
	Changes []SyncChange `json:"changes"`
	HasMore bool         `json:"has_more"`
}

// SyncPushRequest is the request body for POST /api/v1/sync/push.
type SyncPushRequest struct {
	PeerID  string       `json:"peer_id"`
	Changes []SyncChange `json:"changes"`
}

// SyncPushResponse is the response body for POST /api/v1/sync/push.
type SyncPushResponse struct {
	Accepted []string            `json:"accepted"`
	Rejected []SyncPushRejection `json:"rejected"`
}

// SyncPushRejection describes why a particular change was rejected.
type SyncPushRejection struct {
	GUID   string `json:"guid"`
	Reason string `json:"reason"`
}

// SyncStatusResponse is the response body for GET /api/v1/sync/status.
// The checksum provides a quick way for peers to detect whether their
// data sets have diverged without comparing every record.
type SyncStatusResponse struct {
	NoteCount     int    `json:"note_count"`
	CategoryCount int    `json:"category_count"`
	Checksum      string `json:"checksum"`
}

// ============================================================================
// Fragment Conversion Helpers
// ============================================================================

// noteFragmentToOutput converts a sql.Null*-based NoteFragment into the
// JSON-friendly NoteFragmentOutput for wire transmission.
func noteFragmentToOutput(f *NoteFragment) *NoteFragmentOutput {
	if f == nil {
		return nil
	}
	out := &NoteFragmentOutput{
		Bitmask:    f.Bitmask,
		BodyIsDiff: f.BodyIsDiff,
	}
	if f.Title.Valid {
		out.Title = &f.Title.String
	}
	if f.Description.Valid {
		out.Description = &f.Description.String
	}
	if f.Body.Valid {
		out.Body = &f.Body.String
	}
	if f.Tags.Valid {
		out.Tags = &f.Tags.String
	}
	if f.IsPrivate.Valid {
		out.IsPrivate = &f.IsPrivate.Bool
	}
	if f.Categories.Valid {
		out.Categories = &f.Categories.String
	}
	return out
}

// categoryFragmentToOutput converts a sql.Null*-based CategoryFragment into
// the JSON-friendly CategoryFragmentOutput.
func categoryFragmentToOutput(f *CategoryFragment) *CategoryFragmentOutput {
	if f == nil {
		return nil
	}
	out := &CategoryFragmentOutput{
		Bitmask: f.Bitmask,
	}
	if f.Name.Valid {
		out.Name = &f.Name.String
	}
	if f.Description.Valid {
		out.Description = &f.Description.String
	}
	if f.Subcategories.Valid {
		out.Subcategories = &f.Subcategories.String
	}
	return out
}

// noteFragmentFromOutput converts a NoteFragmentOutput back into the internal
// NoteFragment type for passing to ApplySync* functions.
func noteFragmentFromOutput(out *NoteFragmentOutput) NoteFragment {
	f := NoteFragment{
		Bitmask:    out.Bitmask,
		BodyIsDiff: out.BodyIsDiff,
	}
	if out.Title != nil {
		f.Title = sql.NullString{String: *out.Title, Valid: true}
	}
	if out.Description != nil {
		f.Description = sql.NullString{String: *out.Description, Valid: true}
	}
	if out.Body != nil {
		f.Body = sql.NullString{String: *out.Body, Valid: true}
	}
	if out.Tags != nil {
		f.Tags = sql.NullString{String: *out.Tags, Valid: true}
	}
	if out.IsPrivate != nil {
		f.IsPrivate = sql.NullBool{Bool: *out.IsPrivate, Valid: true}
	}
	if out.Categories != nil {
		f.Categories = sql.NullString{String: *out.Categories, Valid: true}
	}
	return f
}

// categoryFragmentFromOutput converts a CategoryFragmentOutput back into the
// internal CategoryFragment type for passing to ApplySync* functions.
func categoryFragmentFromOutput(out *CategoryFragmentOutput) CategoryFragment {
	f := CategoryFragment{
		Bitmask: out.Bitmask,
	}
	if out.Name != nil {
		f.Name = sql.NullString{String: *out.Name, Valid: true}
	}
	if out.Description != nil {
		f.Description = sql.NullString{String: *out.Description, Valid: true}
	}
	if out.Subcategories != nil {
		f.Subcategories = sql.NullString{String: *out.Subcategories, Valid: true}
	}
	return f
}

// ============================================================================
// GetUnifiedChangesForPeer
// ============================================================================

// GetUnifiedChangesForPeer merges note and category changes into a single
// chronologically ordered stream for a specific peer.
//
// The algorithm:
//  1. Fetch unsent note changes (limit+1 to detect has_more)
//  2. Fetch unsent category changes (limit+1 to detect has_more)
//  3. Convert each to SyncChange (loading fragments, authored_at)
//  4. Merge into one slice sorted by CreatedAt ASC
//  5. Categories with the same timestamp are sorted before notes so that
//     category definitions exist before note-category mappings reference them
//  6. Truncate to 'limit' and report has_more
func GetUnifiedChangesForPeer(peerID string, limit int) (*SyncPullResponse, error) {
	if limit <= 0 {
		limit = 100
	}

	// Fetch limit+1 to detect whether more changes exist beyond this batch
	fetchLimit := limit + 1

	noteChanges, err := GetUnsentChangesForPeer(peerID, fetchLimit)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get unsent note changes for peer")
	}

	categoryChanges, err := GetUnsentCategoryChangesForPeer(peerID, fetchLimit)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get unsent category changes for peer")
	}

	// Convert note changes to SyncChange envelopes
	var unified []SyncChange
	for _, nc := range noteChanges {
		sc := SyncChange{
			ID:         nc.ID,
			GUID:       nc.GUID,
			EntityType: "note",
			EntityGUID: nc.NoteGUID,
			Operation:  nc.Operation,
			CreatedAt:  nc.CreatedAt,
		}
		if nc.User.Valid {
			sc.User = nc.User.String
		}

		// Load fragment if present
		if nc.NoteFragmentID.Valid {
			fragment, err := GetNoteFragment(nc.NoteFragmentID.Int64)
			if err != nil {
				logger.LogErr(err, "failed to load note fragment for sync", "fragment_id", nc.NoteFragmentID.Int64)
			} else {
				sc.Fragment = noteFragmentToOutput(fragment)
			}
		}

		// Retrieve authored_at from disk DB (cache schema lacks it)
		authoredAt, err := getNoteAuthoredAt(nc.NoteGUID)
		if err == nil {
			sc.AuthoredAt = authoredAt
		}

		unified = append(unified, sc)
	}

	// Convert category changes to SyncChange envelopes
	for _, cc := range categoryChanges {
		sc := SyncChange{
			ID:         cc.ID,
			GUID:       cc.GUID,
			EntityType: "category",
			EntityGUID: cc.CategoryGUID,
			Operation:  cc.Operation,
			CreatedAt:  cc.CreatedAt,
		}
		if cc.User.Valid {
			sc.User = cc.User.String
		}

		// Load fragment if present
		if cc.CategoryFragmentID.Valid {
			fragment, err := GetCategoryFragment(cc.CategoryFragmentID.Int64)
			if err != nil {
				logger.LogErr(err, "failed to load category fragment for sync", "fragment_id", cc.CategoryFragmentID.Int64)
			} else {
				sc.Fragment = categoryFragmentToOutput(fragment)
			}
		}

		// Use category updated_at as authored_at (categories don't have a
		// separate authored_at column)
		catUpdatedAt, err := getCategoryUpdatedAt(cc.CategoryGUID)
		if err == nil {
			sc.AuthoredAt = catUpdatedAt
		}

		unified = append(unified, sc)
	}

	// Sort by CreatedAt ASC; categories sort before notes at the same timestamp
	// so that category definitions exist before note-category mappings reference them
	sort.SliceStable(unified, func(i, j int) bool {
		if unified[i].CreatedAt.Equal(unified[j].CreatedAt) {
			// Categories before notes at same timestamp
			if unified[i].EntityType == "category" && unified[j].EntityType == "note" {
				return true
			}
			return false
		}
		return unified[i].CreatedAt.Before(unified[j].CreatedAt)
	})

	// Determine has_more and truncate to limit
	hasMore := len(unified) > limit
	if len(unified) > limit {
		unified = unified[:limit]
	}

	// Return empty slice instead of nil for clean JSON serialization
	if unified == nil {
		unified = []SyncChange{}
	}

	return &SyncPullResponse{
		Changes: unified,
		HasMore: hasMore,
	}, nil
}

// getNoteAuthoredAt queries the disk database for a note's authored_at timestamp.
// Falls back to zero time if the note doesn't exist or the query fails.
func getNoteAuthoredAt(noteGUID string) (time.Time, error) {
	var authoredAt sql.NullTime
	err := db.QueryRow(`SELECT authored_at FROM notes WHERE guid = ?`, noteGUID).Scan(&authoredAt)
	if err != nil {
		return time.Time{}, serr.Wrap(err, "failed to get note authored_at")
	}
	if authoredAt.Valid {
		return authoredAt.Time, nil
	}
	return time.Time{}, nil
}

// getCategoryUpdatedAt queries the disk database for a category's updated_at timestamp.
func getCategoryUpdatedAt(categoryGUID string) (time.Time, error) {
	var updatedAt time.Time
	err := db.QueryRow(`SELECT updated_at FROM categories WHERE guid = ?`, categoryGUID).Scan(&updatedAt)
	if err != nil {
		return time.Time{}, serr.Wrap(err, "failed to get category updated_at")
	}
	return updatedAt, nil
}

// ============================================================================
// ApplyIncomingSyncChange
// ============================================================================

// ApplyIncomingSyncChange dispatches an incoming SyncChange to the appropriate
// ApplySync* function based on entity type and operation.
//
// Dispatch table:
//
//	entity_type=note,     op=1 (Create) -> ApplySyncNoteCreate()
//	entity_type=note,     op=2 (Update) -> ApplySyncNoteUpdate()
//	entity_type=note,     op=3 (Delete) -> ApplySyncNoteDelete()
//	entity_type=category, op=1 (Create) -> ApplySyncCategoryCreate()
//	entity_type=category, op=2 (Update) -> ApplySyncCategoryUpdate()
//	entity_type=category, op=3 (Delete) -> ApplySyncCategoryDelete()
//
// If a note fragment includes FragmentCategories (0x04), the category mappings
// are applied via ApplySyncNoteCategoryMapping after the note update.
//
// Idempotency: if the change GUID already exists in the change log, the
// operation is skipped (returns nil without error).
func ApplyIncomingSyncChange(change SyncChange) error {
	// Idempotency check — skip if this exact change GUID was already applied.
	// Check both note_changes and category_changes tables.
	if changeGUIDExists(change.GUID) {
		return nil
	}

	switch change.EntityType {
	case "note":
		return applyIncomingNoteChange(change)
	case "category":
		return applyIncomingCategoryChange(change)
	default:
		return serr.New("unknown entity type in sync change: " + change.EntityType)
	}
}

// applyIncomingNoteChange handles note-type sync changes (create/update/delete).
func applyIncomingNoteChange(change SyncChange) error {
	switch change.Operation {
	case OperationCreate:
		// Idempotency: if the note GUID already exists, skip the create.
		// This handles the case where the change was applied previously
		// but recorded under a different internal change GUID.
		existing, err := GetNoteByGUID(change.EntityGUID)
		if err != nil {
			return serr.Wrap(err, "failed to check existing note for idempotency")
		}
		if existing != nil {
			return nil // Already exists — idempotent skip
		}

		// Deserialize the fragment from the generic any field
		fragment, err := deserializeNoteFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize note fragment for create")
		}

		// Title is required — extract from fragment
		title := ""
		if fragment.Title.Valid {
			title = fragment.Title.String
		}

		_, err = ApplySyncNoteCreate(change.EntityGUID, title, fragment, change.AuthoredAt, change.User)
		if err != nil {
			return serr.Wrap(err, "failed to apply sync note create")
		}

		// If the fragment includes category mappings, apply them
		if fragment.Bitmask&FragmentCategories != 0 && fragment.Categories.Valid {
			if err := ApplySyncNoteCategoryMapping(change.EntityGUID, fragment.Categories.String); err != nil {
				logger.LogErr(err, "failed to apply category mappings during note create", "note_guid", change.EntityGUID)
			}
		}
		return nil

	case OperationUpdate:
		fragment, err := deserializeNoteFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize note fragment for update")
		}

		err = ApplySyncNoteUpdate(change.EntityGUID, fragment, change.AuthoredAt)
		if err != nil {
			return serr.Wrap(err, "failed to apply sync note update")
		}

		// If the fragment includes category mappings, apply them
		if fragment.Bitmask&FragmentCategories != 0 && fragment.Categories.Valid {
			if err := ApplySyncNoteCategoryMapping(change.EntityGUID, fragment.Categories.String); err != nil {
				logger.LogErr(err, "failed to apply category mappings during note update", "note_guid", change.EntityGUID)
			}
		}
		return nil

	case OperationDelete:
		return ApplySyncNoteDelete(change.EntityGUID)

	default:
		return serr.New(fmt.Sprintf("unknown note operation: %d", change.Operation))
	}
}

// applyIncomingCategoryChange handles category-type sync changes.
func applyIncomingCategoryChange(change SyncChange) error {
	switch change.Operation {
	case OperationCreate:
		// Idempotency: if the category GUID already exists, skip the create
		existingCat, err := GetCategoryByGUID(change.EntityGUID)
		if err != nil {
			return serr.Wrap(err, "failed to check existing category for idempotency")
		}
		if existingCat != nil {
			return nil // Already exists — idempotent skip
		}

		fragment, err := deserializeCategoryFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize category fragment for create")
		}

		name := ""
		if fragment.Name.Valid {
			name = fragment.Name.String
		}

		_, err = ApplySyncCategoryCreate(change.EntityGUID, name, fragment)
		if err != nil {
			return serr.Wrap(err, "failed to apply sync category create")
		}
		return nil

	case OperationUpdate:
		fragment, err := deserializeCategoryFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize category fragment for update")
		}

		return ApplySyncCategoryUpdate(change.EntityGUID, fragment)

	case OperationDelete:
		return ApplySyncCategoryDelete(change.EntityGUID)

	default:
		return serr.New(fmt.Sprintf("unknown category operation: %d", change.Operation))
	}
}

// deserializeNoteFragment converts the Fragment field (which could be a
// *NoteFragmentOutput, map[string]any from JSON decode, or json.RawMessage)
// into a NoteFragment for use by ApplySync* functions.
func deserializeNoteFragment(fragment any) (NoteFragment, error) {
	if fragment == nil {
		return NoteFragment{}, nil
	}

	// If already a *NoteFragmentOutput (in-process call), convert directly
	if nfo, ok := fragment.(*NoteFragmentOutput); ok {
		return noteFragmentFromOutput(nfo), nil
	}

	// Otherwise, re-marshal then unmarshal through NoteFragmentOutput.
	// This handles the case where Fragment was decoded from JSON as map[string]any.
	jsonBytes, err := json.Marshal(fragment)
	if err != nil {
		return NoteFragment{}, serr.Wrap(err, "failed to marshal note fragment for deserialization")
	}

	var out NoteFragmentOutput
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return NoteFragment{}, serr.Wrap(err, "failed to unmarshal note fragment output")
	}

	return noteFragmentFromOutput(&out), nil
}

// deserializeCategoryFragment converts the Fragment field into a CategoryFragment.
func deserializeCategoryFragment(fragment any) (CategoryFragment, error) {
	if fragment == nil {
		return CategoryFragment{}, nil
	}

	if cfo, ok := fragment.(*CategoryFragmentOutput); ok {
		return categoryFragmentFromOutput(cfo), nil
	}

	jsonBytes, err := json.Marshal(fragment)
	if err != nil {
		return CategoryFragment{}, serr.Wrap(err, "failed to marshal category fragment for deserialization")
	}

	var out CategoryFragmentOutput
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return CategoryFragment{}, serr.Wrap(err, "failed to unmarshal category fragment output")
	}

	return categoryFragmentFromOutput(&out), nil
}

// changeGUIDExists checks if a change with the given GUID already exists
// in either the note_changes or category_changes table.
func changeGUIDExists(guid string) bool {
	var count int

	// Check note_changes
	err := db.QueryRow(`SELECT COUNT(*) FROM note_changes WHERE guid = ?`, guid).Scan(&count)
	if err == nil && count > 0 {
		return true
	}

	// Check category_changes
	err = db.QueryRow(`SELECT COUNT(*) FROM category_changes WHERE guid = ?`, guid).Scan(&count)
	if err == nil && count > 0 {
		return true
	}

	return false
}

// ============================================================================
// GetEntitySnapshot
// ============================================================================

// GetEntitySnapshot returns the full current state of a note or category as a
// SyncChange with operation=Create and a full-snapshot fragment. This is used
// for initial sync or conflict resolution when a peer needs the complete entity.
func GetEntitySnapshot(entityType, entityGUID string) (*SyncChange, error) {
	switch entityType {
	case "note":
		return getNoteSnapshot(entityGUID)
	case "category":
		return getCategorySnapshot(entityGUID)
	default:
		return nil, serr.New("unknown entity type for snapshot: " + entityType)
	}
}

// getNoteSnapshot builds a full-body snapshot SyncChange for a note.
// Reads from disk to get authored_at (not present in cache schema).
func getNoteSnapshot(noteGUID string) (*SyncChange, error) {
	note, err := getNoteByGUIDFromDisk(noteGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note from disk for snapshot")
	}
	if note == nil {
		return nil, serr.New("note not found for snapshot: " + noteGUID)
	}

	// Build a full-snapshot fragment with all fields populated
	fragment := &NoteFragmentOutput{
		Bitmask: FragmentTitle | FragmentDescription | FragmentBody | FragmentTags | FragmentIsPrivate,
	}
	title := note.Title
	fragment.Title = &title
	if note.Description.Valid {
		fragment.Description = &note.Description.String
	}
	if note.Body.Valid {
		fragment.Body = &note.Body.String
	}
	if note.Tags.Valid {
		fragment.Tags = &note.Tags.String
	}
	fragment.IsPrivate = &note.IsPrivate

	// Determine authored_at
	authoredAt := time.Time{}
	if note.AuthoredAt.Valid {
		authoredAt = note.AuthoredAt.Time
	}

	// Determine user
	user := ""
	if note.CreatedBy.Valid {
		user = note.CreatedBy.String
	}

	return &SyncChange{
		GUID:       GenerateChangeGUID(),
		EntityType: "note",
		EntityGUID: noteGUID,
		Operation:  OperationCreate,
		Fragment:   fragment,
		AuthoredAt: authoredAt,
		User:       user,
		CreatedAt:  note.CreatedAt,
	}, nil
}

// getCategorySnapshot builds a full-snapshot SyncChange for a category.
func getCategorySnapshot(categoryGUID string) (*SyncChange, error) {
	cat, err := GetCategoryByGUID(categoryGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category for snapshot")
	}
	if cat == nil {
		return nil, serr.New("category not found for snapshot: " + categoryGUID)
	}

	fragment := &CategoryFragmentOutput{
		Bitmask: CatFragmentName | CatFragmentDescription | CatFragmentSubcategories,
	}
	fragment.Name = &cat.Name
	if cat.Description.Valid {
		fragment.Description = &cat.Description.String
	}
	if cat.Subcategories.Valid {
		fragment.Subcategories = &cat.Subcategories.String
	}

	return &SyncChange{
		GUID:       GenerateChangeGUID(),
		EntityType: "category",
		EntityGUID: categoryGUID,
		Operation:  OperationCreate,
		Fragment:   fragment,
		AuthoredAt: cat.UpdatedAt,
		CreatedAt:  cat.CreatedAt,
	}, nil
}

// ============================================================================
// GetSyncStatus
// ============================================================================

// GetSyncStatus returns counts and a content-based checksum of all notes and
// categories. Peers compare checksums to detect data divergence without
// transmitting every record.
//
// The checksum is SHA-256 of sorted note GUIDs concatenated with sorted
// category GUIDs, separated by a pipe character. Identical data sets on
// two machines will produce the same checksum.
func GetSyncStatus() (*SyncStatusResponse, error) {
	// Count notes (non-deleted)
	var noteCount int
	err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE deleted_at IS NULL`).Scan(&noteCount)
	if err != nil {
		return nil, serr.Wrap(err, "failed to count notes for sync status")
	}

	// Count categories
	var categoryCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&categoryCount)
	if err != nil {
		return nil, serr.Wrap(err, "failed to count categories for sync status")
	}

	// Build checksum from sorted GUIDs
	checksum, err := computeSyncChecksum()
	if err != nil {
		return nil, serr.Wrap(err, "failed to compute sync checksum")
	}

	return &SyncStatusResponse{
		NoteCount:     noteCount,
		CategoryCount: categoryCount,
		Checksum:      checksum,
	}, nil
}

// computeSyncChecksum produces a SHA-256 hash of sorted note GUIDs and sorted
// category GUIDs. The hash changes whenever an entity is added, removed, or
// has its GUID altered (which shouldn't happen, but would be caught).
func computeSyncChecksum() (string, error) {
	// Collect note GUIDs
	noteGUIDs, err := collectGUIDs(`SELECT guid FROM notes WHERE deleted_at IS NULL ORDER BY guid`)
	if err != nil {
		return "", serr.Wrap(err, "failed to collect note GUIDs for checksum")
	}

	// Collect category GUIDs
	categoryGUIDs, err := collectGUIDs(`SELECT guid FROM categories ORDER BY guid`)
	if err != nil {
		return "", serr.Wrap(err, "failed to collect category GUIDs for checksum")
	}

	// Sort both slices (queries already sort, but being defensive)
	sort.Strings(noteGUIDs)
	sort.Strings(categoryGUIDs)

	// Concatenate with delimiters for the hash input
	hashInput := ""
	for _, g := range noteGUIDs {
		hashInput += g + ","
	}
	hashInput += "|"
	for _, g := range categoryGUIDs {
		hashInput += g + ","
	}

	h := sha256.Sum256([]byte(hashInput))
	return fmt.Sprintf("%x", h), nil
}

// collectGUIDs executes a query that returns a single VARCHAR column and
// collects all rows into a string slice.
func collectGUIDs(query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var guids []string
	for rows.Next() {
		var guid string
		if err := rows.Scan(&guid); err != nil {
			return nil, err
		}
		guids = append(guids, guid)
	}
	return guids, rows.Err()
}

// ============================================================================
// MarkSyncChangesForPeer
// ============================================================================

// MarkSyncChangesForPeer marks a batch of SyncChanges as synced to a peer.
// This records that the peer has received these changes so they won't be
// returned in subsequent GetUnifiedChangesForPeer calls.
func MarkSyncChangesForPeer(changes []SyncChange, peerID string) {
	for _, ch := range changes {
		switch ch.EntityType {
		case "note":
			if err := MarkChangeSyncedToPeer(ch.ID, peerID); err != nil {
				logger.LogErr(err, "failed to mark note change as synced", "change_id", ch.ID, "peer_id", peerID)
			}
		case "category":
			if err := MarkCategoryChangeSyncedToPeer(ch.ID, peerID); err != nil {
				logger.LogErr(err, "failed to mark category change as synced", "change_id", ch.ID, "peer_id", peerID)
			}
		}
	}
}
