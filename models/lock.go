package models

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/rohanthewiz/serr"
)

// lock.go is the note-lock registry: short-lived, renewable leases that keep
// two sessions from editing the same note at once.
//
// WHY THIS EXISTS
//
// bytdb is single-process, so every GoNotes session that is not the one
// holding the data directory reaches the notes over HTTP against the server
// that does (see tui/store.go). That funnel is a gift for this problem —
// there is exactly one process in a position to arbitrate — and a hazard
// without it: before this file, two cats panes could both open note 42, both
// type for ten minutes, and the second ctrl+s would overwrite the first with
// no error, no record, and no way to get the lost text back.
//
//	pane w1:p3 ─┐
//	pane w2:p1 ─┼─→ GoNotes server ─→ bytdb        one arbiter, so one registry
//	web browser ┘        │
//	                     └── locks: map[noteID] → lease
//
// WHY MEMORY, NOT bytdb
//
// A lease is not data; it is a statement about a process that is running right
// now. Persisting it would be actively wrong in the failure case that matters
// most: a server restart would restore locks whose holders died with it,
// wedging notes until their TTLs ran out for no benefit. It would also put a
// write in the WAL every heartbeat, per open form, forever — durability
// spending on state whose whole design assumption is that it evaporates.
//
// The trade is that leases do not survive a restart. That is the intended
// behavior, and the version guard (note.go) is what makes it safe: even with
// every lease forgotten, a write built on a stale read still cannot land.
//
// WHY LEASES EXPIRE
//
// A lock that a crashed pane holds forever is a worse bug than the one this
// file fixes — it is unrecoverable without an admin, whereas a lost update is
// at least visible. So a lease is a lease: it dies unless the holder keeps
// saying it is alive. Holders renew on a timer well inside the TTL, and a
// holder that stops (killed pane, closed laptop, severed network) releases the
// note automatically within LockTTL.

// LockTTL is how long a lease survives without a renewal.
//
// The number balances two costs that pull in opposite directions. Too short
// and a momentarily-stalled holder — a laptop that slept, a TUI suspended in
// $EDITOR on a slow filesystem — loses its lock while its user is still
// typing. Too long and a genuinely dead session holds a note hostage for that
// long. Ninety seconds is comfortably longer than any pause the TUI can take
// while still being a wait a person will sit through rather than route around.
const LockTTL = 90 * time.Second

// LockHeartbeat is the interval holders should renew at. A third of the TTL,
// so two consecutive renewals can be lost — a hiccup, a dropped packet, one
// slow round trip — before the lease actually lapses.
const LockHeartbeat = LockTTL / 3

// LockHolder identifies the session asking for a lease. Every field is
// cosmetic except SessionID, and that asymmetry is the point: the registry
// arbitrates on SessionID alone, while the rest exists so the message shown to
// whoever is turned away says something a human can act on. "Locked" is a dead
// end; "held by pane w1:p3 (claude) on studio.local since 2m ago" tells you
// which window to go to.
type LockHolder struct {
	// SessionID is the identity the registry actually compares. It must be
	// stable for the life of a session and distinct between sessions; the TUI
	// generates one UUID per process (see tui/lock.go).
	SessionID string `json:"session_id"`

	// Label is what to call this session in a message — a pane handle, a
	// hostname, "web", whatever the client thinks is most recognizable.
	Label string `json:"label,omitempty"`

	// Host is the machine the session runs on, which matters as soon as notes
	// are reachable from more than one.
	Host string `json:"host,omitempty"`

	// PaneHandle is the cats pane the holder occupies ("w1:p3"), when it is in
	// one. It is what lets a blocked session offer to jump straight to the
	// window that has the note open, rather than leaving the user to find it.
	PaneHandle string `json:"pane_handle,omitempty"`

	// Client names the kind of session: "tui", "web", "cli". Purely for the
	// message.
	Client string `json:"client,omitempty"`
}

// NoteLock is one live lease.
type NoteLock struct {
	NoteID   int64  `json:"note_id"`
	UserGUID string `json:"-"` // never leaves the process; scoping only

	// Token is the bearer secret proving a writer is the holder. It is what
	// travels on a write (X-GoNotes-Lock) and what a renewal or release must
	// present. SessionID identifies; Token authorizes — separate values so a
	// session id, which is not secret and appears in messages, can never be
	// replayed as permission to write.
	Token string `json:"token,omitempty"`

	Holder LockHolder `json:"holder"`

	AcquiredAt time.Time `json:"acquired_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`

	// StolenFrom records the session displaced by a forced takeover, kept for
	// the life of the new lease so the audit trail survives long enough to be
	// shown. Empty on a normal acquire.
	StolenFrom string `json:"stolen_from,omitempty"`
}

// Redacted returns a copy safe to hand to a session that does NOT hold the
// lock: everything needed to explain the refusal, with the token removed.
//
// Never return a NoteLock to a non-holder without this. The token is the only
// thing standing between "you may not write" and "you may", so shipping it in
// the body of a 409 would hand the blocked client exactly what it was denied.
func (l *NoteLock) Redacted() *NoteLock {
	if l == nil {
		return nil
	}
	cp := *l
	cp.Token = ""
	return &cp
}

// Expired reports whether the lease has lapsed as of now.
func (l *NoteLock) Expired() bool { return l != nil && time.Now().After(l.ExpiresAt) }

// HeldBy reports whether this lease belongs to the given session.
func (l *NoteLock) HeldBy(sessionID string) bool {
	return l != nil && sessionID != "" && l.Holder.SessionID == sessionID
}

// Age is how long the holder has had the note open — the number a contention
// message leads with, because "since 2m ago" and "since 3h ago" call for very
// different reactions from the person reading it.
func (l *NoteLock) Age() time.Duration {
	if l == nil {
		return 0
	}
	return time.Since(l.AcquiredAt)
}

// ErrNoteLocked is the class of "somebody else has this note open". Test for
// it with errors.Is; type-assert to *NoteLockedError for the holder details.
var ErrNoteLocked = serr.New("note is locked by another session")

// ErrLockNotHeld reports that a renewal or release named a lease this caller
// does not hold — it expired, it was stolen, or the token is wrong. Holders
// treat it as "your lock is gone", which is a thing the user must be told
// before they keep typing.
var ErrLockNotHeld = serr.New("lock not held")

// NoteLockedError carries the blocking lease so a caller can explain itself.
type NoteLockedError struct {
	// Lock is always redacted — see NoteLock.Redacted.
	Lock *NoteLock
}

func (e *NoteLockedError) Error() string {
	if e.Lock == nil {
		return "note is locked by another session"
	}
	who := e.Lock.Holder.Label
	if who == "" {
		who = "another session"
	}
	return "note is locked by " + who + " since " + humanizeAge(e.Lock.Age()) + " ago"
}

func (e *NoteLockedError) Unwrap() error { return ErrNoteLocked }

// humanizeAge renders a duration the way a status line wants it: one unit,
// no decimals, biggest unit that is not zero.
func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Truncate(time.Second).String()
	case d < time.Hour:
		return d.Truncate(time.Minute).String()
	default:
		return d.Truncate(time.Hour).String()
	}
}

// lockRegistry holds every live lease, keyed by note id.
//
// One flat map under one mutex, not a sharded or per-note structure. Every
// operation here is a map lookup plus a timestamp comparison, and the call
// rate is bounded by open edit forms times three per TTL — single digits per
// minute in any realistic session. A lock-free design would be complexity
// bought with no measurable return.
type lockRegistry struct {
	mu    sync.Mutex
	locks map[int64]*NoteLock
}

var noteLocks = &lockRegistry{locks: map[int64]*NoteLock{}}

// liveLocked returns the unexpired lease on noteID, dropping it if it lapsed.
// The caller must hold r.mu.
//
// Expiry is evaluated lazily, on access, rather than by a sweeper goroutine.
// A lapsed lease that nobody asks about harms nothing, and a goroutine would
// have to be owned, stopped, and reasoned about in tests for no behavioral
// gain. Bounded memory is handled by sweepLocked instead.
func (r *lockRegistry) liveLocked(noteID int64) *NoteLock {
	l := r.locks[noteID]
	if l == nil {
		return nil
	}
	if l.Expired() {
		delete(r.locks, noteID)
		return nil
	}
	return l
}

// sweepLocked drops every lapsed lease. Called on acquire — the only operation
// that can grow the map — so the registry cannot accumulate dead entries for
// notes nobody revisits. The caller must hold r.mu.
func (r *lockRegistry) sweepLocked() {
	now := time.Now()
	for id, l := range r.locks {
		if now.After(l.ExpiresAt) {
			delete(r.locks, id)
		}
	}
}

// newLockToken mints a bearer token. crypto/rand because this value is the
// only thing authorizing a write: a guessable token would let one session
// write through another's lock.
func newLockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", serr.Wrap(err, "failed to generate lock token")
	}
	return "lk_" + hex.EncodeToString(b[:]), nil
}

// AcquireNoteLock takes (or re-takes) the lease on a note.
//
// The outcomes, in the order they are decided:
//
//	no live lease            → granted, fresh token
//	lease held by this session → granted, SAME token, expiry extended
//	lease held by another    → *NoteLockedError, unless steal is true
//	steal is true            → granted, fresh token, StolenFrom recorded
//
// The second case is what makes the whole thing usable: reopening the form on
// a note this session already holds must not deadlock against itself, and it
// must not hand back a second token that would invalidate the first. Same
// session, same lease, later expiry.
//
// steal is the escape hatch for the case the TTL cannot cover — a colleague
// who walked away mid-edit, a holder on a machine you cannot reach. It is
// deliberately a parameter rather than an automatic fallback: taking a note
// away from someone who may still be typing in it is a decision a person makes
// with the holder's name in front of them, not something a client retries into.
func AcquireNoteLock(noteID int64, userGUID string, holder LockHolder, steal bool) (*NoteLock, error) {
	if holder.SessionID == "" {
		return nil, serr.New("a lock requires a session id")
	}

	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()
	noteLocks.sweepLocked()

	now := time.Now()
	var stolenFrom string

	if existing := noteLocks.liveLocked(noteID); existing != nil {
		if existing.HeldBy(holder.SessionID) {
			// Re-entrant: extend rather than reissue. The holder's label and pane
			// are refreshed too — a session can move panes without changing id.
			existing.Holder = holder
			existing.RenewedAt = now
			existing.ExpiresAt = now.Add(LockTTL)
			cp := *existing
			return &cp, nil
		}
		if !steal {
			return nil, &NoteLockedError{Lock: existing.Redacted()}
		}
		// Record the displaced session here, while the old lease is still in
		// hand. Only a LIVE lease counts as a theft: taking over a note whose
		// lease had already lapsed displaced nobody, and an audit trail that
		// claimed otherwise would be worse than none.
		stolenFrom = existing.Holder.SessionID
		if stolenFrom == "" {
			stolenFrom = "unknown"
		}
	}

	token, err := newLockToken()
	if err != nil {
		return nil, err
	}

	lock := &NoteLock{
		NoteID:     noteID,
		UserGUID:   userGUID,
		Token:      token,
		Holder:     holder,
		AcquiredAt: now,
		RenewedAt:  now,
		ExpiresAt:  now.Add(LockTTL),
		StolenFrom: stolenFrom,
	}
	noteLocks.locks[noteID] = lock

	cp := *lock
	return &cp, nil
}

// RenewNoteLock extends a lease the caller can prove it holds. This is the
// heartbeat: the holder calls it every LockHeartbeat, and the FAILURE is as
// important as the success — ErrLockNotHeld is how a session that was stolen
// out from under finds out, ideally while its user is still typing rather than
// when they press save.
func RenewNoteLock(noteID int64, token string) (*NoteLock, error) {
	if token == "" {
		return nil, ErrLockNotHeld
	}

	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()

	l := noteLocks.liveLocked(noteID)
	if l == nil || l.Token != token {
		return nil, ErrLockNotHeld
	}

	now := time.Now()
	l.RenewedAt = now
	l.ExpiresAt = now.Add(LockTTL)

	cp := *l
	return &cp, nil
}

// ReleaseNoteLock drops a lease, reporting whether one was actually released.
//
// A wrong or stale token is not an error. By the time a client releases, the
// interesting outcomes have all already happened — it saved, it cancelled, it
// is quitting — and the lease being gone (expired, or stolen) means the goal
// is met either way. Returning an error here would only produce a scary
// message on the way out of a screen the user has already left.
func ReleaseNoteLock(noteID int64, token string) bool {
	if token == "" {
		return false
	}

	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()

	l := noteLocks.liveLocked(noteID)
	if l == nil || l.Token != token {
		return false
	}
	delete(noteLocks.locks, noteID)
	return true
}

// ReleaseNoteLocksForSession drops every lease a session holds, returning how
// many. This is the clean-shutdown path: a TUI quitting should not leave a
// note locked for the rest of the TTL just because the user pressed q instead
// of esc.
func ReleaseNoteLocksForSession(sessionID string) int {
	if sessionID == "" {
		return 0
	}

	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()

	n := 0
	for id, l := range noteLocks.locks {
		if l.Holder.SessionID == sessionID {
			delete(noteLocks.locks, id)
			n++
		}
	}
	return n
}

// GetNoteLock returns the live lease on a note, redacted, or nil if there is
// none. For display: the badge on a list row, the header on a detail screen.
func GetNoteLock(noteID int64) *NoteLock {
	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()
	return noteLocks.liveLocked(noteID).Redacted()
}

// ListNoteLocks returns every live lease belonging to a user, redacted.
//
// One call, not one per row: the browse list needs to know which of a hundred
// notes are locked, and asking per note would turn a screen refresh into a
// hundred round trips over HTTP.
func ListNoteLocks(userGUID string) []NoteLock {
	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()

	now := time.Now()
	out := make([]NoteLock, 0, len(noteLocks.locks))
	for id, l := range noteLocks.locks {
		if now.After(l.ExpiresAt) {
			delete(noteLocks.locks, id)
			continue
		}
		if userGUID != "" && l.UserGUID != "" && l.UserGUID != userGUID {
			continue
		}
		out = append(out, *l.Redacted())
	}
	return out
}

// AuthorizeNoteWrite is the gate every write passes through: it reports
// whether a caller presenting token may modify noteID.
//
// An UNLOCKED note is writable by anyone who owns it. That is a deliberate
// choice and the thing that keeps this system deployable: requiring a lease
// for every write would break the web form, gn-clip.sh, the Markdown importer
// and sync apply on the day it shipped, for the sake of a race none of them
// are in. The lock is not a permission system — ownership already is one — it
// is a mutual-exclusion protocol between sessions that opt into it, backed by
// the version guard for everyone who does not.
//
// What it does guarantee is the case it was built for: once a session holds a
// note, no other writer gets past this line until the holder releases, the
// lease lapses, or somebody explicitly steals it.
func AuthorizeNoteWrite(noteID int64, token string) error {
	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()

	l := noteLocks.liveLocked(noteID)
	if l == nil {
		return nil // unlocked: ownership is the only gate
	}
	if token != "" && l.Token == token {
		return nil // the holder
	}
	return &NoteLockedError{Lock: l.Redacted()}
}

// ResetNoteLocksForTest empties the registry. Tests only — the registry is
// process-global, so a test that leaves a lease behind would fail the next one
// for reasons that have nothing to do with what it was checking.
func ResetNoteLocksForTest() {
	noteLocks.mu.Lock()
	defer noteLocks.mu.Unlock()
	noteLocks.locks = map[int64]*NoteLock{}
}
