package api_test

import (
	"net/http"
	"net/url"
	"testing"
)

// query_test.go covers the advanced-search endpoints over the real HTTP stack:
// the query itself, its two failure modes (a bad query is 400 and carries a
// position; an unauthenticated one is 401), the completion endpoint, and the
// schema document a UI builds its help panel from.
//
// The language's own behavior is pinned in models/query_test.go; what is
// checked here is the wire contract those two front ends depend on.

// queryPath builds /api/v1/notes/query with the given parameters, so a test
// reads as the query it is about rather than as URL escaping.
func queryPath(base string, params map[string]string) string {
	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	if len(v) == 0 {
		return base
	}
	return base + "?" + v.Encode()
}

// seedQueryNotes creates the notes the tests below query for, and files two of
// them under a category with subcategories.
func seedQueryNotes(t *testing.T, ts *testServer) {
	t.Helper()

	status, resp := ts.request("POST", "/api/v1/categories", map[string]interface{}{
		"name":          "airflow",
		"subcategories": []string{"conversion", "scheduling"},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the category failed with %d: %v", status, resp)
	}
	catID := int(resp["data"].(map[string]interface{})["id"].(float64))

	notes := []struct {
		guid, title, tags string
		flagged           bool
		subcats           []string
	}{
		{"aq-1", "Airflow DAG conversion", "airflow,migration", true, []string{"conversion"}},
		{"aq-2", "Airflow scheduling notes", "airflow", false, []string{"scheduling"}},
		{"aq-3", "Unrelated grocery list", "home", false, nil},
	}
	for _, n := range notes {
		status, resp := ts.request("POST", "/api/v1/notes", map[string]interface{}{
			"guid": n.guid, "title": n.title, "tags": n.tags, "is_flagged": n.flagged,
			"body": "body of " + n.guid,
		})
		if status != http.StatusCreated {
			t.Fatalf("creating note %s failed with %d: %v", n.guid, status, resp)
		}
		if n.subcats == nil {
			continue
		}
		id := int(resp["data"].(map[string]interface{})["id"].(float64))
		status, resp = ts.request("POST",
			"/api/v1/notes/"+itoa(int64(id))+"/categories/"+itoa(int64(catID)),
			map[string]interface{}{"subcategories": n.subcats})
		if status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("linking note %s to the category failed with %d: %v", n.guid, status, resp)
		}
	}
}

func TestQueryNotesEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()
	seedQueryNotes(t, ts)

	// The query from the original request.
	t.Run("CategoryAndSubcategory", func(t *testing.T) {
		status, resp := ts.request("GET", queryPath("/api/v1/notes/query", map[string]string{
			"q": "category = 'airflow' and subcategory = 'conversion'",
		}), nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", status, resp)
		}
		data := resp["data"].(map[string]interface{})
		notes := data["notes"].([]interface{})
		if len(notes) != 1 {
			t.Fatalf("expected 1 note, got %d", len(notes))
		}
		if got := notes[0].(map[string]interface{})["guid"]; got != "aq-1" {
			t.Errorf("matched note %v, want aq-1", got)
		}
		if data["matched"].(float64) != 1 {
			t.Errorf("matched is %v, want 1", data["matched"])
		}
		// The normalized form is what a UI echoes back under the box.
		if data["normalized"] != "category = 'airflow' AND subcategory = 'conversion'" {
			t.Errorf("normalized form is %q", data["normalized"])
		}
		fields := data["fields"].([]interface{})
		if len(fields) != 2 {
			t.Errorf("fields is %v, want the two fields the query names", fields)
		}
	})

	t.Run("EmptyQueryReturnsEverything", func(t *testing.T) {
		status, resp := ts.request("GET", "/api/v1/notes/query", nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", status, resp)
		}
		data := resp["data"].(map[string]interface{})
		if data["matched"].(float64) != 3 {
			t.Errorf("an empty query matched %v notes, want all 3", data["matched"])
		}
	})

	t.Run("PagingAndSorting", func(t *testing.T) {
		status, resp := ts.request("GET", queryPath("/api/v1/notes/query", map[string]string{
			"q": "'airflow'", "limit": "1", "sort": "title", "dir": "asc",
		}), nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", status, resp)
		}
		data := resp["data"].(map[string]interface{})
		if data["returned"].(float64) != 1 {
			t.Errorf("returned is %v under limit=1", data["returned"])
		}
		// Matched must ignore the page — it is the count a UI displays.
		if data["matched"].(float64) != 2 {
			t.Errorf("matched is %v, want the unpaged 2", data["matched"])
		}
		if data["ordered"] != true {
			t.Error("an explicit sort parameter should report ordered=true")
		}
		notes := data["notes"].([]interface{})
		if got := notes[0].(map[string]interface{})["guid"]; got != "aq-1" {
			t.Errorf("sorted by title ascending the first note is %v, want aq-1", got)
		}
	})

	// A malformed query is the user's mistake, answered 400 with the position
	// so the input can underline it.
	t.Run("SyntaxErrorCarriesAPosition", func(t *testing.T) {
		status, resp := ts.request("GET", queryPath("/api/v1/notes/query", map[string]string{
			"q": "catgory = 'airflow'",
		}), nil)
		if status != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %v", status, resp)
		}
		if resp["success"] != false {
			t.Error("a syntax error must not report success")
		}
		data, ok := resp["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("a syntax error should carry its position, got data %v", resp["data"])
		}
		if data["position"].(float64) != 0 {
			t.Errorf("position is %v, want 0", data["position"])
		}
		if data["hint"] == nil || data["hint"] == "" {
			t.Error("a near-miss field name should come back with a suggestion")
		}
	})

	t.Run("BadParametersAreRefused", func(t *testing.T) {
		for _, params := range []map[string]string{
			{"q": "", "limit": "-1"},
			{"q": "", "offset": "abc"},
			{"q": "", "sort": "nonexistent"},
			{"q": "", "sort": "tags"}, // multi-valued: nothing to sort on
		} {
			status, _ := ts.request("GET", queryPath("/api/v1/notes/query", params), nil)
			if status != http.StatusBadRequest {
				t.Errorf("params %v gave %d, want 400", params, status)
			}
		}
	})

	t.Run("RequiresAuthentication", func(t *testing.T) {
		saved := ts.authToken
		ts.authToken = ""
		defer func() { ts.authToken = saved }()

		for _, path := range []string{
			"/api/v1/notes/query?q=is_flagged",
			"/api/v1/notes/query/complete?q=cat",
			"/api/v1/notes/query/schema",
		} {
			status, _ := ts.request("GET", path, nil)
			if status != http.StatusUnauthorized {
				t.Errorf("%s without a token gave %d, want 401", path, status)
			}
		}
	})
}

func TestQueryCompleteEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()
	seedQueryNotes(t, ts)

	suggestionLabels := func(resp map[string]interface{}) []string {
		data := resp["data"].(map[string]interface{})
		var out []string
		for _, s := range data["suggestions"].([]interface{}) {
			out = append(out, s.(map[string]interface{})["label"].(string))
		}
		return out
	}
	contains := func(list []string, want string) bool {
		for _, s := range list {
			if s == want {
				return true
			}
		}
		return false
	}

	t.Run("FieldNames", func(t *testing.T) {
		status, resp := ts.request("GET", queryPath("/api/v1/notes/query/complete",
			map[string]string{"q": "subc"}), nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", status, resp)
		}
		if !contains(suggestionLabels(resp), "subcategory") {
			t.Errorf("completing %q offered %v", "subc", suggestionLabels(resp))
		}
	})

	// The reason the completer lives on the server: these values are the
	// user's own data, and no client-side list could know them.
	t.Run("RealCategoryValues", func(t *testing.T) {
		status, resp := ts.request("GET", queryPath("/api/v1/notes/query/complete",
			map[string]string{"q": "category = "}), nil)
		if status != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", status, resp)
		}
		if !contains(suggestionLabels(resp), "airflow") {
			t.Errorf("category values offered %v, want the seeded category", suggestionLabels(resp))
		}
	})

	t.Run("RealSubcategoryAndTagValues", func(t *testing.T) {
		_, resp := ts.request("GET", queryPath("/api/v1/notes/query/complete",
			map[string]string{"q": "subcategory = 'conv"}), nil)
		if !contains(suggestionLabels(resp), "conversion") {
			t.Errorf("subcategory values offered %v", suggestionLabels(resp))
		}
		data := resp["data"].(map[string]interface{})
		if int(data["replace_start"].(float64)) != len("subcategory = ") {
			t.Errorf("replace_start is %v, want the opening quote's offset", data["replace_start"])
		}

		_, resp = ts.request("GET", queryPath("/api/v1/notes/query/complete",
			map[string]string{"q": "tags = "}), nil)
		if !contains(suggestionLabels(resp), "migration") {
			t.Errorf("tag values offered %v", suggestionLabels(resp))
		}
	})

	// The cursor need not be at the end of the text.
	t.Run("HonorsCursorPosition", func(t *testing.T) {
		q := "category = 'airflow' AND is_flagged"
		_, resp := ts.request("GET", queryPath("/api/v1/notes/query/complete",
			map[string]string{"q": q, "pos": "8"}), nil)
		data := resp["data"].(map[string]interface{})
		if data["context"] != "field" {
			t.Errorf("with the cursor inside the first field the context is %v, want field", data["context"])
		}
		if int(data["replace_end"].(float64)) != len("category") {
			t.Errorf("replace_end is %v, want the end of the word under the cursor", data["replace_end"])
		}
	})

	// Half-typed text is the normal state here and must never be an error.
	t.Run("NeverFailsOnPartialText", func(t *testing.T) {
		for _, q := range []string{"", "c", "category", "category =", "category = '", "cat # ~~ ", "(((", "'"} {
			status, resp := ts.request("GET", queryPath("/api/v1/notes/query/complete",
				map[string]string{"q": q}), nil)
			if status != http.StatusOK {
				t.Errorf("completing %q gave %d: %v", q, status, resp)
			}
		}
	})
}

func TestQuerySchemaEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := newTestServer(t)
	defer ts.cleanup()

	status, resp := ts.request("GET", "/api/v1/notes/query/schema", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, resp)
	}
	data := resp["data"].(map[string]interface{})

	fields := data["fields"].([]interface{})
	if len(fields) < 15 {
		t.Fatalf("the schema advertises %d fields; every note attribute should be there", len(fields))
	}
	// Spot-check that the shape a UI reads is present on every field.
	first := fields[0].(map[string]interface{})
	for _, key := range []string{"name", "kind", "description", "example"} {
		if _, ok := first[key]; !ok {
			t.Errorf("a schema field is missing %q", key)
		}
	}
	// The category/subcategory pair is the whole point of the feature.
	names := map[string]bool{}
	for _, f := range fields {
		names[f.(map[string]interface{})["name"].(string)] = true
	}
	for _, want := range []string{"category", "subcategory", "tags", "body", "is_private", "updated_at", "version"} {
		if !names[want] {
			t.Errorf("the schema does not advertise %q", want)
		}
	}
	if len(data["operators"].([]interface{})) == 0 {
		t.Error("the schema carries no operators")
	}
	if len(data["examples"].([]interface{})) == 0 {
		t.Error("the schema carries no examples")
	}
	if len(data["semantics"].([]interface{})) == 0 {
		t.Error("the schema should document where the language departs from SQL")
	}
}
