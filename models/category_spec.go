package models

import (
	"slices"
	"strings"
)

// Category "specs" are the flat, one-line notation for a category plus the
// subcategories selected under it:
//
//	Work              category only
//	Work/backend      category Work, subcategory backend
//	Work/backend/api  category Work, subcategories backend and api
//
// It is the notation the Markdown frontmatter already used (`categories:` is a
// list of these) and the one gn-clip.sh takes for -c. It lives here rather than
// beside either consumer because the TUI needs the same grammar: its category
// field is a single line of text, so a nested picker is not an option and the
// slash form is how a subcategory gets typed at all.
//
// The one thing the notation cannot express is a category name containing "/".
// That was already true of the Markdown format (whose exporter also writes
// these into file paths), and accepting the limitation in one shared place is
// better than two dialects that disagree about the same string.

// ParseCategorySpecs groups specs by category name, preserving first-seen
// order and deduplicating subcategories.
//
// Repeated entries for one category merge, so "Work/backend, Work/api" and
// "Work/backend/api" parse identically — the first is what a Markdown export
// writes (one entry per subcategory), the second is what a person types.
//
// Empty segments are dropped rather than rejected: a trailing comma or a
// doubled slash is a typo with an obvious intent, and this is called on text a
// user is still editing.
func ParseCategorySpecs(specs []string) (names []string, subsByName map[string][]string) {
	subsByName = map[string][]string{}
	for _, spec := range specs {
		parts := strings.Split(spec, "/")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		if _, seen := subsByName[name]; !seen {
			names = append(names, name)
			subsByName[name] = nil
		}
		for _, sub := range parts[1:] {
			sub = strings.TrimSpace(sub)
			if sub != "" && !slices.Contains(subsByName[name], sub) {
				subsByName[name] = append(subsByName[name], sub)
			}
		}
	}
	return names, subsByName
}

// ParseCategorySpecCSV parses one comma-separated line of specs — the shape a
// single-line text field holds. "Work/backend, Personal" yields names
// [Work Personal] and subs {Work: [backend]}.
func ParseCategorySpecCSV(csv string) (names []string, subsByName map[string][]string) {
	return ParseCategorySpecs(strings.Split(csv, ","))
}

// FormatCategorySpec renders one category and its selected subcategories back
// into the compact notation, so what a field shows is what it accepts.
//
// The compact form (all subcategories on one entry) is used rather than the
// Markdown exporter's one-entry-per-subcategory, because this feeds a
// single-line field where "Work/backend/api" is both shorter and clearer than
// "Work/backend, Work/api". ParseCategorySpecs reads either.
func FormatCategorySpec(name string, subs []string) string {
	if len(subs) == 0 {
		return name
	}
	return name + "/" + strings.Join(subs, "/")
}

// FormatCategorySpecCSV renders a whole set of assignments as one line, in the
// order given. The separator is ", " — a comma alone would parse back the same,
// but the space is what makes a long list readable in a narrow field.
func FormatCategorySpecCSV(specs []string) string {
	return strings.Join(specs, ", ")
}

// MergeSubcategories returns existing plus any of add that is not already
// present, preserving existing order and appending new names in the order given.
//
// This is the rule for growing a category's *definition* (the palette of
// subcategories the UIs offer) when a note is filed under a subcategory nobody
// has used before: the definition only ever gains names here, because another
// note may already be filed under one this caller has never heard of.
func MergeSubcategories(existing, add []string) (merged []string, changed bool) {
	merged = slices.Clone(existing)
	for _, name := range add {
		name = strings.TrimSpace(name)
		if name == "" || slices.Contains(merged, name) {
			continue
		}
		merged = append(merged, name)
		changed = true
	}
	return merged, changed
}

// SameSubcategories reports whether two subcategory selections hold the same
// names, ignoring order.
//
// Order-insensitive on purpose: the stored order is whatever the last writer
// typed, and rewriting the junction row (and with it a sync change record)
// because a user typed "Work/api/backend" instead of "Work/backend/api" would
// be churn with no meaning behind it.
func SameSubcategories(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}
