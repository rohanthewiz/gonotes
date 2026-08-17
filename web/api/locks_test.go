package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gonotes/models"
	"gonotes/web/api"
)

// locks_test.go drives the lock protocol through the real HTTP stack — the
// arbiter as every non-local session actually reaches it.
//
// These tests care about STATUS CODES AND BODIES, not about the registry's
// internals (models/lock_test.go covers those). What is being pinned here is
// the contract a second GoNotes session, the web UI, or gn-clip.sh depends on:
// a 409 when somebody else has the note, a token that makes writes work, and a
// 409 again when the version has moved.

// requestWithLock is `request` plus the lock header, which is the only way to
// present a token on a PUT or a DELETE.
func (ts *testServer) requestWithLock(method, path, lockToken string, body interface{}) (int, map[string]interface{}) {
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
	if ts.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+ts.authToken)
	}
	if lockToken != "" {
		req.Header.Set(api.LockHeaderName, lockToken)
	}

	resp, err := ts.client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

// seedLockNote creates a note and returns its id and version.
func seedLockNote(t *testing.T, ts *testServer, guid, title string) (int64, int64) {
	t.Helper()
	status, resp := ts.request("POST", "/api/v1/notes", map[string]interface{}{
		"guid": guid, "title": title, "body": "original body",
	})
	if status != http.StatusCreated {
		t.Fatalf("seeding a note returned %d: %v", status, resp)
	}
	data := resp["data"].(map[string]interface{})
	return int64(data["id"].(float64)), int64(data["version"].(float64))
}

// acquire claims a note as the named session, returning the status and body.
func acquire(ts *testServer, noteID int64, sessionID string, steal bool) (int, map[string]interface{}) {
	path := "/api/v1/notes/" + itoa(noteID) + "/lock"
	if steal {
		path += "?steal=true"
	}
	return ts.request("POST", path, map[string]string{
		"session_id":  sessionID,
		"label":       "pane " + sessionID,
		"pane_handle": "w1:p" + sessionID,
		"client":      "tui",
	})
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestLockAPILifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	models.ResetNoteLocksForTest()
	ts := newTestServer(t)
	defer ts.cleanup()

	noteID, version := seedLockNote(t, ts, "lock-lifecycle", "Lockable note")

	// ---- acquire ----------------------------------------------------------
	status, resp := acquire(ts, noteID, "alpha", false)
	if status != http.StatusOK {
		t.Fatalf("acquiring an unheld note returned %d: %v", status, resp)
	}
	lock := resp["data"].(map[string]interface{})
	token, _ := lock["token"].(string)
	if token == "" {
		t.Fatal("the acquire response carries no token; nothing could write under this lease")
	}

	// ---- a second session is refused, and told who has it -----------------
	status, resp = acquire(ts, noteID, "beta", false)
	if status != http.StatusConflict {
		t.Fatalf("a second session got %d, want 409", status)
	}
	detail := resp["data"].(map[string]interface{})
	if detail["reason"] != "locked" {
		t.Fatalf("the 409 reports reason %v, want \"locked\"", detail["reason"])
	}
	blocking := detail["lock"].(map[string]interface{})
	holder := blocking["holder"].(map[string]interface{})
	if holder["label"] != "pane alpha" {
		t.Fatalf("the 409 names the holder as %v, want \"pane alpha\"", holder["label"])
	}
	// The blocked session must not be handed the very credential it was denied.
	if tok, ok := blocking["token"].(string); ok && tok != "" {
		t.Fatal("the 409 leaked the holder's lock token")
	}

	// ---- the holder's write goes through ----------------------------------
	status, resp = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID), token, map[string]interface{}{
		"guid": "lock-lifecycle", "title": "Edited by alpha", "expected_version": version,
	})
	if status != http.StatusOK {
		t.Fatalf("the lock holder's write returned %d: %v", status, resp)
	}

	// ---- everybody else's write does not -----------------------------------
	status, resp = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID), "", map[string]interface{}{
		"guid": "lock-lifecycle", "title": "Sneaky write",
	})
	if status != http.StatusConflict {
		t.Fatalf("a write with no lock token returned %d, want 409", status)
	}
	status, _ = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID), "lk_wrong", map[string]interface{}{
		"guid": "lock-lifecycle", "title": "Sneakier write",
	})
	if status != http.StatusConflict {
		t.Fatalf("a write with a foreign lock token returned %d, want 409", status)
	}

	// ...and nothing it tried actually landed.
	_, resp = ts.request("GET", "/api/v1/notes/"+itoa(noteID), nil)
	if title := resp["data"].(map[string]interface{})["title"]; title != "Edited by alpha" {
		t.Fatalf("a blocked write landed anyway: the note says %v", title)
	}

	// ---- renew --------------------------------------------------------------
	status, _ = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID)+"/lock", token, nil)
	if status != http.StatusOK {
		t.Fatalf("renewing a held lease returned %d, want 200", status)
	}
	status, _ = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID)+"/lock", "lk_wrong", nil)
	if status != http.StatusConflict {
		t.Fatalf("renewing with a foreign token returned %d, want 409", status)
	}

	// ---- read ---------------------------------------------------------------
	status, resp = ts.request("GET", "/api/v1/notes/"+itoa(noteID)+"/lock", nil)
	if status != http.StatusOK {
		t.Fatalf("reading a held lock returned %d, want 200", status)
	}
	if tok, ok := resp["data"].(map[string]interface{})["token"].(string); ok && tok != "" {
		t.Fatal("GET /lock leaked the token")
	}

	// ---- release ------------------------------------------------------------
	status, _ = ts.requestWithLock("DELETE", "/api/v1/notes/"+itoa(noteID)+"/lock", token, nil)
	if status != http.StatusOK {
		t.Fatalf("releasing returned %d, want 200", status)
	}
	status, _ = ts.request("GET", "/api/v1/notes/"+itoa(noteID)+"/lock", nil)
	if status != http.StatusNotFound {
		t.Fatalf("after a release the lock reads as %d, want 404 (nobody holds it)", status)
	}
	// And the next session can now have it.
	if status, resp = acquire(ts, noteID, "beta", false); status != http.StatusOK {
		t.Fatalf("after a release the next session got %d: %v", status, resp)
	}
}

func TestLockAPISteal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	models.ResetNoteLocksForTest()
	ts := newTestServer(t)
	defer ts.cleanup()

	noteID, _ := seedLockNote(t, ts, "lock-steal", "Contested note")

	_, resp := acquire(ts, noteID, "alpha", false)
	victimToken := resp["data"].(map[string]interface{})["token"].(string)

	status, resp := acquire(ts, noteID, "beta", true)
	if status != http.StatusOK {
		t.Fatalf("a steal returned %d: %v", status, resp)
	}
	stolen := resp["data"].(map[string]interface{})
	if stolen["stolen_from"] != "alpha" {
		t.Fatalf("the steal recorded stolen_from=%v, want \"alpha\"", stolen["stolen_from"])
	}

	// The victim's token is now worthless for both renewing and writing —
	// which is how the displaced session finds out at all.
	status, _ = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID)+"/lock", victimToken, nil)
	if status != http.StatusConflict {
		t.Fatalf("the displaced holder renewed successfully (%d); it would never learn it was robbed", status)
	}
	status, _ = ts.requestWithLock("PUT", "/api/v1/notes/"+itoa(noteID), victimToken, map[string]interface{}{
		"guid": "lock-steal", "title": "victim's save",
	})
	if status != http.StatusConflict {
		t.Fatalf("the displaced holder's write returned %d, want 409", status)
	}
}

// The version guard, over HTTP. Distinct from the lock: this fires even when
// nobody holds the note, which is the case the lock cannot cover.
func TestUpdateRefusesAStaleVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	models.ResetNoteLocksForTest()
	ts := newTestServer(t)
	defer ts.cleanup()

	noteID, version := seedLockNote(t, ts, "stale-write", "Shared note")

	// The first writer wins.
	status, _ := ts.request("PUT", "/api/v1/notes/"+itoa(noteID), map[string]interface{}{
		"guid": "stale-write", "title": "A's title", "expected_version": version,
	})
	if status != http.StatusOK {
		t.Fatalf("the first writer got %d, want 200", status)
	}

	// The second, against the same loaded version, is refused — with the
	// winning note attached so its UI can show what it lost to.
	status, resp := ts.request("PUT", "/api/v1/notes/"+itoa(noteID), map[string]interface{}{
		"guid": "stale-write", "title": "B's title", "expected_version": version,
	})
	if status != http.StatusConflict {
		t.Fatalf("the second writer got %d, want 409", status)
	}
	detail := resp["data"].(map[string]interface{})
	if detail["reason"] != "stale" {
		t.Fatalf("the 409 reports reason %v, want \"stale\"", detail["reason"])
	}
	current := detail["current"].(map[string]interface{})
	if current["title"] != "A's title" {
		t.Fatalf("the 409 reports the stored title as %v, want \"A's title\"", current["title"])
	}

	// A write with no expected_version is unguarded and still lands — the
	// importer, sync, and older clients all depend on that staying true.
	status, _ = ts.request("PUT", "/api/v1/notes/"+itoa(noteID), map[string]interface{}{
		"guid": "stale-write", "title": "unguarded",
	})
	if status != http.StatusOK {
		t.Fatalf("an unguarded write got %d, want 200", status)
	}
}

// A lock must not become an oracle for note ids belonging to other people: the
// answer for a note you do not own is the same 404 GET gives.
func TestLockOnAMissingNoteIs404(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	models.ResetNoteLocksForTest()
	ts := newTestServer(t)
	defer ts.cleanup()

	if status, _ := acquire(ts, 999999, "alpha", false); status != http.StatusNotFound {
		t.Fatalf("locking a note that does not exist returned %d, want 404", status)
	}
}

func TestListNoteLocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	models.ResetNoteLocksForTest()
	ts := newTestServer(t)
	defer ts.cleanup()

	one, _ := seedLockNote(t, ts, "bulk-lock-1", "First")
	two, _ := seedLockNote(t, ts, "bulk-lock-2", "Second")
	seedLockNote(t, ts, "bulk-lock-3", "Third") // deliberately unlocked

	acquire(ts, one, "alpha", false)
	acquire(ts, two, "beta", false)

	status, resp := ts.request("GET", "/api/v1/note-locks", nil)
	if status != http.StatusOK {
		t.Fatalf("listing locks returned %d, want 200", status)
	}
	locks := resp["data"].([]interface{})
	if len(locks) != 2 {
		t.Fatalf("the listing has %d leases, want 2 (the third note is unlocked)", len(locks))
	}
	for _, l := range locks {
		if tok, ok := l.(map[string]interface{})["token"].(string); ok && tok != "" {
			t.Fatal("the bulk listing leaked a lock token")
		}
	}
}

// Deleting a note releases its lease. Otherwise the registry would hold a lock
// on something that no longer exists until the TTL ran out.
func TestDeleteReleasesTheLock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	models.ResetNoteLocksForTest()
	ts := newTestServer(t)
	defer ts.cleanup()

	noteID, _ := seedLockNote(t, ts, "delete-releases", "Doomed note")

	_, resp := acquire(ts, noteID, "alpha", false)
	token := resp["data"].(map[string]interface{})["token"].(string)

	// A non-holder cannot delete it out from under the holder.
	status, _ := ts.requestWithLock("DELETE", "/api/v1/notes/"+itoa(noteID), "", nil)
	if status != http.StatusConflict {
		t.Fatalf("a non-holder's delete returned %d, want 409", status)
	}

	// The holder can, and the lease goes with it.
	status, _ = ts.requestWithLock("DELETE", "/api/v1/notes/"+itoa(noteID), token, nil)
	if status != http.StatusOK {
		t.Fatalf("the holder's delete returned %d, want 200", status)
	}
	if models.GetNoteLock(noteID) != nil {
		t.Fatal("the lease outlived the note it was protecting")
	}
}
