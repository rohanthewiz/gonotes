package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// The keymap replaced six hand-written `switch msg.String()` blocks and six
// hand-written footer strings. These tests guard the two things that
// centralization is supposed to buy and that nothing else checks:
//
//  1. The bindings still carry the key strings the terminal actually sends.
//     TestKeyNames in tui_test.go pins the other end of that contract — what
//     v2 stringifies a keypress as — so together they close the loop.
//  2. A footer never advertises a key that no screen handles. That was
//     unenforceable before: the strings were literals with no connection to
//     the switch.

// TestBindingKeys pins the key strings each binding matches on. A binding whose
// keys drift out from under it is silently dead: key.Matches simply stops
// returning true, and the feature stops working with no compile error.
func TestBindingKeys(t *testing.T) {
	cases := []struct {
		name string
		b    key.Binding
		want []string
	}{
		{"force quit", keys.ForceQuit, []string{"ctrl+c"}},
		{"quit", keys.Quit, []string{"q"}},
		{"back", keys.Back, []string{"esc"}},

		{"open", keys.Open, []string{"enter"}},
		{"new", keys.New, []string{"n"}},
		{"edit", keys.Edit, []string{"e"}},
		{"delete", keys.Delete, []string{"d"}},
		{"flag", keys.Flag, []string{"f"}},
		{"categories", keys.Categories, []string{"c"}},
		// Shifted, and deliberately not sharing with DiscardExit's "d","D" —
		// that pair lives on the unsaved dialog, which this never reaches.
		{"duplicate", keys.Duplicate, []string{"D"}},

		{"scroll (help-only; the viewport consumes these)", keys.Scroll, []string{"up", "down"}},

		{"filter by category", keys.Filter, []string{"enter"}},
		{"all notes", keys.AllNotes, []string{"a"}},
		{"open subcategories", keys.Subcats, []string{"s"}},
		// Shares the space bar with TogglePrivate, on a screen that binding never
		// reaches. Pinned separately so a rebind of one cannot quietly move the
		// other.
		{"select a subcategory", keys.SelectSub, []string{"space"}},

		{"capture from an agent pane", keys.Capture, []string{"ctrl+g"}},
		{"move within the picker", keys.Move, []string{"up", "down"}},
		{"pick a pane to capture", keys.Pick, []string{"enter"}},

		{"save", keys.Save, []string{"ctrl+s"}},
		{"external editor", keys.Editor, []string{"ctrl+e"}},
		{"next field", keys.NextField, []string{"tab"}},
		{"prev field", keys.PrevField, []string{"shift+tab"}},
		{"field down", keys.FieldDown, []string{"down"}},
		{"field up", keys.FieldUp, []string{"up"}},
		{"submit", keys.Submit, []string{"enter"}},

		// The v2 break the whole key-name suite exists for.
		{"toggle private", keys.TogglePrivate, []string{"space"}},

		{"confirm yes", keys.Yes, []string{"y", "Y", "enter"}},
		// The third screen to bind the space bar, pinned separately for the same
		// reason SelectSub is: a rebind of one must not quietly move the others.
		{"include in a duplicate", keys.Include, []string{"space"}},
		{"confirm no", keys.No, []string{"n", "N", "esc", "q"}},

		// The unsaved-changes dialog. Its three answers are pinned as carefully
		// as the two-way ones because the cost of a drifted binding is asymmetric
		// here: the key that stops matching is the key whose work gets thrown
		// away.
		{"save and exit", keys.SaveExit, []string{"s", "y", "Y", "enter"}},
		{"discard unsaved changes", keys.DiscardExit, []string{"d", "D"}},
		{"keep editing", keys.CancelExit, []string{"esc", "c"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.b.Keys()
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("binding %q matches %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestTogglePrivateBindingMatchesRealSpaceKey wires the two halves together:
// the binding above, and the message a terminal actually delivers when the
// space bar is pressed. Either half can be right on its own while the pair is
// broken — which is precisely how the v1 " " binding died.
func TestTogglePrivateBindingMatchesRealSpaceKey(t *testing.T) {
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	if !key.Matches(space, keys.TogglePrivate) {
		t.Fatalf("the space bar (%q) does not match keys.TogglePrivate %v — "+
			"the private checkbox is dead", space.String(), keys.TogglePrivate.Keys())
	}
}

// TestHelpSetsAreHandled asserts that every key a screen's footer advertises is
// one that screen's Update actually dispatches on.
//
// The "handled" sets below are written out by hand rather than derived, on
// purpose: deriving them from the same keymap the footer reads would make the
// test tautological. This is a second, independent statement of what each
// screen does, so a footer gaining a row without the switch gaining a case
// fails here.
func TestHelpSetsAreHandled(t *testing.T) {
	cases := []struct {
		screen  string
		help    []key.Binding
		handled []key.Binding
	}{
		{
			screen: "browse",
			help:   keys.browseHelp(),
			handled: []key.Binding{
				keys.Open, keys.New, keys.Edit, keys.Delete,
				keys.Flag, keys.Categories, keys.Quit, keys.Back,
				keys.Duplicate, keys.Sync, keys.SummarizeClip,
				// The capture door. It is handled but deliberately NOT
				// advertised — see captureHint in capture.go.
				keys.Capture,
			},
		},
		{
			screen:  "agent picker",
			help:    keys.agentPickerHelp(),
			handled: []key.Binding{keys.Move, keys.Pick, keys.Back},
		},
		{
			screen: "categories",
			help:   keys.categoriesHelp(),
			handled: []key.Binding{
				keys.Filter, keys.Subcats, keys.AllNotes, keys.New, keys.Delete,
				keys.Back, keys.Quit,
			},
		},
		{
			screen: "subcategories",
			help:   keys.subcategoriesHelp(),
			handled: []key.Binding{
				keys.Filter, keys.SelectSub, keys.New, keys.Delete,
				keys.Back, keys.Quit,
			},
		},
		{
			screen: "detail",
			help:   keys.detailHelp(),
			handled: []key.Binding{
				// Scroll's arrows are not matched by the screen; they fall
				// through to the viewport, which is a real handler.
				keys.Scroll, keys.Edit, keys.Duplicate, keys.Flag, keys.Delete,
				keys.Back, keys.Quit,
			},
		},
		{
			screen: "form",
			help:   keys.formHelp(),
			handled: []key.Binding{
				keys.Save, keys.Editor, keys.Summarize, keys.NextField,
				keys.PrevField, keys.Submit, keys.TogglePrivate, keys.Back,
			},
		},
		{
			screen: "login",
			help:   keys.loginHelp(),
			handled: []key.Binding{
				keys.Submit, keys.NextField, keys.PrevField,
				keys.FieldDown, keys.FieldUp, keys.Back,
				// Handled by the root, above every screen.
				keys.ForceQuit,
			},
		},
		{
			screen:  "prompt",
			help:    keys.promptHelp(),
			handled: []key.Binding{keys.Submit, keys.Back},
		},
		{
			screen: "duplicate",
			help:   keys.duplicateHelp(),
			handled: []key.Binding{
				// Move is help-only here: the dispatch is on FieldUp/FieldDown,
				// which are the same two arrows named one direction at a time.
				keys.FieldUp, keys.FieldDown, keys.NextField, keys.PrevField,
				keys.Include, keys.Submit, keys.Back,
			},
		},
		{
			screen:  "unsaved changes",
			help:    keys.unsavedHelp(),
			handled: []key.Binding{keys.SaveExit, keys.DiscardExit, keys.CancelExit},
		},
		{
			// The sync dialog in the form the clock raises. Quit-anyway is
			// absent from both sides here: the screen guards it on purpose,
			// so a footer that offered it would be advertising a key that
			// does nothing.
			screen: "sync (due)",
			help:   keys.syncHelp(syncDue),
			handled: []key.Binding{
				keys.SyncGo, keys.SyncCompact, keys.SyncCompactOnly, keys.SyncLater,
			},
		},
		{
			screen: "sync (quitting)",
			help:   keys.syncHelp(syncQuitting),
			handled: []key.Binding{
				keys.SyncGo, keys.SyncCompact, keys.SyncCompactOnly,
				keys.SyncLater, keys.SyncQuitAnyway,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.screen, func(t *testing.T) {
			handled := map[string]bool{}
			for _, b := range tc.handled {
				for _, k := range b.Keys() {
					handled[k] = true
				}
			}
			for _, b := range tc.help {
				for _, k := range b.Keys() {
					if !handled[k] {
						t.Errorf("the %s footer advertises %q (%s), but the screen does not handle it",
							tc.screen, k, b.Help().Desc)
					}
				}
			}
		})
	}
}

// TestHelpSetsHaveText catches the other footer failure: a binding added to a
// help set without help text renders as nothing, silently shortening the
// footer instead of erroring.
func TestHelpSetsHaveText(t *testing.T) {
	sets := map[string][]key.Binding{
		"agentPicker":   keys.agentPickerHelp(),
		"browse":        keys.browseHelp(),
		"categories":    keys.categoriesHelp(),
		"subcategories": keys.subcategoriesHelp(),
		"detail":        keys.detailHelp(),
		"form":          keys.formHelp(),
		"login":         keys.loginHelp(),
		"prompt":        keys.promptHelp(),
		"duplicate":     keys.duplicateHelp(),
		"unsaved":       keys.unsavedHelp(),
		"sync":          keys.syncHelp(syncDue),
		"syncQuitting":  keys.syncHelp(syncQuitting),
	}
	for name, set := range sets {
		for i, b := range set {
			h := b.Help()
			if h.Key == "" || h.Desc == "" {
				t.Errorf("%s help[%d] has empty help text (key=%q desc=%q); it will render as nothing",
					name, i, h.Key, h.Desc)
			}
		}
	}
}

// TestRenderHelpJoinsBindings checks the footer formatter itself: the text it
// produces has to name every enabled binding, since the alternative is a
// footer that quietly drops one.
func TestRenderHelpJoinsBindings(t *testing.T) {
	out := stripANSI(renderHelp(keys.formHelp()...))
	for _, want := range []string{"ctrl+s save", "ctrl+e $EDITOR", "tab next field", "esc back"} {
		if !strings.Contains(out, want) {
			t.Errorf("form footer %q is missing %q", out, want)
		}
	}
	if n := strings.Count(out, "•"); n != len(keys.formHelp())-1 {
		t.Errorf("form footer %q has %d separators, want %d", out, n, len(keys.formHelp())-1)
	}
}

// TestRenderHelpSkipsDisabled covers the two ways a row drops out of a footer.
// The keyless case is the subtle one: key.Binding reports itself disabled when
// its key set is empty, so a binding written as help text alone renders as
// nothing rather than as a hint — which is why keys.Scroll carries the arrows
// it never matches on.
func TestRenderHelpSkipsDisabled(t *testing.T) {
	b := key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "should not appear"))
	b.SetEnabled(false)
	if out := stripANSI(renderHelp(b)); out != "" {
		t.Errorf("renderHelp rendered a disabled binding: %q", out)
	}

	keyless := key.NewBinding(key.WithHelp("?", "help text only"))
	if out := stripANSI(renderHelp(keyless)); out != "" {
		t.Errorf("a keyless binding rendered as %q; if this now works, keys.Scroll can drop its unused arrows", out)
	}

	if out := stripANSI(renderHelp(keys.Scroll)); !strings.Contains(out, "scroll") {
		t.Errorf("renderHelp dropped the detail screen's scroll hint: %q", out)
	}
}
