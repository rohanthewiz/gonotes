package web

import (
	"gonotes/web/api"
	"gonotes/web/pages/auth"
	"gonotes/web/pages/landing"

	"github.com/rohanthewiz/rweb"
)

// setupRoutes configures all application routes
func setupRoutes(s *rweb.Server) {
	// Page routes - HTML responses

	// Main landing page (requires authentication via JavaScript)
	s.Get("/", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("Content-Type", "text/html; charset=utf-8")
		page := landing.NewPage()
		return ctx.WriteHTML(page.Render())
	})

	// Login page
	s.Get("/login", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("Content-Type", "text/html; charset=utf-8")
		page := auth.NewLoginPage()
		return ctx.WriteHTML(page.Render())
	})

	// Register page
	s.Get("/register", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("Content-Type", "text/html; charset=utf-8")
		page := auth.NewRegisterPage()
		return ctx.WriteHTML(page.Render())
	})

	// Health check endpoint
	// s.Get("/health", handlers.HealthCheck)

	// =========================================
	// Authentication routes - public endpoints
	// =========================================
	// Note: JWT middleware is applied globally, but these endpoints don't require auth
	s.Post("/api/v1/auth/register", api.Register) // Create new account
	s.Post("/api/v1/auth/login", api.Login)       // Authenticate user

	// Protected auth routes - handlers check authentication
	s.Get("/api/v1/auth/me", api.GetCurrentUser)     // Get current user profile
	s.Post("/api/v1/auth/refresh", api.RefreshToken) // Refresh JWT token

	// =========================================
	// API v1 routes - JSON responses
	// =========================================
	// Note: Authentication is enforced within handlers via api.GetCurrentUserGUID()
	// This allows handlers to return proper 401 errors with JSON responses

	// Notes CRUD endpoints following RESTful conventions
	s.Post("/api/v1/notes", api.CreateNote)       // Create a new note
	s.Get("/api/v1/notes", api.ListNotes)         // List all notes (with pagination)
	s.Get("/api/v1/notes/:id", api.GetNote)       // Get a single note by ID
	s.Put("/api/v1/notes/:id", api.UpdateNote)    // Update a note by ID
	s.Delete("/api/v1/notes/:id", api.DeleteNote) // Soft delete a note by ID

	// Categories CRUD endpoints following RESTful conventions
	s.Post("/api/v1/categories", api.CreateCategory)       // Create a new category
	s.Get("/api/v1/categories", api.ListCategories)        // List all categories (with pagination)
	s.Get("/api/v1/categories/:id", api.GetCategory)       // Get a single category by ID
	s.Put("/api/v1/categories/:id", api.UpdateCategory)    // Update a category by ID
	s.Delete("/api/v1/categories/:id", api.DeleteCategory) // Delete a category by ID

	// Note-Category relationship endpoints
	s.Post("/api/v1/notes/:id/categories/:category_id", api.AddCategoryToNote)        // Add a category to a note
	s.Delete("/api/v1/notes/:id/categories/:category_id", api.RemoveCategoryFromNote) // Remove a category from a note
	s.Put("/api/v1/notes/:id/categories/:category_id", api.UpdateNoteCategory)         // Update subcategories for a note-category relationship
	s.Get("/api/v1/notes/:id/categories", api.GetNoteCategories)                      // Get all categories for a note
	s.Get("/api/v1/categories/:id/notes", api.GetCategoryNotes)                       // Get all notes for a category
	s.Get("/api/v1/note-category-mappings", api.GetNoteCategoryMappings)              // Bulk: all note-category mappings for search bar

	// =========================================
	// Sync endpoints
	// =========================================
	// Used for peer-to-peer synchronization between devices/clients
	s.Get("/api/v1/sync/changes", api.GetUserChanges) // Get user's changes since timestamp
}
