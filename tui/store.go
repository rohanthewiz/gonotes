package tui

import (
	"errors"

	"gonotes/models"
	"gonotes/summarize"
)

// Store is the TUI's entire data-access surface. Every screen reaches the
// database through this interface and never through models.* directly.
//
// Why the seam exists at all: bytdb is single-process. The GoNotes server (and
// the MacApp, which embeds it) holds both database files open for as long as
// it runs, so a TUI launched beside it — the cats-hosted case this whole
// integration is built for — cannot open the same data directory. It has to
// talk to the running server over HTTP instead.
//
//	         ┌────────────┐
//	screens ─┤   Store    ├─ localStore ── models.* ── bytdb  (no server up)
//	         └────────────┘
//	               └──────── httpStore ── /api/v1/* ── the running server
//
// main.go's runTui decides which one by probing the server's health endpoint;
// in HTTP mode models.InitDB is never called, so the process never even tries
// to take the lock.
//
// The choice of shape is deliberate: the methods mirror the models functions
// the TUI already called, with the arguments the TUI already had. That keeps
// localStore a pure pass-through with nothing to get wrong, and pushes all the
// impedance mismatch (envelopes, output structs, tokens) into store_http.go
// where it belongs.
//
// The seam also pays for itself in tests: a fakeStore lets every teatest flow
// run with no database at all — see fake_store_test.go.
type Store interface {
	// ---- Session -----------------------------------------------------------

	// ResumeSession returns an already-authenticated user when the store can
	// establish one without asking for credentials, or (nil, nil) when the
	// login screen must be shown. The local store never can — a password is
	// the only way in. The HTTP store can, via a cached token or credentials
	// supplied in the environment.
	//
	// An error here is not fatal: the caller falls back to the login screen.
	ResumeSession() (*models.User, error)

	// ListUsernames returns the accounts on this installation, used to prefill
	// the login screen. It returns ErrNoUserList when the store has no way to
	// enumerate them; see that error's doc for why the HTTP store does.
	ListUsernames() ([]string, error)

	AuthenticateUser(username, password string) (*models.User, error)
	CreateUser(username, password string) (*models.User, error)

	// ---- Notes -------------------------------------------------------------

	// ListNotes returns every live note owned by the user. There is no
	// pagination here on purpose: personal note collections are small enough
	// that loading everything and letting the list widget filter client-side
	// is both simpler and snappier (the same reasoning as the old
	// models.ListNotes(guid, 0, 0) call this replaces).
	ListNotes(userGUID string) ([]models.Note, error)
	GetCategoryNotes(categoryID int64, userGUID string) ([]models.Note, error)

	// GetCategorySubcategoryNotes narrows a category to the notes filed under
	// ALL of the given subcategories (AND, matching the web UI's chips and
	// models.GetNotesByCategoryAndSubcategories). An empty list is the
	// unfiltered category, so callers need no special case.
	//
	// It takes the category NAME, not the id, because that is what both
	// implementations can act on: the models function is name-keyed and the API
	// exposes this filter only as /notes?cat=<name>&subcats[]=... — there is no
	// id-keyed door to it. The name is read from the category the user just
	// picked, so a rename would have to land between the pick and the reload to
	// matter.
	GetCategorySubcategoryNotes(categoryName string, subcategories []string, userGUID string) ([]models.Note, error)

	// GetNoteByID returns (nil, nil) when the note does not exist or is not
	// owned by the user — a missing note is not an error at this layer.
	GetNoteByID(id int64, userGUID string) (*models.Note, error)
	CreateNote(input models.NoteInput, userGUID string) (*models.Note, error)
	UpdateNote(id int64, input models.NoteInput, userGUID string) (*models.Note, error)

	// DeleteNote soft-deletes the note, reporting whether one was actually
	// removed (false = not found or not owned).
	DeleteNote(id int64, userGUID string) (bool, error)
	ToggleNoteFlag(id int64, userGUID string) (*models.Note, error)

	// ---- Categories --------------------------------------------------------

	ListCategories(userGUID string) ([]models.Category, error)
	CreateCategory(name, userGUID string) (*models.Category, error)
	DeleteCategory(id int64, userGUID string) error

	// SetCategorySubcategories replaces the subcategory list on the category
	// DEFINITION — the palette of names every UI offers for that category,
	// independent of which notes selected which.
	//
	// It takes the whole category rather than an id because the write is a
	// whole-object update on both sides (models.UpdateCategory rewrites name,
	// description and subcategories in one statement; so does PUT
	// /api/v1/categories/:id), and the fields this call is not about must be
	// carried through rather than blanked. Callers always have the category in
	// hand — they had to, to know its current subcategories.
	SetCategorySubcategories(cat models.Category, subcategories []string, userGUID string) (*models.Category, error)

	// GetCategoryByName returns (nil, nil) when no category has that name.
	// The form's category sync uses this to decide create-or-link.
	GetCategoryByName(name, userGUID string) (*models.Category, error)

	GetNoteCategories(noteID int64, userGUID string) ([]models.Category, error)

	// GetNoteCategoryDetails is GetNoteCategories plus the per-note SELECTION:
	// which of each category's subcategories this note is filed under. It is
	// what the form and the detail header read, since a note's assignment is
	// "Work/backend", not "Work".
	//
	// Both live on the interface because they answer different questions and
	// the cheap one is still the right call when the selection is irrelevant.
	GetNoteCategoryDetails(noteID int64, userGUID string) ([]models.NoteCategoryDetailOutput, error)

	AddCategoryToNote(noteID, categoryID int64, userGUID string) error

	// AddCategoryToNoteWithSubcategories links a category and records the
	// subcategory selection in one step, which is what the API's POST does with
	// a body and what avoids a create-then-update pair on every new link.
	AddCategoryToNoteWithSubcategories(noteID, categoryID int64, subcategories []string, userGUID string) error

	// SetNoteCategorySubcategories replaces the selection on a link that
	// already exists. An empty list clears it, leaving the category attached —
	// that is the difference between editing "Work/backend" down to "Work" and
	// removing the category outright.
	//
	// No userGUID, for the same reason RemoveCategoryFromNote takes none: the
	// junction row's ownership was settled when the link was created.
	SetNoteCategorySubcategories(noteID, categoryID int64, subcategories []string) error

	// RemoveCategoryFromNote takes no userGUID, matching the models function:
	// the junction table only ever holds rows whose ownership was verified
	// when the link was created.
	RemoveCategoryFromNote(noteID, categoryID int64) error

	// ---- Note locks --------------------------------------------------------
	//
	// The mutual-exclusion protocol between editing sessions. A screen about to
	// edit acquires; a keeper goroutine renews; leaving releases. See
	// models/lock.go for what a lease is and tui/lock.go for how the TUI holds
	// one.
	//
	// NOTE THE MISSING PARAMETER: none of these take a token, and UpdateNote
	// does not take one either. The store that acquired a lease keeps its
	// token and attaches it to the right request by note id — the impedance
	// this seam exists to absorb. A token in these signatures would be a
	// secret threaded through five screens, one forgotten argument away from a
	// save that 409s against its own lock.

	// AcquireNoteLock claims a note for this session, or reports who has it as
	// a *models.NoteLockedError (test with lockedBy). Re-acquiring a note this
	// session already holds succeeds and extends the lease.
	//
	// steal takes the note by force from a live holder. It is never something
	// a client should retry into on its own — see models.AcquireNoteLock.
	AcquireNoteLock(noteID int64, userGUID string, holder models.LockHolder, steal bool) (*models.NoteLock, error)

	// RenewNoteLock is the heartbeat. It returns models.ErrLockNotHeld when
	// the lease is gone, which is the signal the holder needs most: the note
	// belongs to somebody else now and the form on screen can no longer save.
	RenewNoteLock(noteID int64) (*models.NoteLock, error)

	// ReleaseNoteLock gives the note back. Releasing a lease this session does
	// not hold is not an error — by then the goal is met either way.
	ReleaseNoteLock(noteID int64) error

	// ReleaseAllNoteLocks gives back every lease this session holds. It is the
	// shutdown path (see Run): the exits that bypass a form's own release — q
	// from the list, ctrl+c, a program loop that failed — must not leave a note
	// locked by a process that has stopped existing.
	ReleaseAllNoteLocks() error

	// GetNoteLock returns the live lease on a note, or (nil, nil) when it is
	// unlocked. Never returns the token: a lease is only ever handed to a
	// non-holder redacted.
	GetNoteLock(noteID int64) (*models.NoteLock, error)

	// ListNoteLocks returns every live lease belonging to the user, for the
	// list screen's badges. One call rather than one per row — a hundred-row
	// refresh must not become a hundred round trips.
	ListNoteLocks(userGUID string) ([]models.NoteLock, error)

	// ---- Sync --------------------------------------------------------------
	//
	// Sync defaults to prompt mode: nothing leaves the machine unasked, and
	// something has to do the asking. In a terminal that is this TUI, which is
	// why these five methods exist at a seam that otherwise only deals in
	// notes and categories.
	//
	// Which side is actually syncing differs by store, and the difference is
	// invisible above this line: the local store drives the sync client this
	// process started, while the HTTP store asks the server that owns it. Both
	// answer the same three questions — how overdue are we, how much is
	// waiting, and what should be done about it.

	// SyncStatus reports the sync clock, or (nil, nil) when this installation
	// has no sync configured at all. Nil is not an error: most GoNotes
	// installations are a single machine with no hub, and the UI simply says
	// nothing about sync on those.
	SyncStatus() (*models.SyncClientStatus, error)

	// SyncNow runs one full cycle and returns the status afterwards. When
	// compact is true the pending change log is collapsed first — the two
	// halves of the "compact & sync" answer, kept in one call so a UI cannot
	// compact and then fail to sync.
	SyncNow(compact bool) (*models.SyncClientStatus, error)

	// SnoozeSync defers the "sync is due" prompt by the configured interval
	// without syncing anything.
	SnoozeSync() (*models.SyncClientStatus, error)

	// CompactChanges collapses the pending change log and reports what it did,
	// without syncing. Useful precisely when the hub is unreachable, which is
	// when an unsynced log grows.
	CompactChanges() (*models.CompactionResult, error)

	// DeclineExitSync records that the user has answered the quit-time sync
	// question with "no", so the exit path does not then sync anyway.
	//
	// The HTTP store does nothing here, and that is correct rather than
	// unimplemented: quitting the TUI is not the server exiting, and one
	// client leaving must not disarm the exit cycle of a server that goes on
	// running for everyone else.
	DeclineExitSync() error

	// ---- Summarization -----------------------------------------------------

	// Summarize condenses a block of text into a note's three fields (see
	// package summarize). model may be empty for the default.
	//
	// It sits at this seam for the same reason everything else does: the
	// summarizer is the local `claude` CLI, so it runs wherever the DATA lives
	// — in the TUI's own process when it owns the databases, and on the server
	// when the server does. A TUI attached to a hub on another machine
	// therefore summarizes with that machine's CLI and credentials, which is
	// the same answer it already gives for notes themselves.
	//
	// The call takes seconds and blocks the goroutine it runs on, so every
	// caller must be a tea.Cmd, never the event loop.
	Summarize(text, model string) (*summarize.Result, error)
}

// ErrNoUserList reports that a store cannot enumerate accounts.
//
// The HTTP store returns it always, because the API deliberately has no
// "list usernames" endpoint and this integration is not the place to add one:
// on a shared sync hub that endpoint would hand every unauthenticated caller
// the full account list. The login screen treats this as "unknown" rather than
// "no users" — the distinction matters, since an empty list is what puts the
// screen into first-run registration mode.
var ErrNoUserList = errors.New("this store cannot list usernames")

// errSyncNotConfigured reports that an action was asked of a sync that does
// not exist on this installation. Distinct from a nil SyncStatus, which is the
// quiet answer to a question ("is there sync here?"); this is the answer to an
// instruction ("sync"), and an instruction that cannot be carried out is worth
// saying out loud.
var errSyncNotConfigured = errors.New("sync is not configured on this installation")
