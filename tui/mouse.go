package tui

import (
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Mouse support for the TUI.
//
// WHY THE MOUSE WAS DEAD. Bubble Tea v1 turned mouse reporting on with a
// program option (tea.WithMouseCellMotion()). v2 moved it — exactly as it moved
// the alternate screen — onto the tea.View the model returns. A v1→v2 port that
// fixes AltScreen and stops there loses the mouse completely and silently: the
// program never emits the DECSET that asks the terminal to report clicks, so no
// mouse byte is ever sent, no mouse message is ever parsed, and any handler in
// the app is unreachable code. Confirmed on the wire — a pty capture of the
// launch showed ?1049h (alt screen), ?2004h (bracketed paste) and the kitty
// keyboard push, and no 1000/1002/1003/1006 at all. mouseMode below is the
// repair, and it belongs to View() in tui.go.
//
// WHAT ENABLING IT COSTS. A terminal reporting mouse events to the application
// stops using the mouse for its own drag-to-select. Most terminals keep a
// bypass (hold shift, or option on macOS, while dragging); a browser-canvas
// host like cats decides per pane from the app's own mode, so inside a cats
// pane the drag belongs to GoNotes while it runs. That is the standard trade a
// TUI makes for a clickable list, and reverting it is one line here.
//
// WHAT IS WIRED, AND WHAT IS NOT. The two list screens (notes, categories) take
// clicks and the wheel; the detail screen gets wheel scrolling for free, since
// its viewport has handled tea.MouseWheelMsg all along and was only ever
// starved of events. The modals — confirm, the agent picker, the form — are
// deliberately left on the keyboard: they are a handful of rows inside a
// lipgloss.Place'd box, so hit-testing them means reproducing the centering
// math, and a two-choice dialog does not earn that.

// mouseMode is the tracking level the whole program runs at.
//
// CellMotion, not AllMotion: it reports button presses, releases, and motion
// only while a button is held. AllMotion adds a report for every cell the
// pointer crosses with no button down — a steady stream of messages through the
// event loop that nothing here reads.
const mouseMode = tea.MouseModeCellMotion

// doubleClickWindow is how long after a click a second click on the same row
// counts as a double-click rather than a fresh selection.
//
// The terminal gives us no click count — SGR mouse reports carry a button and a
// cell, nothing more — so a double-click is something we time ourselves. 500ms
// is the common desktop default and is comfortably longer than the round trip
// through the event loop.
const doubleClickWindow = 500 * time.Millisecond

// clickTracker remembers the last click so the next one can be recognized as a
// double-click. Embedded by the screens that want click-to-activate.
type clickTracker struct {
	lastIdx int
	lastAt  time.Time
	seen    bool // no click yet — distinguishes "never clicked" from row 0
}

// double records a click on idx and reports whether it completes a double-click.
//
// A completed double-click RESETS the tracker rather than leaving its timestamp
// in place. Without that, a third click inside the window would read as a
// second double-click and fire the action again, so a fast triple-click on a
// note would open it twice — and the second push would land on a screen that is
// no longer the list.
func (c *clickTracker) double(idx int) bool {
	now := time.Now()
	if c.seen && c.lastIdx == idx && now.Sub(c.lastAt) <= doubleClickWindow {
		*c = clickTracker{}
		return true
	}
	c.lastIdx, c.lastAt, c.seen = idx, now, true
	return false
}

// listHeaderHeight is how many lines a list.Model draws above its first item.
//
// Derived from the widget's public state rather than hardcoded, because the two
// blocks it counts are both optional and both styled from the palette:
//
//	┌───────────────────────┐ ─┐
//	│ GoNotes               │  │ title bar: 1 line + its style's vertical frame
//	│                       │  │ (the default TitleBar carries a bottom padding)
//	├───────────────────────┤ ─┤
//	│ 31 notes              │  │ status bar: same shape, and it is what
//	│                       │  │ SetShowStatusBar(false) would remove
//	├───────────────────────┤ ─┘
//	│ ▎ First note          │ ←─ item rows start here
//
// The title bar is drawn when there is a title OR when filtering is available,
// since the filter prompt takes the title's place while it is up — which is
// list.View's own condition, mirrored here so the geometry cannot drift from it
// when the filter opens.
func listHeaderHeight(l *list.Model) int {
	h := 0
	if l.ShowTitle() || (l.ShowFilter() && l.FilteringEnabled()) {
		h += 1 + l.Styles.TitleBar.GetVerticalFrameSize()
	}
	if l.ShowStatusBar() {
		h += 1 + l.Styles.StatusBar.GetVerticalFrameSize()
	}
	return h
}

// listRowAt maps a terminal row to the index of the list item drawn there,
// reporting false when the row holds no item.
//
// The index it returns is into VisibleItems() — the filtered set — which is the
// index list.Select and list.Index speak in, so a click during an active filter
// selects the note the user actually clicked rather than its position in the
// unfiltered list.
//
// The item region repeats on a fixed stride (populatedView writes each item
// followed by Spacing() blank lines), so within one stride the first Height()
// lines are the item and the remainder is the gap between it and the next:
//
//	y ── header ──►  0  ▎ Title         ─┐
//	                 1  ▎ description    │ delegate.Height() = 2   ─┐ stride
//	                 2                  ─┘ delegate.Spacing() = 1  ─┘
//	                 3  ▎ Title            (next item)
//
// A click on a gap line hits nothing, which is why the stride is tested and not
// just divided: the alternative rounds every gap click down onto the item above
// it, so the row a user aims at and the row that selects are one apart every
// third line.
//
// Height and Spacing come from a freshly built delegate rather than from
// constants, since list.Model exposes no getter for the one it holds. Both list
// screens construct theirs through newListDelegate, so this is the same
// delegate — and if that ever grows a third line, this follows it.
func listRowAt(l *list.Model, y int) (int, bool) {
	head := listHeaderHeight(l)
	if y < head {
		return 0, false // the title bar or the status bar, not an item
	}

	d := newListDelegate()
	itemHeight, stride := d.Height(), d.Height()+d.Spacing()
	if itemHeight < 1 || stride < 1 {
		return 0, false
	}

	offset := y - head
	if offset%stride >= itemHeight {
		return 0, false // the blank gap between two items
	}

	row := offset / stride
	if row >= l.Paginator.PerPage {
		return 0, false // below the last row of the page (pagination dots, help)
	}

	idx := l.Paginator.Page*l.Paginator.PerPage + row
	if idx >= len(l.VisibleItems()) {
		return 0, false // a filled-out blank line on a short final page
	}
	return idx, true
}

// wheelList scrolls a list by one row per wheel notch, reporting whether the
// event was a wheel event it consumed.
//
// The bubbles list has no mouse handling at all (in v2.1.1 the viewport is the
// only widget that does), so the wheel has to be translated into the cursor
// movement the keyboard would have made. CursorUp/CursorDown are called
// directly rather than synthesizing a key: while the filter prompt is up the
// list disables its own ↑/↓ bindings, and a wheel roll should still scroll the
// matches the user is looking at.
func wheelList(l *list.Model, msg tea.MouseWheelMsg) bool {
	switch msg.Button {
	case tea.MouseWheelUp:
		l.CursorUp()
	case tea.MouseWheelDown:
		l.CursorDown()
	default:
		return false // horizontal wheel: nothing to scroll sideways
	}
	return true
}
