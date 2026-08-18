package api

import (
	"encoding/json"
	"net/http"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Control API Handlers
//
// These endpoints power the UI controls for sync: a status indicator,
// an enable/disable toggle, and a "Sync Now" button.
// All require authentication to prevent unauthorized state changes.
//
// Prompt mode (the default — see models/sync_config.go) is what the rest of
// this file exists for. Nothing syncs on its own, so the UI has to be able to
// ask, and these are the four things it needs to do that: read the clock
// (status, which now carries due/pending/mode), act on it (sync-now, which
// can compact on the way), defer it (snooze), and change it (mode).
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
//
// Optional request body: {"compact": true} collapses the pending change log
// before the cycle runs. This is the "compact & sync" answer to a prompt —
// one round trip, one decision — and it is a body flag rather than a separate
// endpoint so the UI cannot end up having compacted and then failed to sync.
func SyncControlNow(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		return writeError(ctx, http.StatusServiceUnavailable, "sync is not configured")
	}

	// An absent or empty body is the ordinary "just sync" call, so a parse
	// failure here is not an error — it is the default.
	var req struct {
		Compact bool `json:"compact"`
	}
	if body := ctx.Request().Body(); len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}

	if req.Compact {
		res, err := client.Compact()
		if err != nil {
			// Report it, but still sync: the user asked for their changes to
			// go out, and compaction was how they wanted them packed, not
			// whether they wanted them sent.
			logger.LogErr(serr.Wrap(err, "compaction before sync failed"), "user_guid", userGUID)
		} else {
			logger.Info("Compacted before sync",
				"changes_before", res.ChangesBefore, "changes_after", res.ChangesAfter)
		}
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

// SyncControlSnooze handles POST /api/v1/sync/control/snooze
// Defers the "sync is due" prompt without syncing.
//
// Request body (optional): {"duration": "30m"}. Omitted, the deferral is the
// configured prompt interval — "not now" meaning "ask me next time you would
// have asked". A zero or negative duration clears the snooze instead, which
// is how a UI un-dismisses a prompt it hid by mistake.
func SyncControlSnooze(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		return writeError(ctx, http.StatusServiceUnavailable, "sync is not configured")
	}

	var req struct {
		Duration string `json:"duration"`
	}
	if body := ctx.Request().Body(); len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}

	snooze := client.SnoozeDuration()
	if req.Duration != "" {
		parsed, err := time.ParseDuration(req.Duration)
		if err != nil {
			return writeError(ctx, http.StatusBadRequest,
				"invalid duration, expected a value like '30m' or '2h'")
		}
		snooze = parsed
	}

	client.Snooze(snooze)
	return writeSuccess(ctx, http.StatusOK, client.GetStatus())
}

// SyncControlMode handles POST /api/v1/sync/control/mode
// Switches between prompt mode (nothing syncs unasked) and auto mode (a cycle
// every interval) at runtime.
//
// Request body: {"mode": "prompt"} or {"mode": "auto"}, optionally with
// {"persist": true} to write GONOTES_SYNC_MODE into config/cfg_files/.env so
// the choice survives a restart.
//
// Persisting is opt-in rather than automatic because the two are genuinely
// different requests: "sync in the background for the rest of this afternoon"
// is not "sync in the background from now on", and only one of them should
// edit a file holding the hub credentials. The response says which mode is now
// live, and whether it was written down.
func SyncControlMode(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		return writeError(ctx, http.StatusServiceUnavailable, "sync is not configured")
	}

	var req struct {
		Mode    string `json:"mode"`
		Persist bool   `json:"persist"`
	}
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid request body")
	}

	mode, err := models.ParseSyncMode(req.Mode)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, err.Error())
	}
	if req.Mode == "" {
		// ParseSyncMode treats empty as "the default", which is right when
		// reading configuration and wrong here: an empty field in a request
		// body is a caller that forgot to say, not one asking for prompt mode.
		return writeError(ctx, http.StatusBadRequest, "mode is required: 'prompt' or 'auto'")
	}
	if err := client.SetMode(mode); err != nil {
		return writeError(ctx, http.StatusBadRequest, err.Error())
	}

	persisted := false
	if req.Persist {
		if err := setEnvFileValue("GONOTES_SYNC_MODE", string(mode)); err != nil {
			// The live mode already changed, and saying so is more useful than
			// pretending the whole request failed — the caller needs to know
			// which half took.
			logger.LogErr(serr.Wrap(err, "failed to persist sync mode"), "user_guid", userGUID)
			return writeSuccess(ctx, http.StatusOK, map[string]any{
				"status":    client.GetStatus(),
				"persisted": false,
				"warning":   "mode changed for this session, but could not be written to the .env file",
			})
		}
		persisted = true
		logger.Info("Sync mode persisted to env file", "mode", string(mode))
	}

	return writeSuccess(ctx, http.StatusOK, map[string]any{
		"status":    client.GetStatus(),
		"persisted": persisted,
	})
}

// SyncControlCompact handles POST /api/v1/sync/control/compact
// Collapses the pending (unsynced) change log to one change per entity and
// reports what it did.
//
// Offered on its own, separate from sync-now's compact flag, because the two
// answer different questions: this one is "my change log has grown while I
// have been offline and I want it tidied", which is worth being able to do
// while the hub is unreachable — exactly when it accumulates.
func SyncControlCompact(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	client := models.GetSyncClient()
	if client == nil {
		return writeError(ctx, http.StatusServiceUnavailable, "sync is not configured")
	}

	res, err := client.Compact()
	if err != nil {
		logger.LogErr(serr.Wrap(err, "change log compaction failed"), "user_guid", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to compact changes")
	}

	return writeSuccess(ctx, http.StatusOK, map[string]any{
		"compaction": res,
		"status":     client.GetStatus(),
	})
}
