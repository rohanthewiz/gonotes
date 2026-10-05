package models_test

import (
	"errors"
	"testing"

	"gonotes/models"
)

// One-note compaction (N-022): the same rewrite as the whole-log pass,
// applied to one note and nothing else. As in sync_compact_test.go, each test
// checks both what shrank and what survived.

// pendingFor returns the change GUIDs and operations pending to peer for one
// entity (note or category), in stream order.
func pendingFor(t *testing.T, peer, entityGUID string) []models.SyncChange {
	t.Helper()
	pull, err := models.GetUnifiedChangesForPeer(peer, "", 1000)
	if err != nil {
		t.Fatalf("failed to read the pending stream: %v", err)
	}
	var out []models.SyncChange
	for _, c := range pull.Changes {
		if c.EntityGUID == entityGUID {
			out = append(out, c)
		}
	}
	return out
}

// editNote creates a note and updates it n times, giving n+1 pending changes.
func editNote(t *testing.T, guid string, n int) *models.Note {
	t.Helper()
	note, err := models.CreateNote(models.NoteInput{GUID: guid, Title: guid, Body: strp("v0")}, compactUserGUID)
	if err != nil {
		t.Fatalf("failed to create %s: %v", guid, err)
	}
	for i := 1; i <= n; i++ {
		body := "v" + string(rune('0'+i))
		if _, err := models.UpdateNote(note.ID, models.NoteInput{GUID: guid, Title: guid, Body: &body},
			compactUserGUID); err != nil {
			t.Fatalf("failed to update %s: %v", guid, err)
		}
	}
	return note
}

// The central property: the named note collapses, and every other note and
// every category keeps its pending changes exactly as they were.
func TestCompactOneNoteLeavesTheRestAlone(t *testing.T) {
	setupCompactTestDB(t)

	editNote(t, "one-target", 3) // 4 pending
	editNote(t, "one-other", 2)  // 3 pending
	cat, err := models.CreateCategory(models.CategoryInput{Name: "OneCat"}, compactUserGUID)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := models.UpdateCategory(cat.ID, models.CategoryInput{Name: "OneCat2"}, compactUserGUID); err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	otherBefore := pendingFor(t, "hub", "one-other")
	catBefore := pendingFor(t, "hub", cat.GUID)

	preview, err := models.PreviewNoteCompaction("hub", compactUserGUID, "one-target")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(pendingFor(t, "hub", "one-target")) != 4 {
		t.Fatal("the preview wrote something")
	}

	res, err := models.CompactNotePendingChanges("hub", compactUserGUID, "one-target")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if *res != *preview {
		t.Errorf("preview %+v differs from the real result %+v", *preview, *res)
	}
	want := models.CompactionResult{NotesCompacted: 1, ChangesBefore: 4, ChangesAfter: 1}
	if *res != want {
		t.Errorf("result = %+v, want %+v", *res, want)
	}

	// The target is one create carrying its final body.
	target := pendingFor(t, "hub", "one-target")
	if len(target) != 1 || target[0].Operation != models.OperationCreate {
		t.Fatalf("target now has %+v, want one create", target)
	}
	if f, ok := target[0].Fragment.(*models.NoteFragmentOutput); !ok || f.Body == nil || *f.Body != "v3" {
		t.Errorf("target's fragment = %+v, want body %q", target[0].Fragment, "v3")
	}

	// Everything else is untouched, change for change.
	for name, pair := range map[string][2][]models.SyncChange{
		"the other note": {otherBefore, pendingFor(t, "hub", "one-other")},
		"the category":   {catBefore, pendingFor(t, "hub", cat.GUID)},
	} {
		before, after := pair[0], pair[1]
		if len(before) != len(after) {
			t.Errorf("%s went from %d pending changes to %d", name, len(before), len(after))
			continue
		}
		for i := range before {
			if before[i].GUID != after[i].GUID {
				t.Errorf("%s: change %d is now %s, was %s", name, i, after[i].GUID, before[i].GUID)
			}
		}
	}
}

// A note whose pending tail ends in a delete is still found (the row is
// soft-deleted, not gone) and collapses to one delete.
func TestCompactOneDeletedNote(t *testing.T) {
	setupCompactTestDB(t)
	note := editNote(t, "one-doomed", 1)
	if _, err := models.DeleteNote(note.ID, compactUserGUID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}

	res, err := models.CompactNotePendingChanges("hub", compactUserGUID, "one-doomed")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if res.ChangesBefore != 3 || res.ChangesAfter != 1 {
		t.Errorf("result = %+v, want 3 → 1", *res)
	}
	if got := pendingFor(t, "hub", "one-doomed"); len(got) != 1 || got[0].Operation != models.OperationDelete {
		t.Errorf("pending = %+v, want one delete", got)
	}
}

// One pending change is already compact: reported, not rewritten.
func TestCompactOneNoteWithASingleChange(t *testing.T) {
	setupCompactTestDB(t)
	editNote(t, "one-single", 0)
	before := pendingFor(t, "hub", "one-single")

	res, err := models.CompactNotePendingChanges("hub", compactUserGUID, "one-single")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if want := (models.CompactionResult{ChangesBefore: 1, ChangesAfter: 1}); *res != want {
		t.Errorf("result = %+v, want %+v", *res, want)
	}
	if after := pendingFor(t, "hub", "one-single"); len(after) != 1 || after[0].GUID != before[0].GUID {
		t.Error("a lone change was rewritten")
	}
}

// An unknown GUID and someone else's note get the same answer, and nothing
// is written for either.
func TestCompactOneNoteRefusesUnknownAndForeignNotes(t *testing.T) {
	setupCompactTestDB(t)
	editNote(t, "one-mine", 2)

	for name, call := range map[string]func() (*models.CompactionResult, error){
		"unknown, compact": func() (*models.CompactionResult, error) {
			return models.CompactNotePendingChanges("hub", compactUserGUID, "no-such-note")
		},
		"unknown, preview": func() (*models.CompactionResult, error) {
			return models.PreviewNoteCompaction("hub", compactUserGUID, "no-such-note")
		},
		"foreign, compact": func() (*models.CompactionResult, error) {
			return models.CompactNotePendingChanges("hub", "someone-else", "one-mine")
		},
		"foreign, preview": func() (*models.CompactionResult, error) {
			return models.PreviewNoteCompaction("hub", "someone-else", "one-mine")
		},
	} {
		if _, err := call(); !errors.Is(err, models.ErrCompactNoteNotFound) {
			t.Errorf("%s: err = %v, want ErrCompactNoteNotFound", name, err)
		}
	}
	if got := len(pendingFor(t, "hub", "one-mine")); got != 3 {
		t.Errorf("a refused call wrote something: %d pending, want 3", got)
	}
}
