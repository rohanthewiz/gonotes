package models

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Invite Token System
//
// Admins generate single-use invite tokens that new users redeem during
// registration. This replaces the shared GONOTES_REGISTRATION_SECRET for
// user onboarding — each token is cryptographically random, time-limited,
// and tracks who created and consumed it for auditing.
//
// Flow: Admin creates token → gives to user → user registers with token →
// token is marked as used and cannot be reused.
// ============================================================================

// defaultInviteExpiry controls how long an invite token remains valid.
// 72 hours gives enough time for the recipient to set up their spoke
// without leaving tokens dangling indefinitely.
const defaultInviteExpiry = 72 * time.Hour

// DDL for invite_tokens table — stores invite tokens with usage tracking
const DDLCreateInviteTokensSequence = `CREATE SEQUENCE IF NOT EXISTS invite_tokens_id_seq START 1;`

const DDLCreateInviteTokensTable = `
CREATE TABLE IF NOT EXISTS invite_tokens (
    id         BIGINT PRIMARY KEY DEFAULT nextval('invite_tokens_id_seq'),
    token      VARCHAR NOT NULL UNIQUE,
    created_by VARCHAR NOT NULL,
    used_by    VARCHAR,
    expires_at TIMESTAMP NOT NULL,
    used_at    TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

const DDLCreateInviteTokensIndex = `CREATE INDEX IF NOT EXISTS idx_invite_tokens_token ON invite_tokens(token);`

// InviteToken represents a row in the invite_tokens table.
type InviteToken struct {
	ID        int64          `json:"id"`
	Token     string         `json:"token"`
	CreatedBy string         `json:"created_by"`
	UsedBy    sql.NullString `json:"used_by"`
	ExpiresAt time.Time      `json:"expires_at"`
	UsedAt    sql.NullTime   `json:"used_at"`
	CreatedAt time.Time      `json:"created_at"`
}

// InviteTokenOutput provides a JSON-friendly representation for API responses.
type InviteTokenOutput struct {
	ID        int64      `json:"id"`
	Token     string     `json:"token"`
	CreatedBy string     `json:"created_by"`
	UsedBy    *string    `json:"used_by,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	IsExpired bool       `json:"is_expired"`
	IsUsed    bool       `json:"is_used"`
}

// ToOutput converts an InviteToken to its API-safe representation.
func (t *InviteToken) ToOutput() InviteTokenOutput {
	out := InviteTokenOutput{
		ID:        t.ID,
		Token:     t.Token,
		CreatedBy: t.CreatedBy,
		ExpiresAt: t.ExpiresAt,
		CreatedAt: t.CreatedAt,
		IsExpired: time.Now().After(t.ExpiresAt),
		IsUsed:    t.UsedBy.Valid,
	}
	if t.UsedBy.Valid {
		out.UsedBy = &t.UsedBy.String
	}
	if t.UsedAt.Valid {
		out.UsedAt = &t.UsedAt.Time
	}
	return out
}

// CreateInviteToken generates a new cryptographically random invite token.
// The token is 32 bytes (64 hex characters) — sufficient entropy to prevent
// brute-force guessing even without rate limiting.
func CreateInviteToken(createdByGUID string, expiresIn time.Duration) (*InviteToken, error) {
	if expiresIn <= 0 {
		expiresIn = defaultInviteExpiry
	}

	// Generate 32 bytes of cryptographic randomness → 64 hex chars
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, serr.Wrap(err, "failed to generate random token")
	}
	tokenStr := hex.EncodeToString(tokenBytes)

	expiresAt := time.Now().Add(expiresIn)

	query := `
		INSERT INTO invite_tokens (token, created_by, expires_at)
		VALUES (?, ?, ?)
		RETURNING id, token, created_by, used_by, expires_at, used_at, created_at
	`

	token := &InviteToken{}
	err := db.QueryRow(query, tokenStr, createdByGUID, expiresAt).Scan(
		&token.ID, &token.Token, &token.CreatedBy, &token.UsedBy,
		&token.ExpiresAt, &token.UsedAt, &token.CreatedAt,
	)
	if err != nil {
		return nil, serr.Wrap(err, "failed to create invite token")
	}

	return token, nil
}

// ValidateInviteToken checks that a token exists, has not been used, and has not expired.
// Returns the token record if valid, or an error describing why it's invalid.
func ValidateInviteToken(tokenStr string) (*InviteToken, error) {
	query := `
		SELECT id, token, created_by, used_by, expires_at, used_at, created_at
		FROM invite_tokens
		WHERE token = ?
	`

	token := &InviteToken{}
	err := db.QueryRow(query, tokenStr).Scan(
		&token.ID, &token.Token, &token.CreatedBy, &token.UsedBy,
		&token.ExpiresAt, &token.UsedAt, &token.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, serr.New("invalid invite token")
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to query invite token")
	}

	if token.UsedBy.Valid {
		return nil, serr.New("invite token has already been used")
	}
	if time.Now().After(token.ExpiresAt) {
		return nil, serr.New("invite token has expired")
	}

	return token, nil
}

// RedeemInviteToken marks a token as used by the specified user.
// Must be called after successful user registration to prevent reuse.
func RedeemInviteToken(tokenStr, usedByGUID string) error {
	query := `
		UPDATE invite_tokens
		SET used_by = ?, used_at = CURRENT_TIMESTAMP
		WHERE token = ? AND used_by IS NULL
	`

	result, err := db.Exec(query, usedByGUID, tokenStr)
	if err != nil {
		return serr.Wrap(err, "failed to redeem invite token")
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return serr.New("invite token not found or already used")
	}

	return nil
}

// ListInviteTokens returns all invite tokens created by a specific admin.
// Ordered by most recent first for the admin dashboard.
func ListInviteTokens(createdByGUID string) ([]InviteTokenOutput, error) {
	query := `
		SELECT id, token, created_by, used_by, expires_at, used_at, created_at
		FROM invite_tokens
		WHERE created_by = ?
		ORDER BY created_at DESC
	`

	rows, err := db.Query(query, createdByGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list invite tokens")
	}
	defer rows.Close()

	var tokens []InviteTokenOutput
	for rows.Next() {
		token := &InviteToken{}
		err := rows.Scan(
			&token.ID, &token.Token, &token.CreatedBy, &token.UsedBy,
			&token.ExpiresAt, &token.UsedAt, &token.CreatedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan invite token")
		}
		tokens = append(tokens, token.ToOutput())
	}

	if err = rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating invite tokens")
	}

	return tokens, nil
}
