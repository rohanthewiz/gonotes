# Session: Rename or Merge a Misnamed Local Account (N-025)

**Session ID:** `ba8ce503-6389-4dd4-81a3-7c3431cb7c6a`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `e1e96e6` — Save session doc: N-048 TUI save/forget queries

Picks up N-025 from `ai_docs/todo/next-list.md`, first raised in
[2026-0818-1934-spoke-user-guid-alignment](2026-0818-1934-spoke-user-guid-alignment.md):

> A local account named differently from `GONOTES_SYNC_USERNAME` is diagnosed
> but can't be fixed in the app. There's no merge or rename affordance; the
> log's advice is to re-register. A small admin endpoint or CLI subcommand
> would close it.

---

## The gap

`ReconcileHubUserGUID` matches a local account to its hub account by
**username only**. That is deliberate: it will not guess that "bob" here is
"rob" on the hub. So a spoke whose account is named differently from the sync
username gets a log line and nothing else, and notes pulled from the hub stay
owned by a GUID nobody can log in as.

The old advice ("register under the sync username") creates a second case. The
new account is born holding the hub GUID, so hub notes appear. But everything
the original account wrote before sync is now stranded under the old GUID.

The two cases need two operations:

| State | Operation |
|---|---|
| Only `bob` exists | **rename** bob → rob; the reconcile then adopts the hub GUID |
| `bob` and `rob` both exist | **merge** bob into rob: re-point bob's rows, delete bob |

---

## What was built

### `gonotes account list|rename|merge` (`account_cmd.go`)

```bash
gonotes account list   -d <dir>
gonotes account rename -d <dir> --from bob --to rob   [--dry-run]
gonotes account merge  -d <dir> --from bob --into rob [--dry-run]
```

- **`list`** prints each account, its GUID, and a per-column count of what it
  owns. It shows the hub identity recorded in `sync_state`, and either `✓
  aligned` or the exact command that fixes the mismatch. When the sync account
  is aligned but other accounts exist, it suggests merging them. It suggests
  rather than assumes, since only the operator knows they are the same person.
- **`rename` / `merge`** run `models.AlignLocalUserWithHub` afterwards, so the
  hub GUID is adopted at once rather than at the next server start. They then
  print "Sign out and back in on any open browser or TUI session" (see N-051).
- Each leaf command declares its own `--dir`, as `tui` does, so `gonotes
  account list -d <dir>` works.
- `withAccountDB` does **not** `MkdirAll` the directory. A missing directory
  can only be a mistyped `-d`, and creating it would report "no accounts".

**CLI, not a web endpoint, on purpose.** The repair rewrites the identity of
the very account a web session is authenticated as. It is a rare operator-level
repair, so stopping the server first is an acceptable cost.

### `models/account_admin.go`

| Function | Purpose |
|---|---|
| `CountUserGUIDReferences(guid)` | per-column row counts, for `list` and `--dry-run` |
| `ListLocalUsers()` / `RecordedHubIdentities()` | listing |
| `RenameLocalUser(from, to)` | validates the name; refuses a taken name and points at merge |
| `MergeLocalUsers(from, into)` | sweep, carry admin rights over, delete source **last** |
| `AlignLocalUserWithHub(username)` | `hubIdentityForUsername` + `ReconcileHubUserGUID`, on demand |
| `CategoryNameCollisions(a, b)` | case-insensitive category names both own |

**Crash ordering in merge** follows the reconcile's rule: the row that
*triggers* the work goes last. While `from` still exists, re-running the merge
re-runs the idempotent `WHERE col = fromGUID` sweep and finishes. If `from` were
deleted first, a crash would strand rows under a GUID nothing names.

**Admin rights carry over.** Merging the only admin into a regular account
would otherwise leave nobody able to issue invites.

**Categories are not combined.** Combining them would mean refiling notes and
deleting a category, both sync-visible changes. A merge is local bookkeeping,
like the reconcile, and records no sync changes. Same-named categories are
listed before the merge (afterwards they can't be told apart) so the operator
can tidy up in the app.

### Bug fixed on the way: the sweep missed `saved_queries`

`rewriteUserGUIDReferences` predates `saved_queries` (N-034, private DB only),
so a hub-identity reconcile left a user's saved queries under the old GUID.
The sweep's hard-coded statement list became one inventory, `userGUIDRefs()`
(table, column, engines). The sweep, the counter and the merge all walk it, so
a future per-user table has exactly one place to be registered. A test,
`TestReconcileSweepsSavedQueries`, pins it.

### Finding: no cross-process lock on the databases

The first draft relied on "bytdb is single-process, so `InitDB` fails against a
running server". **That is false.** `gonotes account list` opened the
databases beside a live server without complaint. Neither `bytdb` v0.11.0 nor
`btypedb` v0.7.0 contains an `flock`. Two writers, or two background
compactors, on one file is corruption.

So `withAccountDB` now probes first. `serverHoldingDataDir` sends the TUI's
`/api/v1/health` probe to `GONOTES_URL`, `localhost:$GONOTES_PORT`,
`localhost:$PORT` and the default URL, after loading the directory's `.env`.
It refuses if a server reports the same resolved data directory. A server
that answers without reporting one (an older build) also counts, because
refusing a repair is cheap and corrupting the notes is not. This is
best-effort: a server on a port none of those name is not found. The general
fix went to the list as **N-050**. `runTui`'s fallback and the README's "stop
the server before import/export" advice both assume a lock that doesn't
exist.

### Docs

- `models/sync_identity.go`: the "no local account matches" log line now says
  to stop the server and run `gonotes account list`, instead of advising
  re-registration.
- `README.md`, "One account, one GUID": the three commands, `--dry-run`, and
  what rename and merge keep.
- `.claude/skills/gonotes/SKILL.md`: the sweep inventory now includes
  `saved_queries` and `userGUIDRefs()`; adds the account command and the
  missing lock.

---

## Verification

### Unit tests: `models/account_admin_test.go` (7)

- Rename then align adopts the hub GUID. Public and private notes are visible
  afterwards, and the password still works under the new name.
- Rename refuses a taken name (pointing at merge), an invalid name, and a
  missing account.
- Merge moves public and private notes, categories and a saved query. It
  carries admin over and deletes the source, and leaves zero rows under the
  old GUID across the whole inventory.
- Merge refuses a self-merge, a missing target (pointing at rename), and a
  missing source.
- Reconcile now sweeps `saved_queries`.
- Listing helpers.

### End to end: real processes over HTTP

A hub plus two spokes in scratch directories, with different JWT secrets on hub
and spoke, sync in `auto` mode at 10s (the minimum; 3s is rejected).

| Scenario | Before | After |
|---|---|---|
| **A: rename.** Spoke registers `bob`, writes a note, then syncs as `rob` | bob sees only `bob-local-A`; the hub note is invisible | `rob` logs in with bob's password and sees `bob-local-A`, `hub-note` |
| **B: merge.** As A, then `rob` is registered via an invite from bob (the old advice) | rob sees the hub and fan-out notes; `bob-local-B` is stranded | rob sees all four notes, is admin, and the "Work"/"work" collision is reported |
| Either, with the spoke server running | — | `account list` refuses: "a GoNotes server at http://localhost:18562 is using the notes in …" |

The B setup also exercised the `invite_tokens.created_by` / `used_by` sweep.
There were 0 `level=error` lines in any hub or spoke log from the run.

`go build ./... && go vet ./... && go test ./...` all green; `gofmt -l` is clean.

---

## Files touched

| File | What |
|---|---|
| `account_cmd.go` | **new**: `gonotes account` command, live-server guard, output |
| `models/account_admin.go` | **new**: rename, merge, align, counts, listings, collisions |
| `models/account_admin_test.go` | **new**: seven tests |
| `models/sync_identity.go` | `userGUIDRefs()` inventory (+ `saved_queries`); sweep walks it; log advice |
| `main.go` | registers `accountCommand(defaultDir)` |
| `README.md` | account repair commands under "One account, one GUID" |
| `.claude/skills/gonotes/SKILL.md` | sweep inventory, account command, missing lock |
| `ai_docs/todo/next-list.md` | closed N-025; raised N-050, N-051 |

The same commit also carries the previously uncommitted N-016 bulk-lock-gate
work that was in the tree at the start of this session (`models/lock*.go`,
`models/subcategory_rename*.go`, `models/db.go`, `tui/*store*`,
`web/api/categories*.go`, `web/api/locks.go`, and its next-list edits). This
session did not change that work. It was committed at the user's request to
"commit all".

## Next

Closed: N-025. Declined: None. Raised: N-050, N-051.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
