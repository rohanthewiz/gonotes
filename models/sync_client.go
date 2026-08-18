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
// WHEN a cycle runs is decided by SyncConfig.Mode:
//
//	prompt (default)   the loop runs no cycles of its own. It keeps the clock
//	                   (Due / DueIn / PendingChanges) that a UI turns into a
//	                   question, and cycles happen when someone answers it —
//	                   SyncNow — or when the process is going away (SyncOnExit).
//	auto               the original timer: a cycle every Interval.
//
// Design decisions:
//   - Single goroutine + mutex: the polling timer and "Sync Now" button both
//     call runSyncCycle protected by syncMu. No channel complexity needed.
//   - stateMu guards the mutable bookkeeping (last sync, last error, backoff,
//     snooze). It has to: the loop writes it while an HTTP status poll or a
//     TUI keystroke reads it, and prompt mode makes those reads frequent
//     rather than incidental.
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
	inProgress atomic.Bool // True while a sync cycle is running

	// mode is the runtime copy of SyncConfig.Mode, held separately so the UI
	// can flip between prompt and auto without a restart. atomic.Value rather
	// than a stateMu field because the loop reads it on every tick.
	mode atomic.Value // SyncMode

	// stateMu guards everything below it. See the note in the file header.
	stateMu   sync.Mutex
	lastSync  time.Time
	lastError error

	// snoozeUntil suppresses the "sync is due" report until it passes. This is
	// what a UI's "not now" writes: due-ness is derived from lastSync, which a
	// dismissal must not touch (dismissing a prompt is not syncing), so the
	// deferral needs a home of its own.
	snoozeUntil time.Time

	// startedAt anchors due-ness for a spoke that has never synced. Without it
	// a first run is due the instant it starts, which is a prompt before the
	// user has typed anything — the least useful moment to ask.
	startedAt time.Time

	// exitDeclined records that a user has already been asked the exit
	// question and said no. Without it the two halves of "prompt or on exit"
	// contradict each other: the TUI's quit dialog IS the exit prompt, and
	// syncing anyway after someone chose "quit without syncing" would make
	// answering it pointless.
	exitDeclined bool

	// Exponential backoff state — consecutive failures increase wait time.
	// Cap at maxBackoff to avoid indefinitely long pauses.
	consecutiveFailures int
}

// maxBackoff caps the exponential backoff to prevent excessively long waits
// between retries when the hub is down for an extended period.
const maxBackoff = 5 * time.Minute

// exitSyncTimeout bounds the final cycle at shutdown. Long enough for a real
// push over a slow link, short enough that quitting an app never feels hung
// when the hub is simply not there — the changes stay pending either way and
// go out on the next sync.
const exitSyncTimeout = 20 * time.Second

// SyncClientStatus exposes sync state to the UI without leaking internal details.
//
// The second half of the struct is what prompt mode needs a UI to be able to
// render without asking a second question: whether to show the prompt (Due),
// what to say in it (Pending), when it will next appear (DueInSeconds), and
// which of the two options to offer alongside "sync now" (CompactBeforePush
// tells the UI whether compaction is already automatic).
type SyncClientStatus struct {
	Enabled    bool       `json:"enabled"`
	Connected  bool       `json:"connected"` // True if last sync succeeded
	LastSync   *time.Time `json:"last_sync"` // nil if never synced
	InProgress bool       `json:"in_progress"`
	LastError  string     `json:"last_error,omitempty"`
	PeerID     string     `json:"peer_id"`

	Mode              SyncMode   `json:"mode"`                    // "prompt" or "auto"
	Due               bool       `json:"due"`                     // prompt mode: it is time to ask
	DueInSeconds      int64      `json:"due_in_seconds"`          // seconds until Due (0 when due or in auto mode)
	PromptAfterSecs   int64      `json:"prompt_after_seconds"`    // the configured prompt interval
	Pending           int        `json:"pending_changes"`         // local changes not yet pushed
	SnoozedUntil      *time.Time `json:"snoozed_until,omitempty"` // nil when not snoozed
	CompactBeforePush bool       `json:"compact_before_push"`     // compaction runs automatically on push
	SyncOnExit        bool       `json:"sync_on_exit"`            // a final cycle runs at shutdown
}

// Schema for sync_state lives in schema.go (public database only). Keyed
// by hub_url so a spoke could theoretically sync with multiple hubs
// (though the current design assumes one).

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
	client.mode.Store(config.Mode)
	client.startedAt = time.Now()

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

	// Restore when this spoke last synced. In auto mode this only sharpened
	// the backoff; in prompt mode it is load-bearing — due-ness is measured
	// from it, and a client that forgot it on every restart would either never
	// prompt (a spoke restarted daily) or prompt on every launch.
	if state.LastSyncAt.Valid {
		client.lastSync = state.LastSyncAt.Time
	}

	syncClientInstance = client
	return client, nil
}

// Mode reports how cycles are currently triggered.
func (sc *SyncClient) Mode() SyncMode {
	if m, ok := sc.mode.Load().(SyncMode); ok && m != "" {
		return m
	}
	return SyncModePrompt
}

// SetMode switches between prompt and auto at runtime. Switching to auto does
// NOT run a cycle immediately — the loop picks it up on its next tick — so a
// user toggling the setting never triggers an upload as a side effect of
// changing a preference.
func (sc *SyncClient) SetMode(mode SyncMode) error {
	if mode != SyncModePrompt && mode != SyncModeAuto {
		return serr.New("invalid sync mode: " + string(mode))
	}
	sc.mode.Store(mode)
	// A mode change answers the question the snooze was deferring, so it
	// clears it: switching to auto makes it moot, and switching back to prompt
	// should ask rather than stay quiet on the strength of an old dismissal.
	sc.stateMu.Lock()
	sc.snoozeUntil = time.Time{}
	sc.stateMu.Unlock()

	logger.Info("Sync mode changed", "mode", string(mode))
	return nil
}

// GetSyncClient returns the package-level sync client instance.
// Returns nil if sync is not configured — callers must nil-check.
func GetSyncClient() *SyncClient {
	return syncClientInstance
}

// Start launches the background sync goroutine.
//
// In auto mode the first cycle runs immediately (passive sync on startup) and
// subsequent cycles run on the configured interval. In prompt mode the
// goroutine still runs — it is what notices a later switch to auto — but it
// syncs nothing until asked.
func (sc *SyncClient) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	sc.cancelFunc = cancel

	go sc.syncLoop(ctx)
	logger.Info("Sync client started",
		"hub_url", sc.config.HubURL,
		"peer_id", sc.peerID,
		"mode", string(sc.Mode()),
		"interval", sc.config.Interval.String(),
		"prompt_after", sc.config.PromptAfter.String(),
	)
}

// Stop gracefully shuts down the sync client.
func (sc *SyncClient) Stop() {
	if sc.cancelFunc != nil {
		sc.cancelFunc()
	}
	logger.Info("Sync client stopped")
}

// SyncNow triggers an immediate sync cycle (for the "Sync Now" button, and
// for the answer to a prompt-mode prompt).
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

// ============================================================================
// Prompt mode: the clock a UI reads
// ============================================================================

// LastSync reports when the last cycle succeeded — zero if this spoke has
// never synced with this hub.
func (sc *SyncClient) LastSync() time.Time {
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	return sc.lastSync
}

// failureCount reports the consecutive-failure count behind stateMu.
func (sc *SyncClient) failureCount() int {
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	return sc.consecutiveFailures
}

// dueSince is the moment due-ness is measured from: the last successful sync,
// or — for a spoke that has never managed one — when this process started.
// Anchoring an unsynced spoke to process start is what keeps a fresh install
// from opening with a prompt.
func (sc *SyncClient) dueSince() time.Time {
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	if sc.lastSync.IsZero() {
		return sc.startedAt
	}
	return sc.lastSync
}

// DueIn reports how long until a sync prompt is owed. Zero means it is owed
// now; a negative value never escapes (it is clamped to zero). In auto mode
// there is no prompt to be owed, so it always reports zero.
func (sc *SyncClient) DueIn() time.Duration {
	if !sc.enabled.Load() || sc.Mode() != SyncModePrompt {
		return 0
	}

	deadline := sc.dueSince().Add(sc.config.PromptAfter)

	// A snooze can only push the deadline out, never pull it in: "ask me
	// later" is a deferral, not a way to be asked sooner than the interval.
	sc.stateMu.Lock()
	snooze := sc.snoozeUntil
	sc.stateMu.Unlock()
	if snooze.After(deadline) {
		deadline = snooze
	}

	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Due reports whether a UI should be asking. It is deliberately independent
// of whether anything is pending: a cycle also PULLS, and a spoke that has
// written nothing for two hours may still be two hours behind the hub. What
// is pending shapes the wording of the prompt, not whether it appears — see
// PendingChanges.
func (sc *SyncClient) Due() bool {
	if !sc.enabled.Load() || sc.Mode() != SyncModePrompt {
		return false
	}
	if sc.inProgress.Load() {
		return false // a cycle is already answering the question
	}
	return sc.DueIn() == 0
}

// Snooze defers the prompt by d. A non-positive d clears the snooze instead,
// which is how "ask me again now" is expressed.
func (sc *SyncClient) Snooze(d time.Duration) {
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	if d <= 0 {
		sc.snoozeUntil = time.Time{}
		return
	}
	sc.snoozeUntil = time.Now().Add(d)
}

// DeclineExitSync records that the user was asked at quit time and declined.
// Set by a UI that has already put the question in front of them — the exit
// path must not then do it silently. Cleared by an explicit sync, so a later
// change of mind in the same session is honored.
func (sc *SyncClient) DeclineExitSync() {
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	sc.exitDeclined = true
}

// SnoozeDuration is the deferral a UI gets when it does not name one: the
// same interval as the prompt itself, so "not now" means "ask me next time
// you would have asked".
func (sc *SyncClient) SnoozeDuration() time.Duration {
	return sc.config.PromptAfter
}

// PendingChanges counts the local changes waiting to be pushed. Errors are
// swallowed into 0 — this feeds a status line, and a count that cannot be
// read is not worth failing a status poll over. The error is returned too, so
// a caller that cares can log it.
func (sc *SyncClient) PendingChanges() (int, error) {
	n, err := CountUnsentChangesForPeer(sc.peerID, "")
	if err != nil {
		return 0, serr.Wrap(err, "failed to count pending sync changes")
	}
	return n, nil
}

// Compact collapses the pending change log for this spoke's peer id. It is
// the explicit form of the same work GONOTES_SYNC_COMPACT does before every
// push, exposed so a UI can offer it as a choice at the moment the user is
// being asked to sync — which is exactly when a long unsynced log exists and
// the user is in a position to say what should happen to it.
func (sc *SyncClient) Compact() (*CompactionResult, error) {
	return CompactPendingChanges(sc.peerID, "")
}

// SyncOnExit runs the final cycle during shutdown, if one is warranted.
//
// This is the half of "prompt or on exit" that needs nobody present: a
// headless server stopping, a TUI whose user has answered the quit dialog, an
// app being quit. It is best-effort and bounded — a shutdown must not hang on
// a hub that has gone away — and it reports whether a cycle actually ran so a
// caller can say so.
func (sc *SyncClient) SyncOnExit(ctx context.Context) (ran bool, err error) {
	if !sc.config.SyncOnExit || !sc.enabled.Load() {
		return false, nil
	}

	sc.stateMu.Lock()
	declined := sc.exitDeclined
	sc.stateMu.Unlock()
	if declined {
		return false, nil
	}

	// Nothing pending and nothing overdue means the exit cycle would be a
	// round trip to confirm what is already true. A pull is still worth
	// running when the spoke is past its prompt interval, which is why this
	// is not simply "pending == 0, skip".
	pending, countErr := sc.PendingChanges()
	if countErr != nil {
		logger.LogErr(countErr, "could not count pending changes for exit sync")
	}
	if pending == 0 && sc.Mode() == SyncModePrompt && sc.DueIn() > 0 {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(ctx, exitSyncTimeout)
	defer cancel()

	logger.Info("Running final sync before exit", "pending_changes", pending)
	if err := sc.runSyncCycle(ctx); err != nil {
		return true, serr.Wrap(err, "exit sync failed")
	}
	return true, nil
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
//
// The snapshot is taken under stateMu in one go rather than field by field:
// a status that says "due in 0s" beside "last synced just now" is a lie
// assembled from two moments, and prompt mode puts this in front of a user
// deciding whether to act.
func (sc *SyncClient) GetStatus() *SyncClientStatus {
	sc.stateMu.Lock()
	lastSync := sc.lastSync
	lastErr := sc.lastError
	failures := sc.consecutiveFailures
	snooze := sc.snoozeUntil
	sc.stateMu.Unlock()

	status := &SyncClientStatus{
		Enabled:    sc.enabled.Load(),
		Connected:  failures == 0 && !lastSync.IsZero(),
		InProgress: sc.inProgress.Load(),
		PeerID:     sc.peerID,

		Mode:              sc.Mode(),
		Due:               sc.Due(),
		DueInSeconds:      int64(sc.DueIn().Seconds()),
		PromptAfterSecs:   int64(sc.config.PromptAfter.Seconds()),
		CompactBeforePush: sc.config.CompactBeforePush,
		SyncOnExit:        sc.config.SyncOnExit,
	}
	if !lastSync.IsZero() {
		status.LastSync = &lastSync
	}
	if lastErr != nil {
		status.LastError = lastErr.Error()
	}
	if snooze.After(time.Now()) {
		status.SnoozedUntil = &snooze
	}
	if pending, err := sc.PendingChanges(); err == nil {
		status.Pending = pending
	} else {
		logger.LogErr(err, "failed to count pending changes for sync status")
	}
	return status
}

// syncLoop is the background goroutine that runs sync cycles on a timer.
//
// The tick is a poll of the world rather than the schedule itself: it fires
// often enough to notice a runtime mode change or a lifted backoff, and each
// firing decides for itself whether a cycle is owed. That indirection is what
// lets prompt mode and auto mode share one goroutine — and what lets a user
// switch between them without a restart.
//
//	tick ──► enabled?  ──no──► wait
//	          │yes
//	          ▼
//	         mode==auto? ──no──► wait   (prompt mode syncs only when asked)
//	          │yes
//	          ▼
//	         interval elapsed and backoff clear? ──no──► wait
//	          │yes
//	          ▼
//	         runSyncCycle
func (sc *SyncClient) syncLoop(ctx context.Context) {
	// The startup cycle belongs to auto mode alone. In prompt mode it would be
	// the one thing the mode exists to prevent: a push nobody asked for, at
	// the moment of launch.
	if sc.enabled.Load() && sc.Mode() == SyncModeAuto {
		if err := sc.runSyncCycle(ctx); err != nil {
			logger.LogErr(err, "initial sync cycle failed")
		}
	}

	ticker := time.NewTicker(sc.tickInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sc.enabled.Load() || sc.Mode() != SyncModeAuto {
				continue
			}

			// The tick can be finer than the configured interval, so elapsed
			// time — not "a tick happened" — is what makes a cycle due.
			if time.Since(sc.LastSync()) < sc.config.Interval {
				continue
			}

			// Apply exponential backoff if we've had consecutive failures.
			// The ticker still fires at the normal interval, but we skip
			// cycles until the backoff period has elapsed.
			if sc.failureCount() > 0 {
				backoff := sc.calculateBackoff()
				timeSinceLastSync := time.Since(sc.LastSync())
				if timeSinceLastSync < backoff {
					continue // Still in backoff period
				}
			}

			if err := sc.runSyncCycle(ctx); err != nil {
				logger.LogErr(err, "sync cycle failed",
					"consecutive_failures", sc.failureCount(),
				)
			}
		}
	}
}

// tickInterval is how often the loop wakes to re-decide. It tracks the
// configured interval but never coarser than a minute — otherwise a spoke
// configured with a 6h auto interval would take up to 6h to notice that the
// user just switched it to prompt mode — and never finer than 10s.
func (sc *SyncClient) tickInterval() time.Duration {
	t := sc.config.Interval
	if t > time.Minute {
		t = time.Minute
	}
	if t < 10*time.Second {
		t = 10 * time.Second
	}
	return t
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

	// Success — reset backoff and record timestamps. Clearing the snooze here
	// matters in prompt mode: the deferral was of a question that has now been
	// answered, and leaving it set would suppress the NEXT prompt too.
	sc.stateMu.Lock()
	sc.consecutiveFailures = 0
	sc.lastError = nil
	sc.lastSync = time.Now()
	sc.snoozeUntil = time.Time{}
	sc.exitDeclined = false
	sc.stateMu.Unlock()

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
//
// If login fails and an invite token is configured, the client will
// auto-register on the hub first, then retry login. This enables hands-free
// spoke onboarding: set env vars and start — the first sync cycle handles
// registration automatically.
func (sc *SyncClient) authenticate(ctx context.Context) error {
	// If we have a cached token, try it first — tokens last 7 days,
	// so most of the time this saves a round trip
	if sc.authToken != "" {
		return nil // Will re-auth on 401 during pull/push
	}

	err := sc.login(ctx)
	if err == nil {
		return nil
	}

	// Login failed — if we have an invite token, try auto-registering
	if sc.config.InviteToken != "" {
		logger.Info("Login failed, attempting auto-registration with invite token")
		if regErr := sc.registerWithInviteToken(ctx); regErr != nil {
			return serr.Wrap(regErr, "auto-registration failed (original login error: "+err.Error()+")")
		}
		// Registration succeeded — now login with the new credentials
		return sc.login(ctx)
	}

	return err
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

// registerWithInviteToken sends a registration request to the hub using the
// configured invite token. Called automatically when login fails and an invite
// token is available — enables zero-touch spoke onboarding.
func (sc *SyncClient) registerWithInviteToken(ctx context.Context) error {
	url := sc.config.HubURL + "/api/v1/auth/register"

	body, err := json.Marshal(map[string]string{
		"username":     sc.config.Username,
		"password":     sc.config.Password,
		"invite_token": sc.config.InviteToken,
	})
	if err != nil {
		return serr.Wrap(err, "failed to marshal registration request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return serr.Wrap(err, "failed to create registration request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return serr.Wrap(err, "registration request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return serr.New(fmt.Sprintf("registration failed with status %d", resp.StatusCode))
	}

	logger.Info("Auto-registered on hub with invite token", "username", sc.config.Username)
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
			// Fetch authored_at from the note itself for LWW comparison.
			// getNoteAuthoredAt fans out across both databases.
			if at, err := getNoteAuthoredAt(localChange.NoteGUID); err == nil {
				localAsSyncChange.AuthoredAt = at
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
	if err := ApplyIncomingSyncChange(change); err != nil {
		return err
	}

	// The hub already has this change — it is what we just pulled from it. The
	// row applying it recorded carries the same GUID, so marking it here keeps
	// the next push from sending the hub its own change back. Symmetric with
	// what the hub does for a spoke's push; see MarkChangeGUIDSyncedToPeer.
	MarkChangeGUIDSyncedToPeer(change.GUID, sc.peerID)
	return nil
}

// pushChanges builds a batch of local unsent changes and sends them to the hub.
//
// When the spoke is configured to compact, that happens here rather than at
// the end of the previous cycle: the log to collapse is only fully known
// immediately before it is read, and doing it here means an "unsynced for six
// hours" log is compacted exactly once, on its way out.
func (sc *SyncClient) pushChanges(ctx context.Context) error {
	if sc.config.CompactBeforePush {
		if res, err := CompactPendingChanges(sc.peerID, ""); err != nil {
			// Compaction is an optimization; a failed one must not stop the
			// push it was meant to shrink.
			logger.LogErr(err, "failed to compact changes before push (pushing uncompacted)")
		} else if res.Removed() > 0 {
			logger.Info("Compacted change log before push",
				"changes_before", res.ChangesBefore, "changes_after", res.ChangesAfter)
		}
	}

	// Use the same unified change stream that the hub uses for pulls,
	// but from our local perspective: changes not yet sent to the hub.
	// Empty userGUID: spoke is single-user, no per-user filtering needed locally
	response, err := GetUnifiedChangesForPeer(sc.peerID, "", 100)
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

	// Empty userGUID: spoke is single-user, no per-user filtering needed
	localStatus, err := GetSyncStatus("")
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
	sc.stateMu.Lock()
	defer sc.stateMu.Unlock()
	sc.consecutiveFailures++
	sc.lastError = err
}

// calculateBackoff returns the wait duration based on consecutive failures.
// Uses exponential backoff: 1s, 2s, 4s, 8s, ... capped at maxBackoff.
func (sc *SyncClient) calculateBackoff() time.Duration {
	failures := sc.failureCount()
	backoff := time.Second
	for i := 0; i < failures; i++ {
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
	err := pubDB.QueryRow(
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

		_, err = pubDB.Exec(
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
	_, err := pubDB.Exec(
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
	_, err := pubDB.Exec(
		`UPDATE sync_state SET auth_token = ?, updated_at = ? WHERE hub_url = ?`,
		token, time.Now(), hubURL,
	)
	if err != nil {
		return serr.Wrap(err, "failed to update sync auth token")
	}
	return nil
}
