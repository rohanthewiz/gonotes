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
// Requires authentication - note is owned by the authenticated user.
//
// Content encoding modes:
// - Standard JSON: Body field is plain string
// - MsgPack mode: Header "X-Body-Encoding: msgpack", body_encoded field contains Base64 msgpack
func CreateNote(ctx rweb.Context) error {
	// Authentication check - all note operations require auth
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	var input models.NoteInput

	// Check for msgpack body encoding mode via header
	// Design: Using header rather than content-type to maintain JSON envelope
	useMsgPack := ctx.Request().Header("X-Body-Encoding") == "msgpack"

	if useMsgPack {
		// Decode JSON with msgpack-encoded body field
		var msgpackReq models.MsgPackBodyRequest
		if err := json.Unmarshal(ctx.Request().Body(), &msgpackReq); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode msgpack request body"), "invalid JSON")
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}

		// Convert msgpack request to standard NoteInput
		converted, err := msgpackReq.ToNoteInput()
		if err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode msgpack body"), "msgpack decode error")
			return writeError(ctx, http.StatusBadRequest, "invalid msgpack body encoding")
		}
		input = *converted
	} else {
		// Standard JSON body - decode directly
		// rweb provides Body() as []byte, so we unmarshal directly
		if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}
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

	// Create the note with user ownership
	// Note: CreateNote may return both a note AND an error if disk write succeeded
	// but cache update failed. In this case, we still return success since the
	// disk DB is the source of truth.
	logger.Debug("API CreateNote: calling models.CreateNote", "guid", input.GUID, "title", input.Title)

	note, err := models.CreateNote(input, userGUID)

	logger.Debug("API CreateNote: models.CreateNote returned",
		"note_is_nil", note == nil,
		"err_is_nil", err == nil,
		"err_msg", func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(),
	)

	if err != nil {
		if note != nil {
			// Disk write succeeded, cache failed - log warning but return success
			logger.LogErr(err, "note created but cache update failed", "id", note.ID, "guid", note.GUID)
			logger.Info("Note created (cache sync pending)", "id", note.ID, "guid", note.GUID, "user", userGUID)
			partialOutput := note.ToOutput()
			if useMsgPack {
				if msgpackResp, encErr := partialOutput.ToMsgPackResponse(); encErr == nil {
					return writeSuccess(ctx, http.StatusCreated, msgpackResp)
				}
			}
			return writeSuccess(ctx, http.StatusCreated, partialOutput)
		}
		// Complete failure - disk write failed
		logger.LogErr(serr.Wrap(err, "failed to create note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to create note")
	}

	logger.Info("Note created", "id", note.ID, "guid", note.GUID, "user", userGUID)

	// Return msgpack-encoded response if client requested it
	output := note.ToOutput()
	if useMsgPack {
		msgpackResp, err := output.ToMsgPackResponse()
		if err != nil {
			// Fallback to standard JSON if msgpack encoding fails
			logger.LogErr(err, "failed to encode msgpack response, falling back to JSON")
			return writeSuccess(ctx, http.StatusCreated, output)
		}
		return writeSuccess(ctx, http.StatusCreated, msgpackResp)
	}
	return writeSuccess(ctx, http.StatusCreated, output)
}

// GetNote handles GET /api/v1/notes/:id
// Retrieves a single note by ID. Only returns notes owned by the authenticated user.
// Supports msgpack body encoding via X-Body-Encoding: msgpack header.
func GetNote(ctx rweb.Context) error {
	// Authentication check - all note operations require auth
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	// GetNoteByID filters by user ownership
	note, err := models.GetNoteByID(id, userGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	if note == nil {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	// Return msgpack-encoded response if client requested it
	output := note.ToOutput()
	if ctx.Request().Header("X-Body-Encoding") == "msgpack" {
		if msgpackResp, encErr := output.ToMsgPackResponse(); encErr == nil {
			return writeSuccess(ctx, http.StatusOK, msgpackResp)
		}
		// Fallback to JSON on encoding error
	}
	return writeSuccess(ctx, http.StatusOK, output)
}

// ListNotes handles GET /api/v1/notes
// Returns all notes owned by the authenticated user with optional filtering and pagination.
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
	// Authentication check - all note operations require auth
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

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
		// Filter by category (and optionally subcategories) with user scoping
		if len(subcategories) > 0 {
			notes, err = models.GetNotesByCategoryAndSubcategories(categoryName, subcategories, userGUID)
		} else {
			notes, err = models.GetNotesByCategoryName(categoryName, userGUID)
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
		// No category filter - use standard ListNotes with pagination and user scoping
		notes, err = models.ListNotes(userGUID, limit, offset)
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

	// Return msgpack-encoded response if client requested it
	if ctx.Request().Header("X-Body-Encoding") == "msgpack" {
		msgpackOutputs := make([]models.MsgPackBodyResponse, 0, len(outputs))
		for _, output := range outputs {
			if msgpackResp, encErr := output.ToMsgPackResponse(); encErr == nil {
				msgpackOutputs = append(msgpackOutputs, *msgpackResp)
			} else {
				// Log error but skip this note in msgpack response
				logger.LogErr(encErr, "failed to encode msgpack response for note", "id", output.ID)
			}
		}
		return writeSuccess(ctx, http.StatusOK, msgpackOutputs)
	}

	return writeSuccess(ctx, http.StatusOK, outputs)
}

// UpdateNote handles PUT /api/v1/notes/:id
// Updates an existing note with the provided JSON body.
// Only updates notes owned by the authenticated user.
// Supports msgpack body encoding via X-Body-Encoding: msgpack header.
func UpdateNote(ctx rweb.Context) error {
	// Authentication check - all note operations require auth
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	var input models.NoteInput

	// Check for msgpack body encoding mode via header
	useMsgPack := ctx.Request().Header("X-Body-Encoding") == "msgpack"

	if useMsgPack {
		// Decode JSON with msgpack-encoded body field
		var msgpackReq models.MsgPackBodyRequest
		if err := json.Unmarshal(ctx.Request().Body(), &msgpackReq); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode msgpack request body"), "invalid JSON")
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}

		// Convert msgpack request to standard NoteInput
		converted, err := msgpackReq.ToNoteInput()
		if err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode msgpack body"), "msgpack decode error")
			return writeError(ctx, http.StatusBadRequest, "invalid msgpack body encoding")
		}
		input = *converted
	} else {
		// Standard JSON body
		if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}
	}

	// Title is required for updates
	if input.Title == "" {
		return writeError(ctx, http.StatusBadRequest, "title is required")
	}

	// UpdateNote verifies ownership via userGUID
	// Note: UpdateNote may return both a note AND an error if disk write succeeded
	// but cache update failed. In this case, we still return success since the
	// disk DB is the source of truth.
	logger.Debug("API UpdateNote: calling models.UpdateNote", "id", id, "title", input.Title)

	note, err := models.UpdateNote(id, input, userGUID)

	logger.Debug("API UpdateNote: models.UpdateNote returned",
		"note_is_nil", note == nil,
		"err_is_nil", err == nil,
		"err_msg", func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(),
	)

	if err != nil {
		if note != nil {
			// Disk write succeeded, cache failed - log warning but return success
			logger.LogErr(err, "note updated but cache update failed", "id", note.ID)
			logger.Info("Note updated (cache sync pending)", "id", note.ID, "user", userGUID)
			partialOutput := note.ToOutput()
			if useMsgPack {
				if msgpackResp, encErr := partialOutput.ToMsgPackResponse(); encErr == nil {
					return writeSuccess(ctx, http.StatusOK, msgpackResp)
				}
			}
			return writeSuccess(ctx, http.StatusOK, partialOutput)
		}
		// Complete failure - disk write failed
		logger.LogErr(serr.Wrap(err, "failed to update note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to update note")
	}
	if note == nil {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	logger.Info("Note updated", "id", note.ID, "user", userGUID)

	// Return msgpack-encoded response if client requested it
	output := note.ToOutput()
	if useMsgPack {
		if msgpackResp, encErr := output.ToMsgPackResponse(); encErr == nil {
			return writeSuccess(ctx, http.StatusOK, msgpackResp)
		}
		// Fallback to JSON on encoding error
	}
	return writeSuccess(ctx, http.StatusOK, output)
}

// SearchNotes handles GET /api/v1/notes/search?q=query
// Returns notes matching the query string in their title, for use in note-linking autocomplete.
// Results include id, guid, and title. Limited to 20 results.
func SearchNotes(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	query := ctx.Request().QueryParam("q")
	if query == "" {
		return writeSuccess(ctx, http.StatusOK, []models.NoteOutput{})
	}

	notes, err := models.SearchNotesByTitle(query, userGUID, 20)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to search notes"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}

	// Return lightweight output with only id, guid, title for autocomplete
	type SearchResult struct {
		ID    int64  `json:"id"`
		GUID  string `json:"guid"`
		Title string `json:"title"`
	}

	results := make([]SearchResult, len(notes))
	for i, note := range notes {
		results[i] = SearchResult{
			ID:    note.ID,
			GUID:  note.GUID,
			Title: note.Title,
		}
	}

	return writeSuccess(ctx, http.StatusOK, results)
}

// DeleteNote handles DELETE /api/v1/notes/:id
// Performs a soft delete on the note (sets deleted_at timestamp).
// Only deletes notes owned by the authenticated user.
func DeleteNote(ctx rweb.Context) error {
	// Authentication check - all note operations require auth
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	// DeleteNote verifies ownership via userGUID
	deleted, err := models.DeleteNote(id, userGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to delete note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to delete note")
	}
	if !deleted {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	logger.Info("Note deleted", "id", id, "user", userGUID)
	return writeSuccess(ctx, http.StatusOK, map[string]interface{}{"deleted": true, "id": id})
}
