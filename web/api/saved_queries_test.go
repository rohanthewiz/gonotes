package api_test

import (
	"net/http"
	"testing"
)

// saved_queries_test.go covers the saved-query and history endpoints over the
// real HTTP stack, and the completion endpoint's use of them — which is how
// both the web bar and the TUI actually see these rows.

func TestSavedQueryEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()

	var savedID int64

	t.Run("SaveAndList", func(t *testing.T) {
		status, resp := ts.request("POST", "/api/v1/notes/query/saved", map[string]interface{}{
			"name": "flagged", "query": "is_flagged = true",
		})
		if status != http.StatusOK {
			t.Fatalf("save gave %d: %v", status, resp)
		}
		savedID = int64(resp["data"].(map[string]interface{})["id"].(float64))

		status, resp = ts.request("POST", "/api/v1/notes/query/history", map[string]interface{}{
			"query": "tags = 'airflow'",
		})
		if status != http.StatusOK {
			t.Fatalf("record gave %d: %v", status, resp)
		}

		status, resp = ts.request("GET", "/api/v1/notes/query/saved", nil)
		if status != http.StatusOK {
			t.Fatalf("list gave %d: %v", status, resp)
		}
		data := resp["data"].(map[string]interface{})
		if n := len(data["saved"].([]interface{})); n != 1 {
			t.Errorf("want 1 saved query, got %d", n)
		}
		if n := len(data["history"].([]interface{})); n != 1 {
			t.Errorf("want 1 history entry, got %d", n)
		}
	})

	t.Run("CompletionOffersThem", func(t *testing.T) {
		status, resp := ts.request("GET", "/api/v1/notes/query/complete?q=&pos=0", nil)
		if status != http.StatusOK {
			t.Fatalf("complete gave %d: %v", status, resp)
		}
		sugg := resp["data"].(map[string]interface{})["suggestions"].([]interface{})
		first := sugg[0].(map[string]interface{})
		second := sugg[1].(map[string]interface{})
		if first["kind"] != "saved" || first["label"] != "flagged" || first["id"] == nil {
			t.Errorf("an empty box should lead with the saved query, got %v", first)
		}
		if second["kind"] != "history" || second["text"] != "tags = 'airflow'" {
			t.Errorf("then the recent query, got %v", second)
		}
	})

	t.Run("BadQueryIsPositioned", func(t *testing.T) {
		status, resp := ts.request("POST", "/api/v1/notes/query/saved", map[string]interface{}{
			"name": "broken", "query": "category = ",
		})
		if status != http.StatusBadRequest {
			t.Fatalf("an unparseable query should be 400, got %d: %v", status, resp)
		}
		data, ok := resp["data"].(map[string]interface{})
		if !ok || data["position"] == nil {
			t.Errorf("the 400 should carry the error position, got %v", resp)
		}
	})

	t.Run("MissingNameIs400", func(t *testing.T) {
		status, resp := ts.request("POST", "/api/v1/notes/query/saved", map[string]interface{}{
			"name": "  ", "query": "is_flagged = true",
		})
		if status != http.StatusBadRequest {
			t.Fatalf("want 400, got %d: %v", status, resp)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		path := "/api/v1/notes/query/saved/" + itoa(savedID)
		if status, resp := ts.request("DELETE", path, nil); status != http.StatusOK {
			t.Fatalf("delete gave %d: %v", status, resp)
		}
		if status, _ := ts.request("DELETE", path, nil); status != http.StatusNotFound {
			t.Errorf("a second delete should be 404, got %d", status)
		}
	})

	t.Run("RequiresAuthentication", func(t *testing.T) {
		saved := ts.authToken
		ts.authToken = ""
		defer func() { ts.authToken = saved }()

		for _, c := range []struct{ method, path string }{
			{"GET", "/api/v1/notes/query/saved"},
			{"POST", "/api/v1/notes/query/saved"},
			{"DELETE", "/api/v1/notes/query/saved/1"},
			{"POST", "/api/v1/notes/query/history"},
		} {
			status, _ := ts.request(c.method, c.path, map[string]interface{}{"name": "x", "query": "id = 1"})
			if status != http.StatusUnauthorized {
				t.Errorf("%s %s without a token gave %d, want 401", c.method, c.path, status)
			}
		}
	})
}
