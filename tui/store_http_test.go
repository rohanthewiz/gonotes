package tui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gonotes/models"
)

// These tests run the real httpStore against a real HTTP server. The server is
// a stand-in for gonotes' web/api package, not a mock of the store: it decodes
// the same requests, enforces the same bearer-token rule, and answers in the
// same {success, data, error} envelope. So a change to the store's URLs, verbs,
// payload shapes, or auth handling fails here.
//
// It is backed by a fakeStore, which makes the round trips genuine — a note
// created over HTTP is a note the next GET returns. That is what lets
// TestSyncNoteCategoriesOverHTTP exercise commands.go's one piece of real
// logic across the wire.

// ---- The fake API ----------------------------------------------------------

type fakeAPI struct {
	t    *testing.T
	srv  *httptest.Server
	data *fakeStore
	user *models.User

	mu         sync.Mutex
	token      string // the token currently considered valid
	loginCount int    // how many times credentials were exchanged for a token
	rejectAuth bool   // when true, every bearer token is rejected with 401
}

const fakeAPIPassword = "test-password-123"

// newFakeAPI starts a server holding one account and returns it. The caller
// gets the account so it can seed data through api.data with the right owner.
func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	api := &fakeAPI{t: t, data: newFakeStore(), token: "initial-token"}
	api.user = api.data.addUser("api_user", fakeAPIPassword)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)

		user, _ := api.data.AuthenticateUser(in.Username, in.Password)
		if user == nil {
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		api.mu.Lock()
		api.loginCount++
		// A fresh login always mints a new token and clears any forced
		// rejection — that is what makes the silent-relogin path recoverable.
		api.token = "token-" + strconv.Itoa(api.loginCount)
		api.rejectAuth = false
		tok := api.token
		api.mu.Unlock()

		writeOK(w, http.StatusOK, map[string]any{"user": user.ToOutput(), "token": tok})
	})

	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password, RegistrationSecret string }
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &in)
		// Mirror the real handler's field name, which is snake_case.
		var raw map[string]string
		_ = json.Unmarshal(body, &raw)
		in.RegistrationSecret = raw["registration_secret"]

		user, err := api.data.CreateUser(in.Username, in.Password)
		if err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}

		api.mu.Lock()
		api.loginCount++
		api.token = "token-" + strconv.Itoa(api.loginCount)
		tok := api.token
		api.mu.Unlock()

		writeOK(w, http.StatusCreated, map[string]any{"user": user.ToOutput(), "token": tok})
	})

	// Everything below requires a valid bearer token.
	auth := func(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !api.authorized(r) {
				writeErr(w, http.StatusUnauthorized, "authentication required")
				return
			}
			h(w, r)
		}
	}

	mux.HandleFunc("GET /api/v1/auth/me", auth(func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, http.StatusOK, api.user.ToOutput())
	}))

	mux.HandleFunc("GET /api/v1/notes", auth(func(w http.ResponseWriter, r *http.Request) {
		notes, _ := api.data.ListNotes(api.user.GUID)
		writeOK(w, http.StatusOK, noteOutputs(notes))
	}))

	mux.HandleFunc("POST /api/v1/notes", auth(func(w http.ResponseWriter, r *http.Request) {
		var in models.NoteInput
		body, _ := readAll(r)
		if err := json.Unmarshal(body, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid request body")
			return
		}
		note, _ := api.data.CreateNote(in, api.user.GUID)
		writeOK(w, http.StatusCreated, note.ToOutput())
	}))

	mux.HandleFunc("GET /api/v1/notes/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		note, _ := api.data.GetNoteByID(pathID(r, "id"), api.user.GUID)
		if note == nil {
			writeErr(w, http.StatusNotFound, "note not found")
			return
		}
		writeOK(w, http.StatusOK, note.ToOutput())
	}))

	mux.HandleFunc("PUT /api/v1/notes/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		var in models.NoteInput
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &in)
		note, _ := api.data.UpdateNote(pathID(r, "id"), in, api.user.GUID)
		if note == nil {
			writeErr(w, http.StatusNotFound, "note not found")
			return
		}
		writeOK(w, http.StatusOK, note.ToOutput())
	}))

	mux.HandleFunc("DELETE /api/v1/notes/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r, "id")
		ok, _ := api.data.DeleteNote(id, api.user.GUID)
		if !ok {
			writeErr(w, http.StatusNotFound, "note not found")
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	}))

	mux.HandleFunc("PUT /api/v1/notes/{id}/flag", auth(func(w http.ResponseWriter, r *http.Request) {
		note, _ := api.data.ToggleNoteFlag(pathID(r, "id"), api.user.GUID)
		if note == nil {
			writeErr(w, http.StatusNotFound, "note not found")
			return
		}
		writeOK(w, http.StatusOK, note.ToOutput())
	}))

	// The note-categories endpoint answers with the richer detail shape, not
	// CategoryOutput — reproducing that here is the only way the store's
	// categoryFromDetail mapping gets tested.
	mux.HandleFunc("GET /api/v1/notes/{id}/categories", auth(func(w http.ResponseWriter, r *http.Request) {
		cats, _ := api.data.GetNoteCategories(pathID(r, "id"), api.user.GUID)
		details := make([]models.NoteCategoryDetailOutput, 0, len(cats))
		for _, c := range cats {
			details = append(details, models.NoteCategoryDetailOutput{
				ID:        c.ID,
				Name:      c.Name,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			})
		}
		writeOK(w, http.StatusOK, details)
	}))

	mux.HandleFunc("POST /api/v1/notes/{id}/categories/{cid}", auth(func(w http.ResponseWriter, r *http.Request) {
		noteID, catID := pathID(r, "id"), pathID(r, "cid")
		if err := api.data.AddCategoryToNote(noteID, catID, api.user.GUID); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeOK(w, http.StatusCreated, map[string]any{"note_id": noteID, "category_id": catID, "added": true})
	}))

	mux.HandleFunc("DELETE /api/v1/notes/{id}/categories/{cid}", auth(func(w http.ResponseWriter, r *http.Request) {
		noteID, catID := pathID(r, "id"), pathID(r, "cid")
		if err := api.data.RemoveCategoryFromNote(noteID, catID); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"note_id": noteID, "category_id": catID, "removed": true})
	}))

	mux.HandleFunc("GET /api/v1/categories", auth(func(w http.ResponseWriter, r *http.Request) {
		cats, _ := api.data.ListCategories(api.user.GUID)
		outs := make([]models.CategoryOutput, 0, len(cats))
		for i := range cats {
			outs = append(outs, cats[i].ToOutput())
		}
		writeOK(w, http.StatusOK, outs)
	}))

	mux.HandleFunc("POST /api/v1/categories", auth(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &in)
		cat, _ := api.data.CreateCategory(in.Name, api.user.GUID)
		writeOK(w, http.StatusCreated, cat.ToOutput())
	}))

	mux.HandleFunc("DELETE /api/v1/categories/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r, "id")
		if err := api.data.DeleteCategory(id, api.user.GUID); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	}))

	mux.HandleFunc("GET /api/v1/categories/{id}/notes", auth(func(w http.ResponseWriter, r *http.Request) {
		notes, _ := api.data.GetCategoryNotes(pathID(r, "id"), api.user.GUID)
		writeOK(w, http.StatusOK, noteOutputs(notes))
	}))

	api.srv = httptest.NewServer(mux)
	t.Cleanup(api.srv.Close)
	return api
}

func (a *fakeAPI) authorized(r *http.Request) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rejectAuth {
		return false
	}
	return r.Header.Get("Authorization") == "Bearer "+a.token
}

func (a *fakeAPI) currentToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token
}

func (a *fakeAPI) logins() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loginCount
}

// expireTokens makes every bearer token invalid until the next successful
// login — the JWT-expiry situation, reproducible on demand.
func (a *fakeAPI) expireTokens() {
	a.mu.Lock()
	a.rejectAuth = true
	a.mu.Unlock()
}

// store returns an httpStore pointed at this server, with its token cache in a
// temp dir so no test can touch the developer's real ~/.gonotes/.api_token.
func (a *fakeAPI) store(t *testing.T) *httpStore {
	t.Helper()
	t.Setenv(envTokenFile, filepath.Join(t.TempDir(), ".api_token"))
	return NewHTTPStore(a.srv.URL).(*httpStore)
}

func writeOK(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func pathID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id
}

func noteOutputs(notes []models.Note) []models.NoteOutput {
	outs := make([]models.NoteOutput, 0, len(notes))
	for i := range notes {
		outs = append(outs, notes[i].ToOutput())
	}
	return outs
}

// ---- Health probe ----------------------------------------------------------

// TestProbeServerRequiresTheGoNotesEnvelope is the test that keeps the mode
// decision honest. Port 8444 can be held by anything; if the probe accepted a
// bare 200 the TUI would start in HTTP mode against a stranger and every screen
// would show a decode error. Each rejection case below is a service that would
// pass a laxer check.
func TestProbeServerRequiresTheGoNotesEnvelope(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    bool
	}{
		{
			name:    "a real gonotes health response",
			handler: func(w http.ResponseWriter, r *http.Request) { writeOK(w, 200, map[string]string{"status": "ok"}) },
			want:    true,
		},
		{
			name:    "some other service answering 200",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) },
			want:    false,
		},
		{
			name:    "a JSON API that is not gonotes",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"healthy"}`)) },
			want:    false,
		},
		{
			name:    "the envelope, but reporting failure",
			handler: func(w http.ResponseWriter, r *http.Request) { writeErr(w, 200, "degraded") },
			want:    false,
		},
		{
			name:    "the envelope with a status other than ok",
			handler: func(w http.ResponseWriter, r *http.Request) { writeOK(w, 200, map[string]string{"status": "starting"}) },
			want:    false,
		},
		{
			name:    "a non-200 status",
			handler: func(w http.ResponseWriter, r *http.Request) { writeErr(w, 503, "unavailable") },
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			if got := ProbeServer(srv.URL, 2*time.Second); got != tc.want {
				t.Errorf("ProbeServer = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProbeServerRejectsADeadAddress covers the default path: nothing
// listening means local mode, which is the pre-Phase-4 behavior.
func TestProbeServerRejectsADeadAddress(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // the port is now closed; the dial will be refused

	if ProbeServer(url, 500*time.Millisecond) {
		t.Error("ProbeServer accepted an address with nothing listening")
	}
}

func TestServerURLPrefersTheEnvironment(t *testing.T) {
	t.Setenv(envURL, "")
	if got := ServerURL(); got != DefaultServerURL {
		t.Errorf("with no env, ServerURL = %q, want %q", got, DefaultServerURL)
	}

	// The trailing slash must be stripped, or every path becomes a double
	// slash and the router's exact-match patterns stop matching.
	t.Setenv(envURL, "http://hub.example:9000/")
	if got := ServerURL(); got != "http://hub.example:9000" {
		t.Errorf("ServerURL = %q, want the trailing slash stripped", got)
	}
}

// ---- Reads and mapping -----------------------------------------------------

// TestListNotesMapsOutputsToModels checks the *Output → models translation the
// screens depend on. The pointer/sql.Null split is the risky part: a body that
// arrives as a JSON null must become an invalid NullString, not the string
// "null" or an empty-but-valid one, because the detail screen keys off .Valid.
func TestListNotesMapsOutputsToModels(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)

	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	body := "# Heading\n\nSome text."
	tags := "alpha,beta"
	if _, err := st.CreateNote(models.NoteInput{
		Title:     "Full note",
		Body:      &body,
		Tags:      &tags,
		IsPrivate: true,
		IsFlagged: true,
	}, api.user.GUID); err != nil {
		t.Fatalf("CreateNote (full): %v", err)
	}
	if _, err := st.CreateNote(models.NoteInput{Title: "Bare note"}, api.user.GUID); err != nil {
		t.Fatalf("CreateNote (bare): %v", err)
	}

	notes, err := st.ListNotes(api.user.GUID)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}

	full, bare := notes[0], notes[1]

	if full.Title != "Full note" || full.Body.String != body || !full.Body.Valid {
		t.Errorf("full note body round-tripped as %+v", full.Body)
	}
	if full.Tags.String != tags || !full.Tags.Valid {
		t.Errorf("tags round-tripped as %+v", full.Tags)
	}
	if !full.IsPrivate || !full.IsFlagged {
		t.Errorf("flags lost: private=%v flagged=%v", full.IsPrivate, full.IsFlagged)
	}
	if full.CreatedAt.IsZero() || full.UpdatedAt.IsZero() {
		t.Error("timestamps did not survive the RFC3339 round trip")
	}
	if full.GUID == "" {
		t.Error("CreateNote must generate a GUID when the caller supplies none")
	}

	// The omitted fields are the real assertion.
	if bare.Body.Valid {
		t.Errorf("an absent body became a valid NullString (%q) — the detail screen would render an empty note as having content", bare.Body.String)
	}
	if bare.Description.Valid || bare.Tags.Valid {
		t.Error("absent description/tags became valid NullStrings")
	}
	if bare.DeletedAt.Valid {
		t.Error("a live note came back with DeletedAt set")
	}
}

// TestGetNoteByIDTreatsMissingAsNil pins the Store contract: a 404 is not an
// error. The detail screen shows the status-bar error text on err != nil, so
// getting this wrong turns "note was deleted in another window" into a red
// error line instead of a quiet empty state.
func TestGetNoteByIDTreatsMissingAsNil(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)
	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	note, err := st.GetNoteByID(9999, api.user.GUID)
	if err != nil {
		t.Errorf("a missing note returned an error: %v", err)
	}
	if note != nil {
		t.Errorf("a missing note returned %+v, want nil", note)
	}

	ok, err := st.DeleteNote(9999, api.user.GUID)
	if err != nil {
		t.Errorf("deleting a missing note returned an error: %v", err)
	}
	if ok {
		t.Error("deleting a missing note reported success")
	}
}

// TestGetCategoryByNameScansClientSide covers the one method with no endpoint
// behind it.
func TestGetCategoryByNameScansClientSide(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)
	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	if _, err := st.CreateCategory("work", api.user.GUID); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	got, err := st.GetCategoryByName("work", api.user.GUID)
	if err != nil {
		t.Fatalf("GetCategoryByName: %v", err)
	}
	if got == nil || got.Name != "work" {
		t.Fatalf("GetCategoryByName(work) = %+v, want the work category", got)
	}

	missing, err := st.GetCategoryByName("nope", api.user.GUID)
	if err != nil {
		t.Errorf("an unknown category name returned an error: %v", err)
	}
	if missing != nil {
		t.Errorf("an unknown category name returned %+v, want nil", missing)
	}
}

// TestSyncNoteCategoriesOverHTTP runs commands.go's category reconciliation —
// the only non-trivial logic in that file — against the wire. Six endpoints
// participate: list, create, note-categories, attach, detach.
func TestSyncNoteCategoriesOverHTTP(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)
	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	note, err := st.CreateNote(models.NoteInput{Title: "Categorized"}, api.user.GUID)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	if err := syncNoteCategories(st, note.ID, "work, reading", api.user.GUID); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	if got := categoryNames(t, st, note.ID, api.user.GUID); got != "reading,work" {
		t.Errorf("after initial sync: %q, want %q", got, "reading,work")
	}

	if err := syncNoteCategories(st, note.ID, "work,archive", api.user.GUID); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := categoryNames(t, st, note.ID, api.user.GUID); got != "archive,work" {
		t.Errorf("after second sync: %q, want %q", got, "archive,work")
	}
}

// TestListUsernamesDeclines states the deliberate gap. If someone later adds a
// user-list endpoint and wires it up here, this test is where the decision gets
// re-argued rather than quietly reversed. See ErrNoUserList.
func TestListUsernamesDeclines(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)

	names, err := st.ListUsernames()
	if !errors.Is(err, ErrNoUserList) {
		t.Errorf("ListUsernames error = %v, want ErrNoUserList", err)
	}
	if names != nil {
		t.Errorf("ListUsernames returned %v, want nil", names)
	}
}

// ---- Token cache -----------------------------------------------------------

// TestLoginCachesTheTokenForTheNextLaunch covers the file gn-clip.sh already
// writes: a successful login must leave a token behind, readable only by the
// owner, so the next `gonotes tui` needs no password.
func TestLoginCachesTheTokenForTheNextLaunch(t *testing.T) {
	api := newFakeAPI(t)

	tokenFile := filepath.Join(t.TempDir(), "nested", ".api_token")
	t.Setenv(envTokenFile, tokenFile)

	st := NewHTTPStore(api.srv.URL).(*httpStore)
	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	// The parent directory must be created — ~/.gonotes may not exist on a
	// machine that has only ever used the TUI in HTTP mode.
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("token file was not written: %v", err)
	}
	if string(raw) != api.currentToken() {
		t.Errorf("cached token = %q, want %q", raw, api.currentToken())
	}

	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o600) {
		t.Errorf("token file mode is %v, want 0600 — a JWT is a bearer credential", perm)
	}
}

// TestResumeSessionUsesTheCachedToken is the "skip straight to browse"
// behavior: a second launch finds a valid token and never renders a password
// field.
func TestResumeSessionUsesTheCachedToken(t *testing.T) {
	api := newFakeAPI(t)

	tokenFile := filepath.Join(t.TempDir(), ".api_token")
	t.Setenv(envTokenFile, tokenFile)

	first := NewHTTPStore(api.srv.URL)
	if _, err := first.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}
	loginsAfterFirst := api.logins()

	// A brand-new store, as a second process would build.
	second := NewHTTPStore(api.srv.URL)
	user, err := second.ResumeSession()
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if user == nil || user.Username != "api_user" {
		t.Fatalf("ResumeSession = %+v, want the cached user", user)
	}
	if api.logins() != loginsAfterFirst {
		t.Error("ResumeSession logged in again instead of reusing the cached token")
	}
}

// TestResumeSessionFallsBackToEnvCredentials is the unattended launch: no
// usable token, but the environment carries a credential pair (the same
// variables gn-clip.sh reads, including the base64 form a sync spoke holds).
func TestResumeSessionFallsBackToEnvCredentials(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t) // token file points at an empty temp dir

	t.Setenv(envUser, "api_user")
	t.Setenv(envPassword, "")
	t.Setenv(envPasswordB64, base64.StdEncoding.EncodeToString([]byte(fakeAPIPassword)))

	user, err := st.ResumeSession()
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if user == nil || user.Username != "api_user" {
		t.Fatalf("ResumeSession = %+v, want the env-configured user", user)
	}
}

// TestResumeSessionDeclinesWithoutCredentials is the ordinary interactive
// case: no token, nothing in the environment, so the login screen appears.
// Crucially it must not report an error — "could not resume" is normal.
func TestResumeSessionDeclinesWithoutCredentials(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)

	t.Setenv(envUser, "")
	t.Setenv(envSyncUser, "")
	t.Setenv(envPassword, "")
	t.Setenv(envPasswordB64, "")

	user, err := st.ResumeSession()
	if err != nil {
		t.Errorf("ResumeSession reported an error for the normal no-session case: %v", err)
	}
	if user != nil {
		t.Errorf("ResumeSession = %+v, want nil", user)
	}
}

// TestResumeSessionDropsAStaleToken guards a subtle failure: if a rejected
// cached token stayed on the store, every later request would send it and get
// a 401, and the silent-relogin path would fire on each one.
func TestResumeSessionDropsAStaleToken(t *testing.T) {
	api := newFakeAPI(t)

	tokenFile := filepath.Join(t.TempDir(), ".api_token")
	t.Setenv(envTokenFile, tokenFile)
	if err := os.WriteFile(tokenFile, []byte("long-expired-token"), 0o600); err != nil {
		t.Fatalf("seed token file: %v", err)
	}
	t.Setenv(envUser, "")
	t.Setenv(envSyncUser, "")
	t.Setenv(envPassword, "")
	t.Setenv(envPasswordB64, "")

	st := NewHTTPStore(api.srv.URL).(*httpStore)
	if user, _ := st.ResumeSession(); user != nil {
		t.Fatalf("a stale token resumed a session: %+v", user)
	}

	st.mu.Lock()
	tok := st.token
	st.mu.Unlock()
	if tok != "" {
		t.Errorf("the stale token is still set (%q); later requests would keep sending it", tok)
	}
}

// ---- Expiry and re-authentication ------------------------------------------

// TestExpiredTokenTriggersOneSilentRelogin is the reason httpStore remembers
// the password. A TUI can sit open longer than a JWT lives; the user should
// see their notes refresh, not an "unauthorized" error on the next keystroke.
//
// The count assertion matters as much as the success: exactly one extra login,
// not a retry loop.
func TestExpiredTokenTriggersOneSilentRelogin(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)

	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := st.CreateNote(models.NoteInput{Title: "Before expiry"}, api.user.GUID); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	loginsBefore := api.logins()

	api.expireTokens()

	notes, err := st.ListNotes(api.user.GUID)
	if err != nil {
		t.Fatalf("ListNotes after expiry should have recovered silently, got: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("got %d notes after the re-login, want 1", len(notes))
	}
	if got := api.logins() - loginsBefore; got != 1 {
		t.Errorf("the 401 caused %d logins, want exactly 1", got)
	}
}

// TestReloginFailureReportsTheOriginal401 covers the other branch: when the
// remembered credentials no longer work (password changed on the hub), the
// user must see "unauthorized", not a confusing report about a login attempt
// they did not make.
func TestReloginFailureReportsTheOriginal401(t *testing.T) {
	api := newFakeAPI(t)
	st := api.store(t)

	t.Setenv(envUser, "")
	t.Setenv(envSyncUser, "")
	t.Setenv(envPassword, "")
	t.Setenv(envPasswordB64, "")

	if _, err := st.AuthenticateUser("api_user", fakeAPIPassword); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Expire the token and invalidate the remembered password, so the silent
	// re-login is attempted and fails.
	api.expireTokens()
	st.mu.Lock()
	st.password = "no-longer-correct"
	st.mu.Unlock()

	_, err := st.ListNotes(api.user.GUID)
	if err == nil {
		t.Fatal("ListNotes succeeded with no valid credentials")
	}
	if !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("error was %q, want the server's 401 message", err.Error())
	}
}

// TestNonGoNotesResponseIsReportedByStatus covers the case the health probe is
// meant to prevent but cannot fully: a proxy that starts returning HTML
// mid-session. The store must not surface a JSON decode error, which reads as
// a bug in gonotes rather than a problem with the endpoint.
func TestNonGoNotesResponseIsReportedByStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
	}))
	defer srv.Close()

	t.Setenv(envTokenFile, filepath.Join(t.TempDir(), ".api_token"))
	st := NewHTTPStore(srv.URL)

	_, err := st.ListNotes("guid")
	if err == nil {
		t.Fatal("an HTML error page was accepted as a notes list")
	}
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Errorf("error was %q, want it to name the endpoint rather than blame JSON", err.Error())
	}
}
