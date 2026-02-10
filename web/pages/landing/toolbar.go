package landing

import "github.com/rohanthewiz/element"

// Toolbar represents the top navigation bar with actions
type Toolbar struct{}

// Render implements the element.Component interface
func (t Toolbar) Render(b *element.Builder) any {
	b.HeaderClass("toolbar").R(
		// Left section
		b.DivClass("toolbar-left").R(
			// New Note button
			b.Button("class", "btn btn-primary", "id", "btn-new-note", "onclick", "app.newNote()").R(
				b.Span().T("+"),
				b.Span().T(" New Note"),
			),
			// View indicator
			b.DivClass("view-indicator").R(
				b.Span("id", "view-title").T("All Notes"),
				b.Span("class", "view-count", "id", "view-count").T(""),
			),
		),

		// Right section
		b.DivClass("toolbar-right").R(
			// Theme toggle
			b.Button("class", "theme-toggle", "id", "btn-theme-toggle", "onclick", "app.toggleTheme()", "title", "Toggle theme").T("\u2600"),
			// Init toggle icon based on current theme
			b.Script().T(`(function(){var t=localStorage.getItem('gonotes-theme')||'dark-green';var b=document.getElementById('btn-theme-toggle');if(b)b.textContent=t==='dark-green'?'\u2600':'\u263E';})()`),
			// Sort dropdown
			b.DivClass("dropdown").R(
				b.ButtonClass("sort-dropdown", "onclick", "app.toggleSortMenu()").R(
					b.Span().T("Sort: "),
					b.Span("id", "sort-label").T("Modified"),
					b.Span().T(" ▾"),
				),
				b.Div("class", "dropdown-menu", "id", "sort-menu").R(
					b.Div("class", "dropdown-item", "data-sort", "updated_at", "onclick", "app.setSort('updated_at')").T("Modified"),
					b.Div("class", "dropdown-item", "data-sort", "created_at", "onclick", "app.setSort('created_at')").T("Created"),
					b.Div("class", "dropdown-item", "data-sort", "title", "onclick", "app.setSort('title')").T("Title"),
				),
			),
			// Sync button
			b.Button("class", "btn-icon", "id", "btn-sync", "onclick", "app.syncNotes()", "title", "Sync notes").R(
				b.Span("id", "sync-icon").T("↻"),
			),
			// User menu
			b.DivClass("dropdown").R(
				b.ButtonClass("user-menu", "onclick", "app.toggleUserMenu()").R(
					b.Div("class", "user-avatar", "id", "user-avatar").T("?"),
					b.Span("id", "username-display").T(""),
				),
				b.Div("class", "dropdown-menu", "id", "user-menu").R(
					b.Div("class", "dropdown-item", "onclick", "app.showSettings()").T("Settings"),
					// Empty divider needs R() termination
					b.DivClass("dropdown-divider").R(),
					b.Div("class", "dropdown-item danger", "onclick", "app.logout()").T("Logout"),
				),
			),
		),
	)
	return nil
}
