package handlers

import (
	"net/http"

	"github.com/rohanthewiz/rweb"
)

// Global events channel for SSE
var eventsCh chan interface{}

// SetEventsChannel stores the SSE channel for handlers to use
func SetEventsChannel(ch chan interface{}) {
	eventsCh = ch
}

// HealthCheck returns the health status of the application
func HealthCheck(c rweb.Context) error {
	return c.WriteJSON(map[string]interface{}{
		"status":  "healthy",
		"service": "gonotes-web",
		"version": "2.0.0",
	})
}

// NotFound handles 404 errors
func NotFound(c rweb.Context) error {
	if c.Request().Header("Accept") == "application/json" {
		c.SetStatus(http.StatusNotFound)
		return c.WriteJSON(map[string]string{
			"error": "Resource not found",
		})
	}

	// Return HTML 404 page
	c.SetStatus(http.StatusNotFound)
	return c.WriteHTML("<h1>404 - Page Not Found</h1>")
}

// ServerError handles 500 errors
func ServerError(c rweb.Context) error {
	if c.Request().Header("Accept") == "application/json" {
		c.SetStatus(http.StatusInternalServerError)
		return c.WriteJSON(map[string]string{
			"error": "Internal server error",
		})
	}

	// Return HTML error page
	c.SetStatus(http.StatusInternalServerError)
	return c.WriteHTML("<h1>500 - Internal Server Error</h1>")
}
