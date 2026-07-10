package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmScreen is a modal yes/no dialog pushed on top of whatever screen
// needs a confirmation (destructive actions only — deletes).
//
// Design choice: on "yes" we pop FIRST and then run the action, so the
// action's result message (e.g. noteDeletedMsg) is delivered to the screen
// that asked for confirmation. That screen already knows how to handle the
// outcome; the dialog stays completely generic.
type confirmScreen struct {
	sess   *session
	prompt string
	onYes  tea.Cmd
}

func newConfirmScreen(sess *session, prompt string, onYes tea.Cmd) *confirmScreen {
	return &confirmScreen{sess: sess, prompt: prompt, onYes: onYes}
}

func (s *confirmScreen) Init() tea.Cmd { return nil }

func (s *confirmScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			return s, tea.Sequence(pop(false), s.onYes)
		case "n", "N", "esc", "q":
			return s, pop(false)
		}
	}
	return s, nil
}

func (s *confirmScreen) View() string {
	body := s.prompt + "\n\n" +
		lipgloss.NewStyle().Foreground(colorDanger).Bold(true).Render("y") + helpStyle.Render(" / enter confirm   ") +
		lipgloss.NewStyle().Bold(true).Render("n") + helpStyle.Render(" / esc cancel")
	box := dialogBoxStyle.Render(body)
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, box)
}

// promptScreen is a one-line modal text input (e.g. "New category name").
// Like confirmScreen, it pops before running onSubmit so the result message
// lands on the requesting screen.
type promptScreen struct {
	sess     *session
	title    string
	input    textinput.Model
	onSubmit func(value string) tea.Cmd
}

func newPromptScreen(sess *session, title string, onSubmit func(string) tea.Cmd) *promptScreen {
	ti := textinput.New()
	ti.CharLimit = 120
	ti.Width = 32
	ti.Focus()
	return &promptScreen{sess: sess, title: title, input: ti, onSubmit: onSubmit}
}

func (s *promptScreen) Init() tea.Cmd { return textinput.Blink }

func (s *promptScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			value := strings.TrimSpace(s.input.Value())
			if value == "" {
				return s, nil
			}
			return s, tea.Sequence(pop(false), s.onSubmit(value))
		case "esc":
			return s, pop(false)
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

func (s *promptScreen) View() string {
	body := labelFocusedStyle.Render(s.title) + "\n\n" + s.input.View() + "\n\n" +
		helpStyle.Render("enter confirm • esc cancel")
	box := dialogBoxStyle.Render(body)
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, box)
}

var _ screen = (*confirmScreen)(nil)
var _ screen = (*promptScreen)(nil)
