package web

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
)

// Embed static directory files
//
//go:embed all:static
var staticFiles embed.FS

// SetupStaticFiles configures static file serving using embedded files
func SetupStaticFiles(s *rweb.Server) {
	// Get the static subdirectory from embedded files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		logger.LogErr(err, "failed to get static subdirectory")
		return
	}

	// Serve /favicon.ico as an inline SVG so no separate icon file is needed
	const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 500 500"><rect width="500" height="500" rx="40" fill="#6b9f7b"/><rect x="40" y="180" width="80" height="25" rx="12.5" fill="white" fill-opacity=".8"/><rect x="20" y="235" width="110" height="25" rx="12.5" fill="white" fill-opacity=".9"/><rect x="50" y="290" width="70" height="25" rx="12.5" fill="white" fill-opacity=".7"/><text x="300" y="285" font-family="Arial,sans-serif" font-weight="900" font-size="180" fill="white" text-anchor="middle">GN</text></svg>`

	s.Get("/favicon.ico", func(c rweb.Context) error {
		c.Response().SetHeader("Content-Type", "image/svg+xml")
		c.Response().SetHeader("Cache-Control", "public, max-age=86400")
		return c.Bytes([]byte(faviconSVG))
	})

	// Serve static files at /static/ path
	s.Get("/static/*", func(c rweb.Context) error {
		// Strip /static/ prefix and serve from embedded FS
		path := c.Request().Path()[8:] // Remove "/static/" prefix

		// Open and serve the file
		file, err := staticFS.Open(path)
		if err != nil {
			c.SetStatus(http.StatusNotFound)
			return nil
		}
		defer file.Close()

		// Check if it's a directory
		stat, err := file.Stat()
		if err != nil {
			c.SetStatus(http.StatusInternalServerError)
			return nil
		}

		// fmt.Printf("**-> file stat %#v\n", stat)

		if stat.IsDir() {
			c.SetStatus(http.StatusNotFound)
			return nil
		}

		// Set appropriate content type based on file extension
		contentType := getContentType(path)
		if contentType != "" {
			c.Response().SetHeader("Content-Type", contentType)
		}

		// Set cache headers for static assets
		if isAsset(path) {
			c.Response().SetHeader("Cache-Control", "public, max-age=31536000") // 1 year
		} else {
			c.Response().SetHeader("Cache-Control", "public, max-age=3600") // 1 hour
		}

		// Read file content
		content, err := io.ReadAll(file)
		if err != nil {
			c.SetStatus(http.StatusInternalServerError)
			return nil
		}

		// Serve the file content
		return c.Bytes(content)
	})
}

// getContentType returns the content type based on file extension
func getContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".css"):
		return "text/css"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	case strings.HasSuffix(path, ".html"):
		return "text/html"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(path, ".woff"):
		return "font/woff"
	case strings.HasSuffix(path, ".ttf"):
		return "font/ttf"
	default:
		return ""
	}
}

// isAsset checks if the path is a cacheable asset
func isAsset(path string) bool {
	return strings.Contains(path, "/vendor/") ||
		strings.HasSuffix(path, ".woff2") ||
		strings.HasSuffix(path, ".woff") ||
		strings.HasSuffix(path, ".ttf")
}
