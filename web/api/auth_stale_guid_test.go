package api_test

import (
	"net/http"
	"testing"

	"gonotes/models"
)

// Stale-GUID tests (N-051). A token's signature proves it was issued, not
// that its account still exists. After `gonotes account merge` (or a
// hub-identity reconcile that re-keys the local account), a session opened
// earlier used to keep acting as the vanished GUID: its note list came back
// 200 and empty, and a note it created was owned by a GUID no account holds.
// JWTAuthMiddleware now looks the account up, so the old token stops
// authenticating and the client is sent back to sign in.

func TestTokenForMergedAwayAccountNoLongerAuthenticates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()

	if status, _ := ts.request("GET", "/api/v1/notes", nil); status != http.StatusOK {
		t.Fatalf("baseline: GET /notes = %d, want 200", status)
	}

	// The account the session's token names is folded into another one and
	// deleted, as `gonotes account merge --from notetest --into survivor`
	// would do.
	if _, err := models.CreateUser(models.UserRegisterInput{
		Username: "survivor", Password: "testpassword123",
	}); err != nil {
		t.Fatalf("create survivor: %v", err)
	}
	if _, err := models.MergeLocalUsers("notetest", "survivor"); err != nil {
		t.Fatalf("merge: %v", err)
	}

	// Reads and writes are both refused, rather than an empty list and an
	// orphaned note.
	if status, _ := ts.request("GET", "/api/v1/notes", nil); status != http.StatusUnauthorized {
		t.Errorf("GET /notes with the merged-away token = %d, want 401", status)
	}
	status, _ := ts.request("POST", "/api/v1/notes", map[string]any{"title": "orphan?"})
	if status != http.StatusUnauthorized {
		t.Errorf("POST /notes with the merged-away token = %d, want 401", status)
	}

	// Signing in again gets a token for the surviving account, which works.
	ts.authToken = ""
	status, resp := ts.request("POST", "/api/v1/auth/login", map[string]string{
		"username": "survivor", "password": "testpassword123",
	})
	if status != http.StatusOK {
		t.Fatalf("login as survivor = %d: %v", status, resp)
	}
	ts.authToken = resp["data"].(map[string]any)["token"].(string)
	if status, _ := ts.request("GET", "/api/v1/notes", nil); status != http.StatusOK {
		t.Errorf("GET /notes after signing in again = %d, want 200", status)
	}
}

// A rename keeps the GUID, so the per-request lookup still finds the account
// and the session survives it, now under the new name.
func TestTokenSurvivesRenameAndReportsCurrentName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()

	if _, err := models.RenameLocalUser("notetest", "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	status, resp := ts.request("GET", "/api/v1/auth/me", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /auth/me after rename = %d, want 200", status)
	}
	if got := resp["data"].(map[string]any)["username"]; got != "renamed" {
		t.Errorf("/auth/me username = %v, want renamed", got)
	}
}
