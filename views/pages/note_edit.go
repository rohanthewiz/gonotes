package pages

import (
	"github.com/rohanthewiz/element"
	"go_notes_web/models"
	"go_notes_web/views"
)

// RenderNoteEdit renders the note editor page
func RenderNoteEdit(note *models.Note, userGUID string) string {
	// Add Monaco Editor specific styles
	monacoStyles := `
		#editor-container {
			height: 500px;
			border: 1px solid #ddd;
			border-radius: 4px;
		}
		.editor-toolbar {
			display: flex;
			gap: 10px;
			margin-bottom: 10px;
		}
	`
	
	return views.BaseLayout(monacoStyles, "", views.PageWithHeader{
		UserGUID: userGUID,
		ActivePage: "notes",
		Content: NoteEditContent{
			Note: note,
			IsNew: note == nil,
		},
	})
}

// NoteEditContent component for editing a note
type NoteEditContent struct {
	Note  *models.Note
	IsNew bool
}

func (ne NoteEditContent) Render(b *element.Builder) (x any) {
	// Determine form action and method
	formAction := "/api/notes"
	formMethod := "post"
	noteGUID := ""
	noteTitle := ""
	noteBody := ""
	noteTags := ""
	isPrivate := false
	
	if !ne.IsNew && ne.Note != nil {
		formAction = "/api/notes/" + ne.Note.GUID
		formMethod = "put"
		noteGUID = ne.Note.GUID
		noteTitle = ne.Note.Title
		if ne.Note.Body.Valid {
			noteBody = ne.Note.Body.String
		}
		noteTags = ne.Note.Tags
		isPrivate = ne.Note.IsPrivate
	}
	
	b.DivClass("note-editor").R(
		// Editor header
		b.DivClass("editor-header").R(
			b.H2().T(ne.getPageTitle()),
		),
		
		// Editor form
		b.Form("id", "note-form",
			"hx-"+formMethod, formAction,
			"hx-trigger", "submit",
			"hx-target", "body",
			"hx-swap", "innerHTML").R(
			
			// Title input
			b.DivClass("form-group").R(
				b.Label("for", "title").T("Title"),
				b.Input("type", "text",
					"id", "title",
					"name", "title",
					"class", "form-control",
					"placeholder", "Enter note title...",
					"value", noteTitle,
					"required", "required"),
			),
			
			// Tags input
			b.DivClass("form-group").R(
				b.Label("for", "tags").T("Tags (comma-separated)"),
				b.Input("type", "text",
					"id", "tags",
					"name", "tags",
					"class", "form-control",
					"placeholder", "e.g., work, personal, ideas",
					"value", noteTags),
			),
			
			// Privacy toggle
			b.DivClass("form-group").R(
				b.Label("class", "checkbox-label").R(
					b.Input("type", "checkbox",
						"id", "is_private",
						"name", "is_private",
						"value", "true",
						"checked", ne.checkedAttr(isPrivate)),
					b.T(" Private note (encrypted)"),
				),
			),
			
			// Monaco Editor container
			b.DivClass("form-group").R(
				b.Label("for", "editor").T("Content (Markdown)"),
				b.DivClass("editor-toolbar").R(
					b.Button("type", "button", "class", "btn btn-sm",
						"onclick", "insertMarkdown('**', '**')").T("Bold"),
					b.Button("type", "button", "class", "btn btn-sm",
						"onclick", "insertMarkdown('*', '*')").T("Italic"),
					b.Button("type", "button", "class", "btn btn-sm",
						"onclick", "insertMarkdown('`', '`')").T("Code"),
					b.Button("type", "button", "class", "btn btn-sm",
						"onclick", "insertMarkdown('\\n## ', '')").T("Heading"),
					b.Button("type", "button", "class", "btn btn-sm",
						"onclick", "insertMarkdown('\\n- ', '')").T("List"),
				),
				b.Div("id", "editor-container").R(),
				// Hidden textarea to submit the content
				b.TextArea("id", "body",
					"name", "body",
					"style", "display: none").T(noteBody),
			),
			
			// Action buttons
			b.DivClass("form-actions").R(
				b.Button("type", "submit", "class", "btn btn-primary").T("Save Note"),
				b.Button("type", "button", "class", "btn btn-secondary",
					"onclick", "saveDraft()").T("Save Draft"),
				b.A("href", ne.getCancelUrl(),
					"class", "btn btn-link").T("Cancel"),
			),
			
			// Hidden field for GUID if editing
			ne.renderGuidField(b, noteGUID),
		),
		
		// Monaco Editor initialization script
		b.Script().T(`
			// Initialize Monaco Editor
			require.config({ paths: { 'vs': '/static/vendor/monaco/min/vs' }});
			require(['vs/editor/editor.main'], function() {
				window.monacoEditor = monaco.editor.create(document.getElementById('editor-container'), {
					value: document.getElementById('body').value,
					language: 'markdown',
					theme: 'vs-light',
					minimap: { enabled: false },
					wordWrap: 'on',
					lineNumbers: 'on',
					fontSize: 14,
					automaticLayout: true
				});
				
				// Auto-save functionality
				let autoSaveTimeout;
				window.monacoEditor.onDidChangeModelContent(() => {
					// Update hidden textarea
					document.getElementById('body').value = window.monacoEditor.getValue();
					
					// Auto-save after 2 seconds of inactivity
					clearTimeout(autoSaveTimeout);
					autoSaveTimeout = setTimeout(() => {
						if ('` + noteGUID + `' !== '') {
							saveDraft();
						}
					}, 2000);
				});
			});
			
			// Insert markdown helper
			function insertMarkdown(before, after) {
				const editor = window.monacoEditor;
				const selection = editor.getSelection();
				const text = editor.getModel().getValueInRange(selection);
				editor.executeEdits('', [{
					range: selection,
					text: before + text + after,
					forceMoveMarkers: true
				}]);
				editor.focus();
			}
			
			// Save draft function
			function saveDraft() {
				const formData = new FormData(document.getElementById('note-form'));
				fetch('/api/notes/` + noteGUID + `/save', {
					method: 'POST',
					body: formData
				}).then(response => {
					if (response.ok) {
						console.log('Draft saved');
						// Show save indicator
						const indicator = document.createElement('div');
						indicator.textContent = 'Draft saved';
						indicator.className = 'save-indicator';
						document.body.appendChild(indicator);
						setTimeout(() => indicator.remove(), 2000);
					}
				});
			}
		`),
	)
	return
}

func (ne NoteEditContent) getPageTitle() string {
	if ne.IsNew {
		return "Create New Note"
	}
	return "Edit Note"
}

func (ne NoteEditContent) getCancelUrl() string {
	if ne.IsNew {
		return "/"
	}
	if ne.Note != nil {
		return "/notes/" + ne.Note.GUID
	}
	return "/"
}

func (ne NoteEditContent) checkedAttr(checked bool) string {
	if checked {
		return "checked"
	}
	return ""
}

func (ne NoteEditContent) renderGuidField(b *element.Builder, guid string) (x any) {
	if guid != "" {
		b.Input("type", "hidden", "name", "guid", "value", guid)
	}
	return
}