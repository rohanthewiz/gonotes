package models

import (
	"os"
	"testing"
)

// TestValidateUsername tests username validation rules.
func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"valid alphanumeric", "johndoe", false},
		{"valid with underscore", "john_doe", false},
		{"valid with numbers", "user123", false},
		{"valid uppercase", "JohnDoe", false},
		{"valid minimum length", "abc", false},
		{"too short", "ab", true},
		{"empty", "", true},
		{"too long", string(make([]byte, 51)), true}, // 51 chars
		{"contains space", "john doe", true},
		{"contains at sign", "john@doe", true},
		{"contains hyphen", "john-doe", true},
		{"contains dot", "john.doe", true},
		{"contains special char", "john$doe", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.username)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUsername(%q) error = %v, wantErr %v", tt.username, err, tt.wantErr)
			}
		})
	}
}

// TestValidatePassword tests password validation rules.
func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid long password", "mysecurepassword", false},
		{"valid exactly 8 chars", "12345678", false},
		{"too short 7 chars", "1234567", true},
		{"empty", "", true},
		{"one char", "a", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePassword(%q) error = %v, wantErr %v", tt.password, err, tt.wantErr)
			}
		})
	}
}

// TestHashAndCheckPassword tests the bcrypt hash/check round-trip.
func TestHashAndCheckPassword(t *testing.T) {
	password := "my_secure_password_123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() unexpected error: %v", err)
	}

	// Hash should not be empty or equal to the plaintext
	if hash == "" {
		t.Error("HashPassword() returned empty hash")
	}
	if hash == password {
		t.Error("HashPassword() returned plaintext — not hashed")
	}

	// Correct password should match
	if !CheckPassword(password, hash) {
		t.Error("CheckPassword() returned false for correct password")
	}

	// Wrong password should not match
	if CheckPassword("wrong_password", hash) {
		t.Error("CheckPassword() returned true for wrong password")
	}
}

// TestHashPasswordProducesUniqueHashes verifies that the same plaintext
// produces different hashes (bcrypt uses random salt).
func TestHashPasswordProducesUniqueHashes(t *testing.T) {
	password := "same_password"

	hash1, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() unexpected error: %v", err)
	}

	hash2, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() unexpected error: %v", err)
	}

	if hash1 == hash2 {
		t.Error("HashPassword() produced identical hashes for the same input — salt may not be working")
	}

	// Both should still verify correctly
	if !CheckPassword(password, hash1) {
		t.Error("CheckPassword() failed for hash1")
	}
	if !CheckPassword(password, hash2) {
		t.Error("CheckPassword() failed for hash2")
	}
}

// TestTokenRoundTrip tests JWT generation and validation.
func TestTokenRoundTrip(t *testing.T) {
	// Initialize JWT with a test secret
	os.Setenv("GONOTES_JWT_SECRET", "test-secret-key-for-jwt-testing-minimum-32-chars")
	defer os.Unsetenv("GONOTES_JWT_SECRET")

	if err := InitJWT(); err != nil {
		t.Fatalf("InitJWT() unexpected error: %v", err)
	}

	user := &User{
		ID:       1,
		GUID:     "test-guid-12345",
		Username: "testuser",
		IsActive: true,
	}

	// Generate token
	tokenString, err := GenerateToken(user)
	if err != nil {
		t.Fatalf("GenerateToken() unexpected error: %v", err)
	}

	if tokenString == "" {
		t.Fatal("GenerateToken() returned empty token")
	}

	// Validate token
	claims, err := ValidateToken(tokenString)
	if err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}

	if claims.UserGUID != user.GUID {
		t.Errorf("claims.UserGUID = %q, want %q", claims.UserGUID, user.GUID)
	}
	if claims.Username != user.Username {
		t.Errorf("claims.Username = %q, want %q", claims.Username, user.Username)
	}
	if claims.Issuer != TokenIssuer {
		t.Errorf("claims.Issuer = %q, want %q", claims.Issuer, TokenIssuer)
	}
}

// TestValidateTokenRejectsInvalid verifies that tampered/garbage tokens fail validation.
func TestValidateTokenRejectsInvalid(t *testing.T) {
	os.Setenv("GONOTES_JWT_SECRET", "test-secret-key-for-jwt-testing-minimum-32-chars")
	defer os.Unsetenv("GONOTES_JWT_SECRET")

	if err := InitJWT(); err != nil {
		t.Fatalf("InitJWT() unexpected error: %v", err)
	}

	tests := []struct {
		name  string
		token string
	}{
		{"empty string", ""},
		{"garbage", "not.a.jwt"},
		{"truncated", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0"},
		{"wrong signature", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateToken(tt.token)
			if err == nil {
				t.Errorf("ValidateToken(%q) expected error, got nil", tt.token)
			}
		})
	}
}

// TestGetTokenExpiration verifies that token expiration time is in the future.
func TestGetTokenExpiration(t *testing.T) {
	os.Setenv("GONOTES_JWT_SECRET", "test-secret-key-for-jwt-testing-minimum-32-chars")
	defer os.Unsetenv("GONOTES_JWT_SECRET")

	if err := InitJWT(); err != nil {
		t.Fatalf("InitJWT() unexpected error: %v", err)
	}

	user := &User{
		ID:       1,
		GUID:     "exp-test-guid",
		Username: "expuser",
	}

	tokenString, err := GenerateToken(user)
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	expTime, err := GetTokenExpiration(tokenString)
	if err != nil {
		t.Fatalf("GetTokenExpiration() error: %v", err)
	}

	if expTime.IsZero() {
		t.Error("GetTokenExpiration() returned zero time")
	}
}
