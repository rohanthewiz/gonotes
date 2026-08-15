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

	// listWidth is the list's width under the current layout: the full
	// terminal when narrow, a fraction of it when the preview pane is showing.
	// Zero until the first layout.
	listWidth int
	// previewWidth is the preview pane's total width including its rule and
	// gutter, or 0 when the layout is narrow — which is also the "is the
	// preview showing" test.
	previewWidth int
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
type noteItem struct{ note models.Note }

func (i noteItem) Title() string {
	var prefix strings.Builder
	if i.note.IsFlagged {
		prefix.WriteString(flaggedStyle.Render("⚑ "))
	}
	if i.note.IsPrivate {
		prefix.WriteString(privateStyle.Render("🔒 "))
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

// filterBodyRunes caps how much of a note body feeds the fuzzy filter. The cap
// keeps matching fast with hundreds of long notes while still making "/" a
// genuine content search rather than a title search.
const filterBodyRunes = 2000

// FilterValue feeds the fuzzy filter.
//
// The cap is applied in runes, not bytes. Slicing a UTF-8 string at a byte
// offset can land mid-rune, and the resulting invalid byte would flow into
// bubbles' matcher — which normalizes and compares runes, so a note whose
// 2000th byte falls inside a multi-byte character would match slightly
// differently than the same note one character shorter. Rare and silent, which
// is precisely why it is worth not having.
func (i noteItem) FilterValue() string {
	body := truncateRunes(i.note.Body.String, filterBodyRunes)
	return i.note.Title + " " + i.note.Tags.String + " " + i.note.Description.String + " " + body
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
	s.layout()
	return s.refresh()
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

// refresh reloads the list honoring the active category filter. Also called
// by the root when a pushed screen (form/detail/confirm) pops with changes.
func (s *browseScreen) refresh() tea.Cmd {
	if s.catFilter != nil {
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
			items = append(items, noteItem{note: n})
		}
		title := "GoNotes"
		if s.catFilter != nil {
			title = "GoNotes — " + s.catFilter.Name
		}
		s.list.Title = title
		return s, s.list.SetItems(items)

	case categoryPickedMsg:
		s.catFilter = msg.cat
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

	case tea.KeyPressMsg:
		// While the fuzzy filter prompt is active, every key belongs to it.
		if s.list.FilterState() == list.Filtering {
			break
		}

		switch {
		case key.Matches(msg, keys.Quit):
			return s, tea.Quit

		case key.Matches(msg, keys.Back):
			// esc peels back UI state in order: applied fuzzy filter first,
			// then the category filter, and does nothing at the home state.
			if s.list.FilterState() == list.FilterApplied {
				break // let the list clear its own filter
			}
			if s.catFilter != nil {
				s.catFilter = nil
				return s, s.refresh()
			}
			return s, nil

		case key.Matches(msg, keys.Open):
			if n := s.selectedNote(); n != nil {
				return s, push(newDetailScreen(s.sess, *n))
			}

		case key.Matches(msg, keys.New):
			return s, push(newFormScreen(s.sess, nil))

		case key.Matches(msg, keys.Edit):
			if n := s.selectedNote(); n != nil {
				return s, push(newFormScreen(s.sess, n))
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
