package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// APIResponse provides a consistent JSON response structure for all API endpoints.
// Success responses include data, error responses include an error message.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// writeSuccess sends a successful JSON response with data.
// Uses rweb's built-in WriteJSON which sets content-type automatically.
func writeSuccess(ctx rweb.Context, status int, data interface{}) error {
	ctx.SetStatus(status)
	return ctx.WriteJSON(APIResponse{Success: true, Data: data})
}

// writeError sends an error JSON response.
func writeError(ctx rweb.Context, status int, message string) error {
	ctx.SetStatus(status)
	return ctx.WriteJSON(APIResponse{Success: false, Error: message})
}

// CreateNote handles POST /api/v1/notes
// Creates a new note from JSON body and returns the created note.
func CreateNote(ctx rweb.Context) error {
	var input models.NoteInput

	// Decode JSON body into input struct
	// rweb provides Body() as []byte, so we unmarshal directly
	if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}

	// Validate required fields
	if input.GUID == "" {
		return writeError(ctx, http.StatusBadRequest, "guid is required")
	}
	if input.Title == "" {
		return writeError(ctx, http.StatusBadRequest, "title is required")
	}

	// Check for duplicate GUID to provide clear error message
	existing, err := models.GetNoteByGUID(input.GUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to check existing note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	if existing != nil {
		return writeError(ctx, http.StatusConflict, "note with this guid already exists")
	}

	// Create the note
	note, err := models.CreateNote(input)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to create note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to create note")
	}

	logger.Info("Note created", "id", note.ID, "guid", note.GUID)
	return writeSuccess(ctx, http.StatusCreated, note.ToOutput())
}

// GetNote handles GET /api/v1/notes/:id
// Retrieves a single note by ID.
func GetNote(ctx rweb.Context) error {
	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	note, err := models.GetNoteByID(id)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	if note == nil {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	return writeSuccess(ctx, http.StatusOK, note.ToOutput())
}

// ListNotes handles GET /api/v1/notes
// Returns all notes with optional filtering and pagination.
//
// Query parameters:
//   - limit: Maximum number of results (default: no limit)
//   - offset: Number of results to skip (default: 0)
//   - cat: Filter by category name (e.g., ?cat=k8s)
//   - subcats[]: Filter by subcategories within the category (e.g., ?cat=k8s&subcats[]=pod&subcats[]=replicaset)
//
// When cat is provided, returns only notes in that category.
// When both cat and subcats[] are provided, returns notes that match the category
// AND have ALL the specified subcategories.
func ListNotes(ctx rweb.Context) error {
	// Parse pagination parameters with sensible defaults
	limit := 0 // 0 means no limit
	offset := 0

	if limitStr := ctx.Request().QueryParam("limit"); limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit < 0 {
			return writeError(ctx, http.StatusBadRequest, "invalid limit parameter")
		}
		limit = parsedLimit
	}

	if offsetStr := ctx.Request().QueryParam("offset"); offsetStr != "" {
		parsedOffset, err := strconv.Atoi(offsetStr)
		if err != nil || parsedOffset < 0 {
			return writeError(ctx, http.StatusBadRequest, "invalid offset parameter")
		}
		offset = parsedOffset
	}

	// Check for category filter
	categoryName := ctx.Request().QueryParam("cat")

	// Parse subcats[] array from query string
	var subcategories []string
	if queryStr := ctx.Request().Query(); queryStr != "" {
		queryValues, err := url.ParseQuery(queryStr)
		if err == nil {
			// Look for subcats[] parameter (array notation)
			subcategories = queryValues["subcats[]"]
		}
	}

	var notes []models.Note
	var err error

	if categoryName != "" {
		// Filter by category (and optionally subcategories)
		if len(subcategories) > 0 {
			notes, err = models.GetNotesByCategoryAndSubcategories(categoryName, subcategories)
		} else {
			notes, err = models.GetNotesByCategoryName(categoryName)
		}
		if err != nil {
			logger.LogErr(serr.Wrap(err, "failed to get notes by category"), "database error")
			return writeError(ctx, http.StatusInternalServerError, "database error")
		}

		// Apply pagination manually for category-filtered results
		// (The category query functions don't support pagination directly)
		if offset > 0 && offset < len(notes) {
			notes = notes[offset:]
		} else if offset >= len(notes) {
			notes = []models.Note{}
		}
		if limit > 0 && limit < len(notes) {
			notes = notes[:limit]
		}
	} else {
		// No category filter - use standard ListNotes with pagination
		notes, err = models.ListNotes(limit, offset)
		if err != nil {
			logger.LogErr(serr.Wrap(err, "failed to list notes"), "database error")
			return writeError(ctx, http.StatusInternalServerError, "database error")
		}
	}

	// Convert to output format for clean JSON serialization
	outputs := make([]models.NoteOutput, len(notes))
	for i, note := range notes {
		outputs[i] = note.ToOutput()
	}

	return writeSuccess(ctx, http.StatusOK, outputs)
}

// UpdateNote handles PUT /api/v1/notes/:id
// Updates an existing note with the provided JSON body.
func UpdateNote(ctx rweb.Context) error {
	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	var input models.NoteInput
	if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}

	// Title is required for updates
	if input.Title == "" {
		return writeError(ctx, http.StatusBadRequest, "title is required")
	}

	note, err := models.UpdateNote(id, input)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to update note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to update note")
	}
	if note == nil {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	logger.Info("Note updated", "id", note.ID)
	return writeSuccess(ctx, http.StatusOK, note.ToOutput())
}

// DeleteNote handles DELETE /api/v1/notes/:id
// Performs a soft delete on the note (sets deleted_at timestamp).
func DeleteNote(ctx rweb.Context) error {
	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	deleted, err := models.DeleteNote(id)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to delete note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to delete note")
	}
	if !deleted {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	logger.Info("Note deleted", "id", id)
	return writeSuccess(ctx, http.StatusOK, map[string]interface{}{"deleted": true, "id": id})
}
