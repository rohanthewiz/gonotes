package handlers

import (
	"encoding/json"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"gonotes/models"
	"time"
)

// ExportNotes exports all user notes as JSON
func ExportNotes(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Get all notes for user (no pagination for export)
	notes, err := models.GetNotesForUser(userGUID, 10000, 0)
	if err != nil {
		logger.LogErr(err, "failed to export notes")
		return c.WriteJSON(map[string]string{"error": "Export failed"})
	}

	// Create export structure
	export := map[string]interface{}{
		"version":     "2.0",
		"exported_at": time.Now(),
		"notes_count": len(notes),
		"notes":       notes,
	}

	// Set download headers
	filename := "gonotes-export-" + time.Now().Format("20060102-150405") + ".json"
	c.Response().SetHeader("Content-Disposition", "attachment; filename="+filename)
	c.Response().SetHeader("Content-Type", "application/json")

	return c.WriteJSON(export)
}

// ImportNotes imports notes from JSON
func ImportNotes(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// Parse uploaded file
	file, _, err := c.Request().GetFormFile("import_file")
	if err != nil {
		logger.LogErr(err, "failed to get import file")
		return c.WriteJSON(map[string]string{"error": "No file uploaded"})
	}
	defer file.Close()

	// Decode JSON
	var importData struct {
		Version string        `json:"version"`
		Notes   []models.Note `json:"notes"`
	}

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&importData); err != nil {
		logger.LogErr(err, "failed to decode import file")
		return c.WriteJSON(map[string]string{"error": "Invalid file format"})
	}

	// Import notes
	imported := 0
	failed := 0

	for _, note := range importData.Notes {
		// Create new note with new GUID
		newNote := models.Note{
			Title:       note.Title + " (imported)",
			Description: note.Description,
			Body:        note.Body,
			Tags:        note.Tags,
			IsPrivate:   note.IsPrivate,
		}

		if err := newNote.Save(userGUID); err != nil {
			logger.LogErr(err, "failed to import note", "title", note.Title)
			failed++
		} else {
			imported++
		}
	}

	return c.WriteJSON(map[string]interface{}{
		"success":  true,
		"imported": imported,
		"failed":   failed,
		"total":    len(importData.Notes),
	})
}

// GetPreferences returns user preferences
func GetPreferences(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// TODO: Implement actual preferences storage
	preferences := map[string]interface{}{
		"user_guid":    userGUID,
		"theme":        "light",
		"editor_theme": "vs",
		"auto_save":    true,
		"font_size":    14,
	}

	return c.WriteJSON(preferences)
}

// SavePreferences saves user preferences
func SavePreferences(c rweb.Context) error {
	userGUID := getUserGUID(c)

	// TODO: Implement actual preferences storage
	// For now, just return success
	logger.Info("Saving preferences", "user", userGUID)

	return c.WriteJSON(map[string]bool{"success": true})
}
