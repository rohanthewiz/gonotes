package partials

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rohanthewiz/element"
	"gonotes/models"
)

// RenderNotesList renders a list of notes as HTML partial
func RenderNotesList(notes []models.Note) string {
	b := element.NewBuilder()

	b.Div("id", "notes-list", "class", "notes-grid").R(
		func() (x any) {
			if len(notes) == 0 {
				b.DivClass("empty-state").R(
					b.P().T("No notes found"),
					b.A("href", "/notes/new", "class", "btn btn-primary").T("Create your first note"),
				)
			} else {
				for _, note := range notes {
					renderNoteCard(b, note)
				}
			}
			return
		}(),
	)

	return b.String()
}

// renderNoteCard renders a single note card
func renderNoteCard(b *element.Builder, note models.Note) {
	// Parse tags from JSON
	var tags []string
	if note.Tags != "" {
		json.Unmarshal([]byte(note.Tags), &tags)
	}

	cardID := fmt.Sprintf("note-card-%s", note.GUID)

	b.Div("id", cardID, "class", "note-card").R(
		b.A("href", fmt.Sprintf("/notes/%s", note.GUID), "class", "note-link").R(
			b.H3("class", "note-title").T(note.Title),
		),

		func() (x any) {
			if note.Description.Valid && note.Description.String != "" {
				b.P("class", "note-description").T(note.Description.String)
			}
			return
		}(),

		b.Div("class", "note-tags").R(
			func() (x any) {
				for _, tag := range tags {
					b.Span("class", "tag").T(tag)
				}
				return
			}(),
		),

		b.Div("class", "note-meta").R(
			b.Span("class", "note-date").T(formatDate(note.UpdatedAt)),
			b.Div("class", "note-actions").R(
				b.A("href", fmt.Sprintf("/notes/%s/edit", note.GUID),
					"class", "btn-icon", "title", "Edit").T("✏️"),
				b.Button("class", "btn-icon",
					"hx-delete", fmt.Sprintf("/api/notes/%s", note.GUID),
					"hx-confirm", "Are you sure you want to delete this note?",
					"hx-target", fmt.Sprintf("#%s", cardID),
					"hx-swap", "outerHTML",
					"title", "Delete").T("🗑️"),
			),
		),
	)
}

// RenderRecentNotes renders recent notes as HTML partial
func RenderRecentNotes(notes []models.Note, limit int) string {
	b := element.NewBuilder()

	if limit > 0 && len(notes) > limit {
		notes = notes[:limit]
	}

	b.Div("id", "recent-notes", "class", "recent-notes").R(
		b.H3().T("Recent Notes"),
		b.Ul("class", "recent-list").R(
			func() (x any) {
				for _, note := range notes {
					b.Li().R(
						b.A("href", fmt.Sprintf("/notes/%s", note.GUID)).R(
							b.Span("class", "recent-title").T(note.Title),
							b.Span("class", "recent-time").T(formatRelativeTime(note.UpdatedAt)),
						),
					)
				}
				return
			}(),
		),
	)

	return b.String()
}

// RenderSearchResults renders search results as HTML partial
func RenderSearchResults(notes []models.Note, query string) string {
	b := element.NewBuilder()

	b.Div("id", "search-results", "class", "search-results").R(
		b.Div("class", "search-summary").F("Found %d results for \"%s\"", len(notes), query),
		b.Div("class", "results-list").R(
			func() (x any) {
				if len(notes) == 0 {
					b.P("class", "no-results").T("No results found")
				} else {
					for _, note := range notes {
						renderSearchResult(b, note, query)
					}
				}
				return
			}(),
		),
	)

	return b.String()
}

// renderSearchResult renders a single search result
func renderSearchResult(b *element.Builder, note models.Note, query string) {
	b.Div("class", "search-result").R(
		b.A("href", fmt.Sprintf("/notes/%s", note.GUID), "class", "result-link").R(
			b.H4("class", "result-title").T(note.Title),

			func() (x any) {
				if note.Body.Valid && note.Body.String != "" {
					excerpt := getExcerpt(note.Body.String, query, 150)
					b.P("class", "result-excerpt").T(excerpt)
				}
				return
			}(),

			b.Span("class", "result-date").T(formatDate(note.UpdatedAt)),
		),
	)
}

// RenderTagsCloud renders a tag cloud as HTML partial
func RenderTagsCloud(tagCounts map[string]int) string {
	b := element.NewBuilder()

	// Calculate max count for sizing
	maxCount := 0
	for _, count := range tagCounts {
		if count > maxCount {
			maxCount = count
		}
	}

	b.Div("id", "tags-cloud", "class", "tags-cloud").R(
		b.H3().T("Tags"),
		b.Div("class", "tags-container").R(
			func() (x any) {
				for tag, count := range tagCounts {
					size := calculateTagSize(count, maxCount)
					b.A("href", fmt.Sprintf("/tags/%s", tag),
						"class", fmt.Sprintf("tag-link tag-size-%d", size),
						"title", fmt.Sprintf("%d notes", count),
						"hx-get", fmt.Sprintf("/api/notes?tag=%s", tag),
						"hx-target", "#notes-list",
						"hx-push-url", "true").R(
						b.Span("class", "tag-name").T(tag),
						b.Span("class", "tag-count").F("(%d)", count),
					)
				}
				return
			}(),
		),
	)

	return b.String()
}

// RenderNoteEditor renders a note editing form partial
func RenderNoteEditor(note *models.Note) string {
	b := element.NewBuilder()

	// Parse tags
	var tags []string
	if note != nil && note.Tags != "" {
		json.Unmarshal([]byte(note.Tags), &tags)
	}

	formAction := "/api/notes"
	method := "post"
	buttonText := "Create Note"

	if note != nil && note.GUID != "" {
		formAction = fmt.Sprintf("/api/notes/%s", note.GUID)
		method = "put"
		buttonText = "Update Note"
	}

	b.Form("id", "note-editor-form",
		"hx-"+method, formAction,
		"hx-target", "#editor-status",
		"hx-swap", "innerHTML",
		"class", "note-editor-form").R(

		b.Div("class", "form-group").R(
			b.Label("for", "title").T("Title"),
			b.Input("type", "text", "id", "title", "name", "title",
				"class", "form-control", "required", "required",
				"value", func() string {
					if note != nil {
						return note.Title
					}
					return ""
				}(),
				"placeholder", "Enter note title"),
		),

		b.Div("class", "form-group").R(
			b.Label("for", "description").T("Description"),
			b.Input("type", "text", "id", "description", "name", "description",
				"class", "form-control",
				"value", func() string {
					if note != nil && note.Description.Valid {
						return note.Description.String
					}
					return ""
				}(),
				"placeholder", "Brief description (optional)"),
		),

		b.Div("class", "form-group").R(
			b.Label("for", "tags").T("Tags"),
			b.Input("type", "text", "id", "tags", "name", "tags",
				"class", "form-control",
				"value", joinTags(tags),
				"placeholder", "Comma-separated tags"),
		),

		b.Div("class", "form-group").R(
			b.Label("for", "body").T("Content"),
			b.TextArea("id", "body", "name", "body",
				"class", "form-control", "rows", "15",
				"placeholder", "Write your note in Markdown...").T(
				func() string {
					if note != nil && note.Body.Valid {
						return note.Body.String
					}
					return ""
				}(),
			),
		),

		b.Div("class", "form-actions").R(
			b.Button("type", "submit", "class", "btn btn-primary").T(buttonText),
			b.A("href", "/", "class", "btn btn-secondary").T("Cancel"),
			b.Div("id", "editor-status", "class", "editor-status").R(),
		),
	)

	return b.String()
}

// RenderNotification renders a notification message
func RenderNotification(msgType, message string) string {
	b := element.NewBuilder()

	b.Div("class", fmt.Sprintf("notification notification-%s", msgType),
		"x-data", "{ show: true }",
		"x-show", "show",
		"x-init", "setTimeout(() => show = false, 5000)").R(
		b.Span("class", "notification-message").T(message),
		b.Button("class", "notification-close", "@click", "show = false").T("×"),
	)

	return b.String()
}

// Helper functions

func formatDate(t time.Time) string {
	return t.Format("Jan 2, 2006")
}

func formatRelativeTime(t time.Time) string {
	duration := time.Since(t)

	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else if duration < 7*24*time.Hour {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	}

	return formatDate(t)
}

func getExcerpt(text, query string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}

	// Try to find query in text and show context around it
	// For now, just return first maxLength characters
	excerpt := text[:maxLength]
	if len(text) > maxLength {
		excerpt += "..."
	}

	return excerpt
}

func calculateTagSize(count, maxCount int) int {
	if maxCount == 0 {
		return 1
	}

	ratio := float64(count) / float64(maxCount)
	if ratio > 0.8 {
		return 5
	} else if ratio > 0.6 {
		return 4
	} else if ratio > 0.4 {
		return 3
	} else if ratio > 0.2 {
		return 2
	}
	return 1
}

func joinTags(tags []string) string {
	result := ""
	for i, tag := range tags {
		if i > 0 {
			result += ", "
		}
		result += tag
	}
	return result
}
