package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gonotes/models"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Sync, in the terminal.
//
// GoNotes no longer syncs in the background by default (see
// models/sync_config.go). Something therefore has to ask, and in a terminal
// that is this file. It is three small pieces:
//
//	syncState    what this process last heard about the sync clock, polled
//	             on a timer and read by the browse list's banner.
//	syncScreen   the dialog: sync now, compact and sync, or not yet. It is
//	             the same screen whether the user opened it (S), the clock
//	             raised it (due), or they tried to leave with changes still
//	             on this machine (quitting) — only the question at the top
//	             and the shape of the "no" answer differ.
//	commands     the store calls, as tea.Cmds, since every one of them is a
//	             network round trip in HTTP mode.
//
// The whole feature is invisible on an installation with no hub configured:
// SyncStatus answers nil there, syncState reports itself unconfigured, and
// neither the banner nor the quit guard ever appears.

// ---- State -----------------------------------------------------------------

// syncState is the session's copy of the sync clock. It is mutated in exactly
// one place — the root's Update, on a syncStatusMsg — for the same reason the
// cats state is: the poll that produces it runs on a command goroutine, and a
// screen reading it mid-render must never see a half-written struct.
type syncState struct {
	// status is nil until the first poll answers, and stays nil forever on an
	// installation with no sync configured.
	status *models.SyncClientStatus

	// polling guards against starting a second timer. The poll is kicked off
	// on login, and a re-login (or a second loggedInMsg) must not double it.
	polling bool

	// asked records that the due prompt has already been raised in this
	// session. The banner keeps showing while a sync is owed, but the dialog
	// is not thrown in front of the user again and again — being asked twice
	// is how a prompt becomes a nag.
	asked bool
}

// configured reports whether this installation syncs at all. Everything the
// TUI shows about sync is gated on it.
func (s *syncState) configured() bool {
	return s != nil && s.status != nil && s.status.Enabled
}

// due reports that the clock says it is time to ask.
func (s *syncState) due() bool {
	return s.configured() && s.status.Due
}

// pending is how many local changes have not reached the hub.
func (s *syncState) pending() int {
	if !s.configured() {
		return 0
	}
	return s.status.Pending
}

// banner is the one line the browse list shows about sync, or "" when there is
// nothing worth saying. It leads with the count because that is the part that
// answers "does this matter to me right now".
func (s *syncState) banner() string {
	if !s.due() {
		return ""
	}
	return "⟲ " + s.overdueSummary() + " — " + keys.Sync.Help().Key + " to sync"
}

// overdueSummary phrases the state in the two terms a person actually holds:
// how much is waiting, and how long it has been.
func (s *syncState) overdueSummary() string {
	if !s.configured() {
		return "sync is not configured"
	}
	parts := []string{}
	if n := s.status.Pending; n > 0 {
		parts = append(parts, plural(n, "change", "changes")+" not synced")
	}
	if s.status.LastSync == nil {
		parts = append(parts, "never synced with the hub")
	} else if ago := time.Since(*s.status.LastSync); ago > time.Minute {
		parts = append(parts, "last synced "+humanAge(ago)+" ago")
	}
	if len(parts) == 0 {
		return "sync is due"
	}
	return strings.Join(parts, ", ")
}

// plural renders a count with the right noun. Small enough to inline and
// repeated often enough here that inlining it would be the noisier choice.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// humanAge renders an elapsed time at the coarsest useful unit, in words. The
// question the reader is answering is "is this stale enough to act on", and no
// answer to that needs seconds once it is past an hour.
//
// Separate from locked.go's humanDuration, which is Go's own duration
// formatting truncated to one unit ("5h0m0s"). That reads correctly beside a
// lock holder's name and badly in the middle of a sentence, which is where
// this one lives.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute", "minutes")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour", "hours")
	default:
		return plural(int(d.Hours()/24), "day", "days")
	}
}

// ---- Messages and commands -------------------------------------------------

// syncStatusMsg carries a poll result to the root. err is kept rather than
// surfaced: a hub that cannot be reached is the normal state of a laptop on a
// train, and a status bar reporting it every minute would be noise.
type syncStatusMsg struct {
	status *models.SyncClientStatus
	err    error
}

// syncTickMsg is the poll timer.
type syncTickMsg struct{}

// syncDoneMsg reports the outcome of a cycle the user asked for.
type syncDoneMsg struct {
	status   *models.SyncClientStatus
	compact  bool // the cycle was asked to compact on the way
	quitting bool // the user asked to sync AND leave
	err      error
}

// compactDoneMsg reports a compaction run on its own.
type compactDoneMsg struct {
	result *models.CompactionResult
	err    error
}

// syncPollInterval is how often the TUI re-reads the clock. A minute is far
// finer than the two-hour prompt interval it is watching for, and coarse
// enough that in HTTP mode it is one request per minute rather than a stream.
const syncPollInterval = time.Minute

// syncIdlePollInterval is the rate for an installation that has answered "no
// sync here". Most GoNotes installations are a single machine with no hub, and
// a request a minute forever to be told that again is the wrong price for a
// feature that is not in use.
//
// It backs off rather than stopping, because the answer is not quite
// immutable: in HTTP mode the server could be restarted with sync configured
// while this TUI stays open. Fifteen minutes finds that without anyone
// noticing the polling.
const syncIdlePollInterval = 15 * time.Minute

// syncStatusCmd reads the sync clock.
func syncStatusCmd(st Store) tea.Cmd {
	return func() tea.Msg {
		status, err := st.SyncStatus()
		return syncStatusMsg{status: status, err: err}
	}
}

// syncTickCmd schedules the next poll, at the rate the current state deserves.
func syncTickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg { return syncTickMsg{} })
}

// pollInterval is how soon this state wants to be looked at again.
func (s *syncState) pollInterval() time.Duration {
	if s.configured() {
		return syncPollInterval
	}
	return syncIdlePollInterval
}

// syncNowCmd runs a cycle, optionally compacting first. quitting is carried
// through untouched so the reply knows whether the user is still here.
func syncNowCmd(st Store, compact, quitting bool) tea.Cmd {
	return func() tea.Msg {
		status, err := st.SyncNow(compact)
		return syncDoneMsg{status: status, compact: compact, quitting: quitting, err: err}
	}
}

// snoozeSyncCmd defers the prompt without syncing.
func snoozeSyncCmd(st Store) tea.Cmd {
	return func() tea.Msg {
		status, err := st.SnoozeSync()
		return syncStatusMsg{status: status, err: err}
	}
}

// compactCmd collapses the pending change log without syncing.
func compactCmd(st Store) tea.Cmd {
	return func() tea.Msg {
		res, err := st.CompactChanges()
		return compactDoneMsg{result: res, err: err}
	}
}

// ---- The dialog ------------------------------------------------------------

// syncPurpose is why the dialog is on screen. It decides the question and what
// the escape hatch does — nothing else about the screen changes.
type syncPurpose int

const (
	// syncAsked: the user pressed S. Cancelling leaves everything as it was.
	syncAsked syncPurpose = iota
	// syncDue: the clock reached the prompt interval. Cancelling defers.
	syncDue
	// syncQuitting: the user is leaving with changes still here. Cancelling
	// stays in the app; the third answer leaves without syncing.
	syncQuitting
)

// syncScreen is the modal that asks. Like the unsaved-changes dialog it is a
// three-way question rather than a yes/no, and for the same reason: "sync" and
// "don't sync" leave out the answer the user most often wants when the log has
// been piling up for a day, which is "sync, but pack it down first".
//
//	     S, or the clock, or q with changes pending
//	                     │
//	           ┌─────────▼──────────┐
//	           │  sync now?         │
//	           └──┬────────┬────────┘
//	s ────────────┘        │        └──────────── esc / q
//	sync now               │        later, or leave without syncing
//	                       c
//	               compact, then sync
type syncScreen struct {
	sess    *session
	purpose syncPurpose

	// busy is set while a cycle is in flight. The dialog stays up rather than
	// popping, because a sync is the one thing here that takes long enough to
	// need saying so — and because popping would put the result on a screen
	// that never asked.
	busy bool
	note string // in-dialog progress or error line
}

func newSyncScreen(sess *session, purpose syncPurpose) *syncScreen {
	return &syncScreen{sess: sess, purpose: purpose}
}

func (s *syncScreen) Init() tea.Cmd { return nil }

func (s *syncScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case syncDoneMsg:
		s.busy = false
		if msg.err != nil {
			// Staying on the dialog with the error in it is deliberate: the
			// user asked for something that did not happen, and if they were
			// quitting, the choice they made is no longer available to make.
			s.note = "Sync failed: " + msg.err.Error()
			return s, nil
		}
		if msg.quitting {
			return s, tea.Quit
		}
		// Three things follow a completed cycle, and each is here for its own
		// reason:
		//
		//	pop(true)     a pull can have brought notes in, so the list under
		//	              this dialog is stale the moment the cycle finishes.
		//	syncStatusMsg the clock has moved. Without this the banner would
		//	              keep saying "sync is due" until the next poll — up to
		//	              a minute of the UI contradicting what just happened.
		//	status(...)   what it achieved, in the bottom line.
		return s, tea.Batch(
			pop(true),
			func() tea.Msg { return syncStatusMsg{status: msg.status} },
			status(syncSuccessLine(msg)),
		)

	case compactDoneMsg:
		s.busy = false
		if msg.err != nil {
			s.note = "Compaction failed: " + msg.err.Error()
			return s, nil
		}
		s.note = compactionLine(msg.result)
		return s, nil

	case tea.KeyPressMsg:
		if s.busy {
			return s, nil // every answer is already given; wait for the reply
		}
		switch {
		case key.Matches(msg, keys.SyncGo):
			s.busy = true
			s.note = "Syncing…"
			return s, syncNowCmd(s.sess.store, false, s.purpose == syncQuitting)

		case key.Matches(msg, keys.SyncCompact):
			s.busy = true
			s.note = "Compacting, then syncing…"
			return s, syncNowCmd(s.sess.store, true, s.purpose == syncQuitting)

		case key.Matches(msg, keys.SyncCompactOnly):
			s.busy = true
			s.note = "Compacting…"
			return s, compactCmd(s.sess.store)

		case key.Matches(msg, keys.SyncQuitAnyway):
			// Only the quit dialog offers this, and it has to say so to the
			// process's exit path as well as to itself — otherwise the exit
			// cycle would sync the very changes the user just declined to.
			//
			// The decline is recorded INLINE rather than as a command paired
			// with tea.Quit. A command is a goroutine, and the one thing that
			// must be true before the program stops is precisely this: a
			// decline that lost the race would be a user answering "no" and
			// being synced anyway. It costs nothing to do here — the local
			// store sets a flag, the HTTP store does nothing at all.
			if s.purpose == syncQuitting {
				_ = s.sess.store.DeclineExitSync()
				return s, tea.Quit
			}

		case key.Matches(msg, keys.SyncLater):
			// "Later" defers the clock; cancelling a dialog the user opened
			// themselves does not, because they were not being asked.
			//
			// Batch rather than Sequence: closing the dialog and telling the
			// server to stop asking are independent — one is a stack pop, the
			// other a round trip — and sequencing them would make the dialog
			// linger on screen for the length of a network call that has
			// nothing to say.
			if s.purpose == syncAsked {
				return s, pop(false)
			}
			return s, tea.Batch(pop(false), snoozeSyncCmd(s.sess.store))
		}
	}
	return s, nil
}

// syncSuccessLine says what a completed cycle achieved, in the status bar.
func syncSuccessLine(msg syncDoneMsg) string {
	line := "Synced with the hub"
	if msg.compact {
		line = "Compacted and synced with the hub"
	}
	if msg.status != nil && msg.status.Pending > 0 {
		// A cycle pushes in batches, so a very long backlog can survive one
		// run. Saying so beats a "Synced" that leaves the banner up.
		line += " — " + plural(msg.status.Pending, "change", "changes") + " still queued"
	}
	return line
}

// compactionLine reports a compaction in terms of what it removed rather than
// what it wrote: "12 changes packed into 3" is the sentence a person can check
// against the count they were just shown.
func compactionLine(res *models.CompactionResult) string {
	if res == nil || res.Removed() <= 0 {
		return "Nothing to compact — every pending change is already the only one for its note"
	}
	return fmt.Sprintf("Packed %d pending changes into %d", res.ChangesBefore, res.ChangesAfter)
}

func (s *syncScreen) View() string {
	var b strings.Builder
	b.WriteString(s.question())
	b.WriteString("\n\n")

	if s.note != "" {
		b.WriteString(helpStyle.Render(s.note))
		b.WriteString("\n\n")
	}

	// The answer row is generated from the same bindings the switch matches,
	// for the reason keymap.go exists: a footer that names a key the dispatch
	// no longer honors is a lie the compiler cannot catch.
	var row strings.Builder
	for i, bind := range keys.syncHelp(s.purpose) {
		if i > 0 {
			row.WriteString(helpStyle.Render("   "))
		}
		h := bind.Help()
		keyStyle := lipgloss.NewStyle().Bold(true)
		// "Quit anyway" is the answer that leaves changes behind, so it takes
		// the same error color the delete dialog gives its "yes" — the
		// asymmetry the other dialogs use, pointed at whichever answer loses
		// something.
		if s.purpose == syncQuitting && bind.Help().Key == keys.SyncQuitAnyway.Help().Key {
			keyStyle = errorTextStyle.Bold(true)
		}
		row.WriteString(keyStyle.Render(h.Key) + helpStyle.Render(" "+h.Desc))
	}
	b.WriteString(row.String())

	box := dialogBoxStyle.Render(b.String())
	return lipgloss.Place(s.sess.width, s.sess.height, lipgloss.Center, lipgloss.Center, box)
}

// question is the line at the top: what happened, and what is at stake.
func (s *syncScreen) question() string {
	summary := s.sess.sync.overdueSummary()
	switch s.purpose {
	case syncQuitting:
		return "Leaving GoNotes — " + summary + "."
	case syncDue:
		return "Sync is due — " + summary + "."
	default:
		return "Sync with the hub — " + summary + "."
	}
}
