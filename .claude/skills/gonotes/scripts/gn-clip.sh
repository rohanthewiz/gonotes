#!/usr/bin/env bash
#
# gn-clip.sh — create a GoNotes note from the system clipboard.
#
# Why this exists as a script rather than an inline curl: pasted text is
# arbitrary (quotes, backslashes, newlines, emoji), so the payload has to be
# assembled by jq rather than string-interpolated into a shell heredoc. On top
# of that, every capture needs a GUID, a JWT, and usually a category — four
# round trips that are tedious to retype.
#
# Requires the GoNotes server to be running (DuckDB is single-writer, so the
# HTTP API is the only safe write path while it is up). With the server down,
# see the import-md recipe in SKILL.md.
#
# Usage: gn-clip.sh [-t title] [-k] [-c cat[/sub]]... [-g tags] [-p] [-f]
#                   [-u user] [-U base_url] [-n]

set -euo pipefail

BASE_URL="${GONOTES_URL:-http://localhost:8444}"
TOKEN_FILE="${GONOTES_TOKEN_FILE:-$HOME/.gonotes/.api_token}"

title=""
keep_first_line=0
tags=""
is_private=false
is_flagged=false
username="${GONOTES_USER:-${GONOTES_SYNC_USERNAME:-}}"
dry_run=0
categories=()

usage() {
	# Print the header comment block (everything after the shebang up to the
	# first non-comment line) as the help text, so the two can't drift apart.
	awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"
	exit "${1:-0}"
}

while getopts ":t:kc:g:pfu:U:nh" opt; do
	case "$opt" in
	t) title="$OPTARG" ;;
	k) keep_first_line=1 ;;
	c) categories+=("$OPTARG") ;;
	g) tags="$OPTARG" ;;
	p) is_private=true ;;
	f) is_flagged=true ;;
	u) username="$OPTARG" ;;
	U) BASE_URL="$OPTARG" ;;
	n) dry_run=1 ;;
	h) usage 0 ;;
	\?) echo "unknown option: -$OPTARG" >&2; usage 1 ;;
	:) echo "option -$OPTARG needs a value" >&2; usage 1 ;;
	esac
done

for tool in jq curl uuidgen; do
	command -v "$tool" >/dev/null 2>&1 || { echo "gn-clip: '$tool' is required" >&2; exit 1; }
done

# --- clipboard ---------------------------------------------------------------

# Platform-agnostic read: macOS first (the primary target), then the two
# common Linux paths so the script survives a move to a Linux spoke.
read_clipboard() {
	if command -v pbpaste >/dev/null 2>&1; then
		pbpaste
	elif command -v wl-paste >/dev/null 2>&1; then
		wl-paste --no-newline
	elif command -v xclip >/dev/null 2>&1; then
		xclip -selection clipboard -o
	elif command -v xsel >/dev/null 2>&1; then
		xsel --clipboard --output
	else
		echo "gn-clip: no clipboard tool found (pbpaste/wl-paste/xclip/xsel)" >&2
		return 1
	fi
}

clip="$(read_clipboard)"
if [ -z "${clip//[[:space:]]/}" ]; then
	echo "gn-clip: clipboard is empty" >&2
	exit 1
fi

# --- title / body split ------------------------------------------------------
#
# With no -t, the first non-blank line becomes the title and is dropped from
# the body — the natural shape for a pasted Markdown snippet whose first line
# is an H1, and it matches how import-md derives a title. -k keeps the line in
# the body when the paste is really a single block of prose.

body="$clip"
if [ -z "$title" ]; then
	first_line="$(printf '%s\n' "$clip" | grep -m1 -v '^[[:space:]]*$' || true)"
	# Strip Markdown heading markers and surrounding whitespace.
	title="$(printf '%s' "$first_line" | sed -E 's/^[[:space:]]*#{1,6}[[:space:]]*//; s/[[:space:]]+$//; s/^[[:space:]]+//')"
	# DB titles are not meant to hold a paragraph; cap the derived one.
	if [ "${#title}" -gt 120 ]; then
		title="${title:0:117}..."
	fi
	if [ "$keep_first_line" -eq 0 ]; then
		# Drop everything through the first non-blank line, then the blank
		# lines that followed it, so the body starts at real content.
		body="$(printf '%s\n' "$clip" | awk 'seen { print; next } /[^[:space:]]/ { seen = 1 }')"
		body="$(printf '%s\n' "$body" | sed '/./,$!d')"
	fi
fi
[ -n "$title" ] || title="Clipboard note"

# --- auth --------------------------------------------------------------------

api() { # api METHOD PATH [curl args...] -> body on stdout
	local method="$1" path="$2"; shift 2
	curl -sS -X "$method" "$BASE_URL$path" \
		-H "Authorization: Bearer $TOKEN" \
		-H 'Content-Type: application/json' "$@"
}

token_is_valid() {
	[ -n "${1:-}" ] || return 1
	local code
	code="$(curl -sS -o /dev/null -w '%{http_code}' \
		-H "Authorization: Bearer $1" "$BASE_URL/api/v1/auth/me")"
	[ "$code" = "200" ]
}

login() {
	[ -n "$username" ] || read -r -p "GoNotes username: " username

	local password=""
	if [ -n "${GONOTES_PASSWORD:-}" ]; then
		password="$GONOTES_PASSWORD"
	elif [ -n "${GONOTES_SYNC_PASSWORD_B64:-}" ]; then
		# Same credential a sync spoke already holds; decode rather than re-prompt.
		password="$(printf '%s' "$GONOTES_SYNC_PASSWORD_B64" | base64 -d)"
	else
		read -r -s -p "GoNotes password for $username: " password
		echo >&2
	fi

	local resp tok
	resp="$(curl -sS -X POST "$BASE_URL/api/v1/auth/login" \
		-H 'Content-Type: application/json' \
		-d "$(jq -n --arg u "$username" --arg p "$password" '{username:$u, password:$p}')")"
	tok="$(printf '%s' "$resp" | jq -r '.data.token // empty')"
	if [ -z "$tok" ]; then
		echo "gn-clip: login failed: $(printf '%s' "$resp" | jq -r '.error // .')" >&2
		exit 1
	fi

	# Cache the JWT so back-to-back captures don't each prompt for a password.
	mkdir -p "$(dirname "$TOKEN_FILE")"
	(umask 077; printf '%s' "$tok" > "$TOKEN_FILE")
	printf '%s' "$tok"
}

# --- payload -----------------------------------------------------------------

payload="$(jq -n \
	--arg guid "$(uuidgen)" \
	--arg title "$title" \
	--arg body "$body" \
	--arg tags "$tags" \
	--argjson priv "$is_private" \
	--argjson flag "$is_flagged" \
	'{guid: $guid, title: $title, body: $body, is_private: $priv, is_flagged: $flag}
	 + (if $tags == "" then {} else {tags: $tags} end)')"

if [ "$dry_run" -eq 1 ]; then
	printf '%s\n' "$payload"
	# The +expansion guard keeps an empty array from tripping `set -u` on the
	# bash 3.2 that ships with macOS.
	[ -n "${categories[*]+x}" ] && printf 'categories: %s\n' "${categories[*]}" >&2
	exit 0
fi

if ! curl -sS -m 5 -o /dev/null "$BASE_URL/api/v1/health" 2>/dev/null; then
	echo "gn-clip: no GoNotes server at $BASE_URL — start it, or use the import-md recipe in SKILL.md" >&2
	exit 1
fi

TOKEN="$(cat "$TOKEN_FILE" 2>/dev/null || true)"
token_is_valid "$TOKEN" || TOKEN="$(login)"

# --- create ------------------------------------------------------------------

resp="$(api POST /api/v1/notes -d "$payload")"
note_id="$(printf '%s' "$resp" | jq -r '.data.id // empty')"
if [ -z "$note_id" ]; then
	echo "gn-clip: create failed: $(printf '%s' "$resp" | jq -r '.error // .')" >&2
	exit 1
fi

# --- categories --------------------------------------------------------------
#
# Categories are separate entities linked to the note after creation, so each
# -c means: find-or-create the category, then link it (with the subcategory,
# if the argument used Name/Sub notation).

for spec in ${categories[@]+"${categories[@]}"}; do
	cat_name="${spec%%/*}"
	sub="${spec#"$cat_name"}"; sub="${sub#/}"

	cat_id="$(api GET /api/v1/categories \
		| jq -r --arg n "$cat_name" '.data[]? | select(.name == $n) | .id' | head -1)"

	if [ -z "$cat_id" ]; then
		cat_id="$(api POST /api/v1/categories \
			-d "$(jq -n --arg n "$cat_name" '{name: $n}')" | jq -r '.data.id // empty')"
	fi
	if [ -z "$cat_id" ]; then
		echo "gn-clip: could not resolve category '$cat_name' (note $note_id still created)" >&2
		continue
	fi

	link_body='{}'
	[ -n "$sub" ] && link_body="$(jq -n --arg s "$sub" '{subcategories: [$s]}')"
	link_resp="$(api POST "/api/v1/notes/$note_id/categories/$cat_id" -d "$link_body")"
	if [ "$(printf '%s' "$link_resp" | jq -r '.success // false')" != "true" ]; then
		echo "gn-clip: failed to link category '$spec': $(printf '%s' "$link_resp" | jq -r '.error // .')" >&2
	fi
done

echo "Created note $note_id: $title"
