package models

import (
	"sort"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Hub change-log compaction
//
// The spoke compactor (CompactPendingChanges) packs down one machine's
// unsent tail to one peer, and skips operation 9. On a hub that covers almost
// nothing: every change a spoke pushes is recorded there as an operation-9
// relay, and those relays are what the other spokes pull. So a hub's log only
// grows. Every edit made anywhere lands as one more change row and, worse, one
// more fragment, since relayed fragments are full-body snapshots.
//
// The hub's log also has a second job the spoke's doesn't: it is how a NEW
// spoke bootstraps. Pull hands a peer every change not yet marked delivered to
// it, so a spoke seen for the first time receives the whole log from the
// beginning. That rules out simply deleting changes every known peer has
// received, because the next peer would never see those notes.
//
// What stays safe is what the spoke compactor already does, applied to every
// peer at once: collapse each entity's history into one change that says "this
// entity is now X".
//
//	note A: create ─ relay ─ relay ─ update ─ relay   ──►  relay (full snapshot of A)
//	note B: relay ─ relay ─ delete                    ──►  delete
//	note C: relay                                     ──►  untouched
//
// A spoke that already had every change in the group is marked as holding the
// replacement, so it never sees it. Any other spoke, new or partly behind,
// receives one snapshot instead of the tail, which lands it in the same
// state.
//
// It differs from the spoke compactor in four places, each forced by the
// hub's many-readers position:
//
//  1. Operation 9, not the group's net operation. Readers are at different
//     points. For one of them a net "create" would be right; for another,
//     which already has the note, a create is ignored as a duplicate (see
//     applyIncomingNoteChange) and the edits in it would be lost. Operation 9
//     is applied as create-or-update according to what the receiver has on
//     disk, which is correct for every reader. Deletes stay deletes.
//  2. Full snapshots. For the same reason, the fragment carries every field,
//     not the union of the group's bitmasks: a reader that never had the note
//     builds it from this fragment alone.
//  3. Tombstones. A spoke whose push response was lost pushes the same change
//     GUIDs again on a later cycle. The hub recognises the repeat by GUID
//     (changeGUIDExists), and hub apply has no last-writer-wins guard, so a
//     GUID it had forgotten would re-apply a stale body diff. Superseded GUIDs
//     are therefore kept in compacted_change_guids.
//  4. Only quiet entities. A group is compacted only when its newest change
//     is older than quietFor. Recent history stays readable for debugging,
//     and a group that is still growing isn't rewritten on every pass.
//
// Placement in the stream: a category's replacement takes its group's FIRST
// timestamp, and a note's its LAST. A note's snapshot carries its category
// mappings, and a receiver can map a note only to a category it already has.
// Every category a note maps to was created before the mapping, so it sorts
// before the note on both counts. Taking the category's last timestamp could
// put a renamed category after notes that reference it, and a bootstrapping
// spoke would then drop those mappings.
//
// Nothing runs this automatically unless the server opts in (see
// GONOTES_HUB_COMPACT_INTERVAL in main.go), or an admin calls
// POST /api/v1/admin/sync/compact.
// ============================================================================

// DefaultHubCompactQuiet is how long an entity must go unchanged before the
// hub compactor rewrites its history, when the caller does not say.
const DefaultHubCompactQuiet = 24 * time.Hour

// HubCompactionResult reports one hub compaction pass.
type HubCompactionResult struct {
	NotesCompacted      int `json:"notes_compacted"`      // notes whose history collapsed
	CategoriesCompacted int `json:"categories_compacted"` // categories whose history collapsed
	ChangesRemoved      int `json:"changes_removed"`      // change rows eliminated (n per group becomes 1)
}

// CompactHubChangeLog collapses the history of every entity that has been
// unchanged for at least quietFor. It is global: every user and every peer.
// Delivery is tracked per peer, so compacting for one peer at a time could not
// shrink a log that all of them read.
//
// Categories are compacted before notes, as in CompactPendingChanges.
func CompactHubChangeLog(quietFor time.Duration) (*HubCompactionResult, error) {
	if quietFor < 0 {
		return nil, serr.New("quiet period cannot be negative")
	}
	cutoff := time.Now().Add(-quietFor)
	res := &HubCompactionResult{}

	if err := compactHubCategories(cutoff, res); err != nil {
		return nil, err
	}
	if err := compactHubNotes(cutoff, res); err != nil {
		return nil, err
	}

	if res.ChangesRemoved > 0 {
		logger.Info("Compacted hub change log",
			"notes", res.NotesCompacted,
			"categories", res.CategoriesCompacted,
			"changes_removed", res.ChangesRemoved,
			"quiet_for", quietFor.String(),
		)
	}
	return res, nil
}

// compactHubNotes rewrites every quiet note with more than one change.
func compactHubNotes(cutoff time.Time, res *HubCompactionResult) error {
	changes, err := allNoteChanges()
	if err != nil {
		return err
	}

	// Group by note, keeping first-appearance order so a pass is deterministic.
	// Operation 9 rows are included: on a hub they are most of the log.
	order := make([]string, 0, len(changes))
	groups := make(map[string][]NoteChange, len(changes))
	for _, c := range changes {
		if _, seen := groups[c.NoteGUID]; !seen {
			order = append(order, c.NoteGUID)
		}
		groups[c.NoteGUID] = append(groups[c.NoteGUID], c)
	}

	for _, noteGUID := range order {
		grp := groups[noteGUID]
		if len(grp) < 2 {
			continue
		}
		last := grp[len(grp)-1]
		if last.CreatedAt.After(cutoff) {
			continue // not quiet yet
		}

		plan := groupPlan{
			operation:    OperationSync,
			fullSnapshot: true,
			createdAt:    last.CreatedAt,
			tombstone:    true,
		}
		if last.Operation == OperationDelete {
			plan.operation = OperationDelete
		}

		compacted, err := compactNoteGroupAs(noteGUID, grp, plan)
		if err != nil {
			// One note that can't be rewritten must not stop the others. Its
			// group is left exactly as it was, which is always correct to serve.
			logger.LogErr(err, "failed to compact hub note history", "note_guid", noteGUID)
			continue
		}
		if compacted {
			res.NotesCompacted++
			res.ChangesRemoved += len(grp) - 1
		}
	}
	return nil
}

// compactHubCategories rewrites every quiet category with more than one
// change.
func compactHubCategories(cutoff time.Time, res *HubCompactionResult) error {
	changes, err := allCategoryChanges()
	if err != nil {
		return err
	}

	order := make([]string, 0, len(changes))
	groups := make(map[string][]CategoryChange, len(changes))
	for _, c := range changes {
		if _, seen := groups[c.CategoryGUID]; !seen {
			order = append(order, c.CategoryGUID)
		}
		groups[c.CategoryGUID] = append(groups[c.CategoryGUID], c)
	}

	for _, catGUID := range order {
		grp := groups[catGUID]
		if len(grp) < 2 {
			continue
		}
		first, last := grp[0], grp[len(grp)-1]
		if last.CreatedAt.After(cutoff) {
			continue
		}

		// First timestamp for a live category, so it still sorts ahead of every
		// note mapping that names it (see the file comment). A delete keeps the
		// last: it has to follow those mappings, not precede them.
		plan := groupPlan{
			operation:    OperationSync,
			fullSnapshot: true,
			createdAt:    first.CreatedAt,
			tombstone:    true,
		}
		if last.Operation == OperationDelete {
			plan.operation = OperationDelete
			plan.createdAt = last.CreatedAt
		}

		compacted, err := compactCategoryGroupAs(catGUID, grp, plan)
		if err != nil {
			logger.LogErr(err, "failed to compact hub category history", "category_guid", catGUID)
			continue
		}
		if compacted {
			res.CategoriesCompacted++
			res.ChangesRemoved += len(grp) - 1
		}
	}
	return nil
}

// allNoteChanges reads every note change from both databases, oldest first.
// Unlike GetUnsentChangesForPeer there is no peer or user filter: the hub
// compactor rewrites the log for everyone who reads it.
func allNoteChanges() ([]NoteChange, error) {
	changes, err := queryBothNotes(func(en *dbEngine) ([]NoteChange, error) {
		rows, err := en.Query(`
			SELECT id, guid, note_guid, operation, note_fragment_id, change_user, created_at
			FROM note_changes
			ORDER BY created_at ASC`)
		if err != nil {
			return nil, serr.Wrap(err, "failed to query note changes for hub compaction")
		}
		defer rows.Close()
		var out []NoteChange
		for rows.Next() {
			var c NoteChange
			if err := scanNoteChange(rows, &c); err != nil {
				return nil, serr.Wrap(err, "failed to scan note change for hub compaction")
			}
			out = append(out, c)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, err
	}
	// A note that changed privacy has history in both databases; merge them
	// into one timeline so each group is in true order.
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].CreatedAt.Before(changes[j].CreatedAt) })
	return changes, nil
}

// allCategoryChanges reads every category change, oldest first.
func allCategoryChanges() ([]CategoryChange, error) {
	rows, err := pubDB.Query(`
		SELECT id, guid, category_guid, operation, category_fragment_id, change_user, created_at
		FROM category_changes
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query category changes for hub compaction")
	}
	defer rows.Close()
	var out []CategoryChange
	for rows.Next() {
		var c CategoryChange
		if err := rows.Scan(&c.ID, &c.GUID, &c.CategoryGUID, &c.Operation,
			&c.CategoryFragmentID, &c.User, &c.CreatedAt); err != nil {
			return nil, serr.Wrap(err, "failed to scan category change for hub compaction")
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// recordCompactedGUIDs adds superseded change GUIDs to the tombstone table.
//
// A GUID already present is skipped rather than treated as an error. That
// happens only when an earlier pass crashed after writing tombstones but before
// deleting the rows. The rerun must go through, or that group could never be
// compacted.
func recordCompactedGUIDs(guids []string) error {
	for _, guid := range guids {
		var n int
		if err := pubDB.QueryRow(`SELECT COUNT(*) FROM compacted_change_guids WHERE guid = ?`, guid).Scan(&n); err != nil {
			return serr.Wrap(err, "failed to check compacted change guid")
		}
		if n > 0 {
			continue
		}
		if _, err := pubDB.Exec(`INSERT INTO compacted_change_guids (guid) VALUES (?)`, guid); err != nil {
			return serr.Wrap(err, "failed to record compacted change guid")
		}
	}
	return nil
}
