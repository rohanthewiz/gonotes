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
		deleted_at  TIMESTAMP
	)`); err != nil {
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
		hub_url      VARCHAR PRIMARY KEY,
		peer_id      VARCHAR NOT NULL,
		last_push_at TIMESTAMP,
		last_pull_at TIMESTAMP,
		last_sync_at TIMESTAMP,
		auth_token   VARCHAR,
		created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
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
