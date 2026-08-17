package tui

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"gonotes/models"
)

// These tests exist because the note filter failed in a way no compiler and no
// smoke test can see: it matched, it just matched EVERYTHING. bubbles'
// DefaultFilter is sahilm/fuzzy, a subsequence matcher, and once 2000 runes of
// note body were in the haystack a few hundred words of English contained
// almost every short query as a subsequence. The list dutifully reported "31
// notes • 31 filtered" and narrowed nothing, which from the outside is
// indistinguishable from a filter that does not work at all.
//
// So the property under test is SELECTIVITY, not "does it match". A test that
// only asserted the right note is in the results would have passed against the
// broken filter every single time.

// filterNote builds the noteItem for a title/body pair, which is what carries
// the FilterValue the filter actually sees.
func filterNote(title, body string) noteItem {
	return noteItem{note: models.Note{
		Title: title,
		Body:  sql.NullString{String: body, Valid: body != ""},
	}}
}

// filterCorpus is five notes shaped the way real ones are: a short title and a
// paragraph of prose. The prose is the whole point — the bug needed length to
// appear, so a corpus of one-line bodies would not reproduce it. The content is
// invented; only its shape matters.
func filterCorpus() []noteItem {
	return []noteItem{
		filterNote("Container build notes",
			"The multi-stage build copies the compiled binary into a distroless image. "+
				"Layer caching depends on the dependency manifest being copied before the "+
				"source tree, or every edit invalidates everything below it."),
		filterNote("Weekly planning",
			"Standing agenda: review what carried over, pick the two items that will "+
				"actually ship this week, and leave the rest in the backlog instead of "+
				"pretending they are scheduled."),
		filterNote("Shell one-liners",
			"Listing every target in a makefile, finding the largest files under a "+
				"directory, and watching a log for a pattern without tailing it forever."),
		filterNote("Laptop disk cleanup",
			"Editor caches, old container images and stale simulator runtimes are the "+
				"usual offenders. Nothing here needs a tool; a handful of directories "+
				"account for most of the space."),
		filterNote("Reading list",
			"Papers on consensus protocols, a book about typography, and the long "+
				"article on spreadsheet errors that everyone keeps recommending."),
	}
}

// filterTitles runs a query over the corpus and returns the matching titles in
// rank order.
func filterTitles(t *testing.T, query string) []string {
	t.Helper()

	corpus := filterCorpus()
	targets := make([]string, len(corpus))
	for i, it := range corpus {
		targets[i] = it.FilterValue()
	}

	ranks := notesFilter(query, targets)
	out := make([]string, 0, len(ranks))
	for _, r := range ranks {
		if r.Index < 0 || r.Index >= len(corpus) {
			t.Fatalf("query %q: rank index %d out of range", query, r.Index)
		}
		out = append(out, corpus[r.Index].note.Title)
	}
	return out
}

// TestFilterIsSelective is the regression guard proper. Each of these queries
// belongs to exactly one note; under the old fuzzy-over-bodies filter a
// three-to-five letter query routinely matched every note in the corpus,
// because a paragraph of English contains almost any short query as a
// subsequence.
func TestFilterIsSelective(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"planning", "Weekly planning"},         // a title match
		{"distroless", "Container build notes"}, // body-only: the content search that must survive
		{"typography", "Reading list"},          // body-only
		{"simulator", "Laptop disk cleanup"},    // body-only
		{"makefile", "Shell one-liners"},        // body-only, and a near-miss for two other titles
		{"backlog", "Weekly planning"},          // body-only
	}

	for _, tc := range cases {
		got := filterTitles(t, tc.query)
		if len(got) != 1 {
			t.Errorf("query %q matched %d notes (%s); want exactly 1 (%s)",
				tc.query, len(got), strings.Join(got, ", "), tc.want)
			continue
		}
		if got[0] != tc.want {
			t.Errorf("query %q matched %q; want %q", tc.query, got[0], tc.want)
		}
	}
}

// TestFilterStillSearchesBodies pins the feature the fix had to preserve: "/"
// is a content search, not a title search. The narrow way to "fix" selectivity
// would have been to drop bodies from the haystack entirely, and this is what
// says that is not the fix that was made.
func TestFilterStillSearchesBodies(t *testing.T) {
	got := filterTitles(t, "duckdb")
	if len(got) != 0 {
		t.Fatalf("nothing in the corpus mentions duckdb, yet %d notes matched", len(got))
	}

	corpus := append(filterCorpus(), filterNote("Storage options", "we settled on duckdb for the columnar side"))
	targets := make([]string, len(corpus))
	for i, it := range corpus {
		targets[i] = it.FilterValue()
	}

	ranks := notesFilter("duckdb", targets)
	if len(ranks) != 1 || corpus[ranks[0].Index].note.Title != "Storage options" {
		t.Errorf("a body-only mention of duckdb did not produce exactly its note: %v", ranks)
	}
}

// TestFilterTokensNarrow: a second word makes the result set smaller, which is
// the only reason to type one.
func TestFilterTokensNarrow(t *testing.T) {
	one := filterTitles(t, "caches")
	two := filterTitles(t, "caches simulator")

	if len(two) > len(one) {
		t.Errorf("adding a word widened the results: %q -> %d, %q -> %d",
			"caches", len(one), "caches simulator", len(two))
	}
	if !slices.Contains(two, "Laptop disk cleanup") {
		t.Errorf("multi-token query lost its note: got %v", two)
	}
}

// TestFilterIsCaseInsensitive: the body pass lowercases both sides, and a
// search box that cared about capitals would be its own bug report.
func TestFilterIsCaseInsensitive(t *testing.T) {
	for _, q := range []string{"TYPOGRAPHY", "Typography", "typography"} {
		if got := filterTitles(t, q); len(got) != 1 || got[0] != "Reading list" {
			t.Errorf("query %q matched %v; want [Reading list]", q, got)
		}
	}
}

// TestFilterMatchedIndexesStayInTheHead guards the cosmetic half of the bug.
// The list delegate applies MatchedIndexes to the item's TITLE to underline the
// matched characters, so an index pointing into a body underlined arbitrary
// letters of an unrelated title.
func TestFilterMatchedIndexesStayInTheHead(t *testing.T) {
	corpus := filterCorpus()
	targets := make([]string, len(corpus))
	for i, it := range corpus {
		targets[i] = it.FilterValue()
	}

	for _, q := range []string{"planning", "distroless", "typography", "disk"} {
		for _, r := range notesFilter(q, targets) {
			head, _, _ := strings.Cut(targets[r.Index], filterSep)
			for _, idx := range r.MatchedIndexes {
				if idx < 0 || idx >= len([]rune(head)) {
					t.Errorf("query %q: matched index %d falls outside the head %q",
						q, idx, head)
				}
			}
		}
	}
}

// TestFilterValueSeparatorIsPresent: the two halves are split on a byte that
// cannot occur in a note, and everything above depends on that byte being there
// exactly once.
func TestFilterValueSeparatorIsPresent(t *testing.T) {
	v := filterNote("Title here", "body here").FilterValue()
	if n := strings.Count(v, filterSep); n != 1 {
		t.Fatalf("FilterValue has %d separators, want exactly 1: %q", n, v)
	}
	head, body, _ := strings.Cut(v, filterSep)
	if !strings.HasPrefix(head, "Title here") {
		t.Errorf("head does not start with the title: %q", head)
	}
	if body != "body here" {
		t.Errorf("body half = %q, want %q", body, "body here")
	}
}
