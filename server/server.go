package server

import (
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/logger"
	"go_notes_web/handlers"
)

// NewServer creates and configures the RWeb server
func NewServer() *rweb.Server {
	// Create server instance with options
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})
	
	// Apply middleware
	s.Use(rweb.RequestInfo) // Logs request info
	s.Use(CorsMiddleware)   // Custom CORS middleware
	s.Use(SessionMiddleware) // Session management
	s.Use(SecurityHeadersMiddleware) // Security headers
	s.Use(LoggingMiddleware) // Request logging
	
	// Setup routes
	setupRoutes(s)
	
	// Serve static files using embedded FS
	SetupStaticFiles(s)
	
	// Setup Server-Sent Events for real-time updates
	eventsCh := make(chan interface{}, 16)
	handlers.SetEventsChannel(eventsCh) // Store channel for handlers to use
	
	s.Get("/events", func(c rweb.Context) error {
		logger.Info("SSE connection established")
		return s.SetupSSE(c, eventsCh)
	})
	
	return s
}

// Run starts the server
func Run(s *rweb.Server) error {
	logger.Info("GoNotes Web Server starting on", "address", ":8080")
	return s.Run()
}