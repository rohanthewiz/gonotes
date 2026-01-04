package models

import (
	"database/sql"
	_ "github.com/marcboeker/go-duckdb"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
	"sync"
	"time"
)

var (
	memDB  *sql.DB      // In-memory cache for fast reads
	diskDB *sql.DB      // Persistent storage
	dbMu   sync.RWMutex // Protect concurrent access during writes
)

// InitDB initializes both in-memory and disk-based databases
func InitDB() error {
	var err error

	// Initialize disk-based database for persistence
	diskDB, err = sql.Open("duckdb", "./data/notes.db")
	if err != nil {
		return serr.Wrap(err, "failed to open disk database")
	}

	// Initialize in-memory database for fast queries
	// DuckDB's go driver uses empty string or ":memory:" for in-memory databases
	memDB, err = sql.Open("duckdb", "")
	if err != nil {
		return serr.Wrap(err, "failed to open memory database")
	}

	// Run migrations on both databases
	if err := migrateBoth(); err != nil {
		return serr.Wrap(err, "failed to migrate databases")
	}

	// Load existing data from disk to memory
	if err := syncDiskToMemory(); err != nil {
		return serr.Wrap(err, "failed to sync data to memory")
	}

	// Start background sync worker for periodic consistency checks
	go startSyncWorker()

	return nil
}

// CloseDB closes both database connections
func CloseDB() {
	if memDB != nil {
		memDB.Close()
	}
	if diskDB != nil {
		diskDB.Close()
	}
}

// migrateBoth runs migrations on both databases
func migrateBoth() error {
	// Run migration on disk DB
	if err := migrateDB(diskDB); err != nil {
		return serr.Wrap(err, "disk migration failed")
	}

	// Run migration on memory DB
	if err := migrateDB(memDB); err != nil {
		return serr.Wrap(err, "memory migration failed")
	}

	return nil
}

// syncDiskToMemory loads all data from disk into memory cache
func syncDiskToMemory() error {
	// Use ATTACH to efficiently copy data
	query := `
		ATTACH './data/notes.db' AS disk_db;
		INSERT OR IGNORE INTO notes SELECT * FROM disk_db.notes;
		INSERT OR IGNORE INTO users SELECT * FROM disk_db.users;
		INSERT OR IGNORE INTO note_users SELECT * FROM disk_db.note_users;
		INSERT OR IGNORE INTO sessions SELECT * FROM disk_db.sessions;
		DETACH disk_db;
	`

	_, err := memDB.Exec(query)
	if err != nil {
		// If attach doesn't work, fall back to manual copy
		logger.LogErr(err, "ATTACH failed, falling back to manual sync")
		return manualSync()
	}

	logger.Info("Successfully synced disk data to memory cache")
	return nil
}

// manualSync performs manual table-by-table sync
func manualSync() error {
	tables := []string{"users", "notes", "note_users", "sessions"}

	for _, table := range tables {
		// Check if table exists in disk DB
		var count int
		err := diskDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count == 0 {
			logger.Debug("Table does not exist yet, skipping", "table", table)
			continue
		}

		// Read from disk
		rows, err := diskDB.Query("SELECT * FROM " + table)
		if err != nil {
			logger.LogErr(err, "failed to read from disk", "table", table)
			continue
		}

		// Get column names
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			continue
		}

		// Prepare insert statement for memory DB
		placeholders := ""
		for i := range cols {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
		}

		stmt, err := memDB.Prepare(
			"INSERT OR IGNORE INTO " + table + " VALUES (" + placeholders + ")")
		if err != nil {
			rows.Close()
			logger.LogErr(err, "failed to prepare insert", "table", table)
			continue
		}

		// Copy rows
		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		for rows.Next() {
			if err := rows.Scan(valuePtrs...); err != nil {
				continue
			}
			if _, err := stmt.Exec(values...); err != nil {
				logger.LogErr(err, "failed to insert into memory", "table", table)
			}
		}

		stmt.Close()
		rows.Close()
	}

	return nil
}

// WriteThrough writes to both databases ensuring consistency
func WriteThrough(query string, args ...interface{}) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	// Write to disk first for durability
	_, err := diskDB.Exec(query, args...)
	if err != nil {
		return serr.Wrap(err, "failed to write to disk")
	}

	// Then update memory cache
	_, err = memDB.Exec(query, args...)
	if err != nil {
		// Log error but don't fail - disk write succeeded
		logger.LogErr(err, "failed to update memory cache")
		// Mark cache as dirty for resync
		markCacheDirty()
	}

	return nil
}

// ReadFromCache performs fast reads from memory
func ReadFromCache(query string, args ...interface{}) (*sql.Rows, error) {
	dbMu.RLock()
	defer dbMu.RUnlock()

	rows, err := memDB.Query(query, args...)
	if err != nil {
		// Fallback to disk on cache miss
		logger.LogErr(err, "cache read failed, falling back to disk")
		return diskDB.Query(query, args...)
	}

	return rows, nil
}

// QueryRowFromCache performs single row query from cache
func QueryRowFromCache(query string, args ...interface{}) *sql.Row {
	dbMu.RLock()
	defer dbMu.RUnlock()

	return memDB.QueryRow(query, args...)
}

// Transaction wrapper for dual-database writes
type DualTx struct {
	diskTx    *sql.Tx
	memTx     *sql.Tx
	committed bool // Track if transaction was committed to prevent double unlock
}

// BeginDualTx starts a transaction on both databases
func BeginDualTx() (*DualTx, error) {
	dbMu.Lock()

	diskTx, err := diskDB.Begin()
	if err != nil {
		dbMu.Unlock()
		return nil, serr.Wrap(err, "failed to begin disk transaction")
	}

	memTx, err := memDB.Begin()
	if err != nil {
		diskTx.Rollback()
		dbMu.Unlock()
		return nil, serr.Wrap(err, "failed to begin memory transaction")
	}

	return &DualTx{
		diskTx: diskTx,
		memTx:  memTx,
	}, nil
}

// Exec executes query on both transactions
func (dt *DualTx) Exec(query string, args ...interface{}) error {
	// Execute on disk first
	if _, err := dt.diskTx.Exec(query, args...); err != nil {
		return err
	}

	// Then on memory
	if _, err := dt.memTx.Exec(query, args...); err != nil {
		// Log but don't fail
		logger.LogErr(err, "memory tx exec failed")
	}

	return nil
}

// Commit commits both transactions
func (dt *DualTx) Commit() error {
	// Ensure we only unlock once and mark as committed
	defer func() {
		dt.committed = true
		dbMu.Unlock()
	}()

	// Commit disk first
	if err := dt.diskTx.Commit(); err != nil {
		dt.memTx.Rollback()
		return serr.Wrap(err, "failed to commit disk transaction")
	}

	// Then memory
	if err := dt.memTx.Commit(); err != nil {
		logger.LogErr(err, "failed to commit memory transaction")
		markCacheDirty()
	}

	return nil
}

// Rollback rolls back both transactions
func (dt *DualTx) Rollback() error {
	// Only unlock if we haven't committed (Commit unlocks the mutex)
	if !dt.committed {
		defer dbMu.Unlock()
	}

	dt.diskTx.Rollback()
	dt.memTx.Rollback()

	return nil
}

// Cache management
var (
	cacheDirty bool
	cacheMu    sync.Mutex
)

func markCacheDirty() {
	cacheMu.Lock()
	cacheDirty = true
	cacheMu.Unlock()
}

func isCacheDirty() bool {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return cacheDirty
}

// startSyncWorker periodically checks cache consistency
func startSyncWorker() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		if isCacheDirty() {
			logger.Info("Cache marked dirty, resyncing...")
			if err := resyncCache(); err != nil {
				logger.LogErr(err, "failed to resync cache")
			} else {
				cacheMu.Lock()
				cacheDirty = false
				cacheMu.Unlock()
			}
		}
	}
}

// resyncCache rebuilds the memory cache from disk
func resyncCache() error {
	dbMu.Lock()
	defer dbMu.Unlock()

	// Clear memory database
	tables := []string{"notes", "users", "note_users", "sessions"}
	for _, table := range tables {
		_, _ = memDB.Exec("DELETE FROM " + table)
	}

	// Reload from disk
	return manualSync()
}
