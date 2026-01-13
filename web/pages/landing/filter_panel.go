package landing

import "github.com/rohanthewiz/element"

// FilterPanel represents the left panel with search and filter controls
type FilterPanel struct{}

// Render implements the element.Component interface
func (f FilterPanel) Render(b *element.Builder) any {
	b.Aside("class", "left-panel", "id", "filter-panel").R(
		// Search box
		b.Div("class", "search-box").R(
			b.Div("class", "search-input-wrapper").R(
				b.Span("class", "search-icon").T("🔍"),
				b.Input("type", "text", "class", "search-input", "id", "search-input",
					"placeholder", "Search notes...", "oninput", "app.handleSearch(this.value)"),
				b.Button("class", "search-clear", "onclick", "app.clearSearch()", "title", "Clear search").T("×"),
			),
		),

		// Filter sections container
		b.Div("class", "filter-sections", "id", "filter-sections").R(
			// Categories section
			f.renderCategoriesSection(b),

			// Tags section
			f.renderTagsSection(b),

			// Privacy section
			f.renderPrivacySection(b),

			// Date section
			f.renderDateSection(b),

			// Sync status section
			f.renderSyncSection(b),
		),

		// Clear filters button
		b.Div("class", "filter-actions").R(
			b.Button("class", "btn-clear-filters", "onclick", "app.clearAllFilters()").T("Clear All Filters"),
		),
	)
	return nil
}

func (f FilterPanel) renderCategoriesSection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "categories-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('categories-section')").R(
			b.Span("class", "filter-title").T("Categories"),
			b.Span("class", "filter-toggle").T("▼"),
		),
		b.Div("class", "filter-content").R(
			b.Div("id", "categories-list").R(
				// Categories will be populated via JavaScript
				b.Div("class", "text-muted").T("Loading..."),
			),
		),
	)
}

func (f FilterPanel) renderTagsSection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "tags-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('tags-section')").R(
			b.Span("class", "filter-title").T("Tags"),
			b.Span("class", "filter-toggle").T("▼"),
		),
		b.Div("class", "filter-content").R(
			b.Div("id", "tags-list").R(
				// Tags will be populated via JavaScript
				b.Div("class", "text-muted").T("Loading..."),
			),
		),
	)
}

func (f FilterPanel) renderPrivacySection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "privacy-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('privacy-section')").R(
			b.Span("class", "filter-title").T("Privacy"),
			b.Span("class", "filter-toggle").T("▼"),
		),
		b.Div("class", "filter-content").R(
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "privacy",
					"value", "all", "checked", "checked", "onchange", "app.setPrivacyFilter('all')"),
				b.Span("class", "filter-label").T("All"),
			),
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "privacy",
					"value", "private", "onchange", "app.setPrivacyFilter('private')"),
				b.Span("class", "filter-label").T("Private only"),
			),
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "privacy",
					"value", "public", "onchange", "app.setPrivacyFilter('public')"),
				b.Span("class", "filter-label").T("Public only"),
			),
		),
	)
}

func (f FilterPanel) renderDateSection(b *element.Builder) any {
	return b.Div("class", "filter-section collapsed", "id", "date-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('date-section')").R(
			b.Span("class", "filter-title").T("Date"),
			b.Span("class", "filter-toggle").T("▼"),
		),
		b.Div("class", "filter-content").R(
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "all", "checked", "checked", "onchange", "app.setDateFilter('all')"),
				b.Span("class", "filter-label").T("All time"),
			),
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "today", "onchange", "app.setDateFilter('today')"),
				b.Span("class", "filter-label").T("Today"),
			),
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "week", "onchange", "app.setDateFilter('week')"),
				b.Span("class", "filter-label").T("Last 7 days"),
			),
			b.Label("class", "filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "month", "onchange", "app.setDateFilter('month')"),
				b.Span("class", "filter-label").T("Last 30 days"),
			),
		),
	)
}

func (f FilterPanel) renderSyncSection(b *element.Builder) any {
	return b.Div("class", "filter-section collapsed", "id", "sync-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('sync-section')").R(
			b.Span("class", "filter-title").T("Sync"),
			b.Span("class", "filter-toggle").T("▼"),
		),
		b.Div("class", "filter-content").R(
			b.Label("class", "filter-item").R(
				b.Input("type", "checkbox", "class", "filter-checkbox", "id", "filter-unsynced",
					"onchange", "app.toggleUnsyncedFilter(this.checked)"),
				b.Span("class", "filter-label").T("Unsynced only"),
			),
		),
	)
}
