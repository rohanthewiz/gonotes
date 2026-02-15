package api

import (
	"encoding/json"
	"net/http"

	"gonotes/models"

	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Control API Handlers
//
// These endpoints power the UI controls for sync: a status indicator,
// an enable/disable toggle, and a "Sync Now" button.
// All require authentication to prevent unauthorized state changes.
// ============================================================================

// SyncControlStatus handles GET /api/v1/sync/control/status
// Returns the current state of the sync client for the UI status indicator.
// If sync is not configured (no sync client), returns a disabled state
// rather than an error so the UI can render gracefully.
func SyncControlStatus(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		// Sync not configured — return a minimal "disabled" status
		// so the UI can hide/disable sync controls
		return writeSuccess(ctx, http.StatusOK, models.SyncClientStatus{
			Enabled:   false,
			Connected: false,
		})
	}

	return writeSuccess(ctx, http.StatusOK, client.GetStatus())
}

// SyncControlToggle handles POST /api/v1/sync/control/toggle
// Enables or disables the sync client at runtime.
// Request body: {"enabled": true} or {"enabled": false}
func SyncControlToggle(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		return writeError(ctx, http.StatusServiceUnavailable, "sync is not configured")
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid request body")
	}

	client.SetEnabled(req.Enabled)

	return writeSuccess(ctx, http.StatusOK, client.GetStatus())
}

// SyncControlNow handles POST /api/v1/sync/control/sync-now
// Triggers an immediate sync cycle. Returns 409 Conflict if a sync
// is already in progress to avoid queueing multiple cycles.
func SyncControlNow(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		return writeError(ctx, http.StatusServiceUnavailable, "sync is not configured")
	}

	if err := client.SyncNow(); err != nil {
		// Distinguish "already in progress" from other errors
		if err.Error() == "sync already in progress" || err.Error() == "sync is disabled" {
			return writeError(ctx, http.StatusConflict, err.Error())
		}
		return writeError(ctx, http.StatusInternalServerError, serr.Wrap(err, "sync failed").Error())
	}

	return writeSuccess(ctx, http.StatusOK, client.GetStatus())
}
