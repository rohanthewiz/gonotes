package tui

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gonotes/cats"

	tea "charm.land/bubbletea/v2"
)

// ---- fake host -------------------------------------------------------------

// catsSockPath returns a unix socket path in a deliberately SHORT temp
// directory. t.TempDir() cannot be used: it embeds the test's name, and
// sun_path is 104 bytes on macOS — a descriptive test name plus the system
// TMPDIR overruns it, and the failure ("bind: invalid argument") reads like a
// permissions problem rather than a length one.
func catsSockPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "g")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// hookSink is a fake cats hook socket: it reads one fire-and-forget request per
// connection and never replies, which is exactly what the real one does.
type hookSink struct {
	path string

	mu   sync.Mutex
	reqs []hookReq
}

// hookReq is the shape a report arrives in. Seq is deliberately absent: the
// cats package's own suite pins the numbering, and decoding it here would need
// the json.Number dance for no assertion this file makes.
type hookReq struct {
	Method string `json:"method"`
	Params struct {
		PaneID       string `json:"pane_id"`
		Source       string `json:"source"`
		Agent        string `json:"agent"`
		State        string `json:"state"`
		CustomStatus string `json:"custom_status"`
	} `json:"params"`
}

func newHookSink(t *testing.T) *hookSink {
	t.Helper()
	s := &hookSink{path: catsSockPath(t, "h.sock")}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req hookReq
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					return
				}
				s.mu.Lock()
				s.reqs = append(s.reqs, req)
				s.mu.Unlock()
			}()
		}
	}()
	return s
}

func (s *hookSink) all() []hookReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]hookReq(nil), s.reqs...)
}

func (s *hookSink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// wantReports blocks until n reports have landed. Reports are sent on their own
// goroutines, so they arrive after the call that triggered them returned.
func (s *hookSink) wantReports(t *testing.T, n int) []hookReq {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.len() >= n {
			return s.all()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d hook reports, got %d: %+v", n, s.len(), s.all())
	return nil
}

// inCatsPane sets the four environment variables cats gives a pane. An empty
// control socket is the "hooks only" host: reporting works, Tier 1 does not.
func inCatsPane(t *testing.T, handle, control, hook string) {
	t.Helper()
	t.Setenv(cats.EnvMarker, "1")
	t.Setenv(cats.EnvPaneID, handle)
	t.Setenv(cats.EnvControlSocket, control)
	t.Setenv(cats.EnvHookSocket, hook)
}

// notInCats clears the pane environment, which is how every other test in this
// package already runs — stated explicitly where a test depends on it.
func notInCats(t *testing.T) {
	t.Helper()
	t.Setenv(cats.EnvMarker, "")
	t.Setenv(cats.EnvPaneID, "")
	t.Setenv(cats.EnvControlSocket, "")
	t.Setenv(cats.EnvHookSocket, "")
}

// testCatsState is the glue with its terminal writes captured instead of sent
// to a real tty, which a test process may not even have.
func testCatsState(seqs *[]string) *catsState {
	cs := newCatsState()
	cs.ttyWrite = func(seq string) error {
		*seqs = append(*seqs, seq)
		return nil
	}
	return cs
}

// ---- Tier 0 ----------------------------------------------------------------

// The whole point of the zero value: outside cats, init detects nothing, arms
// nothing, and every method is a no-op. This is the same state the Phase 2-4
// suites run in, which is why they needed no changes.
func TestCatsTier0IsInert(t *testing.T) {
	notInCats(t)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()

	if cs.reporter != nil {
		t.Error("no hook socket should produce no reporter")
	}
	if cs.tier1() {
		t.Error("outside cats there is no Tier 1")
	}
	if cs.probeCmd() != nil {
		t.Error("there is nothing to probe outside a cats pane")
	}
	if cs.caps.Reason == "" {
		t.Error("a Tier-0 verdict should carry a reason")
	}

	// The transitions still run; they simply reach a nil reporter.
	cs.working("editing: Recipes")
	cs.idle()
	cs.askingNow("session expired")
	cs.askingDone()
	cs.close() // must not panic on a state that never connected to anything
}

// A nil *catsState is what a screen would see if the session were built without
// one. Every method has to survive it, because the alternative is a nil check
// at each of the call sites this file exists to keep clean.
func TestCatsNilStateIsSafe(t *testing.T) {
	var cs *catsState
	cs.working("x")
	cs.idle()
	cs.askingNow("x")
	cs.askingDone()
	cs.post(catsLinkMsg{up: true})
	cs.reportNow()
	cs.close()
	if cs.tier1() {
		t.Error("a nil state is Tier 0")
	}
	if got := cs.windowTitle(); got != appTitle {
		t.Errorf("windowTitle on a nil state = %q, want %q", got, appTitle)
	}
}

// ---- host identity ---------------------------------------------------------

func TestWindowTitleLeadsWithTheActivity(t *testing.T) {
	cases := []struct {
		activity string
		want     string
	}{
		{"", "GoNotes"},
		{"editing: Recipes", "editing: Recipes — GoNotes"},
		{"   ", "GoNotes"}, // whitespace is not an activity
	}
	for _, c := range cases {
		cs := &catsState{activity: c.activity}
		if got := cs.windowTitle(); got != c.want {
			t.Errorf("activity %q → title %q, want %q", c.activity, got, c.want)
		}
	}
}

// A note title is user text and it reaches an OSC 2 sequence. An embedded ESC
// or BEL would end that sequence early and leave the rest to be interpreted as
// terminal commands.
func TestWindowTitleStripsControlBytes(t *testing.T) {
	cs := &catsState{activity: "editing: \x1b]0;pwned\x07note"}
	got := cs.windowTitle()
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Errorf("control bytes survived into the window title: %q", got)
	}
}

func TestOSC7EncodesThePath(t *testing.T) {
	got := osc7CwdSeq("box", "/Users/me/my notes/ü")
	if !strings.HasPrefix(got, "\x1b]7;file://box/") {
		t.Errorf("wrong prefix: %q", got)
	}
	if !strings.HasSuffix(got, "\x07") {
		t.Errorf("sequence is not BEL-terminated: %q", got)
	}
	// A space in a path is the common case and must not end the URL early;
	// non-ASCII is percent-encoded byte by byte.
	if strings.ContainsRune(got, ' ') {
		t.Errorf("an unencoded space reached the sequence: %q", got)
	}
	if !strings.Contains(got, "my%20notes") {
		t.Errorf("space not percent-encoded: %q", got)
	}
}

// ---- reporting -------------------------------------------------------------

// init has to claim the pane before the first frame — that report is what puts
// "gonotes" in cats' sidebar — and emit the working directory before Bubble Tea
// takes the terminal.
func TestCatsInitClaimsThePaneAndEmitsCwd(t *testing.T) {
	sink := newHookSink(t)
	inCatsPane(t, "w1:p3", "", sink.path)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()

	got := sink.wantReports(t, 1)[0]
	if got.Method != "pane.report_agent" {
		t.Errorf("method = %q", got.Method)
	}
	if got.Params.PaneID != "w1:p3" {
		t.Errorf("pane_id = %q — the hook API addresses the PUBLIC handle", got.Params.PaneID)
	}
	if got.Params.Source != "gonotes" || got.Params.Agent != "gonotes" {
		t.Errorf("source/agent = %q/%q", got.Params.Source, got.Params.Agent)
	}
	if got.Params.State != cats.StateIdle {
		t.Errorf("the opening claim should be idle, got %q", got.Params.State)
	}

	if len(seqs) != 1 || !strings.HasPrefix(seqs[0], "\x1b]7;file://") {
		t.Errorf("init should emit exactly one OSC 7, got %q", seqs)
	}
}

// Every report is a potential toast or phone push, so only transitions are
// published. Reporting steady state is how a notification channel earns being
// muted.
func TestOnlyTransitionsAreReported(t *testing.T) {
	sink := newHookSink(t)
	inCatsPane(t, "w1:p3", "", sink.path)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init() // report 1: the idle claim

	cs.working("editing: Recipes") // report 2
	cs.working("editing: Recipes") // identical — must not be sent again
	cs.idle()                      // report 3
	cs.idle()                      // already idle — must not be sent again

	sink.wantReports(t, 3)
	// A fourth would arrive late, so give the goroutines a moment to be wrong.
	time.Sleep(100 * time.Millisecond)
	got := sink.all()
	if len(got) != 3 {
		t.Fatalf("expected exactly 3 reports, got %d: %+v", len(got), got)
	}

	// Counted rather than indexed: each report rides its own goroutine, so
	// arrival order is genuinely not guaranteed — that is the whole reason the
	// wire carries a seq, and the cats package's own suite is where the
	// ordering is pinned. What this test owns is the DEDUP: five calls, three
	// reports.
	counts := map[string]int{}
	for _, r := range got {
		counts[r.Params.State+"/"+r.Params.CustomStatus]++
	}
	if n := counts[cats.StateWorking+"/editing: Recipes"]; n != 1 {
		t.Errorf("the working span was reported %d times, want 1: %+v", n, got)
	}
	if n := counts[cats.StateIdle+"/"]; n != 2 {
		t.Errorf("idle was reported %d times, want 2 (the claim and the close): %+v", n, got)
	}
}

// asking outranks a working span: a question the user did not ask for is the
// one thing more worth their attention than a job that is still running.
func TestAskingOutranksWorking(t *testing.T) {
	cs := &catsState{activity: "editing: Recipes", asking: "session expired"}
	state, status := cs.selfState()
	if state != cats.StateBlocked || status != "session expired" {
		t.Errorf("selfState = %q/%q, want blocked/session expired", state, status)
	}
	cs.asking = ""
	if state, status = cs.selfState(); state != cats.StateWorking || status != "editing: Recipes" {
		t.Errorf("selfState = %q/%q, want working/editing: Recipes", state, status)
	}
}

// close hands the pane back, so it stops wearing a "gonotes" badge the moment
// the process exits rather than until something else claims it. It runs after
// the program's Run has returned — including when Run failed to start, which is
// why it can never depend on anything the event loop set up.
func TestCatsCloseReleasesThePane(t *testing.T) {
	sink := newHookSink(t)
	inCatsPane(t, "w1:p3", "", sink.path)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()
	sink.wantReports(t, 1)

	cs.close()
	got := sink.wantReports(t, 2)[1]
	if got.Method != "pane.release_agent" {
		t.Errorf("method = %q, want pane.release_agent", got.Method)
	}
	if got.Params.PaneID != "w1:p3" || got.Params.Source != "gonotes" {
		t.Errorf("release did not name the pane and source: %+v", got.Params)
	}

	cs.close() // idempotent: the quit path and the shutdown path can both reach it
}

// A message posted after the program has gone away must be dropped rather than
// parked on a queue nobody is draining.
func TestPostIsDroppedAfterStop(t *testing.T) {
	var got []tea.Msg
	cs := newCatsState()
	cs.send = func(m tea.Msg) { got = append(got, m) }

	cs.post(catsLinkMsg{up: true})
	if len(got) != 1 {
		t.Fatalf("a live post should reach the loop, got %d messages", len(got))
	}

	cs.stop()
	cs.post(catsLinkMsg{up: false})
	if len(got) != 1 {
		t.Errorf("a post after stop must be dropped, got %d messages", len(got))
	}
}

// The link callback is the only thing that notices a cats which died AFTER the
// probe said Tier 1, and dropping Control is what re-gates the features.
func TestLinkDownRegatesTier1(t *testing.T) {
	m := newAppModel(newFakeStore())
	cs := m.sess.cats
	cs.caps = cats.Caps{InCats: true, Control: true, PaneHandle: "w1:p3"}
	cs.client = cats.NewClient("/nonexistent.sock")
	if !cs.tier1() {
		t.Fatal("setup: expected Tier 1")
	}

	updated, _ := m.Update(catsLinkMsg{up: false})
	if updated.(appModel).sess.cats.tier1() {
		t.Error("a dropped link must re-gate Tier 1")
	}
	updated, _ = updated.(appModel).Update(catsLinkMsg{up: true})
	if !updated.(appModel).sess.cats.tier1() {
		t.Error("a recovered link must restore Tier 1 without a new probe")
	}
}
