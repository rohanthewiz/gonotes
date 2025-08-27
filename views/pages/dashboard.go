package pages

import (
	"github.com/rohanthewiz/element"
	"gonotes/models"
	"gonotes/views"
)

// RenderDashboard creates the main dashboard page
func RenderDashboard(notes []models.Note, userGUID string) string {
	return views.BaseLayout("", "", views.PageWithHeader{
		UserGUID:   userGUID,
		ActivePage: "dashboard",
		Content: DashboardContent{
			Notes:    notes,
			UserGUID: userGUID,
		},
	})
}

// DashboardContent component for the dashboard
type DashboardContent struct {
	Notes    []models.Note
	UserGUID string
}

func (d DashboardContent) Render(b *element.Builder) (x any) {
	b.DivClass("dashboard").R(
		// Dashboard header
		b.DivClass("dashboard-header").R(
			b.H2().T("Your Notes"),
			b.DivClass("dashboard-stats").R(
				b.Span("class", "stat").F("Total: %d notes", len(d.Notes)),
			),
		),

		// Filter and sort controls
		b.DivClass("controls-bar").R(
			b.DivClass("filter-controls").R(
				b.Select("name", "sort",
					"class", "sort-select",
					"hx-get", "/partials/notes-list",
					"hx-target", "#notes-grid",
					"hx-include", "[name='filter']").R(
					b.Option("value", "updated_desc").T("Recently Updated"),
					b.Option("value", "created_desc").T("Recently Created"),
					b.Option("value", "title_asc").T("Title (A-Z)"),
					b.Option("value", "title_desc").T("Title (Z-A)"),
				),
				b.Input("type", "text",
					"name", "filter",
					"placeholder", "Filter notes...",
					"class", "filter-input",
					"hx-get", "/partials/notes-list",
					"hx-target", "#notes-grid",
					"hx-trigger", "keyup changed delay:300ms",
					"hx-include", "[name='sort']"),
			),
			b.DivClass("view-controls").R(
				b.Button("class", "btn-view active",
					"data-view", "grid",
					"@click", "switchView('grid')").T("Grid"),
				b.Button("class", "btn-view",
					"data-view", "list",
					"@click", "switchView('list')").T("List"),
			),
		),

		// Notes grid/list
		b.Div("id", "notes-grid", "class", "notes-grid").R(
			d.renderNotes(b),
		),

		// Load more button for pagination
		b.DivClass("load-more-container").R(
			b.Button("class", "btn btn-secondary",
				"hx-get", "/partials/notes-list?offset=20",
				"hx-target", "#notes-grid",
				"hx-swap", "beforeend",
				"hx-indicator", "#loading-spinner").T("Load More"),
			b.Div("id", "loading-spinner", "class", "htmx-indicator").T("Loading..."),
		),
	)
	return
}

func (d DashboardContent) renderNotes(b *element.Builder) (x any) {
	if len(d.Notes) == 0 {
		b.DivClass("empty-state").R(
			b.H3().T("No notes yet"),
			b.P().T("Create your first note to get started!"),
			b.A("href", "/notes/new", "class", "btn btn-primary").T("Create New Note"),
		)
		return
	}

	element.ForEach(d.Notes, func(note models.Note) {
		b.Wrap(func() {
			element.RenderComponents(b, RenderNoteCard(note))
		})
	})
	return
}

// RenderNoteCard creates a note card component
func RenderNoteCard(note models.Note) element.Component {
	return NoteCard{Note: note}
}

// NoteCard component
type NoteCard struct {
	Note models.Note
}

func (nc NoteCard) Render(b *element.Builder) (x any) {
	b.Article("class", "note-card",
		"data-note-guid", nc.Note.GUID,
		"hx-get", "/notes/"+nc.Note.GUID,
		"hx-target", "#content-wrapper",
		"hx-push-url", "true").R(

		// Note header
		b.DivClass("note-card-header").R(
			b.H3Class("note-title").T(nc.Note.Title),
			b.DivClass("note-meta").R(
				b.Span("class", "note-date").T(nc.Note.UpdatedAt.Format("Jan 2, 2006")),
				nc.renderPrivateIcon(b),
			),
		),

		// Note preview
		b.DivClass("note-preview").R(
			b.P().T(nc.truncateText(nc.Note.Body.String, 150)),
		),

		// Note tags
		nc.renderTags(b),

		// Note actions
		b.DivClass("note-actions").R(
			b.Button("class", "btn-icon",
				"hx-get", "/notes/"+nc.Note.GUID+"/edit",
				"hx-target", "#content-wrapper",
				"hx-push-url", "true",
				"@click.stop", "").R(
				b.T("✏️"),
			),
			b.Button("class", "btn-icon",
				"hx-delete", "/api/notes/"+nc.Note.GUID,
				"hx-confirm", "Are you sure you want to delete this note?",
				"hx-target", "closest .note-card",
				"hx-swap", "outerHTML",
				"@click.stop", "").R(
				b.T("🗑️"),
			),
		),
	)
	return
}

func (nc NoteCard) renderPrivateIcon(b *element.Builder) (x any) {
	if nc.Note.IsPrivate {
		b.Span("class", "icon-private", "title", "Private Note").T("🔒")
	}
	return
}

func (nc NoteCard) renderTags(b *element.Builder) (x any) {
	if nc.Note.Tags == "" {
		return
	}

	b.DivClass("note-tags").R(
		b.Wrap(func() {
			// Parse tags (comma-separated) and render each
			// This is simplified - in production, tags might be parsed differently
			b.Span("class", "tag").T(nc.Note.Tags)
		}),
	)
	return
}

func (nc NoteCard) truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
