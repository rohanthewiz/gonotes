package api

import (
	"errors"
	"net/http"
	"strconv"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// query.go exposes the advanced search: an SQL-shaped WHERE expression over
// every note attribute, the completions that make it typeable, and the schema
// that documents it.
//
//	GET /api/v1/notes/query?q=…      run a query
//	GET /api/v1/notes/query/complete what can be typed at a cursor position
//	GET /api/v1/notes/query/schema   the field catalog, operators and examples
//
// All three are authenticated and user-scoped, like every other note endpoint,
// and all three delegate to models — the language lives there so the web page
// and the terminal UI cannot drift apart. See models/query.go.
//
// The one thing this layer decides on its own is the difference between a
// SYNTAX error and a STORAGE error. A malformed query is the user's, answered
// 400 with the offending position so the box can underline it; a failure to
// read the databases is ours, answered 500 and logged. models.QueryError is
// what tells them apart.

// QueryNotesResponse is the envelope's data for a successful run.
type QueryNotesResponse struct {
	// Notes is the page of results, already ordered by the query (or by the
	// default, updated_at descending).
	Notes []models.NoteOutput `json:"notes"`
	// Matched is how many notes satisfied the query before paging — what a
	// UI shows as "N results". Returned is how many came back in this page.
	Matched  int `json:"matched"`
	Returned int `json:"returned"`
	// Scanned is the size of the candidate set, so a UI can say "3 of 412".
	Scanned int `json:"scanned"`
	// Normalized is the query re-rendered from its parse tree: canonical
	// field names, uppercase keywords. Showing it back is the cheapest
	// answer to "why did that match?".
	Normalized string `json:"normalized"`
	// Fields lists the canonical names the query actually touched, which is
	// how a user notices an alias resolved somewhere unexpected.
	Fields []string `json:"fields"`
	// Ordered reports that the query specified its own ORDER BY. A client
	// with a sort control of its own uses this to decide whether re-sorting
	// would be overriding an explicit instruction.
	Ordered bool `json:"ordered"`
	// IncludesDeleted reports that soft-deleted notes were candidates,
	// because the query named deleted_at or the caller asked.
	IncludesDeleted bool    `json:"includes_deleted"`
	ElapsedMs       float64 `json:"elapsed_ms"`
	Limit           int     `json:"limit"`
	Offset          int     `json:"offset"`
}

// writeQueryError answers a malformed query with the position of the problem
// alongside the message.
//
// It writes the envelope by hand rather than through writeError because a
// syntax error carries structure — where, how long, and what was probably
// meant — and a UI that can only print a sentence has to make the user find
// the mistake themselves. Success stays false, so no existing client is
// confused by the extra data.
func writeQueryError(ctx rweb.Context, qe *models.QueryError) error {
	ctx.SetStatus(http.StatusBadRequest)
	return ctx.WriteJSON(APIResponse{
		Success: false,
		Error:   qe.Msg,
		Data:    qe,
	})
}

// QueryNotes handles GET /api/v1/notes/query
//
// Parameters:
//
//	q                the query text; empty matches every note
//	limit, offset    paging, overriding any LIMIT/OFFSET in the text
//	sort, dir        ordering, overriding any ORDER BY in the text
//	include_deleted  include soft-deleted notes without saying so in the text
func QueryNotes(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	src := ctx.Request().QueryParam("q")

	opts := models.QueryOptions{}
	if s := ctx.Request().QueryParam("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return writeError(ctx, http.StatusBadRequest, "invalid limit parameter")
		}
		opts.Limit = n
	}
	if s := ctx.Request().QueryParam("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return writeError(ctx, http.StatusBadRequest, "invalid offset parameter")
		}
		opts.Offset = n
	}
	if s := ctx.Request().QueryParam("sort"); s != "" {
		f, ok := models.LookupQueryField(s)
		if !ok || f.Multi {
			return writeError(ctx, http.StatusBadRequest, "invalid sort field")
		}
		opts.SortField = f.Name
		opts.SortDesc = ctx.Request().QueryParam("dir") == "desc"
	}
	if s := ctx.Request().QueryParam("include_deleted"); s == "true" || s == "1" {
		opts.IncludeDeleted = true
	}

	// Parsing is separated from running so the two failure modes stay
	// distinguishable: a parse error is the user's and never reaches storage.
	q, err := models.ParseQuery(src)
	if err != nil {
		var qe *models.QueryError
		if errors.As(err, &qe) {
			return writeQueryError(ctx, qe)
		}
		return writeError(ctx, http.StatusBadRequest, err.Error())
	}

	res, err := models.RunQuery(q, userGUID, opts)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to run note query"), "database error", "query", src)
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}

	outputs := make([]models.NoteOutput, len(res.Notes))
	for i := range res.Notes {
		outputs[i] = res.Notes[i].ToOutput()
	}

	return writeSuccess(ctx, http.StatusOK, QueryNotesResponse{
		Notes:           outputs,
		Matched:         res.Matched,
		Returned:        len(outputs),
		Scanned:         res.Scanned,
		Normalized:      q.String(),
		Fields:          q.UsedFields(),
		Ordered:         len(q.Order) > 0 || opts.SortField != "",
		IncludesDeleted: q.IncludesDeleted() || opts.IncludeDeleted,
		ElapsedMs:       float64(res.Elapsed.Microseconds()) / 1000,
		Limit:           opts.Limit,
		Offset:          opts.Offset,
	})
}

// QueryNotesComplete handles GET /api/v1/notes/query/complete
//
//	q    the query text as it currently stands
//	pos  byte offset of the cursor; defaults to the end of q
//
// This is called on a keystroke, so it does the least it can: no parse, no
// note bodies, and a suggestion list already capped to what a popup can show.
// It never fails — a query being typed is usually invalid, and answering an
// error would take the help away exactly when it is wanted.
func QueryNotesComplete(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	src := ctx.Request().QueryParam("q")
	pos := len(src)
	if s := ctx.Request().QueryParam("pos"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			pos = n
		}
	}

	return writeSuccess(ctx, http.StatusOK, models.CompleteQuery(src, pos, userGUID))
}

// QueryNotesSchema handles GET /api/v1/notes/query/schema
//
// It is the language's documentation as data: every queryable field with its
// type, every operator with what it applies to, the keywords, worked examples,
// and the handful of places the semantics deliberately differ from SQL. A UI
// renders its help panel from this rather than hard-coding a list that would
// go stale the first time a column is added to the note model.
//
// Authentication is required even though nothing here is user-specific: the
// field catalog describes the shape of a user's data, and an unauthenticated
// endpoint that enumerates it is a needless disclosure on a hub.
func QueryNotesSchema(ctx rweb.Context) error {
	if GetCurrentUserGUID(ctx) == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	return writeSuccess(ctx, http.StatusOK, models.QuerySchema())
}
