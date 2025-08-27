package pages

import (
	"github.com/rohanthewiz/element"
	"gonotes/models"
	"gonotes/views"
	"html"
	"strings"
)

// RenderNoteView renders a single note view page
func RenderNoteView(note *models.Note, canEdit bool, userGUID string) string {
	return views.BaseLayout("", "", views.PageWithHeader{
		UserGUID:   userGUID,
		ActivePage: "notes",
		Content: NoteViewContent{
			Note:    note,
			CanEdit: canEdit,
		},
	})
}

// NoteViewContent component for viewing a note
type NoteViewContent struct {
	Note    *models.Note
	CanEdit bool
}

func (nv NoteViewContent) Render(b *element.Builder) (x any) {
	b.DivClass("note-view").R(
		// Note header
		b.DivClass("note-header").R(
			b.H1Class("note-title").T(nv.Note.Title),
			b.DivClass("note-meta").R(
				b.Span("class", "note-date").T("Updated: "+nv.Note.UpdatedAt.Format("Jan 2, 2006 3:04 PM")),
				nv.renderPrivateIcon(b),
			),
		),

		// Note actions bar
		b.DivClass("note-actions-bar").R(
			nv.renderEditButton(b),
			b.Button("class", "btn btn-secondary",
				"@click", "copyToClipboard()").T("📋 Copy"),
			b.Button("class", "btn btn-secondary",
				"@click", "exportNote()").T("💾 Export"),
			nv.renderDeleteButton(b),
		),

		// Note tags
		nv.renderTags(b),

		// Note body (rendered markdown)
		b.DivClass("note-content").R(
			// Content will be rendered as HTML from markdown
			// For now, just escape and show as text
			b.Wrap(func() {
				content := nv.Note.Body.String
				if nv.Note.Body.Valid {
					// In production, this would be converted from markdown to HTML
					// For now, preserve line breaks and escape HTML
					lines := strings.Split(html.EscapeString(content), "\n")
					for i, line := range lines {
						b.T(line)
						if i < len(lines)-1 {
							b.Br()
						}
					}
				}
			}),
		),

		// JavaScript for note actions
		b.Script().T(`
			function copyToClipboard() {
				const noteContent = document.querySelector('.note-content').innerText;
				navigator.clipboard.writeText(noteContent).then(() => {
					alert('Note copied to clipboard!');
				});
			}
			
			function exportNote() {
				// Implementation for exporting note
				window.location.href = '/api/notes/`+nv.Note.GUID+`/export';
			}
		`),
	)
	return
}

func (nv NoteViewContent) renderPrivateIcon(b *element.Builder) (x any) {
	if nv.Note.IsPrivate {
		b.Span("class", "icon-private", "title", "Private Note").T("🔒 Private")
	}
	return
}

func (nv NoteViewContent) renderEditButton(b *element.Builder) (x any) {
	if nv.CanEdit {
		b.Button("class", "btn btn-primary",
			"hx-get", "/notes/"+nv.Note.GUID+"/edit",
			"hx-target", "#content-wrapper",
			"hx-push-url", "true").T("✏️ Edit")
	}
	return
}

func (nv NoteViewContent) renderDeleteButton(b *element.Builder) (x any) {
	if nv.CanEdit {
		b.Button("class", "btn btn-danger",
			"hx-delete", "/api/notes/"+nv.Note.GUID,
			"hx-confirm", "Are you sure you want to delete this note?",
			"hx-target", "body",
			"hx-swap", "innerHTML",
			"hx-redirect", "/").T("🗑️ Delete")
	}
	return
}

func (nv NoteViewContent) renderTags(b *element.Builder) (x any) {
	if nv.Note.Tags == "" {
		return
	}

	b.DivClass("note-tags-view").R(
		b.Span("class", "tags-label").T("Tags: "),
		b.Wrap(func() {
			// Parse and render tags
			tags := strings.Split(nv.Note.Tags, ",")
			for i, tag := range tags {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					b.A("href", "/tags/"+tag,
						"class", "tag-link").T(tag)
					if i < len(tags)-1 {
						b.T(" ")
					}
				}
			}
		}),
	)
	return
}
