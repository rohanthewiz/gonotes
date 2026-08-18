# Session: Sync Asks Before It Syncs

**Session ID:** `1f5ba3a0-5774-4697-bcf2-1d8b5f52b41b`
**Date:** 2026-08-18
**Branch:** master
**Base commit:** `dd87da9` — Ask what a duplicate should carry, then carry it

## Context

Task, as given: "Do not auto-synch by default -- rather prompt to sync after
2hrs (configurable) or on exit. Add option to compact changes if not yet
synched."

Before this, a configured spoke ran `syncLoop` on a `GONOTES_SYNC_INTERVAL`
ticker (default 5m) and pushed whenever the timer fired. Nothing in either UI
knew sync existed except the web's peer-sync panel, which is a *different* sync
(browser→peer over CORS) and not what the ask was about.

Three questions were put to the user before writing anything, because the
readings diverged materially:

| Question | Answer |
|---|---|
| Where does the prompt appear? | TUI **and** web UI (not server-only) |
| What is "on exit" for a headless server? | Auto-sync on shutdown |
| What does "compact" mean? | Collapse per entity to one change, on disk |

---

## The shape of the answer

### Prompt mode is the default, auto is one line away

`SyncConfig.Mode` (`GONOTES_SYNC_MODE`, default `prompt`). The background
goroutine still runs — it is what notices a runtime mode switch — but in prompt
mode it syncs nothing. It keeps a clock: `Due()`, `DueIn()`, `PendingChanges()`,
`Snooze()`, and a UI turns that into a question.

```
tick ──► enabled? ──no──► wait
          │yes
          ▼
         mode==auto? ──no──► wait   (prompt mode syncs only when asked)
          │yes
          ▼
         interval elapsed and backoff clear? ──no──► wait
          │yes
          ▼
         runSyncCycle
```

| Variable | Default | Meaning |
|---|---|---|
| `GONOTES_SYNC_MODE` | `prompt` | `prompt` or `auto`; unknown values are an error, not a guess |
| `GONOTES_SYNC_PROMPT_AFTER` | `2h` | floor 1m |
| `GONOTES_SYNC_ON_EXIT` | `true` | the cycle nobody has to ask for |
| `GONOTES_SYNC_COMPACT` | `false` | compact before every push |
| `GONOTES_SYNC_INTERVAL` | `5m` | **auto mode only** now |

### Decisions worth keeping

**Silence means prompt, including on upgrade.** An existing `.env` that names
only an interval stops syncing in the background when this lands. That is the
intended migration: the interval it names goes dormant until the mode says
`auto`. Reasoning in the header of `models/sync_config.go` — a background sync
is a write to another machine the user did not initiate and cannot see.

**Due-ness does not require pending changes.** A cycle also *pulls*; a spoke
that wrote nothing for two hours may still be two hours behind. What is pending
shapes the *wording* of the prompt, not whether it appears.

**The quit guard is the opposite** — pending changes only. A dialog on the way
out of a read-only session is a toll for nothing.

**A fresh spoke measures from process start**, not from the zero time.
Otherwise a first launch opens with a prompt before the user has typed anything.

**Snooze can only push the deadline out, never pull it in.** "Ask me later" is
a deferral; a 1m snooze on a 2h interval must not shorten it. Snooze is its own
field, not a touch of `lastSync`, because dismissing a prompt is not syncing.
A successful cycle clears it — the question it deferred has been answered.

**`exitDeclined` exists so answering "no" means something.** The TUI's quit
dialog *is* the exit prompt; without a decline flag the exit path would sync the
very changes the user just declined to, and the question would be theatre.
Cleared by any successful cycle, so a change of mind in the same session works.

**`stateMu` was added, not just extended.** `lastSync` / `lastError` /
`consecutiveFailures` were already read by HTTP status handlers while the loop
wrote them. Prompt mode makes those reads frequent rather than incidental, and
`GetStatus` now snapshots under one lock — a status saying "due in 0s" beside
"last synced just now" is a lie assembled from two moments.

### Compaction

`models/sync_compact.go`. The pending (unsent) tail of the change log collapses
to one change per entity:

```
note A: create ─ update ─ update ─ update  ──►  create (snapshot of A now)
note B: update ─ update ─ delete           ──►  delete
note C: update                             ──►  untouched
```

Three properties make it safe rather than clever:

1. **Built from current state, not by replaying fragments.** Whatever the
   sequence did, the row on disk is its result — so a snapshot cannot drift from
   it, and a chain of body diffs collapses to plain final text with no patch
   application at all.
2. **The bitmask is the UNION of what it replaces.** A field nobody touched
   stays absent and the hub keeps its own value. Compaction narrows the number
   of changes, never the fields they claim. A *create* is the exception — the
   hub builds the whole note from that one fragment, so it claims everything.
3. **The replacement inherits the last swallowed change's `created_at`.** Its
   position in the merged stream — where a category definition must precede the
   note mapping that references it — is exactly the position the group held.
   `insertNoteChangeAt` / `insertCategoryChangeAt` exist only for this; the
   ordinary inserters keep taking the column default.

Also: `OperationSync` rows are skipped entirely (provenance, not work owed);
peers that had received *every* change in a group get a marker carried onto the
replacement so compaction never resurrects work they have seen; the insert
happens before the deletes, so a crash between them makes the push redundant
rather than lossy; and `noteCategoryMappingsJSON` was extracted from
`recordNoteCategoryMappingChange` so both writers of a categories fragment
produce byte-identical content.

It is destructive to local history — which is the point, and why nothing calls
it unasked unless `GONOTES_SYNC_COMPACT=true`.

### The TUI

New `tui/sync.go`: `syncState` (session-held, mutated only in the root's
`Update`, like the cats state), the dialog, and the store commands.

- **Banner in the status bar**, not a line of its own. Every keypress clears
  status text; sitting in the fallback branch means the reminder comes back the
  instant a transient message is gone, for as long as the sync is owed. A
  reminder that vanishes after one arrow key is not a reminder.
- **One dialog, three purposes** (`syncAsked` / `syncDue` / `syncQuitting`).
  Only the question at the top and the shape of the "no" answer differ.
- **Answers:** `s` sync now (also `enter` — the reflex key is the one that
  changes least), `c` compact & sync, `p` compact only (the hub may be exactly
  what is unreachable), `q` quit anyway (quit dialog only, error-colored),
  `esc` later.
- **The modal is raised once per overdue period**, and only from the list with
  a stack depth of 1 — a dialog thrown over a half-typed form would steal the
  next keystroke. `asked` re-arms when the status goes not-due, so a session
  open for days is asked again.
- **`DeclineExitSync` is called inline in `Update`**, not as a command paired
  with `tea.Quit`. A command is a goroutine, and a decline that lost that race
  is a user answering "no" and being synced anyway.
- After a successful cycle: `pop(true)` (a pull may have brought notes in), a
  synthetic `syncStatusMsg` (otherwise the banner contradicts what just happened
  for up to a minute), and the status line.

`Store` grew from 18 to 23 methods. Local mode drives `models.GetSyncClient()`
directly — `runTui` now starts a client, which it never did before — and HTTP
mode calls the control API. `DeclineExitSync` is a deliberate no-op over HTTP:
this TUI quitting is not the server exiting.

### The web UI

A standing banner between toolbar and panes (`sync-due-banner`), polled every
60s from `/sync/control/status`. **Compact & sync** appears only when there is
more than one pending change and the server is not already compacting on push —
a button that does what is already happening teaches the wrong thing.

Exit uses `fetch(keepalive)`, not `sendBeacon`: beacon cannot set an
`Authorization` header, and the workaround would be a JWT in a query string.

### The API

| Verb | Endpoint | |
|---|---|---|
| `GET` | `/sync/control/status` | + `mode`, `due`, `due_in_seconds`, `pending_changes`, `snoozed_until`, `compact_before_push`, `sync_on_exit` |
| `POST` | `/sync/control/sync-now` | body `{"compact":bool}` — one call, so a UI cannot compact and then fail to sync |
| `POST` | `/sync/control/snooze` | body `{"duration":"30m"}` optional |
| `POST` | `/sync/control/mode` | `{"mode":"prompt"\|"auto"}`; session-scoped, the `.env` is what survives a restart |
| `POST` | `/sync/control/compact` | returns `{compaction, status}` |

`CountUnsentChangesForPeer` counts rows rather than measuring
`GetUnifiedChangesForPeer` — that path loads every fragment and resolves an
`authored_at` per change, which is an absurd price for rendering "3 changes
waiting" on every status poll.

---

## The bug the smoke test caught

The exit sync was first wired to its own `signal.Notify` goroutine. **rweb
installs its own SIGINT/SIGTERM handler and returns from `Run`**
(`rweb@v0.1.26/Server.go:665`), so both handlers received the same signal at the
same moment and the deferred `CloseDB` won:

```
"Private database closed"
error: "could not count pending changes for exit sync -> btypedb: database is closed"
"Running final sync before exit" pending_changes=0     ← wrong, there was 1
```

Now it is a plain `syncBeforeExit(client)` call after `web.Run` returns and
before `CloseDB` unwinds — deterministic, and shared with `runTui`. Re-verified
live: `pending_changes=1` → cycle attempted → databases close, in that order.

Worth remembering: **rweb owns the process's signal handling.** Any future
shutdown work belongs after `web.Run` returns, not in a handler of its own.

---

## Files touched

| File | What |
|---|---|
| `models/sync_compact.go` | **new** — the compactor, both entity halves |
| `models/sync_compact_test.go` | **new** — edit chains, deletes, grouping, already-sent, category snapshots, stream ordering |
| `models/sync_prompt_test.go` | **new** — config defaults/overrides/rejections, due-ness, snooze, decline, exit skip |
| `models/sync_config.go` | `SyncMode`, `PromptAfter`, `SyncOnExit`, `CompactBeforePush`, `ParseSyncMode` |
| `models/sync_client.go` | mode-aware loop + `tickInterval`, `Due`/`DueIn`/`Snooze`/`PendingChanges`/`Compact`/`SyncOnExit`/`DeclineExitSync`, `stateMu`, `lastSync` restored from disk, compact-before-push |
| `models/sync_protocol.go` | `CountUnsentChangesForPeer` |
| `models/category_change.go` | `noteCategoryMappingsJSON` extracted (behavior unchanged) |
| `tui/sync.go` | **new** — `syncState`, banner, dialog, commands |
| `tui/sync_test.go` | **new** — quit guard both ways, the `S` door, every answer, failure keeps the dialog up |
| `tui/store.go` / `store_local.go` / `store_http.go` | 5 new methods + `errSyncNotConfigured` |
| `tui/tui.go` | `session.sync`, poll on login, `syncStatusMsg`/`syncTickMsg`, `maybeAskToSync`, banner in the status bar |
| `tui/browse.go` | `leave()` guard on `q`/`esc`, `S` opens the dialog |
| `tui/keymap.go`, `keymap_test.go` | 6 bindings, `syncHelp(purpose)`, browse footer |
| `tui/styles.go` | `syncDueStyle` (warn, not danger) |
| `tui/fake_store_test.go` | sync doubles + counters |
| `tui/testdata/narrow-browse.golden` | regenerated (one more elided footer entry; text unchanged) |
| `main.go` | `initSyncClient` returns the client, `syncBeforeExit`, sync client for local TUI |
| `web/api/sync_control.go` | snooze / mode / compact handlers, `compact` on sync-now |
| `web/routes.go` | three routes |
| `web/api/config_export.go` / `config_import.go` | `sync_mode` + `prompt_after` in the spoke config |
| `web/static/js/sync.js` | the spoke-sync block (poll, banner, answers, unload) |
| `web/static/css/app.css`, `web/pages/landing/page.go` | banner + container + cache-bust |
| `README.md`, `.claude/skills/gonotes/SKILL.md` | prompt mode, compaction, the new keys and endpoints |

`go build ./... && go vet ./... && go test ./...` green; `go test -race
./models/ ./tui/` green. Smoke-tested against a scratch server on port 8972 with
a deliberately dead hub: prompt mode confirmed in the logs, 4 changes compacted
to 1 over the API, snooze/mode/failure paths all behaving.

## Follow-ups not done

- **`GetUnsentChangesForPeer` still has no operation filter**, so
  `OperationSync` rows remain eligible to be pushed, where the hub's
  `applyIncomingNoteChange` would reject operation 9. Pre-existing. The new
  count and the compactor both exclude them, so the prompt's number is right;
  the push path is a one-line filter away from agreeing with it.
- **Mode changes are not persisted.** `POST /sync/control/mode` is
  session-scoped; the `.env` is what survives a restart. A "write it back"
  endpoint would need the same first-run guard `ApplySpokeConfig` has.
- **No sync affordance in cats-mobile**, which has no notion of the prompt.
- **The web banner does not offer compact-only.** `app.spokeCompactChanges` is
  wired and callable, but nothing renders a button for it — the TUI's `p` has no
  web twin yet.
- **The TUI polls every 60s even when nothing is configured.** `SyncStatus`
  answers nil instantly on a local store, but in HTTP mode it is a request per
  minute against a server that will keep saying `enabled:false`. Cheap to gate
  on the first answer.
- **Compaction is all-or-nothing per peer.** There is no "compact just this
  note", and no dry run that reports what *would* collapse before it does.
