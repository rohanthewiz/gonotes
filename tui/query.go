package tui

import (
	"errors"
	"strings"
	"unicode/utf8"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// queryScreen is the terminal half of the advanced search: one line of
// SQL-shaped query text, a completion list under it, and the field catalog
// behind ctrl+t.
//
//	┌─ Query ────────────────────────────────────────────────┐
//	│ category = 'airflow' AND subcategory = 'conv▊          │
//	│                                                        │
//	│  ◆ conversion                              airflow     │  ← completions
//	│  ◆ scheduling                              airflow     │
//	│                                                        │
//	│ value for subcategory                                  │  ← what is being
//	│ the subcategories selected on this note's links        │    completed, and
//	└────────────────────────────────────────────────────────┘    the doc for the
//	 tab accept · ↑/↓ move · enter run · ctrl+t fields · esc      highlighted row
//
// It is the same language, the same completions and the same live data the web
// UI gets, because both go through models/query*.go — here via the Store, so a
// TUI attached to a hub completes against the hub's categories and tags rather
// than against anything this process guessed at. See tui/store.go.
//
// Running a query does NOT push a result screen. It pops back to the browse
// list and hands it the query text, which browse then treats as one more way of
// loading its notes (see browseScreen.reloadNotes). That is what keeps every
// list behavior — the preview pane, lock badges, edit, delete, "/" within the
// results — working unchanged on a query result, instead of a second list
// screen that would have to reimplement all of it.
type queryScreen struct {
	sess  *session
	input textinput.Model

	// ---- completion state ---------------------------------------------------

	// sugg is the current suggestion list; highlight indexes it, or is -1 when
	// the list is empty.
	sugg      []models.QuerySuggestion
	highlight int
	// replaceStart/replaceEnd is the byte range in the input that accepting a
	// suggestion replaces. Both come from the server (or the models layer) with
	// the suggestions they belong to, so this screen never decides how to quote
	// a value or where a word ends.
	replaceStart, replaceEnd int
	// context names what is being completed, for the hint line.
	context string
	// seq discards completions that arrive out of order. Each request carries
	// the sequence number it was issued with; a reply whose number is no longer
	// current is dropped, so typing faster than the round trip cannot leave an
	// older list on screen.
	seq int

	// ---- feedback -----------------------------------------------------------

	// qerr is the positioned syntax error from the last run, or nil. Held as
	// the structured error rather than a string so the caret line can point at
	// the offending run.
	qerr *models.QueryError
	// errText carries a failure that has no position — a transport error, a
	// store that could not be reached.
	errText string
	running bool

	// helpOpen shows the field catalog instead of the completion list. The two
	// share the space because they answer the same question at different
	// scales, and a terminal has no room to show both.
	helpOpen bool
}

// initialQuery seeds the screen with the query already in force, so reopening
// the bar edits the current filter rather than starting from nothing.
func newQueryScreen(sess *session, initialQuery string) *queryScreen {
	ti := textinput.New()
	ti.CharLimit = 1000
	ti.Placeholder = "category = 'airflow' AND subcategory = 'conversion'"
	ti.SetValue(initialQuery)
	ti.Focus()
	ti.CursorEnd()

	s := &queryScreen{sess: sess, input: ti, highlight: -1}
	s.restyle()
	// Size it here as well as in Init: the caret line under a syntax error is
	// drawn against the input's width, so a screen that has not been laid out
	// yet would decide the caret cannot fit and fall back to prose.
	s.layout()
	return s
}

func (s *queryScreen) restyle() {
	s.input.SetStyles(textinput.DefaultStyles(pal.Dark))
}

// takingText: this screen is a text input, so every ⌘ chord that has a typing
// twin must use it rather than the command one. See metakeys.go.
func (s *queryScreen) takingText() bool { return true }

func (s *queryScreen) Init() tea.Cmd {
	s.layout()
	// Ask for completions before the first frame: an empty box is where the
	// language most needs to introduce itself, and the models layer answers an
	// empty query with the worked examples.
	return tea.Batch(textinput.Blink, s.complete())
}

// layout sizes the input to the terminal, leaving room for the box border and
// the prompt.
func (s *queryScreen) layout() {
	w := s.sess.width - 8
	if w < 20 {
		w = 20
	}
	if w > 160 {
		w = 160
	}
	s.input.SetWidth(w)
}

// ---------------------------------------------------------------------------
// Async work
// ---------------------------------------------------------------------------

// queryCompletionMsg carries a completion reply. seq is echoed back so a stale
// one can be recognised and dropped.
type queryCompletionMsg struct {
	seq int
	res *models.QueryCompletion
	err error
}

// queryRanMsg is the answer to enter. The query text travels with it so the
// browse screen can adopt it as its filter without the two screens sharing
// state.
type queryRanMsg struct {
	query string
	notes []models.Note
	err   error
}

// complete asks for the suggestions valid at the cursor.
//
// The commands live here rather than in commands.go because they are the whole
// of this feature's async surface and nothing else calls them — the same reason
// capture.go and summarize.go carry their own.
func (s *queryScreen) complete() tea.Cmd {
	s.seq++
	seq := s.seq
	text := s.input.Value()
	pos := cursorByteOffset(text, s.input.Position())
	store, guid := s.sess.store, s.sess.user.GUID

	return func() tea.Msg {
		res, err := store.CompleteQuery(text, pos, guid)
		return queryCompletionMsg{seq: seq, res: res, err: err}
	}
}

func runQueryCmd(store Store, query, userGUID string) tea.Cmd {
	return func() tea.Msg {
		notes, err := store.QueryNotes(query, userGUID)
		return queryRanMsg{query: query, notes: notes, err: err}
	}
}

// cursorByteOffset converts the textinput's cursor — an index into the value's
// RUNES — into the byte offset the query language works in.
//
// The two differ the moment a query contains anything outside ASCII, which a
// category name easily can. Handing a rune index to the completer would put the
// replacement range in the wrong place and splice a suggestion into the middle
// of a multi-byte character.
func cursorByteOffset(text string, runePos int) int {
	if runePos <= 0 {
		return 0
	}
	// range over a string yields byte offsets, one per rune, which is exactly
	// the mapping being asked for.
	n := 0
	for i := range text {
		if n == runePos {
			return i
		}
		n++
	}
	return len(text)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *queryScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.layout()
		return s, nil

	case queryCompletionMsg:
		// Drop a reply that a later keystroke has already superseded.
		if msg.seq != s.seq {
			return s, nil
		}
		if msg.err != nil {
			// A completion failure is deliberately quiet. The suggestions are
			// a convenience; the query still runs, and a red line here would
			// train the user to ignore the line that reports the real failure.
			s.sugg, s.highlight = nil, -1
			return s, nil
		}
		s.adopt(msg.res)
		return s, nil

	case queryRanMsg:
		s.running = false
		if msg.err != nil {
			s.showError(msg.err)
			return s, nil
		}
		// Hand the query to the browse screen underneath, which adopts it as
		// its filter. Sequenced, not batched: the pop has to land first or the
		// message arrives at this screen.
		return s, tea.Sequence(pop(false), func() tea.Msg { return msg })

	case tea.KeyPressMsg:
		return s.handleKey(msg)
	}

	prev := s.input.Value()
	prevPos := s.input.Position()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() != prev || s.input.Position() != prevPos {
		return s, tea.Batch(cmd, s.complete())
	}
	return s, cmd
}

func (s *queryScreen) handleKey(k tea.KeyPressMsg) (screen, tea.Cmd) {
	popupOpen := len(s.sugg) > 0 && !s.helpOpen

	switch {
	case key.Matches(k, keys.QueryFields):
		s.helpOpen = !s.helpOpen
		return s, nil

	case key.Matches(k, keys.QueryComplete):
		// The conventional "complete now", for when the list was dismissed or
		// the cursor moved somewhere the last request did not cover.
		s.helpOpen = false
		return s, s.complete()

	case key.Matches(k, keys.Move) && popupOpen:
		if len(s.sugg) == 0 {
			break
		}
		if k.String() == "down" {
			s.highlight = (s.highlight + 1) % len(s.sugg)
		} else {
			s.highlight = (s.highlight - 1 + len(s.sugg)) % len(s.sugg)
		}
		return s, nil

	case key.Matches(k, keys.QueryAccept) && popupOpen:
		return s, s.accept(s.highlight)

	case key.Matches(k, keys.Submit):
		// Enter runs the query. It accepts a suggestion first only when the
		// user has moved OFF the default row — otherwise finishing a complete
		// query and pressing enter would insert a completion nobody asked for
		// instead of running what is on screen.
		if popupOpen && s.highlight > 0 {
			return s, s.accept(s.highlight)
		}
		return s, s.run()

	case key.Matches(k, keys.Back):
		// esc dismisses the suggestions first, then the field list, then the
		// screen — each press undoes the most recent thing that appeared.
		switch {
		case s.helpOpen:
			s.helpOpen = false
			return s, nil
		case len(s.sugg) > 0:
			s.sugg, s.highlight = nil, -1
			return s, nil
		}
		return s, pop(false)
	}

	// Anything else is text. Re-complete whenever the value or the cursor moved.
	prev := s.input.Value()
	prevPos := s.input.Position()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(k)
	if s.input.Value() != prev || s.input.Position() != prevPos {
		s.qerr, s.errText = nil, "" // the text changed; the old complaint is stale
		return s, tea.Batch(cmd, s.complete())
	}
	return s, cmd
}

// adopt installs a completion reply.
func (s *queryScreen) adopt(res *models.QueryCompletion) {
	if res == nil {
		s.sugg, s.highlight = nil, -1
		return
	}
	s.sugg = res.Suggestions
	s.replaceStart, s.replaceEnd = res.ReplaceStart, res.ReplaceEnd
	s.context = res.Context
	if len(s.sugg) == 0 {
		s.highlight = -1
	} else {
		s.highlight = 0
	}
}

// accept splices a suggestion into the input.
//
// The splice is pure: the range and the replacement text both came from the
// completer, quoting and trailing space included, so this screen and the web
// UI insert byte-for-byte the same thing.
func (s *queryScreen) accept(i int) tea.Cmd {
	if i < 0 || i >= len(s.sugg) {
		return nil
	}
	sg := s.sugg[i]

	// An example is a whole query rather than a fragment: it replaces the line
	// and runs, which is the fastest way to learn the language.
	if sg.Kind == "example" {
		s.input.SetValue(sg.Text)
		s.input.CursorEnd()
		s.sugg, s.highlight = nil, -1
		return s.run()
	}

	text := s.input.Value()
	start, end := clampRange(s.replaceStart, s.replaceEnd, len(text))
	next := text[:start] + sg.Text + text[end:]
	s.input.SetValue(next)
	s.input.SetCursor(runeCount(next[:start+len(sg.Text)]))
	s.qerr, s.errText = nil, ""
	// Completing a field immediately asks what operator belongs next, which is
	// what makes the bar feel like it is leading rather than waiting.
	return s.complete()
}

func clampRange(start, end, n int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	if end < start {
		end = start
	}
	if end > n {
		end = n
	}
	return start, end
}

// runeCount is len() in runes, for converting a byte offset back into the
// cursor index the textinput wants.
func runeCount(s string) int { return utf8.RuneCountInString(s) }

// run executes what is in the box. An empty query is not an error — it clears
// the filter and shows the whole library, which is what an emptied search box
// should always do.
func (s *queryScreen) run() tea.Cmd {
	text := strings.TrimSpace(s.input.Value())
	s.sugg, s.highlight = nil, -1
	s.qerr, s.errText = nil, ""

	if text == "" {
		return tea.Sequence(pop(false), func() tea.Msg {
			return queryRanMsg{query: "", notes: nil}
		})
	}
	s.running = true
	return runQueryCmd(s.sess.store, text, s.sess.user.GUID)
}

// showError splits a failure into the two kinds a user acts on differently: a
// syntax error, which points at a character they can fix, and everything else,
// which is a sentence about the world.
func (s *queryScreen) showError(err error) {
	var qe *models.QueryError
	if errors.As(err, &qe) {
		s.qerr, s.errText = qe, ""
		// Put the cursor on the mistake so the fix starts with the next
		// keystroke — the terminal's version of selecting the range.
		s.input.SetCursor(runeCount(truncateBytes(s.input.Value(), qe.Pos)))
		return
	}
	s.qerr, s.errText = nil, err.Error()
}

func truncateBytes(s string, n int) string {
	if n < 0 {
		return ""
	}
	if n > len(s) {
		return s
	}
	return s[:n]
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// maxSuggestionRows caps the completion list. A list taller than this stops
// being scannable, and the cap keeps the box from swallowing the screen on a
// tall terminal.
const maxSuggestionRows = 8

func (s *queryScreen) View() string {
	var b strings.Builder

	b.WriteString(labelFocusedStyle.Render("Query") + "  " +
		dimStyle.Render("SQL over every note attribute") + "\n\n")
	b.WriteString(s.input.View() + "\n")

	switch {
	case s.errText != "":
		b.WriteString("\n" + errorTextStyle.Render("✗ "+s.errText) + "\n")
	case s.qerr != nil:
		b.WriteString(s.renderSyntaxError())
	case s.running:
		b.WriteString("\n" + dimStyle.Render("running…") + "\n")
	}

	if s.helpOpen {
		b.WriteString("\n" + s.renderFields())
	} else if len(s.sugg) > 0 {
		b.WriteString("\n" + s.renderSuggestions())
	}

	b.WriteString("\n" + renderHelp(keys.queryHelp()...))
	box := dialogBoxStyle.Render(b.String())
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Top, box)
}

// renderSyntaxError draws a caret under the offending run, which is the
// terminal's answer to the web UI selecting the range:
//
//	category = 'airflow' AND catgory = 'x'
//	                         ^^^^^^^
//	unknown field "catgory" — did you mean category?
//
// The caret line is built from the byte offset the parser reported, measured in
// display cells so it lines up under a query containing wide characters.
func (s *queryScreen) renderSyntaxError() string {
	qe := s.qerr
	value := s.input.Value()
	pos, end := clampRange(qe.Pos, qe.Pos+max(qe.Len, 1), len(value))

	lead := lipgloss.Width(value[:pos])
	span := lipgloss.Width(value[pos:end])
	if span < 1 {
		span = 1
	}

	msg := qe.Msg
	if qe.Hint != "" {
		msg += " — " + qe.Hint
	}

	// A query wider than the box scrolls inside the widget, and this caret is
	// drawn against the box rather than against the widget's scroll offset — so
	// past that point it would point at the wrong character, which is worse
	// than not pointing at all. The message then names the position instead.
	if w := s.input.Width(); w > 0 && lead+span > w {
		return "\n" + errorTextStyle.Render("✗ "+msg+" (at character "+itoa(qe.Pos+1)+")") + "\n"
	}

	// The +2 offsets the caret past the textinput's own prompt ("> ").
	return "\n" + strings.Repeat(" ", lead+2) + errorTextStyle.Render(strings.Repeat("^", span)) +
		"\n" + errorTextStyle.Render(msg) + "\n"
}

// suggestionKindGlyph is the one-cell gutter that says what a row is. The
// glyphs match the web UI's popup so the two read as one feature.
func suggestionKindGlyph(kind string) string {
	switch kind {
	case "field":
		return "▤"
	case "operator":
		return "="
	case "value":
		return "◆"
	case "keyword", "logic":
		return "ⓚ"
	case "example":
		return "★"
	}
	return "·"
}

func (s *queryScreen) renderSuggestions() string {
	var b strings.Builder

	// Scroll the window so the highlighted row is always visible, without
	// moving it more than it has to — a list that re-centres on every keypress
	// is harder to read than one that scrolls at the edges.
	start := 0
	if s.highlight >= maxSuggestionRows {
		start = s.highlight - maxSuggestionRows + 1
	}
	end := min(start+maxSuggestionRows, len(s.sugg))

	// The label column is sized to the widest label on screen so the detail
	// column lines up, capped so one long note title cannot push it off.
	labelWidth := 0
	for _, sg := range s.sugg[start:end] {
		if w := lipgloss.Width(sg.Label); w > labelWidth {
			labelWidth = w
		}
	}
	labelWidth = min(labelWidth, 46)

	for i := start; i < end; i++ {
		sg := s.sugg[i]
		label := truncateRunes(sg.Label, 46)
		row := suggestionKindGlyph(sg.Kind) + " " +
			label + strings.Repeat(" ", max(labelWidth-lipgloss.Width(label), 0))
		if sg.Detail != "" {
			row += "  " + dimStyle.Render(sg.Detail)
		}
		if i == s.highlight {
			b.WriteString(querySelStyle.Render("▸ "+row) + "\n")
		} else {
			b.WriteString("  " + row + "\n")
		}
	}

	if len(s.sugg) > end {
		b.WriteString(dimStyle.Render("  … " + itoa(len(s.sugg)-end) + " more\n"))
	}

	// The context line says what is being completed; the doc line explains the
	// highlighted row. Together they are why a person can use this language
	// without having read anything about it first.
	b.WriteString("\n" + dimStyle.Render(s.context))
	if s.highlight >= 0 && s.sugg[s.highlight].Doc != "" {
		b.WriteString("\n" + dimStyle.Render(s.sugg[s.highlight].Doc))
	}
	return b.String() + "\n"
}

// renderFields is the ctrl+t view: every queryable attribute with its type.
//
// It reads models.QuerySchema() directly rather than going through the Store.
// The catalog is a compiled-in table with no storage behind it, so a round trip
// would fetch a constant this binary already holds — and in HTTP mode it would
// describe the SERVER's build, which is the wrong answer for a screen
// explaining what THIS parser accepts.
func (s *queryScreen) renderFields() string {
	schema := models.QuerySchema()

	nameWidth := 0
	for _, f := range schema.Fields {
		if w := lipgloss.Width(f.Name); w > nameWidth {
			nameWidth = w
		}
	}

	var b strings.Builder
	b.WriteString(labelFocusedStyle.Render("Queryable attributes") + "\n")
	for _, f := range schema.Fields {
		kind := string(f.Kind)
		if f.Multi {
			kind += "·multi"
		}
		b.WriteString("  " + f.Name + strings.Repeat(" ", nameWidth-lipgloss.Width(f.Name)) +
			"  " + dimStyle.Render(pad(kind, 13)) + dimStyle.Render(f.Example) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(
		"operators: = != < <= > >= CONTAINS LIKE MATCHES IN BETWEEN IS NULL IS EMPTY") + "\n")
	b.WriteString(dimStyle.Render(
		"joins: AND OR NOT ( )   ordering: ORDER BY <field> [ASC|DESC]   LIMIT n") + "\n")
	b.WriteString(dimStyle.Render(
		"time: now today yesterday -7d -24h -6mo, or a quoted date such as '2026-03-04'") + "\n")
	return b.String()
}

func pad(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s + " "
}

// queryResultSummary is the status line a run leaves behind. It exists because
// a query that matched nothing looks exactly like a query that failed — an
// empty list — and the difference matters: one needs the query edited, the
// other needs nothing.
func queryResultSummary(n int) string {
	switch n {
	case 0:
		return "No notes match that query"
	case 1:
		return "1 note matches"
	}
	return itoa(n) + " notes match"
}

// itoa keeps this file free of a strconv import for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

var _ screen = (*queryScreen)(nil)
var _ restyler = (*queryScreen)(nil)
var _ texter = (*queryScreen)(nil)
