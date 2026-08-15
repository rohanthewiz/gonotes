package tui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gonotes/models"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// ---- Helpers shared by the palette and layout suites -----------------------

// ansiRE matches CSI sequences — the SGR color runs lipgloss emits. Tests that
// assert on *text* strip them; tests that assert on *color* look for them.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;:]*[a-zA-Z]")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// withPalette installs p for the duration of a test and restores whatever was
// active afterwards. The palette is package state — deliberately, since every
// style in the process derives from it — so a test that switches to light and
// leaves it there would break any later test asserting dark output, and only
// when the two happened to run in that order.
func withPalette(t *testing.T, p Palette) {
	t.Helper()
	prev := pal
	setPalette(p)
	t.Cleanup(func() { setPalette(prev) })
}

// ---- parseHex / blendHex ---------------------------------------------------

func TestParseHex(t *testing.T) {
	cases := []struct {
		in         string
		r, g, b    uint8
		ok         bool
		annotation string
	}{
		{in: "#7D79F6", r: 0x7D, g: 0x79, b: 0xF6, ok: true},
		{in: "7D79F6", r: 0x7D, g: 0x79, b: 0xF6, ok: true, annotation: "the # is optional"},
		{in: "  #7d79f6  ", r: 0x7D, g: 0x79, b: 0xF6, ok: true, annotation: "case and surrounding space"},
		{in: "#f0a", r: 0xFF, g: 0x00, b: 0xAA, ok: true, annotation: "shorthand doubles each nibble"},
		{in: "", ok: false},
		{in: "#12345", ok: false, annotation: "five digits is not a color"},
		{in: "#GGGGGG", ok: false},
		{in: "red", ok: false, annotation: "named colors are rejected, not guessed at"},
		{in: "5", ok: false, annotation: "an ANSI index is not hex"},
	}

	for _, tc := range cases {
		name := tc.in
		if tc.annotation != "" {
			name += " — " + tc.annotation
		}
		t.Run(name, func(t *testing.T) {
			r, g, b, ok := parseHex(tc.in)
			if ok != tc.ok {
				t.Fatalf("parseHex(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			}
			if ok && (r != tc.r || g != tc.g || b != tc.b) {
				t.Errorf("parseHex(%q) = %02X%02X%02X, want %02X%02X%02X",
					tc.in, r, g, b, tc.r, tc.g, tc.b)
			}
		})
	}
}

func TestBlendHex(t *testing.T) {
	cases := []struct {
		name   string
		fg, bg string
		alpha  float64
		want   string
	}{
		{"fully opaque yields the foreground", "#FF0000", "#000000", 1.0, "#FF0000"},
		{"fully transparent yields the background", "#FF0000", "#000000", 0.0, "#000000"},
		{"half and half", "#FFFFFF", "#000000", 0.5, "#808080"},
		// 0x7D*0.3 + 0x1C*0.7 = 57 (0x39), and so on per channel.
		{"the sel recipe: 30% accent over a dark ground", "#7D79F6", "#1C1C1C", 0.30, "#39385D"},
		{"unparseable foreground falls through unchanged", "chartreuse", "#000000", 0.3, "chartreuse"},
		{"unparseable background falls back to the foreground", "#FF0000", "nope", 0.3, "#FF0000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blendHex(tc.fg, tc.bg, tc.alpha); !strings.EqualFold(got, tc.want) {
				t.Errorf("blendHex(%q, %q, %v) = %q, want %q", tc.fg, tc.bg, tc.alpha, got, tc.want)
			}
		})
	}
}

// TestDefaultPaletteIsAllHex guards the invariant every consumer relies on:
// lipgloss.Color and glamour's *string color fields both take hex and neither
// reports a parse failure — an unparseable color renders as "no color" and the
// only symptom is a style that quietly stops applying.
func TestDefaultPaletteIsAllHex(t *testing.T) {
	for _, dark := range []bool{true, false} {
		p := DefaultPalette(dark)
		fields := map[string]string{
			"Primary": p.Primary, "Subtle": p.Subtle, "Success": p.Success,
			"Danger": p.Danger, "Warn": p.Warn, "Fg": p.Fg, "Bg": p.Bg, "Sel": p.Sel,
		}
		for name, v := range fields {
			if !isHexColor(v) {
				t.Errorf("DefaultPalette(dark=%v).%s = %q, which is not a usable hex color", dark, name, v)
			}
		}
	}
}

// TestDefaultPaletteSelIsDerived states where Sel comes from, because Phase 6's
// host-theme mapping has to reproduce it: a host supplies an accent and a
// background but no selection color, and the fill it gets must be the same
// blend the default palette uses.
func TestDefaultPaletteSelIsDerived(t *testing.T) {
	for _, dark := range []bool{true, false} {
		p := DefaultPalette(dark)
		want := blendHex(p.Primary, p.Bg, selAlpha)
		if p.Sel != want {
			t.Errorf("DefaultPalette(dark=%v).Sel = %q, want the accent blended over the background at %v: %q",
				dark, p.Sel, selAlpha, want)
		}
	}
}

// ---- setPalette ------------------------------------------------------------

// TestSetPaletteReportsChange covers the early return that keeps a repeated
// background report from rebuilding every widget in the stack. The generation
// counter must not move either — it is the markdown cache's key, so bumping it
// for an identical palette throws away every rendered body for nothing.
func TestSetPaletteReportsChange(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	genBefore := paletteGen
	if setPalette(DefaultPalette(true)) {
		t.Error("setPalette reported a change for an identical palette")
	}
	if paletteGen != genBefore {
		t.Errorf("paletteGen moved (%d → %d) for an identical palette; the markdown cache was flushed for nothing",
			genBefore, paletteGen)
	}

	if !setPalette(DefaultPalette(false)) {
		t.Fatal("setPalette did not report the dark → light switch")
	}
	if paletteGen == genBefore {
		t.Error("paletteGen did not move on a real palette change; cached markdown would keep the old colors")
	}
	if pal.Dark {
		t.Error("pal.Dark is still true after switching to the light palette")
	}
}

// TestSetPaletteRebuildsStyles proves the styles are actually derived from the
// palette rather than captured once at init. Rendering the same text under both
// palettes must produce different bytes; if applyPalette were skipped the two
// would be identical and every light-terminal user would get dark styling.
func TestSetPaletteRebuildsStyles(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	dark := labelFocusedStyle.Render("x")

	setPalette(DefaultPalette(false))
	light := labelFocusedStyle.Render("x")

	if dark == light {
		t.Fatalf("the focused-label style renders identically under both palettes (%q) — applyPalette is not running", dark)
	}
	if !strings.Contains(dark, "125;121;246") { // #7D79F6
		t.Errorf("dark focused label does not carry the dark accent: %q", dark)
	}
	if !strings.Contains(light, "90;86;224") { // #5A56E0
		t.Errorf("light focused label does not carry the light accent: %q", light)
	}
}

// ---- The restyle broadcast -------------------------------------------------

// spyScreen is a screen that records restyle calls. Used to assert the
// broadcast's *reach* — every screen in the stack, not just the top one —
// separately from what any particular screen does about it.
type spyScreen struct{ restyled int }

func (s *spyScreen) Init() tea.Cmd                    { return nil }
func (s *spyScreen) Update(tea.Msg) (screen, tea.Cmd) { return s, nil }
func (s *spyScreen) View() string                     { return "spy" }
func (s *spyScreen) restyle()                         { s.restyled++ }

// TestBackgroundColorMsgBroadcastsRestyle walks the whole path the terminal's
// OSC 11 reply takes: root Update installs the palette and emits
// paletteChangedMsg, and handling that message reaches every screen in the
// stack — including buried ones, which is the bug the loop exists to prevent
// (a screen resumed after a pop would otherwise still be painted dark).
func TestBackgroundColorMsgBroadcastsRestyle(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	sess := &session{width: 100, height: 30}
	buried, top := &spyScreen{}, &spyScreen{}
	m := appModel{sess: sess, stack: []screen{buried, newLoginScreen(sess), top}}

	// A light background. lipgloss reads IsDark() off the color itself, so the
	// message carries a color rather than a flag.
	updated, cmd := m.Update(tea.BackgroundColorMsg{Color: white{}})
	if cmd == nil {
		t.Fatal("a background change produced no command; nothing would restyle")
	}
	if pal.Dark {
		t.Fatal("the light background did not install the light palette")
	}
	if _, ok := cmd().(paletteChangedMsg); !ok {
		t.Fatalf("expected paletteChangedMsg, got %T", cmd())
	}

	updated.Update(paletteChangedMsg{})

	if buried.restyled != 1 {
		t.Errorf("the buried screen was restyled %d times, want 1 — a broadcast to the top screen only looks exactly like this",
			buried.restyled)
	}
	if top.restyled != 1 {
		t.Errorf("the top screen was restyled %d times, want 1", top.restyled)
	}
}

// TestIdenticalBackgroundReportDoesNotBroadcast covers the other half: a
// terminal that re-announces the background it already reported must not cost a
// full restyle. tea.BackgroundColorMsg is not a once-per-program event.
func TestIdenticalBackgroundReportDoesNotBroadcast(t *testing.T) {
	withPalette(t, DefaultPalette(false)) // already light

	spy := &spyScreen{}
	m := appModel{sess: &session{width: 80, height: 24}, stack: []screen{spy}}

	if _, cmd := m.Update(tea.BackgroundColorMsg{Color: white{}}); cmd != nil {
		t.Errorf("a repeat report of the current background produced %T; it should be ignored", cmd())
	}
	if spy.restyled != 0 {
		t.Errorf("a repeat report restyled %d screens, want 0", spy.restyled)
	}
}

// TestRestyleRebuildsListDelegate is the specific trap this machinery exists
// for: bubbles copies the delegate's style set into the list at construction,
// so a list built under the dark palette keeps drawing dark rows forever unless
// the delegate is rebuilt.
//
// The assertion is on an *unselected* row's title color, because that is the
// one thing on screen the delegate alone owns. The selected row and the item
// descriptions are colored by this package's own styles, which are re-read
// every frame and so would repaint with or without restyle — asserting on them
// would make this test pass even with applyListStyles deleted.
func TestRestyleRebuildsListDelegate(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	// bubbles' own defaults, from list.NewDefaultItemStyles.
	const darkTitleFg = "221;221;221" // #DDDDDD
	const lightTitleFg = "26;26;26"   // #1A1A1A

	sess := &session{width: 80, height: 24}
	s := newBrowseScreen(sess)
	s.layout()
	s.list.SetItems([]list.Item{
		noteItem{note: fixtureNote(1, "Alpha note", "body")},
		noteItem{note: fixtureNote(2, "Beta note", "body")}, // unselected
	})

	if v := s.list.View(); !strings.Contains(v, darkTitleFg) {
		t.Fatalf("the list did not start with bubbles' dark row color %s", darkTitleFg)
	}

	// A bare palette switch must NOT reach the delegate. Stating the trap as an
	// assertion means that if bubbles ever starts reading its styles per frame,
	// this fails and says so rather than leaving dead machinery in place.
	setPalette(DefaultPalette(false))
	if v := s.list.View(); !strings.Contains(v, darkTitleFg) {
		t.Fatal("the delegate repainted on a bare palette change; if bubbles now reads its styles per frame, " +
			"applyListStyles and the restyler broadcast are no longer needed for it")
	}

	s.restyle()
	light := s.list.View()
	if strings.Contains(light, darkTitleFg) {
		t.Error("restyle() left the dark row color in place — the delegate was not rebuilt")
	}
	if !strings.Contains(light, lightTitleFg) {
		t.Errorf("restyle() did not install bubbles' light row color %s", lightTitleFg)
	}
}

// TestFormRestyleRebuildsWidgetStyles is the same guarantee for the form's five
// widgets. It asserts on the style sets rather than on rendered output on
// purpose: as of bubbles v2.1.1 textinput's light and dark sets happen to render
// a focused, empty input identically (they differ in the blurred and cursor
// styles), so a View() comparison would pass whether or not restyle ran.
func TestFormRestyleRebuildsWidgetStyles(t *testing.T) {
	withPalette(t, DefaultPalette(true))

	sess := &session{user: &models.User{GUID: "u"}, width: 80, height: 24}
	f := newFormScreen(sess, nil)

	dark := f.title.Styles()

	setPalette(DefaultPalette(false))
	f.restyle()
	light := f.title.Styles()

	if reflect.DeepEqual(dark, light) {
		t.Error("the title input's style set is unchanged after restyle; it still holds the dark defaults")
	}
	if got := f.body.Styles(); reflect.DeepEqual(got, textarea.DefaultStyles(true)) {
		t.Error("the body textarea still holds the dark default styles after restyle")
	}
}

// white is a color.Color reporting a light background, which is how
// tea.BackgroundColorMsg conveys the terminal's OSC 11 answer.
type white struct{}

func (white) RGBA() (r, g, b, a uint32) { return 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF }
