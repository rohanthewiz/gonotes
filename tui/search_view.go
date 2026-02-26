package tui

import (
	"encoding/json"
	"fmt"
	"gonotes/models"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// searchField identifies which filter field has focus.
type searchField int

const (
	searchFieldTitle searchField = iota
	searchFieldCategory
	searchFieldSubcategory
	searchFieldTags
)

// searchModel provides multi-dimensional search/filter for notes.
// Filters: title text, category, subcategory (within selected category), tags.
// Results are passed back to the notes list with a filter label.
type searchModel struct {
	userGUID string

	// Filter inputs
	titleInput textinput.Model
	tagsInput  textinput.Model

	// Category selector (scrollable list instead of text input)
	categories   []models.Category
	catCursor    int
	selectedCat  int64  // selected category ID, 0 = none
	selectedName string // selected category name (for display)

	// Subcategory multi-select (populated from selected category)
	subcategories   []string        // available subcats from selected category
	selectedSubcats map[string]bool // toggled subcategories
	subCursor       int

	focusedField searchField
	errMsg       string
}

func newSearchModel() searchModel {
	ti := textinput.New()
	ti.Placeholder = "Search by title..."
	ti.CharLimit = 256

	tg := textinput.New()
	tg.Placeholder = "Filter by tags (comma-separated)"
	tg.CharLimit = 256

	return searchModel{
		titleInput:      ti,
		tagsInput:       tg,
		selectedSubcats: make(map[string]bool),
	}
}

// searchResultsMsg carries the filtered notes back from the DB query.
type searchResultsMsg struct {
	notes []models.Note
	label string
}

// initSearch prepares the search screen and loads categories for the selector.
func (s *searchModel) initSearch(userGUID string) tea.Cmd {
	s.userGUID = userGUID
	s.focusedField = searchFieldTitle
	s.titleInput.SetValue("")
	s.tagsInput.SetValue("")
	s.titleInput.Focus()
	s.tagsInput.Blur()
	s.selectedCat = 0
	s.selectedName = ""
	s.catCursor = 0
	s.subcategories = nil
	s.selectedSubcats = make(map[string]bool)
	s.subCursor = 0
	s.errMsg = ""

	return func() tea.Msg {
		cats, _ := models.ListCategories(0, 0, userGUID)
		return categoriesLoadedForSearchMsg{categories: cats}
	}
}

// categoriesLoadedForSearchMsg populates the category selector.
type categoriesLoadedForSearchMsg struct {
	categories []models.Category
}

func (s searchModel) Update(msg tea.Msg, app *appModel) (searchModel, tea.Cmd) {
	switch msg := msg.(type) {
	case categoriesLoadedForSearchMsg:
		s.categories = msg.categories
		return s, nil

	case searchResultsMsg:
		// Pass results to the list view and navigate there
		app.listModel.setFilteredNotes(msg.notes, msg.label)
		return s, app.goToNoteList()

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, globalKeys.Back):
			return s, app.goToNoteList()

		case key.Matches(msg, globalKeys.Quit):
			return s, tea.Quit

		case key.Matches(msg, searchKeys.Execute):
			return s, s.executeSearch()

		case key.Matches(msg, searchKeys.NextField):
			s.advanceFocus(1)
			return s, nil

		case key.Matches(msg, searchKeys.PrevField):
			s.advanceFocus(-1)
			return s, nil
		}

		// Field-specific input handling
		switch s.focusedField {
		case searchFieldTitle:
			var cmd tea.Cmd
			s.titleInput, cmd = s.titleInput.Update(msg)
			return s, cmd

		case searchFieldCategory:
			return s.updateCategorySelector(msg)

		case searchFieldSubcategory:
			return s.updateSubcategorySelector(msg)

		case searchFieldTags:
			var cmd tea.Cmd
			s.tagsInput, cmd = s.tagsInput.Update(msg)
			return s, cmd
		}

	default:
		// Forward blink and other non-key messages to focused text inputs
		switch s.focusedField {
		case searchFieldTitle:
			var cmd tea.Cmd
			s.titleInput, cmd = s.titleInput.Update(msg)
			return s, cmd
		case searchFieldTags:
			var cmd tea.Cmd
			s.tagsInput, cmd = s.tagsInput.Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

// advanceFocus cycles focus between the four filter fields.
func (s *searchModel) advanceFocus(delta int) {
	s.titleInput.Blur()
	s.tagsInput.Blur()

	next := int(s.focusedField) + delta
	if next < 0 {
		next = int(searchFieldTags)
	} else if next > int(searchFieldTags) {
		next = 0
	}
	s.focusedField = searchField(next)

	switch s.focusedField {
	case searchFieldTitle:
		s.titleInput.Focus()
	case searchFieldTags:
		s.tagsInput.Focus()
	}
}

// updateCategorySelector handles j/k navigation and space/enter to select a category.
func (s searchModel) updateCategorySelector(msg tea.KeyMsg) (searchModel, tea.Cmd) {
	switch {
	case key.Matches(msg, listKeys.Up):
		if s.catCursor > 0 {
			s.catCursor--
		}
	case key.Matches(msg, listKeys.Down):
		if s.catCursor < len(s.categories) {
			// len (not len-1) because index 0 is "(none)"
			s.catCursor++
		}
	case key.Matches(msg, searchKeys.Toggle), msg.Type == tea.KeyEnter:
		if s.catCursor == 0 {
			// "(none)" — clear category filter
			s.selectedCat = 0
			s.selectedName = ""
			s.subcategories = nil
			s.selectedSubcats = make(map[string]bool)
		} else {
			idx := s.catCursor - 1 // offset for the "(none)" entry
			if idx < len(s.categories) {
				cat := s.categories[idx]
				s.selectedCat = cat.ID
				s.selectedName = cat.Name
				// Parse subcategories from the selected category
				s.subcategories = nil
				if cat.Subcategories.Valid && cat.Subcategories.String != "" {
					var subs []string
					json.Unmarshal([]byte(cat.Subcategories.String), &subs)
					s.subcategories = subs
				}
				s.selectedSubcats = make(map[string]bool)
				s.subCursor = 0
			}
		}
	}
	return s, nil
}

// updateSubcategorySelector handles toggling subcategories with space.
func (s searchModel) updateSubcategorySelector(msg tea.KeyMsg) (searchModel, tea.Cmd) {
	if len(s.subcategories) == 0 {
		return s, nil
	}
	switch {
	case key.Matches(msg, listKeys.Up):
		if s.subCursor > 0 {
			s.subCursor--
		}
	case key.Matches(msg, listKeys.Down):
		if s.subCursor < len(s.subcategories)-1 {
			s.subCursor++
		}
	case key.Matches(msg, searchKeys.Toggle):
		sub := s.subcategories[s.subCursor]
		if s.selectedSubcats[sub] {
			delete(s.selectedSubcats, sub)
		} else {
			s.selectedSubcats[sub] = true
		}
	}
	return s, nil
}

// executeSearch builds and runs the appropriate query based on active filters.
func (s *searchModel) executeSearch() tea.Cmd {
	titleQuery := strings.TrimSpace(s.titleInput.Value())
	tagsFilter := strings.TrimSpace(s.tagsInput.Value())
	catName := s.selectedName
	userGUID := s.userGUID

	// Collect selected subcategories
	var subcats []string
	for sub, selected := range s.selectedSubcats {
		if selected {
			subcats = append(subcats, sub)
		}
	}

	return func() tea.Msg {
		var notes []models.Note
		var label string
		var err error

		// Decide which query to use based on which filters are active.
		// Priority: category+subcats > category > title > all notes.
		switch {
		case catName != "" && len(subcats) > 0:
			notes, err = models.GetNotesByCategoryAndSubcategories(catName, subcats, userGUID)
			label = fmt.Sprintf("cat:%s subcats:%s", catName, strings.Join(subcats, ","))

		case catName != "":
			notes, err = models.GetNotesByCategoryName(catName, userGUID)
			label = fmt.Sprintf("cat:%s", catName)

		case titleQuery != "":
			notes, err = models.SearchNotesByTitle(titleQuery, userGUID, 50)
			label = fmt.Sprintf("title:\"%s\"", titleQuery)

		default:
			notes, err = models.ListNotes(userGUID, pageSize, 0)
			label = ""
		}

		if err != nil {
			return searchResultsMsg{notes: nil, label: "error"}
		}

		// Post-filter by tags (client-side — no dedicated model function exists)
		if tagsFilter != "" {
			filterTags := parseTags(tagsFilter)
			filtered := make([]models.Note, 0, len(notes))
			for _, n := range notes {
				noteTags := strings.ToLower(nullStr(n.Tags))
				match := true
				for _, ft := range filterTags {
					if !strings.Contains(noteTags, strings.ToLower(ft)) {
						match = false
						break
					}
				}
				if match {
					filtered = append(filtered, n)
				}
			}
			notes = filtered
			if label != "" {
				label += " "
			}
			label += fmt.Sprintf("tags:%s", tagsFilter)
		}

		// If title search was combined with category filter, post-filter by title
		if titleQuery != "" && catName != "" {
			titleLower := strings.ToLower(titleQuery)
			filtered := make([]models.Note, 0, len(notes))
			for _, n := range notes {
				if strings.Contains(strings.ToLower(n.Title), titleLower) {
					filtered = append(filtered, n)
				}
			}
			notes = filtered
			label = fmt.Sprintf("title:\"%s\" %s", titleQuery, label)
		}

		return searchResultsMsg{notes: notes, label: label}
	}
}

func (s searchModel) View(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("  Search / Filter Notes") + "\n\n")

	// Field 1: Title search
	titleLabel := "  Title"
	if s.focusedField == searchFieldTitle {
		titleLabel = "  ▸ Title"
	}
	b.WriteString(labelStyle.Render(titleLabel) + "\n")
	b.WriteString("  " + s.titleInput.View() + "\n\n")

	// Field 2: Category selector
	catLabel := "  Category"
	if s.focusedField == searchFieldCategory {
		catLabel = "  ▸ Category"
	}
	b.WriteString(labelStyle.Render(catLabel) + "\n")

	// Render category options as a scrollable list
	nonePrefix := "    "
	noneStyle := normalStyle
	if s.focusedField == searchFieldCategory && s.catCursor == 0 {
		nonePrefix = "  ▸ "
		noneStyle = selectedStyle
	}
	noneCheck := "( )"
	if s.selectedCat == 0 {
		noneCheck = "(•)"
	}
	b.WriteString(noneStyle.Render(fmt.Sprintf("%s%s (none)", nonePrefix, noneCheck)) + "\n")

	for i, cat := range s.categories {
		displayIdx := i + 1 // offset for "(none)" at position 0
		prefix := "    "
		style := normalStyle
		if s.focusedField == searchFieldCategory && s.catCursor == displayIdx {
			prefix = "  ▸ "
			style = selectedStyle
		}
		check := "( )"
		if s.selectedCat == cat.ID {
			check = "(•)"
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s %s", prefix, check, cat.Name)) + "\n")
	}
	b.WriteString("\n")

	// Field 3: Subcategory multi-select (only shown when a category is selected)
	subLabel := "  Subcategories"
	if s.focusedField == searchFieldSubcategory {
		subLabel = "  ▸ Subcategories"
	}
	b.WriteString(labelStyle.Render(subLabel) + "\n")
	if len(s.subcategories) == 0 {
		b.WriteString(dimStyle.Render("    (select a category first)") + "\n\n")
	} else {
		for i, sub := range s.subcategories {
			prefix := "    "
			style := normalStyle
			if s.focusedField == searchFieldSubcategory && i == s.subCursor {
				prefix = "  ▸ "
				style = selectedStyle
			}
			check := "[ ]"
			if s.selectedSubcats[sub] {
				check = "[x]"
			}
			b.WriteString(style.Render(fmt.Sprintf("%s%s %s", prefix, check, sub)) + "\n")
		}
		b.WriteString("\n")
	}

	// Field 4: Tags filter
	tagsLabel := "  Tags"
	if s.focusedField == searchFieldTags {
		tagsLabel = "  ▸ Tags"
	}
	b.WriteString(labelStyle.Render(tagsLabel) + "\n")
	b.WriteString("  " + s.tagsInput.View() + "\n\n")

	// Help bar
	b.WriteString(helpStyle.Render(
		"  tab: next field • space: toggle • ctrl+s: search • esc: cancel",
	))

	return b.String()
}
