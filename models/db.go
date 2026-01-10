package models

import (
	"database/sql"

	_ "github.com/marcboeker/go-duckdb" // DuckDB driver registration
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// db holds the database connection pool for the disk-based database.
// This is the source of truth. Using a package-level variable allows for
// simple access across the models package while maintaining a single
// connection pool for the application lifecycle.
var db *sql.DB

// cacheDB holds the in-memory database connection used as a read cache.
// Read operations query this cache for better performance. Write operations
// update both the disk DB and this cache to keep them synchronized.
var cacheDB *sql.DB

// DBPath defines the location of the DuckDB database file.
// Stored in ./data/ to keep data separate from application code.
const DBPath = "./data/notes.ddb"

// InitDB establishes a connection to the DuckDB database and creates
// the required tables if they don't exist. This should be called once
// at application startup before any database operations.
// Also initializes the in-memory cache and synchronizes it with disk data.
func InitDB() error {
	var err error

	// Open connection to disk DuckDB. The driver will create the file if it
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

	logger.Info("Disk database initialized successfully", "path", DBPath)

	// Initialize in-memory cache database
	if err = initCacheDB(); err != nil {
		return serr.Wrap(err, "failed to initialize cache database")
	}

	// Synchronize cache with disk data
	if err = syncCacheFromDisk(); err != nil {
		return serr.Wrap(err, "failed to sync cache from disk")
	}

	logger.Info("In-memory cache initialized and synchronized")
	return nil
}

// createTables executes DDL statements to set up the database schema.
// Each table creation is idempotent via IF NOT EXISTS clauses.
func createTables() error {
	_, err := db.Exec(CreateNotesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create notes table")
	}

	_, err = db.Exec(CreateCategoriesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create categories table")
	}

	_, err = db.Exec(CreateNoteCategoriesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create note_categories table")
	}

	return nil
}

// CloseDB gracefully closes the database connections. Should be called
// during application shutdown, typically via defer after InitDB.
func CloseDB() error {
	var errs []error

	if cacheDB != nil {
		if err := cacheDB.Close(); err != nil {
			errs = append(errs, serr.Wrap(err, "failed to close cache database"))
		} else {
			logger.Info("Cache database connection closed")
		}
	}

	if db != nil {
		if err := db.Close(); err != nil {
			errs = append(errs, serr.Wrap(err, "failed to close database"))
		} else {
			logger.Info("Disk database connection closed")
		}
	}

	if len(errs) > 0 {
		return errs[0] // Return first error
	}
	return nil
}

// DB returns the disk database connection for use in write queries.
// Panics if called before InitDB - this is intentional as it
// indicates a programming error in application startup sequence.
func DB() *sql.DB {
	if db == nil {
		panic("database not initialized - call InitDB first")
	}
	return db
}

// CacheDB returns the in-memory cache database connection for use in read queries.
// Panics if called before InitDB - this is intentional as it
// indicates a programming error in application startup sequence.
func CacheDB() *sql.DB {
	if cacheDB == nil {
		panic("cache database not initialized - call InitDB first")
	}
	return cacheDB
}

// initCacheDB initializes the in-memory DuckDB database for caching.
// Creates the same schema as the disk database.
func initCacheDB() error {
	var err error

	// Open in-memory DuckDB connection
	cacheDB, err = sql.Open("duckdb", ":memory:")
	if err != nil {
		return serr.Wrap(err, "failed to open in-memory DuckDB connection")
	}

	// Verify connection is working
	if err = cacheDB.Ping(); err != nil {
		return serr.Wrap(err, "failed to ping in-memory DuckDB")
	}

	// Create tables in cache - we need to use cacheDB instead of db here
	_, err = cacheDB.Exec(CreateNotesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create notes table in cache")
	}

	_, err = cacheDB.Exec(CreateCategoriesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create categories table in cache")
	}

	_, err = cacheDB.Exec(CreateNoteCategoriesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create note_categories table in cache")
	}

	logger.Info("Cache database initialized")
	return nil
}

// syncCacheFromDisk loads all data from the disk database into the cache.
// This ensures the cache is up-to-date with the source of truth.
// Critical: We must preserve the exact IDs from disk to maintain consistency.
func syncCacheFromDisk() error {
	// Query all notes from disk (including soft-deleted ones for complete sync)
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
	`

	rows, err := db.Query(query)
	if err != nil {
		return serr.Wrap(err, "failed to query notes from disk")
	}
	defer rows.Close()

	// Insert each note into cache preserving the ID
	insertQuery := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, encryption_iv,
		                   created_by, updated_by, created_at, updated_at, synced_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	count := 0
	for rows.Next() {
		var note Note

		err := rows.Scan(
			&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
			&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
			&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.SyncedAt, &note.DeletedAt,
		)
		if err != nil {
			return serr.Wrap(err, "failed to scan note from disk")
		}

		_, err = cacheDB.Exec(insertQuery,
			note.ID, note.GUID, note.Title, note.Description, note.Body,
			note.Tags, note.IsPrivate, note.EncryptionIV, note.CreatedBy,
			note.UpdatedBy, note.CreatedAt, note.UpdatedAt, note.SyncedAt, note.DeletedAt,
		)
		if err != nil {
			return serr.Wrap(err, "failed to insert note into cache")
		}
		count++
	}

	if err = rows.Err(); err != nil {
		return serr.Wrap(err, "error iterating notes from disk")
	}

	// Sync the sequence to match the disk database's next value
	// Query the current sequence value from disk
	var nextVal int64
	err = db.QueryRow("SELECT nextval('notes_id_seq')").Scan(&nextVal)
	if err != nil {
		return serr.Wrap(err, "failed to get next sequence value from disk")
	}

	// Set the cache sequence to the same value
	// We need to set it to nextVal - 1 because we just consumed a value by calling nextval
	_, err = cacheDB.Exec("SELECT setval('notes_id_seq', ?)", nextVal-1)
	if err != nil {
		return serr.Wrap(err, "failed to sync sequence in cache")
	}

	logger.Info("Cache synchronized from disk", "notes_count", count)

	// Sync categories
	categoriesCount, err := syncCategoriesFromDisk()
	if err != nil {
		return serr.Wrap(err, "failed to sync categories from disk")
	}
	logger.Info("Categories synchronized from disk", "categories_count", categoriesCount)

	// Sync note_categories relationships
	noteCategoriesCount, err := syncNoteCategoriesFromDisk()
	if err != nil {
		return serr.Wrap(err, "failed to sync note_categories from disk")
	}
	logger.Info("Note-category relationships synchronized from disk", "relationships_count", noteCategoriesCount)

	return nil
}

// syncCategoriesFromDisk loads all categories from the disk database into the cache.
func syncCategoriesFromDisk() (int, error) {
	query := `
		SELECT id, name, description, subcategories, created_at, updated_at
		FROM categories
	`

	rows, err := db.Query(query)
	if err != nil {
		return 0, serr.Wrap(err, "failed to query categories from disk")
	}
	defer rows.Close()

	insertQuery := `
		INSERT INTO categories (id, name, description, subcategories, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	count := 0
	for rows.Next() {
		var category Category

		err := rows.Scan(
			&category.ID, &category.Name, &category.Description,
			&category.Subcategories, &category.CreatedAt, &category.UpdatedAt,
		)
		if err != nil {
			return 0, serr.Wrap(err, "failed to scan category from disk")
		}

		_, err = cacheDB.Exec(insertQuery,
			category.ID, category.Name, category.Description,
			category.Subcategories, category.CreatedAt, category.UpdatedAt,
		)
		if err != nil {
			return 0, serr.Wrap(err, "failed to insert category into cache")
		}
		count++
	}

	if err = rows.Err(); err != nil {
		return 0, serr.Wrap(err, "error iterating categories from disk")
	}

	// Sync the sequence
	var nextVal int64
	err = db.QueryRow("SELECT nextval('categories_id_seq')").Scan(&nextVal)
	if err != nil {
		return 0, serr.Wrap(err, "failed to get next sequence value for categories from disk")
	}

	_, err = cacheDB.Exec("SELECT setval('categories_id_seq', ?)", nextVal-1)
	if err != nil {
		return 0, serr.Wrap(err, "failed to sync categories sequence in cache")
	}

	return count, nil
}

// syncNoteCategoriesFromDisk loads all note-category relationships from the disk database into the cache.
func syncNoteCategoriesFromDisk() (int, error) {
	query := `
		SELECT note_id, category_id, created_at
		FROM note_categories
	`

	rows, err := db.Query(query)
	if err != nil {
		return 0, serr.Wrap(err, "failed to query note_categories from disk")
	}
	defer rows.Close()

	insertQuery := `
		INSERT INTO note_categories (note_id, category_id, created_at)
		VALUES (?, ?, ?)
	`

	count := 0
	for rows.Next() {
		var noteCategory NoteCategory

		err := rows.Scan(
			&noteCategory.NoteID, &noteCategory.CategoryID, &noteCategory.CreatedAt,
		)
		if err != nil {
			return 0, serr.Wrap(err, "failed to scan note_category from disk")
		}

		_, err = cacheDB.Exec(insertQuery,
			noteCategory.NoteID, noteCategory.CategoryID, noteCategory.CreatedAt,
		)
		if err != nil {
			return 0, serr.Wrap(err, "failed to insert note_category into cache")
		}
		count++
	}

	if err = rows.Err(); err != nil {
		return 0, serr.Wrap(err, "error iterating note_categories from disk")
	}

	return count, nil
}

// InitTestDB initializes the database with a custom path for testing.
// This allows tests to use an isolated database without affecting
// production data. The path should include the full file path.
// Also initializes the in-memory cache for testing.
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

	// Initialize cache for tests
	if err = initCacheDB(); err != nil {
		return serr.Wrap(err, "failed to initialize test cache database")
	}

	// Sync cache with disk (which should be empty for new tests)
	if err = syncCacheFromDisk(); err != nil {
		return serr.Wrap(err, "failed to sync test cache from disk")
	}

	return nil
}