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

// categoryMode tracks the current interaction state in the category manager.
type categoryMode int

const (
	catModeBrowse categoryMode = iota // navigating the category list
	catModeCreate                     // inline form for new category
	catModeEdit                       // inline form for editing a category
)

// categoryModel manages the category list screen with inline CRUD.
// Categories are displayed as a scrollable list; create and edit use
// inline text inputs at the bottom of the screen.
type categoryModel struct {
	categories []models.Category
	cursor     int
	userGUID   string
	mode       categoryMode

	// Inline form fields (shared for create and edit)
	nameInput        textinput.Model
	descriptionInput textinput.Model
	subcatsInput     textinput.Model // comma-separated subcategories
	formFocus        int             // 0=name, 1=description, 2=subcategories
	editingID        int64           // category ID being edited (0 for create)

	// Delete confirmation
	confirmDelete bool
	deleteTarget  int64
	deleteName    string

	errMsg string
}

func newCategoryModel() categoryModel {
	ni := textinput.New()
	ni.Placeholder = "Category name (required)"
	ni.CharLimit = 128

	di := textinput.New()
	di.Placeholder = "Description (optional)"
	di.CharLimit = 256

	si := textinput.New()
	si.Placeholder = "Subcategories: sub1, sub2, sub3"
	si.CharLimit = 512

	return categoryModel{
		nameInput:        ni,
		descriptionInput: di,
		subcatsInput:     si,
	}
}

// categoriesLoadedMsg carries the refreshed category list from the DB.
type categoriesLoadedMsg struct {
	categories []models.Category
}

// categorySavedMsg signals a category was created or updated successfully.
type categorySavedMsg struct{}

// categoryDeletedMsg signals a category was deleted.
type categoryDeletedMsg struct{}

// loadCategories fetches all categories for the current user.
func (c *categoryModel) loadCategories(userGUID string) tea.Cmd {
	c.userGUID = userGUID
	return func() tea.Msg {
		cats, err := models.ListCategories(0, 0, userGUID)
		if err != nil {
			return categoriesLoadedMsg{categories: nil}
		}
		return categoriesLoadedMsg{categories: cats}
	}
}

func (c categoryModel) Update(msg tea.Msg, app *appModel) (categoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case categoriesLoadedMsg:
		c.categories = msg.categories
		if c.cursor >= len(c.categories) {
			c.cursor = maxInt(0, len(c.categories)-1)
		}
		return c, nil

	case categorySavedMsg:
		c.mode = catModeBrowse
		app.statusMsg = "Category saved"
		return c, c.loadCategories(c.userGUID)

	case categoryDeletedMsg:
		c.confirmDelete = false
		app.statusMsg = "Category deleted"
		return c, c.loadCategories(c.userGUID)

	case tea.KeyMsg:
		// Delete confirmation takes priority
		if c.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				catID := c.deleteTarget
				userGUID := c.userGUID
				c.confirmDelete = false
				return c, func() tea.Msg {
					models.DeleteCategory(catID, userGUID)
					return categoryDeletedMsg{}
				}
			default:
				c.confirmDelete = false
				return c, nil
			}
		}

		// Inline form mode — capture input for create/edit
		if c.mode == catModeCreate || c.mode == catModeEdit {
			return c.updateForm(msg, app)
		}

		// Browse mode
		switch {
		case key.Matches(msg, globalKeys.Quit):
			return c, tea.Quit

		case key.Matches(msg, globalKeys.Back):
			return c, app.goToNoteList()

		case key.Matches(msg, listKeys.Up):
			if c.cursor > 0 {
				c.cursor--
			}

		case key.Matches(msg, listKeys.Down):
			if c.cursor < len(c.categories)-1 {
				c.cursor++
			}

		case key.Matches(msg, categoryKeys.New):
			c.mode = catModeCreate
			c.editingID = 0
			c.nameInput.SetValue("")
			c.descriptionInput.SetValue("")
			c.subcatsInput.SetValue("")
			c.formFocus = 0
			c.nameInput.Focus()
			c.errMsg = ""
			return c, nil

		case key.Matches(msg, categoryKeys.Edit):
			if len(c.categories) > 0 {
				cat := c.categories[c.cursor]
				c.mode = catModeEdit
				c.editingID = cat.ID
				c.nameInput.SetValue(cat.Name)
				c.descriptionInput.SetValue(nullStr(cat.Description))
				// Parse subcategories JSON to comma-separated
				var subcats []string
				if cat.Subcategories.Valid && cat.Subcategories.String != "" {
					json.Unmarshal([]byte(cat.Subcategories.String), &subcats)
				}
				c.subcatsInput.SetValue(joinTags(subcats))
				c.formFocus = 0
				c.nameInput.Focus()
				c.errMsg = ""
			}
			return c, nil

		case key.Matches(msg, categoryKeys.Delete):
			if len(c.categories) > 0 {
				c.confirmDelete = true
				c.deleteTarget = c.categories[c.cursor].ID
				c.deleteName = c.categories[c.cursor].Name
			}
		}
	}
	return c, nil
}

// updateForm handles input when the inline create/edit form is active.
func (c categoryModel) updateForm(msg tea.KeyMsg, app *appModel) (categoryModel, tea.Cmd) {
	switch {
	case key.Matches(msg, globalKeys.Back):
		// Cancel form
		c.mode = catModeBrowse
		c.errMsg = ""
		return c, nil

	case msg.Type == tea.KeyEnter:
		// Save the category
		name := strings.TrimSpace(c.nameInput.Value())
		if name == "" {
			c.errMsg = "Name is required"
			return c, nil
		}
		desc := strings.TrimSpace(c.descriptionInput.Value())
		subcatStr := strings.TrimSpace(c.subcatsInput.Value())
		subcats := parseTags(subcatStr)
		editID := c.editingID
		userGUID := c.userGUID

		return c, func() tea.Msg {
			input := models.CategoryInput{
				Name:          name,
				Description:   strPtr(desc),
				Subcategories: subcats,
			}
			if editID > 0 {
				models.UpdateCategory(editID, input, userGUID)
			} else {
				models.CreateCategory(input, userGUID)
			}
			return categorySavedMsg{}
		}

	case msg.Type == tea.KeyTab:
		c.advanceFormFocus(1)
		return c, nil

	case msg.Type == tea.KeyShiftTab:
		c.advanceFormFocus(-1)
		return c, nil
	}

	// Forward to focused input
	var cmd tea.Cmd
	switch c.formFocus {
	case 0:
		c.nameInput, cmd = c.nameInput.Update(msg)
	case 1:
		c.descriptionInput, cmd = c.descriptionInput.Update(msg)
	case 2:
		c.subcatsInput, cmd = c.subcatsInput.Update(msg)
	}
	return c, cmd
}

func (c *categoryModel) advanceFormFocus(delta int) {
	c.nameInput.Blur()
	c.descriptionInput.Blur()
	c.subcatsInput.Blur()

	c.formFocus += delta
	if c.formFocus < 0 {
		c.formFocus = 2
	} else if c.formFocus > 2 {
		c.formFocus = 0
	}

	switch c.formFocus {
	case 0:
		c.nameInput.Focus()
	case 1:
		c.descriptionInput.Focus()
	case 2:
		c.subcatsInput.Focus()
	}
}

func (c categoryModel) View(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("  Categories") + "\n")

	if len(c.categories) == 0 && c.mode == catModeBrowse {
		b.WriteString(dimStyle.Render("  No categories. Press 'n' to create one.") + "\n")
	}

	// Render category list
	for i, cat := range c.categories {
		prefix := "  "
		style := normalStyle
		if i == c.cursor {
			prefix = "▸ "
			style = selectedStyle
		}

		// Show subcategory count inline
		var subcatCount int
		if cat.Subcategories.Valid && cat.Subcategories.String != "" {
			var subcats []string
			json.Unmarshal([]byte(cat.Subcategories.String), &subcats)
			subcatCount = len(subcats)
		}

		line := fmt.Sprintf("%s%s", prefix, cat.Name)
		desc := nullStr(cat.Description)
		if desc != "" {
			line += " — " + truncate(desc, 40)
		}
		if subcatCount > 0 {
			line += dimStyle.Render(fmt.Sprintf(" [%d subcats]", subcatCount))
		}

		b.WriteString(style.Render(line) + "\n")
	}

	b.WriteString("\n")

	// Inline form for create/edit
	if c.mode == catModeCreate || c.mode == catModeEdit {
		formTitle := "New Category"
		if c.mode == catModeEdit {
			formTitle = "Edit Category"
		}
		b.WriteString(labelStyle.Render("  "+formTitle) + "\n")
		b.WriteString("  " + c.nameInput.View() + "\n")
		b.WriteString("  " + c.descriptionInput.View() + "\n")
		b.WriteString("  " + c.subcatsInput.View() + "\n")
		if c.errMsg != "" {
			b.WriteString("  " + errorStyle.Render(c.errMsg) + "\n")
		}
		b.WriteString(helpStyle.Render("  tab: next field • enter: save • esc: cancel"))
	} else if c.confirmDelete {
		b.WriteString(dangerStyle.Render(
			fmt.Sprintf("  Delete category \"%s\"? (y/n)", c.deleteName),
		))
	} else {
		b.WriteString(helpStyle.Render(
			"  ↑/k: up • ↓/j: down • n: new • e: edit • d: delete • esc: back",
		))
	}

	return b.String()
}
