package handlers

import (
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"gonotes/models"
	"gonotes/views/pages"
	"gonotes/views/partials"
	"net/http"
)

// TagsPage displays all tags with note counts
func TagsPage(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Get all unique tags
	tags, err := models.GetAllUniqueTags(userGUID)
	if err != nil {
		logger.LogErr(err, "failed to get tags")
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	// Convert strings to TagInfo structs
	// For now, we'll just use count of 0 - in production, we'd count notes per tag
	tagInfos := make([]pages.TagInfo, len(tags))
	for i, tag := range tags {
		tagInfos[i] = pages.TagInfo{
			Name:  tag,
			Count: 0, // TODO: Get actual count from database
		}
	}

	// Render tags page
	html := pages.RenderTagsPage(tagInfos, userGUID)
	return c.WriteHTML(html)
}

// GetAllTags returns all unique tags as JSON
func GetAllTags(c rweb.Context) error {
	userGUID := getUserGUID(c)

	tags, err := models.GetAllUniqueTags(userGUID)
	if err != nil {
		logger.LogErr(err, "failed to get tags for API")
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	// Return tags with counts (simplified for now)
	tagData := make([]map[string]interface{}, len(tags))
	for i, tag := range tags {
		tagData[i] = map[string]interface{}{
			"name":  tag,
			"count": 0, // TODO: Add actual count
		}
	}

	return c.WriteJSON(tagData)
}

// GetNotesByTag returns all notes with a specific tag
func GetNotesByTag(c rweb.Context) error {
	userGUID := getUserGUID(c)
	tag := c.Request().Param("tag")

	notes, err := models.SearchByTag(userGUID, tag)
	if err != nil {
		logger.LogErr(err, "failed to get notes by tag", "tag", tag)
		c.SetStatus(http.StatusInternalServerError)
		return nil
	}

	// Return based on request type
	if c.Request().Header("HX-Request") == "true" {
		// Return as HTML partial
		html := partials.RenderNotesList(notes)
		return c.WriteHTML(html)
	}

	// Return as JSON
	return c.WriteJSON(map[string]interface{}{
		"tag":   tag,
		"notes": notes,
		"count": len(notes),
	})
}

// TagsCloudPartial returns a tag cloud as HTML partial
func TagsCloudPartial(c rweb.Context) error {
	userGUID := getUserGUID(c)

	tags, err := models.GetAllUniqueTags(userGUID)
	if err != nil {
		logger.LogErr(err, "failed to get tags for cloud")
		return c.WriteHTML("<div>Failed to load tags</div>")
	}

	html := partials.RenderTagsCloud(tags)
	return c.WriteHTML(html)
}
