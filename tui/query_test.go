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

// ---------------------------------------------------------------------------
// Saved queries and history
// ---------------------------------------------------------------------------

// A deliberate run lands in history; the browse screen's refresh of an active
// query does not. Without the distinction every reload would churn the list
// and bump whatever query happened to be active back to the top.
func TestOnlyADeliberateRunIsRecorded(t *testing.T) {
	store := newFakeStore()
	store.seedNote("u", "Airflow DAG conversion", "dags")

	q := newQueryScreen(querySession(store), "")
	q = typeQuery(q, "title CONTAINS 'airflow'")
	tap(q, "enter")
	if len(store.recorded) != 1 || store.recorded[0] != "title CONTAINS 'airflow'" {
		t.Fatalf("enter should record the query once, got %q", store.recorded)
	}

	// The refresh path.
	drainCmd(runQueryCmd(store, "title CONTAINS 'airflow'", "u"))
	if len(store.recorded) != 1 {
		t.Fatalf("a refresh must not be recorded, got %q", store.recorded)
	}

	// A failed run is not history either.
	q = newQueryScreen(querySession(store), "")
	q = typeQuery(q, "title = ")
	tap(q, "enter")
	if len(store.recorded) != 1 {
		t.Fatalf("a query that did not parse must not be recorded, got %q", store.recorded)
	}
}

// Saved and recent rows are whole queries: accepting one replaces the line and
// runs it, exactly like an example — not a splice into the replace range.
func TestAcceptingAStoredQueryReplacesTheLine(t *testing.T) {
	for _, kind := range []string{"saved", "history"} {
		store := newFakeStore()
		store.seedNote("u", "Airflow DAG conversion", "dags")
		store.seedNote("u", "Grocery list", "milk")

		q := newQueryScreen(querySession(store), "")
		q = typeQuery(q, "air")
		q.adopt(&models.QueryCompletion{
			ReplaceStart: 0, ReplaceEnd: 3,
			Suggestions: []models.QuerySuggestion{
				{Label: "airflow work", Text: "title CONTAINS 'airflow'", Kind: kind, ID: 7},
			},
		})

		next, msgs := tap(q, "tab")
		q = next.(*queryScreen)
		if got := q.input.Value(); got != "title CONTAINS 'airflow'" {
			t.Fatalf("%s: accepting should replace the line, got %q", kind, got)
		}
		var ran *queryRanMsg
		for i := range msgs {
			if m, ok := msgs[i].(queryRanMsg); ok {
				ran = &m
			}
		}
		if ran == nil || len(ran.notes) != 1 {
			t.Fatalf("%s: accepting should run the query, got %+v", kind, ran)
		}
	}
}

// chord sends a modified keypress — ctrl+s, shift+delete — the way the
// terminal reports it, and drains what it produced.
func chord(s screen, code rune, mod tea.KeyMod) (screen, []tea.Msg) {
	next, cmd := s.Update(tea.KeyPressMsg{Code: code, Mod: mod})
	return next, drainCmd(cmd)
}

// storedRows installs a completion list of one example and two stored rows,
// the shape an empty box gets from the real completer when the user has
// history. Built by hand because the fake's completer has no saved_queries
// table behind it.
func storedRows(q *queryScreen) {
	q.adopt(&models.QueryCompletion{
		Suggestions: []models.QuerySuggestion{
			{Label: "airflow work", Text: "title CONTAINS 'airflow'", Kind: "saved", ID: 7},
			{Label: "tag = 'x'", Text: "tag = 'x'", Kind: "history", ID: 9},
			{Label: "flagged notes", Text: "flagged = true", Kind: "example"},
		},
	})
}

// ctrl+s opens a name prompt, and submitting it saves the query that was in the
// box — not whatever the box holds by the time the name is typed.
func TestCtrlSSavesTheQueryUnderAName(t *testing.T) {
	store := newFakeStore()
	q := newQueryScreen(querySession(store), "")
	q = typeQuery(q, "title CONTAINS 'dag'")

	_, msgs := chord(q, 's', tea.ModCtrl)
	var prompt *promptScreen
	for _, m := range msgs {
		if p, ok := m.(pushMsg); ok {
			prompt, _ = p.s.(*promptScreen)
		}
	}
	if prompt == nil {
		t.Fatalf("ctrl+s should push a name prompt, got %#v", msgs)
	}
	if prompt.input.Value() != "" {
		t.Fatalf("an unsaved query should open an empty prompt, got %q", prompt.input.Value())
	}

	q.input.SetValue("changed meanwhile")
	for _, m := range drainCmd(prompt.onSubmit("dags")) {
		next, _ := q.Update(m)
		q = next.(*queryScreen)
	}
	if len(store.saved) != 1 || store.saved[0].Name != "dags" || store.saved[0].Query != "title CONTAINS 'dag'" {
		t.Fatalf("saved = %+v", store.saved)
	}
	if !strings.Contains(q.notice, "dags") || !strings.Contains(q.View(), "saved as") {
		t.Fatalf("a save should be acknowledged, notice = %q", q.notice)
	}
}

// Re-saving a query that is already saved opens the prompt on its name, since
// saving over a name is how a saved query is edited.
func TestSavePrefillsTheNameOfASavedQuery(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	storedRows(q)
	q.input.SetValue("title CONTAINS 'airflow'")

	_, msgs := chord(q, 's', tea.ModCtrl)
	for _, m := range msgs {
		if p, ok := m.(pushMsg); ok {
			if got := p.s.(*promptScreen).input.Value(); got != "airflow work" {
				t.Fatalf("prompt should open on the saved name, got %q", got)
			}
			return
		}
	}
	t.Fatal("ctrl+s pushed no prompt")
}

// There is nothing to name in an empty box; say so instead of asking for a name
// the store would refuse anyway.
func TestSavingAnEmptyQueryIsRefusedUpFront(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	_, msgs := chord(q, 's', tea.ModCtrl)
	for _, m := range msgs {
		if _, ok := m.(pushMsg); ok {
			t.Fatal("an empty box should not open the name prompt")
		}
	}
	if q.errText == "" {
		t.Fatal("an empty save should explain itself")
	}
}

// A query that does not parse is not stored, and the refusal points at the
// mistake the same way a failed run does.
func TestSavingABrokenQueryUnderlinesTheMistake(t *testing.T) {
	store := newFakeStore()
	q := newQueryScreen(querySession(store), "")
	q = typeQuery(q, "title = ")

	for _, m := range drainCmd(saveQueryCmd(store, "broken", "title = ", "u")) {
		next, _ := q.Update(m)
		q = next.(*queryScreen)
	}
	if len(store.saved) != 0 {
		t.Fatalf("a broken query was saved: %+v", store.saved)
	}
	if q.qerr == nil {
		t.Fatalf("a syntax error should be positioned, errText = %q", q.errText)
	}
	if q.notice != "" {
		t.Fatalf("a refused save must not be acknowledged, notice = %q", q.notice)
	}

	// A refusal that is not about syntax is a sentence.
	for _, m := range drainCmd(saveQueryCmd(store, "  ", "title = 'x'", "u")) {
		next, _ := q.Update(m)
		q = next.(*queryScreen)
	}
	if !strings.Contains(q.errText, "needs a name") {
		t.Fatalf("errText = %q", q.errText)
	}
}

// shift+delete — and its ctrl+x twin — forgets the highlighted stored row by
// its id, drops it from the list at once, and asks for the list again once the
// store has answered.
func TestForgetRemovesTheHighlightedStoredRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		code rune
		mod  tea.KeyMod
	}{
		{"shift+delete", tea.KeyDelete, tea.ModShift},
		{"ctrl+x", 'x', tea.ModCtrl},
	} {
		store := newFakeStore()
		q := newQueryScreen(querySession(store), "")
		storedRows(q)
		q.highlight = 1 // the history row

		next, msgs := chord(q, tc.code, tc.mod)
		q = next.(*queryScreen)
		if len(store.forgot) != 1 || store.forgot[0] != 9 {
			t.Fatalf("%s: forgot = %v, want [9]", tc.name, store.forgot)
		}
		if len(q.sugg) != 2 || q.sugg[1].Kind != "example" {
			t.Fatalf("%s: the row should leave the list at once, got %+v", tc.name, q.sugg)
		}

		var reasked bool
		for _, m := range msgs {
			next, cmd := q.Update(m)
			q = next.(*queryScreen)
			for _, mm := range drainCmd(cmd) {
				if _, ok := mm.(queryCompletionMsg); ok {
					reasked = true
				}
			}
		}
		if !reasked {
			t.Fatalf("%s: a forget should re-request completions", tc.name)
		}
		if !strings.Contains(q.notice, "tag = 'x'") {
			t.Fatalf("%s: notice = %q", tc.name, q.notice)
		}
	}
}

// Forget acts only on a stored row. On an example it is swallowed: nothing is
// deleted, and the text is not edited either.
func TestForgetIgnoresRowsThatAreNotStored(t *testing.T) {
	store := newFakeStore()
	q := newQueryScreen(querySession(store), "")
	q.input.SetValue("abc")
	q.input.SetCursor(1)
	storedRows(q)
	q.highlight = 2 // the example

	next, _ := chord(q, tea.KeyDelete, tea.ModShift)
	q = next.(*queryScreen)
	if len(store.forgot) != 0 {
		t.Fatalf("forgot = %v on an example row", store.forgot)
	}
	if len(q.sugg) != 3 || q.input.Value() != "abc" {
		t.Fatalf("nothing should change, sugg=%d value=%q", len(q.sugg), q.input.Value())
	}
}

// The footer names forget only while it would do something.
func TestFooterOffersForgetOnlyOnStoredRows(t *testing.T) {
	q := newQueryScreen(querySession(newFakeStore()), "")
	storedRows(q)

	q.highlight = 0
	if !strings.Contains(q.View(), "forget") {
		t.Fatal("a saved row should advertise forget")
	}
	q.highlight = 2
	if strings.Contains(q.View(), "forget") {
		t.Fatal("an example row should not advertise forget")
	}
}

// The whole loop against the real local store: a save shows up as a ☆ row on an
// empty box (through the real completer and saved_queries table), shift+delete
// removes it, and forgetting an id that is already gone is not an error. The
// fake-store tests above pin the screen; this pins that the pass-throughs reach
// bytdb and that the completer hands back the ids forget depends on.
func TestSaveAndForgetAgainstTheLocalStore(t *testing.T) {
	user := setupTestDB(t)
	store := NewLocalStore()
	sess := querySession(store)
	sess.user = user

	if _, err := store.SaveQuery("dags", "title CONTAINS 'dag'", user.GUID); err != nil {
		t.Fatalf("SaveQuery: %v", err)
	}
	var qe *models.QueryError
	if _, err := store.SaveQuery("broken", "title = ", user.GUID); !errors.As(err, &qe) {
		t.Fatalf("a broken query should be refused with a QueryError, got %v", err)
	}

	q := newQueryScreen(sess, "")
	q = settle(q, drainCmd(q.complete()))
	if len(q.sugg) == 0 || q.sugg[0].Kind != "saved" || q.sugg[0].Label != "dags" || q.sugg[0].ID == 0 {
		t.Fatalf("an empty box should lead with the saved query, got %+v", q.sugg)
	}
	id := q.sugg[0].ID

	next, msgs := chord(q, tea.KeyDelete, tea.ModShift)
	q = next.(*queryScreen)
	for _, m := range msgs {
		next, cmd := q.Update(m)
		q = settle(next.(*queryScreen), drainCmd(cmd))
	}
	for _, sg := range q.sugg {
		if sg.Kind == "saved" {
			t.Fatalf("the saved query survived a forget: %+v", q.sugg)
		}
	}
	if q.errText != "" {
		t.Fatalf("forget reported %q", q.errText)
	}

	if err := store.DeleteSavedQuery(id, user.GUID); err != nil {
		t.Fatalf("forgetting a row that is already gone should succeed, got %v", err)
	}
}
