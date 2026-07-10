package tui

import (
	"strings"

	"gonotes/models"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// browseScreen is the home screen: the user's notes in a filterable list.
//
// Search & filtering (two complementary mechanisms):
//   - "/" activates bubbles/list's built-in fuzzy filter, which we point at
//     title + tags + description + a slice of the body (see FilterValue), so
//     typing "/duck" finds notes that merely mention DuckDB in passing.
//   - "c" opens the category screen; picking a category narrows the list to
//     that category's notes (server-side via the join table). esc clears it.
//
// The two compose: you can filter by category first, then "/" within it.
type browseScreen struct {
	sess *session
	list list.Model

	// catFilter is the active category filter; nil means "all notes".
	catFilter *models.Category
}

// noteItem adapts a models.Note to the list.Item interface.
type noteItem struct{ note models.Note }

func (i noteItem) Title() string {
	var prefix strings.Builder
	if i.note.IsFlagged {
		prefix.WriteString(flaggedStyle.Render("⚑ "))
	}
	if i.note.IsPrivate {
		prefix.WriteString(privateStyle.Render("🔒 "))
	}
	return prefix.String() + i.note.Title
}

func (i noteItem) Description() string {
	// Prefer the explicit description; fall back to the body's first line so
	// every row gives a hint of its content.
	desc := i.note.Description.String
	if desc == "" && i.note.Body.Valid {
		desc = firstLine(i.note.Body.String)
	}
	date := i.note.UpdatedAt.Format("2006-01-02")
	if tags := i.note.Tags.String; tags != "" {
		return dimStyle.Render(date+"  #"+tags) + "  " + desc
	}
	return dimStyle.Render(date) + "  " + desc
}

// FilterValue feeds the fuzzy filter. Including a bounded slice of the body
// makes "/" a genuine content search, while the cap keeps matching fast even
// with hundreds of long notes.
func (i noteItem) FilterValue() string {
	body := i.note.Body.String
	if len(body) > 2000 {
		body = body[:2000]
	}
	return i.note.Title + " " + i.note.Tags.String + " " + i.note.Description.String + " " + body
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	// Strip a leading markdown heading marker so the preview reads cleanly.
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

func newBrowseScreen(sess *session) *browseScreen {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(colorPrimary).BorderLeftForeground(colorPrimary)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(colorSubtle).BorderLeftForeground(colorPrimary)

	l := list.New([]list.Item{}, delegate, sess.width, sess.height)
	l.Title = "GoNotes"
	l.Styles.Title = appTitleStyle
	l.SetShowStatusBar(true)
	l.SetStatusBarItemName("note", "notes")
	// We own quit ("q") and back ("esc") semantics; the widget must not
	// intercept them for its own quit behavior.
	l.DisableQuitKeybindings()
	// Surface our custom actions inside the list's built-in help footer.
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view")),
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
			key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
			key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "flag")),
			key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "categories")),
			key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		}
	}

	return &browseScreen{sess: sess, list: l}
}

func (s *browseScreen) Init() tea.Cmd {
	s.list.SetSize(s.sess.width, s.sess.height)
	return s.refresh()
}

// refresh reloads the list honoring the active category filter. Also called
// by the root when a pushed screen (form/detail/confirm) pops with changes.
func (s *browseScreen) refresh() tea.Cmd {
	if s.catFilter != nil {
		return loadCategoryNotesCmd(s.catFilter.ID, s.sess.user.GUID)
	}
	return loadNotesCmd(s.sess.user.GUID)
}

func (s *browseScreen) selectedNote() *models.Note {
	if item, ok := s.list.SelectedItem().(noteItem); ok {
		return &item.note
	}
	return nil
}

func (s *browseScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.list.SetSize(s.sess.width, s.sess.height)
		return s, nil

	case notesLoadedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to load notes")
		}
		items := make([]list.Item, 0, len(msg.notes))
		for _, n := range msg.notes {
			items = append(items, noteItem{note: n})
		}
		title := "GoNotes"
		if s.catFilter != nil {
			title = "GoNotes — " + s.catFilter.Name
		}
		s.list.Title = title
		return s, s.list.SetItems(items)

	case categoryPickedMsg:
		s.catFilter = msg.cat
		return s, s.refresh()

	case flagToggledMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to toggle flag")
		}
		return s, s.refresh()

	case noteDeletedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Delete failed")
		}
		return s, tea.Batch(s.refresh(), status("Note deleted"))

	case tea.KeyMsg:
		// While the fuzzy filter prompt is active, every key belongs to it.
		if s.list.FilterState() == list.Filtering {
			break
		}

		switch msg.String() {
		case "q":
			return s, tea.Quit

		case "esc":
			// esc peels back UI state in order: applied fuzzy filter first,
			// then the category filter, and does nothing at the home state.
			if s.list.FilterState() == list.FilterApplied {
				break // let the list clear its own filter
			}
			if s.catFilter != nil {
				s.catFilter = nil
				return s, s.refresh()
			}
			return s, nil

		case "enter":
			if n := s.selectedNote(); n != nil {
				return s, push(newDetailScreen(s.sess, *n))
			}

		case "n":
			return s, push(newFormScreen(s.sess, nil))

		case "e":
			if n := s.selectedNote(); n != nil {
				return s, push(newFormScreen(s.sess, n))
			}

		case "f":
			if n := s.selectedNote(); n != nil {
				return s, toggleFlagCmd(n.ID, s.sess.user.GUID)
			}

		case "d":
			if n := s.selectedNote(); n != nil {
				return s, push(newConfirmScreen(s.sess,
					"Delete \""+n.Title+"\"?",
					deleteNoteCmd(n.ID, s.sess.user.GUID)))
			}

		case "c":
			return s, push(newCategoriesScreen(s.sess))
		}
	}

	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *browseScreen) View() string {
	return s.list.View()
}

var _ screen = (*browseScreen)(nil)
var _ refresher = (*browseScreen)(nil)
