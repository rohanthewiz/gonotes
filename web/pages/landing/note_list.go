package landing

import "github.com/rohanthewiz/element"

// NoteList represents the center panel showing the list of notes
type NoteList struct{}

// Render implements the element.Component interface
func (n NoteList) Render(b *element.Builder) any {
	b.Main("class", "center-panel", "id", "center-panel").R(
		// Batch actions bar (hidden by default)
		b.Div("class", "batch-actions", "id", "batch-actions").R(
			b.Button("class", "btn", "onclick", "app.deleteSelected()").T("Delete"),
			b.Button("class", "btn", "onclick", "app.tagSelected()").T("Add Tags"),
			b.Button("class", "btn", "onclick", "app.categorySelected()").T("Set Category"),
			b.Button("class", "btn", "onclick", "app.togglePrivacySelected()").T("Toggle Privacy"),
			b.Span("class", "batch-count", "id", "batch-count").T("0 selected"),
		),

		// List header
		b.Div("class", "list-header").R(
			b.Input("type", "checkbox", "class", "select-all-checkbox", "id", "select-all",
				"onchange", "app.toggleSelectAll(this.checked)", "title", "Select all"),
			b.Span("class", "list-header-title").T("Notes"),
		),

		// Note list container
		b.Div("class", "note-list", "id", "note-list").R(
			// Initial loading state
			b.Div("class", "empty-state", "id", "loading-state").R(
				b.Div("class", "loading-spinner"),
				b.P().T("Loading notes..."),
			),
			// Empty state (hidden by default)
			b.Div("class", "empty-state hidden", "id", "empty-state").R(
				b.Div("class", "empty-icon").T("📝"),
				b.H3("class", "empty-title").T("No notes yet"),
				b.P("class", "empty-description").T("Create your first note to get started."),
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
      <div class="note-tags">
        {{#each tags}}<span class="note-tag" onclick="event.stopPropagation(); app.filterByTag('{{this}}')">#{{this}}</span>{{/each}}
      </div>
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
