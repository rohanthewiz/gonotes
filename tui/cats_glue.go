package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"gonotes/cats"

	tea "charm.land/bubbletea/v2"
)

// The seam between the cats client (package cats — sockets, no UI) and this
// Bubble Tea program.
//
// THE THREADING RULE, which every function in this file obeys: a goroutine
// started here may touch only the values it was handed and catsState.post.
// Everything that reads or writes catsState happens on the Bubble Tea event
// loop — inside Update, or inside a method Update calls. Program.Send is this
// program's PostEvent, and the same discipline applies: model state has
// exactly one mutator, the event loop.
//
// The corollary, which is easy to get wrong in Bubble Tea specifically: a
// tea.Cmd body ALSO runs on its own goroutine, so a Cmd may read the values
// closed over when it was built and must not touch catsState either. The two
// Cmds here (probeCmd) return their answer as a message and mutate nothing.
//
// TIER 0 IS THE ZERO VALUE. catsState's zero value is "not in cats, nothing
// connected", which is also what every failure path produces, so there is no
// enabled flag to check and no cleanup to do when detection fails. Outside
// cats the whole integration costs four getenv calls at startup — which is
// also why the Phase 2-4 test suites needed no changes: they construct the app
// model without ever calling init, and every method below is inert on that.
//
// WHAT THE HOOK REPORTER IS FOR. It is the only part of the integration that
// matters when the user is not looking at the screen: cats turns a
// working→idle transition into a "finished" badge, toast, and phone push. An
// editor session that ends while you are in another window is the whole point,
// and it is why the reporter is armed from the environment alone — before, and
// independently of, the control-socket probe that decides Tier 1.

// ---- messages ------------------------------------------------------------
//
// Everything a cats goroutine learns comes back as one of these. They are
// handled in the root Update (tui.go) so that the mutation happens in exactly
// one place on exactly one goroutine.

// catsReadyMsg carries the probe's verdict plus our own resolved pane id.
// The two ride together because resolving the pane needs the same socket the
// probe just proved live, and doing it here saves a second round trip later
// from a keystroke handler that must not dial.
type catsReadyMsg struct {
	caps   cats.Caps
	self   uint32
	selfOK bool
}

// catsEventMsg is one frame off the event stream, lifted onto the event loop.
type catsEventMsg struct{ ev cats.Event }

// catsLinkMsg reports the stream connection coming up or going down. It is the
// only thing that notices a cats which died AFTER startup said Tier 1.
type catsLinkMsg struct{ up bool }

// ---- state ---------------------------------------------------------------

// catsState is everything this process knows about its cats host, plus the
// handles it talks back through. It hangs off session as one pointer so the
// integration adds one field rather than eight, and so screens that report
// transitions (form.go) can reach it without message plumbing.
type catsState struct {
	caps     cats.Caps
	client   *cats.Client   // nil below Tier 1
	reporter *cats.Reporter // nil without a hook socket; independent of client
	stream   *cats.Stream   // nil below Tier 1; the event subscription

	// self is this pane's internal id, which control commands address panes
	// by. The environment only carries the public handle, so it costs a
	// pane.list to learn — done once during the startup probe. Its consumer is
	// Phase 7's agent picker, which must leave GoNotes' own pane off the list.
	self   uint32
	selfOK bool

	// activity is the phrase for the span currently in progress ("editing:
	// Recipes"), empty when nothing is running. asking holds the phrase for a
	// question GoNotes raised that the user did NOT ask for — see selfState,
	// where it is deliberately without a caller today.
	activity string
	asking   string

	// The last report, so transitions are reported and steady state is not.
	lastState, lastStatus string

	// send posts a message onto the Bubble Tea event loop. Set by Run from
	// Program.Send before the program starts; nil in tests and at Tier 0,
	// which makes post a no-op rather than a special case.
	send func(tea.Msg)

	// ttyWrite emits a raw escape sequence to the controlling terminal, for
	// the one sequence Bubble Tea has no field for (OSC 7). A field so a test
	// can capture instead of writing to a real tty.
	ttyWrite func(string) error

	// stopping is closed once the program is going away, so a late result from
	// a background goroutine does not try to post onto an event loop that has
	// stopped reading.
	stopping chan struct{}
	stopOnce sync.Once
}

// newCatsState builds the inert Tier-0 form. Nothing here does IO or reads the
// environment: detection is init's job, and init is called only from Run, so
// constructing an app model in a test costs nothing at all.
func newCatsState() *catsState {
	return &catsState{
		ttyWrite: ttyWriteReal,
		stopping: make(chan struct{}),
	}
}

// ---- lifecycle -----------------------------------------------------------

// init detects the host and arms the reporter. Called from Run BEFORE the
// program is created, for two reasons: the first state report is what puts
// "gonotes" in cats' sidebar, and it should be there from the first frame
// rather than a round trip later; and the OSC 7 emission has to reach the
// terminal before Bubble Tea switches to the alternate screen and starts
// owning the output stream.
//
// The detection is deliberately in two halves. The env sniff is free, so it
// runs inline and immediately decides whether the reporter can exist at all;
// the socket probe is IO against a server that might be wedged, so it becomes
// a tea.Cmd (probeCmd) and its answer arrives as a message.
//
// The reporter does not wait for the probe: the hook socket is a different
// address, and reporting state is worth doing even when the control API is
// unreachable.
func (cs *catsState) init() {
	env := cats.DetectEnv()
	cs.caps = env
	cs.reporter = cats.NewReporter(env.HookSocket, env.PaneHandle)

	// Claim the pane immediately. This first report both puts us in the
	// sidebar and establishes the state the first transition is measured
	// against.
	cs.reportNow()

	// Independent of cats: the working directory is for whatever terminal this
	// is, and cats is only the host that does the most with it (a new pane
	// opened from here inherits the notes directory).
	cs.emitCwd()
}

// probeCmd completes detection on a command goroutine and returns the verdict
// as a message. nil when there is nothing to probe, which tea.Batch drops.
//
// It is a tea.Cmd rather than a bare goroutine on purpose, and the reason is a
// shutdown deadlock rather than style. Program.Send BLOCKS while the program
// has not started yet; a goroutine launched before Run that ended up posting
// into that window — and then a stream reader doing the same — would make
// close's stream.Close(), which waits for its reader, hang forever on a
// program that failed to start. A Cmd cannot run until the event loop is
// already running, so the blocking window does not exist for anything
// downstream of this message.
func (cs *catsState) probeCmd() tea.Cmd {
	env := cs.caps
	if !env.InCats || env.ControlSocket == "" {
		return nil
	}
	return func() tea.Msg {
		caps := env.Probe()
		// Resolving our own pane id rides the probe rather than costing a
		// second round trip later: the Phase 7 picker that needs it is opened
		// by a keystroke, which must not dial a socket.
		var self uint32
		var selfOK bool
		if caps.Tier1() {
			if id, err := cats.NewClient(caps.ControlSocket).ResolvePane(caps.PaneHandle); err == nil {
				self, selfOK = id, true
			}
		}
		return catsReadyMsg{caps: caps, self: self, selfOK: selfOK}
	}
}

// ready installs the probe's verdict and opens the event stream. Runs on the
// event loop.
//
// The returned Cmd is the one user-visible trace of a FAILED probe. Silent
// degradation is the rule, but a user sitting inside a cats pane whose socket
// did not answer is the one person who might wonder why the integration is
// missing, and the status bar is where they would look. Outside cats there is
// nothing to explain, so nothing is said — and a successful connection says
// nothing either, because a notice that displaced the login screen's own
// feedback would be a regression for the common case.
func (cs *catsState) ready(msg catsReadyMsg) tea.Cmd {
	cs.caps, cs.self, cs.selfOK = msg.caps, msg.self, msg.selfOK
	if !msg.caps.Tier1() {
		if msg.caps.InCats && msg.caps.Reason != "" {
			return status("cats: " + msg.caps.Reason + " — running standalone")
		}
		return nil
	}
	cs.client = cats.NewClient(msg.caps.ControlSocket)
	cs.subscribe()
	return nil
}

// subscribe opens the event stream. Called once Tier 1 is confirmed, from the
// event loop, which is what guarantees the callbacks below can never hit
// Program.Send's pre-start blocking window.
//
// The filter names events and never a pane, which is load-bearing for Phase
// 6's theme sync: theme_changed is SESSION-scoped and cats emits it against
// pane 0, so a subscription narrowed to our own pane would never see it.
func (cs *catsState) subscribe() {
	if cs.stream != nil || !cs.tier1() {
		return
	}
	cs.stream = cats.Subscribe(cs.caps.ControlSocket,
		cats.SubscribeFilter{Events: []string{
			cats.EventThemeChanged,
			cats.EventPaneAgent,
			cats.EventPaneNotify,
			cats.EventPaneAdded,
			cats.EventPaneRemoved,
			cats.EventFocusChanged,
		}},
		// Both callbacks run on the reader goroutine: post and nothing else.
		func(ev cats.Event) { cs.post(catsEventMsg{ev: ev}) },
		func(up bool, _ error) { cs.post(catsLinkMsg{up: up}) })
}

// post hands a message to the event loop. It is the ONLY way a cats goroutine
// may reach model state.
//
// The stopping check keeps a late arrival from parking on the program's
// message queue after the loop has stopped draining it. It is a best-effort
// check rather than a lock: the loser of the race is a goroutine that blocks
// while the process is already exiting, which costs nothing, whereas a lock
// held across a queue send could deadlock against the loop itself.
func (cs *catsState) post(msg tea.Msg) {
	if cs == nil || cs.send == nil {
		return
	}
	select {
	case <-cs.stopping:
		return
	default:
	}
	cs.send(msg)
}

// frame dispatches one event. Runs on the event loop.
//
// This is the transport's seam, so it stays a switch on the name and nothing
// else: what a frame MEANS lives with the feature that consumes it —
// theme_changed in catstheme.go, and the pane events once the Phase 7 picker
// has a cache for them to invalidate. Until then they are dropped, which is
// deliberate: the subscription is what put the handshake and the shutdown
// ordering under test a phase before anything was built on top of them.
//
// An unknown name is ignored rather than surfaced: the event vocabulary grows
// on the host's schedule, and a client that complained about names newer than
// itself would turn every cats upgrade into noise.
func (cs *catsState) frame(ev cats.Event) tea.Cmd {
	switch ev.Name {
	case cats.EventThemeChanged:
		return catsThemeChanged(ev)
	}
	return nil
}

// stop closes the stopping channel exactly once. Both the quit path and the
// shutdown path can reach it, and either may be first.
func (cs *catsState) stop() {
	if cs == nil || cs.stopping == nil {
		return
	}
	cs.stopOnce.Do(func() { close(cs.stopping) })
}

// close hands the pane back. Called after the program's Run has returned —
// including when it returned an error, because a TUI that failed to start
// still claimed the pane in init.
//
// Order is load-bearing: the stream stops FIRST, and Close waits for its
// reader to be gone, so no callback is still in flight posting onto a loop
// that has stopped. Only then is the pane released — which blocks briefly, as
// it must, because the process is about to exit and a goroutine posted here
// would be killed before it ever dialed.
func (cs *catsState) close() {
	if cs == nil {
		return
	}
	cs.stop()
	cs.stream.Close() // nil-safe
	cs.stream = nil
	cs.reporter.Release() // nil-safe
}

// tier1 is the one question every Tier-1 feature asks. Both halves are checked
// because the stream can report the link down later without the client being
// torn down.
func (cs *catsState) tier1() bool {
	return cs != nil && cs.client != nil && cs.caps.Tier1()
}

// ---- state reporting -----------------------------------------------------

// selfState maps GoNotes' run state onto the hook API's vocabulary, in
// priority order.
//
// There is no honest `blocked` source today, and that is a deliberate reading
// of what the state means rather than an omission: blocked is a question
// GoNotes raised that the user did not ask for. Every modal the TUI opens —
// the delete confirmation, the category picker — was opened by the keystroke
// the user just pressed, and paging someone about a dialog they summoned is
// how a notification channel earns being muted. A future prompt that
// INTERRUPTS (an expired session asking for the password again) is the case
// that should call asking, which is why the branch exists ahead of any caller.
func (cs *catsState) selfState() (state, status string) {
	if cs.asking != "" {
		return cats.StateBlocked, cs.asking
	}
	if cs.activity != "" {
		return cats.StateWorking, cs.activity
	}
	return cats.StateIdle, ""
}

// reportNow publishes the current state if it differs from the last one
// published. Call from the event loop.
//
// Reporting only on change is noise control, not economy: every report is a
// potential toast or phone push, and a channel that fires when nothing
// happened is one the user switches off. The cost when nothing changed is two
// string comparisons.
func (cs *catsState) reportNow() {
	if cs == nil || cs.reporter == nil {
		return
	}
	state, status := cs.selfState()
	if state == cs.lastState && status == cs.lastStatus {
		return
	}
	cs.lastState, cs.lastStatus = state, status
	cs.reporter.ReportState(state, status)
}

// working opens a span: GoNotes is doing something the user may walk away
// from. what is the phrase cats shows beside the pane and pushes to a phone,
// so it is written as a message ("editing: Recipes") rather than as a UI fact
// ("form busy").
//
// Both edges are called from screen Update methods, i.e. from the event loop.
// They mutate catsState directly rather than returning a tea.Cmd on purpose: a
// Cmd body runs on its own goroutine, which would put lastState/lastStatus
// under a race. The send itself is already asynchronous inside the reporter.
func (cs *catsState) working(what string) {
	if cs == nil {
		return
	}
	cs.activity = what
	cs.reportNow()
}

// idle closes whatever span was open. Safe to call when none was.
func (cs *catsState) idle() {
	if cs == nil {
		return
	}
	cs.activity = ""
	cs.reportNow()
}

// asking marks that GoNotes has raised a question the user did not ask for.
// It has no caller today by design; see selfState for the rule that decides
// when it should get one. askingDone is its other half.
func (cs *catsState) askingNow(reason string) {
	if cs == nil {
		return
	}
	cs.asking = reason
	cs.reportNow()
}

// askingDone clears the mark once the question has been answered.
func (cs *catsState) askingDone() {
	if cs == nil || cs.asking == "" {
		return
	}
	cs.asking = ""
	cs.reportNow()
}

// ---- host identity (Tier 0) ----------------------------------------------
//
// Telling the terminal who and where we are. This works in ANY terminal that
// understands the sequences; cats is simply the host that does the most with
// them (the tab label, and the directory a new pane inherits). It lives here
// because it is the same act of identifying ourselves to a host, one layer
// down.
//
//	title   the WindowTitle field on the tea.View the root model returns.
//	        Bubble Tea owns the OSC 2 emission and its save/restore, so
//	        nothing here writes it directly.
//	cwd     OSC 7, written to /dev/tty before the program starts. Bubble Tea
//	        has no cwd field, and this is the one sequence we spell out
//	        ourselves.

// windowTitle is what the terminal tab says. The running span leads, because a
// title is read at a glance from a tab bar the pane is not in — the question it
// answers there is "is this thing still going?", and the app name is the
// context for that answer rather than the answer.
//
// Nothing here is gated on cats: this is the Tier-0 half, and it is computed
// fresh in View rather than cached, because it is two string comparisons and a
// concatenation against a frame that was going to be rebuilt anyway.
func (cs *catsState) windowTitle() string {
	if cs == nil {
		return appTitle
	}
	if tag := titleSafe(cs.activity); tag != "" {
		return tag + " — " + appTitle
	}
	return appTitle
}

// appTitle is the bare window title, and the suffix every other form ends in.
const appTitle = "GoNotes"

// emitCwd reports the working directory as a file URL, once, before the
// program takes the terminal. Hosts use it to open a new tab or split in the
// same place — which is the whole payoff, and why it is worth emitting even
// though nothing in GoNotes reads it back.
//
// Every failure is ignored: a terminal that does not understand the sequence,
// or a process with no controlling terminal, costs one failed open.
func (cs *catsState) emitCwd() {
	if cs.ttyWrite == nil {
		return // no controlling terminal, or a test
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	_ = cs.ttyWrite(osc7CwdSeq(hostname(), cwd))
}

// titleSafe strips what must never reach a terminal's parser. Note titles come
// from the user's own text, so an embedded ESC or BEL would terminate the
// escape sequence early and leave the remainder to be interpreted as commands.
func titleSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
}

// osc7CwdSeq builds the OSC 7 report for a directory.
func osc7CwdSeq(host, dir string) string {
	return fmt.Sprintf("\x1b]7;file://%s%s\x07", host, fileURLPath(dir))
}

// fileURLPath percent-encodes a path for a file:// URL. net/url is
// deliberately not used: url.URL escapes for a generic URL and leaves
// characters this sequence has to encode, and the terminating BEL means a
// stray control byte in a directory name would truncate the sequence rather
// than corrupt one field of it.
func fileURLPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '/' || c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// hostname is the URL's authority. An unknown host is spelled as the empty
// authority rather than "localhost": a file URL with no host means "this
// machine", which is exactly what we would be guessing at anyway.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return titleSafe(h)
}

// ttyWriteReal writes an escape sequence straight to the controlling terminal.
// Opened per emission — there is exactly one in a session, and holding the
// descriptor open would be a resource kept for nothing.
func ttyWriteReal(seq string) error {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(seq)
	return err
}
