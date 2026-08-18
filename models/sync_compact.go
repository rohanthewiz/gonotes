package models

import (
	"database/sql"
	"sort"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Change-log compaction
//
// Every edit appends a row to note_changes / category_changes with a fragment
// describing what moved. That is exactly right for a spoke that syncs every
// five minutes and increasingly wasteful for one that syncs when asked: an
// afternoon of work on the same note is thirty rows, each carrying a body diff
// against the one before it, all of which the hub will replay in order to
// arrive at the state the note is already in locally.
//
// Compaction rewrites the PENDING (unsent) tail of that log into one change
// per entity:
//
//	note A: create ─ update ─ update ─ update  ──►  create (snapshot of A now)
//	note B: update ─ update ─ delete           ──►  delete
//	note C: update                             ──►  untouched (nothing to gain)
//
// Three properties make this safe rather than clever:
//
//  1. The replacement is built from the entity's CURRENT state, not by
//     replaying fragments. Whatever the sequence did, the row on disk is its
//     result, so a snapshot cannot drift from it — and a chain of body diffs
//     collapses to plain final text with no patch application at all.
//  2. The bitmask is the UNION of the bitmasks it replaces, so a field nobody
//     touched stays absent and the hub keeps its own value for it. Compaction
//     narrows the number of changes, never the fields they claim.
//  3. It only ever touches changes unsent to the peer being compacted for, and
//     the replacement inherits the created_at of the last change it swallows —
//     so its position in the merged, chronologically ordered stream (where a
//     category definition must precede the note mapping that references it) is
//     exactly the position the group already held.
//
// Changes recorded with OperationSync are skipped entirely: those are rows
// this machine wrote while APPLYING someone else's change, and they exist to
// record provenance, not to be replayed outward.
//
// This is destructive to local history — the superseded rows and their
// fragments are deleted. That is the point (an unsynced log is the only thing
// they are for), but it is also why nothing calls this on its own: it runs
// when a user asks, or when GONOTES_SYNC_COMPACT explicitly opts a spoke in.
// ============================================================================

// CompactionResult reports what one compaction pass did. Counts are of
// changes, not entities, except where the field name says otherwise.
type CompactionResult struct {
	NotesCompacted      int `json:"notes_compacted"`      // notes whose pending changes collapsed
	CategoriesCompacted int `json:"categories_compacted"` // categories whose pending changes collapsed
	ChangesBefore       int `json:"changes_before"`       // pending changes on entry
	ChangesAfter        int `json:"changes_after"`        // pending changes on exit
}

// Removed is how many pending changes the pass eliminated. Never negative:
// each group of n>1 becomes exactly one change.
func (r *CompactionResult) Removed() int {
	if r == nil {
		return 0
	}
	return r.ChangesBefore - r.ChangesAfter
}

// CompactPendingChanges collapses this peer's unsent change log in place and
// reports the result.
//
// Categories are compacted before notes so that, if both end up rewritten in
// the same pass, category changes keep the earlier timestamps they already
// had — the ordering the hub relies on to have a category before the note
// mapping that names it.
//
// userGUID scopes the work on a multi-user hub; a spoke passes "".
func CompactPendingChanges(peerID string, userGUID string) (*CompactionResult, error) {
	if peerID == "" {
		return nil, serr.New("peer ID is required to compact pending changes")
	}

	res := &CompactionResult{}

	before, err := CountUnsentChangesForPeer(peerID, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to count pending changes before compaction")
	}
	res.ChangesBefore = before

	if err := compactCategoryChanges(peerID, userGUID, res); err != nil {
		return nil, err
	}
	if err := compactNoteChanges(peerID, userGUID, res); err != nil {
		return nil, err
	}

	after, err := CountUnsentChangesForPeer(peerID, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to count pending changes after compaction")
	}
	res.ChangesAfter = after

	if res.Removed() > 0 {
		logger.Info("Compacted pending sync changes",
			"peer_id", peerID,
			"notes", res.NotesCompacted,
			"categories", res.CategoriesCompacted,
			"changes_before", res.ChangesBefore,
			"changes_after", res.ChangesAfter,
		)
	}
	return res, nil
}

// ============================================================================
// Notes
// ============================================================================

// compactNoteChanges rewrites every note with more than one pending change.
func compactNoteChanges(peerID string, userGUID string, res *CompactionResult) error {
	// limit 0 means "all of them": compaction that stopped at a page boundary
	// would leave the tail of a group behind and change what gets pushed.
	changes, err := GetUnsentChangesForPeer(peerID, userGUID, 0)
	if err != nil {
		return serr.Wrap(err, "failed to load pending note changes for compaction")
	}

	order, groups := groupNoteChanges(changes)
	for _, noteGUID := range order {
		grp := groups[noteGUID]
		if len(grp) < 2 {
			continue // one change is already its own compaction
		}
		compacted, err := compactNoteGroup(noteGUID, grp)
		if err != nil {
			// One unrewritable note must not abandon the rest: the group is
			// left exactly as it was, which is always a correct thing to push.
			logger.LogErr(err, "failed to compact note change group", "note_guid", noteGUID)
			continue
		}
		if compacted {
			res.NotesCompacted++
		}
	}
	return nil
}

// groupNoteChanges buckets changes by note, dropping OperationSync rows, and
// returns the note GUIDs in first-appearance order so a pass over them is
// deterministic (map iteration is not).
func groupNoteChanges(changes []NoteChange) ([]string, map[string][]NoteChange) {
	order := make([]string, 0, len(changes))
	groups := make(map[string][]NoteChange, len(changes))
	for _, c := range changes {
		if c.Operation == OperationSync {
			continue
		}
		if _, seen := groups[c.NoteGUID]; !seen {
			order = append(order, c.NoteGUID)
		}
		groups[c.NoteGUID] = append(groups[c.NoteGUID], c)
	}
	// GetUnsentChangesForPeer already sorts oldest-first globally; sorting
	// within the group keeps that true even if a caller hands us a raw slice.
	for guid := range groups {
		g := groups[guid]
		sort.SliceStable(g, func(i, j int) bool { return g[i].CreatedAt.Before(g[j].CreatedAt) })
		groups[guid] = g
	}
	return order, groups
}

// compactNoteGroup replaces one note's pending changes with a single change.
// Returns false (with no error) when the group is better left alone.
func compactNoteGroup(noteGUID string, grp []NoteChange) (bool, error) {
	last := grp[len(grp)-1]
	operation := netNoteOperation(grp)

	// Peers that had already received EVERY change in the group must not be
	// handed a replacement for them. The intersection is what the compacted
	// change starts out marked as synced to.
	settled, err := peersHoldingAllNoteChanges(grp)
	if err != nil {
		return false, err
	}

	var fragmentID sql.NullInt64
	// The engine the replacement lands in: the note's current database, since
	// privacy toggles move a note (and therefore where its history belongs).
	// A net delete has no note to ask, so it inherits the last change's home.
	en := engineForID(last.ID)

	if operation != OperationDelete {
		note, err := GetNoteByGUID(noteGUID)
		if err != nil {
			return false, serr.Wrap(err, "failed to load note for compaction")
		}
		if note == nil {
			// The note is gone but the group does not say so — the log and the
			// data disagree, and rewriting is not how that gets resolved.
			return false, nil
		}
		en = engineForNoteID(note.ID)
		if en == nil {
			return false, serr.New("could not resolve database for note " + noteGUID)
		}

		fragment, err := noteSnapshotFragment(note, unionNoteBitmask(grp, operation))
		if err != nil {
			return false, err
		}
		id, err := insertNoteFragment(en, fragment)
		if err != nil {
			return false, serr.Wrap(err, "failed to insert compacted note fragment")
		}
		fragmentID = sql.NullInt64{Int64: id, Valid: true}
	}

	// The replacement takes the last change's timestamp and author: it stands
	// in for that change, and AuthoredAt (which drives last-writer-wins on the
	// hub) is read from the note itself, not from here.
	user := ""
	if last.User.Valid {
		user = last.User.String
	}
	changeGUID := GenerateChangeGUID()
	if err := insertNoteChangeAt(en, changeGUID, noteGUID, operation, fragmentID, user, last.CreatedAt); err != nil {
		return false, serr.Wrap(err, "failed to insert compacted note change")
	}

	// Only now is it safe to drop the originals: if the process dies between
	// the insert above and the deletes below, the push is redundant rather
	// than lossy.
	for _, c := range grp {
		if err := deleteNoteChangeRow(c); err != nil {
			logger.LogErr(err, "failed to delete superseded note change", "change_id", c.ID)
		}
	}

	if len(settled) > 0 {
		newID, err := noteChangeIDByGUID(en, changeGUID)
		if err != nil {
			logger.LogErr(err, "failed to re-read compacted note change id", "change_guid", changeGUID)
		} else {
			for _, peer := range settled {
				if err := MarkChangeSyncedToPeer(newID, peer); err != nil {
					logger.LogErr(err, "failed to carry sync marker onto compacted change",
						"change_id", newID, "peer_id", peer)
				}
			}
		}
	}
	return true, nil
}

// netNoteOperation reduces a group of operations to the one that describes
// their combined effect:
//
//	…anything, delete  → delete   (the note is gone; how it got there is moot)
//	create, …          → create   (the hub has never seen this note)
//	otherwise          → update
func netNoteOperation(grp []NoteChange) int32 {
	if grp[len(grp)-1].Operation == OperationDelete {
		return OperationDelete
	}
	for _, c := range grp {
		if c.Operation == OperationCreate {
			return OperationCreate
		}
	}
	return OperationUpdate
}

// unionNoteBitmask ORs together the bitmasks of a group's fragments, so the
// compacted fragment claims every field any member claimed and no more.
//
// A create is the exception: the receiving hub builds the whole note from
// this one fragment, so every field it can carry has to be present.
func unionNoteBitmask(grp []NoteChange, operation int32) int16 {
	if operation == OperationCreate {
		return FragmentTitle | FragmentDescription | FragmentBody |
			FragmentTags | FragmentIsPrivate | FragmentCategories
	}
	var mask int16
	for _, c := range grp {
		if !c.NoteFragmentID.Valid {
			continue
		}
		fragment, err := GetNoteFragment(c.NoteFragmentID.Int64)
		if err != nil || fragment == nil {
			continue
		}
		mask |= fragment.Bitmask
	}
	return mask
}

// noteSnapshotFragment fills a fragment from the note as it stands now, for
// exactly the fields the bitmask names. Body is written as literal text with
// BodyIsDiff false — the whole point of snapshotting is that no chain of
// patches has to survive the trip.
func noteSnapshotFragment(note *Note, bitmask int16) (NoteFragment, error) {
	fragment := NoteFragment{Bitmask: bitmask}

	if bitmask&FragmentTitle != 0 {
		fragment.Title = sql.NullString{String: note.Title, Valid: true}
	}
	if bitmask&FragmentDescription != 0 {
		fragment.Description = note.Description
	}
	if bitmask&FragmentBody != 0 {
		fragment.Body = note.Body
		fragment.BodyIsDiff = false
	}
	if bitmask&FragmentTags != 0 {
		fragment.Tags = note.Tags
	}
	if bitmask&FragmentIsPrivate != 0 {
		fragment.IsPrivate = sql.NullBool{Bool: note.IsPrivate, Valid: true}
	}
	if bitmask&FragmentCategories != 0 {
		mappingsJSON, err := noteCategoryMappingsJSON(note.ID)
		if err != nil {
			return fragment, serr.Wrap(err, "failed to snapshot note categories for compaction")
		}
		fragment.Categories = sql.NullString{String: mappingsJSON, Valid: true}
	}
	return fragment, nil
}

// insertNoteChangeAt is insertNoteChange with an explicit created_at. The
// ordinary path takes the column default (now), which is right for a change
// that is happening now; a compacted change is standing in for one that
// already happened, and it has to keep that change's place in the stream.
func insertNoteChangeAt(en *dbEngine, changeGUID, noteGUID string, operation int32,
	fragmentID sql.NullInt64, user string, createdAt time.Time) error {
	query := `
		INSERT INTO note_changes (id, guid, note_guid, operation, note_fragment_id, change_user, created_at)
		VALUES (nextval('note_changes_id_seq'), ?, ?, ?, ?, ?, ?)
	`
	userVal := sql.NullString{}
	if user != "" {
		userVal = sql.NullString{String: user, Valid: true}
	}
	_, err := en.Exec(query, changeGUID, noteGUID, operation, fragmentID, userVal, createdAt)
	if err != nil {
		return serr.Wrap(err, "failed to insert note change with explicit timestamp")
	}
	return nil
}

// noteChangeIDByGUID re-reads the id the sequence assigned to a just-inserted
// change. insertNoteChange has no RETURNING clause and is used by every other
// writer as-is, so this asks rather than changing that contract.
func noteChangeIDByGUID(en *dbEngine, changeGUID string) (int64, error) {
	var id int64
	if err := en.QueryRow(`SELECT id FROM note_changes WHERE guid = ?`, changeGUID).Scan(&id); err != nil {
		return 0, serr.Wrap(err, "failed to look up note change by GUID")
	}
	return id, nil
}

// deleteNoteChangeRow removes a superseded change, its fragment, and its
// per-peer sync markers. Each row is deleted from its own home database:
// change and fragment ids encode which engine holds them (see engineForID),
// and a note that has changed privacy can have history on both sides.
func deleteNoteChangeRow(c NoteChange) error {
	if c.NoteFragmentID.Valid {
		if _, err := engineForID(c.NoteFragmentID.Int64).Exec(
			`DELETE FROM note_fragments WHERE id = ?`, c.NoteFragmentID.Int64); err != nil {
			return serr.Wrap(err, "failed to delete superseded note fragment")
		}
	}
	en := engineForID(c.ID)
	if _, err := en.Exec(`DELETE FROM note_change_sync_peers WHERE note_change_id = ?`, c.ID); err != nil {
		return serr.Wrap(err, "failed to delete superseded note change sync markers")
	}
	if _, err := en.Exec(`DELETE FROM note_changes WHERE id = ?`, c.ID); err != nil {
		return serr.Wrap(err, "failed to delete superseded note change")
	}
	return nil
}

// peersHoldingAllNoteChanges returns the peers that have already received
// every change in the group. Those peers are done with this group, and the
// compacted change is marked synced to them so compaction never resurrects
// work a peer has already seen.
func peersHoldingAllNoteChanges(grp []NoteChange) ([]string, error) {
	var common map[string]bool
	for _, c := range grp {
		peers, err := peersForNoteChange(c.ID)
		if err != nil {
			return nil, err
		}
		if common == nil {
			common = peers
			continue
		}
		for p := range common {
			if !peers[p] {
				delete(common, p)
			}
		}
		if len(common) == 0 {
			return nil, nil
		}
	}
	return sortedKeys(common), nil
}

// peersForNoteChange lists the peers a single change has been sent to.
func peersForNoteChange(changeID int64) (map[string]bool, error) {
	rows, err := engineForID(changeID).Query(
		`SELECT peer_id FROM note_change_sync_peers WHERE note_change_id = ?`, changeID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query sync peers for note change")
	}
	defer rows.Close()

	peers := map[string]bool{}
	for rows.Next() {
		var peer string
		if err := rows.Scan(&peer); err != nil {
			return nil, serr.Wrap(err, "failed to scan sync peer row")
		}
		peers[peer] = true
	}
	return peers, rows.Err()
}

// ============================================================================
// Categories
//
// The category half is the same shape with a smaller surface: categories live
// only in the public database, so there is no engine routing to do, and a
// category's whole state is three fields.
// ============================================================================

// compactCategoryChanges rewrites every category with more than one pending
// change.
func compactCategoryChanges(peerID string, userGUID string, res *CompactionResult) error {
	changes, err := GetUnsentCategoryChangesForPeer(peerID, userGUID, 0)
	if err != nil {
		return serr.Wrap(err, "failed to load pending category changes for compaction")
	}

	order, groups := groupCategoryChanges(changes)
	for _, catGUID := range order {
		grp := groups[catGUID]
		if len(grp) < 2 {
			continue
		}
		compacted, err := compactCategoryGroup(catGUID, grp)
		if err != nil {
			logger.LogErr(err, "failed to compact category change group", "category_guid", catGUID)
			continue
		}
		if compacted {
			res.CategoriesCompacted++
		}
	}
	return nil
}

// groupCategoryChanges buckets changes by category, dropping OperationSync
// rows, in first-appearance order.
func groupCategoryChanges(changes []CategoryChange) ([]string, map[string][]CategoryChange) {
	order := make([]string, 0, len(changes))
	groups := make(map[string][]CategoryChange, len(changes))
	for _, c := range changes {
		if c.Operation == OperationSync {
			continue
		}
		if _, seen := groups[c.CategoryGUID]; !seen {
			order = append(order, c.CategoryGUID)
		}
		groups[c.CategoryGUID] = append(groups[c.CategoryGUID], c)
	}
	for guid := range groups {
		g := groups[guid]
		sort.SliceStable(g, func(i, j int) bool { return g[i].CreatedAt.Before(g[j].CreatedAt) })
		groups[guid] = g
	}
	return order, groups
}

// compactCategoryGroup replaces one category's pending changes with a single
// change built from its current row.
func compactCategoryGroup(categoryGUID string, grp []CategoryChange) (bool, error) {
	last := grp[len(grp)-1]
	operation := netCategoryOperation(grp)

	settled, err := peersHoldingAllCategoryChanges(grp)
	if err != nil {
		return false, err
	}

	var fragmentID sql.NullInt64
	if operation != OperationDelete {
		category, err := GetCategoryByGUID(categoryGUID)
		if err != nil {
			return false, serr.Wrap(err, "failed to load category for compaction")
		}
		if category == nil {
			return false, nil // gone without a delete change; leave the log alone
		}

		fragment := categorySnapshotFragment(category, unionCategoryBitmask(grp, operation))
		id, err := insertCategoryFragment(fragment)
		if err != nil {
			return false, serr.Wrap(err, "failed to insert compacted category fragment")
		}
		fragmentID = sql.NullInt64{Int64: id, Valid: true}
	}

	user := ""
	if last.User.Valid {
		user = last.User.String
	}
	changeGUID := GenerateChangeGUID()
	if err := insertCategoryChangeAt(changeGUID, categoryGUID, operation, fragmentID, user, last.CreatedAt); err != nil {
		return false, serr.Wrap(err, "failed to insert compacted category change")
	}

	for _, c := range grp {
		if err := deleteCategoryChangeRow(c); err != nil {
			logger.LogErr(err, "failed to delete superseded category change", "change_id", c.ID)
		}
	}

	if len(settled) > 0 {
		newID, err := categoryChangeIDByGUID(changeGUID)
		if err != nil {
			logger.LogErr(err, "failed to re-read compacted category change id", "change_guid", changeGUID)
		} else {
			for _, peer := range settled {
				if err := MarkCategoryChangeSyncedToPeer(newID, peer); err != nil {
					logger.LogErr(err, "failed to carry sync marker onto compacted category change",
						"change_id", newID, "peer_id", peer)
				}
			}
		}
	}
	return true, nil
}

// netCategoryOperation mirrors netNoteOperation.
func netCategoryOperation(grp []CategoryChange) int32 {
	if grp[len(grp)-1].Operation == OperationDelete {
		return OperationDelete
	}
	for _, c := range grp {
		if c.Operation == OperationCreate {
			return OperationCreate
		}
	}
	return OperationUpdate
}

// unionCategoryBitmask ORs the group's fragment bitmasks; a create claims
// everything, for the same reason a note create does.
func unionCategoryBitmask(grp []CategoryChange, operation int32) int16 {
	if operation == OperationCreate {
		return CatFragmentName | CatFragmentDescription | CatFragmentSubcategories
	}
	var mask int16
	for _, c := range grp {
		if !c.CategoryFragmentID.Valid {
			continue
		}
		fragment, err := GetCategoryFragment(c.CategoryFragmentID.Int64)
		if err != nil || fragment == nil {
			continue
		}
		mask |= fragment.Bitmask
	}
	return mask
}

// categorySnapshotFragment fills a fragment from the category as it stands.
func categorySnapshotFragment(category *Category, bitmask int16) CategoryFragment {
	fragment := CategoryFragment{Bitmask: bitmask}
	if bitmask&CatFragmentName != 0 {
		fragment.Name = sql.NullString{String: category.Name, Valid: true}
	}
	if bitmask&CatFragmentDescription != 0 {
		fragment.Description = category.Description
	}
	if bitmask&CatFragmentSubcategories != 0 {
		fragment.Subcategories = category.Subcategories
	}
	return fragment
}

// insertCategoryChangeAt is insertCategoryChange with an explicit created_at.
func insertCategoryChangeAt(changeGUID, categoryGUID string, operation int32,
	fragmentID sql.NullInt64, user string, createdAt time.Time) error {
	query := `
		INSERT INTO category_changes (id, guid, category_guid, operation, category_fragment_id, change_user, created_at)
		VALUES (nextval('category_changes_id_seq'), ?, ?, ?, ?, ?, ?)
	`
	userVal := sql.NullString{}
	if user != "" {
		userVal = sql.NullString{String: user, Valid: true}
	}
	_, err := pubDB.Exec(query, changeGUID, categoryGUID, operation, fragmentID, userVal, createdAt)
	if err != nil {
		return serr.Wrap(err, "failed to insert category change with explicit timestamp")
	}
	return nil
}

// categoryChangeIDByGUID re-reads a just-inserted category change's id.
func categoryChangeIDByGUID(changeGUID string) (int64, error) {
	var id int64
	if err := pubDB.QueryRow(`SELECT id FROM category_changes WHERE guid = ?`, changeGUID).Scan(&id); err != nil {
		return 0, serr.Wrap(err, "failed to look up category change by GUID")
	}
	return id, nil
}

// deleteCategoryChangeRow removes a superseded category change, its fragment,
// and its per-peer sync markers.
func deleteCategoryChangeRow(c CategoryChange) error {
	if c.CategoryFragmentID.Valid {
		if _, err := pubDB.Exec(`DELETE FROM category_fragments WHERE id = ?`, c.CategoryFragmentID.Int64); err != nil {
			return serr.Wrap(err, "failed to delete superseded category fragment")
		}
	}
	if _, err := pubDB.Exec(`DELETE FROM category_change_sync_peers WHERE category_change_id = ?`, c.ID); err != nil {
		return serr.Wrap(err, "failed to delete superseded category change sync markers")
	}
	if _, err := pubDB.Exec(`DELETE FROM category_changes WHERE id = ?`, c.ID); err != nil {
		return serr.Wrap(err, "failed to delete superseded category change")
	}
	return nil
}

// peersHoldingAllCategoryChanges mirrors peersHoldingAllNoteChanges.
func peersHoldingAllCategoryChanges(grp []CategoryChange) ([]string, error) {
	var common map[string]bool
	for _, c := range grp {
		peers, err := peersForCategoryChange(c.ID)
		if err != nil {
			return nil, err
		}
		if common == nil {
			common = peers
			continue
		}
		for p := range common {
			if !peers[p] {
				delete(common, p)
			}
		}
		if len(common) == 0 {
			return nil, nil
		}
	}
	return sortedKeys(common), nil
}

// peersForCategoryChange lists the peers a single category change reached.
func peersForCategoryChange(changeID int64) (map[string]bool, error) {
	rows, err := pubDB.Query(
		`SELECT peer_id FROM category_change_sync_peers WHERE category_change_id = ?`, changeID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query sync peers for category change")
	}
	defer rows.Close()

	peers := map[string]bool{}
	for rows.Next() {
		var peer string
		if err := rows.Scan(&peer); err != nil {
			return nil, serr.Wrap(err, "failed to scan category sync peer row")
		}
		peers[peer] = true
	}
	return peers, rows.Err()
}

// sortedKeys returns a map's keys in a stable order, so the markers written
// after a compaction land in a predictable sequence.
func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
