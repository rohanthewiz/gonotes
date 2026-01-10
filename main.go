package main

import (
	"gonotes/models"
	"gonotes/web"
	"os"

	"github.com/rohanthewiz/logger"
)

func main() {
	// Initialize logger
	logger.SetLogLevel("info")

	// Initialize DuckDB database and create tables
	if err := models.InitDB(); err != nil {
		logger.LogErr(err, "Failed to initialize database")
		os.Exit(1)
	}
	defer models.CloseDB()

	// Start server
	srv := web.NewServer()
	logger.Info("Starting GoNotes Web on port 8080")

	logger.LogErr(web.Run(srv))
}
