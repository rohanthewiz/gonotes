package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"gonotes/models"
)

const migTestKey = "12345678901234567890123456789012" // 32 bytes

// legacyDDL is the pre-migration DuckDB schema (bundled statements, which
// DuckDB accepts) for the four durable tables the migration reads.
const legacyDDL = `
CREATE SEQUENCE users_id_seq START 1;
CREATE TABLE users (
  id BIGINT PRIMARY KEY DEFAULT nextval('users_id_seq'),
  guid VARCHAR NOT NULL UNIQUE, username VARCHAR NOT NULL UNIQUE, email VARCHAR,
  password_hash VARCHAR NOT NULL, display_name VARCHAR,
  is_active BOOLEAN DEFAULT true, is_admin BOOLEAN DEFAULT false,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  last_login_at TIMESTAMP);
CREATE SEQUENCE categories_id_seq START 1;
CREATE TABLE categories (
  id BIGINT PRIMARY KEY DEFAULT nextval('categories_id_seq'),
  guid VARCHAR, name VARCHAR NOT NULL, description VARCHAR, subcategories VARCHAR,
  created_by VARCHAR, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
CREATE SEQUENCE notes_id_seq START 1;
CREATE TABLE notes (
  id BIGINT PRIMARY KEY DEFAULT nextval('notes_id_seq'),
  guid VARCHAR NOT NULL UNIQUE, title VARCHAR NOT NULL, description VARCHAR, body VARCHAR, tags VARCHAR,
  is_private BOOLEAN DEFAULT false, is_flagged BOOLEAN DEFAULT false, encryption_iv VARCHAR,
  created_by VARCHAR, updated_by VARCHAR,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  authored_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, synced_at TIMESTAMP, deleted_at TIMESTAMP);
CREATE TABLE note_categories (
  note_id BIGINT NOT NULL, category_id BIGINT NOT NULL, subcategories VARCHAR,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (note_id, category_id));
`

// TestMigrationRoundTrip builds a legacy DuckDB with a user, a category, a
// public note, and an AES-encrypted private note plus a link, migrates it,
// and verifies the data arrives intact in the new bytdb databases — with
// the private body decrypted and the notes routed by privacy.
func TestMigrationRoundTrip(t *testing.T) {
	os.Setenv(models.EncryptionKeyEnvVar, migTestKey)
	defer os.Unsetenv(models.EncryptionKeyEnvVar)
	if err := models.InitEncryption(); err != nil {
		t.Fatalf("init encryption: %v", err)
	}
	defer models.ResetEncryption()

	// Encrypt the private note's body the way the legacy app did.
	const privateBody = "top secret migration content"
	cipher, iv, err := models.Encrypt(privateBody)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// --- Build the legacy DuckDB ---
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "notes.ddb")
	old, err := sql.Open("duckdb", srcPath)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	for _, stmt := range splitStmts(legacyDDL) {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatalf("legacy DDL %q: %v", stmt, err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	mustExec(t, old, `INSERT INTO users (id, guid, username, password_hash, is_active, is_admin, created_at, updated_at)
		VALUES (1, 'user-guid-1', 'alice', 'hash', true, true, ?, ?)`, now, now)
	mustExec(t, old, `INSERT INTO categories (id, guid, name, created_by, created_at, updated_at)
		VALUES (10, 'cat-guid-1', 'Work', 'user-guid-1', ?, ?)`, now, now)
	mustExec(t, old, `INSERT INTO notes (id, guid, title, body, is_private, created_by, updated_by, created_at, updated_at, authored_at)
		VALUES (100, 'note-pub-1', 'Public Note', 'plain public body', false, 'user-guid-1', 'user-guid-1', ?, ?, ?)`, now, now, now)
	mustExec(t, old, `INSERT INTO notes (id, guid, title, body, is_private, encryption_iv, created_by, updated_by, created_at, updated_at, authored_at)
		VALUES (101, 'note-priv-1', 'Private Note', ?, true, ?, 'user-guid-1', 'user-guid-1', ?, ?, ?)`, cipher, iv, now, now, now)
	mustExec(t, old, `INSERT INTO note_categories (note_id, category_id, created_at) VALUES (100, 10, ?)`, now)
	mustExec(t, old, `INSERT INTO note_categories (note_id, category_id, created_at) VALUES (101, 10, ?)`, now)
	old.Close()

	// --- Run the migration ---
	destDir := t.TempDir()
	if err := run(srcPath, destDir); err != nil {
		t.Fatalf("migration run: %v", err)
	}

	// --- Reopen the new databases and verify ---
	if err := models.InitTestDB(destDir); err != nil {
		t.Fatalf("reopen migrated DB: %v", err)
	}
	defer models.CloseDB()

	user, err := models.GetUserByUsername("alice")
	if err != nil || user == nil {
		t.Fatalf("migrated user missing: %v", err)
	}
	if user.GUID != "user-guid-1" || !user.IsAdmin {
		t.Errorf("user fields not preserved: %+v", user)
	}

	pub, err := models.GetNoteByGUID("note-pub-1")
	if err != nil || pub == nil {
		t.Fatalf("public note missing: %v", err)
	}
	if pub.IsPrivate || pub.Body.String != "plain public body" {
		t.Errorf("public note wrong: %+v", pub)
	}

	priv, err := models.GetNoteByGUID("note-priv-1")
	if err != nil || priv == nil {
		t.Fatalf("private note missing: %v", err)
	}
	if !priv.IsPrivate {
		t.Error("private note should be private")
	}
	if priv.Body.String != privateBody {
		t.Errorf("private note body should be decrypted plaintext. got %q want %q", priv.Body.String, privateBody)
	}
	if priv.ID < 1_000_000_000_000 {
		t.Errorf("private note id should be in the offset range, got %d", priv.ID)
	}

	// Category links resolve across the two databases.
	pubCats, err := models.GetNoteCategories(pub.ID, "user-guid-1")
	if err != nil || len(pubCats) != 1 || pubCats[0].Name != "Work" {
		t.Errorf("public note category link wrong: %v (%v)", pubCats, err)
	}
	privCats, err := models.GetNoteCategories(priv.ID, "user-guid-1")
	if err != nil || len(privCats) != 1 || privCats[0].Name != "Work" {
		t.Errorf("private note category link wrong: %v (%v)", privCats, err)
	}

	// The private body must not be on disk in plaintext.
	entries, _ := os.ReadDir(destDir)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(destDir, e.Name()))
		if len(data) > 0 && containsSub(data, privateBody) {
			t.Errorf("private body found in plaintext in %s — should be encrypted at rest", e.Name())
		}
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// splitStmts splits the bundled DDL on ';' into individual statements.
func splitStmts(ddl string) []string {
	var out []string
	cur := ""
	for _, r := range ddl {
		if r == ';' {
			if s := trimSpace(cur); s != "" {
				out = append(out, s)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if s := trimSpace(cur); s != "" {
		out = append(out, s)
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func containsSub(haystack []byte, needle string) bool {
	n := []byte(needle)
	if len(n) == 0 {
		return true
	}
	for i := 0; i+len(n) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(n); j++ {
			if haystack[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
