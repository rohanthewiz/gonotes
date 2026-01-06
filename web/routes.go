package web

import (
	"github.com/rohanthewiz/rweb"
	"gonotes/web/pages"
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
}
