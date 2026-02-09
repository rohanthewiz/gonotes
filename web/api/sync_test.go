package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/rohanthewiz/rweb"

	"gonotes/models"
	"gonotes/web"
	"gonotes/web/api"
)

// syncTestServer manages a running server instance for sync integration tests.
// Mirrors the categoryTestServer pattern from categories_test.go.
type syncTestServer struct {
	baseURL   string
	client    *http.Client
	server    *rweb.Server
	authToken string // JWT token for authenticated requests
}

// createAuthenticatedRequest creates an HTTP request with the Authorization header.
func (s *syncTestServer) createAuthenticatedRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// registerAndLogin registers a test user and obtains a JWT token.
func (s *syncTestServer) registerAndLogin(t *testing.T) {
	t.Helper()

	regInput := map[string]string{
		"username": "synctestuser",
		"password": "testpassword123",
	}
	body, _ := json.Marshal(regInput)
	resp, err := http.Post(s.baseURL+"/api/v1/auth/register", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("failed to register user, status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result api.APIResponse
	json.NewDecoder(resp.Body).Decode(&result)
	data := result.Data.(map[string]interface{})
	s.authToken = data["token"].(string)
}

// setupSyncTestServer creates a test server with a fresh database for sync tests.
func setupSyncTestServer(t *testing.T) (*syncTestServer, func()) {
	t.Helper()

	os.Remove("./data/test_sync.ddb")
	os.Remove("./data/test_sync.ddb.wal")

	if err := models.InitTestDB("./data/test_sync.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Initialize JWT for auth
	os.Setenv("GONOTES_JWT_SECRET", "test-secret-key-for-jwt-testing-32chars")
	if err := models.InitJWT(); err != nil {
		t.Fatalf("failed to initialize JWT: %v", err)
	}

	readyChan := make(chan struct{}, 1)
	srv := web.NewTestServer(rweb.ServerOptions{
		Verbose:   true,
		ReadyChan: readyChan,
		Address:   "localhost:", // Dynamic port
	})

	go func() {
		_ = srv.Run()
	}()

	<-readyChan

	testServer := &syncTestServer{
		baseURL: fmt.Sprintf("http://localhost:%s", srv.GetListenPort()),
		client:  &http.Client{Timeout: 5 * time.Second},
		server:  srv,
	}

	cleanup := func() {
		models.CloseDB()
		os.Remove("./data/test_sync.ddb")
		os.Remove("./data/test_sync.ddb.wal")
	}

	return testServer, cleanup
}

// ============================================================================
// TestHealthEndpoint
// ============================================================================

// TestHealthEndpoint verifies that GET /api/v1/health returns 200 without auth.
func TestHealthEndpoint(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	resp, err := http.Get(server.baseURL + "/api/v1/health")
	if err != nil {
		t.Fatalf("failed to hit health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var result api.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !result.Success {
		t.Error("expected success to be true")
	}

	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}
	if status, ok := data["status"].(string); !ok || status != "ok" {
		t.Errorf("expected status 'ok', got %v", data["status"])
	}
}

// ============================================================================
// TestPullEndpoint_Empty
// ============================================================================

// TestPullEndpoint_Empty verifies that pulling from a fresh DB returns
// an empty changes array.
func TestPullEndpoint_Empty(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	server.registerAndLogin(t)

	req, err := server.createAuthenticatedRequest("GET",
		server.baseURL+"/api/v1/sync/pull?peer_id=spoke-001", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := server.client.Do(req)
	if err != nil {
		t.Fatalf("failed to pull changes: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result api.APIResponse
	json.NewDecoder(resp.Body).Decode(&result)

	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object with changes and has_more")
	}

	changes, ok := data["changes"].([]interface{})
	if !ok {
		t.Fatal("expected changes to be an array")
	}

	if len(changes) != 0 {
		t.Errorf("expected 0 changes on fresh DB, got %d", len(changes))
	}
}

// ============================================================================
// TestPullPushRoundTrip
// ============================================================================

// TestPullPushRoundTrip creates a note locally, pulls it (simulating a spoke),
// then pushes a new note back and verifies it was accepted.
func TestPullPushRoundTrip(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	server.registerAndLogin(t)

	// Step 1: Create a note locally via the API
	noteInput := models.NoteInput{
		GUID:  "roundtrip-note-001",
		Title: "Round Trip Test Note",
	}
	body, _ := json.Marshal(noteInput)
	createReq, _ := server.createAuthenticatedRequest("POST",
		server.baseURL+"/api/v1/notes", bytes.NewBuffer(body))
	createResp, err := server.client.Do(createReq)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for note creation, got %d", createResp.StatusCode)
	}

	// Step 2: Pull changes as a spoke peer
	pullReq, _ := server.createAuthenticatedRequest("GET",
		server.baseURL+"/api/v1/sync/pull?peer_id=spoke-roundtrip", nil)
	pullResp, err := server.client.Do(pullReq)
	if err != nil {
		t.Fatalf("failed to pull changes: %v", err)
	}
	defer pullResp.Body.Close()

	var pullResult api.APIResponse
	json.NewDecoder(pullResp.Body).Decode(&pullResult)
	pullData := pullResult.Data.(map[string]interface{})
	changes := pullData["changes"].([]interface{})

	if len(changes) == 0 {
		t.Fatal("expected at least 1 change after creating a note")
	}

	// Step 3: Push a new note from the "spoke"
	pushTitle := "Pushed From Spoke"
	pushBody := "This came from the spoke"
	pushReq := models.SyncPushRequest{
		PeerID: "spoke-roundtrip",
		Changes: []models.SyncChange{
			{
				GUID:       "push-change-guid-001",
				EntityType: "note",
				EntityGUID: "pushed-note-from-spoke",
				Operation:  models.OperationCreate,
				Fragment: &models.NoteFragmentOutput{
					Bitmask: models.FragmentTitle | models.FragmentBody,
					Title:   &pushTitle,
					Body:    &pushBody,
				},
				AuthoredAt: time.Now(),
				User:       "spoke-user",
			},
		},
	}

	pushBodyJSON, _ := json.Marshal(pushReq)
	pushHTTPReq, _ := server.createAuthenticatedRequest("POST",
		server.baseURL+"/api/v1/sync/push", bytes.NewBuffer(pushBodyJSON))
	pushResp, err := server.client.Do(pushHTTPReq)
	if err != nil {
		t.Fatalf("failed to push changes: %v", err)
	}
	defer pushResp.Body.Close()

	if pushResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(pushResp.Body)
		t.Fatalf("expected 200 for push, got %d: %s", pushResp.StatusCode, string(bodyBytes))
	}

	var pushResult api.APIResponse
	json.NewDecoder(pushResp.Body).Decode(&pushResult)
	pushData := pushResult.Data.(map[string]interface{})
	accepted := pushData["accepted"].([]interface{})
	rejected := pushData["rejected"].([]interface{})

	if len(accepted) != 1 {
		t.Errorf("expected 1 accepted change, got %d", len(accepted))
	}
	if len(rejected) != 0 {
		t.Errorf("expected 0 rejected changes, got %d", len(rejected))
	}

	// Verify the pushed note exists in the local database
	note, err := models.GetNoteByGUID("pushed-note-from-spoke")
	if err != nil {
		t.Fatalf("failed to get pushed note: %v", err)
	}
	if note == nil {
		t.Fatal("expected pushed note to exist")
	}
	if note.Title != pushTitle {
		t.Errorf("expected title %q, got %q", pushTitle, note.Title)
	}
}

// ============================================================================
// TestPushIdempotency
// ============================================================================

// TestPushIdempotency verifies that pushing the same change GUID twice
// is accepted without creating a duplicate.
func TestPushIdempotency(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	server.registerAndLogin(t)

	pushTitle := "Idempotent Push"
	pushReq := models.SyncPushRequest{
		PeerID: "spoke-idempotent",
		Changes: []models.SyncChange{
			{
				GUID:       "idempotent-push-guid-001",
				EntityType: "note",
				EntityGUID: "idempotent-pushed-note",
				Operation:  models.OperationCreate,
				Fragment: &models.NoteFragmentOutput{
					Bitmask: models.FragmentTitle,
					Title:   &pushTitle,
				},
				AuthoredAt: time.Now(),
				User:       "spoke-user",
			},
		},
	}

	pushBodyJSON, _ := json.Marshal(pushReq)

	// First push
	req1, _ := server.createAuthenticatedRequest("POST",
		server.baseURL+"/api/v1/sync/push", bytes.NewBuffer(pushBodyJSON))
	resp1, err := server.client.Do(req1)
	if err != nil {
		t.Fatalf("first push failed: %v", err)
	}
	resp1.Body.Close()

	// Second push with the same GUID — should not fail
	pushBodyJSON2, _ := json.Marshal(pushReq) // re-marshal since buffer was consumed
	req2, _ := server.createAuthenticatedRequest("POST",
		server.baseURL+"/api/v1/sync/push", bytes.NewBuffer(pushBodyJSON2))
	resp2, err := server.client.Do(req2)
	if err != nil {
		t.Fatalf("second push failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 200 for idempotent push, got %d: %s", resp2.StatusCode, string(bodyBytes))
	}

	var result api.APIResponse
	json.NewDecoder(resp2.Body).Decode(&result)
	data := result.Data.(map[string]interface{})
	accepted := data["accepted"].([]interface{})

	// The idempotent push should be accepted (not rejected)
	if len(accepted) != 1 {
		t.Errorf("expected 1 accepted (idempotent), got %d", len(accepted))
	}
}

// ============================================================================
// TestSnapshotEndpoint
// ============================================================================

// TestSnapshotEndpoint verifies that GET /api/v1/sync/snapshot returns
// the full entity state.
func TestSnapshotEndpoint(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	server.registerAndLogin(t)

	// Create a note
	body := "Full body for snapshot test"
	noteInput := models.NoteInput{
		GUID:  "snapshot-endpoint-note",
		Title: "Snapshot Endpoint Note",
		Body:  &body,
	}
	bodyJSON, _ := json.Marshal(noteInput)
	createReq, _ := server.createAuthenticatedRequest("POST",
		server.baseURL+"/api/v1/notes", bytes.NewBuffer(bodyJSON))
	createResp, err := server.client.Do(createReq)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}
	createResp.Body.Close()

	// Get snapshot
	snapReq, _ := server.createAuthenticatedRequest("GET",
		server.baseURL+"/api/v1/sync/snapshot?entity_type=note&entity_guid=snapshot-endpoint-note", nil)
	snapResp, err := server.client.Do(snapReq)
	if err != nil {
		t.Fatalf("failed to get snapshot: %v", err)
	}
	defer snapResp.Body.Close()

	if snapResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(snapResp.Body)
		t.Fatalf("expected 200, got %d: %s", snapResp.StatusCode, string(bodyBytes))
	}

	var result api.APIResponse
	json.NewDecoder(snapResp.Body).Decode(&result)

	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}

	if entityType, ok := data["entity_type"].(string); !ok || entityType != "note" {
		t.Errorf("expected entity_type 'note', got %v", data["entity_type"])
	}
	if entityGUID, ok := data["entity_guid"].(string); !ok || entityGUID != "snapshot-endpoint-note" {
		t.Errorf("expected entity_guid 'snapshot-endpoint-note', got %v", data["entity_guid"])
	}

	// Verify fragment contains the full body
	fragment, ok := data["fragment"].(map[string]interface{})
	if !ok {
		t.Fatal("expected fragment in snapshot response")
	}
	if fragBody, ok := fragment["body"].(string); !ok || fragBody != body {
		t.Errorf("expected body %q, got %v", body, fragment["body"])
	}
}

// ============================================================================
// TestSyncStatusEndpoint
// ============================================================================

// TestSyncStatusEndpoint verifies that GET /api/v1/sync/status returns
// correct counts and a checksum.
func TestSyncStatusEndpoint(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	server.registerAndLogin(t)

	// Create some data
	noteInput := models.NoteInput{
		GUID:  "status-endpoint-note",
		Title: "Status Note",
	}
	bodyJSON, _ := json.Marshal(noteInput)
	createReq, _ := server.createAuthenticatedRequest("POST",
		server.baseURL+"/api/v1/notes", bytes.NewBuffer(bodyJSON))
	createResp, _ := server.client.Do(createReq)
	createResp.Body.Close()

	catInput := models.CategoryInput{Name: "Status Category"}
	catJSON, _ := json.Marshal(catInput)
	http.Post(server.baseURL+"/api/v1/categories", "application/json", bytes.NewBuffer(catJSON))

	// Get sync status
	statusReq, _ := server.createAuthenticatedRequest("GET",
		server.baseURL+"/api/v1/sync/status", nil)
	statusResp, err := server.client.Do(statusReq)
	if err != nil {
		t.Fatalf("failed to get sync status: %v", err)
	}
	defer statusResp.Body.Close()

	if statusResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(statusResp.Body)
		t.Fatalf("expected 200, got %d: %s", statusResp.StatusCode, string(bodyBytes))
	}

	var result api.APIResponse
	json.NewDecoder(statusResp.Body).Decode(&result)

	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}

	noteCount, ok := data["note_count"].(float64)
	if !ok {
		t.Fatal("expected note_count to be a number")
	}
	if noteCount < 1 {
		t.Errorf("expected at least 1 note, got %v", noteCount)
	}

	if checksum, ok := data["checksum"].(string); !ok || checksum == "" {
		t.Errorf("expected non-empty checksum, got %v", data["checksum"])
	}
}

// ============================================================================
// TestPullPaginates
// ============================================================================

// TestPullPaginates verifies that pulling with a small limit returns
// has_more=true when additional changes exist.
func TestPullPaginates(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()

	server.registerAndLogin(t)

	// Create several notes to generate multiple changes
	for i := 1; i <= 5; i++ {
		noteInput := models.NoteInput{
			GUID:  fmt.Sprintf("paginate-note-%d", i),
			Title: fmt.Sprintf("Paginate Note %d", i),
		}
		bodyJSON, _ := json.Marshal(noteInput)
		req, _ := server.createAuthenticatedRequest("POST",
			server.baseURL+"/api/v1/notes", bytes.NewBuffer(bodyJSON))
		resp, _ := server.client.Do(req)
		resp.Body.Close()
	}

	// Pull with limit=2
	pullReq, _ := server.createAuthenticatedRequest("GET",
		server.baseURL+"/api/v1/sync/pull?peer_id=spoke-paginate&limit=2", nil)
	pullResp, err := server.client.Do(pullReq)
	if err != nil {
		t.Fatalf("failed to pull changes: %v", err)
	}
	defer pullResp.Body.Close()

	var result api.APIResponse
	json.NewDecoder(pullResp.Body).Decode(&result)
	data := result.Data.(map[string]interface{})
	changes := data["changes"].([]interface{})
	hasMore := data["has_more"].(bool)

	if len(changes) != 2 {
		t.Errorf("expected 2 changes with limit=2, got %d", len(changes))
	}

	if !hasMore {
		t.Error("expected has_more=true when more changes exist beyond limit")
	}

	// Pull again — should get more changes (the first 2 were marked as synced)
	pullReq2, _ := server.createAuthenticatedRequest("GET",
		server.baseURL+"/api/v1/sync/pull?peer_id=spoke-paginate&limit=2", nil)
	pullResp2, err := server.client.Do(pullReq2)
	if err != nil {
		t.Fatalf("failed to pull changes second time: %v", err)
	}
	defer pullResp2.Body.Close()

	var result2 api.APIResponse
	json.NewDecoder(pullResp2.Body).Decode(&result2)
	data2 := result2.Data.(map[string]interface{})
	changes2 := data2["changes"].([]interface{})

	if len(changes2) != 2 {
		t.Errorf("expected 2 more changes on second pull, got %d", len(changes2))
	}
}
