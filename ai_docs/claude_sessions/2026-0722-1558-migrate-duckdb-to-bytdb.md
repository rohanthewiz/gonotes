# Session: Migrate GoNotes from DuckDB to bytdb (two databases, encrypted private)

**Session ID:** `82aaed39-a82f-4520-825c-b4686f43b762`
**Date:** 2026-07-22
**Branch:** migrate-to-bytdb

## Context / Goal

Move GoNotes off DuckDB (disk file + in-memory cache) onto
`github.com/rohanthewiz/bytdb` v0.6.4. Requirements from the user:

1. **Two bytdb databases** — one for **private** notes (encrypted), one for
   **non-private** notes (plaintext).
2. **Queries use parallel goroutines to divide and conquer.**
3. Provide a **migration script** off DuckDB.
4. Move **sync and all other features** over.

## Decisions (confirmed with the user up front)

- **DB layout:** the encrypted DB holds ONLY private notes + their category
  links + their sync change-tracking. The public DB holds public notes + links
  + change-tracking, plus all shared/system tables (categories catalog, users,
  invite_tokens, sync_state, sync_conflicts). Private-note category links
  reference the public catalog by category id (categories are single-DB, so the
  int id is globally meaningful).
- **Encryption:** bytdb whole-DB WAL encryption only (`WithEncryptionKey`,
  reusing the 32-byte `GONOTES_ENCRYPTION_KEY`). Dropped the app-level per-note
  AES and the `encryption_iv` column entirely. Rows are plaintext in RAM,
  ciphertext on disk.
- **Cache:** dropped. bytdb is already RAM-resident; the disk↔cache dual-write
  layer is gone.
- **Migration:** a **standalone module** under `scripts/migrate/` (its own
  go.mod) so cgo go-duckdb never enters the main binary's build.

## bytdb dialect facts that shaped the design (verified against the library)

- No `database/sql` driver — returns `Result.Rows [][]any`, binds `$1` params,
  one writer per engine, reads never block (so parallel fan-out is safe).
- **`DEFAULT nextval(...)` is rejected** ("DEFAULT must be a constant"). IDs are
  drawn via `nextval('<t>_id_seq')` **in the INSERT** instead. `DEFAULT
  CURRENT_TIMESTAMP` IS allowed.
- **No `IF NOT EXISTS` on CREATE TABLE/INDEX** (only on CREATE SEQUENCE). Schema
  creation is gated on `engine.Table(name) == nil`.
- **One statement per `Exec`** — the old bundled "SEQUENCE; TABLE" constants are
  split.
- Timestamps come back as `int64` microseconds; `time.Time` args coerce
  natively; NULL → `nil`.
- `||`, `LIKE`/`ILIKE`, `LOWER`, `IN (subquery)`, `RETURNING`, `COUNT` all work.
  `list_contains`/`json_extract_string` (DuckDB-only) do NOT — reimplemented in
  Go.

## Architecture built

### New `models/` foundation
- **`store.go`** — `dbEngine` wraps one bytdb engine behind a `*sql.DB`-shaped
  shim: `Exec/Query/QueryRow`, `?`→`$n` rebind (quote-aware), `driver.Valuer`
  unwrap (handles `sql.Null*` args), `sql.ErrNoRows`, and `scanRow`/`assignValue`
  mapping `[][]any` → the model structs' Go types (incl. `int16`/`int32` for
  bitmasks/operations). **Divide-and-conquer** helpers `queryBothNotes[T]` and
  `firstFromBothNotes[T]` run both engines concurrently (goroutines + WaitGroup)
  and merge.
- **`schema.go`** — full final schema for both DBs. Private DB sequences start at
  `1e12` (`seqOffsetPrivate`) so note/fragment/change ids are globally unique;
  FK constraints dropped (cross-DB impossible; app enforces integrity). Renamed
  the reserved-word `user` column to `change_user`.
- **`db.go`** — `pubDB`/`privDB` engines; `InitDB`/`InitTestDB(dir)`/`CloseDB`;
  routing helpers `noteEngine(isPrivate)`, `engineForID(id)` (routes
  change/fragment reads by id range), `engineForNoteID`/`engineForOwnedNote`.
- **`migrate.go`** — value-preserving `MigrateInsert{User,Category,Note,NoteCategory}`
  helpers for the migration tool (fresh ids from sequences, remapped FKs,
  preserved GUIDs/timestamps/ownership, no change-tracking).

### Rewritten model files
- `note.go` — routes by privacy; fan-out reads (`GetNoteByID/GUID`, `ListNotes`,
  `SearchNotesByTitle`); **privacy flip = cross-DB move preserving the id**
  (`moveNoteBetweenEngines` + `moveNoteCategories`); dropped encryption.
- `category.go` — catalog is public-only; note⇄category JOINs resolved in Go
  (links read from the note's DB, categories fetched from the public catalog);
  `notesForCategoryID` fans out; `GetNotesByCategoryAndSubcategories` filters
  subcategories in Go (replacing the DuckDB JSON query).
- `note_change.go` / `category_change.go` — engine-aware inserts; note change
  reads fan out + merge; category change-tracking stays public-only;
  `recordNoteCategoryMappingChange` writes the note change into the note's DB and
  resolves category GUIDs from the public catalog.
- `sync_apply.go` — routes applied notes by privacy incl. **sync-driven privacy
  flips as cross-DB moves**; categories → public; no cache/encryption.
- `sync_protocol.go` / `sync_conflict.go` / `sync_client.go` — counts, checksums,
  `changeGUIDExists`, unsent-change reads, pending-change reads all fan out
  across both note DBs; `getNoteByGUIDFromDisk` is now a fan-out read.
- `user.go` / `user_list.go` / `invite_token.go` — public-only; `MigrateOrphanedNotes`
  updates both note DBs.

### Migration tool — `scripts/migrate/` (own module)
`main.go` reads legacy users/categories/notes/note_categories from DuckDB,
decrypts legacy private bodies with `GONOTES_ENCRYPTION_KEY`, writes both bytdb
files via the migrate helpers, remapping ids. Transient sync bookkeeping
(change-log, sync_state, conflicts, invites) is intentionally NOT carried over —
a migrated node reconciles via the normal checksum/snapshot sync path.
```
GONOTES_ENCRYPTION_KEY=... go run ./scripts/migrate -src ./data/notes.ddb -dest ./data
```

## Dependency chain (important gotcha)

bytdb requires **Go 1.26.1** (go.mod bumped; toolchain auto-downloaded). bytdb
pulls `serr` v1.4.0, whose `*serr.SErr` return change broke every rohanthewiz
package built against older serr. Bumped to: `logger` v1.3.0, `rutil` v0.2.0,
`rweb` v0.1.26, `element` v0.6.0. `GOPRIVATE=github.com/rohanthewiz/*` persisted
via `go env -w` (bytdb isn't in the public checksum DB). `go mod tidy` dropped
go-duckdb + Arrow from the main module.

## Verification (all green)

- `go build ./...` (readonly mode) + `go vet ./...` clean; full test suite passes
  (models, web/api sync/notes/categories, note-change, sync-protocol,
  import/export).
- Obsolete cache tests archived to `arch_test_scripts/*.txt`; `encryption_test.go`
  rewritten for the whole-DB model (at-rest ciphertext check, key-required-on-
  reopen, privacy-flip move).
- `scripts/migrate` round-trip test passes: user/category/2 notes/2 links
  migrated, private body decrypted, private note in offset id range, plaintext
  absent from disk.
- **Live binary smoke test:** server boots, both DBs init (`private_encrypted=true`),
  public note body lands plaintext in `notes_public.bytdb`, private note body is
  absent there and NOT plaintext in `notes_private.bytdb`.

## Files touched

- New: `models/store.go`, `models/schema.go`, `models/migrate.go`,
  `scripts/migrate/{go.mod,go.sum,main.go,migrate_test.go}`
- Rewritten: `models/{db,note,note_change,category,category_change,sync_apply,
  sync_conflict,sync_protocol,user,user_list,invite_token,sync_client}.go`,
  `models/encryption_test.go`
- Edited: test setups (`InitTestDB(t.TempDir())`), `go.mod`/`go.sum`, `README.md`
  (new Storage section), `.gitignore` (`*.bytdb`)
- Archived: `models/{cache,category_cache}_test.go` → `arch_test_scripts/*.txt`
- Unchanged behavior: `encryption.go` retains `Encrypt`/`Decrypt` (now used only
  by the migration tool to decrypt legacy bodies).

## Follow-ups / open items

- Sequential (not parallel) fan-outs remain in a few low-frequency paths
  (`GetSyncStatus` counts, `computeSyncChecksum`, `changeGUIDExists`) — could be
  parallelized but aren't hot.
- Migration deliberately skips the sync change-log; revisit if a migrated spoke
  must push its pre-existing notes to a hub without a full snapshot reconcile.
