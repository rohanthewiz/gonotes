package tui

import (
	"gonotes/models"

	tea "charm.land/bubbletea/v2"
	"github.com/rohanthewiz/serr"
)

// Package tui implements a terminal UI for GoNotes built on Bubble Tea.
//
// Architecture: a single root model owns a *stack* of screens and a shared
// session. Screens are pushed (list → detail → form) and popped with esc,
// which gives free "go back" semantics without every screen knowing what
// came before it — a deliberate departure from the earlier flat screen-enum
// prototype where each transition was hand-wired.
//
//	┌──────────────────────────────────────────────┐
//	│ appModel (root)                              │
//	│   session{user, width, height}   ── shared   │
//	│   stack: [login] → [browse, detail, form...] │
//	│   status bar (bottom line, all screens)      │
//	└──────────────────────────────────────────────┘
//
// Screens communicate only via messages (pushMsg/popMsg/statusNote and the
// data-carrying messages in commands.go); they never reference each other.

// screen is the small contract every view implements. Update returns the
// (possibly replaced) screen plus a command, mirroring tea.Model but typed
// to our interface so the root can keep a heterogeneous stack.
type screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (screen, tea.Cmd)
	View() string
}

// refresher is optionally implemented by screens that can reload their data.
// The root invokes it on the screen exposed by a pop(refresh=true), e.g.
// the browse list reloads after a form save or a delete.
type refresher interface {
	refresh() tea.Cmd
}

// restyler is implemented by every screen that holds state derived from the
// palette rather than read from it at render time — bubbles widgets (which
// copy their style set in at construction) and cached rendered output.
//
// The package-level styles in styles.go do not need this: they are read fresh
// on every View(), so a screen that only uses them repaints correctly with no
// notification at all. Everything a screen *stores* is what this exists for.
//
// The root broadcasts to the whole stack, not just the top screen, so a screen
// resumed after a pop is never left painted in the old colors.
type restyler interface {
	restyle()
}

// paletteChangedMsg announces that setPalette accepted a new palette. It is a
// message rather than a direct call so the restyle happens on the Bubble Tea
// event loop: Phase 6's host theme changes arrive on a socket goroutine, and
// mutating bubbles models from there would race every Update in flight.
type paletteChangedMsg struct{}

func paletteChanged() tea.Cmd {
	return func() tea.Msg { return paletteChangedMsg{} }
}

// Mode is how this launch reached its data, said in a form a person can read.
// It is display only — the Store is what actually does the reaching — and it
// exists because the choice between them is made before the TUI starts and was,
// until now, invisible afterwards.
type Mode struct {
	// Badge is a short label shown beside the list title for as long as the
	// program runs, and ONLY when the data is not in the ordinary place: the
	// server it is talking to, or a second data directory. Empty means the usual
	// notes in the usual location, which needs no sign.
	//
	// Persistent rather than a startup line because "which notes am I looking
	// at" is not a question that stops mattering after three seconds — and
	// because the status bar is claimed by the next thing that has something to
	// say (inside cats, the capture hint, within a second of launch).
	Badge string

	// Notice is a one-off status line explaining a decision the user did not ask
	// for and might not expect — a server that was ignored, or one that was
	// expected and is not there. Empty when the launch was unremarkable.
	Notice string
}

// session carries state shared by every screen. A pointer to it is embedded
// in each screen, so window resizes, the authenticated user, and the data
// store are always current without message plumbing.
type session struct {
	// store is how every screen reaches its data. It is set once, before the
	// program starts, and never reassigned — which is what makes it safe to
	// read from the command goroutines. See store.go.
	store Store

	// cats is the host-integration state: what this process knows about the
	// cats pane it may be running in, and the handles it reports back through.
	// Always non-nil; its zero value is Tier 0 (not in cats, nothing
	// connected), so screens call into it without an availability check. See
	// cats_glue.go.
	cats *catsState

	// mode labels where the data is coming from. Read by the browse title and
	// by the root's opening status line; nothing routes on it.
	mode Mode

	// sync is what this process last heard about the sync clock. Always
	// non-nil; its zero value reports itself unconfigured, so screens read it
	// without a nil check on the many installations that have no hub. Mutated
	// only in the root's Update — see syncState.
	sync *syncState

	user   *models.User
	width  int
	height int // content height available to screens (status bar excluded)
}

// Run starts the TUI and blocks until the user quits.
//
// The caller chooses the store and is responsible for whatever it needs: a
// local store requires models.InitDB to have succeeded, an HTTP store requires
// nothing (it will report its own connection failures through the status bar).
// That inversion is the point of Phase 4 — the TUI no longer assumes it owns
// the database files.
//
// tea.WithAltScreen() is gone in v2: the alternate screen is now a property
// of the view the model returns, not of the program, so it is set in View()
// below instead of here.
//
// The cats wiring is ordered, and each step is where it is for a reason:
//
//	theme fetch    before newAppModel — bubbles widgets copy their style set in
//	               at construction, and newAppModel builds the login screen, so
//	               a palette that landed later would repaint rather than paint.
//	cs.init()      before NewProgram — the first hook report claims the pane,
//	               and the OSC 7 emission has to reach the terminal before
//	               Bubble Tea switches to the alternate screen and owns output.
//	cs.send        before p.Run() — the stream's callbacks reach the event
//	               loop through it, and it must be in place before anything
//	               can be subscribed.
//	cs.close()     after p.Run() returns, ALWAYS. init claimed the pane even
//	               on a run that failed to start, so the release is owed
//	               either way; deferring the error return until after is what
//	               makes that unconditional.
//
// The probe itself is NOT started here. It is a tea.Cmd issued from Init(),
// so nothing downstream of it can run before the event loop does — see
// probeCmd for the shutdown deadlock that ordering avoids.
func Run(st Store, mode Mode) error {
	// One synchronous config.get inside a cats pane, and nothing at all outside
	// one. See catstheme.go for why it is worth blocking the launch for.
	catsThemeAtStartup()

	m := newAppModel(st)
	// Set after construction rather than passed through newAppModel, which every
	// test builds a model with: the mode is display state the screens read off
	// the session, and threading it through the constructor would make each of
	// those call sites state something they do not care about.
	m.sess.mode = mode
	cs := m.sess.cats
	cs.init()

	p := tea.NewProgram(m)
	cs.send = p.Send

	_, err := p.Run()

	// Give back every note this session still has claimed, before anything else
	// about shutdown. A form releases its own lease on save and on esc, but the
	// exits that bypass a form's key handling — q from the list with a form
	// beneath it, ctrl+c, the program loop failing outright — would otherwise
	// leave a note locked for the rest of models.LockTTL by a process that no
	// longer exists. This is the one place all of those converge.
	//
	// Best-effort by design: a server that has gone away cannot be told, and
	// the TTL is what covers that case. There is nothing useful to report to a
	// user who has already left, so the error is dropped rather than logged
	// over whatever the terminal is showing next.
	_ = st.ReleaseAllNoteLocks()

	cs.close()
	if err != nil {
		return serr.Wrap(err, "TUI terminated abnormally")
	}
	return nil
}

type appModel struct {
	sess  *session
	stack []screen

	statusText string
	statusErr  bool
	statusOK   bool
}

// newAppModel builds the root model. Its signature deliberately did not change
// when the cats integration landed: the state it adds is constructed inert
// here and only detects anything when Run calls init on it, which is what lets
// every pre-existing test keep constructing an app model with no host at all.
func newAppModel(st Store) appModel {
	sess := &session{store: st, cats: newCatsState(), sync: &syncState{}}
	return appModel{
		sess:  sess,
		stack: []screen{newLoginScreen(sess)},
	}
}

func (m appModel) top() screen { return m.stack[len(m.stack)-1] }

func (m appModel) Init() tea.Cmd {
	// RequestBackgroundColor is how a v2 program asks the terminal whether it
	// is light or dark. The reply arrives later as a tea.BackgroundColorMsg,
	// so unlike lipgloss's synchronous HasDarkBackground this costs nothing
	// when the terminal declines to answer — see the long note in styles.go.
	//
	// It is passed uncalled: its signature is func() tea.Msg, which is
	// exactly tea.Cmd. (Its own doc comment shows it invoked, which does not
	// compile.)
	//
	// probeCmd is nil outside a cats pane, which tea.Batch drops — so at Tier
	// 0 this is the same two-command batch it has always been.
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.sess.cats.probeCmd(), m.top().Init()}
	// The mode notice, when the launch did something worth explaining. It is
	// first among equals only in intent: inside cats the capture hint replaces it
	// a moment later, which is why anything that must survive is in Mode.Badge.
	if n := m.sess.mode.Notice; n != "" {
		cmds = append(cmds, status(n))
	}
	return tea.Batch(cmds...)
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// The ⌘ accelerator layer runs before everything else, because its job is
	// to decide what the rest of Update even sees. A claimed chord is REPLACED
	// by its twin keystroke and handled from here on as if the user had typed
	// that; an unclaimed one is dropped here, so it can never reach a focused
	// text widget as a bare letter. Both halves are in metakeys.go.
	if k, ok := msg.(tea.KeyPressMsg); ok {
		if twin, claimed := metaTranslate(k, m.takingText()); claimed {
			if twin == nil {
				return m, nil
			}
			msg = *twin
		}
	}

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.sess.width = msg.Width
		m.sess.height = msg.Height - 1 // reserve the bottom line for the status bar
		// Every screen in the stack gets the resize, not just the top one —
		// otherwise a screen resumed after a pop would render at a stale size.
		var cmds []tea.Cmd
		for i, s := range m.stack {
			updated, cmd := s.Update(msg)
			m.stack[i] = updated
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	// v2 splits key events into KeyPressMsg and KeyReleaseMsg (both satisfy
	// the tea.KeyMsg interface). Matching the press type specifically means
	// that if key-release reporting is ever enabled — Phase 5's kitty
	// keyboard work is the likely trigger — releases won't silently
	// double-fire every binding.
	case tea.KeyPressMsg:
		// ctrl+c always quits, regardless of which screen has focus.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// Any keypress clears stale status feedback so old errors don't linger.
		m.statusText = ""
		m.statusErr = false
		m.statusOK = false

	case tea.BackgroundColorMsg:
		// The terminal answered the Init() query. setPalette reports whether
		// this actually changed anything — the reply can arrive more than once
		// (a repaint, a terminal that re-announces on focus) and rebuilding
		// every widget for an identical palette is visible work for no visible
		// difference.
		//
		// A host theme outranks the terminal's answer, and inside cats the two
		// are the same answer at different resolutions: the OSC 11 reply comes
		// from cats' own emulator reporting the background its theme just gave
		// us. Acting on it would trade a full host palette for the built-in one
		// picked by a single light/dark bit derived from that same background —
		// the theme sync undone by the first repaint.
		if hostOwnsThePalette() {
			return m, nil
		}
		if setPalette(DefaultPalette(msg.IsDark())) {
			return m, paletteChanged()
		}
		return m, nil

	case paletteChangedMsg:
		// Screens that only read the styles in styles.go repaint on their own;
		// this reaches the ones holding derived state. See the restyler doc.
		for _, s := range m.stack {
			if r, ok := s.(restyler); ok {
				r.restyle()
			}
		}
		return m, nil

	// The cats messages. They are handled here, at the root, rather than
	// wherever their effects land, because catsState has exactly one mutator
	// and this is it — see the threading rule at the top of cats_glue.go.
	case catsReadyMsg:
		return m, m.sess.cats.ready(msg)

	case catsEventMsg:
		return m, m.sess.cats.frame(msg.ev)

	case catsPanesMsg:
		m.sess.cats.setPanes(msg)
		return m, nil

	// captureDoneMsg is the one data result that is NOT routed to the active
	// screen. A capture can take seconds and the user is free to navigate while
	// it runs; see appModel.captureDone.
	case captureDoneMsg:
		return m, m.captureDone(msg)

	// summarizeDoneMsg is delivered at the root for the same reason: a model
	// call takes seconds and the user is free to move while it runs. See the
	// rules at the top of summarize.go.
	case summarizeDoneMsg:
		return m, m.summarizeDone(msg)

	case catsLinkMsg:
		// The stream is the only thing that notices a cats which died after the
		// probe said Tier 1. Dropping Control here is what re-gates every
		// Tier-1 feature without tearing down the client the reconnect loop is
		// about to start using again.
		m.sess.cats.caps.Control = msg.up
		return m, nil

	case pushMsg:
		m.stack = append(m.stack, msg.s)
		return m, msg.s.Init()

	case popMsg:
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		if msg.refresh {
			if r, ok := m.top().(refresher); ok {
				return m, r.refresh()
			}
		}
		return m, nil

	case loggedInMsg:
		// Login/registration succeeded: store the user and replace the whole
		// stack with the notes browser (there is no "back" from browse to login).
		m.sess.user = msg.user
		browse := newBrowseScreen(m.sess)
		m.stack = []screen{browse}
		cmds := []tea.Cmd{browse.Init()}
		// Start watching the sync clock now rather than at startup: in HTTP
		// mode the status endpoint requires the token that login just
		// obtained, and before that there is nobody whose notes are overdue.
		if !m.sess.sync.polling {
			m.sess.sync.polling = true
			cmds = append(cmds, syncStatusCmd(m.sess.store), syncTickCmd(m.sess.sync.pollInterval()))
		}
		return m, tea.Batch(cmds...)

	// ---- Sync ------------------------------------------------------------
	//
	// Handled at the root, like the cats messages and for the same reason:
	// syncState has one writer and this is it. A poll answers on a command
	// goroutine while any screen may be rendering.

	case syncStatusMsg:
		// A failed poll leaves the last known status in place rather than
		// blanking it. An unreachable hub is the ordinary state of a laptop
		// away from home, and it does not make what we last knew untrue.
		if msg.err == nil {
			m.sess.sync.status = msg.status
		}
		// Arm the prompt again once the sync is no longer owed. "Asked once"
		// is meant to stop a single overdue period from being raised twice —
		// not to make the dialog a one-time event in a session that stays open
		// for days and goes overdue again.
		if !m.sess.sync.due() {
			m.sess.sync.asked = false
		}
		return m, m.maybeAskToSync()

	case syncTickMsg:
		// The next tick is scheduled from what the LAST poll found, so an
		// installation with no hub settles onto the idle rate after one round
		// rather than asking every minute forever.
		return m, tea.Batch(syncStatusCmd(m.sess.store), syncTickCmd(m.sess.sync.pollInterval()))

	case statusNote:
		m.statusText = msg.text
		m.statusErr = msg.isErr
		m.statusOK = !msg.isErr
		return m, nil
	}

	// Everything else goes to the active screen only.
	updated, cmd := m.top().Update(msg)
	m.stack[len(m.stack)-1] = updated
	return m, cmd
}

// maybeAskToSync raises the sync dialog when the clock says one is owed —
// once per session, and never on top of something the user is in the middle
// of.
//
// The two guards are the difference between a prompt and an interruption:
//
//	asked        the banner keeps saying a sync is due for as long as it is,
//	             but the modal appears once. Anything else turns a two-hour
//	             reminder into a two-hour heckle.
//	stack depth  a dialog thrown over a half-typed note form would steal the
//	             next keystroke. From the list there is nothing to steal.
func (m appModel) maybeAskToSync() tea.Cmd {
	if !m.sess.sync.due() || m.sess.sync.asked {
		return nil
	}
	if len(m.stack) != 1 {
		return nil
	}
	if _, onList := m.top().(*browseScreen); !onList {
		return nil
	}
	m.sess.sync.asked = true
	return push(newSyncScreen(m.sess, syncDue))
}

// View builds the root tea.View. Screens still return plain strings — only
// the root deals in tea.View, because AltScreen (and, later, cursor and
// window-title state) is a whole-program property that the screen stack has
// no business knowing about.
func (m appModel) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true // replaces the v1 tea.WithAltScreen() program option
	// Mouse reporting moved onto the view in v2 for the same reason AltScreen
	// did, and it is just as invisible when missed: without this the terminal is
	// never asked to report clicks, so no mouse message can reach any screen.
	// See mouse.go for the mode choice and what turning it on costs.
	v.MouseMode = mouseMode

	// The window title is a Tier-0 feature: any terminal that understands OSC
	// 2 gets it, and cats is only the host that does the most with it. Set
	// before the size check so a program that has not been measured yet still
	// names itself in the tab bar.
	v.WindowTitle = m.sess.cats.windowTitle()

	if m.sess.width == 0 {
		return v // no size yet; first WindowSizeMsg is on its way
	}

	content := m.top().View()

	// The bottom line, in priority order: what just happened, then what is
	// waiting to be decided, then whatever the status bar was already saying.
	//
	// The sync banner lives here rather than as a line of its own because it
	// has to survive: every keypress clears the status text (see Update), and
	// a reminder that vanishes the moment the user touches an arrow key is not
	// a reminder. Sitting in the fallback branch, it comes back the instant
	// the transient message is gone, for as long as the sync is owed.
	var bar string
	switch {
	case m.statusErr:
		bar = statusErrStyle.Render(truncate(m.statusText, m.sess.width-2))
	case m.statusOK && m.statusText != "":
		bar = statusOKStyle.Render(truncate(m.statusText, m.sess.width-2))
	default:
		if banner := m.sess.sync.banner(); m.statusText == "" && banner != "" {
			bar = syncDueStyle.Render(truncate(banner, m.sess.width-2))
		} else {
			bar = statusBarStyle.Render(truncate(m.statusText, m.sess.width-2))
		}
	}

	v.Content = content + "\n" + bar
	return v
}

// truncate hard-caps a string to n runes so status text can never wrap and
// push the layout off by a line.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
