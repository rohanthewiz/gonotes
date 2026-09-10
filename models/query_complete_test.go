package models

import (
	"strings"
	"testing"
)

// query_complete_test.go pins the completer's state machine — the part that
// decides WHAT kind of thing belongs at the cursor. The data-driven half (the
// user's real categories and tags) needs a database and is covered in
// query_run_test.go.

// complete is the shorthand these tests use: the cursor sits at the end of
// src, which is where a person typing actually is.
func complete(src string) *QueryCompletion {
	return CompleteQuery(src, len(src), "")
}

func labels(c *QueryCompletion) []string {
	out := make([]string, len(c.Suggestions))
	for i, s := range c.Suggestions {
		out[i] = s.Label
	}
	return out
}

func hasLabel(c *QueryCompletion, want string) bool {
	for _, s := range c.Suggestions {
		if s.Label == want {
			return true
		}
	}
	return false
}

func TestCompletionContextFollowsTheGrammar(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"", "field"},
		{"cat", "field"},
		{"category ", "operator for category"},
		{"category =", "value for category"},
		{"category = ", "value for category"},
		{"category = 'air", "value for category"},
		{"category = 'airflow' ", "conjunction"},
		{"category = 'airflow' AND ", "field"},
		{"category = 'airflow' AND sub", "field"},
		{"title IS ", "NULL or EMPTY"},
		{"title IS NOT ", "NULL or EMPTY"},
		{"id BETWEEN 1 ", "AND (upper bound)"},
		// The AND in a BETWEEN belongs to the operator, not to the expression.
		// Reading it as a conjunction is the classic bug in this grammar, and
		// it shows up first in the completer: everything after the AND would
		// complete as if a new condition had started.
		{"id BETWEEN 1 AND ", "value for id"},
		{"id BETWEEN 1 AND 5 ", "conjunction"},
		{"created_at BETWEEN '2026-01-01' AND ", "value for created_at"},
		{"is_flagged ORDER ", "BY"},
		{"is_flagged ORDER BY ", "order by field"},
		{"is_flagged ORDER BY title ", "sort direction"},
		{"is_flagged LIMIT ", "count"},
		{"NOT ", "field"},
		{"(", "field"},
		{"tags IN (", "value for tags"},
		{"tags IN ('a', ", "value for tags"},
	}
	for _, c := range cases {
		got := complete(c.src)
		if got.Context != c.want {
			t.Errorf("CompleteQuery(%q) is in context %q, want %q (offered %v)",
				c.src, got.Context, c.want, labels(got))
		}
	}
}

func TestEmptyQueryLeadsWithExamples(t *testing.T) {
	c := complete("")
	if len(c.Suggestions) == 0 || c.Suggestions[0].Kind != "example" {
		t.Fatalf("an empty box should lead with worked examples, got %v", labels(c))
	}
	// The field list still follows, so the box is not only examples.
	if !hasLabel(c, "category") {
		t.Error("the field list should follow the examples")
	}
}

func TestFieldCompletionMatchesAliasesButInsertsCanonicalNames(t *testing.T) {
	c := complete("subcat")
	if !hasLabel(c, "subcategory") {
		t.Fatalf("typing an alias should find the field, got %v", labels(c))
	}
	for _, s := range c.Suggestions {
		if s.Label == "subcategory" && strings.TrimSpace(s.Text) != "subcategory" {
			t.Errorf("completion inserts %q; it should converge on the canonical name", s.Text)
		}
	}

	// A substring, not a prefix: "flag" must still find is_flagged, or the
	// underscore-prefixed columns would be unreachable by the word people use.
	if !hasLabel(complete("flag"), "is_flagged") {
		t.Error("a substring match should find is_flagged")
	}
}

func TestOperatorSuggestionsFitTheFieldsType(t *testing.T) {
	str := complete("title ")
	if !hasLabel(str, "CONTAINS") || !hasLabel(str, "LIKE") {
		t.Errorf("a text field should offer CONTAINS and LIKE, got %v", labels(str))
	}

	num := complete("id ")
	if hasLabel(num, "LIKE") {
		t.Errorf("a number field should not offer LIKE, got %v", labels(num))
	}
	if !hasLabel(num, "BETWEEN") {
		t.Errorf("a number field should offer BETWEEN, got %v", labels(num))
	}

	b := complete("is_private ")
	if hasLabel(b, "<") || hasLabel(b, "BETWEEN") {
		t.Errorf("a boolean should not offer ordering operators, got %v", labels(b))
	}

	// IS NULL is only worth offering where a NULL is possible.
	if hasLabel(complete("guid "), "IS NULL") {
		t.Error("a NOT NULL column should not offer IS NULL")
	}
	if !hasLabel(complete("synced_at "), "IS NULL") {
		t.Error("a nullable column should offer IS NULL")
	}
}

func TestValueSuggestionsForLiteralKinds(t *testing.T) {
	b := complete("is_private = ")
	if !hasLabel(b, "true") || !hasLabel(b, "false") {
		t.Errorf("a boolean value position should offer true/false, got %v", labels(b))
	}
	// Bare, not quoted: `is_private = 'true'` would be a string comparison in
	// most languages and a confusing thing to insert here.
	for _, s := range b.Suggestions {
		if strings.ContainsAny(s.Text, "'\"") {
			t.Errorf("boolean literal %q should not be quoted", s.Text)
		}
	}

	tm := complete("updated_at > ")
	for _, want := range []string{"today", "now", "-7d"} {
		if !hasLabel(tm, want) {
			t.Errorf("a time value position should offer %q, got %v", want, labels(tm))
		}
	}

	me := complete("created_by = ")
	if !hasLabel(me, "me") {
		t.Errorf("an ownership field should offer me, got %v", labels(me))
	}
}

func TestSortFieldsExcludeMultiValuedOnes(t *testing.T) {
	c := complete("is_flagged ORDER BY ")
	if hasLabel(c, "tags") || hasLabel(c, "category") {
		t.Errorf("multi-valued fields cannot be sorted on and must not be offered: %v", labels(c))
	}
	if !hasLabel(c, "updated_at") {
		t.Errorf("ORDER BY should offer updated_at, got %v", labels(c))
	}
}

// The replacement span is what makes accepting a suggestion a pure splice in
// both front ends. These are the cases that get it wrong when it is wrong.
func TestReplacementSpan(t *testing.T) {
	c := complete("cat")
	if c.ReplaceStart != 0 || c.ReplaceEnd != 3 {
		t.Errorf("a half-typed word spans [%d,%d), want [0,3)", c.ReplaceStart, c.ReplaceEnd)
	}

	c = complete("category = 'airflow' AND ")
	if c.ReplaceStart != len("category = 'airflow' AND ") || c.ReplaceStart != c.ReplaceEnd {
		t.Errorf("after a space the span should be empty at the cursor, got [%d,%d)",
			c.ReplaceStart, c.ReplaceEnd)
	}

	// Completing in the MIDDLE of a word replaces the whole word, tail
	// included, rather than splicing a new head onto the old tail.
	src := "categ AND is_flagged"
	c = CompleteQuery(src, 5, "")
	if c.ReplaceStart != 0 || c.ReplaceEnd != 5 {
		t.Errorf("mid-word span is [%d,%d), want [0,5)", c.ReplaceStart, c.ReplaceEnd)
	}
	src = "catgory = 'x'"
	c = CompleteQuery(src, 3, "")
	if c.ReplaceStart != 0 || c.ReplaceEnd != 7 {
		t.Errorf("a cursor inside a word should still replace the whole word: got [%d,%d), want [0,7)",
			c.ReplaceStart, c.ReplaceEnd)
	}

	// A half-typed quoted value: the span starts at the opening quote so the
	// inserted, properly quoted value replaces it whole.
	src = "category = 'air"
	c = CompleteQuery(src, len(src), "")
	if c.ReplaceStart != len("category = ") {
		t.Errorf("a partial quoted value should be replaced from its opening quote, got %d", c.ReplaceStart)
	}
	// And when the quote is already closed, the closing quote is consumed too.
	src = "category = 'air'"
	c = CompleteQuery(src, len(src)-1, "")
	if c.ReplaceEnd != len(src) {
		t.Errorf("a closed quoted value should be replaced through its closing quote, got %d", c.ReplaceEnd)
	}
}

// Accepting a suggestion must produce text that still parses. This is the
// property that matters most and the easiest to break, so it is checked by
// construction over a walk of the language rather than by listing cases.
func TestAcceptingASuggestionKeepsTheQueryParseable(t *testing.T) {
	seeds := []string{
		"", "cat", "category ", "category = ", "is_flagged AND ", "title ",
		"id ", "updated_at ", "updated_at > ", "is_private = ",
		"title IS ", "is_flagged ORDER ", "is_flagged ORDER BY ",
		"is_flagged ORDER BY title ", "category = 'x' ",
	}
	for _, seed := range seeds {
		c := CompleteQuery(seed, len(seed), "")
		for _, s := range c.Suggestions {
			if s.Kind == "example" {
				continue // an example replaces the whole box, tested elsewhere
			}
			spliced := seed[:c.ReplaceStart] + s.Text + seed[c.ReplaceEnd:]
			// The result is usually an INCOMPLETE query (a field with no
			// operator yet), which must not parse — so what is checked is
			// that it either parses or fails somewhere at or after the
			// splice, never with a complaint about the text we just wrote.
			if _, err := ParseQuery(spliced); err != nil {
				qe, ok := err.(*QueryError)
				if !ok {
					t.Errorf("splicing %q into %q gave a non-positional error: %v", s.Text, seed, err)
					continue
				}
				if qe.Pos < c.ReplaceStart {
					t.Errorf("splicing %q into %q broke earlier text: %v (at %d, spliced at %d)",
						s.Text, seed, qe.Msg, qe.Pos, c.ReplaceStart)
				}
			}
		}
	}
}

// A query that cannot lex at all (a stray character) must still complete:
// help is wanted most when the text is wrong.
func TestCompleterSurvivesUnlexableText(t *testing.T) {
	c := CompleteQuery("category = 'x' # AND ti", len("category = 'x' # AND ti"), "")
	if c == nil {
		t.Fatal("the completer returned nothing for text that cannot lex")
	}
}

func TestCompletionDoesNotDoubleSpaces(t *testing.T) {
	// Completing before an existing space must not add another one.
	src := "cat = 'x'"
	c := CompleteQuery(src, 3, "")
	for _, s := range c.Suggestions {
		if strings.HasSuffix(s.Text, " ") {
			t.Errorf("suggestion %q adds a space in front of one that is already there", s.Text)
		}
	}
}
