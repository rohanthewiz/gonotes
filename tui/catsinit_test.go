package tui

import (
	"encoding/json"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"gonotes/cats"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// ---- a whole fake host on one socket ---------------------------------------

// controlServer answers the entire startup conversation: ping, pane.list, and
// the event subscription. One socket, because that is how the real thing works
// — the control API is connectionless per command and the stream is the same
// address upgraded.
type controlServer struct {
	path string

	mu  sync.Mutex
	got []string
}

func newControlServer(t *testing.T) *controlServer {
	t.Helper()
	s := &controlServer{path: catsSockPath(t, "c.sock")}
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
				var req struct {
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					return
				}
				s.mu.Lock()
				s.got = append(s.got, req.Method)
				s.mu.Unlock()

				var data any
				switch req.Method {
				case cats.MethodPing:
					data = cats.Pong{Protocol: 1, Service: "catway"}
				case cats.MethodPaneList:
					data = cats.PaneListResult{Panes: []cats.PaneInfo{
						{Pane: 7, Handle: "w1:p7"},
						{Pane: 9, Handle: "w1:p9", Agent: "claude"},
					}}
				case cats.MethodSubscribe:
					// Ack and hold the connection open, as a live stream does.
					// The sleep is what keeps this goroutine from closing the
					// socket and sending the client into its reconnect loop
					// mid-assertion.
					_, _ = conn.Write([]byte(`{"ok":true}` + "\n"))
					time.Sleep(2 * time.Second)
					return
				}
				raw, _ := json.Marshal(data)
				reply := map[string]any{"ok": true}
				if data != nil {
					reply["data"] = json.RawMessage(raw)
				}
				line, _ := json.Marshal(reply)
				_, _ = conn.Write(append(line, '\n'))
			}()
		}
	}()
	return s
}

func (s *controlServer) saw(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.got {
		if m == method {
			return true
		}
	}
	return false
}

func (s *controlServer) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func (s *controlServer) wantMethod(t *testing.T, method string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.saw(method) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("startup never sent %s; saw %v", method, s.all())
}

// TestCatsInitCompletesTheTier1Handshake drives the whole startup conversation
// exactly as Run and the root Update drive it — init, then the probe as a
// command, then its verdict through ready — and pins the four steps that have
// to happen in that order:
//
//	ping        the Tier-1 gate; a socket file alone proves nothing
//	pane.list   resolving "which pane am I?", which the environment never says
//	subscribe   the event stream, opened only once Tier 1 is confirmed
//	close       stream first, then the pane release
//
// The probe command is invoked synchronously here rather than through a live
// program. That is the same function body Init() hands to Bubble Tea, and
// calling it directly is what makes the sequence deterministic instead of a
// race against a renderer.
func TestCatsInitCompletesTheTier1Handshake(t *testing.T) {
	srv := newControlServer(t)
	sink := newHookSink(t)
	inCatsPane(t, "w1:p7", srv.path, sink.path)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()

	// The pane claim does not wait for the probe: the hook socket is a
	// different address, and reporting state is worth doing even when the
	// control API turns out to be unreachable.
	sink.wantReports(t, 1)

	probe := cs.probeCmd()
	if probe == nil {
		t.Fatal("a pane with a control socket must produce a probe")
	}
	msg, ok := probe().(catsReadyMsg)
	if !ok {
		t.Fatalf("probe returned %T, want catsReadyMsg", msg)
	}
	if !msg.caps.Tier1() {
		t.Fatalf("expected Tier 1, got %+v", msg.caps)
	}
	if msg.caps.Service != "catway" {
		t.Errorf("the ping self-identification was lost: %q", msg.caps.Service)
	}
	// "w1:p7" is the public form, which carries no internal id — the only way
	// to get 7 is the pane.list round trip, and getting it wrong would mean a
	// picker that offers to capture from GoNotes' own pane.
	if !msg.selfOK || msg.self != 7 {
		t.Errorf("our own pane id was not resolved: %d/%v", msg.self, msg.selfOK)
	}

	cs.ready(msg)
	if !cs.tier1() {
		t.Fatal("ready did not install the Tier-1 verdict")
	}
	srv.wantMethod(t, cats.MethodPing)
	srv.wantMethod(t, cats.MethodPaneList)
	srv.wantMethod(t, cats.MethodSubscribe)

	cs.close()
	if cs.stream != nil {
		t.Error("close should drop the stream")
	}
	got := sink.wantReports(t, 2)[1]
	if got.Method != "pane.release_agent" {
		t.Errorf("close did not release the pane: %+v", got)
	}
}

// A pane whose control socket does not answer stays at Tier 0 — and says so
// once, in the status bar. Silent degradation is the rule, but a user sitting
// inside a cats pane is the one person who might wonder where the integration
// went, and this is the only place they would look.
func TestFailedProbeDegradesWithAReason(t *testing.T) {
	sink := newHookSink(t)
	// A path that exists as a directory entry but has nothing listening: the
	// dial fails, which is exactly a crashed cats that left its socket behind.
	inCatsPane(t, "w1:p7", catsSockPath(t, "dead.sock"), sink.path)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()

	msg := cs.probeCmd()().(catsReadyMsg)
	if msg.caps.Tier1() {
		t.Fatal("a socket nothing is listening on must not reach Tier 1")
	}

	cmd := cs.ready(msg)
	if cmd == nil {
		t.Fatal("a failed probe inside a pane should explain itself")
	}
	note, ok := cmd().(statusNote)
	if !ok {
		t.Fatalf("ready returned a %T, want a statusNote", cmd())
	}
	if !strings.Contains(note.text, "standalone") {
		t.Errorf("the degradation notice does not say what happened: %q", note.text)
	}
	if cs.stream != nil {
		t.Error("no stream may be opened below Tier 1")
	}

	// Reporting still works: the hook socket is a separate address, and the
	// pane claim from init is the proof. Polled rather than counted on the
	// spot — reports ride their own goroutines and land after the call that
	// triggered them returns.
	sink.wantReports(t, 1)
}

// Outside cats there is nothing to explain, so nothing is said. A status note
// on every launch in a plain terminal would be the integration announcing
// itself for no reason.
func TestTier0SaysNothing(t *testing.T) {
	notInCats(t)
	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()

	if cmd := cs.ready(catsReadyMsg{caps: cs.caps}); cmd != nil {
		t.Errorf("Tier 0 outside cats should be silent, got %v", cmd())
	}
}

// ---- the editor span, end to end -------------------------------------------

// TestEditorSpanIsReportedToTheHost is the payoff test for the whole phase: the
// external editor is the one place GoNotes hands the terminal away for an
// unbounded time, and a working→idle edge is what cats turns into a "finished"
// toast and a phone push.
//
// It runs through the real program — a real key press on the real form screen,
// a real tea.ExecProcess — against a fake hook socket, so what is asserted is
// what a live cats would receive.
func TestEditorSpanIsReportedToTheHost(t *testing.T) {
	// A real editor that exits immediately. Looked up rather than hardcoded
	// because /usr/bin/true and /bin/true both exist depending on the system.
	editor, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no `true` binary to stand in for an editor")
	}
	t.Setenv("VISUAL", editor)
	t.Setenv("EDITOR", editor)

	sink := newHookSink(t)
	// Hooks only, no control socket: the attention story is the half that must
	// work without Tier 1, and running the test this way proves it.
	inCatsPane(t, "w1:p3", "", sink.path)

	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	note := fs.seedNote(user.GUID, "Recipes", "original body")

	m := newAppModel(fs)
	var seqs []string
	m.sess.cats.ttyWrite = func(seq string) error { seqs = append(seqs, seq); return nil }
	m.sess.cats.init()
	sink.wantReports(t, 1) // the idle claim

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))
	tm.Send(loggedInMsg{user: user})
	tm.Send(tea.WindowSizeMsg{Width: 100, Height: 40})

	// The form is pushed by a message rather than by navigating with keys.
	// pushMsg is handled synchronously in the root Update, so it is ordered
	// against the ctrl+e that follows; a key that pushes goes through a command
	// whose result is re-queued, and nothing orders that against a later Send.
	tm.Send(pushMsg{s: newFormScreen(m.sess, &note)})
	tm.Send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})

	got := sink.wantReports(t, 3)
	if got[1].Params.State != cats.StateWorking {
		t.Fatalf("opening the editor should report working, got %q", got[1].Params.State)
	}
	if got[1].Params.CustomStatus != "editing: Recipes" {
		t.Errorf("the status should name the note, got %q", got[1].Params.CustomStatus)
	}
	// The closing edge: the editor exited, so the pane must stop being badged.
	// Without it the pane would wear "editing" until the next transition, and
	// cats would never fire the finished notification this phase exists for.
	if got[2].Params.State != cats.StateIdle {
		t.Errorf("the editor exiting should report idle, got %q", got[2].Params.State)
	}

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// A temp-file failure means no editor ever runs, so no span may open — a pane
// badged "editing" for an editor that never launched would stay badged until
// the next transition.
func TestNoEditorSpanWhenTheTempFileFails(t *testing.T) {
	sink := newHookSink(t)
	inCatsPane(t, "w1:p3", "", sink.path)

	var seqs []string
	cs := testCatsState(&seqs)
	cs.init()
	sink.wantReports(t, 1)

	// TMPDIR at a path that cannot hold a file is the cheapest way to make the
	// write fail without touching the function's signature.
	t.Setenv("TMPDIR", "/nonexistent-dir-for-gonotes-test")

	cmd := openEditorCmd(cs, "body", "Recipes")
	msg, ok := cmd().(editorFinishedMsg)
	if !ok || msg.err == nil {
		t.Fatalf("expected an editorFinishedMsg carrying an error, got %#v", msg)
	}
	if cs.activity != "" {
		t.Errorf("no span may open when no editor runs, got %q", cs.activity)
	}

	time.Sleep(100 * time.Millisecond)
	if n := sink.len(); n != 1 {
		t.Errorf("expected only the opening claim, got %d reports: %+v", n, sink.all())
	}
}

// The phrase a note with no title gets. It is read on a phone with no other
// context, so it has to be a sentence rather than a dangling "editing:".
func TestEditorActivityPhrase(t *testing.T) {
	cases := []struct{ title, want string }{
		{"Recipes", "editing: Recipes"},
		{"  Recipes  ", "editing: Recipes"},
		{"", "editing a note"},
		{"   ", "editing a note"},
	}
	for _, c := range cases {
		if got := editorActivity(c.title); got != c.want {
			t.Errorf("editorActivity(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}
