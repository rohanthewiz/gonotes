package server

import (
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"net/http"
	"strings"
	"time"
)

// CorsMiddleware handles CORS headers for cross-origin requests
func CorsMiddleware(c rweb.Context) error {
	// Set CORS headers for all responses
	c.Response().SetHeader("Access-Control-Allow-Origin", "*")
	c.Response().SetHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	c.Response().SetHeader("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

	// Handle preflight OPTIONS requests
	if c.Request().Method() == "OPTIONS" {
		c.SetStatus(http.StatusOK)
		return nil
	}

	return c.Next()
}

// SessionMiddleware manages user sessions
func SessionMiddleware(c rweb.Context) error {
	// Get session cookie from header
	cookieValue, err := c.GetCookie("session_id")

	if err != nil {
		// No session cookie - create one for anonymous users
		sessionID := generateSessionID()
		err = c.SetCookie("session_id", sessionID)
		if err != nil {
			logger.LogErr(err, "failed to set session cookie")
		}

		// Set default user in context
		c.Set("user_guid", "default-user-guid")
		c.Set("session_id", sessionID)
	} else {
		// Validate session
		userGUID := validateSession(cookieValue)
		if userGUID == "" {
			// Invalid session - use default user
			userGUID = "default-user-guid"
		}
		c.Set("user_guid", userGUID)
		c.Set("session_id", cookieValue)
	}

	return c.Next()
}

// SecurityHeadersMiddleware adds security headers to responses
func SecurityHeadersMiddleware(c rweb.Context) error {
	// Add security headers
	c.Response().SetHeader("X-Content-Type-Options", "nosniff")
	c.Response().SetHeader("X-Frame-Options", "DENY")
	c.Response().SetHeader("X-XSS-Protection", "1; mode=block")
	c.Response().SetHeader("Referrer-Policy", "strict-origin-when-cross-origin")

	// Content Security Policy - adjust as needed
	csp := []string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline' 'unsafe-eval'", // Monaco requires unsafe-eval
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
	}
	c.Response().SetHeader("Content-Security-Policy", strings.Join(csp, "; "))

	return c.Next()
}

// RateLimitMiddleware implements basic rate limiting
func RateLimitMiddleware(requestsPerMinute int) rweb.Handler {
	// Simple in-memory rate limiter (production should use Redis or similar)
	type visitor struct {
		lastSeen time.Time
		count    int
	}

	visitors := make(map[string]*visitor)

	return func(c rweb.Context) error {
		ip := c.Request().Header("X-Forwarded-For")
		if ip == "" {
			ip = c.Request().Header("X-Real-IP")
		}
		if ip == "" {
			// Fallback to remote address from connection
			ip = "unknown"
		}

		// Clean up old entries periodically
		now := time.Now()
		for addr, v := range visitors {
			if now.Sub(v.lastSeen) > time.Minute {
				delete(visitors, addr)
			}
		}

		// Check rate limit for current IP
		v, exists := visitors[ip]
		if !exists {
			visitors[ip] = &visitor{lastSeen: now, count: 1}
		} else {
			if now.Sub(v.lastSeen) < time.Minute {
				v.count++
				if v.count > requestsPerMinute {
					logger.Info("Rate limit exceeded", "ip", ip)
					c.SetStatus(http.StatusTooManyRequests)
					return nil
				}
			} else {
				v.lastSeen = now
				v.count = 1
			}
		}

		return c.Next()
	}
}

// LoggingMiddleware provides detailed request logging
func LoggingMiddleware(c rweb.Context) error {
	start := time.Now()

	// Log request details
	logger.Debug("Request started",
		"method", c.Request().Method(),
		"path", c.Request().Path(),
		"ip", c.Request().Header("X-Forwarded-For"),
	)

	// Process request
	err := c.Next()

	// Log response details
	duration := time.Since(start)
	logger.Debug("Request completed",
		"method", c.Request().Method(),
		"path", c.Request().Path(),
		"duration", duration,
		"error", err,
	)

	return err
}

// Helper functions

func generateSessionID() string {
	// Simple session ID generation - should use crypto/rand in production
	return time.Now().Format("20060102150405") + "-session"
}

func validateSession(sessionID string) string {
	// TODO: Implement actual session validation against database
	// For now, return default user
	return "default-user-guid"
}

func isProductionEnvironment() bool {
	// TODO: Check actual environment
	return false
}
