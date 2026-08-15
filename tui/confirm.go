package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		// Order matters: enter is bound to Yes and appears in neither No nor
		// any other binding here, but checking Yes first documents that a bare
		// enter confirms rather than cancels.
		case key.Matches(k, keys.Yes):
			return s, tea.Sequence(pop(false), s.onYes)
		case key.Matches(k, keys.No):
			return s, pop(false)
		}
	}
	return s, nil
}

func (s *confirmScreen) View() string {
	// The destructive choice is colored, the safe one is not — the same
	// asymmetry the status bar uses for errors.
	yes := keys.Yes.Help()
	no := keys.No.Help()
	body := s.prompt + "\n\n" +
		errorTextStyle.Bold(true).Render(yes.Key) + helpStyle.Render(" "+yes.Desc+"   ") +
		lipgloss.NewStyle().Bold(true).Render(no.Key) + helpStyle.Render(" "+no.Desc)
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
	ti.SetWidth(32) // v2: width is unexported
	ti.Focus()
	s := &promptScreen{sess: sess, title: title, input: ti, onSubmit: onSubmit}
	s.restyle()
	return s
}

// restyle re-applies the palette to the input. See the note in form.go — the
// widget copied a hardcoded dark style set in at construction.
func (s *promptScreen) restyle() {
	s.input.SetStyles(textinput.DefaultStyles(pal.Dark))
}

func (s *promptScreen) Init() tea.Cmd { return textinput.Blink }

func (s *promptScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(k, keys.Submit):
			value := strings.TrimSpace(s.input.Value())
			if value == "" {
				return s, nil
			}
			return s, tea.Sequence(pop(false), s.onSubmit(value))
		case key.Matches(k, keys.Back):
			return s, pop(false)
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

func (s *promptScreen) View() string {
	body := labelFocusedStyle.Render(s.title) + "\n\n" + s.input.View() + "\n\n" +
		renderHelp(keys.promptHelp()...)
	box := dialogBoxStyle.Render(body)
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, box)
}

var _ screen = (*confirmScreen)(nil)
var _ screen = (*promptScreen)(nil)

// confirmScreen needs no restyle: it holds no widget and renders entirely from
// the package styles, which are read fresh on every frame.
var _ restyler = (*promptScreen)(nil)
