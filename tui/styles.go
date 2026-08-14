package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Central color palette. Keeping every color here (rather than inline in
// views) means a re-theme is a one-file change.
//
// Light/dark handling, and why this file gained a function:
//
// Lipgloss v1 had `AdaptiveColor{Light, Dark}`, a value that resolved itself
// at render time by consulting a package-global background detection. v2
// deleted it — colors are now plain `color.Color` values, and choosing
// between a light and a dark variant is the application's job via
// `lipgloss.LightDark(isDark)`.
//
// That forces a decision about *when* detection happens, and the obvious
// answer is wrong twice over. `lipgloss.HasDarkBackground` writes an OSC 11
// query and blocks on the reply with a 2s timeout — and BackgroundColor tries
// stdin and stdout in turn, so a terminal that ignores OSC 11 costs ~4s of
// black screen. Calling it from a package-level var initializer would also
// run it during `gonotes serve`, since main imports this package
// unconditionally.
//
// So detection is asynchronous instead: the root model asks for the
// background color with tea.RequestBackgroundColor() and calls setPalette()
// when tea.BackgroundColorMsg arrives (lipgloss's own docs point Bubble Tea
// users at exactly this). Until then the vars hold the dark variant, which is
// also what bubbles v2 hardcodes in its widget defaults. A terminal that
// never answers simply stays dark rather than stalling.
//
// Phase 3 replaces this with a Palette struct and a restyle broadcast to the
// whole screen stack; setPalette and the restyler interface in tui.go are the
// seams that work lands in.

// In v2 lipgloss.Color is a *function* returning a color.Color, not a type,
// so these are declared against the standard image/color interface.
var (
	colorPrimary color.Color // brand accent
	colorSubtle  color.Color // secondary text
	colorSuccess color.Color
	colorDanger  color.Color
	colorWarn    color.Color
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
)

// isDark records which variant the palette currently holds. The bubbles v2
// widgets (list, textinput, textarea) build their own default styles and
// hardcode the dark set, so every construction site has to pass this along —
// see newListDelegate in browse.go and the widget constructors in form.go,
// login.go and confirm.go.
var isDark = true

func init() { setPalette(true) } // dark until the terminal says otherwise

// setPalette rebuilds every style for a light or dark terminal background.
// Safe to call more than once. The package-level styles above are read on
// every render, so screens pick the new palette up immediately; styles that
// bubbles widgets copied into themselves at construction do not, which is
// what the restyler interface in tui.go exists to fix.
func setPalette(dark bool) {
	isDark = dark
	c := lipgloss.LightDark(dark)

	colorPrimary = c(lipgloss.Color("#5A56E0"), lipgloss.Color("#7D79F6"))
	colorSubtle = c(lipgloss.Color("#8A8A8A"), lipgloss.Color("#6C6C6C"))
	colorSuccess = c(lipgloss.Color("#2E7D32"), lipgloss.Color("#7CD992"))
	colorDanger = c(lipgloss.Color("#C62828"), lipgloss.Color("#FF8A80"))
	colorWarn = c(lipgloss.Color("#B26A00"), lipgloss.Color("#FFC46B"))

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
}
