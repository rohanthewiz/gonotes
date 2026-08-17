package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
)

// locks.go is the HTTP door to the note-lock registry (models/lock.go): the
// endpoints a session uses to claim a note before editing it, keep the claim
// alive while it does, and give it back afterwards.
//
// The server is the only process that can arbitrate — bytdb is single-process,
// so every other session reaches these notes through this one — which is why
// the registry lives behind an API rather than in each client.
//
// The lifecycle one editing session runs through:
//
//	POST   /notes/42/lock            acquire → {token, holder, expires_at}
//	PUT    /notes/42/lock            renew, every models.LockHeartbeat
//	PUT    /notes/42   X-GoNotes-Lock: <token>    the write the lock was for
//	DELETE /notes/42/lock            release
//
// and the two reads that let a UI show what is going on without taking part:
//
//	GET    /notes/42/lock            one note's lease, or 404
//	GET    /note-locks               every lease this user holds anywhere

// LockHeaderName is the header a write presents its lock token in.
//
// A header rather than a body field or a query parameter, for one practical
// reason: it applies uniformly to PUT (JSON body), DELETE (no body), and the
// msgpack-encoded update path, none of which share a place to put it
// otherwise. Callers that hold no lock simply omit it.
const LockHeaderName = "X-GoNotes-Lock"

// lockAcquireRequest is the POST body: who is asking. It mirrors
// models.LockHolder rather than embedding it so the wire shape is stated here,
// where the contract with non-Go clients lives, instead of being whatever the
// internal struct happens to look like this week.
type lockAcquireRequest struct {
	SessionID  string `json:"session_id"`
	Label      string `json:"label,omitempty"`
	Host       string `json:"host,omitempty"`
	PaneHandle string `json:"pane_handle,omitempty"`
	Client     string `json:"client,omitempty"`
}

func (r lockAcquireRequest) holder() models.LockHolder {
	return models.LockHolder{
		SessionID:  r.SessionID,
		Label:      r.Label,
		Host:       r.Host,
		PaneHandle: r.PaneHandle,
		Client:     r.Client,
	}
}

// writeConflict sends 409 with both a human message and the machine-readable
// detail a client needs to offer the user a choice.
//
// The detail is the whole point of the status code here. A 409 that says only
// "conflict" leaves a TUI with nothing to render but the word; a 409 carrying
// the blocking lease lets it say who has the note and offer to jump to their
// pane, and a 409 carrying the current note lets a stale save show what it
// lost to without a second round trip.
func writeConflict(ctx rweb.Context, message string, detail interface{}) error {
	ctx.SetStatus(http.StatusConflict)
	return ctx.WriteJSON(APIResponse{Success: false, Error: message, Data: detail})
}

// lockConflictDetail is the 409 body when a lock blocks the request.
type lockConflictDetail struct {
	Reason string           `json:"reason"` // always "locked"
	Lock   *models.NoteLock `json:"lock"`   // redacted by the registry
}

// staleConflictDetail is the 409 body when the version guard blocks the write.
type staleConflictDetail struct {
	Reason          string             `json:"reason"` // always "stale"
	ExpectedVersion int64              `json:"expected_version"`
	Current         *models.NoteOutput `json:"current,omitempty"`
}

// noteLockParams resolves the authenticated user and the :id path parameter,
// writing the error response itself when either fails. ok false means a
// response has already been sent and the handler must return err.
func noteLockParams(ctx rweb.Context) (userGUID string, id int64, err error, ok bool) {
	userGUID = GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return "", 0, writeError(ctx, http.StatusUnauthorized, "authentication required"), false
	}
	id, parseErr := strconv.ParseInt(ctx.Request().Param("id"), 10, 64)
	if parseErr != nil {
		return "", 0, writeError(ctx, http.StatusBadRequest, "invalid note id"), false
	}
	return userGUID, id, nil, true
}

// ownsNote reports whether the user owns a live note with this id.
//
// Every lock endpoint checks this first, and answers 404 rather than 403 when
// it fails — the same answer GetNote gives for a note belonging to someone
// else. Otherwise the lock endpoints would become an oracle: POST a lock on
// each id in turn and the status codes map out another user's note ids.
func ownsNote(id int64, userGUID string) (bool, error) {
	note, err := models.GetNoteByID(id, userGUID)
	if err != nil {
		return false, err
	}
	return note != nil, nil
}

// AcquireNoteLock handles POST /api/v1/notes/:id/lock
//
// Claims the note for the calling session, or reports who has it. ?steal=true
// takes it by force — see models.AcquireNoteLock for why that is an explicit
// request and never an automatic retry.
//
// The response carries the TOKEN, which no other endpoint ever returns: this
// is the one moment a session is handed its authority to write, and it is
// handed only to the session that just proved it could take the lock.
func AcquireNoteLock(ctx rweb.Context) error {
	userGUID, id, errResp, ok := noteLockParams(ctx)
	if !ok {
		return errResp
	}

	var req lockAcquireRequest
	if body := ctx.Request().Body(); len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
		}
	}
	if req.SessionID == "" {
		return writeError(ctx, http.StatusBadRequest, "session_id is required")
	}

	owned, err := ownsNote(id, userGUID)
	if err != nil {
		logger.LogErr(err, "failed to look up note for lock", "id", id)
		return writeError(ctx, http.StatusInternalServerError, "failed to lock note")
	}
	if !owned {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	steal := queryFlag(ctx, "steal")

	lock, err := models.AcquireNoteLock(id, userGUID, req.holder(), steal)
	if err != nil {
		var locked *models.NoteLockedError
		if errors.As(err, &locked) {
			return writeConflict(ctx, locked.Error(),
				lockConflictDetail{Reason: "locked", Lock: locked.Lock})
		}
		logger.LogErr(err, "failed to acquire note lock", "id", id)
		return writeError(ctx, http.StatusInternalServerError, "failed to lock note")
	}

	if steal && lock.StolenFrom != "" {
		// Worth a log line at Info: a takeover is the one path in this system
		// that can cost somebody their work, so it should be reconstructable
		// after the fact from the server log alone.
		logger.Info("Note lock stolen", "id", id,
			"from_session", lock.StolenFrom, "to_session", lock.Holder.SessionID,
			"to_label", lock.Holder.Label, "user", userGUID)
	}
	return writeSuccess(ctx, http.StatusOK, lock)
}

// RenewNoteLock handles PUT /api/v1/notes/:id/lock — the heartbeat.
//
// 409 rather than 404 when the lease is gone, because "gone" is a conflict
// from the holder's point of view: something else has the note now (or is free
// to take it), and the client's job is to tell its user before they type
// another paragraph into a form that can no longer save.
func RenewNoteLock(ctx rweb.Context) error {
	_, id, errResp, ok := noteLockParams(ctx)
	if !ok {
		return errResp
	}

	token := lockToken(ctx)
	if token == "" {
		return writeError(ctx, http.StatusBadRequest, LockHeaderName+" header is required")
	}

	lock, err := models.RenewNoteLock(id, token)
	if err != nil {
		return writeConflict(ctx, "lock is no longer held",
			lockConflictDetail{Reason: "lost", Lock: models.GetNoteLock(id)})
	}
	return writeSuccess(ctx, http.StatusOK, lock)
}

// ReleaseNoteLock handles DELETE /api/v1/notes/:id/lock
//
// Always 200, even when nothing was released. A release is a client saying "I
// am done with this note", and it is done either way; the only thing an error
// would achieve is a misleading message on a screen the user has already left.
// The released flag is there for anyone who wants to know.
func ReleaseNoteLock(ctx rweb.Context) error {
	_, id, errResp, ok := noteLockParams(ctx)
	if !ok {
		return errResp
	}

	token := lockToken(ctx)
	released := models.ReleaseNoteLock(id, token)
	return writeSuccess(ctx, http.StatusOK, map[string]bool{"released": released})
}

// GetNoteLock handles GET /api/v1/notes/:id/lock — who has this note, if
// anyone. 404 when the note is unlocked, so a client can ask with a bare
// status check.
func GetNoteLock(ctx rweb.Context) error {
	userGUID, id, errResp, ok := noteLockParams(ctx)
	if !ok {
		return errResp
	}

	owned, err := ownsNote(id, userGUID)
	if err != nil {
		logger.LogErr(err, "failed to look up note for lock", "id", id)
		return writeError(ctx, http.StatusInternalServerError, "failed to read lock")
	}
	if !owned {
		return writeError(ctx, http.StatusNotFound, "note not found")
	}

	lock := models.GetNoteLock(id)
	if lock == nil {
		return writeError(ctx, http.StatusNotFound, "note is not locked")
	}
	return writeSuccess(ctx, http.StatusOK, lock)
}

// ListNoteLocks handles GET /api/v1/note-locks — every live lease this user
// holds, across every session.
//
// One call rather than one per note, because the caller is a list screen: the
// TUI's browse view needs to badge whichever of a hundred rows are locked, and
// doing that per row would turn one refresh into a hundred round trips.
func ListNoteLocks(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	return writeSuccess(ctx, http.StatusOK, models.ListNoteLocks(userGUID))
}

// ---- Enforcement helpers, used by the note write handlers -------------------

// lockToken reads the presented token off the request, matching the header
// name case-insensitively.
//
// It does NOT use ctx.Request().Header, and the reason is a bug that would
// otherwise be invisible: rweb matches the key exactly or against its
// lowercase form, while Go's http client canonicalizes outgoing header names —
// so "X-GoNotes-Lock" leaves the client as "X-Gonotes-Lock" and matches
// neither. The symptom is not an error anywhere; it is every authorized write
// silently arriving with no token and being refused against its own lock.
//
// HTTP header names are case-insensitive by specification (RFC 9110 §5.1), so
// scanning is the correct reading rather than a workaround, and it also means
// no client — curl, the mobile app, a shell script — has to guess our casing.
func lockToken(ctx rweb.Context) string {
	for _, h := range ctx.Request().Headers() {
		if strings.EqualFold(h.Key, LockHeaderName) {
			return h.Value
		}
	}
	return ""
}

// authorizeNoteWrite gates a mutating note request on the lock registry.
//
// It returns ok false when the write must not proceed, having already written
// the 409. Handlers call it immediately after resolving the id and before
// touching the models layer, so a blocked write costs one map lookup.
func authorizeNoteWrite(ctx rweb.Context, id int64) (err error, ok bool) {
	aerr := models.AuthorizeNoteWrite(id, lockToken(ctx))
	if aerr == nil {
		return nil, true
	}
	var locked *models.NoteLockedError
	if errors.As(aerr, &locked) {
		return writeConflict(ctx, locked.Error(),
			lockConflictDetail{Reason: "locked", Lock: locked.Lock}), false
	}
	return writeError(ctx, http.StatusInternalServerError, "failed to check note lock"), false
}

// writeStaleConflict renders the version guard's refusal.
func writeStaleConflict(ctx rweb.Context, stale *models.StaleWriteError) error {
	detail := staleConflictDetail{Reason: "stale", ExpectedVersion: stale.ExpectedVersion}
	if stale.Current != nil {
		out := stale.Current.ToOutput()
		detail.Current = &out
	}
	return writeConflict(ctx, stale.Error(), detail)
}

// queryFlag reads a boolean query parameter, treating a bare presence
// ("?steal") as true — the shape a hand-typed curl takes.
func queryFlag(ctx rweb.Context, name string) bool {
	raw := ctx.Request().Query()
	if raw == "" {
		return false
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	if _, present := values[name]; !present {
		return false
	}
	switch values.Get(name) {
	case "", "1", "true", "yes":
		return true
	default:
		return false
	}
}
