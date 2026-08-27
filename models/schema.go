package models

import (
	"fmt"

	"github.com/rohanthewiz/serr"
)

// schema.go defines the bytdb schema for both databases and creates it
// idempotently at startup.
//
// Design notes specific to bytdb (vs the old DuckDB schema):
//
//   - One statement per Exec. The old bundled "CREATE SEQUENCE; CREATE
//     TABLE" constants are split into individual statements here.
//   - No CREATE TABLE/INDEX IF NOT EXISTS. Creation is gated on
//     dbEngine.hasTable so re-running InitDB is safe. Sequences do take
//     IF NOT EXISTS and are harmless to re-issue.
//   - No DEFAULT nextval(...). bytdb requires DEFAULT to be a constant
//     (or now()/current_date). Auto-increment ids are therefore drawn by
//     calling nextval('<table>_id_seq') directly in each INSERT.
//   - No FOREIGN KEY constraints. They cannot span the two databases
//     (a private note's category link references the public categories
//     catalog), and the application already maintains referential
//     integrity through soft-deletes and explicit cleanup. Dropping them
//     keeps one uniform schema across both DBs.
//
// The two databases share the same note-side tables (notes,
// note_categories, note_fragments, note_changes, *_sync_peers). The
// PRIVATE database offsets its id sequences by seqOffsetPrivate so note,
// fragment, and change ids are globally unique across the two DBs — this
// lets a read fan out to both databases and identify a row by id with no
// ambiguity, and lets a note keep its id if it flips privacy and moves
// between databases. The PUBLIC database additionally holds the
// shared/system tables: the categories catalog and its change-tracking,
// users, invite_tokens, sync_state, and sync_conflicts.

// seqOffsetPrivate is added to the private database's id sequence starts
// so its ids never collide with the public database's (which start at 1).
// 1e12 leaves room for ~1 trillion public rows before the ranges could
// meet — unreachable for a personal notes store.
const seqOffsetPrivate int64 = 1_000_000_000_000

// createSequence issues an idempotent CREATE SEQUENCE. start lets the
// private database offset its ranges.
func (en *dbEngine) createSequence(name string, start int64) error {
	_, err := en.Exec(fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s START %d", name, start))
	if err != nil {
		return serr.Wrap(err, "failed to create sequence", "name", name)
	}
	return nil
}

// hasColumn reports whether a table already carries a column. bytdb keeps
// the column list on the table descriptor, so this is an in-memory lookup
// with no query behind it.
//
// A missing table answers false rather than erroring: the only caller is
// ensureColumn, which runs after ensureTable, so "no table" cannot happen —
// and if the ordering were ever broken, skipping the ALTER is the safe
// direction (the CREATE will have included the column already).
func (en *dbEngine) hasColumn(table, column string) bool {
	desc := en.eng.Table(table)
	if desc == nil {
		return false
	}
	for _, c := range desc.Columns {
		if c.Name == column {
			return true
		}
	}
	return false
}

// ensureColumn adds a column to an existing table once, then backfills it.
//
// This is the whole migration story for a table that predates a column.
// ensureTable cannot help: it skips a table that already exists, so a column
// added to its CREATE statement reaches new databases only. Every database
// created before the column needs the ALTER.
//
// backfillSQL is the belt to the ALTER's braces. bytdb stores rows tagged by
// column ID, so a row written before the ALTER carries no value for the new
// column and reads back as NULL rather than as the column's DEFAULT — the SQL
// layer applies a DEFAULT at INSERT time and cannot reach rows already on
// disk. Since bytdb v0.10.0 ADD COLUMN closes that gap itself: a DEFAULT is
// evaluated once and written into every stored row inside the same
// transaction that publishes the descriptor, so the rows and the schema commit
// together or not at all.
//
// backfillSQL is still worth passing for a column whose NULLs would be
// dangerous rather than merely untidy. It covers the two cases the engine's
// own backfill cannot: a database whose column was added by some other path
// (or a future DEFAULT-less ALTER), and rows that acquired a NULL after the
// migration. A NULL where the code expects a number is the kind of difference
// that shows up as `version = 1` matching nothing. It runs on every startup
// but touches rows only when there are any (its WHERE finds none afterwards).
func (en *dbEngine) ensureColumn(table, column, colType, backfillSQL string) error {
	if en.hasColumn(table, column) {
		// Still run the backfill: a column added before the engine did its own
		// backfilling (or by a path that skipped the UPDATE) would otherwise
		// leave NULLs behind forever, and the UPDATE is free once they are gone.
		if backfillSQL != "" {
			if _, err := en.Exec(backfillSQL); err != nil {
				return serr.Wrap(err, "failed to backfill column", "table", table, "column", column)
			}
		}
		return nil
	}
	if _, err := en.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, colType)); err != nil {
		return serr.Wrap(err, "failed to add column", "table", table, "column", column)
	}
	if backfillSQL != "" {
		if _, err := en.Exec(backfillSQL); err != nil {
			return serr.Wrap(err, "failed to backfill column", "table", table, "column", column)
		}
	}
	return nil
}

// ensureNotNull marks an existing column NOT NULL, so that a database that
// reached the column through ensureColumn ends up with the same constraint a
// freshly created one gets from its CREATE TABLE. Without it the two diverge
// permanently: ADD COLUMN wrote the constraint the ALTER named, but a database
// whose column predates the constraint keeps the nullable version forever, and
// the only databases exercising the strict path would be the brand new ones.
//
// The call is idempotent and cheap. bytdb (>= v0.11.0) skips the write when
// the flag is already set, and validates by scanning stored value tuples for
// the column's tag rather than materializing rows — a read-only pass with no
// rewrite. It publishes the flag in the same transaction as the scan, so no
// concurrent write can insert a NULL between "checked" and "constrained".
//
// A column still holding NULLs fails the statement, which is the correct
// direction: startup stops loudly rather than a constraint quietly not being
// what the schema says it is. Callers therefore run any backfill first.
func (en *dbEngine) ensureNotNull(table, column string) error {
	if !en.hasColumn(table, column) {
		// Nothing to constrain. Not an error: this mirrors ensureColumn's
		// tolerance for a schema whose CREATE already covered the case.
		return nil
	}
	if _, err := en.Exec(fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL", table, column)); err != nil {
		return serr.Wrap(err, "failed to set column NOT NULL", "table", table, "column", column)
	}
	return nil
}

// ensureTable creates a table (and its indexes) once. If the table
// already exists it is left untouched — this is how schema creation stays
// idempotent without CREATE TABLE IF NOT EXISTS.
func (en *dbEngine) ensureTable(name, createSQL string, indexSQL ...string) error {
	if en.hasTable(name) {
		return nil
	}
	if _, err := en.Exec(createSQL); err != nil {
		return serr.Wrap(err, "failed to create table", "table", name)
	}
	for _, ix := range indexSQL {
		if _, err := en.Exec(ix); err != nil {
			return serr.Wrap(err, "failed to create index", "table", name)
		}
	}
	return nil
}

// createNoteSchema builds the note-side tables that both databases share.
// offset is 0 for the public database and seqOffsetPrivate for the
// private one.
func (en *dbEngine) createNoteSchema(offset int64) error {
	// --- notes ---
	if err := en.createSequence("notes_id_seq", 1+offset); err != nil {
		return err
	}
	if err := en.ensureTable("notes", `CREATE TABLE notes (
		id          BIGINT PRIMARY KEY,
		guid        VARCHAR NOT NULL UNIQUE,
		title       VARCHAR NOT NULL,
		description VARCHAR,
		body        VARCHAR,
		tags        VARCHAR,
		is_private  BOOLEAN DEFAULT false,
		is_flagged  BOOLEAN DEFAULT false,
		created_by  VARCHAR,
		updated_by  VARCHAR,
		created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		authored_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		synced_at   TIMESTAMP,
		deleted_at  TIMESTAMP,
		version     BIGINT NOT NULL DEFAULT 1
	)`); err != nil {
		return err
	}

	// version is the note's optimistic-concurrency counter: every write that
	// changes the note bumps it by one, and a writer that loaded version N can
	// name N in its UPDATE to be told "someone got here first" instead of
	// silently overwriting them. See UpdateNote and ErrStaleWrite.
	//
	// A counter rather than updated_at, deliberately. The timestamp is the
	// obvious candidate and is wrong for the job in two ways that both bite in
	// this system specifically: its resolution can round two writes inside the
	// same tick into one value, and peer-to-peer sync means the writes being
	// compared did not come from one clock. A counter has neither problem — it
	// only ever has to be equal or not.
	// The ALTER carries the DEFAULT as well as the backfill, and both are
	// needed. The backfill fixes rows already on disk; the default covers any
	// INSERT that forgets the column on a database that took this migration
	// path. (Every INSERT in the models layer names version explicitly, so the
	// default is a backstop for a future one that does not — a NULL version
	// reads back as 0, which the guard treats as "unchecked", so a note could
	// silently opt itself out of the protection this column exists to give.)
	//
	// NOT NULL turns that backstop into a guarantee, and is what the CREATE
	// above declares. It is spelled out in three places for three different
	// database ages, all converging on the same shape:
	//
	//	database age            gets NOT NULL from
	//	----------------------  ------------------------------------------
	//	created from scratch    the CREATE TABLE above
	//	predates the column     this ALTER (the DEFAULT backfills the rows
	//	                        in the same transaction, so the constraint
	//	                        is satisfiable the moment it is published)
	//	took an earlier, nul-   ensureNotNull below, after the UPDATE has
	//	lable version of this   removed any NULL left by that path
	//	migration
	if err := en.ensureColumn("notes", "version", "BIGINT NOT NULL DEFAULT 1",
		`UPDATE notes SET version = 1 WHERE version IS NULL`); err != nil {
		return err
	}
	if err := en.ensureNotNull("notes", "version"); err != nil {
		return err
	}

	// --- note_categories (link table) ---
	// category_id references the public categories catalog by its integer
	// id (categories are single-DB, so the id is globally meaningful); the
	// join to categories is resolved against the public engine in Go.
	if err := en.ensureTable("note_categories", `CREATE TABLE note_categories (
		note_id       BIGINT NOT NULL,
		category_id   BIGINT NOT NULL,
		subcategories VARCHAR,
		created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (note_id, category_id)
	)`); err != nil {
		return err
	}

	// --- note change-tracking (peer-to-peer sync) ---
	if err := en.createSequence("note_fragments_id_seq", 1+offset); err != nil {
		return err
	}
	if err := en.ensureTable("note_fragments", `CREATE TABLE note_fragments (
		id           BIGINT PRIMARY KEY,
		bitmask      SMALLINT NOT NULL,
		title        VARCHAR,
		description  VARCHAR,
		body         VARCHAR,
		tags         VARCHAR,
		is_private   BOOLEAN,
		categories   VARCHAR,
		body_is_diff BOOLEAN DEFAULT false
	)`); err != nil {
		return err
	}

	if err := en.createSequence("note_changes_id_seq", 1+offset); err != nil {
		return err
	}
	// change_user (not "user") avoids the SQL reserved word entirely.
	if err := en.ensureTable("note_changes", `CREATE TABLE note_changes (
		id               BIGINT PRIMARY KEY,
		guid             VARCHAR NOT NULL UNIQUE,
		note_guid        VARCHAR NOT NULL,
		operation        INTEGER NOT NULL,
		note_fragment_id BIGINT,
		change_user      VARCHAR,
		created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
		`CREATE INDEX idx_note_changes_note_guid ON note_changes(note_guid)`,
		`CREATE INDEX idx_note_changes_created_at ON note_changes(created_at)`,
	); err != nil {
		return err
	}

	if err := en.ensureTable("note_change_sync_peers", `CREATE TABLE note_change_sync_peers (
		note_change_id BIGINT NOT NULL,
		peer_id        VARCHAR NOT NULL,
		synced_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (note_change_id, peer_id)
	)`,
		`CREATE INDEX idx_note_change_sync_peers_peer_id ON note_change_sync_peers(peer_id)`,
	); err != nil {
		return err
	}

	return nil
}

// createPublicOnlySchema builds the shared/system tables that live only
// in the public database: the categories catalog and its change-tracking,
// users, invite_tokens, sync_state, and sync_conflicts.
func (en *dbEngine) createPublicOnlySchema() error {
	// --- categories catalog ---
	if err := en.createSequence("categories_id_seq", 1); err != nil {
		return err
	}
	if err := en.ensureTable("categories", `CREATE TABLE categories (
		id            BIGINT PRIMARY KEY,
		guid          VARCHAR,
		name          VARCHAR NOT NULL,
		description   VARCHAR,
		subcategories VARCHAR,
		created_by    VARCHAR,
		created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
		`CREATE UNIQUE INDEX idx_categories_guid ON categories(guid)`,
	); err != nil {
		return err
	}

	// --- category change-tracking ---
	if err := en.createSequence("category_fragments_id_seq", 1); err != nil {
		return err
	}
	if err := en.ensureTable("category_fragments", `CREATE TABLE category_fragments (
		id            BIGINT PRIMARY KEY,
		bitmask       SMALLINT NOT NULL,
		name          VARCHAR,
		description   VARCHAR,
		subcategories VARCHAR
	)`); err != nil {
		return err
	}

	if err := en.createSequence("category_changes_id_seq", 1); err != nil {
		return err
	}
	if err := en.ensureTable("category_changes", `CREATE TABLE category_changes (
		id                   BIGINT PRIMARY KEY,
		guid                 VARCHAR NOT NULL UNIQUE,
		category_guid        VARCHAR NOT NULL,
		operation            INTEGER NOT NULL,
		category_fragment_id BIGINT,
		change_user          VARCHAR,
		created_at           TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
		`CREATE INDEX idx_category_changes_category_guid ON category_changes(category_guid)`,
		`CREATE INDEX idx_category_changes_created_at ON category_changes(created_at)`,
	); err != nil {
		return err
	}

	if err := en.ensureTable("category_change_sync_peers", `CREATE TABLE category_change_sync_peers (
		category_change_id BIGINT NOT NULL,
		peer_id            VARCHAR NOT NULL,
		synced_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (category_change_id, peer_id)
	)`,
		`CREATE INDEX idx_category_change_sync_peers_peer_id ON category_change_sync_peers(peer_id)`,
	); err != nil {
		return err
	}

	// --- users ---
	if err := en.createSequence("users_id_seq", 1); err != nil {
		return err
	}
	if err := en.ensureTable("users", `CREATE TABLE users (
		id            BIGINT PRIMARY KEY,
		guid          VARCHAR NOT NULL UNIQUE,
		username      VARCHAR NOT NULL UNIQUE,
		email         VARCHAR UNIQUE,
		password_hash VARCHAR NOT NULL,
		display_name  VARCHAR,
		is_active     BOOLEAN DEFAULT true,
		is_admin      BOOLEAN DEFAULT false,
		created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_login_at TIMESTAMP
	)`,
		`CREATE INDEX idx_users_username ON users(username)`,
		`CREATE INDEX idx_users_email ON users(email)`,
	); err != nil {
		return err
	}

	// --- invite_tokens ---
	if err := en.createSequence("invite_tokens_id_seq", 1); err != nil {
		return err
	}
	if err := en.ensureTable("invite_tokens", `CREATE TABLE invite_tokens (
		id         BIGINT PRIMARY KEY,
		token      VARCHAR NOT NULL UNIQUE,
		created_by VARCHAR NOT NULL,
		used_by    VARCHAR,
		expires_at TIMESTAMP NOT NULL,
		used_at    TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
		`CREATE INDEX idx_invite_tokens_token ON invite_tokens(token)`,
	); err != nil {
		return err
	}

	// --- sync_state (natural PK on hub_url) ---
	if err := en.ensureTable("sync_state", `CREATE TABLE sync_state (
		hub_url       VARCHAR PRIMARY KEY,
		peer_id       VARCHAR NOT NULL,
		last_push_at  TIMESTAMP,
		last_pull_at  TIMESTAMP,
		last_sync_at  TIMESTAMP,
		auth_token    VARCHAR,
		hub_user_guid VARCHAR,
		hub_username  VARCHAR,
		created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}

	// hub_user_guid / hub_username record who this spoke is ON THE HUB, which
	// is not something it could previously answer without a network call.
	// Ownership (notes.created_by) travels with every synced change, so the
	// spoke's local account has to carry the same GUID as its hub account or
	// pulled notes belong to nobody it can log in as. See sync_identity.go.
	//
	// No backfill: the value cannot be derived from what is already on disk.
	// It is filled in by the next login, or recovered early from a cached
	// hub JWT at sync client startup.
	if err := en.ensureColumn("sync_state", "hub_user_guid", "VARCHAR", ""); err != nil {
		return err
	}
	if err := en.ensureColumn("sync_state", "hub_username", "VARCHAR", ""); err != nil {
		return err
	}

	// --- sync_conflicts (audit log) ---
	if err := en.createSequence("sync_conflicts_id_seq", 1); err != nil {
		return err
	}
	if err := en.ensureTable("sync_conflicts", `CREATE TABLE sync_conflicts (
		id            BIGINT PRIMARY KEY,
		entity_type   VARCHAR NOT NULL,
		entity_guid   VARCHAR NOT NULL,
		local_change  VARCHAR,
		remote_change VARCHAR,
		resolution    VARCHAR NOT NULL,
		resolved_at   TIMESTAMP,
		created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
		`CREATE INDEX idx_sync_conflicts_entity_guid ON sync_conflicts(entity_guid)`,
	); err != nil {
		return err
	}

	return nil
}
