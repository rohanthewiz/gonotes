package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"os"

	"github.com/rohanthewiz/serr"
)

// encryptionKey holds the AES-256 key loaded from the environment.
// Must be exactly 32 bytes for AES-256. Using a package-level variable
// allows one-time initialization at startup and efficient reuse.
var encryptionKey []byte

// EncryptionKeyEnvVar is the environment variable name for the encryption key.
// The key should be a 32-character string (256 bits) for AES-256 encryption.
const EncryptionKeyEnvVar = "GONOTES_ENCRYPTION_KEY"

// InitEncryption loads the encryption key from the environment.
// Call this at application startup before any encryption operations.
// Returns an error if the key is missing or invalid length.
//
// Design decision: We require an explicit key rather than generating one
// because the key must persist across application restarts to decrypt
// existing data. Generating a new key would make old encrypted notes
// unreadable.
func InitEncryption() error {
	keyStr := os.Getenv(EncryptionKeyEnvVar)
	if keyStr == "" {
		return serr.New("encryption key not set: environment variable " + EncryptionKeyEnvVar + " is required")
	}

	// AES-256 requires exactly 32 bytes (256 bits)
	if len(keyStr) != 32 {
		return serr.New("encryption key must be exactly 32 characters for AES-256, got " + string(rune(len(keyStr))))
	}

	encryptionKey = []byte(keyStr)
	return nil
}

// IsEncryptionEnabled returns true if the encryption key has been initialized.
// This allows graceful handling when encryption is not configured.
func IsEncryptionEnabled() bool {
	return len(encryptionKey) == 32
}

// ResetEncryption clears the encryption key. This is intended for testing only
// to ensure proper test isolation between encryption tests.
func ResetEncryption() {
	encryptionKey = nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns the ciphertext
// and the IV used for encryption. Both are base64-encoded for safe storage
// in database VARCHAR fields.
//
// AES-GCM provides both confidentiality and authenticity:
// - The ciphertext cannot be read without the key
// - Any tampering with the ciphertext is detected on decryption
//
// A fresh random IV (nonce) is generated for each encryption operation,
// which is critical for GCM security - never reuse an IV with the same key.
func Encrypt(plaintext string) (ciphertext string, iv string, err error) {
	if !IsEncryptionEnabled() {
		return "", "", serr.New("encryption not initialized: call InitEncryption first")
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", "", serr.Wrap(err, "failed to create AES cipher")
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", serr.Wrap(err, "failed to create GCM mode")
	}

	// Generate a random nonce (IV) for this encryption operation
	// GCM standard nonce size is 12 bytes
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", "", serr.Wrap(err, "failed to generate random nonce")
	}

	// Encrypt the plaintext - GCM appends an authentication tag
	plaintextBytes := []byte(plaintext)
	ciphertextBytes := gcm.Seal(nil, nonce, plaintextBytes, nil)

	// Base64 encode for safe storage in VARCHAR database columns
	ciphertext = base64.StdEncoding.EncodeToString(ciphertextBytes)
	iv = base64.StdEncoding.EncodeToString(nonce)

	return ciphertext, iv, nil
}

// Decrypt decrypts ciphertext that was encrypted with Encrypt.
// Both ciphertext and iv should be base64-encoded strings as returned
// by Encrypt.
//
// Returns an error if:
// - Encryption is not initialized
// - The base64 decoding fails
// - The ciphertext was tampered with (GCM authentication fails)
// - The IV doesn't match the one used for encryption
func Decrypt(ciphertext string, iv string) (plaintext string, err error) {
	if !IsEncryptionEnabled() {
		return "", serr.New("encryption not initialized: call InitEncryption first")
	}

	// Decode base64-encoded inputs
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", serr.Wrap(err, "failed to decode ciphertext from base64")
	}

	nonce, err := base64.StdEncoding.DecodeString(iv)
	if err != nil {
		return "", serr.Wrap(err, "failed to decode IV from base64")
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", serr.Wrap(err, "failed to create AES cipher")
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", serr.Wrap(err, "failed to create GCM mode")
	}

	// Decrypt and verify authenticity in one operation
	// GCM will return an error if the ciphertext was modified
	plaintextBytes, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", serr.Wrap(err, "decryption failed: ciphertext may be corrupted or tampered")
	}

	return string(plaintextBytes), nil
}

// EncryptNoteBody encrypts the body of a private note.
// Returns the encrypted body and IV, or empty strings if the body is nil/empty.
// This is a convenience wrapper around Encrypt for note-specific use.
func EncryptNoteBody(body *string) (encryptedBody string, iv string, err error) {
	if body == nil || *body == "" {
		return "", "", nil
	}
	return Encrypt(*body)
}

// DecryptNoteBody decrypts an encrypted note body using the stored IV.
// Returns the decrypted body, or empty string if inputs are empty.
// This is a convenience wrapper around Decrypt for note-specific use.
func DecryptNoteBody(encryptedBody string, iv string) (plaintext string, err error) {
	if encryptedBody == "" || iv == "" {
		return "", nil
	}
	return Decrypt(encryptedBody, iv)
}
