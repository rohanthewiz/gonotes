# Session: Mobile Parity, Premise Check (N-015)

**Session ID:** `4d84aea5-2d3c-4540-88ce-aaff8d3930a6`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `900a242` — Close N-022: compact one note's pending changes

Same session as the N-049, N-026, N-023 and N-022 docs earlier today (latest:
[2026-1004-2318-n022-compact-one-note](2026-1004-2318-n022-compact-one-note.md)).

Picks up N-015 from `ai_docs/todo/next-list.md`, first raised in
[2026-0817-1420-gonotes-note-locks](2026-0817-1420-gonotes-note-locks.md):

> cats-mobile parity. The same app came up three times: unaware of note locks
> and version guards (`2026-0817-1420`), no duplicate action
> (`2026-0818-1739`), and no sync affordance (`2026-0818-1824`, `-1859`,
> `-1934`). Premise unverified: the mobile app has since been rebuilt as a
> sync spoke (branch `roh/sync-spoke-rebuild`), so check that repo before
> acting.

The item asked for a check before acting. The check was done, and its result
replaced the item's text. **No code changed in either repo.** N-015 stays
Open, rewritten. The user was asked which of the remaining mobile-side
pieces to take on.

---

## Finding the app

- `~/projs/go/cats-mobile` is the phone client for **cats** (agents,
  workspaces, panes on grmob). It has no notes feature, no
  `roh/sync-spoke-rebuild` branch locally or on its remote, and no
  `tool/regen.sh`, the script the 0817 session referred to.
- No local repo other than gonotes calls `/api/v1/notes`.
- `gh repo list rohanthewiz` turned up `go_notes_mobile` (a Flutter proof of
  concept, `master` only) and **`gonotes_mobile`** (Kotlin, "Take notes on
  Android"), which has the branch `roh/sync-spoke-rebuild`. It is not cloned
  locally. Everything below was read through `gh api`, read-only.

So "cats-mobile" in the August session docs was a misnomer for
`gonotes_mobile`.

## The branch

`roh/sync-spoke-rebuild`: 8 commits from `b3af0fe` to `ab46321`, all dated
2026-07-04. It is 8 ahead of `master` and 0 behind, so it is unmerged and
`master` hasn't moved. It rebuilds the app as a gonotes sync spoke: a Room
data layer with an outbox and inbox, a sync engine (`HubApiClient`,
`SyncEngine`, `ChangeApplier`, `ChangeRecorder`, `ConflictResolver`),
WorkManager background sync, a CodeMirror 6 editor, and categories. Its unit
tests use golden wire fixtures (`golden_pull_diff_relay.json`,
`golden_checksum.json`, and so on).

## The three complaints, checked

| Complaint | Finding |
|---|---|
| Unaware of note locks and version guards | **Doesn't apply.** `HubApiClient` calls only `/health`, `/auth/{login,register,me}` and `/sync/{pull,push,status,snapshot}`, never `/notes`. Sync apply on the hub is outside the lock protocol by design (`models.AuthorizeNoteWrite`'s doc). Since `9740ad7`, `ApplySync*` bumps `notes.version`, so a web or TUI form holding the note gets the stale-write 409 instead of a silent clobber. The old complaint was about the pre-rebuild app writing to `/notes`. |
| No sync affordance | **Done on the branch.** `SyncSetupScreen` (Settings → Sync), a sync-now button with `pendingSyncCount` on `NoteListScreen`, and a `SyncWorker` on WorkManager. |
| No duplicate action | **Still missing.** No duplicate or copy action in either the list or editor screens, or in their view models. |

## Is the July spoke compatible with today's hub?

Everything sync-related in gonotes after 2026-07-04 was compared:
`436b9f7` (bytdb), `9740ad7` (locks), `f80373c` (prompt mode), `cff9559`
(relay identity) and `a8989dd` (hub compaction).

- **Relays (operation 9):** `ChangeApplier` upserts `SyncOp.SYNC`
  (create-or-update), which is the rule `cff9559` brought to the Go side.
- **No echo loop:** `ChangeApplier` "never records outbox entries", so
  applied changes aren't re-pushed. On the hub, `cff9559`'s
  `MarkChangeGUIDSyncedToPeer` stops the hub handing a spoke's own push back
  to it.
- **Hub compaction snapshots** are operation 9 with a full-field fragment,
  which the upsert handles. The `compacted_change_guids` tombstones live on
  the hub only.
- **Wire constants:** the note bits (0x80…0x04), category bits (including
  `SUBCATEGORIES` 0x20) and `NoteCategoryMappingDto`
  (`category_guid`, `selected_subcategories`) match
  `models/note_change.go`, `category_change.go` and
  `NoteCategoryMappingSnapshot` exactly.
- **Endpoints:** all 8 are in `web/routes.go` (`/sync/snapshot` since
  `2b71acc`, Feb).
- **Checksum:** `computeSyncChecksum` was restructured in `436b9f7`, to
  collect from both databases and concatenate. It still runs
  `sort.Strings` over the combined list before hashing, so the output is
  unchanged and the mobile `golden_checksum.json` still holds.
- The other hub changes (the `version` column, hub compaction
  `changeGUIDExists` tombstones, the prompt-mode count) are internal to the
  server.

**Not verified:** a real end-to-end run. The branch's own "Remaining" list
says no AVD or device test of two-way sync against a hub has been done.

## What remains on N-015 (all in `gonotes_mobile`)

1. A duplicate action.
2. An end-to-end two-way sync test against a hub. It needs an emulator or
   device, which may not be available here.
3. Merging `roh/sync-spoke-rebuild` into `master`, which is the user's call.

Offered to the user. The duplicate action could be written and unit-tested
on that branch after cloning, but not run on a device.

## Files

- `ai_docs/todo/next-list.md`: N-015 rewritten in place, with ID, `raised`
  and value unchanged

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: N-015. Full list: `ai_docs/todo/next-list.md`.
