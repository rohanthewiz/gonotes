package models

import (
	"database/sql"

	_ "github.com/marcboeker/go-duckdb" // DuckDB driver registration
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// db holds the database connection pool. Using a package-level variable
// allows for simple access across the models package while maintaining
// a single connection pool for the application lifecycle.
var db *sql.DB

// DBPath defines the location of the DuckDB database file.
// Stored in ./data/ to keep data separate from application code.
const DBPath = "./data/notes.ddb"

// InitDB establishes a connection to the DuckDB database and creates
// the required tables if they don't exist. This should be called once
// at application startup before any database operations.
func InitDB() error {
	var err error

	// Open connection to DuckDB. The driver will create the file if it
	// doesn't exist, which is the expected behavior for first-run setup.
	db, err = sql.Open("duckdb", DBPath)
	if err != nil {
		return serr.Wrap(err, "failed to open DuckDB connection")
	}

	// Verify connection is working before proceeding with schema setup
	if err = db.Ping(); err != nil {
		return serr.Wrap(err, "failed to ping DuckDB")
	}

	// Create tables - using IF NOT EXISTS makes this idempotent,
	// safe to run on every startup without migration complexity
	if err = createTables(); err != nil {
		return serr.Wrap(err, "failed to create tables")
	}

	logger.Info("Database initialized successfully", "path", DBPath)
	return nil
}

// createTables executes DDL statements to set up the database schema.
// Each table creation is idempotent via IF NOT EXISTS clauses.
func createTables() error {
	_, err := db.Exec(CreateNotesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create notes table")
	}
	return nil
}

// CloseDB gracefully closes the database connection. Should be called
// during application shutdown, typically via defer after InitDB.
func CloseDB() error {
	if db != nil {
		if err := db.Close(); err != nil {
			return serr.Wrap(err, "failed to close database")
		}
		logger.Info("Database connection closed")
	}
	return nil
}

// DB returns the database connection for use in queries.
// Panics if called before InitDB - this is intentional as it
// indicates a programming error in application startup sequence.
func DB() *sql.DB {
	if db == nil {
		panic("database not initialized - call InitDB first")
	}
	return db
}

// InitTestDB initializes the database with a custom path for testing.
// This allows tests to use an isolated database without affecting
// production data. The path should include the full file path.
func InitTestDB(path string) error {
	var err error

	db, err = sql.Open("duckdb", path)
	if err != nil {
		return serr.Wrap(err, "failed to open test DuckDB connection")
	}

	if err = db.Ping(); err != nil {
		return serr.Wrap(err, "failed to ping test DuckDB")
	}

	if err = createTables(); err != nil {
		return serr.Wrap(err, "failed to create test tables")
	}

	return nil
}