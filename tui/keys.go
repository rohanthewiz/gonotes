package tui

import "github.com/charmbracelet/bubbles/key"

// globalKeys are active in every screen unless a text input has focus.
type globalKeyMap struct {
	Quit key.Binding
	Back key.Binding
	Help key.Binding
}

var globalKeys = globalKeyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q/ctrl+c", "quit"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
}

// listKeys are active on the notes list screen.
type listKeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Enter    key.Binding
	New      key.Binding
	Delete   key.Binding
	Search   key.Binding
	Category key.Binding
	NextPage key.Binding
	PrevPage key.Binding
}

var listKeys = listKeyMap{
	Up: key.NewBinding(
		key.WithKeys("k", "up"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("j", "down"),
		key.WithHelp("↓/j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "view"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new note"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	Category: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "categories"),
	),
	NextPage: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next page"),
	),
	PrevPage: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev page"),
	),
}

// detailKeys are active on the note detail screen.
type detailKeyMap struct {
	Edit   key.Binding
	Delete key.Binding
}

var detailKeys = detailKeyMap{
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
}

// formKeys are active when editing a note form.
type formKeyMap struct {
	Save       key.Binding
	NextField  key.Binding
	PrevField  key.Binding
	Categories key.Binding
	ToggleBool key.Binding
}

var formKeys = formKeyMap{
	Save: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "save"),
	),
	NextField: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	PrevField: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev field"),
	),
	Categories: key.NewBinding(
		key.WithKeys("ctrl+a"),
		key.WithHelp("ctrl+a", "categories"),
	),
	ToggleBool: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
}

// categoryKeys are active on the category management screen.
type categoryKeyMap struct {
	New    key.Binding
	Edit   key.Binding
	Delete key.Binding
	Enter  key.Binding
}

var categoryKeys = categoryKeyMap{
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new category"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
}

// searchKeys are active on the search/filter screen.
type searchKeyMap struct {
	Execute   key.Binding
	NextField key.Binding
	PrevField key.Binding
	Toggle    key.Binding
}

var searchKeys = searchKeyMap{
	Execute: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "search"),
	),
	NextField: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	PrevField: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev field"),
	),
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
}
