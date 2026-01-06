package web

import (
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
)

// NewServer creates and configures the RWeb server
func NewServer() *rweb.Server {
	// Create server instance with options
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8000",
		Verbose: true,
	})

	// Apply middleware
	s.Use(rweb.RequestInfo)          // Logs request info
	s.Use(CorsMiddleware)            // Custom CORS middleware
	s.Use(SessionMiddleware)         // Session management
	s.Use(SecurityHeadersMiddleware) // Security headers
	s.Use(LoggingMiddleware)         // Request logging

	// Setup routes
	setupRoutes(s)

	// Serve static files using embedded FS
	SetupStaticFiles(s)

	return s
}

// Run starts the server
func Run(s *rweb.Server) error {
	logger.Info("GoNotes Web Server starting on", "address", ":8000")
	return s.Run()
}
