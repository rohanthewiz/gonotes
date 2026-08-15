package tui

import (
	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// categoriesScreen lists the user's categories. Its primary job is picking a
// category filter for the notes list (enter), with light management on the
// side (n = new, d = delete). Renames and subcategories stay in the web UI —
// the TUI optimizes for the frequent operations.
type categoriesScreen struct {
	sess *session
	list list.Model
}

type catItem struct{ cat models.Category }

func (i catItem) Title() string { return i.cat.Name }
func (i catItem) Description() string {
	if d := i.cat.Description.String; d != "" {
		return d
	}
	return dimStyle.Render("created " + i.cat.CreatedAt.Format("2006-01-02"))
}
func (i catItem) FilterValue() string { return i.cat.Name }

func newCategoriesScreen(sess *session) *categoriesScreen {
	l := list.New([]list.Item{}, newListDelegate(), sess.width, sess.height)
	l.Title = "Categories"
	applyListStyles(&l) // see the note on applyListStyles in browse.go
	l.SetStatusBarItemName("category", "categories")
	l.DisableQuitKeybindings()
	l.AdditionalShortHelpKeys = func() []key.Binding { return keys.categoriesHelp() }

	return &categoriesScreen{sess: sess, list: l}
}

// restyle rebuilds the styles the list widget copied in at construction.
func (s *categoriesScreen) restyle() {
	applyListStyles(&s.list)
}

func (s *categoriesScreen) Init() tea.Cmd {
	s.list.SetSize(s.sess.width, s.sess.height)
	return s.refresh()
}

func (s *categoriesScreen) refresh() tea.Cmd {
	return loadCategoriesCmd(s.sess.user.GUID)
}

func (s *categoriesScreen) selected() *models.Category {
	if item, ok := s.list.SelectedItem().(catItem); ok {
		return &item.cat
	}
	return nil
}

func (s *categoriesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.list.SetSize(s.sess.width, s.sess.height)
		return s, nil

	case categoriesLoadedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to load categories")
		}
		items := make([]list.Item, 0, len(msg.cats))
		for _, c := range msg.cats {
			items = append(items, catItem{cat: c})
		}
		return s, s.list.SetItems(items)

	case categoryCreatedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to create category")
		}
		return s, tea.Batch(s.refresh(), status("Category created"))

	case categoryDeletedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to delete category")
		}
		return s, tea.Batch(s.refresh(), status("Category deleted"))

	case tea.KeyPressMsg:
		if s.list.FilterState() == list.Filtering {
			break
		}

		switch {
		case key.Matches(msg, keys.Back, keys.Quit):
			if s.list.FilterState() == list.FilterApplied {
				break // let the list clear its own fuzzy filter first
			}
			return s, pop(false)

		case key.Matches(msg, keys.Filter):
			if c := s.selected(); c != nil {
				// Pop first, then deliver the pick to the browse screen that
				// is now on top. tea.Sequence guarantees that ordering;
				// tea.Batch would not.
				picked := *c
				return s, tea.Sequence(pop(false), func() tea.Msg {
					return categoryPickedMsg{cat: &picked}
				})
			}

		case key.Matches(msg, keys.AllNotes):
			// Convenience escape hatch: jump back with the filter cleared.
			return s, tea.Sequence(pop(false), func() tea.Msg {
				return categoryPickedMsg{cat: nil}
			})

		case key.Matches(msg, keys.New):
			return s, push(newPromptScreen(s.sess, "New category name",
				func(name string) tea.Cmd {
					return createCategoryCmd(name, s.sess.user.GUID)
				}))

		case key.Matches(msg, keys.Delete):
			if c := s.selected(); c != nil {
				return s, push(newConfirmScreen(s.sess,
					"Delete category \""+c.Name+"\"? Notes keep their other categories.",
					deleteCategoryCmd(c.ID, s.sess.user.GUID)))
			}
		}
	}

	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *categoriesScreen) View() string {
	// Same clamp as the browse list, for the same reason — see clampPane.
	return clampPane(s.list.View(), s.sess.width, s.sess.height)
}

var _ screen = (*categoriesScreen)(nil)
var _ refresher = (*categoriesScreen)(nil)
var _ restyler = (*categoriesScreen)(nil)
