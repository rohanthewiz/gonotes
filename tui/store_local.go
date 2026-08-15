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
type localStore struct{}

// NewLocalStore returns a Store backed by the process's own bytdb databases.
// models.InitDB must have succeeded before any method is called.
func NewLocalStore() Store { return localStore{} }

// ---- Session ---------------------------------------------------------------

// ResumeSession never resumes: with direct database access there is no session
// to resume, only a password to check. Returning (nil, nil) sends the caller
// to the login screen.
func (localStore) ResumeSession() (*models.User, error) { return nil, nil }

func (localStore) ListUsernames() ([]string, error) { return models.ListUsernames() }

func (localStore) AuthenticateUser(username, password string) (*models.User, error) {
	return models.AuthenticateUser(models.UserLoginInput{Username: username, Password: password})
}

func (localStore) CreateUser(username, password string) (*models.User, error) {
	return models.CreateUser(models.UserRegisterInput{Username: username, Password: password})
}

// ---- Notes -----------------------------------------------------------------

// ListNotes passes limit/offset 0, which the models layer reads as "no cap".
func (localStore) ListNotes(userGUID string) ([]models.Note, error) {
	return models.ListNotes(userGUID, 0, 0)
}

func (localStore) GetCategoryNotes(categoryID int64, userGUID string) ([]models.Note, error) {
	return models.GetCategoryNotes(categoryID, userGUID)
}

func (localStore) GetNoteByID(id int64, userGUID string) (*models.Note, error) {
	return models.GetNoteByID(id, userGUID)
}

func (localStore) CreateNote(input models.NoteInput, userGUID string) (*models.Note, error) {
	return models.CreateNote(input, userGUID)
}

func (localStore) UpdateNote(id int64, input models.NoteInput, userGUID string) (*models.Note, error) {
	return models.UpdateNote(id, input, userGUID)
}

func (localStore) DeleteNote(id int64, userGUID string) (bool, error) {
	return models.DeleteNote(id, userGUID)
}

func (localStore) ToggleNoteFlag(id int64, userGUID string) (*models.Note, error) {
	return models.ToggleNoteFlag(id, userGUID)
}

// ---- Categories ------------------------------------------------------------

func (localStore) ListCategories(userGUID string) ([]models.Category, error) {
	return models.ListCategories(0, 0, userGUID)
}

func (localStore) CreateCategory(name, userGUID string) (*models.Category, error) {
	return models.CreateCategory(models.CategoryInput{Name: name}, userGUID)
}

func (localStore) DeleteCategory(id int64, userGUID string) error {
	return models.DeleteCategory(id, userGUID)
}

func (localStore) GetCategoryByName(name, userGUID string) (*models.Category, error) {
	return models.GetCategoryByName(name, userGUID)
}

func (localStore) GetNoteCategories(noteID int64, userGUID string) ([]models.Category, error) {
	return models.GetNoteCategories(noteID, userGUID)
}

func (localStore) AddCategoryToNote(noteID, categoryID int64, userGUID string) error {
	return models.AddCategoryToNote(noteID, categoryID, userGUID)
}

func (localStore) RemoveCategoryFromNote(noteID, categoryID int64) error {
	return models.RemoveCategoryFromNote(noteID, categoryID)
}

// Compile-time check: the interface and this implementation must not drift.
var _ Store = localStore{}
