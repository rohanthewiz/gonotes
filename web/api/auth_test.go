package api_test

import (
	"net/http"
	"testing"
)

// TestAuthAPI tests the authentication endpoints: register, login, /me, refresh.
func TestAuthAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts := newTestServer(t)
	defer ts.cleanup()

	// ----------------------------------------------------------------
	// Register
	// ----------------------------------------------------------------

	t.Run("RegisterSuccess", func(t *testing.T) {
		input := map[string]string{
			"username": "authuser",
			"password": "securepass123",
		}

		status, resp := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusCreated {
			t.Fatalf("expected status %d, got %d – %v", http.StatusCreated, status, resp)
		}
		if resp["success"] != true {
			t.Errorf("expected success=true, got %v", resp["success"])
		}

		data, ok := resp["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected data map, got %v", resp["data"])
		}

		// Should return a token
		if data["token"] == nil || data["token"] == "" {
			t.Error("expected non-empty token in registration response")
		}
	})

	t.Run("RegisterDuplicateUsername", func(t *testing.T) {
		input := map[string]string{
			"username": "authuser",
			"password": "anotherpass123",
		}

		status, resp := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusConflict {
			t.Errorf("expected status %d, got %d – %v", http.StatusConflict, status, resp)
		}
	})

	t.Run("RegisterMissingUsername", func(t *testing.T) {
		input := map[string]string{
			"password": "securepass123",
		}

		status, _ := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	t.Run("RegisterMissingPassword", func(t *testing.T) {
		input := map[string]string{
			"username": "nopassuser",
		}

		status, _ := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	t.Run("RegisterShortPassword", func(t *testing.T) {
		input := map[string]string{
			"username": "shortpw",
			"password": "short",
		}

		status, _ := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	t.Run("RegisterShortUsername", func(t *testing.T) {
		input := map[string]string{
			"username": "ab",
			"password": "longpassword",
		}

		status, _ := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	t.Run("RegisterInvalidUsernameChars", func(t *testing.T) {
		input := map[string]string{
			"username": "user@name",
			"password": "longpassword",
		}

		status, _ := ts.request("POST", "/api/v1/auth/register", input)

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	// ----------------------------------------------------------------
	// Login
	// ----------------------------------------------------------------

	var loginToken string

	t.Run("LoginSuccess", func(t *testing.T) {
		input := map[string]string{
			"username": "authuser",
			"password": "securepass123",
		}

		status, resp := ts.request("POST", "/api/v1/auth/login", input)

		if status != http.StatusOK {
			t.Fatalf("expected status %d, got %d – %v", http.StatusOK, status, resp)
		}

		data := resp["data"].(map[string]interface{})
		token, ok := data["token"].(string)
		if !ok || token == "" {
			t.Error("expected non-empty token in login response")
		}
		loginToken = token
	})

	t.Run("LoginWrongPassword", func(t *testing.T) {
		input := map[string]string{
			"username": "authuser",
			"password": "wrongpassword",
		}

		status, _ := ts.request("POST", "/api/v1/auth/login", input)

		if status != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, status)
		}
	})

	t.Run("LoginNonExistentUser", func(t *testing.T) {
		input := map[string]string{
			"username": "nonexistent",
			"password": "somepassword",
		}

		status, _ := ts.request("POST", "/api/v1/auth/login", input)

		if status != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, status)
		}
	})

	t.Run("LoginMissingFields", func(t *testing.T) {
		status, _ := ts.request("POST", "/api/v1/auth/login", map[string]string{})

		if status != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, status)
		}
	})

	// ----------------------------------------------------------------
	// GetCurrentUser (/me)
	// ----------------------------------------------------------------

	t.Run("GetCurrentUser", func(t *testing.T) {
		// Temporarily swap the auth token to the one from login
		origToken := ts.authToken
		ts.authToken = loginToken
		defer func() { ts.authToken = origToken }()

		status, resp := ts.request("GET", "/api/v1/auth/me", nil)

		if status != http.StatusOK {
			t.Fatalf("expected status %d, got %d – %v", http.StatusOK, status, resp)
		}

		data := resp["data"].(map[string]interface{})
		if data["username"] != "authuser" {
			t.Errorf("expected username 'authuser', got %v", data["username"])
		}
	})

	t.Run("GetCurrentUserNoToken", func(t *testing.T) {
		origToken := ts.authToken
		ts.authToken = ""
		defer func() { ts.authToken = origToken }()

		status, _ := ts.request("GET", "/api/v1/auth/me", nil)

		if status != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, status)
		}
	})

	t.Run("GetCurrentUserBadToken", func(t *testing.T) {
		origToken := ts.authToken
		ts.authToken = "totally.invalid.token"
		defer func() { ts.authToken = origToken }()

		status, _ := ts.request("GET", "/api/v1/auth/me", nil)

		if status != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, status)
		}
	})

	// ----------------------------------------------------------------
	// Refresh Token
	// ----------------------------------------------------------------

	t.Run("RefreshTokenSuccess", func(t *testing.T) {
		origToken := ts.authToken
		ts.authToken = loginToken
		defer func() { ts.authToken = origToken }()

		status, resp := ts.request("POST", "/api/v1/auth/refresh", nil)

		if status != http.StatusOK {
			t.Fatalf("expected status %d, got %d – %v", http.StatusOK, status, resp)
		}

		data := resp["data"].(map[string]interface{})
		if data["token"] == nil || data["token"] == "" {
			t.Error("expected non-empty token in refresh response")
		}
	})

	t.Run("RefreshTokenNoAuth", func(t *testing.T) {
		origToken := ts.authToken
		ts.authToken = ""
		defer func() { ts.authToken = origToken }()

		status, _ := ts.request("POST", "/api/v1/auth/refresh", nil)

		if status != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, status)
		}
	})
}
