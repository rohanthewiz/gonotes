package components

import (
	"github.com/rohanthewiz/element"
)

// Header component for the application
type Header struct {
	UserGUID string
}

func (h Header) Render(b *element.Builder) (x any) {
	b.Header("id", "main-header").R(
		b.DivClass("header-content").R(
			// Logo and title
			b.DivClass("header-left").R(
				b.H1Class("app-title").R(
					b.A("href", "/").T("GoNotes"),
				),
			),
			
			// Search bar
			b.DivClass("header-center").R(
				b.Form("hx-get", "/api/search", 
					"hx-target", "#search-results",
					"hx-trigger", "input changed delay:300ms, submit",
					"class", "search-form").R(
					b.Input("type", "search",
						"name", "q",
						"placeholder", "Search notes...",
						"class", "search-input",
						"onkeydown", "if(event.key==='Escape') this.value=''"),
				),
			),
			
			// User menu and actions
			b.DivClass("header-right").R(
				// New note button
				b.Button("class", "btn btn-primary",
					"hx-get", "/notes/new",
					"hx-target", "#content-wrapper",
					"hx-push-url", "true").R(
					b.Span().T("+ New Note"),
				),
				
				// Settings/preferences
				b.Button("class", "btn btn-icon",
					"onclick", "showPreferences()",
					"title", "Settings").R(
					// Settings icon SVG
					b.T(`<svg width="20" height="20" fill="currentColor" viewBox="0 0 20 20">
						<path d="M10 12a2 2 0 100-4 2 2 0 000 4z"/>
						<path fill-rule="evenodd" d="M.458 10C1.732 5.943 5.522 3 10 3s8.268 2.943 9.542 7c-1.274 4.057-5.064 7-9.542 7S1.732 14.057.458 10zM14 10a4 4 0 11-8 0 4 4 0 018 0z" clip-rule="evenodd"/>
					</svg>`),
				),
				
				// User info
				b.DivClass("user-info").R(
					b.Span("class", "user-guid").T(h.UserGUID[:8] + "..."),
				),
			),
		),
	)
	return
}