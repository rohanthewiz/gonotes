package api_test

import (
	"net/http"
	"testing"
	"time"

	"gonotes/models"
)

// compactionCounts pulls data.compaction's before/after counts out of a
// sync-control response.
func compactionCounts(t *testing.T, resp map[string]interface{}) (before, after int) {
	t.Helper()
	data, _ := resp["data"].(map[string]interface{})
	c, _ := data["compaction"].(map[string]interface{})
	if c == nil {
		t.Fatalf("response has no data.compaction: %v", resp)
	}
	return int(c["changes_before"].(float64)), int(c["changes_after"].(float64))
}

// GET and POST /api/v1/sync/control/compact take ?note_guid= to scope the dry
// run and the compaction to one note (N-022). Other notes keep their pending
// changes, an unknown note is a 404, and without the parameter the endpoint
// still compacts the whole log.
func TestSyncControlCompactOneNote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()

	// A spoke: the compact endpoints need a sync client. Nothing here
	// contacts the hub, so its URL never has to answer.
	if _, err := models.NewSyncClient(&models.SyncConfig{
		Enabled: true, HubURL: "http://hub.invalid:8444", Username: "u", Password: "p",
		Interval: time.Minute, Mode: models.SyncModePrompt, PromptAfter: time.Hour,
	}); err != nil {
		t.Fatalf("NewSyncClient: %v", err)
	}
	t.Cleanup(models.ResetSyncClientForTest)

	// Two notes, three pending changes each (a create and two edits).
	for _, guid := range []string{"cn-target", "cn-other"} {
		id, _ := seedLockNote(t, ts, guid, guid)
		for _, title := range []string{"edit one", "edit two"} {
			if status, resp := ts.request("PUT", "/api/v1/notes/"+itoa(id), map[string]interface{}{
				"guid": guid, "title": title,
			}); status != http.StatusOK {
				t.Fatalf("editing %s returned %d: %v", guid, status, resp)
			}
		}
	}

	status, resp := ts.request("GET", "/api/v1/sync/control/compact?note_guid=cn-target", nil)
	if status != http.StatusOK {
		t.Fatalf("scoped preview returned %d: %v", status, resp)
	}
	if b, a := compactionCounts(t, resp); b != 3 || a != 1 {
		t.Errorf("scoped preview = %d → %d, want 3 → 1", b, a)
	}

	status, resp = ts.request("POST", "/api/v1/sync/control/compact?note_guid=cn-target", nil)
	if status != http.StatusOK {
		t.Fatalf("scoped compact returned %d: %v", status, resp)
	}
	if b, a := compactionCounts(t, resp); b != 3 || a != 1 {
		t.Errorf("scoped compact = %d → %d, want 3 → 1", b, a)
	}

	// The other note was left alone: the whole log is now its 3 plus the
	// target's 1.
	_, resp = ts.request("GET", "/api/v1/sync/control/compact", nil)
	if b, a := compactionCounts(t, resp); b != 4 || a != 2 {
		t.Errorf("whole-log preview after the scoped compact = %d → %d, want 4 → 2", b, a)
	}

	for _, method := range []string{"GET", "POST"} {
		if status, resp := ts.request(method, "/api/v1/sync/control/compact?note_guid=no-such-note", nil); status != http.StatusNotFound {
			t.Errorf("%s for an unknown note returned %d, want 404: %v", method, status, resp)
		}
	}

	// No parameter: the whole log, as before.
	status, resp = ts.request("POST", "/api/v1/sync/control/compact", nil)
	if status != http.StatusOK {
		t.Fatalf("whole-log compact returned %d: %v", status, resp)
	}
	if b, a := compactionCounts(t, resp); b != 4 || a != 2 {
		t.Errorf("whole-log compact = %d → %d, want 4 → 2", b, a)
	}
}
