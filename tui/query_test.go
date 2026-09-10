package tui

import (
	"errors"
	"strings"
	"testing"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// query_test.go covers the terminal half of the advanced search: what the
// screen asks for, what it does with the answer, and how a query reaches the
// note list.
//
// The language itself is pinned in models/query_test.go, and the fakeStore runs
// the REAL parser (see its QueryNotes), so nothing here is testing a mock's
// idea of what a query means.

func querySession(store Store) *session {
	return &session{
		store:  store,
		cats:   newCatsState(),
		sync:   &syncState{},
		user:   &models.User{GUID: "u"},
		width:  100,
		height: 30,
	}
}

// typeInto feeds a string to a screen one keypress at a time, the way a person
// would, and returns every message the screen emitted along the way.
func typeInto(s screen, text string) []tea.Msg {
	var msgs []tea.Msg
	for _, r := range text {
		var cmd tea.Cmd
		s, cmd = s.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		msgs = append(msgs, drainCmd(cmd)...)
	}
	return msgs
}

// tap sends one named key and drains what it produced. It is the named-key
// companion to unsaved_test.go's press, which takes a constructed message.
func tap(s screen, name string) (screen, []tea.Msg) {
	var code rune
	switch name {
	case "enter":
		code = tea.KeyEnter
	case "tab":
		code = tea.KeyTab
	case "esc":
		code = tea.KeyEscape
	case "up":
		code = tea.KeyUp
	case "down":
		code = tea.KeyDown
	}
	next, cmd := s.Update(tea.KeyPressMsg{Code: code})
	return next, drainCmd(cmd)
}

// completionsOf finds the LAST completion reply in a batch of drained
// messages. Typing n characters produces n replies, and only the final one
// describes the text that is now on screen.
func completionsOf(msgs []tea.Msg) *queryCompletionMsg {
	var found *queryCompletionMsg
	for i := range msgs {
		if m, ok := msgs[i].(queryCompletionMsg); ok {
			found = &m
		}
	}
	return found
}

// typeQuery puts text in the box and completes at the end of it.
//
// It exists to keep this suite fast, not to dodge the real path. textinput's
// Update returns a cursor-BLINK command alongside its own, and drainCmd runs
// commands synchronously — so draining one keystroke waits out the blink
// interval, roughly half a second per character. One test below types
// character by character deliberately, to prove that typing is what triggers a
// completion; the rest set the value and drive the very command Update would
// have returned.
func typeQuery(q *queryScreen, text string) *queryScreen {
	q.input.SetValue(text)
	q.input.CursorEnd()
	return settle(q, drainCmd(q.complete()))
}

// settle feeds completion replies back through Update, which is how they arrive
// in the running program — including the sequence check that drops stale ones.
// Tests that install a reply by hand would skip exactly that.
func settle(q *queryScreen, msgs []tea.Msg) *queryScreen {
	for i := range msgs {
		if m, ok := msgs[i].(queryCompletionMsg); ok {
			next, _ := q.Update(m)
			q = next.(*queryScreen)
		}
	}
	return q
}

func labelsOf(res *models.QueryCompletion) []string {
	if res == nil {
		return nil
	}
	out := make([]string, len(res.Suggestions))
	for i, s := range res.Suggestions {
		out[i] = s.Label
	}
	return out
}

func hasLabel(res *models.QueryCompletion, want string) bool {
	for _, l := range labelsOf(res) {
		if l == want {
			return true
		}
	}
	return false
}

// The screen asks for completions before its first frame, because an empty box
// is where the language most needs to introduce itself.
func TestQueryScreenAsksForCompletionsOnOpen(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	msgs := drainCmd(q.Init())

	c := completionsOf(msgs)
	if c == nil || c.res == nil || len(c.res.Suggestions) == 0 {
		t.Fatal("opening the query screen offered no suggestions")
	}
	if c.res.Suggestions[0].Kind != "example" {
		t.Fatalf("an empty query should lead with examples, got %v", labelsOf(c.res))
	}
}

// Reopening with a query already in force edits it rather than starting over.
func TestQueryScreenOpensWithTheCurrentQuery(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "is_flagged")
	if q.input.Value() != "is_flagged" {
		t.Fatalf("the screen opened with %q", q.input.Value())
	}
}

func TestQueryScreenCompletesAsYouType(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	// Real keypresses, one per character — this is the test that proves a
	// keystroke is what asks for completions. It is also the only one that
	// pays the blink cost; see typeQuery.
	msgs := typeInto(q, "sub")

	c := completionsOf(msgs)
	if !hasLabel(c.res, "subcategory") {
		t.Fatalf("typing an alias offered %v", labelsOf(c.res))
	}
	if c.seq != q.seq {
		t.Fatalf("the last reply carries seq %d but the screen is at %d", c.seq, q.seq)
	}
}

// Tab splices the server's replacement in verbatim. The screen must not do any
// quoting or spacing of its own, or it would drift from the web UI, which does
// the same splice with the same numbers.
func TestTabAcceptsTheHighlightedSuggestion(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	q = typeQuery(q, "subcat")

	if q.highlight != 0 {
		t.Fatalf("the first suggestion should start highlighted, got %d", q.highlight)
	}
	next, _ := tap(q, "tab")
	q = next.(*queryScreen)

	if q.input.Value() != "subcategory " {
		t.Fatalf("after tab the query reads %q, want %q", q.input.Value(), "subcategory ")
	}
	if q.input.Position() != len("subcategory ") {
		t.Fatalf("the cursor is at %d, want the end of the insertion", q.input.Position())
	}
}

// Arrow keys move within the list; the highlighted row is what tab takes.
func TestArrowsMoveTheHighlight(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	q = typeQuery(q, "cat")
	if len(q.sugg) < 2 {
		t.Skip("need at least two suggestions to move between")
	}

	next, _ := tap(q, "down")
	q = next.(*queryScreen)
	if q.highlight != 1 {
		t.Fatalf("down moved the highlight to %d, want 1", q.highlight)
	}
	next, _ = tap(q, "up")
	q = next.(*queryScreen)
	if q.highlight != 0 {
		t.Fatalf("up moved the highlight to %d, want 0", q.highlight)
	}
	// And it wraps, so a long list is reachable from either end.
	next, _ = tap(q, "up")
	q = next.(*queryScreen)
	if q.highlight != len(q.sugg)-1 {
		t.Fatalf("up from the top should wrap to the end, got %d", q.highlight)
	}
}

// Enter on the DEFAULT row runs the query rather than accepting a completion.
// The other way round, finishing a complete query and pressing enter would
// insert a suggestion nobody asked for.
func TestEnterRunsRatherThanAccepting(t *testing.T) {
	store := newFakeStore()
	store.seedNote("u", "Airflow DAG conversion", "convert the dags")
	store.seedNote("u", "Grocery list", "milk")

	q := newQueryScreen(querySession(store), "")
	q = typeQuery(q, "title CONTAINS 'airflow'")
	q.adopt(&models.QueryCompletion{
		Suggestions: []models.QuerySuggestion{{Label: "AND", Text: "AND ", Kind: "keyword"}},
	})

	_, msgs := tap(q, "enter")
	var ran *queryRanMsg
	for i := range msgs {
		if m, ok := msgs[i].(queryRanMsg); ok {
			ran = &m
		}
	}
	if ran == nil {
		t.Fatal("enter did not run the query")
	}
	if ran.err != nil {
		t.Fatalf("the query failed: %v", ran.err)
	}
	if len(ran.notes) != 1 || ran.notes[0].Title != "Airflow DAG conversion" {
		t.Fatalf("the query matched %d notes: %+v", len(ran.notes), ran.notes)
	}
}

// Enter on a row the user MOVED to accepts it — they went there on purpose.
func TestEnterAcceptsAfterMoving(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	q.adopt(&models.QueryCompletion{
		Suggestions: []models.QuerySuggestion{
			{Label: "AND", Text: "AND ", Kind: "keyword"},
			{Label: "OR", Text: "OR ", Kind: "keyword"},
		},
		ReplaceStart: 0, ReplaceEnd: 0,
	})
	q.highlight = 1

	next, _ := tap(q, "enter")
	q = next.(*queryScreen)
	if q.input.Value() != "OR " {
		t.Fatalf("enter on a moved-to row gave %q, want the accepted suggestion", q.input.Value())
	}
}

// A syntax error is held as a structure, not a sentence, so the caret line can
// point at the offending run and the cursor can be put on it.
func TestSyntaxErrorIsPositioned(t *testing.T) {
	store := newFakeStore()
	q := newQueryScreen(querySession(store), "")
	q = typeQuery(q, "catgory = 'x'")

	_, msgs := tap(q, "enter")
	for i := range msgs {
		if m, ok := msgs[i].(queryRanMsg); ok {
			next, _ := q.Update(m)
			q = next.(*queryScreen)
		}
	}

	if q.qerr == nil {
		t.Fatalf("a bad field name left no positioned error (errText=%q)", q.errText)
	}
	if q.qerr.Pos != 0 || q.qerr.Len != 7 {
		t.Fatalf("the error spans [%d,%d), want the field name", q.qerr.Pos, q.qerr.Len)
	}
	if !strings.Contains(q.qerr.Hint, "category") {
		t.Fatalf("no suggestion for the near-miss: %q", q.qerr.Hint)
	}
	// The caret line must appear under the query, pointing at it.
	view := q.View()
	if !strings.Contains(view, "^^^^^^^") {
		t.Fatalf("the view does not underline the mistake:\n%s", view)
	}
	if !strings.Contains(view, "did you mean category?") {
		t.Fatalf("the view does not show the suggestion:\n%s", view)
	}
}

// A failure with no position — the store could not be reached — is reported as
// a sentence rather than pretending to point at a character.
func TestTransportFailureIsReportedPlainly(t *testing.T) {
	store := newFakeStore()
	store.failWith = errors.New("connection refused")

	q := newQueryScreen(querySession(store), "")
	q = typeQuery(q, "is_flagged")
	_, msgs := tap(q, "enter")
	for i := range msgs {
		if m, ok := msgs[i].(queryRanMsg); ok {
			next, _ := q.Update(m)
			q = next.(*queryScreen)
		}
	}
	if q.qerr != nil {
		t.Fatal("a transport failure must not be shown as a syntax error")
	}
	if !strings.Contains(q.errText, "connection refused") {
		t.Fatalf("the failure was reported as %q", q.errText)
	}
}

// A completion that arrives after a later keystroke must not replace the newer
// list. Typing faster than the round trip is the normal case on a hub.
func TestStaleCompletionsAreDropped(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	q = typeQuery(q, "cat")
	current := q.seq

	stale := queryCompletionMsg{
		seq: current - 1,
		res: &models.QueryCompletion{Suggestions: []models.QuerySuggestion{
			{Label: "STALE", Text: "STALE", Kind: "field"},
		}},
	}
	next, _ := q.Update(stale)
	q = next.(*queryScreen)

	for _, l := range labelsOf(&models.QueryCompletion{Suggestions: q.sugg}) {
		if l == "STALE" {
			t.Fatal("an out-of-order completion reply was installed")
		}
	}
}

// esc peels one layer at a time: the suggestions, then the field list, then the
// screen. Each press undoes the most recent thing that appeared.
func TestEscapePeelsOneLayerAtATime(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	q = typeQuery(q, "cat")
	if len(q.sugg) == 0 {
		t.Fatal("expected suggestions to be showing")
	}

	next, msgs := tap(q, "esc")
	q = next.(*queryScreen)
	if len(q.sugg) != 0 {
		t.Fatal("the first esc should dismiss the suggestions")
	}
	for i := range msgs {
		if _, ok := msgs[i].(popMsg); ok {
			t.Fatal("the first esc must not leave the screen")
		}
	}

	_, msgs = tap(q, "esc")
	popped := false
	for i := range msgs {
		if _, ok := msgs[i].(popMsg); ok {
			popped = true
		}
	}
	if !popped {
		t.Fatal("the second esc should leave the screen")
	}
}

// An emptied box clears the filter rather than erroring — the same thing an
// emptied search box does anywhere else — and it does so without asking the
// store anything, since "everything" is not a query.
//
// The assertion is indirect because run() returns a tea.Sequence for this case
// (pop, then the message), and tea.Sequence's message type is unexported, so
// drainCmd cannot see inside it. What CAN be observed is that a command was
// produced and that the store was never asked, which is the behavior that
// matters.
func TestEmptyQueryClearsTheFilterWithoutQuerying(t *testing.T) {
	store := newFakeStore()
	q := newQueryScreen(querySession(store), "is_flagged")
	q.input.SetValue("")

	next, cmd := q.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	q = next.(*queryScreen)
	if cmd == nil {
		t.Fatal("enter on an empty query did nothing")
	}
	if store.queryCalls != 0 {
		t.Fatalf("an empty query reached the store %d times", store.queryCalls)
	}
	if q.running {
		t.Fatal("an empty query should not be marked as running")
	}
}

// ctrl+t swaps the completion list for the field catalog, which is the answer
// to "what can I even ask about".
func TestFieldCatalogToggle(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	next, _ := q.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	q = next.(*queryScreen)

	if !q.helpOpen {
		t.Fatal("ctrl+t did not open the field catalog")
	}
	view := q.View()
	for _, want := range []string{"category", "subcategory", "is_private", "updated_at", "ORDER BY"} {
		if !strings.Contains(view, want) {
			t.Errorf("the field catalog does not mention %q", want)
		}
	}
}

// ^H is what several terminals still send for Backspace, and bubbles' textinput
// binds ctrl+h to delete-backward for that reason. The query screen must not
// claim it — a key that stops deleting is the worst kind of regression, and it
// only shows up on the terminals that send it.
func TestCtrlHStillDeletes(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "is_flagged")
	q.input.CursorEnd()

	next, _ := q.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	q = next.(*queryScreen)

	if q.helpOpen {
		t.Fatal("ctrl+h opened the field catalog; it must stay a delete")
	}
	if q.input.Value() != "is_flagge" {
		t.Fatalf("ctrl+h left %q, want a character deleted", q.input.Value())
	}
}

// ---------------------------------------------------------------------------
// The browse screen's half
// ---------------------------------------------------------------------------

// A query result becomes an ordinary note list — same rows, same badges, same
// keys — which is the whole reason the query screen pops instead of pushing a
// result screen of its own.
func TestBrowseAdoptsQueryResults(t *testing.T) {
	store := newFakeStore()
	store.seedNote("u", "Airflow DAG conversion", "dags")
	store.seedNote("u", "Grocery list", "milk")

	b := newBrowseScreen(querySession(store))
	drainCmd(b.Init())

	notes, err := store.QueryNotes("title CONTAINS 'airflow'", "u")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	next, cmd := b.Update(queryRanMsg{query: "title CONTAINS 'airflow'", notes: notes})
	b = next.(*browseScreen)
	drainCmd(cmd)

	if b.queryFilter != "title CONTAINS 'airflow'" {
		t.Fatalf("the browse screen did not adopt the query: %q", b.queryFilter)
	}
	if got := len(b.list.Items()); got != 1 {
		t.Fatalf("the list holds %d rows, want the 1 matching note", got)
	}
	if !strings.Contains(b.title(), "title CONTAINS") {
		t.Fatalf("the title does not name the query: %q", b.title())
	}
}

// esc peels the query before the category filter: it is the narrowest thing on
// screen and the most recently applied.
func TestEscapeClearsTheQueryBeforeTheCategory(t *testing.T) {
	store := newFakeStore()
	store.seedNote("u", "Airflow DAG conversion", "dags")

	b := newBrowseScreen(querySession(store))
	drainCmd(b.Init())
	b.catFilter = &models.Category{ID: 1, Name: "airflow"}
	b.queryFilter = "is_flagged"

	next, _ := b.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	b = next.(*browseScreen)

	if b.queryFilter != "" {
		t.Fatal("esc should clear the query first")
	}
	if b.catFilter == nil {
		t.Fatal("esc must not clear the category filter in the same press")
	}
}

// Picking a category retires the query. A query outranks the category filter
// when the list reloads, so leaving both in place would make the pick appear to
// do nothing — the one outcome a user cannot make sense of.
func TestCategoryPickRetiresTheQuery(t *testing.T) {
	store := newFakeStore()
	store.seedNote("u", "Airflow DAG conversion", "dags")

	b := newBrowseScreen(querySession(store))
	drainCmd(b.Init())
	b.queryFilter = "is_flagged"

	cat := models.Category{ID: 1, Name: "airflow"}
	next, cmd := b.Update(categoryPickedMsg{cat: &cat})
	b = next.(*browseScreen)
	drainCmd(cmd)

	if b.queryFilter != "" {
		t.Fatalf("the query survived a category pick: %q", b.queryFilter)
	}
	if b.catFilter == nil || b.catFilter.Name != "airflow" {
		t.Fatal("the category pick did not take effect")
	}
}

// A query in force is what the list reloads from, so a save or a delete comes
// back to the same filtered view rather than silently widening to everything.
func TestRefreshHonorsTheQuery(t *testing.T) {
	store := newFakeStore()
	store.seedNote("u", "Airflow DAG conversion", "dags")
	store.seedNote("u", "Grocery list", "milk")

	b := newBrowseScreen(querySession(store))
	b.queryFilter = "title CONTAINS 'airflow'"

	var loaded *notesLoadedMsg
	var ran *queryRanMsg
	for _, msg := range drainCmd(b.refresh()) {
		switch m := msg.(type) {
		case notesLoadedMsg:
			loaded = &m
		case queryRanMsg:
			ran = &m
		}
	}
	if loaded != nil {
		t.Fatal("a refresh under a query must not fall back to loading every note")
	}
	if ran == nil {
		t.Fatal("a refresh under a query should re-run the query")
	}
	if len(ran.notes) != 1 {
		t.Fatalf("the re-run matched %d notes, want 1", len(ran.notes))
	}
}

// ":" is the door. It must open the screen carrying whatever query is in force.
func TestColonOpensTheQueryScreenWithTheCurrentQuery(t *testing.T) {
	store := newFakeStore()
	b := newBrowseScreen(querySession(store))
	drainCmd(b.Init())
	b.queryFilter = "is_flagged"

	_, cmd := b.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	var pushed *pushMsg
	for _, msg := range drainCmd(cmd) {
		if m, ok := msg.(pushMsg); ok {
			pushed = &m
		}
	}
	if pushed == nil {
		t.Fatal(`":" did not open the query screen`)
	}
	q, ok := pushed.s.(*queryScreen)
	if !ok {
		t.Fatalf(`":" pushed a %T`, pushed.s)
	}
	if q.input.Value() != "is_flagged" {
		t.Fatalf("the query screen opened with %q, want the query in force", q.input.Value())
	}
}
