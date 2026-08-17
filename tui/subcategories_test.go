package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// Subcategory support spans four places that can each break on their own: the
// spec grammar in the form's field (syncNoteCategories), the definition of what
// a category offers (the subcategory screen), the filter built from it (browse),
// and the store methods underneath. These tests take them in that order.
//
// The properties worth stating up front, because each is a way the feature
// silently half-works:
//
//   - A note's SELECTION and a category's DEFINITION are different things. Filing
//     a note under Work/backend must register "backend" on Work, so the web UI
//     and the subcategory screen offer it — but removing it from one note must
//     not remove it from Work.
//   - Prefill and save speak the same notation. A form opened and saved with no
//     edits must leave the filing exactly as it was.
//   - Unchanged links are not rewritten. Every write here becomes a sync change
//     record, so a plain re-save must produce none.

// ---- helpers ---------------------------------------------------------------

// subcatSession builds a session on a fake store with one user, the shape every
// screen test here needs.
func subcatSession(t *testing.T) (*session, *fakeStore, *models.User) {
	t.Helper()
	st := newFakeStore()
	u := st.addUser("subcat_tester", "pw")
	sess := &session{store: st, user: u, width: 100, height: 30, cats: newCatsState()}
	return sess, st, u
}

// noteSpecs is what the form field would be prefilled with for a note — the
// same string the detail header shows.
func noteSpecs(t *testing.T, st Store, noteID int64, userGUID string) string {
	t.Helper()
	details, err := st.GetNoteCategoryDetails(noteID, userGUID)
	if err != nil {
		t.Fatalf("GetNoteCategoryDetails: %v", err)
	}
	return models.FormatCategorySpecCSV(noteCatSpecs(details))
}

// definedSubs returns a category's defined subcategory list by name.
func definedSubs(t *testing.T, st Store, name, userGUID string) []string {
	t.Helper()
	cat, err := st.GetCategoryByName(name, userGUID)
	if err != nil {
		t.Fatalf("GetCategoryByName(%q): %v", name, err)
	}
	if cat == nil {
		t.Fatalf("category %q does not exist", name)
	}
	return cat.ToOutput().Subcategories
}

// ---- the form's field ------------------------------------------------------

// TestSyncNoteCategoriesHandlesSubcategorySpecs is the whole form-side feature in
// one flow: type a spec, get a link with a selection, a definition that learned
// the name, and a prefill that reads back identically.
func TestSyncNoteCategoriesHandlesSubcategorySpecs(t *testing.T) {
	_, fs, user := subcatSession(t)
	note := fs.seedNote(user.GUID, "Filed deep", "body")

	if err := syncNoteCategories(fs, note.ID, "Work/backend/api, Personal", user.GUID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// The selection landed on the link, in the order typed.
	details, err := fs.GetNoteCategoryDetails(note.ID, user.GUID)
	if err != nil {
		t.Fatalf("GetNoteCategoryDetails: %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("expected two categories, got %d", len(details))
	}
	byName := map[string][]string{}
	for _, d := range details {
		byName[d.Name] = d.SelectedSubcategories
	}
	if got := byName["Work"]; !slices.Equal(got, []string{"backend", "api"}) {
		t.Errorf("Work selection = %v, want [backend api]", got)
	}
	if got := byName["Personal"]; len(got) != 0 {
		t.Errorf("Personal picked up subcategories it was never given: %v", got)
	}

	// The names were registered on the category definition, which is what makes
	// them appear as chips in the web UI and rows on the subcategory screen.
	if got := definedSubs(t, fs, "Work", user.GUID); !slices.Equal(got, []string{"backend", "api"}) {
		t.Errorf("Work definition = %v, want [backend api]", got)
	}

	// And the prefill reads back what was typed (categories sorted by name, which
	// is the order the store returns them in).
	if got := noteSpecs(t, fs, note.ID, user.GUID); got != "Personal, Work/backend/api" {
		t.Errorf("prefill = %q, want %q", got, "Personal, Work/backend/api")
	}
}

// TestSyncNoteCategoriesEditsTheSelectionInPlace covers the three edits a user
// can make to an existing assignment, and the one they cannot: shrinking a
// category's definition by editing one note.
func TestSyncNoteCategoriesEditsTheSelectionInPlace(t *testing.T) {
	_, fs, user := subcatSession(t)
	note := fs.seedNote(user.GUID, "Refiled", "body")

	if err := syncNoteCategories(fs, note.ID, "Work/backend", user.GUID); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// Another note in the same category, filed under a different subcategory —
	// the bystander whose filing must survive every edit below.
	other := fs.seedNote(user.GUID, "Someone else's note", "body")
	if err := syncNoteCategories(fs, other.ID, "Work/ops", user.GUID); err != nil {
		t.Fatalf("bystander sync: %v", err)
	}

	// 1. Swap the subcategory. The link stays; the selection changes.
	if err := syncNoteCategories(fs, note.ID, "Work/api", user.GUID); err != nil {
		t.Fatalf("swap sync: %v", err)
	}
	if got := noteSpecs(t, fs, note.ID, user.GUID); got != "Work/api" {
		t.Errorf("after the swap the note is filed as %q, want %q", got, "Work/api")
	}

	// 2. Drop back to the bare category. The category stays attached — that is
	// the difference between deselecting a subcategory and removing a category.
	if err := syncNoteCategories(fs, note.ID, "Work", user.GUID); err != nil {
		t.Fatalf("clearing sync: %v", err)
	}
	if got := noteSpecs(t, fs, note.ID, user.GUID); got != "Work" {
		t.Errorf("after clearing the subcategory the note is filed as %q, want %q", got, "Work")
	}

	// 3. The definition kept every name it ever learned, including the one only
	// the bystander uses.
	if got := definedSubs(t, fs, "Work", user.GUID); !slices.Equal(got, []string{"backend", "ops", "api"}) {
		t.Errorf("Work definition = %v, want [backend ops api] — editing one note must not shrink it", got)
	}
	if got := noteSpecs(t, fs, other.ID, user.GUID); got != "Work/ops" {
		t.Errorf("the bystander is now filed as %q, want %q", got, "Work/ops")
	}
}

// TestSyncNoteCategoriesSkipsUnchangedLinks is the no-churn property. Every write
// through the store becomes a sync change record, so re-saving a note nobody
// edited must touch nothing — including when the subcategories are listed in a
// different order than they are stored.
func TestSyncNoteCategoriesSkipsUnchangedLinks(t *testing.T) {
	_, fs, user := subcatSession(t)
	note := fs.seedNote(user.GUID, "Untouched", "body")

	if err := syncNoteCategories(fs, note.ID, "Work/backend/api", user.GUID); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	before := fs.linkWrites

	// Same filing, subcategories reordered — what a user retyping the field by
	// hand would produce.
	if err := syncNoteCategories(fs, note.ID, "Work/api/backend", user.GUID); err != nil {
		t.Fatalf("re-sync: %v", err)
	}

	if fs.linkWrites != before {
		t.Errorf("a re-save with the same filing performed %d link writes, want 0",
			fs.linkWrites-before)
	}
	if got := noteSpecs(t, fs, note.ID, user.GUID); got != "Work/backend/api" {
		t.Errorf("the stored order changed to %q; it should have been left alone", got)
	}
}

// ---- the subcategory screen ------------------------------------------------

// catWithSubs stores a category with a definition and returns it.
func catWithSubs(t *testing.T, fs *fakeStore, name string, subs []string, userGUID string) models.Category {
	t.Helper()
	cat, err := fs.CreateCategory(name, userGUID)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	updated, err := fs.SetCategorySubcategories(*cat, subs, userGUID)
	if err != nil {
		t.Fatalf("SetCategorySubcategories: %v", err)
	}
	return *updated
}

// drainInit applies a screen's Init command so its list holds items.
func drainInit(s screen) {
	if cmd := s.Init(); cmd != nil {
		cmd()
	}
}

// drainSequence flattens a command into the messages it produces, seeing through
// both tea.Batch and tea.Sequence.
//
// Sequence needs reflection, and it is worth explaining why rather than reaching
// for a real program as capture_test.go does: tea.Sequence returns a value of an
// unexported type whose underlying type is []tea.Cmd, so the ELEMENTS are an
// exported type and can be read back out — no unexported field is ever touched.
// That is enough to assert the one thing the pick path is about, which is
// ordering: two pops and then the filter message, in that order, because the
// browse screen has to be on top before the message reaches it.
func drainSequence(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()

	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drainSequence(c)...)
		}
		return out
	}

	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice &&
		v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		var out []tea.Msg
		for i := range v.Len() {
			inner, _ := v.Index(i).Interface().(tea.Cmd)
			out = append(out, drainSequence(inner)...)
		}
		return out
	}

	return []tea.Msg{msg}
}

// TestSubcategoryScreenListsTheDefinition: the rows are the category's defined
// subcategories, which is the palette both this screen and the form offer.
func TestSubcategoryScreenListsTheDefinition(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend", "ops"}, user.GUID)

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)

	if got := len(s.list.Items()); got != 2 {
		t.Fatalf("the screen shows %d rows for a two-subcategory category", got)
	}
	view := stripANSI(s.View())
	for _, want := range []string{"backend", "ops", "Work"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view %q is missing %q", view, want)
		}
	}
}

// TestSubcategoryScreenEmptyStateTeaches: a category with nothing defined is the
// normal first visit, so the screen has to say what to do rather than render an
// empty list.
func TestSubcategoryScreenEmptyStateTeaches(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", nil, user.GUID)

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)

	view := stripANSI(s.View())
	if !strings.Contains(view, "No subcategories yet") {
		t.Errorf("the empty view %q does not say the list is empty", view)
	}
	if !strings.Contains(view, "Work/name") {
		t.Errorf("the empty view %q does not mention the field notation, the other way to create one", view)
	}
}

// TestSubcategoryPickAppliesTheFilterToBrowse is the pick path end to end: enter
// on a row has to pop TWO screens (this one and the category list) and then hand
// browse a filter carrying the subcategory.
func TestSubcategoryPickAppliesTheFilterToBrowse(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend", "ops"}, user.GUID)

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)

	_, cmd := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a subcategory produced no command")
	}

	msgs := drainSequence(cmd)
	pops := 0
	var picked *categoryPickedMsg
	for _, msg := range msgs {
		switch m := msg.(type) {
		case popMsg:
			pops++
		case categoryPickedMsg:
			pick := m
			picked = &pick
		}
	}
	if pops != 2 {
		t.Errorf("enter popped %d screens, want 2 (this screen and the category list)", pops)
	}
	if picked == nil {
		t.Fatal("enter never delivered a categoryPickedMsg")
	}
	if picked.cat == nil || picked.cat.Name != "Work" {
		t.Fatalf("the pick names the wrong category: %+v", picked.cat)
	}
	if !slices.Equal(picked.subs, []string{"backend"}) {
		t.Errorf("the pick carries subs %v, want [backend] (the highlighted row)", picked.subs)
	}
}

// TestSubcategoryToggleBuildsAMultiFilter: space accumulates rows, and enter
// then filters by all of them. The cursor must stay put while toggling, since
// rebuilding the list resets it.
func TestSubcategoryToggleBuildsAMultiFilter(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend", "ops"}, user.GUID)

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)

	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	down := tea.KeyPressMsg{Code: tea.KeyDown}

	// Toggle the first row, move down, toggle the second.
	updated, cmd := s.Update(space)
	s = updated.(*subcategoriesScreen)
	if cmd != nil {
		cmd()
	}
	if s.list.Index() != 0 {
		t.Fatalf("toggling moved the cursor to %d; multi-select is unusable if each toggle jumps", s.list.Index())
	}

	updated, _ = s.Update(down)
	s = updated.(*subcategoriesScreen)
	updated, cmd = s.Update(space)
	s = updated.(*subcategoriesScreen)
	if cmd != nil {
		cmd()
	}

	if !slices.Equal(s.selected, []string{"backend", "ops"}) {
		t.Fatalf("selected = %v, want [backend ops]", s.selected)
	}
	if view := stripANSI(s.View()); !strings.Contains(view, "Work/backend/ops") {
		t.Errorf("the view %q does not show the filter being built", view)
	}

	_, cmd = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter with a selection produced no command")
	}
	for _, msg := range drainSequence(cmd) {
		if pick, ok := msg.(categoryPickedMsg); ok {
			if !slices.Equal(pick.subs, []string{"backend", "ops"}) {
				t.Errorf("the pick carries %v, want both toggled subcategories", pick.subs)
			}
			return
		}
	}
	t.Fatal("enter never delivered a categoryPickedMsg")
}

// TestSubcategoryScreenAddsToTheDefinition covers the n path, including the
// no-op: retyping an existing name is a status line, not a duplicate row.
func TestSubcategoryScreenAddsToTheDefinition(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend"}, user.GUID)

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)

	// The prompt screen's submit calls this closure; drive it directly rather
	// than typing into the modal, which has its own tests.
	msg := s.addSubcategory("ops")()
	updated, ok := msg.(categorySubsUpdatedMsg)
	if !ok {
		t.Fatalf("adding a subcategory produced %T, want categorySubsUpdatedMsg", msg)
	}
	if updated.err != nil {
		t.Fatalf("add: %v", updated.err)
	}

	next, cmd := s.Update(updated)
	s = next.(*subcategoriesScreen)
	if cmd != nil {
		cmd()
	}
	if got := s.subcategories(); !slices.Equal(got, []string{"backend", "ops"}) {
		t.Errorf("after adding, the screen shows %v, want [backend ops]", got)
	}
	if !s.dirty {
		t.Error("the screen did not record that the definition changed; the category list behind it will show stale rows")
	}
	if got := definedSubs(t, fs, "Work", user.GUID); !slices.Equal(got, []string{"backend", "ops"}) {
		t.Errorf("the store holds %v, want [backend ops]", got)
	}

	// A duplicate is refused before any store call.
	dupe := s.addSubcategory("backend")()
	if note, ok := dupe.(statusNote); !ok {
		t.Errorf("re-adding an existing subcategory produced %T, want a status note", dupe)
	} else if note.isErr {
		t.Errorf("re-adding an existing subcategory reported an error: %q", note.text)
	}
}

// TestSubcategoryScreenRemovesFromTheDefinitionOnly: d takes a name out of the
// palette. Notes filed under it keep their selection — the confirm prompt says
// so, and this pins that it is true.
func TestSubcategoryScreenRemovesFromTheDefinitionOnly(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend", "ops"}, user.GUID)

	note := fs.seedNote(user.GUID, "Filed under ops", "body")
	if err := syncNoteCategories(fs, note.ID, "Work/ops", user.GUID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)
	// Select "ops" so the toggle below has something to strand.
	s.selected = []string{"ops"}

	msg := s.removeSubcategory("ops")()
	updated, ok := msg.(categorySubsUpdatedMsg)
	if !ok {
		t.Fatalf("removing a subcategory produced %T, want categorySubsUpdatedMsg", msg)
	}
	next, cmd := s.Update(updated)
	s = next.(*subcategoriesScreen)
	if cmd != nil {
		cmd()
	}

	if got := s.subcategories(); !slices.Equal(got, []string{"backend"}) {
		t.Errorf("after removal the definition is %v, want [backend]", got)
	}
	if len(s.selected) != 0 {
		t.Errorf("the removed name is still in the pending filter (%v); enter would match no note", s.selected)
	}
	if got := noteSpecs(t, fs, note.ID, user.GUID); got != "Work/ops" {
		t.Errorf("the note's filing changed to %q; removing a definition entry must not refile notes", got)
	}
}

// TestSubcategoryBackRefreshesOnlyAfterAnEdit: a look-and-leave costs no reload,
// an edit costs one — otherwise the category row behind shows a stale list.
func TestSubcategoryBackRefreshesOnlyAfterAnEdit(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend"}, user.GUID)

	s := newSubcategoriesScreen(sess, cat)
	drainInit(s)

	esc := tea.KeyPressMsg{Code: tea.KeyEscape}

	_, cmd := s.Update(esc)
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if pop, ok := cmd().(popMsg); !ok || pop.refresh {
		t.Errorf("esc after no edits popped with refresh=%v, want a plain pop", pop.refresh)
	}

	s.dirty = true
	_, cmd = s.Update(esc)
	if pop, ok := cmd().(popMsg); !ok || !pop.refresh {
		t.Error("esc after an edit did not ask the category list to reload")
	}
}

// ---- the category screen's door -------------------------------------------

// TestCategoryScreenOpensSubcategories pins the "s" door and the row text that
// advertises it.
func TestCategoryScreenOpensSubcategories(t *testing.T) {
	sess, fs, user := subcatSession(t)
	cat := catWithSubs(t, fs, "Work", []string{"backend", "ops"}, user.GUID)

	s := newCategoriesScreen(sess)
	if cmd := s.Init(); cmd != nil {
		if msg, ok := cmd().(categoriesLoadedMsg); ok {
			updated, setCmd := s.Update(msg)
			s = updated.(*categoriesScreen)
			if setCmd != nil {
				setCmd()
			}
		}
	}

	if view := stripANSI(s.View()); !strings.Contains(view, "backend, ops") {
		t.Errorf("the category row %q does not show its subcategories", view)
	}

	_, cmd := s.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if cmd == nil {
		t.Fatal("\"s\" on a category produced no command")
	}
	push, ok := cmd().(pushMsg)
	if !ok {
		t.Fatalf("\"s\" produced %T, want a push", cmd())
	}
	sub, ok := push.s.(*subcategoriesScreen)
	if !ok {
		t.Fatalf("\"s\" pushed %T, want the subcategory screen", push.s)
	}
	if sub.cat.ID != cat.ID {
		t.Errorf("the subcategory screen opened on category %d, want %d", sub.cat.ID, cat.ID)
	}
}

// ---- the browse filter -----------------------------------------------------

// TestBrowseAppliesTheSubcategoryFilter checks the two things a pick has to do:
// narrow the list through the AND-semantics store call, and say so in the title.
func TestBrowseAppliesTheSubcategoryFilter(t *testing.T) {
	sess, fs, user := subcatSession(t)
	catWithSubs(t, fs, "Work", []string{"backend", "ops"}, user.GUID)

	backend := fs.seedNote(user.GUID, "Backend note", "body")
	ops := fs.seedNote(user.GUID, "Ops note", "body")
	both := fs.seedNote(user.GUID, "Both note", "body")
	for _, tc := range []struct {
		id    int64
		specs string
	}{
		{backend.ID, "Work/backend"},
		{ops.ID, "Work/ops"},
		{both.ID, "Work/backend/ops"},
	} {
		if err := syncNoteCategories(fs, tc.id, tc.specs, user.GUID); err != nil {
			t.Fatalf("sync %q: %v", tc.specs, err)
		}
	}

	cat, _ := fs.GetCategoryByName("Work", user.GUID)
	s := newBrowseScreen(sess)
	s.layout()

	// One subcategory: the two notes carrying it.
	titles := browseTitlesAfterPick(t, s, categoryPickedMsg{cat: cat, subs: []string{"backend"}})
	if !slices.Equal(titles, []string{"Backend note", "Both note"}) {
		t.Errorf("filtering by Work/backend showed %v, want [Backend note, Both note]", titles)
	}
	if got := s.title(); !strings.Contains(got, "Work/backend") {
		t.Errorf("the list title is %q; it does not name the active subcategory", got)
	}

	// Two subcategories: only the note carrying both (AND, like the web UI's chips).
	titles = browseTitlesAfterPick(t, s, categoryPickedMsg{cat: cat, subs: []string{"backend", "ops"}})
	if !slices.Equal(titles, []string{"Both note"}) {
		t.Errorf("filtering by Work/backend/ops showed %v, want [Both note] only", titles)
	}
}

// TestBrowseEscPeelsSubcategoryBeforeCategory: esc has to walk back out the way
// the user walked in, or "Work/backend" is one keystroke from every note.
func TestBrowseEscPeelsSubcategoryBeforeCategory(t *testing.T) {
	sess, fs, user := subcatSession(t)
	catWithSubs(t, fs, "Work", []string{"backend"}, user.GUID)

	inWork := fs.seedNote(user.GUID, "Backend note", "body")
	if err := syncNoteCategories(fs, inWork.ID, "Work/backend", user.GUID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	fs.seedNote(user.GUID, "Loose note", "body") // in no category at all

	cat, _ := fs.GetCategoryByName("Work", user.GUID)
	s := newBrowseScreen(sess)
	s.layout()
	browseTitlesAfterPick(t, s, categoryPickedMsg{cat: cat, subs: []string{"backend"}})

	esc := tea.KeyPressMsg{Code: tea.KeyEscape}

	// First esc: back to the whole category.
	updated, cmd := s.Update(esc)
	s = updated.(*browseScreen)
	if len(s.subFilter) != 0 {
		t.Fatalf("the first esc left the subcategory filter %v in place", s.subFilter)
	}
	if s.catFilter == nil {
		t.Fatal("the first esc dropped the category filter too; that is a bigger step than esc promises")
	}
	if titles := browseTitles(t, s, cmd); !slices.Equal(titles, []string{"Backend note"}) {
		t.Errorf("after one esc the list shows %v, want the whole category", titles)
	}

	// Second esc: back to everything.
	updated, cmd = s.Update(esc)
	s = updated.(*browseScreen)
	if s.catFilter != nil {
		t.Fatal("the second esc did not clear the category filter")
	}
	if titles := browseTitles(t, s, cmd); len(titles) != 2 {
		t.Errorf("after two escs the list shows %v, want every note", titles)
	}
}

// browseTitlesAfterPick applies a pick to the browse screen and returns the
// titles the list ends up holding.
func browseTitlesAfterPick(t *testing.T, s *browseScreen, pick categoryPickedMsg) []string {
	t.Helper()
	updated, cmd := s.Update(pick)
	*s = *(updated.(*browseScreen))
	return browseTitles(t, s, cmd)
}

// browseTitles runs the load command a filter change returned, feeds the result
// back to the screen, and reads the rows out.
func browseTitles(t *testing.T, s *browseScreen, cmd tea.Cmd) []string {
	t.Helper()
	if cmd == nil {
		t.Fatal("a filter change returned no load command")
	}
	loaded, ok := cmd().(notesLoadedMsg)
	if !ok {
		t.Fatalf("a filter change resolved to %T, want notesLoadedMsg", cmd())
	}
	if loaded.err != nil {
		t.Fatalf("loading filtered notes: %v", loaded.err)
	}

	updated, setCmd := s.Update(loaded)
	*s = *(updated.(*browseScreen))
	if setCmd != nil {
		setCmd()
	}

	titles := make([]string, 0, len(s.list.Items()))
	for _, item := range s.list.Items() {
		titles = append(titles, item.(noteItem).note.Title)
	}
	slices.Sort(titles)
	return titles
}
