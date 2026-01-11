package models_test

import (
	"database/sql"
	"os"
	"testing"

	"gonotes/models"
)

// setupEncryptionTestDB initializes a test database with encryption enabled
// The encryption key must be exactly 32 characters for AES-256
func setupEncryptionTestDB(t *testing.T) func() {
	t.Helper()

	// Reset encryption state from any previous tests
	models.ResetEncryption()

	// Set encryption key - exactly 32 bytes for AES-256
	os.Setenv("GONOTES_ENCRYPTION_KEY", "12345678901234567890123456789012")

	// Initialize encryption
	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to initialize encryption: %v", err)
	}

	// Remove existing test database files
	os.Remove("./test_encryption.ddb")
	os.Remove("./test_encryption.ddb.wal")

	// Initialize test database
	if err := models.InitTestDB("./test_encryption.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Return cleanup function
	return func() {
		models.CloseDB()
		os.Remove("./test_encryption.ddb")
		os.Remove("./test_encryption.ddb.wal")
		os.Unsetenv("GONOTES_ENCRYPTION_KEY")
		models.ResetEncryption()
	}
}

// TestEncryptDecrypt verifies basic encryption and decryption functionality
func TestEncryptDecrypt(t *testing.T) {
	// Set up encryption key
	os.Setenv("GONOTES_ENCRYPTION_KEY", "12345678901234567890123456789012")
	defer os.Unsetenv("GONOTES_ENCRYPTION_KEY")

	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to initialize encryption: %v", err)
	}

	testCases := []struct {
		name      string
		plaintext string
	}{
		{"simple text", "Hello, World!"},
		{"empty string", ""},
		{"unicode content", "日本語テスト 🎉"},
		{"long text", "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua."},
		{"special characters", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
		{"multiline", "Line 1\nLine 2\nLine 3"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plaintext == "" {
				// Empty string case - should return empty
				ciphertext, iv, err := models.Encrypt(tc.plaintext)
				if err != nil {
					t.Fatalf("encryption failed for empty string: %v", err)
				}
				// For empty input, we expect empty output
				if ciphertext != "" || iv != "" {
					t.Logf("empty string produced ciphertext: %s, iv: %s", ciphertext, iv)
				}
				return
			}

			// Encrypt
			ciphertext, iv, err := models.Encrypt(tc.plaintext)
			if err != nil {
				t.Fatalf("encryption failed: %v", err)
			}

			// Verify ciphertext is different from plaintext
			if ciphertext == tc.plaintext {
				t.Error("ciphertext should not equal plaintext")
			}

			// Verify IV is not empty
			if iv == "" {
				t.Error("IV should not be empty")
			}

			// Decrypt
			decrypted, err := models.Decrypt(ciphertext, iv)
			if err != nil {
				t.Fatalf("decryption failed: %v", err)
			}

			// Verify decrypted matches original
			if decrypted != tc.plaintext {
				t.Errorf("decrypted text doesn't match original. got: %q, want: %q", decrypted, tc.plaintext)
			}
		})
	}
}

// TestEncryptionProducesUniqueIV verifies that each encryption produces a unique IV
// This is critical for security - reusing IVs with the same key breaks GCM security
func TestEncryptionProducesUniqueIV(t *testing.T) {
	os.Setenv("GONOTES_ENCRYPTION_KEY", "12345678901234567890123456789012")
	defer os.Unsetenv("GONOTES_ENCRYPTION_KEY")

	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to initialize encryption: %v", err)
	}

	plaintext := "Same content encrypted multiple times"
	ivSet := make(map[string]bool)

	// Encrypt the same content 100 times
	for i := 0; i < 100; i++ {
		_, iv, err := models.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("encryption failed on iteration %d: %v", i, err)
		}

		if ivSet[iv] {
			t.Fatalf("duplicate IV detected on iteration %d - this breaks GCM security", i)
		}
		ivSet[iv] = true
	}
}

// TestPrivateNoteEncryptedOnDisk verifies that private notes have encrypted body on disk
// but unencrypted body in cache
func TestPrivateNoteEncryptedOnDisk(t *testing.T) {
	cleanup := setupEncryptionTestDB(t)
	defer cleanup()

	body := "This is secret content that should be encrypted on disk"
	input := models.NoteInput{
		GUID:      "private-test-001",
		Title:     "Private Note",
		Body:      &body,
		IsPrivate: true,
	}

	// Create private note
	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create private note: %v", err)
	}

	// Verify the note returned has unencrypted body (from cache)
	if !note.Body.Valid || note.Body.String != body {
		t.Errorf("returned note body should be unencrypted. got: %q, want: %q",
			note.Body.String, body)
	}

	// Verify encryption IV was set
	if !note.EncryptionIV.Valid || note.EncryptionIV.String == "" {
		t.Error("encryption IV should be set for private notes")
	}

	// Read directly from disk to verify encryption
	diskBody, diskIV := readNoteDirectFromDisk(t, note.ID)

	// Disk body should be different from plaintext (encrypted)
	if diskBody == body {
		t.Error("disk body should be encrypted, but it matches plaintext")
	}

	// Disk IV should match what was returned
	if diskIV != note.EncryptionIV.String {
		t.Errorf("disk IV doesn't match note IV. disk: %q, note: %q", diskIV, note.EncryptionIV.String)
	}

	// Verify we can decrypt the disk body using the IV
	decrypted, err := models.Decrypt(diskBody, diskIV)
	if err != nil {
		t.Fatalf("failed to decrypt disk body: %v", err)
	}

	if decrypted != body {
		t.Errorf("decrypted disk body doesn't match original. got: %q, want: %q", decrypted, body)
	}
}

// TestPublicNoteNotEncrypted verifies that public notes are NOT encrypted
func TestPublicNoteNotEncrypted(t *testing.T) {
	cleanup := setupEncryptionTestDB(t)
	defer cleanup()

	body := "This is public content that should NOT be encrypted"
	input := models.NoteInput{
		GUID:      "public-test-001",
		Title:     "Public Note",
		Body:      &body,
		IsPrivate: false,
	}

	// Create public note
	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create public note: %v", err)
	}

	// Verify no encryption IV
	if note.EncryptionIV.Valid && note.EncryptionIV.String != "" {
		t.Error("public note should not have encryption IV")
	}

	// Read directly from disk - should be plaintext
	diskBody, diskIV := readNoteDirectFromDisk(t, note.ID)

	if diskBody != body {
		t.Errorf("disk body should be plaintext for public notes. got: %q, want: %q", diskBody, body)
	}

	if diskIV != "" {
		t.Error("disk IV should be empty for public notes")
	}
}

// TestCacheContainsDecryptedContent verifies that cache reads return decrypted content
func TestCacheContainsDecryptedContent(t *testing.T) {
	cleanup := setupEncryptionTestDB(t)
	defer cleanup()

	body := "Secret content for cache test"
	input := models.NoteInput{
		GUID:      "cache-decrypt-test",
		Title:     "Cache Decrypt Test",
		Body:      &body,
		IsPrivate: true,
	}

	// Create private note
	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Read from cache (via GetNoteByID)
	cachedNote, err := models.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("failed to get note by ID: %v", err)
	}

	// Cache should have decrypted content
	if !cachedNote.Body.Valid || cachedNote.Body.String != body {
		t.Errorf("cached note body should be decrypted. got: %q, want: %q",
			cachedNote.Body.String, body)
	}

	// Read by GUID should also return decrypted content
	cachedByGUID, err := models.GetNoteByGUID(input.GUID)
	if err != nil {
		t.Fatalf("failed to get note by GUID: %v", err)
	}

	if !cachedByGUID.Body.Valid || cachedByGUID.Body.String != body {
		t.Errorf("cached note (by GUID) body should be decrypted. got: %q, want: %q",
			cachedByGUID.Body.String, body)
	}
}

// TestUpdatePrivateNoteReencrypts verifies that updating a private note generates new encryption
func TestUpdatePrivateNoteReencrypts(t *testing.T) {
	cleanup := setupEncryptionTestDB(t)
	defer cleanup()

	originalBody := "Original secret content"
	input := models.NoteInput{
		GUID:      "update-encrypt-test",
		Title:     "Update Encryption Test",
		Body:      &originalBody,
		IsPrivate: true,
	}

	// Create private note
	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	originalIV := note.EncryptionIV.String
	_, originalDiskIV := readNoteDirectFromDisk(t, note.ID)

	// Update the note with new content
	newBody := "Updated secret content"
	updateInput := models.NoteInput{
		GUID:      input.GUID,
		Title:     "Updated Title",
		Body:      &newBody,
		IsPrivate: true,
	}

	updatedNote, err := models.UpdateNote(note.ID, updateInput)
	if err != nil {
		t.Fatalf("failed to update note: %v", err)
	}

	// Verify the returned note has decrypted body
	if !updatedNote.Body.Valid || updatedNote.Body.String != newBody {
		t.Errorf("updated note body should be decrypted. got: %q, want: %q",
			updatedNote.Body.String, newBody)
	}

	// Verify new IV was generated (for security, each encryption should use new IV)
	newIV := updatedNote.EncryptionIV.String
	if newIV == originalIV {
		t.Error("update should generate new IV for security")
	}

	// Verify disk has new encrypted content with new IV
	_, newDiskIV := readNoteDirectFromDisk(t, note.ID)
	if newDiskIV == originalDiskIV {
		t.Error("disk IV should be different after update")
	}
}

// TestPrivateToPublicRemovesEncryption verifies that changing from private to public
// removes encryption (stores plaintext on disk)
func TestPrivateToPublicRemovesEncryption(t *testing.T) {
	cleanup := setupEncryptionTestDB(t)
	defer cleanup()

	body := "Content that will become public"
	input := models.NoteInput{
		GUID:      "private-to-public-test",
		Title:     "Private to Public Test",
		Body:      &body,
		IsPrivate: true,
	}

	// Create private note
	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Verify it's encrypted on disk
	diskBody, _ := readNoteDirectFromDisk(t, note.ID)
	if diskBody == body {
		t.Error("private note should be encrypted on disk")
	}

	// Update to public
	updateInput := models.NoteInput{
		GUID:      input.GUID,
		Title:     "Now Public",
		Body:      &body,
		IsPrivate: false, // Now public
	}

	_, err = models.UpdateNote(note.ID, updateInput)
	if err != nil {
		t.Fatalf("failed to update note: %v", err)
	}

	// Verify disk now has plaintext
	diskBodyAfter, diskIVAfter := readNoteDirectFromDisk(t, note.ID)
	if diskBodyAfter != body {
		t.Errorf("public note should have plaintext on disk. got: %q, want: %q", diskBodyAfter, body)
	}

	if diskIVAfter != "" {
		t.Error("public note should not have encryption IV on disk")
	}
}

// TestCacheSyncDecryptsOnRestart simulates application restart by closing
// and reinitializing the database, verifying that encrypted notes are
// properly decrypted when synced to cache
func TestCacheSyncDecryptsOnRestart(t *testing.T) {
	// Set encryption key
	os.Setenv("GONOTES_ENCRYPTION_KEY", "12345678901234567890123456789012")
	defer os.Unsetenv("GONOTES_ENCRYPTION_KEY")

	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to initialize encryption: %v", err)
	}

	// Remove existing test database
	os.Remove("./test_restart.ddb")
	os.Remove("./test_restart.ddb.wal")
	defer func() {
		models.CloseDB()
		os.Remove("./test_restart.ddb")
		os.Remove("./test_restart.ddb.wal")
	}()

	// Initialize first "session"
	if err := models.InitTestDB("./test_restart.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	body := "Secret content for restart test"
	input := models.NoteInput{
		GUID:      "restart-test-001",
		Title:     "Restart Test",
		Body:      &body,
		IsPrivate: true,
	}

	// Create private note
	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	noteID := note.ID

	// Simulate application restart - close and reopen
	models.CloseDB()

	// Reinitialize encryption (simulating fresh start)
	if err := models.InitEncryption(); err != nil {
		t.Fatalf("failed to reinitialize encryption: %v", err)
	}

	// Reinitialize database (will sync cache from disk)
	if err := models.InitTestDB("./test_restart.ddb"); err != nil {
		t.Fatalf("failed to reinitialize test database: %v", err)
	}

	// Read note - should be decrypted in cache after sync
	retrievedNote, err := models.GetNoteByID(noteID)
	if err != nil {
		t.Fatalf("failed to get note after restart: %v", err)
	}

	if retrievedNote == nil {
		t.Fatal("note should exist after restart")
	}

	// Verify body is decrypted
	if !retrievedNote.Body.Valid || retrievedNote.Body.String != body {
		t.Errorf("note body should be decrypted after restart. got: %q, want: %q",
			retrievedNote.Body.String, body)
	}
}

// TestEncryptionNotInitialized verifies proper error handling when encryption
// is not initialized
func TestEncryptionNotInitialized(t *testing.T) {
	// Reset encryption state and ensure key is not set
	models.ResetEncryption()
	os.Unsetenv("GONOTES_ENCRYPTION_KEY")

	// IsEncryptionEnabled should return false
	if models.IsEncryptionEnabled() {
		t.Error("IsEncryptionEnabled should return false when not initialized")
	}
}

// readNoteDirectFromDisk reads a note directly from the disk database,
// bypassing the cache. This is used to verify encryption is actually
// happening on disk.
//
// Uses the existing disk DB connection (models.DB()) to avoid DuckDB
// connection isolation issues where a new connection might not see
// uncommitted writes.
func readNoteDirectFromDisk(t *testing.T, id int64) (body string, iv string) {
	t.Helper()

	var bodyNull, ivNull sql.NullString
	err := models.DB().QueryRow(
		"SELECT body, encryption_iv FROM notes WHERE id = ?", id,
	).Scan(&bodyNull, &ivNull)
	if err != nil {
		t.Fatalf("failed to query note from disk: %v", err)
	}

	if bodyNull.Valid {
		body = bodyNull.String
	}
	if ivNull.Valid {
		iv = ivNull.String
	}

	return body, iv
}
