package landing

import "github.com/rohanthewiz/element"

// Toolbar is a single-row bar with three visual groups separated by a flex spacer:
//
//	Left:  [🔍 Search] [.* regex] [All Categories ▾] [Sort ▾] [subcats…] [Clear]
//	       --- flexible space ---
//	Right: [All Notes (n)] [+ New Note] [☀/☾ theme] [↻ sync] [user menu]
type Toolbar struct{}

// Render implements the element.Component interface
func (t Toolbar) Render(b *element.Builder) any {
	b.HeaderClass("toolbar").R(
		// Left group — search and filter controls pushed to the left edge
		b.DivClass("toolbar-left").R(
			// Search input — reuses id="search-input" so "/" shortcut keeps working
			b.DivClass("search-bar-input-wrapper").R(
				b.SpanClass("search-bar-icon").T("🔍"),
				b.Input("type", "text", "class", "search-bar-input", "id", "search-input",
					"placeholder", "Search by text or ID...",
					"oninput", "app.handleSearch(this.value)",
					"autocomplete", "off"),
			),
			// Regex toggle — switches between substring and regular expression mode
			b.Button("class", "btn btn-secondary search-bar-regex-toggle", "id", "regex-toggle",
				"onclick", "app.toggleRegex()",
				"title", "Toggle regular expression search").T(".*"),
			// Category dropdown — populated from state.categories by JS on init
			b.Select("class", "search-bar-select", "id", "search-category-select",
				"onchange", "app.handleCategoryFilter(this.value)").R(
				b.Option("value", "").T("All Categories"),
			),
			// Sort control — two-part: column name opens dropdown, arrow cycles direction
			// Clicking column name: shows field picker (Modified / Created / Title)
			// Clicking arrow: cycles desc (▼) → asc (▲) → off (—)
			b.DivClass("sort-control").R(
				b.DivClass("dropdown").R(
					b.ButtonClass("sort-field-btn", "onclick", "app.toggleSortMenu()",
						"title", "Choose sort field").R(
						b.Span("id", "sort-label").T("Modified"),
					),
					b.Div("class", "dropdown-menu", "id", "sort-menu").R(
						b.Div("class", "dropdown-item", "data-sort", "updated_at", "onclick", "app.setSort('updated_at')").T("Modified"),
						b.Div("class", "dropdown-item", "data-sort", "created_at", "onclick", "app.setSort('created_at')").T("Created"),
						b.Div("class", "dropdown-item", "data-sort", "title", "onclick", "app.setSort('title')").T("Title"),
					),
				),
				b.Button("class", "sort-dir-btn", "id", "sort-dir-btn",
					"onclick", "app.cycleSortDir()",
					"title", "Toggle sort direction").R(
					b.Span("id", "sort-dir-icon").T("▼"),
				),
			),
			// Subcategory chips container — hidden until a category with subcats is chosen
			b.Div("class", "search-bar-subcats", "id", "search-subcats-container").R(),
			// Clear button — resets all search bar state (text, category, subcats)
			b.ButtonClass("btn btn-secondary search-bar-clear", "onclick", "app.clearSearchBar()",
				"title", "Clear search and filters").T("Clear"),
		),

		// Flexible spacer pushes the right group to the far right
		b.DivClass("toolbar-spacer").R(),

		// Right group — new-note, sync, theme, user
		b.DivClass("toolbar-right").R(
			// New Note button
			b.Button("class", "btn btn-primary", "id", "btn-new-note", "onclick", "app.newNote()").R(
				b.Span().T("+"),
				b.Span().T(" New Note"),
			),
			// Sync button
			b.Button("class", "btn-icon", "id", "btn-sync", "onclick", "app.syncNotes()", "title", "Sync notes").R(
				b.Span("id", "sync-icon").T("↻"),
			),
			// Theme toggle — just left of user menu
			b.Button("class", "theme-toggle", "id", "btn-theme-toggle", "onclick", "app.toggleTheme()", "title", "Toggle theme").T("\u2600"),
			// Init toggle icon based on current theme
			b.Script().T(`(function(){var t=localStorage.getItem('gonotes-theme')||'dark-green';var b=document.getElementById('btn-theme-toggle');if(b)b.textContent=t==='dark-green'?'\u2600':'\u263E';})()`),
			// User menu — rightmost element
			b.DivClass("dropdown").R(
				b.ButtonClass("user-menu", "onclick", "app.toggleUserMenu()").R(
					b.Div("class", "user-avatar", "id", "user-avatar").T("?"),
					b.Span("id", "username-display").T(""),
				),
				b.Div("class", "dropdown-menu", "id", "user-menu").R(
					b.Div("class", "dropdown-item", "onclick", "app.showSettings()").T("Settings"),
					b.DivClass("dropdown-divider").R(),
					b.Div("class", "dropdown-item danger", "onclick", "app.logout()").T("Logout"),
				),
			),
		),
	)
	return nil
}
