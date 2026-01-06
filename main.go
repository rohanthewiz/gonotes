package main

import (
	"gonotes/models"
	"gonotes/web"
	"log"

	"github.com/rohanthewiz/logger"
)

func main() {
	// Initialize logger
	logger.SetLogLevel("info")

	// Initialize DuckDB database and create tables
	if err := models.InitDB(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer models.CloseDB()

	// Start server
	srv := web.NewServer()
	logger.Info("Starting GoNotes Web on port 8080")
	log.Fatal(web.Run(srv))
}
