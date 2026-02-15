package landing

import "github.com/rohanthewiz/element"

// FilterPanel represents the left panel with filter controls.
// Search and tags have been moved/removed — search lives in the SearchBar component,
// and tags are replaced by the category/subcategory system.
type FilterPanel struct{}

// Render implements the element.Component interface
func (f FilterPanel) Render(b *element.Builder) any {
	b.Aside("class", "left-panel", "id", "filter-panel").R(
		// Filter sections container
		b.Div("class", "filter-sections", "id", "filter-sections").R(
			// Categories section — only the "Manage" link; filtering is in the search bar
			f.renderCategoriesSection(b),

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

// renderCategoriesSection shows only the "Manage" link for the category manager modal.
// Category filtering has moved to the SearchBar component's dropdown + subcategory chips.
func (f FilterPanel) renderCategoriesSection(b *element.Builder) any {
	return b.Div("class", "filter-section", "id", "categories-section").R(
		b.Div("class", "filter-header").R(
			b.SpanClass("filter-title").T("Categories"),
			b.Span("class", "filter-header-actions").R(
				b.A("href", "#", "class", "filter-action-link",
					"onclick", "event.stopPropagation(); app.showCategoryManager(); return false;",
					"title", "Manage categories").T("Manage"),
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
			// Auto-sync toggle row
			b.Div("class", "sync-control-row").R(
				b.SpanClass("sync-control-label").T("Auto-sync"),
				b.Label("class", "sync-toggle").R(
					b.Input("type", "checkbox", "id", "auto-sync-toggle",
						"onchange", "app.toggleAutoSync(this.checked)"),
					b.Span("class", "slider").R(),
				),
			),
			// Interval selector row
			b.Div("class", "sync-control-row").R(
				b.SpanClass("sync-control-label").T("Interval"),
				b.Select("class", "sync-select", "id", "sync-interval",
					"onchange", "app.setSyncInterval(this.value)").R(
					b.Option("value", "1").T("1 min"),
					b.Option("value", "5", "selected", "selected").T("5 min"),
					b.Option("value", "15").T("15 min"),
					b.Option("value", "30").T("30 min"),
				),
			),
			// Peer URL input row
			b.Div("class", "sync-control-row sync-peer-row").R(
				b.SpanClass("sync-control-label").T("Peer URL"),
				b.Input("type", "text", "class", "sync-peer-input", "id", "sync-peer-url",
					"placeholder", "https://peer:port",
					"onchange", "app.setPeerUrl(this.value)"),
				b.Button("class", "btn btn-secondary btn-sm", "id", "sync-test-btn",
					"onclick", "app.testPeerConnection()").T("Test"),
			),
			// Sync Now button
			b.Div("class", "sync-control-row").R(
				b.Button("class", "btn btn-secondary btn-sm sync-now-btn",
					"onclick", "app.syncNotes()").T("↻ Sync Now"),
			),
			// Sync stats block
			b.Div("class", "sync-stats", "id", "sync-stats").R(
				b.Div("class", "sync-stat-row", "id", "sync-last-time").T("Last sync: Never"),
				b.Div("class", "sync-stat-row").R(
					b.Span("id", "sync-received").T("Received: 0"),
					b.Span("class", "sync-stat-sep").T(" "),
					b.Span("id", "sync-pushed").T("Pushed: 0"),
				),
				b.Div("class", "sync-stat-row", "id", "sync-conflict-row", "style", "display:none").R(
					b.Span("class", "text-warning", "id", "sync-conflict-count").T("Conflicts: 0"),
					b.Button("class", "btn-link text-warning", "onclick", "app.showConflicts()").T("Resolve"),
				),
			),
			// Unsynced only filter
			b.LabelClass("filter-item").R(
				b.Input("type", "checkbox", "class", "filter-checkbox", "id", "filter-unsynced",
					"onchange", "app.toggleUnsyncedFilter(this.checked)"),
				b.SpanClass("filter-label").T("Unsynced only"),
			),
		),
	)
}
