package tui

import (
	"encoding/json"

	"gonotes/cats"

	tea "charm.land/bubbletea/v2"
)

// Wearing the host's colors: GoNotes running in a cats pane paints itself from
// the same palette the rest of that window uses, so the pane reads as part of
// the session rather than as a foreign application pasted into it.
//
// THE MAPPING. cats' theme vocabulary is a flat map of CSS custom-property
// names; this package's Palette is eight fields. Seven of them are a rename,
// and the other two are computed:
//
//	host      Palette   note
//	accent →  Primary
//	fg     →  Fg
//	bg     →  Bg
//	muted  →  Subtle
//	ok     →  Success
//	warn   →  Warn
//	err    →  Danger
//	line   →  (unused)  GoNotes draws its rules in Subtle and its focus
//	                    borders in Primary; it has no separate line color, and
//	                    requiring a key nothing reads would reject usable
//	                    themes for no benefit.
//	(none) →  Sel       accent over bg at selAlpha — cats' own sel-fill recipe,
//	                    so a GoNotes selection and a cats selection in the next
//	                    pane are the same color rather than two guesses at one.
//	(none) →  Dark      derived from the background's luminance; see isDarkHex.
//
// HEX ONLY, AND ALL OR NOTHING. cats emits rgba() for its translucent keys, and
// a terminal cell is one color or another — there is nothing to composite
// against. A missing or non-hex value in any of the seven keys above abandons
// the whole synthesis rather than shipping a palette that is part the host's
// and part ours: a half-translated theme looks like a rendering fault, and the
// built-in palette is a perfectly good answer.
//
// WHERE IT ARRIVES FROM, twice:
//
//	startup   catsThemeAtStartup — one synchronous config.get before the model
//	          exists, so the first frame is already in the host's colors.
//	live      theme_changed on the event stream → catsThemeChanged → the
//	          Phase 3 restyle broadcast.
//
// Both paths go through applyHostTheme, which is what keeps them from drifting.

// catsThemeCore is the mapping, in the order it is read. All seven keys must be
// present and hex for the synthesis to succeed.
//
// A slice of setters rather than a struct literal per key so that the "every
// key is required" rule is expressed once, as a loop, instead of seven times.
var catsThemeCore = []struct {
	host string
	set  func(*Palette, string)
}{
	{"bg", func(p *Palette, v string) { p.Bg = v }},
	{"fg", func(p *Palette, v string) { p.Fg = v }},
	{"muted", func(p *Palette, v string) { p.Subtle = v }},
	{"accent", func(p *Palette, v string) { p.Primary = v }},
	{"ok", func(p *Palette, v string) { p.Success = v }},
	{"warn", func(p *Palette, v string) { p.Warn = v }},
	{"err", func(p *Palette, v string) { p.Danger = v }},
}

// catsHostPalette turns a host theme's color map into a Palette. ok is false
// when the host's colors cannot produce a complete one — see the file comment.
func catsHostPalette(colors map[string]string) (Palette, bool) {
	if len(colors) == 0 {
		return Palette{}, false
	}
	var p Palette
	for _, k := range catsThemeCore {
		v := colors[k.host]
		if !isHexColor(v) {
			return Palette{}, false
		}
		k.set(&p, v)
	}
	// Both derived fields read Bg and Primary, so they are computed after the
	// loop rather than inside it.
	p.Sel = blendHex(p.Primary, p.Bg, selAlpha)
	p.Dark = isDarkHex(p.Bg)
	return p, true
}

// isDarkHex reports whether a background color is a dark one. Every bubbles v2
// widget constructor has to be told which it is (they carry two hardcoded style
// sets), so Palette.Dark has to be answered for a host theme too.
//
// It is derived rather than read because the wire does not carry it where it is
// needed. config.get's theme registry does name each theme's dark flag — but
// theme_changed's payload is the effective appearance alone, with no flag and
// no registry. Deriving in both places means a live theme change and a fresh
// launch on the same theme always agree; reading the authoritative flag on the
// one path that has it would let them disagree, and the symptom would be widget
// styling that depends on how the session happened to reach its current theme.
//
// Rec. 709 luma on the sRGB values as they come, with no linearization: the
// same non-linear arithmetic blendHex does, chosen for the same reason —
// agreeing with what the host and the terminal do matters more here than being
// colorimetrically right. The midpoint is the threshold, which is the only
// place it can be without inventing a preference.
func isDarkHex(hex string) bool {
	r, g, b, ok := parseHex(hex)
	if !ok {
		// Unreachable through catsHostPalette, which gates on hex first. Dark
		// is the answer anywhere else: it is what the built-in palette and the
		// bubbles defaults both start from.
		return true
	}
	lum := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
	return lum < 128
}

// hostThemed records that the colors on screen were synthesized from the host's
// theme rather than chosen here. It is package state for the same reason the
// palette itself is: there is one set of colors in a process.
//
// Its one consumer is the terminal background report — see hostOwnsThePalette.
var hostThemed bool

// hostOwnsThePalette reports whether the active palette came from the cats host.
//
// The root Update consults this before acting on tea.BackgroundColorMsg, and
// the reason is specific to running inside cats: the OSC 11 reply comes from
// cats' own terminal emulator, so it reports the background the host theme just
// gave us. Acting on it would replace a full host palette with the built-in one
// selected by a single light/dark bit derived from that same background — the
// theme sync undone by the first repaint that re-announces the background.
func hostOwnsThePalette() bool { return hostThemed }

// applyHostTheme installs a palette synthesized from a host theme and reports
// whether anything actually changed. Both arrival paths funnel through here.
//
// hostThemed is set on a successful synthesis rather than on a successful
// change: what it records is whose colors we are wearing, and a host theme that
// happens to match what is already on screen is still the host's.
func applyHostTheme(t cats.ConfigTheme) bool {
	p, ok := catsHostPalette(t.Colors)
	if !ok {
		return false
	}
	hostThemed = true
	return setPalette(p)
}

// catsThemeAtStartup fetches the host's palette before the model is built, so
// the FIRST frame is already in the host's colors.
//
// This is the one control call GoNotes makes synchronously, and it is why Run
// calls it before newAppModel rather than after: bubbles widgets copy their
// style set in at construction, and newAppModel constructs the login screen. A
// theme that landed a moment later would still repaint — the restyle broadcast
// exists — but the launch would open on a frame in the wrong colors.
//
// The cost is bounded by ProbeTimeout, half a second in the worst case, and
// only ever inside a cats pane where the socket is local. Fetching on a
// goroutine instead would save milliseconds nobody can perceive and buy a
// visible flash on every launch.
//
// It deliberately does not probe first: a ping followed by a config.get is two
// round trips to answer one question, and a config.get that fails IS the
// negative answer. The real probe runs later, as a command, and decides Tier 1
// for everything else.
//
// Detection is repeated here rather than read off catsState because the state
// does not exist yet — that is the whole point of running this early — and the
// env sniff it duplicates is four getenv calls.
func catsThemeAtStartup() {
	env := cats.DetectEnv()
	if !env.InCats || env.ControlSocket == "" {
		return
	}
	client := &cats.Client{Socket: env.ControlSocket, Timeout: cats.ProbeTimeout}
	res, err := client.ConfigGet()
	if err != nil {
		return
	}
	applyHostTheme(res.Theme)
}

// catsThemeChanged handles one theme_changed frame. Runs on the event loop
// (the stream's reader goroutine posts it as a message — see cats_glue.go),
// which is what makes it safe to touch the package palette and the styles
// derived from it at all.
//
// A payload that does not decode is dropped in silence for the same reason an
// unknown event name is: the host's vocabulary grows on its own schedule, and a
// client that complained about it would turn every cats upgrade into noise.
//
// The nil return when nothing changed is load-bearing. cats broadcasts its
// effective appearance after any config change, including ones that touched no
// color at all, and a restyle rebuilds every bubbles widget in the stack and
// flushes the markdown cache. setPalette's own comparison is what catches that.
func catsThemeChanged(ev cats.Event) tea.Cmd {
	var t cats.ThemeChangedEvent
	if err := json.Unmarshal(ev.Data, &t); err != nil {
		return nil
	}
	if !applyHostTheme(t) {
		return nil
	}
	return paletteChanged()
}
