package tui

import "github.com/charmbracelet/lipgloss"

// Central color palette using AdaptiveColor so the TUI is legible on both
// light and dark terminal backgrounds without any user configuration.
// Keeping every color here (rather than inline in views) means a re-theme
// is a one-file change.
var (
	colorPrimary = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7D79F6"} // brand accent
	colorSubtle  = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6C6C6C"} // secondary text
	colorSuccess = lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#7CD992"}
	colorDanger  = lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF8A80"}
	colorWarn    = lipgloss.AdaptiveColor{Light: "#B26A00", Dark: "#FFC46B"}
)

var (
	// appTitleStyle renders screen headers (e.g. "GoNotes", note titles).
	appTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorPrimary).
			Bold(true).
			Padding(0, 1)

	// statusBarStyle / statusErrStyle render the single feedback line pinned
	// to the bottom of every screen. Errors get a distinct color so failed
	// saves/deletes are impossible to miss.
	statusBarStyle = lipgloss.NewStyle().Foreground(colorSubtle).Padding(0, 1)
	statusErrStyle = lipgloss.NewStyle().Foreground(colorDanger).Bold(true).Padding(0, 1)
	statusOKStyle  = lipgloss.NewStyle().Foreground(colorSuccess).Padding(0, 1)

	// helpStyle renders key hints (e.g. "enter view • n new • q quit").
	helpStyle = lipgloss.NewStyle().Foreground(colorSubtle)

	// dimStyle is for de-emphasized metadata like dates and tag lists.
	dimStyle = lipgloss.NewStyle().Foreground(colorSubtle)

	// flaggedStyle / privateStyle color the ⚑ and lock indicators in lists
	// and detail headers.
	flaggedStyle = lipgloss.NewStyle().Foreground(colorWarn)
	privateStyle = lipgloss.NewStyle().Foreground(colorDanger)

	// Form field styles: the focused label gets the accent color so the
	// active field is obvious even before the cursor is noticed.
	labelStyle        = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)
	labelFocusedStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	// dialogBoxStyle draws the bordered box for confirm dialogs and prompts.
	dialogBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(1, 3)

	// detailHeaderStyle underlines the metadata block above a note body.
	detailHeaderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				BorderForeground(colorSubtle)
)
