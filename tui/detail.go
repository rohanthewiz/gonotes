package tui

import (
	"strings"

	"gonotes/models"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
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
	cats []models.Category

	vp    viewport.Model
	ready bool // viewport must not render before it has real dimensions
}

func newDetailScreen(sess *session, note models.Note) *detailScreen {
	return &detailScreen{sess: sess, note: note}
}

func (s *detailScreen) Init() tea.Cmd {
	s.layout()
	return loadNoteCategoriesCmd(s.note.ID, s.sess.user.GUID)
}

// refresh re-fetches the note after an edit made on the form screen above us.
func (s *detailScreen) refresh() tea.Cmd {
	return tea.Batch(
		loadNoteCmd(s.note.ID, s.sess.user.GUID),
		loadNoteCategoriesCmd(s.note.ID, s.sess.user.GUID),
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
		names := make([]string, len(s.cats))
		for i, c := range s.cats {
			names[i] = c.Name
		}
		meta = append(meta, "in "+strings.Join(names, ", "))
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

	if !s.ready {
		s.vp = viewport.New(s.sess.width, vpHeight)
		s.ready = true
	} else {
		s.vp.Width = s.sess.width
		s.vp.Height = vpHeight
	}
	s.vp.SetContent(s.renderBody())
}

// renderBody converts the markdown body to styled terminal output. On any
// renderer error we fall back to the raw text — showing something always
// beats showing an error where the note should be.
func (s *detailScreen) renderBody() string {
	body := s.note.Body.String
	if strings.TrimSpace(body) == "" {
		return dimStyle.Render("(this note has no body)")
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(), // match the terminal's light/dark background
		glamour.WithWordWrap(min(s.sess.width-2, 100)),
	)
	if err != nil {
		return body
	}
	out, err := r.Render(body)
	if err != nil {
		return body
	}
	return out
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

	case noteDeletedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Delete failed")
		}
		// We just deleted the note we're looking at — back to the list.
		return s, tea.Sequence(pop(true), status("Note deleted"))

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			// Pop with refresh: a flag toggle here should show in the list.
			return s, pop(true)
		case "e":
			return s, push(newFormScreen(s.sess, &s.note))
		case "f":
			return s, toggleFlagCmd(s.note.ID, s.sess.user.GUID)
		case "d":
			return s, push(newConfirmScreen(s.sess,
				"Delete \""+s.note.Title+"\"?",
				deleteNoteCmd(s.note.ID, s.sess.user.GUID)))
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
	help := helpStyle.Render("↑/↓ scroll • e edit • f flag • d delete • esc back")
	return s.headerView() + "\n" + s.vp.View() + "\n" + help
}

var _ screen = (*detailScreen)(nil)
var _ refresher = (*detailScreen)(nil)
