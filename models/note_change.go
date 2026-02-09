package models

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// NoteChange tracks note modifications for peer-to-peer sync
// Each change records only what changed (using delta storage via NoteFragment)
type NoteChange struct {
	ID             int64          // Primary key
	GUID           string         // Unique identifier for this change
	NoteGUID       string         // GUID of the affected note
	Operation      int32          // 1: Create, 2: Update, 3: Delete, 9: Sync
	NoteFragmentID sql.NullInt64  // FK to note_fragments (null for deletes)
	User           sql.NullString // User who made the change
	CreatedAt      time.Time      // Immutable timestamp
}

// Operation constants define the type of change
const (
	OperationCreate = 1
	OperationUpdate = 2
	OperationDelete = 3
	OperationSync   = 9 // Change received from peer
)

// NoteFragment stores delta information - only changed fields are populated.
// The bitmask indicates which fields are active/changed.
// When BodyIsDiff is true, the Body field contains a unified diff patch
// rather than a full snapshot, enabling efficient storage for large notes
// with small edits.
type NoteFragment struct {
	ID          int64          // Primary key
	Bitmask     int16          // Indicates which fields are active
	Title       sql.NullString // New title (if changed)
	Description sql.NullString // New description (if changed)
	Body        sql.NullString // New body (if changed), or unified diff if BodyIsDiff is true
	Tags        sql.NullString // New tags (if changed)
	IsPrivate   sql.NullBool   // New privacy value (if changed)
	Categories  sql.NullString // JSON array of category changes
	BodyIsDiff  bool           // True if Body contains a diff patch rather than full snapshot
}

// Bitmask constants indicate which fields are active in a NoteFragment
// Using high-to-low bit ordering for clarity
const (
	FragmentTitle       = 0x80 // 128 - bit 7
	FragmentDescription = 0x40 // 64  - bit 6
	FragmentBody        = 0x20 // 32  - bit 5
	FragmentTags        = 0x10 // 16  - bit 4
	FragmentIsPrivate   = 0x08 // 8   - bit 3
	FragmentCategories  = 0x04 // 4   - bit 2
)

// NoteChangeSyncPeer tracks which peers have received each change
// This allows efficient querying of unsent changes per peer
type NoteChangeSyncPeer struct {
	NoteChangeID int64     // FK to note_changes
	PeerID       string    // Unique peer identifier
	SyncedAt     time.Time // When synced to peer
}

// SQL DDL constants for table creation

const DDLCreateNoteFragmentsSequence = `
CREATE SEQUENCE IF NOT EXISTS note_fragments_id_seq START 1;
`

const DDLCreateNoteFragmentsTable = `
CREATE TABLE IF NOT EXISTS note_fragments (
    id          BIGINT PRIMARY KEY DEFAULT nextval('note_fragments_id_seq'),
    bitmask     SMALLINT NOT NULL,
    title       VARCHAR,
    description VARCHAR,
    body        VARCHAR,
    tags        VARCHAR,
    is_private  BOOLEAN,
    categories  VARCHAR,
    body_is_diff BOOLEAN DEFAULT false
);
`

const DDLCreateNoteChangesSequence = `
CREATE SEQUENCE IF NOT EXISTS note_changes_id_seq START 1;
`

const DDLCreateNoteChangesTable = `
CREATE TABLE IF NOT EXISTS note_changes (
    id               BIGINT PRIMARY KEY DEFAULT nextval('note_changes_id_seq'),
    guid             VARCHAR NOT NULL UNIQUE,
    note_guid        VARCHAR NOT NULL,
    operation        INTEGER NOT NULL,
    note_fragment_id BIGINT,
    user             VARCHAR,
    created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (note_fragment_id) REFERENCES note_fragments(id)
);
`

const DDLCreateNoteChangesIndexNoteGUID = `
CREATE INDEX IF NOT EXISTS idx_note_changes_note_guid ON note_changes(note_guid);
`

const DDLCreateNoteChangesIndexCreatedAt = `
CREATE INDEX IF NOT EXISTS idx_note_changes_created_at ON note_changes(created_at);
`

const DDLCreateNoteChangeSyncPeersTable = `
CREATE TABLE IF NOT EXISTS note_change_sync_peers (
    note_change_id BIGINT NOT NULL,
    peer_id        VARCHAR NOT NULL,
    synced_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (note_change_id, peer_id),
    FOREIGN KEY (note_change_id) REFERENCES note_changes(id)
);
`

const DDLCreateNoteChangeSyncPeersIndexPeerID = `
CREATE INDEX IF NOT EXISTS idx_note_change_sync_peers_peer_id ON note_change_sync_peers(peer_id);
`

// Helper Functions

// GenerateChangeGUID creates a unique identifier for a note change
func GenerateChangeGUID() string {
	return uuid.New().String()
}

// computeChangeBitmask determines which fields changed between existing note and input
// Returns a bitmask indicating changed fields, or 0 if nothing changed
func computeChangeBitmask(existing *Note, input NoteInput) int16 {
	var bitmask int16 = 0

	// Compare each field and set corresponding bit if changed
	if existing.Title != input.Title {
		bitmask |= FragmentTitle
	}

	// Compare nullable string fields (sql.NullString vs *string)
	if !sqlNullStringEqualsPointer(existing.Description, input.Description) {
		bitmask |= FragmentDescription
	}
	if !sqlNullStringEqualsPointer(existing.Body, input.Body) {
		bitmask |= FragmentBody
	}
	if !sqlNullStringEqualsPointer(existing.Tags, input.Tags) {
		bitmask |= FragmentTags
	}

	if existing.IsPrivate != input.IsPrivate {
		bitmask |= FragmentIsPrivate
	}

	// Note: Category changes are tracked separately via the note_categories table
	// and are not included in this bitmask computation

	return bitmask
}

// sqlNullStringEqualsPointer compares a sql.NullString with a *string pointer
// Returns true if they represent the same value (both null/nil or same string)
func sqlNullStringEqualsPointer(ns sql.NullString, sp *string) bool {
	// Both are null/nil
	if !ns.Valid && sp == nil {
		return true
	}
	// One is null, the other isn't
	if !ns.Valid || sp == nil {
		return false
	}
	// Both have values, compare them
	return ns.String == *sp
}

// computeBodyDiff generates a unified diff patch from oldBody to newBody.
// Uses diff-match-patch for efficient text diffing. Returns the patch text
// and a boolean indicating whether the diff is smaller than the full new body.
// If the diff is larger, the caller should fall back to a full snapshot to
// avoid bloating the change log for complete rewrites.
func computeBodyDiff(oldBody, newBody string) (diffText string, isDiffSmaller bool) {
	dmp := diffmatchpatch.New()
	// Compute line-level diff for better readability and efficiency
	charsA, charsB, lineArray := dmp.DiffLinesToChars(oldBody, newBody)
	diffs := dmp.DiffMain(charsA, charsB, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	patches := dmp.PatchMake(oldBody, diffs)
	patchText := dmp.PatchToText(patches)

	// Size comparison: only use diff if it's actually smaller than the full body
	isDiffSmaller = len(patchText) < len(newBody)
	return patchText, isDiffSmaller
}

// applyBodyDiff applies a unified diff patch to a base body text.
// The patch is in diff-match-patch format. Returns the resulting text
// or an error if the patch could not be applied cleanly.
func applyBodyDiff(currentBody, patchText string) (string, error) {
	dmp := diffmatchpatch.New()
	patches, err := dmp.PatchFromText(patchText)
	if err != nil {
		return "", serr.Wrap(err, "failed to parse body diff patch")
	}

	result, applied := dmp.PatchApply(patches, currentBody)

	// Verify all patches applied successfully — partial application would
	// leave the body in an inconsistent state
	for i, ok := range applied {
		if !ok {
			return "", serr.New(fmt.Sprintf("body diff patch %d failed to apply", i))
		}
	}

	return result, nil
}

// createFragmentFromInput creates a NoteFragment with all fields from input.
// Used for create operations where everything is "changed".
// Body is always stored as full snapshot for creates (no diff against nothing).
func createFragmentFromInput(input NoteInput, bitmask int16) NoteFragment {
	fragment := NoteFragment{
		Bitmask: bitmask,
	}

	// Set all fields as specified by bitmask
	if bitmask&FragmentTitle != 0 {
		fragment.Title = sql.NullString{String: input.Title, Valid: true}
	}
	if bitmask&FragmentDescription != 0 && input.Description != nil {
		fragment.Description = sql.NullString{String: *input.Description, Valid: true}
	}
	if bitmask&FragmentBody != 0 && input.Body != nil {
		fragment.Body = sql.NullString{String: *input.Body, Valid: true}
	}
	if bitmask&FragmentTags != 0 && input.Tags != nil {
		fragment.Tags = sql.NullString{String: *input.Tags, Valid: true}
	}
	if bitmask&FragmentIsPrivate != 0 {
		fragment.IsPrivate = sql.NullBool{Bool: input.IsPrivate, Valid: true}
	}

	// Note: Categories are tracked separately via note_categories table

	return fragment
}

// createDeltaFragment creates a NoteFragment with only changed fields from input.
// Used for update operations where only modified fields are stored.
// When the body changed, it computes a diff against the existing body and stores
// the diff if it's smaller than the full new body (otherwise falls back to snapshot).
func createDeltaFragment(existing *Note, input NoteInput, bitmask int16) NoteFragment {
	fragment := createFragmentFromInput(input, bitmask)

	// For body changes, attempt to store as diff rather than full snapshot
	if bitmask&FragmentBody != 0 && input.Body != nil && existing != nil {
		existingBody := ""
		if existing.Body.Valid {
			existingBody = existing.Body.String
		}

		diffText, isDiffSmaller := computeBodyDiff(existingBody, *input.Body)
		if isDiffSmaller {
			fragment.Body = sql.NullString{String: diffText, Valid: true}
			fragment.BodyIsDiff = true
		}
		// else: keep full snapshot (default from createFragmentFromInput)
	}

	return fragment
}

// insertNoteFragment saves a fragment to the database.
// Returns the fragment ID or an error.
// The body_is_diff flag indicates whether the body column contains a diff patch
// (true) or a full body snapshot (false).
func insertNoteFragment(fragment NoteFragment) (int64, error) {
	query := `
		INSERT INTO note_fragments (bitmask, title, description, body, tags, is_private, categories, body_is_diff)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id
	`

	var fragmentID int64
	err := db.QueryRow(
		query,
		fragment.Bitmask,
		fragment.Title,
		fragment.Description,
		fragment.Body,
		fragment.Tags,
		fragment.IsPrivate,
		fragment.Categories,
		fragment.BodyIsDiff,
	).Scan(&fragmentID)

	if err != nil {
		return 0, serr.Wrap(err, "failed to insert note fragment")
	}

	return fragmentID, nil
}

// insertNoteChange records a note change to the database
// This is the core tracking function called by CRUD operations
func insertNoteChange(changeGUID, noteGUID string, operation int32, fragmentID sql.NullInt64, user string) error {
	query := `
		INSERT INTO note_changes (guid, note_guid, operation, note_fragment_id, user)
		VALUES (?, ?, ?, ?, ?)
	`

	userVal := sql.NullString{}
	if user != "" {
		userVal = sql.NullString{String: user, Valid: true}
	}

	_, err := db.Exec(query, changeGUID, noteGUID, operation, fragmentID, userVal)
	if err != nil {
		return serr.Wrap(err, "failed to insert note change")
	}

	return nil
}

// GetNoteFragment retrieves a fragment by ID.
// Returns nil if not found. Includes the body_is_diff flag to indicate
// whether the body is a diff patch or full snapshot.
func GetNoteFragment(id int64) (*NoteFragment, error) {
	query := `
		SELECT id, bitmask, title, description, body, tags, is_private, categories, body_is_diff
		FROM note_fragments
		WHERE id = ?
	`

	fragment := &NoteFragment{}
	err := db.QueryRow(query, id).Scan(
		&fragment.ID,
		&fragment.Bitmask,
		&fragment.Title,
		&fragment.Description,
		&fragment.Body,
		&fragment.Tags,
		&fragment.IsPrivate,
		&fragment.Categories,
		&fragment.BodyIsDiff,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note fragment")
	}

	return fragment, nil
}

// Sync Functions for Peer-to-Peer

// MarkChangeSyncedToPeer records that a change has been synced to a specific peer
// This prevents the change from being sent to that peer again
func MarkChangeSyncedToPeer(noteChangeID int64, peerID string) error {
	query := `
		INSERT INTO note_change_sync_peers (note_change_id, peer_id)
		VALUES (?, ?)
	`

	_, err := db.Exec(query, noteChangeID, peerID)
	if err != nil {
		return serr.Wrap(err, "failed to mark change as synced to peer")
	}

	return nil
}

// GetUnsentChangesForPeer retrieves changes that haven't been sent to a specific peer
// Returns up to 'limit' changes, ordered by creation time (oldest first)
// This is the key function for identifying what needs to be synced to a peer
func GetUnsentChangesForPeer(peerID string, limit int) ([]NoteChange, error) {
	query := `
		SELECT nc.id, nc.guid, nc.note_guid, nc.operation, nc.note_fragment_id, nc.user, nc.created_at
		FROM note_changes nc
		WHERE nc.id NOT IN (
			SELECT note_change_id
			FROM note_change_sync_peers
			WHERE peer_id = ?
		)
		ORDER BY nc.created_at ASC
		LIMIT ?
	`

	rows, err := db.Query(query, peerID, limit)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query unsent changes for peer")
	}
	defer rows.Close()

	var changes []NoteChange
	for rows.Next() {
		var change NoteChange
		err := rows.Scan(
			&change.ID,
			&change.GUID,
			&change.NoteGUID,
			&change.Operation,
			&change.NoteFragmentID,
			&change.User,
			&change.CreatedAt,
		)
		if err != nil {
			logger.LogErr(err, "failed to scan note change row")
			continue
		}
		changes = append(changes, change)
	}

	if err = rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating note changes")
	}

	return changes, nil
}

// NoteChangeOutput provides a complete view of a change for API responses
// Includes the change metadata plus the fragment details if present
type NoteChangeOutput struct {
	ID             int64          `json:"id"`
	GUID           string         `json:"guid"`
	NoteGUID       string         `json:"note_guid"`
	Operation      int32          `json:"operation"`
	NoteFragmentID sql.NullInt64  `json:"note_fragment_id,omitempty"`
	Fragment       *NoteFragment  `json:"fragment,omitempty"`
	User           sql.NullString `json:"user,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// GetNoteChangeWithFragment retrieves a complete change with its fragment
// Used for API responses when full change details are needed
func GetNoteChangeWithFragment(changeID int64) (*NoteChangeOutput, error) {
	query := `
		SELECT id, guid, note_guid, operation, note_fragment_id, user, created_at
		FROM note_changes
		WHERE id = ?
	`

	change := &NoteChangeOutput{}
	err := db.QueryRow(query, changeID).Scan(
		&change.ID,
		&change.GUID,
		&change.NoteGUID,
		&change.Operation,
		&change.NoteFragmentID,
		&change.User,
		&change.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note change")
	}

	// Load fragment if present
	if change.NoteFragmentID.Valid {
		fragment, err := GetNoteFragment(change.NoteFragmentID.Int64)
		if err != nil {
			return nil, serr.Wrap(err, "failed to get associated fragment")
		}
		change.Fragment = fragment
	}

	return change, nil
}

// GetUserChangesSince retrieves all changes for a user since the specified timestamp.
// Used for sync operations where a client needs to fetch changes made after their last sync.
// The userGUID parameter filters to changes made by that user.
// Returns changes ordered by created_at ascending (oldest first) for proper replay order.
func GetUserChangesSince(userGUID string, since time.Time, limit int) ([]NoteChangeOutput, error) {
	query := `
		SELECT id, guid, note_guid, operation, note_fragment_id, user, created_at
		FROM note_changes
		WHERE user = ? AND created_at > ?
		ORDER BY created_at ASC
	`

	// Add limit if specified
	if limit > 0 {
		query += " LIMIT ?"
	}

	var rows *sql.Rows
	var err error

	if limit > 0 {
		rows, err = db.Query(query, userGUID, since, limit)
	} else {
		rows, err = db.Query(query, userGUID, since)
	}

	if err != nil {
		return nil, serr.Wrap(err, "failed to query user changes")
	}
	defer rows.Close()

	var changes []NoteChangeOutput
	for rows.Next() {
		var change NoteChangeOutput
		err := rows.Scan(
			&change.ID,
			&change.GUID,
			&change.NoteGUID,
			&change.Operation,
			&change.NoteFragmentID,
			&change.User,
			&change.CreatedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan change row")
		}

		// Load fragment if present
		if change.NoteFragmentID.Valid {
			fragment, err := GetNoteFragment(change.NoteFragmentID.Int64)
			if err != nil {
				return nil, serr.Wrap(err, "failed to get associated fragment")
			}
			change.Fragment = fragment
		}

		changes = append(changes, change)
	}

	if err = rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating user changes")
	}

	return changes, nil
}
