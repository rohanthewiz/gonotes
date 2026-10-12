# Session: Next-list review (`/next-list`, living-list mode)

**Session ID:** `cc197096-dd87-44ee-ae30-086aa70e102d`
**Date:** 2026-10-11
**Branch:** master
**Base commit:** `ae5abb5` — Close N-015: mobile parity reached

## The ask

Run `/next-list` with its defaults (n = 15, sorted by age) against the
living list at `ai_docs/todo/next-list.md`. No code changed. The only
edits are to the list.

## What was checked

- **Dating.** 39 session docs. The newest,
  `2026-1004-2338-n005-drop-monaco-locales`, is position N. Every Open and
  Roadmap item's `raised` doc exists.
- **Lapses (L3).** I collected every ID in `git log -p --follow` of the
  list. All 52 (N-001…N-052) are still present, IDs are unique, and
  **Next ID** (N-053) is above the highest. N-053 appears in history only
  on that line. The last 15 docs' `## Next` sections are all
  `Closed: … Raised: …` summaries, and each reported ID sits in the section
  it says. So there were no transition lapses and nothing to restore.
- **Provenance gap.** N-049's `raised` stem,
  `2026-1004-n016-bulk-lock-gate`, names a doc that was never written.
  N-016 was closed in `2802d2e` without a session doc. N-049 is closed, so
  I left it as it is.

## Premises re-checked against the code

- **N-002 holds.** `GetSyncStatus`, `computeSyncChecksum` and
  `changeGUIDExists` (`models/sync_protocol.go`) still run their queries in
  sequence. `changeGUIDExists` runs once per applied change (line 389), up to
  four point queries each. Not worth parallelizing.
- **N-003 holds.** `scripts/migrate/main.go:17` still says the change log is
  not migrated.
- **N-021 holds.** The duplicate dialog's "Follow-up flag" row defaults on
  (`tui/duplicate.go:141`; web shows it only for flagged notes).
- **N-030** is unchanged. No reappearing-deletes report since 2026-08-19.
- **N-051 holds.** `JWTAuthMiddleware` (`web/middleware.go:66`) sets
  `user_guid`, `username` and `is_admin` straight from the token claims, with
  no lookup.
- **N-050's premise changed.** gonotes pins `bytdb v0.11.0` (over
  `btypedb v0.7.0`), and neither takes a file lock. But upstream now does:
  - bytdb v0.17.0 added a non-blocking `flock` on a `<path>.lock` sidecar.
  - bytdb v0.18.0 moved it down into btypedb (v0.8.0+), so it survives
    compaction's rename-over.
  - A second `Open` fails with `bytdb.ErrLocked` (= `btypedb.ErrLocked`).
  - bytdb v0.21.2 / btypedb v0.9.0 are current in the module cache.

  The fix is a dependency bump plus error handling, not our own `flock` in
  `openDatabases`. README line 56 ("the second to start won't be able to
  open them") is false until then. I raised the value to **high** because
  `gonotes account`'s health probe is a workaround in use today.
- **Roadmap, light pass.** N-004: Monaco is now 12MB / 96 files after N-005,
  and is still committed. N-028: the web UI still fetches `/notes` whole.
  Neither is done, and neither is ready to promote.

## Sorted view (by age)

1. **N-002** (age 28 · value low): sequential sync fan-outs
2. **N-003** (age 28 · value low): migration skips the change log
3. **N-021** (age 23 · value low): duplicate's follow-up flag default
4. **N-030** (age 19 · value low): batch-delete fix never confirmed against
   the real bug
5. **N-050** (age 7 · value high): no cross-process DB lock; bump bytdb to
   get one
6. **N-051** (age 7 · value low): stale JWT GUID after a reconcile or merge

Validate: empty. Roadmap: N-004, N-028.

## Suggested, not done (user's call)

- N-002, N-003, N-021 → Non-goals (old, low, and the DuckDB migration is
  long past).
- N-030 → Closed (no recurrence in 19 sessions).

## Files

- `ai_docs/todo/next-list.md`: N-050's text and value updated, N-004's
  sizes corrected.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-050, N-004. Full list: `ai_docs/todo/next-list.md`.
