package tui

import (
	"regexp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rohanthewiz/serr"
	"gopkg.in/yaml.v3"
)

// Note intake: another program hands GoNotes a finished note.
//
// cats-todo is the first sender. A backlog prompt marked "info" is a note that
// landed in the wrong place — a quirk, a decision and its reason — and its
// "Send to notes" finds this pane by plugin_type == "notes_mgr" in cats'
// pane.list, then delivers the note with pane.send_input. That is the whole
// transport: cats has no plugin-to-plugin message, and a pane's input is the
// one door every TUI already has. It is also the right door for a second
// reason: the process behind it is the one that already owns the store — it
// holds the bytdb lock in local mode, or the login token in HTTP mode — so a
// sender needs neither the lock nor a password to file a note.
//
// THE CONTRACT (v1). cats paste-encodes send_input, and Bubble Tea turns on
// bracketed paste, so the note arrives here as one tea.PasteMsg:
//
//	<!-- cats-note v1 -->          sentinel: first line, exactly
//	---                            YAML frontmatter, optional; the same keys
//	title: The note's title        `gonotes import-md` reads, minus the ones a
//	description: from cats-todo    sender has no business setting (guid,
//	tags: [cats-todo, info]        timestamps, private). Unknown keys are
//	categories: [Work/backend]     ignored, so a newer sender degrades rather
//	---                            than failing.
//	The body, as markdown…
//
// The sentinel is an HTML comment so that the one failure a sender cannot rule
// out — an older GoNotes, or some other notes plugin, that does not know this
// contract — is benign: the text lands wherever a paste would have, and in
// rendered markdown the marker line is invisible.
//
// WHAT HAPPENS ON ARRIVAL. The note opens as a prefilled, UNSAVED form, the
// rule capture-to-note keeps (see captureDone): the sender decided the text is
// a note; which category it files under, and whether it is kept at all, is
// decided here, one ctrl+s away. It is handled at the ROOT, ahead of the
// active screen, because a paste otherwise goes to whatever widget has focus —
// and the user may be halfway through typing a different note when it lands.
// Pushed on top, the half-typed note is one esc away and untouched.

// intakeSentinel matches the envelope's first line and captures its version.
// Anchored and whole-line: a paste that merely MENTIONS the marker (someone
// pasting this file) is not an envelope.
var intakeSentinel = regexp.MustCompile(`^<!-- cats-note v(\d+) -->$`)

// intakeVersion is the newest envelope this build understands. A sender stamps
// the version it writes; a higher one is refused in words rather than guessed
// at, since a field that changed meaning would be filed wrong silently.
const intakeVersion = 1

// intakeFrontmatter is the v1 key set. Tags and categories go through the
// form's own comma-separated fields, so they are plain lists here and joined
// on the way in.
type intakeFrontmatter struct {
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
	Categories  []string `yaml:"categories"`
}

// intakeNote is one parsed envelope, ready to seed a form.
type intakeNote struct {
	title, desc, tags, categories, body string
}

// parseIntake reports whether text is a note envelope and, if so, what it
// carries.
//
// Three outcomes, kept apart on purpose:
//
//	ok=false            not an envelope — the paste belongs to the active screen
//	ok=true,  err!=nil  an envelope we cannot use (newer version, broken YAML);
//	                    it is swallowed with a status line rather than passed
//	                    on, because pasting raw frontmatter into whatever field
//	                    has focus is the worst of the available answers
//	ok=true,  err==nil  a note
func parseIntake(text string) (note intakeNote, ok bool, err error) {
	// A terminal paste may carry CR line endings (that is how a pasted newline
	// is typed); the envelope's structure is line-based, so settle on LF first.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	first, rest, _ := strings.Cut(text, "\n")
	m := intakeSentinel.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return note, false, nil
	}
	if v, _ := strconv.Atoi(m[1]); v > intakeVersion {
		return note, true, serr.New("note envelope v" + m[1] + " is newer than this GoNotes understands (v" +
			strconv.Itoa(intakeVersion) + ") — update GoNotes")
	}

	// Frontmatter is optional: a sender with nothing but a body may send just
	// the sentinel and the text. An opening "---" with no closing one is not
	// frontmatter either — it is a body that starts with a rule — which is the
	// same reading parseNoteMd gives a file on import.
	var fm intakeFrontmatter
	body := rest
	if after, found := strings.CutPrefix(rest, "---\n"); found {
		if yamlPart, bodyPart, closed := cutFrontmatter(after); closed {
			if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
				return note, true, serr.Wrap(err, "note envelope has invalid frontmatter")
			}
			body = bodyPart
		}
	}

	return intakeNote{
		title:      strings.TrimSpace(fm.Title),
		desc:       strings.TrimSpace(fm.Description),
		tags:       joinNonEmpty(fm.Tags, ","),
		categories: joinNonEmpty(fm.Categories, ", "),
		// Blank lines between the frontmatter and the body, and trailing ones,
		// are envelope padding, not content. Interior blank lines are kept.
		body: strings.Trim(body, "\n"),
	}, true, nil
}

// cutFrontmatter splits what follows an opening "---" at its closing "---"
// line. closed is false when there is none.
func cutFrontmatter(s string) (yamlPart, body string, closed bool) {
	if after, found := strings.CutPrefix(s, "---\n"); found { // empty block
		return "", after, true
	}
	if s == "---" { // empty block, nothing after
		return "", "", true
	}
	if i := strings.Index(s, "\n---\n"); i >= 0 {
		return s[:i+1], s[i+len("\n---\n"):], true
	}
	if strings.HasSuffix(s, "\n---") { // frontmatter only, no body
		return s[:len(s)-len("---")], "", true
	}
	return "", "", false
}

// joinNonEmpty joins the trimmed, non-empty items of list with sep.
func joinNonEmpty(list []string, sep string) string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}

// intakePaste is the root's handler for a paste that parsed as an envelope.
//
// Before login there is no user to file a note under and no browse screen to
// return to, so the note is HELD and opened by loggedInMsg. Refusing instead
// would lose it: the sender has already been told the hand-off worked and has
// no way to learn otherwise.
func (m appModel) intakePaste(note intakeNote) (appModel, tea.Cmd) {
	if m.sess.user == nil {
		m.heldIntake = append(m.heldIntake, note)
		return m, status("A note arrived — it opens once you log in")
	}
	return m, m.openIntake(note)
}

// openIntake pushes the prefilled form for one note.
func (m appModel) openIntake(note intakeNote) tea.Cmd {
	f := newFormScreen(m.sess, nil)
	// prefill leaves the dirty baseline where a blank form has it, so the note
	// counts as unsaved work from the moment it opens: a stray esc asks before
	// discarding text that exists nowhere else in GoNotes.
	f.prefill(note.title, note.tags, note.body)
	f.desc.SetValue(note.desc)
	// The sender's categories win; without any, file it where the list is
	// looking, as a capture does. Either way it is a visible, editable
	// suggestion, and presetCategories keeps it out of the dirty check.
	spec := note.categories
	if spec == "" {
		spec = m.browseFilingSpec()
	}
	f.presetCategories(spec)
	return tea.Batch(push(f), status("Note received — ctrl+s to save"))
}
