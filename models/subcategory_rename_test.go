package models_test

import (
	"slices"
	"testing"

	"gonotes/models"
)

// TestRenameSubcategory renames a subcategory that two notes (one public, one
// private) are filed under, and checks the definition, both notes' selections,
// an untouched note, and the sync log.
func TestRenameSubcategory(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	cat, err := models.CreateCategory(models.CategoryInput{
		Name: "Work", Subcategories: []string{"backend", "api", "rapid"},
	}, ncTestUserGUID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	mk := func(guid string, private bool, subs ...string) int64 {
		t.Helper()
		n, err := models.CreateNote(models.NoteInput{GUID: guid, Title: guid, IsPrivate: private}, ncTestUserGUID)
		if err != nil {
			t.Fatalf("create note: %v", err)
		}
		if err := models.AddCategoryToNoteWithSubcategories(n.ID, cat.ID, subs, ncTestUserGUID); err != nil {
			t.Fatalf("link: %v", err)
		}
		return n.ID
	}
	pub := mk("rename-pub", false, "api", "backend")
	priv := mk("rename-priv", true, "api")
	other := mk("rename-other", false, "rapid")

	before, _ := models.GetUnsentChangesForPeer("peer-rename", "", 1000)
	catBefore, _ := models.GetUnsentCategoryChangesForPeer("peer-rename", "", 1000)

	updated, n, err := models.RenameSubcategory(cat.ID, "api", "http", ncTestUserGUID)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if n != 2 {
		t.Errorf("rewrote %d notes, want 2", n)
	}
	if got := updated.SubcategoryList(); !slices.Equal(got, []string{"backend", "http", "rapid"}) {
		t.Errorf("definition = %v, want [backend http rapid] (order kept)", got)
	}

	selected := func(noteID int64) []string {
		t.Helper()
		details, err := models.GetNoteCategoryDetails(noteID, ncTestUserGUID)
		if err != nil || len(details) != 1 {
			t.Fatalf("note %d details: %v (%d links)", noteID, err, len(details))
		}
		return details[0].SelectedSubcategories
	}
	if got := selected(pub); !slices.Equal(got, []string{"http", "backend"}) {
		t.Errorf("public note selection = %v, want [http backend]", got)
	}
	if got := selected(priv); !slices.Equal(got, []string{"http"}) {
		t.Errorf("private note selection = %v, want [http]", got)
	}
	if got := selected(other); !slices.Equal(got, []string{"rapid"}) {
		t.Errorf("unrelated note selection = %v, want [rapid] (no substring match)", got)
	}

	// One note mapping change per rewritten note, and one category change.
	after, _ := models.GetUnsentChangesForPeer("peer-rename", "", 1000)
	if got := len(after) - len(before); got != 2 {
		t.Errorf("rename recorded %d note changes, want 2", got)
	}
	catAfter, _ := models.GetUnsentCategoryChangesForPeer("peer-rename", "", 1000)
	if got := len(catAfter) - len(catBefore); got != 1 {
		t.Errorf("rename recorded %d category changes, want 1", got)
	}
}

// Renaming onto an existing subcategory merges the two, and a note filed
// under both ends up with the name once.
func TestRenameSubcategoryMerges(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	cat, err := models.CreateCategory(models.CategoryInput{
		Name: "Work", Subcategories: []string{"api", "http"},
	}, ncTestUserGUID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	note, _ := models.CreateNote(models.NoteInput{GUID: "merge", Title: "merge"}, ncTestUserGUID)
	if err := models.AddCategoryToNoteWithSubcategories(note.ID, cat.ID, []string{"api", "http"}, ncTestUserGUID); err != nil {
		t.Fatalf("link: %v", err)
	}

	updated, _, err := models.RenameSubcategory(cat.ID, "api", "http", ncTestUserGUID)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := updated.SubcategoryList(); !slices.Equal(got, []string{"http"}) {
		t.Errorf("definition = %v, want [http]", got)
	}
	details, _ := models.GetNoteCategoryDetails(note.ID, ncTestUserGUID)
	if got := details[0].SelectedSubcategories; !slices.Equal(got, []string{"http"}) {
		t.Errorf("selection = %v, want [http] once", got)
	}
}

func TestRenameSubcategoryRefusals(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	cat, _ := models.CreateCategory(models.CategoryInput{Name: "Work", Subcategories: []string{"api"}}, ncTestUserGUID)
	for _, c := range []struct{ from, to, user string }{
		{"api", "", ncTestUserGUID},
		{"api", "api", ncTestUserGUID},
		{"api", "a/b", ncTestUserGUID},
		{"api", "a,b", ncTestUserGUID},
		{"missing", "x", ncTestUserGUID},
		{"api", "x", "someone-else"},
	} {
		if _, _, err := models.RenameSubcategory(cat.ID, c.from, c.to, c.user); err == nil {
			t.Errorf("rename %q → %q as %q succeeded, want a refusal", c.from, c.to, c.user)
		}
	}
}
