package models_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gonotes/models"
)

// encTestUserGUID is a constant user GUID used for encryption tests.
const encTestUserGUID = "enc-test-user-guid-001"

const encTestKey = "12345678901234567890123456789012" // exactly 32 bytes for AES-256

// --- Crypto helper unit tests (the Encrypt/Decrypt helpers are retained) ---

// TestEncryptDecrypt verifies basic encryption and decryption functionality.
func TestEncryptDecrypt(t *testing.T) {
	os.Setenv(models.EncryptionKeyEnvVar, encTestKey)
	defer os.Unsetenv(models.EncryptionKeyEnvVar)

	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to initialize encryption: %v", err)
	}
	defer models.ResetEncryption()

	testCases := []struct {
		name      string
		plaintext string
	}{
		{"simple text", "Hello, World!"},
		{"unicode content", "日本語テスト 🎉"},
		{"long text", "Lorem ipsum dolor sit amet, consectetur adipiscing elit."},
		{"special characters", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
		{"multiline", "Line 1\nLine 2\nLine 3"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ciphertext, iv, err := models.Encrypt(tc.plaintext)
			if err != nil {
				t.Fatalf("encryption failed: %v", err)
			}
			if ciphertext == tc.plaintext {
				t.Error("ciphertext should not equal plaintext")
			}
			if iv == "" {
				t.Error("IV should not be empty")
			}
			decrypted, err := models.Decrypt(ciphertext, iv)
			if err != nil {
				t.Fatalf("decryption failed: %v", err)
			}
			if decrypted != tc.plaintext {
				t.Errorf("decrypted text doesn't match original. got: %q, want: %q", decrypted, tc.plaintext)
			}
		})
	}
}

// TestEncryptionProducesUniqueIV verifies each encryption uses a fresh IV.
func TestEncryptionProducesUniqueIV(t *testing.T) {
	os.Setenv(models.EncryptionKeyEnvVar, encTestKey)
	defer os.Unsetenv(models.EncryptionKeyEnvVar)

	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to initialize encryption: %v", err)
	}
	defer models.ResetEncryption()

	ivSet := make(map[string]bool)
	for i := 0; i < 100; i++ {
		_, iv, err := models.Encrypt("Same content encrypted multiple times")
		if err != nil {
			t.Fatalf("encryption failed on iteration %d: %v", i, err)
		}
		if ivSet[iv] {
			t.Fatalf("duplicate IV detected on iteration %d — breaks GCM security", i)
		}
		ivSet[iv] = true
	}
}

// TestEncryptionNotInitialized verifies IsEncryptionEnabled reports false
// when no key is configured.
func TestEncryptionNotInitialized(t *testing.T) {
	models.ResetEncryption()
	os.Unsetenv(models.EncryptionKeyEnvVar)
	if models.IsEncryptionEnabled() {
		t.Error("IsEncryptionEnabled should return false when not initialized")
	}
}

// --- Whole-database at-rest encryption tests (the current model) ---

// dirContainsBytes reports whether any file under dir contains the given
// substring — used to prove a plaintext body is (not) present on disk.
func dirContainsBytes(t *testing.T, dir, needle string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read db dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("failed to read db file %s: %v", e.Name(), err)
		}
		if bytes.Contains(data, []byte(needle)) {
			return true
		}
	}
	return false
}

// TestPrivateNoteEncryptedAtRest verifies that a private note's body is
// ciphertext on disk (the private database is encrypted), while a public
// note's body is stored in plaintext.
func TestPrivateNoteEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	os.Setenv(models.EncryptionKeyEnvVar, encTestKey)
	defer os.Unsetenv(models.EncryptionKeyEnvVar)
	defer models.ResetEncryption()

	if err := models.InitTestDB(dir); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}

	const privateBody = "SECRET_MARKER_private_body_content_xyz"
	const publicBody = "PLAINTEXT_MARKER_public_body_content_xyz"

	pbody := privateBody
	if _, err := models.CreateNote(models.NoteInput{
		GUID: "enc-private-1", Title: "Private", Body: &pbody, IsPrivate: true,
	}, encTestUserGUID); err != nil {
		t.Fatalf("create private note: %v", err)
	}
	ubody := publicBody
	if _, err := models.CreateNote(models.NoteInput{
		GUID: "enc-public-1", Title: "Public", Body: &ubody, IsPrivate: false,
	}, encTestUserGUID); err != nil {
		t.Fatalf("create public note: %v", err)
	}

	// Flush to disk before inspecting the raw files.
	models.CloseDB()

	if dirContainsBytes(t, dir, privateBody) {
		t.Error("private note body appears in plaintext on disk — should be encrypted at rest")
	}
	if !dirContainsBytes(t, dir, publicBody) {
		t.Error("public note body should appear in plaintext on disk")
	}
}

// TestPrivateDBRequiresKeyOnReopen verifies the encrypted private database
// cannot be reopened without its key.
func TestPrivateDBRequiresKeyOnReopen(t *testing.T) {
	dir := t.TempDir()
	os.Setenv(models.EncryptionKeyEnvVar, encTestKey)
	if err := models.InitTestDB(dir); err != nil {
		t.Fatalf("InitTestDB (with key): %v", err)
	}
	body := "content in encrypted db"
	if _, err := models.CreateNote(models.NoteInput{
		GUID: "enc-reopen-1", Title: "Secret", Body: &body, IsPrivate: true,
	}, encTestUserGUID); err != nil {
		t.Fatalf("create private note: %v", err)
	}
	models.CloseDB()

	// Reopen without the key — the private engine must refuse to open.
	os.Unsetenv(models.EncryptionKeyEnvVar)
	models.ResetEncryption()
	if err := models.InitTestDB(dir); err == nil {
		models.CloseDB()
		t.Fatal("expected reopening the encrypted private database without a key to fail")
	}
	models.CloseDB()
}

// TestPrivacyFlipMovesNoteBetweenDatabases verifies that toggling a note's
// privacy moves it between the two databases while preserving its id.
func TestPrivacyFlipMovesNoteBetweenDatabases(t *testing.T) {
	dir := t.TempDir()
	os.Setenv(models.EncryptionKeyEnvVar, encTestKey)
	defer os.Unsetenv(models.EncryptionKeyEnvVar)
	defer models.ResetEncryption()

	if err := models.InitTestDB(dir); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	defer models.CloseDB()

	body := "content that flips privacy"
	note, err := models.CreateNote(models.NoteInput{
		GUID: "enc-flip-1", Title: "Flip", Body: &body, IsPrivate: true,
	}, encTestUserGUID)
	if err != nil {
		t.Fatalf("create private note: %v", err)
	}
	originalID := note.ID
	if originalID < 1_000_000_000_000 {
		t.Errorf("private note id should be in the offset range, got %d", originalID)
	}

	// Flip to public.
	updated, err := models.UpdateNote(originalID, models.NoteInput{
		GUID: "enc-flip-1", Title: "Flip", Body: &body, IsPrivate: false,
	}, encTestUserGUID)
	if err != nil {
		t.Fatalf("update note to public: %v", err)
	}
	if updated == nil {
		t.Fatal("update returned nil note")
	}
	if updated.ID != originalID {
		t.Errorf("note id should be preserved across privacy move: got %d, want %d", updated.ID, originalID)
	}
	if updated.IsPrivate {
		t.Error("note should now be public")
	}

	// It must still be retrievable by the same id and guid.
	got, err := models.GetNoteByID(originalID, encTestUserGUID)
	if err != nil {
		t.Fatalf("get note after flip: %v", err)
	}
	if got == nil || got.GUID != "enc-flip-1" || got.IsPrivate {
		t.Errorf("note not correctly readable after privacy flip: %+v", got)
	}
}
