package partials

import (
	"github.com/rohanthewiz/element"
	"gonotes/models"
)

// RenderNotesList renders a list of notes as HTML partial
func RenderNotesList(notes []models.Note) string {
	b := element.NewBuilder()

	b.DivClass("notes-grid").R(
		func() (x any) {
			if len(notes) == 0 {
				b.P().T("No notes found")
			} else {
				for _, note := range notes {
					b.DivClass("note-item").R(
						b.H3().T(note.Title),
						b.P().T("Note content preview"),
					)
				}
			}
			return
		}(),
	)

	return b.String()
}

// RenderRecentNotes renders recent notes as HTML partial
func RenderRecentNotes(notes []models.Note) string {
	b := element.NewBuilder()

	b.DivClass("recent-notes").R(
		b.H2().T("Recent Notes"),
		b.Ul().R(
			func() (x any) {
				for _, note := range notes {
					b.Li().R(
						b.A("href", "/note/"+note.GUID).T(note.Title),
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

	b.DivClass("search-results").R(
		b.H2().F("Search Results for: %s", query),
		b.DivClass("results-list").R(
			func() (x any) {
				if len(notes) == 0 {
					b.P().T("No results found")
				} else {
					for _, note := range notes {
						b.DivClass("result-item").R(
							b.H3().R(
								b.A("href", "/note/"+note.GUID).T(note.Title),
							),
						)
					}
				}
				return
			}(),
		),
	)

	return b.String()
}

// RenderTagsCloud renders a tag cloud as HTML partial
func RenderTagsCloud(tags []string) string {
	b := element.NewBuilder()

	b.DivClass("tags-cloud").R(
		b.H2().T("Tags"),
		b.DivClass("tags").R(
			func() (x any) {
				for _, tag := range tags {
					b.SpanClass("tag").R(
						b.A("href", "/tags/"+tag).T(tag),
					)
					b.T(" ")
				}
				return
			}(),
		),
	)

	return b.String()
}

// RenderNoteForm renders a note editing form partial
func RenderNoteForm(note models.Note, isNew bool) string {
	b := element.NewBuilder()

	action := "/api/note/create"
	if !isNew {
		action = "/api/note/update/" + note.GUID
	}

	b.Form("method", "post", "action", action, "hx-post", action, "hx-target", "#message").R(
		b.DivClass("form-group").R(
			b.Label("for", "title").T("Title"),
			b.Input("type", "text", "id", "title", "name", "title", "value", note.Title, "required"),
		),
		b.DivClass("form-group").R(
			b.Label("for", "body").T("Content"),
			b.TextArea("id", "body", "name", "body").T("Note body here"),
		),
		b.DivClass("form-group").R(
			b.Button("type", "submit").T("Save Note"),
		),
	)

	return b.String()
}

// RenderPagination renders pagination controls
func RenderPagination(currentPage, totalPages int) string {
	b := element.NewBuilder()

	b.DivClass("pagination").R(
		func() (x any) {
			if currentPage > 1 {
				b.A("href", "?page="+string(rune(currentPage-1))).T("Previous")
				b.T(" ")
			}

			b.Span().F("Page %d of %d", currentPage, totalPages)

			if currentPage < totalPages {
				b.T(" ")
				b.A("href", "?page="+string(rune(currentPage+1))).T("Next")
			}
			return
		}(),
	)

	return b.String()
}
