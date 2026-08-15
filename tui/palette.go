package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// Palette is the single source of color truth for the TUI. Everything that
// draws — the lipgloss styles in styles.go, the bubbles widget style sets, and
// the glamour markdown renderer — is derived from one of these.
//
// Why hex strings rather than color.Color values:
//
//   - glamour's ansi.StylePrimitive takes colors as *string hex and nothing
//     else, so a color.Color palette would need converting back on every
//     renderer rebuild.
//   - a struct of strings is comparable with ==, which is what lets setPalette
//     cheaply detect "nothing actually changed" and skip the restyle broadcast.
//     Comparing color.Color means unwrapping RGBA() field by field.
//   - the cats host theme (Phase 6) hands over hex strings; keeping the
//     internal form identical makes that mapping a straight assignment with a
//     validation gate rather than a conversion layer.
//
// lipgloss.Color(hex) turns each one into a color.Color at style-build time,
// which happens once per palette change, not once per frame.
type Palette struct {
	// Dark records which background the palette was built for. Every bubbles
	// v2 widget constructor needs telling (they hardcode a dark style set), so
	// this travels with the colors rather than living in its own global.
	Dark bool

	Primary string // brand accent: titles, focused labels, selected rows, links
	Subtle  string // secondary text: dates, tags, help lines
	Success string
	Danger  string
	Warn    string

	// Fg and Bg are the terminal's own text/background colors. The TUI paints
	// no background of its own (a transparent app is the right default in a
	// terminal), but both are needed anyway: Fg becomes glamour's document text
	// color, and Bg is the surface Sel is blended against.
	Fg string
	Bg string

	// Sel is the selection fill — Primary composited over Bg at 30% opacity.
	// Derived rather than hand-picked so that a host-supplied palette
	// (Phase 6), which provides an accent and a background but no selection
	// color, produces a fill that actually sits on its own background.
	Sel string
}

// pal is the active palette. Read at style-build time and at every bubbles
// widget construction site; never written except through setPalette.
var pal Palette

// paletteGen increments on every accepted palette change. It is the
// invalidation key for anything that caches rendered output — see markdown.go,
// where a glamour renderer built for the old colors would otherwise be reused
// forever.
var paletteGen uint64

func init() {
	// Dark until the terminal says otherwise. This matches what bubbles v2
	// hardcodes in its widget defaults, so the first frame is self-consistent
	// even if the background query is never answered. See the long note in
	// styles.go for why detection is asynchronous.
	setPalette(DefaultPalette(true))
}

// DefaultPalette returns the built-in palette for a light or dark background.
func DefaultPalette(dark bool) Palette {
	p := Palette{Dark: dark}
	if dark {
		p.Primary = "#7D79F6"
		p.Subtle = "#6C6C6C"
		p.Success = "#7CD992"
		p.Danger = "#FF8A80"
		p.Warn = "#FFC46B"
		p.Fg = "#E4E4E4"
		p.Bg = "#1C1C1C"
	} else {
		p.Primary = "#5A56E0"
		p.Subtle = "#8A8A8A"
		p.Success = "#2E7D32"
		p.Danger = "#C62828"
		p.Warn = "#B26A00"
		p.Fg = "#1C1C1C"
		p.Bg = "#FFFFFF"
	}
	p.Sel = blendHex(p.Primary, p.Bg, selAlpha)
	return p
}

// selAlpha is the opacity the accent is composited at to produce the selection
// fill. 0.30 is cats' own sel-fill recipe, kept identical so a gonotes pane
// inside cats and the cats UI around it agree on how a selection looks.
const selAlpha = 0.30

// setPalette installs a palette and rebuilds every derived style. It reports
// whether anything actually changed, which is what the caller uses to decide
// between broadcasting a restyle and doing nothing.
//
// The no-change early return matters more than it looks: tea.BackgroundColorMsg
// can arrive on any terminal repaint, and Phase 6's theme_changed events fire
// for palette-adjacent host settings too. Rebuilding the bubbles delegates and
// dropping the markdown cache on every one of those would be visible work for
// no visual difference.
func setPalette(p Palette) bool {
	if p == pal {
		return false
	}
	pal = p
	paletteGen++
	applyPalette(p)
	return true
}

// blendHex composites fg over bg at the given alpha and returns the result as
// a hex string. Straight per-channel linear interpolation in sRGB space — not
// colorimetrically correct, but it is what terminals and cats itself do, and
// matching the host's arithmetic matters more here than being right in the
// abstract.
//
// Either input failing to parse yields fg unchanged: a selection fill that is
// merely too saturated is survivable, one that is silently black is not.
func blendHex(fg, bg string, alpha float64) string {
	fr, fgn, fb, ok := parseHex(fg)
	if !ok {
		return fg
	}
	br, bgn, bb, ok := parseHex(bg)
	if !ok {
		return fg
	}
	mix := func(a, b uint8) uint8 {
		return uint8(float64(a)*alpha + float64(b)*(1-alpha) + 0.5)
	}
	return fmt.Sprintf("#%02X%02X%02X", mix(fr, br), mix(fgn, bgn), mix(fb, bb))
}

// parseHex accepts "#RGB" and "#RRGGBB" (the leading # optional) and reports
// whether the parse succeeded. Anything else — a named color, an ANSI index, a
// malformed string from a host theme — is rejected rather than guessed at.
func parseHex(s string) (r, g, b uint8, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(s) {
	case 3:
		// Shorthand: each nibble is doubled, so "#f0a" means "#ff00aa".
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
	default:
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), true
}

// isHexColor reports whether s is a hex color this package can use. Phase 6's
// host-theme mapping gates on this: if any core color arrives in a form we
// cannot parse, the whole synthesis is abandoned in favor of the default
// palette rather than half-applied.
func isHexColor(s string) bool {
	_, _, _, ok := parseHex(s)
	return ok
}
