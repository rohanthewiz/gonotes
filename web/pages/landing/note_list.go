package landing

import "github.com/rohanthewiz/element"

// NoteList represents the center panel showing the list of notes
type NoteList struct{}

// Render implements the element.Component interface
func (n NoteList) Render(b *element.Builder) any {
	b.Main("class", "center-panel", "id", "center-panel").R(
		// Batch actions bar (hidden by default)
		b.Div("class", "batch-actions", "id", "batch-actions").R(
			b.ButtonClass("btn", "onclick", "app.deleteSelected()").T("Delete"),
			// "Add", not "Set": existing categories on each note are kept (see
			// app.addCategorySelected for why a bulk replace is the wrong default).
			b.ButtonClass("btn", "onclick", "app.addCategorySelected()",
				"title", "File the selected notes under a category, keeping their current ones").T("Add Category"),
			// A separate, named action rather than a mode of Add: taking a category
			// off many notes should never happen by accident.
			b.ButtonClass("btn", "onclick", "app.removeCategorySelected()",
				"title", "Take a category off the selected notes, keeping their others").T("Remove Category"),
			// Converges a mixed selection: any public → all private, else all public.
			b.ButtonClass("btn", "onclick", "app.togglePrivacySelected()",
				"title", "Make the selected notes private (or public, if they already all are)").T("Toggle Privacy"),
			b.Span("class", "batch-count", "id", "batch-count").T("0 selected"),
		),

		// Filters (flagged, privacy, date, unsynced) — formerly the left sidebar.
		element.RenderComponents(b, FilterBar{}),

		// List header: [☐ select all] [Modified | ▼] NOTES
		//
		// The sort control lives here, beside select-all, rather than in the
		// toolbar: it orders exactly the rows below it, so it reads as a column
		// header the way a file manager's does, and it freed toolbar width for
		// the search controls. Ids (sort-label, sort-menu, sort-dir-icon) are
		// unchanged, so app.setSort / app.cycleSortDir work as before.
		b.DivClass("list-header").R(
			b.Input("type", "checkbox", "class", "select-all-checkbox", "id", "select-all",
				"onchange", "app.toggleSelectAll(this.checked)", "title", "Select all"),
			// Two-part control: the field name opens a picker (Modified /
			// Created / Title); the arrow cycles desc (▼) → asc (▲) → default.
			b.DivClass("sort-control").R(
				b.DivClass("dropdown").R(
					b.ButtonClass("sort-field-btn", "onclick", "app.toggleSortMenu()",
						"title", "Choose sort field").R(
						b.Span("id", "sort-label").T("Modified"),
					),
					// .dropdown-menu-left: the default menu anchors right:0,
					// which at the list's left edge would push it off-screen.
					b.Div("class", "dropdown-menu dropdown-menu-left", "id", "sort-menu").R(
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
			b.SpanClass("list-header-title").T("Notes"),
		),

		// Note list container
		b.Div("class", "note-list", "id", "note-list").R(
			// Initial loading state
			b.Div("class", "empty-state", "id", "loading-state").R(
				// Empty loading spinner div needs R() termination
				b.DivClass("loading-spinner").R(),
				b.P().T("Loading notes..."),
			),
			// Empty state (hidden by default)
			b.Div("class", "empty-state hidden", "id", "empty-state").R(
				b.DivClass("empty-icon").T("📝"),
				b.H3Class("empty-title").T("No notes yet"),
				b.PClass("empty-description").T("Create your first note to get started."),
				b.Button("class", "btn btn-primary", "onclick", "app.newNote()").T("+ New Note"),
			),
			// Notes will be rendered here by JavaScript
		),
	)
	return nil
}

// NoteRowTemplate returns HTML template for a note row (used by JavaScript)
// This is a reference implementation showing the expected structure
func NoteRowTemplate() string {
	return `
<div class="note-row" data-id="{{id}}" onclick="app.selectNote({{id}})">
  <input type="checkbox" class="note-checkbox" onclick="event.stopPropagation(); app.toggleNoteSelection({{id}})" />
  <div class="note-content">
    <div class="note-title-row">
      {{#if is_private}}<span class="note-privacy-icon" title="Private note">🔒</span>{{/if}}
      <span class="note-title">{{title}}</span>
    </div>
    <div class="note-meta">
      {{#if categories}}<span class="note-categories">{{categories}}</span>{{/if}}
    </div>
    <div class="note-preview">{{preview}}</div>
    <div class="note-footer">
      <span class="note-timestamp">{{timestamp}}</span>
      <div class="note-actions">
        <button class="note-action-btn" onclick="event.stopPropagation(); app.previewNote({{id}})" title="Preview">👁</button>
        <button class="note-action-btn" onclick="event.stopPropagation(); app.editNote({{id}})" title="Edit">Edit</button>
      </div>
    </div>
  </div>
</div>
`
}
