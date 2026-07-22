package models

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// note_change.go tracks note modifications for peer-to-peer sync. Each
// change records only what changed (delta storage via NoteFragment).
//
// Two-database routing: a note's change-tracking rows live in the SAME
// database as the note (private-note history in the encrypted database).
// Since each database's id sequences are offset (see schema.go), a
// change/fragment id alone identifies its home engine — engineForID does
// the routing, and reads that span a user's whole history fan out to both
// databases and merge.

// NoteChange tracks one note modification.
type NoteChange struct {
	ID             int64          // Primary key
	GUID           string         // Unique identifier for this change
	NoteGUID       string         // GUID of the affected note
	Operation      int32          // 1: Create, 2: Update, 3: Delete, 9: Sync
	NoteFragmentID sql.NullInt64  // FK to note_fragments (null for deletes)
	User           sql.NullString // User who made the change
	CreatedAt      time.Time      // Immutable timestamp
}

// Operation constants define the type of change.
const (
	OperationCreate = 1
	OperationUpdate = 2
	OperationDelete = 3
	OperationSync   = 9 // Change received from peer
)

// NoteFragment stores delta information — only changed fields are
// populated; the bitmask indicates which. When BodyIsDiff is true, Body
// holds a unified diff patch rather than a full snapshot.
type NoteFragment struct {
	ID          int64          // Primary key
	Bitmask     int16          // Indicates which fields are active
	Title       sql.NullString // New title (if changed)
	Description sql.NullString // New description (if changed)
	Body        sql.NullString // New body (if changed), or unified diff if BodyIsDiff
	Tags        sql.NullString // New tags (if changed)
	IsPrivate   sql.NullBool   // New privacy value (if changed)
	Categories  sql.NullString // JSON array of category changes
	BodyIsDiff  bool           // True if Body contains a diff patch
}

// Bitmask constants indicate which fields are active in a NoteFragment.
const (
	FragmentTitle       = 0x80 // 128 - bit 7
	FragmentDescription = 0x40 // 64  - bit 6
	FragmentBody        = 0x20 // 32  - bit 5
	FragmentTags        = 0x10 // 16  - bit 4
	FragmentIsPrivate   = 0x08 // 8   - bit 3
	FragmentCategories  = 0x04 // 4   - bit 2
)

// NoteChangeSyncPeer tracks which peers have received each change.
type NoteChangeSyncPeer struct {
	NoteChangeID int64
	PeerID       string
	SyncedAt     time.Time
}

// GenerateChangeGUID creates a unique identifier for a note change.
func GenerateChangeGUID() string {
	return uuid.New().String()
}

// computeChangeBitmask determines which fields changed between the
// existing note and the input. Returns 0 if nothing changed.
func computeChangeBitmask(existing *Note, input NoteInput) int16 {
	var bitmask int16 = 0

	if existing.Title != input.Title {
		bitmask |= FragmentTitle
	}
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

	// Category changes are tracked separately via the note_categories table.
	return bitmask
}

// sqlNullStringEqualsPointer compares a sql.NullString with a *string.
func sqlNullStringEqualsPointer(ns sql.NullString, sp *string) bool {
	if !ns.Valid && sp == nil {
		return true
	}
	if !ns.Valid || sp == nil {
		return false
	}
	return ns.String == *sp
}

// computeBodyDiff generates a unified diff patch from oldBody to newBody.
// Returns the patch text and whether it is smaller than the full new body.
func computeBodyDiff(oldBody, newBody string) (diffText string, isDiffSmaller bool) {
	dmp := diffmatchpatch.New()
	charsA, charsB, lineArray := dmp.DiffLinesToChars(oldBody, newBody)
	diffs := dmp.DiffMain(charsA, charsB, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	patches := dmp.PatchMake(oldBody, diffs)
	patchText := dmp.PatchToText(patches)
	isDiffSmaller = len(patchText) < len(newBody)
	return patchText, isDiffSmaller
}

// applyBodyDiff applies a unified diff patch to a base body text.
func applyBodyDiff(currentBody, patchText string) (string, error) {
	dmp := diffmatchpatch.New()
	patches, err := dmp.PatchFromText(patchText)
	if err != nil {
		return "", serr.Wrap(err, "failed to parse body diff patch")
	}
	result, applied := dmp.PatchApply(patches, currentBody)
	for i, ok := range applied {
		if !ok {
			return "", serr.New(fmt.Sprintf("body diff patch %d failed to apply", i))
		}
	}
	return result, nil
}

// createFragmentFromInput creates a NoteFragment with all fields from
// input (used for creates, where everything is "changed").
func createFragmentFromInput(input NoteInput, bitmask int16) NoteFragment {
	fragment := NoteFragment{Bitmask: bitmask}
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
	return fragment
}

// createDeltaFragment creates a NoteFragment with only the changed fields;
// a changed body is stored as a diff when that is smaller than a snapshot.
func createDeltaFragment(existing *Note, input NoteInput, bitmask int16) NoteFragment {
	fragment := createFragmentFromInput(input, bitmask)
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
	}
	return fragment
}

// insertNoteFragment saves a fragment to the given engine and returns its
// id. The id is drawn from that engine's offset sequence, so it encodes
// which database the fragment lives in.
func insertNoteFragment(en *dbEngine, fragment NoteFragment) (int64, error) {
	query := `
		INSERT INTO note_fragments (id, bitmask, title, description, body, tags, is_private, categories, body_is_diff)
		VALUES (nextval('note_fragments_id_seq'), ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id
	`
	var fragmentID int64
	err := en.QueryRow(
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

// insertNoteChange records a note change to the given engine (the note's
// database).
func insertNoteChange(en *dbEngine, changeGUID, noteGUID string, operation int32, fragmentID sql.NullInt64, user string) error {
	query := `
		INSERT INTO note_changes (id, guid, note_guid, operation, note_fragment_id, change_user)
		VALUES (nextval('note_changes_id_seq'), ?, ?, ?, ?, ?)
	`
	userVal := sql.NullString{}
	if user != "" {
		userVal = sql.NullString{String: user, Valid: true}
	}
	_, err := en.Exec(query, changeGUID, noteGUID, operation, fragmentID, userVal)
	if err != nil {
		return serr.Wrap(err, "failed to insert note change")
	}
	return nil
}

// GetNoteFragment retrieves a fragment by id from its home database
// (routed by the id range). Returns nil if not found.
func GetNoteFragment(id int64) (*NoteFragment, error) {
	query := `
		SELECT id, bitmask, title, description, body, tags, is_private, categories, body_is_diff
		FROM note_fragments
		WHERE id = ?
	`
	fragment := &NoteFragment{}
	err := engineForID(id).QueryRow(query, id).Scan(
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

// MarkChangeSyncedToPeer records that a change has been synced to a peer,
// writing into the change's home database (routed by id range).
func MarkChangeSyncedToPeer(noteChangeID int64, peerID string) error {
	query := `
		INSERT INTO note_change_sync_peers (note_change_id, peer_id)
		VALUES (?, ?)
	`
	_, err := engineForID(noteChangeID).Exec(query, noteChangeID, peerID)
	if err != nil {
		return serr.Wrap(err, "failed to mark change as synced to peer")
	}
	return nil
}

// scanNoteChange reads one note_changes row into c.
func scanNoteChange(s scanner, c *NoteChange) error {
	return s.Scan(
		&c.ID, &c.GUID, &c.NoteGUID, &c.Operation, &c.NoteFragmentID, &c.User, &c.CreatedAt,
	)
}

// GetUnsentChangesForPeer retrieves note changes not yet sent to a peer,
// across BOTH databases, oldest first, capped at limit. When userGUID is
// non-empty the hub filters to that user's notes (multi-user isolation);
// the join stays within each database.
func GetUnsentChangesForPeer(peerID string, userGUID string, limit int) ([]NoteChange, error) {
	build := func() (string, []any) {
		if userGUID != "" {
			q := `
				SELECT nc.id, nc.guid, nc.note_guid, nc.operation, nc.note_fragment_id, nc.change_user, nc.created_at
				FROM note_changes nc
				INNER JOIN notes n ON nc.note_guid = n.guid AND n.created_by = ?
				WHERE nc.id NOT IN (
					SELECT note_change_id FROM note_change_sync_peers WHERE peer_id = ?
				)
				ORDER BY nc.created_at ASC`
			args := []any{userGUID, peerID}
			if limit > 0 {
				q += " LIMIT ?"
				args = append(args, limit)
			}
			return q, args
		}
		q := `
			SELECT nc.id, nc.guid, nc.note_guid, nc.operation, nc.note_fragment_id, nc.change_user, nc.created_at
			FROM note_changes nc
			WHERE nc.id NOT IN (
				SELECT note_change_id FROM note_change_sync_peers WHERE peer_id = ?
			)
			ORDER BY nc.created_at ASC`
		args := []any{peerID}
		if limit > 0 {
			q += " LIMIT ?"
			args = append(args, limit)
		}
		return q, args
	}

	changes, err := queryBothNotes(func(en *dbEngine) ([]NoteChange, error) {
		q, args := build()
		rows, err := en.Query(q, args...)
		if err != nil {
			return nil, serr.Wrap(err, "failed to query unsent changes for peer")
		}
		defer rows.Close()
		var out []NoteChange
		for rows.Next() {
			var c NoteChange
			if err := scanNoteChange(rows, &c); err != nil {
				logger.LogErr(err, "failed to scan note change row")
				continue
			}
			out = append(out, c)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, err
	}

	// Merge the two databases' oldest-unsent slices into one global
	// oldest-first order, then re-apply the limit.
	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].CreatedAt.Before(changes[j].CreatedAt)
	})
	if limit > 0 && len(changes) > limit {
		changes = changes[:limit]
	}
	return changes, nil
}

// NoteChangeOutput provides a complete view of a change for API responses.
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

// scanNoteChangeOutput reads one note_changes row into c.
func scanNoteChangeOutput(s scanner, c *NoteChangeOutput) error {
	return s.Scan(
		&c.ID, &c.GUID, &c.NoteGUID, &c.Operation, &c.NoteFragmentID, &c.User, &c.CreatedAt,
	)
}

// GetNoteChangeWithFragment retrieves a complete change with its fragment,
// routed to the change's home database by id range.
func GetNoteChangeWithFragment(changeID int64) (*NoteChangeOutput, error) {
	query := `
		SELECT id, guid, note_guid, operation, note_fragment_id, change_user, created_at
		FROM note_changes
		WHERE id = ?
	`
	change := &NoteChangeOutput{}
	err := scanNoteChangeOutput(engineForID(changeID).QueryRow(query, changeID), change)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note change")
	}

	if change.NoteFragmentID.Valid {
		fragment, err := GetNoteFragment(change.NoteFragmentID.Int64)
		if err != nil {
			return nil, serr.Wrap(err, "failed to get associated fragment")
		}
		change.Fragment = fragment
	}
	return change, nil
}

// GetUserChangesSince retrieves all of a user's changes made after `since`,
// across both databases, oldest first (for replay), capped at limit.
func GetUserChangesSince(userGUID string, since time.Time, limit int) ([]NoteChangeOutput, error) {
	build := func() (string, []any) {
		q := `
			SELECT id, guid, note_guid, operation, note_fragment_id, change_user, created_at
			FROM note_changes
			WHERE change_user = ? AND created_at > ?
			ORDER BY created_at ASC`
		args := []any{userGUID, since}
		if limit > 0 {
			q += " LIMIT ?"
			args = append(args, limit)
		}
		return q, args
	}

	changes, err := queryBothNotes(func(en *dbEngine) ([]NoteChangeOutput, error) {
		q, args := build()
		rows, err := en.Query(q, args...)
		if err != nil {
			return nil, serr.Wrap(err, "failed to query user changes")
		}
		defer rows.Close()
		var out []NoteChangeOutput
		for rows.Next() {
			var c NoteChangeOutput
			if err := scanNoteChangeOutput(rows, &c); err != nil {
				return nil, serr.Wrap(err, "failed to scan change row")
			}
			if c.NoteFragmentID.Valid {
				fragment, err := GetNoteFragment(c.NoteFragmentID.Int64)
				if err != nil {
					return nil, serr.Wrap(err, "failed to get associated fragment")
				}
				c.Fragment = fragment
			}
			out = append(out, c)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].CreatedAt.Before(changes[j].CreatedAt)
	})
	if limit > 0 && len(changes) > limit {
		changes = changes[:limit]
	}
	return changes, nil
}
