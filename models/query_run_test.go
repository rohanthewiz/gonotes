package models_test

import (
	"strings"
	"testing"
	"time"

	"gonotes/models"
)

// query_run_test.go covers the advanced search against real storage: the
// two-database fan-out, the category link join that no single SQL statement
// could do, paging, and the data-driven half of the autocompleter.
//
// The language's own semantics are pinned without a database in
// models/query_test.go; what is checked here is that the plumbing feeds the
// evaluator the right rows.

const queryUser = "query-test-user-guid"
const otherUser = "query-test-other-user"

type seededNote struct {
	note *models.Note
	cats map[string][]string // category name → subcategories
}

func setupQueryDB(t *testing.T) func() {
	t.Helper()
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}
	return func() { models.CloseDB() }
}

func seedNote(t *testing.T, owner, guid, title string, in models.NoteInput, cats map[string][]string) *models.Note {
	t.Helper()
	in.GUID = guid
	in.Title = title
	note, err := models.CreateNote(in, owner)
	if err != nil {
		t.Fatalf("creating note %q failed: %v", guid, err)
	}
	for name, subs := range cats {
		cat, err := models.GetCategoryByName(name, owner)
		if err != nil {
			t.Fatalf("looking up category %q failed: %v", name, err)
		}
		if cat == nil {
			cat, err = models.CreateCategory(models.CategoryInput{Name: name, Subcategories: subs}, owner)
			if err != nil {
				t.Fatalf("creating category %q failed: %v", name, err)
			}
		}
		if err := models.AddCategoryToNoteWithSubcategories(note.ID, cat.ID, subs, owner); err != nil {
			t.Fatalf("linking category %q to note %q failed: %v", name, guid, err)
		}
	}
	return note
}

func str(s string) *string { return &s }

// seedLibrary builds a small but deliberately awkward library: notes in both
// databases, one owned by somebody else, one soft-deleted, categories shared
// across notes and a subcategory name that appears under two categories.
func seedLibrary(t *testing.T) {
	t.Helper()

	seedNote(t, queryUser, "q-1", "Airflow DAG conversion",
		models.NoteInput{Body: str("convert the legacy DAGs"), Tags: str("airflow,migration"), IsFlagged: true},
		map[string][]string{"airflow": {"conversion"}})

	seedNote(t, queryUser, "q-2", "Airflow scheduling notes",
		models.NoteInput{Body: str("schedules and sensors"), Tags: str("airflow")},
		map[string][]string{"airflow": {"scheduling"}})

	// A PRIVATE note — it lives in the other database entirely, so finding it
	// is what proves the query fans out across both.
	seedNote(t, queryUser, "q-3", "Secret conversion plan",
		models.NoteInput{Body: str("private conversion details"), IsPrivate: true, Tags: str("migration")},
		map[string][]string{"airflow": {"conversion"}})

	seedNote(t, queryUser, "q-4", "Grocery list",
		models.NoteInput{Body: str("milk, eggs"), Tags: str("home")},
		map[string][]string{"Home": {"errands"}})

	// Someone else's note, matching every term of the queries below.
	seedNote(t, otherUser, "q-5", "Airflow DAG conversion",
		models.NoteInput{Tags: str("airflow,migration")}, nil)

	// A soft-deleted note that would otherwise match.
	del := seedNote(t, queryUser, "q-6", "Airflow conversion (old)",
		models.NoteInput{Tags: str("airflow")}, map[string][]string{"airflow": {"conversion"}})
	if _, err := models.DeleteNote(del.ID, queryUser); err != nil {
		t.Fatalf("soft-deleting the note failed: %v", err)
	}
}

func run(t *testing.T, src string) *models.QueryResult {
	t.Helper()
	res, err := models.QueryNotes(src, queryUser, models.QueryOptions{})
	if err != nil {
		t.Fatalf("QueryNotes(%q) failed: %v", src, err)
	}
	return res
}

func guids(res *models.QueryResult) []string {
	out := make([]string, len(res.Notes))
	for i, n := range res.Notes {
		out[i] = n.GUID
	}
	return out
}

func wantGUIDs(t *testing.T, res *models.QueryResult, want ...string) {
	t.Helper()
	got := guids(res)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// The query from the original request, run against real rows in two databases.
func TestQueryByCategoryAndSubcategory(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	res := run(t, "category = 'airflow' and subcategory = 'conversion'")
	wantGUIDs(t, res, "q-1", "q-3")

	if res.Matched != 2 {
		t.Errorf("Matched is %d, want 2", res.Matched)
	}
	// Four live notes are owned by this user; the deleted one and the other
	// user's are not candidates at all.
	if res.Scanned != 4 {
		t.Errorf("Scanned is %d, want the 4 live notes this user owns", res.Scanned)
	}
}

// A private note lives in the encrypted database and a public one does not.
// Any query that does not say otherwise spans both.
func TestQuerySpansBothDatabases(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	all := run(t, "tags = 'migration'")
	wantGUIDs(t, all, "q-1", "q-3")

	priv := run(t, "is_private AND category = 'airflow'")
	wantGUIDs(t, priv, "q-3")

	pub := run(t, "category = 'airflow' AND NOT is_private")
	wantGUIDs(t, pub, "q-1", "q-2")
}

// Every read in this codebase is user-scoped; the query language is not an
// exception, and the note seeded under another user proves it.
func TestQueryIsScopedToTheUser(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	res := run(t, "title = 'Airflow DAG conversion'")
	wantGUIDs(t, res, "q-1")

	other, err := models.QueryNotes("title = 'Airflow DAG conversion'", otherUser, models.QueryOptions{})
	if err != nil {
		t.Fatalf("query as the other user failed: %v", err)
	}
	wantGUIDs(t, other, "q-5")
}

func TestDeletedNotesNeedAnOptIn(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	live := run(t, "category = 'airflow'")
	for _, g := range guids(live) {
		if g == "q-6" {
			t.Fatal("a soft-deleted note came back from a query that never mentioned deleted_at")
		}
	}

	// Naming the field is the opt-in.
	gone := run(t, "deleted_at IS NOT NULL")
	wantGUIDs(t, gone, "q-6")

	// The category links of a deleted note are loaded too, or this query
	// could never match anything.
	both := run(t, "deleted_at IS NOT NULL AND category = 'airflow'")
	wantGUIDs(t, both, "q-6")

	// And the option does the same without the text saying so.
	opt, err := models.QueryNotes("category = 'airflow'", queryUser,
		models.QueryOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("IncludeDeleted query failed: %v", err)
	}
	if len(opt.Notes) != 4 {
		t.Fatalf("with IncludeDeleted the airflow category holds %d notes, want 4", len(opt.Notes))
	}
}

func TestFreeTextSpansTitleBodyTagsAndCategories(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	wantGUIDs(t, run(t, "'sensors'"), "q-2") // body only
	wantGUIDs(t, run(t, "'errands'"), "q-4") // a subcategory only
	wantGUIDs(t, run(t, "'grocery'"), "q-4") // title
	wantGUIDs(t, run(t, "'home'"), "q-4")    // both a tag and a category
	wantGUIDs(t, run(t, "'conversion' AND is_private"), "q-3")
}

func TestOrderingAndPaging(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	asc := run(t, "category = 'airflow' ORDER BY title ASC")
	got := guids(asc)
	if len(got) < 3 || got[0] != "q-1" {
		t.Fatalf("ORDER BY title ASC gave %v; 'Airflow DAG conversion' should lead", got)
	}

	// LIMIT pages the result but must not change the reported match count —
	// the UI shows "N results" from Matched, not from the page it got.
	page := run(t, "category = 'airflow' ORDER BY title ASC LIMIT 1 OFFSET 1")
	if len(page.Notes) != 1 {
		t.Fatalf("LIMIT 1 returned %d notes", len(page.Notes))
	}
	if page.Matched != 3 {
		t.Fatalf("Matched is %d under a LIMIT, want the unpaged count 3", page.Matched)
	}
	if page.Notes[0].GUID == got[0] {
		t.Fatal("OFFSET 1 returned the first note again")
	}

	// An option overrides the text, so an API caller's paging wins over a
	// LIMIT somebody left in the box.
	over, err := models.QueryNotes("category = 'airflow' LIMIT 1", queryUser,
		models.QueryOptions{Limit: 3, SortField: "title", SortDesc: false})
	if err != nil {
		t.Fatalf("option override query failed: %v", err)
	}
	if len(over.Notes) != 3 {
		t.Fatalf("QueryOptions.Limit did not override the text's LIMIT: got %d notes", len(over.Notes))
	}
}

func TestRelativeTimeQueriesSeeFreshNotes(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	// Everything was created moments ago. This is also the end-to-end check
	// that stored timestamps and time.Now() are on the same clock — if the
	// storage layer ever handed back a naive local time labelled UTC, this is
	// the test that would notice.
	res := run(t, "created_at > -1h")
	if len(res.Notes) < 4 {
		t.Fatalf("only %d notes were created in the last hour; every seeded note should be", len(res.Notes))
	}
	old := run(t, "created_at < -1h")
	if len(old.Notes) != 0 {
		t.Fatalf("%d notes claim to be older than an hour", len(old.Notes))
	}
	today := run(t, "created_at >= today")
	if len(today.Notes) < 4 {
		t.Fatalf("only %d notes were created today", len(today.Notes))
	}
}

func TestQueryErrorsAreDistinguishableFromStorageErrors(t *testing.T) {
	defer setupQueryDB(t)()

	_, err := models.QueryNotes("catgory = 'x'", queryUser, models.QueryOptions{})
	if err == nil {
		t.Fatal("a bad field name should not run")
	}
	if _, ok := err.(*models.QueryError); !ok {
		t.Fatalf("got %T, want *models.QueryError so the API can answer 400", err)
	}
}

// The half of the completer that only a database can answer.
func TestCompletionOffersRealValues(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	has := func(c *models.QueryCompletion, label string) bool {
		for _, s := range c.Suggestions {
			if s.Label == label {
				return true
			}
		}
		return false
	}
	dump := func(c *models.QueryCompletion) string {
		var b strings.Builder
		for _, s := range c.Suggestions {
			b.WriteString(s.Label + " ")
		}
		return b.String()
	}

	src := "category = "
	cats := models.CompleteQuery(src, len(src), queryUser)
	if !has(cats, "airflow") || !has(cats, "Home") {
		t.Errorf("category completion offered [%s], want the user's real categories", dump(cats))
	}

	src = "subcategory = "
	subs := models.CompleteQuery(src, len(src), queryUser)
	for _, want := range []string{"conversion", "scheduling", "errands"} {
		if !has(subs, want) {
			t.Errorf("subcategory completion offered [%s], missing %q", dump(subs), want)
		}
	}

	src = "tags = "
	tags := models.CompleteQuery(src, len(src), queryUser)
	if !has(tags, "airflow") || !has(tags, "migration") {
		t.Errorf("tag completion offered [%s], want tags drawn from the notes", dump(tags))
	}
	// Most-used first: airflow is on three notes, home on one.
	if len(tags.Suggestions) > 0 && tags.Suggestions[0].Label != "airflow" {
		t.Errorf("tag completion leads with %q, want the most used tag", tags.Suggestions[0].Label)
	}

	// Values are quoted on insertion, so accepting one leaves a valid query.
	for _, s := range cats.Suggestions {
		if !strings.HasPrefix(s.Text, "'") {
			t.Errorf("category value %q should be inserted quoted", s.Text)
		}
	}

	// A completion is scoped to the user like everything else.
	empty := models.CompleteQuery("category = ", len("category = "), otherUser)
	if has(empty, "airflow") {
		t.Error("another user's categories leaked into a completion")
	}
}

func TestCompletionValuesFilterOnWhatIsTyped(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	src := "category = 'ai"
	c := models.CompleteQuery(src, len(src), queryUser)
	if len(c.Suggestions) == 0 {
		t.Fatal("a partially typed value offered nothing")
	}
	for _, s := range c.Suggestions {
		if !strings.Contains(strings.ToLower(s.Label), "ai") {
			t.Errorf("suggestion %q does not match what was typed", s.Label)
		}
	}
	// Accepting it must produce text that parses.
	spliced := src[:c.ReplaceStart] + c.Suggestions[0].Text
	if _, err := models.ParseQuery(strings.TrimSpace(spliced)); err != nil {
		t.Fatalf("accepting %q gave unparseable text %q: %v", c.Suggestions[0].Text, spliced, err)
	}
}

func TestQueryResultTimingIsRecorded(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	res := run(t, "is_flagged")
	if res.Elapsed <= 0 || res.Elapsed > 10*time.Second {
		t.Fatalf("Elapsed is %v, which cannot be right", res.Elapsed)
	}
	if res.Query == nil || res.Query.String() != "is_flagged" {
		t.Fatalf("the result should carry the parsed query back")
	}
}
