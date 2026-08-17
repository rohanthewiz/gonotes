package tui

import (
	"slices"
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

// unsavedScreen is the modal that stands between an edit in progress and the
// key that would throw it away. Its three answers are the reason it is not a
// confirmScreen: "are you sure?" has no place to put the outcome the user
// actually wants, which is to keep the work AND leave.
//
//	   esc on a dirty form
//	           │
//	     ┌─────▼─────┐
//	     │ unsaved?  │
//	     └──┬──┬──┬──┘
//	save ───┘  │  └─── esc: pop this dialog only, form resumes untouched
//	           └────── discard: pop dialog, then pop form
//
// Both actions are funcs rather than the plain tea.Cmds confirmScreen takes,
// because save has to be *decided* at press time: formScreen.save validates the
// title, sets the busy flag and reads the fields as they stand now, none of
// which can be baked in when the dialog is constructed.
type unsavedScreen struct {
	sess    *session
	prompt  string
	onSave  func() tea.Cmd
	discard func() tea.Cmd
}

func newUnsavedScreen(sess *session, prompt string, onSave, discard func() tea.Cmd) *unsavedScreen {
	return &unsavedScreen{sess: sess, prompt: prompt, onSave: onSave, discard: discard}
}

func (s *unsavedScreen) Init() tea.Cmd { return nil }

// takingText is deliberately NOT implemented, exactly as on confirmScreen: the
// answers here are single-letter commands, so a ⌘ chord with no twin should be
// swallowed rather than typed. See metakeys.go.

func (s *unsavedScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		// The dialog pops itself first in both action arms, for the same reason
		// confirmScreen does: the command's eventual result message
		// (noteSavedMsg, and the pop it carries) has to land on the form, which
		// is only the top of the stack once this screen is off it.
		case key.Matches(k, keys.SaveExit):
			return s, tea.Sequence(pop(false), s.onSave())
		case key.Matches(k, keys.DiscardExit):
			return s, tea.Sequence(pop(false), s.discard())
		case key.Matches(k, keys.CancelExit):
			return s, pop(false)
		}
	}
	return s, nil
}

func (s *unsavedScreen) View() string {
	// The three answers are read from the help set rather than written out, so
	// this row and the switch above cannot list different keys — the same
	// guarantee every footer gets from renderHelp, which this cannot use because
	// one of the rows is colored.
	//
	// Discard takes the error color the delete dialog gives its "yes": the same
	// asymmetry, pointed at whichever answer is the one that loses something.
	var row strings.Builder
	for i, b := range keys.unsavedHelp() {
		if i > 0 {
			row.WriteString(helpStyle.Render("   "))
		}
		h := b.Help()
		keyStyle := lipgloss.NewStyle().Bold(true)
		// key.Binding holds a slice, so it is not comparable with ==; its key
		// set is the identity that matters anyway, since that is what dispatch
		// compares on.
		if slices.Equal(b.Keys(), keys.DiscardExit.Keys()) {
			keyStyle = errorTextStyle.Bold(true)
		}
		row.WriteString(keyStyle.Render(h.Key) + helpStyle.Render(" "+h.Desc))
	}
	box := dialogBoxStyle.Render(s.prompt + "\n\n" + row.String())
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

// takingText: this screen is one focused text input and nothing else. Its
// sibling confirmScreen deliberately does not implement texter — y/n is a
// command, not dictation. See metakeys.go.
func (s *promptScreen) takingText() bool { return true }

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
var _ screen = (*unsavedScreen)(nil)
var _ screen = (*promptScreen)(nil)

// confirmScreen and unsavedScreen need no restyle: they hold no widget and
// render entirely from the package styles, which are read fresh on every frame.
var _ restyler = (*promptScreen)(nil)
