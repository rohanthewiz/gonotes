package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonotes/models"
	"gonotes/web"

	"github.com/rohanthewiz/rweb"
)

// spokeConfigBody is a minimal valid /setup/apply payload.
func spokeConfigBody(overrides map[string]string) []byte {
	cfg := map[string]string{
		"hub_url": "https://hub.example.com", "username": "me", "password_b64": "cGFzcw==",
	}
	for k, v := range overrides {
		cfg[k] = v
	}
	b, _ := json.Marshal(cfg)
	return b
}

// postSetup sends a config to /api/v1/setup/apply, with a bearer token when
// one is given, and returns the status.
func postSetup(t *testing.T, baseURL, token string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+"/api/v1/setup/apply", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /setup/apply: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// setupEnvIsolated points the handler's relative .env path at a temp tree and
// clears the one variable its first guard reads.
func setupEnvIsolated(t *testing.T) (envPath string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("GONOTES_SYNC_ENABLED", "")
	return filepath.Join(dir, "config", "cfg_files", ".env")
}

// Once the server has accounts, applying a spoke config takes an admin.
// Before this, "sync is not enabled" was the only check, which is true of
// every hub and standalone server: anyone who could reach the port could
// rewrite its .env.
func TestSetupApplyRequiresAnAdminOnceAccountsExist(t *testing.T) {
	ts := newTestServer(t) // registers the first user, who is the admin
	defer ts.cleanup()
	envPath := setupEnvIsolated(t)

	if status := postSetup(t, ts.baseURL, "", spokeConfigBody(nil)); status != http.StatusForbidden {
		t.Errorf("unauthenticated apply on a server with accounts: status %d, want 403", status)
	}

	regBody, _ := json.Marshal(map[string]string{
		"username": "ordinary", "password": "testpassword123", "registration_secret": "test-reg-secret",
	})
	resp, err := http.Post(ts.baseURL+"/api/v1/auth/register", "application/json", bytes.NewBuffer(regBody))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("register second user: %v", err)
	}
	var reg map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	ordinary := reg["data"].(map[string]interface{})["token"].(string)

	if status := postSetup(t, ts.baseURL, ordinary, spokeConfigBody(nil)); status != http.StatusForbidden {
		t.Errorf("non-admin apply: status %d, want 403", status)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatal("a refused apply wrote the env file")
	}

	// A line break in a value would become a second line of the file: an
	// environment variable of the sender's choosing.
	injected := spokeConfigBody(map[string]string{"hub_url": "https://hub.example.com\nGONOTES_JWT_SECRET=attacker"})
	if status := postSetup(t, ts.baseURL, ts.authToken, injected); status != http.StatusBadRequest {
		t.Errorf("multi-line value: status %d, want 400", status)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatal("a rejected multi-line value wrote the env file")
	}

	if status := postSetup(t, ts.baseURL, ts.authToken, spokeConfigBody(nil)); status != http.StatusOK {
		t.Fatalf("admin apply: status %d, want 200", status)
	}
	written, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("admin apply wrote no env file: %v", err)
	}
	if !strings.Contains(string(written), "GONOTES_SYNC_HUB_URL=https://hub.example.com\n") {
		t.Errorf("env file lacks the hub URL:\n%s", written)
	}
}

// With no accounts at all, setup still needs no sign-in: that is the first-run
// case the endpoint exists for, and there is nobody yet who could sign in.
func TestSetupApplyIsOpenOnFirstRun(t *testing.T) {
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	defer models.CloseDB()
	t.Setenv("GONOTES_JWT_SECRET", "test-secret-key-for-jwt-testing-32chars")
	if err := models.InitJWT(); err != nil {
		t.Fatalf("InitJWT: %v", err)
	}
	ready := make(chan struct{}, 1)
	srv := web.NewTestServer(rweb.ServerOptions{ReadyChan: ready, Address: "localhost:"})
	go func() { _ = srv.Run() }()
	<-ready
	baseURL := fmt.Sprintf("http://localhost:%s", srv.GetListenPort())

	envPath := setupEnvIsolated(t)
	if status := postSetup(t, baseURL, "", spokeConfigBody(nil)); status != http.StatusOK {
		t.Fatalf("first-run apply with no accounts: status %d, want 200", status)
	}
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("first-run apply wrote no env file: %v", err)
	}
}
