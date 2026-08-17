package tui

import (
	"gonotes/models"
)

// localStore is the in-process implementation: the TUI owns the bytdb files
// and calls the models layer directly, exactly as it did before the Store seam
// existed.
//
// Every method here is a one-line pass-through, and that is the point. The
// seam only earns its keep if the *default* path stays trivially correct;
// anything clever in this file would be a behavior change smuggled in under a
// refactor. The arguments the interface takes were chosen to make that
// possible — where the models function has an extra parameter the TUI always
// passed the same value for (ListNotes' limit/offset), it is supplied here
// rather than pushed onto every caller.
//
// The one exception to "no state here" is the lock-token map, and it is not a
// behavior change so much as the same bookkeeping the HTTP store does: a lease
// is issued with a bearer token, and whoever took the lease has to keep the
// token to renew, release, or write under it. Holding it in the store rather
// than passing it through every screen is what keeps the token out of five
// signatures — see lockTokens in lock.go.
type localStore struct {
	tokens *lockTokens
}

// NewLocalStore returns a Store backed by the process's own bytdb databases.
// models.InitDB must have succeeded before any method is called.
func NewLocalStore() Store { return &localStore{tokens: newLockTokens()} }

// ---- Session ---------------------------------------------------------------

// ResumeSession never resumes: with direct database access there is no session
// to resume, only a password to check. Returning (nil, nil) sends the caller
// to the login screen.
func (s *localStore) ResumeSession() (*models.User, error) { return nil, nil }

func (s *localStore) ListUsernames() ([]string, error) { return models.ListUsernames() }

func (s *localStore) AuthenticateUser(username, password string) (*models.User, error) {
	return models.AuthenticateUser(models.UserLoginInput{Username: username, Password: password})
}

func (s *localStore) CreateUser(username, password string) (*models.User, error) {
	return models.CreateUser(models.UserRegisterInput{Username: username, Password: password})
}

// ---- Notes -----------------------------------------------------------------

// ListNotes passes limit/offset 0, which the models layer reads as "no cap".
func (s *localStore) ListNotes(userGUID string) ([]models.Note, error) {
	return models.ListNotes(userGUID, 0, 0)
}

func (s *localStore) GetCategoryNotes(categoryID int64, userGUID string) ([]models.Note, error) {
	return models.GetCategoryNotes(categoryID, userGUID)
}

func (s *localStore) GetCategorySubcategoryNotes(categoryName string, subcategories []string, userGUID string) ([]models.Note, error) {
	// The models function already special-cases an empty subcategory list as
	// "the whole category", so no branch is needed here.
	return models.GetNotesByCategoryAndSubcategories(categoryName, subcategories, userGUID)
}

func (s *localStore) GetNoteByID(id int64, userGUID string) (*models.Note, error) {
	return models.GetNoteByID(id, userGUID)
}

func (s *localStore) CreateNote(input models.NoteInput, userGUID string) (*models.Note, error) {
	return models.CreateNote(input, userGUID)
}

// UpdateNote is the one write that checks the lock before delegating — the
// same check web/api/notes.go performs on the HTTP path.
//
// It looks redundant in local mode, where bytdb's single-process rule means
// this TUI is the only session there is, and in the common case it costs one
// map lookup that always passes. It is here so the invariant "no write lands
// past a foreign lock" holds at the seam rather than in one of its two
// implementations. A rule that is true on one side of an interface and merely
// customary on the other is a rule that stops being true the first time
// somebody adds a third implementation.
func (s *localStore) UpdateNote(id int64, input models.NoteInput, userGUID string) (*models.Note, error) {
	if err := models.AuthorizeNoteWrite(id, s.tokens.get(id)); err != nil {
		return nil, err
	}
	return models.UpdateNote(id, input, userGUID)
}

func (s *localStore) DeleteNote(id int64, userGUID string) (bool, error) {
	if err := models.AuthorizeNoteWrite(id, s.tokens.get(id)); err != nil {
		return false, err
	}
	deleted, err := models.DeleteNote(id, userGUID)
	if deleted {
		// The note is gone; its lease is meaningless. Drop both sides so the
		// registry and this store's bookkeeping agree.
		models.ReleaseNoteLock(id, s.tokens.get(id))
		s.tokens.clear(id)
	}
	return deleted, err
}

func (s *localStore) ToggleNoteFlag(id int64, userGUID string) (*models.Note, error) {
	if err := models.AuthorizeNoteWrite(id, s.tokens.get(id)); err != nil {
		return nil, err
	}
	return models.ToggleNoteFlag(id, userGUID)
}

// ---- Categories ------------------------------------------------------------

func (s *localStore) ListCategories(userGUID string) ([]models.Category, error) {
	return models.ListCategories(0, 0, userGUID)
}

func (s *localStore) CreateCategory(name, userGUID string) (*models.Category, error) {
	return models.CreateCategory(models.CategoryInput{Name: name}, userGUID)
}

func (s *localStore) DeleteCategory(id int64, userGUID string) error {
	return models.DeleteCategory(id, userGUID)
}

// SetCategorySubcategories carries the category's existing name and description
// into the update. models.UpdateCategory writes all three columns from the
// input, so omitting the description here would silently erase it — the one
// place in this file where a pass-through needs more than the arguments given.
func (s *localStore) SetCategorySubcategories(cat models.Category, subcategories []string, userGUID string) (*models.Category, error) {
	input := models.CategoryInput{Name: cat.Name, Subcategories: subcategories}
	if cat.Description.Valid {
		desc := cat.Description.String
		input.Description = &desc
	}
	return models.UpdateCategory(cat.ID, input, userGUID)
}

func (s *localStore) GetCategoryByName(name, userGUID string) (*models.Category, error) {
	return models.GetCategoryByName(name, userGUID)
}

func (s *localStore) GetNoteCategories(noteID int64, userGUID string) ([]models.Category, error) {
	return models.GetNoteCategories(noteID, userGUID)
}

func (s *localStore) GetNoteCategoryDetails(noteID int64, userGUID string) ([]models.NoteCategoryDetailOutput, error) {
	return models.GetNoteCategoryDetails(noteID, userGUID)
}

func (s *localStore) AddCategoryToNote(noteID, categoryID int64, userGUID string) error {
	return models.AddCategoryToNote(noteID, categoryID, userGUID)
}

func (s *localStore) AddCategoryToNoteWithSubcategories(noteID, categoryID int64, subcategories []string, userGUID string) error {
	return models.AddCategoryToNoteWithSubcategories(noteID, categoryID, subcategories, userGUID)
}

func (s *localStore) SetNoteCategorySubcategories(noteID, categoryID int64, subcategories []string) error {
	return models.UpdateNoteCategorySubcategories(noteID, categoryID, subcategories)
}

func (s *localStore) RemoveCategoryFromNote(noteID, categoryID int64) error {
	return models.RemoveCategoryFromNote(noteID, categoryID)
}

// ---- Note locks --------------------------------------------------------------
//
// These call the very same registry functions the HTTP handlers call, which is
// why the two modes cannot disagree about what a lease means: there is one
// implementation of the protocol, and store_http.go reaches it over a socket
// instead of a function call.

func (s *localStore) AcquireNoteLock(noteID int64, userGUID string, holder models.LockHolder, steal bool) (*models.NoteLock, error) {
	lock, err := models.AcquireNoteLock(noteID, userGUID, holder, steal)
	if err != nil {
		return nil, err
	}
	// Remember the token: from here on, writes to this note carry it and the
	// screens never have to know it exists.
	s.tokens.set(noteID, lock.Token)
	return lock, nil
}

func (s *localStore) RenewNoteLock(noteID int64) (*models.NoteLock, error) {
	lock, err := models.RenewNoteLock(noteID, s.tokens.get(noteID))
	if err != nil {
		// The lease is gone, so the token is worthless. Dropping it here means
		// a subsequent write is correctly refused rather than presenting a
		// token the registry no longer recognizes.
		s.tokens.clear(noteID)
		return nil, err
	}
	return lock, nil
}

func (s *localStore) ReleaseNoteLock(noteID int64) error {
	models.ReleaseNoteLock(noteID, s.tokens.get(noteID))
	s.tokens.clear(noteID)
	return nil
}

func (s *localStore) ReleaseAllNoteLocks() error {
	for noteID, token := range s.tokens.drain() {
		models.ReleaseNoteLock(noteID, token)
	}
	return nil
}

func (s *localStore) GetNoteLock(noteID int64) (*models.NoteLock, error) {
	return models.GetNoteLock(noteID), nil
}

func (s *localStore) ListNoteLocks(userGUID string) ([]models.NoteLock, error) {
	return models.ListNoteLocks(userGUID), nil
}

// Compile-time check: the interface and this implementation must not drift.
var _ Store = (*localStore)(nil)
