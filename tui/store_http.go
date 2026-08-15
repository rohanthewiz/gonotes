package tui

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gonotes/models"

	"github.com/google/uuid"
	"github.com/rohanthewiz/serr"
)

// httpStore talks to a running GoNotes server over its v1 REST API.
//
// This is the mode a cats-hosted TUI runs in: bytdb is single-process, so when
// the server (or the MacApp that embeds it) already holds the data directory,
// HTTP is the only safe path to the same notes. See store.go for the split.
//
// Conventions are lifted wholesale from scripts/gn-clip.sh, which has been the
// de-facto API client for a while — same env vars, same token cache file, same
// find-or-create category dance. Anything a user already configured for
// gn-clip works here unchanged.
//
// Concurrency: every Store call happens inside a tea.Cmd, and Bubble Tea runs
// those on their own goroutines, so two calls can be in flight at once. The
// mutex guards the token and the cached credentials — the http.Client itself
// is already safe for concurrent use.
type httpStore struct {
	baseURL   string
	hc        *http.Client
	tokenFile string

	mu       sync.Mutex
	token    string
	username string // remembered from the last successful login, for silent re-auth
	password string
}

// Environment variables, all shared with gn-clip.sh.
const (
	envURL          = "GONOTES_URL"
	envUser         = "GONOTES_USER"
	envSyncUser     = "GONOTES_SYNC_USERNAME"
	envPassword     = "GONOTES_PASSWORD"
	envPasswordB64  = "GONOTES_SYNC_PASSWORD_B64"
	envTokenFile    = "GONOTES_TOKEN_FILE"
	envRegistration = "GONOTES_REGISTRATION_SECRET"
)

// DefaultServerURL is where a locally running GoNotes server listens.
const DefaultServerURL = "http://localhost:8444"

// ServerURL returns the base URL the TUI should probe and, if the probe
// succeeds, talk to.
func ServerURL() string {
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv(envURL)), "/"); u != "" {
		return u
	}
	return DefaultServerURL
}

// NewHTTPStore returns a Store that reaches the server at baseURL.
//
// Nothing is contacted here — construction is pure so main.go can decide the
// mode from the health probe alone and so tests can point the store at an
// httptest.Server with no side effects.
func NewHTTPStore(baseURL string) Store {
	return &httpStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		// The timeout is generous compared with the probe's: this covers real
		// work (a note save with a long body), not a liveness check.
		hc:        &http.Client{Timeout: 15 * time.Second},
		tokenFile: tokenFilePath(),
	}
}

// tokenFilePath resolves the JWT cache location, honoring the same override
// gn-clip.sh does. A failure to find HOME is not fatal: an empty path simply
// disables caching, and every launch logs in afresh.
func tokenFilePath() string {
	if p := strings.TrimSpace(os.Getenv(envTokenFile)); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gonotes", ".api_token")
}

// ---- Health probe ----------------------------------------------------------

// ProbeServer reports whether a GoNotes server is answering at baseURL.
//
// The check is deliberately stricter than "something returned 200": port 8444
// could be held by anything, and guessing wrong means the TUI starts in HTTP
// mode against a stranger. The response must be the GoNotes envelope with
// success true and data.status == "ok", which no unrelated service will
// produce by accident.
func ProbeServer(baseURL string, timeout time.Duration) bool {
	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Get(strings.TrimRight(baseURL, "/") + "/api/v1/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Cap the read: a hostile or merely confused endpoint must not be able to
	// stall startup by streaming forever.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false
	}

	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false
	}
	return env.Success && env.Data.Status == "ok"
}

// ---- Transport -------------------------------------------------------------

// apiEnvelope mirrors web/api.APIResponse. Data stays raw so each caller
// decodes it into the shape that endpoint actually returns.
type apiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// apiError carries the HTTP status alongside the server's message, because
// several callers need to tell "not found" apart from "went wrong": a missing
// note is a nil return, not an error the status bar should shout about.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message == "" {
		return "server returned " + strconv.Itoa(e.status)
	}
	return e.message
}

// isNotFound reports whether err is a 404 from the API.
func isNotFound(err error) bool {
	var ae *apiError
	if ok := asAPIError(err, &ae); ok {
		return ae.status == http.StatusNotFound
	}
	return false
}

// asAPIError unwraps err looking for an *apiError. serr wraps errors, so a
// plain type assertion is not enough.
func asAPIError(err error, target **apiError) bool {
	for err != nil {
		if ae, ok := err.(*apiError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// request performs one authenticated API call and decodes the envelope's data
// into out (which may be nil when the caller only cares that it succeeded).
//
// The 401 path is the interesting one. JWTs expire, and a TUI can sit open for
// days, so a single silent re-login is attempted with the credentials from the
// last successful login (or from the environment) and the call is retried
// once. If that fails the error propagates and the user sees it — there is no
// retry loop, because a wrong password would otherwise be re-sent forever.
func (s *httpStore) request(method, path string, body, out any) error {
	err := s.requestOnce(method, path, body, out)
	var ae *apiError
	if !asAPIError(err, &ae) || ae.status != http.StatusUnauthorized {
		return err
	}

	if relogErr := s.reauthenticate(); relogErr != nil {
		// Report the original 401, not the re-login failure: "unauthorized"
		// is the accurate description of what happened to the user's action.
		return err
	}
	return s.requestOnce(method, path, body, out)
}

func (s *httpStore) requestOnce(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return serr.Wrap(err, "failed to encode request body", "path", path)
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, s.baseURL+path, rdr)
	if err != nil {
		return serr.Wrap(err, "failed to build request", "path", path)
	}
	req.Header.Set("Content-Type", "application/json")

	s.mu.Lock()
	tok := s.token
	s.mu.Unlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := s.hc.Do(req)
	if err != nil {
		return serr.Wrap(err, "request to GoNotes server failed", "path", path)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return serr.Wrap(err, "failed to read response", "path", path)
	}

	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// A non-JSON body means we are not talking to GoNotes (a proxy error
		// page, say). Surface the status rather than a decode error.
		return &apiError{status: resp.StatusCode, message: "unexpected response from " + s.baseURL + path}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		return &apiError{status: resp.StatusCode, message: env.Error}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return serr.Wrap(err, "failed to decode response data", "path", path)
	}
	return nil
}

// ---- Authentication --------------------------------------------------------

// authResponse mirrors web/api.AuthResponse.
type authResponse struct {
	User  models.UserOutput `json:"user"`
	Token string            `json:"token"`
}

// login exchanges credentials for a JWT, remembering both: the token for
// subsequent calls, the credentials so an expired token can be renewed without
// interrupting the user.
//
// Holding the password in memory is a real (if small) cost. It is accepted
// because the alternative is worse: a TUI that dies on a token expiry mid-edit,
// or one that prompts again for a password already sitting in a textinput
// belonging to the same process.
func (s *httpStore) login(username, password string) (*models.User, error) {
	var out authResponse
	err := s.requestOnce(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": username, "password": password}, &out)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.token = out.Token
	s.username = username
	s.password = password
	s.mu.Unlock()

	s.cacheToken(out.Token)
	return userFromOutput(out.User), nil
}

// reauthenticate refreshes the token after a 401, preferring the credentials
// the user actually logged in with and falling back to the environment (the
// unattended case: a cats plugin tab whose token was cached long ago).
func (s *httpStore) reauthenticate() error {
	s.mu.Lock()
	user, pass := s.username, s.password
	s.mu.Unlock()

	if user == "" || pass == "" {
		user, pass = envCredentials()
	}
	if user == "" || pass == "" {
		return serr.New("no credentials available to re-authenticate")
	}
	_, err := s.login(user, pass)
	return err
}

// envCredentials reads the credential pair gn-clip.sh uses, including the
// base64 form a configured sync spoke already holds.
func envCredentials() (username, password string) {
	username = strings.TrimSpace(os.Getenv(envUser))
	if username == "" {
		username = strings.TrimSpace(os.Getenv(envSyncUser))
	}

	password = os.Getenv(envPassword)
	if password == "" {
		if b64 := strings.TrimSpace(os.Getenv(envPasswordB64)); b64 != "" {
			if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
				password = string(decoded)
			}
		}
	}
	return username, password
}

// cacheToken writes the JWT so back-to-back TUI launches don't each need a
// password. Best-effort by design — a read-only HOME must not break the TUI —
// and 0600 with a 0700 parent, matching gn-clip.sh's umask 077.
func (s *httpStore) cacheToken(token string) {
	if s.tokenFile == "" || token == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.tokenFile), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(s.tokenFile, []byte(token), 0o600)
}

func (s *httpStore) readCachedToken() string {
	if s.tokenFile == "" {
		return ""
	}
	b, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---- Store: session --------------------------------------------------------

// ResumeSession gets the user past the login screen without typing, in the two
// situations where that is possible and safe:
//
//  1. A cached JWT that /auth/me still accepts. This is the common case for a
//     second launch, and the reason the token file exists.
//  2. A full credential pair in the environment. This is the unattended case —
//     a cats plugin tab or a scripted launch — and it is opt-in by definition,
//     since the user had to put the password there.
//
// Any failure returns (nil, nil): "could not resume" is not an error worth
// showing, it just means the login screen appears as normal.
func (s *httpStore) ResumeSession() (*models.User, error) {
	if tok := s.readCachedToken(); tok != "" {
		s.mu.Lock()
		s.token = tok
		s.mu.Unlock()

		var out models.UserOutput
		// requestOnce, not request: a 401 here means the cached token is stale,
		// which is exactly what the credential fallback below handles. Going
		// through the retry path would just duplicate it.
		if err := s.requestOnce(http.MethodGet, "/api/v1/auth/me", nil, &out); err == nil {
			return userFromOutput(out), nil
		}

		// Stale token — drop it so no later call sends it.
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
	}

	if user, pass := envCredentials(); user != "" && pass != "" {
		u, err := s.login(user, pass)
		if err != nil {
			return nil, nil
		}
		return u, nil
	}
	return nil, nil
}

// ListUsernames always declines. See ErrNoUserList for the reasoning.
func (s *httpStore) ListUsernames() ([]string, error) { return nil, ErrNoUserList }

func (s *httpStore) AuthenticateUser(username, password string) (*models.User, error) {
	return s.login(username, password)
}

// CreateUser registers an account. Past the first user the server gates
// registration behind an invite token or a shared secret; the secret is passed
// through from the environment when present, which is the only one of the two
// a TUI could plausibly hold.
func (s *httpStore) CreateUser(username, password string) (*models.User, error) {
	payload := map[string]string{"username": username, "password": password}
	if secret := strings.TrimSpace(os.Getenv(envRegistration)); secret != "" {
		payload["registration_secret"] = secret
	}

	var out authResponse
	if err := s.requestOnce(http.MethodPost, "/api/v1/auth/register", payload, &out); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.token = out.Token
	s.username = username
	s.password = password
	s.mu.Unlock()

	s.cacheToken(out.Token)
	return userFromOutput(out.User), nil
}

// ---- Store: notes ----------------------------------------------------------

// The userGUID arguments go unused in this file: the server derives ownership
// from the JWT and scopes every query itself. They stay in the signatures
// because the local store genuinely needs them, and a Store whose two
// implementations took different arguments would not be a seam.

func (s *httpStore) ListNotes(_ string) ([]models.Note, error) {
	var out []models.NoteOutput
	if err := s.request(http.MethodGet, "/api/v1/notes", nil, &out); err != nil {
		return nil, serr.Wrap(err, "failed to list notes")
	}
	return notesFromOutputs(out), nil
}

// GetCategoryNotes uses the category's own notes endpoint rather than
// /notes?cat=<name>, so no name lookup is needed and a rename mid-session
// can't silently return the wrong set.
func (s *httpStore) GetCategoryNotes(categoryID int64, _ string) ([]models.Note, error) {
	var out []models.NoteOutput
	path := "/api/v1/categories/" + strconv.FormatInt(categoryID, 10) + "/notes"
	if err := s.request(http.MethodGet, path, nil, &out); err != nil {
		return nil, serr.Wrap(err, "failed to list category notes")
	}
	return notesFromOutputs(out), nil
}

func (s *httpStore) GetNoteByID(id int64, _ string) (*models.Note, error) {
	var out models.NoteOutput
	err := s.request(http.MethodGet, "/api/v1/notes/"+strconv.FormatInt(id, 10), nil, &out)
	if isNotFound(err) {
		return nil, nil // contract: missing is not an error
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to load note")
	}
	note := noteFromOutput(out)
	return &note, nil
}

// CreateNote fills in a GUID when the caller left it blank, matching what the
// local path does — the models layer expects the caller to supply one, and so
// does the API.
func (s *httpStore) CreateNote(input models.NoteInput, _ string) (*models.Note, error) {
	if input.GUID == "" {
		input.GUID = uuid.New().String()
	}
	var out models.NoteOutput
	if err := s.request(http.MethodPost, "/api/v1/notes", input, &out); err != nil {
		return nil, serr.Wrap(err, "failed to create note")
	}
	note := noteFromOutput(out)
	return &note, nil
}

func (s *httpStore) UpdateNote(id int64, input models.NoteInput, _ string) (*models.Note, error) {
	var out models.NoteOutput
	err := s.request(http.MethodPut, "/api/v1/notes/"+strconv.FormatInt(id, 10), input, &out)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to update note")
	}
	note := noteFromOutput(out)
	return &note, nil
}

func (s *httpStore) DeleteNote(id int64, _ string) (bool, error) {
	err := s.request(http.MethodDelete, "/api/v1/notes/"+strconv.FormatInt(id, 10), nil, nil)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, serr.Wrap(err, "failed to delete note")
	}
	return true, nil
}

func (s *httpStore) ToggleNoteFlag(id int64, _ string) (*models.Note, error) {
	var out models.NoteOutput
	err := s.request(http.MethodPut, "/api/v1/notes/"+strconv.FormatInt(id, 10)+"/flag", nil, &out)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to toggle flag")
	}
	note := noteFromOutput(out)
	return &note, nil
}

// ---- Store: categories -----------------------------------------------------

func (s *httpStore) ListCategories(_ string) ([]models.Category, error) {
	var out []models.CategoryOutput
	if err := s.request(http.MethodGet, "/api/v1/categories", nil, &out); err != nil {
		return nil, serr.Wrap(err, "failed to list categories")
	}
	cats := make([]models.Category, 0, len(out))
	for _, c := range out {
		cats = append(cats, categoryFromOutput(c))
	}
	return cats, nil
}

func (s *httpStore) CreateCategory(name, _ string) (*models.Category, error) {
	var out models.CategoryOutput
	if err := s.request(http.MethodPost, "/api/v1/categories",
		map[string]string{"name": name}, &out); err != nil {
		return nil, serr.Wrap(err, "failed to create category")
	}
	cat := categoryFromOutput(out)
	return &cat, nil
}

func (s *httpStore) DeleteCategory(id int64, _ string) error {
	if err := s.request(http.MethodDelete,
		"/api/v1/categories/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
		return serr.Wrap(err, "failed to delete category")
	}
	return nil
}

// GetCategoryByName is resolved client-side. The API has no lookup-by-name
// endpoint, and adding one for this would be the wrong trade: category lists
// are short, the call already happens once per save, and a client-side scan
// keeps the server surface unchanged.
func (s *httpStore) GetCategoryByName(name, userGUID string) (*models.Category, error) {
	cats, err := s.ListCategories(userGUID)
	if err != nil {
		return nil, err
	}
	for i := range cats {
		if cats[i].Name == name {
			return &cats[i], nil
		}
	}
	return nil, nil // contract: not found is not an error
}

// GetNoteCategories decodes the note-categories endpoint's richer detail
// shape. It carries the junction table's selected subcategories but no GUID
// or created_by — neither of which the TUI reads, since it shows names and
// filters by id. A caller that ever needs the GUID must fetch the category
// itself rather than assume this filled it in.
func (s *httpStore) GetNoteCategories(noteID int64, _ string) ([]models.Category, error) {
	var out []models.NoteCategoryDetailOutput
	path := "/api/v1/notes/" + strconv.FormatInt(noteID, 10) + "/categories"
	if err := s.request(http.MethodGet, path, nil, &out); err != nil {
		return nil, serr.Wrap(err, "failed to load note categories")
	}
	cats := make([]models.Category, 0, len(out))
	for _, d := range out {
		cats = append(cats, categoryFromDetail(d))
	}
	return cats, nil
}

func (s *httpStore) AddCategoryToNote(noteID, categoryID int64, _ string) error {
	path := "/api/v1/notes/" + strconv.FormatInt(noteID, 10) +
		"/categories/" + strconv.FormatInt(categoryID, 10)
	// An empty object rather than no body: the handler decodes the body only
	// when it is non-empty, and this keeps the request shape identical to
	// gn-clip.sh's, which is the shape the endpoint is exercised with daily.
	if err := s.request(http.MethodPost, path, map[string]any{}, nil); err != nil {
		return serr.Wrap(err, "failed to attach category")
	}
	return nil
}

func (s *httpStore) RemoveCategoryFromNote(noteID, categoryID int64) error {
	path := "/api/v1/notes/" + strconv.FormatInt(noteID, 10) +
		"/categories/" + strconv.FormatInt(categoryID, 10)
	if err := s.request(http.MethodDelete, path, nil, nil); err != nil {
		return serr.Wrap(err, "failed to detach category")
	}
	return nil
}

// ---- Output → model mapping ------------------------------------------------
//
// The API speaks in *Output structs (pointers and RFC3339 strings, so JSON
// omits empties cleanly); the TUI's screens are written against the models
// structs (sql.Null* and time.Time). These convert one to the other so no
// screen has to know which store it is talking to.
//
// Note what is deliberately NOT reconstructed: Note.EncryptionIV (dead since
// bytdb moved to whole-database encryption) and User.PasswordHash (the API
// never sends it, and the TUI never reads it).

func notesFromOutputs(out []models.NoteOutput) []models.Note {
	notes := make([]models.Note, 0, len(out))
	for _, o := range out {
		notes = append(notes, noteFromOutput(o))
	}
	return notes
}

func noteFromOutput(o models.NoteOutput) models.Note {
	return models.Note{
		ID:          o.ID,
		GUID:        o.GUID,
		Title:       o.Title,
		Description: nullString(o.Description),
		Body:        nullString(o.Body),
		Tags:        nullString(o.Tags),
		IsPrivate:   o.IsPrivate,
		IsFlagged:   o.IsFlagged,
		CreatedBy:   nullString(o.CreatedBy),
		UpdatedBy:   nullString(o.UpdatedBy),
		CreatedAt:   parseAPITime(o.CreatedAt),
		UpdatedAt:   parseAPITime(o.UpdatedAt),
		AuthoredAt:  nullTime(o.AuthoredAt),
		SyncedAt:    nullTime(o.SyncedAt),
		DeletedAt:   nullTime(o.DeletedAt),
	}
}

func categoryFromOutput(o models.CategoryOutput) models.Category {
	return models.Category{
		ID:            o.ID,
		GUID:          o.GUID,
		Name:          o.Name,
		Description:   nullString(o.Description),
		Subcategories: subcategoriesJSON(o.Subcategories),
		CreatedAt:     o.CreatedAt,
		UpdatedAt:     o.UpdatedAt,
	}
}

func categoryFromDetail(d models.NoteCategoryDetailOutput) models.Category {
	return models.Category{
		ID:            d.ID,
		Name:          d.Name,
		Description:   nullString(d.Description),
		Subcategories: subcategoriesJSON(d.Subcategories),
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

func userFromOutput(o models.UserOutput) *models.User {
	return &models.User{
		ID:          o.ID,
		GUID:        o.GUID,
		Username:    o.Username,
		Email:       nullString(o.Email),
		DisplayName: nullString(o.DisplayName),
		IsActive:    o.IsActive,
		IsAdmin:     o.IsAdmin,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}
}

// subcategoriesJSON re-encodes the decoded list into the JSON-array-in-a-string
// form the Category model stores it as, so a Category from either store
// serializes identically.
func subcategoriesJSON(subs []string) sql.NullString {
	if len(subs) == 0 {
		return sql.NullString{}
	}
	b, err := json.Marshal(subs)
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func nullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullTime(p *string) sql.NullTime {
	if p == nil || *p == "" {
		return sql.NullTime{}
	}
	t, err := time.Parse(time.RFC3339, *p)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// parseAPITime falls back to the zero time rather than failing the whole
// decode: a note with an unreadable timestamp still has a title and a body,
// and the list would rather show it than drop it.
func parseAPITime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Compile-time check: the interface and this implementation must not drift.
var _ Store = (*httpStore)(nil)
