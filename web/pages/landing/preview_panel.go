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
				b.Div("class", "preview-meta", "id", "preview-meta").R(
				// Meta information will be populated by JavaScript
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

				// Meta fields
				b.DivClass("edit-meta").R(
					// Tags input
					b.DivClass("edit-field").R(
						b.LabelClass("edit-label", "for", "edit-tags").T("Tags"),
						b.Input("type", "text", "class", "edit-input", "id", "edit-tags",
							"name", "tags", "placeholder", "tag1, tag2, tag3"),
					),
					// Description input
					b.DivClass("edit-field").R(
						b.LabelClass("edit-label", "for", "edit-description").T("Description"),
						b.Input("type", "text", "class", "edit-input", "id", "edit-description",
							"name", "description", "placeholder", "Brief description..."),
					),
					// Category select (populated by JavaScript)
					// Note: Event listener attached in app.js init() to avoid timing issues
					b.DivClass("edit-field").R(
						b.LabelClass("edit-label", "for", "edit-category").T("Category"),
						b.Select("class", "edit-input", "id", "edit-category", "name", "category").R(
							b.Option("value", "").T("Select category..."),
						),
					),
					// Subcategory multi-select (shown when category has subcats defined)
					// Design: Displays available subcats for the selected category as checkboxes
					b.DivClass("edit-field subcat-field", "id", "subcat-field", "style", "display:none").R(
						b.LabelClass("edit-label").T("Subcats"),
						b.DivClass("subcat-select", "id", "subcat-select").R(
						// Subcategory checkboxes populated dynamically by JavaScript
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
						b.Button("type", "button", "class", "btn btn-secondary", "onclick", "app.cancelEdit()").T("Cancel"),
						b.Button("type", "submit", "class", "btn btn-primary", "id", "btn-save").T("Save"),
					),
				),
			),
		),
	)
	return nil
}
