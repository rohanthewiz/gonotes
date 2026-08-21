package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gonotes/summarize"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
)

// ============================================================================
// Summarize API
//
// One endpoint, two callers: the web UI's Summarize buttons and the TUI's HTTP
// store (a TUI in local mode calls the summarize package directly and never
// comes through here).
//
// WHY THE SERVER DOES THIS AND NOT THE BROWSER: the summarizer is the local
// `claude` CLI (see package summarize), which exists on the machine running
// GoNotes, not in the tab. That also means the answer to "can I summarize?"
// is a property of the SERVER — so GET reports it, and the web UI hides its
// buttons rather than offering a door that always fails.
//
// Authentication is required even though nothing is read from or written to
// the database: this endpoint spends money and CPU on the host's account, so
// it must not be reachable by anyone who can reach the port.
// ============================================================================

// summarizeTimeout bounds the model call. Generous, because the input may be a
// long paste on a slow link, but finite: without it a wedged CLI would hold the
// handler — and the browser's spinner — open forever.
const summarizeTimeout = 3 * time.Minute

// SummarizeStatus handles GET /api/v1/summarize
// Reports whether this installation can summarize at all, so a UI can decide
// whether to show the feature. Never an error: "no" is a legitimate answer.
func SummarizeStatus(ctx rweb.Context) error {
	if GetCurrentUserGUID(ctx) == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	return writeSuccess(ctx, http.StatusOK, map[string]any{
		"available":     summarize.Available(),
		"default_model": summarize.DefaultModel,
	})
}

// Summarize handles POST /api/v1/summarize
// Request body: {"text": "...", "model": "haiku"}  (model optional)
// Response data: {"title": "...", "description": "...", "body": "..."}
//
// Nothing is stored. The caller gets the three fields and decides what to do
// with them — which is what lets the same endpoint serve "summarize the
// clipboard into a new note" and "summarize what is in the editor right now".
func Summarize(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	var req struct {
		Text  string `json:"text"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid request body")
	}
	// Trimmed, not just empty: a body of spaces and newlines is a client mistake
	// (an empty editor, a clipboard holding a blank line), and it deserves a 400
	// saying so rather than the 502 it would earn by reaching the summarizer and
	// being refused there.
	if strings.TrimSpace(req.Text) == "" {
		return writeError(ctx, http.StatusBadRequest, "nothing to summarize")
	}

	// 503 rather than 500 for a missing CLI: the request was fine, the capability
	// is absent, and that distinction is what tells the UI to explain rather than
	// to apologize.
	if !summarize.Available() {
		return writeError(ctx, http.StatusServiceUnavailable,
			"summarizing needs the 'claude' CLI on this machine's PATH")
	}

	cCtx, cancel := context.WithTimeout(context.Background(), summarizeTimeout)
	defer cancel()

	res, err := summarize.Summarize(cCtx, req.Text, req.Model)
	if err != nil {
		// Logged here and reported to the caller as its own message: a failed
		// summary is usually explained by what the model said back (see
		// summarize.parseResult), and hiding that behind a generic string would
		// leave the user with nothing to act on.
		//
		// The note text itself is NEVER logged — only the failure. Bodies do not
		// go to disk outside the database.
		logger.LogErr(err, "summarize failed", "user", userGUID)
		return writeError(ctx, http.StatusBadGateway, err.Error())
	}

	return writeSuccess(ctx, http.StatusOK, res)
}
