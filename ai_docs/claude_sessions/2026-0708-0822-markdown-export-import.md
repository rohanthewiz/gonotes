# Session: Markdown Export/Import (Obsidian-compatible) + Test Suite Fixes

**Session ID:** `04f4c064-dc69-44a3-85a0-f32d763c2f6a`
**Date:** 2026-07-08
**Branch:** master

## Context / Decision

Started as a design discussion: should GoNotes get a file-based mode like Obsidian?
Three options were weighed:

1. **Export/import commands** (one-way snapshots, on demand)
2. **Vault mirror** (DB stays source of truth, fsnotify watcher folds external edits back in)
3. **Files as source of truth** (DB becomes a rebuildable index — inverts the architecture)

**Decision:** Option 1 only. The user explicitly does *not* want to change the storage
layer, and wants private notes to stay encrypted at rest — leaving encrypted storage
should only happen through a deliberate export. Key architectural reasons noted:

- The models package is directly SQL-coupled (no storage interface to swap).
- Sync is built on field-level change tracking (bitmask delta fragments, body diffs,
  `authored_at` LWW) — file edits entering through `UpdateNote` keep sync working free.
- Private-note encryption fights plaintext files.
- DuckDB is single-writer.

## What Was Built

### New CLI commands (pattern mirrors `import-gob`)

- **`gonotes export-md -o <dir> -u <user> [--skip-private]`**
  One file per note: `<Category>/<Title>.md` (uncategorized → vault root). YAML
  frontmatter carries `guid`, `title`, `description`, `tags`, `categories`
  (`Name` or `Name/Subcategory`, one entry per selected subcategory), `private`,
  `flagged`, `created`, `updated` (RFC3339). Private notes export **decrypted by
  default** (that is the point of exporting) but keep `private: true` so re-import
  restores privacy/encryption; `--skip-private` excludes them. Bodies come from the
  in-memory cache (already plaintext). Filename collisions get a `-<guid8>` suffix,
  tracked case-insensitively for APFS/NTFS.

- **`gonotes import-md -i <dir> -u <user>`**
  Walks recursively for `.md`, skipping hidden dirs (`.obsidian`). GUID frontmatter
  anchors identity:
  - guid matches → update **only if content differs** (via `UpdateNote`, so
    encryption + sync change tracking keep working); ownership checked first.
  - guid unknown → create, preserving frontmatter timestamps via
    `CreateNoteWithTimestamps` (private notes take `CreateNote` instead since the
    timestamps variant skips encryption).
  - no frontmatter (plain Markdown) → create; title from filename, category from
    first folder segment; generated guid **written back into the file** so
    re-imports are idempotent.
  - Frontmatter categories are authoritative; missing ones created on the fly.
    Categories are only added, never unlinked.

### Files

- `md_format.go` — frontmatter struct, tolerant parser (accepts scalar or list for
  tags/categories via `stringList` custom unmarshaler; plain files = all body;
  CRLF normalized; body trailing-newline symmetric between render/parse),
  `sanitizeFileName`, `parseCategorySpecs`, `normalizeTags`, `parseMdTime`
  (RFC3339 + lax hand-typed formats).
- `md_export.go` — `runExportMd` / `exportNotesMd`. NOTE: `--out`/`--in` are
  resolved to absolute paths *before* chdir into the working dir.
- `md_import.go` — `runImportMd`, `importMdFiles`, `ensureNoteCategories`,
  `noteMatchesInput` (body compared with trailing newlines trimmed, tags normalized).
- `md_roundtrip_test.go` — roundtrip (export → all-unchanged re-import → edited-file
  update), plain-file import with guid writeback + folder-as-category, skip-private.
- `main.go` — command wiring. New dep: `gopkg.in/yaml.v3` (hand-rolled YAML would
  break when Obsidian rewrites property blocks).
- `README.md` — new "Markdown Export / Import" section.

### Wire format example

```markdown
---
guid: seed-001
title: 'Groceries: weekly'
tags:
    - errand
    - home
categories:
    - Household
created: "2026-07-08T13:11:33Z"
updated: "2026-07-08T13:11:33Z"
---

Remember the milk.
```

## Test Suite Fixes (pre-existing failures on clean master)

1. **`models.InitTestDB` missing parent dir** — DuckDB creates the file but not the
   directory; `web/api` tests open `./data/test_*.ddb` and failed on machines
   without a stray `web/api/data/`. Added the same `os.MkdirAll(filepath.Dir(path))`
   guard `InitDB` already had (`models/db.go`).
2. **Stale hardcoded port in test harness** — `newTestServer` (notes_test.go)
   started the server on `web.WebPort` (now `8444` since the PORT-env commit) but
   aimed requests at hardcoded `http://localhost:8000`. Switched to the
   `web.NewTestServer` dynamic-port + `ReadyChan` pattern `sync_test.go` already
   used, with base URL from `srv.GetListenPort()`. Also removed the flaky 100ms sleep.

## Verification

- Unit tests: `go test ./...` all green (`gonotes`, `models`, `web/api`).
- Real end-to-end in scratch dir: seeded DB (user + notes + category) via a small
  seed program → `export-md` (frontmatter/quoting/folders correct, private note
  decrypted with `private: true`) → sed-edited a body + dropped a new plain file +
  `.obsidian/junk.md` → `import-md` reported `1 created, 1 updated, 1 unchanged`
  → second import all-unchanged → `--skip-private` export excluded the private note
  → re-export confirmed the edit persisted in the DB.

## Gotchas / Notes for future work

- Category names containing `/` are unsupported in the `Name/Sub` notation.
- Import never unlinks categories removed from a file.
- Body trailing-newline normalization: parse trims exactly one; render adds one.
  A body genuinely ending in `\n` loses it across one roundtrip (accepted).
- Soft-deleted note with matching guid: create path errors on the unique guid
  constraint → counted as errored (honest, not silent).
- If a **vault mirror** mode is ever wanted, the frontmatter format and GUID
  anchoring here carry over unchanged; the watcher would feed `UpdateNote`.
- Server must be stopped for both commands (DuckDB exclusive lock).

## State at session end

All changes in working tree, **uncommitted** (md feature + test fixes; suggested
as two commits). `web/api` lint hints (`interface{}` → `any`) are pre-existing style,
untouched.
