# Session: Drop Monaco's Unused Locale Bundles (N-005)

**Session ID:** `4d84aea5-2d3c-4540-88ce-aaff8d3930a6`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `d0fdc0c` — Close N-007: make image paste work inside Monaco

Same session as the N-049, N-026, N-023, N-022, N-015 and N-007 docs earlier
today (latest:
[2026-1004-2329-n007-monaco-e2e-paste-fix](2026-1004-2329-n007-monaco-e2e-paste-fix.md)).

Picks up N-005 from `ai_docs/todo/next-list.md`, first raised in
[2026-0812-1306-monaco-editor-option](2026-0812-1306-monaco-editor-option.md):

> Binary size: `vs/language/typescript` (5.5MB) and non-English
> `nls.messages.*` (~1.7MB) could be dropped from the vendor dir if "full"
> mode is ever relaxed.

---

## The two halves were not equally tied to "full" mode

Measured against the vendored monaco-editor 0.52.2 (`web/static/vendor/monaco`,
14 MB at the start):

| Part | Size | What it is |
|---|---|---|
| `vs/language/typescript/` | 5.5 MB | `monaco.languages.typescript` plus the TS/JS language-service workers |
| `vs/nls.messages.{de,es,fr,it,ja,ko,ru,zh-cn,zh-tw}.js` | 1.8 MB | UI string translations |

In `vs/editor/editor.main.js`, the `vs/nls.messages-loader` plugin does this:

```js
const requestedLanguage = config['vs/nls']?.availableLanguages?.['*'];
if (!requestedLanguage || requestedLanguage === 'en') { load({}); }
else { req([`vs/nls.messages.${requestedLanguage}`], ...) }
```

`monaco_editor.js` calls `require.config({ paths: { vs: base + '/vs' } })`
and nothing else, so no locale bundle is ever requested. English is built
in. Dropping the bundles therefore removes **no API**. The item's "if full
mode is ever relaxed" condition applies only to the TypeScript half.

The user was offered locales only (recommended), locales plus TypeScript,
or neither. **They chose locales only.** TypeScript stays as part of the
full build originally asked for.

## Changes

- **`web/static/vendor/monaco/vs/nls.messages.*.js`:** all 9 deleted with
  `git rm`. The vendor dir goes from 14 MB to 12 MB.
- **`scripts/vendor_monaco.sh`:** after copying `min/vs`, it runs
  `rm -f "$VENDOR_DIR"/vs/nls.messages.*.js`. The comment explains the
  loader rule and what to do if a locale is ever wanted (keep its file and
  set it in `loadMonaco`'s `require.config`). Without this, the next
  re-vendor, which replaces the dir wholesale, would bring them back.
- **`web/static/js/monaco_editor.js`:** a comment at the
  `require.config` call says no `'vs/nls'` locale is configured and the
  bundles aren't vendored. It's comment-only, so the cache-buster stays at
  `v=3`.

## Verification

- `node --check monaco_editor.js` and `bash -n vendor_monaco.sh` pass, and
  `go vet ./web/...` is clean.
- **Binary:** 48,783,506 → 46,950,562 bytes (−1.83 MB), comparing the same
  scratch build before and after.
- **Browser:** the N-007 headless-Chrome script (`monaco_e2e.mjs`, harness
  per the `cdp-browser-harness` memory) was rerun against the trimmed build
  on a fresh data dir. It has a new check that no request matches
  `nls.messages.`. **ALL PASSED**: vendored load, typing, toggles, image
  paste and drop, note switching, reload, and no console errors. The server
  log shows no 404 for any vendor or Monaco path. A second run on the same
  data stopped partway because the script reuses fixed note GUIDs; that is
  a quirk of the script, not a finding.
- The scratch server and Chrome were stopped by PID, and the ports were
  confirmed free.

## Files

- `web/static/vendor/monaco/vs/nls.messages.*.js` (9 deleted)
- `scripts/vendor_monaco.sh`
- `web/static/js/monaco_editor.js` (comment)
- `ai_docs/todo/next-list.md`: N-005 moved to Closed, with the TypeScript
  half recorded as kept by the user's choice

## Next

Closed: N-005. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
