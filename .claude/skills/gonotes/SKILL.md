---
name: gonotes
description: Work with the GoNotes app — capture notes (especially from the clipboard), query/export/import them, run the TUI inside a cats pane, and navigate the codebase. Use whenever the task mentions gonotes, "save this as a note", "add a note from my clipboard", note capture, the notes DB, note locks, the GoNotes web/TUI/sync layers, or running GoNotes as a cats plugin / cats-native client.
---

# GoNotes

GoNotes is a self-hosted note app: a Go binary that serves a web UI (`gonotes`),
a terminal UI (`gonotes tui`), and Markdown/gob import-export commands. It is
also a **cats-native client** — inside a cats pane the TUI reports its state to
the host, follows the host theme, takes ⌘ accelerators, and can capture a
sibling agent pane's output straight into a note (see [Inside cats](#inside-cats)).

## Storage: two bytdb files, one process

Data lives in **two [bytdb](https://github.com/rohanthewiz/bytdb) databases**
under `<dir>/data`, where `<dir>` defaults to `~/.gonotes`:

| File | Holds |
|---|---|
| `notes_public.bytdb` | Non-private notes plus all shared/system tables (users, sync state, locks) |
| `notes_private.bytdb` | Private notes only — the **whole database** is encrypted at rest when `GONOTES_ENCRYPTION_KEY` (exactly 32 chars) is set |

DuckDB is **gone** (replaced 2026-08-14, gonotes `bdc0796`). A leftover
`~/.gonotes/data/notes.ddb` is a dead pre-migration artifact — never read it and
never assume it reflects current data. There is no in-memory cache layer either;
bytdb is the read path.

Because encryption is now whole-database rather than per-note, a server that
opened the private DB with the key serves **plaintext** over the API. There is
no per-note ciphertext to worry about (`Note.EncryptionIV` survives only for
transport compatibility).

**bytdb is single-process.** Whoever opens the two files owns them. That still
governs the CLI:

- `import-md`, `export-md`, `import-gob` open the databases directly → the
  server (or `GoNotes.app`) must be **stopped**.
- `gonotes tui` no longer has that problem — it decides for itself (below).

```bash
curl -s -m 2 http://localhost:8444/api/v1/health   # {"success":true,...} => server is up
pgrep -fl gonotes                                  # what's actually running
```

Default port is `8444` (`web.WebPort`, override with `--port` / `$GONOTES_PORT`).

### Which notes is the TUI attached to?

`runTui` (`main.go`) probes `/api/v1/health` and picks a mode. "A server
answered" is deliberately *not* the deciding question — deferring is only correct
when that server holds *these* files, so `/api/v1/health` reports `data_dir`
(absolute, symlink-resolved) and the launch compares it:

| Situation | Mode |
|---|---|
| `GONOTES_URL` set explicitly | **HTTP**, honored as named, no identity check (it may be another machine) |
| `GONOTES_URL` set but not answering | local, with a notice naming the URL |
| Unset, server reports the **same** `data_dir` | **HTTP** |
| Unset, server reports a **different** `data_dir` | **local** — the server is ignored and says so |
| Unset, server too old to report `data_dir` | HTTP, with a notice |
| No server | local |

Every outcome is labelled on screen (`tui.Mode`), so the TUI always says which
notes these are. In HTTP mode `models.InitDB` is skipped entirely — no file lock,
no conflict with the running server or MacApp.

### CLI traps worth knowing before you type

- **`gonotes serve` is not a subcommand.** Serving is the default action, so
  `gonotes serve -d <dir>` silently ignores `-d`. Use `gonotes -d <dir> -p <port>`.
- **`gonotes -d <dir> tui` silently ignores `-d`.** The `tui` command declares its
  own `--dir` with the same default and command-level flags win. Use
  `gonotes tui -d <dir>`.
- **`<dir>/config/cfg_files/.env` overrides the shell environment.** The loader
  calls `os.Setenv` unconditionally, truncates an unquoted value at `#`, and
  strips surrounding quotes. Both `serve` and `runTui` chdir into `<dir>` before
  loading, so one file covers both.
- `GONOTES_JWT_SECRET` lives in that `.env` (0600). Unset, `InitJWT` falls back to
  a publicly known development constant — fine locally, not fine once hooks and
  phone push carry auth off the machine. It is shared hub↔spoke, so on a
  configured spoke it must match the hub.

## Adding a note from the clipboard

This is the most common ask. Use the bundled script — it handles clipboard
reading, title extraction, JSON escaping, auth, and categories. Paths below are
relative to the **gonotes checkout**:

```bash
.claude/skills/gonotes/scripts/gn-clip.sh                       # title = first line of clipboard
.claude/skills/gonotes/scripts/gn-clip.sh -t "Deploy checklist" # explicit title
.claude/skills/gonotes/scripts/gn-clip.sh -t "Auth notes" -c "Work/backend" -g "auth,jwt" -p
.claude/skills/gonotes/scripts/gn-clip.sh -s                    # summarize the clipboard instead of storing it raw
```

| Flag | Meaning |
|---|---|
| `-t <title>` | Title. Omitted → first non-blank clipboard line (leading `#`s stripped) becomes the title and is removed from the body. |
| `-k` | Keep that first line in the body too (no removal). |
| `-s` | Summarize the clipboard (see below). The body becomes the summary, the title is derived from the content. |
| `-m <model>` | Model for `-s` (default `haiku`); any alias `claude --model` accepts. |
| `-c <cat[/sub]>` | Category, created if it doesn't exist. `Work/backend` = category `Work`, subcategory `backend`. Repeatable. |
| `-g <tags>` | Comma-separated tags. |
| `-p` | Mark private (stored in the encrypted database). |
| `-f` | Set the follow-up flag. |
| `-u <user>` | Username for login (else `$GONOTES_USER`, else prompt). |
| `-U <url>` | Base URL (default `http://localhost:8444`, or `$GONOTES_URL`). |
| `-n` | Dry run — print the JSON payload, post nothing. |

Auth: the script caches a JWT at `~/.gonotes/.api_token` (mode 600) and reuses
it until it 401s, then logs in again. Supply the password non-interactively via
`$GONOTES_PASSWORD`, or `$GONOTES_SYNC_PASSWORD_B64` (base64) if the machine is
already configured as a sync spoke; otherwise it prompts.

Run `-n` first when the clipboard content is unclear — it shows exactly what
would be stored without writing. `-n` still runs the summarizer, so `-s -n` is
the way to see a summary before committing it.

### Summarizing on the way in (`-s`)

`-s` sends the clipboard through the **local `claude` CLI** — not an HTTP API —
because the CLI already holds the machine's credentials, so no key has to live
in the environment or in `.env` for this to work. `ANTHROPIC_API_KEY` is not
consulted and does not need to be set.

What lands in the note:

- **body** — the summary, in Markdown, shaped like the source (prose stays
  prose, an enumeration stays bullets). **The raw paste is not kept**; when the
  original matters, capture it first without `-s` and summarize separately.
- **title** — derived from the content, not from the first line. An explicit
  `-t` still wins.
- **description** — one or two sentences, and only when the title alone leaves
  the note ambiguous. Most summaries come back without one, which is correct.

The model is asked for a strict JSON object (`{title, description, body}`); a
reply that will not parse aborts the run and prints what came back, so a bad
summary never becomes a note. Same for an empty body. The clipboard is untouched
either way, so a re-run costs only the model call.

The call is deliberately made lean: cwd is an empty temp dir, MCP servers and
tools are off, and session persistence is disabled. Claude Code otherwise
discovers `CLAUDE.md`, skills and MCP config from the working directory — inside
this checkout that measured ~20k cached prompt tokens per summary against ~4k
lean, all of it irrelevant to condensing a paste.

### The same summarizer in the UIs

The feature is not script-only. The Go side lives in **package `summarize`**,
which is the same prompt and the same lean invocation, and it is reachable three
ways:

| Where | How | What it summarizes |
|---|---|---|
| `gn-clip.sh -s` | the script's own call | the clipboard |
| TUI | `ctrl+r` in the note list | the clipboard → a new, **unsaved** note |
| TUI | `ctrl+r` on the note form | the body on screen, replaced in place |
| Web | the clipboard button in the toolbar | the clipboard → a new, unsaved note |
| Web | **Summarize** in the edit footer | the body in the editor, replaced in place |

Two rules hold everywhere, and they are the whole safety story:

- **Nothing is saved.** A summary lands in a form and waits for `ctrl+s` /
  **Save**. Cancelling walks away from all of it, and the note on disk is
  untouched until then. The status line always says the body was replaced.
- **A title you typed is never overwritten** — only an empty title or
  description gets filled in. Same rule as `-t` outranking the generated title.

The TUI tags clipboard summaries `summary`, the way captures are tagged
`capture`, so "what did a model condense for me" is one `/summary` away in the
filter. The web form has no tags field, so summaries made there are untagged —
add the tag from the TUI (or `gn-clip -g summary`) if you rely on that filter.

**Where the CLI runs is where the notes are.** The summarizer is a subprocess of
whoever owns the databases: the TUI itself in local mode, and the **server** in
HTTP mode (`POST /api/v1/summarize`, authenticated, stores nothing;
`GET /api/v1/summarize` reports `{available, default_model}`). A TUI or browser
attached to a hub on another machine therefore summarizes with *that* machine's
`claude` and *that* machine's credentials. On a host without the CLI installed
the web buttons never appear — they ship hidden and are revealed only by that
GET — and the endpoint answers 503.

The browser reads the clipboard itself, through `navigator.clipboard.readText`,
which needs a secure context (`localhost` counts; a plain-http LAN address does
not) and a permission the user can refuse. When it is refused the toolbar button
opens an empty note and says to paste and use **Summarize** instead — the other
door reaches the same place.

### Doing it by hand (server up)

The whole flow is four calls; `jq -n --arg` is what keeps arbitrary clipboard
text from breaking the JSON:

```bash
TOKEN=$(curl -s -X POST http://localhost:8444/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"me","password":"…"}' | jq -r '.data.token')

pbpaste > /tmp/clip.txt
jq -n --arg guid "$(uuidgen)" --arg title "Pasted note" --rawfile body /tmp/clip.txt \
   '{guid:$guid, title:$title, body:$body, is_private:false, is_flagged:false}' \
| curl -s -X POST http://localhost:8444/api/v1/notes \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d @-
```

`guid` and `title` are the only required fields; a duplicate `guid` returns 409.

### Doing it with the server stopped

`import-md` treats a plain `.md` file as a new note: filename → title,
containing folder → category. It writes the generated `guid` back into the
file, so a re-import updates rather than duplicates.

```bash
mkdir -p /tmp/gnclip/Inbox                      # "Inbox" becomes the category
pbpaste > "/tmp/gnclip/Inbox/Pasted $(date +%F).md"
gonotes import-md -i /tmp/gnclip -u <username>
```

## Other common tasks

**Read notes back** (server up):

```bash
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8444/api/v1/notes?limit=20' | jq '.data[].title'
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8444/api/v1/notes/search?q=bytdb' | jq
```

**Markdown round-trip** (server stopped) — export is Obsidian-compatible and
idempotent in both directions, anchored on the frontmatter `guid`:

```bash
gonotes export-md -o ~/notes-vault -u <user> [--skip-private]
gonotes import-md -i ~/notes-vault -u <user>
```

Private notes export **decrypted** by default; `--skip-private` omits them.

**`import-md` is an excellent witness and a poor migrator.** It requires the user
to already exist, routes private notes through `CreateNote` (so their
`created`/`updated` reset to import time), and trims trailing whitespace. Use
`scripts/migrate` for anything that has to preserve identity; use `import-md` to
re-export and byte-diff as an independent check.

**Terminal UI**: `gonotes tui` (or `gonotes tui -d <dir>`). Keys: `/` search,
`n`/`e` new/edit, `c` category filter (then `s` for that category's
subcategories, `space` to toggle several, `enter` to filter), `f` flag, `d`
delete, `D` duplicate, `S` sync, `ctrl+e` edit body in `$EDITOR`, `ctrl+s` save,
`ctrl+g` capture an agent pane, `ctrl+r` summarize (the clipboard in the list,
the body on the form — see above), `q`/`esc` quit — which stops to ask when changes
have not reached the hub (`s` sync, `c` compact & sync, `p` compact only, `q`
leave anyway, `esc` keep working). Leaving a dirty form raises a three-way
dialog — `s`/`enter` save & exit, `d` discard, `esc` keep editing. The note form's
Categories field takes the same `Name/Sub` notation as the frontmatter and
`gn-clip -c`, creating unknown categories and subcategories on save.

`D` (browse or detail) opens the duplicate dialog: an editable title prefilled
`COPY <title>`, then a checkbox per part the copy can carry over — categories
**with their per-note subcategory selection** first, then body, description,
tags, private and flagged, all checked, and only shown when the source note has
them. `↑`/`↓` move, `space` includes, `enter` duplicates, `esc` cancels. The web
UI's Duplicate button opens the same dialog. No lock is taken: duplicating reads
the original and writes a new note.

**Build & test**: `go build -o gonotes .` (pure Go now — bytdb needs no cgo);
`go vet && go test -race ./...`. Tests that touch models open their own
databases (`models.InitTestDB`), so stop the server before the full suite.

**Charm stack**: Bubble Tea **v2**. Canonical import paths are `charm.land/*/v2`
(bubbletea v2.0.8, bubbles v2.1.1, lipgloss v2.0.6, glamour v2.0.1) — the
`github.com/charmbracelet/*` paths fail with a module-path mismatch. The one
exception is teatest, still `github.com/charmbracelet/x/exp/teatest/v2`. In v2:
space stringifies as `"space"` not `" "`, the root model returns `tea.View`
(AltScreen and WindowTitle are fields on it), and key messages are
`KeyPressMsg` — matching the `KeyMsg` *interface* would double-fire once release
reporting is on.

## Inside cats

GoNotes is both a **cats plugin** (how a pane gets opened) and a **cats-native
client** (what the TUI does once running). They are orthogonal — the native side
works identically whether the pane came from the ⌘K palette, from `gonotes tui`
typed into a shell, or from nothing at all.

### Capability ladder

Everything is gated on `cats.Caps` and degrades silently. Tier 0 (any terminal)
loses nothing.

- **Tier 0** — not in a cats pane, or the control socket is not answering.
- **Tier 1** — `CATS_ENV=1` + `CATS_PANE_ID` present *and* the control socket
  (`CATS_CONTROL_SOCKET`) answered `ping`. The hook socket is `CATS_SOCKET_PATH`,
  checked separately with a `stat`.

`cats.DetectEnv()` is pure environment reads (no IO); `.Probe()` adds the dial.
A Tier-0 verdict carries a one-phrase `Reason` — degradation is silent but not
invisible when someone goes looking.

The `cats/` package (`detect.go`, `client.go`, `hooks.go`, `events.go`) is
**hand-copied, stdlib-only, and must never import the cats module**. Verbs:
`ping`, `pane.list` / `ResolvePane`, `pane.focus`, `pane.send_input`,
`chat.send`, `config.get`, `events.subscribe`, `capture`.

### What the integration actually does

| Feature | Where | Behavior |
|---|---|---|
| **Hook reporting** | `tui/cats_glue.go` | Source/agent `"gonotes"` (unprefixed — `cats:` is reserved). The **`$EDITOR` session is the one reported span**: `working` + `custom_status = "editing: <title>"` around `tea.ExecProcess`, so quitting a long edit fires the cats "finished" badge/toast/phone push. Saves and captures are deliberately *not* spans — they finish faster than the badge renders. `Release()` runs after `p.Run()` returns even when the TUI failed to start (no stale badges). |
| **Host theme sync** | `tui/catstheme.go` | Synchronous `config.get` before the first frame (bounded by `ProbeTimeout`), then live `theme_changed` frames. Host colors → `Palette` (hex strings; `Sel` = accent blended over bg at 0.30). `hostThemed` makes the host outrank the terminal's own OSC 11 background report, which would otherwise undo the sync on first repaint. |
| **Host identity** | `tui/cats_glue.go` | `v.WindowTitle` is a pure function of model state each frame — `GoNotes`, `<n> notes — GoNotes`, `editing: <title> — GoNotes`. OSC 7 once at startup. |
| **Capture-to-note** | `tui/capture.go` | `ctrl+g` in browse → modal picker of sibling agent panes (cached `pane.list`, 2s rate limit, blocked > working > idle, own pane excluded) → `Capture(pane, CaptureRecent, 200)` → note form prefilled with title `Capture: <agent> — <timestamp>`, tag `capture`, **unsaved**. `ansi` and `unwrap` are off (a note stores markdown); `stripEscapeSequences` removes sequences whole as a second line of defense. At Tier 0 the door answers with one status line. |
| **⌘ accelerators** | `tui/metakeys.go` | ⌘S save, ⌘E edit (`e` in lists / `ctrl+e` on the form), ⌘F flag, ⌘D delete, ⌘G capture, ⌘/ filter. Every other armed chord is **swallowed** — "⌘S didn't work" must never type an `s`. Each row has a command-mode twin and a typing-mode twin (may be empty); which applies comes from the `texter` interface. Bubble Tea v2 engages the kitty keyboard protocol unconditionally, so every gonotes pane passes cats' `cmdGoesToPane` with no enabling work. |
| **Lock jump-to-pane** | `tui/locked.go` | The contention dialog offers `g` — go to the pane holding the note — via `pane.focus`. Offered only at Tier 1, when the holder recorded a pane handle, and when it is not our own. |

The goroutine rule, stated at the top of `tui/cats_glue.go`: *cats-layer
goroutines may touch only the transport objects and `p.Send`; every model/screen/
style mutation happens in `Update` on a typed message* (`catsReadyMsg`,
`catsThemeMsg`, `catsEventMsg`, `catsPanesMsg`, `captureDoneMsg`). The initial
probe is a `tea.Cmd`, not a goroutine — `Program.Send` blocks before the program
starts, which would deadlock shutdown on a TUI that failed to launch.

### Plugin

`cats-plugin.toml` at the repo root, id `rohanthewiz.gonotes`.

```bash
catctl plugin link .                              # from the gonotes checkout; fires [[build]]
catctl plugin run rohanthewiz.gonotes             # bare run takes the first action: tui
catctl plugin run rohanthewiz.gonotes serve       # the web server
```

`plugin link` installs a **symlink**, and the link is what runs
`mkdir -p bin && go build -o bin/gonotes .` — so `bin/gonotes` only refreshes on
relink. The `tui` action carries **no `-d` flag on purpose**: the argv is exec'd
directly with no shell, so `~/.gonotes` would create a directory literally named
`~`; `gonotes tui` already defaults `--dir` to `$HOME/.gonotes` via
`os.UserHomeDir` and chdirs in. Confirmed live: a plugin-launched pane has cwd
`/Users/<you>/.gonotes`, not the project.

cats-side, gonotes holds seat **4** in `AGENT_HUE` (`cmd/catway/web/index.html`)
— its FNV fallback collides with claude's slot 1, which is exactly the pane it
sits beside most.

### Testing against a host

`catctl capture` **cannot** observe an alt-screen TUI — it returns a stale or
primary-buffer snapshot, and `pane.send_input` reports `ok` with nothing
observable coming back. Capture-to-note, theme repaint, ⌘ chords and the
`modes.kitty` set-form question all need a human at the keyboard. For automated
smoke runs, use a pty harness (`pty.fork` + `TIOCSWINSZ`, answering the OSC 11
query) plus a scripted control/hook socket; `script -q /dev/null` inherits a 0x0
winsize and renders nothing. Keep the fake socket directory **short** — macOS
caps `sun_path` at 104 bytes and `AF_UNIX path too long` reads like a
permissions error.

## Note locks

Two sessions cannot edit one note. The server arbitrates (`models/lock.go`,
`web/api/locks.go`); `tui/lock.go` holds the client half — a per-process
`SessionID` (fresh UUID; two GoNotes in the *same* pane are two sessions), a
human-recognizable label (cats pane handle → hostname → truncated id), and a
heartbeat that renews leases and reports when one is lost.

- `GET /api/v1/note-locks` — every live lease in one call, for list badges.
- Contention dialog (`tui/locked.go`): `r`/`enter` open read-only, `t` take over,
  `g` go to their pane, `esc` never mind. Waiting is deliberately not offered — a
  renewed lease never lapses.
- On the form: `ctrl+l` retake a lease lost or stolen while the form stayed open.
  A stale-write conflict forks to `l` load theirs (drops your edits) / `o`
  overwrite theirs / `esc` decide later.

## Codebase map

| Path | What lives there |
|---|---|
| `main.go` | urfave/cli wiring: default action serves; subcommands `tui`, `import-gob`, `export-md`, `import-md`. `runTui` holds the local-vs-HTTP mode decision. |
| `models/` | Data layer + business logic: `note.go`, `category.go`, `user.go`, `db.go` (two bytdb engines), `encryption.go`, `lock.go`, `sync_*.go` (including `sync_compact.go`, the change-log compactor). |
| `web/routes.go` | Every route in one file — read it first when looking for an endpoint. |
| `web/api/` | JSON handlers. All responses use the `APIResponse` envelope: `{success, data?, error?}`. |
| `web/pages/` | Server-rendered HTML built with `rohanthewiz/element` (+ Monaco editor). |
| `summarize/` | The `claude`-CLI summarizer: prompt, lean invocation, strict-JSON parsing. Called by `web/api/summarize.go` and by the TUI's Store. |
| `cats/` | The cats transport: `detect.go`, `client.go`, `hooks.go`, `events.go`. Stdlib only; never imports cats. |
| `tui/` | Bubble Tea v2 screens (`browse`, `detail`, `form`, `categories`, `subcategories`, `login`, `confirm`, `locked`, `sync`) + the seams: `store.go` / `store_local.go` / `store_http.go`, `keymap.go`, `palette.go`, `styles.go`, `markdown.go`, `mouse.go`, `lock.go`, and the cats glue (`cats_glue.go`, `catstheme.go`, `capture.go`, `metakeys.go`). |
| `md_*.go`, `import_gob.go` | Markdown frontmatter format, import/export, legacy gob import. |
| `scripts/migrate` | The identity-preserving DuckDB → bytdb migrator (historical, but the reference for "move data without changing who you are"). |
| `cats-plugin.toml` | Plugin manifest — build command and the `tui` / `serve` actions. |
| `ai_docs/claude_sessions/` | Session logs — check for prior context via `/sess-list`, `/sess-load`. |
| `../cats/ai_docs/cats-gonotes-intg.md` | The integration plan **and** running status record: every phase, every decision, and what each one cost. |

Conventions in this repo: errors are wrapped with `serr` and logged once at the
top with `logger.LogErr`; handlers authenticate via
`api.GetCurrentUserGUID(ctx)` and return 401 themselves rather than relying on
route-level middleware; every query is user-scoped by GUID.

### The `Store` seam

`tui/store.go` is a 23-method interface every screen goes through;
`store_local.go` is one-line pass-throughs to `models.*` and `store_http.go`
talks to the API (gn-clip conventions: `GONOTES_USER` / `GONOTES_PASSWORD` /
`GONOTES_SYNC_PASSWORD_B64`, token cached at `~/.gonotes/.api_token`, validated
via `/auth/me`). Two methods have no models analog:

- `ResumeSession()` — get past the login screen without a password. Local always
  declines.
- `ListUsernames()` — `httpStore` always returns `ErrNoUserList`, because "list
  every account" is an endpoint a shared hub must not have. The distinction from
  an *empty* list is load-bearing: empty is what puts the login screen into
  first-run registration mode.

On 401 the HTTP store does **one** silent re-login then retries; on failure it
reports the original 401, never the re-login error.

## Note model

`models.NoteInput` (the API create/update payload):

`guid` (required), `title` (required), `description`, `body`, `tags`
(comma-separated string, not an array), `is_private`, `is_flagged`.

Categories are attached separately, after the note exists:

```bash
POST /api/v1/categories                              {"name":"Work"}
POST /api/v1/notes/<note_id>/categories/<cat_id>     {"subcategories":["backend"]}
```

Markdown frontmatter uses a different shape — `guid`, `title`, `description`,
`tags` (list), `categories` (list of `Name` or `Name/Subcategory`), `private`,
`flagged`, `created`, `updated`. See `md_format.go`.

## Sync (hub-spoke)

Spokes sync to a hub, configured entirely by env vars (`GONOTES_SYNC_ENABLED`,
`_HUB_URL`, `_USERNAME`, `_PASSWORD_B64`, `_INVITE_TOKEN`, `_INTERVAL`,
`_MODE`, `_PROMPT_AFTER`, `_ON_EXIT`, `_COMPACT`) read from the environment or
`<dir>/config/cfg_files/.env`. Conflict resolution is delete-wins, then
last-writer-wins on `authored_at`; conflicts land in the `sync_conflicts`
table. Full setup walkthrough is in `README.md`.

**Nothing syncs in the background by default.** `GONOTES_SYNC_MODE` defaults to
`prompt`: the client keeps a clock and reports itself *due* once
`GONOTES_SYNC_PROMPT_AFTER` (default `2h`) has passed since the last successful
cycle; a UI turns that into a question. `auto` is the old timer, and only then
does `_INTERVAL` mean anything. So a spoke that looks configured and shows no
"Sync cycle completed" lines is working as designed — check `mode` in the
status, not the logs.

A cycle runs when: a prompt is answered, `S` is pressed in the TUI, **Sync now**
is clicked in the web UI, `POST /sync/control/sync-now` arrives, or the process
exits (`GONOTES_SYNC_ON_EXIT`, default true — SIGINT/SIGTERM for the server,
the quit dialog for the TUI, `pagehide` for the web tab).

**A spoke's local user carries its hub user's GUID** (`models/sync_identity.go`).
Ownership (`notes.created_by`) travels with every synced change, and every read
filters on it, so a local account with a GUID of its own makes pulled notes
invisible — sync succeeds, the UI shows nothing. The spoke learns the hub GUID
at login (or from its cached hub JWT at startup, parsed *unverified* — the hub
holds the signing key), records it in `sync_state.hub_user_guid` /
`hub_username`, and then either hands it to the next local registration
(`CreateUser` → `adoptableHubUserGUID`) or makes an existing same-named account
adopt it (`ReconcileHubUserGUID`, sweeping `notes`, `note_changes`,
`categories`, `category_changes`, `invite_tokens` across both engines, users row
last so a crashed sweep re-runs). Matching is by **username**; a local account
under a different name is left alone and logged. The push direction was never
broken — the hub's `PushChanges` already overwrites `change.User` with the
authenticated GUID.

**Compaction** (`models/sync_compact.go`) collapses the pending unsent log to
one change per entity, built from the entity's CURRENT state — so body-diff
chains become literal text and the field bitmask is the union of what it
replaces. It is destructive to local change history and therefore never
automatic unless `GONOTES_SYNC_COMPACT=true`. Offered as `c` (compact & sync)
and `p` (compact only) in the TUI dialog, **Compact & sync** in the web banner,
and `POST /sync/control/compact`.

Runtime control — all authenticated:

| Verb | Endpoint | Notes |
|---|---|---|
| `GET` | `/api/v1/sync/control/status` | Adds `mode`, `due`, `due_in_seconds`, `pending_changes`, `snoozed_until` |
| `POST` | `/api/v1/sync/control/toggle` | `{"enabled":bool}` |
| `POST` | `/api/v1/sync/control/sync-now` | `{"compact":bool}` optional |
| `POST` | `/api/v1/sync/control/snooze` | `{"duration":"30m"}` optional; defaults to the prompt interval |
| `POST` | `/api/v1/sync/control/mode` | `{"mode":"prompt"\|"auto","persist":bool}`; `persist` writes `GONOTES_SYNC_MODE` into the `.env`. Returns `{status, persisted}` |
| `POST` | `/api/v1/sync/control/compact` | Returns `{compaction, status}` |

The TUI reaches all of this through four Store methods (`SyncStatus`,
`SyncNow`, `SnoozeSync`, `CompactChanges`) plus `DeclineExitSync`; local mode
drives `models.GetSyncClient()` directly (runTui starts one now), HTTP mode
calls the endpoints above. It polls status every minute, dropping to every 15
when the answer is "no sync configured here".

**An applied change keeps its origin's GUID.** Every `ApplySync*` function
takes an `originChangeGUID` and records it as the local change row's own guid
rather than generating a fresh one. That stable identity is what makes a
hub-and-two-spokes topology converge — `changeGUIDExists` finally has something
to recognize — and it is why `MarkChangeGUIDSyncedToPeer` can stop a hub from
handing a spoke back its own push.

**`OperationSync` (9) is a first-class inbound operation**, not an error. A hub
does not re-record a spoke's push as a create; it records what it did, which is
a sync — and that row is the only account it has of the edit, so it is what a
SECOND spoke pulls. `applyIncoming*Change` therefore shares its create arm with
op 9 and decides create-vs-update from what is on disk. Relayed note fragments
are snapshots of the resolved note, never the diff that arrived, since a diff's
base belongs to the sender.

## Privacy

**Never echo note bodies into chat, docs, commits, or session logs.** Verify
work with metadata instead — counts, titles, GUIDs, status codes, `jq` on
envelope fields. This holds for every note, and doubly for `is_private` ones,
whose whole point is that they live in the encrypted database.
