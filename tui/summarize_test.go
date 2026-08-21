package tui

import (
	"errors"
	"strings"
	"testing"

	"gonotes/models"
	"gonotes/summarize"

	tea "charm.land/bubbletea/v2"
)

// Tests for the summarize doors. Four things can go wrong here and each has a
// test below:
//
//	the wrong text is sent          (the title instead of the body, say)
//	a result overwrites something the user typed
//	a result is dropped because the user navigated while it was in flight
//	a failure is reported as if it were the feature's fault
//
// The model call itself is fakeStore's, so none of this costs anything.

func summarizeSession(store Store) *session {
	return &session{
		store:  store,
		cats:   newCatsState(),
		sync:   &syncState{},
		user:   &models.User{GUID: "u"},
		width:  100,
		height: 30,
	}
}

// withClipboard points the clipboard reader at a stub, so a test never touches
// (or depends on) the real pasteboard of the machine running it.
func withClipboard(t *testing.T, text string) {
	t.Helper()
	prev := clipboardReaders
	// `printf %s` is the smallest tool that returns arbitrary text verbatim on
	// stdout, and it exists on every platform this suite runs on.
	clipboardReaders = []clipboardReader{{name: "printf", args: []string{"%s", text}}}
	t.Cleanup(func() { clipboardReaders = prev })
}

// The form door must send what is in the BODY. Sending the whole form, or the
// title, would summarize the wrong thing and the mistake would be invisible in
// the result.
func TestFormSummarizeSendsTheBody(t *testing.T) {
	store := newFakeStore()
	f := newFormScreen(summarizeSession(store), nil)
	f.title.SetValue("A title the user typed")
	f.body.SetValue("a long body worth condensing")

	for _, msg := range drainCmd(f.summarize()) {
		_ = msg
	}
	if store.summarizedText != "a long body worth condensing" {
		t.Fatalf("summarized %q", store.summarizedText)
	}
	if !f.summarizing {
		t.Fatal("the form should be marked summarizing while the call is in flight")
	}
}

func TestFormSummarizeRefusesAnEmptyBody(t *testing.T) {
	store := newFakeStore()
	f := newFormScreen(summarizeSession(store), nil)
	f.body.SetValue("   \n ")

	drainCmd(f.summarize())
	if store.summarizeCalls != 0 {
		t.Fatal("an empty body must not reach the summarizer")
	}
}

// A title the user typed is their own words; the generated one came from a
// request that was only about condensing the text. Same rule as gn-clip's -t.
func TestApplySummaryKeepsWhatTheUserTyped(t *testing.T) {
	f := newFormScreen(summarizeSession(newFakeStore()), nil)
	f.title.SetValue("My own title")
	f.desc.SetValue("My own description")
	f.body.SetValue("the original long text")

	f.applySummary(&summarize.Result{
		Title:       "Generated title",
		Description: "Generated description",
		Body:        "the summary",
	})

	if f.title.Value() != "My own title" {
		t.Errorf("title = %q, want the user's own", f.title.Value())
	}
	if f.desc.Value() != "My own description" {
		t.Errorf("description = %q, want the user's own", f.desc.Value())
	}
	if f.body.Value() != "the summary" {
		t.Errorf("body = %q, want the summary — replacing it is the request", f.body.Value())
	}
}

func TestApplySummaryFillsEmptyFields(t *testing.T) {
	f := newFormScreen(summarizeSession(newFakeStore()), nil)
	f.body.SetValue("the original long text")

	line := f.applySummary(&summarize.Result{
		Title:       "Generated title",
		Description: "Generated description",
		Body:        "the summary",
	})

	if f.title.Value() != "Generated title" || f.desc.Value() != "Generated description" {
		t.Fatalf("title = %q, description = %q", f.title.Value(), f.desc.Value())
	}
	// The user has to be told the body was replaced — it is the one change here
	// they did not watch happen.
	if !strings.Contains(line, "Body replaced") {
		t.Errorf("status = %q, want it to name the body replacement", line)
	}
}

// The clipboard door: read locally, summarize through the store, land in a new
// UNSAVED note carrying the summary tag.
func TestClipboardSummaryOpensAnUnsavedForm(t *testing.T) {
	withClipboard(t, "a paste worth condensing")
	store := newFakeStore()
	store.summarizeResult = &summarize.Result{
		Title:       "Condensed",
		Description: "Why this note exists",
		Body:        "the summary",
	}

	msgs := drainCmd(summarizeClipboardCmd(store))
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	done, ok := msgs[0].(summarizeDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("got %#v", msgs[0])
	}
	if store.summarizedText != "a paste worth condensing" {
		t.Fatalf("summarized %q", store.summarizedText)
	}

	m := appModel{sess: summarizeSession(store), stack: []screen{newBrowseScreen(summarizeSession(store))}}
	var pushed *formScreen
	for _, msg := range drainCmd(m.summarizeDone(done)) {
		if p, ok := msg.(pushMsg); ok {
			pushed, _ = p.s.(*formScreen)
		}
	}
	if pushed == nil {
		t.Fatal("no form was pushed")
	}
	if pushed.editing != nil {
		t.Error("the summary must open a NEW note, not an edit of an existing one")
	}
	if pushed.title.Value() != "Condensed" || pushed.body.Value() != "the summary" {
		t.Errorf("form prefilled with title %q body %q", pushed.title.Value(), pushed.body.Value())
	}
	if pushed.desc.Value() != "Why this note exists" {
		t.Errorf("description = %q", pushed.desc.Value())
	}
	if pushed.tags.Value() != summarizeTag {
		t.Errorf("tags = %q, want %q so summaries stay findable", pushed.tags.Value(), summarizeTag)
	}
	if !pushed.dirty() {
		t.Error("a prefilled form must count as dirty — esc has to offer to save it")
	}
}

// An empty clipboard is a real outcome, not a summarizer fault, and has to be
// reported in the clipboard's own terms.
func TestEmptyClipboardIsReportedAsSuch(t *testing.T) {
	withClipboard(t, "   \n")
	store := newFakeStore()

	msgs := drainCmd(summarizeClipboardCmd(store))
	done := msgs[0].(summarizeDoneMsg)
	if store.summarizeCalls != 0 {
		t.Fatal("an empty clipboard must not reach the summarizer")
	}

	m := appModel{sess: summarizeSession(store), stack: []screen{newBrowseScreen(summarizeSession(store))}}
	note := statusOf(t, m.summarizeDone(done))
	if !strings.Contains(note.text, "clipboard is empty") {
		t.Fatalf("status = %q", note.text)
	}
	if note.isErr {
		t.Error("an empty clipboard is not an error state")
	}
}

// A summary that lands after the user has left the form is dropped — but never
// silently, and never onto whatever screen replaced it.
func TestFormSummaryLandingElsewhereIsReported(t *testing.T) {
	sess := summarizeSession(newFakeStore())
	m := appModel{sess: sess, stack: []screen{newBrowseScreen(sess)}}

	note := statusOf(t, m.summarizeDone(summarizeDoneMsg{
		target: summarizeIntoForm,
		res:    &summarize.Result{Title: "T", Body: "B"},
	}))
	if !strings.Contains(note.text, "discarded") {
		t.Fatalf("status = %q", note.text)
	}
}

func TestSummarizeFailureIsOneStatusLine(t *testing.T) {
	sess := summarizeSession(newFakeStore())
	m := appModel{sess: sess, stack: []screen{newBrowseScreen(sess)}}

	note := statusOf(t, m.summarizeDone(summarizeDoneMsg{
		target: summarizeIntoNewNote,
		err:    errors.New("the summarizer did not return usable JSON"),
	}))
	if !note.isErr || !strings.Contains(note.text, "usable JSON") {
		t.Fatalf("status = %+v, want the summarizer's own message", note)
	}
}

// statusOf extracts the single statusNote a command produced.
func statusOf(t *testing.T, cmd tea.Cmd) statusNote {
	t.Helper()
	for _, msg := range drainCmd(cmd) {
		if note, ok := msg.(statusNote); ok {
			return note
		}
	}
	t.Fatal("no status line was produced")
	return statusNote{}
}
