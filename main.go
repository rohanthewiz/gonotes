package main

import (
	"gonotes/web"
	"log"

	"github.com/rohanthewiz/logger"
)

func main() {
	// Initialize logger
	logger.SetLogLevel("info")

	// Initialize database with dual-database architecture
	// if err := models.InitDB(); err != nil {
	// 	log.Fatal("Failed to initialize database:", err)
	// }
	// defer models.CloseDB()

	// Start server
	srv := web.NewServer()
	logger.Info("Starting GoNotes Web on port 8080")
	log.Fatal(web.Run(srv))
}
