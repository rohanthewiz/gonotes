# Session: One Account, One GUID

**Session ID:** `773a1b87-08d3-45f6-820a-113ab5f4b9d5`
**Date:** 2026-08-18
**Branch:** master
**Base commit:** `4506ef5` — Add session doc for the sync relay work

Picks up the first follow-up left by
[2026-0818-1859-sync-relay-convergence-and-followups](2026-0818-1859-sync-relay-convergence-and-followups.md):

> **A spoke's local user GUID does not match the hub's.** […] Notes pulled onto
> a spoke are therefore invisible to a locally-registered user there. […] Worth
> its own session.

## Context

Task, as given: "Fix the spoke user GUID mismatch".

---

## The bug, stated precisely

Every note and category is owned by a user GUID (`notes.created_by`), that
ownership rides along with every synced change (`change_user` → `created_by`),
and every read query ends in `WHERE created_by = ?`.

A spoke acquires a user two ways, and they produced two GUIDs for one person:

```
spoke ──register(username,password)──► HUB    creates user, GUID = H
spoke ──register locally (web/TUI)───► SPOKE  creates user, GUID = S
```

Notes pulled from the hub arrive owned by **H**. The person sitting at the
spoke is logged in as **S**. Same account, same password, same human — and
their own notes are invisible.

**Only one direction was broken.** The hub's `PushChanges` already overwrites
`change.User` with the authenticated user's GUID (an anti-impersonation
measure that predates this work), so a spoke's notes land on the hub owned by
H regardless of what the spoke called itself. That asymmetry is exactly why
the symptom reads as *"sync ran successfully and nothing appeared"* rather
than as a sync failure — there is no error anywhere, at either end.

---

## The fix: one account, one GUID — the hub's

The hub is the account of record. It is also the identity every other machine
already agrees on, because it is the one stamped onto every change that fans
out. So the spoke adopts it, in three places.

**New file: `models/sync_identity.go`.**

### 1. The spoke learns H

`login()` was decoding `{success, data:{token}}` and discarding the `user`
half. It now reads `user.guid` / `user.username` and records them in two new
`sync_state` columns, `hub_user_guid` and `hub_username`.

### 2. A local account created afterward is *born* holding H

`CreateUser` no longer mints a GUID unconditionally:

```go
userGUID := adoptableHubUserGUID(input.Username)
if userGUID == "" {
    userGUID = uuid.New().String()
}
```

This is the half that keeps the mismatch from ever forming again. `CreateUser`
is the single funnel for every user-creation path (web `/register`, TUI local
store, TUI HTTP store → web register), so one edit covers all of them.

### 3. An account that already exists *adopts* H

`ReconcileHubUserGUID` is the repair path for databases that already carry the
mismatch. It re-points every row naming the old GUID:

| Table | Column(s) | Engines |
|---|---|---|
| `notes` | `created_by`, `updated_by` | public + private |
| `note_changes` | `change_user` | public + private |
| `categories` | `created_by` | public |
| `category_changes` | `change_user` | public |
| `invite_tokens` | `created_by`, `used_by` | public |

…and *then* updates the `users` row.

**That ordering is the crash story, and it is the one design decision here
worth defending.** The sweep spans two databases, so no single transaction
covers it. What makes a half-finished pass recoverable is that the *trigger*
survives it: leave the user on the old GUID and the next call sees the
mismatch again and re-runs the (idempotent, `WHERE col = oldGUID`) sweep to
completion. Flip the user first and a crash strands the remaining rows under a
GUID nobody remembers — with the mismatch check now reporting all clear.

### Decisions worth keeping

- **Matching is by username, and only by username.** A GUID is an account; the
  only local evidence that two accounts are the same account is that they
  share a name. A local account under a different name from
  `GONOTES_SYNC_USERNAME` is left alone and logged, not merged. "There is only
  one local user, so it must be the one" was considered and rejected —
  silently rewriting an account's identity on a headcount is not a call this
  code should make.
- **A GUID already held locally is never stolen.** `users.guid` is unique, so
  offering a taken one would turn a registration into a constraint violation
  and a reconcile into corruption. Both paths check and back off; reconcile
  returns an error naming both accounts.
- **Reconcile failures are logged, never returned.** This is a repair that
  makes synced notes *visible*; a spoke that cannot perform it should still
  sync. Failing login over it would trade a display problem for an outage.
- **No sync changes are recorded by the rewrite.** Ownership is not part of a
  note fragment — the hub sets `created_by` from `change.User`, never from
  fragment content — so re-pointing `created_by` is purely local bookkeeping.
  Nothing about the realignment should travel, and nothing does.
- **`migrate.go` is untouched.** The DuckDB→bytdb tool preserves source GUIDs
  verbatim by design; a bulk data move is not an authoring event.

### Where reconciliation runs

Every login, **and** at `NewSyncClient` construction. The startup hook matters
because prompt mode is now the default: a cycle may not run for hours, and the
symptom being fixed is something the user is looking at *right now*.

Startup has two branches:

```go
if state.HubUserGUID.Valid && ... {          // recorded by a previous login
    client.adoptHubIdentity(...)
} else if client.authToken != "" {            // the upgrade path
    if guid, username := hubIdentityFromToken(client.authToken); guid != "" {
        client.adoptHubIdentity(guid, username)
    }
}
```

**`hubIdentityFromToken` parses the cached hub JWT without verifying it**, and
that is correct rather than a shortcut. The signing key belongs to the hub; a
spoke need not have it (only if the deployment happens to reuse
`GONOTES_JWT_SECRET`), so verification would fail on exactly the installations
this exists for. And there is nothing to defend: the token came out of this
spoke's own `sync_state`, it is only ever replayed to the hub as a bearer
credential, and the hub validates it there. Without this branch, an existing
spoke holding a week-long token would not call `login()` again — and so would
not repair itself — until that token expired.

---

## Verification

### Unit tests — `models/sync_identity_test.go` (nine)

Adoption at registration; name-matching required; a held GUID is not stolen
(both paths); the two-engine sweep with a public *and* a private note;
idempotency; the refusal; the no-local-account no-op; unverifiable-token
recovery (token minted under one secret, read after re-keying to another);
and sync-client startup repairing from a cached token alone.

### End-to-end — real processes over HTTP

A hub and spokes in scratch directories, in `auto` mode at a 10s interval,
with **different JWT secrets on hub and spoke** so nothing is proved by an
accidentally shared signing key.

| Scenario | Result |
|---|---|
| Spoke syncs first, then registers locally | GUIDs match; hub note visible |
| Registers locally first, then sync starts | GUID adopted; pre-sync local note **and** hub notes visible |
| Spoke A → hub → spoke B fan-out | all three notes visible at both ends |
| `level=error` in any log | 0 |

### The upgrade path, against a genuinely old database

Built a binary from `HEAD` before the change, ran a spoke on it against the new
hub, registered locally — **mismatch reproduced, pulled note invisible**. Then
restarted the *same data directory* under the new binary:

```
Local user adopted its hub identity — pulled notes are now visible to it
  old_guid=4fcebd54-… new_guid=daa736d5-… username=…
notes visible: hub-note
```

The `ALTER TABLE sync_state ADD COLUMN` ran clean (0 errors), the identity was
recovered from the cached token, and the invisible note appeared. This is the
one path the unit tests could not reach, since a fresh database gets the
columns from `CREATE TABLE` and `ensureColumn` short-circuits.

`go build ./... && go vet ./... && go test ./...` green; `go test -race
./models/` green.

---

## Files touched

| File | What |
|---|---|
| `models/sync_identity.go` | **new** — record/learn, adopt-at-birth, reconcile+sweep, unverified token read |
| `models/sync_identity_test.go` | **new** — nine tests |
| `models/sync_client.go` | decode the user on login, `adoptHubIdentity`, startup hook, `SyncState` + its SELECT |
| `models/user.go` | `CreateUser` adopts a recorded hub GUID |
| `models/schema.go` | `sync_state.hub_user_guid` / `hub_username` + `ensureColumn` upgrade |
| `README.md` | "One account, one GUID"; the previously missing step 5 (register locally under the sync username) |
| `.claude/skills/gonotes/SKILL.md` | the mechanism, in the sync section |

## Follow-ups not done

Carried forward from the previous session, still open:

- **The hub's own change log is never compacted.** Compaction is spoke-side
  and skips operation 9, which is nearly everything a hub holds. A long-lived
  hub's log only grows.
- **Compaction is all-or-nothing per peer** — no "just this note", no dry run.
- **No sync affordance in cats-mobile.**
- **`GetUnsentChangesForPeer` returns operation 9 rows** — correct, but a
  spoke's push batch can carry relays the hub then skips by GUID. One wasted
  entry per batch.

New, from this work:

- **A local account under a different name from `GONOTES_SYNC_USERNAME` is
  diagnosed but not fixable in-app.** The log says what is wrong; there is no
  "merge these two accounts" or "rename this account" affordance, and the
  advice is to register again under the sync username. A small admin endpoint
  or CLI subcommand would close it.
- **Multi-hub spokes are untested here.** `sync_state` is keyed by `hub_url`
  and `hubIdentityForUsername` takes the first matching row; a spoke synced to
  two hubs under the same username would adopt whichever it found. The current
  design assumes one hub, and this does not make that worse — but it is now
  one more thing resting on the assumption.
