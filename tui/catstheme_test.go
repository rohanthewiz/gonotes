package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gonotes/cats"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// ---- fixtures ---------------------------------------------------------------

// hostColors is a complete cats theme: a dark green one, deliberately nothing
// like either built-in palette so that "did the host's colors reach the screen"
// is answerable by looking at any single field.
func hostColors() map[string]string {
	return map[string]string{
		"bg": "#0B0F0C", "fg": "#D7E4D9", "muted": "#7A8C7E",
		"line": "#1E2A20", "accent": "#4AF08C",
		"ok": "#35C46B", "warn": "#E2B23C", "err": "#E45C5C",
	}
}

func hostTheme() cats.ConfigTheme {
	return cats.ConfigTheme{Name: "cats-green", Colors: hostColors()}
}

// withHostThemeFlag restores the "whose colors are these" flag around a test.
// It is package state next to the palette itself, and a test that left it set
// would make a later one's terminal background report look like it was ignored
// on purpose — a failure that only appears in a particular test order.
func withHostThemeFlag(t *testing.T) {
	t.Helper()
	prev := hostThemed
	t.Cleanup(func() { hostThemed = prev })
}

// ---- the mapping ------------------------------------------------------------

// Every field of the palette has to come from somewhere, and this is the table
// that says where. The two derived fields are checked against the same
// expressions the default palette uses, not against literals, so the host
// palette and the built-in one can never drift apart in how a selection is
// mixed or how light/dark is decided.
func TestHostPaletteMapsEveryField(t *testing.T) {
	p, ok := catsHostPalette(hostColors())
	if !ok {
		t.Fatal("a complete host theme was rejected")
	}

	c := hostColors()
	cases := []struct{ name, got, want string }{
		{"Primary", p.Primary, c["accent"]},
		{"Subtle", p.Subtle, c["muted"]},
		{"Success", p.Success, c["ok"]},
		{"Warn", p.Warn, c["warn"]},
		{"Danger", p.Danger, c["err"]},
		{"Fg", p.Fg, c["fg"]},
		{"Bg", p.Bg, c["bg"]},
		{"Sel", p.Sel, blendHex(c["accent"], c["bg"], selAlpha)},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if !p.Dark {
		t.Errorf("a #0B0F0C background produced a light palette; every bubbles widget would be built with the wrong style set")
	}
}

// Dark is derived from the background because the theme_changed payload carries
// no flag to read — see isDarkHex. This is the assertion that the derivation
// actually discriminates rather than always answering "dark", which is what a
// broken luminance calculation looks like on the themes people mostly run.
func TestHostPaletteDarkFollowsTheBackground(t *testing.T) {
	cases := []struct {
		bg   string
		dark bool
	}{
		{"#000000", true},
		{"#0B0F0C", true},
		{"#1C1C1C", true},
		{"#FFFFFF", false},
		{"#FFFDF7", false},
		// Mid-gray either side of the midpoint. Included because the threshold
		// is the one number in isDarkHex that is a choice rather than a
		// consequence, and a silent move would be invisible everywhere else.
		{"#707070", true},
		{"#909090", false},
	}
	for _, tc := range cases {
		colors := hostColors()
		colors["bg"] = tc.bg
		p, ok := catsHostPalette(colors)
		if !ok {
			t.Fatalf("bg=%s was rejected", tc.bg)
		}
		if p.Dark != tc.dark {
			t.Errorf("bg=%s gave Dark=%v, want %v", tc.bg, p.Dark, tc.dark)
		}
	}
}

// All or nothing: a theme that cannot fill every field GoNotes reads is
// abandoned whole. Half the host's colors and half ours reads as a rendering
// fault, and the built-in palette is a perfectly good answer.
//
// The rgba() case is not hypothetical — cats emits it for every translucent key
// it derives, and a host that hand-authored one of the core colors that way is
// one config edit away.
func TestHostPaletteIsAllOrNothing(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, ok := catsHostPalette(nil); ok {
			t.Error("a theme with no colors produced a palette")
		}
		if _, ok := catsHostPalette(map[string]string{}); ok {
			t.Error("an empty color map produced a palette")
		}
	})

	// Every key the mapping reads, one at a time — so adding a field to the
	// palette without adding it here cannot pass by accident.
	for _, k := range catsThemeCore {
		t.Run("missing "+k.host, func(t *testing.T) {
			colors := hostColors()
			delete(colors, k.host)
			if _, ok := catsHostPalette(colors); ok {
				t.Errorf("a theme missing %q produced a palette", k.host)
			}
		})
		t.Run("rgba "+k.host, func(t *testing.T) {
			colors := hostColors()
			colors[k.host] = "rgba(74, 240, 140, 0.3)"
			if _, ok := catsHostPalette(colors); ok {
				t.Errorf("a translucent %q produced a palette; a terminal cell cannot be translucent", k.host)
			}
		})
	}
}

// line is required by cats and unread here. Rejecting a theme over a key
// GoNotes never draws with would refuse usable palettes for nothing.
func TestHostPaletteIgnoresKeysItDoesNotDraw(t *testing.T) {
	colors := hostColors()
	delete(colors, "line")
	if _, ok := catsHostPalette(colors); !ok {
		t.Error("a theme without a line color was rejected, but nothing here draws one")
	}
}

// ---- the startup fetch ------------------------------------------------------

// The payoff of doing this synchronously: by the time anything is constructed,
// the palette is the host's. The assertion is on the package palette rather
// than on a return value because that is what every widget built afterwards
// reads.
func TestStartupThemeArrivesBeforeTheModel(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)

	srv := newControlServer(t)
	srv.setTheme(hostTheme())
	sink := newHookSink(t)
	inCatsPane(t, "w1:p7", srv.path, sink.path)

	catsThemeAtStartup()
	srv.wantMethod(t, cats.MethodConfigGet)

	if pal.Primary != "#4AF08C" {
		t.Errorf("the palette is still %q; the first frame would be painted in GoNotes' own colors", pal.Primary)
	}
	if !hostThemed {
		t.Error("the host palette was installed without recording that the host owns it; the terminal's background report would overwrite it")
	}

	// The fetch is the whole conversation at this point: probing first would be
	// two round trips to answer one question, and a config.get that fails is
	// already the negative answer.
	if srv.saw(cats.MethodPing) {
		t.Error("the startup theme fetch pinged first; that is a second round trip for an answer the fetch itself gives")
	}
}

// Outside cats there is no socket and nothing to ask, so the launch may not pay
// for the attempt — this is the Tier-0 path, which is every other terminal.
func TestStartupThemeIsInertOutsideCats(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)
	notInCats(t)

	before := pal
	catsThemeAtStartup()

	if pal != before {
		t.Errorf("the palette changed outside cats: %+v", pal)
	}
	if hostThemed {
		t.Error("hostThemed was set with no host")
	}
}

// A cats that is not answering must not hold the launch beyond the probe budget
// or leave a half-applied palette. The socket path exists as a directory entry
// with nothing listening, which is what a crashed cats leaves behind.
func TestStartupThemeSurvivesADeadSocket(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)
	sink := newHookSink(t)
	inCatsPane(t, "w1:p7", catsSockPath(t, "dead.sock"), sink.path)

	before := pal
	start := time.Now()
	catsThemeAtStartup()
	elapsed := time.Since(start)

	if pal != before {
		t.Errorf("a failed fetch changed the palette: %+v", pal)
	}
	if hostThemed {
		t.Error("a failed fetch claimed the host owns the palette")
	}
	// Generous next to ProbeTimeout: the assertion is that the launch is
	// bounded at all, not that a dial fails in any particular number of
	// milliseconds on a loaded CI box.
	if elapsed > 3*time.Second {
		t.Errorf("the startup fetch blocked the launch for %v", elapsed)
	}
}

// ---- theme_changed ----------------------------------------------------------

// The live path, at the seam: a frame off the stream becomes a new palette plus
// the restyle broadcast. Everything downstream of paletteChangedMsg is the
// Phase 3 machinery, which has its own tests.
func TestThemeChangedInstallsTheHostPalette(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)

	cs := newCatsState()
	cmd := cs.frame(themeEvent(t, hostTheme()))
	if cmd == nil {
		t.Fatal("a theme change produced no command; nothing holding derived state would repaint")
	}
	if _, ok := cmd().(paletteChangedMsg); !ok {
		t.Fatalf("expected paletteChangedMsg, got %T", cmd())
	}
	if pal.Primary != "#4AF08C" || pal.Bg != "#0B0F0C" {
		t.Errorf("the host palette did not land: %+v", pal)
	}
}

// cats broadcasts its effective appearance after ANY config change, including
// ones that touched no color at all. A restyle rebuilds every bubbles widget in
// the stack and flushes the rendered-markdown cache, so a repeat that changes
// nothing has to cost nothing.
func TestRepeatedThemeEventDoesNotRestyle(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)

	cs := newCatsState()
	if cmd := cs.frame(themeEvent(t, hostTheme())); cmd == nil {
		t.Fatal("the first theme event should install the palette")
	}
	genAfterFirst := paletteGen

	if cmd := cs.frame(themeEvent(t, hostTheme())); cmd != nil {
		t.Errorf("a repeat of the theme already on screen produced %T", cmd())
	}
	if paletteGen != genAfterFirst {
		t.Errorf("paletteGen moved (%d → %d) for an identical theme; every cached markdown body was thrown away for nothing",
			genAfterFirst, paletteGen)
	}
}

// Two shapes of frame that must not disturb the screen: a name from a newer
// cats than this binary knows, and a payload that does not decode. Both are
// dropped in silence — the event vocabulary grows on the host's schedule.
func TestUnusableFramesAreDropped(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)

	cs := newCatsState()
	before := pal

	if cmd := cs.frame(cats.Event{Name: "some_event_from_a_newer_cats"}); cmd != nil {
		t.Errorf("an unknown event produced %T", cmd())
	}
	if cmd := cs.frame(cats.Event{
		Name: cats.EventThemeChanged,
		Data: json.RawMessage(`{"colors": "not an object"}`),
	}); cmd != nil {
		t.Errorf("an undecodable theme payload produced %T", cmd())
	}
	// A well-formed frame whose colors cannot make a palette: the theme is
	// abandoned, and the palette on screen is untouched rather than partly
	// replaced.
	if cmd := cs.frame(themeEvent(t, cats.ConfigTheme{
		Name:   "half-authored",
		Colors: map[string]string{"bg": "#101010", "accent": "rgba(0,0,0,0.3)"},
	})); cmd != nil {
		t.Errorf("an incomplete theme produced %T", cmd())
	}
	if pal != before {
		t.Errorf("a dropped frame still changed the palette: %+v", pal)
	}
}

// THE CLOBBER. tea.BackgroundColorMsg is not a once-per-program event — a
// repaint or a focus re-announcement can bring another — and inside cats the
// answer comes from cats' own emulator, reporting the background the host theme
// just gave us. Acting on it would swap the whole host palette for the built-in
// one chosen by a single bit derived from that same background, so the theme
// sync would survive exactly until the terminal mentioned its background again.
func TestTerminalBackgroundDoesNotOverrideTheHost(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)

	if !applyHostTheme(hostTheme()) {
		t.Fatal("the host theme did not install")
	}
	hostPal := pal

	spy := &spyScreen{}
	m := appModel{sess: &session{width: 80, height: 24, cats: newCatsState()}, stack: []screen{spy}}

	// A light background — the maximally disruptive report, since acting on it
	// would flip every widget's style set as well as the colors.
	if _, cmd := m.Update(tea.BackgroundColorMsg{Color: white{}}); cmd != nil {
		t.Errorf("the terminal's background report produced %T while wearing the host's colors", cmd())
	}
	if pal != hostPal {
		t.Errorf("the host palette was overwritten by the terminal's background report: %+v", pal)
	}
	if spy.restyled != 0 {
		t.Errorf("the screen was restyled %d times for a report that should have been ignored", spy.restyled)
	}
}

// Without a host, the terminal is still the authority: this is the same path
// every non-cats launch takes, and the guard above must not have disabled it.
func TestTerminalBackgroundStillWinsWithoutAHost(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)
	hostThemed = false

	m := appModel{sess: &session{width: 80, height: 24, cats: newCatsState()}, stack: []screen{&spyScreen{}}}
	if _, cmd := m.Update(tea.BackgroundColorMsg{Color: white{}}); cmd == nil {
		t.Fatal("a background report outside cats produced no command")
	}
	if pal.Dark {
		t.Error("the light background did not install the light palette")
	}
}

// ---- live, through the program ---------------------------------------------

// The end-to-end assertion, made on bytes the terminal would receive: a theme
// change arriving on the stream repaints the running program in the host's
// accent. The login screen's heading renders that accent as a BACKGROUND, so
// the color reaches the output as a truecolor SGR run that can be matched
// exactly.
//
// It runs against the real root Update — event → frame → palette →
// paletteChangedMsg → restyle — rather than calling the pieces, because the
// order those happen in is the part that has been wrong before.
func TestThemeChangeRepaintsTheRunningProgram(t *testing.T) {
	withPalette(t, DefaultPalette(true))
	withHostThemeFlag(t)
	notInCats(t) // no startup fetch: the change has to be what does the work

	fs := newFakeStore()
	m := newAppModel(fs)

	// The color profile has to be forced. Bubble Tea picks one from the output,
	// and teatest's output is a pipe rather than a terminal — so the program
	// degrades to Ascii and writes the frame with every SGR run stripped. That
	// is correct behavior and it makes a color assertion unfalsifiable, which is
	// worse than no test: it would pass with the theme sync deleted.
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(100, 40),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.TrueColor)))
	tm.Send(tea.WindowSizeMsg{Width: 100, Height: 40})

	// #7D79F6, the dark default's accent: the frame we are about to replace.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "125;121;246")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(catsEventMsg{ev: themeEvent(t, hostTheme())})

	// #4AF08C as a background fill — the host's accent, on screen.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "48;2;74;240;140")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// themeEvent builds a theme_changed frame the way the stream delivers one:
// the payload as raw JSON, so the decode in catsThemeChanged is exercised
// rather than bypassed.
func themeEvent(t *testing.T, theme cats.ConfigTheme) cats.Event {
	t.Helper()
	raw, err := json.Marshal(theme)
	if err != nil {
		t.Fatalf("marshal theme: %v", err)
	}
	return cats.Event{Name: cats.EventThemeChanged, Data: raw}
}
