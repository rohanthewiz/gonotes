package web

import (
	"gonotes/web/api"
	"gonotes/web/pages"

	"github.com/rohanthewiz/rweb"
)

// setupRoutes configures all application routes
func setupRoutes(s *rweb.Server) {
	// Page routes - HTML responses

	s.Get("/", func(ctx rweb.Context) error {
		// METHOD CHAINING: ctx.Response() returns a response object, then we call SetHeader() on it
		// SetHeader sets an HTTP response header (key-value pair)
		ctx.Response().SetHeader("Content-Type", "text/html; charset=utf-8")

		// CALLING METHODS ACROSS PACKAGES
		// pages.HomePage is a struct instance from the pages package
		// We call its Render() method, which returns an HTML string
		// ctx.WriteHTML() sends that HTML back to the client
		// The return statement returns the error (or nil) from WriteHTML
		return ctx.WriteHTML(pages.HomePage.Render())
	})

	// Health check endpoint
	// s.Get("/health", handlers.HealthCheck)

	// API v1 routes - JSON responses
	// Notes CRUD endpoints following RESTful conventions
	s.Post("/api/v1/notes", api.CreateNote)       // Create a new note
	s.Get("/api/v1/notes", api.ListNotes)         // List all notes (with pagination)
	s.Get("/api/v1/notes/:id", api.GetNote)       // Get a single note by ID
	s.Put("/api/v1/notes/:id", api.UpdateNote)    // Update a note by ID
	s.Delete("/api/v1/notes/:id", api.DeleteNote) // Soft delete a note by ID
}
