# Session: Hub Identity on a Spoke with More Than One Hub (N-026)

**Session ID:** `4d84aea5-2d3c-4540-88ce-aaff8d3930a6`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `860a955` — Close N-049: lock-gate the per-note category writes

Same session as
[2026-1004-2045-n049-category-link-lock-gate](2026-1004-2045-n049-category-link-lock-gate.md).
Picks up N-026 from `ai_docs/todo/next-list.md`, first raised in
[2026-0818-1934-spoke-user-guid-alignment](2026-0818-1934-spoke-user-guid-alignment.md):

> Spokes synced to more than one hub are untested. `hubIdentityForUsername`
> takes the first `sync_state` row that matches. The design assumes one hub.

---

## It was a bug, not just a gap

`sync_state` is keyed by `hub_url`. The sync client syncs with one hub at a
time (`SyncConfig.HubURL`), but `GetOrCreateSyncState` adds a row for every
hub URL ever configured, and nothing removes old rows. A hub move therefore
leaves:

```
sync_state
  http://old-hub:8444   hub_user_guid = G1   updated_at = March   ← stale
  https://new-hub       hub_user_guid = G2   updated_at = today   ← current
```

If the new hub is a different instance, the same username has a different
GUID on it. The lookup was:

```sql
SELECT hub_user_guid FROM sync_state
 WHERE hub_username = ? AND hub_user_guid IS NOT NULL AND hub_user_guid <> ''
 LIMIT 1
```

There was no `ORDER BY`. A throwaway probe test showed rows come back in
`hub_url` order:

| Old URL / new URL | Lookup returned | New user's GUID |
|---|---|---|
| `z-old` / `a-new` | `guid-new` | correct by luck |
| `a-old` / `z-new` | `guid-old` | **stale** |

Two callers were affected:

- **`adoptableHubUserGUID` (`CreateUser`):** a new local account was created
  holding the stale hub's GUID. No pulled note carries it, so the user's
  synced notes were invisible until the next server start, when
  `NewSyncClient` → `ReconcileHubUserGUID` rewrote every row to G2.
- **`AlignLocalUserWithHub` (`gonotes account rename|merge`):** could align
  the account to G1. The next server start flipped it to G2, rewriting every
  owned row twice.

`ReconcileHubUserGUID` itself was unaffected. Its callers pass the configured
hub's identity directly.

## Design: the current hub is the most recently active row

Options considered:

1. **Read `GONOTES_SYNC_HUB_URL`.** Rejected. Env comes from the shell (there
   is no `.env` loading), and `gonotes account` may run from a shell without
   the server's sync settings.
2. **Add an explicit "current" column.** Workable, but it needs a schema change
   and a rule for upgraded databases where no row is marked.
3. **Most recent `updated_at`.** Chosen. Only the sync client for a row's own
   hub writes to that row, so `updated_at` already tracks the active hub. Two
   gaps closed it:
   - `RecordHubIdentity` (login) did not stamp `updated_at`. Now it does.
   - Switching *back* to an earlier hub: `GetOrCreateSyncState` doesn't touch
     an existing row. A new `MarkCurrentHub(hubURL)` is called from
     `NewSyncClient` right after `GetOrCreateSyncState`, before the startup
     adoption. A failure is logged, not fatal.

Timestamps are stored at microsecond precision (checked in the probe), so
ordering is reliable.

### `currentHubState()`, in `models/sync_identity.go`

It reads every `sync_state` row and picks the newest in Go rather than with
`ORDER BY ... LIMIT 1`. There are only a handful of rows, and this makes the
tie break (`hub_url`) and a NULL `updated_at` (treated as oldest) explicit.
It returns a `HubIdentity` and a found flag.

The current row may have **no identity yet**, for example a hub just switched
to, before its first login. That is returned as-is. Falling back to an older
row would reintroduce the bug.

### `hubIdentityForUsername(username)`

It returns the current row's GUID only when the current row has one **and**
its `hub_username` matches. Otherwise it returns `""`. Both checks are
documented as safety catches.

## CLI (`account_cmd.go`, `models/account_admin.go`)

- `HubIdentity` gains `Current bool`, set by `RecordedHubIdentities` from
  `currentHubState`.
- `runAccountList` prints `Hub <url> (current): ...` with the existing
  `describeHubMatch` advice. Other hubs print `(earlier hub)` and
  `· not the hub this spoke syncs with now; its identity is not adopted`,
  with no advice. Advice about an earlier hub would send the operator to a
  rename that align then ignores. If no recorded identity is current, it
  prints a line saying the current hub's identity is recorded at the next
  sync login.
- `printHubAlignmentPreview` (`--dry-run`) considers only the current hub.

The design note above `NewSyncClient` ("the current design assumes one") now
points at `currentHubState`.

## Tests

| Test | Pins |
|---|---|
| `TestNewLocalUserAdoptsTheCurrentHubNotAnEarlierOne` | Both URL sort orders. A new user gets the current hub's GUID. |
| `TestAnEarlierHubIsNotAFallbackForTheCurrentOne` | New hub created and marked current, no login yet. `adoptableHubUserGUID` offers nothing. |
| `TestMarkCurrentHubSwitchesBack` | Two logged-in hubs. `MarkCurrentHub` on the older one makes its identity current again. |
| `TestSyncClientStartupMakesItsHubCurrent` | The configured hub is not the newest row. `NewSyncClient` makes it current and realigns the local account to its GUID. |
| `TestAlignAndListingUseOnlyTheCurrentHub` | `Current` flags, plus rename + align adopts the current hub's GUID. |
| `TestAccountListAdvisesOnlyTheCurrentHub` (new `account_cmd_test.go`) | The `account list` stdout labels current and earlier hubs, and never advises `--to <earlier hub's name>`. |

All five `models` tests fail when `hubIdentityForUsername`'s body is swapped
back to the old `LIMIT 1` query. The "stale hub sorts last" subcase passes
there, as expected. `gofmt -l` is clean, and `go vet ./...` and
`go test ./... -count=1` pass.

## Files

- `models/sync_identity.go`: `currentHubState`, `MarkCurrentHub`, new
  `hubIdentityForUsername`, and `RecordHubIdentity` stamps `updated_at`
- `models/sync_client.go`: `NewSyncClient` calls `MarkCurrentHub`, and the
  design note is updated
- `models/account_admin.go`: `HubIdentity.Current`
- `account_cmd.go`: list and preview use only the current hub
- `models/sync_identity_test.go`, `models/account_admin_test.go`,
  `account_cmd_test.go`: tests
- `ai_docs/todo/next-list.md`: N-026 moved to Closed

## Next

Closed: N-026. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
