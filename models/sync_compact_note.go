package models

import (
	"database/sql"
	"errors"

	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Compacting one note
//
// CompactPendingChanges rewrites every entity's pending tail at once. This is
// the same rewrite scoped to a single note: the note's unsent changes to one
// peer collapse into one, and every other note and every category is left
// exactly as it was.
//
//	pending log                         CompactNotePendingChanges(peer, _, A)
//	  A: create ─ update ─ update   ──►   A: create (snapshot of A now)
//	  B: update ─ update            ──►   B: update ─ update   (untouched)
//	  cat X: update ─ update        ──►   cat X: update ─ update (untouched)
//
// It reuses the whole-log machinery unchanged (groupNoteChanges for the
// grouping and OperationSync skip, compactNoteGroup for the rewrite), so a
// note compacted alone and the same note compacted in a full pass end up as
// the same change.
//
// Leaving categories alone is safe for ordering. The replacement takes the
// created_at of the last change it swallows (see compactNoteGroup), so it
// sits at or after every position the note's changes held. Any category
// definition that preceded one of them still precedes it.
// ============================================================================

// ErrCompactNoteNotFound reports that the note named for a one-note
// compaction does not exist, or is not the caller's. The two are not told
// apart, so the answer cannot be used to probe for other users' note GUIDs.
var ErrCompactNoteNotFound = errors.New("note not found")

// CompactNotePendingChanges collapses one note's unsent changes to peerID into
// a single change.
//
// The result's counts are of THIS note's pending changes: ChangesBefore is
// how many it had, ChangesAfter how many it has now (1 when compacted), and
// NotesCompacted is 0 or 1. CategoriesCompacted is always 0.
//
// When userGUID is non-empty the note must belong to that user, or the call
// returns ErrCompactNoteNotFound and writes nothing.
func CompactNotePendingChanges(peerID, userGUID, noteGUID string) (*CompactionResult, error) {
	grp, err := pendingNoteGroup(peerID, userGUID, noteGUID)
	if err != nil {
		return nil, err
	}
	res := &CompactionResult{ChangesBefore: len(grp), ChangesAfter: len(grp)}
	if len(grp) < 2 {
		return res, nil // zero or one change is already as compact as it gets
	}

	compacted, err := compactNoteGroup(noteGUID, grp)
	if err != nil {
		return nil, serr.Wrap(err, "failed to compact note", "note_guid", noteGUID)
	}
	if !compacted {
		return res, nil
	}
	res.NotesCompacted = 1

	// Re-read rather than assume 1. compactNoteGroup logs and carries on when
	// deleting a superseded row fails, and the count should report what is
	// actually pending, as the whole-log pass does.
	after, err := pendingNoteGroup(peerID, "", noteGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to count the note's pending changes after compaction")
	}
	res.ChangesAfter = len(after)
	return res, nil
}

// PreviewNoteCompaction is CompactNotePendingChanges without the writes, for
// a dry run: what compacting this one note would do right now. It answers
// with the same counts and the same ErrCompactNoteNotFound.
func PreviewNoteCompaction(peerID, userGUID, noteGUID string) (*CompactionResult, error) {
	grp, err := pendingNoteGroup(peerID, userGUID, noteGUID)
	if err != nil {
		return nil, err
	}
	res := &CompactionResult{ChangesBefore: len(grp), ChangesAfter: len(grp)}
	if noteGroupCompactable(noteGUID, grp) {
		res.NotesCompacted = 1
		res.ChangesAfter = 1
	}
	return res, nil
}

// noteGroupCompactable predicts whether compactNoteGroup would rewrite grp.
// It is the one decline rule the rewrite has, shared by both previews
// (PreviewCompaction and PreviewNoteCompaction) so neither can drift from it:
// a group of fewer than two changes has nothing to gain, and a group whose
// net result is not a delete, for a note that no longer exists, is a log that
// disagrees with the data, which the rewrite leaves alone.
func noteGroupCompactable(noteGUID string, grp []NoteChange) bool {
	if len(grp) < 2 {
		return false
	}
	if netNoteOperation(grp) != OperationDelete {
		if note, err := GetNoteByGUID(noteGUID); err != nil || note == nil {
			return false
		}
	}
	return true
}

// pendingNoteGroup returns one note's unsent changes to peerID, oldest first,
// without OperationSync rows: the group the whole-log pass would build for
// that note.
//
// It loads the peer's whole pending log and keeps one note's rows, rather
// than querying by note_guid. That keeps exactly the selection and ordering
// rules of GetUnsentChangesForPeer (both databases, merged by created_at),
// and a pending log is the unsent tail, not the full history, so it is
// small. The load passes no userGUID on purpose: that filter joins each
// database to its own notes table, so after a privacy flip it would drop the
// changes left behind in the note's former database. Ownership is checked
// separately, against the note row itself.
func pendingNoteGroup(peerID, userGUID, noteGUID string) ([]NoteChange, error) {
	if peerID == "" {
		return nil, serr.New("peer ID is required to compact a note's pending changes")
	}
	if noteGUID == "" {
		return nil, serr.New("note GUID is required to compact a note's pending changes")
	}
	if err := checkNoteOwner(noteGUID, userGUID); err != nil {
		return nil, err
	}

	changes, err := GetUnsentChangesForPeer(peerID, "", 0)
	if err != nil {
		return nil, serr.Wrap(err, "failed to load pending note changes")
	}
	mine := make([]NoteChange, 0, 4)
	for _, c := range changes {
		if c.NoteGUID == noteGUID {
			mine = append(mine, c)
		}
	}
	_, groups := groupNoteChanges(mine)
	return groups[noteGUID], nil
}

// checkNoteOwner returns ErrCompactNoteNotFound unless a note row with this
// GUID exists and, when userGUID is non-empty, belongs to that user.
//
// Soft-deleted rows count. A note whose pending changes end in a delete is
// exactly the kind worth compacting (the whole tail becomes one delete), and
// GetNoteByGUID, which hides deleted notes, would wrongly report it missing.
func checkNoteOwner(noteGUID, userGUID string) error {
	owner, err := firstFromBothNotes(func(en *dbEngine) (*sql.NullString, error) {
		var createdBy sql.NullString
		if e := en.QueryRow(`SELECT created_by FROM notes WHERE guid = ?`, noteGUID).Scan(&createdBy); e != nil {
			return nil, e
		}
		return &createdBy, nil
	})
	if err == sql.ErrNoRows || (err == nil && owner == nil) {
		return ErrCompactNoteNotFound
	}
	if err != nil {
		return serr.Wrap(err, "failed to look up the note to compact", "note_guid", noteGUID)
	}
	if userGUID != "" && owner.String != userGUID {
		return ErrCompactNoteNotFound
	}
	return nil
}
