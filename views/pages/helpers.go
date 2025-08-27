package pages

import (
	"github.com/rohanthewiz/element"
	"gonotes/models"
)

// RenderNoteCardHTML renders a note card as HTML string
func RenderNoteCardHTML(note models.Note) string {
	b := element.NewBuilder()
	element.RenderComponents(b, NoteCard{Note: note})
	return b.String()
}
