package views

import (
	"github.com/rohanthewiz/element"
	"gonotes/views/components"
)

// BaseLayout creates the base HTML structure for all pages
// Takes CSS styles, additional head content, and a body component
func BaseLayout(styles string, headContent string, bodyComponent element.Component) string {
	b := element.NewBuilder()

	b.Html().R(
		b.Head().R(
			b.Meta("charset", "UTF-8"),
			b.Meta("viewport", "width=device-width, initial-scale=1.0"),
			b.Title().T("GoNotes Web"),

			// External libraries from CDN (can be changed to local vendor files)
			b.Link("rel", "stylesheet", "href", "/static/css/main.css"),
			b.Link("rel", "stylesheet", "href", "/static/css/editor.css"),
			b.Link("rel", "stylesheet", "href", "/static/vendor/monaco/editor/editor.main.css"),

			// Alpine.js and HTMX for interactivity
			b.Script("src", "/static/vendor/alpine.min.js", "defer").R(),
			b.Script("src", "/static/vendor/htmx.min.js").R(),
			b.Script("src", "/static/vendor/msgpack.min.js").R(),

			// Monaco Editor loader
			b.Script("src", "/static/vendor/monaco/loader.js").R(),

			// Custom styles if provided
			b.Wrap(func() {
				if styles != "" {
					b.Style().T(styles)
				}
			}),

			// Additional head content if provided
			b.Wrap(func() {
				if headContent != "" {
					b.T(headContent)
				}
			}),
		),
		b.Body("x-data", "{sidebarOpen: true}").R(
			element.RenderComponents(b, bodyComponent),

			// Main application JavaScript
			b.Script("src", "/static/js/app.js").R(),

			// SSE connection for real-time updates
			b.Script().T(`
				// Establish SSE connection for real-time updates
				if (typeof(EventSource) !== "undefined") {
					const evtSource = new EventSource("/events");
					evtSource.onmessage = function(event) {
						console.log("SSE message:", event.data);
						// Handle real-time updates here
						if (window.handleSSEUpdate) {
							window.handleSSEUpdate(event.data);
						}
					};
					evtSource.onerror = function(err) {
						console.error("SSE error:", err);
					};
				}
			`),
		),
	)

	return b.String()
}

// SimpleLayout creates a minimal HTML layout without the full navigation
// Useful for login pages, error pages, etc.
func SimpleLayout(title string, content element.Component) string {
	b := element.NewBuilder()

	b.Html().R(
		b.Head().R(
			b.Meta("charset", "UTF-8"),
			b.Meta("viewport", "width=device-width, initial-scale=1.0"),
			b.Title().T(title),
			b.Link("rel", "stylesheet", "href", "/static/css/main.css"),
		),
		b.Body().R(
			element.RenderComponents(b, content),
		),
	)

	return b.String()
}

// PageWithHeader creates a page with the standard header and sidebar
type PageWithHeader struct {
	UserGUID   string
	Content    element.Component
	ActivePage string
}

func (p PageWithHeader) Render(b *element.Builder) (x any) {
	// Main container with flex layout
	b.DivClass("app-container").R(
		// Sidebar
		element.RenderComponents(b, components.Sidebar{
			UserGUID:   p.UserGUID,
			ActivePage: p.ActivePage,
		}),

		// Main content area
		b.DivClass("main-content").R(
			// Header
			element.RenderComponents(b, components.Header{
				UserGUID: p.UserGUID,
			}),

			// Content
			b.DivClass("content-wrapper").R(
				element.RenderComponents(b, p.Content),
			),
		),
	)
	return
}
