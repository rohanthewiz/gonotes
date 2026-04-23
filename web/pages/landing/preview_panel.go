package landing

import "github.com/rohanthewiz/element"

// PreviewPanel represents the right panel for note preview and editing
type PreviewPanel struct{}

// Render implements the element.Component interface
func (p PreviewPanel) Render(b *element.Builder) any {
	b.Aside("class", "right-panel", "id", "right-panel").R(
		// Preview mode
		b.Div("class", "preview-panel", "id", "preview-mode").R(
			// Preview header
			b.DivClass("preview-header").R(
				b.H1("class", "preview-title", "id", "preview-title").T("Select a note"),
				// Description: short subtitle shown beneath the title; hidden when empty
				b.Div("class", "preview-description", "id", "preview-description", "style", "display:none").R(),
				// Meta row: populated meta items on the left, action icons pinned right
				b.DivClass("preview-meta-row").R(
					b.Div("class", "preview-meta", "id", "preview-meta").R(
						// Meta information will be populated by JavaScript
					),
					// Right-aligned icon group: search + focus
					b.DivClass("preview-header-actions").R(
						// In-note text search — toggles a search bar above the preview body.
						b.Button("class", "btn-icon", "id", "btn-search-toggle",
							"onclick", "app.toggleNoteSearch()",
							"title", "Search within this note").R(
							b.Text(`<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><line x1="21" y1="21" x2="16.5" y2="16.5"/></svg>`),
						),
						// Focus-mode toggle — expands the preview panel to full width,
						// collapsing the filter/list panels. A handle on the left edge restores layout.
						b.Button("class", "btn-icon", "id", "btn-focus-mode",
							"onclick", "app.toggleFocusMode()",
							"title", "Toggle focus mode (expand preview)").R(
							b.Text(`<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="6" cy="14" r="4.5"/><circle cx="18" cy="14" r="4.5"/><circle cx="6" cy="14" r="1.8" fill="currentColor" stroke="none"/><circle cx="18" cy="14" r="1.8" fill="currentColor" stroke="none"/><path d="M3 10 L5 4.5 L9 4.5 L10 10"/><path d="M14 10 L15 4.5 L19 4.5 L21 10"/><line x1="10" y1="7" x2="14" y2="7"/></svg>`),
						),
					),
				),
				// Category rows: each row shows a category (bold, colored) followed by
				// its subcategories. Populated dynamically when a note is selected.
				b.Div("class", "preview-categories", "id", "preview-categories").R(),
			),
			// In-note search bar (hidden by default). Toggled by the magnifying-glass
			// button in the preview header; searches text within the rendered note.
			b.Div("class", "note-search-bar", "id", "note-search-bar", "style", "display:none").R(
				b.Input("type", "text", "class", "note-search-input", "id", "note-search-input",
					"placeholder", "Find in note...", "autocomplete", "off"),
				// Case-sensitive toggle
				b.Button("type", "button", "class", "note-search-toggle", "id", "btn-search-case",
					"onclick", "app.toggleNoteSearchCase()", "title", "Match case").T("Aa"),
				// Whole-word toggle
				b.Button("type", "button", "class", "note-search-toggle", "id", "btn-search-word",
					"onclick", "app.toggleNoteSearchWord()", "title", "Whole word").T("W"),
				b.Span("class", "note-search-count", "id", "note-search-count").T(""),
				b.Button("type", "button", "class", "btn-icon", "id", "btn-search-prev",
					"onclick", "app.noteSearchPrev()", "title", "Previous match (Shift+Enter)").R(
					b.Text(`<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="18 15 12 9 6 15"/></svg>`),
				),
				b.Button("type", "button", "class", "btn-icon", "id", "btn-search-next",
					"onclick", "app.noteSearchNext()", "title", "Next match (Enter)").R(
					b.Text(`<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>`),
				),
				b.Button("type", "button", "class", "btn-icon", "id", "btn-search-close",
					"onclick", "app.closeNoteSearch()", "title", "Close (Esc)").R(
					b.Text(`<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>`),
				),
			),
			// Preview body
			b.DivClass("preview-body").R(
				b.Div("class", "markdown-content", "id", "preview-content").R(
					b.PClass("text-muted").T("Select a note from the list to preview its content."),
				),
			),
			// Preview footer
			b.Div("class", "preview-footer", "id", "preview-footer", "style", "display:none").R(
				b.Button("class", "btn btn-primary", "onclick", "app.editCurrentNote()").T("Edit"),
				b.Button("class", "btn btn-secondary", "onclick", "app.duplicateCurrentNote()").T("Duplicate"),
				b.Button("class", "btn btn-secondary text-danger", "onclick", "app.deleteCurrentNote()").T("Delete"),
			),
		),

		// Edit mode (hidden by default)
		b.Div("class", "edit-panel", "id", "edit-mode").R(
			b.Form("class", "edit-form", "id", "edit-form", "onsubmit", "return app.saveNote(event)").R(
				// Hidden field for note ID/GUID
				b.Input("type", "hidden", "id", "edit-id", "name", "id"),
				b.Input("type", "hidden", "id", "edit-guid", "name", "guid"),

				// Title input
				b.DivClass("edit-header").R(
					b.Input("type", "text", "class", "edit-title-input", "id", "edit-title",
						"name", "title", "placeholder", "Note title...", "required", "required"),
				),

				// Meta fields (tags removed — replaced by category/subcategory system)
				b.DivClass("edit-meta").R(
					// Description input
					b.DivClass("edit-field").R(
						b.LabelClass("edit-label", "for", "edit-description").T("Description"),
						b.Input("type", "text", "class", "edit-input", "id", "edit-description",
							"name", "description", "placeholder", "Brief description..."),
					),
					// Multi-category support: container for assigned category entry cards
					// Each card shows the category name, remove button, and subcategory checkboxes
					b.DivClass("edit-field").R(
						b.LabelClass("edit-label").T("Categories"),
						b.Div("class", "category-entries-container", "id", "category-entries-container").R(
							// Category entry cards populated dynamically by JavaScript
						),
						// Add category row: input with datalist + "Add" button
						b.DivClass("category-add-row").R(
							b.Input("type", "text", "class", "edit-input", "id", "edit-category",
								"placeholder", "Type or select category...",
								"list", "category-datalist", "autocomplete", "off"),
							b.DataList("id", "category-datalist").R(
								// Options populated dynamically by JavaScript
							),
							b.SpanClass("new-indicator", "id", "new-category-indicator", "style", "display:none").T("(new)"),
							b.Button("type", "button", "class", "btn btn-secondary btn-sm", "onclick", "app.addCategoryEntry()").T("Add"),
						),
					),
				),

				// Body textarea
				b.DivClass("edit-body-wrapper").R(
					b.TextArea("class", "edit-body", "id", "edit-body", "name", "body",
						"placeholder", "Write your note in Markdown...").R(),
				),

				// Footer with actions
				b.DivClass("edit-footer").R(
					b.LabelClass("privacy-toggle").R(
						b.Input("type", "checkbox", "class", "privacy-checkbox", "id", "edit-private", "name", "is_private"),
						b.Span().T("Private (Encrypt this note)"),
					),
					b.DivClass("edit-actions").R(
						b.Button("type", "button", "class", "btn btn-secondary", "onclick", "app.showLinkNotePopup()").T("Link to Note"),
						b.Button("type", "button", "class", "btn btn-secondary", "onclick", "app.cancelEdit()").T("Cancel"),
						b.Button("type", "submit", "class", "btn btn-primary", "id", "btn-save").T("Save"),
					),
				),
			),
		),
	)
	return nil
}
