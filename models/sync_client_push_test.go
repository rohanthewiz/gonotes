package models

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The desktop spoke's push (pushChanges), against a scripted hub. As with the
// pull tests, hub and spoke cannot share a process because the models package
// has one database, so the hub here only records what it was sent and answers
// from a script.

// scriptedPushHub accepts every pushed change except those whose entity GUID
// is in reject, and records each push batch. It can also answer the first
// push with 401, to drive the re-login path.
type scriptedPushHub struct {
	mu          sync.Mutex
	batches     [][]SyncChange // one entry per accepted POST /sync/push
	emptyBodies int            // pushes that arrived with no changes at all
	reject      map[string]string
	expireFirst bool // answer the first push 401, as an expired token would get
	logins      int
}

func (h *scriptedPushHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch r.URL.Path {
	case "/api/v1/auth/login":
		h.logins++
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
			"token": "fresh-token",
			"user":  map[string]string{"guid": "hub-user", "username": "me"},
		}})

	case "/api/v1/sync/push":
		raw, _ := io.ReadAll(r.Body)
		if h.expireFirst {
			h.expireFirst = false
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "token expired"})
			return
		}
		var req SyncPushRequest
		_ = json.Unmarshal(raw, &req)
		if len(req.Changes) == 0 {
			h.emptyBodies++
		}
		h.batches = append(h.batches, req.Changes)

		resp := SyncPushResponse{Accepted: []string{}, Rejected: []SyncPushRejection{}}
		for _, ch := range req.Changes {
			if reason, refused := h.reject[ch.EntityGUID]; refused {
				resp.Rejected = append(resp.Rejected, SyncPushRejection{GUID: ch.GUID, Reason: reason})
				continue
			}
			resp.Accepted = append(resp.Accepted, ch.GUID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": resp})

	default:
		http.NotFound(w, r)
	}
}

// pushTestClient points a spoke at a scripted push hub over a fresh local
// database holding n notes, each of which is one pending change.
func pushTestClient(t *testing.T, hub *scriptedPushHub, n int) *SyncClient {
	t.Helper()
	if err := InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("init test db: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
	for i := range n {
		body := "body"
		guid := fmt.Sprintf("push-note-%03d", i)
		if _, err := CreateNote(NoteInput{GUID: guid, Title: guid, Body: &body}, "push-user"); err != nil {
			t.Fatalf("CreateNote %d: %v", i, err)
		}
	}
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	return &SyncClient{
		config:     &SyncConfig{HubURL: srv.URL, Username: "me", Password: "pw"},
		peerID:     "desk-spoke",
		authToken:  "token",
		httpClient: srv.Client(),
	}
}

func pendingForHub(t *testing.T, sc *SyncClient) int {
	t.Helper()
	n, err := CountUnsentChangesForPeer(sc.peerID, "")
	if err != nil {
		t.Fatalf("CountUnsentChangesForPeer: %v", err)
	}
	return n
}

// One cycle's push sends the whole backlog. It used to send the first 100 and
// report the cycle complete, leaving the rest for however many further syncs
// someone remembered to ask for.
func TestPushSendsTheWholeBacklogInBatches(t *testing.T) {
	const notes = 2*pushBatchSize + 37
	hub := &scriptedPushHub{}
	sc := pushTestClient(t, hub, notes)

	if got := pendingForHub(t, sc); got != notes {
		t.Fatalf("setup: %d changes pending, want %d", got, notes)
	}
	if err := sc.pushChanges(t.Context()); err != nil {
		t.Fatalf("pushChanges: %v", err)
	}

	var sizes []int
	seen := map[string]int{}
	for _, b := range hub.batches {
		sizes = append(sizes, len(b))
		for _, ch := range b {
			seen[ch.EntityGUID]++
		}
	}
	if len(sizes) != 3 || sizes[0] != pushBatchSize || sizes[1] != pushBatchSize || sizes[2] != 37 {
		t.Errorf("batch sizes = %v, want [%d %d 37]", sizes, pushBatchSize, pushBatchSize)
	}
	if len(seen) != notes {
		t.Errorf("hub received %d distinct notes, want all %d", len(seen), notes)
	}
	for guid, n := range seen {
		if n != 1 {
			t.Errorf("note %s was pushed %d times", guid, n)
		}
	}
	if got := pendingForHub(t, sc); got != 0 {
		t.Errorf("%d changes still pending after the push, want 0", got)
	}
	if st := sc.GetStatus(); st.PushRejected != 0 || len(st.PushRejections) != 0 {
		t.Errorf("a clean push reports %d rejections", st.PushRejected)
	}
}

// A change the hub refuses must show up in the status. The cycle still counts
// as a success (and the change is not retried: see pushBatch), which is
// exactly why the status has to say so: before, the only trace was one Info
// line with two counts.
func TestPushReportsWhatTheHubRefused(t *testing.T) {
	hub := &scriptedPushHub{reject: map[string]string{
		"push-note-001": "note not found for sync update",
		"push-note-003": "failed to apply body diff",
	}}
	sc := pushTestClient(t, hub, 5)

	if err := sc.pushChanges(t.Context()); err != nil {
		t.Fatalf("pushChanges: %v", err)
	}

	st := sc.GetStatus()
	if st.PushRejected != 2 {
		t.Fatalf("status reports %d rejections, want 2", st.PushRejected)
	}
	reasons := map[string]bool{}
	for _, r := range st.PushRejections {
		reasons[r.Reason] = true
	}
	if !reasons["note not found for sync update"] || !reasons["failed to apply body diff"] {
		t.Errorf("status rejections lack the hub's reasons: %+v", st.PushRejections)
	}

	// A later clean push clears it: the status describes the latest push.
	body := "another"
	if _, err := CreateNote(NoteInput{GUID: "push-note-later", Title: "later", Body: &body}, "push-user"); err != nil {
		t.Fatal(err)
	}
	if err := sc.pushChanges(t.Context()); err != nil {
		t.Fatalf("second pushChanges: %v", err)
	}
	if st := sc.GetStatus(); st.PushRejected != 0 {
		t.Errorf("status still reports %d rejections after a clean push", st.PushRejected)
	}
}

// When the token has expired, the push is retried after a re-login, and the
// retry must carry the changes. The request body is a reader the first attempt
// has already read to the end; replayed without rewinding it reached the hub
// empty, was answered "success" with nothing accepted, and the batch was then
// marked delivered.
func TestPushRetriedAfterReLoginStillCarriesTheChanges(t *testing.T) {
	hub := &scriptedPushHub{expireFirst: true}
	sc := pushTestClient(t, hub, 3)

	if err := sc.pushChanges(t.Context()); err != nil {
		t.Fatalf("pushChanges: %v", err)
	}
	if hub.logins != 1 {
		t.Errorf("spoke logged in %d times after the 401, want 1", hub.logins)
	}
	if hub.emptyBodies != 0 {
		t.Fatalf("the retried push reached the hub with no changes in it")
	}
	if len(hub.batches) != 1 || len(hub.batches[0]) != 3 {
		t.Fatalf("hub received batches %v, want one batch of 3", len(hub.batches))
	}
}
