package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The mouse tests exist because mouse support fails in two silent ways, and
// neither produces a compile error:
//
//  1. The program never asks the terminal to report mouse events. That is the
//     v1→v2 regression this file's subject was written to fix — the handlers
//     were fine, they simply never ran. TestViewEnablesMouseReporting pins the
//     one line that makes the rest reachable.
//  2. The row arithmetic drifts from what the list actually draws. A click then
//     selects the note above or below the one under the pointer, which looks
//     like a flaky mouse rather than a bug. TestListRowAtMatchesRenderedRows
//     therefore checks the mapping against the RENDERED view, not against a
//     second copy of the same arithmetic.

// browseWithNotes returns a browse screen already holding n notes, laid out for
// a terminal wide enough for the preview pane.
func browseWithNotes(t *testing.T, n int) *browseScreen {
	t.Helper()

	st := newFakeStore()
	u := st.addUser("mouse", "pw")
	titles := []string{"Alpha note", "Beta note", "Gamma note", "Delta note", "Epsilon note"}
	for i := range n {
		title := titles[i%len(titles)]
		st.seedNote(u.GUID, title, "body of "+title)
	}

	sess := &session{store: st, user: u, width: 120, height: 40, cats: newCatsState()}
	s := newBrowseScreen(sess)
	s.layout()

	notes, err := st.ListNotes(u.GUID)
	if err != nil {
		t.Fatalf("seeding notes: %v", err)
	}
	updated, cmd := s.Update(notesLoadedMsg{notes: notes})
	if cmd != nil {
		cmd() // SetItems' filter command; nothing to route in the unfiltered case
	}
	return updated.(*browseScreen)
}

// TestViewEnablesMouseReporting is the regression guard for the actual bug: a
// tea.View with MouseMode left at its zero value emits no mouse DECSET at all,
// so the terminal never sends a click and every handler below is dead.
func TestViewEnablesMouseReporting(t *testing.T) {
	st := newFakeStore()
	m := newAppModel(st)
	m.sess.width, m.sess.height = 80, 24

	v := m.View()
	if v.MouseMode == tea.MouseModeNone {
		t.Fatal("View() leaves MouseMode at None; the terminal will never report clicks")
	}
	if v.MouseMode != mouseMode {
		t.Errorf("View() MouseMode = %v, want %v", v.MouseMode, mouseMode)
	}
}

// TestListRowAtMatchesRenderedRows walks every line of the rendered list and
// asserts that listRowAt agrees with the note whose title is drawn there.
//
// This is deliberately an oracle test rather than a table of expected offsets:
// the header height and the row stride are both derived from widget state, so a
// table would just restate the implementation and would keep passing if the
// widget's own layout changed underneath it.
func TestListRowAtMatchesRenderedRows(t *testing.T) {
	s := browseWithNotes(t, 5)
	lines := strings.Split(s.list.View(), "\n")

	items := s.list.VisibleItems()
	hits := 0

	for y, line := range lines {
		idx, ok := listRowAt(&s.list, y)
		if !ok {
			continue
		}
		if idx < 0 || idx >= len(items) {
			t.Fatalf("line %d: listRowAt returned out-of-range index %d", y, idx)
		}
		hits++

		// The title occupies the first line of an item, the description the
		// second, so accept either as proof that this row belongs to that note.
		note := items[idx].(noteItem).note
		if !strings.Contains(line, note.Title) && !strings.Contains(line, firstLine(note.Body.String)) {
			t.Errorf("line %d maps to note %d (%q) but renders %q",
				y, idx, note.Title, strings.TrimSpace(line))
		}
	}

	if hits == 0 {
		t.Fatal("listRowAt claimed no line at all in a list of five notes")
	}
}

// TestListRowAtRejectsNonItemRows pins the three ways a row can hold no item.
// The gap case is the one worth stating out loud: the delegate leaves a blank
// line between items, and treating it as part of the item above would move the
// selection one row off every third click.
func TestListRowAtRejectsNonItemRows(t *testing.T) {
	s := browseWithNotes(t, 2)
	head := listHeaderHeight(&s.list)
	d := newListDelegate()

	if _, ok := listRowAt(&s.list, head-1); ok {
		t.Error("a click on the header claimed an item")
	}
	if _, ok := listRowAt(&s.list, head+d.Height()); ok {
		t.Error("a click on the blank gap between items claimed an item")
	}
	// Two notes, so the row where a third would be is empty.
	if _, ok := listRowAt(&s.list, head+2*(d.Height()+d.Spacing())); ok {
		t.Error("a click past the last note claimed an item")
	}
}

// TestClickSelectsAndDoubleClickOpens covers the interaction contract: one
// click moves the selection (and with it the preview), two open the note.
func TestClickSelectsAndDoubleClickOpens(t *testing.T) {
	s := browseWithNotes(t, 5)
	head := listHeaderHeight(&s.list)
	stride := newListDelegate().Height() + newListDelegate().Spacing()

	// Third row, well away from the initially selected first one.
	y := head + 2*stride
	want, ok := listRowAt(&s.list, y)
	if !ok {
		t.Fatalf("no item at y=%d", y)
	}

	click := tea.MouseClickMsg{X: 1, Y: y, Button: tea.MouseLeft}

	updated, cmd := s.Update(click)
	s = updated.(*browseScreen)
	if got := s.list.Index(); got != want {
		t.Fatalf("after one click Index() = %d, want %d", got, want)
	}
	if cmd != nil {
		t.Error("a single click should not push a screen")
	}

	updated, cmd = s.Update(click)
	s = updated.(*browseScreen)
	if cmd == nil {
		t.Fatal("a double click did not open the note")
	}
	if _, isPush := cmd().(pushMsg); !isPush {
		t.Error("a double click produced something other than a push")
	}
}

// TestSlowSecondClickDoesNotOpen keeps the double-click a double-click: two
// clicks minutes apart are two selections, not an open.
func TestSlowSecondClickDoesNotOpen(t *testing.T) {
	s := browseWithNotes(t, 3)
	y := listHeaderHeight(&s.list)
	click := tea.MouseClickMsg{X: 1, Y: y, Button: tea.MouseLeft}

	updated, _ := s.Update(click)
	s = updated.(*browseScreen)

	// Age the first click past the window without sleeping through it.
	s.clicks.lastAt = time.Now().Add(-2 * doubleClickWindow)

	_, cmd := s.Update(click)
	if cmd != nil {
		t.Error("a click outside the double-click window opened the note")
	}
}

// TestClicksOnThePreviewPaneAreIgnored: the right-hand pane is not a list, and
// a click there must not jump the selection to whatever row shares its line.
func TestClicksOnThePreviewPaneAreIgnored(t *testing.T) {
	s := browseWithNotes(t, 5)
	if s.previewWidth == 0 {
		t.Fatal("test setup is too narrow for a preview pane")
	}
	before := s.list.Index()

	updated, cmd := s.Update(tea.MouseClickMsg{
		X: s.listWidth + 3, Y: listHeaderHeight(&s.list) + 2*3, Button: tea.MouseLeft,
	})
	s = updated.(*browseScreen)

	if cmd != nil {
		t.Error("a click on the preview pane produced a command")
	}
	if s.list.Index() != before {
		t.Errorf("a click on the preview pane moved the selection to %d", s.list.Index())
	}
}

// TestWheelScrollsTheList pins the translation the bubbles list does not do for
// itself — it has no mouse handling at all, so without this the wheel is inert.
func TestWheelScrollsTheList(t *testing.T) {
	s := browseWithNotes(t, 5)
	if s.list.Index() != 0 {
		t.Fatalf("expected the first note selected, got %d", s.list.Index())
	}

	updated, _ := s.Update(tea.MouseWheelMsg{X: 1, Y: 5, Button: tea.MouseWheelDown})
	s = updated.(*browseScreen)
	if got := s.list.Index(); got != 1 {
		t.Errorf("wheel down: Index() = %d, want 1", got)
	}

	updated, _ = s.Update(tea.MouseWheelMsg{X: 1, Y: 5, Button: tea.MouseWheelUp})
	s = updated.(*browseScreen)
	if got := s.list.Index(); got != 0 {
		t.Errorf("wheel up: Index() = %d, want 0", got)
	}
}

// TestWheelOverThePreviewDoesNotMoveTheSelection: scrolling while reading the
// preview must not swap the note out from under the pointer.
func TestWheelOverThePreviewDoesNotMoveTheSelection(t *testing.T) {
	s := browseWithNotes(t, 5)
	before := s.list.Index()

	updated, _ := s.Update(tea.MouseWheelMsg{
		X: s.listWidth + 3, Y: 5, Button: tea.MouseWheelDown,
	})
	s = updated.(*browseScreen)

	if s.list.Index() != before {
		t.Errorf("wheel over the preview moved the selection to %d", s.list.Index())
	}
}
