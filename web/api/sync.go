package api

import (
	"encoding/json"
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

// PullChanges handles GET /api/v1/sync/pull
// Returns a unified, chronologically ordered stream of note and category
// changes that haven't been sent to the requesting peer yet.
//
// Query parameters:
//   - peer_id: Unique identifier for the requesting peer (required)
//   - limit:   Maximum number of changes to return (optional, default: 100)
//
// The response includes a has_more flag so the client knows whether to
// issue another pull request for the remaining changes.
func PullChanges(ctx rweb.Context) error {
	// Authentication required for sync operations
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Parse peer_id (required — each spoke has a stable identity)
	peerID := ctx.Request().QueryParam("peer_id")
	if peerID == "" {
		return writeError(ctx, http.StatusBadRequest, "peer_id parameter is required")
	}

	// Parse optional limit (defaults to 100 in GetUnifiedChangesForPeer)
	limit := 100
	if limitStr := ctx.Request().QueryParam("limit"); limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			return writeError(ctx, http.StatusBadRequest, "invalid limit parameter")
		}
		limit = parsedLimit
	}

	// Fetch unified changes for this peer
	response, err := models.GetUnifiedChangesForPeer(peerID, limit)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get unified changes for peer"), "pull error")
		return writeError(ctx, http.StatusInternalServerError, "failed to retrieve changes")
	}

	// Mark the returned changes as synced to this peer so they aren't
	// returned on the next pull
	models.MarkSyncChangesForPeer(response.Changes, peerID)

	logger.Info("Sync pull completed",
		"peer_id", peerID,
		"count", len(response.Changes),
		"has_more", response.HasMore,
	)

	return writeSuccess(ctx, http.StatusOK, response)
}

// PushChanges handles POST /api/v1/sync/push
// Accepts a batch of SyncChanges from a peer and applies them locally.
// Each change is checked for idempotency (duplicate GUIDs are accepted
// silently) and dispatched to the appropriate ApplySync* function.
//
// Request body: SyncPushRequest { peer_id, changes[] }
// Response: SyncPushResponse { accepted[], rejected[] }
func PushChanges(ctx rweb.Context) error {
	// Authentication required
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Parse request body
	var req models.SyncPushRequest
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to decode sync push request"), "invalid JSON")
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}

	if req.PeerID == "" {
		return writeError(ctx, http.StatusBadRequest, "peer_id is required")
	}

	// Process each change — collect accepted/rejected results
	var accepted []string
	var rejected []models.SyncPushRejection

	for _, change := range req.Changes {
		err := models.ApplyIncomingSyncChange(change)
		if err != nil {
			logger.LogErr(err, "failed to apply incoming sync change",
				"change_guid", change.GUID,
				"entity_type", change.EntityType,
			)
			rejected = append(rejected, models.SyncPushRejection{
				GUID:   change.GUID,
				Reason: err.Error(),
			})
			continue
		}
		accepted = append(accepted, change.GUID)
	}

	// Return empty slices instead of nil for clean JSON serialization
	if accepted == nil {
		accepted = []string{}
	}
	if rejected == nil {
		rejected = []models.SyncPushRejection{}
	}

	logger.Info("Sync push completed",
		"peer_id", req.PeerID,
		"accepted", len(accepted),
		"rejected", len(rejected),
	)

	return writeSuccess(ctx, http.StatusOK, models.SyncPushResponse{
		Accepted: accepted,
		Rejected: rejected,
	})
}

// GetSnapshot handles GET /api/v1/sync/snapshot
// Returns the full current state of a single entity (note or category)
// as a SyncChange with operation=Create and all fields populated.
// Useful for initial sync or conflict resolution.
//
// Query parameters:
//   - entity_type: "note" or "category" (required)
//   - entity_guid: GUID of the entity to snapshot (required)
func GetSnapshot(ctx rweb.Context) error {
	// Authentication required
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	entityType := ctx.Request().QueryParam("entity_type")
	entityGUID := ctx.Request().QueryParam("entity_guid")

	if entityType == "" || entityGUID == "" {
		return writeError(ctx, http.StatusBadRequest, "entity_type and entity_guid parameters are required")
	}

	if entityType != "note" && entityType != "category" {
		return writeError(ctx, http.StatusBadRequest, "entity_type must be 'note' or 'category'")
	}

	snapshot, err := models.GetEntitySnapshot(entityType, entityGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get entity snapshot"), "snapshot error")
		return writeError(ctx, http.StatusNotFound, "entity not found")
	}

	return writeSuccess(ctx, http.StatusOK, snapshot)
}

// GetSyncStatus handles GET /api/v1/sync/status
// Returns note/category counts and a content-based checksum.
// Peers compare checksums to quickly detect data divergence.
func GetSyncStatus(ctx rweb.Context) error {
	// Authentication required
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	status, err := models.GetSyncStatus()
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get sync status"), "status error")
		return writeError(ctx, http.StatusInternalServerError, "failed to retrieve sync status")
	}

	return writeSuccess(ctx, http.StatusOK, status)
}

// HealthCheck handles GET /api/v1/health
// A lightweight, unauthenticated endpoint that returns 200 OK if the
// server is running. Used by peers and monitoring systems.
func HealthCheck(ctx rweb.Context) error {
	return writeSuccess(ctx, http.StatusOK, map[string]string{"status": "ok"})
}
