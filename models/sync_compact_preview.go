package models

import (
	"github.com/rohanthewiz/serr"
)

// PreviewCompaction reports what CompactPendingChanges would do for this peer
// right now, without writing anything: how many notes and categories would
// collapse, and the pending count before and after.
//
// Compaction deletes local history, so "how much would this actually save?"
// deserves an answer before the button is pressed. A log of thirty changes to
// thirty different notes compacts to the same thirty; one of thirty edits to
// one note compacts to one.
//
// It walks the same groups as the real pass (groupNoteChanges /
// groupCategoryChanges) and applies the one rule by which a real pass declines
// a group: a group whose net result is not a delete, for an entity that no
// longer exists (the log and the data disagree, so the pass leaves it alone).
// A group the real pass then fails to rewrite (a database error) is the only
// way the two can differ, and then the real pass does less, never more.
func PreviewCompaction(peerID string, userGUID string) (*CompactionResult, error) {
	if peerID == "" {
		return nil, serr.New("peer ID is required to preview compaction")
	}
	res := &CompactionResult{}
	before, err := CountUnsentChangesForPeer(peerID, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to count pending changes")
	}
	res.ChangesBefore = before
	removed := 0

	catChanges, err := GetUnsentCategoryChangesForPeer(peerID, userGUID, 0)
	if err != nil {
		return nil, serr.Wrap(err, "failed to load pending category changes")
	}
	catOrder, catGroups := groupCategoryChanges(catChanges)
	for _, guid := range catOrder {
		grp := catGroups[guid]
		if len(grp) < 2 {
			continue
		}
		if netCategoryOperation(grp) != OperationDelete {
			if cat, err := GetCategoryByGUID(guid); err != nil || cat == nil {
				continue
			}
		}
		res.CategoriesCompacted++
		removed += len(grp) - 1
	}

	noteChanges, err := GetUnsentChangesForPeer(peerID, userGUID, 0)
	if err != nil {
		return nil, serr.Wrap(err, "failed to load pending note changes")
	}
	noteOrder, noteGroups := groupNoteChanges(noteChanges)
	for _, guid := range noteOrder {
		grp := noteGroups[guid]
		if len(grp) < 2 {
			continue
		}
		if netNoteOperation(grp) != OperationDelete {
			if note, err := GetNoteByGUID(guid); err != nil || note == nil {
				continue
			}
		}
		res.NotesCompacted++
		removed += len(grp) - 1
	}

	res.ChangesAfter = res.ChangesBefore - removed
	return res, nil
}
