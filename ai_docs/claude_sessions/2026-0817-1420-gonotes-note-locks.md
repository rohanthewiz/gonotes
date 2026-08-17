# Session: Note Locks and the Version Guard

**Session ID:** `b2696e3e-0e61-4baa-a5f7-c4aff1a92d28`
**Date:** 2026-08-17
**Branch:** master
**Base commit:** `a30479d` — Let esc quit the TUI, and guard unsaved edits behind it

## Context

Task, as given: "need a proper lock contention system for multiple gonotes so
the same note isn't edited by multiple gonotes sessions."

The shape of the problem comes straight out of the Store seam (`tui/store.go`).
bytdb is single-process, so every GoNotes that is not the one holding the data
directory reaches the notes over HTTP against the process that is. Inside cats
that means several TUI panes, a browser tab, and the MacApp all funnelling
through one server.

That funnel is a gift — there is exactly one process in a position to arbitrate
— and before this session it was doing nothing with the privilege. Two panes
could both press `e` on note 42, both type for ten minutes, and the second
`ctrl+s` would overwrite the first with no error, no record, and no way back.

---

## Three decisions taken up front

Asked rather than assumed, because each changed what got built:

1. **Enforcement: server-side, on all writers.** `PUT /notes/:id` refuses a
   write into a note another session holds. The alternative — advisory locks the
   TUI honours and nothing else does — was rejected: it leaves the web UI free to
   clobber, which is one of the two clients actually in the collision.
2. **Backstop: a version counter, not timestamps.** Every note carries a
   `version`; an editor saves against the one it loaded.
3. **Contention UX: read-only, steal, jump-to-pane.** Explicitly *not* "wait for
   release" — a lease being renewed never lapses, so "wait" would be either an
   endless spinner or a lie about how long.

---

## The two layers

The central design point, and the reason this is not just a lock:

```
  lock  (models/lock.go, web/api/locks.go)   stops the second EDITOR starting
  ────────────────────────────────────────────────────────────────────────────
  version guard (notes.version)              stops the second WRITE landing
```

A lock alone is not enough — it can expire, be stolen, or simply never be taken
by a client that does not speak the protocol. A version guard alone is not
enough either — it is correct but discovers the conflict *after* ten minutes of
typing. Together, contention is normally prevented, and when prevention fails
the write is still refused rather than silently applied.

### Layer 1 — leases (`models/lock.go`)

In-memory registry in the server process, keyed by note id.

| | |
|---|---|
| TTL | `LockTTL` = 90s without a renewal |
| Heartbeat | `LockHeartbeat` = 30s (TTL/3, so two renewals can be lost) |
| Identity | one UUID per client process (`SessionID`) + label / host / cats pane handle |
| Authority | `crypto/rand` bearer token, returned only to the acquirer |

**Not persisted, deliberately.** A lease describes a process running *now*.
Restoring one after a server restart would wedge a note held by something that
died with it, and a heartbeat per open form would put a write in the WAL forever
for state whose whole design assumption is that it evaporates. Restart drops
every lease; the version guard is what makes that safe.

**Not a permission system.** An unlocked note is writable by anyone who owns it
— ownership is already the permission check. Requiring a lease for *every* write
would have broken the web form, `gn-clip.sh`, the Markdown importer and sync
apply on the day it shipped, for a race none of them are in.

Re-acquiring a note the same session already holds returns the **same token**
with a later expiry. Reopening a form must not deadlock against itself, and must
not reissue a token that invalidates the one already in use.

`?steal=true` forces a takeover, records `StolenFrom`, and logs at Info — it is
the one path here that can cost somebody their work, so it should be
reconstructable from the server log alone. It is a parameter, never a
client-side retry: taking a note from someone who may still be typing is a
decision a person makes with the holder's name in front of them.

### Layer 2 — the version guard (`models/note.go`)

`notes.version` starts at 1 and advances on every change: update, flag, delete,
privacy flip, sync apply. `NoteInput.ExpectedVersion` opts a write into the
check; a mismatch returns `*StaleWriteError` carrying the **current note**, so
the loser can be shown what it lost to without a second round trip.

A counter rather than `updated_at`, for two reasons that both bite this system
specifically: timestamp resolution can collapse two writes in the same tick, and
peer-to-peer sync means the writes being compared did not come from one clock.

`ExpectedVersion == 0` means "do not check", and that is the default. Bulk
import, sync apply (which resolves conflicts by its own rules and must land its
result rather than ask again), and any client predating the field all keep
working unchanged.

The predicate is applied **twice**: once before the write, where `existing` is
still in hand to build the good error, and again inside the `UPDATE`'s `WHERE`,
which closes the window between that read and the write. bytdb serializes
writers per engine, so there is no interleaving in which both apply.

---

## Schema migration

`ensureTable` skips a table that already exists, so a column added to the
`CREATE` reaches new databases only. Needed a real ALTER path:

- `dbEngine.hasColumn` — reads bytdb's table descriptor, no query.
- `dbEngine.ensureColumn(table, column, type, backfillSQL)` — idempotent
  `ALTER TABLE … ADD COLUMN`, then a backfill.

The backfill is **not optional**. bytdb stores rows tagged by column ID, so rows
written before the ALTER have no value and read back as NULL — *not* as the
column DEFAULT, which the SQL layer applies at INSERT time only. A NULL version
scans into `int64` as 0, which the guard reads as "unchecked" — so those notes
would have silently opted themselves out of the protection. The ALTER carries
`DEFAULT 1` **and** the backfill runs `UPDATE notes SET version = 1 WHERE
version IS NULL`, and every INSERT in the models layer names `version`
explicitly rather than relying on either.

---

## The seam

`Store` grew six methods. The notable design point is **what is missing from the
signatures**: none of them take a token, and `UpdateNote` does not either.

The store that acquired a lease keeps its token and attaches it to the right
request by note id — the impedance this seam exists to absorb. A token in those
signatures would be a secret threaded through five screens, one forgotten
argument away from a save that mysteriously 409s against its own lock. Both
stores embed the same `lockTokens` type.

`localStore` stopped being a pure pass-through for the first time (it holds the
token map, and gates its writes on `AuthorizeNoteWrite`). That gate looks
redundant in local mode, where bytdb's single-writer rule means this TUI is the
only session there is — it is there so the invariant "no write lands past a
foreign lock" holds *at the seam* rather than in one of its two implementations.

`store_http.go` translates a 409 back into the **same Go error types** the local
store raises by calling the models layer directly (`asConflictError`). That is
what lets every screen handle contention once: nothing above the seam can tell
whether the arbiter was in this process or across a socket.

---

## TUI

**`tui/lock.go`** — session identity, the token map, and `leaseKeeper`.

The keeper is a **plain goroutine, not a `tea.Tick`**, and that is the whole
point of the file. The form's most important pause is `ctrl+e`, which suspends
the entire Bubble Tea program and hands the terminal to `$EDITOR` for as long as
the user wants it. No Cmd runs in that window — so a tick-based heartbeat would
stop renewing exactly when the user is most deeply engaged with the note, drop
the lease inside 90s, and let another session walk in while they are still in
vim. A goroutine does not care that the event loop is parked; the *news* of a
lost lease waits on a channel until the loop comes back to read it.

A failed renewal is reported as lost even when the cause may have been a
transient network failure. That is the safe direction: a banner shown wrongly
costs a glance, the reverse costs the user's text.

**`tui/locked.go`** — the contention dialog and the stale-write fork.

```
Note is being edited elsewhere

On-call runbook
held by pane w1:p3 on <host>  •  for 2m

r/enter open read-only   t take over   g go to their pane   esc back
```

`enter` is bound to the *safe* answer, the same principle as the unsaved-changes
dialog: a dialog the user did not ask for gets dismissed on reflex, so the
reflex key must be the one that takes nothing from anyone. `t` opens a
confirmation before the takeover.

`onReadOnly` is supplied by the caller — browse pushes the detail view, detail
(already the read-only view) just closes the dialog. Pushing a second detail
screen from detail would stack two identical views and make `esc` take two
presses to do what it looks like it does once.

`g` uses `ResolvePane` + `PaneFocus`: the lock records the *public* handle
(`w1:p3`, what the pane env carries) while focus addresses the internal id. It
is offered only at Tier 1, with a holder that named a pane, and only when that
pane is not this one — a session can hold a lock, be killed, and be replaced by
a new GoNotes in the same pane.

The stale fork is emphatically **not** a confirmScreen. "Your save failed,
retry?" is the wrong question — the retry destroys one side or the other
depending on an implementation detail the user cannot see. Both outcomes are
named; `esc` is the only answer that loses nothing.

**`tui/form.go`** — holds the lease, never takes it (whoever pushed the form
did, which is why a blocked edit never reaches this screen). Releases on save
*and* on cancel — discarding work is still leaving, and a colleague who changed
their mind should not hold the note for the rest of the TTL.

The lock banner renders **below**, next to the help line rather than above with
the heading. It is a warning about what the next `ctrl+s` will do, so it belongs
where the eye goes when reaching for `ctrl+s` — and putting it above would
reflow every field the moment it appeared, moving the cursor's surroundings out
from under someone mid-sentence.

**`tui/tui.go`** — `ReleaseAllNoteLocks()` after `p.Run()` returns. Every exit
that bypasses a form's own release (`q` from the list, `ctrl+c`, a program loop
that failed outright) converges there.

**Badges.** `✎` on list rows, not `🔒` — that already means "private" on the
same row and would say two different things in one column. This session's own
lease reads as unheld, or a form open right now would badge its own note.

---

## Two bugs found

**A silent one, caught by the new API tests.** rweb's `Header()` matches the key
exactly or lowercased; Go's HTTP client canonicalizes outgoing names, so
`X-GoNotes-Lock` arrived as `X-Gonotes-Lock` and matched neither. No error
anywhere — every *authorized* write would have arrived tokenless and been
refused against its own lock. `lockToken` now scans headers case-insensitively,
which is the correct reading per RFC 9110 §5.1 rather than a workaround, and
means no client has to guess our casing.

**A pre-existing drift hazard, made live.** `models/category.go` holds the one
note scan that cannot use `scanNoteRow` — the subcategory-filter query carries an
extra trailing column (`nc.subcategories`) and the cursor is positional, so the
note fields and that column must be read in a single `Scan`. Adding `version` to
`noteColsN` without mirroring it there produced a runtime scan-type error on that
screen only. Both `noteColsN` and the hand-written scan now carry comments saying
they must move together.

---

## Web UI

The browser is now a writer that can be **refused**, so it had to stop
swallowing the reason: `apiRequest` tags 409s with `isConflict` + the detail
object, and the save path shows the server's own message
("note is locked by pane w1:p3 since 2m ago") instead of "Failed to save note".
Its update also sends `expected_version`, so two browser tabs get the guard too.

The form is left exactly as it is on a refusal — the user's text is the only copy
of their edit, and a refused save must never be the reason they lose it.

---

## Tests

| File | Covers |
|---|---|
| `models/lock_test.go` | grant, refuse, re-entrancy, steal + audit, renew, release, per-session release, the write gate, expiry (aged by hand, not by sleeping 90s), scoping and redaction |
| `models/note_version_test.go` | version starts at 1, bumps, the two-writer race, retry against the winner, unguarded writes still land, flag/delete bump, privacy flip carries and bumps |
| `web/api/locks_test.go` | the whole lifecycle over real HTTP, steal, stale 409 with `reason`/`current`, token never leaked in a refusal, 404 for a note you do not own, bulk listing, delete releases |
| `tui/lock_test.go` | `e` on a held note opens the dialog not the form, steal → confirm → form, release on save and on esc, save names the loaded version, overwrite lands, load-theirs clears dirty, lost lock keeps the text, badges skip your own lease |

Two fidelity fixes to `fakeStore` were needed, and both are worth remembering:
its `CreateNote` had to set `Version: 1` (zero means "unchecked", so conflict
tests would have passed by never having a conflict), and its `UpdateNote` had to
run the gate and guard **before** touching any field — an early version applied
the write and *then* reported the refusal.

The fake delegates its lock methods to the **real** registry rather than faking
one: `models/lock.go` is pure in-memory state with no database behind it, so
there is nothing to stub, and a hand-rolled second implementation of leases
inside a test double is exactly the kind of thing that drifts and then certifies
the original. `newFakeStore` resets the process-global registry.

`go build ./...`, `go vet ./...`, `go test ./...` all pass.

---

## Files

| File | Change |
|---|---|
| `models/lock.go` | **new** — the lease registry, TTL, steal, the write gate |
| `models/lock_test.go` | **new** |
| `models/note_version_test.go` | **new** |
| `models/note.go` | `Version` field, `ExpectedVersion`, `ErrStaleWrite`/`StaleWriteError`, guarded UPDATE, bumps on flag/delete/move |
| `models/schema.go` | `version` column, `hasColumn`, `ensureColumn` (ALTER + backfill) |
| `models/category.go` | `noteColsN` + the hand-written scan carry `version` |
| `models/sync_apply.go` | sync bumps the version but never guards on it |
| `models/migrate.go` | migrated notes start at version 1 |
| `web/api/locks.go` | **new** — the five lock endpoints, 409 bodies, the write gate, case-insensitive header read |
| `web/api/locks_test.go` | **new** |
| `web/api/notes.go` | update/flag/delete gate on the lock; stale → 409; delete releases |
| `web/routes.go` | the lock routes |
| `web/static/js/app.js` | 409 detail surfaced, `expected_version` sent |
| `tui/lock.go` | **new** — identity, token map, `leaseKeeper`, the lock commands |
| `tui/locked.go` | **new** — the contention dialog and the stale fork |
| `tui/lock_test.go` | **new** |
| `tui/store.go` | six lock methods, documented at the seam |
| `tui/store_local.go` | pointer receiver + token map; writes gated |
| `tui/store_http.go` | header support, 409 → typed errors, the lock endpoints, `version` across the wire |
| `tui/form.go` | keeper lifecycle, release on exit, `ExpectedVersion`, lost-lock banner, retake, stale callbacks |
| `tui/browse.go` | acquire before edit, `✎` badges, locks reloaded with the notes |
| `tui/detail.go` | acquire before edit |
| `tui/keymap.go` | `ReadOnly`, `Steal`, `JumpPane`, `Retake`, `Reload`, `Overwrite` + two help sets |
| `tui/tui.go` | release every lease on the way out |
| `tui/fake_store_test.go` | lock methods via the real registry, `Version: 1`, gate-before-mutate, `lockAsOther` |
| `tui/layout_test.go` | the fixture has a store (edit now needs one) |
| `tui/metakeys_test.go` | ⌘E asserts the acquire, then drives the reply to the form |
| `tui/subcategories_test.go` | `findNotesLoaded` — refresh is a Batch now |
| `README.md` | "Editing the same note from two places" + key table rows |
| `ai_docs/ARCHITECTURE.md` | "Concurrent Editing" section, `version` in the schema |

## Follow-ups not done

- **The web UI respects leases but does not take them.** It sends
  `expected_version` and shows refusals, so no silent clobber is possible in any
  direction — but a TUI can block a browser tab and not vice versa, and two
  browser tabs fall back to the version guard alone (the refusal, not the
  up-front warning). Adding acquire + heartbeat to the web form is the natural
  symmetry.
- **cats-mobile is unaware of both mechanisms.** It writes unguarded, which
  still works; it would be refused by a TUI's lock with a message it does not
  render specially. Worth a pass when `tool/regen.sh` next runs.
- **No lock UI on the categories/subcategories screens** — they do not edit
  notes, so nothing is at risk, but a bulk operation added later would need the
  gate.
- **`ReleaseNoteLocksForSession` has no HTTP door.** It is used by the local
  store only; the HTTP store releases by iterating its own token map, which is
  equivalent but chattier.
- **Leases are per-note, not per-note-and-field.** Two people cannot edit the
  same note's body and tags simultaneously. That is intended, and the version
  guard's fragment machinery (`FragmentBody` etc.) would be where a finer
  answer went if it were ever wanted.
