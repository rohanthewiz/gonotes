package handlers

import (
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"gonotes/models"
	"gonotes/views/pages"
	"gonotes/views/partials"
	"strconv"
)

// NotesListPartial returns the notes list as an HTML partial
func NotesListPartial(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Get pagination parameters
	limit := 20
	offset := 0
	if offsetStr := c.Request().QueryParam("offset"); offsetStr != "" {
		offset, _ = strconv.Atoi(offsetStr)
	}

	notes, err := models.GetNotesForUser(userGUID, limit, offset)
	if err != nil {
		logger.LogErr(err, "failed to get notes for partial")
		return c.WriteHTML("<div>Failed to load notes</div>")
	}

	html := partials.RenderNotesList(notes)
	return c.WriteHTML(html)
}

// NoteCardPartial returns a single note card as HTML
func NoteCardPartial(c rweb.Context) error {
	guid := c.Request().Param("guid")
	userGUID := getUserGUID(c)

	// Check permissions
	canRead, err := models.UserCanReadNote(userGUID, guid)
	if err != nil || !canRead {
		return c.WriteHTML("<div>Note not found</div>")
	}

	note, err := models.GetNoteByGUID(guid)
	if err != nil {
		logger.LogErr(err, "failed to get note for card partial")
		return c.WriteHTML("<div>Failed to load note</div>")
	}

	html := pages.RenderNoteCardHTML(*note)
	return c.WriteHTML(html)
}

// RecentNotesPartial returns recent notes as HTML partial
func RecentNotesPartial(c rweb.Context) error {
	userGUID := getUserGUID(c)
	limit := 10 // Show last 10 recent notes

	notes, err := models.GetRecentNotes(userGUID, limit)
	if err != nil {
		logger.LogErr(err, "failed to get recent notes")
		return c.WriteHTML("<div>Failed to load recent notes</div>")
	}

	html := partials.RenderRecentNotes(notes, 10) // Show 10 recent notes
	return c.WriteHTML(html)
}
