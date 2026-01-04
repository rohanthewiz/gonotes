package handlers

import (
	"database/sql"
	"encoding/json"
	"gonotes/models"
	"gonotes/views/pages"
	"net/http"
	"strconv"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// Dashboard displays the main notes dashboard
func Dashboard(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Get pagination parameters
	limit := 20
	offset := 0
	if offsetStr := c.Request().QueryParam("offset"); offsetStr != "" {
		offset, _ = strconv.Atoi(offsetStr)
	}

	// Get user's notes
	notes, err := models.GetNotesForUser(userGUID, limit, offset)
	if err != nil {
		logger.LogErr(err, "failed to get notes for dashboard")
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	// Get unique tags for sidebar
	_, err = models.GetAllUniqueTags(userGUID)
	if err != nil {
		logger.LogErr(err, "failed to get tags")
		// tags = []string{} // Continue with empty tags
	}

	// Render dashboard page
	html := pages.RenderDashboard(notes, userGUID)
	return c.WriteHTML(html)
}

// ViewNote displays a single note
func ViewNote(c rweb.Context) error {
	guid := c.Request().Param("guid")
	userGUID := getUserGUID(c)

	// Get the note first to check if it exists
	note, err := models.GetNoteByGUID(guid)
	if err != nil {
		if err == sql.ErrNoRows {
			c.SetStatus(http.StatusNotFound)
			return nil
		}
		logger.LogErr(err, "failed to get note", "guid", guid)
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	// Check permissions
	canRead, err := models.UserCanReadNote(userGUID, guid)
	if err != nil {
		logger.LogErr(err, "failed to check read permission", "userGUID", userGUID, "noteGUID", guid)
		// If there's an error checking permissions, check if user is the owner
		if note.CreatedBy.Valid && note.CreatedBy.String == userGUID {
			canRead = true // Creator can always read their own notes
		} else {
			c.SetStatus(http.StatusForbidden)
			return nil
		}
	}
	if !canRead {
		c.SetStatus(http.StatusForbidden)
		return nil
	}

	// Determine if user can edit
	canEdit := false
	if note.CreatedBy.Valid && note.CreatedBy.String == userGUID {
		canEdit = true // Creator can always edit their own notes
	} else {
		canEdit, _ = models.UserCanEditNote(userGUID, guid)
	}

	// Render note view page
	html := pages.RenderNoteView(note, canEdit, userGUID)
	return c.WriteHTML(html)
}

// EditNote displays the note editor
func EditNote(c rweb.Context) error {
	guid := c.Request().Param("guid")
	userGUID := getUserGUID(c)

	// Check edit permissions
	canEdit, err := models.UserCanEditNote(userGUID, guid)
	if err != nil || !canEdit {
		c.SetStatus(http.StatusForbidden)
		return nil
	}

	// Get the note
	note, err := models.GetNoteByGUID(guid)
	if err != nil {
		return serr.Wrap(err, "failed to get note for editing")
	}

	// Render edit page
	html := pages.RenderNoteEdit(note, userGUID)
	return c.WriteHTML(html)
}

// NewNoteForm displays the form for creating a new note
func NewNoteForm(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Pass nil for new note
	html := pages.RenderNoteEdit(nil, userGUID)
	return c.WriteHTML(html)
}

// CreateNote handles note creation
func CreateNote(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Parse form data
	title := c.Request().FormValue("title")
	body := c.Request().FormValue("body")
	description := c.Request().FormValue("description")
	tagsJSON := c.Request().FormValue("tags")
	isPrivate := c.Request().FormValue("is_private") == "true"

	// Validate title
	if title == "" {
		return c.WriteJSON(map[string]string{"error": "Title is required"})
	}

	// Create note object
	note := &models.Note{
		Title:       title,
		Body:        sql.NullString{String: body, Valid: body != ""},
		Description: sql.NullString{String: description, Valid: description != ""},
		Tags:        tagsJSON,
		IsPrivate:   isPrivate,
	}

	// Validate and set tags
	if tagsJSON == "" {
		note.Tags = "[]"
	} else {
		// Validate JSON
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			note.Tags = "[]"
		}
	}

	// Save note
	if err := note.Save(userGUID); err != nil {
		logger.LogErr(err, "failed to create note")
		return c.WriteJSON(map[string]string{"error": "Failed to create note"})
	}

	// Broadcast update via SSE
	broadcastNoteUpdate("created", note)

	// Return success with redirect URL
	if c.Request().Header("HX-Request") == "true" {
		// HTMX request - return redirect header
		c.Response().SetHeader("HX-Redirect", "/notes/"+note.GUID)
		c.SetStatus(http.StatusCreated)
		return nil
	}

	// Regular request - redirect
	return c.Redirect(http.StatusFound, "/notes/"+note.GUID)
}

// UpdateNote handles note updates
func UpdateNote(c rweb.Context) error {
	guid := c.Request().Param("guid")
	userGUID := getUserGUID(c)

	// Check permissions
	canEdit, err := models.UserCanEditNote(userGUID, guid)
	if err != nil || !canEdit {
		c.SetStatus(http.StatusForbidden)
		return nil
	}

	// Get existing note
	note, err := models.GetNoteByGUID(guid)
	if err != nil {
		return serr.Wrap(err, "failed to get note for update")
	}

	// Update fields
	note.Title = c.Request().FormValue("title")
	note.Body = sql.NullString{
		String: c.Request().FormValue("body"),
		Valid:  true,
	}
	note.Description = sql.NullString{
		String: c.Request().FormValue("description"),
		Valid:  c.Request().FormValue("description") != "",
	}
	note.Tags = c.Request().FormValue("tags")
	note.IsPrivate = c.Request().FormValue("is_private") == "true"

	// Save changes
	if err := note.Update(userGUID); err != nil {
		logger.LogErr(err, "failed to update note")
		return c.WriteJSON(map[string]string{"error": "Failed to update note"})
	}

	// Broadcast update
	broadcastNoteUpdate("updated", note)

	// Return response
	if c.Request().Header("HX-Request") == "true" {
		// Return updated note card partial for HTMX
		html := pages.RenderNoteCardHTML(*note)
		return c.WriteHTML(html)
	}

	return c.WriteJSON(map[string]bool{"success": true})
}

// DeleteNote handles note deletion (soft delete)
func DeleteNote(c rweb.Context) error {
	guid := c.Request().Param("guid")
	userGUID := getUserGUID(c)

	// Check permissions
	canEdit, err := models.UserCanEditNote(userGUID, guid)
	if err != nil || !canEdit {
		c.SetStatus(http.StatusForbidden)
		return nil
	}

	// Get note
	note, err := models.GetNoteByGUID(guid)
	if err != nil {
		return serr.Wrap(err, "failed to get note for deletion")
	}

	// Soft delete
	if err := note.Delete(userGUID); err != nil {
		logger.LogErr(err, "failed to delete note")
		return c.WriteJSON(map[string]string{"error": "Failed to delete note"})
	}

	// Broadcast deletion
	broadcastNoteUpdate("deleted", note)

	// Return response
	if c.Request().Header("HX-Request") == "true" {
		// HTMX - trigger redirect to dashboard
		c.Response().SetHeader("HX-Redirect", "/")
		c.SetStatus(http.StatusOK)
		return nil
	}

	return c.WriteJSON(map[string]bool{"success": true})
}

// AutoSaveNote handles auto-save requests from the editor
func AutoSaveNote(c rweb.Context) error {
	guid := c.Request().Param("guid")
	userGUID := getUserGUID(c)

	// Check permissions
	canEdit, err := models.UserCanEditNote(userGUID, guid)
	if err != nil || !canEdit {
		logger.LogErr(err, "unauthorized auto-save attempt", "user", userGUID, "note", guid)
		c.SetStatus(http.StatusForbidden)
		return nil
	}

	// Get note
	note, err := models.GetNoteByGUID(guid)
	if err != nil {
		return serr.Wrap(err, "failed to get note for auto-save")
	}

	// Update only the body content (auto-save typically only updates content)
	content := c.Request().FormValue("content")
	note.Body = sql.NullString{String: content, Valid: true}

	// Save without updating timestamp for auto-saves
	// This prevents constant "updated" notifications
	query := `
		UPDATE notes 
		SET body = ?
		WHERE guid = ?
	`

	if err := models.WriteThrough(query, note.Body, guid); err != nil {
		logger.LogErr(err, "failed to auto-save note")
		return c.WriteJSON(map[string]string{"error": "Auto-save failed"})
	}

	return c.WriteJSON(map[string]interface{}{
		"success":  true,
		"saved_at": note.UpdatedAt,
	})
}

// Helper function to get user GUID from context
func getUserGUID(c rweb.Context) string {
	if guid, ok := c.Get("user_guid").(string); ok {
		return guid
	}
	// Fallback to default user for development
	return "default-user-guid"
}

// Helper function to broadcast note updates via SSE
func broadcastNoteUpdate(action string, note *models.Note) {
	if eventsCh == nil {
		return
	}

	event := map[string]interface{}{
		"type":   "note-" + action,
		"note":   note,
		"action": action,
	}

	select {
	case eventsCh <- event:
		// Event sent successfully
	default:
		// Channel full, skip
		logger.Debug("SSE channel full, skipping event")
	}
}
