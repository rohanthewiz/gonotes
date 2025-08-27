package pages

import (
	"github.com/rohanthewiz/element"
	"go_notes_web/models"
)

// RenderNoteCardHTML renders a note card as HTML string
func RenderNoteCardHTML(note models.Note) string {
	b := element.NewBuilder()
	element.RenderComponents(b, NoteCard{Note: note})
	return b.String()
}