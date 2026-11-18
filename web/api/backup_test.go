package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gonotes/models"
)

// TestBackupAPI covers the two admin backup endpoints: an admin can take a
// backup and see it listed, a repeat with nothing changed is reported as
// unchanged, and an ordinary account is refused both. What a backup contains
// and how it rotates is tested in models.
func TestBackupAPI(t *testing.T) {
	ts := newTestServer(t)
	defer ts.cleanup()

	// The default directory is relative to the working directory, which under
	// `go test` is the package directory. Point it somewhere disposable.
	backupDir := filepath.Join(t.TempDir(), "backups")
	t.Setenv(models.BackupDirEnv, backupDir)
	t.Setenv(models.BackupKeepEnv, "5")

	status, _ := ts.request("POST", "/api/v1/notes", models.NoteInput{GUID: "backup-api-note", Title: "kept"})
	if status != http.StatusCreated {
		t.Fatalf("create note: status %d", status)
	}

	// The first registered user is the admin.
	status, body := ts.request("POST", "/api/v1/admin/backup", nil)
	if status != http.StatusOK {
		t.Fatalf("POST /admin/backup: status %d, body %v", status, body)
	}
	data := body["data"].(map[string]interface{})
	if data["unchanged"] == true {
		t.Error("first backup reported unchanged")
	}
	set := data["set"].(map[string]interface{})
	setPath, _ := set["path"].(string)
	for _, name := range []string{"notes_public.bytdb", "notes_private.bytdb", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(setPath, name)); err != nil {
			t.Errorf("backup set is missing %s: %v", name, err)
		}
	}

	status, body = ts.request("POST", "/api/v1/admin/backup", nil)
	if status != http.StatusOK {
		t.Fatalf("second POST /admin/backup: status %d", status)
	}
	if body["data"].(map[string]interface{})["unchanged"] != true {
		t.Error("a second backup with nothing changed was not reported as unchanged")
	}

	status, body = ts.request("GET", "/api/v1/admin/backups", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /admin/backups: status %d", status)
	}
	list := body["data"].(map[string]interface{})
	if got := len(list["backups"].([]interface{})); got != 1 {
		t.Errorf("listed %d backups, want 1", got)
	}
	if list["dir"] != backupDir || list["keep"] != float64(5) {
		t.Errorf("settings reported as dir=%v keep=%v", list["dir"], list["keep"])
	}

	// A second account is not an admin (registration needs the secret once a
	// user exists) and must be refused both endpoints.
	regBody, _ := json.Marshal(map[string]string{
		"username": "ordinary", "password": "testpassword123",
		"registration_secret": "test-reg-secret",
	})
	resp, err := http.Post(ts.baseURL+"/api/v1/auth/register", "application/json", bytes.NewBuffer(regBody))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("register second user: %v", err)
	}
	var reg map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	ts.authToken = reg["data"].(map[string]interface{})["token"].(string)

	if status, _ = ts.request("POST", "/api/v1/admin/backup", nil); status != http.StatusForbidden {
		t.Errorf("non-admin POST /admin/backup: status %d, want 403", status)
	}
	if status, _ = ts.request("GET", "/api/v1/admin/backups", nil); status != http.StatusForbidden {
		t.Errorf("non-admin GET /admin/backups: status %d, want 403", status)
	}
}
