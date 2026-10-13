package landing

import "github.com/rohanthewiz/element"

// Toolbar is a single-row bar with three visual groups separated by a flex spacer:
//
//	Left:  [🔍 Search] [.* regex] [{ } SQL] [All Categories ▾] [subcats…] [Clear]
//	       --- flexible space ---
//	Right: [✒ new-note] [summarize] [⚙ sync/settings] [☀/☾ theme] [user menu]
//
// Sort moved down to the note list's header (see NoteList), and the other
// filters to the row above it (FilterBar).
type Toolbar struct{}

// Render implements the element.Component interface
func (t Toolbar) Render(b *element.Builder) any {
	b.HeaderClass("toolbar").R(
		// Left group — logo, search, and filter controls pushed to the left edge
		b.DivClass("toolbar-left").R(
			// App logo — inline SVG so it doesn't require a separate file request
			b.A("href", "/", "class", "toolbar-logo", "title", "GoNotes").R(
				b.Text(`<svg width="34" height="34" viewBox="0 0 500 500" xmlns="http://www.w3.org/2000/svg"><rect width="500" height="500" rx="60" fill="#6b9f7b"/><text x="250" y="310" font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif" font-weight="800" font-size="240" fill="white" text-anchor="middle" letter-spacing="-12">GN</text><rect x="175" y="370" width="150" height="26" rx="13" fill="white" fill-opacity="0.85"/></svg>`),
			),
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
			// Advanced query toggle — opens the SQL query bar below the toolbar.
			// It sits beside the regex toggle because the three of them are one
			// escalating scale of the same idea: substring, pattern, then an
			// expression over every attribute. See AdvancedSearchBar.
			b.Button("class", "btn btn-secondary search-bar-sql-toggle", "id", "btn-advanced-search",
				"onclick", "app.toggleAdvancedSearch()",
				"aria-expanded", "false",
				"title", "Advanced query \u2014 SQL over every note attribute (\u2318\u21e7F)").T("{ }"),
			// Category dropdown — populated from state.categories by JS on init
			b.Select("class", "search-bar-select", "id", "search-category-select",
				"onchange", "app.handleCategoryFilter(this.value)").R(
				b.Option("value", "").T("All Categories"),
			),
			// Subcategory chips container — hidden until a category with subcats is chosen
			b.Div("class", "search-bar-subcats", "id", "search-subcats-container").R(),
			// Clear button — resets all search bar state (text, category, subcats)
			b.ButtonClass("btn btn-secondary search-bar-clear", "onclick", "app.clearSearchBar()",
				"title", "Clear search and filters").T("Clear"),
		),

		// Flexible spacer pushes the right group to the far right
		b.DivClass("toolbar-spacer").R(),

		// Right group — new-note, theme, user
		b.DivClass("toolbar-right").R(
			// New Note button — icon-only: a page with a folded corner and a plus badge.
			// The page's right edge is deliberately drawn only down to y=11 and its
			// bottom edge stops at x=10, leaving the lower-right quadrant open so the
			// plus sits in clear space instead of crossing the outline:
			//
			//	┌──────┐        outline: left/top/bottom-left
			//	│ ───  │╲       fold:    triangle at top-right
			//	│ ──   │ ╲
			//	│    ┌─┼─┐      plus:    centered (17.5, 17.5), arms ±3.5
			//	└────┤ + │
			//	     └───┘
			b.Button("class", "btn btn-primary", "id", "btn-new-note", "onclick", "app.newNote()",
				"title", "New Note", "aria-label", "New Note").R(
				b.Text(`<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M13 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h4"/><path d="M13 3l5 5h-5z" fill="currentColor" fill-opacity="0.25"/><path d="M13 3l5 5v3"/><line x1="7.5" y1="10" x2="12" y2="10"/><line x1="7.5" y1="13.5" x2="11" y2="13.5"/><line x1="17.5" y1="14" x2="17.5" y2="21" stroke-width="1.8"/><line x1="14" y1="17.5" x2="21" y2="17.5" stroke-width="1.8"/></svg>`),
			),
			// Summarize the clipboard into a new note. Hidden until summarize.js
			// hears back from GET /api/v1/summarize: the summarizer is the
			// `claude` CLI on the SERVER's machine, so on a host without it this
			// button is a door with nothing behind it. Starting hidden (rather
			// than hiding it after the probe) keeps it from flashing on and off
			// on every page load.
			//
			// The icon is a clipboard with three lines collapsing into one —
			// board, clip at the top, then a long, a shorter, and a short rule:
			//
			//	 ┌─┴─┐        clip
			//	┌┤   ├┐
			//	│ ───  │      long   ← the source text
			//	│ ──   │      medium
			//	│ ─    │      short  ← what it condenses to
			//	└──────┘
			b.Button("class", "btn btn-secondary", "id", "btn-summarize-clip",
				"onclick", "app.summarizeClipboard()", "style", "display:none",
				"title", "Summarize the clipboard into a new note", "aria-label", "Summarize clipboard").R(
				b.Text(`<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M9 4H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2h-2"/><rect x="9" y="2.5" width="6" height="3.5" rx="1" fill="currentColor" fill-opacity="0.25"/><line x1="8.5" y1="10.5" x2="15.5" y2="10.5"/><line x1="8.5" y1="14" x2="13" y2="14"/><line x1="8.5" y1="17.5" x2="11" y2="17.5"/></svg>`),
			),
			// Sync + Settings — opens SettingsPanel on its Sync tab. A gear rather
			// than a sync glyph because the panel holds both, and sync is the tab
			// people reach for most, so it is the one the button lands on.
			b.Button("class", "theme-toggle", "id", "btn-settings-panel",
				"onclick", "app.openSettingsPanel('sync')",
				"title", "Sync and settings", "aria-label", "Sync and settings").R(
				// Inline SVG gear, sized like the neighboring icons; the U+2699
				// text glyph renders noticeably smaller than them in most fonts.
				b.Text(`<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`),
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
					// Opens the same panel as ⚙, on its Settings tab.
					b.Div("class", "dropdown-item", "onclick", "app.openSettingsPanel('settings')").T("Settings"),
					b.DivClass("dropdown-divider").R(),
					b.Div("class", "dropdown-item danger", "onclick", "app.logout()").T("Logout"),
				),
			),
		),
	)
	return nil
}
