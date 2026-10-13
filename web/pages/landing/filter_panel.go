package landing

import "github.com/rohanthewiz/element"

// FilterBar is the compact row of note filters that sits at the top of the
// note list, directly above the list header:
//
//	[⚑ Flagged] [Privacy: All ▾] [Date: All time ▾] [↻ Unsynced]   Manage categories · Reset
//
// It replaced the left sidebar that used to hold the same controls. The
// sidebar spent a full-height column (220px) on five small controls, most of
// them collapsed by default, so they were both easy to miss and expensive in
// width. As a single wrapping row they are always visible and the note list
// gets that width back. Sync, which also lived in the sidebar, is not a
// filter and moved to the ⚙ panel (see SettingsPanel); only its "Unsynced
// only" filter stayed here.
//
// Privacy and Date were radio groups in the sidebar; here they are <select>s,
// which carry the same one-of-N choice in a fraction of the space. The ids
// (filter-privacy, filter-date) are new; the handlers are unchanged, so
// app.setPrivacyFilter / app.setDateFilter still receive 'all' | 'private' |
// 'public' and 'all' | 'today' | 'week' | 'month'. app.clearAllFilters resets
// the selects by id.
type FilterBar struct{}

// Render implements the element.Component interface
func (f FilterBar) Render(b *element.Builder) any {
	b.Div("class", "filter-bar", "id", "filter-bar").R(
		// Checkbox "chips": the <label> is the visible pill and the click
		// target; the checkbox inside keeps native keyboard/a11y behavior and
		// is styled via :has(:checked) so the pill lights up when it is on.
		b.Label("class", "filter-chip", "title", "Show only flagged notes").R(
			b.Input("type", "checkbox", "id", "filter-flagged",
				"onchange", "app.toggleFlaggedFilter(this.checked)"),
			b.Span("class", "filter-chip-label").R(
				b.SpanClass("flag-icon-red").T("⚑"),
				b.T(" Flagged"),
			),
		),

		b.Label("class", "filter-select-wrap", "title", "Filter by privacy").R(
			b.SpanClass("filter-select-label").T("Privacy"),
			b.Select("class", "filter-select", "id", "filter-privacy",
				"onchange", "app.setPrivacyFilter(this.value)").R(
				b.Option("value", "all", "selected", "selected").T("All"),
				b.Option("value", "private").T("Private"),
				b.Option("value", "public").T("Public"),
			),
		),

		b.Label("class", "filter-select-wrap", "title", "Filter by last modified date").R(
			b.SpanClass("filter-select-label").T("Date"),
			b.Select("class", "filter-select", "id", "filter-date",
				"onchange", "app.setDateFilter(this.value)").R(
				b.Option("value", "all", "selected", "selected").T("All time"),
				b.Option("value", "today").T("Today"),
				b.Option("value", "week").T("Last 7 days"),
				b.Option("value", "month").T("Last 30 days"),
			),
		),

		b.Label("class", "filter-chip", "title", "Show only notes not yet synced").R(
			b.Input("type", "checkbox", "id", "filter-unsynced",
				"onchange", "app.toggleUnsyncedFilter(this.checked)"),
			b.SpanClass("filter-chip-label").T("↻ Unsynced"),
		),

		// Right-aligned actions. "Manage categories" is not a filter, but the
		// category filter itself is the toolbar's dropdown, and this was the
		// link's home in the sidebar, so it stays with the other list controls
		// rather than going into the ⚙ panel where nobody would look for it.
		b.DivClass("filter-bar-actions").R(
			b.Button("type", "button", "class", "filter-bar-link",
				"onclick", "app.showCategoryManager()",
				"title", "Manage categories").T("Manage categories"),
			b.SpanClass("filter-bar-sep").T("·"),
			b.Button("type", "button", "class", "filter-bar-link",
				"onclick", "app.clearAllFilters()",
				"title", "Clear search, category, and every filter").T("Reset"),
		),
	)
	return nil
}
