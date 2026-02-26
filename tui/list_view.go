package tui

import (
	"fmt"
	"gonotes/models"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

const pageSize = 50

// listModel renders a paginated list of notes with cursor navigation.
// It is the "home" screen and hub for navigating to other screens.
type listModel struct {
	notes    []models.Note
	cursor   int
	offset   int
	total    int // total notes loaded (used for "end of list" detection)
	userGUID string

	// Delete confirmation state
	confirmDelete bool
	deleteTarget  int64  // note ID pending deletion
	deleteTitle   string // note title for the confirmation prompt

	// Filter label (set when returning from search with active filters)
	filterLabel string
}

func newListModel() listModel {
	return listModel{}
}

// notesLoadedMsg carries the result of a notes fetch from the DB.
type notesLoadedMsg struct {
	notes  []models.Note
	offset int
}

// noteDeletedMsg signals that a note was successfully deleted.
type noteDeletedMsg struct{}

// loadNotes fetches a page of notes for the authenticated user.
func (l *listModel) loadNotes(userGUID string) tea.Cmd {
	l.userGUID = userGUID
	offset := l.offset
	return func() tea.Msg {
		notes, err := models.ListNotes(userGUID, pageSize, offset)
		if err != nil {
			return notesLoadedMsg{notes: nil, offset: offset}
		}
		return notesLoadedMsg{notes: notes, offset: offset}
	}
}

func (l listModel) Update(msg tea.Msg, app *appModel) (listModel, tea.Cmd) {
	switch msg := msg.(type) {
	case notesLoadedMsg:
		l.notes = msg.notes
		l.total = len(msg.notes)
		l.offset = msg.offset
		// Keep cursor in bounds after reload
		if l.cursor >= len(l.notes) {
			l.cursor = maxInt(0, len(l.notes)-1)
		}
		return l, nil

	case noteDeletedMsg:
		app.statusMsg = "Note deleted"
		l.confirmDelete = false
		return l, l.loadNotes(l.userGUID)

	case tea.KeyMsg:
		// Handle delete confirmation first — it captures all input until resolved
		if l.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				noteID := l.deleteTarget
				userGUID := l.userGUID
				l.confirmDelete = false
				return l, func() tea.Msg {
					models.DeleteNote(noteID, userGUID)
					return noteDeletedMsg{}
				}
			default:
				// Any other key cancels
				l.confirmDelete = false
				return l, nil
			}
		}

		switch {
		case key.Matches(msg, globalKeys.Quit):
			return l, tea.Quit

		case key.Matches(msg, listKeys.Up):
			if l.cursor > 0 {
				l.cursor--
			}

		case key.Matches(msg, listKeys.Down):
			if l.cursor < len(l.notes)-1 {
				l.cursor++
			}

		case key.Matches(msg, listKeys.Enter):
			if len(l.notes) > 0 {
				noteID := l.notes[l.cursor].ID
				return l, app.goToNoteDetail(noteID)
			}

		case key.Matches(msg, listKeys.New):
			return l, app.goToNoteForm(0)

		case key.Matches(msg, listKeys.Delete):
			if len(l.notes) > 0 {
				l.confirmDelete = true
				l.deleteTarget = l.notes[l.cursor].ID
				l.deleteTitle = l.notes[l.cursor].Title
			}

		case key.Matches(msg, listKeys.Search):
			return l, app.goToSearch()

		case key.Matches(msg, listKeys.Category):
			return l, app.goToCategoryList()

		case key.Matches(msg, listKeys.NextPage):
			// Only advance if we got a full page (there might be more)
			if l.total == pageSize {
				l.offset += pageSize
				l.cursor = 0
				return l, l.loadNotes(l.userGUID)
			}

		case key.Matches(msg, listKeys.PrevPage):
			if l.offset > 0 {
				l.offset -= pageSize
				if l.offset < 0 {
					l.offset = 0
				}
				l.cursor = 0
				return l, l.loadNotes(l.userGUID)
			}
		}
	}
	return l, nil
}

func (l listModel) View(width, height int) string {
	var b strings.Builder

	// Header
	title := titleStyle.Render("  Notes")
	if l.filterLabel != "" {
		title += "  " + highlightStyle.Render("["+l.filterLabel+"]")
	}
	b.WriteString(title + "\n")

	if len(l.notes) == 0 {
		b.WriteString(dimStyle.Render("  No notes found. Press 'n' to create one."))
		b.WriteString("\n\n")
		b.WriteString(l.helpBar())
		return b.String()
	}

	// Calculate available height for the list (reserve lines for header, help, status)
	availableHeight := height - 6
	if availableHeight < 5 {
		availableHeight = 5
	}

	// Render visible notes with cursor indicator
	listWidth := minInt(width-4, 100)
	for i, note := range l.notes {
		if i >= availableHeight {
			break
		}

		prefix := "  "
		style := normalStyle
		if i == l.cursor {
			prefix = "▸ "
			style = selectedStyle
		}

		// Format: "▸ Title                              2024-01-15"
		dateStr := note.CreatedAt.Format("2006-01-02")
		titleMaxLen := listWidth - len(dateStr) - 4
		noteTitle := truncate(note.Title, titleMaxLen)

		// Right-align the date
		padding := listWidth - len(noteTitle) - len(dateStr) - 2
		if padding < 2 {
			padding = 2
		}

		line := fmt.Sprintf("%s%s%s%s",
			prefix,
			style.Render(noteTitle),
			strings.Repeat(" ", padding),
			dimStyle.Render(dateStr),
		)
		b.WriteString(line + "\n")
	}

	// Page indicator
	page := (l.offset / pageSize) + 1
	pageInfo := dimStyle.Render(fmt.Sprintf("\n  Page %d", page))
	b.WriteString(pageInfo + "\n")

	// Delete confirmation or help bar
	if l.confirmDelete {
		b.WriteString("\n" + dangerStyle.Render(
			fmt.Sprintf("  Delete \"%s\"? (y/n)", truncate(l.deleteTitle, 40)),
		))
	} else {
		b.WriteString(l.helpBar())
	}

	return b.String()
}

func (l listModel) helpBar() string {
	return helpStyle.Render(
		"  ↑/k: up • ↓/j: down • enter: view • n: new • d: delete • /: search • c: categories • tab/shift+tab: page • q: quit",
	)
}

// setFilteredNotes replaces the list contents with search/filter results
// and sets a label describing the active filter.
func (l *listModel) setFilteredNotes(notes []models.Note, label string) {
	l.notes = notes
	l.total = len(notes)
	l.offset = 0
	l.cursor = 0
	l.filterLabel = label
}

// clearFilter removes the filter label (returning to normal list mode).
func (l *listModel) clearFilter() {
	l.filterLabel = ""
}
