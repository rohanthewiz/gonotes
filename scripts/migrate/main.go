// Command migrate moves GoNotes data off the legacy single-file DuckDB
// database (data/notes.ddb) into the two new bytdb databases
// (data/notes_public.bytdb and data/notes_private.bytdb).
//
// What it does:
//   - Reads users, categories, notes, and note⇄category links from DuckDB.
//   - Writes them into the new bytdb databases, routing each note to the
//     public or private (encrypted) database by its is_private flag.
//   - Decrypts any legacy per-note AES ciphertext (using
//     GONOTES_ENCRYPTION_KEY) before storing, because bytdb now encrypts the
//     private database as a whole rather than per note.
//   - Draws fresh integer ids from the new databases' sequences and remaps
//     foreign keys, so the old single id space maps cleanly onto the two new
//     offset id spaces. Source GUIDs, timestamps, and ownership are
//     preserved verbatim.
//
// What it does NOT migrate: the sync change-log, sync_state, sync_conflicts,
// and invite_tokens. These are transient bookkeeping — a migrated node
// starts with a clean change-log and reconciles with peers via the normal
// checksum/snapshot sync path.
//
// Usage:
//
//	# from the gonotes working directory (e.g. ~/.gonotes)
//	GONOTES_ENCRYPTION_KEY=... go run ./scripts/migrate -src ./data/notes.ddb -dest ./data
//
// The new .bytdb files coexist with the old .ddb file; the migration reads
// the former and writes the latter. Run it once into an empty destination.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/marcboeker/go-duckdb" // DuckDB driver, for reading the legacy DB
	"gonotes/models"
)

func main() {
	src := flag.String("src", "./data/notes.ddb", "path to the legacy DuckDB database file")
	dest := flag.String("dest", "./data", "directory to write the new bytdb databases into")
	flag.Parse()

	if err := run(*src, *dest); err != nil {
		fmt.Fprintln(os.Stderr, "migration failed:", err)
		os.Exit(1)
	}
}

func run(src, dest string) error {
	// Load the encryption key if present. It is needed both to DECRYPT legacy
	// private-note bodies and to open the new private database encrypted.
	if os.Getenv(models.EncryptionKeyEnvVar) != "" {
		if err := models.InitEncryption(); err != nil {
			return fmt.Errorf("load encryption key: %w", err)
		}
		fmt.Println("Encryption key loaded — legacy private notes will be decrypted and the new private DB encrypted.")
	} else {
		fmt.Println("No encryption key set — private notes are assumed already plaintext on disk.")
	}

	old, err := sql.Open("duckdb", src)
	if err != nil {
		return fmt.Errorf("open legacy DuckDB %q: %w", src, err)
	}
	defer old.Close()
	if err := old.Ping(); err != nil {
		return fmt.Errorf("ping legacy DuckDB %q: %w", src, err)
	}

	// Create/open the new bytdb databases and schema in dest.
	if err := models.InitTestDB(dest); err != nil {
		return fmt.Errorf("initialize new bytdb databases in %q: %w", dest, err)
	}
	defer models.CloseDB()

	if err := migrateUsers(old); err != nil {
		return err
	}
	catMap, err := migrateCategories(old)
	if err != nil {
		return err
	}
	noteMap, err := migrateNotes(old)
	if err != nil {
		return err
	}
	if err := migrateNoteCategories(old, catMap, noteMap); err != nil {
		return err
	}

	fmt.Println("Migration complete.")
	return nil
}

// migrateUsers copies every user row into the public database.
func migrateUsers(old *sql.DB) error {
	rows, err := old.Query(`SELECT id, guid, username, email, password_hash, display_name,
		is_active, is_admin, created_at, updated_at, last_login_at FROM users`)
	if err != nil {
		return fmt.Errorf("query legacy users: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			id                              int64
			guid, username, passwordHash    string
			email, displayName              sql.NullString
			isActive, isAdmin               bool
			createdAt, updatedAt            time.Time
			lastLoginAt                     sql.NullTime
		)
		if err := rows.Scan(&id, &guid, &username, &email, &passwordHash, &displayName,
			&isActive, &isAdmin, &createdAt, &updatedAt, &lastLoginAt); err != nil {
			return fmt.Errorf("scan legacy user: %w", err)
		}
		if _, err := models.MigrateInsertUser(guid, username, email, displayName, passwordHash,
			isActive, isAdmin, createdAt, updatedAt, lastLoginAt); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy users: %w", err)
	}
	fmt.Printf("Migrated %d users\n", count)
	return nil
}

// migrateCategories copies the categories catalog into the public database
// and returns a map from old category id to new category id.
func migrateCategories(old *sql.DB) (map[int64]int64, error) {
	rows, err := old.Query(`SELECT id, guid, name, description, subcategories, created_by,
		created_at, updated_at FROM categories`)
	if err != nil {
		return nil, fmt.Errorf("query legacy categories: %w", err)
	}
	defer rows.Close()

	catMap := map[int64]int64{}
	count := 0
	for rows.Next() {
		var (
			id                                    int64
			guid, name                            string
			description, subcategories, createdBy sql.NullString
			createdAt, updatedAt                  time.Time
		)
		if err := rows.Scan(&id, &guid, &name, &description, &subcategories, &createdBy,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy category: %w", err)
		}
		// A GUID is required in the new schema (UNIQUE index); older rows may
		// lack one, so backfilling is out of scope here — skip guid-less rows
		// with a warning rather than fail the whole migration.
		if guid == "" {
			fmt.Printf("  warning: skipping category %q (id %d) with empty GUID\n", name, id)
			continue
		}
		newID, err := models.MigrateInsertCategory(guid, name, description, subcategories, createdBy, createdAt, updatedAt)
		if err != nil {
			return nil, err
		}
		catMap[id] = newID
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy categories: %w", err)
	}
	fmt.Printf("Migrated %d categories\n", count)
	return catMap, nil
}

// noteRef records where a migrated note landed.
type noteRef struct {
	newID     int64
	isPrivate bool
}

// migrateNotes copies notes into the public/private databases by privacy,
// decrypting legacy private-note bodies, and returns a map from old note id
// to its new id and database.
func migrateNotes(old *sql.DB) (map[int64]noteRef, error) {
	rows, err := old.Query(`SELECT id, guid, title, description, body, tags, is_private, is_flagged,
		encryption_iv, created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at
		FROM notes`)
	if err != nil {
		return nil, fmt.Errorf("query legacy notes: %w", err)
	}
	defer rows.Close()

	noteMap := map[int64]noteRef{}
	count, decrypted := 0, 0
	for rows.Next() {
		var (
			id                                         int64
			guid, title                                string
			description, body, tags                    sql.NullString
			isPrivate, isFlagged                       bool
			encryptionIV, createdBy, updatedBy         sql.NullString
			createdAt, updatedAt                       time.Time
			authoredAt, syncedAt, deletedAt            sql.NullTime
		)
		if err := rows.Scan(&id, &guid, &title, &description, &body, &tags, &isPrivate, &isFlagged,
			&encryptionIV, &createdBy, &updatedBy, &createdAt, &updatedAt, &authoredAt, &syncedAt, &deletedAt); err != nil {
			return nil, fmt.Errorf("scan legacy note: %w", err)
		}

		// Decrypt a legacy private-note body (AES-GCM + IV) into plaintext;
		// the new private database provides at-rest encryption itself.
		if isPrivate && encryptionIV.Valid && encryptionIV.String != "" && body.Valid {
			if models.IsEncryptionEnabled() {
				plain, derr := models.Decrypt(body.String, encryptionIV.String)
				if derr != nil {
					fmt.Printf("  warning: could not decrypt note %q (id %d): %v — storing as-is\n", guid, id, derr)
				} else {
					body = sql.NullString{String: plain, Valid: true}
					decrypted++
				}
			} else {
				fmt.Printf("  warning: note %q (id %d) is encrypted but no key is set — storing ciphertext\n", guid, id)
			}
		}

		newID, err := models.MigrateInsertNote(guid, title, description, body, tags, isPrivate, isFlagged,
			createdBy, updatedBy, createdAt, updatedAt, authoredAt, syncedAt, deletedAt)
		if err != nil {
			return nil, err
		}
		noteMap[id] = noteRef{newID: newID, isPrivate: isPrivate}
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy notes: %w", err)
	}
	fmt.Printf("Migrated %d notes (%d private bodies decrypted)\n", count, decrypted)
	return noteMap, nil
}

// migrateNoteCategories copies the join table, remapping foreign keys to the
// new ids and routing each link to its note's database.
func migrateNoteCategories(old *sql.DB, catMap map[int64]int64, noteMap map[int64]noteRef) error {
	rows, err := old.Query(`SELECT note_id, category_id, subcategories, created_at FROM note_categories`)
	if err != nil {
		return fmt.Errorf("query legacy note_categories: %w", err)
	}
	defer rows.Close()

	count, skipped := 0, 0
	for rows.Next() {
		var (
			noteID, categoryID int64
			subcategories      sql.NullString
			createdAt          time.Time
		)
		if err := rows.Scan(&noteID, &categoryID, &subcategories, &createdAt); err != nil {
			return fmt.Errorf("scan legacy note_category: %w", err)
		}
		nr, okN := noteMap[noteID]
		newCatID, okC := catMap[categoryID]
		if !okN || !okC {
			// The note or category was skipped (e.g. missing GUID) — its links
			// cannot be remapped, so drop them.
			skipped++
			continue
		}
		if err := models.MigrateInsertNoteCategory(nr.newID, newCatID, subcategories, createdAt, nr.isPrivate); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy note_categories: %w", err)
	}
	fmt.Printf("Migrated %d note-category links (%d skipped)\n", count, skipped)
	return nil
}
