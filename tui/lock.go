package tui

import (
	"errors"
	"os"
	"sync"
	"time"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
)

// lock.go is the TUI's half of the note-lock protocol: who this session says
// it is, which leases it holds, and the heartbeat that keeps them alive.
//
// The server owns arbitration (models/lock.go, web/api/locks.go). What lives
// here is everything that has to be true on the client for that arbitration to
// mean anything:
//
//	sessionIdentity  a stable id per process, plus a label a human recognizes
//	lockTokens       the bearer tokens this session holds, per note
//	leaseKeeper      the goroutine that renews them, and says when one is lost

// ---- Session identity -------------------------------------------------------

var (
	identityOnce sync.Once
	identity     models.LockHolder
)

// sessionIdentity returns this process's lock identity, computed once.
//
// The SessionID is a fresh UUID per process, not something derived from the
// pane or the host. That is deliberate: two GoNotes started in the SAME cats
// pane (one after another, or one under the other) are two sessions and must
// not be able to inherit each other's leases, while a session that survives a
// pane rename or a window move is still the same session. A random id per
// process is the only identifier with both properties.
//
// Everything else is decoration, and decoration is the feature: the label is
// what the blocked session shows its user, so it works down a ladder from most
// to least recognizable — the cats pane handle, then the hostname, then a
// truncated id that at least distinguishes two sessions from each other.
func sessionIdentity() models.LockHolder {
	identityOnce.Do(func() {
		id := uuid.New().String()
		host := hostname()
		pane := os.Getenv("CATS_PANE_ID")

		label := pane
		switch {
		case label != "" && host != "":
			label = "pane " + pane + " on " + host
		case label != "":
			label = "pane " + pane
		case host != "":
			label = host
		default:
			label = "session " + id[:8]
		}

		identity = models.LockHolder{
			SessionID:  id,
			Label:      label,
			Host:       host,
			PaneHandle: pane,
			Client:     "tui",
		}
	})
	return identity
}

// ---- Token bookkeeping ------------------------------------------------------

// lockTokens remembers the bearer token for each note this session has locked.
//
// It exists so the token never appears in a screen's code. A screen acquires a
// lock and later saves; the store attaches the right token to the right request
// by note id, because it is the thing that took the lock and therefore the
// thing that knows. Threading the token through every call site instead would
// put a secret in five signatures and one forgotten argument away from a save
// that mysteriously 409s against its own lock.
//
// Both stores embed one — the local store needs the same bookkeeping, since it
// calls the same registry functions the HTTP handlers do.
type lockTokens struct {
	mu     sync.Mutex
	byNote map[int64]string
}

func newLockTokens() *lockTokens { return &lockTokens{byNote: map[int64]string{}} }

func (t *lockTokens) set(noteID int64, token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if token == "" {
		delete(t.byNote, noteID)
		return
	}
	t.byNote[noteID] = token
}

// get returns the token this session holds for a note, or "" — which callers
// send as "no lock", the correct thing to present for an unlocked note.
func (t *lockTokens) get(noteID int64) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.byNote[noteID]
}

func (t *lockTokens) clear(noteID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.byNote, noteID)
}

// drain returns every held (noteID, token) pair and empties the map. It is the
// shutdown primitive: taking and clearing in one locked step means a release
// sweep cannot race a concurrent acquire into releasing a lease twice or
// missing one entirely.
func (t *lockTokens) drain() map[int64]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	held := t.byNote
	t.byNote = map[int64]string{}
	return held
}

// ---- The heartbeat ----------------------------------------------------------

// lockLostMsg says a lease this session was holding is gone: it lapsed despite
// the heartbeat, or somebody stole it. The form turns this into a banner, while
// the user is still typing, rather than letting them find out at save time.
type lockLostMsg struct {
	noteID int64
	// lock is whoever holds it now, when the server said; nil if nobody does.
	lock *models.NoteLock
}

// leaseKeeper renews one note's lease until it is stopped.
//
// IT IS A PLAIN GOROUTINE, NOT A tea.Tick, AND THAT IS THE WHOLE POINT.
//
// The form's most important pause is ctrl+e, which suspends the entire Bubble
// Tea program (tea.ExecProcess) and hands the terminal to $EDITOR for as long
// as the user wants it. No Cmd runs during that window and no Tick fires — so a
// tick-based heartbeat would stop renewing exactly when the user is most
// deeply engaged with the note, drop the lease inside LockTTL, and let another
// session walk in while they are still typing in vim.
//
// A goroutine does not care that the event loop is parked. It keeps renewing
// through the suspension, and the news of a lost lease simply waits on the
// channel until the loop comes back to read it.
//
//	tea loop:  ──run──┤ SUSPENDED (in $EDITOR) ├──run──▶
//	keeper:    ──renew──renew──renew──renew──renew──▶     unaffected
//	lost chan: ─────────────────────────[queued]──▶ delivered on resume
type leaseKeeper struct {
	store  Store
	noteID int64

	// lost carries at most one message and is then closed. One lease can only
	// be lost once, and the buffer of 1 means the keeper never blocks on a
	// receiver that is currently suspended.
	lost chan *models.NoteLock

	stop     chan struct{}
	stopOnce sync.Once
}

// startLeaseKeeper begins renewing noteID's lease every models.LockHeartbeat.
//
// The caller must already hold the lock — this only keeps one, it does not take
// one. Returns nil for noteID 0 (a new note has nothing to lock yet), so
// callers need no branch.
func startLeaseKeeper(store Store, noteID int64) *leaseKeeper {
	if noteID == 0 || store == nil {
		return nil
	}
	k := &leaseKeeper{
		store:  store,
		noteID: noteID,
		lost:   make(chan *models.NoteLock, 1),
		stop:   make(chan struct{}),
	}
	go k.run()
	return k
}

func (k *leaseKeeper) run() {
	ticker := time.NewTicker(models.LockHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-k.stop:
			return
		case <-ticker.C:
			if _, err := k.store.RenewNoteLock(k.noteID); err != nil {
				// A renewal can fail for two very different reasons and the
				// keeper cannot always tell them apart: the lease is genuinely
				// gone (stolen, expired), or the server was briefly unreachable.
				// Both are reported as lost, and that is the safe direction —
				// telling the user their lock may be gone when it is not costs
				// them a glance at a banner, while the reverse costs them their
				// text. The lease is re-acquirable either way; see the banner's
				// retake action.
				holder, _ := k.store.GetNoteLock(k.noteID)
				select {
				case k.lost <- holder:
				default:
				}
				close(k.lost)
				return
			}
		}
	}
}

// Stop ends the renewals. Safe to call more than once and on a nil keeper, so
// every teardown path — save, cancel, quit, an error — can call it without
// first working out whether there was ever a lease.
func (k *leaseKeeper) Stop() {
	if k == nil {
		return
	}
	k.stopOnce.Do(func() { close(k.stop) })
}

// watch is the tea.Cmd that delivers the keeper's bad news to the event loop.
// It blocks on a Cmd goroutine, which is what Cmds are for; on a stopped keeper
// the channel closes and it returns nil, which tea drops.
func (k *leaseKeeper) watch() tea.Cmd {
	if k == nil {
		return nil
	}
	return func() tea.Msg {
		lock, ok := <-k.lost
		if !ok {
			return nil
		}
		return lockLostMsg{noteID: k.noteID, lock: lock}
	}
}

// ---- Error shapes -----------------------------------------------------------

// lockedBy extracts the blocking lease from a contention error, reporting
// false for every other kind of failure.
//
// Both stores raise the SAME type here — *models.NoteLockedError — even though
// one gets it from a function call and the other from parsing a 409. That
// uniformity is what lets every screen handle contention once: nothing above
// the store seam should be able to tell whether the arbiter was in this process
// or across a socket.
func lockedBy(err error) (*models.NoteLock, bool) {
	var locked *models.NoteLockedError
	if errors.As(err, &locked) {
		return locked.Lock, true
	}
	return nil, false
}

// staleWrite extracts the winning note from a version-guard rejection.
// Same contract as lockedBy: one type from both stores.
func staleWrite(err error) (*models.StaleWriteError, bool) {
	var stale *models.StaleWriteError
	if errors.As(err, &stale) {
		return stale, true
	}
	return nil, false
}

// ---- Commands ---------------------------------------------------------------

// lockAcquiredMsg reports the outcome of trying to claim a note for editing.
// Exactly one of lock / blockedBy / err is meaningful:
//
//	lock      != nil → the note is ours, open the form
//	blockedBy != nil → somebody else has it, show the contention screen
//	err       != nil → the attempt itself failed
type lockAcquiredMsg struct {
	noteID    int64
	note      *models.Note
	lock      *models.NoteLock
	blockedBy *models.NoteLock
	err       error
}

// acquireLockCmd claims a note before its form opens, carrying the note along
// so the caller can open the form from the message without a second load.
//
// steal forces the takeover. It is a parameter rather than a retry inside this
// command because stealing is a decision the user makes looking at the holder's
// name — never something the client escalates to on its own.
func acquireLockCmd(st Store, note *models.Note, userGUID string, steal bool) tea.Cmd {
	return func() tea.Msg {
		lock, err := st.AcquireNoteLock(note.ID, userGUID, sessionIdentity(), steal)
		if err != nil {
			if blocking, ok := lockedBy(err); ok {
				return lockAcquiredMsg{noteID: note.ID, note: note, blockedBy: blocking}
			}
			return lockAcquiredMsg{noteID: note.ID, note: note, err: err}
		}
		return lockAcquiredMsg{noteID: note.ID, note: note, lock: lock}
	}
}

// releaseLockCmd gives a note back. Failures are swallowed: by the time this
// runs the user has left the form, the lease expires on its own regardless, and
// an error toast about a lock they never saw taken would be noise.
func releaseLockCmd(st Store, noteID int64) tea.Cmd {
	if noteID == 0 {
		return nil
	}
	return func() tea.Msg {
		_ = st.ReleaseNoteLock(noteID)
		return nil
	}
}

// locksLoadedMsg carries every live lease for the list screen's badges.
type locksLoadedMsg struct {
	locks map[int64]models.NoteLock
	err   error
}

// loadLocksCmd fetches all of the user's live leases in one call, keyed by note
// id for the list to index into.
//
// Errors are carried but are not worth interrupting a refresh over: a missing
// badge is a cosmetic loss, and the lock still does its job at acquire time.
func loadLocksCmd(st Store, userGUID string) tea.Cmd {
	return func() tea.Msg {
		locks, err := st.ListNoteLocks(userGUID)
		if err != nil {
			return locksLoadedMsg{err: err}
		}
		byNote := make(map[int64]models.NoteLock, len(locks))
		for _, l := range locks {
			byNote[l.NoteID] = l
		}
		return locksLoadedMsg{locks: byNote}
	}
}
