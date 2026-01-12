package api

import (
	"net/http"
	"strconv"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// GetUserChanges handles GET /api/v1/sync/changes
// Returns all changes for the authenticated user since a specified timestamp.
//
// Query parameters:
//   - since: RFC3339 timestamp (required) - only return changes after this time
//   - limit: Maximum number of changes to return (optional, default: no limit)
//
// Response contains an array of NoteChangeOutput objects, each with:
//   - Change metadata (id, guid, operation, created_at)
//   - Associated note_guid for the affected note
//   - Fragment with delta fields if this was a create/update operation
//
// Changes are ordered by created_at ascending for proper replay order.
func GetUserChanges(ctx rweb.Context) error {
	// Authentication check - sync operations require auth
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Parse 'since' parameter (required)
	sinceStr := ctx.Request().QueryParam("since")
	if sinceStr == "" {
		return writeError(ctx, http.StatusBadRequest, "since parameter is required (RFC3339 format)")
	}

	since, err := time.Parse(time.RFC3339, sinceStr)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid since parameter: must be RFC3339 format")
	}

	// Parse optional 'limit' parameter
	limit := 0 // 0 means no limit
	if limitStr := ctx.Request().QueryParam("limit"); limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit < 0 {
			return writeError(ctx, http.StatusBadRequest, "invalid limit parameter")
		}
		limit = parsedLimit
	}

	// Get user's changes since the timestamp
	changes, err := models.GetUserChangesSince(userGUID, since, limit)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get user changes"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "failed to retrieve changes")
	}

	// Return empty array instead of null if no changes
	if changes == nil {
		changes = []models.NoteChangeOutput{}
	}

	logger.Info("Sync changes retrieved",
		"user", userGUID,
		"since", since.Format(time.RFC3339),
		"count", len(changes),
	)

	return writeSuccess(ctx, http.StatusOK, changes)
}
