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
func Run(st Store) error {
	// One synchronous config.get inside a cats pane, and nothing at all outside
	// one. See catstheme.go for why it is worth blocking the launch for.
	catsThemeAtStartup()

	m := newAppModel(st)
	cs := m.sess.cats
	cs.init()

	p := tea.NewProgram(m)
	cs.send = p.Send

	_, err := p.Run()
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
	sess := &session{store: st, cats: newCatsState()}
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
	return tea.Batch(tea.RequestBackgroundColor, m.sess.cats.probeCmd(), m.top().Init())
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	// The three cats messages. They are handled here, at the root, rather than
	// wherever their effects land, because catsState has exactly one mutator
	// and this is it — see the threading rule at the top of cats_glue.go.
	case catsReadyMsg:
		return m, m.sess.cats.ready(msg)

	case catsEventMsg:
		return m, m.sess.cats.frame(msg.ev)

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
		return m, browse.Init()

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

// View builds the root tea.View. Screens still return plain strings — only
// the root deals in tea.View, because AltScreen (and, later, cursor and
// window-title state) is a whole-program property that the screen stack has
// no business knowing about.
func (m appModel) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true // replaces the v1 tea.WithAltScreen() program option

	// The window title is a Tier-0 feature: any terminal that understands OSC
	// 2 gets it, and cats is only the host that does the most with it. Set
	// before the size check so a program that has not been measured yet still
	// names itself in the tab bar.
	v.WindowTitle = m.sess.cats.windowTitle()

	if m.sess.width == 0 {
		return v // no size yet; first WindowSizeMsg is on its way
	}

	content := m.top().View()

	var bar string
	switch {
	case m.statusErr:
		bar = statusErrStyle.Render(truncate(m.statusText, m.sess.width-2))
	case m.statusOK && m.statusText != "":
		bar = statusOKStyle.Render(truncate(m.statusText, m.sess.width-2))
	default:
		bar = statusBarStyle.Render(truncate(m.statusText, m.sess.width-2))
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
