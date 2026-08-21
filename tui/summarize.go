package tui

import (
	"strings"

	"gonotes/summarize"

	tea "charm.land/bubbletea/v2"
)

// Summarize-into-a-note, the TUI half. Two doors, one model call:
//
//	browse   ctrl+r  read the system clipboard, summarize it, and open a NEW
//	                 note prefilled with the result — the interactive twin of
//	                 gn-clip.sh -s.
//	form     ctrl+r  summarize what is in the body field RIGHT NOW, in place.
//
// The second is a superset of the first once something has been pasted into the
// form, and the pair covers both ways a long text arrives: from the clipboard,
// or already typed/pasted into a note that grew too long to be useful.
//
// WHERE THE WORK HAPPENS. The clipboard is always read HERE — it belongs to the
// person at the keyboard. The summary is produced by the Store, so it runs
// wherever the notes do: in this process in local mode, on the server in HTTP
// mode. See the Summarize doc in store.go.
//
// THREE RULES, all learned from the capture feature next door (capture.go):
//
//   - THE RESULT IS NEVER SAVED. A summary lands in an unsaved form and waits
//     for ctrl+s. What the model wrote is a proposal; whether it is worth
//     keeping is the user's call, and a note written without that call is a
//     note the user finds days later and cannot check against a source that is
//     long gone from the clipboard.
//   - EVERY RESULT IS DELIVERED AT THE ROOT. A summary takes seconds, and the
//     user is free to move while it runs. Routing it to whatever screen ended
//     up on top would mean a summary the user explicitly asked for vanishing
//     because they pressed esc while waiting.
//   - A FAILURE IS ONE STATUS LINE AND NOTHING ELSE. The clipboard and the
//     form body are untouched on every error path, so a retry costs only the
//     model call.

// summarizeTarget says what should be done with a finished summary. It is
// decided when the request is MADE, not when it lands, because by the time it
// lands the screen that asked may be gone.
type summarizeTarget int

const (
	// summarizeIntoNewNote opens a new prefilled form. The clipboard door.
	summarizeIntoNewNote summarizeTarget = iota
	// summarizeIntoForm rewrites the open form's body. The form door.
	summarizeIntoForm
)

// summarizeStartedMsg carries the clipboard read back to the event loop so the
// status line can say how much is being summarized before the model call
// begins. Without it the user presses ctrl+r and watches nothing happen for
// several seconds.
type summarizeStartedMsg struct {
	chars  int
	target summarizeTarget
}

// summarizeDoneMsg is one finished summary, successful or not.
type summarizeDoneMsg struct {
	target summarizeTarget
	res    *summarize.Result
	err    error
}

// summarizeClipboardCmd is the browse door: read the clipboard, then summarize
// it. Both halves run on the command goroutine — reading a large clipboard
// shells out to pbpaste, which is fast but not free.
//
// The empty case is a real outcome rather than a failure, and gets its own
// message so the user is told "your clipboard is empty" instead of watching an
// error scroll by that describes the summarizer.
func summarizeClipboardCmd(store Store) tea.Cmd {
	return func() tea.Msg {
		text, err := readClipboard()
		if err != nil {
			return summarizeDoneMsg{target: summarizeIntoNewNote, err: err}
		}
		if strings.TrimSpace(text) == "" {
			return summarizeDoneMsg{target: summarizeIntoNewNote,
				err: errEmptyClipboard}
		}
		res, err := store.Summarize(text, "")
		return summarizeDoneMsg{target: summarizeIntoNewNote, res: res, err: err}
	}
}

// summarizeTextCmd is the form door: summarize text already in hand.
func summarizeTextCmd(store Store, text string, target summarizeTarget) tea.Cmd {
	return func() tea.Msg {
		res, err := store.Summarize(text, "")
		return summarizeDoneMsg{target: target, res: res, err: err}
	}
}

// summarizeDone installs a finished summary. Runs on the event loop, from the
// root Update.
func (m appModel) summarizeDone(msg summarizeDoneMsg) tea.Cmd {
	// The form's spinner-equivalent has to come down on every path, including
	// the ones where the form is no longer on screen (in which case this finds
	// nothing and does nothing).
	if f, ok := m.top().(*formScreen); ok {
		f.summarizing = false
	}

	if msg.err != nil {
		// The empty clipboard is reported in the clipboard's own terms. Wrapping
		// it as "Summarize failed" would send the user looking at the summarizer
		// for a problem that is one ⌘C away from fixed.
		if _, empty := msg.err.(clipboardEmptyError); empty {
			return status("Nothing to summarize — the clipboard is empty")
		}
		return statusErr(msg.err, "Summarize failed")
	}

	if msg.target == summarizeIntoForm {
		f, ok := m.top().(*formScreen)
		if !ok {
			// The user left the form while the model was working. Nothing is
			// lost — the body they were editing is exactly as they left it —
			// but they asked for something and deserve to hear what became of
			// it rather than watch it disappear.
			return status("Summary discarded — the note form was closed")
		}
		return status(f.applySummary(msg.res))
	}

	f := newFormScreen(m.sess, nil)
	f.prefill(msg.res.Title, summarizeTag, msg.res.Body)
	if msg.res.Description != "" {
		f.desc.SetValue(msg.res.Description)
	}
	return tea.Batch(push(f), status("Summarized from the clipboard — ctrl+s to save"))
}

// summarizeTag marks notes that came in through a summarizer, the way
// captureTag marks the ones that came out of an agent pane. It is what makes
// "which of these did I read and which did a model condense for me" a filter
// rather than a memory exercise — a distinction worth keeping, since a summary
// is a claim about a text rather than the text.
const summarizeTag = "summary"

// errEmptyClipboard is its own value so the empty case can be reported in the
// clipboard's terms. Declared here rather than inline so a test can match it.
var errEmptyClipboard = clipboardEmptyError{}

type clipboardEmptyError struct{}

func (clipboardEmptyError) Error() string { return "the clipboard is empty" }
