package server

import (
	"github.com/rohanthewiz/rweb"
	"go_notes_web/handlers"
)

// setupRoutes configures all application routes
func setupRoutes(s *rweb.Server) {
	// Page routes - HTML responses
	s.Get("/", handlers.Dashboard)                   // Main dashboard
	s.Get("/notes/new", handlers.NewNoteForm)        // New note form
	s.Get("/notes/:guid", handlers.ViewNote)         // View single note
	s.Get("/notes/:guid/edit", handlers.EditNote)    // Edit note form
	s.Get("/search", handlers.SearchPage)            // Search page
	s.Get("/tags", handlers.TagsPage)                // Tags overview
	
	// API endpoints - JSON/partial responses
	api := s.Group("/api")
	{
		// Note CRUD operations
		api.Post("/notes", handlers.CreateNote)
		api.Put("/notes/:guid", handlers.UpdateNote)
		api.Delete("/notes/:guid", handlers.DeleteNote)
		api.Post("/notes/:guid/save", handlers.AutoSaveNote) // Auto-save endpoint
		
		// Search endpoints
		api.Get("/search", handlers.SearchNotes)
		api.Get("/search/title", handlers.SearchByTitleAPI)
		api.Get("/search/tag", handlers.SearchByTagAPI)
		api.Get("/search/body", handlers.SearchByBodyAPI)
		
		// Tag management
		api.Get("/tags", handlers.GetAllTags)
		api.Get("/tags/:tag/notes", handlers.GetNotesByTag)
		
		// Import/Export functionality
		api.Get("/export", handlers.ExportNotes)
		api.Post("/import", handlers.ImportNotes)
		
		// User preferences
		api.Get("/preferences", handlers.GetPreferences)
		api.Post("/preferences", handlers.SavePreferences)
	}
	
	// HTMX partial endpoints - return HTML fragments
	partials := s.Group("/partials")
	{
		partials.Get("/notes-list", handlers.NotesListPartial)
		partials.Get("/note-card/:guid", handlers.NoteCardPartial)
		partials.Get("/search-results", handlers.SearchResultsPartial)
		partials.Get("/tags-cloud", handlers.TagsCloudPartial)
		partials.Get("/recent-notes", handlers.RecentNotesPartial)
	}
	
	// Health check endpoint
	s.Get("/health", handlers.HealthCheck)
}