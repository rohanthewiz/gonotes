package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// loginScreen handles both sign-in and first-run registration.
//
// Convenience choices:
//   - Existing usernames are loaded at startup; if there is exactly one user
//     the username field is prefilled and focus jumps straight to password —
//     the common single-user case becomes "type password, hit enter".
//   - If the database has no users at all we switch to register mode
//     automatically, so a fresh install works without visiting the web UI.
type loginScreen struct {
	sess *session

	username textinput.Model
	password textinput.Model
	confirm  textinput.Model // only shown in register mode

	registering bool
	focus       int // index into the visible field list
	busy        bool
	errText     string
	knownUsers  []string
}

func newLoginScreen(sess *session) *loginScreen {
	username := textinput.New()
	username.Placeholder = "username"
	username.CharLimit = 64
	username.Focus()

	password := textinput.New()
	password.Placeholder = "password"
	password.EchoMode = textinput.EchoPassword
	password.CharLimit = 128

	confirm := textinput.New()
	confirm.Placeholder = "confirm password"
	confirm.EchoMode = textinput.EchoPassword
	confirm.CharLimit = 128

	return &loginScreen{
		sess:     sess,
		username: username,
		password: password,
		confirm:  confirm,
	}
}

func (s *loginScreen) Init() tea.Cmd {
	return tea.Batch(loadUsernamesCmd(), textinput.Blink)
}

// fields returns the currently visible inputs in focus order.
func (s *loginScreen) fields() []*textinput.Model {
	if s.registering {
		return []*textinput.Model{&s.username, &s.password, &s.confirm}
	}
	return []*textinput.Model{&s.username, &s.password}
}

func (s *loginScreen) setFocus(idx int) tea.Cmd {
	fields := s.fields()
	s.focus = (idx + len(fields)) % len(fields)
	var cmd tea.Cmd
	for i, f := range fields {
		if i == s.focus {
			cmd = f.Focus()
		} else {
			f.Blur()
		}
	}
	return cmd
}

func (s *loginScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case usernamesLoadedMsg:
		if msg.err != nil {
			s.errText = "Could not read users: " + msg.err.Error()
			return s, nil
		}
		s.knownUsers = msg.names
		if len(msg.names) == 0 {
			// Fresh database — go straight to account creation.
			s.registering = true
			return s, nil
		}
		if len(msg.names) == 1 {
			// Single-user install: prefill and jump to the password field.
			s.username.SetValue(msg.names[0])
			return s, s.setFocus(1)
		}
		return s, nil

	case loginFailedMsg:
		s.busy = false
		s.errText = msg.err.Error()
		return s, nil

	case tea.KeyMsg:
		if s.busy {
			return s, nil // ignore input while a login attempt is in flight
		}
		s.errText = ""

		switch msg.String() {
		case "tab", "down":
			return s, s.setFocus(s.focus + 1)
		case "shift+tab", "up":
			return s, s.setFocus(s.focus - 1)
		case "enter":
			// Enter advances until the last field, then submits — familiar
			// from web login forms.
			if s.focus < len(s.fields())-1 {
				return s, s.setFocus(s.focus + 1)
			}
			return s, s.submit()
		case "esc":
			// No screen below us to pop to; esc here means "get me out".
			// ("q" must NOT quit here — it's a legitimate character in
			// usernames and passwords.)
			return s, tea.Quit
		}
	}

	// Route everything else (typing) to the focused input.
	fields := s.fields()
	var cmd tea.Cmd
	*fields[s.focus], cmd = fields[s.focus].Update(msg)
	return s, cmd
}

func (s *loginScreen) submit() tea.Cmd {
	username := strings.TrimSpace(s.username.Value())
	password := s.password.Value()

	if username == "" || password == "" {
		s.errText = "Username and password are required"
		return nil
	}
	if s.registering {
		if password != s.confirm.Value() {
			s.errText = "Passwords do not match"
			return nil
		}
		s.busy = true
		return registerCmd(username, password)
	}
	s.busy = true
	return loginCmd(username, password)
}

func (s *loginScreen) View() string {
	var b strings.Builder

	heading := "GoNotes — Sign in"
	if s.registering {
		heading = "GoNotes — Create your account"
	}
	b.WriteString(appTitleStyle.Render(heading))
	b.WriteString("\n\n")

	if s.registering && len(s.knownUsers) == 0 {
		b.WriteString(dimStyle.Render("No users found — let's create the first account."))
		b.WriteString("\n\n")
	}

	labels := []string{"Username", "Password", "Confirm"}
	for i, f := range s.fields() {
		label := labels[i]
		if i == s.focus {
			b.WriteString(labelFocusedStyle.Render(label))
		} else {
			b.WriteString(labelStyle.Render(label))
		}
		b.WriteString("\n")
		b.WriteString(f.View())
		b.WriteString("\n\n")
	}

	if s.busy {
		b.WriteString(dimStyle.Render("Signing in..."))
		b.WriteString("\n")
	}
	if s.errText != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDanger).Render(s.errText))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("enter submit • tab next field • ctrl+c quit"))

	// Center the login card in the terminal for a polished first impression.
	card := dialogBoxStyle.Width(48).Render(b.String())
	if s.sess.width > 0 {
		return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, card)
	}
	return card
}

// Compile-time interface check keeps refactors honest.
var _ screen = (*loginScreen)(nil)
