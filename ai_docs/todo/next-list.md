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

**Next ID:** N-048

## Open

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

- **N-007** · raised `2026-0812-1306-monaco-editor-option` · value low
  The Monaco surface has never been exercised end to end in a browser: typing,
  toggling mid-edit, and image paste inside Monaco. The CDP harness (see
  memory) makes this cheap now.

- **N-012** · raised `2026-0817-1046-tui-subcategory-support` · value low
  Toggling several subcategories means AND ("all of them"), in both UIs.
  There's no OR, and no models function for one.

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

- **N-021** · raised `2026-0818-1739-duplicate-note-dialog` · value low
  Whether a note's follow-up flag should carry over to a duplicate is left to
  a row in the dialog. If nobody ever ticks that row, changing its default is
  a one-line change. This is a wait-and-see item and a candidate for
  Non-goals.

- **N-022** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · value low
  Compaction is all-or-nothing per peer: there's no "compact just this note".
  The dry-run half is done (`models.PreviewCompaction`,
  `GET /api/v1/sync/control/compact`, shown in the web banner's compact-button
  tooltips, in `2026-0923-1133-next-list-batches`), so this item is now only
  the per-note part.

- **N-023** · raised `2026-0818-1824-sync-prompt-mode-and-compaction` · value low
  `GetUnsentChangesForPeer` returns operation 9 (relay) rows. First written
  down as a missing operation filter. `2026-0818-1859` reframed it: with
  relays that behaviour is correct, but a spoke's push batch can carry relays
  the hub then skips by GUID, wasting one entry per batch. Filter per
  direction only if it shows up in a profile.

- **N-025** · raised `2026-0818-1934-spoke-user-guid-alignment` · value low
  A local account named differently from `GONOTES_SYNC_USERNAME` is diagnosed
  but can't be fixed in the app. There's no merge or rename affordance; the
  log's advice is to re-register. A small admin endpoint or CLI subcommand
  would close it.

- **N-026** · raised `2026-0818-1934-spoke-user-guid-alignment` · value low
  Spokes synced to more than one hub are untested. `hubIdentityForUsername`
  takes the first `sync_state` row that matches. The design assumes one hub.

- **N-028** · raised `2026-0819-1410-web-batch-delete` · value low
  The web UI loads the whole library, with no limit and no "very large
  library" signal. If this ever matters, the answer is real server-side
  search and paging, not a silent cap.

- **N-030** · raised `2026-0819-1410-web-batch-delete` · value low
  It was never confirmed that the batch-delete fix addressed the user's actual
  bug; the lock conflict was reproduced synthetically. If deleted notes
  *reappear* later, that is the sync path and needs its own investigation.
  Candidate for Closed if the symptom hasn't come back.

- **N-034** · raised `2026-0909-1858-advanced-sql-search` · value low
  The web advanced-search query bar isn't wired to the note-link autocomplete
  or to saved/named queries. Query history is per browser (`localStorage`).

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
- **N-008** · declined `2026-0923-1133-next-list-batches` — Mouse support in the TUI agent picker and
  confirm dialog. `tui/mouse.go` already records this as deliberate: they are a
  few rows in a `lipgloss.Place`d box, so hit-testing means repeating the
  centering math, and a two-choice dialog doesn't earn that. Raised in
  `2026-0817-0015-tui-mouse-filter-and-store-identity`.

## Closed

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
- **N-006** · raised `2026-0812-1306-monaco-editor-option` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  `scripts/download_vendor.sh` was stale. Done: deleted. It wrote to a directory that no longer exists, msgpack loads from the CDN, and `vendor_monaco.sh` covers Monaco.
- **N-017** · raised `2026-0817-1420-gonotes-note-locks` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  `ReleaseNoteLocksForSession` had no HTTP door. Done: `DELETE /api/v1/note-locks?session_id=…` (user-scoped via `models.ReleaseNoteLocksForUserSession`, since a session id is not secret). The TUI HTTP store uses it on shutdown and falls back to per-note releases on an older server.
- **N-033** · raised `2026-0827-1640-bytdb-v0.11-bump-notnull-version` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  `gofmt -l` flagged files. Done: the whole tree is gofmt-clean. Two comments were reworded first so gofmt would not turn `''` into a typographic quote or indent a line into a code block.
- **N-044** · raised `2026-0923-1124-medium-next-items` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The spoke compactor placed a compacted category at its LAST timestamp. Done: `compactCategoryGroup` now uses the FIRST (a net delete keeps the last), matching the hub. `TestCompactKeepsARenamedCategoryAheadOfItsNotes` fails with the old placement.
- **N-042** · raised `2026-0923-1043-web-comma-categories-and-next-list-seed` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The web category `<datalist>` stopped suggesting after a comma. Done: `refreshCategorySuggestions` rebuilds the options on each keystroke with the typed prefix in front (`Work, Pe` → `Work, Personal`), and after a `/` it offers the category's subcategories. The option lists were checked in Chrome; whether the native popup shows them was not, because the popup never opened under automation.
- **N-043** · raised `2026-0923-1043-web-comma-categories-and-next-list-seed` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The web category input ignored `/` notation. Done: `parseCategorySpec` reads `Work/backend` as the TUI does. A defined subcategory matches case-insensitively and an unknown one is created. An existing category whose whole name contains `/` still matches literally. The batch dialog refuses `/` rather than drop it.
- **N-045** · raised `2026-0923-1124-medium-next-items` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The web batch bar couldn't remove a category. Done: a separate **Remove Category** action. Unknown names are refused, only linked pairs get a DELETE, and a 404 for an already-gone link counts as success. While building it, found and fixed a gap: `DELETE` and `PUT /api/v1/notes/:id/categories/:cid` never checked note ownership, so any signed-in user could unlink or rewrite categories on another user's note by id. `TestRemoveCategoryFromAnotherUsersNote` covers it.
- **N-018** · raised `2026-0818-1739-duplicate-note-dialog` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The web duplicate dialog had no keyboard navigation beyond Enter. Done: ↑/↓ move between the title and the checkbox rows, space toggles, Enter confirms from any row, and the focused row is outlined (`:focus-visible`). Checked in Chrome with real key presses.
- **N-031** · raised `2026-0821-1626-summarize-ui-web-tui` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  Web summaries were untagged. Done: the clipboard door tags the new note `summary`, as the TUI does (the body door doesn't, in either UI). Found while doing it: the web form sent `tags: null` on every save and `UpdateNote` writes every column, so **editing a note in the web UI erased its tags**. `state.editTags` now carries a note's tags through the edit. Checked in Chrome: a tagged note keeps `infra,api` after a web edit.
- **N-032** · raised `2026-0826-1559-summarize-progress-indication` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The icon-only summarize button was dimmed but not animated. Done: `busy()` sets `aria-busy` and `app.css` pulses the icon. The animation is off under `prefers-reduced-motion`.
- **N-046** · raised `2026-0923-1124-medium-next-items` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The web note list had no "being edited elsewhere" badge. Done: `refreshNoteLocks` polls `GET /note-locks` every 30s while the tab is visible (and on return) and re-renders only on a change. It skips this tab's own lease, and the tooltip names the holder. Checked in Chrome against a curl-held lease.
- **N-010** · raised `2026-0817-0015-tui-mouse-filter-and-store-identity` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The TUI never said *why* an HTTP request failed mid-session. In fact it did, but as Go's raw transport error (`Put "http://…": dial tcp …: connection refused`), and the one-line status bar truncated it before the cause. Done: `errReason` (`tui/errreason.go`) names refused, timed out, unknown host, dropped connection, 5xx and 401 in a few words, and `statusErr` uses it. Tested against real failures from the HTTP store.
- **N-011** · raised `2026-0817-1046-tui-subcategory-support` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  `ctrl+g` capture filed notes with no category. Its form always had an editable Categories field; what was missing was a default. Done: the field is preset to the list's category filter (`Work/backend`), and left empty when the list is unfiltered or a query is in force. A preset alone doesn't make the form dirty.
- **N-019** · raised `2026-0818-1739-duplicate-note-dialog` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The TUI duplicate dialog showed nothing while the copy was made. Done: `confirm` sequences pop → "Duplicating…" → the create, and the result's status replaces it.
- **N-001** · raised `2026-0710-1656-tui-implementation` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  Category and subcategory rename in the TUI. Done: `r` on both screens. A category rename is the whole-object update and refuses a name another category has (case-insensitive). A subcategory rename goes through the new `models.RenameSubcategory` (links first, then the definition, safe to re-run, merges onto an existing name) and `POST /api/v1/categories/:id/subcategories/rename`, so notes filed under the old name follow it. The web UI has no subcategory rename yet.
- **N-013** · raised `2026-0817-1046-tui-subcategory-support` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
  The TUI subcategory screen showed no note counts. Done: one `SubcategoryNoteCounts` read per screen (the bulk note-category mappings, counted client-side, not a query per row), reloaded after a rename. Rows show nothing until the counts load.
- **N-047** · raised `2026-0923-1133-next-list-batches` · closed 2026-09-23, `2026-0923-1133-next-list-batches` —
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
