package models_test

import (
	"strings"
	"testing"
	"time"

	"gonotes/models"
)

// Compaction tests.
//
// The property under test throughout is the one the feature promises: fewer
// changes go out, and what goes out still describes the same final state. So
// every test asserts on BOTH sides — the count that shrank and the content
// that survived — because a compactor that drops changes is trivially good at
// the first half.

const compactUserGUID = "compact-test-user-guid"

func setupCompactTestDB(t *testing.T) {
	t.Helper()
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}
	t.Cleanup(func() { models.CloseDB() })
}

// strp is a pointer to a string literal, which NoteInput's optional fields
// take. Named short because the tests below are mostly made of it.
func strp(s string) *string { return &s }

// TestCompactCollapsesAnEditChain is the central case: a note created and then
// edited three times should reach the hub as one create carrying the final
// text, not as four changes replaying the history of a note nobody else has
// ever seen.
func TestCompactCollapsesAnEditChain(t *testing.T) {
	setupCompactTestDB(t)

	note, err := models.CreateNote(models.NoteInput{
		GUID:  "compact-chain",
		Title: "Draft",
		Body:  strp("first"),
	}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	for _, body := range []string{"second", "third", "final"} {
		if _, err := models.UpdateNote(note.ID, models.NoteInput{
			GUID:  "compact-chain",
			Title: "Draft",
			Body:  strp(body),
		}, compactUserGUID); err != nil {
			t.Fatalf("failed to update note with body %q: %v", body, err)
		}
	}

	before, err := models.CountUnsentChangesForPeer("hub", "")
	if err != nil {
		t.Fatalf("failed to count pending changes: %v", err)
	}
	if before < 4 {
		t.Fatalf("expected at least 4 pending changes before compaction, got %d", before)
	}

	res, err := models.CompactPendingChanges("hub", "")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if res.NotesCompacted != 1 {
		t.Errorf("expected 1 note compacted, got %d", res.NotesCompacted)
	}
	if res.ChangesAfter != 1 {
		t.Errorf("expected 1 change to remain, got %d", res.ChangesAfter)
	}
	if res.Removed() != before-1 {
		t.Errorf("Removed() = %d, want %d", res.Removed(), before-1)
	}

	// What survived has to be a create (the hub has never seen this note)
	// carrying the CURRENT body as literal text — no diff for the receiver to
	// apply against a base it does not have.
	pull, err := models.GetUnifiedChangesForPeer("hub", "", 100)
	if err != nil {
		t.Fatalf("failed to read the compacted stream: %v", err)
	}
	if len(pull.Changes) != 1 {
		t.Fatalf("expected 1 change in the stream, got %d", len(pull.Changes))
	}
	change := pull.Changes[0]
	if change.Operation != models.OperationCreate {
		t.Errorf("compacted operation = %d, want create (%d)", change.Operation, models.OperationCreate)
	}

	fragment, ok := change.Fragment.(*models.NoteFragmentOutput)
	if !ok {
		t.Fatalf("compacted fragment has type %T, want *models.NoteFragmentOutput", change.Fragment)
	}
	if fragment.BodyIsDiff {
		t.Error("compacted fragment carries a diff; a snapshot must be literal text")
	}
	if fragment.Body == nil || *fragment.Body != "final" {
		t.Errorf("compacted body = %v, want %q", fragment.Body, "final")
	}
	if fragment.Title == nil || *fragment.Title != "Draft" {
		t.Errorf("compacted title = %v, want %q", fragment.Title, "Draft")
	}
}

// TestCompactCollapsesToADelete pins the reduction rule: whatever a note's
// pending history is, if it ends in a delete then a delete is all the hub
// needs to hear.
func TestCompactCollapsesToADelete(t *testing.T) {
	setupCompactTestDB(t)

	note, err := models.CreateNote(models.NoteInput{
		GUID:  "compact-doomed",
		Title: "Doomed",
		Body:  strp("here today"),
	}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}
	if _, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID:  "compact-doomed",
		Title: "Doomed",
		Body:  strp("still here"),
	}, compactUserGUID); err != nil {
		t.Fatalf("failed to update note: %v", err)
	}
	if _, err := models.DeleteNote(note.ID, compactUserGUID); err != nil {
		t.Fatalf("failed to delete note: %v", err)
	}

	if _, err := models.CompactPendingChanges("hub", ""); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}

	pull, err := models.GetUnifiedChangesForPeer("hub", "", 100)
	if err != nil {
		t.Fatalf("failed to read the compacted stream: %v", err)
	}
	if len(pull.Changes) != 1 {
		t.Fatalf("expected 1 change in the stream, got %d", len(pull.Changes))
	}
	if got := pull.Changes[0].Operation; got != models.OperationDelete {
		t.Errorf("compacted operation = %d, want delete (%d)", got, models.OperationDelete)
	}
	if pull.Changes[0].Fragment != nil {
		t.Error("a delete carries no fragment, but one is present")
	}
}

// TestCompactLeavesASingleChangeAlone guards the no-op case. Compaction that
// rewrote a lone change would churn the log — new GUID, new row — for no
// reduction at all, and would make every "did anything change?" check lie.
func TestCompactLeavesASingleChangeAlone(t *testing.T) {
	setupCompactTestDB(t)

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "compact-lonely",
		Title: "Only change",
	}, compactUserGUID); err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	before, err := models.GetUnsentChangesForPeer("hub", "", 0)
	if err != nil {
		t.Fatalf("failed to read changes: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 pending change, got %d", len(before))
	}

	res, err := models.CompactPendingChanges("hub", "")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if res.NotesCompacted != 0 {
		t.Errorf("NotesCompacted = %d, want 0 — nothing had a chain to collapse", res.NotesCompacted)
	}
	if res.Removed() != 0 {
		t.Errorf("Removed() = %d, want 0", res.Removed())
	}

	after, err := models.GetUnsentChangesForPeer("hub", "", 0)
	if err != nil {
		t.Fatalf("failed to re-read changes: %v", err)
	}
	if len(after) != 1 || after[0].GUID != before[0].GUID {
		t.Errorf("the single change was rewritten; before %v, after %v", before, after)
	}
}

// TestCompactKeepsNotesSeparate checks the grouping. Two notes edited in an
// interleaved sequence must come out as two changes, not one merged mess.
func TestCompactKeepsNotesSeparate(t *testing.T) {
	setupCompactTestDB(t)

	first, err := models.CreateNote(models.NoteInput{GUID: "compact-a", Title: "A", Body: strp("a1")}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create first note: %v", err)
	}
	second, err := models.CreateNote(models.NoteInput{GUID: "compact-b", Title: "B", Body: strp("b1")}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create second note: %v", err)
	}
	if _, err := models.UpdateNote(first.ID, models.NoteInput{GUID: "compact-a", Title: "A", Body: strp("a2")}, compactUserGUID); err != nil {
		t.Fatalf("failed to update first note: %v", err)
	}
	if _, err := models.UpdateNote(second.ID, models.NoteInput{GUID: "compact-b", Title: "B", Body: strp("b2")}, compactUserGUID); err != nil {
		t.Fatalf("failed to update second note: %v", err)
	}

	res, err := models.CompactPendingChanges("hub", "")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if res.NotesCompacted != 2 {
		t.Errorf("NotesCompacted = %d, want 2", res.NotesCompacted)
	}
	if res.ChangesAfter != 2 {
		t.Errorf("ChangesAfter = %d, want 2 (one per note)", res.ChangesAfter)
	}

	pull, err := models.GetUnifiedChangesForPeer("hub", "", 100)
	if err != nil {
		t.Fatalf("failed to read the compacted stream: %v", err)
	}
	bodies := map[string]string{}
	for _, ch := range pull.Changes {
		fragment, ok := ch.Fragment.(*models.NoteFragmentOutput)
		if !ok || fragment.Body == nil {
			t.Fatalf("change for %s has no body fragment", ch.EntityGUID)
		}
		bodies[ch.EntityGUID] = *fragment.Body
	}
	if bodies["compact-a"] != "a2" || bodies["compact-b"] != "b2" {
		t.Errorf("compacted bodies = %v, want a2 / b2", bodies)
	}
}

// TestCompactSkipsChangesAlreadySent is the safety property that makes
// compaction per-peer rather than global: a change the hub already has must
// not be rewritten into a new one and sent again.
func TestCompactSkipsChangesAlreadySent(t *testing.T) {
	setupCompactTestDB(t)

	note, err := models.CreateNote(models.NoteInput{GUID: "compact-sent", Title: "Sent", Body: strp("v1")}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Simulate a completed push: everything so far has reached the hub.
	sent, err := models.GetUnifiedChangesForPeer("hub", "", 100)
	if err != nil {
		t.Fatalf("failed to read changes for the simulated push: %v", err)
	}
	models.MarkSyncChangesForPeer(sent.Changes, "hub")

	// Two more edits arrive afterwards.
	for _, body := range []string{"v2", "v3"} {
		if _, err := models.UpdateNote(note.ID, models.NoteInput{
			GUID: "compact-sent", Title: "Sent", Body: strp(body),
		}, compactUserGUID); err != nil {
			t.Fatalf("failed to update note: %v", err)
		}
	}

	res, err := models.CompactPendingChanges("hub", "")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if res.ChangesAfter != 1 {
		t.Errorf("ChangesAfter = %d, want 1", res.ChangesAfter)
	}

	pull, err := models.GetUnifiedChangesForPeer("hub", "", 100)
	if err != nil {
		t.Fatalf("failed to read the compacted stream: %v", err)
	}
	if len(pull.Changes) != 1 {
		t.Fatalf("expected 1 pending change, got %d", len(pull.Changes))
	}
	// An UPDATE, not a create: the create was already delivered, so the
	// compacted stand-in for the two edits must not claim the note is new.
	if got := pull.Changes[0].Operation; got != models.OperationUpdate {
		t.Errorf("compacted operation = %d, want update (%d)", got, models.OperationUpdate)
	}
	fragment, ok := pull.Changes[0].Fragment.(*models.NoteFragmentOutput)
	if !ok || fragment.Body == nil || *fragment.Body != "v3" {
		t.Errorf("compacted body = %v, want v3", fragment)
	}
}

// TestCompactPreservesCategoryLinks covers the field that is a snapshot rather
// than a value. A note's category mappings live in a fragment of their own; a
// compaction that rebuilt the note without them would silently unfile it on
// the hub.
func TestCompactPreservesCategoryLinks(t *testing.T) {
	setupCompactTestDB(t)

	note, err := models.CreateNote(models.NoteInput{GUID: "compact-filed", Title: "Filed"}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}
	category, err := models.CreateCategory(models.CategoryInput{Name: "Work"}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}
	if err := models.AddCategoryToNote(note.ID, category.ID, compactUserGUID); err != nil {
		t.Fatalf("failed to link category: %v", err)
	}
	if _, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID: "compact-filed", Title: "Filed", Body: strp("body"),
	}, compactUserGUID); err != nil {
		t.Fatalf("failed to update note: %v", err)
	}

	if _, err := models.CompactPendingChanges("hub", ""); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}

	pull, err := models.GetUnifiedChangesForPeer("hub", "", 100)
	if err != nil {
		t.Fatalf("failed to read the compacted stream: %v", err)
	}

	// Ordering matters as much as content here: the hub applies the stream in
	// order, and a note mapping that arrives before the category it names has
	// nothing to resolve against. A compacted change inherits the timestamp of
	// the change it replaced, so the order the group already had survives.
	var noteChange *models.SyncChange
	var sawCategory bool
	for i := range pull.Changes {
		if pull.Changes[i].EntityType == "category" {
			sawCategory = true
		}
		if pull.Changes[i].EntityType == "note" && noteChange == nil {
			if !sawCategory {
				t.Error("the note change sorts before the category it references")
			}
			noteChange = &pull.Changes[i]
		}
	}
	if noteChange == nil {
		t.Fatal("no note change survived compaction")
	}
	fragment, ok := noteChange.Fragment.(*models.NoteFragmentOutput)
	if !ok {
		t.Fatalf("fragment has type %T, want *models.NoteFragmentOutput", noteChange.Fragment)
	}
	if fragment.Categories == nil {
		t.Fatal("the compacted fragment dropped the note's category mappings")
	}
	if !strings.Contains(*fragment.Categories, category.GUID) {
		t.Errorf("category snapshot %q does not name category %s", *fragment.Categories, category.GUID)
	}
}

// TestCompactRequiresAPeer guards the argument that decides WHICH changes are
// pending. An empty peer id would compact against "sent to nobody", which is
// every change ever recorded.
func TestCompactRequiresAPeer(t *testing.T) {
	setupCompactTestDB(t)

	if _, err := models.CompactPendingChanges("", ""); err == nil {
		t.Error("compaction with an empty peer id succeeded; it must not")
	}
}

// TestCountUnsentChangesExcludesSyncOperations pins the exclusion the counter
// and the compactor share: rows recorded while APPLYING a peer's change are
// provenance, not work owed to anyone.
func TestCountUnsentChangesExcludesSyncOperations(t *testing.T) {
	setupCompactTestDB(t)

	// A note that arrives BY sync records an OperationSync row locally.
	fragment := models.NoteFragment{
		Bitmask: models.FragmentTitle | models.FragmentBody,
	}
	fragment.Title.String, fragment.Title.Valid = "From the hub", true
	fragment.Body.String, fragment.Body.Valid = "arrived", true

	if _, err := models.ApplySyncNoteCreate("compact-incoming", "From the hub", fragment,
		time.Now(), compactUserGUID, "incoming-change-guid"); err != nil {
		t.Fatalf("failed to apply an incoming note: %v", err)
	}

	count, err := models.CountUnsentChangesForPeer("hub", "")
	if err != nil {
		t.Fatalf("failed to count pending changes: %v", err)
	}
	if count != 0 {
		t.Errorf("pending count = %d, want 0 — a synced-in change is owed to nobody", count)
	}
}
