# Session: A Change Keeps Its Name

**Session ID:** `1f5ba3a0-5774-4697-bcf2-1d8b5f52b41b`
**Date:** 2026-08-18
**Branch:** master
**Base commit:** `f80373c` — Ask before syncing, and offer to pack the backlog first

Continues the same session as
[2026-0818-1824-sync-prompt-mode-and-compaction](2026-0818-1824-sync-prompt-mode-and-compaction.md),
which introduced prompt mode and change-log compaction and left a follow-up
list. This doc covers working that list — and finding that its first item was
wrong.

## Context

Task, as given: "Do ignore .cats-todo/ . And remove it from the last commit.
Then work on the follow-up loose ends."

---

## Part 1: the git cleanup

`.cats-todo/todos.json` is written by the cats host as work happens, so it had
been arriving as an unrelated diff in every session. Two commits:

- The prompt-mode commit was **amended** so it no longer touches the file
  (`git restore --source=HEAD~1 --staged --worktree` on that path, then
  `--amend`). It became `f80373c`; the push was `--force-with-lease`.
- `e66db28` untracks it (`git rm --cached`) and adds `.cats-todo/` to
  `.gitignore`. The local file content was preserved across both steps.

---

## Part 2: the follow-up that was wrong

The previous session's list opened with:

> `GetUnsentChangesForPeer` still has no operation filter, so `OperationSync`
> rows remain eligible to be pushed […] The push path is a one-line filter
> away from agreeing with [the pending count].

**Following the call graph says the opposite.** A hub does not record a spoke's
push as a create — `ApplySyncNoteCreate` records what the hub *did*, which is
an `OperationSync` (9) row. That row is the hub's **only** account of the edit,
so it is also what a *second* spoke pulls. Filtering operation 9 out of the
push would have been filtering out fan-out itself.

What was actually broken was the other end:

```go
case OperationCreate: …
case OperationUpdate: …
case OperationDelete: …
default: return serr.New("unknown note operation: 9")   // ← every relay died here
```

So: **a note written on spoke A never reached spoke B.** It reached the hub and
stopped. And the spoke that pushed got its own change handed back on the next
pull, where it failed the same way — an error line per change per cycle,
forever.

### The fix, and why it is two things

**(1) Operation 9 shares the create arm.** Whether a relayed change lands as a
create or an update is decided by what is on disk, not by the operation code —
the honest reading of "this entity now says this".

That alone trades a dropped change for an endless one: applying a relay records
another change, which is pushed, applied, recorded, pushed…

**(2) An applied change keeps its origin's GUID.** Every `ApplySync*` function
now takes an `originChangeGUID` and records it as the local row's own guid
rather than generating a fresh one:

```
spoke A ──push(X)──► hub records X ──pull──► spoke A sees X, already has it
                          │                  (changeGUIDExists → skip)
                          └────pull────────► spoke B applies X, records X
                                             ──push(X)──► hub already has X
```

The idempotency check in `ApplyIncomingSyncChange` was always written for this;
it just never had a stable identity to check against. With one, the echo is
recognized and recorded nowhere — and `MarkChangeGUIDSyncedToPeer` can now stop
the echo *travelling*, because a change is finally findable by name: the hub
marks a pushed change as delivered to its sender, and a spoke marks a pulled
one as owed to nobody.

**(3) Relayed fragments are snapshots.** `ApplySyncNoteUpdate` records a
fragment built from the *resolved* note rather than forwarding what arrived. A
body diff is expressed against the SENDER's previous body; relaying it verbatim
asks a third machine to patch a base it may never have had. Same reasoning as
the compactor.

### Decisions worth keeping

- **`relayChangeGUID("")` generates one.** An empty origin means "no incoming
  change to inherit from", which is what a caller outside the sync path gets.
  Nothing had to be special-cased at the six recording sites.
- **The pending count still filters on operation**, even though the peer
  markers now make it redundant. It is belt-and-braces on a number a user
  reads, and it keeps the count meaningful if a marking ever fails.
- **The compactor still skips operation 9.** Those rows may be owed to peers
  that have not pulled yet, and they are not this machine's work to pack down.
- **`models/export_test.go`** opens `computeBodyDiff` to the external
  `models_test` package rather than exporting it. A test needing one internal
  helper is not a reason to widen the package's API.

### End-to-end verification

Three real processes over HTTP — a hub (8981) and two spokes (8982, 8983) in
scratch directories, spokes in `auto` mode at a 10s interval so cycles ran
without prompting:

| Check | Result |
|---|---|
| Note written on spoke A → hub | arrived |
| … → spoke B (**the broken path**) | arrived |
| Edit on spoke B → hub → spoke A | arrived, `lww_remote` |
| Body text through the relay | intact |
| `unknown … operation` in any log | 0 |
| `pending_changes` after 9 cycles | 0 and flat — no ping-pong |

Four unit tests pin the pieces (`models/sync_relay_test.go`); each fails
against the old code, two by erroring on operation 9 and two on the assertion.

---

## Part 3: the smaller follow-ups

**Idle polling.** The TUI asked "is a sync due?" every minute forever,
including on the many installations with no hub. It now drops to
`syncIdlePollInterval` (15m) once the answer is "no sync configured here" —
backing off rather than stopping, because in HTTP mode a server could be
restarted with sync on while the TUI stays open. The next tick is scheduled
from what the last poll found.

**Mode persistence.** `POST /sync/control/mode` takes `{"persist": true}` and
writes `GONOTES_SYNC_MODE` into `config/cfg_files/.env`. Opt-in, because "sync
in the background for the rest of this afternoon" is not "from now on", and
only one of those should edit a file holding the hub credentials. The response
became `{status, persisted}`, matching the compact endpoint's shape; a failed
write reports `persisted:false` with a warning rather than failing the whole
request, since the live half already took.

`setEnvFileValue` changes the one line it was asked to:

- matches on `KEY=` after trimming, so a key named in a comment is left alone;
- drops duplicate assignments of the same key rather than leaving two;
- appends after trimming trailing blanks, so the file does not grow a gap;
- writes temp-then-rename at 0600, so a failure cannot truncate a file holding
  the hub password and the JWT secret.

`writeEnvFile` stays exactly as it was — it composes the whole file for
first-run setup, where there is nothing to preserve.

**Compact-only in the web banner.** Offered when `pending > 1 &&
!compact_before_push && last_error`. An unreachable hub is both why the log is
growing and why "compact & sync" would not finish; unconditionally it would be
a fourth button doing nearly what two others do. The summary line now also says
"the hub is not responding" when that is why.

---

## Files touched

| File | What |
|---|---|
| `models/sync_apply.go` | `originChangeGUID` on all six `ApplySync*`, `relayChangeGUID`, `noteRelayFragment`, resolved-state relay |
| `models/sync_protocol.go` | op-9 in both incoming arms, `applyIncomingNoteUpdate` extracted, `MarkChangeGUIDSyncedToPeer` |
| `models/sync_client.go` | mark a pulled change as owed to nobody |
| `models/sync_relay_test.go` | **new** — identity, echo, fan-out, upsert, diff-free relay |
| `models/export_test.go` | **new** — `ComputeBodyDiffForTest` |
| `web/api/sync.go` | mark a pushed change as delivered to its sender |
| `web/api/sync_control.go` | `persist` on the mode endpoint |
| `web/api/config_import.go` | `setEnvFileValue` |
| `web/api/config_env_test.go` | **new** — in-place, append, comment-safety, 0600 |
| `tui/sync.go`, `tui/tui.go` | idle poll backoff |
| `web/static/js/sync.js` | compact-only button, hub-not-responding in the summary |
| `.gitignore` | `.cats-todo/` |
| `README.md`, `.claude/skills/gonotes/SKILL.md` | the relay rules, the mode endpoint's new body and response |

Commits: `e66db28` (untrack), `cff9559` (relay), `2bdfb0c` (the three smaller
ones). `go build ./... && go vet ./... && go test ./...` green; `go test -race
./models/` green.

## Follow-ups not done

- **A spoke's local user GUID does not match the hub's.** Found while wiring
  the fan-out test: the spoke auto-registers on the *hub*, but its own database
  has no user until someone registers locally — and that local user gets a
  fresh GUID, while synced notes carry the hub user's. Notes pulled onto a
  spoke are therefore invisible to a locally-registered user there. The test
  only worked by using the hub's token against all three servers (the JWT
  secret is shared). Pre-existing and outside this work, but it means the
  documented "register on the spoke and restart" flow may not show you your own
  notes. Worth its own session.
- **The hub's own change log is never compacted.** Compaction is spoke-side
  (`SyncClient.Compact()` uses the spoke's peer id) and skips operation 9,
  which is nearly everything a hub holds. A long-lived hub's log only grows.
- **Compaction is still all-or-nothing per peer** — no "just this note", and no
  dry run reporting what *would* collapse.
- **No sync affordance in cats-mobile.**
- **`GetUnsentChangesForPeer` still returns operation 9 rows**, which is now
  correct rather than a wart — but it means a spoke's push batch can contain
  relays it received, which the hub will skip by GUID. Harmless, one wasted
  entry per batch, and cheap to filter per-direction if it ever shows up in a
  profile.
