package pages

import (
	"github.com/rohanthewiz/element"
	"go_notes_web/views"
)

// TagInfo holds information about a tag
type TagInfo struct {
	Name  string
	Count int
}

// RenderTagsPage renders the tags overview page
func RenderTagsPage(tags []TagInfo, userGUID string) string {
	return views.BaseLayout("", "", views.PageWithHeader{
		UserGUID: userGUID,
		ActivePage: "tags",
		Content: TagsContent{
			Tags: tags,
		},
	})
}

// TagsContent component for tags page
type TagsContent struct {
	Tags []TagInfo
}

func (tc TagsContent) Render(b *element.Builder) (x any) {
	b.DivClass("tags-page").R(
		// Tags header
		b.DivClass("tags-header").R(
			b.H2().T("Tags"),
			b.P().F("Manage and explore your %d tags", len(tc.Tags)),
		),
		
		// Tags cloud/grid
		b.DivClass("tags-container").R(
			tc.renderTags(b),
		),
	)
	return
}

func (tc TagsContent) renderTags(b *element.Builder) (x any) {
	if len(tc.Tags) == 0 {
		b.DivClass("no-tags").R(
			b.H3().T("No tags yet"),
			b.P().T("Tags will appear here as you add them to your notes."),
		)
		return
	}
	
	// Render tags as a grid of cards
	b.DivClass("tags-grid").R(
		element.ForEach(tc.Tags, func(tag TagInfo) {
			b.DivClass("tag-card").R(
				b.A("href", "/tags/"+tag.Name,
					"class", "tag-card-link",
					"hx-get", "/api/tags/"+tag.Name+"/notes",
					"hx-target", "#content-wrapper",
					"hx-push-url", "true").R(
					b.H3Class("tag-name").T(tag.Name),
					b.Span("class", "tag-count").F("%d notes", tag.Count),
				),
				b.DivClass("tag-actions").R(
					b.Button("class", "btn-icon",
						"title", "Rename tag",
						"@click", "renameTag('"+tag.Name+"')").T("✏️"),
					b.Button("class", "btn-icon",
						"title", "Delete tag", 
						"hx-delete", "/api/tags/"+tag.Name,
						"hx-confirm", "Remove this tag from all notes?",
						"hx-target", "closest .tag-card",
						"hx-swap", "outerHTML").T("🗑️"),
				),
			)
		}),
	)
	
	// JavaScript for tag operations
	b.Script().T(`
		function renameTag(oldName) {
			const newName = prompt('Enter new name for tag "' + oldName + '":');
			if (newName && newName !== oldName) {
				// Send rename request
				fetch('/api/tags/' + oldName + '/rename', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({newName: newName})
				}).then(response => {
					if (response.ok) {
						window.location.reload();
					}
				});
			}
		}
	`)
	
	return
}