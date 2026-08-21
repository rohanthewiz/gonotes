# Session: Summarizing the Clipboard on the Way Into a Note

**Session ID:** `c2afb3f5-2e0c-447f-a737-f8106237c3af`
**Date:** 2026-08-21
**Branch:** master
**Base commit:** `725e2c7` — Add session doc for the web batch delete work

## Context

Task, as given: "I would like the ability to send text in the clipboard for
summarization into a gonote."

Two things were genuinely undecided and would have produced different work, so
they were asked rather than guessed:

1. **Where the feature lives** — a flag on the existing `gn-clip.sh` skill
   script, or a first-class Go feature (API endpoint + web/TUI action) calling
   the Anthropic API.
2. **What the note contains** — summary in the description with the original
   preserved as the body, summary replacing the body, or both concatenated.

Answers: the script, and **body = the summary**, with a derived title, plus a
description "only if the title warrants another sentence or two". Plus: improve
the gonotes skill doc as needed.

---

## Survey

No LLM integration existed anywhere in the repo — a grep for
`anthropic|openai|claude|llm|summar` across `*.go`/`*.sh`/`*.toml` hit only
incidental words (`cats/hooks.go`, `tui/capture.go`, test files). So this is a
greenfield seam, not an extension of something.

Environment facts that drove the design:

- `claude` CLI present at `~/.local/bin/claude`.
- `ANTHROPIC_API_KEY` **not set**.

That pair settles the transport: shelling out to the CLI needs no credential
management at all, since the CLI already holds this machine's auth. An HTTP
call to the API would have required inventing a place to keep a key
(`.env`? keychain?) for what is a one-line convenience script.

## The cost discovery

First working invocation — plain `claude -p --model haiku --output-format json`
run from the repo — returned good JSON but reported:

```
cache_creation_input_tokens: 19687     cost: $0.043
```

~20k tokens of prompt for "condense this paragraph". Claude Code discovers
`CLAUDE.md`, skills and MCP servers **from the working directory**, and the
gonotes checkout has all three. Re-run lean:

```
cd <empty temp dir>
claude -p --model haiku --output-format text \
  --no-session-persistence \
  --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
  --allowedTools "" \
  --system-prompt "$SUMMARY_SYSTEM_PROMPT" \
  'Summarize the text on stdin into the JSON object described in the system prompt.'
```

```
cache_creation_input_tokens: 3802      cost: $0.012
```

5x less prompt, same answer quality. The empty cwd is the load-bearing part —
the flags alone do not stop `CLAUDE.md` discovery. This is written into both
the script comment and SKILL.md so nobody "simplifies" the temp dir away later.

## Design decisions

**Strict JSON, not prose.** The model is asked for
`{"title": string, "description": string, "body": string}`. A plain-text answer
would put us back to guessing where a title ends and a summary begins — which
is precisely the heuristic `-s` exists to replace (the non-`-s` path derives a
title by taking the first non-blank line).

**Fences get stripped.** Models fence JSON even when told not to. Dropping
whole lines matching `^\s*```` is safe *because* JSON escapes newlines inside
strings: no line of a valid answer can begin with a fence unless it is the
fence. (A summary body full of code blocks is still fine — it lives inside one
JSON string.)

**Fail loudly, never partially.** Unparseable JSON or an empty body aborts
before any HTTP call and prints what came back. The clipboard is untouched, so
a re-run costs only the model call. Storing a mangled note would be the worse
outcome — it is the thing the user would have to notice later.

**`-t` outranks the generated title.** The flag is the user's own words; `-s`
was only asked to condense the text.

**Description stays rare by construction.** The system prompt says to return
`""` when the title already conveys the gist. In testing it came back empty for
every well-titled sample, which is the requested behavior, not a bug.

**No trap for the temp dir.** Cleanup is explicit on both paths so it cannot
clobber a trap the rest of the script may want.

## Implementation

`.claude/skills/gonotes/scripts/gn-clip.sh`:

- New flags `-s` (summarize) and `-m <model>` (default `haiku`); getopts string
  `":t:ksm:c:g:pfu:U:nh"`.
- New `summarize()` function reading stdin, returning the JSON object.
- The title/body split became a three-way branch:
  `-s` → generated fields; else `-z "$title"` → existing first-line derivation;
  else the paste as-is.
- Payload gained a conditional `description` key
  (`if $desc == "" then {} else {description: $desc} end`), matching how `tags`
  was already handled. `models.NoteInput` already carries `Description *string`,
  so no server change was needed.
- Header comment extended — `usage()` prints that block, so help text cannot
  drift from the flags.

`.claude/skills/gonotes/SKILL.md`: `-s`/`-m` rows in the flag table, an example
line, a note that `-n` still runs the summarizer (so `-s -n` previews), and a
new **"Summarizing on the way in (`-s`)"** section covering the no-API-key
point, what lands in each field, the failure behavior, and the lean-invocation
rationale with the token numbers.

## Verification

Testing needed clipboard content without touching the real clipboard. Rather
than save/restore via `pbcopy` (which would drop non-text flavors), a stub
`pbpaste` was put on `PATH` — `read_clipboard()` probes with `command -v`, so
the stub is picked up cleanly:

```bash
d=$(mktemp -d); printf '#!/bin/sh\ncat "$FAKE_CLIP"\n' > "$d/pbpaste"; chmod +x "$d/pbpaste"
FAKE_CLIP=... PATH="$d:$PATH" bash .claude/skills/gonotes/scripts/gn-clip.sh -s -n
```

| Check | Result |
|---|---|
| `bash -n` | clean |
| Meeting-notes paste, `-s -n -g postmortem` | structured Markdown body, apt title, **no** description, tags carried |
| Short prose paste, `-s -n -c "Ref/db"` | prose summary, no description, category echoed |
| `-s -n -t "My own title"` | flag title kept, generated one discarded |
| `-h` | new flags documented, block renders correctly |

Not run: a live POST. It would write a real note into the user's database, and
the only changed thing on that path is the optional `description` key, whose
acceptance was confirmed by reading `models/note.go:77`.

## Flagged to the user

The raw paste is **not** kept — body is the summary alone, per the explicit
choice. SKILL.md now tells the reader to capture without `-s` first when the
original matters, and the user was offered an append-the-original variant.

## Files touched

- `.claude/skills/gonotes/scripts/gn-clip.sh`
- `.claude/skills/gonotes/SKILL.md`
