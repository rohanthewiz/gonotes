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

// restyler is optionally implemented by screens holding bubbles widgets,
// which copy their style set in at construction and therefore cannot see a
// later palette change. The root calls it on every screen in the stack when
// tea.BackgroundColorMsg reports a background different from the assumed
// dark default.
//
// Only loginScreen implements it here: it is the one screen that exists
// before the terminal can possibly have answered. Phase 3 makes the rest of
// the stack conform, at which point a mid-session theme switch is fully
// supported.
type restyler interface {
	restyle()
}

// session carries state shared by every screen. A pointer to it is embedded
// in each screen, so window resizes and the authenticated user are always
// current without message plumbing.
type session struct {
	user   *models.User
	width  int
	height int // content height available to screens (status bar excluded)
}

// Run starts the TUI and blocks until the user quits. The database must be
// initialized before calling this.
//
// tea.WithAltScreen() is gone in v2: the alternate screen is now a property
// of the view the model returns, not of the program, so it is set in View()
// below instead of here.
func Run() error {
	p := tea.NewProgram(newAppModel())
	if _, err := p.Run(); err != nil {
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

func newAppModel() appModel {
	sess := &session{}
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
	return tea.Batch(tea.RequestBackgroundColor, m.top().Init())
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
		// The terminal answered the Init() query. Rebuilding the palette is
		// enough for everything the screens render themselves, because those
		// styles are read fresh on every View(). Widgets are the exception:
		// bubbles copies its style set into each Model at construction, so
		// any screen already on the stack needs telling.
		if msg.IsDark() != isDark {
			setPalette(msg.IsDark())
			for _, s := range m.stack {
				if r, ok := s.(restyler); ok {
					r.restyle()
				}
			}
		}
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
