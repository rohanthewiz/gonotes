package models

import (
	"slices"
	"strings"
	"testing"
)

// The spec notation is now shared by three front ends — Markdown frontmatter,
// gn-clip's -c, and the TUI's one-line category field — so these tests pin the
// grammar itself rather than any one caller's use of it. The round-trip test is
// the important one: the TUI PREFILLS its field with FormatCategorySpec and
// PARSES what comes back, so a pair that disagree would silently rewrite a
// note's filing on the next save.

func TestParseCategorySpecs(t *testing.T) {
	cases := []struct {
		name  string
		specs []string
		want  map[string][]string // nil slice = category with no subcategories
		order []string
	}{
		{
			name:  "bare names",
			specs: []string{"Work", "Personal"},
			want:  map[string][]string{"Work": nil, "Personal": nil},
			order: []string{"Work", "Personal"},
		},
		{
			name:  "one subcategory",
			specs: []string{"Work/backend"},
			want:  map[string][]string{"Work": {"backend"}},
			order: []string{"Work"},
		},
		{
			name:  "several on one entry, as a person types them",
			specs: []string{"Work/backend/api"},
			want:  map[string][]string{"Work": {"backend", "api"}},
			order: []string{"Work"},
		},
		{
			name:  "repeated entries merge, as a Markdown export writes them",
			specs: []string{"Work/backend", "Work/api"},
			want:  map[string][]string{"Work": {"backend", "api"}},
			order: []string{"Work"},
		},
		{
			name:  "duplicates collapse",
			specs: []string{"Work/backend", "Work/backend"},
			want:  map[string][]string{"Work": {"backend"}},
			order: []string{"Work"},
		},
		{
			name:  "whitespace and empty segments are typos, not errors",
			specs: []string{"  Work / backend ", "", "   ", "Personal//"},
			want:  map[string][]string{"Work": {"backend"}, "Personal": nil},
			order: []string{"Work", "Personal"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names, subs := ParseCategorySpecs(tc.specs)
			if !slices.Equal(names, tc.order) {
				t.Errorf("names = %v, want %v (first-seen order matters: it is the order writes happen in)", names, tc.order)
			}
			if len(subs) != len(tc.want) {
				t.Fatalf("parsed %d categories, want %d: %v", len(subs), len(tc.want), subs)
			}
			for name, want := range tc.want {
				if got := subs[name]; !slices.Equal(got, want) {
					t.Errorf("subcategories of %q = %v, want %v", name, got, want)
				}
			}
		})
	}
}

// TestParseCategorySpecCSVSplitsOnCommas covers the single-line-field entry
// point: the TUI hands over one string, not a list.
func TestParseCategorySpecCSVSplitsOnCommas(t *testing.T) {
	names, subs := ParseCategorySpecCSV("Work/backend, Personal, Work/api")
	if !slices.Equal(names, []string{"Work", "Personal"}) {
		t.Errorf("names = %v, want [Work Personal]", names)
	}
	if got := subs["Work"]; !slices.Equal(got, []string{"backend", "api"}) {
		t.Errorf("Work subcategories = %v, want [backend api]", got)
	}

	// An empty field means no categories at all — which is how the form clears
	// every link, so it must not parse as one nameless category.
	if names, _ := ParseCategorySpecCSV("   "); len(names) != 0 {
		t.Errorf("a blank field parsed as %v, want nothing", names)
	}
}

// TestCategorySpecRoundTrip is the contract between the form's prefill and its
// save: whatever FormatCategorySpec renders, ParseCategorySpecs must read back
// unchanged.
func TestCategorySpecRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		subs []string
	}{
		{"Work", nil},
		{"Work", []string{"backend"}},
		{"Work", []string{"backend", "api", "ops"}},
		{"Reading list", []string{"long form"}}, // spaces survive; only "/" is structural
	}

	for _, tc := range cases {
		spec := FormatCategorySpec(tc.name, tc.subs)
		names, subs := ParseCategorySpecs([]string{spec})
		if len(names) != 1 || names[0] != tc.name {
			t.Fatalf("%q parsed to names %v, want [%s]", spec, names, tc.name)
		}
		if got := subs[tc.name]; !slices.Equal(got, tc.subs) {
			t.Errorf("%q parsed to subcategories %v, want %v", spec, got, tc.subs)
		}
	}
}

// TestFormatCategorySpecCSVIsParseable closes the same loop for a whole field:
// a note in two categories has to survive the join as well as the format.
func TestFormatCategorySpecCSVIsParseable(t *testing.T) {
	line := FormatCategorySpecCSV([]string{
		FormatCategorySpec("Work", []string{"backend"}),
		FormatCategorySpec("Personal", nil),
	})
	if !strings.Contains(line, ", ") {
		t.Errorf("field text %q is missing the readable separator", line)
	}

	names, subs := ParseCategorySpecCSV(line)
	if !slices.Equal(names, []string{"Work", "Personal"}) {
		t.Fatalf("%q parsed to %v, want [Work Personal]", line, names)
	}
	if got := subs["Work"]; !slices.Equal(got, []string{"backend"}) {
		t.Errorf("%q lost Work's subcategory: %v", line, got)
	}
	if got := subs["Personal"]; len(got) != 0 {
		t.Errorf("%q invented subcategories for Personal: %v", line, got)
	}
}

// TestMergeSubcategoriesOnlyAdds pins the rule that keeps one note's save from
// deleting names another note is filed under: the definition grows, never
// shrinks, through this function.
func TestMergeSubcategoriesOnlyAdds(t *testing.T) {
	merged, changed := MergeSubcategories([]string{"backend", "api"}, []string{"api", "ops"})
	if !changed {
		t.Error("adding a new name reported no change")
	}
	if !slices.Equal(merged, []string{"backend", "api", "ops"}) {
		t.Errorf("merged = %v, want [backend api ops] (existing order kept, new appended)", merged)
	}

	// The common case: nothing new. The caller skips a whole round trip on this.
	merged, changed = MergeSubcategories([]string{"backend", "api"}, []string{"backend"})
	if changed {
		t.Error("re-adding an existing name reported a change; the store would be written for nothing")
	}
	if !slices.Equal(merged, []string{"backend", "api"}) {
		t.Errorf("merged = %v, want the original list unchanged", merged)
	}

	// Blank input is a typo, not a subcategory named "".
	if _, changed := MergeSubcategories(nil, []string{"  "}); changed {
		t.Error("a blank name was merged in as a subcategory")
	}
}

// TestSameSubcategoriesIgnoresOrder is why a plain re-save writes nothing: the
// stored order is whatever the last writer typed.
func TestSameSubcategoriesIgnoresOrder(t *testing.T) {
	if !SameSubcategories([]string{"backend", "api"}, []string{"api", "backend"}) {
		t.Error("the same two names in a different order compared as different")
	}
	if !SameSubcategories(nil, nil) {
		t.Error("two empty selections compared as different")
	}
	if SameSubcategories([]string{"backend"}, []string{"backend", "api"}) {
		t.Error("a subset compared as equal; a real change would not be written")
	}
	if SameSubcategories([]string{"backend"}, []string{"api"}) {
		t.Error("two different names compared as equal")
	}
}
