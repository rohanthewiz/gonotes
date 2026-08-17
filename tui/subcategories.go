package tui

import (
	"slices"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// subcategoriesScreen is one level below the category list: the subcategories
// ONE category defines, as a filter and as a short editing surface.
//
//	browse ──"c"──► categories ──"s"──► subcategories
//	   ▲                                     │
//	   └─────── enter (filter applied) ◄──────┘
//
// Two things live here, and they are deliberately the same two the categories
// screen offers one level up:
//
//   - PICKING. space toggles a subcategory, enter filters the note list to the
//     notes carrying every one that is selected (AND, the same rule the web
//     UI's chips use — see models.GetNotesByCategoryAndSubcategories). With
//     nothing toggled, enter filters by the highlighted row alone, so the common
//     case is one keystroke and multi-select is there when it is wanted.
//   - DEFINING. n adds a name to the category's definition, d removes one. The
//     definition is a palette, not an assignment: it is what the form field, the
//     web UI's checkboxes and this screen offer, and editing it here never
//     touches which notes are filed where.
//
// The category travels in by value and is REPLACED from the store's answer on
// every definition edit, so the rows always show what was actually written
// rather than what this screen asked for.
type subcategoriesScreen struct {
	sess *session
	cat  models.Category

	list list.Model

	// selected holds the toggled subcategory names, in toggle order — the order
	// is what a filter built from them reads in, and stable order keeps the
	// browse title from reshuffling between two picks of the same pair.
	selected []string

	// dirty records that the definition changed while this screen was open, so
	// the pop can tell the category list to reload. Without it, a subcategory
	// added here would not show in the row behind until the next visit.
	dirty bool

	// clicks turns two clicks on one row into "filter by this subcategory",
	// matching what enter does. See mouse.go.
	clicks clickTracker
}

// subItem is one subcategory row. It carries the toggle state rather than
// looking it up, because list.Item values are what the widget renders from —
// there is no hook for "ask the screen whether this row is selected".
type subItem struct {
	name string
	on   bool
}

// Title marks a toggled row with a checkbox. The marker is text, not color: the
// list's own selection highlight already owns the color of the cursor row, and a
// row that is both highlighted and toggled has to stay distinguishable from one
// that is only highlighted.
func (i subItem) Title() string {
	if i.on {
		return "[x] " + i.name
	}
	return "[ ] " + i.name
}

// Description marks membership in the filter being built and otherwise says
// nothing. The keys are in the footer already; repeating "space selects" on
// every row would be three lines of instruction for three rows of content.
func (i subItem) Description() string {
	if i.on {
		return dimStyle.Render("in the filter")
	}
	return ""
}

func (i subItem) FilterValue() string { return i.name }

func newSubcategoriesScreen(sess *session, cat models.Category) *subcategoriesScreen {
	l := list.New([]list.Item{}, newListDelegate(), sess.width, sess.height)
	l.Title = cat.Name + " subcategories"
	applyListStyles(&l) // see the note on applyListStyles in browse.go
	l.SetStatusBarItemName("subcategory", "subcategories")
	// The empty state has to teach, because reaching a category with no
	// subcategories defined is the normal first visit rather than an error.
	l.SetShowStatusBar(true)
	l.DisableQuitKeybindings()
	l.AdditionalShortHelpKeys = func() []key.Binding { return keys.subcategoriesHelp() }

	s := &subcategoriesScreen{sess: sess, cat: cat, list: l}
	s.syncTitle()
	return s
}

// syncTitle keeps the heading showing what enter would apply.
//
// The pending filter goes in the list's TITLE rather than on a line of its own
// under the list: the list widget is sized to the whole screen, so anything
// appended below it is clamped away (clampPane) and would simply never be seen.
func (s *subcategoriesScreen) syncTitle() {
	if len(s.selected) > 0 {
		s.list.Title = "filter: " + models.FormatCategorySpec(s.cat.Name, s.selected)
		return
	}
	s.list.Title = s.cat.Name + " subcategories"
}

// restyle rebuilds the styles the list widget copied in at construction.
func (s *subcategoriesScreen) restyle() {
	applyListStyles(&s.list)
}

func (s *subcategoriesScreen) Init() tea.Cmd {
	s.list.SetSize(s.sess.width, s.sess.height)
	return s.rebuild()
}

// takingText: the same fuzzy-filter condition as the other list screens. See
// metakeys.go.
func (s *subcategoriesScreen) takingText() bool {
	return s.list.FilterState() == list.Filtering
}

// subcategories is the category's defined list, decoded from the JSON column
// through the model's own accessor so this screen never parses it itself.
func (s *subcategoriesScreen) subcategories() []string {
	return s.cat.ToOutput().Subcategories
}

// rebuild refreshes the rows from the category in hand. It is not a store read:
// everything on this screen comes from the category value, which arrives whole
// from the categories list and is replaced by the store's answer after an edit.
func (s *subcategoriesScreen) rebuild() tea.Cmd {
	subs := s.subcategories()
	items := make([]list.Item, 0, len(subs))
	for _, name := range subs {
		items = append(items, subItem{name: name, on: slices.Contains(s.selected, name)})
	}
	return s.list.SetItems(items)
}

func (s *subcategoriesScreen) highlighted() string {
	if item, ok := s.list.SelectedItem().(subItem); ok {
		return item.name
	}
	return ""
}

// toggle flips the highlighted row's membership in the filter.
func (s *subcategoriesScreen) toggle() tea.Cmd {
	name := s.highlighted()
	if name == "" {
		return nil
	}
	if idx := slices.Index(s.selected, name); idx >= 0 {
		s.selected = slices.Delete(s.selected, idx, idx+1)
	} else {
		s.selected = append(s.selected, name)
	}
	// Rebuilding replaces every row, which resets the widget's cursor to the top
	// — so put it back where the user left it. Toggling four subcategories in a
	// row is the whole point of multi-select, and it is unusable if each toggle
	// jumps home.
	idx := s.list.Index()
	cmd := s.rebuild()
	s.list.Select(idx)
	s.syncTitle()
	return cmd
}

// pick applies the filter and returns to the note list. Shared by enter and by
// a double-click so the two doors cannot drift apart.
//
// TWO pops, not one: this screen sits on top of the category list, and the
// filter belongs to the browse screen under both. tea.Sequence runs them in
// order, so the message lands after the stack is back at browse — the same
// pattern categoriesScreen.pick uses, one level deeper.
func (s *subcategoriesScreen) pick(subs []string) tea.Cmd {
	if len(subs) == 0 {
		return nil
	}
	cat := s.cat
	picked := slices.Clone(subs) // the screen's slice keeps mutating; the message must not
	return tea.Sequence(pop(false), pop(false), func() tea.Msg {
		return categoryPickedMsg{cat: &cat, subs: picked}
	})
}

// pickCurrent is what enter does: the toggled set when there is one, otherwise
// the highlighted row on its own.
func (s *subcategoriesScreen) pickCurrent() tea.Cmd {
	if len(s.selected) > 0 {
		return s.pick(s.selected)
	}
	if name := s.highlighted(); name != "" {
		return s.pick([]string{name})
	}
	return nil
}

// addSubcategory is the n path: merge a typed name into the definition. Merging
// rather than appending blindly means retyping an existing name is a no-op
// instead of a duplicate row.
func (s *subcategoriesScreen) addSubcategory(name string) tea.Cmd {
	merged, changed := models.MergeSubcategories(s.subcategories(), []string{name})
	if !changed {
		return status("\"" + name + "\" is already a subcategory of " + s.cat.Name)
	}
	return setCategorySubcategoriesCmd(s.sess.store, s.cat, merged, s.sess.user.GUID)
}

// removeSubcategory is the d path. It drops the name from the DEFINITION only;
// notes already filed under it keep that selection until they are edited, which
// is the same thing the web UI does and the reason the confirm prompt says so.
func (s *subcategoriesScreen) removeSubcategory(name string) tea.Cmd {
	remaining := slices.DeleteFunc(slices.Clone(s.subcategories()),
		func(sub string) bool { return sub == name })
	return setCategorySubcategoriesCmd(s.sess.store, s.cat, remaining, s.sess.user.GUID)
}

func (s *subcategoriesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.list.SetSize(s.sess.width, s.sess.height)
		return s, nil

	case categorySubsUpdatedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to update subcategories")
		}
		if msg.cat != nil {
			s.cat = *msg.cat
		}
		s.dirty = true
		// A subcategory that no longer exists must not linger in the filter the
		// next enter would apply — it would match no note at all.
		defined := s.subcategories()
		s.selected = slices.DeleteFunc(s.selected, func(name string) bool {
			return !slices.Contains(defined, name)
		})
		s.syncTitle()
		return s, tea.Batch(s.rebuild(), status("Subcategories updated"))

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
			// A double-click filters by the row that was clicked, not by whatever
			// is toggled: the pointer names one subcategory, and honoring a
			// keyboard selection the user cannot see under the cursor would make
			// the same gesture do two different things.
			if name := s.highlighted(); name != "" {
				return s, s.pick([]string{name})
			}
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
			// Refresh the category list behind us only when the definition
			// actually changed — a plain look-and-leave costs no reload.
			return s, pop(s.dirty)

		case key.Matches(msg, keys.SelectSub):
			return s, s.toggle()

		case key.Matches(msg, keys.Filter):
			if cmd := s.pickCurrent(); cmd != nil {
				return s, cmd
			}

		case key.Matches(msg, keys.New):
			return s, push(newPromptScreen(s.sess,
				"New subcategory of "+s.cat.Name,
				func(name string) tea.Cmd { return s.addSubcategory(name) }))

		case key.Matches(msg, keys.Delete):
			if name := s.highlighted(); name != "" {
				return s, push(newConfirmScreen(s.sess,
					"Remove subcategory \""+name+"\" from "+s.cat.Name+
						"? Notes already filed under it keep it until edited.",
					s.removeSubcategory(name)))
			}
		}
	}

	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *subcategoriesScreen) View() string {
	// The empty state gets its own view rather than an empty list: "no items"
	// with a footer is a dead end, and the one thing to do here (press n) is
	// worth saying out loud.
	if len(s.subcategories()) == 0 {
		// A reduced footer: with no rows there is nothing to select or filter by,
		// and advertising keys that do nothing is worse than a short help line.
		body := appTitleStyle.Render(s.cat.Name) + "\n\n" +
			dimStyle.Render("No subcategories yet.") + "\n" +
			dimStyle.Render("Add one with \"n\", or type \""+s.cat.Name+
				"/name\" in a note's Categories field.") + "\n\n" +
			renderHelp(keys.New, keys.Back)
		return clampPane(body, s.sess.width, s.sess.height)
	}

	// Same clamp as the other list screens, for the same reason — see clampPane.
	// What enter would apply is in the title (syncTitle), which is inside the
	// clamp rather than below it.
	return clampPane(s.list.View(), s.sess.width, s.sess.height)
}

var _ screen = (*subcategoriesScreen)(nil)
var _ restyler = (*subcategoriesScreen)(nil)
var _ texter = (*subcategoriesScreen)(nil)
