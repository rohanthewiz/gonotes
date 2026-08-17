package models

import (
	"os"
	"path/filepath"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// db.go owns the lifecycle of the two bytdb databases. See store.go for
// the engine shim and the divide-and-conquer read helpers, and schema.go
// for the table definitions.
//
// There is no longer a separate in-memory read cache: bytdb already holds
// every row in RAM (the on-disk WAL is only for durability), so reads run
// at memory speed straight against the engines. The old DuckDB
// disk-plus-cache dual-write pattern is gone.

// pubDB is the public, unencrypted database: non-private notes and their
// satellite data plus all shared/system tables. privDB is the private,
// (optionally) encrypted database holding private notes and their
// satellite data. Both are package-level singletons for the process
// lifetime, mirroring the previous design.
var (
	pubDB  *dbEngine
	privDB *dbEngine
)

// DataDir is where the two database files live, relative to the working
// directory the CLI switches into at startup.
const DataDir = "./data"

// PublicDBPath and PrivateDBPath are the on-disk locations of the two
// databases.
const (
	PublicDBPath  = DataDir + "/notes_public.bytdb"
	PrivateDBPath = DataDir + "/notes_private.bytdb"
)

// ResolvedDataDir returns the absolute, symlink-resolved path of DataDir — the
// identity of a dataset, as opposed to the many ways one can be spelled.
//
// It exists so that two processes can agree on whether they are looking at the
// same notes. DataDir is relative to a working directory each process chdirs
// into at startup, so "./data" alone says nothing; a server reports this over
// /api/v1/health and the TUI compares it with its own before deciding that the
// server owns the files it was about to open. Getting that comparison wrong in
// the permissive direction is not a cosmetic bug — it points the TUI at a
// different person's notes and lets them edit it.
//
// Symlinks are resolved because macOS makes that a live concern: /tmp is a
// symlink to /private/tmp, so two processes with the identical dataset can spell
// it two ways and a plain string compare would call them different — a false
// mismatch, which fails toward opening the files locally and colliding on the
// single-process lock.
//
// Returns "" when neither form can be computed, which callers must read as "I
// do not know" rather than as a path that matches another empty one.
func ResolvedDataDir() string {
	abs, err := filepath.Abs(DataDir)
	if err != nil {
		return ""
	}
	// EvalSymlinks fails when the directory does not exist yet — a first run,
	// where abs is still the right answer and nothing is there to collide with.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// InitDB opens both databases (creating the files and schema on first
// run) and must be called once at startup before any model operation.
//
// The private database is opened with the encryption key when
// GONOTES_ENCRYPTION_KEY is set (32 bytes); without a key it is opened as
// plaintext — encryption stays optional, exactly as before, but the
// private/non-private split is always maintained.
func InitDB() error {
	if err := os.MkdirAll(DataDir, 0o755); err != nil {
		return serr.Wrap(err, "failed to create data directory")
	}
	return openDatabases(PublicDBPath, PrivateDBPath)
}

// InitTestDB opens both databases under dir, for isolated tests. Callers
// pass a temp directory; the two files are created inside it.
func InitTestDB(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return serr.Wrap(err, "failed to create test data directory")
	}
	pub := filepath.Join(dir, "notes_public.bytdb")
	priv := filepath.Join(dir, "notes_private.bytdb")
	return openDatabases(pub, priv)
}

// openDatabases is the shared open+schema path for InitDB and InitTestDB.
func openDatabases(pubPath, privPath string) error {
	// Load the encryption key if the environment provides one, so the
	// private database opens encrypted. Best-effort: a malformed key is a
	// hard error (we must not silently store private notes in the clear
	// under an operator who intended encryption).
	if os.Getenv(EncryptionKeyEnvVar) != "" {
		if err := InitEncryption(); err != nil {
			return serr.Wrap(err, "failed to load encryption key")
		}
	}
	key := encryptionKey // nil when unset; 32 bytes when loaded

	var err error
	if pubDB, err = openEngine(pubPath, nil); err != nil {
		return serr.Wrap(err, "failed to open public database")
	}
	if privDB, err = openEngine(privPath, key); err != nil {
		return serr.Wrap(err, "failed to open private database")
	}

	// Build the schema. Both databases get the note-side tables; only the
	// public database gets the shared/system tables. The private database
	// offsets its id sequences so note ids are globally unique.
	if err = pubDB.createNoteSchema(0); err != nil {
		return serr.Wrap(err, "failed to create public note schema")
	}
	if err = pubDB.createPublicOnlySchema(); err != nil {
		return serr.Wrap(err, "failed to create public system schema")
	}
	if err = privDB.createNoteSchema(seqOffsetPrivate); err != nil {
		return serr.Wrap(err, "failed to create private note schema")
	}

	logger.Info("Databases initialized",
		"public", pubPath, "private", privPath, "private_encrypted", privDB.encrypted)
	return nil
}

// CloseDB closes both databases. Safe to call via defer after InitDB.
func CloseDB() error {
	var firstErr error
	if err := privDB.Close(); err != nil {
		firstErr = serr.Wrap(err, "failed to close private database")
		logger.LogErr(firstErr, "closing private database")
	} else {
		logger.Info("Private database closed")
	}
	if err := pubDB.Close(); err != nil {
		if firstErr == nil {
			firstErr = serr.Wrap(err, "failed to close public database")
		}
		logger.LogErr(err, "closing public database")
	} else {
		logger.Info("Public database closed")
	}
	pubDB, privDB = nil, nil
	return firstErr
}

// PubDB returns the public engine. Panics if called before InitDB — a
// programming error in the startup sequence.
func PubDB() *dbEngine {
	if pubDB == nil {
		panic("database not initialized - call InitDB first")
	}
	return pubDB
}

// PrivDB returns the private engine. Panics if called before InitDB.
func PrivDB() *dbEngine {
	if privDB == nil {
		panic("database not initialized - call InitDB first")
	}
	return privDB
}

// noteEngine picks the database a note belongs in by its privacy: private
// notes live in the encrypted database, everything else in the public one.
func noteEngine(isPrivate bool) *dbEngine {
	if isPrivate {
		return privDB
	}
	return pubDB
}

// engineForNoteID finds which database holds a non-deleted note by id,
// returning nil if neither does. Used when an operation keyed on a note id
// (a category link, say) must run against the note's own database — the
// note_categories rows live alongside the note.
func engineForNoteID(noteID int64) *dbEngine {
	for _, en := range []*dbEngine{pubDB, privDB} {
		var one int
		if err := en.QueryRow(`SELECT 1 FROM notes WHERE id = ? AND deleted_at IS NULL`, noteID).Scan(&one); err == nil {
			return en
		}
	}
	return nil
}

// engineForOwnedNote is engineForNoteID scoped to a note the user owns. An
// empty userGUID means "no ownership filter".
func engineForOwnedNote(noteID int64, userGUID string) *dbEngine {
	for _, en := range []*dbEngine{pubDB, privDB} {
		var one int
		var err error
		if userGUID != "" {
			err = en.QueryRow(`SELECT 1 FROM notes WHERE id = ? AND created_by = ? AND deleted_at IS NULL`, noteID, userGUID).Scan(&one)
		} else {
			err = en.QueryRow(`SELECT 1 FROM notes WHERE id = ? AND deleted_at IS NULL`, noteID).Scan(&one)
		}
		if err == nil {
			return en
		}
	}
	return nil
}

// engineForID routes a note_changes / note_fragments id to its home
// database. These ids are drawn from per-database offset sequences (the
// private database starts above seqOffsetPrivate), so the id alone
// identifies the engine — no side table needed.
//
// This is for change/fragment ids ONLY, never notes.id: a note keeps its
// id when a privacy flip moves it between databases, whereas change and
// fragment rows are always created fresh in their engine and never move.
func engineForID(id int64) *dbEngine {
	if id >= seqOffsetPrivate {
		return privDB
	}
	return pubDB
}
