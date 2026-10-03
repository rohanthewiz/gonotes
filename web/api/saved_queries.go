package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// saved_queries.go gives the advanced-search bar a memory that outlives the
// browser tab: named ("saved") queries and the recent-query history, both
// stored per user on the server (models/saved_query.go).
//
//	GET    /api/v1/notes/query/saved      both lists
//	POST   /api/v1/notes/query/saved      save {name, query} (upsert by name)
//	DELETE /api/v1/notes/query/saved/:id  forget one saved or history row
//	POST   /api/v1/notes/query/history    record {query} as just run
//
// History is recorded by an explicit POST rather than as a side effect of
// GET /notes/query. That endpoint is also how the TUI REFRESHES an active
// query, many times per session; only a deliberate run is history, and only
// the client knows which is which.
//
// The completion endpoint is what most users will see this through: on an
// empty box it leads with these rows (see models.storedQuerySuggestions), so
// the list endpoint exists mainly for management — and for tests.

// saveQueryRequest is the body of POST /notes/query/saved.
type saveQueryRequest struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

// recordQueryRequest is the body of POST /notes/query/history.
type recordQueryRequest struct {
	Query string `json:"query"`
}

// ListSavedQueries handles GET /api/v1/notes/query/saved
func ListSavedQueries(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	list, err := models.ListSavedQueries(userGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to list saved queries"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	return writeSuccess(ctx, http.StatusOK, list)
}

// SaveQuery handles POST /api/v1/notes/query/saved
//
// A query that does not parse is refused with the same positioned 400 the run
// endpoint gives, so the bar can underline the mistake rather than store it.
func SaveQuery(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	var req saveQueryRequest
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}

	saved, err := models.SaveNamedQuery(userGUID, req.Name, req.Query)
	if err != nil {
		var qe *models.QueryError
		if errors.As(err, &qe) {
			return writeQueryError(ctx, qe)
		}
		var ie *models.SavedQueryInputError
		if errors.As(err, &ie) {
			return writeError(ctx, http.StatusBadRequest, ie.Msg)
		}
		logger.LogErr(serr.Wrap(err, "failed to save query"), "database error", "name", req.Name)
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	return writeSuccess(ctx, http.StatusOK, saved)
}

// DeleteSavedQuery handles DELETE /api/v1/notes/query/saved/:id
//
// It removes a history row as readily as a named one: "forget this" in the
// popup applies to whichever row is highlighted.
func DeleteSavedQuery(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	id, err := strconv.ParseInt(ctx.Request().Param("id"), 10, 64)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid saved query id")
	}
	if err := models.DeleteSavedQuery(userGUID, id); err != nil {
		if errors.Is(err, models.ErrSavedQueryNotFound) {
			return writeError(ctx, http.StatusNotFound, "saved query not found")
		}
		logger.LogErr(serr.Wrap(err, "failed to delete saved query"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	return writeSuccess(ctx, http.StatusOK, map[string]int64{"id": id})
}

// RecordQueryHistory handles POST /api/v1/notes/query/history
//
// The text is not re-parsed: the client only records what it just ran
// successfully, and history is a convenience whose worst failure is a row the
// user can forget from the popup.
func RecordQueryHistory(ctx rweb.Context) error {
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}
	var req recordQueryRequest
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid JSON body")
	}
	if err := models.RecordQueryHistory(userGUID, req.Query); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to record query history"), "database error")
		return writeError(ctx, http.StatusInternalServerError, "database error")
	}
	return writeSuccess(ctx, http.StatusOK, map[string]string{"query": req.Query})
}
