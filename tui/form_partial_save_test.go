package tui

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// A save is two steps: write the note, then reconcile its categories. These
// tests cover the cases where the two do not share a fate. Each used to end
// in quiet damage: a second copy of the note, or a note stripped of its
// categories by a save that only meant to change its body.

var errCategoryStep = errors.New("category store unavailable")

// deliverSave runs the form's save and feeds the result back to the form, the
// way the runtime would, returning the status texts the form produced.
func deliverSave(t *testing.T, form *formScreen) (statuses []string) {
	t.Helper()
	var saved *noteSavedMsg
	for _, msg := range drainCmd(form.save()) {
		switch m := msg.(type) {
		case noteSavedMsg:
			saved = &m
		case statusNote:
			statuses = append(statuses, m.text)
		}
	}
	if saved == nil {
		return statuses // the save did not start (a status says why)
	}
	_, cmd := form.Update(*saved)
	for _, msg := range drainCmd(cmd) {
		if st, ok := msg.(statusNote); ok {
			statuses = append(statuses, st.text)
		}
	}
	return statuses
}

// linkedCategoryNames reads the note's category names straight from the fake.
func linkedCategoryNames(t *testing.T, fs *fakeStore, noteID int64, userGUID string) []string {
	t.Helper()
	details, err := fs.GetNoteCategoryDetails(noteID, userGUID)
	if err != nil {
		t.Fatalf("reading the note's categories: %v", err)
	}
	var names []string
	for _, d := range details {
		spec := d.Name
		if len(d.SelectedSubcategories) > 0 {
			spec += "/" + strings.Join(d.SelectedSubcategories, "/")
		}
		names = append(names, spec)
	}
	sort.Strings(names)
	return names
}

func newNoteForm(fs *fakeStore, user *models.User) *formScreen {
	sess := &session{store: fs, cats: newCatsState(), user: user, width: 100, height: 40}
	return newFormScreen(sess, nil)
}

// A new note whose category step fails has still been created. The form must
// carry on as an edit of that note: a retry that started over would create it
// again, once per attempt.
func TestRetryingAHalfSavedNewNoteDoesNotCreateItAgain(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	form := newNoteForm(fs, user)
	form.title.SetValue("Written once")
	form.body.SetValue("the body")
	form.categories.SetValue("Work/backend")

	fs.failCategoryDetails = errCategoryStep
	statuses := deliverSave(t, form)

	if len(fs.notes) != 1 {
		t.Fatalf("after the first save there are %d notes, want 1", len(fs.notes))
	}
	if form.editing == nil || form.editing.ID != fs.notes[0].ID {
		t.Fatal("the form did not adopt the note it created; the next save would create another")
	}
	if joined := strings.Join(statuses, " | "); !strings.Contains(joined, "Note saved, but its categories were not updated") {
		t.Errorf("status does not say the note itself was saved: %q", joined)
	}
	if !form.dirty() {
		t.Error("the form reports no unsaved work, but its categories were never saved")
	}

	// The same failure again, then success: still exactly one note.
	deliverSave(t, form)
	fs.failCategoryDetails = nil
	deliverSave(t, form)

	if len(fs.notes) != 1 {
		t.Fatalf("after two retries there are %d notes, want 1 (each retry made a copy)", len(fs.notes))
	}
	if got := linkedCategoryNames(t, fs, fs.notes[0].ID, user.GUID); len(got) != 1 || got[0] != "Work/backend" {
		t.Errorf("the note's categories after the retry = %v, want [Work/backend]", got)
	}
}

// An existing note whose category step fails has moved to a new version. A
// retry has to name that version, or it is refused as stale against the
// user's own save a moment earlier.
func TestRetryingAHalfSavedEditIsNotRefusedAsStale(t *testing.T) {
	form, fs, note := formFixture(t)
	form.Update(noteCatsLoadedMsg{noteID: note.ID}) // the prefill: no categories
	form.body.SetValue("edited body")
	form.categories.SetValue("Work")

	fs.failCategoryDetails = errCategoryStep
	deliverSave(t, form)
	if form.editing.Version == note.Version {
		t.Fatal("the form still holds the pre-save version")
	}

	fs.failCategoryDetails = nil
	var stale bool
	for _, msg := range drainCmd(form.save()) {
		if saved, ok := msg.(noteSavedMsg); ok {
			_, stale = staleWrite(saved.err)
			if saved.err != nil && !stale {
				t.Fatalf("retry failed: %v", saved.err)
			}
		}
	}
	if stale {
		t.Fatal("the retry was refused as stale against the form's own earlier save")
	}
	if got := linkedCategoryNames(t, fs, note.ID, fs.notes[0].CreatedBy.String); len(got) != 1 || got[0] != "Work" {
		t.Errorf("categories after the retry = %v, want [Work]", got)
	}
}

// When the category prefill cannot be read, the field is empty and says
// nothing about the note. A save from that form must keep the note's
// categories; it used to replace them with the empty field.
func TestSavingAfterAFailedCategoryLoadKeepsTheNotesCategories(t *testing.T) {
	form, fs, note := formFixture(t)
	userGUID := fs.notes[0].CreatedBy.String
	if err := syncNoteCategories(fs, note.ID, "Work/backend, Personal", userGUID); err != nil {
		t.Fatalf("seeding categories: %v", err)
	}

	// The form opens, and its prefill fails.
	form.catsPrefill = catsPrefillLoading // what Init sets when it sends the load
	_, cmd := form.Update(noteCatsLoadedMsg{noteID: note.ID, err: errCategoryStep})
	var warned bool
	for _, msg := range drainCmd(cmd) {
		if st, ok := msg.(statusNote); ok && strings.Contains(st.text, "saving will keep them") {
			warned = true
		}
	}
	if !warned {
		t.Error("a failed category load was not reported")
	}
	if form.categories.Value() != "" {
		t.Fatalf("setup: the field is %q, want empty", form.categories.Value())
	}

	// The user edits only the body and saves; the store is healthy again.
	form.body.SetValue("only the body changed")
	deliverSave(t, form)

	want := []string{"Personal", "Work/backend"}
	if got := linkedCategoryNames(t, fs, note.ID, userGUID); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("categories after the save = %v, want %v (an empty field wiped them)", got, want)
	}

	// Typing in that state adds, and re-selects what it names.
	form2 := newFormScreen(form.sess, &fs.notes[0])
	form2.catsPrefill = catsPrefillLoading
	form2.Update(noteCatsLoadedMsg{noteID: note.ID, err: errCategoryStep})
	form2.categories.SetValue("Reading, Work/api")
	deliverSave(t, form2)

	want = []string{"Personal", "Reading", "Work/api"}
	if got := linkedCategoryNames(t, fs, note.ID, userGUID); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("categories after typing additions = %v, want %v", got, want)
	}
}

// Until the prefill has answered, the field cannot be read either way, so the
// save waits and says so rather than guessing.
func TestSavingWhileCategoriesAreStillLoadingWaits(t *testing.T) {
	form, fs, note := formFixture(t)
	userGUID := fs.notes[0].CreatedBy.String
	if err := syncNoteCategories(fs, note.ID, "Work", userGUID); err != nil {
		t.Fatal(err)
	}
	form.catsPrefill = catsPrefillLoading
	form.body.SetValue("saved too early")

	statuses := deliverSave(t, form)
	if joined := strings.Join(statuses, " | "); !strings.Contains(joined, "still loading") {
		t.Errorf("status = %q, want a note that the categories are still loading", joined)
	}
	if fs.notes[0].Body.String == "saved too early" {
		t.Error("the note was saved before its categories had loaded")
	}

	// Once the answer lands, the same save goes through and keeps the category.
	form.Update(noteCatsLoadedMsg{noteID: note.ID, cats: []models.NoteCategoryDetailOutput{{ID: 1, Name: "Work"}}})
	deliverSave(t, form)
	if fs.notes[0].Body.String != "saved too early" {
		t.Error("the save did not go through after the categories loaded")
	}
	if got := linkedCategoryNames(t, fs, note.ID, userGUID); len(got) != 1 || got[0] != "Work" {
		t.Errorf("categories = %v, want [Work]", got)
	}
}

// A category reply for a different note must not land in this form's field.
// Replies go to whichever screen is on top, and the next save would file this
// note under the other note's categories.
func TestACategoryReplyForAnotherNoteIsIgnored(t *testing.T) {
	form, _, note := formFixture(t)
	form.catsPrefill = catsPrefillLoading

	form.Update(noteCatsLoadedMsg{noteID: note.ID + 99, cats: []models.NoteCategoryDetailOutput{{ID: 7, Name: "SomeoneElses"}}})

	if got := form.categories.Value(); got != "" {
		t.Errorf("another note's categories reached the field: %q", got)
	}
	if form.catsPrefill != catsPrefillLoading {
		t.Error("another note's reply was taken as this form's prefill")
	}
}

// Text typed into the field before the prefill arrives is kept, after the
// stored set, and still counts as unsaved work.
func TestCategoriesTypedBeforeThePrefillArrivesAreKept(t *testing.T) {
	form, _, note := formFixture(t)
	form.catsPrefill = catsPrefillLoading
	form.categories.SetValue("Reading")

	form.Update(noteCatsLoadedMsg{noteID: note.ID, cats: []models.NoteCategoryDetailOutput{{ID: 1, Name: "Work"}}})

	if got := form.categories.Value(); got != "Work, Reading" {
		t.Errorf("field = %q, want the stored set followed by what was typed", got)
	}
	if !form.dirty() {
		t.Error("the typed category no longer counts as unsaved work")
	}
}

var _ tea.Msg = noteSavedMsg{} // keep the tea import honest if helpers change
