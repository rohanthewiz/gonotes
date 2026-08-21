package tui

import (
	"database/sql"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gonotes/models"
	"gonotes/summarize"

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
	// links is note id → its category links, in attach order. Each link carries
	// the subcategory selection, because that is where the real schema keeps it
	// (the note_categories junction row) and a fake that stored selections on the
	// category instead would hide the bugs this seam exists to catch.
	links map[int64][]fakeLink

	nextNoteID int64
	nextCatID  int64

	// linkWrites counts every write to the junction — attach, detach, or a
	// change of selection. In production each of those records a sync change, so
	// a test can assert that an edit-free re-save costs nothing by watching this
	// rather than by inspecting state that looks identical either way.
	linkWrites int

	// resume, when set, is what ResumeSession returns — the HTTP store's
	// cached-token path expressed without a token.
	resume *models.User

	// failWith, when set, is returned by every read method. Injecting a
	// storage failure any other way means corrupting a real database.
	failWith error

	// ---- Sync ---------------------------------------------------------------
	// syncStatus is what SyncStatus reports; nil means this installation has no
	// sync configured, which is what most do. The counters are how a test says
	// "the user answered the prompt with this" without a hub anywhere in sight.
	syncStatus    *models.SyncClientStatus
	syncFailWith  error // when set, SyncNow reports this instead of succeeding
	syncCalls     int
	syncCompacted bool // a SyncNow was asked to compact on the way
	snoozeCalls   int
	compactCalls  int
	declineCalls  int

	// The summarizer's fake. summarizedText is what the last call was handed,
	// which is how a test checks that ctrl+r sent the BODY and not, say, the
	// title.
	summarizeCalls  int
	summarizedText  string
	summarizeResult *summarize.Result
	summarizeErr    error

	// tokens mirrors what the real stores keep: the lease tokens this "session"
	// holds. Same type, same bookkeeping — so a test exercises the same
	// token-by-note-id plumbing production uses.
	tokens *lockTokens
}

// fakeLink is one row of the note_categories junction.
type fakeLink struct {
	catID int64
	subs  []string
}

func newFakeStore() *fakeStore {
	// The lock registry is process-global (see models/lock.go), so a lease left
	// behind by one test would arrive in the next one as a note mysteriously
	// held by a session that no longer exists. Clearing it here rather than in
	// each test's cleanup means no test can forget.
	models.ResetNoteLocksForTest()
	return &fakeStore{
		users:      map[string]*models.User{},
		passwd:     map[string]string{},
		links:      map[int64][]fakeLink{},
		nextNoteID: 1,
		nextCatID:  1,
		tokens:     newLockTokens(),
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
		for _, l := range f.links[n.ID] {
			if l.catID == categoryID {
				out = append(out, n)
				break
			}
		}
	}
	return out, nil
}

// GetCategorySubcategoryNotes mirrors the real AND semantics: a note qualifies
// only if its link carries EVERY requested subcategory. Getting that wrong in
// the double would make a broken filter look like a working one.
func (f *fakeStore) GetCategorySubcategoryNotes(categoryName string, subcategories []string, userGUID string) ([]models.Note, error) {
	cat, err := f.GetCategoryByName(categoryName, userGUID)
	if err != nil {
		return nil, err
	}
	if cat == nil {
		return nil, nil
	}
	if len(subcategories) == 0 {
		return f.GetCategoryNotes(cat.ID, userGUID)
	}

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
		for _, l := range f.links[n.ID] {
			if l.catID != cat.ID {
				continue
			}
			hasAll := true
			for _, want := range subcategories {
				if !slices.Contains(l.subs, want) {
					hasAll = false
					break
				}
			}
			if hasAll {
				out = append(out, n)
			}
			break
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
		// Version 1, matching models.CreateNote. A double that left this at
		// zero would silently disable the version guard for every note it made
		// — zero means "unchecked" — so conflict tests would pass by never
		// having a conflict to detect.
		Version: 1,
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
		// The lock gate and the version guard, in the same order and with the
		// same meanings the real stores use — and BEFORE any field is touched.
		// A double that skipped them would let every conflict test pass by not
		// having any conflicts; one that ran them after the assignments would
		// report a refusal while having already applied the write.
		if err := models.AuthorizeNoteWrite(id, f.tokens.get(id)); err != nil {
			return nil, err
		}
		if input.ExpectedVersion != 0 && f.notes[i].Version != input.ExpectedVersion {
			current := f.notes[i]
			return nil, &models.StaleWriteError{Current: &current, ExpectedVersion: input.ExpectedVersion}
		}

		f.notes[i].Title = input.Title
		f.notes[i].Description = ptrToNull(input.Description)
		f.notes[i].Body = ptrToNull(input.Body)
		f.notes[i].Tags = ptrToNull(input.Tags)
		f.notes[i].IsPrivate = input.IsPrivate
		f.notes[i].IsFlagged = input.IsFlagged
		f.notes[i].UpdatedAt = time.Now()
		f.notes[i].Version++
		n := f.notes[i]
		return &n, nil
	}
	return nil, nil
}

// ---- Note locks --------------------------------------------------------------
//
// These delegate to the REAL registry rather than faking one. models/lock.go is
// pure in-memory state with no database behind it, so there is nothing to stub —
// and a hand-rolled second implementation of leases inside a test double is
// exactly the kind of thing that drifts from the real one and then certifies it.
//
// The registry is process-global, so newFakeStore resets it; see there.

func (f *fakeStore) AcquireNoteLock(noteID int64, userGUID string, holder models.LockHolder, steal bool) (*models.NoteLock, error) {
	lock, err := models.AcquireNoteLock(noteID, userGUID, holder, steal)
	if err != nil {
		return nil, err
	}
	f.tokens.set(noteID, lock.Token)
	return lock, nil
}

func (f *fakeStore) RenewNoteLock(noteID int64) (*models.NoteLock, error) {
	lock, err := models.RenewNoteLock(noteID, f.tokens.get(noteID))
	if err != nil {
		f.tokens.clear(noteID)
		return nil, err
	}
	return lock, nil
}

func (f *fakeStore) ReleaseNoteLock(noteID int64) error {
	models.ReleaseNoteLock(noteID, f.tokens.get(noteID))
	f.tokens.clear(noteID)
	return nil
}

func (f *fakeStore) ReleaseAllNoteLocks() error {
	for noteID, token := range f.tokens.drain() {
		models.ReleaseNoteLock(noteID, token)
	}
	return nil
}

func (f *fakeStore) GetNoteLock(noteID int64) (*models.NoteLock, error) {
	return models.GetNoteLock(noteID), nil
}

func (f *fakeStore) ListNoteLocks(userGUID string) ([]models.NoteLock, error) {
	return models.ListNoteLocks(userGUID), nil
}

// lockAsOther takes a note's lock as some OTHER session — how a test sets up
// contention, since there is no second process to run, only a second identity.
func (f *fakeStore) lockAsOther(noteID int64, label string) *models.NoteLock {
	lock, err := models.AcquireNoteLock(noteID, "", models.LockHolder{
		SessionID:  "session-" + label,
		Label:      label,
		PaneHandle: "w9:p9",
		Client:     "tui",
	}, false)
	if err != nil {
		return nil
	}
	return lock
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

// SetCategorySubcategories writes the definition and, like the real update,
// leaves the name and description it was handed in place — a caller that dropped
// them would see them preserved here and blanked in production.
func (f *fakeStore) SetCategorySubcategories(cat models.Category, subcategories []string, userGUID string) (*models.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	for i := range f.cats {
		if f.cats[i].ID != cat.ID || f.cats[i].CreatedBy.String != userGUID {
			continue
		}
		f.cats[i].Name = cat.Name
		f.cats[i].Description = cat.Description
		f.cats[i].Subcategories = subcategoriesJSON(subcategories)
		f.cats[i].UpdatedAt = time.Now()
		updated := f.cats[i]
		return &updated, nil
	}
	return nil, serr.New("category not found")
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
	for _, l := range f.links[noteID] {
		for _, c := range f.cats {
			if c.ID == l.catID {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// GetNoteCategoryDetails joins the definition (on the category) to the selection
// (on the link), which is exactly the join the real one does across two
// databases — and sorts by name, because the real one does and the form's
// prefill string is built in that order.
func (f *fakeStore) GetNoteCategoryDetails(noteID int64, userGUID string) ([]models.NoteCategoryDetailOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []models.NoteCategoryDetailOutput
	for _, l := range f.links[noteID] {
		for _, c := range f.cats {
			if c.ID != l.catID {
				continue
			}
			detail := models.NoteCategoryDetailOutput{
				ID:                    c.ID,
				Name:                  c.Name,
				Subcategories:         c.ToOutput().Subcategories,
				SelectedSubcategories: slices.Clone(l.subs),
				CreatedAt:             c.CreatedAt,
				UpdatedAt:             c.UpdatedAt,
			}
			if c.Description.Valid {
				desc := c.Description.String
				detail.Description = &desc
			}
			out = append(out, detail)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeStore) AddCategoryToNote(noteID, categoryID int64, userGUID string) error {
	return f.AddCategoryToNoteWithSubcategories(noteID, categoryID, nil, userGUID)
}

func (f *fakeStore) AddCategoryToNoteWithSubcategories(noteID, categoryID int64, subcategories []string, userGUID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links[noteID] {
		if l.catID == categoryID {
			return serr.New("category already added to this note")
		}
	}
	f.links[noteID] = append(f.links[noteID],
		fakeLink{catID: categoryID, subs: slices.Clone(subcategories)})
	f.linkWrites++
	return nil
}

func (f *fakeStore) SetNoteCategorySubcategories(noteID, categoryID int64, subcategories []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, l := range f.links[noteID] {
		if l.catID == categoryID {
			f.links[noteID][i].subs = slices.Clone(subcategories)
			f.linkWrites++
			return nil
		}
	}
	return serr.New("relationship not found")
}

func (f *fakeStore) RemoveCategoryFromNote(noteID, categoryID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, l := range f.links[noteID] {
		if l.catID == categoryID {
			f.links[noteID] = append(f.links[noteID][:i], f.links[noteID][i+1:]...)
			f.linkWrites++
			return nil
		}
	}
	return serr.New("relationship not found")
}

// ---- Sync ------------------------------------------------------------------
//
// The fake's sync is a struct a test sets and four methods that read it. That
// is enough for everything the TUI does with sync — the banner, the due
// prompt, the quit guard, the compact answer — because the TUI never computes
// any of it, it only renders what the store reports and calls back.

// syncStatus, when non-nil, is what SyncStatus returns. Nil is the common
// installation: no hub, nothing about sync on screen anywhere.
func (f *fakeStore) setSyncStatus(status *models.SyncClientStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncStatus = status
}

func (f *fakeStore) SyncStatus() (*models.SyncClientStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.syncStatus, nil
}

func (f *fakeStore) SyncNow(compact bool) (*models.SyncClientStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCalls++
	f.syncCompacted = f.syncCompacted || compact
	if f.syncFailWith != nil {
		return f.syncStatus, f.syncFailWith
	}
	// A successful cycle clears the two things the UI reads: nothing is
	// pending any more, and nothing is due.
	if f.syncStatus != nil {
		f.syncStatus.Pending = 0
		f.syncStatus.Due = false
	}
	return f.syncStatus, nil
}

func (f *fakeStore) SnoozeSync() (*models.SyncClientStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snoozeCalls++
	if f.syncStatus != nil {
		f.syncStatus.Due = false
	}
	return f.syncStatus, nil
}

func (f *fakeStore) CompactChanges() (*models.CompactionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compactCalls++
	return &models.CompactionResult{ChangesBefore: 9, ChangesAfter: 3}, nil
}

// Summarize returns a canned result and records the text it was given, so a
// test can assert what was sent without a model call. summarizeErr forces the
// failure path.
func (f *fakeStore) Summarize(text, model string) (*summarize.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summarizeCalls++
	f.summarizedText = text
	if f.summarizeErr != nil {
		return nil, f.summarizeErr
	}
	if f.summarizeResult != nil {
		return f.summarizeResult, nil
	}
	return &summarize.Result{Title: "Fake summary", Body: "condensed"}, nil
}

func (f *fakeStore) DeclineExitSync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declineCalls++
	return nil
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
