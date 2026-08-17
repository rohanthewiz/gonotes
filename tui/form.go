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
//
// Subcategories ride in the same field, after a slash: "Work/backend, Personal"
// files the note under Work's backend subcategory and under Personal plainly.
// That notation is not invented here — it is what the Markdown frontmatter and
// gn-clip.sh's -c already take (models.ParseCategorySpecs) — and it is why a
// one-line field is enough for a two-level hierarchy. A subcategory typed here
// for the first time is added to the category's definition too, so it shows up
// as a chip in the web UI and a row on the TUI's subcategory screen.
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

	// baseline is what the form held the last time its contents matched what is
	// stored — at construction, and again after the async category load fills a
	// field the user never touched. Everything since is unsaved work, and esc
	// asks before dropping it. See dirty().
	baseline formValues
}

// formValues is a comparable snapshot of every editable field. A struct rather
// than a bool flag flipped on keypress because the flag would be wrong in the
// common case that matters most: type a character, delete it, and a flag says
// dirty while the note is byte-for-byte what it was. Comparing values means
// only a real difference can raise the dialog, which is what keeps the dialog
// worth reading.
type formValues struct {
	title      string
	desc       string
	tags       string
	categories string
	body       string
	isPrivate  bool
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
		categories: newInput("Work/backend, Personal — created if new", 300),
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
	// Read the baseline back OUT of the widgets rather than off the note that
	// seeded them: a widget is free to normalize what it is given (the textarea
	// does its own line-ending handling), and a baseline that skipped the round
	// trip would report a form as dirty the instant it opened.
	f.baseline = f.values()
	return f
}

// values snapshots the fields as they stand right now.
func (s *formScreen) values() formValues {
	return formValues{
		title:      s.title.Value(),
		desc:       s.desc.Value(),
		tags:       s.tags.Value(),
		categories: s.categories.Value(),
		body:       s.body.Value(),
		isPrivate:  s.isPrivate,
	}
}

// dirty reports whether there is unsaved work in this form.
func (s *formScreen) dirty() bool { return s.values() != s.baseline }

// prefill seeds a NEW note's fields from something other than the user typing —
// today, a capture from a sibling agent pane (capture.go).
//
// It deliberately does not touch focus. The title it is given is generated, so
// the title field is exactly where the user's first keystroke should land, and
// that is already where a fresh form starts. It is also deliberately separate
// from the `editing` path in newFormScreen: this note does not exist yet, so it
// must save as a create (noteID 0) and must not try to load categories for an
// id that was never assigned.
//
// It also deliberately does NOT move the dirty baseline. Captured text is
// unsaved work that exists nowhere else — losing a pane capture to a stray esc
// is precisely the loss the unsaved-changes dialog is for — so a prefilled form
// counts as dirty from the moment it opens, even though the user typed none of
// it.
func (s *formScreen) prefill(title, tags, body string) {
	s.title.SetValue(title)
	s.tags.SetValue(tags)
	s.body.SetValue(body)
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

// takingText is unconditional here: every focus position except the private
// checkbox has a text widget under the cursor, and the checkbox is one tab away
// from one. So a ⌘ chord with no ctrl-shaped twin is swallowed on this screen
// rather than translated — see metakeys.go.
func (s *formScreen) takingText() bool { return true }

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
		// Prefill in the same notation the field accepts, subcategory selections
		// included: a note filed under Work/backend must come back as
		// "Work/backend", or the first save from this form would quietly drop the
		// subcategory the user never touched.
		if msg.err == nil && len(msg.cats) > 0 {
			s.categories.SetValue(models.FormatCategorySpecCSV(noteCatSpecs(msg.cats)))
			// This field just came from storage, so it is not an edit — move the
			// baseline with it. Without this, every edit form would be dirty the
			// moment this message landed, and esc would always stop to ask.
			s.baseline.categories = s.categories.Value()
		}
		return s, nil

	case editorFinishedMsg:
		// Close the host-reported span first, and on BOTH outcomes: an editor
		// that exited with an error is still an editor that is no longer
		// running, and leaving the pane badged "editing" would be worse than
		// never having badged it. openEditorCmd opens the span; see the note
		// there for why the two edges live apart.
		s.sess.cats.idle()
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
			return s, openEditorCmd(s.sess.cats, s.body.Value(), s.title.Value())

		case key.Matches(msg, keys.Back):
			// The guard, and the only place esc is not immediate. A clean form
			// leaves exactly as it always did; a dirty one stops to offer the
			// third answer. See unsavedScreen.
			if s.dirty() {
				return s, push(newUnsavedScreen(s.sess, s.unsavedPrompt(), s.save, s.abandon))
			}
			return s, s.abandon()

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

// abandon leaves the form without saving. It is the plain esc path and also
// the dialog's discard arm, which is why it is a method and not two copies of
// a tea.Sequence: the wording of the departure should not depend on how many
// keys it took to get there.
//
// pop(false) — no refresh. Nothing was written, so there is nothing behind this
// screen that has gone stale.
func (s *formScreen) abandon() tea.Cmd {
	if s.dirty() {
		return tea.Sequence(pop(false), status("Discarded unsaved changes"))
	}
	return tea.Sequence(pop(false), status("Edit canceled"))
}

// unsavedPrompt names the thing at risk. A new note has no title of its own to
// quote yet — and may have no title at all — so it is described rather than
// named.
func (s *formScreen) unsavedPrompt() string {
	if s.editing != nil {
		return "Unsaved changes to \"" + s.editing.Title + "\"."
	}
	if t := strings.TrimSpace(s.title.Value()); t != "" {
		return "\"" + t + "\" has not been saved."
	}
	return "This new note has not been saved."
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

	// Deliberately NOT reported to the cats host as a working span, unlike the
	// external editor. cats turns a working→idle edge into a "finished" toast
	// and a phone push, so a span is a claim that the user might reasonably
	// have walked away during it. A save is one bytdb write, or one POST — it
	// resolves faster than the badge would render, and pushing a notification
	// for it is how a channel earns being muted. The editor, where the user is
	// in another program for as long as they like, is the span that qualifies.
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
