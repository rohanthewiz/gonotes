package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The desktop spoke's side of acknowledged delivery (pullChanges), against a
// scripted hub. A hub and a spoke cannot share one test process here, because
// the models package has one database, so the hub's answers are canned and
// the test checks what the spoke asks for.

// scriptedPullHub answers GET /api/v1/sync/pull with pages in order, then
// with an empty page, and records every request's query.
type scriptedPullHub struct {
	mu    sync.Mutex
	pages []SyncPullResponse
	after SyncPullResponse // answered once the script runs out
	got   []url.Values
}

func (h *scriptedPullHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.got = append(h.got, r.URL.Query())
	page := h.after
	if n := len(h.got) - 1; n < len(h.pages) {
		page = h.pages[n]
	}
	h.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": page})
}

// pullTestClient points a spoke at a scripted hub over a fresh local database.
func pullTestClient(t *testing.T, hub *scriptedPullHub) *SyncClient {
	t.Helper()
	if err := InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("init test db: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	return &SyncClient{
		config:     &SyncConfig{HubURL: srv.URL},
		peerID:     "desk-spoke",
		authToken:  "token",
		httpClient: srv.Client(),
	}
}

// hubNote is a note create as a hub would relay it.
func hubNote(changeGUID, noteGUID string) SyncChange {
	title, body := "Title "+noteGUID, "body"
	now := time.Now().UTC()
	return SyncChange{
		GUID: changeGUID, EntityType: "note", EntityGUID: noteGUID,
		Operation: OperationCreate, AuthoredAt: now, CreatedAt: now,
		Fragment: &NoteFragmentOutput{Bitmask: FragmentTitle | FragmentBody, Title: &title, Body: &body},
	}
}

func heldPage(hasMore bool, changes ...SyncChange) SyncPullResponse {
	return SyncPullResponse{Changes: changes, HasMore: hasMore, Delivery: PullDeliveryAck}
}

// TestDesktopPullAcknowledgesEachPage checks the request sequence: each pull
// acknowledges the page before it, the last page is acknowledged by a closing
// pull, and every pull asks for acknowledged delivery.
func TestDesktopPullAcknowledgesEachPage(t *testing.T) {
	p1 := heldPage(true, hubNote("chg-1", "desk-note-1"), hubNote("chg-2", "desk-note-2"))
	p2 := heldPage(false, hubNote("chg-3", "desk-note-3"))
	hub := &scriptedPullHub{pages: []SyncPullResponse{p1, p2}, after: heldPage(false)}
	sc := pullTestClient(t, hub)

	if err := sc.pullChanges(t.Context()); err != nil {
		t.Fatalf("pullChanges: %v", err)
	}

	if len(hub.got) != 3 {
		t.Fatalf("spoke made %d pulls, want 3 (two pages and the closing pull)", len(hub.got))
	}
	for i, q := range hub.got {
		if q.Get("delivery") != PullDeliveryAck || q.Get("peer_id") != "desk-spoke" {
			t.Errorf("pull %d query = %v, want delivery=ack for desk-spoke", i, q)
		}
	}
	if hub.got[0].Has("ack") {
		t.Errorf("first pull acknowledged %q before any page was applied", hub.got[0].Get("ack"))
	}
	wantAcks := [][2]string{
		{"chg-2", PullPageSum([]string{"chg-1", "chg-2"})},
		{"chg-3", PullPageSum([]string{"chg-3"})},
	}
	for i, want := range wantAcks {
		q := hub.got[i+1]
		if q.Get("ack") != want[0] || q.Get("ack_sum") != want[1] {
			t.Errorf("pull %d ack = %s/%s, want %s/%s", i+1, q.Get("ack"), q.Get("ack_sum"), want[0], want[1])
		}
	}

	for _, g := range []string{"desk-note-1", "desk-note-2", "desk-note-3"} {
		if n, err := GetNoteByGUID(g); err != nil || n == nil {
			t.Errorf("note %s not applied (%v)", g, err)
		}
	}
}

// TestDesktopPullAgainstAMarkOnServeHub checks the spoke against a hub that
// predates acknowledged delivery. Its pages carry no delivery field and are
// already marked, so has_more alone ends the loop, with no closing pull.
func TestDesktopPullAgainstAMarkOnServeHub(t *testing.T) {
	page := SyncPullResponse{Changes: []SyncChange{hubNote("old-1", "old-note-1")}, HasMore: false}
	hub := &scriptedPullHub{pages: []SyncPullResponse{page}}
	sc := pullTestClient(t, hub)

	if err := sc.pullChanges(t.Context()); err != nil {
		t.Fatalf("pullChanges: %v", err)
	}
	if len(hub.got) != 1 {
		t.Fatalf("spoke made %d pulls against a mark-on-serve hub, want 1", len(hub.got))
	}
}

// TestDesktopPullStopsWhenTheAckIsIgnored checks the loop guard. A hub that
// serves the acknowledged page again unchanged did not take the
// acknowledgement, and pulling on would never end.
func TestDesktopPullStopsWhenTheAckIsIgnored(t *testing.T) {
	stuck := heldPage(true, hubNote("stuck-1", "stuck-note-1"))
	hub := &scriptedPullHub{after: stuck}
	sc := pullTestClient(t, hub)

	err := sc.pullChanges(t.Context())
	if err == nil || !strings.Contains(err.Error(), "acknowledged page again") {
		t.Fatalf("pullChanges = %v, want the re-served-page error", err)
	}
	if len(hub.got) != 2 {
		t.Fatalf("spoke made %d pulls before giving up, want 2", len(hub.got))
	}
}
