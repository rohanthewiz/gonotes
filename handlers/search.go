package handlers

import (
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"gonotes/models"
	"gonotes/views/pages"
	"gonotes/views/partials"
	"net/http"
)

// SearchPage displays the search interface
func SearchPage(c rweb.Context) error {
	userGUID := getUserGUID(c)
	query := c.Request().QueryParam("q")

	// If query provided, perform search
	var results []models.Note
	if query != "" {
		var err error
		results, err = models.SearchAll(userGUID, query)
		if err != nil {
			logger.LogErr(err, "failed to search notes")
			results = []models.Note{}
		}
	}

	// Render search page with query and results
	html := pages.RenderSearchPage(query, results, userGUID)
	return c.WriteHTML(html)
}

// SearchNotes performs a general search and returns results
func SearchNotes(c rweb.Context) error {
	userGUID := getUserGUID(c)
	query := c.Request().QueryParam("q")
	searchType := c.Request().QueryParam("type") // title, tag, body, all

	if query == "" {
		return c.WriteJSON(map[string]interface{}{
			"results": []models.Note{},
			"count":   0,
		})
	}

	var notes []models.Note
	var err error

	// Perform search based on type
	switch searchType {
	case "title":
		notes, err = models.SearchByTitle(userGUID, query)
	case "tag":
		notes, err = models.SearchByTag(userGUID, query)
	case "body":
		notes, err = models.SearchByBody(userGUID, query)
	default: // "all" or unspecified
		notes, err = models.SearchAll(userGUID, query)
	}

	if err != nil {
		logger.LogErr(err, "search failed", "query", query, "type", searchType)
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	// Return results based on request type
	if c.Request().Header("HX-Request") == "true" {
		// HTMX request - return partial HTML
		html := partials.RenderSearchResults(notes, query)
		return c.WriteHTML(html)
	}

	// API request - return JSON
	return c.WriteJSON(map[string]interface{}{
		"results": notes,
		"count":   len(notes),
		"query":   query,
		"type":    searchType,
	})
}

// SearchByTitleAPI searches notes by title
func SearchByTitleAPI(c rweb.Context) error {
	userGUID := getUserGUID(c)
	query := c.Request().QueryParam("q")

	if query == "" {
		return c.WriteJSON([]models.Note{})
	}

	notes, err := models.SearchByTitle(userGUID, query)
	if err != nil {
		logger.LogErr(err, "title search failed", "query", query)
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	return c.WriteJSON(notes)
}

// SearchByTagAPI searches notes by tag
func SearchByTagAPI(c rweb.Context) error {
	userGUID := getUserGUID(c)
	tag := c.Request().QueryParam("tag")

	if tag == "" {
		return c.WriteJSON([]models.Note{})
	}

	notes, err := models.SearchByTag(userGUID, tag)
	if err != nil {
		logger.LogErr(err, "tag search failed", "tag", tag)
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	return c.WriteJSON(notes)
}

// SearchByBodyAPI performs full-text search in note bodies
func SearchByBodyAPI(c rweb.Context) error {
	userGUID := getUserGUID(c)
	query := c.Request().QueryParam("q")

	if query == "" {
		return c.WriteJSON([]models.Note{})
	}

	notes, err := models.SearchByBody(userGUID, query)
	if err != nil {
		logger.LogErr(err, "body search failed", "query", query)
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	return c.WriteJSON(notes)
}

// SearchResultsPartial returns search results as HTML partial
func SearchResultsPartial(c rweb.Context) error {
	userGUID := getUserGUID(c)
	query := c.Request().QueryParam("q")
	searchType := c.Request().QueryParam("type")

	var notes []models.Note
	var err error

	switch searchType {
	case "title":
		notes, err = models.SearchByTitle(userGUID, query)
	case "tag":
		notes, err = models.SearchByTag(userGUID, query)
	case "body":
		notes, err = models.SearchByBody(userGUID, query)
	default:
		notes, err = models.SearchAll(userGUID, query)
	}

	if err != nil {
		logger.LogErr(err, "search failed for partial", "query", query)
		return c.WriteHTML("<div>Search failed</div>")
	}

	html := partials.RenderSearchResults(notes, query)
	return c.WriteHTML(html)
}
