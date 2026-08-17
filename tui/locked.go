package tui

import (
	"strings"
	"time"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// lockedScreen is what a session sees when it reaches for a note somebody else
// already has open.
//
// It is a dialog rather than a status-bar message because "locked" is not
// information, it is a fork in the road, and the user needs the holder's name
// in front of them to take it:
//
//	      e on a note another session holds
//	                    │
//	          ┌─────────▼─────────┐
//	          │ held by pane w1:p3│
//	          │   since 2m ago    │
//	          └──┬─────┬────┬─────┘
//	read-only ───┘     │    └─── esc: never mind
//	                   │
//	      take over ───┘  (confirmed; the holder finds out on its heartbeat)
//	              └── g: jump to the holder's pane instead
//
// The fourth answer — waiting for the lease to lapse — is deliberately absent.
// A lease that is being renewed never lapses, so "wait" would either be a spinner
// with no end or a lie about how long it will take. The two honest answers to a
// live holder are to look without editing, or to take it.
type lockedScreen struct {
	sess *session
	note *models.Note
	lock *models.NoteLock

	// onReadOnly is what "open read-only" does, supplied by whoever pushed this
	// screen. The browse list opens the detail view; the detail screen — already
	// showing the note, already read-only — just closes the dialog. Same answer
	// to the user, different code, which is exactly why the caller decides.
	onReadOnly func() tea.Cmd

	// busy suppresses input while a steal is in flight, so a double-press
	// cannot fire two takeovers.
	busy bool
}

func newLockedScreen(sess *session, note *models.Note, lock *models.NoteLock, onReadOnly func() tea.Cmd) *lockedScreen {
	return &lockedScreen{sess: sess, note: note, lock: lock, onReadOnly: onReadOnly}
}

func (s *lockedScreen) Init() tea.Cmd { return nil }

// takingText is deliberately not implemented, matching confirmScreen: every
// answer here is a single-letter command, so an unclaimed ⌘ chord should be
// swallowed rather than typed. See metakeys.go.

// canJump reports whether "go to the holder's pane" is a real offer.
//
// Three things must all be true, and any of them can fail in ordinary use: we
// are at Tier 1 (there is a live cats to ask), the holder said which pane it is
// in, and that pane is not this one. The last check is not paranoia — a session
// can hold a lock, be killed, and be replaced by a new GoNotes in the same pane,
// which would otherwise offer to jump the user to where they already are.
func (s *lockedScreen) canJump() bool {
	if s.lock == nil || s.lock.Holder.PaneHandle == "" {
		return false
	}
	if !s.sess.cats.tier1() {
		return false
	}
	return s.lock.Holder.PaneHandle != s.sess.cats.caps.PaneHandle
}

func (s *lockedScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {

	case lockAcquiredMsg:
		// The result of a steal. A steal can still be refused — the lease may
		// have changed hands between the refusal that opened this dialog and the
		// takeover — in which case the dialog stays open showing the new holder.
		s.busy = false
		switch {
		case msg.err != nil:
			return s, statusErr(msg.err, "Take over failed")
		case msg.blockedBy != nil:
			s.lock = msg.blockedBy
			return s, status("Still locked — the holder changed")
		case msg.lock != nil:
			// The form opens on the note as it was when this dialog appeared,
			// which the previous holder may have saved over in the meantime.
			// That is left to the version guard rather than re-read here: a
			// reload would cost a round trip on every takeover to cover a case
			// the guard already handles better, by refusing the save and
			// showing both versions instead of silently swapping the text under
			// somebody who is about to type into it.
			//
			// pop this dialog first so the form lands on the stack in its place,
			// exactly as the unsaved dialog does with its save arm.
			return s, tea.Sequence(
				pop(false),
				push(newFormScreen(s.sess, msg.note)),
				status("Took over the note from "+holderName(msg.lock.StolenFrom, s.lock)),
			)
		}
		return s, nil

	case tea.KeyPressMsg:
		if s.busy {
			return s, nil
		}
		switch {
		case key.Matches(msg, keys.ReadOnly):
			return s, tea.Sequence(pop(false), s.onReadOnly())

		case key.Matches(msg, keys.Steal):
			// One more confirmation before taking somebody's note. The dialog
			// this opens is the generic yes/no one — the question is genuinely
			// binary here, unlike the unsaved-changes fork.
			return s, push(newConfirmScreen(s.sess,
				"Take \""+s.note.Title+"\" from "+s.holderLabel()+"?\n"+
					dimStyle.Render("They keep whatever they have typed, but will not be able to save it."),
				s.steal()))

		case key.Matches(msg, keys.JumpPane):
			if !s.canJump() {
				return s, nil
			}
			return s, tea.Sequence(pop(false),
				focusPaneCmd(s.sess.cats, s.lock.Holder.PaneHandle))

		case key.Matches(msg, keys.Back, keys.Quit):
			return s, pop(false)
		}
	}
	return s, nil
}

// steal returns the command that takes the lock by force. It is a method
// returning a Cmd rather than a Cmd built at construction because busy has to
// be set at press time.
func (s *lockedScreen) steal() tea.Cmd {
	s.busy = true
	return acquireLockCmd(s.sess.store, s.note, s.sess.user.GUID, true)
}

// holderLabel names the holder for a sentence, falling back through the same
// ladder sessionIdentity builds labels with.
func (s *lockedScreen) holderLabel() string {
	if s.lock == nil {
		return "another session"
	}
	if l := strings.TrimSpace(s.lock.Holder.Label); l != "" {
		return l
	}
	return "another session"
}

// holderName describes the session a steal displaced, preferring the label from
// the lock we were shown over the raw session id nobody can read.
func holderName(stolenFrom string, previous *models.NoteLock) string {
	if previous != nil && previous.Holder.Label != "" {
		return previous.Holder.Label
	}
	if stolenFrom != "" && stolenFrom != "unknown" {
		return "session " + truncateID(stolenFrom)
	}
	return "another session"
}

func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (s *lockedScreen) View() string {
	var b strings.Builder

	b.WriteString(errorTextStyle.Bold(true).Render("Note is being edited elsewhere"))
	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(s.note.Title))
	b.WriteString("\n")

	// The two facts that decide what the user does next: who, and how long. A
	// note held for eight seconds is a colleague who just opened it; one held
	// for three hours is a pane somebody forgot about.
	held := "held by " + s.holderLabel()
	if s.lock != nil {
		held += "  •  for " + humanDuration(s.lock.Age())
	}
	b.WriteString(dimStyle.Render(held))

	if s.busy {
		b.WriteString("\n\n" + dimStyle.Render("Taking over..."))
		return s.box(b.String())
	}

	b.WriteString("\n\n")

	// Steal is colored like every other action in this TUI that can cost
	// somebody something — the delete dialog's "yes", the unsaved dialog's
	// "discard".
	rows := []key.Binding{keys.ReadOnly, keys.Steal}
	if s.canJump() {
		rows = append(rows, keys.JumpPane)
	}
	rows = append(rows, keys.Back)

	for i, bind := range rows {
		if i > 0 {
			b.WriteString(helpStyle.Render("   "))
		}
		h := bind.Help()
		style := lipgloss.NewStyle().Bold(true)
		if h.Key == keys.Steal.Help().Key {
			style = errorTextStyle.Bold(true)
		}
		b.WriteString(style.Render(h.Key) + helpStyle.Render(" "+h.Desc))
	}

	return s.box(b.String())
}

func (s *lockedScreen) box(body string) string {
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center,
		dialogBoxStyle.Render(body))
}

// humanDuration renders an age the way this dialog wants to read it: one unit,
// rounded, no decimals. models.humanizeAge does the same job for the server's
// error strings; this one is separate because it is user-facing prose and may
// want to diverge (a "just now" case, say) without changing an API message.
func humanDuration(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "a few seconds"
	case d < time.Minute:
		return d.Truncate(time.Second).String()
	case d < time.Hour:
		return d.Truncate(time.Minute).String()
	default:
		return d.Truncate(time.Hour).String()
	}
}

// focusPaneCmd asks cats to bring the holder's pane forward.
//
// Tier 1 only, and silent about failure by design: the whole cats integration
// degrades rather than complains (see package cats), and a user who pressed
// "go there" and did not move can see for themselves that they did not.
func focusPaneCmd(cs *catsState, handle string) tea.Cmd {
	if cs == nil || !cs.tier1() || handle == "" {
		return nil
	}
	client := cs.client
	return func() tea.Msg {
		// Two round trips: the lock records the PUBLIC handle ("w1:p3") because
		// that is what the pane environment carries, while focus addresses the
		// internal id. ResolvePane bridges them, decoding the "p_<n>" form
		// locally and paying for a pane.list only on the public form.
		id, err := client.ResolvePane(handle)
		if err != nil {
			return nil
		}
		_ = client.PaneFocus(id)
		return nil
	}
}

// holderLabelOf names a lock's holder for a sentence, with the same fallback
// the dialog uses. A free function because the form needs it too and neither
// screen owns the other.
func holderLabelOf(l *models.NoteLock) string {
	if l == nil {
		return "another session"
	}
	if label := strings.TrimSpace(l.Holder.Label); label != "" {
		return label
	}
	return "another session"
}

// staleScreen is the fork a save loses on: the note changed underneath this
// form, both versions are somebody's real work, and only a person can decide
// which one survives.
//
// It is emphatically NOT a confirmScreen. "Your save failed, retry?" is the
// wrong question — the retry would either destroy their work or destroy yours
// depending on an implementation detail the user cannot see. Naming both
// outcomes is the entire job:
//
//	l  load theirs      → your edits are gone, their note stands
//	o  overwrite theirs → their edits are gone, your note stands
//	esc                 → decide nothing yet; the form keeps your text
//
// esc is the default-by-reflex answer, and it is the only one that loses
// nothing — the same principle the unsaved-changes dialog is built on.
type staleScreen struct {
	sess    *session
	stale   *models.StaleWriteError
	onLoad  func(*models.Note) tea.Cmd
	onForce func(*models.Note) tea.Cmd
}

func newStaleScreen(sess *session, stale *models.StaleWriteError,
	onLoad, onForce func(*models.Note) tea.Cmd) *staleScreen {
	return &staleScreen{sess: sess, stale: stale, onLoad: onLoad, onForce: onForce}
}

func (s *staleScreen) Init() tea.Cmd { return nil }

func (s *staleScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		// Each arm pops first so the callback's own result — a status line, or
		// the noteSavedMsg an overwrite eventually produces — lands on the form
		// underneath rather than on a dialog that is about to disappear. The
		// same ordering confirmScreen and unsavedScreen use, for the same reason.
		switch {
		case key.Matches(k, keys.Reload):
			return s, tea.Sequence(pop(false), s.onLoad(s.stale.Current))
		case key.Matches(k, keys.Overwrite):
			return s, tea.Sequence(pop(false), s.onForce(s.stale.Current))
		case key.Matches(k, keys.CancelExit), key.Matches(k, keys.Back):
			return s, pop(false)
		}
	}
	return s, nil
}

func (s *staleScreen) View() string {
	var b strings.Builder
	b.WriteString(errorTextStyle.Bold(true).Render("This note changed while you were editing"))
	b.WriteString("\n\n")

	// Say who and when, not just that it happened. "Saved by someone 4 minutes
	// ago" is a fact the user can reason about; "version mismatch" is not.
	if c := s.stale.Current; c != nil {
		by := "another session"
		if c.UpdatedBy.Valid && c.UpdatedBy.String != "" {
			by = c.UpdatedBy.String
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(c.Title) + "\n")
		b.WriteString(dimStyle.Render("saved by " + by + " " +
			humanDuration(time.Since(c.UpdatedAt)) + " ago"))
	} else {
		b.WriteString(dimStyle.Render("Somebody else saved it first."))
	}

	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Your text is still in the form either way you go.") + "\n\n")

	// Both destructive answers are colored; only esc is not. That is honest
	// here in a way it is not on most dialogs — there is no safe action, only
	// a safe delay.
	for i, bind := range keys.staleHelp() {
		if i > 0 {
			b.WriteString(helpStyle.Render("   "))
		}
		h := bind.Help()
		style := lipgloss.NewStyle().Bold(true)
		if h.Key != keys.CancelExit.Help().Key {
			style = errorTextStyle.Bold(true)
		}
		b.WriteString(style.Render(h.Key) + helpStyle.Render(" "+h.Desc))
	}

	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center,
		dialogBoxStyle.Render(b.String()))
}

var _ screen = (*lockedScreen)(nil)
var _ screen = (*staleScreen)(nil)
