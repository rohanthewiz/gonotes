package main

import (
	"log"
	"go_notes_web/server"
	"go_notes_web/models"
	"github.com/rohanthewiz/logger"
)

func main() {
	// Initialize logger
	logger.SetLogLevel("debug")
	
	// Initialize database with dual-database architecture
	if err := models.InitDB(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer models.CloseDB()
	
	// Start server
	srv := server.NewServer()
	logger.Info("Starting GoNotes Web on port 8080")
	log.Fatal(server.Run(srv))
}