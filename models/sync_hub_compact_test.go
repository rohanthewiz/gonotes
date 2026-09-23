package models_test

import (
	"testing"
	"time"

	"gonotes/models"
)

// Hub compaction tests. The hub's log has two kinds of reader, and each
// test checks both: peers that already had the history, which must not get it
// again, and peers that didn't (a new spoke, or one that fell behind), which
// must still reach the same final state from what is left.

// hubReceives applies a spoke's pushed change on the hub the way the push
// handler does: apply, then record that the sender already has it.
func hubReceives(t *testing.T, change models.SyncChange, from string) {
	t.Helper()
	if err := models.ApplyIncomingSyncChange(change); err != nil {
		t.Fatalf("hub failed to apply %s: %v", change.GUID, err)
	}
	models.MarkChangeGUIDSyncedToPeer(change.GUID, from)
}

// noteChangesFor returns the changes about one note that a peer would pull.
func noteChangesFor(t *testing.T, peer, noteGUID string) []models.SyncChange {
	t.Helper()
	resp, err := models.GetUnifiedChangesForPeer(peer, "", 1000)
	if err != nil {
		t.Fatalf("pull for %s: %v", peer, err)
	}
	var out []models.SyncChange
	for _, c := range resp.Changes {
		if c.EntityType == "note" && c.EntityGUID == noteGUID {
			out = append(out, c)
		}
	}
	return out
}

// TestHubCompactCollapsesRelayHistory is the central case: three edits relayed
// through the hub become one snapshot. The spoke that sent them gets nothing,
// and a spoke that is behind or brand new gets exactly the one snapshot.
func TestHubCompactCollapsesRelayHistory(t *testing.T) {
	setupRelayTestDB(t)
	const note = "hub-note-a"

	hubReceives(t, incomingNote("hub-x1", note, "Title", "v1", models.OperationCreate), "spoke-a")
	hubReceives(t, incomingNote("hub-x2", note, "Title", "v2", models.OperationUpdate), "spoke-a")
	hubReceives(t, incomingNote("hub-x3", note, "Title", "v3", models.OperationUpdate), "spoke-a")
	// spoke-b pulled only the first one before going offline.
	models.MarkChangeGUIDSyncedToPeer("hub-x1", "spoke-b")

	res, err := models.CompactHubChangeLog(0)
	if err != nil {
		t.Fatalf("hub compaction: %v", err)
	}
	if res.NotesCompacted != 1 || res.ChangesRemoved != 2 {
		t.Fatalf("result = %+v, want 1 note and 2 changes removed", res)
	}

	if got := noteChangesFor(t, "spoke-a", note); len(got) != 0 {
		t.Errorf("the sender is handed its own history back: %d change(s)", len(got))
	}

	for _, peer := range []string{"spoke-b", "spoke-new"} {
		got := noteChangesFor(t, peer, note)
		if len(got) != 1 {
			t.Fatalf("%s: %d changes, want the one snapshot", peer, len(got))
		}
		c := got[0]
		if c.Operation != models.OperationSync {
			t.Errorf("%s: operation %d, want %d (create-or-update by what the reader has)",
				peer, c.Operation, models.OperationSync)
		}
		frag, ok := c.Fragment.(*models.NoteFragmentOutput)
		if !ok || frag == nil {
			t.Fatalf("%s: no fragment on the snapshot", peer)
		}
		if frag.Body == nil || *frag.Body != "v3" || frag.BodyIsDiff {
			t.Errorf("%s: snapshot body = %v (diff %v), want literal v3", peer, frag.Body, frag.BodyIsDiff)
		}
		if frag.Title == nil || *frag.Title != "Title" {
			t.Errorf("%s: snapshot lacks the title a new reader needs to create the note", peer)
		}
	}
}

// TestHubCompactRemembersSupersededGUIDs covers a spoke whose push response
// was lost, so it pushes an old change again after the hub has compacted it
// away. The hub must still recognise the GUID. Otherwise the stale v2 is
// applied over v3, because hub apply has no last-writer-wins guard.
func TestHubCompactRemembersSupersededGUIDs(t *testing.T) {
	setupRelayTestDB(t)
	const note = "hub-note-b"

	hubReceives(t, incomingNote("hub-y1", note, "Title", "v1", models.OperationCreate), "spoke-a")
	hubReceives(t, incomingNote("hub-y2", note, "Title", "v2", models.OperationUpdate), "spoke-a")
	hubReceives(t, incomingNote("hub-y3", note, "Title", "v3", models.OperationUpdate), "spoke-a")

	if _, err := models.CompactHubChangeLog(0); err != nil {
		t.Fatalf("hub compaction: %v", err)
	}

	if err := models.ApplyIncomingSyncChange(
		incomingNote("hub-y2", note, "Title", "v2", models.OperationUpdate)); err != nil {
		t.Fatalf("re-push: %v", err)
	}
	n, err := models.GetNoteByGUID(note)
	if err != nil || n == nil {
		t.Fatalf("read note: %v", err)
	}
	if n.Body.String != "v3" {
		t.Errorf("a re-pushed, compacted change was applied again: body %q, want v3", n.Body.String)
	}
}

// TestHubCompactWaitsForQuiet: history newer than the quiet period is left
// alone, however long it is.
func TestHubCompactWaitsForQuiet(t *testing.T) {
	setupRelayTestDB(t)
	const note = "hub-note-c"

	hubReceives(t, incomingNote("hub-z1", note, "Title", "v1", models.OperationCreate), "spoke-a")
	hubReceives(t, incomingNote("hub-z2", note, "Title", "v2", models.OperationUpdate), "spoke-a")

	res, err := models.CompactHubChangeLog(time.Hour)
	if err != nil {
		t.Fatalf("hub compaction: %v", err)
	}
	if res.ChangesRemoved != 0 {
		t.Errorf("compacted a note edited moments ago: %+v", res)
	}
	if got := noteChangesFor(t, "spoke-new", note); len(got) != 2 {
		t.Errorf("new peer sees %d changes, want the untouched 2", len(got))
	}
}

// TestHubCompactKeepsDeletes: a history ending in a delete becomes one delete,
// so a spoke that still has the note learns it is gone.
func TestHubCompactKeepsDeletes(t *testing.T) {
	setupRelayTestDB(t)
	const note = "hub-note-d"

	hubReceives(t, incomingNote("hub-d1", note, "Title", "v1", models.OperationCreate), "spoke-a")
	hubReceives(t, incomingNote("hub-d2", note, "Title", "v2", models.OperationUpdate), "spoke-a")
	hubReceives(t, models.SyncChange{
		GUID: "hub-d3", EntityType: "note", EntityGUID: note,
		Operation: models.OperationDelete, AuthoredAt: time.Now().UTC(),
	}, "spoke-a")
	models.MarkChangeGUIDSyncedToPeer("hub-d1", "spoke-b")

	if _, err := models.CompactHubChangeLog(0); err != nil {
		t.Fatalf("hub compaction: %v", err)
	}
	got := noteChangesFor(t, "spoke-b", note)
	if len(got) != 1 || got[0].Operation != models.OperationDelete {
		t.Fatalf("spoke-b gets %+v, want a single delete", got)
	}
}

// TestHubCompactPlacesCategoriesAtTheirFirstChange checks the ordering
// rule. A compacted category takes its history's earliest timestamp, so a
// spoke bootstrapping from the hub receives it before any note that maps to
// it, even when the category was renamed after the mapping was made.
func TestHubCompactPlacesCategoriesAtTheirFirstChange(t *testing.T) {
	setupRelayTestDB(t)

	cat, err := models.CreateCategory(models.CategoryInput{Name: "Before"}, relayUserGUID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := models.UpdateCategory(cat.ID, models.CategoryInput{Name: "After"}, relayUserGUID); err != nil {
		t.Fatalf("rename category: %v", err)
	}

	before, err := models.GetUnsentCategoryChangesForPeer("spoke-new", "", 0)
	if err != nil || len(before) != 2 {
		t.Fatalf("expected 2 category changes before compaction, got %d (%v)", len(before), err)
	}
	first := before[0].CreatedAt

	res, err := models.CompactHubChangeLog(0)
	if err != nil {
		t.Fatalf("hub compaction: %v", err)
	}
	if res.CategoriesCompacted != 1 {
		t.Fatalf("result = %+v, want 1 category compacted", res)
	}

	after, err := models.GetUnsentCategoryChangesForPeer("spoke-new", "", 0)
	if err != nil || len(after) != 1 {
		t.Fatalf("expected 1 category change after compaction, got %d (%v)", len(after), err)
	}
	if !after[0].CreatedAt.Equal(first) {
		t.Errorf("compacted category sits at %v, want its first change's %v", after[0].CreatedAt, first)
	}
	frag, err := models.GetCategoryFragment(after[0].CategoryFragmentID.Int64)
	if err != nil || frag == nil || frag.Name.String != "After" {
		t.Errorf("compacted category fragment = %+v (%v), want the current name", frag, err)
	}
}
