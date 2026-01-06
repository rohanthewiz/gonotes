package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"gonotes/models"
	"gonotes/web"
)

// testServer manages a running server instance for integration testing.
// This approach tests the full HTTP stack including middleware.
type testServer struct {
	baseURL string
	client  *http.Client
}

// newTestServer creates a test server with a fresh database on a random port.
// The server runs in a goroutine and should be cleaned up after tests.
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	// Remove existing test database to ensure clean state
	os.Remove("./data/test_notes.ddb")
	os.Remove("./data/test_notes.ddb.wal")

	// Initialize database with test path
	if err := models.InitTestDB("./data/test_notes.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Create and start server on a test port
	srv := web.NewServer()

	// Start server in background goroutine
	go func() {
		srv.Run()
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	return &testServer{
		baseURL: "http://localhost:8000",
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// cleanup stops the server and removes test database
func (ts *testServer) cleanup() {
	models.CloseDB()
	os.Remove("./data/test_notes.ddb")
	os.Remove("./data/test_notes.ddb.wal")
}

// request makes an HTTP request and returns status code and parsed JSON response
func (ts *testServer) request(method, path string, body interface{}) (int, map[string]interface{}) {
	var reqBody *bytes.Buffer
	if body != nil {
		jsonBody, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(jsonBody)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	req, err := http.NewRequest(method, ts.baseURL+path, reqBody)
	if err != nil {
		return 0, nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	return resp.StatusCode, result
}

func TestNotesAPI(t *testing.T) {
	// Skip if running in short mode (CI without network)
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts := newTestServer(t)
	defer ts.cleanup()

	// Test 1: List notes (empty)
	t.Run("ListNotesEmpty", func(t *testing.T) {
		status, resp := ts.request("GET", "/api/v1/notes", nil)

		if status != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status)
		}

		if resp["success"] != true {
			t.Errorf("expected success=true, got %v", resp["success"])
		}

		data, ok := resp["data"].([]interface{})
		if !ok || len(data) != 0 {
			t.Errorf("expected empty array, got %v", resp["data"])
		}
	})

	// Test 2: Create note
	var createdNoteID float64
	t.Run("CreateNote", func(t *testing.T) {
		input := map[string]interface{}{
			"guid":        "test-note-001",
			"title":       "Test Note",
			"description": "A test note",
			"body":        "Test body content",
			"tags":        "test,api",
		}

		status, resp := ts.request("POST", "/api/v1/notes", input)

		if status != http.StatusCreated {
			t.Errorf("expected status %d, got %d", http.StatusCreated, status)
		}

		if resp["success"] != true {
			t.Errorf("expected success=true, got %v", resp["success"])
		}

		data, ok := resp["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected data object, got %v", resp["data"])
		}

		if data["title"] != "Test Note" {
			t.Errorf("expected title 'Test Note', got %v", data["title"])
		}
		if data["guid"] != "test-note-001" {
			t.Errorf("expected guid 'test-note-001', got %v", data["guid"])
		}

		createdNoteID = data["id"].(float64)
	})

	// Test 3: Get note by ID
	t.Run("GetNote", func(t *testing.T) {
		status, resp := ts.request("GET", fmt.Sprintf("/api/v1/notes/%.0f", createdNoteID), nil)

		if status != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status)
		}

		data := resp["data"].(map[string]interface{})
		if data["id"].(float64) != createdNoteID {
			t.Errorf("expected id %v, got %v", createdNoteID, data["id"])
		}
	})

	// Test 4: Update note
	t.Run("UpdateNote", func(t *testing.T) {
		input := map[string]interface{}{
			"title":      "Updated Test Note",
			"body":       "Updated body content",
			"is_private": true,
		}

		status, resp := ts.request("PUT", fmt.Sprintf("/api/v1/notes/%.0f", createdNoteID), input)

		if status != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status)
		}

		data := resp["data"].(map[string]interface{})
		if data["title"] != "Updated Test Note" {
			t.Errorf("expected title 'Updated Test Note', got %v", data["title"])
		}
		if data["is_private"] != true {
			t.Errorf("expected is_private=true, got %v", data["is_private"])
		}
	})

	// Test 5: Create second note for delete test
	var secondNoteID float64
	t.Run("CreateSecondNote", func(t *testing.T) {
		input := map[string]interface{}{
			"guid":  "test-note-002",
			"title": "Second Note",
		}

		status, resp := ts.request("POST", "/api/v1/notes", input)
		if status != http.StatusCreated {
			t.Errorf("expected status %d, got %d", http.StatusCreated, status)
		}
		data := resp["data"].(map[string]interface{})
		secondNoteID = data["id"].(float64)
	})

	// Test 6: Delete note
	t.Run("DeleteNote", func(t *testing.T) {
		status, resp := ts.request("DELETE", fmt.Sprintf("/api/v1/notes/%.0f", secondNoteID), nil)

		if status != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status)
		}

		data := resp["data"].(map[string]interface{})
		if data["deleted"] != true {
			t.Errorf("expected deleted=true, got %v", data["deleted"])
		}
	})

	// Test 7: Get deleted note should return 404
	t.Run("GetDeletedNote", func(t *testing.T) {
		status, _ := ts.request("GET", fmt.Sprintf("/api/v1/notes/%.0f", secondNoteID), nil)

		if status != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, status)
		}
	})

	// Test 8: List should show only non-deleted notes
	t.Run("ListAfterDelete", func(t *testing.T) {
		status, resp := ts.request("GET", "/api/v1/notes", nil)

		if status != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status)
		}

		data := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Errorf("expected 1 note, got %d", len(data))
		}
	})

	// Test 9: Duplicate GUID should fail
	t.Run("DuplicateGUID", func(t *testing.T) {
		input := map[string]interface{}{
			"guid":  "test-note-001",
			"title": "Duplicate GUID Note",
		}

		status, resp := ts.request("POST", "/api/v1/notes", input)

		if status != http.StatusConflict {
			t.Errorf("expected status %d, got %d", http.StatusConflict, status)
		}

		if resp["success"] != false {
			t.Errorf("expected success=false, got %v", resp["success"])
		}
	})

	// Test 10: Missing required fields
	t.Run("MissingTitle", func(t *testing.T) {
		input := map[string]interface{}{
			"guid": "test-note-no-title",
		}

		status, _ := ts.request("POST", "/api/v1/notes", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	t.Run("MissingGUID", func(t *testing.T) {
		input := map[string]interface{}{
			"title": "Note without GUID",
		}

		status, _ := ts.request("POST", "/api/v1/notes", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	// Test 11: Get non-existent note
	t.Run("GetNonExistent", func(t *testing.T) {
		status, _ := ts.request("GET", "/api/v1/notes/999", nil)

		if status != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, status)
		}
	})

	// Test 12: Update non-existent note
	t.Run("UpdateNonExistent", func(t *testing.T) {
		input := map[string]interface{}{
			"title": "Update Non-Existent",
		}

		status, _ := ts.request("PUT", "/api/v1/notes/999", input)

		if status != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, status)
		}
	})

	// Test 13: Delete non-existent note
	t.Run("DeleteNonExistent", func(t *testing.T) {
		status, _ := ts.request("DELETE", "/api/v1/notes/999", nil)

		if status != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, status)
		}
	})

	// Test 14: Invalid note ID
	t.Run("InvalidNoteID", func(t *testing.T) {
		status, _ := ts.request("GET", "/api/v1/notes/invalid", nil)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	// Test 15: Pagination
	t.Run("Pagination", func(t *testing.T) {
		// Create a few more notes
		for i := 3; i <= 5; i++ {
			input := map[string]interface{}{
				"guid":  fmt.Sprintf("test-note-00%d", i),
				"title": fmt.Sprintf("Note %d", i),
			}
			ts.request("POST", "/api/v1/notes", input)
		}

		// Test limit
		status, resp := ts.request("GET", "/api/v1/notes?limit=2", nil)
		if status != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status)
		}
		data := resp["data"].([]interface{})
		if len(data) != 2 {
			t.Errorf("expected 2 notes with limit=2, got %d", len(data))
		}

		// Test offset
		status2, resp2 := ts.request("GET", "/api/v1/notes?limit=2&offset=2", nil)
		if status2 != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, status2)
		}
		data2 := resp2["data"].([]interface{})
		if len(data2) != 2 {
			t.Errorf("expected 2 notes with limit=2&offset=2, got %d", len(data2))
		}

		// Ensure offset returns different notes
		firstSet := data[0].(map[string]interface{})["id"]
		secondSet := data2[0].(map[string]interface{})["id"]
		if firstSet == secondSet {
			t.Errorf("offset should return different notes")
		}
	})
}
