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
// Also runs migrations for schema changes (e.g., adding new columns).
func createTables() error {
	_, err := db.Exec(CreateNotesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create notes table")
	}

	// Migration: add authored_at column for existing databases
	// This column tracks when a person last created/updated a note (for peer-to-peer sync)
	_, err = db.Exec(`ALTER TABLE notes ADD COLUMN IF NOT EXISTS authored_at TIMESTAMP`)
	if err != nil {
		return serr.Wrap(err, "failed to add authored_at column")
	}

	// Initialize authored_at for existing notes that don't have it
	// Using updated_at as the best approximation of last human modification
	_, err = db.Exec(`UPDATE notes SET authored_at = updated_at WHERE authored_at IS NULL`)
	if err != nil {
		return serr.Wrap(err, "failed to initialize authored_at for existing notes")
	}

	_, err = db.Exec(CreateCategoriesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create categories table")
	}

	_, err = db.Exec(CreateNoteCategoriesTableSQL)
	if err != nil {
		return serr.Wrap(err, "failed to create note_categories table")
	}

	// Create note change tracking tables for peer-to-peer sync
	// Order matters: note_fragments first (referenced by note_changes)
	_, err = db.Exec(DDLCreateNoteFragmentsSequence)
	if err != nil {
		return serr.Wrap(err, "failed to create note_fragments sequence")
	}

	_, err = db.Exec(DDLCreateNoteFragmentsTable)
	if err != nil {
		return serr.Wrap(err, "failed to create note_fragments table")
	}

	// Create note_changes table (references note_fragments)
	_, err = db.Exec(DDLCreateNoteChangesSequence)
	if err != nil {
		return serr.Wrap(err, "failed to create note_changes sequence")
	}

	_, err = db.Exec(DDLCreateNoteChangesTable)
	if err != nil {
		return serr.Wrap(err, "failed to create note_changes table")
	}

	_, err = db.Exec(DDLCreateNoteChangesIndexNoteGUID)
	if err != nil {
		return serr.Wrap(err, "failed to create note_changes note_guid index")
	}

	_, err = db.Exec(DDLCreateNoteChangesIndexCreatedAt)
	if err != nil {
		return serr.Wrap(err, "failed to create note_changes created_at index")
	}

	// Create note_change_sync_peers table (references note_changes)
	_, err = db.Exec(DDLCreateNoteChangeSyncPeersTable)
	if err != nil {
		return serr.Wrap(err, "failed to create note_change_sync_peers table")
	}

	_, err = db.Exec(DDLCreateNoteChangeSyncPeersIndexPeerID)
	if err != nil {
		return serr.Wrap(err, "failed to create note_change_sync_peers peer_id index")
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
// Uses cache-specific schema that excludes authored_at (only needed on disk for sync).
func initCacheDB() error {
	var err error

	// Open in-memory DuckDB connection
	// Note: Empty string creates an in-memory database in go-duckdb
	cacheDB, err = sql.Open("duckdb", "")
	if err != nil {
		return serr.Wrap(err, "failed to open in-memory DuckDB connection")
	}

	// Verify connection is working
	if err = cacheDB.Ping(); err != nil {
		return serr.Wrap(err, "failed to ping in-memory DuckDB")
	}

	// Create tables in cache - uses cache schema without authored_at column
	_, err = cacheDB.Exec(CreateNotesCacheTableSQL)
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
//
// Encryption handling:
// - Private notes are stored encrypted on disk (body + encryption_iv)
// - When syncing to cache, we decrypt the body so cache has plaintext
// - This enables fast reads from cache without decryption overhead
func syncCacheFromDisk() error {
	// Query all notes from disk (including soft-deleted ones for complete sync)
	// Note: authored_at is read from disk but NOT inserted into cache (cache schema lacks it)
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at
		FROM notes
	`

	rows, err := db.Query(query)
	if err != nil {
		return serr.Wrap(err, "failed to query notes from disk")
	}
	defer rows.Close()

	// Insert each note into cache preserving the ID
	// Note: cache schema does not include authored_at column
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
			&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.AuthoredAt, &note.SyncedAt, &note.DeletedAt,
		)
		if err != nil {
			return serr.Wrap(err, "failed to scan note from disk")
		}

		// For private notes with encryption enabled, decrypt the body before caching
		// This keeps the cache in plaintext for fast reads
		cacheBody := note.Body
		if note.IsPrivate && IsEncryptionEnabled() && note.Body.Valid && note.EncryptionIV.Valid {
			decryptedBody, err := DecryptNoteBody(note.Body.String, note.EncryptionIV.String)
			if err != nil {
				// Log error but continue - corrupted notes shouldn't block entire sync
				// The note will have encrypted body in cache (readable but garbled)
				logger.LogErr(err, "failed to decrypt private note body during cache sync",
					"note_id", note.ID, "guid", note.GUID)
			} else {
				cacheBody = sql.NullString{String: decryptedBody, Valid: true}
			}
		}

		_, err = cacheDB.Exec(insertQuery,
			note.ID, note.GUID, note.Title, note.Description, cacheBody,
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

	// Note: Sequence syncing is not needed for the cache since all inserts
	// use explicit IDs from the disk database (source of truth)

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

	// Note: Sequence syncing is not needed for the cache since all inserts
	// use explicit IDs from the disk database (source of truth)

	return count, nil
}

// syncNoteCategoriesFromDisk loads all note-category relationships from the disk database into the cache.
// Includes the subcategories JSON array column for category/subcategory filtering.
func syncNoteCategoriesFromDisk() (int, error) {
	query := `
		SELECT note_id, category_id, subcategories, created_at
		FROM note_categories
	`

	rows, err := db.Query(query)
	if err != nil {
		return 0, serr.Wrap(err, "failed to query note_categories from disk")
	}
	defer rows.Close()

	insertQuery := `
		INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
		VALUES (?, ?, ?, ?)
	`

	count := 0
	for rows.Next() {
		var noteCategory NoteCategory

		err := rows.Scan(
			&noteCategory.NoteID, &noteCategory.CategoryID, &noteCategory.Subcategories, &noteCategory.CreatedAt,
		)
		if err != nil {
			return 0, serr.Wrap(err, "failed to scan note_category from disk")
		}

		_, err = cacheDB.Exec(insertQuery,
			noteCategory.NoteID, noteCategory.CategoryID, noteCategory.Subcategories, noteCategory.CreatedAt,
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