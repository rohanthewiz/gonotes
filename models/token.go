package models

import (
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rohanthewiz/serr"
)

// JWT configuration constants
const (
	// TokenExpirationHours defines how long tokens remain valid (7 days)
	TokenExpirationHours = 24 * 7

	// TokenIssuer identifies the application that issued the token
	TokenIssuer = "gonotes"

	// JWTSecretEnvVar is the environment variable containing the signing key
	JWTSecretEnvVar = "GONOTES_JWT_SECRET"

	// MinSecretLength is the minimum acceptable length for the JWT secret
	MinSecretLength = 32
)

// jwtSecret holds the signing key loaded from environment
// This is set during InitJWT and used for all token operations
var jwtSecret []byte

// TokenClaims extends JWT standard claims with user-specific data.
// Using UserGUID instead of ID allows tokens to work across sync scenarios.
type TokenClaims struct {
	jwt.RegisteredClaims
	UserGUID string `json:"user_guid"`
	Username string `json:"username"`
}

// InitJWT loads the JWT signing key from environment.
// Must be called at application startup before any token operations.
// Generates a temporary key in development if not set.
func InitJWT() error {
	secret := os.Getenv(JWTSecretEnvVar)

	if secret == "" {
		// For development, generate a warning and use a default
		// In production, this should be a secure random string
		secret = "development-only-secret-do-not-use-in-production"
	}

	if len(secret) < MinSecretLength {
		return serr.New("JWT secret must be at least 32 characters")
	}

	jwtSecret = []byte(secret)
	return nil
}

// GenerateToken creates a signed JWT for the authenticated user.
// The token includes the user's GUID and username in the claims.
// Returns the signed token string or an error.
func GenerateToken(user *User) (string, error) {
	if len(jwtSecret) == 0 {
		return "", serr.New("JWT not initialized - call InitJWT first")
	}

	// Create claims with user information and expiration
	claims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    TokenIssuer,
			Subject:   user.GUID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * TokenExpirationHours)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
		UserGUID: user.GUID,
		Username: user.Username,
	}

	// Create token with claims
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Sign the token with our secret
	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		return "", serr.Wrap(err, "failed to sign token")
	}

	return tokenString, nil
}

// ValidateToken parses and validates a JWT token string.
// Returns the claims if valid, or an error if the token is
// expired, malformed, or has an invalid signature.
func ValidateToken(tokenString string) (*TokenClaims, error) {
	if len(jwtSecret) == 0 {
		return nil, serr.New("JWT not initialized - call InitJWT first")
	}

	// Parse the token with claims
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, serr.New("unexpected signing method")
		}
		return jwtSecret, nil
	})

	if err != nil {
		return nil, serr.Wrap(err, "failed to parse token")
	}

	// Extract and validate claims
	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid {
		return nil, serr.New("invalid token claims")
	}

	return claims, nil
}

// RefreshToken generates a new token if the current one is valid.
// This allows extending the session without requiring re-authentication.
// Returns a new token string or an error if the current token is invalid.
func RefreshToken(tokenString string) (string, error) {
	// Validate current token
	claims, err := ValidateToken(tokenString)
	if err != nil {
		return "", err
	}

	// Look up user to ensure they're still active
	user, err := GetUserByGUID(claims.UserGUID)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", serr.New("user not found")
	}
	if !user.IsActive {
		return "", serr.New("account is disabled")
	}

	// Generate new token
	return GenerateToken(user)
}

// GetTokenExpiration returns when a token expires.
// Useful for clients to know when to refresh.
func GetTokenExpiration(tokenString string) (time.Time, error) {
	claims, err := ValidateToken(tokenString)
	if err != nil {
		return time.Time{}, err
	}

	if claims.ExpiresAt == nil {
		return time.Time{}, serr.New("token has no expiration")
	}

	return claims.ExpiresAt.Time, nil
}
