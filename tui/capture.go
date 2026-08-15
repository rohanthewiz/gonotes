package tui

import (
	"strconv"
	"strings"
	"time"

	"gonotes/cats"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Capture-to-note: the inbound half of the cats integration.
//
// Every other cats feature in GoNotes flows outward — the pane badge, the
// window title, the host's palette. This one runs the other way: ctrl+g in the
// note list reads the text out of a SIBLING agent's pane and opens a new note
// prefilled with it. The answer claude just printed next door becomes a note
// without a trip through the system clipboard, a mouse selection, or a
// terminal that may not even support one.
//
// FOUR RULES CARRY THIS FILE:
//
//   - THE PANE LIST IS CACHED, because the picker is opened by a KEYSTROKE and
//     a keystroke must not dial a socket. It is primed when Tier 1 comes up,
//     refreshed when the picker opens (for the NEXT open), and invalidated by
//     the pane events that mean the answer changed.
//   - ONLY AGENT PANES ARE OFFERED, and never our own. A plain shell's
//     scrollback is a prompt and a directory listing, which is not a note; and
//     GoNotes reports ITSELF to cats as the agent "gonotes", so without the
//     self-exclusion the picker would offer to capture the note list into a
//     note.
//   - THE CAPTURE IS SANITIZED, NOT PASTED. A terminal buffer is a rectangle:
//     rows are padded with spaces to the pane width, wrapped lines carry no
//     markers, and control bytes are ordinary content. A note stores markdown,
//     so what lands in the body is the text with that rectangle taken back off.
//   - NOTHING HERE IS GATED BEHIND A FAILURE. Below Tier 1, ctrl+g answers with
//     one status line and the browse screen is otherwise unchanged — the same
//     silent degradation the rest of the integration keeps.
//
// The capture verb itself landed in Phase 5 with the client (cats/client.go);
// this is its consumer.

// panePollMin rate-limits the pane.list refresh. Agent state changes a few
// times a minute at most and every refresh is a round trip, so a burst of pane
// events (a tab opening four panes at once) costs one call rather than four.
const panePollMin = 2 * time.Second

// captureLines is how much scrollback a capture reaches back through, on top of
// the visible screen. Chosen against what the feature is FOR: an agent's last
// answer, not its whole session. A few hundred lines covers a long reply with
// its code blocks; the whole buffer would bury it in the conversation that led
// there, and the user would have to delete more than they kept.
const captureLines = 200

// captureTag is the tag every captured note carries, so "what did the agents
// tell me" is one "/capture" away in the fuzzy filter.
const captureTag = "capture"

// captureHint advertises the door. It is a one-off status line at Tier-1-up
// rather than a row in the browse footer, and the distinction is deliberate:
// the footer is rendered in every terminal, and a permanent "ctrl+g capture"
// hint in a plain shell would advertise a key whose only answer is that the
// feature is unavailable. This says it exactly where and when it is true.
const captureHint = "cats: ctrl+g captures a sibling agent pane into a note"

// ---- messages --------------------------------------------------------------

// catsPanesMsg is a pane.list result on its way back to the event loop. It
// carries the error rather than dropping it into a nil message, because a
// tea.Cmd returning nil still posts nil into Update, where every screen's type
// switch falls through to its widget.
type catsPanesMsg struct {
	panes []cats.PaneInfo
	err   error
}

// captureDoneMsg is one finished capture. agent is carried along because the
// note's title names it and the pane it came from may already be gone by the
// time this lands.
type captureDoneMsg struct {
	agent string
	text  string
	err   error
}

// ---- the pane cache --------------------------------------------------------

// pollPanes refreshes the cached pane list, unless it was refreshed recently.
// Returns nil when there is nothing to do, which tea.Batch drops.
//
// The rate-limit check and the timestamp write both happen HERE, on the event
// loop, and only the round trip itself is in the returned command — which is
// what keeps the cache free of the locking that a goroutine touching catsState
// would need. See the threading rule at the top of cats_glue.go.
func (cs *catsState) pollPanes(force bool) tea.Cmd {
	if !cs.tier1() {
		return nil
	}
	if !force && time.Since(cs.panesAt) < panePollMin {
		return nil
	}
	cs.panesAt = time.Now()
	client := cs.client // closed over, so the command never reads cs
	return func() tea.Msg {
		panes, err := client.PaneList()
		return catsPanesMsg{panes: panes, err: err}
	}
}

// setPanes installs a refresh result. A failed call leaves the cache alone: a
// stale pane list is a picker with one wrong row, and an emptied one is a
// picker with no rows at all.
func (cs *catsState) setPanes(msg catsPanesMsg) {
	if cs == nil || msg.err != nil {
		return
	}
	cs.panes = msg.panes
}

// agentPanes is the cached list filtered to capture targets and ordered by how
// much each one wants attention.
//
// Excluding our own pane is not cosmetic. GoNotes reports itself to cats as the
// agent "gonotes" (cats/hooks.go), so it appears in pane.list exactly like
// claude does — and capturing GoNotes into GoNotes would produce a note
// containing the note list.
func (cs *catsState) agentPanes() []cats.PaneInfo {
	if cs == nil {
		return nil
	}
	var out []cats.PaneInfo
	for _, p := range cs.panes {
		if p.Agent == "" || cs.isSelf(p) {
			continue
		}
		out = append(out, p)
	}
	// Insertion sort: the list is a handful of panes, and this keeps the order
	// stable so equal-ranked rows do not shuffle between openings — a picker
	// whose rows move under a user who has learned their positions is worse
	// than one that is occasionally ordered oddly.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			ri, rj := agentRank(out[j].AgentState), agentRank(out[j-1].AgentState)
			if ri < rj || (ri == rj && out[j].Pane > out[j-1].Pane) {
				break
			}
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// agentRank orders the picker. A blocked agent is the one that just stopped and
// is waiting to be read; an idle one has been finished for a while. Ranking by
// that means the row the user most likely wants is the one already selected.
func agentRank(state string) int {
	switch state {
	case cats.StateBlocked:
		return 3
	case cats.StateWorking:
		return 2
	case cats.StateIdle:
		return 1
	}
	return 0
}

// isSelf reports whether a pane is the one GoNotes is running in. The numeric
// id is the reliable answer — it came from the startup pane.list resolution —
// and the public handle is the fallback for a host that did not resolve.
func (cs *catsState) isSelf(p cats.PaneInfo) bool {
	if cs.selfOK && p.Pane == cs.self {
		return true
	}
	return p.Handle != "" && p.Handle == cs.caps.PaneHandle
}

// ---- the picker ------------------------------------------------------------

// agentPickerScreen is the modal "capture from which pane?" list.
//
// It is hand-rendered rather than built on bubbles/list, and the reason is that
// it holds nothing: three or four rows, no filtering, no pagination. Every
// color it draws with is read fresh from the package styles on each frame, so
// unlike the two list screens it needs no restyle() — a palette change repaints
// it for free. The precedent is confirmScreen, for the same reason.
//
// The pane rows are a SNAPSHOT taken when the picker is constructed. A refresh
// that lands while it is open does not rewrite the list under the user's
// cursor; it is there for the next opening.
type agentPickerScreen struct {
	sess   *session
	panes  []cats.PaneInfo
	cursor int
}

func newAgentPickerScreen(sess *session) *agentPickerScreen {
	return &agentPickerScreen{sess: sess, panes: sess.cats.agentPanes()}
}

func (s *agentPickerScreen) Init() tea.Cmd { return nil }

func (s *agentPickerScreen) selected() *cats.PaneInfo {
	if s.cursor < 0 || s.cursor >= len(s.panes) {
		return nil
	}
	return &s.panes[s.cursor]
}

func (s *agentPickerScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return s, nil
	}
	switch {
	case key.Matches(k, keys.Back):
		return s, pop(false)

	case key.Matches(k, keys.Move):
		// One binding covers both arrows so the footer reads "↑/↓ move" rather
		// than as two rows; the code is what says which way.
		if k.Code == tea.KeyUp {
			s.cursor--
		} else {
			s.cursor++
		}
		if s.cursor < 0 {
			s.cursor = len(s.panes) - 1
		}
		if s.cursor >= len(s.panes) {
			s.cursor = 0
		}
		return s, nil

	case key.Matches(k, keys.Pick):
		p := s.selected()
		if p == nil {
			return s, pop(false)
		}
		// Pop FIRST, then capture. The result is handled by the root (see
		// appModel.captureDone), so it does not matter which screen is on top
		// when it lands — but the picker has done its job the moment the pane
		// is chosen, and leaving a modal up for the seconds a capture can take
		// would read as a hang.
		//
		// tea.Sequence rather than tea.Batch: the "Capturing…" line has to be
		// on screen while the round trip is in flight, not racing it.
		return s, tea.Sequence(
			pop(false),
			status("Capturing from "+agentName(*p)+"…"),
			captureCmd(s.sess.cats.client, *p),
		)
	}
	return s, nil
}

// pickerWidth is the modal's inner width, and pickerNameWidth the agent-name
// column inside it. Fixed rather than derived from the terminal: the content is
// two short fields, and a dialog that grows to 200 columns on a wide screen
// looks like a layout bug.
const (
	pickerWidth     = 46
	pickerNameWidth = 16
)

func (s *agentPickerScreen) View() string {
	var b strings.Builder
	b.WriteString(labelFocusedStyle.Render("Capture from an agent pane"))
	b.WriteString("\n\n")
	for i, p := range s.panes {
		b.WriteString(s.row(i, p) + "\n")
	}
	b.WriteString("\n" + renderHelp(keys.agentPickerHelp()...))

	box := dialogBoxStyle.Render(b.String())
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, box)
}

// row renders one pane: the agent's name, then its state and where it lives.
//
// The whole row is styled once, at full width, rather than styling the two
// fields separately. That is what makes the selection a continuous bar instead
// of a highlight with gaps where the padding is.
func (s *agentPickerScreen) row(i int, p cats.PaneInfo) string {
	state := p.AgentState
	if state == "" {
		state = cats.StateUnknown
	}
	where := p.Handle
	if where == "" {
		where = "pane " + strconv.FormatUint(uint64(p.Pane), 10)
	}
	name := lipgloss.NewStyle().Width(pickerNameWidth).
		Render(truncate(agentName(p), pickerNameWidth-1))

	st := lipgloss.NewStyle().Width(pickerWidth)
	if i == s.cursor {
		st = st.Background(colorSel).Foreground(colorFg).Bold(true)
	} else {
		st = st.Foreground(colorSubtle)
	}
	return st.Render(truncate(name+state+" · "+where, pickerWidth))
}

// agentName is the label for a pane. cats always fills Agent for a pane it has
// identified — which is the only kind the picker offers — so the fallback is
// for a row that arrived between the filter and the render.
func agentName(p cats.PaneInfo) string {
	if a := strings.TrimSpace(p.Agent); a != "" {
		return a
	}
	return "agent pane"
}

var _ screen = (*agentPickerScreen)(nil)

// ---- the capture itself ----------------------------------------------------

// captureCmd reads a pane's recent buffer. Runs on a command goroutine, which
// is why the client is handed in rather than read off catsState.
//
// CaptureRecent (scope 1) rather than CaptureVisible: what the user wants is
// the answer an agent just gave, and by the time they reach for ctrl+g the top
// of it has usually scrolled off. Ansi and Unwrap stay off — see the note on
// cats.CaptureParams for why a note wants neither.
func captureCmd(client *cats.Client, p cats.PaneInfo) tea.Cmd {
	agent := agentName(p)
	pane := p.Pane
	return func() tea.Msg {
		text, err := client.Capture(pane, cats.CaptureRecent, captureLines)
		return captureDoneMsg{agent: agent, text: text, err: err}
	}
}

// captureDone turns a finished capture into a prefilled note form. Runs on the
// event loop, from the root Update.
//
// It is handled at the ROOT rather than on the browse screen, unlike every
// other data message in this package. A capture takes seconds — cats forwards
// it to the cathost daemon — and the user is free to open a note or a category
// in the meantime. Delivering to whatever screen happens to be on top would
// mean a capture the user explicitly asked for vanishing because they navigated
// while it was in flight.
func (m appModel) captureDone(msg captureDoneMsg) tea.Cmd {
	if msg.err != nil {
		return statusErr(msg.err, "Capture failed")
	}
	text := sanitizeCapture(msg.text)
	if text == "" {
		// A real outcome, not a failure: a pane that has printed nothing since
		// it started captures as a rectangle of spaces.
		return status("Nothing to capture — " + msg.agent + "'s pane is empty")
	}

	f := newFormScreen(m.sess, nil)
	f.prefill(captureTitle(msg.agent), captureTag, text)
	// The form is left UNSAVED deliberately — the same rule the outbound half
	// keeps, where pane.send_input stages text without pressing Enter. What was
	// captured is the agent's words; whether they are worth keeping is the
	// user's call, one ctrl+s away.
	//
	// Batch rather than Sequence: the two are independent (one pushes a screen,
	// the other writes the status line) and neither reads what the other did.
	return tea.Batch(push(f), status("Captured from "+msg.agent+" — ctrl+s to save"))
}

// captureNow is the clock the capture title is stamped from. A var so a test
// can pin it; it is not a setting.
var captureNow = time.Now

// captureTitle names a captured note. The agent and the moment are the two
// things that identify it — the body is a terminal transcript, which rarely has
// a usable heading of its own — and the timestamp sorts sensibly when several
// captures from the same agent pile up.
func captureTitle(agent string) string {
	who := strings.TrimSpace(agent)
	if who == "" {
		who = "agent pane"
	}
	return "Capture: " + who + " — " + captureNow().Format("2006-01-02 15:04")
}

// sanitizeCapture turns a terminal buffer into markdown-shaped text.
//
// Four things have to come off, and each one is a rectangle artifact rather
// than content:
//
//	escapes           capture is requested with ansi:false, so cats has already
//	                  stripped styling — but anything that got in another way
//	                  has to go as a SEQUENCE, not as a byte. Dropping the ESC
//	                  alone is worse than leaving it: it turns an invisible
//	                  "\x1b[2K" into a visible "[2K" in the note's text.
//	trailing spaces   every row is padded to the pane's width, so a capture
//	                  pasted raw carries dozens of invisible columns per line —
//	                  which markdown then reads as hard line breaks in places
//	                  the agent never put any.
//	CR                a pty writes \r\n; a note stores \n.
//	control bytes     what survives the sequence strip: a stray BEL, a lone
//	                  cursor move. Tab is kept — it is layout an agent wrote on
//	                  purpose.
//
// Leading and trailing blank lines go too — those are the empty rows above and
// below whatever the pane last printed, and they would open the note with a
// screenful of nothing.
func sanitizeCapture(s string) string {
	s = stripEscapeSequences(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(stripControlBytes(ln), " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// stripEscapeSequences removes ESC-introduced sequences whole.
//
// This is a scanner rather than a regexp for one reason worth stating: the
// terminating byte of a CSI sequence is defined by its RANGE (0x40-0x7E after
// any number of parameter and intermediate bytes), and an OSC's by either BEL
// or ST — so the "what ends this" rule differs per introducer, which a scanner
// expresses directly and a pattern only approximates.
//
// It is deliberately not a full VT parser. DCS and the other string-terminated
// introducers fall into the default branch and lose two bytes rather than their
// whole payload. They cannot reach here from a capture — cats answers ansi:false
// by stripping styling itself, and this is the second line of defense — so
// buying the remaining fidelity would mean carrying a parser for a case that
// does not occur.
func stripEscapeSequences(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s // the overwhelmingly common case, at no cost
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++ // past the ESC
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: parameters and intermediates, then one final byte
			i++
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
				i++
			}
			if i < len(s) {
				i++ // the final byte, 0x40-0x7E
			}
		case ']': // OSC: runs to BEL or to ST (ESC \)
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			i++ // a two-byte escape (charset selects, ESC =, ESC >)
		}
	}
	return b.String()
}

// stripControlBytes drops the C0 controls and DEL from one line, keeping tab.
//
// Deliberately separate from titleSafe in cats_glue.go despite the overlap:
// that one guards an escape sequence's parser and so must drop tabs and trim,
// while this one is preserving a document's layout. Merging them would mean one
// function serving two rules that will not stay the same.
func stripControlBytes(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
