package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gonotes/models"
)

// msgpackRequest is ts.request with the X-Body-Encoding: msgpack header the
// browser sends on every call once its msgpack library has loaded.
func (ts *testServer) msgpackRequest(method, path string, body interface{}) (int, map[string]interface{}) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, ts.baseURL+path, &buf)
	if err != nil {
		return 0, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Body-Encoding", "msgpack")
	req.Header.Set("Authorization", "Bearer "+ts.authToken)
	resp, err := ts.client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

// storedBody reads a note's body back over plain JSON, so the check does not
// depend on the encoding under test.
func (ts *testServer) storedBody(t *testing.T, id int64) string {
	t.Helper()
	status, resp := ts.request("GET", fmt.Sprintf("/api/v1/notes/%d", id), nil)
	if status != http.StatusOK {
		t.Fatalf("GET note: status %d", status)
	}
	body, _ := resp["data"].(map[string]interface{})["body"].(string)
	return body
}

// A request in msgpack mode that carries a plain `body` must store that body.
//
// This is the shape the browser sends when its encoder fails on a large note
// (it spreads the bytes as call arguments, which overflows the stack past
// roughly 100 KB) and falls back to a plain body while still sending the
// header. The server used to read only body_encoded in that mode, see no body
// at all, and write NULL over the note's text while answering 200.
func TestMsgPackModeHonoursAPlainBody(t *testing.T) {
	ts := newTestServer(t)
	defer ts.cleanup()

	status, resp := ts.request("POST", "/api/v1/notes",
		models.NoteInput{GUID: "msgpack-plain-body", Title: "large"})
	if status != http.StatusCreated {
		t.Fatalf("create: status %d", status)
	}
	id := int64(resp["data"].(map[string]interface{})["id"].(float64))

	// 200 KB: comfortably past the size at which the browser encoder fails.
	large := strings.Repeat("0123456789abcdef", 200*1024/16)

	status, resp = ts.msgpackRequest("PUT", fmt.Sprintf("/api/v1/notes/%d", id), map[string]interface{}{
		"guid": "msgpack-plain-body", "title": "large", "body": large,
	})
	if status != http.StatusOK {
		t.Fatalf("update with a plain body in msgpack mode: status %d, %v", status, resp["error"])
	}
	if got := ts.storedBody(t, id); got != large {
		t.Fatalf("stored body is %d bytes, want the %d sent (an empty body is the data-loss bug)", len(got), len(large))
	}

	// Create takes the same path and must not lose the body either.
	status, resp = ts.msgpackRequest("POST", "/api/v1/notes", map[string]interface{}{
		"guid": "msgpack-plain-body-create", "title": "large create", "body": large,
	})
	if status != http.StatusCreated {
		t.Fatalf("create with a plain body in msgpack mode: status %d, %v", status, resp["error"])
	}
	createdID := int64(resp["data"].(map[string]interface{})["id"].(float64))
	if got := ts.storedBody(t, createdID); got != large {
		t.Errorf("created note's body is %d bytes, want %d", len(got), len(large))
	}

	// body_encoded still wins when both are present, so a client that encodes
	// successfully is unaffected.
	encoded, err := models.EncodeMsgPackBody(strPtr("from body_encoded"))
	if err != nil {
		t.Fatal(err)
	}
	status, _ = ts.msgpackRequest("PUT", fmt.Sprintf("/api/v1/notes/%d", id), map[string]interface{}{
		"guid": "msgpack-plain-body", "title": "large", "body_encoded": encoded, "body": "from body",
	})
	if status != http.StatusOK {
		t.Fatalf("update with body_encoded: status %d", status)
	}
	if got := ts.storedBody(t, id); got != "from body_encoded" {
		t.Errorf("stored %q, want the decoded body_encoded", got)
	}
}

// The stale-write guard has to work for a client in msgpack mode, which is
// every browser session: the response must name the note's version, and an
// update naming an outdated one must be refused with a 409 that leaves the
// stored note alone.
func TestMsgPackModeCarriesTheVersionGuard(t *testing.T) {
	ts := newTestServer(t)
	defer ts.cleanup()

	status, resp := ts.msgpackRequest("POST", "/api/v1/notes", map[string]interface{}{
		"guid": "msgpack-version", "title": "guarded", "body": "first",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: status %d", status)
	}
	created := resp["data"].(map[string]interface{})
	id := int64(created["id"].(float64))
	loaded, ok := created["version"].(float64)
	if !ok || loaded < 1 {
		t.Fatalf("msgpack-mode response carries no usable version: %v", created["version"])
	}

	// Somebody else saves: the stored version moves past what this editor loaded.
	status, _ = ts.request("PUT", fmt.Sprintf("/api/v1/notes/%d", id),
		models.NoteInput{GUID: "msgpack-version", Title: "guarded", Body: strPtr("theirs")})
	if status != http.StatusOK {
		t.Fatalf("the other writer's save: status %d", status)
	}

	// The editor's save, built on the version it loaded, must be refused.
	status, resp = ts.msgpackRequest("PUT", fmt.Sprintf("/api/v1/notes/%d", id), map[string]interface{}{
		"guid": "msgpack-version", "title": "guarded", "body": "mine", "expected_version": loaded,
	})
	if status != http.StatusConflict {
		t.Fatalf("stale save in msgpack mode: status %d, want 409 (200 means the guard never engaged)", status)
	}
	detail, _ := resp["data"].(map[string]interface{})
	if detail["reason"] != "stale" {
		t.Errorf("conflict reason = %v, want stale", detail["reason"])
	}
	if got := ts.storedBody(t, id); got != "theirs" {
		t.Errorf("a refused save changed the stored body to %q", got)
	}

	// Naming the current version succeeds, and the response names the next one.
	current := detail["current"].(map[string]interface{})["version"].(float64)
	status, resp = ts.msgpackRequest("PUT", fmt.Sprintf("/api/v1/notes/%d", id), map[string]interface{}{
		"guid": "msgpack-version", "title": "guarded", "body": "mine", "expected_version": current,
	})
	if status != http.StatusOK {
		t.Fatalf("save on the current version: status %d", status)
	}
	if next := resp["data"].(map[string]interface{})["version"].(float64); next != current+1 {
		t.Errorf("version after save = %v, want %v", next, current+1)
	}
}

// strPtr returns a pointer to s, for the optional string fields of NoteInput.
func strPtr(s string) *string { return &s }
