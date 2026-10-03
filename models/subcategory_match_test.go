package models_test

import (
	"slices"
	"sort"
	"testing"

	"gonotes/models"
)

// TestSubcategoryMatchAllVsAny files four notes (one private, so both
// databases are read) under different subcategory sets and checks that AND
// and OR each return exactly the notes their rule names, on a pair of
// subcategories where the two rules disagree.
func TestSubcategoryMatchAllVsAny(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	cat, err := models.CreateCategory(models.CategoryInput{
		Name: "Work", Subcategories: []string{"backend", "api", "ops"},
	}, ncTestUserGUID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	mk := func(guid string, private bool, subs ...string) {
		t.Helper()
		n, err := models.CreateNote(models.NoteInput{GUID: guid, Title: guid, IsPrivate: private}, ncTestUserGUID)
		if err != nil {
			t.Fatalf("create note: %v", err)
		}
		if err := models.AddCategoryToNoteWithSubcategories(n.ID, cat.ID, subs, ncTestUserGUID); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	mk("both", false, "backend", "api")
	mk("backend-only", true, "backend")
	mk("api-only", false, "api")
	mk("ops-only", false, "ops")

	titles := func(notes []models.Note, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		out := make([]string, 0, len(notes))
		for _, n := range notes {
			out = append(out, n.Title)
		}
		sort.Strings(out)
		return out
	}
	pair := []string{"backend", "api"}

	if got := titles(models.GetNotesByCategoryAndSubcategories("Work", pair, ncTestUserGUID)); !slices.Equal(got, []string{"both"}) {
		t.Errorf("AND = %v, want [both]", got)
	}
	want := []string{"api-only", "backend-only", "both"}
	if got := titles(models.GetNotesByCategoryAndAnySubcategory("Work", pair, ncTestUserGUID)); !slices.Equal(got, want) {
		t.Errorf("OR = %v, want %v", got, want)
	}

	// The value-carrying form agrees with both named wrappers.
	if got := titles(models.GetNotesByCategorySubcategoryMatch("Work", pair, models.MatchAnySubcategory, ncTestUserGUID)); !slices.Equal(got, want) {
		t.Errorf("match=any = %v, want %v", got, want)
	}

	// No subcategories is the whole category in either mode, not "nothing".
	all := []string{"api-only", "backend-only", "both", "ops-only"}
	for _, m := range []models.SubcategoryMatch{models.MatchAllSubcategories, models.MatchAnySubcategory} {
		if got := titles(models.GetNotesByCategorySubcategoryMatch("Work", nil, m, ncTestUserGUID)); !slices.Equal(got, all) {
			t.Errorf("empty filter, match=%s = %v, want %v", m, got, all)
		}
	}
}

// TestParseSubcategoryMatch pins the wire spelling, including the fail-safe:
// anything that is not exactly "any" means AND.
func TestParseSubcategoryMatch(t *testing.T) {
	cases := map[string]models.SubcategoryMatch{
		"any": models.MatchAnySubcategory,
		"all": models.MatchAllSubcategories,
		"":    models.MatchAllSubcategories,
		"ANY": models.MatchAllSubcategories,
		"or":  models.MatchAllSubcategories,
	}
	for in, want := range cases {
		if got := models.ParseSubcategoryMatch(in); got != want {
			t.Errorf("ParseSubcategoryMatch(%q) = %v, want %v", in, got, want)
		}
	}
	for _, m := range []models.SubcategoryMatch{models.MatchAllSubcategories, models.MatchAnySubcategory} {
		if got := models.ParseSubcategoryMatch(m.String()); got != m {
			t.Errorf("round trip of %v gave %v", m, got)
		}
	}
}
