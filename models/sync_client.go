package models

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Client (Phase 4)
//
// The sync client runs as a background goroutine on spoke instances. It
// authenticates with the hub, pulls new changes (with conflict resolution),
// pushes local changes, and periodically verifies consistency via checksums.
//
// Design decisions:
//   - Single goroutine + mutex: the polling timer and "Sync Now" button both
//     call runSyncCycle protected by syncMu. No channel complexity needed.
//   - Exponential backoff: consecutive failures increase wait time up to 5m,
//     reset on success. Prevents hammering a downed hub.
//   - Auth token is cached in memory and persisted to sync_state so the
//     client survives restarts without re-authenticating every time.
//   - Package-level singleton follows the existing var db / var cacheDB pattern.
// ============================================================================

// syncClientInstance is the package-level singleton for the sync client.
// Follows the same pattern as var db and var cacheDB in db.go.
var syncClientInstance *SyncClient

// SyncClient manages the background sync loop between a spoke and hub.
type SyncClient struct {
	config     *SyncConfig
	peerID     string
	authToken  string
	httpClient *http.Client
	syncMu     sync.Mutex  // Prevents concurrent sync cycles
	enabled    atomic.Bool // Runtime toggle for the "enable sync" checkbox
	cancelFunc context.CancelFunc
	lastSync   time.Time
	lastError  error
	inProgress atomic.Bool // True while a sync cycle is running

	// Exponential backoff state — consecutive failures increase wait time.
	// Cap at maxBackoff to avoid indefinitely long pauses.
	consecutiveFailures int
}

// maxBackoff caps the exponential backoff to prevent excessively long waits
// between retries when the hub is down for an extended period.
const maxBackoff = 5 * time.Minute

// SyncClientStatus exposes sync state to the UI without leaking internal details.
type SyncClientStatus struct {
	Enabled    bool       `json:"enabled"`
	Connected  bool       `json:"connected"` // True if last sync succeeded
	LastSync   *time.Time `json:"last_sync"` // nil if never synced
	InProgress bool       `json:"in_progress"`
	LastError  string     `json:"last_error,omitempty"`
	PeerID     string     `json:"peer_id"`
}

// DDL for sync_state — persists peer identity and auth tokens across restarts.
// Keyed by hub_url so a spoke could theoretically sync with multiple hubs
// (though the current design assumes one).
const DDLCreateSyncStateTable = `
CREATE TABLE IF NOT EXISTS sync_state (
    hub_url       VARCHAR PRIMARY KEY,
    peer_id       VARCHAR NOT NULL,
    last_push_at  TIMESTAMP,
    last_pull_at  TIMESTAMP,
    last_sync_at  TIMESTAMP,
    auth_token    VARCHAR,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

// NewSyncClient creates and configures a sync client.
// Loads or generates the peer ID from the sync_state table so it remains
// stable across restarts — this is critical for the hub's per-peer change
// tracking to work correctly.
func NewSyncClient(config *SyncConfig) (*SyncClient, error) {
	if err := config.Validate(); err != nil {
		return nil, serr.Wrap(err, "invalid sync config")
	}

	client := &SyncClient{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	client.enabled.Store(config.Enabled)

	// Load or generate a stable peer ID from the database
	state, err := GetOrCreateSyncState(config.HubURL)
	if err != nil {
		return nil, serr.Wrap(err, "failed to initialize sync state")
	}
	client.peerID = state.PeerID

	// Restore cached auth token if available (avoids unnecessary login on restart)
	if state.AuthToken.Valid && state.AuthToken.String != "" {
		client.authToken = state.AuthToken.String
	}

	syncClientInstance = client
	return client, nil
}

// GetSyncClient returns the package-level sync client instance.
// Returns nil if sync is not configured — callers must nil-check.
func GetSyncClient() *SyncClient {
	return syncClientInstance
}

// Start launches the background sync goroutine.
// The first cycle runs immediately (passive sync on startup),
// then subsequent cycles run on the configured interval.
func (sc *SyncClient) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	sc.cancelFunc = cancel

	go sc.syncLoop(ctx)
	logger.Info("Sync client started",
		"hub_url", sc.config.HubURL,
		"peer_id", sc.peerID,
		"interval", sc.config.Interval.String(),
	)
}

// Stop gracefully shuts down the sync client.
func (sc *SyncClient) Stop() {
	if sc.cancelFunc != nil {
		sc.cancelFunc()
	}
	logger.Info("Sync client stopped")
}

// SyncNow triggers an immediate sync cycle (for the "Sync Now" button).
// Returns an error if a sync is already in progress.
func (sc *SyncClient) SyncNow() error {
	if !sc.enabled.Load() {
		return serr.New("sync is disabled")
	}
	if sc.inProgress.Load() {
		return serr.New("sync already in progress")
	}

	// Run synchronously so the caller knows when it completes
	return sc.runSyncCycle(context.Background())
}

// SetEnabled toggles sync on/off at runtime (for the UI checkbox).
func (sc *SyncClient) SetEnabled(enabled bool) {
	sc.enabled.Store(enabled)
	logger.Info("Sync client toggled", "enabled", enabled)
}

// IsEnabled returns whether sync is currently active.
func (sc *SyncClient) IsEnabled() bool {
	return sc.enabled.Load()
}

// GetStatus returns the current sync state for UI display.
func (sc *SyncClient) GetStatus() *SyncClientStatus {
	status := &SyncClientStatus{
		Enabled:    sc.enabled.Load(),
		Connected:  sc.consecutiveFailures == 0 && !sc.lastSync.IsZero(),
		InProgress: sc.inProgress.Load(),
		PeerID:     sc.peerID,
	}
	if !sc.lastSync.IsZero() {
		status.LastSync = &sc.lastSync
	}
	if sc.lastError != nil {
		status.LastError = sc.lastError.Error()
	}
	return status
}

// syncLoop is the background goroutine that runs sync cycles on a timer.
// It runs immediately on startup, then waits for the configured interval
// (or exponential backoff on failure) before each subsequent cycle.
func (sc *SyncClient) syncLoop(ctx context.Context) {
	// Run first cycle immediately (startup sync)
	if sc.enabled.Load() {
		if err := sc.runSyncCycle(ctx); err != nil {
			logger.LogErr(err, "initial sync cycle failed")
		}
	}

	ticker := time.NewTicker(sc.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sc.enabled.Load() {
				continue
			}

			// Apply exponential backoff if we've had consecutive failures.
			// The ticker still fires at the normal interval, but we skip
			// cycles until the backoff period has elapsed.
			if sc.consecutiveFailures > 0 {
				backoff := sc.calculateBackoff()
				timeSinceLastSync := time.Since(sc.lastSync)
				if timeSinceLastSync < backoff {
					continue // Still in backoff period
				}
			}

			if err := sc.runSyncCycle(ctx); err != nil {
				logger.LogErr(err, "sync cycle failed",
					"consecutive_failures", sc.consecutiveFailures,
				)
			}
		}
	}
}

// runSyncCycle executes one full sync cycle: health → auth → pull → push → verify.
// Protected by syncMu to prevent the timer and SyncNow from racing.
func (sc *SyncClient) runSyncCycle(ctx context.Context) error {
	if !sc.syncMu.TryLock() {
		return nil // Another cycle is running; skip this one
	}
	defer sc.syncMu.Unlock()

	sc.inProgress.Store(true)
	defer sc.inProgress.Store(false)

	// Step 1: Health check — verify hub is reachable before doing real work
	if err := sc.healthCheck(ctx); err != nil {
		sc.recordFailure(err)
		return serr.Wrap(err, "hub health check failed")
	}

	// Step 2: Authenticate (or reuse cached token)
	if err := sc.authenticate(ctx); err != nil {
		sc.recordFailure(err)
		return serr.Wrap(err, "authentication failed")
	}

	// Step 3: Pull changes from hub (with conflict resolution)
	if err := sc.pullChanges(ctx); err != nil {
		sc.recordFailure(err)
		return serr.Wrap(err, "pull changes failed")
	}

	// Step 4: Push local changes to hub
	if err := sc.pushChanges(ctx); err != nil {
		sc.recordFailure(err)
		return serr.Wrap(err, "push changes failed")
	}

	// Step 5: Verify consistency (advisory — mismatch is logged, not fatal)
	if err := sc.verifyConsistency(ctx); err != nil {
		logger.LogErr(err, "consistency verification failed (advisory)")
	}

	// Success — reset backoff and record timestamps
	sc.consecutiveFailures = 0
	sc.lastError = nil
	sc.lastSync = time.Now()
	if err := UpdateSyncTimestamps(sc.config.HubURL); err != nil {
		logger.LogErr(err, "failed to persist sync timestamps")
	}

	logger.Info("Sync cycle completed successfully", "peer_id", sc.peerID)
	return nil
}

// healthCheck pings the hub's health endpoint to verify connectivity.
func (sc *SyncClient) healthCheck(ctx context.Context) error {
	url := sc.config.HubURL + "/api/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return serr.Wrap(err, "failed to create health check request")
	}

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return serr.Wrap(err, "health check request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return serr.New(fmt.Sprintf("health check returned status %d", resp.StatusCode))
	}
	return nil
}

// authenticate obtains a JWT from the hub. Reuses the cached token if it's
// still valid (determined by trying authenticated requests first and falling
// back to login on 401).
func (sc *SyncClient) authenticate(ctx context.Context) error {
	// If we have a cached token, try it first — tokens last 7 days,
	// so most of the time this saves a round trip
	if sc.authToken != "" {
		return nil // Will re-auth on 401 during pull/push
	}

	return sc.login(ctx)
}

// login posts credentials to the hub's auth endpoint and caches the JWT.
func (sc *SyncClient) login(ctx context.Context) error {
	url := sc.config.HubURL + "/api/v1/auth/login"

	body, err := json.Marshal(map[string]string{
		"username": sc.config.Username,
		"password": sc.config.Password,
	})
	if err != nil {
		return serr.Wrap(err, "failed to marshal login request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return serr.Wrap(err, "failed to create login request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return serr.Wrap(err, "login request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return serr.New(fmt.Sprintf("login failed with status %d", resp.StatusCode))
	}

	// The login endpoint returns APIResponse { success, data: { user, token } }
	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return serr.Wrap(err, "failed to decode login response")
	}
	if !apiResp.Success || apiResp.Data.Token == "" {
		return serr.New("login response missing token")
	}

	sc.authToken = apiResp.Data.Token

	// Persist token for reuse across restarts
	if err := UpdateSyncAuthToken(sc.config.HubURL, sc.authToken); err != nil {
		logger.LogErr(err, "failed to persist auth token")
	}

	return nil
}

// doAuthenticatedRequest sends an HTTP request with the cached JWT.
// On 401, it re-authenticates once and retries. This handles token expiry
// transparently so callers don't need retry logic.
func (sc *SyncClient) doAuthenticatedRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, serr.Wrap(err, "failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sc.authToken)

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return nil, serr.Wrap(err, "request failed")
	}

	// On 401, re-authenticate once and retry
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()

		if err := sc.login(ctx); err != nil {
			return nil, serr.Wrap(err, "re-authentication failed after 401")
		}

		// Rebuild request with new token (body may have been consumed)
		req, err = http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, serr.Wrap(err, "failed to create retry request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sc.authToken)

		resp, err = sc.httpClient.Do(req)
		if err != nil {
			return nil, serr.Wrap(err, "retry request failed")
		}
	}

	return resp, nil
}

// pullChanges fetches unsynced changes from the hub and applies them locally.
// Pulls in batches (has_more pagination) until all changes are consumed.
// Each change is checked for conflicts before application.
func (sc *SyncClient) pullChanges(ctx context.Context) error {
	hasMore := true

	for hasMore {
		url := fmt.Sprintf("%s/api/v1/sync/pull?peer_id=%s&limit=100", sc.config.HubURL, sc.peerID)
		resp, err := sc.doAuthenticatedRequest(ctx, http.MethodGet, url, nil)
		if err != nil {
			return serr.Wrap(err, "pull request failed")
		}

		var apiResp struct {
			Success bool             `json:"success"`
			Data    SyncPullResponse `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			resp.Body.Close()
			return serr.Wrap(err, "failed to decode pull response")
		}
		resp.Body.Close()

		if !apiResp.Success {
			return serr.New("pull request returned success=false")
		}

		// Apply each change with conflict detection
		for _, change := range apiResp.Data.Changes {
			if err := sc.applyChangeWithConflictDetection(change); err != nil {
				// Log and continue — one bad change shouldn't block the whole pull
				logger.LogErr(err, "failed to apply pulled change",
					"change_guid", change.GUID,
					"entity_type", change.EntityType,
					"entity_guid", change.EntityGUID,
				)
			}
		}

		hasMore = apiResp.Data.HasMore

		if len(apiResp.Data.Changes) > 0 {
			logger.Info("Pulled changes from hub",
				"count", len(apiResp.Data.Changes),
				"has_more", hasMore,
			)
		}
	}

	return nil
}

// applyChangeWithConflictDetection wraps ApplyIncomingSyncChange with
// Phase 3 conflict detection. If a conflict exists, it resolves it
// automatically and logs the result.
func (sc *SyncClient) applyChangeWithConflictDetection(change SyncChange) error {
	var hasConflict bool
	var localAsSyncChange SyncChange

	// Check for conflicts based on entity type
	switch change.EntityType {
	case "note":
		localChange, err := DetectNoteConflict(change)
		if err != nil {
			return serr.Wrap(err, "conflict detection failed for note")
		}
		if localChange != nil {
			hasConflict = true
			// Build a SyncChange envelope from the local NoteChange for resolution.
			// We need the authored_at from disk to compare timestamps.
			localAsSyncChange = SyncChange{
				GUID:       localChange.GUID,
				EntityType: "note",
				EntityGUID: localChange.NoteGUID,
				Operation:  localChange.Operation,
				CreatedAt:  localChange.CreatedAt,
			}
			// Fetch authored_at from the note itself for LWW comparison
			var authoredAt sql.NullTime
			_ = db.QueryRow(`SELECT authored_at FROM notes WHERE guid = ?`, localChange.NoteGUID).Scan(&authoredAt)
			if authoredAt.Valid {
				localAsSyncChange.AuthoredAt = authoredAt.Time
			}
		}

	case "category":
		localChange, err := DetectCategoryConflict(change)
		if err != nil {
			return serr.Wrap(err, "conflict detection failed for category")
		}
		if localChange != nil {
			hasConflict = true
			localAsSyncChange = SyncChange{
				GUID:       localChange.GUID,
				EntityType: "category",
				EntityGUID: localChange.CategoryGUID,
				Operation:  localChange.Operation,
				CreatedAt:  localChange.CreatedAt,
			}
		}
	}

	// If there's a conflict, resolve it before applying
	if hasConflict {
		winner, resolution, err := ResolveConflict(localAsSyncChange, change)
		if err != nil {
			return serr.Wrap(err, "conflict resolution failed")
		}

		// Log the conflict for audit trail
		InsertSyncConflict(change.EntityType, change.EntityGUID, localAsSyncChange, change, resolution)

		logger.Info("Sync conflict resolved",
			"entity_type", change.EntityType,
			"entity_guid", change.EntityGUID,
			"resolution", resolution,
		)

		// If local wins, skip applying the remote change
		if winner.GUID == localAsSyncChange.GUID {
			return nil
		}
		// Otherwise fall through to apply the remote change
	}

	// Apply the change (idempotent — duplicate GUIDs are no-ops)
	return ApplyIncomingSyncChange(change)
}

// pushChanges builds a batch of local unsent changes and sends them to the hub.
func (sc *SyncClient) pushChanges(ctx context.Context) error {
	// Use the same unified change stream that the hub uses for pulls,
	// but from our local perspective: changes not yet sent to the hub.
	response, err := GetUnifiedChangesForPeer(sc.peerID, 100)
	if err != nil {
		return serr.Wrap(err, "failed to get local changes for push")
	}

	if len(response.Changes) == 0 {
		return nil // Nothing to push
	}

	pushReq := SyncPushRequest{
		PeerID:  sc.peerID,
		Changes: response.Changes,
	}

	body, err := json.Marshal(pushReq)
	if err != nil {
		return serr.Wrap(err, "failed to marshal push request")
	}

	url := sc.config.HubURL + "/api/v1/sync/push"
	resp, err := sc.doAuthenticatedRequest(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return serr.Wrap(err, "push request failed")
	}
	defer resp.Body.Close()

	var apiResp struct {
		Success bool             `json:"success"`
		Data    SyncPushResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return serr.Wrap(err, "failed to decode push response")
	}

	if !apiResp.Success {
		return serr.New("push request returned success=false")
	}

	// Mark accepted changes as synced so they won't be pushed again
	MarkSyncChangesForPeer(response.Changes, sc.peerID)

	if len(apiResp.Data.Rejected) > 0 {
		logger.Info("Some changes rejected by hub",
			"accepted", len(apiResp.Data.Accepted),
			"rejected", len(apiResp.Data.Rejected),
		)
	} else {
		logger.Info("Pushed changes to hub", "count", len(apiResp.Data.Accepted))
	}

	return nil
}

// verifyConsistency compares local and remote checksums to detect data divergence.
// This is advisory — a mismatch is logged as a warning but doesn't fail the cycle.
// Over time, continued syncing will converge the data sets.
func (sc *SyncClient) verifyConsistency(ctx context.Context) error {
	url := sc.config.HubURL + "/api/v1/sync/status"
	resp, err := sc.doAuthenticatedRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return serr.Wrap(err, "status request failed")
	}
	defer resp.Body.Close()

	var apiResp struct {
		Success bool               `json:"success"`
		Data    SyncStatusResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return serr.Wrap(err, "failed to decode status response")
	}

	localStatus, err := GetSyncStatus()
	if err != nil {
		return serr.Wrap(err, "failed to get local sync status")
	}

	if localStatus.Checksum != apiResp.Data.Checksum {
		logger.Info("Checksum mismatch between local and hub (will converge over time)",
			"local_checksum", localStatus.Checksum,
			"hub_checksum", apiResp.Data.Checksum,
			"local_notes", localStatus.NoteCount,
			"hub_notes", apiResp.Data.NoteCount,
			"local_categories", localStatus.CategoryCount,
			"hub_categories", apiResp.Data.CategoryCount,
		)
	}

	return nil
}

// recordFailure updates backoff state after a failed sync cycle.
func (sc *SyncClient) recordFailure(err error) {
	sc.consecutiveFailures++
	sc.lastError = err
}

// calculateBackoff returns the wait duration based on consecutive failures.
// Uses exponential backoff: 1s, 2s, 4s, 8s, ... capped at maxBackoff.
func (sc *SyncClient) calculateBackoff() time.Duration {
	backoff := time.Second
	for i := 0; i < sc.consecutiveFailures; i++ {
		backoff *= 2
		if backoff > maxBackoff {
			return maxBackoff
		}
	}
	return backoff
}

// ============================================================================
// Sync State Persistence
//
// These functions manage the sync_state table which stores per-hub peer
// identity and timestamps. The peer ID must be stable across restarts
// for the hub's per-peer change tracking to function correctly.
// ============================================================================

// SyncState represents a row in the sync_state table.
type SyncState struct {
	HubURL     string
	PeerID     string
	LastPushAt sql.NullTime
	LastPullAt sql.NullTime
	LastSyncAt sql.NullTime
	AuthToken  sql.NullString
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// GetOrCreateSyncState loads the sync state for a hub URL, creating a new
// row with a fresh peer ID if none exists. The peer ID is a UUID that
// uniquely identifies this spoke to the hub.
func GetOrCreateSyncState(hubURL string) (*SyncState, error) {
	state := &SyncState{}
	err := db.QueryRow(
		`SELECT hub_url, peer_id, last_push_at, last_pull_at, last_sync_at, auth_token, created_at, updated_at
		 FROM sync_state WHERE hub_url = ?`, hubURL,
	).Scan(&state.HubURL, &state.PeerID, &state.LastPushAt, &state.LastPullAt,
		&state.LastSyncAt, &state.AuthToken, &state.CreatedAt, &state.UpdatedAt)

	if err == sql.ErrNoRows {
		// First time syncing with this hub — generate a new peer ID
		state.HubURL = hubURL
		state.PeerID = uuid.New().String()
		state.CreatedAt = time.Now()
		state.UpdatedAt = time.Now()

		_, err = db.Exec(
			`INSERT INTO sync_state (hub_url, peer_id, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			state.HubURL, state.PeerID, state.CreatedAt, state.UpdatedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to insert sync state")
		}

		logger.Info("Created new sync state", "hub_url", hubURL, "peer_id", state.PeerID)
		return state, nil
	}

	if err != nil {
		return nil, serr.Wrap(err, "failed to query sync state")
	}

	return state, nil
}

// UpdateSyncTimestamps records when the last successful sync cycle completed.
func UpdateSyncTimestamps(hubURL string) error {
	now := time.Now()
	_, err := db.Exec(
		`UPDATE sync_state SET last_sync_at = ?, last_pull_at = ?, last_push_at = ?, updated_at = ?
		 WHERE hub_url = ?`,
		now, now, now, now, hubURL,
	)
	if err != nil {
		return serr.Wrap(err, "failed to update sync timestamps")
	}
	return nil
}

// UpdateSyncAuthToken persists the JWT token for reuse across restarts.
func UpdateSyncAuthToken(hubURL, token string) error {
	_, err := db.Exec(
		`UPDATE sync_state SET auth_token = ?, updated_at = ? WHERE hub_url = ?`,
		token, time.Now(), hubURL,
	)
	if err != nil {
		return serr.Wrap(err, "failed to update sync auth token")
	}
	return nil
}
