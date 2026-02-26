package tui

import (
	"encoding/json"
	"fmt"
	"gonotes/models"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

// formField identifies which field has focus in the note form.
type formField int

const (
	fieldTitle formField = iota
	fieldDescription
	fieldBody
	fieldTags
	fieldPrivate
	fieldCount // sentinel for wrapping
)

// formModel handles both creating new notes and editing existing ones.
// When noteID is 0 the form is in "create" mode; otherwise "edit" mode.
type formModel struct {
	noteID   int64  // 0 = create, >0 = edit
	noteGUID string // preserved on edit so we don't regenerate
	userGUID string

	// Form fields
	titleInput       textinput.Model
	descriptionInput textinput.Model
	bodyArea         textarea.Model
	tagsInput        textinput.Model
	isPrivate        bool

	focusedField formField
	errMsg       string

	// Category picker overlay state
	showCategoryPicker bool
	allCategories      []models.Category
	selectedCats       map[int64][]string // categoryID → selected subcategories
	catCursor          int
	catSubCursor       int
	inSubcats          bool // true when navigating subcategories within a category
}

func newFormModel() formModel {
	ti := textinput.New()
	ti.Placeholder = "Note title (required)"
	ti.CharLimit = 256

	di := textinput.New()
	di.Placeholder = "Short description (optional)"
	di.CharLimit = 512

	ba := textarea.New()
	ba.Placeholder = "Note body..."
	ba.CharLimit = 0 // unlimited
	ba.SetHeight(8)

	tg := textinput.New()
	tg.Placeholder = "tag1, tag2, tag3"
	tg.CharLimit = 512

	return formModel{
		titleInput:       ti,
		descriptionInput: di,
		bodyArea:         ba,
		tagsInput:        tg,
		selectedCats:     make(map[int64][]string),
	}
}

// formReadyMsg carries pre-populated data when editing an existing note.
type formReadyMsg struct {
	note       *models.Note
	categories []models.NoteCategoryDetailOutput
}

// noteSavedMsg signals that a create or update completed successfully.
type noteSavedMsg struct {
	noteID int64
}

// categoriesForFormMsg carries the full category list for the picker overlay.
type categoriesForFormMsg struct {
	categories []models.Category
}

// initForm prepares the form for create (noteID=0) or edit (noteID>0).
func (f *formModel) initForm(noteID int64, userGUID string) tea.Cmd {
	f.noteID = noteID
	f.userGUID = userGUID
	f.errMsg = ""
	f.focusedField = fieldTitle
	f.showCategoryPicker = false
	f.selectedCats = make(map[int64][]string)
	f.catCursor = 0
	f.inSubcats = false

	// Reset all inputs
	f.titleInput.SetValue("")
	f.descriptionInput.SetValue("")
	f.bodyArea.SetValue("")
	f.tagsInput.SetValue("")
	f.isPrivate = false
	f.titleInput.Focus()

	if noteID == 0 {
		// Create mode — nothing to load
		return nil
	}

	// Edit mode — load existing note data
	return func() tea.Msg {
		note, err := models.GetNoteByID(noteID, userGUID)
		if err != nil || note == nil {
			return formReadyMsg{note: nil}
		}
		cats, _ := models.GetNoteCategoryDetails(noteID, userGUID)
		return formReadyMsg{note: note, categories: cats}
	}
}

func (f formModel) Update(msg tea.Msg, app *appModel) (formModel, tea.Cmd) {
	switch msg := msg.(type) {
	case formReadyMsg:
		if msg.note != nil {
			// Pre-populate fields from existing note
			f.noteGUID = msg.note.GUID
			f.titleInput.SetValue(msg.note.Title)
			f.descriptionInput.SetValue(nullStr(msg.note.Description))
			f.bodyArea.SetValue(nullStr(msg.note.Body))
			f.tagsInput.SetValue(nullStr(msg.note.Tags))
			f.isPrivate = msg.note.IsPrivate

			// Pre-populate category selections
			for _, cat := range msg.categories {
				f.selectedCats[cat.ID] = cat.SelectedSubcategories
			}
		}
		return f, nil

	case categoriesForFormMsg:
		f.allCategories = msg.categories
		f.showCategoryPicker = true
		f.catCursor = 0
		f.inSubcats = false
		return f, nil

	case noteSavedMsg:
		app.statusMsg = "Note saved"
		app.listModel.clearFilter()
		return f, app.goToNoteList()

	case tea.KeyMsg:
		// Category picker overlay captures all input when visible
		if f.showCategoryPicker {
			return f.updateCategoryPicker(msg, app)
		}

		switch {
		case key.Matches(msg, globalKeys.Back):
			return f, app.goToNoteList()

		case key.Matches(msg, formKeys.Save):
			return f, f.saveNote()

		case key.Matches(msg, formKeys.Categories):
			// Load categories and show the picker overlay
			userGUID := f.userGUID
			return f, func() tea.Msg {
				cats, _ := models.ListCategories(0, 0, userGUID)
				return categoriesForFormMsg{categories: cats}
			}

		case key.Matches(msg, formKeys.NextField):
			f.advanceFocus(1)
			return f, nil

		case key.Matches(msg, formKeys.PrevField):
			f.advanceFocus(-1)
			return f, nil

		case f.focusedField == fieldPrivate && key.Matches(msg, formKeys.ToggleBool):
			f.isPrivate = !f.isPrivate
			return f, nil
		}

		// Forward key to the focused input
		return f.updateFocusedInput(msg)

	default:
		// Forward non-key messages (e.g., blink) to focused input
		return f.updateFocusedInput(msg)
	}
}

// advanceFocus moves focus forward or backward by delta, wrapping at boundaries.
func (f *formModel) advanceFocus(delta int) {
	f.blurAll()
	next := int(f.focusedField) + delta
	if next < 0 {
		next = int(fieldCount) - 1
	} else if next >= int(fieldCount) {
		next = 0
	}
	f.focusedField = formField(next)
	f.focusCurrent()
}

func (f *formModel) blurAll() {
	f.titleInput.Blur()
	f.descriptionInput.Blur()
	f.bodyArea.Blur()
	f.tagsInput.Blur()
}

func (f *formModel) focusCurrent() {
	switch f.focusedField {
	case fieldTitle:
		f.titleInput.Focus()
	case fieldDescription:
		f.descriptionInput.Focus()
	case fieldBody:
		f.bodyArea.Focus()
	case fieldTags:
		f.tagsInput.Focus()
	case fieldPrivate:
		// No text input to focus — just highlight the toggle
	}
}

// updateFocusedInput forwards the message to whichever input has focus.
func (f formModel) updateFocusedInput(msg tea.Msg) (formModel, tea.Cmd) {
	var cmd tea.Cmd
	switch f.focusedField {
	case fieldTitle:
		f.titleInput, cmd = f.titleInput.Update(msg)
	case fieldDescription:
		f.descriptionInput, cmd = f.descriptionInput.Update(msg)
	case fieldBody:
		f.bodyArea, cmd = f.bodyArea.Update(msg)
	case fieldTags:
		f.tagsInput, cmd = f.tagsInput.Update(msg)
	}
	return f, cmd
}

// saveNote validates and persists the note (create or update).
func (f *formModel) saveNote() tea.Cmd {
	title := strings.TrimSpace(f.titleInput.Value())
	if title == "" {
		f.errMsg = "Title is required"
		return nil
	}
	f.errMsg = ""

	desc := strings.TrimSpace(f.descriptionInput.Value())
	body := f.bodyArea.Value()
	tags := strings.TrimSpace(f.tagsInput.Value())
	isPrivate := f.isPrivate
	noteID := f.noteID
	noteGUID := f.noteGUID
	userGUID := f.userGUID
	selectedCats := f.selectedCats

	return func() tea.Msg {
		input := models.NoteInput{
			Title:       title,
			Description: strPtr(desc),
			Body:        strPtr(body),
			Tags:        strPtr(tags),
			IsPrivate:   isPrivate,
		}

		var savedNote *models.Note
		var err error

		if noteID == 0 {
			// Create new note
			input.GUID = uuid.New().String()
			savedNote, err = models.CreateNote(input, userGUID)
		} else {
			// Update existing note — preserve GUID
			input.GUID = noteGUID
			savedNote, err = models.UpdateNote(noteID, input, userGUID)
		}
		if err != nil || savedNote == nil {
			return noteSavedMsg{noteID: 0} // error case, still navigate back
		}

		// Sync category associations:
		// 1. Get current associations
		currentCats, _ := models.GetNoteCategoryDetails(savedNote.ID, userGUID)
		currentCatIDs := make(map[int64]bool)
		for _, c := range currentCats {
			currentCatIDs[c.ID] = true
		}

		// 2. Add new / update existing associations
		for catID, subcats := range selectedCats {
			if currentCatIDs[catID] {
				// Already associated — update subcategories
				models.UpdateNoteCategorySubcategories(savedNote.ID, catID, subcats)
				delete(currentCatIDs, catID)
			} else {
				// New association
				models.AddCategoryToNoteWithSubcategories(savedNote.ID, catID, subcats, userGUID)
			}
		}

		// 3. Remove deselected associations
		for catID := range currentCatIDs {
			models.RemoveCategoryFromNote(savedNote.ID, catID)
		}

		return noteSavedMsg{noteID: savedNote.ID}
	}
}

// --- Category picker overlay ---

// updateCategoryPicker handles input when the category picker is visible.
func (f formModel) updateCategoryPicker(msg tea.KeyMsg, app *appModel) (formModel, tea.Cmd) {
	switch {
	case key.Matches(msg, globalKeys.Back):
		f.showCategoryPicker = false
		return f, nil

	case msg.Type == tea.KeyEnter:
		// Confirm selections and close picker
		f.showCategoryPicker = false
		return f, nil

	case key.Matches(msg, listKeys.Up):
		if f.inSubcats {
			if f.catSubCursor > 0 {
				f.catSubCursor--
			} else {
				f.inSubcats = false
			}
		} else if f.catCursor > 0 {
			f.catCursor--
		}

	case key.Matches(msg, listKeys.Down):
		if f.inSubcats {
			subcats := f.currentCatSubcategories()
			if f.catSubCursor < len(subcats)-1 {
				f.catSubCursor++
			}
		} else if f.catCursor < len(f.allCategories)-1 {
			f.catCursor++
		}

	case msg.Type == tea.KeyRight:
		// Drill into subcategories of the focused category
		subcats := f.currentCatSubcategories()
		if len(subcats) > 0 {
			f.inSubcats = true
			f.catSubCursor = 0
		}

	case msg.Type == tea.KeyLeft:
		f.inSubcats = false

	case key.Matches(msg, formKeys.ToggleBool):
		if f.inSubcats {
			f.toggleSubcategory()
		} else {
			f.toggleCategory()
		}
	}
	return f, nil
}

// currentCatSubcategories returns the parsed subcategories for the focused category.
func (f *formModel) currentCatSubcategories() []string {
	if f.catCursor >= len(f.allCategories) {
		return nil
	}
	cat := f.allCategories[f.catCursor]
	if !cat.Subcategories.Valid || cat.Subcategories.String == "" {
		return nil
	}
	var subcats []string
	json.Unmarshal([]byte(cat.Subcategories.String), &subcats)
	return subcats
}

// toggleCategory adds or removes the focused category from selections.
func (f *formModel) toggleCategory() {
	if f.catCursor >= len(f.allCategories) {
		return
	}
	catID := f.allCategories[f.catCursor].ID
	if _, selected := f.selectedCats[catID]; selected {
		delete(f.selectedCats, catID)
	} else {
		f.selectedCats[catID] = nil
	}
}

// toggleSubcategory adds or removes the focused subcategory within the focused category.
func (f *formModel) toggleSubcategory() {
	if f.catCursor >= len(f.allCategories) {
		return
	}
	cat := f.allCategories[f.catCursor]
	subcats := f.currentCatSubcategories()
	if f.catSubCursor >= len(subcats) {
		return
	}
	subcat := subcats[f.catSubCursor]
	catID := cat.ID

	// Ensure category is selected when a subcategory is toggled on
	existing, catSelected := f.selectedCats[catID]
	if !catSelected {
		f.selectedCats[catID] = []string{subcat}
		return
	}

	// Toggle the subcategory within the existing selection
	found := false
	updated := make([]string, 0, len(existing))
	for _, s := range existing {
		if s == subcat {
			found = true
			continue // remove it
		}
		updated = append(updated, s)
	}
	if !found {
		updated = append(updated, subcat)
	}
	f.selectedCats[catID] = updated
}

func (f formModel) View(width, height int) string {
	if f.showCategoryPicker {
		return f.viewCategoryPicker(width, height)
	}

	var b strings.Builder

	mode := "New Note"
	if f.noteID > 0 {
		mode = "Edit Note"
	}
	b.WriteString(titleStyle.Render("  "+mode) + "\n")

	// Title field
	titleLabel := "Title"
	if f.focusedField == fieldTitle {
		titleLabel = "▸ Title"
	}
	b.WriteString(labelStyle.Render(titleLabel) + "\n")
	b.WriteString(f.titleInput.View() + "\n\n")

	// Description field
	descLabel := "Description"
	if f.focusedField == fieldDescription {
		descLabel = "▸ Description"
	}
	b.WriteString(labelStyle.Render(descLabel) + "\n")
	b.WriteString(f.descriptionInput.View() + "\n\n")

	// Body field
	bodyLabel := "Body"
	if f.focusedField == fieldBody {
		bodyLabel = "▸ Body"
	}
	b.WriteString(labelStyle.Render(bodyLabel) + "\n")
	b.WriteString(f.bodyArea.View() + "\n\n")

	// Tags field
	tagsLabel := "Tags"
	if f.focusedField == fieldTags {
		tagsLabel = "▸ Tags"
	}
	b.WriteString(labelStyle.Render(tagsLabel) + "\n")
	b.WriteString(f.tagsInput.View() + "\n\n")

	// Private toggle
	privLabel := "Private"
	if f.focusedField == fieldPrivate {
		privLabel = "▸ Private"
	}
	checkbox := "[ ]"
	if f.isPrivate {
		checkbox = "[x]"
	}
	b.WriteString(labelStyle.Render(privLabel) + " " + checkbox + "\n\n")

	// Show selected categories summary
	if len(f.selectedCats) > 0 {
		b.WriteString(labelStyle.Render("Categories: "))
		catNames := make([]string, 0, len(f.selectedCats))
		for catID, subcats := range f.selectedCats {
			name := f.categoryName(catID)
			if len(subcats) > 0 {
				name += " (" + strings.Join(subcats, ", ") + ")"
			}
			catNames = append(catNames, name)
		}
		b.WriteString(strings.Join(catNames, " | ") + "\n\n")
	}

	// Error message
	if f.errMsg != "" {
		b.WriteString(errorStyle.Render(f.errMsg) + "\n")
	}

	// Help bar
	b.WriteString(helpStyle.Render(
		"  tab/shift+tab: fields • ctrl+s: save • ctrl+a: categories • esc: cancel",
	))

	return b.String()
}

// categoryName looks up a category name by ID from the loaded list.
func (f *formModel) categoryName(catID int64) string {
	for _, cat := range f.allCategories {
		if cat.ID == catID {
			return cat.Name
		}
	}
	return fmt.Sprintf("#%d", catID)
}

// viewCategoryPicker renders the category/subcategory selection overlay.
func (f formModel) viewCategoryPicker(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("  Select Categories") + "\n")
	b.WriteString(dimStyle.Render("  space: toggle • →: subcategories • ←: back • enter/esc: done") + "\n\n")

	if len(f.allCategories) == 0 {
		b.WriteString(dimStyle.Render("  No categories. Create some first (esc to go back)."))
		return b.String()
	}

	for i, cat := range f.allCategories {
		_, isSelected := f.selectedCats[cat.ID]

		// Category line
		prefix := "  "
		if i == f.catCursor && !f.inSubcats {
			prefix = "▸ "
		}
		checkbox := "[ ]"
		if isSelected {
			checkbox = "[x]"
		}
		catLine := fmt.Sprintf("%s%s %s", prefix, checkbox, cat.Name)
		if i == f.catCursor && !f.inSubcats {
			b.WriteString(selectedStyle.Render(catLine) + "\n")
		} else {
			b.WriteString(catLine + "\n")
		}

		// Show subcategories if this is the focused category and it has subcategories
		if i == f.catCursor {
			subcats := f.currentCatSubcategories()
			selectedSubcats := f.selectedCats[cat.ID]
			for j, sub := range subcats {
				subPrefix := "      "
				if f.inSubcats && j == f.catSubCursor {
					subPrefix = "    ▸ "
				}
				subCheck := "[ ]"
				for _, sel := range selectedSubcats {
					if sel == sub {
						subCheck = "[x]"
						break
					}
				}
				subLine := fmt.Sprintf("%s%s %s", subPrefix, subCheck, sub)
				if f.inSubcats && j == f.catSubCursor {
					b.WriteString(selectedStyle.Render(subLine) + "\n")
				} else {
					b.WriteString(dimStyle.Render(subLine) + "\n")
				}
			}
		}
	}

	return b.String()
}
