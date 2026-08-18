package tui

import (
	"strings"
	"testing"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// Duplicating a note is one decision made in two places: what the dialog offers
// (duplicate.go) and what the store is then asked to write (duplicateNoteCmd).
// The property that made the feature worth building is that the two halves
// agree about categories — the part of a note that is not one of its fields,
// and the part the old web-only duplicate silently dropped.
//
// What each test here is guarding:
//
//   - The defaults ARE the feature. "D, enter" has to produce a full copy, so
//     every row the dialog shows starts checked and the title arrives prefixed.
//   - A row exists only when there is something behind it. A dead checkbox on
//     every note would be noise, and — more to the point — a row that shifted
//     into existence when the async category load landed would move the cursor
//     out from under the user.
//   - Unchecking a row removes exactly that part, and nothing else.

// dupSession builds a session on a fake store, the shape newDuplicateScreen
// needs. Same helper shape as subcatSession.
func dupSession(t *testing.T) (*session, *fakeStore, *models.User) {
	t.Helper()
	st := newFakeStore()
	u := st.addUser("dup_tester", "pw")
	sess := &session{store: st, user: u, width: 100, height: 30, cats: newCatsState()}
	return sess, st, u
}

// dupRow finds an option row by label, failing the test when the dialog does
// not offer it at all.
func dupRow(t *testing.T, s *duplicateScreen, label string) *dupOption {
	t.Helper()
	for i := range s.opts {
		if s.opts[i].label == label {
			return &s.opts[i]
		}
	}
	t.Fatalf("the duplicate dialog has no %q row; it offers %v", label, dupLabels(s))
	return nil
}

func dupLabels(s *duplicateScreen) []string {
	out := make([]string, 0, len(s.opts))
	for _, o := range s.opts {
		out = append(out, o.label)
	}
	return out
}

// richNote is a note with every optional part populated, so a test can say
// which parts a copy kept by naming the ones it did not.
func richNote(t *testing.T, fs *fakeStore, user *models.User) models.Note {
	t.Helper()
	desc, tags := "the description", "alpha,beta"
	note, err := fs.CreateNote(models.NoteInput{
		GUID:        "dup-src",
		Title:       "Release checklist",
		Description: &desc,
		Body:        strPtr("step one\nstep two"),
		Tags:        &tags,
		IsPrivate:   true,
		IsFlagged:   true,
	}, user.GUID)
	if err != nil {
		t.Fatalf("seed note: %v", err)
	}
	// Filed under one category with a selection and one without, which is the
	// pair that separates "copied the categories" from "copied the filing".
	if err = syncNoteCategories(fs, note.ID, "Work/backend, Personal", user.GUID); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	return *note
}

// loadCats runs the dialog's own category load and feeds it back in, which is
// what the event loop does between Init and the first keypress.
func loadCats(t *testing.T, s *duplicateScreen, sess *session, note models.Note) {
	t.Helper()
	msg := loadNoteCategoriesCmd(sess.store, note.ID, sess.user.GUID)()
	updated, _ := s.Update(msg)
	if updated != screen(s) {
		t.Fatal("the duplicate dialog replaced itself while loading categories")
	}
}

// TestDuplicateDialogDefaults is the "D, enter" path stated as data: the title
// carries the COPY prefix and every offered part is included.
func TestDuplicateDialogDefaults(t *testing.T) {
	sess, fs, user := dupSession(t)
	note := richNote(t, fs, user)

	s := newDuplicateScreen(sess, note)
	if got, want := s.title.Value(), "COPY Release checklist"; got != want {
		t.Errorf("title prefill = %q, want %q", got, want)
	}

	// Before the load, the category row exists but is off — a confirm that beat
	// the load must not claim to have copied categories it never saw.
	catRow := dupRow(t, s, dupLabelCategories)
	if catRow.on {
		t.Error("the category row is checked before the categories have loaded")
	}
	if _, problem := s.plan(); problem == "" {
		t.Error("confirming mid-load was allowed; it would have dropped the categories silently")
	}

	loadCats(t, s, sess, note)

	if !catRow.on {
		t.Error("the category row is not checked after loading; the default must be to keep the filing")
	}
	if got, want := catRow.detail, "Personal, Work/backend"; got != want {
		t.Errorf("category row detail = %q, want %q — it must name the selection, not just the category", got, want)
	}
	for _, label := range []string{dupLabelBody, dupLabelDescription, dupLabelTags, dupLabelPrivate, dupLabelFlagged} {
		if !dupRow(t, s, label).on {
			t.Errorf("the %q row is not checked by default", label)
		}
	}

	p, problem := s.plan()
	if problem != "" {
		t.Fatalf("plan refused: %s", problem)
	}
	if p.input.Body == nil || *p.input.Body != note.Body.String {
		t.Error("the copy does not carry the body")
	}
	if p.input.Description == nil || *p.input.Description != note.Description.String {
		t.Error("the copy does not carry the description")
	}
	if p.input.Tags == nil || *p.input.Tags != note.Tags.String {
		t.Error("the copy does not carry the tags")
	}
	if !p.input.IsPrivate || !p.input.IsFlagged {
		t.Error("the copy does not carry the private/flagged state")
	}
	if len(p.cats) != 2 {
		t.Fatalf("the copy carries %d categories, want 2", len(p.cats))
	}
}

// TestDuplicateOffersOnlyWhatTheNoteHas states the other half of the row rule:
// a bare note gets the category row (always present, so no index can shift when
// the load lands) and nothing else.
func TestDuplicateOffersOnlyWhatTheNoteHas(t *testing.T) {
	sess, fs, user := dupSession(t)
	note := fs.seedNote(user.GUID, "Just a title", "")

	s := newDuplicateScreen(sess, note)
	if got := dupLabels(s); len(got) != 1 || got[0] != dupLabelCategories {
		t.Fatalf("a note with nothing but a title offers %v, want only the category row", got)
	}

	loadCats(t, s, sess, note)
	row := dupRow(t, s, dupLabelCategories)
	if row.on || !row.off {
		t.Error("a note in no categories must leave that row off and un-toggleable")
	}
	// The row is still there — it is what keeps every focus index stable — but
	// the space bar must not be able to switch on a copy of nothing.
	s.focus = 1
	s.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if row.on {
		t.Error("space switched on a category row with no categories behind it")
	}
}

// TestDuplicateUncheckedRowsAreDropped walks the dialog the way a user does —
// arrow down, space — and checks that exactly the unchecked part goes missing.
func TestDuplicateUncheckedRowsAreDropped(t *testing.T) {
	sess, fs, user := dupSession(t)
	note := richNote(t, fs, user)

	s := newDuplicateScreen(sess, note)
	loadCats(t, s, sess, note)

	// Down once lands on the category row (index 0 of the options), space
	// unchecks it. The title field is focus 0, so this is also the assertion
	// that the space bar reaches the rows rather than the text input.
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.focus != 1 {
		t.Fatalf("focus after one ↓ is %d, want 1 (the first option row)", s.focus)
	}
	if s.takingText() {
		t.Error("takingText is true on an option row; an unclaimed ⌘ chord would be typed as a letter")
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})

	// ...and uncheck the body, two rows further down.
	for s.opts[s.focus-1].label != dupLabelBody {
		s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})

	p, problem := s.plan()
	if problem != "" {
		t.Fatalf("plan refused: %s", problem)
	}
	if len(p.cats) != 0 {
		t.Errorf("categories were copied after being unchecked (%d attached)", len(p.cats))
	}
	if p.input.Body != nil {
		t.Errorf("the body was copied after being unchecked (%q)", *p.input.Body)
	}
	// Everything else is untouched by those two presses.
	if p.input.Description == nil || p.input.Tags == nil || !p.input.IsPrivate || !p.input.IsFlagged {
		t.Error("unchecking categories and body dropped something else as well")
	}
}

// TestDuplicateTitleTakesTyping guards the one place a key means two things:
// the space bar types on the title field and toggles everywhere else.
func TestDuplicateTitleTakesTyping(t *testing.T) {
	sess, fs, user := dupSession(t)
	note := richNote(t, fs, user)

	s := newDuplicateScreen(sess, note)
	if !s.takingText() {
		t.Error("takingText is false on the title field, where every key is a character")
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	s.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	s.Update(tea.KeyPressMsg{Code: '2', Text: "2"})

	const typed = "COPY Release checklist v2"
	if got := s.title.Value(); got != typed {
		t.Fatalf("typed title = %q, want %q", got, typed)
	}
	// Nothing was toggled by that space.
	if !dupRow(t, s, dupLabelBody).on {
		t.Error("typing a space into the title also unchecked an option row")
	}

	loadCats(t, s, sess, note)
	p, problem := s.plan()
	if problem != "" {
		t.Fatalf("plan refused: %s", problem)
	}
	if p.input.Title != typed {
		t.Errorf("the copy is titled %q, want %q", p.input.Title, typed)
	}
}

// TestDuplicateNoteCmdWritesCopyAndFiling is the store half: the command has to
// create a note and reproduce each link WITH its subcategory selection. A copy
// filed under "Work" when the original is under "Work/backend" is the exact
// half-working outcome this feature exists to avoid.
func TestDuplicateNoteCmdWritesCopyAndFiling(t *testing.T) {
	sess, fs, user := dupSession(t)
	note := richNote(t, fs, user)

	s := newDuplicateScreen(sess, note)
	loadCats(t, s, sess, note)
	p, problem := s.plan()
	if problem != "" {
		t.Fatalf("plan refused: %s", problem)
	}

	msg, ok := duplicateNoteCmd(fs, p.input, p.cats, user.GUID)().(noteDuplicatedMsg)
	if !ok {
		t.Fatal("duplicateNoteCmd resolved to something other than noteDuplicatedMsg")
	}
	if msg.err != nil {
		t.Fatalf("duplicate: %v", msg.err)
	}
	if msg.note == nil {
		t.Fatal("duplicate reported success with no note")
	}
	if msg.note.ID == note.ID || msg.note.GUID == note.GUID {
		t.Error("the copy shares an identity with the original; it must be a new note")
	}

	// The filing reads back identically, spelled the same way the form and the
	// detail header spell it.
	if got, want := noteSpecs(t, fs, msg.note.ID, user.GUID), noteSpecs(t, fs, note.ID, user.GUID); got != want {
		t.Errorf("the copy is filed under %q, want %q", got, want)
	}
	if !strings.HasPrefix(msg.note.Title, dupTitlePrefix) {
		t.Errorf("the copy is titled %q, which does not mark it as a copy", msg.note.Title)
	}

	// The original is untouched — a duplicate is a read of it.
	orig, err := fs.GetNoteByID(note.ID, user.GUID)
	if err != nil || orig == nil {
		t.Fatalf("re-reading the original: %v", err)
	}
	if orig.Title != note.Title {
		t.Errorf("the original was renamed to %q", orig.Title)
	}
}

// TestDuplicateReportsAPartialCopy pins the shape of the awkward failure: the
// note is written, a category link is not. The message has to carry BOTH, or
// the list will not show a note that genuinely exists.
func TestDuplicateReportsAPartialCopy(t *testing.T) {
	sess, fs, user := dupSession(t)
	note := richNote(t, fs, user)

	s := newDuplicateScreen(sess, note)
	loadCats(t, s, sess, note)
	p, _ := s.plan()

	// Name the same category twice: the second attach is refused by the junction
	// exactly as the real one refuses a duplicate link.
	cats := append(p.cats, p.cats[0])
	msg := duplicateNoteCmd(fs, p.input, cats, user.GUID)().(noteDuplicatedMsg)

	if msg.err == nil {
		t.Fatal("a failed category attach was reported as a clean duplicate")
	}
	if msg.note == nil {
		t.Fatal("the copy was created but not returned; the list would never show it")
	}
	// The phrasing the user reads comes from the message shape, not from the
	// error text: serr's wrap message never reaches Error().
	if got := duplicateErrContext(msg.note); !strings.Contains(got, "created") {
		t.Errorf("a partial copy is reported as %q, which does not say the note exists", got)
	}
	if got := duplicateErrContext(nil); strings.Contains(got, "created") {
		t.Errorf("a copy that was never created is reported as %q", got)
	}
}
