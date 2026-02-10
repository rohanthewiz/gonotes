package models

import (
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rohanthewiz/serr"
	"golang.org/x/crypto/bcrypt"
)

// User represents an authenticated user in the system.
// Design choices:
// - GUID allows external references and sync across machines
// - PasswordHash uses bcrypt and is never exposed in JSON
// - IsActive enables soft account disabling without deletion
// - LastLoginAt tracks login activity for security auditing
type User struct {
	ID           int64          `json:"id"`
	GUID         string         `json:"guid"`
	Username     string         `json:"username"`
	Email        sql.NullString `json:"email"`
	PasswordHash string         `json:"-"` // Never exposed in JSON
	DisplayName  sql.NullString `json:"display_name"`
	IsActive     bool           `json:"is_active"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	LastLoginAt  sql.NullTime   `json:"last_login_at"`
}

// CreateUsersTableSQL returns the DDL for creating the users table.
// Design notes:
// - username and email both have UNIQUE constraints for login flexibility
// - is_active defaults to true for new accounts
// - Indexes on username and email for fast login lookups
const CreateUsersTableSQL = `
CREATE SEQUENCE IF NOT EXISTS users_id_seq START 1;

CREATE TABLE IF NOT EXISTS users (
    id            BIGINT PRIMARY KEY DEFAULT nextval('users_id_seq'),
    guid          VARCHAR NOT NULL UNIQUE,
    username      VARCHAR NOT NULL UNIQUE,
    email         VARCHAR UNIQUE,
    password_hash VARCHAR NOT NULL,
    display_name  VARCHAR,
    is_active     BOOLEAN DEFAULT true,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
`

// DropUsersTableSQL for testing and migration rollback
const DropUsersTableSQL = `
DROP TABLE IF EXISTS users;
DROP SEQUENCE IF EXISTS users_id_seq;
`

// UserRegisterInput contains the data required for user registration.
// Password is plaintext here; it will be hashed before storage.
type UserRegisterInput struct {
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
}

// UserLoginInput contains credentials for authentication
type UserLoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserOutput provides a JSON-friendly representation of a User.
// Excludes PasswordHash for security and converts NullString to pointers.
type UserOutput struct {
	ID          int64     `json:"id"`
	GUID        string    `json:"guid"`
	Username    string    `json:"username"`
	Email       *string   `json:"email,omitempty"`
	DisplayName *string   `json:"display_name,omitempty"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToOutput converts a User to UserOutput for API responses
func (u *User) ToOutput() UserOutput {
	output := UserOutput{
		ID:        u.ID,
		GUID:      u.GUID,
		Username:  u.Username,
		IsActive:  u.IsActive,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}

	if u.Email.Valid {
		output.Email = &u.Email.String
	}
	if u.DisplayName.Valid {
		output.DisplayName = &u.DisplayName.String
	}

	return output
}

// Password hashing configuration
// Cost of 12 provides good security while keeping login times reasonable (~250ms)
const bcryptCost = 12

// HashPassword creates a bcrypt hash of the plaintext password.
// Returns the hash string or an error if hashing fails.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", serr.Wrap(err, "failed to hash password")
	}
	return string(hash), nil
}

// CheckPassword verifies a plaintext password against its hash.
// Returns true if the password matches, false otherwise.
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePassword checks if a password meets security requirements.
// Currently requires minimum 8 characters.
// Returns an error describing the issue, nil if valid.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return serr.New("password must be at least 8 characters")
	}
	return nil
}

// ValidateUsername checks if a username is valid.
// Requires 3-50 characters, alphanumeric and underscores only.
func ValidateUsername(username string) error {
	if len(username) < 3 {
		return serr.New("username must be at least 3 characters")
	}
	if len(username) > 50 {
		return serr.New("username must be at most 50 characters")
	}
	// Allow alphanumeric and underscores
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return serr.New("username can only contain letters, numbers, and underscores")
		}
	}
	return nil
}

// CreateUser creates a new user in the database.
// Handles password hashing and GUID generation.
// Returns the created user or an error (including duplicate username/email).
func CreateUser(input UserRegisterInput) (*User, error) {
	// Validate inputs
	if err := ValidateUsername(input.Username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(input.Password); err != nil {
		return nil, err
	}

	// Hash password before storage
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	// Generate unique GUID
	userGUID := uuid.New().String()

	// Convert optional fields to NullString
	var email sql.NullString
	if input.Email != nil && *input.Email != "" {
		email = sql.NullString{String: *input.Email, Valid: true}
	}

	var displayName sql.NullString
	if input.DisplayName != nil && *input.DisplayName != "" {
		displayName = sql.NullString{String: *input.DisplayName, Valid: true}
	}

	// Insert into database
	query := `
		INSERT INTO users (guid, username, email, password_hash, display_name)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id, guid, username, email, password_hash, display_name, is_active,
		          created_at, updated_at, last_login_at
	`

	user := &User{}
	err = db.QueryRow(query, userGUID, input.Username, email, passwordHash, displayName).Scan(
		&user.ID, &user.GUID, &user.Username, &user.Email, &user.PasswordHash,
		&user.DisplayName, &user.IsActive, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if err != nil {
		// Check for unique constraint violations
		errStr := err.Error()
		if strings.Contains(errStr, "UNIQUE") || strings.Contains(errStr, "unique") || strings.Contains(errStr, "duplicate") {
			if strings.Contains(errStr, "username") {
				return nil, serr.New("username already exists")
			}
			if strings.Contains(errStr, "email") {
				return nil, serr.New("email already exists")
			}
			return nil, serr.New("username or email already exists")
		}
		return nil, serr.Wrap(err, "failed to create user")
	}

	return user, nil
}


// GetUserByUsername retrieves a user by their username.
// Returns nil, nil if user not found.
func GetUserByUsername(username string) (*User, error) {
	query := `
		SELECT id, guid, username, email, password_hash, display_name, is_active,
		       created_at, updated_at, last_login_at
		FROM users
		WHERE username = ?
	`

	user := &User{}
	err := db.QueryRow(query, username).Scan(
		&user.ID, &user.GUID, &user.Username, &user.Email, &user.PasswordHash,
		&user.DisplayName, &user.IsActive, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get user by username")
	}

	return user, nil
}

// GetUserByGUID retrieves a user by their GUID.
// Returns nil, nil if user not found.
func GetUserByGUID(guid string) (*User, error) {
	query := `
		SELECT id, guid, username, email, password_hash, display_name, is_active,
		       created_at, updated_at, last_login_at
		FROM users
		WHERE guid = ?
	`

	user := &User{}
	err := db.QueryRow(query, guid).Scan(
		&user.ID, &user.GUID, &user.Username, &user.Email, &user.PasswordHash,
		&user.DisplayName, &user.IsActive, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get user by GUID")
	}

	return user, nil
}

// GetUserByID retrieves a user by their ID.
// Returns nil, nil if user not found.
func GetUserByID(id int64) (*User, error) {
	query := `
		SELECT id, guid, username, email, password_hash, display_name, is_active,
		       created_at, updated_at, last_login_at
		FROM users
		WHERE id = ?
	`

	user := &User{}
	err := db.QueryRow(query, id).Scan(
		&user.ID, &user.GUID, &user.Username, &user.Email, &user.PasswordHash,
		&user.DisplayName, &user.IsActive, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get user by ID")
	}

	return user, nil
}

// UpdateLastLogin updates the last_login_at timestamp for a user.
// Called after successful authentication.
func UpdateLastLogin(userID int64) error {
	query := `UPDATE users SET last_login_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := db.Exec(query, userID)
	if err != nil {
		return serr.Wrap(err, "failed to update last login")
	}
	return nil
}

// AuthenticateUser validates credentials and returns the user if valid.
// Updates last_login_at on successful authentication.
// Returns nil if credentials are invalid or account is disabled.
func AuthenticateUser(input UserLoginInput) (*User, error) {
	user, err := GetUserByUsername(input.Username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil // User not found
	}

	// Check if account is active
	if !user.IsActive {
		return nil, serr.New("account is disabled")
	}

	// Verify password
	if !CheckPassword(input.Password, user.PasswordHash) {
		return nil, nil // Invalid password
	}

	// Update last login timestamp
	if err := UpdateLastLogin(user.ID); err != nil {
		// Log but don't fail authentication
		// logger.LogErr(err, "failed to update last login")
	}

	return user, nil
}

// IsFirstUser checks if there are any users in the database.
// Used to determine if we should migrate orphaned notes.
func IsFirstUser() (bool, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return false, serr.Wrap(err, "failed to count users")
	}
	return count == 0, nil
}

// MigrateOrphanedNotes assigns all notes with NULL created_by to the specified user.
// Should be called after the first user registers.
// Returns the count of migrated notes.
func MigrateOrphanedNotes(userGUID string) (int, error) {
	query := `
		UPDATE notes
		SET created_by = ?, updated_by = ?
		WHERE created_by IS NULL
	`

	result, err := db.Exec(query, userGUID, userGUID)
	if err != nil {
		return 0, serr.Wrap(err, "failed to migrate orphaned notes")
	}

	// Also update cache
	_, cacheErr := cacheDB.Exec(query, userGUID, userGUID)
	if cacheErr != nil {
		// Log but don't fail - disk is source of truth
		// logger.LogErr(cacheErr, "cache update failed for orphaned notes migration")
	}

	count, _ := result.RowsAffected()
	return int(count), nil
}
