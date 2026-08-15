package tui

import (
	"strings"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// formScreen creates a new note or edits an existing one.
//
// Field order: Title → Description → Tags → Categories → Private → Body.
// tab/shift+tab cycle focus, ctrl+s saves from anywhere, esc cancels.
//
// The body can be edited two ways:
//   - inline, in the textarea (fine for quick tweaks), or
//   - in $VISUAL/$EDITOR via ctrl+e (tea.ExecProcess suspends the TUI and
//     restores it when the editor exits) — the comfortable path for long
//     markdown notes, and the main convenience win over the web form.
//
// Categories are a comma-separated field rather than a picker: typing a name
// that doesn't exist yet auto-creates the category on save, which makes
// organizing notes a zero-ceremony affair (see syncNoteCategories).
type formScreen struct {
	sess *session

	// editing is nil when creating a new note; otherwise the note being edited.
	editing *models.Note

	title      textinput.Model
	desc       textinput.Model
	tags       textinput.Model
	categories textinput.Model
	body       textarea.Model
	isPrivate  bool

	focus int // 0 title, 1 desc, 2 tags, 3 categories, 4 private toggle, 5 body
	busy  bool
}

const (
	focusTitle = iota
	focusDesc
	focusTags
	focusCats
	focusPrivate
	focusBody
	focusCount
)

func newFormScreen(sess *session, editing *models.Note) *formScreen {
	// bubbles v2 builds each widget's default styles from a hardcoded dark
	// set (its own source calls that out as temporary). v1's AdaptiveColor
	// used to make this automatic, so every constructor now has to re-apply
	// the palette's light/dark answer or a light terminal renders grey text
	// on grey.
	newInput := func(placeholder string, limit int) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = limit
		return ti
	}

	f := &formScreen{
		sess:       sess,
		editing:    editing,
		title:      newInput("note title (required)", 200),
		desc:       newInput("short description", 300),
		tags:       newInput("comma,separated,tags", 300),
		categories: newInput("comma, separated, categories (created if new)", 300),
		body:       textarea.New(),
	}
	f.restyle()
	f.body.Placeholder = "markdown body — ctrl+e opens $EDITOR"
	f.body.CharLimit = 0 // unlimited; notes can be long

	if editing != nil {
		f.title.SetValue(editing.Title)
		f.desc.SetValue(editing.Description.String)
		f.tags.SetValue(editing.Tags.String)
		f.body.SetValue(editing.Body.String)
		f.isPrivate = editing.IsPrivate
	}

	f.title.Focus()
	return f
}

func (s *formScreen) Init() tea.Cmd {
	s.layout()
	cmds := []tea.Cmd{textinput.Blink}
	if s.editing != nil {
		// Prefill the categories field with the note's current assignments.
		cmds = append(cmds, loadNoteCategoriesCmd(s.sess.store, s.editing.ID, s.sess.user.GUID))
	}
	return tea.Batch(cmds...)
}

// restyle applies the palette's current light/dark answer to the five widgets.
// bubbles v2 defaults every widget to a hardcoded dark style set (lipgloss v2
// removed AdaptiveColor, so the widget cannot ask the terminal itself) and
// copies that set in at construction — so this runs once at construction and
// again whenever the palette changes.
func (s *formScreen) restyle() {
	st := textinput.DefaultStyles(pal.Dark)
	for _, ti := range s.inputs() {
		ti.SetStyles(st)
	}
	s.body.SetStyles(textarea.DefaultStyles(pal.Dark))
}

// inputs returns the four single-line fields in focus order. Having one place
// that enumerates them keeps restyle and layout from drifting apart as fields
// are added.
func (s *formScreen) inputs() []*textinput.Model {
	return []*textinput.Model{&s.title, &s.desc, &s.tags, &s.categories}
}

// layout sizes the inputs to the terminal. The textarea takes all vertical
// space left after the chrome, so bigger terminals give more body room
// automatically.
//
// The chrome height is *measured*, not counted. It used to be `height - 14`,
// derived from a comment tallying "heading(2) + 4 labeled inputs(4*2=8) +
// private(1) + body label(1) + help(2) ≈ 14" — a constant that was wrong the
// moment any of those lines wrapped, and that nothing would have flagged if a
// field were added. Rendering the chrome and asking lipgloss how tall it came
// out is both exact and self-maintaining, and it is cheap: the strings are
// built once per resize, not per frame.
func (s *formScreen) layout() {
	w := s.sess.width - 4
	if w < 20 {
		w = 20
	}
	// v2 made the input width unexported; SetWidth replaces `ti.Width = w`.
	for _, ti := range s.inputs() {
		ti.SetWidth(w)
	}
	s.body.SetWidth(w)

	above, below := s.chrome()
	bodyHeight := s.sess.height - lipgloss.Height(above) - lipgloss.Height(below)
	// A floor rather than a hard requirement: on a terminal too short to hold
	// the chrome the form scrolls off the bottom, which is survivable and
	// recoverable by resizing. A zero-height textarea is neither — it renders
	// nothing and swallows every keystroke typed into it.
	if bodyHeight < minBodyHeight {
		bodyHeight = minBodyHeight
	}
	s.body.SetHeight(bodyHeight)
}

// minBodyHeight is the smallest textarea that is still usable: one line of
// text plus room to see a line above and below it while scrolling.
const minBodyHeight = 3

func (s *formScreen) setFocus(idx int) tea.Cmd {
	s.focus = (idx + focusCount) % focusCount

	s.title.Blur()
	s.desc.Blur()
	s.tags.Blur()
	s.categories.Blur()
	s.body.Blur()

	switch s.focus {
	case focusTitle:
		return s.title.Focus()
	case focusDesc:
		return s.desc.Focus()
	case focusTags:
		return s.tags.Focus()
	case focusCats:
		return s.categories.Focus()
	case focusBody:
		return s.body.Focus()
	}
	return nil // private toggle has no input to focus
}

func (s *formScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.layout()
		return s, nil

	case noteCatsLoadedMsg:
		if msg.err == nil && len(msg.cats) > 0 {
			names := make([]string, len(msg.cats))
			for i, c := range msg.cats {
				names[i] = c.Name
			}
			s.categories.SetValue(strings.Join(names, ", "))
		}
		return s, nil

	case editorFinishedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "External editor")
		}
		s.body.SetValue(strings.TrimRight(msg.body, "\n"))
		return s, status("Body updated from editor — ctrl+s to save")

	case noteSavedMsg:
		s.busy = false
		if msg.err != nil {
			return s, statusErr(msg.err, "Save failed")
		}
		return s, tea.Sequence(pop(true), status("Saved \""+msg.note.Title+"\""))

	case tea.KeyPressMsg:
		if s.busy {
			return s, nil
		}

		switch {
		case key.Matches(msg, keys.Save):
			return s, s.save()

		case key.Matches(msg, keys.Editor):
			return s, openEditorCmd(s.body.Value())

		case key.Matches(msg, keys.Back):
			return s, tea.Sequence(pop(false), status("Edit canceled"))

		case key.Matches(msg, keys.NextField):
			return s, s.setFocus(s.focus + 1)

		case key.Matches(msg, keys.PrevField):
			return s, s.setFocus(s.focus - 1)

		case key.Matches(msg, keys.Submit):
			// Enter advances through single-line fields (web-form muscle
			// memory), toggles the checkbox, and inside the body falls
			// through to insert a newline.
			if s.focus < focusPrivate {
				return s, s.setFocus(s.focus + 1)
			}
			if s.focus == focusPrivate {
				s.isPrivate = !s.isPrivate
				return s, nil
			}

		case key.Matches(msg, keys.TogglePrivate):
			// Only claim the space bar on the checkbox row; everywhere else it
			// has to reach the focused input as a literal character.
			if s.focus == focusPrivate {
				s.isPrivate = !s.isPrivate
				return s, nil
			}
		}
	}

	// Route remaining messages (typing, paste, cursor blink) to the focused widget.
	var cmd tea.Cmd
	switch s.focus {
	case focusTitle:
		s.title, cmd = s.title.Update(msg)
	case focusDesc:
		s.desc, cmd = s.desc.Update(msg)
	case focusTags:
		s.tags, cmd = s.tags.Update(msg)
	case focusCats:
		s.categories, cmd = s.categories.Update(msg)
	case focusBody:
		s.body, cmd = s.body.Update(msg)
	}
	return s, cmd
}

func (s *formScreen) save() tea.Cmd {
	title := strings.TrimSpace(s.title.Value())
	if title == "" {
		return status("Title is required")
	}

	// Optional fields become nil pointers when empty so the DB stores NULL
	// rather than empty strings.
	strPtr := func(v string) *string {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		return &v
	}

	body := s.body.Value()
	input := models.NoteInput{
		Title:       title,
		Description: strPtr(s.desc.Value()),
		Body:        strPtr(body),
		Tags:        strPtr(s.tags.Value()),
		IsPrivate:   s.isPrivate,
	}

	var noteID int64
	if s.editing != nil {
		noteID = s.editing.ID
		// UpdateNote overwrites all fields, so carry the flag through
		// unchanged — the form intentionally has no flag control (flagging
		// is a one-key action on the list/detail screens).
		input.IsFlagged = s.editing.IsFlagged
	}

	s.busy = true
	return saveNoteCmd(s.sess.store, noteID, input, s.categories.Value(), s.sess.user.GUID)
}

// chrome renders everything drawn around the body textarea: above is the
// heading and the labeled fields, below is the help/busy line.
//
// Split out of View so layout() can measure it. Neither half carries a
// trailing newline — View joins with them — so lipgloss.Height on each returns
// exactly the lines it will occupy, and the two heights plus the textarea's
// add up to the whole screen.
func (s *formScreen) chrome() (above, below string) {
	var b strings.Builder

	heading := "New note"
	if s.editing != nil {
		heading = "Edit: " + s.editing.Title
	}
	b.WriteString(appTitleStyle.Render(heading))
	b.WriteString("\n\n")

	writeField := func(idx int, label string, view string) {
		style := labelStyle
		if s.focus == idx {
			style = labelFocusedStyle
		}
		b.WriteString(style.Render(label) + " " + view + "\n")
	}

	writeField(focusTitle, "Title      ", s.title.View())
	writeField(focusDesc, "Description", s.desc.View())
	writeField(focusTags, "Tags       ", s.tags.View())
	writeField(focusCats, "Categories ", s.categories.View())

	check := "[ ]"
	if s.isPrivate {
		check = "[x]"
	}
	privLabel := labelStyle
	if s.focus == focusPrivate {
		privLabel = labelFocusedStyle
	}
	b.WriteString(privLabel.Render("Private    ") + " " + check +
		dimStyle.Render("  (encrypted at rest when a key is configured)") + "\n")

	bodyLabel := labelStyle
	if s.focus == focusBody {
		bodyLabel = labelFocusedStyle
	}
	b.WriteString(bodyLabel.Render("Body"))

	if s.busy {
		below = dimStyle.Render("Saving...")
	} else {
		below = renderHelp(keys.formHelp()...)
	}
	return b.String(), below
}

func (s *formScreen) View() string {
	above, below := s.chrome()
	return above + "\n" + s.body.View() + "\n" + below
}

var _ screen = (*formScreen)(nil)
var _ restyler = (*formScreen)(nil)
