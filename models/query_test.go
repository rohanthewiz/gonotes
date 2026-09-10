package models

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// query_test.go pins the advanced-search language itself: what parses, what it
// means, and what a mistake looks like. It runs entirely without a database —
// a parsed query is evaluated against a Note value and a slice of category
// links, which is exactly what RunQuery does per row — so these tests cover
// semantics and the DB-backed ones in query_run_test.go cover plumbing.

var queryTestNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.Local)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// sampleNote is the note every semantic test below is asked about.
func sampleNote() *Note {
	return &Note{
		ID:          42,
		GUID:        "note-guid-42",
		Title:       "Airflow DAG conversion",
		Description: ns("Moving the legacy DAGs over"),
		Body:        ns("Steps:\n1. export\n2. convert\n3. verify PANIC handling"),
		Tags:        ns("airflow, migration ,capture"),
		IsPrivate:   false,
		IsFlagged:   true,
		CreatedBy:   ns("user-1"),
		CreatedAt:   queryTestNow.AddDate(0, 0, -3),
		UpdatedAt:   queryTestNow.AddDate(0, 0, -1),
		Version:     4,
	}
}

func sampleLinks() []NoteCategoryMapping {
	return []NoteCategoryMapping{
		{NoteID: 42, CategoryID: 7, CategoryName: "airflow", SelectedSubcategories: []string{"conversion", "dags"}},
		{NoteID: 42, CategoryID: 9, CategoryName: "Work"},
	}
}

// match parses src and evaluates it against the sample note.
func match(t *testing.T, src string) bool {
	t.Helper()
	return matchNote(t, src, sampleNote(), sampleLinks())
}

func matchNote(t *testing.T, src string, n *Note, links []NoteCategoryMapping) bool {
	t.Helper()
	q, err := ParseQuery(src)
	if err != nil {
		t.Fatalf("ParseQuery(%q) failed: %v", src, err)
	}
	if q.Where == nil {
		return true
	}
	f := noteFacts{note: n, links: links}
	return q.Where.eval(&evalEnv{Now: queryTestNow, UserGUID: "user-1"}, &f)
}

func mustMatch(t *testing.T, src string) {
	t.Helper()
	if !match(t, src) {
		t.Errorf("%q did not match the sample note, but should have", src)
	}
}

func mustNotMatch(t *testing.T, src string) {
	t.Helper()
	if match(t, src) {
		t.Errorf("%q matched the sample note, but should not have", src)
	}
}

// The query from the original request, in the shape a person would type it.
func TestCategoryAndSubcategoryQuery(t *testing.T) {
	mustMatch(t, "category = 'airflow' and subcategory = 'conversion'")
	mustMatch(t, "WHERE category = 'airflow' AND subcategory = 'conversion'")
	mustMatch(t, "SELECT * FROM notes WHERE category = 'airflow' AND subcategory = 'conversion'")
	mustNotMatch(t, "category = 'airflow' and subcategory = 'scheduling'")
}

func TestEveryCatalogFieldIsQueryable(t *testing.T) {
	// The guarantee this whole feature rests on: every field the catalog
	// advertises can be parsed AND evaluated. A field added to the catalog
	// with no arm in noteFacts would parse and then quietly match nothing,
	// which is the one failure mode a user could not diagnose.
	n := sampleNote()
	n.AuthoredAt = sql.NullTime{Time: queryTestNow, Valid: true}
	n.SyncedAt = sql.NullTime{Time: queryTestNow, Valid: true}
	n.DeletedAt = sql.NullTime{Time: queryTestNow, Valid: true}
	n.UpdatedBy = ns("user-1")

	for _, f := range QueryFields() {
		var src string
		switch f.Kind {
		case KindString:
			src = f.Name + " IS NOT NULL"
		case KindNumber:
			src = f.Name + " >= 0"
		case KindBool:
			src = f.Name + " = true OR " + f.Name + " = false"
		case KindTime:
			src = f.Name + " < now OR " + f.Name + " IS NULL"
		}
		q, err := ParseQuery(src)
		if err != nil {
			t.Errorf("field %q is advertised but %q does not parse: %v", f.Name, src, err)
			continue
		}
		facts := noteFacts{note: n, links: sampleLinks()}
		q.Where.eval(&evalEnv{Now: queryTestNow, UserGUID: "user-1"}, &facts)

		// And the evaluator must actually know the field: every kind's
		// accessor is asked for it directly, so a catalog entry with no arm
		// shows up here rather than as a query that silently matches nothing.
		if !fieldIsWired(&facts, &f) {
			t.Errorf("field %q has no arm in the evaluator's %s accessor", f.Name, f.Kind)
		}
	}
}

// fieldIsWired reports whether the evaluator recognises a field at all. It
// probes with a note that carries a value for every column, so "returns
// nothing" can only mean "not wired".
func fieldIsWired(f *noteFacts, field *QueryField) bool {
	switch field.Kind {
	case KindNumber:
		return len(f.numberValues(field)) > 0
	case KindBool:
		_, ok := f.boolValue(field)
		return ok
	case KindTime:
		return len(f.timeValues(field)) > 0
	}
	return len(f.stringValues(field)) > 0
}

func TestStringComparisonIgnoresCase(t *testing.T) {
	mustMatch(t, "category = 'AIRFLOW'")
	mustMatch(t, "title CONTAINS 'dag'")
	mustMatch(t, "title LIKE 'airflow%'")
	mustMatch(t, "tags = 'CAPTURE'")
}

// MATCHES is the one text operator that does not fold case, because a regular
// expression carries its own flags and taking that control away would make
// half of them meaningless.
func TestMatchesIsCaseSensitiveUnlessAsked(t *testing.T) {
	mustMatch(t, `body MATCHES 'PANIC'`)
	mustNotMatch(t, `body MATCHES 'panic'`)
	mustMatch(t, `body MATCHES '(?i)panic'`)
	mustMatch(t, `body ~ 'convert'`)
	mustNotMatch(t, `body !~ 'convert'`)
}

func TestMultiValuedNegationIsUniversal(t *testing.T) {
	// The note is filed under both airflow and Work. SQL's reading of
	// `category != 'Work'` would be true (airflow is not Work); ours is false,
	// because the note IS filed under Work.
	mustNotMatch(t, "category != 'Work'")
	mustMatch(t, "category != 'archive'")
	mustNotMatch(t, "NOT category = 'airflow'")
	mustMatch(t, "tags != 'unrelated'")
	mustNotMatch(t, "tags != 'migration'")
}

func TestTagsAreSplitAndTrimmed(t *testing.T) {
	mustMatch(t, "tags = 'migration'") // stored as " migration " between commas
	mustMatch(t, "tags IN ('nope', 'capture')")
	mustNotMatch(t, "tags = 'airflow, migration ,capture'") // the raw column is not a value
}

// Absence is absence, not SQL's third truth value.
func TestNullIsAbsenceNotUnknown(t *testing.T) {
	n := sampleNote()
	n.Description = sql.NullString{}

	if !matchNote(t, "description != 'anything'", n, nil) {
		t.Error("a note with no description should satisfy description != 'anything'")
	}
	if matchNote(t, "description = 'anything'", n, nil) {
		t.Error("a note with no description should not satisfy description = 'anything'")
	}
	if !matchNote(t, "description IS NULL", n, nil) {
		t.Error("IS NULL is the way to ask about absence and it did not work")
	}
	if !matchNote(t, "synced_at IS NULL", n, nil) {
		t.Error("a never-synced note should satisfy synced_at IS NULL")
	}
}

func TestIsEmptyDistinguishesBlankFromMissing(t *testing.T) {
	blank := sampleNote()
	blank.Description = ns("   ")
	if !matchNote(t, "description IS EMPTY", blank, nil) {
		t.Error("a whitespace-only description should be EMPTY")
	}
	if matchNote(t, "description IS NULL", blank, nil) {
		t.Error("a whitespace-only description is present, so it is not NULL")
	}
	mustMatch(t, "description IS NOT EMPTY")
}

func TestBooleanFieldsStandAlone(t *testing.T) {
	mustMatch(t, "is_flagged")
	mustMatch(t, "flagged")            // alias
	mustNotMatch(t, "NOT is_flagged")  //
	mustNotMatch(t, "is_private")      // the sample note is public
	mustMatch(t, "is_private = false") //
	mustMatch(t, "private = no")       // alias + spelled-out literal
	mustMatch(t, "is_flagged AND NOT is_private")
}

func TestTimeLiteralsAndWindows(t *testing.T) {
	// created_at is three days before "now".
	mustMatch(t, "created_at > -7d")
	mustNotMatch(t, "created_at > -1d")
	mustMatch(t, "created_at < today")
	mustMatch(t, "updated_at >= -2d")
	mustMatch(t, "created_at = '2026-03-07'") // the whole day, not midnight
	mustMatch(t, "created_at = '2026-03'")    // the whole month
	mustMatch(t, "created_at BETWEEN '2026-03-01' AND '2026-03-07'")
	mustNotMatch(t, "created_at BETWEEN '2026-03-01' AND '2026-03-06'")
}

// `>` against a day-wide literal means "after that whole day", which is the
// reading that makes >= and > differ the way a person expects.
func TestOrderingAgainstADayWindow(t *testing.T) {
	n := sampleNote()
	n.CreatedAt = time.Date(2026, 3, 7, 18, 0, 0, 0, time.Local)

	if !matchNote(t, "created_at >= '2026-03-07'", n, nil) {
		t.Error(">= a day should include that day")
	}
	if matchNote(t, "created_at > '2026-03-07'", n, nil) {
		t.Error("> a day should exclude that day")
	}
	if !matchNote(t, "created_at <= '2026-03-07'", n, nil) {
		t.Error("<= a day should include that day")
	}
	if matchNote(t, "created_at < '2026-03-07'", n, nil) {
		t.Error("< a day should exclude that day")
	}
}

func TestFreeTextTermSearchesEverything(t *testing.T) {
	mustMatch(t, "'conversion'") // a subcategory
	mustMatch(t, "'legacy'")     // the description
	mustMatch(t, "'verify'")     // the body
	mustMatch(t, "'migration'")  // a tag
	mustMatch(t, "'Work'")       // a category
	mustNotMatch(t, "'kubernetes'")
	mustMatch(t, "'conversion' AND category = 'airflow'")
	mustMatch(t, "airflow") // an unquoted bare word is free text too
}

func TestBareNumberIsANoteId(t *testing.T) {
	mustMatch(t, "42")
	mustNotMatch(t, "43")
}

func TestMeResolvesToTheQueryingUser(t *testing.T) {
	mustMatch(t, "created_by = me")
	mustNotMatch(t, "created_by = 'someone-else'")
}

func TestGroupingAndPrecedence(t *testing.T) {
	// AND binds tighter than OR, so without parentheses the first is true
	// (via the right-hand AND) and the parenthesised version is false.
	mustMatch(t, "category = 'nope' OR category = 'airflow' AND is_flagged")
	mustNotMatch(t, "(category = 'nope' OR category = 'airflow') AND is_private")
	mustMatch(t, "NOT (is_private OR category = 'archive')")
}

func TestInAndBetweenLists(t *testing.T) {
	mustMatch(t, "category IN ('archive', 'airflow')")
	mustNotMatch(t, "category IN ('archive', 'inbox')")
	mustMatch(t, "category NOT IN ('archive', 'inbox')")
	mustMatch(t, "id BETWEEN 40 AND 45")
	mustNotMatch(t, "id BETWEEN 1 AND 10")
	mustMatch(t, "id IN (1, 42, 99)")
	mustMatch(t, "category_id IN (7, 1000)")
}

func TestEmptyQueryMatchesEverything(t *testing.T) {
	for _, src := range []string{"", "   ", "WHERE"} {
		q, err := ParseQuery(src)
		if err != nil {
			t.Fatalf("ParseQuery(%q) should be accepted, got %v", src, err)
		}
		if q.Where != nil {
			t.Errorf("ParseQuery(%q) produced a filter; an empty query must match everything", src)
		}
	}
}

func TestTrailingClauses(t *testing.T) {
	q, err := ParseQuery("is_flagged ORDER BY created_at ASC, title DESC LIMIT 5 OFFSET 10")
	if err != nil {
		t.Fatalf("trailing clauses failed to parse: %v", err)
	}
	if len(q.Order) != 2 || q.Order[0].Field.Name != "created_at" || q.Order[0].Desc {
		t.Fatalf("ORDER BY parsed as %+v", q.Order)
	}
	if !q.Order[1].Desc || q.Order[1].Field.Name != "title" {
		t.Fatalf("second ORDER BY term parsed as %+v", q.Order[1])
	}
	if q.Limit != 5 || q.Offset != 10 {
		t.Fatalf("LIMIT/OFFSET parsed as %d/%d, want 5/10", q.Limit, q.Offset)
	}
}

// Errors carry a position, because underlining the mistake is most of what
// makes a query language usable in a text box.
func TestSyntaxErrorsArePositioned(t *testing.T) {
	cases := []struct {
		src      string
		wantAt   int
		contains string
	}{
		{"catgory = 'x'", 0, "unknown field"},
		{"category = ", 11, "expected a value"},
		{"category 'x'", 9, "expected an operator"},
		{"id = 'not a number'", 5, "is a number"},
		{"is_flagged > 3", 11, "cannot be ordered"},
		{"title MATCHES '('", 14, "invalid regular expression"},
		{"created_at = 'the other day'", 13, "not a date"},
		{"title = 'unterminated", 8, "unterminated"},
		{"category = 'x' AND", 18, "expected a field name"},
		{"(category = 'x'", 15, "expected ')'"},
		{"id BETWEEN 1", 12, "expected AND in BETWEEN"},
		{"ORDER BY tags", 9, "cannot be sorted"},
		{"title IN 'x'", 9, "expected '('"},
	}
	for _, c := range cases {
		_, err := ParseQuery(c.src)
		if err == nil {
			t.Errorf("ParseQuery(%q) succeeded; want an error", c.src)
			continue
		}
		qe, ok := err.(*QueryError)
		if !ok {
			t.Errorf("ParseQuery(%q) returned %T, want *QueryError", c.src, err)
			continue
		}
		if qe.Pos != c.wantAt {
			t.Errorf("ParseQuery(%q) reported position %d, want %d (%s)", c.src, qe.Pos, c.wantAt, qe.Msg)
		}
		if !strings.Contains(qe.Msg, c.contains) {
			t.Errorf("ParseQuery(%q) said %q, want it to mention %q", c.src, qe.Msg, c.contains)
		}
	}
}

func TestUnknownFieldSuggestsTheRealOne(t *testing.T) {
	_, err := ParseQuery("catgory = 'x'")
	qe, _ := err.(*QueryError)
	if qe == nil || !strings.Contains(qe.Hint, "category") {
		t.Fatalf("a near-miss field name should suggest the real one, got %+v", qe)
	}
	// And a word that resembles nothing gets no guess, because a wrong
	// suggestion is worse than none.
	_, err = ParseQuery("zzzzzzzzzzzz = 'x'")
	qe, _ = err.(*QueryError)
	if qe == nil || qe.Hint != "" {
		t.Fatalf("an unrecognisable name should get no suggestion, got %q", qe.Hint)
	}
}

// Every advertised example must parse, and its canonical rendering must parse
// back to the same thing — the guard against the printer and the grammar
// drifting apart.
func TestExamplesParseAndRoundTrip(t *testing.T) {
	for _, ex := range QueryExamples {
		q, err := ParseQuery(ex)
		if err != nil {
			t.Errorf("advertised example %q does not parse: %v", ex, err)
			continue
		}
		canon := q.String()
		q2, err := ParseQuery(canon)
		if err != nil {
			t.Errorf("canonical form of %q (%q) does not parse: %v", ex, canon, err)
			continue
		}
		if q2.String() != canon {
			t.Errorf("canonical form is not stable:\n  %q\n  %q", canon, q2.String())
		}
	}
}

func TestAliasesResolveToCanonicalNames(t *testing.T) {
	q, err := ParseQuery("cat = 'x' AND subcat = 'y' AND flagged AND modified > -1d")
	if err != nil {
		t.Fatalf("aliases failed to parse: %v", err)
	}
	got := q.String()
	for _, want := range []string{"category =", "subcategory =", "is_flagged", "updated_at >"} {
		if !strings.Contains(got, want) {
			t.Errorf("canonical form %q is missing %q", got, want)
		}
	}
}

func TestNeedsCategoriesAndDeletedOptIn(t *testing.T) {
	q, _ := ParseQuery("title = 'x'")
	if q.NeedsCategories() {
		t.Error("a query with no category field should not force the link load")
	}
	if q.IncludesDeleted() {
		t.Error("a query that does not mention deleted_at must not include deleted notes")
	}

	q, _ = ParseQuery("subcategory = 'conversion'")
	if !q.NeedsCategories() {
		t.Error("a subcategory query needs the category links")
	}

	q, _ = ParseQuery("'anything'")
	if !q.NeedsCategories() {
		t.Error("free text spans categories, so it needs the links")
	}

	q, _ = ParseQuery("deleted_at IS NOT NULL")
	if !q.IncludesDeleted() {
		t.Error("naming deleted_at is the opt-in for seeing deleted notes")
	}
}

func TestTableQualifierIsTolerated(t *testing.T) {
	mustMatch(t, "notes.title CONTAINS 'airflow'")
}

func TestDurationUnitsAreDistinguished(t *testing.T) {
	// The scanner must prefer the longest unit: -6mo is six months, not six
	// minutes followed by a stray o.
	q, err := ParseQuery("created_at > -6mo")
	if err != nil {
		t.Fatalf("-6mo failed to parse: %v", err)
	}
	pred := q.Where.(*predicate)
	got := pred.Values[0].timeAt(queryTestNow)
	if want := queryTestNow.Add(-180 * 24 * time.Hour); got.Sub(want).Abs() > time.Hour {
		t.Fatalf("-6mo resolved to %v, want about %v", got, want)
	}

	if _, err := ParseQuery("created_at > -6q"); err == nil {
		t.Fatal("an unknown time unit should be rejected")
	}
}
