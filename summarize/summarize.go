// Package summarize turns a block of arbitrary text into the three fields a
// GoNotes note is made of: a title, an optional description, and a body.
//
// # WHY A LOCAL CLI AND NOT THE ANTHROPIC HTTP API
//
// The work is done by shelling out to the `claude` CLI. That is a deliberate
// choice over an HTTP call to the model API: the CLI already holds this
// machine's credentials, so nothing here has to invent a place to keep an API
// key (an .env file? the keychain? a per-user setting in the database?) for
// what is a convenience feature. ANTHROPIC_API_KEY is never read, and a
// machine with a signed-in `claude` needs no configuration at all.
//
// This mirrors — and shares its prompt with — the `-s` flag of
// .claude/skills/gonotes/scripts/gn-clip.sh, which is where the feature was
// first proven. The script remains the headless path; this package is what the
// web UI and the TUI call.
//
// # THE CALL IS DELIBERATELY LEAN
//
// Claude Code discovers CLAUDE.md, skills and MCP servers from its WORKING
// DIRECTORY. Run from the gonotes checkout, "condense this paragraph" measured
// ~20k prompt tokens; run from an empty directory with tools and MCP off, ~4k
// for the same answer. The empty cwd is the load-bearing part — the flags alone
// do not stop CLAUDE.md discovery — so the temp dir below is not incidental
// tidiness and must not be "simplified" away.
package summarize

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/rohanthewiz/serr"
)

// DefaultModel is what a caller that does not care gets. An alias rather than a
// pinned model id, because `claude --model` resolves aliases itself and a note
// summary is exactly the small, cheap, high-volume task the fast tier is for.
const DefaultModel = "haiku"

// MaxInput caps what will be sent in one call. A paste this size is a mistake
// (a whole log file, a binary that landed in the clipboard) and the useful
// answer is an error naming the size, not a slow expensive summary of noise.
const MaxInput = 400_000

// TitleMax is the longest generated title that will be handed back. Titles live
// in list rows and window titles; a paragraph in that slot is a layout bug.
// An explicit user-supplied title is never touched by this package.
const TitleMax = 120

// systemPrompt is shared verbatim with gn-clip.sh's SUMMARY_SYSTEM_PROMPT.
// Keep the two in step: a divergence would mean the same clipboard summarizes
// differently depending on which door it came through.
const systemPrompt = `You turn a block of text into a GoNotes note.

Reply with ONLY a JSON object — no prose, no code fence:
{"title": string, "description": string, "body": string}

- title: a short, specific noun phrase naming what the text is about, at most
  120 characters, no trailing punctuation.
- body: the summary itself, in Markdown. Keep every fact that matters and drop
  the boilerplate. Match the shape of the source — prose for prose, bullets for
  an enumeration.
- description: one or two sentences of extra orientation, and ONLY when the
  title by itself leaves the note ambiguous. When the title already conveys the
  gist, return "".

Never state anything the source text does not support.`

// userPrompt is the turn itself. The text to summarize arrives on stdin rather
// than interpolated here, so no amount of quoting or newlines in a paste can
// reshape the request.
const userPrompt = "Summarize the text on stdin into the JSON object described in the system prompt."

// Result is what the model is asked for, and what every caller receives. The
// field names match models.NoteInput's, so a handler can copy them across
// without a translation table.
type Result struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// ErrUnavailable is what callers get when the `claude` CLI is not on PATH. It
// is a distinct error because it is the one failure a UI should answer by
// hiding or explaining the feature rather than by reporting a fault: nothing is
// broken, the machine simply has no summarizer installed.
var ErrUnavailable = serr.New("the 'claude' CLI is not installed on this machine")

// Available reports whether summarizing can work here at all. Cheap enough
// (one PATH walk) to call per request, so no result is cached — a CLI installed
// while the server was running should start working without a restart.
func Available() bool {
	_, err := lookPath("claude")
	return err == nil
}

// lookPath is the PATH probe, behind a var so a test can describe a machine
// with (or without) the CLI installed without one actually being there.
var lookPath = exec.LookPath

// runner is the seam the tests replace. It takes the model and the text and
// returns whatever the CLI printed. Everything above it — validation, fence
// stripping, JSON parsing — is then testable without a model call, which is the
// half of this package that can actually be wrong.
var runner = runClaudeCLI

// Summarize condenses text into a note. The context bounds the model call; a
// caller with no deadline of its own should still set one, because a CLI that
// hangs would otherwise hold an HTTP handler open forever.
//
// model may be empty for DefaultModel. Any alias `claude --model` accepts works.
func Summarize(ctx context.Context, text, model string) (*Result, error) {
	if strings.TrimSpace(text) == "" {
		return nil, serr.New("nothing to summarize")
	}
	if len(text) > MaxInput {
		return nil, serr.New("text is too large to summarize",
			"chars", itoa(len(text)), "max", itoa(MaxInput))
	}
	if !Available() {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}

	raw, err := runner(ctx, model, text)
	if err != nil {
		return nil, serr.Wrap(err, "the 'claude' CLI failed — is it signed in? (claude /status)")
	}
	return parseResult(raw)
}

// runClaudeCLI is the real call. See the package comment for why cwd is an
// empty directory and why the tool/MCP/session flags are all off.
func runClaudeCLI(ctx context.Context, model, text string) (string, error) {
	workdir, err := os.MkdirTemp("", "gonotes-summarize-")
	if err != nil {
		return "", serr.Wrap(err, "could not create a working directory for the summarizer")
	}
	defer os.RemoveAll(workdir)

	cmd := exec.CommandContext(ctx, "claude", "-p",
		"--model", model,
		"--output-format", "text",
		"--no-session-persistence",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--allowedTools", "",
		"--system-prompt", systemPrompt,
		userPrompt,
	)
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(text)

	// Stderr is captured rather than inherited: this runs inside a server (or a
	// Bubble Tea program that owns the terminal), so CLI chatter must not land
	// on the user's screen — but it is the only explanation of a non-zero exit,
	// so it rides along in the error.
	var errBuf strings.Builder
	cmd.Stderr = &errBuf

	out, err := cmd.Output()
	if err != nil {
		// BOTH streams ride along, because the CLI does not consistently pick
		// one: an auth problem prints to stderr, while a refusal or a usage
		// error comes back on stdout with a non-zero exit. An error that names
		// neither ("exit status 1") is unactionable, and this is the one place
		// with anything to say.
		return "", serr.Wrap(err, "summarizer exited with an error",
			"stderr", truncate(strings.TrimSpace(errBuf.String()), 400),
			"stdout", truncate(strings.TrimSpace(string(out)), 400))
	}
	return string(out), nil
}

// parseResult turns the CLI's stdout into a Result, or fails loudly.
//
// FAILING LOUDLY IS THE POINT. A half-understood answer stored as a note is the
// worst outcome available here: the user finds it days later, with the original
// text long gone from the clipboard. An unparseable reply costs one model call
// to retry, and the source text is still wherever it came from — so every
// doubtful case below is an error, never a best-effort salvage.
func parseResult(raw string) (*Result, error) {
	cleaned := stripFences(raw)

	var res Result
	if err := json.Unmarshal([]byte(cleaned), &res); err != nil {
		return nil, serr.Wrap(err, "the summarizer did not return usable JSON",
			"reply", truncate(strings.TrimSpace(raw), 400))
	}

	res.Title = strings.TrimSpace(res.Title)
	res.Description = strings.TrimSpace(res.Description)
	res.Body = strings.TrimRight(res.Body, " \t\n")

	if strings.TrimSpace(res.Body) == "" {
		return nil, serr.New("the summarizer returned an empty body")
	}
	if len([]rune(res.Title)) > TitleMax {
		r := []rune(res.Title)
		res.Title = string(r[:TitleMax-3]) + "..."
	}
	return &res, nil
}

// stripFences drops whole lines that open or close a Markdown code fence.
//
// Models fence JSON even when told not to. Dropping fence LINES is safe
// precisely because JSON escapes newlines inside strings: no line of a valid
// answer can begin with ``` unless it IS the fence. (A summary body full of
// code blocks survives untouched — it lives inside one JSON string, on one
// line.)
func stripFences(raw string) string {
	lines := strings.Split(raw, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// itoa keeps the serr key/value pairs free of an fmt import for two integers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
