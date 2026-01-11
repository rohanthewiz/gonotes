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

// categoryTestServer manages a running server instance for integration testing.
// Uses ReadyChan pattern for proper server ready signaling (no arbitrary sleep).
type categoryTestServer struct {
	baseURL string
	client  *http.Client
	server  *rweb.Server
}

// setupCategoryTestServer creates a test server with a fresh database.
// Uses the rweb ReadyChan pattern for reliable server startup detection.
func setupCategoryTestServer(t *testing.T) (*categoryTestServer, func()) {
	t.Helper()

	// Remove existing test database
	os.Remove("./data/test_categories.ddb")
	os.Remove("./data/test_categories.ddb.wal")

	// Initialize test database
	if err := models.InitTestDB("./data/test_categories.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Create ready channel for server startup signaling
	readyChan := make(chan struct{}, 1)

	// Create test server with dynamic port and ready channel
	srv := web.NewTestServer(rweb.ServerOptions{
		Verbose:   true,
		ReadyChan: readyChan,
		Address:   "localhost:", // Dynamic port assignment
	})

	// Start server in background
	go func() {
		_ = srv.Run()
	}()

	// Wait for server to be ready
	<-readyChan

	testServer := &categoryTestServer{
		baseURL: fmt.Sprintf("http://localhost:%s", srv.GetListenPort()),
		client:  &http.Client{Timeout: 5 * time.Second},
		server:  srv,
	}

	// Return cleanup function
	cleanup := func() {
		models.CloseDB()
		os.Remove("./data/test_categories.ddb")
		os.Remove("./data/test_categories.ddb.wal")
	}

	return testServer, cleanup
}

// TestCategoryAPI tests the category CRUD endpoints
func TestCategoryAPI(t *testing.T) {
	server, cleanup := setupCategoryTestServer(t)
	defer cleanup()

	var categoryID int64

	t.Run("list empty categories", func(t *testing.T) {
		resp, err := http.Get(server.baseURL + "/api/v1/categories")
		if err != nil {
			t.Fatalf("failed to get categories: %v", err)
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

		categories, ok := result.Data.([]interface{})
		if !ok {
			t.Fatal("expected data to be an array")
		}

		if len(categories) != 0 {
			t.Errorf("expected 0 categories, got %d", len(categories))
		}
	})

	t.Run("create category", func(t *testing.T) {
		desc := "Test category for API testing"
		input := models.CategoryInput{
			Name:          "API Test Category",
			Description:   &desc,
			Subcategories: []string{"Subcat A", "Subcat B"},
		}

		body, _ := json.Marshal(input)
		resp, err := http.Post(server.baseURL+"/api/v1/categories", "application/json", bytes.NewBuffer(body))
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Errorf("expected status 201, got %d", resp.StatusCode)
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

		if name, ok := data["name"].(string); !ok || name != "API Test Category" {
			t.Errorf("expected name 'API Test Category', got %v", data["name"])
		}

		if id, ok := data["id"].(float64); ok {
			categoryID = int64(id)
		} else {
			t.Fatal("expected id to be a number")
		}

		if subcats, ok := data["subcategories"].([]interface{}); !ok || len(subcats) != 2 {
			t.Errorf("expected 2 subcategories, got %v", data["subcategories"])
		}
	})

	t.Run("get category by id", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/categories/%d", server.baseURL, categoryID))
		if err != nil {
			t.Fatalf("failed to get category: %v", err)
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

		if name, ok := data["name"].(string); !ok || name != "API Test Category" {
			t.Errorf("expected name 'API Test Category', got %v", data["name"])
		}
	})

	t.Run("update category", func(t *testing.T) {
		newDesc := "Updated description"
		input := models.CategoryInput{
			Name:          "Updated Category Name",
			Description:   &newDesc,
			Subcategories: []string{"New1", "New2", "New3"},
		}

		body, _ := json.Marshal(input)
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/categories/%d", server.baseURL, categoryID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to update category: %v", err)
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

		if name, ok := data["name"].(string); !ok || name != "Updated Category Name" {
			t.Errorf("expected name 'Updated Category Name', got %v", data["name"])
		}

		if subcats, ok := data["subcategories"].([]interface{}); !ok || len(subcats) != 3 {
			t.Errorf("expected 3 subcategories, got %v", data["subcategories"])
		}
	})

	t.Run("list categories", func(t *testing.T) {
		resp, err := http.Get(server.baseURL + "/api/v1/categories")
		if err != nil {
			t.Fatalf("failed to get categories: %v", err)
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

		categories, ok := result.Data.([]interface{})
		if !ok {
			t.Fatal("expected data to be an array")
		}

		if len(categories) != 1 {
			t.Errorf("expected 1 category, got %d", len(categories))
		}
	})

	t.Run("delete category", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/categories/%d", server.baseURL, categoryID), nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to delete category: %v", err)
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
	})

	t.Run("get deleted category returns 404", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/categories/%d", server.baseURL, categoryID))
		if err != nil {
			t.Fatalf("failed to get category: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("create category without name", func(t *testing.T) {
		input := models.CategoryInput{
			Name: "",
		}

		body, _ := json.Marshal(input)
		resp, err := http.Post(server.baseURL+"/api/v1/categories", "application/json", bytes.NewBuffer(body))
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})

	t.Run("get non-existent category", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/categories/99999", server.baseURL))
		if err != nil {
			t.Fatalf("failed to get category: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("update non-existent category", func(t *testing.T) {
		input := models.CategoryInput{
			Name: "Does Not Exist",
		}

		body, _ := json.Marshal(input)
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/categories/99999", server.baseURL), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to update category: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("delete non-existent category", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/categories/99999", server.baseURL), nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to delete category: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		// Create several categories
		for i := 1; i <= 5; i++ {
			input := models.CategoryInput{
				Name: fmt.Sprintf("Pagination Test %d", i),
			}
			body, _ := json.Marshal(input)
			resp, err := http.Post(server.baseURL+"/api/v1/categories", "application/json", bytes.NewBuffer(body))
			if err != nil {
				t.Fatalf("failed to create category: %v", err)
			}
			resp.Body.Close()
		}

		// Test limit
		resp, err := http.Get(server.baseURL + "/api/v1/categories?limit=2")
		if err != nil {
			t.Fatalf("failed to get categories: %v", err)
		}
		defer resp.Body.Close()

		var result api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		categories, ok := result.Data.([]interface{})
		if !ok {
			t.Fatal("expected data to be an array")
		}

		if len(categories) != 2 {
			t.Errorf("expected 2 categories with limit=2, got %d", len(categories))
		}

		// Test offset
		resp2, err := http.Get(server.baseURL + "/api/v1/categories?limit=2&offset=2")
		if err != nil {
			t.Fatalf("failed to get categories: %v", err)
		}
		defer resp2.Body.Close()

		var result2 api.APIResponse
		if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		categories2, ok := result2.Data.([]interface{})
		if !ok {
			t.Fatal("expected data to be an array")
		}

		if len(categories2) != 2 {
			t.Errorf("expected 2 categories with offset=2, got %d", len(categories2))
		}
	})

	t.Run("invalid category id", func(t *testing.T) {
		resp, err := http.Get(server.baseURL + "/api/v1/categories/invalid")
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})
}

// TestNoteCategoryRelationshipAPI tests the note-category relationship endpoints
func TestNoteCategoryRelationshipAPI(t *testing.T) {
	server, cleanup := setupCategoryTestServer(t)
	defer cleanup()

	var noteID, categoryID1, categoryID2 int64

	t.Run("setup: create note and categories", func(t *testing.T) {
		// Create a note
		noteInput := models.NoteInput{
			GUID:  "test-note-rel",
			Title: "Test Note for Relationships",
		}
		body, _ := json.Marshal(noteInput)
		resp, err := http.Post(server.baseURL+"/api/v1/notes", "application/json", bytes.NewBuffer(body))
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}
		defer resp.Body.Close()

		var result api.APIResponse
		json.NewDecoder(resp.Body).Decode(&result)
		data := result.Data.(map[string]interface{})
		noteID = int64(data["id"].(float64))

		// Create categories
		for i := 1; i <= 2; i++ {
			catInput := models.CategoryInput{
				Name: fmt.Sprintf("Relationship Category %d", i),
			}
			body, _ := json.Marshal(catInput)
			resp, err := http.Post(server.baseURL+"/api/v1/categories", "application/json", bytes.NewBuffer(body))
			if err != nil {
				t.Fatalf("failed to create category: %v", err)
			}

			var catResult api.APIResponse
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			json.Unmarshal(bodyBytes, &catResult)
			catData := catResult.Data.(map[string]interface{})
			catID := int64(catData["id"].(float64))

			if i == 1 {
				categoryID1 = catID
			} else {
				categoryID2 = catID
			}
		}
	})

	t.Run("add category to note", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/%d/categories/%d", server.baseURL, noteID, categoryID1), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to add category to note: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Errorf("expected status 201, got %d", resp.StatusCode)
		}

		var result api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if !result.Success {
			t.Error("expected success to be true")
		}
	})

	t.Run("get note categories", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/notes/%d/categories", server.baseURL, noteID))
		if err != nil {
			t.Fatalf("failed to get note categories: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var result api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		categories, ok := result.Data.([]interface{})
		if !ok {
			t.Fatal("expected data to be an array")
		}

		if len(categories) != 1 {
			t.Errorf("expected 1 category, got %d", len(categories))
		}
	})

	t.Run("add second category to note", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/%d/categories/%d", server.baseURL, noteID, categoryID2), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to add category to note: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Errorf("expected status 201, got %d", resp.StatusCode)
		}

		// Verify we now have 2 categories
		resp2, err := http.Get(fmt.Sprintf("%s/api/v1/notes/%d/categories", server.baseURL, noteID))
		if err != nil {
			t.Fatalf("failed to get note categories: %v", err)
		}
		defer resp2.Body.Close()

		var result api.APIResponse
		json.NewDecoder(resp2.Body).Decode(&result)
		categories := result.Data.([]interface{})

		if len(categories) != 2 {
			t.Errorf("expected 2 categories, got %d", len(categories))
		}
	})

	t.Run("add duplicate category", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/%d/categories/%d", server.baseURL, noteID, categoryID1), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Errorf("expected status 409, got %d", resp.StatusCode)
		}
	})

	t.Run("get category notes", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/categories/%d/notes", server.baseURL, categoryID1))
		if err != nil {
			t.Fatalf("failed to get category notes: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var result api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		notes, ok := result.Data.([]interface{})
		if !ok {
			t.Fatal("expected data to be an array")
		}

		if len(notes) != 1 {
			t.Errorf("expected 1 note, got %d", len(notes))
		}
	})

	t.Run("remove category from note", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/notes/%d/categories/%d", server.baseURL, noteID, categoryID1), nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to remove category from note: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		// Verify we now have 1 category
		resp2, err := http.Get(fmt.Sprintf("%s/api/v1/notes/%d/categories", server.baseURL, noteID))
		if err != nil {
			t.Fatalf("failed to get note categories: %v", err)
		}
		defer resp2.Body.Close()

		var result api.APIResponse
		json.NewDecoder(resp2.Body).Decode(&result)
		categories := result.Data.([]interface{})

		if len(categories) != 1 {
			t.Errorf("expected 1 category after removal, got %d", len(categories))
		}
	})

	t.Run("remove non-existent relationship", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/notes/%d/categories/%d", server.baseURL, noteID, categoryID1), nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("add category to non-existent note", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/99999/categories/%d", server.baseURL, categoryID1), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("add non-existent category to note", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/%d/categories/99999", server.baseURL, noteID), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("invalid note id in relationship", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/invalid/categories/%d", server.baseURL, categoryID1), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})

	t.Run("invalid category id in relationship", func(t *testing.T) {
		resp, err := http.Post(fmt.Sprintf("%s/api/v1/notes/%d/categories/invalid", server.baseURL, noteID), "application/json", nil)
		if err != nil {
			t.Fatalf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})
}
