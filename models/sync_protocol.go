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
//
// Delivery is PullDeliveryAck when the hub has NOT marked this page delivered
// and is waiting for the next pull to acknowledge it (see sync_pull_ack.go).
// It is empty when the page was marked as it was served, which is what a
// spoke that did not ask for acknowledged delivery gets, and all a hub from
// before it could do. A spoke tells the two apart by this field alone: only a
// held page needs the closing pull that carries its acknowledgement.
type SyncPullResponse struct {
	Changes  []SyncChange `json:"changes"`
	HasMore  bool         `json:"has_more"`
	Delivery string       `json:"delivery,omitempty"`
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
//  3. Merge into one slice in the stream order (see pullStreamLess):
//     created_at ASC, categories before notes at the same timestamp so that
//     category definitions exist before note-category mappings reference
//     them, then change id
//  4. Truncate to 'limit' and report has_more
//  5. Convert each served row to SyncChange (loading fragments, authored_at)
//
// Steps 1–4 are unsentStreamForPeer, which AckPulledPage shares: an
// acknowledgement names a page by the stream it was cut from, so the page and
// the acknowledgement must be cut by the same code. Only the rows actually
// served are hydrated; the limit+1 extra from each source exist only to answer
// has_more.
func GetUnifiedChangesForPeer(peerID string, userGUID string, limit int) (*SyncPullResponse, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, hasMore, err := unsentStreamForPeer(peerID, userGUID, limit)
	if err != nil {
		return nil, err
	}

	unified := make([]SyncChange, 0, len(rows))
	for _, row := range rows {
		if row.note != nil {
			unified = append(unified, noteSyncChange(row.note))
		} else {
			unified = append(unified, categorySyncChange(row.cat))
		}
	}

	return &SyncPullResponse{
		Changes: unified,
		HasMore: hasMore,
	}, nil
}

// pullStreamRow is one change in a peer's unsent stream, before its fragment
// is loaded. Exactly one of note and cat is set.
type pullStreamRow struct {
	note *NoteChange
	cat  *CategoryChange
}

func (r pullStreamRow) guid() string {
	if r.note != nil {
		return r.note.GUID
	}
	return r.cat.GUID
}

func (r pullStreamRow) createdAt() time.Time {
	if r.note != nil {
		return r.note.CreatedAt
	}
	return r.cat.CreatedAt
}

func (r pullStreamRow) id() int64 {
	if r.note != nil {
		return r.note.ID
	}
	return r.cat.ID
}

// pullStreamLess is the stream's order, and it is total: no two rows compare
// equal. created_at alone is not enough. A bulk import or a hub compaction
// pass writes many rows inside one timestamp tick, and without the id
// tie-break their order was left to unspecified places (each database's
// ORDER BY, then which database was merged first). Serving did not care. An
// acknowledgement does: AckPulledPage re-cuts the stream to find the page a
// spoke is acknowledging, and a tie that broke differently the second time
// would make an intact page look altered.
//
// Category ids and note ids come from different sequences and may coincide,
// but the category-first rule already separates the two kinds, so the id
// comparison only ever runs between rows of one kind. Note ids are unique
// across both note databases (the private one's sequence starts past an
// offset), so it is unique there too.
func pullStreamLess(a, b pullStreamRow) bool {
	if at, bt := a.createdAt(), b.createdAt(); !at.Equal(bt) {
		return at.Before(bt)
	}
	if aCat, bCat := a.cat != nil, b.cat != nil; aCat != bCat {
		return aCat // categories before notes at the same timestamp
	}
	return a.id() < b.id()
}

// unsentStreamForPeer returns the first limit changes not yet delivered to
// peerID, in stream order, without their fragments, and whether more remain.
//
// Each source is asked for limit+1 rows in the same order (see the ORDER BY in
// GetUnsentChangesForPeer and GetUnsentCategoryChangesForPeer). Any row of the
// merged first limit is within the first limit of its own source, so the merge
// of the two truncated lists is exactly the head of the full stream.
func unsentStreamForPeer(peerID, userGUID string, limit int) ([]pullStreamRow, bool, error) {
	fetchLimit := limit + 1

	noteChanges, err := GetUnsentChangesForPeer(peerID, userGUID, fetchLimit)
	if err != nil {
		return nil, false, serr.Wrap(err, "failed to get unsent note changes for peer")
	}

	categoryChanges, err := GetUnsentCategoryChangesForPeer(peerID, userGUID, fetchLimit)
	if err != nil {
		return nil, false, serr.Wrap(err, "failed to get unsent category changes for peer")
	}

	rows := make([]pullStreamRow, 0, len(noteChanges)+len(categoryChanges))
	for i := range noteChanges {
		rows = append(rows, pullStreamRow{note: &noteChanges[i]})
	}
	for i := range categoryChanges {
		rows = append(rows, pullStreamRow{cat: &categoryChanges[i]})
	}
	sort.Slice(rows, func(i, j int) bool { return pullStreamLess(rows[i], rows[j]) })

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

// noteSyncChange builds the wire envelope for one note change row, loading
// its fragment and the note's authored_at.
func noteSyncChange(nc *NoteChange) SyncChange {
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
	return sc
}

// categorySyncChange builds the wire envelope for one category change row.
func categorySyncChange(cc *CategoryChange) SyncChange {
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
	return sc
}

// getNoteAuthoredAt returns a note's authored_at timestamp from whichever
// database holds it. Falls back to zero time if the note isn't found.
func getNoteAuthoredAt(noteGUID string) (time.Time, error) {
	for _, en := range []*dbEngine{pubDB, privDB} {
		var authoredAt sql.NullTime
		err := en.QueryRow(`SELECT authored_at FROM notes WHERE guid = ?`, noteGUID).Scan(&authoredAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return time.Time{}, serr.Wrap(err, "failed to get note authored_at")
		}
		if authoredAt.Valid {
			return authoredAt.Time, nil
		}
		return time.Time{}, nil
	}
	return time.Time{}, nil
}

// getCategoryUpdatedAt queries the public catalog for a category's
// updated_at timestamp.
func getCategoryUpdatedAt(categoryGUID string) (time.Time, error) {
	var updatedAt time.Time
	err := pubDB.QueryRow(`SELECT updated_at FROM categories WHERE guid = ?`, categoryGUID).Scan(&updatedAt)
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
// are applied inside ApplySyncNoteCreate/Update, before the relay row is
// recorded, so the relay carries the new filing (see applyIncomingFiling).
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

// applyIncomingNoteChange handles note-type sync changes.
//
// OperationSync shares the create arm, and that is the whole of hub-and-spoke
// fan-out. A hub does not re-record a spoke's push as a create — it records
// what it did, which is a sync (see ApplySyncNoteCreate). That row is the only
// account the hub has of the edit, so it is also what a SECOND spoke pulls. If
// this switch treated operation 9 as unknown, every change would reach the hub
// and stop there.
//
// Whether it lands as a create or an update is decided by what is on disk
// rather than by the operation code, which is the honest reading of a relayed
// change: "this entity now says this".
func applyIncomingNoteChange(change SyncChange) error {
	switch change.Operation {
	case OperationCreate, OperationSync:
		existing, err := GetNoteByGUID(change.EntityGUID)
		if err != nil {
			return serr.Wrap(err, "failed to check existing note for idempotency")
		}

		// Deserialize the fragment from the generic any field
		fragment, err := deserializeNoteFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize note fragment for create")
		}

		if existing != nil {
			// Idempotency: a create for a note we already have is a no-op.
			// This handles the case where the change was applied previously
			// but recorded under a different internal change GUID.
			if change.Operation == OperationCreate {
				return nil
			}
			// A relayed change for a note we already have is an edit made
			// elsewhere, so it applies as one.
			return applyIncomingNoteUpdate(change, fragment)
		}

		// Title is required — extract from fragment
		title := ""
		if fragment.Title.Valid {
			title = fragment.Title.String
		}

		// Category mappings are applied inside, ahead of the relay record.
		_, err = ApplySyncNoteCreate(change.EntityGUID, title, fragment, change.AuthoredAt, change.User, change.GUID)
		if err != nil {
			return serr.Wrap(err, "failed to apply sync note create")
		}
		return nil

	case OperationUpdate:
		fragment, err := deserializeNoteFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize note fragment for update")
		}
		return applyIncomingNoteUpdate(change, fragment)

	case OperationDelete:
		return ApplySyncNoteDelete(change.EntityGUID, change.GUID)

	default:
		return serr.New(fmt.Sprintf("unknown note operation: %d", change.Operation))
	}
}

// applyIncomingNoteUpdate lands an edit and, when the fragment carries them,
// the note's category mappings. Shared by the update arm and by a relayed
// change that turned out to be about a note this machine already has.
//
// The mappings are applied inside ApplySyncNoteUpdate rather than here after
// it returns: the relay row it records snapshots the filing, and applying
// the mappings afterwards relayed the filing as it was BEFORE this change.
func applyIncomingNoteUpdate(change SyncChange, fragment NoteFragment) error {
	if err := ApplySyncNoteUpdate(change.EntityGUID, fragment, change.AuthoredAt, change.GUID); err != nil {
		return serr.Wrap(err, "failed to apply sync note update")
	}
	return nil
}

// applyIncomingCategoryChange handles category-type sync changes. Operation 9
// shares the create arm for the same reason it does on the note side.
func applyIncomingCategoryChange(change SyncChange) error {
	switch change.Operation {
	case OperationCreate, OperationSync:
		existingCat, err := GetCategoryByGUID(change.EntityGUID)
		if err != nil {
			return serr.Wrap(err, "failed to check existing category for idempotency")
		}

		fragment, err := deserializeCategoryFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize category fragment for create")
		}

		if existingCat != nil {
			// Idempotency: a create for a category we already have is a no-op;
			// a relayed change is an edit made elsewhere.
			if change.Operation == OperationCreate {
				return nil
			}
			return ApplySyncCategoryUpdate(change.EntityGUID, fragment, change.GUID)
		}

		name := ""
		if fragment.Name.Valid {
			name = fragment.Name.String
		}

		// Pass the change author's GUID as created_by for multi-user isolation
		_, err = ApplySyncCategoryCreate(change.EntityGUID, name, fragment, change.User, change.GUID)
		if err != nil {
			return serr.Wrap(err, "failed to apply sync category create")
		}
		return nil

	case OperationUpdate:
		fragment, err := deserializeCategoryFragment(change.Fragment)
		if err != nil {
			return serr.Wrap(err, "failed to deserialize category fragment for update")
		}

		return ApplySyncCategoryUpdate(change.EntityGUID, fragment, change.GUID)

	case OperationDelete:
		return ApplySyncCategoryDelete(change.EntityGUID, change.GUID)

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
// in either the note_changes or category_changes table, or was folded away
// by hub compaction (compacted_change_guids).
func changeGUIDExists(guid string) bool {
	var count int

	// note_changes live in both databases.
	for _, en := range []*dbEngine{pubDB, privDB} {
		if err := en.QueryRow(`SELECT COUNT(*) FROM note_changes WHERE guid = ?`, guid).Scan(&count); err == nil && count > 0 {
			return true
		}
	}

	// category_changes live only in the public database.
	if err := pubDB.QueryRow(`SELECT COUNT(*) FROM category_changes WHERE guid = ?`, guid).Scan(&count); err == nil && count > 0 {
		return true
	}

	// A change the hub compactor folded into a snapshot has no row of its own
	// any more, but it was applied, and it must still read as applied.
	if err := pubDB.QueryRow(`SELECT COUNT(*) FROM compacted_change_guids WHERE guid = ?`, guid).Scan(&count); err == nil && count > 0 {
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
func GetEntitySnapshot(entityType, entityGUID, userGUID string) (*SyncChange, error) {
	switch entityType {
	case "note":
		return getNoteSnapshot(entityGUID, userGUID)
	case "category":
		return getCategorySnapshot(entityGUID, userGUID)
	default:
		return nil, serr.New("unknown entity type for snapshot: " + entityType)
	}
}

// getNoteSnapshot builds a full-body snapshot SyncChange for a note.
// Reads from disk to get authored_at (not present in cache schema).
func getNoteSnapshot(noteGUID, userGUID string) (*SyncChange, error) {
	note, err := getNoteByGUIDFromDisk(noteGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note from disk for snapshot")
	}
	if note == nil {
		return nil, serr.New("note not found for snapshot: " + noteGUID)
	}

	// Ownership verification: prevent User B from fetching User A's note snapshot
	if userGUID != "" && note.CreatedBy.Valid && note.CreatedBy.String != userGUID {
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
func getCategorySnapshot(categoryGUID, userGUID string) (*SyncChange, error) {
	cat, err := GetCategoryByGUID(categoryGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category for snapshot")
	}
	if cat == nil {
		return nil, serr.New("category not found for snapshot: " + categoryGUID)
	}

	// Ownership verification: prevent User B from fetching User A's category snapshot
	if userGUID != "" && cat.CreatedBy.Valid && cat.CreatedBy.String != userGUID {
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
func GetSyncStatus(userGUID string) (*SyncStatusResponse, error) {
	// Count notes (non-deleted) across both databases, optionally filtered
	// by user ownership.
	var noteCount int
	for _, en := range []*dbEngine{pubDB, privDB} {
		var c int
		var err error
		if userGUID != "" {
			err = en.QueryRow(`SELECT COUNT(*) FROM notes WHERE deleted_at IS NULL AND created_by = ?`, userGUID).Scan(&c)
		} else {
			err = en.QueryRow(`SELECT COUNT(*) FROM notes WHERE deleted_at IS NULL`).Scan(&c)
		}
		if err != nil {
			return nil, serr.Wrap(err, "failed to count notes for sync status")
		}
		noteCount += c
	}

	// Count categories (public catalog), optionally filtered by ownership.
	var categoryCount int
	if userGUID != "" {
		err := pubDB.QueryRow(`SELECT COUNT(*) FROM categories WHERE created_by = ?`, userGUID).Scan(&categoryCount)
		if err != nil {
			return nil, serr.Wrap(err, "failed to count categories for sync status")
		}
	} else {
		err := pubDB.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&categoryCount)
		if err != nil {
			return nil, serr.Wrap(err, "failed to count categories for sync status")
		}
	}

	// Build checksum from sorted GUIDs
	checksum, err := computeSyncChecksum(userGUID)
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
func computeSyncChecksum(userGUID string) (string, error) {
	// Collect note GUIDs from both databases, optionally filtered by owner.
	var noteGUIDs []string
	var err error
	for _, en := range []*dbEngine{pubDB, privDB} {
		var g []string
		if userGUID != "" {
			g, err = collectGUIDs(en, `SELECT guid FROM notes WHERE deleted_at IS NULL AND created_by = ? ORDER BY guid`, userGUID)
		} else {
			g, err = collectGUIDs(en, `SELECT guid FROM notes WHERE deleted_at IS NULL ORDER BY guid`)
		}
		if err != nil {
			return "", serr.Wrap(err, "failed to collect note GUIDs for checksum")
		}
		noteGUIDs = append(noteGUIDs, g...)
	}

	// Collect category GUIDs from the public catalog, optionally by owner.
	var categoryGUIDs []string
	if userGUID != "" {
		categoryGUIDs, err = collectGUIDs(pubDB, `SELECT guid FROM categories WHERE created_by = ? ORDER BY guid`, userGUID)
	} else {
		categoryGUIDs, err = collectGUIDs(pubDB, `SELECT guid FROM categories ORDER BY guid`)
	}
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

// collectGUIDs executes a (possibly parameterized) query on one engine that
// returns a single VARCHAR column and collects all rows into a slice.
func collectGUIDs(en *dbEngine, query string, args ...any) ([]string, error) {
	rows, err := en.Query(query, args...)
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

// ============================================================================
// CountUnsentChangesForPeer
// ============================================================================

// CountUnsentChangesForPeer reports how many changes are waiting to go to a
// peer, across notes (both databases) and categories.
//
// Prompt mode asks this on every status poll, which is why it counts rows
// rather than calling GetUnifiedChangesForPeer and measuring the result: that
// path loads every fragment and resolves an authored_at per change, which is
// the right cost to pay when the changes are about to be pushed and an absurd
// one to pay to render "3 changes waiting".
//
// OperationSync rows are excluded for the same reason compaction skips them —
// they record changes this machine received, not changes it owes anyone.
func CountUnsentChangesForPeer(peerID string, userGUID string) (int, error) {
	noteCounts, err := queryBothNotes(func(en *dbEngine) ([]int, error) {
		var q string
		var args []any
		if userGUID != "" {
			q = `
				SELECT COUNT(*) FROM note_changes nc
				INNER JOIN notes n ON nc.note_guid = n.guid AND n.created_by = ?
				WHERE nc.operation != ? AND nc.id NOT IN (
					SELECT note_change_id FROM note_change_sync_peers WHERE peer_id = ?
				)`
			args = []any{userGUID, OperationSync, peerID}
		} else {
			q = `
				SELECT COUNT(*) FROM note_changes nc
				WHERE nc.operation != ? AND nc.id NOT IN (
					SELECT note_change_id FROM note_change_sync_peers WHERE peer_id = ?
				)`
			args = []any{OperationSync, peerID}
		}
		var n int
		if err := en.QueryRow(q, args...).Scan(&n); err != nil {
			return nil, serr.Wrap(err, "failed to count unsent note changes")
		}
		return []int{n}, nil
	})
	if err != nil {
		return 0, err
	}

	total := 0
	for _, n := range noteCounts {
		total += n
	}

	var catQuery string
	var catArgs []any
	if userGUID != "" {
		catQuery = `
			SELECT COUNT(*) FROM category_changes cc
			INNER JOIN categories c ON cc.category_guid = c.guid AND c.created_by = ?
			WHERE cc.operation != ? AND cc.id NOT IN (
				SELECT category_change_id FROM category_change_sync_peers WHERE peer_id = ?
			)`
		catArgs = []any{userGUID, OperationSync, peerID}
	} else {
		catQuery = `
			SELECT COUNT(*) FROM category_changes cc
			WHERE cc.operation != ? AND cc.id NOT IN (
				SELECT category_change_id FROM category_change_sync_peers WHERE peer_id = ?
			)`
		catArgs = []any{OperationSync, peerID}
	}

	var catCount int
	if err := pubDB.QueryRow(catQuery, catArgs...).Scan(&catCount); err != nil {
		return 0, serr.Wrap(err, "failed to count unsent category changes")
	}

	return total + catCount, nil
}

// ============================================================================
// MarkChangeGUIDSyncedToPeer
// ============================================================================

// MarkChangeGUIDSyncedToPeer records that the peer a change came FROM already
// has it, so the row this machine wrote while applying it is never handed back.
//
// This is only possible because an applied change keeps its origin's identity
// (see relayChangeGUID): before that, the row a hub wrote for a spoke's push
// had a name only the hub knew, and there was nothing to look it up by. The
// spoke would pull its own edit back on the next cycle and discard it — right
// answer, wasted round trip.
//
// Best-effort: a change that cannot be found was applied as a no-op (an
// idempotent skip), and there is nothing to mark.
func MarkChangeGUIDSyncedToPeer(changeGUID, peerID string) {
	if changeGUID == "" || peerID == "" {
		return
	}

	// note_changes live in both databases; a change is in exactly one.
	for _, en := range []*dbEngine{pubDB, privDB} {
		var id int64
		if err := en.QueryRow(`SELECT id FROM note_changes WHERE guid = ?`, changeGUID).Scan(&id); err == nil {
			if err := MarkChangeSyncedToPeer(id, peerID); err != nil {
				logger.LogErr(err, "failed to mark applied note change as synced to its origin",
					"change_guid", changeGUID, "peer_id", peerID)
			}
			return
		}
	}

	var catID int64
	if err := pubDB.QueryRow(`SELECT id FROM category_changes WHERE guid = ?`, changeGUID).Scan(&catID); err == nil {
		if err := MarkCategoryChangeSyncedToPeer(catID, peerID); err != nil {
			logger.LogErr(err, "failed to mark applied category change as synced to its origin",
				"change_guid", changeGUID, "peer_id", peerID)
		}
	}
}
