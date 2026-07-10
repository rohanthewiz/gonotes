# Session: Terminal UI (Bubble Tea) for GoNotes

**Session ID:** `073d7738-0ebc-41fb-9b83-1a114ae252d1`
**Date:** 2026-07-10
**Branch:** master

## Context / Decision

The user wanted a convenient TUI for GoNotes. An earlier attempt existed on the
`old-add-tui-views` branch (single commit, ~2,500 lines) built with
bubbletea/bubbles/lipgloss. That branch was mined for clues but the code was
rebuilt fresh.

**Kept from the old branch:** the library stack (Bubble Tea + bubbles + lipgloss),
the screen inventory (login → list → detail → form → categories → search), and
vim-ish key conventions.

**Changed (fresh approach):**

- **Screen stack instead of flat screen enum.** The old prototype hand-wired every
  transition through a `screenID` switch in the root model. The rebuild has a small
  `screen` interface (`Init/Update/View`) and the root `appModel` keeps a
  `stack []screen` — push for drill-in (list → detail → form), pop for `esc`.
  Screens never reference each other; navigation happens via `pushMsg`/`popMsg`
  emitted as commands. A `refresher` interface lets a pop with `refresh: true`
  reload the exposed screen (e.g. list after a save).
- **`bubbles/list` instead of a hand-rolled list** — built-in fuzzy filtering (`/`),
  pagination, and help footer for free. The old branch had a separate 422-line
  search view; here `FilterValue()` feeds title + tags + description + first 2KB of
  body into the fuzzy filter, so `/` is a genuine content search.
- **`glamour`** renders note bodies as styled markdown in the detail viewport
  (bodies are markdown by convention — the export/import is Obsidian-compatible).
- **External editor:** `ctrl+e` on the form writes the body to a temp file and
  suspends the TUI via `tea.ExecProcess` for `$VISUAL`/`$EDITOR` (vim fallback),
  reading the file back on exit. Main convenience win over a textarea for long notes.

## What Was Built

### New `tui/` package (~1,400 lines)

- `tui.go` — root model, screen stack, shared `session` (user + terminal size),
  status bar on the bottom line (errors colored). `WindowSizeMsg` is forwarded to
  every screen in the stack so resumed screens aren't stale-sized. `ctrl+c` always
  quits.
- `commands.go` — every models call wrapped in a `tea.Cmd` (async; DB latency never
  blocks rendering) plus all message types. `syncNoteCategories` reconciles a
  comma-separated names field against the join table, auto-creating unknown
  categories. `saveNoteCmd` generates the note GUID on create (models expects the
  caller to supply it, same as the web API).
- `login.go` — sign-in + first-run registration. Fresh DB → registration mode
  automatically. Exactly one user → username prefilled, focus jumps to password.
- `browse.go` — home screen. Keys: `/` search, `enter` view, `n` new, `e` edit,
  `f` flag, `d` delete (confirm), `c` categories, `esc` peels back fuzzy filter
  then category filter, `q` quit. Category filter loads server-side via
  `GetCategoryNotes`; composes with `/`.
- `detail.go` — sticky metadata header + glamour-rendered body in a viewport.
  Falls back to raw text on render errors.
- `form.go` — Title/Description/Tags/Categories inputs, Private toggle, body
  textarea. `tab` cycles, `ctrl+s` saves from anywhere, `ctrl+e` external editor,
  `esc` cancels. Flag state is carried through on edit (no flag control on the
  form — flagging is a one-key list/detail action).
- `categories.go` — pick to filter (`enter`), `a` = all notes, `n` new (prompt
  modal), `d` delete (confirm). Renames/subcategories intentionally stay web-only.
- `confirm.go` — generic confirm + one-line prompt modals. Design note: on "yes"
  they pop FIRST, then run the action (`tea.Sequence`), so the result message
  lands on the screen that asked — keeps the dialogs fully generic.
- `styles.go` — lipgloss `AdaptiveColor` palette (works on light and dark terminals).

### Supporting changes

- `models/user_list.go` — `ListUsernames()` (active users, oldest first) for the
  login prefill. Only usernames exposed; runs pre-auth.
- `main.go` — `tui` subcommand → `runTui(dir)`: mkdir/chdir, logger quieted to
  `error` (stray Info lines would draw over the UI), `.env` pickup, `InitDB`,
  optional `InitEncryption` when `GONOTES_ENCRYPTION_KEY` is set. Skips web
  server, JWT, and sync client. The command declares its own `--dir/-d` flag
  because urfave/cli only accepts global flags *before* the subcommand.
- `README.md` — new "Terminal UI" section with key table.
- Deps added: bubbletea v1.3.10, bubbles v1.0.0, glamour v1.0.0 (lipgloss floated
  to the prerelease glamour requires).

## Gotchas Discovered

- **urfave/cli global flags**: `gonotes tui -d dir` fails with "flag provided but
  not defined" unless the command declares its own `-d`. Global flags only parse
  before the subcommand.
- **`q` on the login screen**: originally quit when fields were empty, which made
  usernames starting with "q" untypable. Only `esc` quits there now.
- **Sync tracking is free**: `insertNoteChange` is called inside
  `CreateNote`/`UpdateNote`/`DeleteNote` in the models layer, so TUI edits are
  recorded for hub-spoke sync with no extra work.
- **Homebrew `duckdb` CLI can't open the app's DB/WAL** (engine version mismatch
  with go-duckdb v1.8.3) — verify data through the app, not the CLI.
- **DuckDB single-writer**: don't run the TUI and web server against the same
  directory simultaneously (noted in README).

## Verification

- Two `expect`-driven pty smoke tests against the real binary in a scratch dir
  (pty sized via `stty rows 35 columns 110 < $spawn_out(slave,name)` — piping
  through `script` gave a zero-sized pty and blank frames):
  1. Fresh DB: register → create note with tags + new category → save confirmed →
     detail view (rendered heading visible) → `/` body search matched ("1 note") →
     quit.
  2. Relaunch: username prefilled, password-only login → persisted note present →
     category picker filter applied ("GoNotes — <category>") → esc clears →
     flag toggle → delete via confirm → quit.
- Full existing test suite passes (`gonotes`, `models`, `web/api`); `go vet` clean.

## Possible Follow-ups

- Two-pane layout (list + live preview) for wide terminals.
- Note rename of categories / subcategory support in the TUI.
- `teatest`-based automated regression tests for the screens.
