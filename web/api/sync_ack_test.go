package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"gonotes/models"
)

// pullAs issues GET /api/v1/sync/pull with the given query and decodes the
// page. It fails the test on any non-200 answer.
func (s *syncTestServer) pullAs(t *testing.T, q url.Values) models.SyncPullResponse {
	t.Helper()
	req, _ := s.createAuthenticatedRequest("GET", s.baseURL+"/api/v1/sync/pull?"+q.Encode(), nil)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull answered %d", resp.StatusCode)
	}
	var out struct {
		Success bool                    `json:"success"`
		Data    models.SyncPullResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.Success {
		t.Fatalf("pull response: %v (success=%v)", err, out.Success)
	}
	return out.Data
}

func pageGUIDs(p models.SyncPullResponse) []string {
	out := make([]string, len(p.Changes))
	for i, c := range p.Changes {
		out[i] = c.GUID
	}
	return out
}

// createNotes makes n notes through the REST API, each one change on the hub.
func (s *syncTestServer) createNotes(t *testing.T, prefix string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		body, _ := json.Marshal(models.NoteInput{
			GUID:  fmt.Sprintf("%s-%d", prefix, i),
			Title: fmt.Sprintf("%s %d", prefix, i),
		})
		req, _ := s.createAuthenticatedRequest("POST", s.baseURL+"/api/v1/notes", bytes.NewBuffer(body))
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatalf("create note: %v", err)
		}
		resp.Body.Close()
	}
}

// TestPullAckDelivery walks a spoke through acknowledged delivery over HTTP.
// A held page comes back until it is acknowledged, an acknowledgement moves
// the stream on by exactly that page, and the closing pull (the one that
// acknowledges the last page) comes back empty.
func TestPullAckDelivery(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()
	server.registerAndLogin(t)
	server.createNotes(t, "ack-http", 5)

	const peer = "spoke-ack-http"
	q := url.Values{"peer_id": {peer}, "limit": {"2"}, "delivery": {models.PullDeliveryAck}}

	page1 := server.pullAs(t, q)
	if len(page1.Changes) != 2 || !page1.HasMore || page1.Delivery != models.PullDeliveryAck {
		t.Fatalf("first page = %d changes, has_more=%v, delivery=%q; want 2, true, ack",
			len(page1.Changes), page1.HasMore, page1.Delivery)
	}

	// The spoke "died" with page 1 in flight: the next pull carries no ack
	// for it, and the hub serves it again instead of having lost it.
	if again := server.pullAs(t, q); !slices.Equal(pageGUIDs(again), pageGUIDs(page1)) {
		t.Fatalf("held page not served again: %v then %v", pageGUIDs(page1), pageGUIDs(again))
	}

	seen := map[string]bool{}
	page := page1
	for round := 0; round < 10; round++ {
		for _, g := range pageGUIDs(page) {
			if seen[g] {
				t.Fatalf("change %s delivered twice after its page was acknowledged", g)
			}
			seen[g] = true
		}
		guids := pageGUIDs(page)
		q.Set("ack", guids[len(guids)-1])
		q.Set("ack_sum", models.PullPageSum(guids))
		page = server.pullAs(t, q)
		if len(page.Changes) == 0 {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatalf("delivered %d distinct changes, want all 5", len(seen))
	}

	// The closing pull's ack marked the last page: a fresh cycle that opens
	// with the same standing ack sees nothing left.
	if rest := server.pullAs(t, q); len(rest.Changes) != 0 {
		t.Fatalf("after the closing pull %d changes remain", len(rest.Changes))
	}
}

// TestPullWithoutAckDeliveryMarksOnServe pins compatibility: a spoke that does
// not ask for acknowledged delivery is marked on serve as before and gets no
// delivery field, even if it sends an ack parameter.
func TestPullWithoutAckDeliveryMarksOnServe(t *testing.T) {
	server, cleanup := setupSyncTestServer(t)
	defer cleanup()
	server.registerAndLogin(t)
	server.createNotes(t, "legacy", 3)

	q := url.Values{"peer_id": {"spoke-legacy"}, "limit": {"2"}}
	first := server.pullAs(t, q)
	if len(first.Changes) != 2 || first.Delivery != "" {
		t.Fatalf("legacy page = %d changes, delivery=%q; want 2 and no delivery", len(first.Changes), first.Delivery)
	}
	// An ack without delivery=ack is ignored, and the served page was marked.
	q.Set("ack", "no-such-change")
	second := server.pullAs(t, q)
	if len(second.Changes) != 1 || slices.Contains(pageGUIDs(first), second.Changes[0].GUID) {
		t.Fatalf("second legacy page = %v, want the one change not served yet", pageGUIDs(second))
	}
}
