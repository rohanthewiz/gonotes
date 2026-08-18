package tui

import (
	"strings"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// duplicateScreen is the modal that stands between "D" and a copy of a note.
//
// WHY A DIALOG AND NOT A KEYSTROKE. The obvious duplicate — clone the fields,
// prefix the title — silently drops the one thing that is not a field: the
// note's categories and the subcategories it selected within them, which live
// in a junction table. Both possible defaults are wrong often enough to be
// annoying (a copy that loses its filing, or one that lands in a category the
// user was copying the note OUT of), so the answer is asked rather than
// assumed. Everything offered starts checked, so the common case is still two
// keys: D, enter.
//
//	D on the list / detail
//	        │
//	┌───────▼────────┐   loads the note's categories asynchronously
//	│ duplicate?     │   ↑/↓ move · space include · enter go · esc cancel
//	└───────┬────────┘
//	        └─ pops FIRST, then creates — so noteDuplicatedMsg lands on the
//	           screen that asked, exactly as confirmScreen does.
//
// The title is a live text field rather than another checkbox: "COPY <title>"
// is the default answer, not the only one, and the moment a user wants to name
// the copy something else is the moment they are looking at this dialog.
type duplicateScreen struct {
	sess *session

	// src is the note being copied, by value: the copy is built from what the
	// user was looking at, and a reload underneath must not change it.
	src models.Note

	title textinput.Model

	// opts are the copy switches in display order. The category row is always
	// opts[0] even before its data arrives — see the note in newDuplicateScreen
	// about why the row set never changes shape after construction.
	opts  []dupOption
	focus int // 0 is the title field; n is opts[n-1]

	// cats is the source note's category assignments, loaded asynchronously.
	// catsState is what the category row reports and what confirm waits on.
	cats      []models.NoteCategoryDetailOutput
	catsState dupCatsState
}

// dupCatsState is where the asynchronous category load has got to. It is a
// state rather than a bool pair because "not yet" and "failed" have to read
// differently to the user, and because confirm treats them differently: one is
// worth waiting a moment for, the other never resolves.
type dupCatsState int

const (
	dupCatsLoading dupCatsState = iota
	dupCatsReady
	dupCatsFailed
)

// dupOption is one line of the "also copy" list.
type dupOption struct {
	label string
	// detail is the dim second line under the label — what exactly this option
	// would carry over. Only the rows where that is not obvious use it.
	detail string
	on     bool
	// off makes the row un-toggleable: there is nothing behind it to copy
	// (no categories on the note, or the load that would have told us failed).
	// Shown rather than hidden so the row set — and therefore every focus
	// index — is fixed at construction and cannot shift under the cursor when
	// the async load lands.
	off bool
}

// The fixed identities of the rows this dialog can show. The category row is
// index 0 always; the rest are appended only when the source note has that
// field, so a note with no tags gets no tags row rather than a dead one — which
// is why the others are found by label rather than by position.
const dupOptCategories = 0

const (
	dupLabelCategories  = "Categories & subcategories"
	dupLabelBody        = "Body"
	dupLabelDescription = "Description"
	dupLabelTags        = "Tags"
	dupLabelPrivate     = "Private"
	dupLabelFlagged     = "Follow-up flag"
)

// dupTitlePrefix is what marks a copy. It LEADS the title rather than trailing
// it (the web UI's old " (Copy)" suffix) because the note list truncates from
// the right: a suffix is the first thing to disappear on the narrow layout,
// which is exactly the layout where two identical-looking rows are hardest to
// tell apart.
const dupTitlePrefix = "COPY "

func newDuplicateScreen(sess *session, src models.Note) *duplicateScreen {
	ti := textinput.New()
	ti.CharLimit = 200
	ti.SetWidth(48) // v2: width is unexported
	ti.SetValue(dupTitlePrefix + src.Title)
	ti.Focus()
	// Caret at the end, not a selection: the prefix is the default answer, so
	// the next keystroke should extend the title rather than replace it.
	ti.CursorEnd()

	s := &duplicateScreen{sess: sess, src: src, title: ti}

	// The category row always exists; only its detail line changes as the load
	// resolves. It starts off, so a confirm that somehow beat the load cannot
	// copy categories the dialog never showed.
	s.opts = []dupOption{{
		label:  dupLabelCategories,
		detail: "loading…",
		off:    true,
	}}
	if src.Body.String != "" {
		s.opts = append(s.opts, dupOption{label: dupLabelBody, on: true})
	}
	if src.Description.String != "" {
		s.opts = append(s.opts, dupOption{
			label: dupLabelDescription, detail: firstLine(src.Description.String), on: true})
	}
	if src.Tags.String != "" {
		s.opts = append(s.opts, dupOption{
			label: dupLabelTags, detail: "#" + src.Tags.String, on: true})
	}
	if src.IsPrivate {
		s.opts = append(s.opts, dupOption{label: dupLabelPrivate, on: true})
	}
	if src.IsFlagged {
		s.opts = append(s.opts, dupOption{label: dupLabelFlagged, on: true})
	}

	s.restyle()
	return s
}

// restyle re-applies the palette to the input, for the same reason every other
// screen holding a bubbles widget does: the widget copied a hardcoded dark
// style set in at construction. See the note in form.go.
func (s *duplicateScreen) restyle() {
	s.title.SetStyles(textinput.DefaultStyles(pal.Dark))
}

// takingText is true only while the title field has focus. On the option rows
// the keys are commands (space includes, enter goes), so an unclaimed ⌘ chord
// there should be swallowed rather than typed. See metakeys.go.
func (s *duplicateScreen) takingText() bool { return s.focus == 0 }

func (s *duplicateScreen) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		loadNoteCategoriesCmd(s.sess.store, s.src.ID, s.sess.user.GUID),
	)
}

func (s *duplicateScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case noteCatsLoadedMsg:
		row := &s.opts[dupOptCategories]
		switch {
		case msg.err != nil:
			// Not a status-bar error: the dialog is what the user is reading,
			// and a row that says what it could not do is the same message
			// delivered where the decision is being made.
			s.catsState = dupCatsFailed
			row.detail, row.on, row.off = "could not be loaded — the copy will have none", false, true
		case len(msg.cats) == 0:
			s.catsState = dupCatsReady
			s.cats = nil
			row.detail, row.on, row.off = "(this note is in no categories)", false, true
		default:
			s.catsState = dupCatsReady
			s.cats = msg.cats
			row.detail, row.on, row.off = strings.Join(noteCatSpecs(msg.cats), ", "), true, false
		}
		return s, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Back):
			return s, pop(false)

		case key.Matches(msg, keys.Submit):
			return s, s.confirm()

		case key.Matches(msg, keys.FieldDown, keys.NextField):
			s.moveFocus(1)
			return s, nil

		case key.Matches(msg, keys.FieldUp, keys.PrevField):
			s.moveFocus(-1)
			return s, nil

		// Only claim the space bar off the title field — everywhere else it is
		// a character the user is typing. The form's private checkbox draws the
		// same line for the same reason.
		case key.Matches(msg, keys.Include) && s.focus > 0:
			row := &s.opts[s.focus-1]
			if !row.off {
				row.on = !row.on
			}
			return s, nil
		}
	}

	// Anything unclaimed belongs to the title field, and only while it has
	// focus: a keystroke aimed at an option row must not edit the title behind
	// it.
	if s.focus == 0 {
		var cmd tea.Cmd
		s.title, cmd = s.title.Update(msg)
		return s, cmd
	}
	return s, nil
}

// moveFocus walks the title field and the option rows as one list, wrapping at
// both ends, and keeps the text input's focus (and therefore its cursor) in
// step — a blinking cursor on a field the keys no longer reach is a lie about
// where input is going.
func (s *duplicateScreen) moveFocus(delta int) {
	n := len(s.opts) + 1
	s.focus = (s.focus + delta + n) % n
	if s.focus == 0 {
		s.title.Focus()
	} else {
		s.title.Blur()
	}
}

// dupPlan is the copy this dialog would create: the note itself, plus the
// category links to reproduce on it.
type dupPlan struct {
	input models.NoteInput
	cats  []models.NoteCategoryDetailOutput
}

// plan reads the dialog's state into that copy, or returns the one-line reason
// it cannot yet. It is split out of confirm so the decision — which of the
// source note's parts survive which combination of toggles — can be asserted
// directly, without driving a command through the event loop to find out.
func (s *duplicateScreen) plan() (dupPlan, string) {
	title := strings.TrimSpace(s.title.Value())
	if title == "" {
		return dupPlan{}, "Title is required"
	}
	// Waiting is the honest answer while the load is in flight: confirming now
	// would produce a copy with no categories and no sign that anything was
	// dropped. A failed load is different — it will not resolve, and the row
	// already says the copy will have none.
	if s.catsState == dupCatsLoading {
		return dupPlan{}, "Still loading this note's categories…"
	}

	on := func(idx int) bool { return idx >= 0 && idx < len(s.opts) && s.opts[idx].on }
	// Rows are looked up by label because the set is built conditionally: a note
	// with no tags has no tags row, so a fixed index would name a different
	// option on every note. dupOptCategories is the one exception — that row is
	// always present, and always first.
	find := func(label string) int {
		for i, o := range s.opts {
			if o.label == label {
				return i
			}
		}
		return -1
	}

	// Empty optional fields become nil so the copy stores NULL rather than an
	// empty string — the same rule the form's save applies.
	strPtrOrNil := func(v string) *string {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		return &v
	}

	p := dupPlan{input: models.NoteInput{Title: title}}
	if on(find(dupLabelBody)) {
		p.input.Body = strPtrOrNil(s.src.Body.String)
	}
	if on(find(dupLabelDescription)) {
		p.input.Description = strPtrOrNil(s.src.Description.String)
	}
	if on(find(dupLabelTags)) {
		p.input.Tags = strPtrOrNil(s.src.Tags.String)
	}
	p.input.IsPrivate = on(find(dupLabelPrivate))
	p.input.IsFlagged = on(find(dupLabelFlagged))

	if on(dupOptCategories) {
		p.cats = s.cats
	}
	return p, ""
}

// confirm hands the planned copy off to be created.
//
// It pops BEFORE creating, exactly as confirmScreen does: noteDuplicatedMsg has
// to land on the screen that opened this dialog, which is only the top of the
// stack once this one is off it.
func (s *duplicateScreen) confirm() tea.Cmd {
	p, problem := s.plan()
	if problem != "" {
		return status(problem)
	}
	return tea.Sequence(pop(false),
		duplicateNoteCmd(s.sess.store, p.input, p.cats, s.sess.user.GUID))
}

func (s *duplicateScreen) View() string {
	var b strings.Builder

	b.WriteString(labelFocusedStyle.Render("Duplicate") + " " +
		dimStyle.Render(truncate(s.src.Title, 48)) + "\n\n")

	titleLabel := labelStyle
	if s.focus == 0 {
		titleLabel = labelFocusedStyle
	}
	b.WriteString(titleLabel.Render("Title") + "  " + s.title.View() + "\n")

	if len(s.opts) > 0 {
		b.WriteString("\n" + dimStyle.Render("Also copy from the original:") + "\n")
	}
	for i, o := range s.opts {
		focused := s.focus == i+1
		check := "[ ]"
		if o.on {
			check = "[x]"
		}
		cursor := "  "
		if focused {
			cursor = "› "
		}
		label := labelStyle
		if focused {
			label = labelFocusedStyle
		}
		if o.off {
			// A row with nothing behind it reads as dim whether or not the
			// cursor is on it, so the cursor cannot look like a live choice.
			label = dimStyle
			check = dimStyle.Render(check)
		}
		b.WriteString(cursor + check + " " + label.Render(o.label) + "\n")
		if o.detail != "" {
			b.WriteString("      " + dimStyle.Render(truncate(o.detail, 52)) + "\n")
		}
	}

	b.WriteString("\n" + renderHelp(keys.duplicateHelp()...))

	box := dialogBoxStyle.Render(b.String())
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, box)
}

var _ screen = (*duplicateScreen)(nil)
var _ restyler = (*duplicateScreen)(nil)
var _ texter = (*duplicateScreen)(nil)

// duplicateErrContext phrases a failed duplicate for the status bar. The two
// failures are not the same news: one leaves nothing behind, the other leaves a
// real note whose filing is incomplete, and a user told "duplicate failed" about
// the second would go looking for a note that is already in their list.
func duplicateErrContext(created *models.Note) string {
	if created != nil {
		return "The copy was created, but its categories did not all follow"
	}
	return "Duplicate failed"
}
