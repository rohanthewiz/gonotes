package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gonotes/cats"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// Tests for capture-to-note. The feature has four seams and each one has a
// different way of being wrong:
//
//	the cache     offers the wrong panes (our own, or a plain shell)
//	the door      dials a socket from a keystroke, or lies at Tier 0
//	the wire      captures the wrong scope, or asks for ANSI it cannot store
//	the note      arrives full of terminal padding
//
// The last test drives all four through a real program.

// ---- helpers ---------------------------------------------------------------

// drainCmd runs a command and flattens tea.Batch into the messages it produces.
//
// Batch is not transparent: it returns a BatchMsg carrying the sub-commands for
// the runtime to run, so a test that just calls cmd() gets the envelope rather
// than the result. Sequence deliberately has no equivalent here — its message
// type is unexported, which is why the enter-to-capture path is exercised
// through a real program instead.
func drainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drainCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// tier1State builds a catsState that believes it is at Tier 1, without a probe.
// socket may point at a real control server or at nothing, depending on whether
// the test intends any call to succeed.
func tier1State(socket string, panes []cats.PaneInfo) *catsState {
	cs := newCatsState()
	cs.caps = cats.Caps{InCats: true, Control: true, PaneHandle: "w1:p7"}
	cs.client = cats.NewClient(socket)
	cs.self, cs.selfOK = 7, true
	cs.panes = panes
	return cs
}

// errPoll stands in for a pane.list that failed.
var errPoll = errors.New("pane.list failed")

// ---- the pane cache --------------------------------------------------------

// The picker's list is a filter and a sort over the raw pane list, and both
// halves matter. Plain shells are not capture targets, and OUR OWN pane is the
// trap: GoNotes reports itself to cats as the agent "gonotes", so it shows up in
// pane.list looking exactly like a target.
func TestAgentPanesExcludesSelfAndPlainShells(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 3, Handle: "w1:p3"},                                              // a plain shell
		{Pane: 7, Handle: "w1:p7", Agent: "gonotes"},                            // us
		{Pane: 9, Handle: "w1:p9", Agent: "claude", AgentState: cats.StateIdle},
		{Pane: 4, Handle: "w1:p4", Agent: "codex", AgentState: cats.StateBlocked},
		{Pane: 5, Handle: "w1:p5", Agent: "ced", AgentState: cats.StateWorking},
	})

	got := cs.agentPanes()
	var names []string
	for _, p := range got {
		names = append(names, p.Agent)
	}
	// blocked first (it is the one waiting to be read), then working, then idle.
	want := []string{"codex", "ced", "claude"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("picker rows = %v, want %v", names, want)
	}
}

// The self-exclusion has a fallback path: a host that never resolved our pane
// id leaves selfOK false, and the public handle is all we have to go on.
func TestAgentPanesExcludesSelfByHandle(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 7, Handle: "w1:p7", Agent: "gonotes"},
		{Pane: 9, Handle: "w1:p9", Agent: "claude"},
	})
	cs.self, cs.selfOK = 0, false // the id was never resolved

	got := cs.agentPanes()
	if len(got) != 1 || got[0].Agent != "claude" {
		t.Errorf("without a resolved id the handle must still exclude us, got %+v", got)
	}
}

// Equal-ranked rows keep pane order, so a picker the user has learned does not
// reshuffle between openings.
func TestAgentPanesIsStableAtEqualRank(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 12, Handle: "w1:pc", Agent: "b", AgentState: cats.StateIdle},
		{Pane: 4, Handle: "w1:p4", Agent: "a", AgentState: cats.StateIdle},
		{Pane: 8, Handle: "w1:p8", Agent: "c", AgentState: cats.StateIdle},
	})
	var ids []uint32
	for _, p := range cs.agentPanes() {
		ids = append(ids, p.Pane)
	}
	if len(ids) != 3 || ids[0] != 4 || ids[1] != 8 || ids[2] != 12 {
		t.Errorf("equal-ranked rows should order by pane id, got %v", ids)
	}
}

// The rate limit is what keeps a burst of pane events — a tab opening four
// panes at once — from costing four round trips.
func TestPanePollIsRateLimited(t *testing.T) {
	cs := tier1State("/nonexistent.sock", nil)

	if cs.pollPanes(false) == nil {
		t.Fatal("a cache that has never been filled must refresh")
	}
	if cs.pollPanes(false) != nil {
		t.Error("a second refresh inside the window must be skipped")
	}
	if cs.pollPanes(true) == nil {
		t.Error("force must override the window — it is what primes the cache at Tier-1-up")
	}

	// Past the window, an ordinary refresh is allowed again.
	cs.panesAt = time.Now().Add(-2 * panePollMin)
	if cs.pollPanes(false) == nil {
		t.Error("the window should have expired")
	}
}

// Below Tier 1 there is no socket to poll, and pollPanes has to answer that
// without a nil-client dial.
func TestPollPanesIsNilBelowTier1(t *testing.T) {
	cs := newCatsState()
	if cs.pollPanes(true) != nil {
		t.Error("Tier 0 must not produce a poll command")
	}
	var nilState *catsState
	if nilState.agentPanes() != nil {
		t.Error("a nil state has no panes")
	}
}

// A failed pane.list leaves the cache alone. A stale list is a picker with one
// wrong row; an emptied one is a picker with no rows at all.
func TestFailedPollKeepsTheCache(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 9, Handle: "w1:p9", Agent: "claude"},
	})
	cs.setPanes(catsPanesMsg{err: errPoll})
	if len(cs.panes) != 1 {
		t.Errorf("a failed refresh emptied the cache: %+v", cs.panes)
	}
	cs.setPanes(catsPanesMsg{panes: []cats.PaneInfo{{Pane: 4, Agent: "codex"}}})
	if len(cs.panes) != 1 || cs.panes[0].Agent != "codex" {
		t.Errorf("a successful refresh should replace the cache, got %+v", cs.panes)
	}
}

// The pane events are what notice that the cached layout went stale. focus
// changes deliberately do not: the picker does not render which pane is
// focused, so refreshing on one would be a round trip that changes nothing.
func TestPaneEventsRefreshTheCache(t *testing.T) {
	cs := tier1State("/nonexistent.sock", nil)

	if cs.frame(cats.Event{Name: cats.EventPaneAgent}) == nil {
		t.Error("a pane's agent state changing should refresh the cache")
	}
	// Immediately after, the rate limit applies — which is the whole point of
	// handling the three events identically.
	if cs.frame(cats.Event{Name: cats.EventPaneAdded}) != nil {
		t.Error("a second pane event inside the window must not re-dial")
	}

	cs.panesAt = time.Now().Add(-2 * panePollMin)
	if cs.frame(cats.Event{Name: cats.EventPaneRemoved}) == nil {
		t.Error("pane_removed should refresh once the window has passed")
	}
	cs.panesAt = time.Now().Add(-2 * panePollMin)
	if cs.frame(cats.Event{Name: cats.EventFocusChanged}) != nil {
		t.Error("focus_changed changes nothing the picker renders")
	}
}

// ---- the door --------------------------------------------------------------

// browseAt builds a browse screen over a store and a cats state.
func browseAt(t *testing.T, cs *catsState) *browseScreen {
	t.Helper()
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	sess := &session{store: fs, cats: cs, user: user, width: 100, height: 40}
	return newBrowseScreen(sess)
}

// ctrlG is the door's keystroke.
var ctrlG = tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}

// Below Tier 1 the door answers with one line and changes nothing else. This is
// the Tier-0 contract for the whole feature: pressing the key in a plain
// terminal must not error, hang, or dial anything.
func TestCaptureDoorDegradesAtTier0(t *testing.T) {
	s := browseAt(t, newCatsState())

	_, cmd := s.Update(ctrlG)
	msgs := drainCmd(cmd)
	if len(msgs) != 1 {
		t.Fatalf("expected exactly one message, got %+v", msgs)
	}
	note, ok := msgs[0].(statusNote)
	if !ok {
		t.Fatalf("Tier 0 produced a %T, want a statusNote", msgs[0])
	}
	if !strings.Contains(note.text, "standalone") {
		t.Errorf("the degradation line does not say what happened: %q", note.text)
	}
	if note.isErr {
		t.Error("running outside cats is not an error")
	}
}

// A cats session where nothing else is running an agent gets a status line
// rather than an empty modal — a dialog listing nothing is one the user has to
// dismiss just to learn there was nothing in it.
func TestCaptureDoorNeedsAnAgentPane(t *testing.T) {
	// Only a plain shell and ourselves: Tier 1, but nothing to capture from.
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 3, Handle: "w1:p3"},
		{Pane: 7, Handle: "w1:p7", Agent: "gonotes"},
	})
	s := browseAt(t, cs)

	msgs := drainCmd(func() tea.Cmd { _, c := s.Update(ctrlG); return c }())
	var note *statusNote
	for _, m := range msgs {
		if n, ok := m.(statusNote); ok {
			note = &n
		}
		if _, ok := m.(pushMsg); ok {
			t.Fatal("no agent panes must not open a picker")
		}
	}
	if note == nil || !strings.Contains(note.text, "No agent panes") {
		t.Errorf("expected a 'no agent panes' status, got %+v", msgs)
	}
}

// The door opens the picker from the CACHE and refreshes for the next opening.
// Both halves are asserted because the ordering is the point: a keystroke that
// waited on pane.list would stall the UI for a round trip every time.
func TestCaptureDoorOpensThePickerFromTheCache(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 7, Handle: "w1:p7", Agent: "gonotes"},
		{Pane: 9, Handle: "w1:p9", Agent: "claude", AgentState: cats.StateIdle},
	})
	s := browseAt(t, cs)

	_, cmd := s.Update(ctrlG)
	var picker *agentPickerScreen
	for _, m := range drainCmd(cmd) {
		if p, ok := m.(pushMsg); ok {
			picker, _ = p.s.(*agentPickerScreen)
		}
	}
	if picker == nil {
		t.Fatal("ctrl+g at Tier 1 should push the agent picker")
	}
	if len(picker.panes) != 1 || picker.panes[0].Agent != "claude" {
		t.Errorf("the picker was not built from the cache: %+v", picker.panes)
	}
	if cs.panesAt.IsZero() {
		t.Error("opening the picker should also schedule a refresh for the next one")
	}
}

// ---- the picker ------------------------------------------------------------

func TestPickerCursorWraps(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 9, Agent: "claude", AgentState: cats.StateIdle},
		{Pane: 4, Agent: "codex", AgentState: cats.StateIdle},
	})
	s := newAgentPickerScreen(&session{cats: cs, width: 100, height: 40})
	if len(s.panes) != 2 {
		t.Fatalf("setup: expected 2 rows, got %d", len(s.panes))
	}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	down := tea.KeyPressMsg{Code: tea.KeyDown}

	s.Update(down)
	if s.cursor != 1 {
		t.Errorf("down moved to %d, want 1", s.cursor)
	}
	s.Update(down)
	if s.cursor != 0 {
		t.Errorf("down past the end should wrap to 0, got %d", s.cursor)
	}
	s.Update(up)
	if s.cursor != 1 {
		t.Errorf("up past the start should wrap to the last row, got %d", s.cursor)
	}
}

// The picker renders what the user needs to tell two panes apart: who is in it
// and where it is.
func TestPickerRendersAgentStateAndPlace(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 9, Handle: "w1:p9", Agent: "claude", AgentState: cats.StateBlocked},
	})
	s := newAgentPickerScreen(&session{cats: cs, width: 100, height: 40})

	out := stripANSI(s.View())
	for _, want := range []string{"Capture from an agent pane", "claude", "blocked", "w1:p9", "capture"} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker does not show %q:\n%s", want, out)
		}
	}
}

// A pane with no public handle still has to be identifiable.
func TestPickerNamesAPaneWithoutAHandle(t *testing.T) {
	cs := tier1State("/nonexistent.sock", []cats.PaneInfo{
		{Pane: 26, Agent: "claude"},
	})
	s := newAgentPickerScreen(&session{cats: cs, width: 100, height: 40})
	if out := stripANSI(s.View()); !strings.Contains(out, "pane 26") {
		t.Errorf("a handle-less pane should be named by its id:\n%s", out)
	}
}

// ---- the wire --------------------------------------------------------------

// What goes on the wire is the part no unit of GoNotes can check for itself:
// the scope decides whether the capture reaches back through scrollback at all,
// and ansi:true would fill a markdown note with escape sequences.
func TestCaptureRequestsRecentScopeWithoutAnsi(t *testing.T) {
	srv := newControlServer(t)
	srv.setCapture("the agent's answer")

	msg, ok := captureCmd(cats.NewClient(srv.path),
		cats.PaneInfo{Pane: 9, Agent: "claude"})().(captureDoneMsg)
	if !ok {
		t.Fatal("captureCmd did not produce a captureDoneMsg")
	}
	if msg.err != nil {
		t.Fatalf("capture failed: %v", msg.err)
	}
	if msg.text != "the agent's answer" || msg.agent != "claude" {
		t.Errorf("capture result = %+v", msg)
	}

	var p cats.CaptureParams
	if err := json.Unmarshal(srv.paramsFor(cats.MethodCapture), &p); err != nil {
		t.Fatalf("capture params did not decode: %v", err)
	}
	if p.Pane != 9 {
		t.Errorf("captured pane %d, want 9", p.Pane)
	}
	if p.Scope != cats.CaptureRecent {
		t.Errorf("scope = %d, want CaptureRecent (%d) — the visible viewport alone "+
			"loses the top of a long answer", p.Scope, cats.CaptureRecent)
	}
	if p.Lines != captureLines {
		t.Errorf("lines = %d, want %d", p.Lines, captureLines)
	}
	if p.Ansi {
		t.Error("ansi must stay off: a note stores markdown, not VT styling")
	}
	if p.Unwrap {
		t.Error("unwrap must stay off: it would rewrap prose to the pane's width")
	}
}

// A capture from a dead socket is a status line, not a crash and not a note.
func TestCaptureFailureBecomesAStatusError(t *testing.T) {
	m := newAppModel(newFakeStore())
	cmd := m.captureDone(captureDoneMsg{agent: "claude", err: errPoll})
	note, ok := drainCmd(cmd)[0].(statusNote)
	if !ok || !note.isErr {
		t.Fatalf("a failed capture should be a status error, got %#v", drainCmd(cmd)[0])
	}
	if !strings.Contains(note.text, "Capture failed") {
		t.Errorf("status = %q", note.text)
	}
}

// A pane that has printed nothing captures as a rectangle of spaces. That is an
// outcome, not a failure — and it must not open an empty note.
func TestCaptureOfAnEmptyPaneOpensNothing(t *testing.T) {
	m := newAppModel(newFakeStore())
	for _, blank := range []string{"", "   \n   \n", "\n\n\n"} {
		msgs := drainCmd(m.captureDone(captureDoneMsg{agent: "claude", text: blank}))
		if len(msgs) != 1 {
			t.Fatalf("blank %q produced %+v", blank, msgs)
		}
		note, ok := msgs[0].(statusNote)
		if !ok || !strings.Contains(note.text, "empty") {
			t.Errorf("blank %q should report an empty pane, got %#v", blank, msgs[0])
		}
	}
}

// ---- the note --------------------------------------------------------------

// A terminal buffer is a rectangle; a note is a document. Everything this strips
// is an artifact of the former that markdown would misread as content — trailing
// padding in particular, which markdown reads as a hard line break.
func TestSanitizeCapture(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"row padding", "hello     \nworld   ", "hello\nworld"},
		{"crlf", "a\r\nb", "a\nb"},
		{"bare cr", "a\rb", "a\nb"},
		{"blank rows above and below", "\n\n  \nreal text\n\n   \n", "real text"},
		{"tabs survive", "col\tcol\nx\ty", "col\tcol\nx\ty"},
		{"interior blank lines survive", "para one\n\npara two", "para one\n\npara two"},
		{"bare control bytes", "be\x07ll", "bell"},
		// The trap the byte-level strip would fall into: dropping the ESC alone
		// leaves "[2K" as visible text in the note.
		{"CSI sequence goes whole", "before\x1b[2Kafter", "beforeafter"},
		{"CSI with parameters", "a\x1b[38;5;204mb\x1b[0mc", "abc"},
		{"OSC ends at BEL", "a\x1b]0;a title\x07b", "ab"},
		{"OSC ends at ST", "a\x1b]0;a title\x1b\\b", "ab"},
		{"two-byte escape", "a\x1b=b", "ab"},
		{"a truncated sequence does not run off the end", "tail\x1b[", "tail"},
		{"already clean", "# Heading\n\ntext", "# Heading\n\ntext"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeCapture(c.in); got != c.want {
				t.Errorf("sanitizeCapture(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The title is what the note is found by later, so it names the agent and the
// moment. The clock is pinned because the format is the assertion.
func TestCaptureTitle(t *testing.T) {
	fixed := time.Date(2026, 8, 14, 15, 4, 0, 0, time.UTC)
	restore := captureNow
	captureNow = func() time.Time { return fixed }
	t.Cleanup(func() { captureNow = restore })

	if got, want := captureTitle("claude"), "Capture: claude — 2026-08-14 15:04"; got != want {
		t.Errorf("captureTitle = %q, want %q", got, want)
	}
	if got, want := captureTitle("  "), "Capture: agent pane — 2026-08-14 15:04"; got != want {
		t.Errorf("an unnamed agent still needs a title: %q, want %q", got, want)
	}
}

// A capture opens a form that is a CREATE, not an edit. Getting this wrong is
// silent: newFormScreen's editing path would make it save over note id 0 and
// try to load categories for an id that was never assigned.
func TestCaptureFormIsANewNote(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	m := newAppModel(fs)
	m.sess.user = user
	m.sess.width, m.sess.height = 100, 40

	var form *formScreen
	for _, msg := range drainCmd(m.captureDone(captureDoneMsg{
		agent: "claude", text: "captured body   \n",
	})) {
		if p, ok := msg.(pushMsg); ok {
			form, _ = p.s.(*formScreen)
		}
	}
	if form == nil {
		t.Fatal("a successful capture should push a form screen")
	}
	if form.editing != nil {
		t.Error("a captured note must be a create, not an edit")
	}
	if !strings.HasPrefix(form.title.Value(), "Capture: claude") {
		t.Errorf("title = %q", form.title.Value())
	}
	if form.tags.Value() != captureTag {
		t.Errorf("tags = %q, want %q", form.tags.Value(), captureTag)
	}
	if form.body.Value() != "captured body" {
		t.Errorf("body = %q — the trailing row padding should be gone", form.body.Value())
	}
}

// ---- end to end ------------------------------------------------------------

// TestCaptureLandsInAPrefilledForm is the payoff test: a real program, a real
// control socket, and a real key press on the picker, ending in a note form
// carrying what the fake agent printed.
//
// The picker is pushed with a pushMsg rather than by pressing ctrl+g, for the
// ordering reason Phase 5 documented: pushMsg is handled synchronously in the
// root Update, so it is ordered against the Enter that follows, whereas a key
// that pushes goes through a command whose result is re-queued and nothing
// orders that against a later Send. The ctrl+g door itself is covered above,
// where the assertion can be exact.
func TestCaptureLandsInAPrefilledForm(t *testing.T) {
	const answer = "The migration is done. 30 notes moved."

	srv := newControlServer(t)
	srv.setCapture("\n\n" + answer + "     \n\n   \n")

	fixed := time.Date(2026, 8, 14, 15, 4, 0, 0, time.UTC)
	restore := captureNow
	captureNow = func() time.Time { return fixed }
	t.Cleanup(func() { captureNow = restore })

	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	fs.seedNote(user.GUID, "Existing note", "body")

	m := newAppModel(fs)
	// Tier 1 installed directly, before the program starts. The handshake that
	// would normally produce this state is pinned by
	// TestCatsInitCompletesTheTier1Handshake; what is under test here is what
	// happens once it has.
	cs := m.sess.cats
	cs.caps = cats.Caps{InCats: true, Control: true, PaneHandle: "w1:p7"}
	cs.client = cats.NewClient(srv.path)
	cs.self, cs.selfOK = 7, true
	cs.panes = []cats.PaneInfo{
		{Pane: 7, Handle: "w1:p7", Agent: "gonotes"},
		{Pane: 9, Handle: "w1:p9", Agent: "claude", AgentState: cats.StateIdle},
	}
	picker := newAgentPickerScreen(m.sess)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))
	tm.Send(loggedInMsg{user: user})
	tm.Send(tea.WindowSizeMsg{Width: 100, Height: 40})
	tm.Send(pushMsg{s: picker})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	// One WaitFor over the whole run: teatest's Output() is a single consumable
	// stream, so a second call would start reading from wherever the first
	// stopped. The three strings span the three stages — picker, title, body.
	waitForAll(t, tm,
		"claude",                            // the picker offered the sibling pane
		"Capture: claude — 2026-08-14 15:04", // the form opened, titled
		answer,                              // carrying what the pane printed
	)

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
