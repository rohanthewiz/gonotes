package components

import (
	"github.com/rohanthewiz/element"
)

// Sidebar component with navigation and tag filter
type Sidebar struct {
	UserGUID   string
	Tags       []string
	ActivePage string
}

func (s Sidebar) Render(b *element.Builder) (x any) {
	b.Aside("id", "sidebar", 
		"class", "sidebar",
		":class", "{'sidebar-collapsed': !sidebarOpen}").R(
		
		// Toggle button
		b.Button("class", "sidebar-toggle",
			"@click", "sidebarOpen = !sidebarOpen").R(
			b.T(`<svg width="20" height="20" fill="currentColor" viewBox="0 0 20 20">
				<path fill-rule="evenodd" d="M3 5a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zM3 10a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zM3 15a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1z" clip-rule="evenodd"/>
			</svg>`),
		),
		
		// Navigation section
		b.Nav("class", "sidebar-nav").R(
			b.H3Class("sidebar-section-title").T("Navigation"),
			b.UlClass("nav-list").R(
				// Dashboard
				b.Li().R(
					b.A("href", "/",
						"class", s.activeClass("dashboard"),
						"hx-get", "/",
						"hx-target", "#content-wrapper",
						"hx-push-url", "true").T("📊 Dashboard"),
				),
				// All Notes
				b.Li().R(
					b.A("href", "/notes",
						"class", s.activeClass("notes"),
						"hx-get", "/notes",
						"hx-target", "#content-wrapper",
						"hx-push-url", "true").T("📝 All Notes"),
				),
				// Recent
				b.Li().R(
					b.A("href", "/recent",
						"class", s.activeClass("recent"),
						"hx-get", "/recent",
						"hx-target", "#content-wrapper",
						"hx-push-url", "true").T("🕐 Recent"),
				),
				// Search
				b.Li().R(
					b.A("href", "/search",
						"class", s.activeClass("search"),
						"hx-get", "/search",
						"hx-target", "#content-wrapper",
						"hx-push-url", "true").T("🔍 Search"),
				),
			),
		),
		
		// Tags section
		b.DivClass("sidebar-tags").R(
			b.H3Class("sidebar-section-title").T("Tags"),
			b.Div("id", "tags-list",
				"hx-get", "/api/tags",
				"hx-trigger", "load, tagUpdate from:body",
				"class", "tags-container").R(
				// Tags will be loaded via HTMX
				s.renderTags(b),
			),
		),
		
		// Quick actions
		b.DivClass("sidebar-actions").R(
			b.H3Class("sidebar-section-title").T("Actions"),
			b.Button("class", "btn btn-sm btn-secondary",
				"hx-get", "/api/export",
				"hx-swap", "none").T("Export Notes"),
			b.Button("class", "btn btn-sm btn-secondary",
				"@click", "$refs.importFile.click()").T("Import Notes"),
			b.Input("type", "file",
				"x-ref", "importFile",
				"style", "display: none",
				"accept", ".json,.md",
				"@change", "handleImport($event)"),
		),
	)
	return
}

func (s Sidebar) activeClass(page string) string {
	if s.ActivePage == page {
		return "nav-link active"
	}
	return "nav-link"
}

func (s Sidebar) renderTags(b *element.Builder) (x any) {
	if len(s.Tags) == 0 {
		b.P("class", "no-tags").T("No tags yet")
		return
	}
	
	b.UlClass("tag-list").R(
		element.ForEach(s.Tags, func(tag string) {
			b.Li().R(
				b.A("href", "/tags/"+tag,
					"class", "tag-link",
					"hx-get", "/api/tags/"+tag+"/notes",
					"hx-target", "#content-wrapper",
					"hx-push-url", "true").R(
					b.Span("class", "tag-name").T(tag),
					// Tag count could be added here
				),
			)
		}),
	)
	return
}