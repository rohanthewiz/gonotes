package tui

import (
	"strings"
	"testing"
	"time"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
	// Not charm.land — see the import note in tui_test.go.
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// The unsaved-changes guard has two halves that fail in opposite directions,
// and both are silent:
//
//   - a dirty check that reports false loses work — esc throws away an edit
//     with no warning, which is the bug the dialog exists to prevent;
//   - a dirty check that reports true on an untouched form makes esc stop and
//     ask every single time, which trains the user to answer without reading
//     and puts them right back where they started.
//
// So the tests below pin BOTH: the paths that must be clean (a freshly opened
// form, an async category load, a typed-then-undone edit) and the paths that
// must be dirty (any real change, and a capture nobody typed).

// formFixture builds an edit form over a seeded note, wired to a session the
// way browse would wire it.
func formFixture(t *testing.T) (*formScreen, *fakeStore, models.Note) {
	t.Helper()
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	note := fs.seedNote(user.GUID, "Seeded note", "seeded body")
	sess := &session{store: fs, cats: newCatsState(), user: user, width: 100, height: 40}
	return newFormScreen(sess, &note), fs, note
}

// press feeds a screen one keystroke and returns whatever it produced.
func press(t *testing.T, s screen, k tea.KeyPressMsg) (screen, tea.Cmd) {
	t.Helper()
	return s.Update(k)
}

// escKey and the rune helper keep the message construction out of the tests.
var escKey = tea.KeyPressMsg{Code: tea.KeyEscape}

func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// pushedScreen returns the screen a command pushes, or nil if it pushes none.
// tea.Sequence's message type is unexported, so a command that sequences pops
// and statuses reads as "not a push" here — which is exactly the distinction
// every test below needs.
func pushedScreen(cmd tea.Cmd) screen {
	for _, msg := range drainCmd(cmd) {
		if p, ok := msg.(pushMsg); ok {
			return p.s
		}
	}
	return nil
}

// ---- the dirty check -------------------------------------------------------

func TestAFreshlyOpenedFormIsClean(t *testing.T) {
	form, _, _ := formFixture(t)
	if form.dirty() {
		t.Error("a form that has taken no input reports unsaved changes; esc would stop to ask on every note opened and closed")
	}
}

// The category field is filled by a command, not by the user. If the baseline
// did not move with it, every edit form in the program would be dirty from the
// moment its categories arrived — the failure that makes the dialog noise.
func TestTheAsyncCategoryLoadDoesNotDirtyTheForm(t *testing.T) {
	form, _, note := formFixture(t)

	form.Update(noteCatsLoadedMsg{cats: []models.NoteCategoryDetailOutput{
		{ID: 1, Name: "Work", SelectedSubcategories: []string{"backend"}},
	}})

	if got := form.categories.Value(); !strings.Contains(got, "Work") {
		t.Fatalf("the load did not reach the field: categories = %q", got)
	}
	if form.dirty() {
		t.Errorf("note %q reports unsaved changes after its own categories loaded", note.Title)
	}
}

// The value comparison earning its keep: a flag flipped on keypress would call
// this dirty, and the dialog would be asking about a note that is byte for byte
// what it was.
func TestTypingAndUndoingLeavesTheFormClean(t *testing.T) {
	form, _, _ := formFixture(t)

	form.Update(runeKey('X'))
	if !form.dirty() {
		t.Fatal("typing a character did not register as a change")
	}
	form.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

	if form.dirty() {
		t.Error("the form is back to its stored contents but still reports unsaved changes")
	}
}

// Every field, not just the one the cursor happened to start in. A guard that
// watches the title and misses the body is worse than no guard, because the
// body is where the work is.
func TestEveryFieldCountsAsAChange(t *testing.T) {
	cases := []struct {
		field string
		edit  func(f *formScreen)
	}{
		{"title", func(f *formScreen) { f.title.SetValue("changed") }},
		{"description", func(f *formScreen) { f.desc.SetValue("changed") }},
		{"tags", func(f *formScreen) { f.tags.SetValue("changed") }},
		{"categories", func(f *formScreen) { f.categories.SetValue("Work") }},
		{"body", func(f *formScreen) { f.body.SetValue("changed") }},
		{"private", func(f *formScreen) { f.isPrivate = !f.isPrivate }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			form, _, _ := formFixture(t)
			tc.edit(form)
			if !form.dirty() {
				t.Errorf("a change to %s does not count as unsaved work; esc would discard it silently", tc.field)
			}
		})
	}
}

// A captured pane is unsaved work that exists nowhere else — the text is gone
// from the form's point of view the moment esc pops it. See prefill.
func TestAPrefilledCaptureFormIsDirty(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	sess := &session{store: fs, cats: newCatsState(), user: user, width: 100, height: 40}

	form := newFormScreen(sess, nil)
	form.prefill("Capture: claude", captureTag, "what the agent printed")

	if !form.dirty() {
		t.Error("a captured note reports no unsaved changes; one esc would drop the capture with no warning")
	}
}

// ---- esc on the form -------------------------------------------------------

func TestEscLeavesACleanFormImmediately(t *testing.T) {
	form, _, _ := formFixture(t)

	_, cmd := press(t, form, escKey)

	if s := pushedScreen(cmd); s != nil {
		t.Errorf("esc on an untouched form pushed a %T; it should just leave", s)
	}
}

func TestEscOnADirtyFormRaisesTheDialog(t *testing.T) {
	form, _, note := formFixture(t)
	form.body.SetValue("a paragraph worth keeping")

	_, cmd := press(t, form, escKey)

	pushed := pushedScreen(cmd)
	dialog, ok := pushed.(*unsavedScreen)
	if !ok {
		t.Fatalf("esc on a dirty form produced %T, want *unsavedScreen — the edit would be gone", pushed)
	}
	if !strings.Contains(dialog.View(), note.Title) {
		t.Errorf("the dialog does not name the note at risk:\n%s", stripANSI(dialog.View()))
	}
}

// The dialog has to say which note it is about even when there is no saved
// title to quote, which is the whole reason unsavedPrompt has three branches.
func TestTheDialogNamesWhatIsAtRisk(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	sess := &session{store: fs, cats: newCatsState(), user: user, width: 100, height: 40}

	untitled := newFormScreen(sess, nil)
	untitled.body.SetValue("body but no title yet")
	if got := untitled.unsavedPrompt(); !strings.Contains(got, "new note") {
		t.Errorf("an untitled new note is described as %q", got)
	}

	titled := newFormScreen(sess, nil)
	titled.title.SetValue("Draft")
	if got := titled.unsavedPrompt(); !strings.Contains(got, "Draft") {
		t.Errorf("a titled new note is described as %q, want its title quoted", got)
	}
}

// ---- the dialog's three answers --------------------------------------------

// Save is observable without running the command: formScreen.save validates and
// sets the busy flag on the way to issuing the write.
func TestTheDialogSaveArmSavesTheForm(t *testing.T) {
	form, _, _ := formFixture(t)
	form.body.SetValue("kept")
	dialog := newUnsavedScreen(form.sess, form.unsavedPrompt(), form.save, form.abandon)

	dialog.Update(runeKey('s'))

	if !form.busy {
		t.Error("the save arm did not issue the form's save")
	}
}

func TestTheDialogDiscardArmDoesNotSave(t *testing.T) {
	form, fs, note := formFixture(t)
	form.body.SetValue("thrown away")
	dialog := newUnsavedScreen(form.sess, form.unsavedPrompt(), form.save, form.abandon)

	dialog.Update(runeKey('d'))

	if form.busy {
		t.Fatal("the discard arm issued a save")
	}
	stored, err := fs.GetNoteByID(note.ID, form.sess.user.GUID)
	if err != nil || stored == nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if stored.Body.String != "seeded body" {
		t.Errorf("the note was written anyway: body = %q", stored.Body.String)
	}
}

// esc keeps editing. It must not save and must not leave — the arm with no
// side effect at all beyond popping the dialog itself.
func TestTheDialogEscArmKeepsEditing(t *testing.T) {
	form, _, _ := formFixture(t)
	form.body.SetValue("still being written")
	dialog := newUnsavedScreen(form.sess, form.unsavedPrompt(), form.save, form.abandon)

	_, cmd := dialog.Update(escKey)

	if form.busy {
		t.Error("esc on the dialog saved the note")
	}
	msgs := drainCmd(cmd)
	if len(msgs) != 1 {
		t.Fatalf("esc produced %d messages, want a single pop", len(msgs))
	}
	if _, ok := msgs[0].(popMsg); !ok {
		t.Errorf("esc produced %T, want popMsg — the dialog alone should come off the stack", msgs[0])
	}
}

// A key the dialog does not bind must do nothing. This is a modal over unsaved
// work: any keystroke that falls through to a default would be a decision the
// user did not make.
func TestTheDialogIgnoresUnboundKeys(t *testing.T) {
	form, _, _ := formFixture(t)
	form.body.SetValue("in progress")
	dialog := newUnsavedScreen(form.sess, form.unsavedPrompt(), form.save, form.abandon)

	if _, cmd := dialog.Update(runeKey('z')); cmd != nil {
		t.Error("an unbound key produced a command")
	}
	if form.busy {
		t.Error("an unbound key saved the note")
	}
}

// The three answers must not overlap: one keystroke, one outcome. Sharing a key
// between save and discard would make the outcome depend on switch order rather
// than on what the user pressed.
func TestTheDialogAnswersDoNotShareKeys(t *testing.T) {
	seen := map[string]string{}
	for _, b := range keys.unsavedHelp() {
		for _, k := range b.Keys() {
			if prev, dup := seen[k]; dup {
				t.Errorf("%q answers both %q and %q", k, prev, b.Help().Desc)
			}
			seen[k] = b.Help().Desc
		}
	}
}

// ---- esc on the browse list ------------------------------------------------

// esc quits, but only once there is nothing left to undo. The peel order is the
// point: a key that quit from a filtered list would throw away the narrowing the
// user built up, and they would have to rebuild it to get back.
func TestEscQuitsOnlyFromTheHomeView(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	fs.seedNote(user.GUID, "Alpha note", "body")
	sess := &session{store: fs, cats: newCatsState(), user: user, width: 100, height: 40}

	browse := newBrowseScreen(sess)
	browse.catFilter = &models.Category{ID: 1, Name: "Work"}
	browse.subFilter = []string{"backend"}

	// First esc drops the subcategory, second the category, third quits.
	_, cmd := press(t, browse, escKey)
	if isQuit(cmd) {
		t.Fatal("esc quit while a subcategory filter was still applied")
	}
	if browse.subFilter != nil {
		t.Fatal("esc did not drop the subcategory filter")
	}

	_, cmd = press(t, browse, escKey)
	if isQuit(cmd) {
		t.Fatal("esc quit while a category filter was still applied")
	}
	if browse.catFilter != nil {
		t.Fatal("esc did not drop the category filter")
	}

	if _, cmd = press(t, browse, escKey); !isQuit(cmd) {
		t.Error("esc on the home view did not quit")
	}
}

// isQuit recognizes the command tea.Quit returns. Comparing the function value
// is not possible, so the test compares the message it produces — tea.QuitMsg
// is exported precisely so a test can.
func isQuit(cmd tea.Cmd) bool {
	for _, msg := range drainCmd(cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

// ---- end to end ------------------------------------------------------------

// The payoff test: a real program, a real edit, and the real key. Everything
// above tests a screen in isolation; this is the only place that proves the
// dialog is actually reachable by pressing esc in a running TUI, that its answer
// reaches the store, and that the stack unwinds back to the list afterwards.
func TestEscOnADirtyFormOffersToSaveEndToEnd(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	note := fs.seedNote(user.GUID, "Alpha note", "original body")

	tm := teatest.NewTestModel(t, newAppModel(fs), teatest.WithInitialTermSize(100, 40))
	tm.Send(loggedInMsg{user: user})
	// See TestBrowseScreenRendersSeededNotes for why the size is re-sent.
	tm.Send(tea.WindowSizeMsg{Width: 100, Height: 40})

	// Each key is sent only after the screen it is meant for has drawn, and that
	// is not politeness — a key that opens a screen does so through a command
	// whose result is re-queued, and nothing orders that against a later Send.
	// Firing e/!/esc back to back races: the "!" can reach the LIST, leaving the
	// form clean and esc with nothing to warn about. (The same trap is documented
	// at TestCaptureLandsInAPrefilledForm.)
	//
	// Reading the output in stages is safe for the same reason it is usually
	// wrong: teatest's Output() is one consumable stream, so each WaitFor resumes
	// where the last stopped — fine here, because every string waited for is
	// printed in response to a key sent after the previous wait returned.
	waitForAll(t, tm, "Alpha note") // the list drew
	tm.Send(runeKey('e'))

	waitForAll(t, tm, "Edit: Alpha note") // e opened the form
	tm.Send(runeKey('!'))                 // now dirty
	tm.Send(escKey)

	waitForAll(t, tm, "Unsaved changes") // esc stopped to ask instead of discarding
	tm.Send(runeKey('s'))

	waitForAll(t, tm, "Saved") // s took the offer

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	stored, err := fs.GetNoteByID(note.ID, user.GUID)
	if err != nil || stored == nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if stored.Title != "Alpha note!" {
		t.Errorf("the saved title is %q, want %q — the dialog's save arm did not reach the store",
			stored.Title, "Alpha note!")
	}
}
