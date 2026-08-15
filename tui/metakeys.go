package tui

import (
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// The ⌘ accelerator layer: a second door onto verbs GoNotes already has, for
// hands trained on a Mac.
//
// HOW A ⌘ CHORD REACHES A TERMINAL PROGRAM AT ALL. Only through the kitty
// keyboard protocol. A compliant terminal reports ⌘S as CSI 115;9u — modifier
// 9 being 1 + kitty's super bit — and ultraviolet's CSI-u parser turns that bit
// into tea.ModSuper. Bubble Tea v2 engages the protocol unconditionally at
// startup, so every GoNotes pane is a kitty pane with no enabling work here;
// under v1 these chords could not have arrived at all. Inside cats the same
// thing happens with cats' emulator in the middle: its front end forwards a
// curated set of ⌘ chords (CMD_TO_PANE) only to panes that asked for the
// protocol, which is now all of ours.
//
// IT IS A BONUS LAYER AND NOTHING MAY LIVE HERE ALONE. Every row is a second
// way to reach a key that already works in any terminal, and the table carries
// that twin as a key.Binding so a test can hold the rule rather than a comment
// (TestMetaAccelsAreSecondDoors). On a terminal that cannot deliver ⌘ at all,
// nothing is lost — the same Tier-0 rule the rest of the cats integration keeps.
//
// WHY THERE IS NO ARMING GATE, unlike dbc's tcell version of this layer. There,
// a terminal configured to send Option as Meta produced the same ModMeta as ⌘,
// so ⌥e could have fired an accelerator, and the layer had to be armed only in
// hosts known to be safe. v2 does not have that ambiguity: Option-as-Meta
// arrives ESC-prefixed and decodes to ModAlt, while ModMeta and ModSuper come
// only from the kitty modifier bits (32 and 8). A chord can therefore only get
// here from a host that speaks the protocol and chose to forward it. Both mods
// are matched anyway — the two names for the ⌘/Windows key differ by terminal,
// and matching the one we do not expect costs nothing.
//
// THE CHORDS ARE CHOSEN FROM WHAT CATS WILL ACTUALLY FORWARD: the CMD_TO_PANE
// allowlist {S, P, E, F, D, G, /} in cmd/catway/web/index.html. ⌘W, ⌘T, ⌘N and
// friends are the browser's own and never arrive. Adding a row means checking
// that list first, because the failure is silent — an unforwarded chord is
// simply inert. TestMetaChordsAreForwardable pins the set we believe in.

// ---- the table -------------------------------------------------------------

// metaTwin is one keystroke a chord can turn into, paired with the binding that
// already matches it.
//
// The binding is not decoration. It is the machine-checkable half of the
// "nothing is ⌘-only" rule: a row whose key does not match its binding is a
// chord that translates into something no screen handles, which is exactly the
// failure this layer is otherwise silent about.
type metaTwin struct {
	key     tea.KeyPressMsg
	binding key.Binding
}

// ok reports whether this twin exists. The zero metaTwin means "this chord has
// no safe translation in this mode" — see the typing field below.
func (t metaTwin) ok() bool { return len(t.binding.Keys()) > 0 }

// metaAccel is one row of the table: one ⌘ chord and the keystroke it becomes.
//
// TWO TWINS, BECAUSE THE SAME VERB HAS TWO KEYS. GoNotes spends unmodified
// letters on commands in the list screens (e edits, f flags, d deletes) and on
// content everywhere text is entered. So a chord cannot translate to one fixed
// keystroke: ⌘E on the note list means the "e" that opens the form, while on
// the form itself it means the ctrl+e that hands the body to $EDITOR — the same
// verb, one level down. Which twin applies is decided per keystroke by whether
// the active screen is taking text; see texter.
//
// AND BECAUSE THE TRANSLATION IS THE DANGEROUS HALF. The letter this layer
// synthesizes is a real printable keystroke, Text and all — that is the point,
// it has to be indistinguishable from the user pressing the key. Deliver a
// plain "f" to a screen that is taking dictation and it is dictation: an f in
// the note title, or in a half-typed search. The incoming chord could never do
// that (see metaTranslate), so this column is where the actual hazard lives.
type metaAccel struct {
	chord rune   // the chord's letter, always lowercase — see metaChord
	label string // what it does, for test failure messages

	// command is the twin used when an unmodified key is a command here.
	command metaTwin
	// typing is the twin used while the active screen is accepting text. A
	// zero value means the chord is SWALLOWED there rather than translated,
	// which is the whole reason this field exists separately: ⌘F on the note
	// list flags a note, and ⌘F in the note body must not type an "f".
	typing metaTwin
}

// metaAccels is the table. Deliberately short: these are accelerators for the
// verbs a hand reaches for without looking, not a second copy of the keymap.
//
// It is a function rather than a package var because `keys` is itself a package
// var, and a var-to-var initialization here would depend on file order.
func metaAccels() []metaAccel {
	ctrl := func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
	// A printable key carries its text as well as its code — that is what a
	// real terminal delivers, and what a focused text widget inserts. Building
	// the twin any other way would work for key.Matches and then behave
	// differently the moment one reached an input.
	plain := func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

	save := metaTwin{ctrl('s'), keys.Save}
	capture := metaTwin{ctrl('g'), keys.Capture}

	return []metaAccel{
		// ⌘S and ⌘G already have ctrl twins that mean the same thing in every
		// mode, so both columns are the same keystroke.
		{'s', "save the note", save, save},
		{'g', "capture an agent pane", capture, capture},

		// ⌘E is the row the two columns exist for.
		{'e', "edit", metaTwin{plain('e'), keys.Edit}, metaTwin{ctrl('e'), keys.Editor}},

		// Flag and delete are list verbs with no text-mode counterpart. The
		// zero typing twin is the instruction to swallow.
		{'f', "flag the note", metaTwin{plain('f'), keys.Flag}, metaTwin{}},
		{'d', "delete the note", metaTwin{plain('d'), keys.Delete}, metaTwin{}},

		// ⌘/ opens the fuzzy filter, which belongs to the bubbles list rather
		// than to our keymap — hence the binding from its default set. Its
		// twin is a printable "/", so it is swallowed while typing for the
		// same reason f and d are.
		{'/', "filter the list", metaTwin{plain('/'), list.DefaultKeyMap().Filter}, metaTwin{}},
	}
}

// ---- what the active screen is doing with plain keys -----------------------

// texter is implemented by screens where an unmodified printable key is CONTENT
// the user is entering rather than a command to run. It is consulted for the
// top of the stack only, on every ⌘ chord.
//
// Sibling of refresher and restyler in tui.go, and declared here instead
// because this layer is its only consumer. Two kinds of screen implement it:
// those that are always taking text (the form, login, the new-category prompt)
// and the two list screens, which are taking text only while their fuzzy filter
// prompt is up. A screen that does not implement it is in command mode, which
// is the right default — a new screen has to opt IN to having its keystrokes
// protected, and the cost of forgetting is a chord that does nothing rather
// than one that types.
type texter interface {
	takingText() bool
}

// takingText answers for the active screen.
func (m appModel) takingText() bool {
	t, ok := m.top().(texter)
	return ok && t.takingText()
}

// ---- translation -----------------------------------------------------------

// metaTranslate resolves a keypress against the accelerator table. Three
// outcomes, and the middle one is the point of the whole file:
//
//	(twin, true)   an armed ⌘ chord this table claims — dispatch twin instead
//	(nil,  true)   a ⌘ chord nothing claims here — SWALLOW it
//	(nil,  false)  an ordinary key — pass it through untouched
//
// EVERY ⌘ CHORD IS CONSUMED, claimed or not — the chords cats forwards but
// GoNotes has no use for (⌘P), and the ones a host would normally keep for
// itself (⌘K, ⌘B, ⌘V) in the event one is ever handed down. Reaching here at
// all means the host declined to handle it, and there is nothing sensible left
// to do with a key the user never meant for us.
//
// Be precise about what that buys, because it is less than the equivalent line
// in dbc's version claims. Under v2 an unclaimed chord that fell through would
// already be inert: key.Matches compares against Key.String(), which is
// "super+f" and matches no binding, and every text widget inserts Key.Text,
// which the CSI-u decoder deliberately leaves EMPTY for any modifier above
// shift (decoder.go: "we need to clear the text if we have a modifier key
// other than a ModShift key"). So this is not repairing a live bug; it is
// making "a ⌘ chord stops here" a property of this file rather than a
// coincidence of three upstream implementation details.
func metaTranslate(k tea.KeyPressMsg, takingText bool) (*tea.KeyPressMsg, bool) {
	if !k.Mod.Contains(tea.ModSuper) && !k.Mod.Contains(tea.ModMeta) {
		return nil, false
	}

	r, shifted := metaChord(k)
	if shifted {
		// No row has a shifted form, and there is no sensible unshifted
		// reading of one: ⌘⇧D is not "delete". Consumed, not translated.
		return nil, true
	}

	for _, a := range metaAccels() {
		if a.chord != r {
			continue
		}
		twin := a.command
		if takingText {
			twin = a.typing
		}
		if !twin.ok() {
			return nil, true
		}
		out := twin.key
		return &out, true
	}
	return nil, true
}

// metaChord folds a ⌘ keystroke into a (lowercase rune, shift) pair.
//
// Two hosts spell the same chord two ways: the kitty protocol reports the
// UNSHIFTED codepoint with the shift bit set, while an emitter reporting the
// produced character sends the capital. Folding both here lets the table name
// each chord once.
//
// BaseCode wins when present for the same reason Key.Keystroke prefers it: it
// is the key's position under the standard PC-101 layout, which is what cats
// matched on (KeyboardEvent.code) when it decided to forward the chord in the
// first place. Without it the table would mean "whatever ⌘S produces on this
// layout" rather than "the ⌘S key", and an AZERTY keyboard would translate the
// wrong row.
func metaChord(k tea.KeyPressMsg) (r rune, shifted bool) {
	r = k.Code
	if k.BaseCode != 0 {
		r = k.BaseCode
	}
	shifted = k.Mod.Contains(tea.ModShift)
	if unicode.IsUpper(r) {
		shifted = true
		r = unicode.ToLower(r)
	}
	return r, shifted
}
