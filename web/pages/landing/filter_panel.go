package landing

import "github.com/rohanthewiz/element"

// FilterPanel represents the left panel with search and filter controls
type FilterPanel struct{}

// Render implements the element.Component interface
func (f FilterPanel) Render(b *element.Builder) any {
	b.Aside("class", "left-panel", "id", "filter-panel").R(
		// Search box
		b.DivClass("search-box").R(
			b.DivClass("search-input-wrapper").R(
				b.SpanClass("search-icon").T("🔍"),
				b.Input("type", "text", "class", "search-input", "id", "search-input",
					"placeholder", "Search notes...", "oninput", "app.handleSearch(this.value)"),
				b.ButtonClass("search-clear", "onclick", "app.clearSearch()", "title", "Clear search").T("×"),
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
		b.DivClass("filter-actions").R(
			b.ButtonClass("btn-clear-filters", "onclick", "app.clearAllFilters()").T("Clear All Filters"),
		),
	)
	return nil
}

func (f FilterPanel) renderCategoriesSection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "categories-section").R(
		b.Div("class", "filter-header").R(
			b.Span("class", "filter-title", "onclick", "app.toggleSection('categories-section')").T("Categories"),
			b.Span("class", "filter-header-actions").R(
				b.A("href", "#", "class", "filter-action-link", "onclick", "event.stopPropagation(); app.showCategoryManager(); return false;", "title", "Manage categories").T("Manage"),
			),
			b.Span("class", "filter-toggle", "onclick", "app.toggleSection('categories-section')").T("▼"),
		),
		b.DivClass("filter-content").R(
			b.Div("id", "categories-list").R(
				// Categories will be populated via JavaScript
				b.DivClass("text-muted").T("Loading..."),
			),
		),
	)
}

func (f FilterPanel) renderTagsSection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "tags-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('tags-section')").R(
			b.SpanClass("filter-title").T("Tags"),
			b.SpanClass("filter-toggle").T("▼"),
		),
		b.DivClass("filter-content").R(
			b.Div("id", "tags-list").R(
				// Tags will be populated via JavaScript
				b.DivClass("text-muted").T("Loading..."),
			),
		),
	)
}

func (f FilterPanel) renderPrivacySection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "privacy-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('privacy-section')").R(
			b.SpanClass("filter-title").T("Privacy"),
			b.SpanClass("filter-toggle").T("▼"),
		),
		b.DivClass("filter-content").R(
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "privacy",
					"value", "all", "checked", "checked", "onchange", "app.setPrivacyFilter('all')"),
				b.SpanClass("filter-label").T("All"),
			),
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "privacy",
					"value", "private", "onchange", "app.setPrivacyFilter('private')"),
				b.SpanClass("filter-label").T("Private only"),
			),
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "privacy",
					"value", "public", "onchange", "app.setPrivacyFilter('public')"),
				b.SpanClass("filter-label").T("Public only"),
			),
		),
	)
}

func (f FilterPanel) renderDateSection(b *element.Builder) any {
	return b.Div("class", "filter-section collapsed", "id", "date-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('date-section')").R(
			b.SpanClass("filter-title").T("Date"),
			b.SpanClass("filter-toggle").T("▼"),
		),
		b.DivClass("filter-content").R(
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "all", "checked", "checked", "onchange", "app.setDateFilter('all')"),
				b.SpanClass("filter-label").T("All time"),
			),
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "today", "onchange", "app.setDateFilter('today')"),
				b.SpanClass("filter-label").T("Today"),
			),
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "week", "onchange", "app.setDateFilter('week')"),
				b.SpanClass("filter-label").T("Last 7 days"),
			),
			b.LabelClass("filter-item").R(
				b.Input("type", "radio", "class", "filter-radio", "name", "date",
					"value", "month", "onchange", "app.setDateFilter('month')"),
				b.SpanClass("filter-label").T("Last 30 days"),
			),
		),
	)
}

func (f FilterPanel) renderSyncSection(b *element.Builder) any {
	return b.Div("class", "filter-section collapsed", "id", "sync-section").R(
		b.Div("class", "filter-header", "onclick", "app.toggleSection('sync-section')").R(
			b.SpanClass("filter-title").T("Sync"),
			b.SpanClass("filter-toggle").T("▼"),
		),
		b.DivClass("filter-content").R(
			b.LabelClass("filter-item").R(
				b.Input("type", "checkbox", "class", "filter-checkbox", "id", "filter-unsynced",
					"onchange", "app.toggleUnsyncedFilter(this.checked)"),
				b.SpanClass("filter-label").T("Unsynced only"),
			),
		),
	)
}
