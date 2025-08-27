package models

import (
	"database/sql"
	"github.com/rohanthewiz/serr"
	"github.com/rohanthewiz/logger"
)

// migrateDB runs all migrations on a single database
func migrateDB(db *sql.DB) error {
	// Create sequences for auto-incrementing IDs in DuckDB
	sequences := []string{
		"CREATE SEQUENCE IF NOT EXISTS users_id_seq START 1",
		"CREATE SEQUENCE IF NOT EXISTS notes_id_seq START 1",
		"CREATE SEQUENCE IF NOT EXISTS sync_log_id_seq START 1",
	}
	
	for _, seqSQL := range sequences {
		if _, err := db.Exec(seqSQL); err != nil {
			logger.LogErr(err, "failed to create sequence", "sql", seqSQL)
			// Continue even if sequence exists
		}
	}
	
	// Create users table
	userTableSQL := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY DEFAULT nextval('users_id_seq'),
		guid VARCHAR(40) UNIQUE NOT NULL,
		email VARCHAR(255) UNIQUE,
		name VARCHAR(128),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_login_at TIMESTAMP,
		is_active BOOLEAN DEFAULT true
	)`
	
	if _, err := db.Exec(userTableSQL); err != nil {
		return serr.Wrap(err, "failed to create users table")
	}
	
	// Create notes table
	notesTableSQL := `
	CREATE TABLE IF NOT EXISTS notes (
		id INTEGER PRIMARY KEY DEFAULT nextval('notes_id_seq'),
		guid VARCHAR(40) UNIQUE NOT NULL,
		title VARCHAR(255) UNIQUE NOT NULL,
		description TEXT,
		body TEXT,
		tags TEXT,  -- JSON array of tags
		is_private BOOLEAN DEFAULT false,
		encryption_iv TEXT,
		created_by VARCHAR(40),
		updated_by VARCHAR(40),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		synced_at TIMESTAMP,
		deleted_at TIMESTAMP NULL
	)`
	
	if _, err := db.Exec(notesTableSQL); err != nil {
		return serr.Wrap(err, "failed to create notes table")
	}
	
	// Create note_users table for sharing/permissions
	noteUsersTableSQL := `
	CREATE TABLE IF NOT EXISTS note_users (
		note_guid VARCHAR(40) NOT NULL,
		user_guid VARCHAR(40) NOT NULL,
		permission VARCHAR(20) DEFAULT 'read',
		shared_by VARCHAR(40),
		shared_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (note_guid, user_guid)
	)`
	
	if _, err := db.Exec(noteUsersTableSQL); err != nil {
		return serr.Wrap(err, "failed to create note_users table")
	}
	
	// Create sessions table for authentication
	sessionsTableSQL := `
	CREATE TABLE IF NOT EXISTS sessions (
		id VARCHAR(40) PRIMARY KEY,
		user_guid VARCHAR(40) NOT NULL,
		data TEXT,
		expires_at TIMESTAMP NOT NULL
	)`
	
	if _, err := db.Exec(sessionsTableSQL); err != nil {
		return serr.Wrap(err, "failed to create sessions table")
	}
	
	// Create sync_log table for distributed sync tracking
	syncLogTableSQL := `
	CREATE TABLE IF NOT EXISTS sync_log (
		id INTEGER PRIMARY KEY DEFAULT nextval('sync_log_id_seq'),
		peer_guid VARCHAR(40) NOT NULL,
		sync_type VARCHAR(20),
		started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		completed_at TIMESTAMP,
		notes_synced INTEGER DEFAULT 0,
		status VARCHAR(20)
	)`
	
	if _, err := db.Exec(syncLogTableSQL); err != nil {
		return serr.Wrap(err, "failed to create sync_log table")
	}
	
	// Create indexes for better query performance
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_notes_title ON notes(title)",
		"CREATE INDEX IF NOT EXISTS idx_notes_tags ON notes(tags)",
		"CREATE INDEX IF NOT EXISTS idx_notes_updated ON notes(updated_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_notes_created_by ON notes(created_by)",
		"CREATE INDEX IF NOT EXISTS idx_notes_updated_by ON notes(updated_by)",
		"CREATE INDEX IF NOT EXISTS idx_note_users_user ON note_users(user_guid)",
		"CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)",
		"CREATE INDEX IF NOT EXISTS idx_sync_log_peer ON sync_log(peer_guid)",
	}
	
	for _, indexSQL := range indexes {
		if _, err := db.Exec(indexSQL); err != nil {
			logger.LogErr(err, "failed to create index", "sql", indexSQL)
			// Continue with other indexes even if one fails
		}
	}
	
	logger.Info("Database migration completed successfully")
	return nil
}

// CreateDefaultUser creates a default user for development
func CreateDefaultUser() error {
	userGUID := "default-user-guid"
	
	// Check if user already exists
	var count int
	err := QueryRowFromCache("SELECT COUNT(*) FROM users WHERE guid = ?", userGUID).Scan(&count)
	if err != nil {
		return serr.Wrap(err, "failed to check for default user")
	}
	
	if count > 0 {
		return nil // User already exists
	}
	
	// Create default user
	insertSQL := `
		INSERT INTO users (guid, email, name, is_active)
		VALUES (?, ?, ?, ?)
	`
	
	err = WriteThrough(insertSQL, userGUID, "user@example.com", "Default User", true)
	if err != nil {
		return serr.Wrap(err, "failed to create default user")
	}
	
	logger.Info("Created default user for development")
	return nil
}