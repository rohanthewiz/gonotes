package models_test

import (
	"testing"

	"gonotes/models"
)

// TestSetNoteCategoriesChangeLog checks the sync-log half of SetNoteCategories:
// however many links one call adds, updates or removes, it records one
// mapping change, and a call that matches what is stored records none.
// That is what separates it from looping over the per-link functions, which
// write one change per link.
func TestSetNoteCategoriesChangeLog(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	note, err := models.CreateNote(models.NoteInput{GUID: "set-cats-log", Title: "Set cats"}, ncTestUserGUID)
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	var ids []int64
	for _, name := range []string{"One", "Two", "Three"} {
		cat, err := models.CreateCategory(models.CategoryInput{Name: name, Subcategories: []string{"x", "y"}}, ncTestUserGUID)
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		ids = append(ids, cat.ID)
	}

	changeCount := func() int {
		t.Helper()
		changes, err := models.GetUnsentChangesForPeer("peer-set", "", 1000)
		if err != nil {
			t.Fatalf("unsent changes: %v", err)
		}
		return len(changes)
	}

	all := []models.NoteCategoryAssignment{
		{CategoryID: ids[0], Subcategories: []string{"x"}},
		{CategoryID: ids[1]},
		{CategoryID: ids[2], Subcategories: []string{"y", "x"}},
	}

	before := changeCount()
	changed, err := models.SetNoteCategories(note.ID, all, ncTestUserGUID)
	if err != nil || !changed {
		t.Fatalf("first set: changed=%v err=%v", changed, err)
	}
	if got := changeCount() - before; got != 1 {
		t.Errorf("three new links recorded %d changes, want 1", got)
	}

	// Same set again, with one selection reordered: nothing to write.
	before = changeCount()
	all[2].Subcategories = []string{"x", "y"}
	changed, err = models.SetNoteCategories(note.ID, all, ncTestUserGUID)
	if err != nil || changed {
		t.Fatalf("identical set: changed=%v err=%v", changed, err)
	}
	if got := changeCount() - before; got != 0 {
		t.Errorf("identical set recorded %d changes, want 0", got)
	}

	// A duplicate entry is merged, and the later selection wins.
	_, err = models.SetNoteCategories(note.ID, []models.NoteCategoryAssignment{
		{CategoryID: ids[0], Subcategories: []string{"x"}},
		{CategoryID: ids[0], Subcategories: []string{"y"}},
	}, ncTestUserGUID)
	if err != nil {
		t.Fatalf("duplicate set: %v", err)
	}
	details, err := models.GetNoteCategoryDetails(note.ID, ncTestUserGUID)
	if err != nil {
		t.Fatalf("details: %v", err)
	}
	if len(details) != 1 || !models.SameSubcategories(details[0].SelectedSubcategories, []string{"y"}) {
		t.Errorf("after duplicate set got %+v, want only %d with [y]", details, ids[0])
	}

	// Another user's note is invisible, not writable.
	if _, err := models.SetNoteCategories(note.ID, nil, "someone-else"); err == nil {
		t.Error("expected note not found for a different user")
	}
}
