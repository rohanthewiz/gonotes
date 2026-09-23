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
- Nothing leaves Open or Roadmap without a line in another section (Closed,
  Non-goals, or a move between Open and Roadmap). Deleting a line outright is
  the leak this file exists to prevent.
- Open and Roadmap stay in ID order. Edit an item's text in place when its
  premise changes; keep its ID and `raised`.

**Next ID:** N-044

## Open

- **N-001** · raised `2026-0710-1656-tui-implementation` · value low
  Category and subcategory **rename** in the TUI. `tui/categories.go:15` still
  says renames stay in the web UI; the subcategory screen only adds and
  removes. A subcategory rename would need a models-level operation that
  rewrites every note's selection, which doesn't exist yet. Re-raised in
  `2026-0817-1046-tui-subcategory-support` (subcategory support itself landed
  there).

- **N-002** · raised `2026-0722-1558-migrate-duckdb-to-bytdb` · value low
  Sequential fan-outs remain in low-frequency paths: `GetSyncStatus` counts,
  `computeSyncChecksum`, `changeGUIDExists` (`models/sync_protocol.go`). They
  could be parallelized, but none of them is hot.

- **N-003** · raised `2026-0722-1558-migrate-duckdb-to-bytdb` · value low
  The DuckDB→bytdb migration skips the sync change log. Revisit only if a
  migrated spoke must push its pre-existing notes to a hub without a full
  snapshot reconcile.

- **N-004** · raised `2026-0812-1306-monaco-editor-option` · value low
  Vendored Monaco (13MB, 105 files under `web/static/vendor/monaco/`) is
  committed to git. If repo size becomes a concern, gitignore it and run
  `scripts/vendor_monaco.sh` as a build step.

- **N-005** · raised `2026-0812-1306-monaco-editor-option` · value low
  Binary size: `vs/language/typescript` (5.5MB) and non-English
  `nls.messages.*` (~1.7MB) could be dropped from the vendor dir if "full"
  mode is ever relaxed.

- **N-006** · raised `2026-0812-1306-monaco-editor-option` · value low
  `scripts/download_vendor.sh` is stale. Still true: it targets
  `server/static/vendor` and says Monaco needs a manual download.
  `vendor_monaco.sh` replaced it for Monaco. Fix it or delete it.

- **N-007** · raised `2026-0812-1306-monaco-editor-option` · value low
  The Monaco surface has never been exercised end to end in a browser: typing,
  toggling mid-edit, and image paste inside Monaco. The CDP harness (see
  memory) makes this cheap now.

- **N-008** · raised `2026-0817-0015-tui-mouse-filter-and-store-identity` · value low
  The TUI agent picker and confirm dialog remain keyboard-only.

- **N-009** · raised `2026-0817-0015-tui-mouse-filter-and-store-identity` · value medium
  An explicit `--local` / `--remote` flag pair for the TUI. Today the
  workaround is pointing `GONOTES_URL` at a dead port, and scripted or test
  TUI runs have to remember it or they edit the live server's notes. Because
  that workaround is in active use, this is rated medium rather than low.

- **N-010** · raised `2026-0817-0015-tui-mouse-filter-and-store-identity` · value low
  The TUI never shows *why* an HTTP request failed mid-session. Only the
  startup local/remote decision is labelled.

- **N-011** · raised `2026-0817-1046-tui-subcategory-support` · value low
  `ctrl+g` pane capture still files notes with no category. It could take a
  category spec (`Work/backend`), as the note form does.

- **N-012** · raised `2026-0817-1046-tui-subcategory-support` · value low
  Toggling several subcategories means AND ("all of them"), in both UIs.
  There's no OR, and no models function for one.

- **N-013** · raised `2026-0817-1046-tui-subcategory-support` · value low
  The TUI subcategory screen shows no note counts per row (each would cost a
  query).

- **N-014** · raised `2026-0817-1420-gonotes-note-locks` · value medium
  The web UI respects note leases but doesn't take them. It sends
  `expected_version` and shows refusals, but it never acquires a lease or
  sends heartbeats (no lock calls in `web/static/js/`). A TUI can block a
  browser tab but not the reverse, and two tabs fall back to the version
  guard alone.

- **N-015** · raised `2026-0817-1420-gonotes-note-locks` · value low
  cats-mobile parity. The same app came up three times: unaware of note locks
  and version guards (`2026-0817-1420`), no duplicate action
  (`2026-0818-1739`), and no sync affordance (`2026-0818-1824`, `-1859`,
  `-1934`). Premise unverified: the mobile app has since been rebuilt as a
  sync spoke (branch `roh/sync-spoke-rebuild`), so check that repo before
  acting.

- **N-016** · raised `2026-0817-1420-gonotes-note-locks` · value low
  The category and subcategory screens have no lock gate. Nothing is at risk
  today because they don't edit notes, but any bulk note operation added
  there later would need one. Contingent on such an operation existing.

- **N-017** · raised `2026-0817-1420-gonotes-note-locks` · value low
  `ReleaseNoteLocksForSession` (`models/lock.go:380`) has no HTTP door. The
  HTTP store releases by iterating its own token map instead, which has the
  same effect with more requests.

- **N-018** · raised `2026-0818-1739-duplicate-note-dialog` · value low
  The web duplicate dialog can't be navigated by keyboard beyond Enter: no
  ↑/↓, and no focus ring beyond the browser default.

- **N-019** · raised `2026-0818-1739-duplicate-note-dialog` · value low
  The TUI duplicate dialog has no "duplicating…" state. Against a slow remote
  hub, nothing shows until the status line arrives.

- **N-020** · raised `2026-0818-1739-duplicate-note-dialog` · value medium
  Categories are attached to a note one request at a time: a POST per link in
  both UIs (`web/static/js/cats_subcats.js` `saveCategoryAssignments`), so ten
  categories means ten round trips. An `AddCategoriesToNote` bulk endpoint
  would fix both UIs at once. It matters more now that the web category input
  accepts a comma-separated list (2026-09-23).

- **N-021** · raised `2026-0818-1739-duplicate-note-dialog` · value low
  Whether a note's follow-up flag should carry over to a duplicate is left to
  a row in the dialog. If nobody ever ticks that row, changing its default is
  a one-line change. This is a wait-and-see item and a candidate for
  Non-goals.

- **N-022** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · value low
  Compaction is all-or-nothing per peer: there's no "compact just this note"
  and no dry run that reports what *would* collapse.

- **N-023** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · value low
  `GetUnsentChangesForPeer` returns operation 9 (relay) rows. First written
  down as a missing operation filter. `2026-0818-1859` reframed it: with
  relays that behaviour is correct, but a spoke's push batch can carry relays
  the hub then skips by GUID, wasting one entry per batch. Filter per
  direction only if it shows up in a profile.

- **N-024** · raised `2026-0818-1859-sync-relay-convergence-and-followups` · value medium
  The hub's own change log is never compacted. Compaction runs on the spoke
  (`SyncClient.Compact`, `models/sync_compact.go`) and skips operation 9,
  which is almost everything a hub holds, so a long-lived hub's log only
  grows.

- **N-025** · raised `2026-0818-1934-spoke-user-guid-alignment` · value low
  A local account named differently from `GONOTES_SYNC_USERNAME` is diagnosed
  but can't be fixed in the app. There's no merge or rename affordance; the
  log's advice is to re-register. A small admin endpoint or CLI subcommand
  would close it.

- **N-026** · raised `2026-0818-1934-spoke-user-guid-alignment` · value low
  Spokes synced to more than one hub are untested. `hubIdentityForUsername`
  takes the first `sync_state` row that matches. The design assumes one hub.

- **N-027** · raised `2026-0819-1410-web-batch-delete` · value medium
  `apiRequest` shows a toast on every failure (`web/static/js/app.js`, its
  `catch` block). A batch where fifty notes are locked stacks fifty toasts
  plus the summary, which already makes them redundant.

- **N-028** · raised `2026-0819-1410-web-batch-delete` · value low
  The web UI loads the whole library, with no limit and no "very large
  library" signal. If this ever matters, the answer is real server-side
  search and paging, not a silent cap.

- **N-029** · raised `2026-0819-1410-web-batch-delete` · value medium
  **The premise was wrong; this is worse than it was written.** Raised as
  "the batch bar's `categorySelected` and `togglePrivacySelected` may swallow
  failures like Delete did." In fact neither function exists anywhere in
  `web/static/js/`. The **Set Category** and **Toggle Privacy** buttons
  (`web/pages/landing/note_list.go:14-15`) throw a TypeError on click, and
  have since the first landing-page commit (`e1540fd`). Implement them or
  remove the buttons.

- **N-030** · raised `2026-0819-1410-web-batch-delete` · value low
  It was never confirmed that the batch-delete fix addressed the user's actual
  bug; the lock conflict was reproduced synthetically. If deleted notes
  *reappear* later, that is the sync path and needs its own investigation.
  Candidate for Closed if the symptom hasn't come back.

- **N-031** · raised `2026-0821-1626-summarize-ui-web-tui` · value low
  Summaries made from the web UI are untagged. The web note form has no tags
  field (`preview_panel.go:103`: "tags removed"), and its save sends
  `tags: null` (`app.js:497`), so only the TUI's clipboard path applies the
  `summary` tag.

- **N-032** · raised `2026-0826-1559-summarize-progress-indication` · value low
  The icon-only summarize toolbar button is dimmed and inert while it works,
  but not animated. A spinner or an `aria-busy` pulse was the nicer option
  left undone (option 3 in that doc).

- **N-033** · raised `2026-0827-1640-bytdb-v0.11-bump-notnull-version` · value low
  `gofmt -l models/` flags `category.go` and `store.go` (still true). The
  formatting problem predates that session.

- **N-034** · raised `2026-0909-1858-advanced-sql-search` · value low
  The web advanced-search query bar isn't wired to the note-link autocomplete
  or to saved/named queries. Query history is per browser (`localStorage`).

- **N-042** · raised `2026-0923-1043-web-comma-categories-and-next-list-seed` · value low
  The web category input's `<datalist>` autocomplete matches the whole input
  value, so once a comma is typed it stops suggesting names for the segment
  after it. A custom autocomplete keyed on the last comma segment would fix it.

- **N-043** · raised `2026-0923-1043-web-comma-categories-and-next-list-seed` · value low
  The web category input splits on commas but does not read the `/`
  subcategory notation (`Work/backend`) that the TUI field and
  `models.ParseCategorySpecCSV` accept, so the two single-line inputs speak
  slightly different dialects. Web names containing `/` are still allowed.

## Roadmap

Wanted, but deliberately not next. Parked, not declined. Seeded empty: no
session doc marked an item as deferred, so move items here from Open by hand.

## Non-goals

- **N-035** · declined `2026-0817-1420-gonotes-note-locks` — Per-field leases.
  Leases are per note, so two people can't edit one note's body and tags at
  the same time. That is intended. If a finer grain were ever wanted, it
  would go in the version guard's fragment machinery (`FragmentBody` etc.).
- **N-036** · declined `2026-0909-1858-advanced-sql-search` — A `gonotes
  query "…"` CLI subcommand. It would have to open the databases directly, so
  it would only work with the server stopped. The TUI's HTTP fallback is the
  better route and already exists.

## Closed

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
