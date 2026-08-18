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
	// Duplicate opens the copy dialog on the selected note. Shifted, because
	// every unshifted letter on these screens is already spent — and "d" of all
	// letters is the one it must not share, since the neighbouring meaning is
	// delete.
	Duplicate key.Binding

	// ---- Category and subcategory screens ---------------------------------
	Filter   key.Binding // enter: narrow the note list to this category
	AllNotes key.Binding // clear the category filter
	Subcats  key.Binding // open the highlighted category's subcategories
	// SelectSub toggles a subcategory into the filter being built. It shares the
	// space bar with TogglePrivate, which is safe because the two screens are
	// disjoint — but they stay separate bindings so each footer can name the
	// thing that screen actually toggles.
	SelectSub key.Binding

	// ---- Capture from a sibling agent pane --------------------------------
	// Capture is the door (browse); Move and Pick drive the picker it opens.
	// The door works only at Tier 1 and says so otherwise, which is why it is
	// deliberately absent from browseHelp — see captureHint in capture.go.
	Capture key.Binding
	Move    key.Binding // ↑/↓ within the picker
	Pick    key.Binding // enter: capture from the highlighted pane

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

	// ---- Duplicate dialog -------------------------------------------------
	// Include toggles one thing the copy will carry over. It shares the space
	// bar with TogglePrivate and SelectSub, on screens neither of those
	// reaches, and stays a separate binding so this footer can name what THIS
	// screen's space bar does.
	Include key.Binding

	// ---- Unsaved-changes dialog -------------------------------------------
	// Leaving a dirty form is a three-way question, not a yes/no one, so it
	// cannot reuse Yes/No: those are two answers, and No already claims esc —
	// which here has to mean the third thing, "I did not mean to leave at all".
	SaveExit    key.Binding
	DiscardExit key.Binding
	CancelExit  key.Binding

	// ---- Sync -------------------------------------------------------------
	// Sync is prompt-driven by default (see tui/sync.go), so it needs a door
	// the user can open and a set of answers for when it opens itself.
	Sync            key.Binding // "S": open the sync dialog from the list
	SyncGo          key.Binding // sync now
	SyncCompact     key.Binding // compact the pending log, then sync
	SyncCompactOnly key.Binding // compact without syncing (the hub may be gone)
	SyncLater       key.Binding // defer the prompt
	SyncQuitAnyway  key.Binding // leave without syncing (quit dialog only)

	// ---- Locked-note dialog -----------------------------------------------
	// Shown when another session already has the note open. Like the unsaved
	// dialog, this is a fork rather than a yes/no, so it gets its own letters
	// instead of reusing Yes/No.
	ReadOnly key.Binding
	Steal    key.Binding
	JumpPane key.Binding
	// Retake re-claims a lease that was lost or stolen while the form stayed
	// open. It lives on the form, not this dialog — see the lock banner.
	Retake key.Binding
	// Reload discards the form's text in favour of the version that won a
	// stale-write conflict. Its partner Overwrite forces the save through.
	Reload    key.Binding
	Overwrite key.Binding
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

		// "D" rather than a free unshifted letter: browse spends every one of
		// those on a note action, and the shifted form of the neighbouring
		// action keeps the mnemonic. The slip that matters — reaching for D and
		// getting d — lands on the delete CONFIRMATION, which is one esc away
		// from nothing having happened.
		Duplicate: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", "duplicate"),
		),

		Filter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "filter notes"),
		),
		AllNotes: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "all notes"),
		),
		// "s" is free on the category screen (which spends letters on a, n, d and
		// q) and is the first letter of the thing it opens. It is deliberately NOT
		// one of the chords cats forwards — see metakeys.go — so no ⌘S ambiguity
		// arises from reusing the letter here.
		Subcats: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "subcategories"),
		),
		SelectSub: key.NewBinding(
			key.WithKeys("space"),
			key.WithHelp("space", "select"),
		),

		// ctrl+g rather than a bare letter: browse spends every unmodified key
		// on a note action, and g is the one cats forwards as ⌘G (Phase 8's
		// accelerator table maps onto exactly these bindings).
		Capture: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("ctrl+g", "capture agent pane"),
		),
		Move: key.NewBinding(
			key.WithKeys("up", "down"),
			key.WithHelp("↑/↓", "move"),
		),
		Pick: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "capture"),
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

		// The same space bar as TogglePrivate, named for what it does here:
		// the question on the duplicate dialog is what the copy INCLUDES.
		Include: key.NewBinding(
			key.WithKeys("space"),
			key.WithHelp("space", "include"),
		),
		No: key.NewBinding(
			key.WithKeys("n", "N", "esc", "q"),
			key.WithHelp("n/esc", "cancel"),
		),

		// enter is bound to the *safe* answer here, the mirror image of the
		// delete dialog where enter confirms the destructive one. A dialog the
		// user did not ask for is one they may dismiss on reflex, and the reflex
		// key should be the one that loses nothing.
		SaveExit: key.NewBinding(
			key.WithKeys("s", "y", "Y", "enter"),
			key.WithHelp("s/enter", "save & exit"),
		),
		// No "n": on this dialog "no" is genuinely ambiguous — no, don't save?
		// no, don't leave? The two answers get unambiguous letters instead.
		DiscardExit: key.NewBinding(
			key.WithKeys("d", "D"),
			key.WithHelp("d", "discard"),
		),
		// esc keeps editing rather than discarding. The user arrived here BY
		// pressing esc, so a second esc is as likely to be the tail of a
		// double-tap as a decision — and this is the reading where that costs
		// nothing.
		CancelExit: key.NewBinding(
			key.WithKeys("esc", "c"),
			key.WithHelp("esc", "keep editing"),
		),

		// "S" rather than a bare "s": browse has no free unshifted letter left,
		// and the shifted form keeps the mnemonic — the same reasoning that put
		// duplicate on "D". A slip that lands on lowercase "s" on the browse
		// screen does nothing at all, which is the right cost for a miss.
		Sync: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "sync"),
		),
		// enter is bound to the plain sync rather than to the compacting one:
		// the reflex answer should be the one that changes least. Compaction
		// discards local change history, so it is always a letter, never enter.
		SyncGo: key.NewBinding(
			key.WithKeys("s", "y", "Y", "enter"),
			key.WithHelp("s/enter", "sync now"),
		),
		SyncCompact: key.NewBinding(
			key.WithKeys("c", "C"),
			key.WithHelp("c", "compact & sync"),
		),
		// "p" for pack. Deliberately not a second "c": the difference between
		// compacting-and-syncing and compacting-only is exactly the thing a
		// user must not press by accident when the hub is unreachable.
		SyncCompactOnly: key.NewBinding(
			key.WithKeys("p", "P"),
			key.WithHelp("p", "compact only"),
		),
		// esc is bound here — the dialog the clock raised was not asked for, so
		// the reflex dismissal has to be the answer that loses nothing: it
		// defers, and the banner keeps saying a sync is owed.
		SyncLater: key.NewBinding(
			key.WithKeys("l", "esc"),
			key.WithHelp("esc", "later"),
		),
		// "q" only, and only on the quit dialog. It is the answer that leaves
		// changes on this machine, so it gets neither enter nor esc.
		SyncQuitAnyway: key.NewBinding(
			key.WithKeys("q", "Q"),
			key.WithHelp("q", "quit without syncing"),
		),

		// "r" for read — the safe answer, and the one enter is bound to for the
		// same reason it is bound to "save & exit" on the unsaved dialog: a
		// dialog the user did not ask for gets dismissed on reflex, so the
		// reflex key must be the one that takes nothing from anyone.
		ReadOnly: key.NewBinding(
			key.WithKeys("r", "R", "enter"),
			key.WithHelp("r/enter", "open read-only"),
		),
		// "t" for take. Deliberately NOT "s" (which reads as save) and not "y"
		// — there is no question here phrased so that "yes" answers it, and a
		// letter that means "the destructive one" on other dialogs should not
		// mean it on this one by accident.
		Steal: key.NewBinding(
			key.WithKeys("t", "T"),
			key.WithHelp("t", "take over"),
		),
		JumpPane: key.NewBinding(
			key.WithKeys("g", "G"),
			key.WithHelp("g", "go to their pane"),
		),

		// ctrl+l, on the form, where every bare letter is a character the user
		// is trying to type into a field. "l" for lock.
		Retake: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("ctrl+l", "retake lock"),
		),

		// The stale-write fork. Both are destructive in opposite directions —
		// one drops your text, the other drops theirs — so neither gets enter,
		// and esc means "leave the form as it is and decide later", which is
		// always available and never loses anything.
		Reload: key.NewBinding(
			key.WithKeys("l", "L"),
			key.WithHelp("l", "load theirs (drops your edits)"),
		),
		Overwrite: key.NewBinding(
			key.WithKeys("o", "O"),
			key.WithHelp("o", "overwrite theirs"),
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

// syncHelp is the sync dialog's answer set. It varies by why the dialog is up:
// only the quit form offers "leave without syncing", and only the forms the
// user did not open themselves describe their escape as deferring.
func (k keyMap) syncHelp(purpose syncPurpose) []key.Binding {
	rows := []key.Binding{k.SyncGo, k.SyncCompact, k.SyncCompactOnly}
	if purpose == syncQuitting {
		rows = append(rows, k.SyncQuitAnyway)
		// On the quit dialog, esc is "stay in GoNotes" rather than "defer" —
		// the same key doing the same thing (nothing irreversible), said in
		// the words this dialog makes true.
		return append(rows, key.NewBinding(
			key.WithKeys(k.SyncLater.Keys()...),
			key.WithHelp("esc", "keep working"),
		))
	}
	return append(rows, k.SyncLater)
}

func (k keyMap) browseHelp() []key.Binding {
	// Duplicate sits late deliberately: the list footer is elided from the right
	// on a narrow terminal, and edit/delete/flag are the rows that must survive
	// the cut. Duplicate is discoverable wherever there is room for it, and the
	// detail screen — which renders its whole footer — always has room.
	//
	// Sync sits beside Duplicate for the same reason and with the same
	// trade-off: it is worth finding, and it is not worth pushing edit or
	// delete off a narrow footer. The banner names the key too, on exactly the
	// occasions when it matters.
	return []key.Binding{k.Open, k.New, k.Edit, k.Delete, k.Flag, k.Categories, k.Duplicate, k.Sync, k.Quit}
}

func (k keyMap) categoriesHelp() []key.Binding {
	return []key.Binding{k.Filter, k.Subcats, k.AllNotes, k.New, k.Delete, k.Back}
}

func (k keyMap) subcategoriesHelp() []key.Binding {
	return []key.Binding{k.Filter, k.SelectSub, k.New, k.Delete, k.Back}
}

func (k keyMap) agentPickerHelp() []key.Binding {
	return []key.Binding{k.Move, k.Pick, k.Back}
}

func (k keyMap) detailHelp() []key.Binding {
	return []key.Binding{k.Scroll, k.Edit, k.Duplicate, k.Flag, k.Delete, k.Back}
}

func (k keyMap) formHelp() []key.Binding {
	return []key.Binding{k.Save, k.Editor, k.NextField, k.Back}
}

func (k keyMap) loginHelp() []key.Binding {
	return []key.Binding{k.Submit, k.NextField, k.ForceQuit}
}

// duplicateHelp is the copy dialog's footer. Move is the help-only ↑/↓ pair —
// the screen dispatches on FieldUp/FieldDown, which are the same two keys said
// one direction at a time.
func (k keyMap) duplicateHelp() []key.Binding {
	// enter is Submit — the same key the screen dispatches on — said in this
	// dialog's own words. Built from Submit.Keys() rather than a fresh literal
	// so a rebind of enter cannot leave this footer naming a dead key.
	confirm := key.NewBinding(
		key.WithKeys(k.Submit.Keys()...),
		key.WithHelp("enter", "duplicate"),
	)
	return []key.Binding{k.Move, k.Include, confirm, k.Back}
}

func (k keyMap) promptHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Back}
}

func (k keyMap) unsavedHelp() []key.Binding {
	return []key.Binding{k.SaveExit, k.DiscardExit, k.CancelExit}
}

// lockedHelp is the contention dialog's answer set. JumpPane is absent because
// it is conditional — it appears only at Tier 1 with a holder that named its
// pane, so lockedScreen.View appends it itself rather than advertising a key
// that would do nothing outside cats.
func (k keyMap) lockedHelp() []key.Binding {
	return []key.Binding{k.ReadOnly, k.Steal, k.Back}
}

// staleHelp is the fork a save loses on.
func (k keyMap) staleHelp() []key.Binding {
	return []key.Binding{k.Reload, k.Overwrite, k.CancelExit}
}
