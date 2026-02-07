package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// CreateCategory handles POST /api/v1/categories
// Creates a new category from JSON body and returns the created category.
func CreateCategory(ctx rweb.Context) error {
	var input models.CategoryInput

	if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}

	// Validate required fields
	if input.Name == "" {
		return writeError(ctx, http.StatusBadRequest, "name is required")
	}

	// Create the category
	category, err := models.CreateCategory(input)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to create category"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to create category")
	}

	logger.Info("Category created", "id", category.ID, "name", category.Name)
	return writeSuccess(ctx, http.StatusCreated, category.ToOutput())
}

// GetCategory handles GET /api/v1/categories/:id
// Retrieves a single category by ID.
func GetCategory(ctx rweb.Context) error {
	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	category, err := models.GetCategory(id)
	if err != nil {
		if err.Error() == "category not found" {
			return writeError(ctx, http.StatusNotFound, "category not found")
		}
		logger.LogErr(serr.Wrap(err, "failed to get category"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}

	return writeSuccess(ctx, http.StatusOK, category.ToOutput())
}

// ListCategories handles GET /api/v1/categories
// Returns all categories with optional pagination via limit/offset query params.
func ListCategories(ctx rweb.Context) error {
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

	categories, err := models.ListCategories(limit, offset)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to list categories"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}

	// Convert to output format for clean JSON serialization
	outputs := make([]models.CategoryOutput, len(categories))
	for i, category := range categories {
		outputs[i] = category.ToOutput()
	}

	return writeSuccess(ctx, http.StatusOK, outputs)
}

// UpdateCategory handles PUT /api/v1/categories/:id
// Updates an existing category with the provided JSON body.
func UpdateCategory(ctx rweb.Context) error {
	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	var input models.CategoryInput
	body := ctx.Request().Body()
	logger.Debug("UpdateCategory request body", "body", string(body))

	if err := json.Unmarshal(body, &input); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}

	logger.Debug("UpdateCategory parsed input", "name", input.Name, "subcategories", input.Subcategories)

	// Name is required for updates
	if input.Name == "" {
		return writeError(ctx, http.StatusBadRequest, "name is required")
	}

	category, err := models.UpdateCategory(id, input)
	if err != nil {
		if err.Error() == "category not found" {
			return writeError(ctx, http.StatusNotFound, "category not found")
		}
		logger.LogErr(serr.Wrap(err, "failed to update category"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to update category")
	}

	logger.Info("Category updated", "id", category.ID)
	return writeSuccess(ctx, http.StatusOK, category.ToOutput())
}

// DeleteCategory handles DELETE /api/v1/categories/:id
// Deletes a category permanently.
func DeleteCategory(ctx rweb.Context) error {
	idStr := ctx.Request().Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	err = models.DeleteCategory(id)
	if err != nil {
		if err.Error() == "category not found" {
			return writeError(ctx, http.StatusNotFound, "category not found")
		}
		logger.LogErr(serr.Wrap(err, "failed to delete category"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to delete category")
	}

	logger.Info("Category deleted", "id", id)
	return writeSuccess(ctx, http.StatusOK, map[string]interface{}{"deleted": true, "id": id})
}

// AddCategoryToNoteRequest represents the optional request body for adding a category to a note.
// The subcategories field allows specifying which subcats of the category apply to this note.
type AddCategoryToNoteRequest struct {
	Subcategories []string `json:"subcategories,omitempty"`
}

// AddCategoryToNote handles POST /api/v1/notes/:id/categories/:category_id
// Adds a category to a note with optional subcategories.
//
// Request body (optional):
//
//	{ "subcategories": ["subcat1", "subcat2"] }
//
// If no body is provided, the category is added without subcategories.
func AddCategoryToNote(ctx rweb.Context) error {
	noteIDStr := ctx.Request().Param("id")
	noteID, err := strconv.ParseInt(noteIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	categoryIDStr := ctx.Request().Param("category_id")
	categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	// Parse optional request body for subcategories
	var subcategories []string
	body := ctx.Request().Body()
	if len(body) > 0 {
		var req AddCategoryToNoteRequest
		if err := json.Unmarshal(body, &req); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}
		subcategories = req.Subcategories
	}

	// Use the subcategory-aware function when subcats are provided
	if len(subcategories) > 0 {
		err = models.AddCategoryToNoteWithSubcategories(noteID, categoryID, subcategories)
	} else {
		err = models.AddCategoryToNote(noteID, categoryID)
	}

	if err != nil {
		if err.Error() == "note not found" {
			return writeError(ctx, http.StatusNotFound, "note not found")
		}
		if err.Error() == "category not found" {
			return writeError(ctx, http.StatusNotFound, "category not found")
		}
		if err.Error() == "category already added to this note" {
			return writeError(ctx, http.StatusConflict, "category already added to this note")
		}
		logger.LogErr(serr.Wrap(err, "failed to add category to note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to add category to note")
	}

	logger.Info("Category added to note", "note_id", noteID, "category_id", categoryID, "subcategories", subcategories)
	return writeSuccess(ctx, http.StatusCreated, map[string]interface{}{
		"note_id":       noteID,
		"category_id":   categoryID,
		"subcategories": subcategories,
		"added":         true,
	})
}

// UpdateNoteCategory handles PUT /api/v1/notes/:id/categories/:category_id
// Updates the subcategories for an existing note-category relationship.
// This allows changing which subcats are selected without removing and re-adding the category.
func UpdateNoteCategory(ctx rweb.Context) error {
	noteIDStr := ctx.Request().Param("id")
	noteID, err := strconv.ParseInt(noteIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	categoryIDStr := ctx.Request().Param("category_id")
	categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	// Parse subcategories from request body
	var req AddCategoryToNoteRequest
	body := ctx.Request().Body()
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to decode request body"), "invalid JSON")
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}
	}

	err = models.UpdateNoteCategorySubcategories(noteID, categoryID, req.Subcategories)
	if err != nil {
		if err.Error() == "relationship not found" {
			return writeError(ctx, http.StatusNotFound, "relationship not found")
		}
		logger.LogErr(serr.Wrap(err, "failed to update note category subcategories"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to update note category")
	}

	logger.Info("Note category subcategories updated", "note_id", noteID, "category_id", categoryID)
	return writeSuccess(ctx, http.StatusOK, map[string]interface{}{
		"note_id":       noteID,
		"category_id":   categoryID,
		"subcategories": req.Subcategories,
		"updated":       true,
	})
}

// RemoveCategoryFromNote handles DELETE /api/v1/notes/:id/categories/:category_id
// Removes a category from a note.
func RemoveCategoryFromNote(ctx rweb.Context) error {
	noteIDStr := ctx.Request().Param("id")
	noteID, err := strconv.ParseInt(noteIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	categoryIDStr := ctx.Request().Param("category_id")
	categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	err = models.RemoveCategoryFromNote(noteID, categoryID)
	if err != nil {
		if err.Error() == "relationship not found" {
			return writeError(ctx, http.StatusNotFound, "relationship not found")
		}
		logger.LogErr(serr.Wrap(err, "failed to remove category from note"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to remove category from note")
	}

	logger.Info("Category removed from note", "note_id", noteID, "category_id", categoryID)
	return writeSuccess(ctx, http.StatusOK, map[string]interface{}{
		"note_id":     noteID,
		"category_id": categoryID,
		"removed":     true,
	})
}

// GetNoteCategories handles GET /api/v1/notes/:id/categories
// Returns categories for a note along with which subcategories are selected.
// The response includes both the full subcategory list (from the category definition)
// and selected_subcategories (from the note-category junction) so the UI can
// render checkboxes with the correct pre-selected state.
func GetNoteCategories(ctx rweb.Context) error {
	noteIDStr := ctx.Request().Param("id")
	noteID, err := strconv.ParseInt(noteIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid note id")
	}

	details, err := models.GetNoteCategoryDetails(noteID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get note categories"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}

	return writeSuccess(ctx, http.StatusOK, details)
}

// GetCategoryNotes handles GET /api/v1/categories/:id/notes
// Retrieves all notes for a category.
func GetCategoryNotes(ctx rweb.Context) error {
	categoryIDStr := ctx.Request().Param("id")
	categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid category id")
	}

	notes, err := models.GetCategoryNotes(categoryID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get category notes"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}

	// Convert to output format for clean JSON serialization
	outputs := make([]models.NoteOutput, len(notes))
	for i, note := range notes {
		outputs[i] = note.ToOutput()
	}

	return writeSuccess(ctx, http.StatusOK, outputs)
}
