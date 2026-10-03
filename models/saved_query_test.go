package models_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gonotes/models"
)

// saved_query_test.go covers the server-side saved queries and history, and
// the two ways the completer surfaces data the query bar used to lack: those
// stored queries, and notes-by-title for a GUID value.

func TestSaveNamedQueryUpsertsByNameIgnoringCase(t *testing.T) {
	defer setupQueryDB(t)()

	first, err := models.SaveNamedQuery(queryUser, "Airflow", "category = 'airflow'")
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	second, err := models.SaveNamedQuery(queryUser, "airflow", "category = 'airflow' AND is_flagged = true")
	if err != nil {
		t.Fatalf("re-save failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("saving under the same name (other case) should update row %d, got new row %d", first.ID, second.ID)
	}

	list, err := models.ListSavedQueries(queryUser)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list.Saved) != 1 {
		t.Fatalf("want 1 saved query, got %d: %+v", len(list.Saved), list.Saved)
	}
	got := list.Saved[0]
	if got.Name != "airflow" || got.Query != "category = 'airflow' AND is_flagged = true" {
		t.Errorf("the newer name spelling and text should win, got %+v", got)
	}
}

func TestSaveNamedQueryRejectsWhatCannotRun(t *testing.T) {
	defer setupQueryDB(t)()

	_, err := models.SaveNamedQuery(queryUser, "broken", "category = ")
	var qe *models.QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("an unparseable query should come back as a positioned *QueryError, got %v", err)
	}

	for _, tc := range []struct{ name, query string }{
		{"", "is_flagged = true"},
		{"   ", "is_flagged = true"},
		{"empty", "  "},
		{strings.Repeat("n", 81), "is_flagged = true"},
	} {
		if _, err := models.SaveNamedQuery(queryUser, tc.name, tc.query); err == nil {
			t.Errorf("SaveNamedQuery(%q, %q) should fail", tc.name, tc.query)
		}
	}
}

func TestQueryHistoryDedupesAndCaps(t *testing.T) {
	defer setupQueryDB(t)()

	for i := 0; i < 30; i++ {
		if err := models.RecordQueryHistory(queryUser, fmt.Sprintf("id = %d", i)); err != nil {
			t.Fatalf("record %d failed: %v", i, err)
		}
	}
	// Re-running an old entry moves it to the top rather than duplicating it.
	if err := models.RecordQueryHistory(queryUser, "id = 10"); err != nil {
		t.Fatalf("re-record failed: %v", err)
	}

	list, err := models.ListSavedQueries(queryUser)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list.History) != 25 {
		t.Fatalf("history should be capped at 25, got %d", len(list.History))
	}
	if list.History[0].Query != "id = 10" {
		t.Errorf("the re-run query should lead, got %q", list.History[0].Query)
	}
	seen := map[string]int{}
	for _, h := range list.History {
		seen[h.Query]++
	}
	if seen["id = 10"] != 1 {
		t.Errorf("a re-run query must not be duplicated, saw it %d times", seen["id = 10"])
	}
	// Thirty distinct runs against a cap of 25 push out the five oldest
	// (0..4). The re-run of 10 adds no row, so it prunes nothing further.
	for _, gone := range []string{"id = 0", "id = 4"} {
		if seen[gone] != 0 {
			t.Errorf("%q should have been pruned", gone)
		}
	}
	for _, kept := range []string{"id = 5", "id = 29"} {
		if seen[kept] != 1 {
			t.Errorf("%q should be kept", kept)
		}
	}

	// Blank text is not history.
	if err := models.RecordQueryHistory(queryUser, "   "); err != nil {
		t.Fatalf("recording blank text should be a no-op, got %v", err)
	}
}

func TestSavedQueriesAreScopedToTheUser(t *testing.T) {
	defer setupQueryDB(t)()

	mine, err := models.SaveNamedQuery(queryUser, "mine", "is_flagged = true")
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if err := models.RecordQueryHistory(queryUser, "is_private = true"); err != nil {
		t.Fatalf("record failed: %v", err)
	}

	theirs, err := models.ListSavedQueries(otherUser)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(theirs.Saved) != 0 || len(theirs.History) != 0 {
		t.Fatalf("another user must see nothing, got %+v", theirs)
	}

	if err := models.DeleteSavedQuery(otherUser, mine.ID); !errors.Is(err, models.ErrSavedQueryNotFound) {
		t.Fatalf("deleting someone else's row should be not-found, got %v", err)
	}
	if err := models.DeleteSavedQuery(queryUser, mine.ID); err != nil {
		t.Fatalf("owner delete failed: %v", err)
	}
	list, _ := models.ListSavedQueries(queryUser)
	if len(list.Saved) != 0 || len(list.History) != 1 {
		t.Fatalf("after deleting the saved row, want 0 saved / 1 history, got %+v", list)
	}
}

func TestSavedQueriesLiveInThePrivateDatabase(t *testing.T) {
	defer setupQueryDB(t)()

	if _, err := models.SaveNamedQuery(queryUser, "secret", "title = 'Secret conversion plan'"); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	// Query text quotes note content, so it must not be readable through the
	// unencrypted public database.
	if _, err := models.PubDB().Query(`SELECT id FROM saved_queries`); err == nil {
		t.Fatal("saved_queries should not exist in the public database")
	}
	rows, err := models.PrivDB().Query(`SELECT query FROM saved_queries`)
	if err != nil {
		t.Fatalf("saved_queries should exist in the private database: %v", err)
	}
	rows.Close()
}

func TestCompletionLeadsWithStoredQueries(t *testing.T) {
	defer setupQueryDB(t)()

	if _, err := models.SaveNamedQuery(queryUser, "airflow work", "category = 'airflow'"); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	// One history entry duplicates the saved text and should be folded into
	// the named row; the other is genuinely recent.
	for _, q := range []string{"category = 'airflow'", "is_flagged = true"} {
		if err := models.RecordQueryHistory(queryUser, q); err != nil {
			t.Fatalf("record failed: %v", err)
		}
	}

	c := models.CompleteQuery("", 0, queryUser)
	if len(c.Suggestions) < 2 {
		t.Fatalf("too few suggestions: %+v", c.Suggestions)
	}
	if s := c.Suggestions[0]; s.Kind != "saved" || s.Label != "airflow work" || s.Text != "category = 'airflow'" || s.ID == 0 {
		t.Errorf("the saved query should lead, got %+v", s)
	}
	if s := c.Suggestions[1]; s.Kind != "history" || s.Text != "is_flagged = true" || s.ID == 0 {
		t.Errorf("the recent query should follow, got %+v", s)
	}
	for _, s := range c.Suggestions[2:] {
		if s.Kind == "history" && s.Text == "category = 'airflow'" {
			t.Errorf("history duplicating a saved query should be dropped")
		}
	}

	// A partial first word offers saved queries by NAME, not history.
	c = models.CompleteQuery("airf", 4, queryUser)
	if len(c.Suggestions) == 0 || c.Suggestions[0].Kind != "saved" {
		t.Fatalf("typing a saved query's name should offer it first, got %+v", c.Suggestions)
	}
	for _, s := range c.Suggestions {
		if s.Kind == "history" {
			t.Errorf("history should not be offered mid-word, got %+v", s)
		}
	}

	// Past the first token the whole-line suggestions would be noise.
	c = models.CompleteQuery("is_flagged = true AND ", 22, queryUser)
	for _, s := range c.Suggestions {
		if s.Kind == "saved" || s.Kind == "history" {
			t.Errorf("stored queries should not appear after the first token, got %+v", s)
		}
	}
}

func TestCompletionOffersNotesForAGUIDByTitle(t *testing.T) {
	defer setupQueryDB(t)()
	seedLibrary(t)

	labels := func(c *models.QueryCompletion) map[string]models.QuerySuggestion {
		out := map[string]models.QuerySuggestion{}
		for _, s := range c.Suggestions {
			out[s.Label] = s
		}
		return out
	}

	// By title: the row shows the title and inserts the quoted GUID.
	c := models.CompleteQuery("guid = sched", len("guid = sched"), queryUser)
	got := labels(c)
	s, ok := got["Airflow scheduling notes"]
	if !ok {
		t.Fatalf("a title match should be offered, got %+v", c.Suggestions)
	}
	if s.Text != "'q-2' " {
		t.Errorf("accepting should insert the quoted GUID, got %q", s.Text)
	}

	// By GUID prefix, and from a pasted note link inside the quotes.
	for _, src := range []string{"guid = 'q-3", "guid = '[[note:q-3|Secret conversion plan]]"} {
		c = models.CompleteQuery(src, len(src), queryUser)
		if _, ok := labels(c)["Secret conversion plan"]; !ok {
			t.Errorf("%s: the private note should be found by its GUID, got %+v", src, c.Suggestions)
		}
	}

	// Another user's note (q-5) and a soft-deleted one (q-6) are never
	// offered.
	c = models.CompleteQuery("guid = ", len("guid = "), queryUser)
	if len(c.Suggestions) != 4 {
		t.Errorf("want the user's 4 live notes, got %+v", c.Suggestions)
	}
	for _, s := range c.Suggestions {
		if s.Text == "'q-5' " || s.Text == "'q-6' " {
			t.Errorf("a note the user should not be offered leaked in: %+v", s)
		}
	}
}
