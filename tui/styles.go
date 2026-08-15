package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// Every lipgloss style the TUI draws with lives here, rebuilt from the active
// Palette (palette.go) by applyPalette. Keeping them in one file means a
// re-theme — whether the terminal reports a light background or the cats host
// pushes its own colors — is one function call, not a sweep through six views.
//
// Light/dark handling, and why this is a function rather than a var block:
//
// Lipgloss v1 had AdaptiveColor{Light, Dark}, a value that resolved itself at
// render time by consulting a package-global background detection. v2 deleted
// it — colors are plain color.Color values now, and choosing between a light
// and a dark variant is the application's job.
//
// That forces a decision about *when* detection happens, and the obvious answer
// is wrong twice over. lipgloss.HasDarkBackground writes an OSC 11 query and
// blocks on the reply with a 2s timeout — and BackgroundColor tries stdin and
// stdout in turn, so a terminal that ignores OSC 11 costs ~4s of black screen.
// Calling it from a package-level var initializer would also run it during
// `gonotes serve`, since main imports this package unconditionally.
//
// So detection is asynchronous: the root model asks with
// tea.RequestBackgroundColor() and installs a new palette when
// tea.BackgroundColorMsg arrives (lipgloss's own docs point Bubble Tea users at
// exactly this). Until then the dark palette holds, which is also what bubbles
// v2 hardcodes in its widget defaults. A terminal that never answers simply
// stays dark rather than stalling.
//
// The styles below are read fresh on every View(), so screens pick a new
// palette up immediately. Widgets are the exception — bubbles copies its style
// set into each Model at construction — which is what the restyler interface in
// tui.go exists to fix.

// In v2 lipgloss.Color is a *function* returning a color.Color, not a type, so
// these are declared against the standard image/color interface.
var (
	colorPrimary color.Color // brand accent
	colorSubtle  color.Color // secondary text
	colorSuccess color.Color
	colorDanger  color.Color
	colorWarn    color.Color
	colorFg      color.Color // terminal foreground
	colorSel     color.Color // selection fill (accent over background)
)

var (
	// appTitleStyle renders screen headers (e.g. "GoNotes", note titles).
	appTitleStyle lipgloss.Style

	// statusBarStyle / statusErrStyle render the single feedback line pinned
	// to the bottom of every screen. Errors get a distinct color so failed
	// saves/deletes are impossible to miss.
	statusBarStyle lipgloss.Style
	statusErrStyle lipgloss.Style
	statusOKStyle  lipgloss.Style

	// helpStyle renders key hints (e.g. "enter view • n new • q quit").
	helpStyle lipgloss.Style

	// dimStyle is for de-emphasized metadata like dates and tag lists.
	dimStyle lipgloss.Style

	// flaggedStyle / privateStyle color the ⚑ and lock indicators in lists
	// and detail headers.
	flaggedStyle lipgloss.Style
	privateStyle lipgloss.Style

	// Form field styles: the focused label gets the accent color so the
	// active field is obvious even before the cursor is noticed.
	labelStyle        lipgloss.Style
	labelFocusedStyle lipgloss.Style

	// dialogBoxStyle draws the bordered box for confirm dialogs and prompts.
	dialogBoxStyle lipgloss.Style

	// detailHeaderStyle underlines the metadata block above a note body.
	detailHeaderStyle lipgloss.Style

	// previewPaneStyle draws the browse screen's wide-layout right pane: a
	// vertical rule separating it from the list, and a gutter so rendered
	// markdown does not touch the rule.
	previewPaneStyle lipgloss.Style

	// previewTitleStyle heads that pane with the selected note's title.
	previewTitleStyle lipgloss.Style

	// errorTextStyle renders inline error text inside a dialog (as opposed to
	// the status bar's version, which is padded to sit on the bottom line).
	errorTextStyle lipgloss.Style
)

// applyPalette rebuilds every style above from p. Called only by setPalette,
// which owns the "did anything change" decision.
func applyPalette(p Palette) {
	colorPrimary = lipgloss.Color(p.Primary)
	colorSubtle = lipgloss.Color(p.Subtle)
	colorSuccess = lipgloss.Color(p.Success)
	colorDanger = lipgloss.Color(p.Danger)
	colorWarn = lipgloss.Color(p.Warn)
	colorFg = lipgloss.Color(p.Fg)
	colorSel = lipgloss.Color(p.Sel)

	appTitleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(colorPrimary).
		Bold(true).
		Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().Foreground(colorSubtle).Padding(0, 1)
	statusErrStyle = lipgloss.NewStyle().Foreground(colorDanger).Bold(true).Padding(0, 1)
	statusOKStyle = lipgloss.NewStyle().Foreground(colorSuccess).Padding(0, 1)

	helpStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	dimStyle = lipgloss.NewStyle().Foreground(colorSubtle)

	flaggedStyle = lipgloss.NewStyle().Foreground(colorWarn)
	privateStyle = lipgloss.NewStyle().Foreground(colorDanger)

	labelStyle = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)
	labelFocusedStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	dialogBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 3)

	detailHeaderStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(colorSubtle)

	previewPaneStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorSubtle).
		PaddingLeft(1)

	previewTitleStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	errorTextStyle = lipgloss.NewStyle().Foreground(colorDanger)
}

// renderHelp formats a key hint line from the keymap — "enter view • n new •
// q quit" — so the footer text and the bindings that actually fire can never
// drift apart. Disabled bindings and those without help text are skipped.
//
// The list-based screens do not use this: bubbles renders their footer itself
// from AdditionalShortHelpKeys, fed from the same keymap.
func renderHelp(bindings ...key.Binding) string {
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		h := b.Help()
		if !b.Enabled() || h.Key == "" {
			continue
		}
		parts = append(parts, h.Key+" "+h.Desc)
	}
	return helpStyle.Render(strings.Join(parts, " • "))
}
