package tui

import "github.com/charmbracelet/lipgloss"

// Color palette — warm neutral tones with teal accents for a calm, readable TUI.
var (
	colorPrimary   = lipgloss.Color("#5FAFAF") // teal accent
	colorSecondary = lipgloss.Color("#AFAFAF") // muted gray for secondary text
	colorSuccess   = lipgloss.Color("#87AF87") // soft green for success messages
	colorDanger    = lipgloss.Color("#D78787") // soft red for delete/errors
	colorHighlight = lipgloss.Color("#FFD787") // warm yellow for highlights
	colorDim       = lipgloss.Color("#626262") // dim gray for borders and separators
	colorWhite     = lipgloss.Color("#E4E4E4") // off-white for primary text
)

// titleStyle renders the app banner / screen title bar.
var titleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorPrimary).
	MarginBottom(1)

// subtitleStyle is for secondary headings within a screen.
var subtitleStyle = lipgloss.NewStyle().
	Foreground(colorSecondary).
	Italic(true)

// selectedStyle highlights the currently focused item in a list.
var selectedStyle = lipgloss.NewStyle().
	Foreground(colorPrimary).
	Bold(true)

// normalStyle is the default style for unselected list items.
var normalStyle = lipgloss.NewStyle().
	Foreground(colorWhite)

// dimStyle is used for low-emphasis text (timestamps, IDs, hints).
var dimStyle = lipgloss.NewStyle().
	Foreground(colorDim)

// successStyle is used for success status messages.
var successStyle = lipgloss.NewStyle().
	Foreground(colorSuccess)

// dangerStyle is used for delete confirmations and error messages.
var dangerStyle = lipgloss.NewStyle().
	Foreground(colorDanger)

// highlightStyle is used for search matches and emphasis.
var highlightStyle = lipgloss.NewStyle().
	Foreground(colorHighlight)

// boxStyle wraps content in a bordered panel (used for note detail, forms).
var boxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorDim).
	Padding(1, 2)

// helpStyle renders the bottom help bar with key binding hints.
var helpStyle = lipgloss.NewStyle().
	Foreground(colorDim).
	MarginTop(1)

// statusBarStyle renders the status message bar below the main content.
var statusBarStyle = lipgloss.NewStyle().
	Foreground(colorSecondary).
	MarginTop(1)

// labelStyle renders form field labels.
var labelStyle = lipgloss.NewStyle().
	Foreground(colorPrimary).
	Bold(true)

// tagStyle renders category/tag badges inline.
var tagStyle = lipgloss.NewStyle().
	Foreground(colorPrimary).
	Background(lipgloss.Color("#1C3A3A")).
	Padding(0, 1)

// errorStyle renders inline error messages within forms.
var errorStyle = lipgloss.NewStyle().
	Foreground(colorDanger).
	Bold(true)
