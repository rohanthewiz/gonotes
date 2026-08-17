package tui

import (
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"gonotes/models"
)

// Tests for the ⌘ accelerator layer. The feature is invisible when it works and
// silent when it breaks, so three separate things need holding down:
//
//	the invariant   no verb is ⌘-only, and no chord translates into a dead key
//	the fold        super/meta/shift/layout all resolve to the same table row
//	the swallow     an unclaimed chord never reaches a text field as a letter
//
// The last is the one with a user-visible failure mode, and it is exercised
// through the root model rather than the resolver alone.

// ---- helpers ---------------------------------------------------------------

// superKey builds the keystroke a kitty-speaking host delivers for ⌘<r>: the
// unshifted codepoint with the super bit set and no text, which is what the
// CSI-u decoder produces (it clears Text for any modifier above shift).
func superKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModSuper}
}

// appWith builds a root model sitting on one screen, with no program running.
// Update is a pure function of (model, msg), so the ⌘ layer can be driven
// straight through it.
func appWith(sess *session, s screen) appModel {
	return appModel{sess: sess, stack: []screen{s}}
}

// testSession is the minimum a screen needs to render and dispatch: a size, a
// user, and the inert Tier-0 cats state every screen assumes is non-nil.
func testSession(st Store) *session {
	return &session{
		store:  st,
		cats:   newCatsState(),
		user:   &models.User{GUID: "meta-test-user"},
		width:  80,
		height: 24,
	}
}

// ---- the invariant ---------------------------------------------------------

// TestMetaAccelsAreSecondDoors is the rule the whole layer rests on: every
// chord translates into a keystroke that some binding already matches, so
// nothing is reachable ONLY by ⌘.
//
// It is also the only thing that would catch the layer's silent failure. A twin
// whose keystroke drifts out from under its binding — a binding rebound, a
// v2 rename like " " → "space" — does not fail to compile and does not fail to
// run. It just makes the accelerator do nothing.
func TestMetaAccelsAreSecondDoors(t *testing.T) {
	for _, a := range metaAccels() {
		for _, tc := range []struct {
			mode string
			twin metaTwin
		}{
			{"command", a.command},
			{"typing", a.typing},
		} {
			if !tc.twin.ok() {
				continue // a deliberately swallowed mode; see metaAccel.typing
			}
			if !key.Matches(tc.twin.key, tc.twin.binding) {
				t.Errorf("⌘%c (%s), %s mode: translates to %q, which does not match its binding %v — "+
					"the accelerator is dead",
					a.chord, a.label, tc.mode, tc.twin.key.String(), tc.twin.binding.Keys())
			}
		}
	}
}

// TestMetaChordsAreForwardable pins the table against cats' CMD_TO_PANE
// allowlist (cmd/catway/web/index.html). A chord outside that set never leaves
// the browser, so the row would be inert — and inert silently, which is why the
// list is restated here rather than trusted to memory.
//
// Ghostty and kitty forward more than this; the allowlist is the narrower of
// the two hosts, so satisfying it satisfies both.
func TestMetaChordsAreForwardable(t *testing.T) {
	forwardable := map[rune]bool{'s': true, 'p': true, 'e': true, 'f': true, 'd': true, 'g': true, '/': true}
	for _, a := range metaAccels() {
		if !forwardable[a.chord] {
			t.Errorf("⌘%c (%s) is not on cats' CMD_TO_PANE allowlist — the chord will never arrive",
				a.chord, a.label)
		}
	}
}

// TestMetaAccelChordsAreUnique guards the one way a table lookup can go wrong
// quietly: a duplicate row is unreachable, because the first match wins.
func TestMetaAccelChordsAreUnique(t *testing.T) {
	seen := map[rune]string{}
	for _, a := range metaAccels() {
		if prev, dup := seen[a.chord]; dup {
			t.Errorf("⌘%c is bound twice (%s and %s); the second row can never fire",
				a.chord, prev, a.label)
		}
		seen[a.chord] = a.label
	}
}

// ---- translation -----------------------------------------------------------

// TestMetaTranslate walks the table from the outside: what does each chord
// become, in each mode. The "" want means the chord is claimed and swallowed.
func TestMetaTranslate(t *testing.T) {
	cases := []struct {
		name       string
		key        tea.KeyPressMsg
		takingText bool
		want       string // "" = swallowed
	}{
		{"⌘S saves", superKey('s'), false, "ctrl+s"},
		{"⌘S saves while typing too — the form is where saving happens",
			superKey('s'), true, "ctrl+s"},

		{"⌘E opens the form from a list", superKey('e'), false, "e"},
		{"⌘E opens $EDITOR from the form", superKey('e'), true, "ctrl+e"},

		{"⌘G opens the capture picker", superKey('g'), false, "ctrl+g"},
		{"⌘G works from the form as well", superKey('g'), true, "ctrl+g"},

		{"⌘F flags", superKey('f'), false, "f"},
		{"⌘F is swallowed while typing", superKey('f'), true, ""},

		{"⌘D deletes", superKey('d'), false, "d"},
		{"⌘D is swallowed while typing", superKey('d'), true, ""},

		{"⌘/ filters", superKey('/'), false, "/"},
		{"⌘/ is swallowed while typing", superKey('/'), true, ""},

		// Forwarded by cats, claimed by nothing here.
		{"⌘P is swallowed", superKey('p'), false, ""},
		// Not forwarded at all, but if a host ever hands one down it must not
		// land in the note as a letter.
		{"⌘K is swallowed", superKey('k'), false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			twin, claimed := metaTranslate(tc.key, tc.takingText)
			if !claimed {
				t.Fatalf("%q was not claimed by the ⌘ layer; it would fall through to the screen",
					tc.key.String())
			}
			switch {
			case tc.want == "" && twin != nil:
				t.Fatalf("expected %q to be swallowed, got twin %q", tc.key.String(), twin.String())
			case tc.want != "" && twin == nil:
				t.Fatalf("expected %q to translate to %q, got swallowed", tc.key.String(), tc.want)
			case tc.want != "" && twin.String() != tc.want:
				t.Fatalf("%q translated to %q, want %q", tc.key.String(), twin.String(), tc.want)
			}
		})
	}
}

// TestOrdinaryKeysPassThrough is the other half of the claim: the layer is
// invisible to every key that is not a ⌘ chord. A false positive here would
// swallow ordinary typing.
func TestOrdinaryKeysPassThrough(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{
		{Code: 'e', Text: "e"},
		{Code: 's', Mod: tea.ModCtrl},
		{Code: 'e', Mod: tea.ModAlt}, // ⌥e: an accent composition, not an accelerator
		{Code: tea.KeyEnter},
		{Code: tea.KeySpace, Text: " "},
	} {
		if twin, claimed := metaTranslate(k, false); claimed {
			t.Errorf("%q was claimed by the ⌘ layer (twin %v); it must pass through untouched",
				k.String(), twin)
		}
	}
}

// TestMetaFold covers the spellings of one chord. All of these are ⌘E and must
// resolve to the same row; the shifted forms are claimed but not translated,
// because no row has a shifted meaning and ⌘⇧E must not arrive as "E".
func TestMetaFold(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"kitty super", tea.KeyPressMsg{Code: 'e', Mod: tea.ModSuper}, "e"},
		{"reported as meta", tea.KeyPressMsg{Code: 'e', Mod: tea.ModMeta}, "e"},
		{"AZERTY: the physical key wins", tea.KeyPressMsg{Code: 'é', BaseCode: 'e', Mod: tea.ModSuper}, "e"},
		{"a host reporting the produced capital", tea.KeyPressMsg{Code: 'E', Mod: tea.ModSuper}, ""},
		{"⌘⇧E", tea.KeyPressMsg{Code: 'e', Mod: tea.ModSuper | tea.ModShift}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			twin, claimed := metaTranslate(tc.key, false)
			if !claimed {
				t.Fatalf("%v was not recognized as a ⌘ chord", tc.key)
			}
			got := ""
			if twin != nil {
				got = twin.String()
			}
			if got != tc.want {
				t.Fatalf("translated to %q, want %q", got, tc.want)
			}
		})
	}
}

// ---- through the root model ------------------------------------------------

// TestMetaChordOpensTheFilter drives the whole path — root Update, translation,
// re-dispatch, the bubbles list — for a chord whose twin belongs to a widget
// rather than to our keymap.
func TestMetaChordOpensTheFilter(t *testing.T) {
	browse := fixtureBrowse(t, 80, 24)
	m := appWith(browse.sess, browse)

	if browse.list.FilterState() == list.Filtering {
		t.Fatal("the fixture starts already filtering")
	}
	m.Update(superKey('/'))
	if browse.list.FilterState() != list.Filtering {
		t.Fatalf("⌘/ did not open the fuzzy filter (state %v)", browse.list.FilterState())
	}
}

// TestMetaChordOpensTheForm covers the other command-mode shape: a twin that is
// a bare letter our own keymap matches.
//
// The observable is the LOCK request, not the form. Editing now claims the note
// before opening it, so the first thing ⌘E produces is an acquire; the form is
// pushed from the reply. That indirection is exactly what this test has to
// tolerate — it is about the chord reaching keys.Edit, not about what keys.Edit
// does once it gets there — so the second half drives the reply through and
// checks the form still arrives.
func TestMetaChordOpensTheForm(t *testing.T) {
	browse := fixtureBrowse(t, 80, 24)
	m := appWith(browse.sess, browse)

	_, cmd := m.Update(superKey('e'))
	if cmd == nil {
		t.Fatal("⌘E produced no command; the edit path was never reached")
	}
	acquired, ok := cmd().(lockAcquiredMsg)
	if !ok {
		t.Fatalf("⌘E produced %T, want a lockAcquiredMsg", cmd())
	}
	if acquired.err != nil || acquired.blockedBy != nil {
		t.Fatalf("the lock on an unheld note was refused: err=%v blocked=%v",
			acquired.err, acquired.blockedBy)
	}

	_, cmd = m.Update(acquired)
	if cmd == nil {
		t.Fatal("a granted lock pushed nothing; the form never opened")
	}
	push, ok := cmd().(pushMsg)
	if !ok {
		t.Fatalf("a granted lock produced %T, want a pushMsg", cmd())
	}
	if _, ok := push.s.(*formScreen); !ok {
		t.Fatalf("⌘E pushed %T, want *formScreen", push.s)
	}
}

// TestMetaChordSavesFromTheForm is the ctrl-twin path on a text screen. The
// empty-title rejection is used as the proof of arrival: it is the first thing
// save() does, and it needs no store behind it.
func TestMetaChordSavesFromTheForm(t *testing.T) {
	sess := testSession(newFakeStore())
	form := newFormScreen(sess, nil)
	m := appWith(sess, form)

	_, cmd := m.Update(superKey('s'))
	if cmd == nil {
		t.Fatal("⌘S produced no command; it never reached keys.Save")
	}
	note, ok := cmd().(statusNote)
	if !ok || note.text != "Title is required" {
		t.Fatalf("⌘S produced %#v, want the save path's empty-title rejection", cmd())
	}
}

// TestMetaChordIsInertOnTheForm pins the quiet outcome: a chord with no
// text-mode twin does nothing at all on a screen full of text inputs.
//
// Note which half it is testing. A raw ⌘F is already inert under v2 — it
// carries no Text for a widget to insert — so removing the layer entirely would
// leave this passing. What it does catch is the layer translating anyway: drop
// the takingText check and it fails with `⌘f typed into the title field: "f"`,
// because the twin this file synthesizes is a printable key and the incoming
// chord was not.
func TestMetaChordIsInertOnTheForm(t *testing.T) {
	sess := testSession(newFakeStore())
	form := newFormScreen(sess, nil)
	m := appWith(sess, form)

	for _, chord := range []rune{'f', 'd', '/', 'p'} {
		if _, cmd := m.Update(superKey(chord)); cmd != nil {
			t.Errorf("⌘%c produced a command on the form; it should have been swallowed", chord)
		}
		if got := form.title.Value(); got != "" {
			t.Fatalf("⌘%c typed into the title field: %q", chord, got)
		}
	}
}

// TestMetaChordDoesNotTypeIntoTheFilterPrompt is the failure the two-column
// table exists to prevent, and the one that is genuinely reachable.
//
// The twin this layer synthesizes is a real printable keystroke — Text and all,
// because it has to be indistinguishable from the user pressing the key. So a
// ⌘D translated to "d" without checking the mode does not "do nothing" during a
// search: it appends a d to the search box. Delete the takingText check in
// metaTranslate and this test fails with `want "al", got "ald"`.
func TestMetaChordDoesNotTypeIntoTheFilterPrompt(t *testing.T) {
	browse := fixtureBrowse(t, 80, 24)
	m := appWith(browse.sess, browse)

	m.Update(superKey('/')) // open the filter prompt
	for _, r := range "al" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := browse.list.FilterValue(); got != "al" {
		t.Fatalf("the search box holds %q before the chord, want %q", got, "al")
	}

	for _, chord := range []rune{'d', 'f', '/'} {
		m.Update(superKey(chord))
		if got := browse.list.FilterValue(); got != "al" {
			t.Fatalf("⌘%c typed into the search box: %q", chord, got)
		}
	}
}

// TestFilteringBrowseIsTakingText pins the dynamic half of texter. The list
// screens are in command mode most of the time and taking text only while the
// filter prompt is up, and the ⌘ layer has to notice the difference — otherwise
// ⌘D during a search deletes the note the cursor happens to be on.
func TestFilteringBrowseIsTakingText(t *testing.T) {
	browse := fixtureBrowse(t, 80, 24)
	m := appWith(browse.sess, browse)

	if m.takingText() {
		t.Fatal("an unfiltered browse screen reports itself as taking text")
	}

	m.Update(superKey('/')) // open the filter prompt
	if !m.takingText() {
		t.Fatal("a filtering browse screen does not report itself as taking text")
	}

	// And now the chord that would otherwise delete a note mid-search.
	if twin, claimed := metaTranslate(superKey('d'), m.takingText()); !claimed || twin != nil {
		t.Fatalf("⌘D while filtering: claimed=%v twin=%v, want swallowed", claimed, twin)
	}
}
