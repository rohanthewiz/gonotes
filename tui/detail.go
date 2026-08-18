package tui

import (
	"strings"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// detailScreen shows a single note full-screen: a sticky metadata header and
// the body rendered as markdown (via glamour) inside a scrollable viewport.
//
// Rendering markdown instead of raw text was chosen because GoNotes bodies
// are markdown by convention (the export/import commands are Obsidian-
// compatible), so headings, code fences and lists display properly styled.
type detailScreen struct {
	sess *session
	note models.Note
	// cats carries each category with this note's subcategory selection, so the
	// header can say "Work/backend" rather than the less true "Work".
	cats []models.NoteCategoryDetailOutput

	vp    viewport.Model
	ready bool // viewport must not render before it has real dimensions
}

func newDetailScreen(sess *session, note models.Note) *detailScreen {
	return &detailScreen{sess: sess, note: note}
}

func (s *detailScreen) Init() tea.Cmd {
	s.layout()
	return loadNoteCategoriesCmd(s.sess.store, s.note.ID, s.sess.user.GUID)
}

// refresh re-fetches the note after an edit made on the form screen above us.
func (s *detailScreen) refresh() tea.Cmd {
	return tea.Batch(
		loadNoteCmd(s.sess.store, s.note.ID, s.sess.user.GUID),
		loadNoteCategoriesCmd(s.sess.store, s.note.ID, s.sess.user.GUID),
	)
}

// headerView builds the sticky metadata block. Kept outside the viewport so
// the title and tags stay visible while scrolling a long body.
func (s *detailScreen) headerView() string {
	var b strings.Builder

	var badges string
	if s.note.IsFlagged {
		badges += flaggedStyle.Render(" ⚑")
	}
	if s.note.IsPrivate {
		badges += privateStyle.Render(" 🔒")
	}
	b.WriteString(appTitleStyle.Render(s.note.Title) + badges)
	b.WriteString("\n")

	var meta []string
	meta = append(meta, "updated "+s.note.UpdatedAt.Format("2006-01-02 15:04"))
	if tags := s.note.Tags.String; tags != "" {
		meta = append(meta, "#"+tags)
	}
	if len(s.cats) > 0 {
		meta = append(meta, "in "+strings.Join(noteCatSpecs(s.cats), ", "))
	}
	if desc := s.note.Description.String; desc != "" {
		meta = append(meta, desc)
	}
	b.WriteString(dimStyle.Render(strings.Join(meta, "  •  ")))

	return detailHeaderStyle.Width(s.sess.width).Render(b.String())
}

// layout (re)computes viewport dimensions and re-renders the body. Called on
// init, resize, and whenever the note content changes.
func (s *detailScreen) layout() {
	if s.sess.width == 0 {
		return
	}
	headerHeight := lipgloss.Height(s.headerView())
	footerHeight := 1 // help line
	vpHeight := s.sess.height - headerHeight - footerHeight
	if vpHeight < 1 {
		vpHeight = 1
	}

	// v2 turned the viewport constructor into functional options and made
	// width/height unexported (SetWidth/SetHeight instead of assignment).
	if !s.ready {
		s.vp = viewport.New(
			viewport.WithWidth(s.sess.width),
			viewport.WithHeight(vpHeight),
		)
		s.ready = true
	} else {
		s.vp.SetWidth(s.sess.width)
		s.vp.SetHeight(vpHeight)
	}
	s.vp.SetContent(s.renderBody())
}

// renderBody converts the markdown body to styled terminal output.
//
// The render goes through markdown.go's cache. That matters more than it looks
// on this screen: layout() re-renders on every resize, on every reload after a
// flag toggle, and again when the category list lands — three times for one
// note open, all producing identical output. The cache key carries the note's
// update timestamp, so an edit still re-renders.
func (s *detailScreen) renderBody() string {
	body := s.note.Body.String
	if strings.TrimSpace(body) == "" {
		return dimStyle.Render("(this note has no body)")
	}
	return renderMarkdownCached(
		noteCacheKey(s.note.ID, s.note.UpdatedAt.Unix()), body, s.sess.width)
}

// restyle re-renders the body under the new palette. The viewport itself holds
// no palette-derived style set (it draws only its content), so the cached
// markdown is the whole of this screen's derived state.
func (s *detailScreen) restyle() {
	s.layout()
}

func (s *detailScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.layout()
		return s, nil

	case noteLoadedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to reload note")
		}
		if msg.note == nil {
			// The note vanished (deleted elsewhere); fall back to the list.
			return s, pop(true)
		}
		s.note = *msg.note
		s.layout()
		return s, nil

	case noteCatsLoadedMsg:
		if msg.err == nil {
			s.cats = msg.cats
			s.layout() // header height may have changed
		}
		return s, nil

	case flagToggledMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to toggle flag")
		}
		if msg.note != nil {
			s.note = *msg.note
			s.layout()
		}
		return s, nil

	case lockAcquiredMsg:
		switch {
		case msg.err != nil:
			return s, statusErr(msg.err, "Could not open for editing")
		case msg.blockedBy != nil:
			// "Open read-only" from here is a no-op beyond closing the dialog:
			// this screen already IS the read-only view of the note. Pushing a
			// second detail screen would stack two identical views and make esc
			// take two presses to do what it looks like it does once.
			return s, push(newLockedScreen(s.sess, msg.note, msg.blockedBy,
				func() tea.Cmd { return nil }))
		default:
			return s, push(newFormScreen(s.sess, msg.note))
		}

	case noteDeletedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Delete failed")
		}
		// We just deleted the note we're looking at — back to the list.
		return s, tea.Sequence(pop(true), status("Note deleted"))

	case noteDuplicatedMsg:
		if msg.err != nil {
			// Stay on the note: the copy is what failed, and this screen is
			// still showing the thing the user was reading.
			return s, statusErr(msg.err, duplicateErrContext(msg.note))
		}
		// Back to the list, which is where the new note actually is. Staying
		// here would leave the user looking at the original with only a status
		// line to say that anything happened.
		return s, tea.Sequence(pop(true), status("Duplicated as \""+msg.note.Title+"\""))

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Back, keys.Quit):
			// Pop with refresh: a flag toggle here should show in the list.
			return s, pop(true)
		case key.Matches(msg, keys.Edit):
			// Claim first, open second — the same order browse uses. The form
			// is pushed from the reply (see lockAcquiredMsg below).
			return s, acquireLockCmd(s.sess.store, &s.note, s.sess.user.GUID, false)
		case key.Matches(msg, keys.Duplicate):
			// Read-only as far as this note is concerned, so no lock is taken —
			// see the same call on the browse screen.
			return s, push(newDuplicateScreen(s.sess, s.note))
		case key.Matches(msg, keys.Flag):
			return s, toggleFlagCmd(s.sess.store, s.note.ID, s.sess.user.GUID)
		case key.Matches(msg, keys.Delete):
			return s, push(newConfirmScreen(s.sess,
				"Delete \""+s.note.Title+"\"?",
				deleteNoteCmd(s.sess.store, s.note.ID, s.sess.user.GUID)))
		}
	}

	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return s, cmd
}

func (s *detailScreen) View() string {
	if !s.ready {
		return ""
	}
	return s.headerView() + "\n" + s.vp.View() + "\n" + renderHelp(keys.detailHelp()...)
}

var _ screen = (*detailScreen)(nil)
var _ refresher = (*detailScreen)(nil)
var _ restyler = (*detailScreen)(nil)
