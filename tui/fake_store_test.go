package tui

import (
	"database/sql"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/serr"
)

// fakeStore is an in-memory Store, and it is the reason the seam in store.go
// was worth building.
//
// Before Phase 4 every teatest flow needed models.InitTestDB: a temp dir, two
// bytdb files, a bcrypt user creation, and a CloseDB in a cleanup — per test.
// That is slow, it serializes (the models layer keeps its databases in package
// globals, so two tests cannot hold different ones at once), and it made the
// UI tests fail for storage reasons.
//
// This has none of that. It is also the only way to exercise failure paths the
// real stores make hard to produce on demand — see the failWith hook.
type fakeStore struct {
	mu sync.Mutex

	users  map[string]*models.User // by username
	passwd map[string]string       // by username

	notes []models.Note
	cats  []models.Category
	links map[int64][]int64 // note id → category ids, in attach order

	nextNoteID int64
	nextCatID  int64

	// resume, when set, is what ResumeSession returns — the HTTP store's
	// cached-token path expressed without a token.
	resume *models.User

	// failWith, when set, is returned by every read method. Injecting a
	// storage failure any other way means corrupting a real database.
	failWith error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:      map[string]*models.User{},
		passwd:     map[string]string{},
		links:      map[int64][]int64{},
		nextNoteID: 1,
		nextCatID:  1,
	}
}

// addUser registers an account and returns it, mirroring what setupTestDB
// gives a test from the real database.
func (f *fakeStore) addUser(username, password string) *models.User {
	f.mu.Lock()
	defer f.mu.Unlock()

	u := &models.User{
		ID:        int64(len(f.users) + 1),
		GUID:      "guid-" + username,
		Username:  username,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	f.users[username] = u
	f.passwd[username] = password
	return u
}

// seedNote inserts a note directly, skipping the Store call — for tests whose
// subject is the UI, not the write path.
func (f *fakeStore) seedNote(userGUID, title, body string) models.Note {
	note, err := f.CreateNote(models.NoteInput{
		GUID:  "fake-" + title,
		Title: title,
		Body:  &body,
	}, userGUID)
	if err != nil {
		panic(err) // impossible: fakeStore.CreateNote only fails via failWith
	}
	return *note
}

// ---- Session ---------------------------------------------------------------

func (f *fakeStore) ResumeSession() (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resume, nil
}

func (f *fakeStore) ListUsernames() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	names := make([]string, 0, len(f.users))
	for name := range f.users {
		names = append(names, name)
	}
	// Map iteration order is random; the login screen's single-user prefill
	// would otherwise be tested against a coin flip.
	sort.Strings(names)
	return names, nil
}

func (f *fakeStore) AuthenticateUser(username, password string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if want, ok := f.passwd[username]; !ok || want != password {
		// (nil, nil) is what the real local store returns for bad credentials.
		return nil, nil
	}
	return f.users[username], nil
}

func (f *fakeStore) CreateUser(username, password string) (*models.User, error) {
	f.mu.Lock()
	if _, exists := f.users[username]; exists {
		f.mu.Unlock()
		return nil, serr.New("username already exists")
	}
	f.mu.Unlock()
	return f.addUser(username, password), nil
}

// ---- Notes -----------------------------------------------------------------

func (f *fakeStore) ListNotes(userGUID string) ([]models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	out := make([]models.Note, 0, len(f.notes))
	for _, n := range f.notes {
		if n.CreatedBy.String == userGUID && !n.DeletedAt.Valid {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeStore) GetCategoryNotes(categoryID int64, userGUID string) ([]models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []models.Note
	for _, n := range f.notes {
		if n.CreatedBy.String != userGUID || n.DeletedAt.Valid {
			continue
		}
		for _, id := range f.links[n.ID] {
			if id == categoryID {
				out = append(out, n)
				break
			}
		}
	}
	return out, nil
}

func (f *fakeStore) GetNoteByID(id int64, userGUID string) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	for i := range f.notes {
		if f.notes[i].ID == id && f.notes[i].CreatedBy.String == userGUID {
			n := f.notes[i]
			return &n, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) CreateNote(input models.NoteInput, userGUID string) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	now := time.Now()
	n := models.Note{
		ID:          f.nextNoteID,
		GUID:        input.GUID,
		Title:       input.Title,
		Description: ptrToNull(input.Description),
		Body:        ptrToNull(input.Body),
		Tags:        ptrToNull(input.Tags),
		IsPrivate:   input.IsPrivate,
		IsFlagged:   input.IsFlagged,
		CreatedBy:   sql.NullString{String: userGUID, Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.nextNoteID++
	f.notes = append(f.notes, n)
	return &n, nil
}

func (f *fakeStore) UpdateNote(id int64, input models.NoteInput, userGUID string) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	for i := range f.notes {
		if f.notes[i].ID != id || f.notes[i].CreatedBy.String != userGUID {
			continue
		}
		f.notes[i].Title = input.Title
		f.notes[i].Description = ptrToNull(input.Description)
		f.notes[i].Body = ptrToNull(input.Body)
		f.notes[i].Tags = ptrToNull(input.Tags)
		f.notes[i].IsPrivate = input.IsPrivate
		f.notes[i].IsFlagged = input.IsFlagged
		f.notes[i].UpdatedAt = time.Now()
		n := f.notes[i]
		return &n, nil
	}
	return nil, nil
}

func (f *fakeStore) DeleteNote(id int64, userGUID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return false, f.failWith
	}
	for i := range f.notes {
		if f.notes[i].ID == id && f.notes[i].CreatedBy.String == userGUID {
			f.notes[i].DeletedAt = sql.NullTime{Time: time.Now(), Valid: true}
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) ToggleNoteFlag(id int64, userGUID string) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	for i := range f.notes {
		if f.notes[i].ID == id && f.notes[i].CreatedBy.String == userGUID {
			f.notes[i].IsFlagged = !f.notes[i].IsFlagged
			n := f.notes[i]
			return &n, nil
		}
	}
	return nil, nil
}

// ---- Categories ------------------------------------------------------------

func (f *fakeStore) ListCategories(userGUID string) ([]models.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	out := make([]models.Category, 0, len(f.cats))
	for _, c := range f.cats {
		if c.CreatedBy.String == userGUID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeStore) CreateCategory(name, userGUID string) (*models.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	now := time.Now()
	c := models.Category{
		ID:        f.nextCatID,
		GUID:      "cat-guid-" + name,
		Name:      name,
		CreatedBy: sql.NullString{String: userGUID, Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.nextCatID++
	f.cats = append(f.cats, c)
	return &c, nil
}

func (f *fakeStore) DeleteCategory(id int64, userGUID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.cats {
		if f.cats[i].ID == id && f.cats[i].CreatedBy.String == userGUID {
			f.cats = append(f.cats[:i], f.cats[i+1:]...)
			return nil
		}
	}
	return serr.New("category not found")
}

func (f *fakeStore) GetCategoryByName(name, userGUID string) (*models.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	for i := range f.cats {
		if f.cats[i].Name == name && f.cats[i].CreatedBy.String == userGUID {
			c := f.cats[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) GetNoteCategories(noteID int64, userGUID string) ([]models.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []models.Category
	for _, catID := range f.links[noteID] {
		for _, c := range f.cats {
			if c.ID == catID {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func (f *fakeStore) AddCategoryToNote(noteID, categoryID int64, userGUID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.links[noteID] {
		if id == categoryID {
			return serr.New("category already added to this note")
		}
	}
	f.links[noteID] = append(f.links[noteID], categoryID)
	return nil
}

func (f *fakeStore) RemoveCategoryFromNote(noteID, categoryID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, id := range f.links[noteID] {
		if id == categoryID {
			f.links[noteID] = append(f.links[noteID][:i], f.links[noteID][i+1:]...)
			return nil
		}
	}
	return serr.New("relationship not found")
}

func ptrToNull(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

// The compile-time check is the whole contract: if Store grows a method, this
// file stops building and every teatest flow that depends on it says so.
var _ Store = (*fakeStore)(nil)

// ---- Tests on the double itself --------------------------------------------

// TestFakeStoreRoundTripsTheCategorySync exercises syncNoteCategories — the
// one piece of real logic in commands.go that is not a one-line wrapper — with
// no database and no server. It is the clearest demonstration of what the seam
// bought: this test used to be impossible without InitTestDB.
func TestFakeStoreRoundTripsTheCategorySync(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("sync_tester", "pw")
	note := fs.seedNote(user.GUID, "Categorized", "body")

	// First pass creates both categories on the fly.
	if err := syncNoteCategories(fs, note.ID, "work, reading", user.GUID); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	if got := categoryNames(t, fs, note.ID, user.GUID); got != "reading,work" {
		t.Errorf("after initial sync, categories are %q, want %q", got, "reading,work")
	}

	// Second pass drops one and adds one; the survivor must not be recreated.
	if err := syncNoteCategories(fs, note.ID, "work,archive", user.GUID); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := categoryNames(t, fs, note.ID, user.GUID); got != "archive,work" {
		t.Errorf("after second sync, categories are %q, want %q", got, "archive,work")
	}

	// The dropped category still exists as a category — unlinking a note must
	// never delete the category itself, which other notes may be using.
	cats, err := fs.ListCategories(user.GUID)
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(cats) != 3 {
		t.Errorf("expected work/reading/archive to all still exist, got %d categories", len(cats))
	}

	// Empty CSV clears every link.
	if err := syncNoteCategories(fs, note.ID, "  ", user.GUID); err != nil {
		t.Fatalf("clearing sync: %v", err)
	}
	if got := categoryNames(t, fs, note.ID, user.GUID); got != "" {
		t.Errorf("after clearing, categories are %q, want empty", got)
	}
}

// categoryNames returns the note's category names, sorted, comma-joined —
// sorted because the desired-set loop in syncNoteCategories iterates a map and
// therefore attaches in random order.
func categoryNames(t *testing.T, st Store, noteID int64, userGUID string) string {
	t.Helper()
	cats, err := st.GetNoteCategories(noteID, userGUID)
	if err != nil {
		t.Fatalf("GetNoteCategories: %v", err)
	}
	names := make([]string, 0, len(cats))
	for _, c := range cats {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
