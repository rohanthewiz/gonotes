package tui

import (
	"fmt"
	"gonotes/models"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// detailModel renders a read-only view of a single note with its categories.
// The note body is displayed in a scrollable viewport for long content.
type detailModel struct {
	note       *models.Note
	categories []models.NoteCategoryDetailOutput
	viewport   viewport.Model
	ready      bool // true once the viewport is sized

	// Delete confirmation state
	confirmDelete bool
}

func newDetailModel() detailModel {
	return detailModel{}
}

// noteLoadedMsg carries a fully loaded note + its categories for display.
type noteLoadedMsg struct {
	note       *models.Note
	categories []models.NoteCategoryDetailOutput
}

// loadNote fetches a single note and its category details from the DB.
func (d *detailModel) loadNote(noteID int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		note, err := models.GetNoteByID(noteID, userGUID)
		if err != nil || note == nil {
			return noteLoadedMsg{note: nil}
		}
		cats, _ := models.GetNoteCategoryDetails(noteID, userGUID)
		return noteLoadedMsg{note: note, categories: cats}
	}
}

func (d detailModel) Update(msg tea.Msg, app *appModel) (detailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case noteLoadedMsg:
		d.note = msg.note
		d.categories = msg.categories
		d.confirmDelete = false
		// Rebuild viewport content
		if d.ready {
			d.viewport.SetContent(d.renderContent(app.width))
			d.viewport.GotoTop()
		}
		return d, nil

	case noteDeletedMsg:
		app.statusMsg = "Note deleted"
		return d, app.goToNoteList()

	case tea.WindowSizeMsg:
		if !d.ready {
			// First resize — initialize the viewport
			d.viewport = viewport.New(msg.Width, msg.Height-6)
			d.ready = true
			if d.note != nil {
				d.viewport.SetContent(d.renderContent(msg.Width))
			}
		} else {
			d.viewport.Width = msg.Width
			d.viewport.Height = msg.Height - 6
		}
		return d, nil

	case tea.KeyMsg:
		// Handle delete confirmation
		if d.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				noteID := d.note.ID
				userGUID := app.user.GUID
				d.confirmDelete = false
				return d, func() tea.Msg {
					models.DeleteNote(noteID, userGUID)
					return noteDeletedMsg{}
				}
			default:
				d.confirmDelete = false
				return d, nil
			}
		}

		switch {
		case key.Matches(msg, globalKeys.Quit):
			return d, tea.Quit

		case key.Matches(msg, globalKeys.Back):
			return d, app.goToNoteList()

		case key.Matches(msg, detailKeys.Edit):
			if d.note != nil {
				return d, app.goToNoteForm(d.note.ID)
			}

		case key.Matches(msg, detailKeys.Delete):
			if d.note != nil {
				d.confirmDelete = true
			}
		}

		// Forward remaining keys (j/k, pgup/pgdn) to the viewport for scrolling
		var cmd tea.Cmd
		d.viewport, cmd = d.viewport.Update(msg)
		return d, cmd
	}
	return d, nil
}

func (d detailModel) View(width, height int) string {
	if d.note == nil {
		return dimStyle.Render("  Loading note...")
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("  Note Detail") + "\n")

	if d.ready {
		b.WriteString(d.viewport.View())
	} else {
		b.WriteString(d.renderContent(width))
	}

	b.WriteString("\n")

	if d.confirmDelete {
		b.WriteString(dangerStyle.Render(
			fmt.Sprintf("  Delete \"%s\"? (y/n)", truncate(d.note.Title, 40)),
		))
	} else {
		b.WriteString(helpStyle.Render(
			"  e: edit • d: delete • esc: back to list • ↑/↓: scroll • q: quit",
		))
	}
	return b.String()
}

// renderContent builds the full note body string for the viewport.
func (d detailModel) renderContent(width int) string {
	if d.note == nil {
		return ""
	}

	contentWidth := minInt(width-6, 100)
	var b strings.Builder

	// Title
	b.WriteString(labelStyle.Render("Title: ") + d.note.Title + "\n\n")

	// Description
	if desc := nullStr(d.note.Description); desc != "" {
		b.WriteString(labelStyle.Render("Description: ") + desc + "\n\n")
	}

	// Tags
	if tags := nullStr(d.note.Tags); tags != "" {
		b.WriteString(labelStyle.Render("Tags: "))
		for _, tag := range parseTags(tags) {
			b.WriteString(tagStyle.Render(tag) + " ")
		}
		b.WriteString("\n\n")
	}

	// Categories with subcategories
	if len(d.categories) > 0 {
		b.WriteString(labelStyle.Render("Categories:") + "\n")
		for _, cat := range d.categories {
			b.WriteString("  • " + cat.Name)
			if len(cat.SelectedSubcategories) > 0 {
				b.WriteString(" → " + strings.Join(cat.SelectedSubcategories, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Privacy flag
	if d.note.IsPrivate {
		b.WriteString(highlightStyle.Render("🔒 Private note") + "\n\n")
	}

	// Timestamps
	b.WriteString(dimStyle.Render(fmt.Sprintf("Created: %s  |  Updated: %s",
		d.note.CreatedAt.Format("2006-01-02 15:04"),
		d.note.UpdatedAt.Format("2006-01-02 15:04"),
	)) + "\n\n")

	// Body (main content, separated by a rule)
	b.WriteString(dimStyle.Render(strings.Repeat("─", minInt(contentWidth, 60))) + "\n\n")
	body := nullStr(d.note.Body)
	if body == "" {
		body = dimStyle.Render("(no body)")
	}
	b.WriteString(body + "\n")

	return b.String()
}
