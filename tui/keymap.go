package tui

import "charm.land/bubbles/v2/key"

// keyMap is the one place a key is bound to a meaning.
//
// Before this the six screens each ran `switch msg.String()` over string
// literals, with the help footer written out as a separate hand-maintained
// string. That has two failure modes and the v1→v2 port hit both: a key that
// upstream renames (" " became "space") turns into dead code with no compile
// error, and a footer that says "d delete" while the switch no longer matches
// "d" is a lie the compiler cannot catch.
//
// key.Binding fixes the second directly — the footer is *generated* from the
// same value the dispatch matches — and narrows the first to one file, where
// keymap_test.go pins every string.
//
// key.Matches compares against Key.String(), so the strings here are still the
// contract with the terminal; the table in tui_test.go remains the guard on
// that layer.
//
// Later phases add rows rather than new switch sites: Phase 7's ctrl+g capture
// door and Phase 8's ⌘ accelerators (which translate a claimed super+X chord to
// its twin here and re-dispatch) both bind through this struct.
type keyMap struct {
	// ---- Global -----------------------------------------------------------
	// ForceQuit is handled by the root before any screen sees the key, so it
	// works even from a modal or while a save is in flight.
	ForceQuit key.Binding
	// Quit is the ordinary "q" exit, honored only by screens where q is not a
	// legitimate character to type. The login screen deliberately does not
	// bind it — q belongs in usernames and passwords.
	Quit key.Binding
	// Back pops one screen. On the login screen, where there is nothing to pop
	// to, it quits.
	Back key.Binding

	// ---- Note list and detail --------------------------------------------
	Open       key.Binding // enter: open the selected note
	New        key.Binding
	Edit       key.Binding
	Delete     key.Binding
	Flag       key.Binding
	Categories key.Binding // open the category screen
	Scroll     key.Binding // help-only: the viewport's own ↑/↓ handling

	// ---- Category screen --------------------------------------------------
	Filter   key.Binding // enter: narrow the note list to this category
	AllNotes key.Binding // clear the category filter

	// ---- Form -------------------------------------------------------------
	Save          key.Binding
	Editor        key.Binding // ctrl+e: hand the body to $VISUAL/$EDITOR
	NextField     key.Binding
	PrevField     key.Binding
	FieldDown     key.Binding // login only: arrows move between fields there,
	FieldUp       key.Binding // but belong to the textarea on the form
	Submit        key.Binding // enter: advance, or submit on the last field
	TogglePrivate key.Binding

	// ---- Confirm dialog ---------------------------------------------------
	Yes key.Binding
	No  key.Binding
}

// keys is the active keymap. A package var rather than a field on session
// because nothing rebinds it at runtime; making it configurable later means
// moving it onto session and threading it, not rewriting the call sites.
var keys = defaultKeyMap()

func defaultKeyMap() keyMap {
	return keyMap{
		ForceQuit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),

		Open: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "view"),
		),
		New: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new"),
		),
		Edit: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete"),
		),
		Flag: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "flag"),
		),
		Categories: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "categories"),
		),
		// The detail screen never matches on these — the arrows fall through
		// to the viewport, which owns its own scrolling — but the footer still
		// has to say so, and a binding with no keys at all reports itself as
		// disabled (key.Binding.Enabled() is false when the key set is empty),
		// which would drop the row from the rendered help entirely.
		Scroll: key.NewBinding(
			key.WithKeys("up", "down"),
			key.WithHelp("↑/↓", "scroll"),
		),

		Filter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "filter notes"),
		),
		AllNotes: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "all notes"),
		),

		Save: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "save"),
		),
		Editor: key.NewBinding(
			key.WithKeys("ctrl+e"),
			key.WithHelp("ctrl+e", "$EDITOR"),
		),
		NextField: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next field"),
		),
		PrevField: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev field"),
		),
		FieldDown: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp("↓", "next field"),
		),
		FieldUp: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp("↑", "prev field"),
		),
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "submit"),
		),
		// v1 stringified the space bar as " "; v2 names it "space" (space is
		// the one printable character whose literal form is invisible, so
		// ultraviolet gives it a word). Binding " " here would silently stop
		// toggling the checkbox — see TestKeyNames.
		TogglePrivate: key.NewBinding(
			key.WithKeys("space"),
			key.WithHelp("space", "toggle"),
		),

		Yes: key.NewBinding(
			key.WithKeys("y", "Y", "enter"),
			key.WithHelp("y/enter", "confirm"),
		),
		No: key.NewBinding(
			key.WithKeys("n", "N", "esc", "q"),
			key.WithHelp("n/esc", "cancel"),
		),
	}
}

// The per-screen help sets. Each returns the bindings in the order they should
// appear in that screen's footer; the list-based screens feed theirs to
// bubbles' AdditionalShortHelpKeys and the rest go through renderHelp.
//
// These exist as methods rather than literals at the call sites so that adding
// a binding to a screen is a one-line change here, and so keymap_test.go can
// assert that no screen advertises a key it does not handle.

func (k keyMap) browseHelp() []key.Binding {
	return []key.Binding{k.Open, k.New, k.Edit, k.Delete, k.Flag, k.Categories, k.Quit}
}

func (k keyMap) categoriesHelp() []key.Binding {
	return []key.Binding{k.Filter, k.AllNotes, k.New, k.Delete, k.Back}
}

func (k keyMap) detailHelp() []key.Binding {
	return []key.Binding{k.Scroll, k.Edit, k.Flag, k.Delete, k.Back}
}

func (k keyMap) formHelp() []key.Binding {
	return []key.Binding{k.Save, k.Editor, k.NextField, k.Back}
}

func (k keyMap) loginHelp() []key.Binding {
	return []key.Binding{k.Submit, k.NextField, k.ForceQuit}
}

func (k keyMap) promptHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Back}
}
