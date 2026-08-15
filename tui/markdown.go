package tui

import (
	"strconv"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

// Markdown rendering, with two levels of cache.
//
// Why caching is not premature here. glamour v2 dropped WithAutoStyle(), which
// used to run its own terminal background detection — meaning a renderer built
// per call used to be a *correctness* problem (a second detection that could
// disagree with lipgloss's) as well as a cost. That is gone: the style comes
// from the palette now. What remains is the cost, and the browse screen's wide
// layout turned it from academic into real. That pane re-renders the selected
// note's markdown on every frame — every arrow key, every cursor blink — and a
// TermRenderer builds a goldmark parser chain and a chroma formatter on
// construction. Rendering a few hundred lines of markdown on each keystroke is
// visible as input lag.
//
// So:
//
//	renderer cache   keyed by (wrap width, palette generation)
//	output cache     keyed by (caller-supplied id, wrap width, palette generation)
//
// The output cache is what makes arrow-key navigation free: moving down and
// back up re-renders nothing. Its key includes the note's updated timestamp
// (see noteCacheKey), so an edit invalidates its own entry without anyone
// having to remember to clear it.
//
// Concurrency: none, and this is checked rather than assumed. Every entry point
// is reached from Update or View, and in bubbletea v2.0.8 all three
// p.render(model) call sites — the initial paint, the event loop, and the final
// paint after it returns — are on Run()'s own goroutine, immediately after the
// model.Update() that precedes them. There is no separate render goroutine
// calling View() concurrently.
//
// What must not change: nothing here may be called from a tea.Cmd. Those DO run
// on their own goroutines. Commands deal in data; rendering happens in Update
// and View. Phase 6's host theme arrives on a socket goroutine and is routed
// through a message for exactly this reason.

// mdCacheKey identifies a rendering context. Two notes rendered at the same
// width under the same palette share a renderer; change either and both caches
// are rebuilt.
type mdCacheKey struct {
	width int
	gen   uint64
}

var (
	mdRenderer    *glamour.TermRenderer
	mdRendererKey mdCacheKey

	mdOutput    = map[string]string{}
	mdOutputKey mdCacheKey
)

// mdOutputCap bounds the output cache. A browse session over a few hundred
// notes would otherwise hold every body it ever previewed in rendered form,
// which is several times the size of the notes themselves. On overflow the map
// is dropped wholesale rather than evicted by age: the working set is whatever
// the user is currently scrolling through, so it refills in a few frames, and
// LRU bookkeeping would cost more than the re-renders it saves.
const mdOutputCap = 64

// markdownRenderer returns a renderer for the given wrap width, building one
// only when the width or the palette has changed since the last call.
func markdownRenderer(width int) *glamour.TermRenderer {
	k := mdCacheKey{width: width, gen: paletteGen}
	if mdRenderer != nil && mdRendererKey == k {
		return mdRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(glamourStyle(pal)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		// Leave the cache empty so the next call retries; callers fall back to
		// raw text for this frame.
		return nil
	}
	mdRenderer, mdRendererKey = r, k
	return r
}

// renderMarkdown renders body for a pane of the given total width. On any
// renderer or parse failure it returns the raw text — showing something always
// beats showing an error where the note should be.
func renderMarkdown(body string, width int) string {
	w := markdownWrapWidth(width)
	r := markdownRenderer(w)
	if r == nil {
		return body
	}
	out, err := r.Render(body)
	if err != nil {
		return body
	}
	return out
}

// renderMarkdownCached is renderMarkdown with memoization on id. Use it
// wherever the same body is re-rendered across frames — the browse preview
// pane and the detail viewport both re-render on every resize and selection
// change. Callers that render one-off text should use renderMarkdown.
func renderMarkdownCached(id, body string, width int) string {
	w := markdownWrapWidth(width)
	k := mdCacheKey{width: w, gen: paletteGen}
	if mdOutputKey != k || len(mdOutput) > mdOutputCap {
		mdOutput = map[string]string{}
		mdOutputKey = k
	}
	if out, ok := mdOutput[id]; ok {
		return out
	}
	out := renderMarkdown(body, width)
	mdOutput[id] = out
	return out
}

// noteCacheKey identifies a note's *content*, not just the note. Including the
// update timestamp means a save invalidates the entry it replaces, so an edited
// note never previews as its old self.
func noteCacheKey(id int64, updatedUnix int64) string {
	return strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(updatedUnix, 10)
}

// markdownWrapWidth converts an available pane width to a glamour wrap width.
// The -2 is the gutter glamour's document block indents by; the 100-column cap
// keeps prose readable on a very wide terminal rather than stretching lines
// across the whole screen. The floor stops a pathologically narrow pane from
// asking for a non-positive wrap, which glamour treats as "do not wrap".
func markdownWrapWidth(width int) int {
	w := width - 2
	if w > 100 {
		w = 100
	}
	if w < 20 {
		w = 20
	}
	return w
}

// glamourStyle derives a markdown style config from the palette.
//
// glamour ships Dark/LightStyleConfig, and those are the right *base* — they
// carry a full set of block prefixes, indents, list markers and a chroma theme
// that would be tedious and pointless to restate. What they do not carry is any
// relationship to this app's colors, so the handful of fields that should track
// the accent are overridden here.
//
// This is also the seam Phase 6 needs: a cats host theme arrives as hex, and
// hex is exactly what ansi.StylePrimitive.Color wants, so a host palette flows
// into the markdown renderer through this function with no extra plumbing.
func glamourStyle(p Palette) ansi.StyleConfig {
	base := styles.LightStyleConfig
	if p.Dark {
		base = styles.DarkStyleConfig
	}

	// Copied by value above, so these writes do not mutate glamour's package
	// vars — which would poison every later render, including a subsequent
	// switch back to the other base.
	base.Document.Color = strPtr(p.Fg)

	// Headings carry the accent. H1 keeps its base treatment (the style configs
	// give it a filled background bar) so only its background is retinted.
	base.H1.BackgroundColor = strPtr(p.Primary)
	for _, h := range []*ansi.StyleBlock{&base.H2, &base.H3, &base.H4, &base.H5, &base.H6} {
		h.Color = strPtr(p.Primary)
	}

	base.Link.Color = strPtr(p.Primary)
	base.LinkText.Color = strPtr(p.Primary)
	base.HorizontalRule.Color = strPtr(p.Subtle)
	base.BlockQuote.Color = strPtr(p.Subtle)

	return base
}

// strPtr is glamour's convention for optional style fields: nil means "inherit
// from the base style", so every override has to be addressable.
func strPtr(s string) *string { return &s }

// clampLines cuts s to at most n display lines and pads it out to exactly that
// many, so a pane rendered beside another cannot push its neighbor's layout
// around. Returns the empty string for n <= 0.
//
// This is deliberately line-based rather than a lipgloss Height/MaxHeight pair:
// MaxHeight truncates but does not pad, and Height pads but does not truncate,
// so laying the two on top of each other is the only way to get both — and
// doing it here keeps the ANSI-aware width handling to lipgloss and the plain
// line arithmetic visible.
func clampLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// clampPane fits a rendered block to exactly w columns and h lines: every line
// truncated (ANSI-aware, so a color run is never cut mid-escape) and padded.
//
// This is not belt-and-braces around widgets that already size themselves. It
// is load-bearing, because bubbles v2.1.1's help footer does not reliably
// honor its own width. help.shouldAddItem drops an item and appends an ellipsis
// when the item would overflow — but only if the ellipsis *itself* still fits;
// otherwise it falls through to "ok" and appends the item anyway, and every
// remaining item after it too. At 48 columns the browse footer truncates
// correctly; at 80 it runs to 111 and the terminal wraps it, shearing the
// layout by a line.
//
// Clamping at the pane boundary is the right place to fix that regardless: the
// screen is what promises "these panes total exactly the terminal width", so
// the screen is what should enforce it, whatever a widget inside decides to
// render.
func clampPane(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(clampLines(s, h))
}
