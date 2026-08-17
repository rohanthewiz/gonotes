package tui

import (
	"strings"

	"gonotes/models"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// browseScreen is the home screen: the user's notes in a filterable list, with
// a markdown preview beside it when the terminal is wide enough.
//
// Search & filtering (two complementary mechanisms):
//   - "/" activates bubbles/list's built-in fuzzy filter, which we point at
//     title + tags + description + a slice of the body (see FilterValue), so
//     typing "/duck" finds notes that merely mention DuckDB in passing.
//   - "c" opens the category screen; picking a category narrows the list to
//     that category's notes (server-side via the join table). esc clears it.
//
// The two compose: you can filter by category first, then "/" within it.
//
// Layout:
//
//	width < 100                    width >= 100
//	┌──────────────────┐           ┌───────────┬──────────────────┐
//	│ list (full)      │           │ list ~40% │ markdown preview │
//	└──────────────────┘           └───────────┴──────────────────┘
//
// The preview costs nothing extra to populate — loadNotesCmd already returns
// full bodies, because the fuzzy filter searches them — so the wide layout is
// purely a rendering decision, made per frame from the current width.
type browseScreen struct {
	sess *session
	list list.Model

	// catFilter is the active category filter; nil means "all notes".
	catFilter *models.Category
	// subFilter narrows catFilter to the notes carrying ALL of these
	// subcategories. Only ever non-empty alongside catFilter, and cleared one
	// step before it by esc — a subcategory is a refinement of a category, so
	// backing out of it should land on the category rather than on everything.
	subFilter []string

	// listWidth is the list's width under the current layout: the full
	// terminal when narrow, a fraction of it when the preview pane is showing.
	// Zero until the first layout.
	listWidth int
	// previewWidth is the preview pane's total width including its rule and
	// gutter, or 0 when the layout is narrow — which is also the "is the
	// preview showing" test.
	previewWidth int

	// clicks turns two clicks on one row into "open that note". See mouse.go.
	clicks clickTracker

	// locks is who is editing what right now, keyed by note id, refreshed
	// alongside the notes. It drives the ✎ badge and nothing else — pressing
	// "e" always asks the server rather than trusting this map, because it is a
	// snapshot and the answer that matters is the one at the moment of asking.
	locks map[int64]models.NoteLock
}

// widePaneMin is the terminal width at which the preview pane appears. Below
// it the list alone would be squeezed under ~60 columns, which is where note
// titles start truncating badly; a preview bought at that price is a bad trade.
const widePaneMin = 100

// listWidthFraction is the share of a wide terminal given to the list. Titles
// and dates fit comfortably at 40% of 100 columns, and prose reads better with
// the larger half.
const listWidthFraction = 0.4

// noteItem adapts a models.Note to the list.Item interface.
//
// heldBy is the label of the session editing this note right now, or "" — the
// list's one piece of state that is not the note itself. It rides on the item
// rather than being looked up at render time because Title() is called once per
// visible row per frame, and a map lookup per row per frame for a badge that
// changes every few minutes is work spent for nothing.
type noteItem struct {
	note   models.Note
	heldBy string
}

func (i noteItem) Title() string {
	var prefix strings.Builder
	if i.note.IsFlagged {
		prefix.WriteString(flaggedStyle.Render("⚑ "))
	}
	if i.note.IsPrivate {
		prefix.WriteString(privateStyle.Render("🔒 "))
	}
	// A pencil, not a padlock: 🔒 already means "private" on this row and would
	// say two different things in the same column. The badge answers "can I
	// edit this right now", which is a different question from "is this secret".
	if i.heldBy != "" {
		prefix.WriteString(errorTextStyle.Render("✎ "))
	}
	return prefix.String() + i.note.Title
}

func (i noteItem) Description() string {
	// Prefer the explicit description; fall back to the body's first line so
	// every row gives a hint of its content.
	desc := i.note.Description.String
	if desc == "" && i.note.Body.Valid {
		desc = firstLine(i.note.Body.String)
	}
	date := i.note.UpdatedAt.Format("2006-01-02")
	if tags := i.note.Tags.String; tags != "" {
		return dimStyle.Render(date+"  #"+tags) + "  " + desc
	}
	return dimStyle.Render(date) + "  " + desc
}

// filterBodyRunes caps how much of a note body feeds the filter. The cap keeps
// matching fast with hundreds of long notes while still making "/" a genuine
// content search rather than a title search.
const filterBodyRunes = 2000

// filterSep divides the two halves of a FilterValue, which notesFilter searches
// by different rules. A NUL is used because it is the one byte that cannot
// occur in a note: titles, tags and bodies all come back from the store as Go
// strings holding user-typed text, and no keyboard, paste or markdown import
// produces one — so the split can never land in the middle of real content.
const filterSep = "\x00"

// FilterValue feeds the filter, in two parts: the note's short fields, then a
// capped slice of its body, separated by filterSep. notesFilter splits them
// back apart; nothing else reads this string.
//
// The cap is applied in runes, not bytes. Slicing a UTF-8 string at a byte
// offset can land mid-rune, and the resulting invalid byte would flow into
// bubbles' matcher — which normalizes and compares runes, so a note whose
// 2000th byte falls inside a multi-byte character would match slightly
// differently than the same note one character shorter. Rare and silent, which
// is precisely why it is worth not having.
func (i noteItem) FilterValue() string {
	body := truncateRunes(i.note.Body.String, filterBodyRunes)
	return i.note.Title + " " + i.note.Tags.String + " " + i.note.Description.String +
		filterSep + body
}

// notesFilter is the note list's filter, replacing bubbles' DefaultFilter.
//
// WHY THE DEFAULT DOES NOT WORK HERE. DefaultFilter is sahilm/fuzzy, which
// matches a SUBSEQUENCE: "apm" matches any haystack containing an a, later a p,
// later an m. That is a good rule for a list of short titles and a useless one
// the moment 2000 characters of prose are in the haystack, because a few
// hundred words of English contain nearly every short query as a subsequence.
// Measured on five realistic notes, "apm", "cats" and "meal" each matched all
// five, and "disk" matched four — a filter that filters nothing, which is
// exactly what "the filter doesn't work" looks like from the outside.
//
// THE SPLIT RULE. Fuzzy stays where it earns its keep (title, tags,
// description — short strings where typo tolerance and abbreviations like
// "dtflw" are the point), and the body is searched as a plain case-insensitive
// substring, every whitespace-separated token having to appear. So "/duck"
// still finds a note that merely mentions DuckDB in passing, while "/apm" no
// longer drags in every note that happens to contain those three letters in
// that order.
//
// MATCHED INDEXES COME FROM THE HEAD ONLY, and that is a fix in itself. The
// list delegate applies them to the item's TITLE to underline the matched
// characters; DefaultFilter returned offsets into the whole FilterValue, so a
// match deep in a body underlined arbitrary letters of an unrelated title. A
// body hit now reports no indexes, which renders as no underline.
func notesFilter(term string, targets []string) []list.Rank {
	heads := make([]string, len(targets))
	bodies := make([]string, len(targets))
	for i, t := range targets {
		head, body, _ := strings.Cut(t, filterSep)
		heads[i], bodies[i] = head, body
	}

	// The head pass is the default behavior, unchanged — including whatever it
	// does with an empty term, which is why this is delegated rather than
	// reimplemented.
	ranks := list.DefaultFilter(term, heads)

	fields := strings.Fields(term)
	if len(fields) == 0 {
		return ranks
	}

	matched := make(map[int]bool, len(ranks))
	for _, r := range ranks {
		matched[r.Index] = true
	}

	// Body hits are appended after the fuzzy ones rather than merged into the
	// ranking: a title match is a stronger signal than a mention buried in a
	// body, and DefaultFilter's scores are not on a scale this could join.
	for i, body := range bodies {
		if matched[i] || !containsAllFold(body, fields) {
			continue
		}
		ranks = append(ranks, list.Rank{Index: i})
	}
	return ranks
}

// containsAllFold reports whether every token appears in s, case-insensitively.
//
// All tokens rather than any: multi-word queries narrow, which is what a second
// word is typed for. Order is not required — "release checklist" finds a note
// that says "checklist for the release" — a search box is not a phrase query.
func containsAllFold(s string, tokens []string) bool {
	// Lowered once per note, not once per token: the body is the long side, and
	// this runs for every note on every keystroke of the filter prompt.
	lower := strings.ToLower(s)
	for _, tok := range tokens {
		if !strings.Contains(lower, strings.ToLower(tok)) {
			return false
		}
	}
	return true
}

// truncateRunes returns at most n runes of s, cutting on a character boundary.
// Unlike truncate() in tui.go it appends no ellipsis — this is for machine
// consumption, where a stray "…" would be one more token to match against.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		// A string of n bytes can hold at most n runes, so this is a safe fast
		// path that skips the conversion for the common short-body case.
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	// Strip a leading markdown heading marker so the preview reads cleanly.
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

// newListDelegate builds the shared row renderer used by both list screens
// (notes and categories), accented with the app's primary color.
//
// The explicit NewDefaultItemStyles(pal.Dark) is not cosmetic bookkeeping.
// list.NewDefaultDelegate() in bubbles v2 hardcodes the dark style set — its
// own source marks that "XXX ... temporarily" — because lipgloss v2 removed
// AdaptiveColor and the widget has no way to ask the terminal itself. Without
// this, a light-background terminal loses the row contrast v1 gave for free.
func newListDelegate() list.DefaultDelegate {
	delegate := list.NewDefaultDelegate()
	delegate.Styles = list.NewDefaultItemStyles(pal.Dark)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(colorPrimary).BorderLeftForeground(colorPrimary).
		Background(colorSel)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(colorSubtle).BorderLeftForeground(colorPrimary).
		Background(colorSel)
	return delegate
}

// applyListStyles restyles a list.Model in place from the current palette.
// Shared by both list screens and called again on a palette change: everything
// it touches was copied into the model at construction and cannot see the new
// colors on its own.
//
// list.Model.Help is easy to miss — it is a nested help.Model with its own
// style set, hardcoded dark by the same v2 constructor pattern, and it draws
// the footer on every frame.
func applyListStyles(l *list.Model) {
	l.SetDelegate(newListDelegate())
	l.Styles = list.DefaultStyles(pal.Dark)
	l.Styles.Title = appTitleStyle
	l.Help.Styles = help.DefaultStyles(pal.Dark)
}

func newBrowseScreen(sess *session) *browseScreen {
	l := list.New([]list.Item{}, newListDelegate(), sess.width, sess.height)
	l.Title = "GoNotes"
	// Only the note list swaps the matcher out. The category list keeps
	// DefaultFilter, which is the right tool there: its FilterValue is a bare
	// category name, exactly the short haystack fuzzy matching is good at.
	l.Filter = notesFilter
	applyListStyles(&l)
	l.SetShowStatusBar(true)
	l.SetStatusBarItemName("note", "notes")
	// We own quit ("q") and back ("esc") semantics; the widget must not
	// intercept them for its own quit behavior.
	l.DisableQuitKeybindings()
	// Surface our custom actions inside the list's built-in help footer, fed
	// from the same keymap the Update switch dispatches on so the footer can
	// never advertise a key that no longer works.
	l.AdditionalShortHelpKeys = func() []key.Binding { return keys.browseHelp() }

	return &browseScreen{sess: sess, list: l}
}

func (s *browseScreen) Init() tea.Cmd {
	s.list.Title = s.title() // before the first paint, not on the first load
	s.layout()
	return s.refresh()
}

// title is the list's heading: the app name, the active category filter, and
// the mode badge.
//
// The badge is here rather than in a startup message because it answers "which
// notes are these" — a question that stays asked. It is empty for the ordinary
// local launch, so the common case reads exactly as it always has, and an
// unusual one (a server, a second data directory) is labelled for as long as it
// lasts. See Mode in tui.go.
func (s *browseScreen) title() string {
	t := "GoNotes"
	if s.catFilter != nil {
		// The same notation the form field takes, so "Work/backend" in the title
		// is a string the user could type back into a note to file it here.
		t += " — " + models.FormatCategorySpec(s.catFilter.Name, s.subFilter)
	}
	if b := s.sess.mode.Badge; b != "" {
		t += " · " + b
	}
	return t
}

// layout splits the terminal between the list and the preview pane. Called on
// init and on every resize; the result is read by View.
func (s *browseScreen) layout() {
	if s.sess.width < widePaneMin {
		s.listWidth = s.sess.width
		s.previewWidth = 0
	} else {
		s.listWidth = int(float64(s.sess.width) * listWidthFraction)
		s.previewWidth = s.sess.width - s.listWidth
	}
	s.list.SetSize(s.listWidth, s.sess.height)
}

// restyle rebuilds everything the list copied in at construction. The markdown
// preview needs no attention: its cache is keyed on the palette generation, so
// a palette change orphans the old entries automatically.
func (s *browseScreen) restyle() {
	applyListStyles(&s.list)
}

// takingText is true only while the fuzzy filter prompt is up — the one state
// in which this screen's unmodified keys are characters rather than note
// actions. It is the same condition Update checks before its own switch, and it
// is what stops ⌘F from typing an "f" into a half-typed search. See metakeys.go.
func (s *browseScreen) takingText() bool {
	return s.list.FilterState() == list.Filtering
}

// refresh reloads the list honoring the active category filter. Also called
// by the root when a pushed screen (form/detail/confirm) pops with changes.
// refresh reloads the notes AND the lock badges. The two travel together
// because a stale badge is worse than no badge: "nobody is editing this" is a
// claim, and one held over from a minute ago is a claim that walks the user
// into a contention dialog they were told not to expect.
func (s *browseScreen) refresh() tea.Cmd {
	return tea.Batch(s.reloadNotes(), loadLocksCmd(s.sess.store, s.sess.user.GUID))
}

// heldBy names the session editing a note, or "" when nobody is.
//
// This session's OWN lease reads as unheld. A form that is open right now holds
// a lock on the note behind it, and badging that row would tell the user
// somebody is editing a note they are editing themselves.
func (s *browseScreen) heldBy(noteID int64) string {
	l, ok := s.locks[noteID]
	if !ok || l.Holder.SessionID == sessionIdentity().SessionID {
		return ""
	}
	return holderLabelOf(&l)
}

// applyLockBadges re-stamps the badge on every row without reloading the notes.
// Locks change on their own schedule — somebody else opens a note, somebody
// else saves — so the badges have to be able to move independently of the list
// they sit on.
func (s *browseScreen) applyLockBadges() tea.Cmd {
	items := s.list.Items()
	updated := make([]list.Item, 0, len(items))
	for _, it := range items {
		ni, ok := it.(noteItem)
		if !ok {
			updated = append(updated, it)
			continue
		}
		ni.heldBy = s.heldBy(ni.note.ID)
		updated = append(updated, ni)
	}
	return s.list.SetItems(updated)
}

func (s *browseScreen) reloadNotes() tea.Cmd {
	if s.catFilter != nil {
		if len(s.subFilter) > 0 {
			// The subcategory filter is name-keyed rather than id-keyed; see
			// Store.GetCategorySubcategoryNotes for why that is the only door.
			return loadCategorySubNotesCmd(s.sess.store,
				s.catFilter.Name, s.subFilter, s.sess.user.GUID)
		}
		return loadCategoryNotesCmd(s.sess.store, s.catFilter.ID, s.sess.user.GUID)
	}
	return loadNotesCmd(s.sess.store, s.sess.user.GUID)
}

func (s *browseScreen) selectedNote() *models.Note {
	if item, ok := s.list.SelectedItem().(noteItem); ok {
		return &item.note
	}
	return nil
}

func (s *browseScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		s.layout()
		return s, nil

	case notesLoadedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to load notes")
		}
		items := make([]list.Item, 0, len(msg.notes))
		for _, n := range msg.notes {
			items = append(items, noteItem{note: n, heldBy: s.heldBy(n.ID)})
		}
		s.list.Title = s.title()
		return s, s.list.SetItems(items)

	case locksLoadedMsg:
		// A failure here is deliberately silent. The badge is a convenience;
		// the lock itself is enforced at acquire time and again at write time,
		// so a missing badge costs the user one extra dialog and nothing else.
		// A red status line for it would train them to ignore red status lines.
		if msg.err != nil {
			return s, nil
		}
		s.locks = msg.locks
		return s, s.applyLockBadges()

	case lockAcquiredMsg:
		// The reply to pressing "e". Three outcomes, and only one of them opens
		// a form — which is the entire point of asking before opening.
		switch {
		case msg.err != nil:
			return s, statusErr(msg.err, "Could not open for editing")
		case msg.blockedBy != nil:
			// Read-only from here means the ordinary detail view: it shows the
			// whole note and has never been able to write.
			note := *msg.note
			return s, push(newLockedScreen(s.sess, msg.note, msg.blockedBy,
				func() tea.Cmd { return push(newDetailScreen(s.sess, note)) }))
		default:
			return s, push(newFormScreen(s.sess, msg.note))
		}

	case categoryPickedMsg:
		s.catFilter = msg.cat
		// Both filters are set from the one message: a pick either replaces the
		// pair or clears it, and a subcategory left over from a previous pick
		// would silently narrow the new category (or, with no category, narrow
		// nothing while claiming to).
		s.subFilter = msg.subs
		if msg.cat == nil {
			s.subFilter = nil
		}
		return s, s.refresh()

	case flagToggledMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Failed to toggle flag")
		}
		return s, s.refresh()

	case noteDeletedMsg:
		if msg.err != nil {
			return s, statusErr(msg.err, "Delete failed")
		}
		return s, tea.Batch(s.refresh(), status("Note deleted"))

	case tea.MouseWheelMsg:
		// Only over the list: the preview pane renders a fixed block rather than
		// a viewport, so there is nothing on that side to scroll, and rolling
		// the wheel there must not move the selection out from under the note
		// the user is reading.
		if s.overList(msg.X) && wheelList(&s.list, msg) {
			return s, nil
		}
		return s, nil

	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft || !s.overList(msg.X) {
			return s, nil
		}
		idx, ok := listRowAt(&s.list, msg.Y)
		if !ok {
			return s, nil
		}
		// Select on the first click, open on the second. Selecting on every
		// click (rather than only when the row changes) is what makes the
		// preview follow the pointer, and it is also what a double-click's first
		// half has to do for the second half to open the right note.
		s.list.Select(idx)
		if s.clicks.double(idx) {
			if n := s.selectedNote(); n != nil {
				return s, push(newDetailScreen(s.sess, *n))
			}
		}
		return s, nil

	case tea.KeyPressMsg:
		// While the fuzzy filter prompt is active, every key belongs to it.
		if s.list.FilterState() == list.Filtering {
			break
		}

		switch {
		case key.Matches(msg, keys.Quit):
			return s, tea.Quit

		case key.Matches(msg, keys.Back):
			// esc peels back UI state one layer at a time, narrowest first:
			// applied fuzzy filter, then the subcategory, then the category, and
			// nothing at the home state. Dropping the subcategory and the category
			// together would make "Work/backend" one esc away from every note the
			// user owns, which is a longer fall than the key implies.
			if s.list.FilterState() == list.FilterApplied {
				break // let the list clear its own filter
			}
			if len(s.subFilter) > 0 {
				s.subFilter = nil
				return s, s.refresh()
			}
			if s.catFilter != nil {
				s.catFilter = nil
				return s, s.refresh()
			}
			// Nothing left to peel: esc has reached the bottom of the stack and
			// the bottom of the filter state, so it means what it means on the
			// login screen — leave. Every layer above still pops one at a time,
			// so this can only be reached from the home view, and only after the
			// keypress that would have undone something has already found it.
			//
			// A dirty form can never be one of those layers: it raises the
			// unsaved-changes dialog before it lets esc past. See formScreen.
			return s, tea.Quit

		case key.Matches(msg, keys.Open):
			if n := s.selectedNote(); n != nil {
				return s, push(newDetailScreen(s.sess, *n))
			}

		case key.Matches(msg, keys.New):
			return s, push(newFormScreen(s.sess, nil))

		case key.Matches(msg, keys.Edit):
			if n := s.selectedNote(); n != nil {
				// Claim the note BEFORE the form opens, not on save. Discovering
				// contention at save time would mean the user typed for ten
				// minutes into a form that was never going to be allowed to
				// write — the exact failure this whole mechanism exists to
				// prevent. The form is pushed from the reply.
				return s, acquireLockCmd(s.sess.store, n, s.sess.user.GUID, false)
			}

		case key.Matches(msg, keys.Flag):
			if n := s.selectedNote(); n != nil {
				return s, toggleFlagCmd(s.sess.store, n.ID, s.sess.user.GUID)
			}

		case key.Matches(msg, keys.Delete):
			if n := s.selectedNote(); n != nil {
				return s, push(newConfirmScreen(s.sess,
					"Delete \""+n.Title+"\"?",
					deleteNoteCmd(s.sess.store, n.ID, s.sess.user.GUID)))
			}

		case key.Matches(msg, keys.Categories):
			return s, push(newCategoriesScreen(s.sess))

		case key.Matches(msg, keys.Capture):
			return s, s.openCapture()
		}
	}

	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

// overList reports whether a column belongs to the note list rather than to the
// preview pane beside it. In the narrow layout there is no preview, so every
// column is the list's — which is why this asks previewWidth and not width.
func (s *browseScreen) overList(x int) bool {
	return s.previewWidth == 0 || x < s.listWidth
}

// openCapture is the ctrl+g door: pick a sibling agent pane and turn its output
// into a note. See capture.go for the feature; this is only the entry.
//
// Three outcomes, and the two that are not the picker are both one status line.
// Below Tier 1 the feature does not exist, and inside a cats session with no
// other agent running there is nothing to offer — a modal listing nothing would
// be a dialog the user has to dismiss to learn that.
func (s *browseScreen) openCapture() tea.Cmd {
	cs := s.sess.cats
	if !cs.tier1() {
		return status("Capturing an agent pane needs cats — GoNotes is running standalone")
	}

	// The refresh is for the NEXT opening, not this one: the picker is built
	// from the cache that is already in hand, because a keystroke must not wait
	// on a socket. Rate-limited, so holding ctrl+g down costs one call.
	refresh := cs.pollPanes(false)

	if len(cs.agentPanes()) == 0 {
		return tea.Batch(refresh, status("No agent panes to capture from"))
	}
	return tea.Batch(refresh, push(newAgentPickerScreen(s.sess)))
}

func (s *browseScreen) View() string {
	// Both layouts clamp the list to its pane. JoinHorizontal would pad a short
	// block itself, but it cannot rescue an over-wide one — and the list can be
	// over-wide, see the note on clampPane.
	list := clampPane(s.list.View(), s.listWidth, s.sess.height)
	if s.previewWidth == 0 {
		return list
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, list, s.previewView())
}

// previewView renders the selected note's markdown into the right-hand pane.
//
// Rendering happens through the (id, width, palette) cache in markdown.go, so
// holding an arrow key down re-renders each note once, not once per frame —
// the difference between a responsive list and a laggy one over long notes.
func (s *browseScreen) previewView() string {
	// The border and its left gutter come out of the content width.
	inner := s.previewWidth - previewPaneStyle.GetHorizontalFrameSize()
	if inner < 1 {
		return ""
	}

	var body string
	switch n := s.selectedNote(); {
	case n == nil:
		body = dimStyle.Render("(no note selected)")
	default:
		title := previewTitleStyle.Render(truncate(n.Title, inner))
		meta := dimStyle.Render("updated " + n.UpdatedAt.Format("2006-01-02 15:04"))
		var content string
		if strings.TrimSpace(n.Body.String) == "" {
			content = dimStyle.Render("(this note has no body)")
		} else {
			content = renderMarkdownCached(
				noteCacheKey(n.ID, n.UpdatedAt.Unix()), n.Body.String, inner)
		}
		body = title + "\n" + meta + "\n\n" + content
	}

	// Clamp the content to the pane's inner box first, then let the style draw
	// the rule and gutter around it. Doing it the other way round would measure
	// the border as content and lose a column of every line.
	return previewPaneStyle.Render(clampPane(body, inner, s.sess.height))
}

var _ screen = (*browseScreen)(nil)
var _ refresher = (*browseScreen)(nil)
var _ restyler = (*browseScreen)(nil)
