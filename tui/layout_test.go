package tui

import (
	"database/sql"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonotes/models"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

// Layout tests. Three things Phase 3 changed are only observable in rendered
// output, so this file renders screens directly — no program, no database —
// and asserts on the strings.
//
// Rendering a screen outside a tea.Program is safe here because every screen's
// View() is a pure function of its own fields: the root owns the program
// lifecycle, the screens own only their layout. That is what makes goldens
// practical at all; driving a real program instead would mean asserting
// against a stream of cursor-positioning escapes.

// goldenUpdate reports whether -update was passed.
//
// The flag is looked up rather than declared: teatest pulls in
// charmbracelet/x/exp/golden, which registers its own -update in an init, and
// a second flag.Bool("update", ...) panics the whole binary before any test
// runs. Sharing the flag is also the behavior you want — one -update rewrites
// every golden in the package.
func goldenUpdate() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// fixedTime is the timestamp every fixture note carries. Goldens must not
// depend on the wall clock — a note created with time.Now() renders today's
// date and the golden rots at midnight.
var fixedTime = time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC)

// fixtureNote builds a note with fully determined content. Used by the goldens
// and by the palette suite's delegate test.
func fixtureNote(id int64, title, body string) models.Note {
	return models.Note{
		ID:        id,
		GUID:      "fixture-" + title,
		Title:     title,
		Body:      sql.NullString{String: body, Valid: body != ""},
		CreatedAt: fixedTime,
		UpdatedAt: fixedTime,
	}
}

// fixtureBrowse builds a browse screen at a given size, seeded and laid out,
// with no database behind it. newBrowseScreen reads sess.width at construction
// and Init() would hit the DB, so the sequence here is deliberate: construct,
// layout, then set items directly.
func fixtureBrowse(t *testing.T, width, height int) *browseScreen {
	t.Helper()

	sess := &session{
		user:   &models.User{GUID: "fixture-user"},
		width:  width,
		height: height,
	}
	s := newBrowseScreen(sess)
	s.layout()

	notes := []models.Note{
		fixtureNote(1, "Alpha note", "# Alpha\n\nThe first note's body, with **bold** text and a list:\n\n- one\n- two\n"),
		fixtureNote(2, "Beta note", "Beta has a plain body and no heading."),
		fixtureNote(3, "Gamma note", ""),
	}
	notes[1].Tags = sql.NullString{String: "work,urgent", Valid: true}
	notes[2].IsFlagged = true

	items := make([]list.Item, len(notes))
	for i, n := range notes {
		items[i] = noteItem{note: n}
	}
	s.list.SetItems(items) // returns a cmd we do not need; the items land synchronously
	return s
}

// ---- Goldens ---------------------------------------------------------------

// TestBrowseGoldens pins the two layouts. Goldens rather than substring
// assertions because the thing under test *is* the arrangement: which pane
// starts at which column, where the rule falls, how tall each side is. A
// substring check would pass on a layout that had collapsed to one column.
//
// Regenerate with: go test ./tui -run TestBrowseGoldens -update
func TestBrowseGoldens(t *testing.T) {
	// Pin the palette: the goldens contain color escapes, so a test that ran
	// after a light-palette test would otherwise diff against the wrong colors.
	withPalette(t, DefaultPalette(true))

	cases := []struct {
		name          string
		width, height int
	}{
		// Just under widePaneMin: the list must own the full width.
		{"narrow", 80, 24},
		// Comfortably over: list plus preview.
		{"wide", 120, 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fixtureBrowse(t, tc.width, tc.height).View()
			compareGolden(t, tc.name+"-browse", got)
		})
	}
}

// compareGolden diffs got against testdata/<name>.golden, or rewrites it under
// -update. The mismatch report prints the ANSI-stripped text of both sides:
// the raw bytes are unreadable, and a layout change shows up in the text
// arrangement anyway.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")
	if goldenUpdate() {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run: go test ./tui -run %s -update)", path, err, t.Name())
	}
	if got == string(want) {
		return
	}
	t.Errorf("rendered output differs from %s\n\n--- want ---\n%s\n--- got ---\n%s\n\n"+
		"if this change is intended: go test ./tui -update",
		path, stripANSI(string(want)), stripANSI(got))
}

// ---- Two-pane layout -------------------------------------------------------

// TestBrowseLayoutSwitchesAtThreshold pins the width the preview appears at,
// from both sides. An off-by-one here is invisible on a normal terminal and
// obvious on exactly a 100-column one.
func TestBrowseLayoutSwitchesAtThreshold(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	cases := []struct {
		width       int
		wantPreview bool
	}{
		{widePaneMin - 1, false},
		{widePaneMin, true},
		{widePaneMin + 40, true},
		{40, false},
	}

	for _, tc := range cases {
		s := fixtureBrowse(t, tc.width, 24)
		gotPreview := s.previewWidth > 0
		if gotPreview != tc.wantPreview {
			t.Errorf("width %d: preview showing = %v, want %v", tc.width, gotPreview, tc.wantPreview)
		}
		// Whatever the split, the two panes have to add up to the terminal —
		// a pane one column too wide wraps every line of the other.
		if s.listWidth+s.previewWidth != tc.width {
			t.Errorf("width %d: list(%d) + preview(%d) = %d, want %d",
				tc.width, s.listWidth, s.previewWidth, s.listWidth+s.previewWidth, tc.width)
		}
	}
}

// TestBrowseViewFitsTheTerminal is the assertion that actually catches a broken
// layout: every rendered line must fit in the terminal, or the terminal wraps
// it and the whole screen shears by a line for each overflow.
//
// 80 is not a redundant case alongside 120. It is the width at which bubbles
// v2.1.1's help footer overruns — its truncation stops truncating once the
// ellipsis no longer fits, so the browse footer renders 111 columns wide inside
// an 80-column list. See clampPane.
func TestBrowseViewFitsTheTerminal(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	for _, size := range []struct{ w, h int }{
		{80, 24},  // narrow; the footer-overrun width
		{100, 30}, // exactly at the two-pane threshold
		{120, 30}, // wide
		{60, 20},  // cramped
	} {
		view := fixtureBrowse(t, size.w, size.h).View()

		for i, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > size.w {
				t.Errorf("at %dx%d line %d is %d columns wide, over the terminal: %q",
					size.w, size.h, i, w, stripANSI(line))
			}
		}
		if h := lipgloss.Height(view); h != size.h {
			t.Errorf("at %dx%d the view is %d lines tall, want exactly %d — a short pane leaves the rule stopping early, a tall one pushes the status bar off",
				size.w, size.h, h, size.h)
		}
	}
}

// TestCategoriesViewFitsTheTerminal covers the other list screen, which shares
// the footer and therefore the overrun.
func TestCategoriesViewFitsTheTerminal(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	const width, height = 80, 24
	sess := &session{user: &models.User{GUID: "u"}, width: width, height: height}
	s := newCategoriesScreen(sess)
	s.list.SetSize(width, height)

	for i, line := range strings.Split(s.View(), "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d is %d columns wide, over the %d-column terminal: %q",
				i, w, width, stripANSI(line))
		}
	}
}

// TestBrowsePreviewShowsSelectedNote confirms the pane is wired to the
// selection rather than rendering a fixed note.
func TestBrowsePreviewShowsSelectedNote(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	s := fixtureBrowse(t, 120, 30)

	first := stripANSI(s.previewView())
	if !strings.Contains(first, "Alpha note") {
		t.Fatalf("the preview does not show the selected note's title:\n%s", first)
	}
	if !strings.Contains(first, "bold") {
		t.Errorf("the preview does not show the selected note's rendered body:\n%s", first)
	}

	s.list.Select(2) // Gamma note, which has no body
	third := stripANSI(s.previewView())
	if !strings.Contains(third, "Gamma note") {
		t.Errorf("the preview did not follow the selection:\n%s", third)
	}
	if !strings.Contains(third, "no body") {
		t.Errorf("an empty note should say so rather than showing a blank pane:\n%s", third)
	}
}

// ---- Markdown cache --------------------------------------------------------

// TestMarkdownCacheHitsAndInvalidates covers the cache the preview pane leans
// on. Rendering the same note twice must not build a second renderer, and a
// palette change must not serve output colored for the old palette.
func TestMarkdownCacheHitsAndInvalidates(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	const body = "# Title\n\nSome *body* text."
	key := noteCacheKey(1, fixedTime.Unix())

	first := renderMarkdownCached(key, body, 60)
	r1 := markdownRenderer(markdownWrapWidth(60))

	second := renderMarkdownCached(key, body, 60)
	r2 := markdownRenderer(markdownWrapWidth(60))

	if first != second {
		t.Error("two renders of the same note at the same width disagree")
	}
	if r1 != r2 {
		t.Error("the renderer was rebuilt for an unchanged (width, palette); every preview frame pays for a new goldmark chain")
	}

	// A different width must not serve the old wrap.
	if wide := renderMarkdownCached(key, body, 100); wide == first {
		t.Error("a wider pane reused the narrow render; the width is not part of the cache key")
	}

	// A palette change must not serve the old colors.
	setPalette(DefaultPalette(false))
	if light := renderMarkdownCached(key, body, 60); light == first {
		t.Error("the light palette reused the dark render; paletteGen is not part of the cache key")
	}
}

// TestNoteCacheKeyTracksUpdates states why the key carries a timestamp: a save
// keeps the note's id, so an id-only key would preview the pre-edit body for
// the rest of the session.
func TestNoteCacheKeyTracksUpdates(t *testing.T) {
	before := noteCacheKey(7, fixedTime.Unix())
	after := noteCacheKey(7, fixedTime.Add(time.Minute).Unix())
	if before == after {
		t.Fatal("editing a note produced the same cache key; the preview would keep showing the old body")
	}
}

// TestMarkdownCacheDoesNotGrowWithoutBound checks the eviction. The cache holds
// rendered markdown, which is several times the size of the source, so an
// unbounded map over a large note collection is a real memory leak.
func TestMarkdownCacheDoesNotGrowWithoutBound(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	for i := 0; i < mdOutputCap*3; i++ {
		renderMarkdownCached(noteCacheKey(int64(i), fixedTime.Unix()), "body text", 60)
	}
	if len(mdOutput) > mdOutputCap+1 {
		t.Errorf("the markdown cache holds %d entries, over the %d cap", len(mdOutput), mdOutputCap)
	}
}

// ---- Form layout -----------------------------------------------------------

// TestFormLayoutMeasuresChrome is the direct replacement for the `height - 14`
// constant. The invariant is not "the body is height-14 tall" but "the chrome
// plus the body fill the screen exactly", which holds at any size the form
// actually fits in and is what a wrapped field or an added row would break.
func TestFormLayoutMeasuresChrome(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	for _, size := range []struct{ w, h int }{
		{80, 24},
		{120, 40},
		{100, 60},
	} {
		sess := &session{user: &models.User{GUID: "u"}, width: size.w, height: size.h}
		f := newFormScreen(sess, nil)
		f.layout()

		if h := lipgloss.Height(f.View()); h != size.h {
			above, below := f.chrome()
			t.Errorf("at %dx%d the form renders %d lines, want %d (chrome above=%d body=%d below=%d)",
				size.w, size.h, h, size.h,
				lipgloss.Height(above), lipgloss.Height(f.body.View()), lipgloss.Height(below))
		}
	}
}

// TestFormLayoutSurvivesTinyTerminals covers the case the old constant handled
// by accident and would have handled wrongly: a terminal shorter than the
// chrome. The form cannot fit, but the textarea must still be usable — a
// zero-height textarea renders nothing and silently swallows typing.
func TestFormLayoutSurvivesTinyTerminals(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	for _, size := range []struct{ w, h int }{
		{40, 10},
		{20, 5},
		{20, 1},
		{1, 1},
	} {
		sess := &session{user: &models.User{GUID: "u"}, width: size.w, height: size.h}
		f := newFormScreen(sess, nil)
		f.layout()

		if got := f.body.Height(); got < minBodyHeight {
			t.Errorf("at %dx%d the body textarea is %d lines tall, under the %d-line floor",
				size.w, size.h, got, minBodyHeight)
		}
		// It must also not panic or render empty — the screen is unusable but
		// it has to stay a screen.
		if v := f.View(); strings.TrimSpace(stripANSI(v)) == "" {
			t.Errorf("at %dx%d the form rendered nothing", size.w, size.h)
		}
	}
}

// TestFormEditingHeadingDoesNotBreakLayout exercises the case the fixed
// constant got wrong: a long note title makes the heading wrap, which adds a
// line of chrome the old arithmetic never knew about.
func TestFormEditingHeadingDoesNotBreakLayout(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	long := fixtureNote(1, strings.Repeat("a very long note title ", 6), "body")
	sess := &session{user: &models.User{GUID: "u"}, width: 80, height: 24}
	f := newFormScreen(sess, &long)
	f.layout()

	if h := lipgloss.Height(f.View()); h != 24 {
		t.Errorf("a wrapping heading rendered %d lines, want 24 — the chrome measurement missed the wrap", h)
	}
}

// ---- Rune-safe filtering ---------------------------------------------------

// TestFilterValueTruncatesOnRuneBoundaries guards the byte-slice bug: cutting a
// UTF-8 string at a byte offset can land mid-character, and the invalid byte
// then flows into bubbles' rune-based matcher.
func TestFilterValueTruncatesOnRuneBoundaries(t *testing.T) {
	// Three-byte runes, so a byte-offset cut at 2000 lands inside one.
	body := strings.Repeat("日", filterBodyRunes+100)
	n := fixtureNote(1, "Multibyte", body)

	got := noteItem{note: n}.FilterValue()
	if !strings.Contains(got, "日") {
		t.Fatal("the filter value lost the body entirely")
	}
	if strings.ContainsRune(got, '�') {
		t.Error("the filter value contains a replacement character — the body was cut mid-rune")
	}
	if !hasValidUTF8Suffix(got) {
		t.Error("the filter value does not end on a valid rune boundary")
	}
}

// TestTruncateRunesRespectsTheCap checks the cap itself is counted in runes,
// not bytes — a byte cap on CJK text would keep a third of what was asked for.
func TestTruncateRunesRespectsTheCap(t *testing.T) {
	in := strings.Repeat("日", 100)
	got := truncateRunes(in, 10)
	if n := len([]rune(got)); n != 10 {
		t.Errorf("truncateRunes kept %d runes, want 10", n)
	}
	// Short input passes through untouched, including the fast path.
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Errorf("truncateRunes shortened a string under the cap: %q", got)
	}
}

func hasValidUTF8Suffix(s string) bool {
	r := []rune(s)
	return len(r) == 0 || r[len(r)-1] != '�'
}
