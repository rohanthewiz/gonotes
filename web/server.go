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

// NewTestServer creates and configures the RWeb server with custom options.
// This is intended for testing, allowing tests to specify a ReadyChan and dynamic port.
func NewTestServer(opts rweb.ServerOptions) *rweb.Server {
	s := rweb.NewServer(opts)

	// Apply the same middleware as production server
	s.Use(rweb.RequestInfo)
	s.Use(CorsMiddleware)
	s.Use(SessionMiddleware)
	s.Use(SecurityHeadersMiddleware)
	s.Use(LoggingMiddleware)

	// Setup routes
	setupRoutes(s)

	// Serve static files using embedded FS
	SetupStaticFiles(s)

	return s
}
