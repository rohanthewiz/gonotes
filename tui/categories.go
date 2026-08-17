package tui

import (
	"strings"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// categoriesScreen lists the user's categories. Its primary job is picking a
// category filter for the notes list (enter), with light management on the
// side (n = new, d = delete). Renames stay in the web UI — the TUI optimizes
// for the frequent operations.
//
// Subcategories are one level down, behind "s": the row shows which ones a
// category defines, and subcategoriesScreen is where they are picked as a
// filter or added and removed. Keeping them off this screen is what lets enter
// keep meaning "filter by this category" — the thing this screen is opened for.
type categoriesScreen struct {
	sess *session
	list list.Model

	// clicks turns two clicks on one row into "filter by this category",
	// matching what enter does. See mouse.go.
	clicks clickTracker
}

type catItem struct{ cat models.Category }

func (i catItem) Title() string { return i.cat.Name }

// Description shows the category's subcategories when it has any, because that
// is the row's most useful fact: it says both that "s" has something to open
// here and what filing under this category can mean. The description and the
// creation date are the fallbacks, in that order — a date is a last resort.
func (i catItem) Description() string {
	if subs := i.cat.ToOutput().Subcategories; len(subs) > 0 {
		return dimStyle.Render("▸ " + strings.Join(subs, ", "))
	}
	if d := i.cat.Description.String; d != "" {
		return d
	}
	return dimStyle.Render("created " + i.cat.CreatedAt.Format("2006-01-02"))
}

// FilterValue includes the subcategory names so the fuzzy filter finds a
// category by something it contains — typing "/backend" is how someone who
// remembers the subcategory but not which category holds it gets there.
func (i catItem) FilterValue() string {
	return models.FormatCategorySpec(i.cat.Name, i.cat.ToOutput().Subcategories)
}

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

// takingText: the same fuzzy-filter condition as browseScreen. See metakeys.go.
func (s *categoriesScreen) takingText() bool {
	return s.list.FilterState() == list.Filtering
}

func (s *categoriesScreen) refresh() tea.Cmd {
	return loadCategoriesCmd(s.sess.store, s.sess.user.GUID)
}

func (s *categoriesScreen) selected() *models.Category {
	if item, ok := s.list.SelectedItem().(catItem); ok {
		return &item.cat
	}
	return nil
}

// pick narrows the note list to the highlighted category. Shared by enter and
// by a double-click, so the two doors cannot drift apart.
//
// Returns nil when nothing is highlighted (an empty list), which is what lets
// the enter case fall through to the widget instead of swallowing the key.
func (s *categoriesScreen) pick() tea.Cmd {
	c := s.selected()
	if c == nil {
		return nil
	}
	// Pop first, then deliver the pick to the browse screen that is now on
	// top. tea.Sequence guarantees that ordering; tea.Batch would not.
	picked := *c
	return tea.Sequence(pop(false), func() tea.Msg {
		// No subs: enter on this screen means the whole category, subcategories
		// and all. Narrowing is subcategoriesScreen's job.
		return categoryPickedMsg{cat: &picked}
	})
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

	case tea.MouseWheelMsg:
		wheelList(&s.list, msg)
		return s, nil

	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return s, nil
		}
		idx, ok := listRowAt(&s.list, msg.Y)
		if !ok {
			return s, nil
		}
		s.list.Select(idx)
		if s.clicks.double(idx) {
			return s, s.pick()
		}
		return s, nil

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
			if cmd := s.pick(); cmd != nil {
				return s, cmd
			}

		case key.Matches(msg, keys.AllNotes):
			// Convenience escape hatch: jump back with the filter cleared.
			return s, tea.Sequence(pop(false), func() tea.Msg {
				return categoryPickedMsg{cat: nil}
			})

		case key.Matches(msg, keys.Subcats):
			// The highlighted category's own copy travels down: the subcategory
			// screen edits its definition and hands back what the store returned,
			// so it never has to re-read the list this screen already loaded.
			if c := s.selected(); c != nil {
				return s, push(newSubcategoriesScreen(s.sess, *c))
			}

		case key.Matches(msg, keys.New):
			return s, push(newPromptScreen(s.sess, "New category name",
				func(name string) tea.Cmd {
					return createCategoryCmd(s.sess.store, name, s.sess.user.GUID)
				}))

		case key.Matches(msg, keys.Delete):
			if c := s.selected(); c != nil {
				return s, push(newConfirmScreen(s.sess,
					"Delete category \""+c.Name+"\"? Notes keep their other categories.",
					deleteCategoryCmd(s.sess.store, c.ID, s.sess.user.GUID)))
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
