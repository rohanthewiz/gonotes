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

**Next ID:** N-088

## Open

Every item here comes from one review of the whole app (2026-11-18): four
area reads (sync, HTTP server, web front end, TUI) plus the data layer. Six
of its items (N-055 to N-058, N-060, N-061) were closed in the same session
and are under Closed. The rest are in suggested order: data safety first,
then performance, then usability.
Unless an item says "confirmed", its premise was established by reading the
code and has not been reproduced at runtime; re-check it when picking it up.

- **N-059** · raised `2026-1118-1516-reliability-perf-usability-review` · value high
  A sync cycle still treats changes that did not land as delivered. Push:
  `pushBatch` (`models/sync_client.go`) marks the whole batch delivered,
  including what the hub rejected, so a rejected edit is never retried and
  later body diffs build on text the hub lacks. Since N-060 the rejections
  are at least visible (status `push_rejected` / `push_rejections`, the web
  and TUI sync summaries, an error log line each), and the marking is a
  documented choice: retrying everything forever would let a few permanently
  refused changes be re-sent indefinitely. What is left: have the hub say
  whether a rejection is transient or permanent, retry the transient ones,
  and give a permanent one somewhere to go (a "needs attention" list, or a
  full-snapshot resend). Pull has the mirror problem: an apply error is
  logged and the page is still acknowledged, with `last_error` empty; do not
  ack a page with transient failures.

- **N-062** · raised `2026-1118-1516-reliability-perf-usability-review` · value high
  A conflict's losing edit is still pushed. When a pull resolves a conflict
  as remote-wins, the spoke applies the remote change and leaves its own
  pending change in the log, so the same cycle pushes it. The hub applies
  pushes with no last-writer-wins guard, so the loser overwrites (or is
  fuzzy-patched onto) the winner there, and the hub marks it delivered to
  the sender, which therefore never sees the result: the spoke holds one
  version and the hub and other spokes another. After resolving, rewrite the
  note's pending changes as one full snapshot of the resolved state
  (`compactNoteGroupAs`, allowing a group of one), or drop the losers and
  add an `authored_at` guard to hub apply. No test calls `ResolveConflict`.

- **N-063** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Conflict detection counts changes that were already pushed.
  `GetPendingNoteChanges` (`models/sync_conflict.go`) has no sync-marker
  filter, so every pulled edit to a note this spoke ever edited is treated
  as a conflict and put through LWW, where clock skew can discard a
  legitimate later edit. Each one also writes a `sync_conflicts` row holding
  the full remote change, body included, into the public (unencrypted)
  database, so private note text lands in plaintext there. Add the
  not-yet-sent filter, and keep bodies out of the conflict log.

- **N-064** · raised `2026-1118-1516-reliability-perf-usability-review` · value high
  Ownership may be lost in transit. Category change rows are recorded with
  an empty user, so a spoke applies pulled categories with `created_by`
  NULL, and every category read filters on `created_by`: the category is
  invisible and filing under its name creates a duplicate. Update and delete
  relays, and hub-compacted snapshots, carry the same empty user for notes.
  `MigrateOrphaned*` at first registration may be masking this. **Needs a
  runtime check first** (hub plus a spoke that registers before its first
  pull); if it reproduces, stamp incoming changes with the recorded hub user
  GUID and record the owner on relay and compaction rows.

- **N-065** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  The hub's user-scoped pull query joins the change log to the entity row
  (`INNER JOIN categories … AND c.created_by = ?`, same for notes). A
  hard-deleted category has no row to join, so its delete never reaches
  other spokes. A note that flipped privacy moved databases while its
  earlier change rows stayed behind, so a new or lagging spoke receives only
  the flip and can create the note from a partial fragment. Store the owner
  on the change row and filter on that; refuse to create a note from a
  non-full fragment.

- **N-066** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  No write is atomic across its statements. A note write and its change-log
  row are separate autocommit statements with the second one's failure only
  logged (`models/note.go`), so a crash or error between them leaves an edit
  that never syncs. Sync apply has the same shape (note row, relay row, peer
  mark), and the privacy move inserts into one database and deletes from
  the other, so a crash leaves the note in both. bytdb offers transactions
  (`Engine.WriteTxn`, or a `sql.Session` with BEGIN/COMMIT); give `dbEngine`
  a `Tx` helper, wrap each per-engine sequence, and add a startup repair for
  a note id present in both databases. Also cuts fsyncs per pulled page by
  about two orders of magnitude (each statement fsyncs under SyncAlways).

- **N-067** · raised `2026-1118-1516-reliability-perf-usability-review` · value high
  The web form drops unsaved edits without asking: clicking any list row,
  Escape in any input (including Escape to dismiss the Link to Note popup),
  `n` or `e` when focus is outside an input, a clipboard summary finishing
  while another note is open, and closing or reloading the tab. There is no
  dirty tracking, no draft and no `beforeunload`. Add a dirty flag and one
  `confirmDiscard()` on every exit, a `beforeunload` guard, and a
  localStorage draft keyed by note GUID. Summarize-body replaces the text
  with no undo; keep the pre-summary body for one.

- **N-068** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  A session that expires mid-edit loses the edit. Web: any 401, including
  one from the 30s lease heartbeat or lock poll, clears the token and
  navigates to `/login`; the 7-day token is never refreshed although
  `/auth/refresh` exists; `checkAuth` also clears the token on a 5xx or a
  network error. TUI over HTTP: a resumed session has no password to
  re-login with, so after expiry every call fails and the advice is to
  restart, which discards the form. Refresh the token while active, and on
  a 401 during an edit ask for the password in place.

- **N-069** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Shutdown does not wait for work in flight. rweb closes the listener and
  returns, open connections keep being served through the exit sync, and
  `CloseDB` then sets the engines to nil under them. `SyncOnExit` and
  `SyncNow` return success when another cycle holds the lock (`TryLock`
  fails, `runSyncCycle` returns nil), so "Final sync complete" can be logged
  over a cycle that is still running when the databases close. Add a
  stopping flag plus WaitGroup middleware, have `SyncOnExit` wait on the
  cycle lock (bounded), and let a second signal abort the exit sync.

- **N-070** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Server hardening for a hub reachable from a phone. Login has no attempt
  limit (and unknown usernames answer faster than known ones);
  `RateLimitMiddleware` exists but is unused and keys on a client-supplied
  header. The request body cap is rweb's 100 MB default, before auth. A
  missing `GONOTES_JWT_SECRET` falls back to a constant in the source with
  no warning. `/sync/push` applies updates and deletes by GUID with no owner
  check (multi-user hubs only). The server speaks plain HTTP with no TLS
  option and no documented proxy. Each is small; do them together.

- **N-071** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Static assets are versioned by hand and it has drifted: `image_embed.js`
  changed in `66c3670` and is still `?v=2`, `auth.js` and `setup.js` have no
  version, the login page uses `app.css?v=4` against the landing page's
  `v=19`. Assets are served `max-age=3600` with no ETag, uncompressed, so a
  phone re-downloads about 400 KB every hour and can run stale JS for up to
  an hour after an upgrade. Use the build commit (already available for the
  build badge) as the version on every asset through one helper, serve
  versioned assets `immutable`, and gzip text assets and large JSON.

- **N-072** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  The page needs five CDN scripts to render a note (marked, DOMPurify,
  highlight.js, mermaid; msgpack is gone as of N-056). `marked` is unpinned
  and mermaid is `@11`, so an upstream release changes every install, and
  offline the body pane silently keeps the previous note's content.
  Separately, a stopped server shows a "Failed to fetch" toast each minute
  while the status bar still says "Synced". Vendor and pin the libraries
  beside Monaco, fall back to escaped text when one is missing, and drive a
  "server unreachable" status from consecutive poll failures.

- **N-073** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Avoidable web client costs with the whole library loaded (N-028 stays
  parked; these apply within it). Every save, delete and batch action
  re-downloads all notes with bodies although the response already holds
  the note. A checkbox tick or search keystroke runs the filter and sort
  twice and rebuilds every row. Search lowercases every body per pass,
  embedded `data:image` payloads included, which also gives false hits on
  short terms. Selecting a note fetches categories it already has in
  `state.noteCategoryMap`, with no stale-response guard.

- **N-074** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  Unsent-change queries use `id NOT IN (SELECT …)`, which bytdb evaluates as
  a linear walk of the subquery per outer row: cost grows as changes times
  rows already marked for that peer. They run three times per status poll
  (every 60s per tab and TUI) and six times per pull page. Fine at today's
  log size; replace with a per-peer high-water mark or an anti-join before
  the log reaches tens of thousands of rows. Not benchmarked.

- **N-075** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  TUI costs that grow with the library. Every "back" from a note reloads all
  notes with bodies, changed or not. The `/` filter converts and lowercases
  every body on each keystroke. The preview pane renders the whole note
  through glamour inside `View()` and shows one screenful, and its cache is
  dropped whenever the detail view's different wrap width is used.

- **N-076** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Web list goes stale under an advanced query: `getFilteredNotes` returns
  the query's snapshot, mutations update only `state.notes`, and nothing
  re-runs the query, so a flagged, edited or deleted note keeps its old row.
  The "Unsynced" filter chip lights up and filters nothing
  (`state.filters.unsynced` is set and never read; confirmed).

- **N-077** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Rendered note content has more reach than it should. Mermaid runs with
  `securityLevel: 'loose'`, which allows click handlers and unsanitised
  URLs in a diagram; the DOMPurify config allows `id` and form elements, so
  a note body can shadow the app's own element ids; toasts are built with
  `innerHTML` from server and user text. `escapeHtml` is also used inside
  inline `onchange="…'${…}'"` attributes, where it does not escape quotes:
  a category or subcategory with an apostrophe gets checkboxes that toggle
  on screen and change nothing.

- **N-078** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  TUI async replies go to whichever screen is on top. A summary can land in
  a different note's form than the one that asked, or replace text typed
  while waiting; a list load that returns after `n` was pressed is dropped
  and the list stays empty; two loads in flight finish in either order.
  Replies carry no request identity, and there is no refresh key. Stamp a
  generation on list loads, carry the requesting form on summary replies,
  and add a refresh binding.

- **N-079** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  TUI, with a `/` filter applied: any reload (back from a note, flag,
  delete, save) returns to page 1 and pagination stays at one page, so
  results past the first page are unreachable until a resize or re-filter.
  From bubbles v2.1.1 `SetItems` clearing `filteredItems` and
  `FilterMatchesMsg` not recomputing pagination. Read from library source,
  not run; write a teatest for it first.

- **N-080** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  Smaller TUI reliability items. "Sync now" over HTTP uses the 15s client
  while the server syncs synchronously (30s per hub request), so a slow hub
  reads as "Sync failed" while the cycle continues. The sync-due dialog can
  open while typing in the `/` prompt, and the next key answers it.
  `$EDITOR` values with arguments (`code -w`) fail, and a non-zero editor
  exit deletes the temp file unread. The lease keeper stops for good after
  one failed renew, its "saving is blocked" banner is not enforced, and one
  goroutine leaks per edit form. The token cache is not keyed by server URL.

- **N-081** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  No TUI screen lists every key. `?` on the list shows the widget's keys and
  none of the app's (`AdditionalFullHelpKeys` is never set), the short
  footer is cut off in the wide layout, and in the detail view `f` and `d`
  (the viewport's paging keys) flag and delete instead.

- **N-082** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  Web keyboard and feedback gaps: list rows are not focusable and have no
  arrow navigation; every toast, including the lease-lost warning, vanishes
  after 3s; Ctrl/Cmd+S works only with focus in an input and can send
  concurrent saves; links in a rendered note open in the same tab and
  replace the app.

- **N-083** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  Summarize failures reach the user as "exit status 1" (the handler returns
  `err.Error()`, which for a serr is the root error), every request forks a
  `claude` process for up to 3 minutes with no concurrency cap, closing the
  tab does not cancel it, and the model name is unrestricted.

- **N-084** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  The follow-up flag does not sync. `is_flagged` has no fragment bit, flag
  toggles record no change, and sync apply keeps the local value, so a note
  flagged on one device is unflagged on the others. Changing that touches
  the wire format (and the mobile app), so it needs a decision, not a patch.

- **N-085** · raised `2026-1118-1516-reliability-perf-usability-review` · value medium
  Backup follow-ups (N-055 landed the mechanism). The web UI has no "Back up
  now" button or "last backup" line in the ⚙ panel, and the TUI shows
  nothing; a failed scheduled backup is only a log line. There is no
  `gonotes restore`: restoring is a documented file copy. Restoring a sync
  spoke is untested: the backup carries the spoke's sync bookkeeping from
  that moment while the hub remembers what it delivered since, so check
  whether a restored spoke converges (and what it needs if not) before
  anyone relies on it. Sets also sit on the same disk by default.

- **N-086** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  Spoke setup round trip, what N-057 left: the exported `hub_url` is built
  from the admin's `Host` header, so exporting while browsing the hub as
  `localhost` yields a config that points the spoke at itself. Accept an
  explicit `hub_url` in the export request and warn on a loopback host.

- **N-087** · raised `2026-1118-1516-reliability-perf-usability-review` · value low
  Small sync items: auto-mode backoff is measured from the last success, so
  it never delays anything and a down hub is probed more often than a
  healthy one; the consistency check hashes GUID sets only, so content
  divergence is invisible to it. (The empty-body 401 retry that was listed
  here is fixed; see N-060.)

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

- **N-061** · raised `2026-1118-1516-reliability-perf-usability-review` · closed
  2026-11-18, `2026-1118-1516-reliability-perf-usability-review` — TUI saves that half-succeeded did quiet
  damage. A save writes the note, then reconciles its categories, and the
  form treated a failure of the second step as a failure of the whole.
  - **Duplicates.** A new note was created, the category step failed, the
    form kept no id, and every retry created another note. On an existing
    note the retry named the pre-save version and was refused as stale
    against the user's own save. Done: `adoptSavedNote` (`tui/form.go`)
    makes the form an edit of the note that was written, with only the
    Categories field still counted as unsaved; the status says the note was
    saved and its categories were not.
  - **Stripped categories.** A failed category prefill left the field empty
    with no message, and the save replaced the note's links with that empty
    field. Done: the form tracks the prefill (`catsPrefillState`). While it
    is loading a save waits and says so; if it failed, the status says so
    and saves run in `categoriesAddOnly` mode (`tui/commands.go`), which
    keeps every stored link and adds or re-selects what is typed.
  - Also: the prefill reply carries its note id and a form ignores another
    note's; text typed before the reply arrives is kept after the stored
    set, not overwritten.
  `tui/form_partial_save_test.go`, six tests; with the two fixes disabled
  three fail, one of them with "categories after the save = []".

- **N-060** · raised `2026-1118-1516-reliability-perf-usability-review` · closed
  2026-11-18, `2026-1118-1516-reliability-perf-usability-review` — Push sent one batch of 100 per cycle. Done:
  `pushChanges` loops until the unsent stream is empty (`pushBatchSize` 100,
  bounded by `maxPushBatches` and by a no-progress check that errors if a
  batch starts with the change the previous one started with). Each batch
  is `pushBatch`. Found and fixed on the way, since a longer loop makes it
  likelier: `doAuthenticatedRequest` retried after a 401 with the request
  body already read to its end, so a push retried after a token expiry
  reached the hub with no changes in it, was answered success, and the
  batch was then marked delivered; the body is now rewound first. Hub
  rejections are recorded on the client and reported (see N-059, which
  stays open for the retry policy). `models/sync_client_push_test.go`: 237
  notes go out as 100/100/37 with none repeated and none left pending;
  rejections reach `GetStatus` and clear after a clean push; the retried
  push carries its changes (fails with the rewind disabled). `sync.js?v=5`.

- **N-058** · raised `2026-1118-1516-reliability-perf-usability-review` · closed
  2026-11-18, `2026-1118-1516-reliability-perf-usability-review` — At window widths of 1024px and below, the
  preview and edit pane was off-screen and nothing opened it. The stylesheet
  parks `.right-panel` past the right edge there and only `.right-panel.open`
  brings it back, and no code ever set `open`: in a half-screen window,
  selecting a note, New and Edit all appeared to do nothing. Confirmed by
  measuring in headless Chrome at 1000px and 700px (pane `left` equal to the
  viewport width). Done: `setRightPanelOpen` (`web/static/js/app.js`) opens
  the drawer from `selectNote` and `showEditMode` and closes it from
  `clearPreview` and from cancelling a never-saved note; a back arrow in the
  preview header (`btn-right-panel-close`, shown only at these widths)
  returns to the list; the drawer is `min(100%, max(60%, 560px))` wide.
  `app.css?v=20`. Re-measured at 1400, 1000 and 700px, 19 checks, plus a
  screenshot at 900px. This is the narrow half of N-054's hand check, which
  had covered the filter row and not the pane.

- **N-057** · raised `2026-1118-1516-reliability-perf-usability-review` · closed
  2026-11-18, `2026-1118-1516-reliability-perf-usability-review` — `POST /api/v1/setup/apply` let anyone who
  could reach the port rewrite the server's `.env`. Its only guard was "sync
  is not enabled", which is true of every hub and standalone server. Three
  things were wrong and all are fixed (`web/api/config_import.go`):
  - **Who:** with no accounts (first run) it stays open, the case it exists
    for; once an account exists it requires an admin. `setup.js` now sends
    the signed-in token, so an admin can still apply from `/setup`.
  - **What:** values are refused if they contain a line break, which would
    have added an environment variable of the sender's choosing.
  - **How:** it overwrote the whole file, deleting anything else kept there,
    `GONOTES_ENCRYPTION_KEY` included (the private database would then not
    open at next start). It now merges through `setEnvFileValues`, one
    temp-file-plus-rename for all keys, and creates `config/cfg_files/` on a
    fresh data directory. Values containing `#` are quoted so the loader
    reads them back whole.
  Tests: `web/api/setup_apply_test.go` (unauthenticated and non-admin 403,
  multi-line 400, admin 200, first-run 200) and three in
  `config_env_test.go`, one of which round-trips values through the real
  loader. Left for N-086: `hub_url` taken from the `Host` header.

- **N-056** · raised `2026-1118-1516-reliability-perf-usability-review` · closed
  2026-11-18, `2026-1118-1516-reliability-perf-usability-review` — The web save path could store an empty body
  and could not be guarded against stale writes in msgpack mode. Found by
  review, then traced further: the msgpack library's script URL
  (`unpkg …@latest/dist/msgpack.min.js`) has been a 404 since before the tag
  was added, so the encoded path **never ran in a browser** and both defects
  were latent. Had the URL been corrected, any save of a body past about
  100 KB would have erased it with a success toast (the encoder spread every
  byte as a call argument, threw, and the fallback sent a plain `body` that
  the server ignored under the msgpack header). Done:
  - Server: `MsgPackBodyRequest` accepts a plain `body` when `body_encoded`
    is empty and carries `expected_version`; `MsgPackBodyResponse` carries
    `version` (`models/msgpack.go`). `web/api/msgpack_body_test.go` fails on
    the old code with "stored body is 0 bytes".
  - Client: the encoder converts in 32 KB chunks; `USE_MSGPACK_ENCODING` is
    now `false` and the dead script tag is removed, since base64-of-msgpack
    is larger than the JSON string it replaced.
  - A live defect on the plain path, fixed alongside: flagging a note and
    then saving it from the same tab was refused as stale on every retry
    (the flag bumps the version, the tab kept the old one, and a stale 409
    never refreshed it). `toggleNoteFlag` now takes the version from the
    response, `editNote` re-reads the note once the lease is held, and a
    stale 409 asks: save over the other change (keeping its tags and flag,
    which the form cannot edit) or keep editing. `app.js?v=20`.
  Checked in headless Chrome against a scratch server, 18 checks: a 300 KB
  body saved through the form and read back intact, the form opening on
  text changed behind the tab's back, a mid-edit change producing 409 then
  200 after one prompt, and flag-then-save succeeding first time.

- **N-055** · raised `2026-1118-1516-reliability-perf-usability-review` · closed
  2026-11-18, `2026-1118-1516-reliability-perf-usability-review` — Nothing backed the notes up. Both databases
  were the only copy that did not follow every edit (sync relays mistakes
  faithfully). Done: `models/backup.go` takes a consistent online copy of
  both engines (`Engine.BackupTo`, no pause, private copy stays ciphertext)
  into `<dir>/data/backups/<UTC stamp>/` with a manifest, published by
  directory rename so a set is whole or absent. Unchanged data is not
  backed up again (content hash against the newest set), so rotation's
  slots hold distinct states. Default: every 24h, keep 7, timed from the
  newest set so short daily sessions still add up; `GONOTES_BACKUP_INTERVAL`
  (`off` disables), `GONOTES_BACKUP_KEEP`, `GONOTES_BACKUP_DIR`. Runs in the
  server and in the local-mode TUI. On demand: admin
  `POST /api/v1/admin/backup`, `GET /api/v1/admin/backups`, and
  `gonotes backup` / `gonotes backup list` (the first needs the server
  stopped and says so, naming the endpoint). Tests cover restore-by-opening,
  unchanged skip, rotation (which caught a same-second naming bug where a
  new set could be pruned by its own pass), stale staging sweep, the
  schedule, the encrypted copy, and the API's admin gate. Run end to end on
  a scratch server. Raised N-085.

- **N-053** · raised `2026-1013-1525-ui-reorder-filter-bar-sync-panel` · closed
  2026-10-13, `2026-1013-1922-n053-regex-placeholder` — Regex placeholder left
  behind by Clear/Reset. Done: one helper,
  `syncRegexControls` (`web/static/js/app.js`), sets both the button's
  `active` class and the search placeholder from `state.filters.regex`, and
  `toggleRegex`, `clearSearchBar` and `clearAllFilters` all call it instead of
  touching the controls themselves. `app.js?v=18`. Checked in headless Chrome
  against a scratch server: regex on, then Clear or Reset, gives back
  "Search by text or ID..." and an inactive button.

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
