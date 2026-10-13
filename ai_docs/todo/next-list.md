# GoNotes — Next list

The one living list of open follow-ups for this project. Sessions edit it in
place rather than copying a "Next" section forward into each session doc, so an
item can only leave by a visible edit here. Session docs summarise their edits
as `Closed: N-… Raised: N-…`.

Seeded 2026-09-23 by `/next-list seed` from the 15 newest session docs
(`2026-0722-1558-migrate-duckdb-to-bytdb` … `2026-0909-1858-advanced-sql-search`),
with every item's premise re-checked against the code at `e0c2a39`.

## Conventions

- **IDs** (`N-001`…) are permanent and never reused, renumbered or recycled.
- **`raised`** is the session-doc stem (in `ai_docs/claude_sessions/`) where the
  item first appeared, even if that is older than any review window.
- **Age** is computed (count of session docs since `raised`), never stored.
- **Value** is the payoff, not the effort:
  - `high` — something is being worked around today, or a second independent
    consumer has arrived
  - `medium` — it blocks one named thing, or it is a visible defect nobody has
    to route around yet
  - `low` — a gap nobody has bumped into, or contingent on something that does
    not exist
- **Open** is what we intend to pick up next. **Roadmap** is wanted but
  deliberately later. **Non-goals** is what we are likely never to do, kept so
  it stays visibly declined.
- **Validate** sits directly below Open: items whose remaining work is purely
  testing (a hand check in a real terminal or app, a run on real data or
  hardware, or a test to write or repair), with no product change planned. A
  check that finds a defect raises it as a new Open item; an Open item whose
  fix has landed but is unchecked moves to Validate.
- Nothing leaves Open, Validate or Roadmap without a line in another section
  (Closed, Non-goals, or a move among Open, Validate and Roadmap). Deleting a
  line outright is the leak this file exists to prevent.
- Open, Validate and Roadmap stay in ID order. Edit an item's text in place
  when its premise changes; keep its ID and `raised`.

**Next ID:** N-055

## Open

- **N-053** · raised `2026-1013-1525-ui-reorder-filter-bar-sync-panel` · value low
  After regex mode is switched off by the toolbar's Clear (`clearSearchBar`)
  or the filter bar's Reset (`clearAllFilters`), the search input keeps the
  "Search by regex pattern..." placeholder. Both reset `state.filters.regex`
  and the button's `active` class but not the placeholder, which only
  `toggleRegex` sets (`web/static/js/app.js`). Pre-dates the UI reorder.

## Validate

Items whose remaining work is purely testing: hand checks in a real terminal
or app, runs on real data or hardware, and tests to write or repair. No
product change is planned unless a check finds a defect, which is then raised
as a new Open item. Added 2026-10-07 and seeded empty: N-030, the one item
first moved here, closed on 2026-10-11 before the section landed. Move items
here from Open by hand, keeping their ID and `raised`.

- **N-054** · raised `2026-1013-1525-ui-reorder-filter-bar-sync-panel` · value low
  Hand check of the UI reorder in a real browser: the filter row above the
  list (pills light up via CSS `:has()`), sort beside select-all, the ⚙
  Sync/Settings panel (Resolve opens the conflict list on top of it), and
  search-term marks in the preview. Verified only in headless Chrome over
  CDP, at 1400px and 700px, in both themes.

## Roadmap

Wanted, but deliberately not next. Parked, not declined. Seeded empty: no
session doc marked an item as deferred, so move items here from Open by hand.

- **N-002** · raised `2026-0722-1558-migrate-duckdb-to-bytdb` · value low
  Sequential fan-outs remain in low-frequency paths: `GetSyncStatus` counts,
  `computeSyncChecksum`, `changeGUIDExists` (`models/sync_protocol.go`). They
  could be parallelized, but none of them is hot.

- **N-003** · raised `2026-0722-1558-migrate-duckdb-to-bytdb` · value low
  The DuckDB→bytdb migration skips the sync change log. Revisit only if a
  migrated spoke must push its pre-existing notes to a hub without a full
  snapshot reconcile.

- **N-004** · raised `2026-0812-1306-monaco-editor-option` · value low
  Vendored Monaco (12MB, 96 files under `web/static/vendor/monaco/` since
  N-005 dropped the locales; 13MB/105 when raised) is committed to git. If repo size becomes a concern, gitignore it and run
  `scripts/vendor_monaco.sh` as a build step.

- **N-028** · raised `2026-0819-1410-web-batch-delete` · value low
  The web UI loads the whole library, with no limit and no "very large
  library" signal. If this ever matters, the answer is real server-side
  search and paging, not a silent cap.

## Non-goals

- **N-035** · declined `2026-0817-1420-gonotes-note-locks` — Per-field leases.
  Leases are per note, so two people can't edit one note's body and tags at
  the same time. That is intended. If a finer grain were ever wanted, it
  would go in the version guard's fragment machinery (`FragmentBody` etc.).
- **N-036** · declined `2026-0909-1858-advanced-sql-search` — A `gonotes
  query "…"` CLI subcommand. It would have to open the databases directly, so
  it would only work with the server stopped. The TUI's HTTP fallback is the
  better route and already exists.
- **N-008** · declined `2026-0923-1203-next-list-batches` — Mouse support in the TUI agent picker and
  confirm dialog. `tui/mouse.go` already records this as deliberate: they are a
  few rows in a `lipgloss.Place`d box, so hit-testing means repeating the
  centering math, and a two-choice dialog doesn't earn that. Raised in
  `2026-0817-0015-tui-mouse-filter-and-store-identity`.
- **N-021** · declined `2026-1011-2315-n021-dup-flag-non-goal` — Changing whether a duplicate
  carries the follow-up flag by default. The duplicate dialog already offers
  it as a row (`dup-flagged` in `web/static/js/app.js`, and the TUI's
  dialog), so whoever wants the flag on the copy ticks it. The item was
  wait-and-see and named Non-goals as its likely end, and no session since
  has raised the default again. Raised in `2026-0818-1739-duplicate-note-dialog`.

## Closed

- **N-030** · raised `2026-0819-1410-web-batch-delete` · closed
  2026-10-11, `2026-1011-2314-n050-n051-n030-db-lock-stale-jwt` — Batch-delete fix vs. the user's real bug. Closed on the item's
  own condition: the symptom hasn't come back. None of the 21 session docs
  since 2026-08-19 reports deleted notes reappearing (searched for
  reappear/resurrect/undelete/came back; the only hits are unrelated). If
  deleted notes ever do come back, that is the sync path and gets a new item.

- **N-051** · raised `2026-1004-2039-n025-account-rename-merge` · closed
  2026-10-11, `2026-1011-2314-n050-n051-n030-db-lock-stale-jwt` — Stale JWT GUIDs. `JWTAuthMiddleware` now looks up the token's
  account on every authenticated request (`currentUserForToken` in
  `web/middleware.go`). A GUID no account holds, or a disabled account (the
  rule Login and Refresh already apply), is treated as unauthenticated, so
  `RequireAuth` answers 401: the web UI goes to `/login`, and the TUI's HTTP
  store re-logs in silently. Username and admin come from the record, not the
  claims. No cache: one indexed read in memory is cheap, and a cache would need
  invalidating from the in-server reconcile, which is the same gap again.
  Checked with `web/api/auth_stale_guid_test.go`: after a merge the old token
  gets 401 on GET and POST `/notes` (on the old middleware it got 200 and an
  empty list), signing in again works, and a rename keeps the session. Full
  suite green.

- **N-050** · raised `2026-1004-2039-n025-account-rename-merge` · closed
  2026-10-11, `2026-1011-2314-n050-n051-n030-db-lock-stale-jwt` — Two processes on one database. bytdb bumped v0.11.0 → v0.21.2
  (btypedb v0.7.0 → v0.9.0). Every open engine now holds a `<db>.lock` flock,
  and a second `InitDB` fails with `models.IsDBLocked`, worded for a person
  (`models.LockedMessage`). `main` prints it as a plain stderr line ahead of
  the structured log. `openDatabases` now closes the public engine when the
  private open fails, so a failed open leaves nothing locked. `runTui`'s
  server fallback now fires only on the lock: a corrupt WAL or a bad key is
  reported instead of being redirected to someone else's notes. `account`'s
  health probe stays as the friendlier message, since it names the server's
  URL, with the lock as the backstop. README, the gonotes skill, comments and
  `.gitignore` (`*.bytdb.lock`) updated. Checked with the full suite on the
  new bytdb, new `models/db_lock_test.go`, and a scratch server: `account
  list`, `export-md` and `tui --local` beside it are all refused, naming the
  holder PID. The `runTui` fallback itself wasn't driven end to end (that
  needs a second server on 8444, which here is the user's live one).

- **N-015** · raised `2026-0817-1420-gonotes-note-locks` · closed
  2026-10-05, no session doc — Mobile parity. The app is
  `github.com/rohanthewiz/gonotes_mobile`, not cats-mobile, which is the cats
  agent client and has no notes. It is now
  Go on grmob, on its `master` since 2026-10-05 (`ec71de1`). The Kotlin
  spoke is archived in that repo. Re-checked 2026-10-05:
  - **Locks and version guards: not applicable.** The app calls only the
    auth and `/sync/*` endpoints, never `/notes`. Sync apply sits outside the
    lock protocol by design, and the hub bumps `version` on apply, so a held
    form gets the stale-write 409 instead of a silent clobber.
  - **Sync affordance: done.** Hub setup, prompt/auto sync with a due banner
    and snooze, and sync-now. Background sync while the app is closed is
    gone with the Kotlin WorkManager job: grmob has no equivalent yet.
  - **Duplicate action: done**, with the same dialog as gonotes.
  - **Hub protocol: verified end to end.** The app's e2e test
    (`internal/spoke/e2e_test.go`) drives two phones and the REST API against
    a real hub built from this repo: push, pull, relay between phones,
    refiling relay, delete, checksum parity. That test found the hub's
    filing-relay gaps (N-052, fixed in `cf561e2`). The phone's workaround
    is removed (`7b00746`), so it needs a hub at or past `cf561e2`.
  - **Parity:** everything a user does on the web UI except server-only
    features (admin, invites, edit locks, hub compaction) and Mermaid
    rendering.

  Closed because every point above is resolved. The remaining mobile work
  (device checks, iOS run, background sync, share intents) is tracked in
  gonotes_mobile's own `ai_docs/todo/next-list.md`, not here.

- **N-052** · raised `gonotes_mobile:2026-1005-0132-grmob-rewrite-gonotes-parity`
  · closed 2026-10-05, no session doc (branch `roh/relay-categories`) — The
  hub dropped note filing from its relays, which affects desktop spokes as well
  as the phone. The mobile e2e test found three gaps:
  - A create's relay carried only the plain fields.
  - A categories-only update returned early in `ApplySyncNoteUpdate`, so it
    was never relayed. Desktop's own refiling is exactly this shape.
  - An update's relay snapshotted the links *before* the caller applied the
    new mapping, so it relayed the old filing (`null` for a first filing,
    which peers apply as "unfile").

  Done: both apply functions now land the mappings themselves
  (`applyIncomingFiling`), between the note write and the relay record. The
  create relay claims the categories bit when the incoming change did. A
  categories-only update leaves the note row and its version alone.
  `models/sync_relay_categories_test.go` covers each gap and fails on the old
  code. The mobile app's `filingEcho` workaround can now go.

- **N-005** · raised `2026-0812-1306-monaco-editor-option` · closed
  2026-10-04, `2026-1004-2338-n005-drop-monaco-locales` — Binary size from the vendored Monaco. **Locales dropped; the
  TypeScript language kept by the user's choice.** The 9 non-English
  `vs/nls.messages.*.js` bundles (1.8MB) never load: editor.main.js's
  `nls.messages-loader` fetches one only when `require.config` sets
  `'vs/nls': { availableLanguages }`, which `monaco_editor.js` never does.
  So removing them leaves the full `monaco.*` API intact, with no need to
  relax "full" mode. They are deleted from the vendor dir, and
  `scripts/vendor_monaco.sh` strips them on every re-vendor. The binary went
  from 48.8MB to 47.0MB. The headless-Chrome Monaco run passes with no
  locale request and no 404. `vs/language/typescript` (5.5MB) stays,
  because dropping it would remove `monaco.languages.typescript`, part of
  the full build that was asked for.

- **N-007** · raised `2026-0812-1306-monaco-editor-option` · closed
  2026-10-04, `2026-1004-2329-n007-monaco-e2e-paste-fix` — The Monaco editor was exercised end to end in headless Chrome
  over CDP, and that **found a bug: image paste inside Monaco had never
  worked.** Monaco's own paste controller registers a capture-phase paste
  listener on the container it's created in, before `setupImageHandlers`
  ran, so it cancelled the event and stopped immediate propagation, and the
  resize dialog never opened. Fixed by moving the app's paste, dragover and
  drop handlers to the container's parent (capture on an ancestor runs
  first), acting only on targets inside Monaco. Also: cache-buster to
  `monaco_editor.js?v=3`, and the toggle tooltip no longer claims a CDN
  load. Passing in the run:
  - activation from the vendored copy
  - typing mirrored into the textarea and saved
  - toggling mid-edit in both directions
  - image paste and drop through the dialog, undoable in one step
  - paste into the plain textarea unaffected
  - note switching without cross-note undo
  - the preference surviving a reload
  - no console errors
  The script stayed in the scratchpad, and the harness is now a saved
  memory (`cdp-browser-harness`).

- **N-022** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · closed
  2026-10-04, `2026-1004-2318-n022-compact-one-note` — Compaction was all-or-nothing per peer. Done backend only, by
  the user's choice (no web or TUI button, since pending rows are invisible
  to users). `models.CompactNotePendingChanges` / `PreviewNoteCompaction`
  (`models/sync_compact_note.go`) collapse one note's pending tail with the
  whole-log machinery (`groupNoteChanges`, `compactNoteGroup`) and leave
  every other note and category alone. Counts are that note's. Ownership is
  checked against the note row in either database, soft-deleted included,
  so a tail ending in a delete still compacts. Unknown and foreign notes get
  the same `ErrCompactNoteNotFound`. Exposed as `?note_guid=` on `GET` and
  `POST /api/v1/sync/control/compact` (404 for the error) via
  `SyncClient.CompactNote` / `PreviewCompactNote`. The decline rule moved
  into `noteGroupCompactable`, shared by both previews. New
  `models.ResetSyncClientForTest` lets API tests stand up a spoke.

- **N-023** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · closed
  2026-10-04, `2026-1004-2055-n023-push-carries-no-relays` — A spoke's push batch was thought to carry the relay
  (operation 9) rows it records while pulling. Measured, and it doesn't:
  `applyChangeWithConflictDetection` marks each pulled change as delivered
  to the hub (`MarkChangeGUIDSyncedToPeer`, added in `cff9559`, the same
  relay work whose follow-up raised this), so `GetUnifiedChangesForPeer`
  never returns them for the hub's peer. A relay reaches a push only if that
  best-effort mark fails or the process dies between apply and mark. That
  costs one entry, once: the hub skips it by GUID and accepts it, and the
  spoke then marks it. No filter added. The hub direction must keep
  returning relays, since they are what other spokes pull.
  `TestASpokesPushCarriesNoRelaysItPulled` drives a mixed pull (relayed
  create, update, privacy flip, delete, category create and rename) through
  the real apply path and checks the push batch. With the mark removed, it
  fails on all seven relays.

- **N-026** · raised `2026-0818-1934-spoke-user-guid-alignment` · closed
  2026-10-04, `2026-1004-2051-n026-current-hub-identity` — Spokes with more than one hub were untested, and testing them
  found a bug. A spoke keeps a `sync_state` row for every hub URL it has
  used. When the stale hub's URL sorted first, the old `LIMIT 1` lookup
  returned that hub's GUID, so a newly registered account (and
  `account rename`/`merge`) adopted a GUID no incoming note carries, until
  the next server start rewrote every row back. Done: `currentHubState`
  treats the most recently active row as the current hub, and
  `hubIdentityForUsername` consults only that row. An earlier hub never
  stands in, even before the new hub's first login. `NewSyncClient` stamps
  its hub current (`MarkCurrentHub`), and `RecordHubIdentity` stamps
  `updated_at`, so switching back to an old hub works too. No env var is
  read, because `gonotes account` may run without the server's settings.
  `RecordedHubIdentities` marks `Current`. `account list` advises only on
  the current hub, labels the others "earlier hub", and the dry-run preview
  follows the same rule. Tests cover both URL orders, a not-yet-logged-in
  new hub, switching back, startup and align, and the list output.

- **N-049** · raised `2026-1004-n016-bulk-lock-gate` · closed 2026-10-04,
  `2026-1004-2045-n049-category-link-lock-gate` —
  The per-note category writes skipped the lock gate, so a second session
  could refile a note another session's form held and that form's save would
  write its own set back over it. Done: all four link routes
  (`POST`/`PUT`/`DELETE /notes/:id/categories/:category_id` and the whole-set
  `PUT /notes/:id/categories`) call `authorizeNoteWrite`, and `localStore`'s
  five link writers call `AuthorizeNoteWrite`, as `UpdateNote` does. The
  premise was half right: the web form does send the token (`leaseHeaderFor`
  matches every `/notes/:id/...` path), but `httpStore`'s link writers did
  not, so gating alone would have refused the TUI's own save over HTTP. They
  now send `lockHeaders` and return the typed `*NoteLockedError` on a 409 with
  reason `locked`. Only that reason is converted: attach's other 409
  ("already added") stays a plain error. The fakeStore and the fake API gate
  the same way. The web batch bar already reports a 409 as a per-note
  failure, so a held note shows up in its summary.

- **N-025** · raised `2026-0818-1934-spoke-user-guid-alignment` · closed
  2026-10-04, `2026-1004-2039-n025-account-rename-merge` — A local account
  named differently from `GONOTES_SYNC_USERNAME` was diagnosed but couldn't
  be fixed in the app. Done as a CLI subcommand, `gonotes account list|rename|merge`
  (`account_cmd.go`, `models/account_admin.go`), not a web endpoint. The
  repair rewrites the identity of the account a web session is signed in
  as. `list` shows each account, what it owns, and the recorded hub
  identity, and prints the command that fixes a mismatch. `rename` handles
  the case where only the misnamed account exists. `merge` handles the case
  where the old advice was followed and both exist: it re-points every row
  and deletes the source last, so a crashed merge re-runs. Both then run
  `AlignLocalUserWithHub` so the hub GUID is adopted at once. Merge carries
  admin rights over and names any category names the two accounts share; it
  doesn't combine them. Along the way: the identity sweep missed
  `saved_queries` (added after it), so a reconcile left saved queries under
  the old GUID. The sweep and the new counters now share one inventory,
  `userGUIDRefs()`. Checked end to end with a real hub and two spokes. It
  raised N-050 (no cross-process DB lock) and N-051 (stale sessions after a
  GUID change).

- **N-016** · raised `2026-0817-1420-gonotes-note-locks` · closed 2026-10-04 —
  The category screens had no lock gate. The premise had already changed:
  subcategory rename (N-001, `f885eec`) is a bulk note operation that rewrites
  every filed note's link, and an open form's save would write the old name
  back. Done: `models.RenameSubcategory` finds every target in both databases,
  checks them with `AuthorizeBulkNoteWrite`, and only then writes. A lease
  held by any session, the caller's own included, blocks the rename, and a
  refusal changes nothing. The refusal is a `*NotesLockedError` that lists
  every blocking lease. Over HTTP it is a 409 with reason `notes_locked`, and
  `httpStore` turns it back into the same type. It is not a
  `NoteLockedError`, so the TUI shows a status line, not the one-note
  contention dialog. `InitTestDB` now empties the lock registry, because leases
  leaked by earlier tests were landing on reused note ids. Category rename and
  delete don't touch note links, so they need no gate.
- **N-048** · raised `2026-1003-1606-n034-saved-queries` · closed 2026-10-03, `2026-1003-2009-n048-tui-save-forget-queries` —
  The TUI query screen showed saved and recent queries but could not name one or forget a row. Done: `SaveQuery` / `DeleteSavedQuery` sit next to `RecordQuery` on the Store seam (local → `models`, HTTP → `POST`/`DELETE /api/v1/notes/query/saved`). A syntax refusal comes back as the positioned `QueryError` in both modes, and an already-gone row is not an error. On the query screen, `ctrl+s` (and ⌘S) opens the shared name prompt, prefilled when the text is already saved. `shift+delete` (or `ctrl+x`) forgets the highlighted ☆/↺ row and re-completes. A `✓` line acknowledges both, and the footer shows forget only on a stored row. Tested against the fake store, the HTTP wire and the real local store.
- **N-012** · raised `2026-0817-1046-tui-subcategory-support` · closed 2026-10-03, `2026-1003-1628-n012-subcategory-any-match` —
  Toggling several subcategories meant AND only, in both UIs. Done: an all/any switch, with AND still the default. `models.SubcategoryMatch` and `GetNotesByCategorySubcategoryMatch` (plus `GetNotesByCategoryAndAnySubcategory`), and the API takes `subcats_mode=any`. The TUI subcategory screen flips with `m`, and the heading and list title end in `(any)`. The web chips get a leading `all of` / `any of` button, and the query string shows `match:any`. Checked in headless Chrome against a scratch server.
- **N-034** · raised `2026-0909-1858-advanced-sql-search` · closed 2026-10-03, `2026-1003-1606-n034-saved-queries` —
  The query bar had no note-link completion, no saved queries, and kept history in `localStorage`. Done: `guid = ` completes notes by title (or GUID prefix, or a pasted link) and inserts the GUID. Saved queries and history live server-side in `saved_queries` (private DB, `models/saved_query.go`) behind `/api/v1/notes/query/saved` and `/query/history`. The completer leads an empty box with them, so web and TUI share them. The web bar saves (☆, ⌘S), forgets (`Shift+Delete`, ×) and uploads its old `localStorage` list once. Checked in Chrome against a scratch server.
- **N-009** · raised `2026-0817-0015-tui-mouse-filter-and-store-identity` · closed 2026-09-23, `2026-0923-1124-medium-next-items` —
  TUI `--local` / `--remote`. Done: `gonotes tui --local` skips the probe and fails rather than use a server; `--remote` fails rather than use local notes (`decideForcedStore`, `main.go`).
- **N-014** · raised `2026-0817-1420-gonotes-note-locks` · closed 2026-09-23, `2026-0923-1124-medium-next-items` —
  The web UI didn't take leases. Done: `app.js` "Note Leases" acquires in `editNote` (asking before taking over), renews every 30s and on tab focus, releases on preview/new note/`pagehide`. `apiRequest` sends the token for the leased note. Checked in Chrome against a scratch server.
- **N-020** · raised `2026-0818-1739-duplicate-note-dialog` · closed 2026-09-23, `2026-0923-1124-medium-next-items` —
  One request per category link. Done: `PUT /api/v1/notes/:id/categories` replaces the whole set (`models.SetNoteCategories`: validate first, diff, one sync change). The web save, web duplicate, TUI form sync and TUI duplicate all use it.
- **N-024** · raised `2026-0818-1859-sync-relay-convergence-and-followups` · closed 2026-09-23, `2026-0923-1124-medium-next-items` —
  The hub's change log was never compacted. Done: `models.CompactHubChangeLog` (`sync_hub_compact.go`) collapses quiet entities to full op-9 snapshots and tombstones the superseded GUIDs. Admin `POST /api/v1/admin/sync/compact` or `GONOTES_HUB_COMPACT_INTERVAL`.
- **N-027** · raised `2026-0819-1410-web-batch-delete` · closed 2026-09-23, `2026-0923-1124-medium-next-items` —
  Batch failures stacked one toast per note. Done: `apiRequest` takes `quiet: true`; the batch loops use it and summarise once.
- **N-029** · raised `2026-0819-1410-web-batch-delete` · closed 2026-09-23, `2026-0923-1124-medium-next-items` —
  The batch **Set Category** / **Toggle Privacy** buttons threw TypeErrors. Done: **Add Category** (keeps existing categories, comma-separated, creates unknown names) and **Toggle Privacy** (a mixed selection converges, via new `PUT /api/v1/notes/:id/privacy`).
- **N-006** · raised `2026-0812-1306-monaco-editor-option` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  `scripts/download_vendor.sh` was stale. Done: deleted. It wrote to a directory that no longer exists, msgpack loads from the CDN, and `vendor_monaco.sh` covers Monaco.
- **N-017** · raised `2026-0817-1420-gonotes-note-locks` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  `ReleaseNoteLocksForSession` had no HTTP door. Done: `DELETE /api/v1/note-locks?session_id=…` (user-scoped via `models.ReleaseNoteLocksForUserSession`, since a session id is not secret). The TUI HTTP store uses it on shutdown and falls back to per-note releases on an older server.
- **N-033** · raised `2026-0827-1640-bytdb-v0.11-bump-notnull-version` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  `gofmt -l` flagged files. Done: the whole tree is gofmt-clean. Two comments were reworded first so gofmt would not turn `''` into a typographic quote or indent a line into a code block.
- **N-044** · raised `2026-0923-1124-medium-next-items` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The spoke compactor placed a compacted category at its LAST timestamp. Done: `compactCategoryGroup` now uses the FIRST (a net delete keeps the last), matching the hub. `TestCompactKeepsARenamedCategoryAheadOfItsNotes` fails with the old placement.
- **N-042** · raised `2026-0923-1043-web-comma-categories-and-next-list-seed` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The web category `<datalist>` stopped suggesting after a comma. Done: `refreshCategorySuggestions` rebuilds the options on each keystroke with the typed prefix in front (`Work, Pe` → `Work, Personal`), and after a `/` it offers the category's subcategories. The option lists were checked in Chrome; whether the native popup shows them was not, because the popup never opened under automation.
- **N-043** · raised `2026-0923-1043-web-comma-categories-and-next-list-seed` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The web category input ignored `/` notation. Done: `parseCategorySpec` reads `Work/backend` as the TUI does. A defined subcategory matches case-insensitively and an unknown one is created. An existing category whose whole name contains `/` still matches literally. The batch dialog refuses `/` rather than drop it.
- **N-045** · raised `2026-0923-1124-medium-next-items` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The web batch bar couldn't remove a category. Done: a separate **Remove Category** action. Unknown names are refused, only linked pairs get a DELETE, and a 404 for an already-gone link counts as success. While building it, found and fixed a gap: `DELETE` and `PUT /api/v1/notes/:id/categories/:cid` never checked note ownership, so any signed-in user could unlink or rewrite categories on another user's note by id. `TestRemoveCategoryFromAnotherUsersNote` covers it.
- **N-018** · raised `2026-0818-1739-duplicate-note-dialog` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The web duplicate dialog had no keyboard navigation beyond Enter. Done: ↑/↓ move between the title and the checkbox rows, space toggles, Enter confirms from any row, and the focused row is outlined (`:focus-visible`). Checked in Chrome with real key presses.
- **N-031** · raised `2026-0821-1626-summarize-ui-web-tui` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  Web summaries were untagged. Done: the clipboard door tags the new note `summary`, as the TUI does (the body door doesn't, in either UI). Found while doing it: the web form sent `tags: null` on every save and `UpdateNote` writes every column, so **editing a note in the web UI erased its tags**. `state.editTags` now carries a note's tags through the edit. Checked in Chrome: a tagged note keeps `infra,api` after a web edit.
- **N-032** · raised `2026-0826-1559-summarize-progress-indication` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The icon-only summarize button was dimmed but not animated. Done: `busy()` sets `aria-busy` and `app.css` pulses the icon. The animation is off under `prefers-reduced-motion`.
- **N-046** · raised `2026-0923-1124-medium-next-items` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The web note list had no "being edited elsewhere" badge. Done: `refreshNoteLocks` polls `GET /note-locks` every 30s while the tab is visible (and on return) and re-renders only on a change. It skips this tab's own lease, and the tooltip names the holder. Checked in Chrome against a curl-held lease.
- **N-010** · raised `2026-0817-0015-tui-mouse-filter-and-store-identity` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The TUI never said *why* an HTTP request failed mid-session. In fact it did, but as Go's raw transport error (`Put "http://…": dial tcp …: connection refused`), and the one-line status bar truncated it before the cause. Done: `errReason` (`tui/errreason.go`) names refused, timed out, unknown host, dropped connection, 5xx and 401 in a few words, and `statusErr` uses it. Tested against real failures from the HTTP store.
- **N-011** · raised `2026-0817-1046-tui-subcategory-support` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  `ctrl+g` capture filed notes with no category. Its form always had an editable Categories field; what was missing was a default. Done: the field is preset to the list's category filter (`Work/backend`), and left empty when the list is unfiltered or a query is in force. A preset alone doesn't make the form dirty.
- **N-019** · raised `2026-0818-1739-duplicate-note-dialog` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The TUI duplicate dialog showed nothing while the copy was made. Done: `confirm` sequences pop → "Duplicating…" → the create, and the result's status replaces it.
- **N-001** · raised `2026-0710-1656-tui-implementation` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  Category and subcategory rename in the TUI. Done: `r` on both screens. A category rename is the whole-object update and refuses a name another category has (case-insensitive). A subcategory rename goes through the new `models.RenameSubcategory` (links first, then the definition, safe to re-run, merges onto an existing name) and `POST /api/v1/categories/:id/subcategories/rename`, so notes filed under the old name follow it. The web UI has no subcategory rename yet.
- **N-013** · raised `2026-0817-1046-tui-subcategory-support` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The TUI subcategory screen showed no note counts. Done: one `SubcategoryNoteCounts` read per screen (the bulk note-category mappings, counted client-side, not a query per row), reloaded after a rename. Rows show nothing until the counts load.
- **N-047** · raised `2026-0923-1203-next-list-batches` · closed 2026-09-23, `2026-0923-1203-next-list-batches` —
  The web category manager couldn't rename a subcategory. Done: click (or Enter on) a tag's name in the manager's edit form to rename it inline. A saved subcategory is renamed at once through the rename endpoint, so its notes are refiled, and the staged list follows. A staged-only one is renamed locally. Found and fixed at the same time: the manager's Save sent no description, so it erased the category's description. Checked in Chrome.

Closures before this file was seeded are recorded in the session docs
themselves. These were found already done while seeding:

- **N-037** · raised `2026-0817-1046-tui-subcategory-support` · closed 2026-09-23 —
  Tag a GoNotes release (cats-todo `b5ac0100…`). Done: `v0.2.0` exists
  (`e0c2a39`).
- **N-038** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · closed 2026-09-23 —
  The web sync banner didn't offer compact-only. Done in `2bdfb0c`: a
  "Compact only" button calls `app.spokeCompactChanges` (`web/static/js/sync.js`).
- **N-039** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · closed 2026-09-23 —
  The TUI polled every 60s even with no sync configured. Done in `2bdfb0c`:
  `syncIdlePollInterval` backs off to 15 minutes (`tui/sync.go`).
- **N-040** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · closed 2026-09-23 —
  Sync mode changes weren't persisted. Done in `2bdfb0c`:
  `POST /sync/control/mode` accepts `persist: true`.
- **N-041** · raised `2026-0818-1859-sync-relay-convergence-and-followups` · closed 2026-09-23 —
  A spoke's local user GUID didn't match the hub's, so synced notes were
  invisible to a locally registered user. Done in
  `2026-0818-1934-spoke-user-guid-alignment`.
