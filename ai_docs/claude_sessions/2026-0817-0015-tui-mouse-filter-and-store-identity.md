# Session: TUI Mouse Support, a Filter That Actually Filters, and Store Identity

**Session ID:** `cd04b7f3-89e9-4e8f-9f76-31502799615b`
**Date:** 2026-08-17
**Branch:** master
**Base commit:** `05bc968` — Add ⌘ accelerators and a cats plugin manifest

## Context

Reported symptom: "In TUI mode filter and mouse click are not working." Two
independent bugs, both silent — nothing logged, nothing crashed, no test failed.
A third defect (the local-vs-HTTP store decision) was found while investigating
and fixed in the same session at the user's direction.

---

## 1. Mouse: never enabled at all

**Root cause.** Bubble Tea v1 turned mouse reporting on with a program option
(`tea.WithMouseCellMotion()`). v2 moved it — exactly as it moved the alternate
screen — onto the `tea.View` the model returns. The v1→v2 port fixed
`View.AltScreen` and stopped there, so the program never emitted the DECSET that
asks the terminal to report clicks. No mouse byte was ever sent by the terminal,
no mouse message was ever parsed, and any handler would have been unreachable
code.

**Evidence.** A pty capture of the launch sequence showed `?1049h` (alt screen),
`?2004h` (bracketed paste) and the kitty keyboard push `\e[>4;2m\e[=1;1u\e[?u`,
and **no** `1000/1002/1003/1006` at all. This is the kind of bug that is obvious
on the wire and invisible everywhere else.

**Fix.** `tui/mouse.go` (new) plus one line in `appModel.View()`:

- `v.MouseMode = mouseMode` where `mouseMode = tea.MouseModeCellMotion`.
  CellMotion rather than AllMotion: the latter reports every cell the pointer
  crosses with no button down, a steady stream nothing reads.
- `listRowAt(l *list.Model, y int) (int, bool)` maps a terminal row to a
  `VisibleItems()` index. Header height is derived from the widget's public
  state (`ShowTitle`/`ShowFilter`/`FilteringEnabled`/`ShowStatusBar` plus the
  styles' vertical frame sizes), not hardcoded, so it follows the filter prompt
  when it replaces the title. The item region repeats on a stride of
  `delegate.Height() + delegate.Spacing()`, and a click landing in the blank gap
  between rows is rejected rather than rounded down onto the row above — the
  alternative puts the selection one row off every third line.
- `clickTracker` implements double-click by timing (500 ms), because SGR mouse
  reports carry a button and a cell and no click count. A completed double-click
  resets the tracker so a triple-click cannot fire the action twice.
- `wheelList` translates wheel notches into `CursorUp`/`CursorDown`. The bubbles
  list has no mouse handling whatsoever in v2.1.1 — only the viewport does.

**Wired:** browse (click selects, double-click opens, wheel scrolls, clicks over
the preview pane ignored) and categories (click selects, double-click applies the
category filter — same path as enter, factored into `categoriesScreen.pick()`).
The detail screen got wheel scrolling for free; its viewport had handled
`tea.MouseWheelMsg` all along and was only starved of events. The modals
(confirm, agent picker, form) stay keyboard-only: hit-testing a
`lipgloss.Place`'d box means reproducing the centering math, which a two-choice
dialog does not earn.

**Trade-off, deliberate.** A terminal reporting mouse events to the application
stops using the mouse for its own drag-to-select. Most terminals keep a
shift/option bypass; a browser-canvas host decides per pane from the app's own
mode. One line to revert.

**Verified** end-to-end in a pty: `?1002h` and `?1006h` now emitted, and a
scripted SGR double-click opened a note.

---

## 2. Filter: matched everything

**Root cause.** `noteItem.FilterValue()` fed 2000 runes of note body to
`list.DefaultFilter`, which is sahilm/fuzzy — a **subsequence** matcher. Over a
paragraph of prose, nearly every short query's letters appear in order somewhere.
Measured on five realistic notes, three- to five-letter queries routinely matched
**all five**. The list reported "N notes • N filtered" and narrowed nothing,
which from the outside is indistinguishable from a filter that does not work.

This is why the model-level tests all passed while the feature was useless: they
asserted that the right note was *among* the matches.

**Fix.** `FilterValue()` now emits two halves separated by a NUL (`filterSep` —
the one byte that cannot occur in user-typed note text), and `notesFilter`
replaces `DefaultFilter` on the notes list only:

- **head** (title + tags + description): fuzzy, unchanged. Short strings are
  where typo tolerance and abbreviations earn their keep.
- **body**: plain case-insensitive substring, every whitespace-separated token
  required. So a content search still finds a note that merely mentions a term in
  passing, while a three-letter query no longer drags in everything.
- **MatchedIndexes come from the head only.** This was a second, cosmetic half of
  the same bug: the list delegate applies those indexes to the item's *title* to
  underline matched characters, so an index pointing into a body underlined
  arbitrary letters of an unrelated title.

The category list keeps `DefaultFilter` — its FilterValue is a bare category
name, exactly the short haystack fuzzy matching is good at.

**Verified** end-to-end in a pty against two notes with prose bodies: a query
matching one note's body produced "1 note • 1 filtered".

---

## 3. Store selection: `-d` was silently overridden

Found while reproducing bug #1, and fixed at the user's direction.

**Root cause.** `runTui` probes `/api/v1/health` and, on any answer, returns
early into HTTP mode. `ProbeServer` could answer *"is a GoNotes server
running?"* — but the question that justifies the early return is *"is a server
running **against this data directory**?"* The lock-avoidance rationale (bytdb is
single-process) only holds when the server owns *those* files. Health returned
`{"status":"ok"}` and nothing else, so the two cases were indistinguishable, and
an explicit `-d` was overridden by whatever happened to be listening — pointing
the TUI at a different dataset, with writes enabled and no sign on screen.

This is not hypothetical: a scripted pty run in this session, launched with a
throwaway `-d`, attached to a live local server and modified real data before the
mistake was noticed.

**Fix.**

- `models.ResolvedDataDir()` — absolute, symlink-resolved path of `DataDir`; the
  identity of a dataset as opposed to the many ways one can be spelled. Symlinks
  are resolved because macOS makes it concrete (`/tmp` → `/private/tmp`), where a
  string compare would produce a *false mismatch* and fail toward a lock
  collision.
- `/api/v1/health` reports `data_dir`.
- `ProbeServer` returns `(ServerInfo, bool)`. An absent `data_dir` travels as
  `""` meaning **unknown**, never compared as if it were a path.
- `decideStore` (in `main.go`, split out as a pure function so the rule is
  testable without a terminal, a data directory, or a server):

| Situation | Store | Shown |
|---|---|---|
| `GONOTES_URL` set, answering | HTTP, no identity check | badge: the server |
| `GONOTES_URL` set, dead | local | badge `local`, notice "No server at …" |
| Default URL, same data dir | HTTP | badge: the server |
| Default URL, different data dir | **local** | badge + notice naming both dirs |
| Default URL, server too old to say | HTTP (old behavior) | badge + notice |
| Default URL, no server | local | nothing |

**Why the rule splits on who chose the URL.** An explicit `GONOTES_URL` may point
at another machine, where a data-directory path is a string in someone else's
filesystem; comparing it would break every legitimate remote setup. So a named
server is honored as named. The default URL is a *guess* that a local server
holds these files, and that is the guess worth checking.

**Two signals, because one was not enough.** The mode started as a startup status
line; a pty run showed the cats capture hint claiming that line about a second
after launch. So the persistent half became `Mode.Badge`, rendered beside the
list title (`GoNotes · local · <dir>`) for the life of the process, while
`Mode.Notice` explains the decision once. Both verified: badge renders in a
cats-connected run, notice renders with the cats env cleared.

**Regression closed on the way.** Choosing local over a live server is a bet that
the server locked a *different* directory. If that bet is ever wrong the files
will not open, so an `InitDB` failure with a server answering falls back to HTTP
with a notice rather than refusing to start.

---

## Method note: how these were actually found

Model-level Bubble Tea tests could not see either bug — one lived in the escape
sequences the program emits, the other in a matcher's selectivity. A small pty
harness (`creack/pty`, a script of `send`/`wait`/`mark` directives) drove the real
binary in a real terminal and captured the raw byte stream, which is how the
missing DECSET and the kitty/`modifyOtherKeys` key encodings were confirmed.

**Hard-won operational rule:** `gonotes tui` ignores `-d` for *data* whenever a
server answers the probe, and a cached token then skips login entirely. Any
scripted run of the TUI must export `GONOTES_URL=http://127.0.0.1:<dead-port>`
(so the probe fails into local mode) **and** point `HOME` at a scratch directory
(so the token cache is out of reach). To exercise HTTP mode, start a throwaway
server on a spare port against a throwaway data directory. Bug #3 reduces the
blast radius of forgetting, but only against a server running the new binary — an
already-running old one omits `data_dir` and still gets the old trusting
behavior.

## Tests added

- `tui/mouse_test.go` — `View()` must not leave `MouseMode` at `None`; row
  geometry checked against the **rendered** view rather than a second copy of the
  arithmetic; gap/header/past-the-end rejection; click-selects/double-click-opens;
  the slow second click; preview-pane clicks and wheel ignored.
- `tui/filter_test.go` — **selectivity**, not "does it match": each query must
  return *exactly one* note. A match-only assertion passed against the broken
  filter every time. Plus body-only search preserved, multi-token narrowing,
  case-insensitivity, and matched-indexes staying inside the head.
- `tui_mode_test.go` — all six `decideStore` branches, badge/notice presence per
  branch, and symlinked/relative directory spellings comparing equal.
- `web/api/sync_test.go` — health carries an absolute, non-empty `data_dir`.
- `tui/store_http_test.go` — the probe carries `data_dir` back; a server
  predating the field reports empty (unknown), never a match.

Full suite and `go vet ./...` pass.

## Files

| File | Change |
|---|---|
| `tui/mouse.go` | **new** — mode, hit-testing, wheel, double-click |
| `tui/tui.go` | `v.MouseMode`; `Mode{Badge,Notice}`; `session.mode`; startup notice from `Init` |
| `tui/browse.go` | mouse handling; `notesFilter` + `filterSep`; `title()` with badge |
| `tui/categories.go` | mouse handling; `pick()` factored out of the enter case |
| `tui/store_http.go` | `ServerInfo`; `ProbeServer` returns it; `ServerURLIsExplicit` |
| `models/db.go` | `ResolvedDataDir()` |
| `web/api/sync.go` | health reports `data_dir` |
| `main.go` | `decideStore`, `badgeForURL`, `localBadge`, `sameDir`; InitDB fallback |

## Follow-ups not done

- The agent picker and confirm dialog remain keyboard-only.
- A `--local` / `--remote` flag pair was discussed as an explicit escape hatch for
  scripts and tests; not implemented (the dead-URL convention covers it today).
- The TUI never surfaces *why* an HTTP request failed mid-session; only the
  startup decision is now labelled.
