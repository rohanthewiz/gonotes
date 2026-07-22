package models

import (
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rohanthewiz/serr"
	"golang.org/x/crypto/bcrypt"
)

// User represents an authenticated user. Users are a shared/system table
// and live only in the public database (see schema.go).
type User struct {
	ID           int64          `json:"id"`
	GUID         string         `json:"guid"`
	Username     string         `json:"username"`
	Email        sql.NullString `json:"email"`
	PasswordHash string         `json:"-"` // Never exposed in JSON
	DisplayName  sql.NullString `json:"display_name"`
	IsActive     bool           `json:"is_active"`
	IsAdmin      bool           `json:"is_admin"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	LastLoginAt  sql.NullTime   `json:"last_login_at"`
}

// userCols is the canonical users projection, shared by every user
// SELECT so it stays in lockstep with scanUser.
const userCols = `id, guid, username, email, password_hash, display_name, is_active, is_admin,
	created_at, updated_at, last_login_at`

func scanUser(s scanner, u *User) error {
	return s.Scan(
		&u.ID, &u.GUID, &u.Username, &u.Email, &u.PasswordHash,
		&u.DisplayName, &u.IsActive, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
	)
}

// UserRegisterInput contains the data required for user registration.
type UserRegisterInput struct {
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
}

// UserLoginInput contains credentials for authentication.
type UserLoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserOutput provides a JSON-friendly representation of a User.
type UserOutput struct {
	ID          int64     `json:"id"`
	GUID        string    `json:"guid"`
	Username    string    `json:"username"`
	Email       *string   `json:"email,omitempty"`
	DisplayName *string   `json:"display_name,omitempty"`
	IsActive    bool      `json:"is_active"`
	IsAdmin     bool      `json:"is_admin"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToOutput converts a User to UserOutput for API responses.
func (u *User) ToOutput() UserOutput {
	output := UserOutput{
		ID:        u.ID,
		GUID:      u.GUID,
		Username:  u.Username,
		IsActive:  u.IsActive,
		IsAdmin:   u.IsAdmin,
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

// bcryptCost of 12 balances security against login latency (~250ms).
const bcryptCost = 12

// HashPassword creates a bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", serr.Wrap(err, "failed to hash password")
	}
	return string(hash), nil
}

// CheckPassword verifies a plaintext password against its hash.
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePassword checks that a password meets security requirements.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return serr.New("password must be at least 8 characters")
	}
	return nil
}

// ValidateUsername checks that a username is valid (3-50 chars,
// alphanumeric and underscores only).
func ValidateUsername(username string) error {
	if len(username) < 3 {
		return serr.New("username must be at least 3 characters")
	}
	if len(username) > 50 {
		return serr.New("username must be at most 50 characters")
	}
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return serr.New("username can only contain letters, numbers, and underscores")
		}
	}
	return nil
}

// CreateUser creates a new user (password hashed, GUID generated) in the
// public database. The first user automatically becomes admin.
func CreateUser(input UserRegisterInput) (*User, error) {
	if err := ValidateUsername(input.Username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(input.Password); err != nil {
		return nil, err
	}

	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	userGUID := uuid.New().String()

	var email sql.NullString
	if input.Email != nil && *input.Email != "" {
		email = sql.NullString{String: *input.Email, Valid: true}
	}
	var displayName sql.NullString
	if input.DisplayName != nil && *input.DisplayName != "" {
		displayName = sql.NullString{String: *input.DisplayName, Valid: true}
	}

	// First user automatically becomes admin — the only way to bootstrap
	// admin access; subsequent users are promoted via invite tokens or DB.
	isFirst, firstErr := IsFirstUser()
	if firstErr != nil {
		return nil, serr.Wrap(firstErr, "failed to check if first user")
	}
	isAdmin := isFirst

	query := `
		INSERT INTO users (id, guid, username, email, password_hash, display_name, is_admin)
		VALUES (nextval('users_id_seq'), ?, ?, ?, ?, ?, ?)
		RETURNING ` + userCols

	user := &User{}
	err = scanUser(pubDB.QueryRow(query, userGUID, input.Username, email, passwordHash, displayName, isAdmin), user)
	if err != nil {
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

// GetUserByUsername retrieves a user by username. Returns nil, nil if not
// found.
func GetUserByUsername(username string) (*User, error) {
	user := &User{}
	err := scanUser(pubDB.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, username), user)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get user by username")
	}
	return user, nil
}

// GetUserByGUID retrieves a user by GUID. Returns nil, nil if not found.
func GetUserByGUID(guid string) (*User, error) {
	user := &User{}
	err := scanUser(pubDB.QueryRow(`SELECT `+userCols+` FROM users WHERE guid = ?`, guid), user)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get user by GUID")
	}
	return user, nil
}

// GetUserByID retrieves a user by id. Returns nil, nil if not found.
func GetUserByID(id int64) (*User, error) {
	user := &User{}
	err := scanUser(pubDB.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id), user)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get user by ID")
	}
	return user, nil
}

// UpdateLastLogin updates the last_login_at timestamp for a user.
func UpdateLastLogin(userID int64) error {
	_, err := pubDB.Exec(`UPDATE users SET last_login_at = CURRENT_TIMESTAMP WHERE id = ?`, userID)
	if err != nil {
		return serr.Wrap(err, "failed to update last login")
	}
	return nil
}

// AuthenticateUser validates credentials and returns the user if valid,
// updating last_login_at. Returns nil for invalid credentials or a
// disabled account.
func AuthenticateUser(input UserLoginInput) (*User, error) {
	user, err := GetUserByUsername(input.Username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil // User not found
	}
	if !user.IsActive {
		return nil, serr.New("account is disabled")
	}
	if !CheckPassword(input.Password, user.PasswordHash) {
		return nil, nil // Invalid password
	}
	if err := UpdateLastLogin(user.ID); err != nil {
		// Log but don't fail authentication.
		_ = err
	}
	return user, nil
}

// IsFirstUser reports whether there are no users yet. Used to decide
// whether to migrate orphaned notes/categories to the first registrant.
func IsFirstUser() (bool, error) {
	var count int
	err := pubDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return false, serr.Wrap(err, "failed to count users")
	}
	return count == 0, nil
}

// MigrateOrphanedCategories assigns categories with NULL created_by to the
// given user (categories live only in the public database).
func MigrateOrphanedCategories(userGUID string) (int, error) {
	result, err := pubDB.Exec(`UPDATE categories SET created_by = ? WHERE created_by IS NULL`, userGUID)
	if err != nil {
		return 0, serr.Wrap(err, "failed to migrate orphaned categories")
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

// MigrateOrphanedNotes assigns notes with NULL created_by to the given
// user. Notes are spread across both databases, so the update runs on
// each and the counts are summed.
func MigrateOrphanedNotes(userGUID string) (int, error) {
	var total int64
	for _, en := range []*dbEngine{pubDB, privDB} {
		result, err := en.Exec(`
			UPDATE notes SET created_by = ?, updated_by = ?
			WHERE created_by IS NULL`, userGUID, userGUID)
		if err != nil {
			return int(total), serr.Wrap(err, "failed to migrate orphaned notes")
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return int(total), nil
}
