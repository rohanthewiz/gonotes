package landing

import "github.com/rohanthewiz/element"

// Page represents the main landing page with the three-pane layout
type Page struct {
	Title string
}

// NewPage creates a new landing page instance
func NewPage() Page {
	return Page{
		Title: "GoNotes - Your Knowledge Base",
	}
}

// Render generates the complete HTML for the landing page
func (p Page) Render() string {
	b := element.NewBuilder()

	// HTML document structure
	b.Html("lang", "en").R(
		p.renderHead(b),
		p.renderBody(b),
	)

	return b.String()
}

func (p Page) renderHead(b *element.Builder) any {
	return b.Head().R(
		b.Meta("charset", "UTF-8"),
		b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1.0"),
		b.Title().T(p.Title),
		// CSS
		b.Link("rel", "stylesheet", "href", "/static/css/app.css"),
		// Marked.js for Markdown rendering
		b.Script("src", "https://cdn.jsdelivr.net/npm/marked/marked.min.js").R(),
		// DOMPurify for XSS prevention
		b.Script("src", "https://cdn.jsdelivr.net/npm/dompurify@3.0.6/dist/purify.min.js").R(),
	)
}

func (p Page) renderBody(b *element.Builder) any {
	return b.Body().R(
		// Main app container
		b.Div("class", "app-container", "id", "app").R(
			// Top toolbar
			element.RenderComponents(b, Toolbar{}),

			// Main content area with three panes
			b.DivClass("app-main").R(
				element.RenderComponents(b,
					FilterPanel{},
					NoteList{},
					PreviewPanel{},
				),
			),

			// Bottom status bar
			element.RenderComponents(b, StatusBar{}),
		),

		// Toast notifications container - empty div needs R() termination
		b.Div("class", "toast-container", "id", "toast-container").R(),

		// Modal overlay
		b.Div("class", "modal-overlay", "id", "modal-overlay", "onclick", "app.closeModal()").R(
			b.Div("class", "modal", "id", "modal", "onclick", "event.stopPropagation()").R(
				b.DivClass("modal-header").R(
					b.H2("class", "modal-title", "id", "modal-title").T("Modal"),
					b.ButtonClass("modal-close", "onclick", "app.closeModal()").T("×"),
				),
				// Empty modal body container needs R() termination
				b.Div("class", "modal-body", "id", "modal-body").R(),
				b.Div("class", "modal-footer", "id", "modal-footer").R(
					b.Button("class", "btn btn-secondary", "onclick", "app.closeModal()").T("Cancel"),
					b.Button("class", "btn btn-primary", "id", "modal-confirm", "onclick", "app.confirmModal()").T("Confirm"),
				),
			),
		),

		// Application JavaScript
		b.Script("src", "/static/js/app.js").R(),
	)
}
