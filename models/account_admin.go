package models

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Local Account Administration
//
// The identity reconciliation in sync_identity.go matches a local account to
// its hub account by USERNAME, and only by username — it will not guess that
// "bob" here is "rob" on the hub. So an account registered under a different
// name from GONOTES_SYNC_USERNAME is diagnosed (the log names it) but never
// repaired, and pulled notes stay owned by a GUID nobody can log in as.
//
// The operator knows what the code refuses to guess, so these operations let
// them say it, from the CLI (`gonotes account ...`):
//
//	rename  bob → rob     bob has no counterpart yet. Change the name; the
//	                      account, password and notes stay. The new name now
//	                      matches the sync username, so the existing reconcile
//	                      adopts the hub GUID immediately (AlignLocalUserWithHub).
//
//	merge   bob → rob     both exist — typically because the old advice was
//	                      followed and "rob" was registered alongside "bob".
//	                      Every row owned by bob is re-pointed at rob, then bob
//	                      is deleted. rob then aligns with the hub as usual.
//
// Both run with the databases opened by the CLI process itself, so the server
// must be stopped first. The storage layer takes no OS file lock and would not
// stop a second opener; the CLI probes for a live server instead (see
// serverHoldingDataDir in account_cmd.go).
//
// Like the reconcile sweep, nothing here records sync changes: ownership is
// not part of a note fragment (the hub stamps created_by from the
// authenticated user), so moving it is local bookkeeping only.
// ============================================================================

// UserRefCount is how many rows in one table/column name a user GUID.
type UserRefCount struct {
	Table  string
	Column string
	Rows   int64
}

// CountUserGUIDReferences reports, per column in userGUIDRefs, how many rows
// name guid. Columns with no rows are included, so callers see the full
// inventory a merge would touch. Used for previews (--dry-run) and listings.
func CountUserGUIDReferences(guid string) ([]UserRefCount, error) {
	var out []UserRefCount
	for _, ref := range userGUIDRefs() {
		// Identifiers come from the fixed inventory, never from input.
		stmt := `SELECT COUNT(*) FROM ` + ref.table + ` WHERE ` + ref.column + ` = ?`
		var total int64
		for _, en := range ref.engines {
			var n int64
			if err := en.QueryRow(stmt, guid).Scan(&n); err != nil {
				return nil, serr.Wrap(err, "failed to count user references",
					"table", ref.table, "column", ref.column)
			}
			total += n
		}
		out = append(out, UserRefCount{Table: ref.table, Column: ref.column, Rows: total})
	}
	return out, nil
}

// ListLocalUsers returns every account on this instance, ordered by id
// (registration order).
func ListLocalUsers() ([]User, error) {
	rows, err := pubDB.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list users")
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u); err != nil {
			return nil, serr.Wrap(err, "failed to scan user")
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "failed to iterate users")
	}
	return users, nil
}

// HubIdentity is the account this spoke authenticates as on one hub, as
// recorded by the last successful login (see RecordHubIdentity).
type HubIdentity struct {
	HubURL   string
	UserGUID string
	Username string
}

// RecordedHubIdentities returns the hub identities this instance has learned.
// Rows from a hub that has never been logged in to (no GUID yet) are skipped.
func RecordedHubIdentities() ([]HubIdentity, error) {
	rows, err := pubDB.Query(
		`SELECT hub_url, hub_user_guid, hub_username FROM sync_state
		 WHERE hub_user_guid IS NOT NULL AND hub_user_guid <> ''
		 ORDER BY hub_url`)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list recorded hub identities")
	}
	defer rows.Close()

	var out []HubIdentity
	for rows.Next() {
		var url string
		var guid, name sql.NullString
		if err := rows.Scan(&url, &guid, &name); err != nil {
			return nil, serr.Wrap(err, "failed to scan hub identity")
		}
		out = append(out, HubIdentity{HubURL: url, UserGUID: guid.String, Username: name.String})
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "failed to iterate hub identities")
	}
	return out, nil
}

// RenameLocalUser changes an account's username, keeping its GUID, password
// and everything it owns. The target name must be free; when it is not, the
// two accounts need merging instead (MergeLocalUsers), and the error says so.
//
// Rename alone does not adopt the hub GUID — call AlignLocalUserWithHub
// afterwards. They are separate steps so each stays a single, re-runnable
// write: if the process dies between them, the next sync login or startup
// performs the alignment on its own, since the name now matches.
func RenameLocalUser(fromUsername, toUsername string) (*User, error) {
	if err := ValidateUsername(toUsername); err != nil {
		return nil, err
	}
	if fromUsername == toUsername {
		return nil, serr.New("the old and new usernames are the same")
	}

	from, err := GetUserByUsername(fromUsername)
	if err != nil {
		return nil, err
	}
	if from == nil {
		return nil, serr.New("no local account named " + strconv.Quote(fromUsername))
	}

	taken, err := GetUserByUsername(toUsername)
	if err != nil {
		return nil, err
	}
	if taken != nil {
		return nil, serr.New("a local account named " + strconv.Quote(toUsername) +
			" already exists; merge the two accounts instead of renaming")
	}

	if _, err := pubDB.Exec(
		`UPDATE users SET username = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		toUsername, from.ID,
	); err != nil {
		return nil, serr.Wrap(err, "failed to rename user", "from", fromUsername, "to", toUsername)
	}

	logger.Info("Renamed local account", "from", fromUsername, "to", toUsername, "user_guid", from.GUID)
	return GetUserByID(from.ID)
}

// MergeLocalUsers folds the account `fromUsername` into `intoUsername`: every
// row naming from's GUID is re-pointed at into's, then from is deleted. The
// surviving account keeps its own name, GUID and password.
//
// CRASH SAFETY follows the same rule as ReconcileHubUserGUID: the row that
// TRIGGERS the work is removed last. The sweep spans two databases, so no
// transaction covers it; but while `from` still exists, re-running the merge
// re-runs the idempotent sweep (`WHERE col = fromGUID`) and finishes the job.
// Delete `from` first and a crash strands its remaining rows under a GUID no
// account holds, with nothing left to name it.
//
// Admin rights are kept, not dropped: if from was an admin, into becomes one.
// Merging the instance's only admin into a regular account would otherwise
// leave nobody able to issue invites.
func MergeLocalUsers(fromUsername, intoUsername string) (*User, error) {
	if fromUsername == intoUsername {
		return nil, serr.New("cannot merge an account into itself")
	}

	from, err := GetUserByUsername(fromUsername)
	if err != nil {
		return nil, err
	}
	if from == nil {
		return nil, serr.New("no local account named " + strconv.Quote(fromUsername))
	}
	into, err := GetUserByUsername(intoUsername)
	if err != nil {
		return nil, err
	}
	if into == nil {
		return nil, serr.New("no local account named " + strconv.Quote(intoUsername) +
			"; rename " + strconv.Quote(fromUsername) + " instead of merging")
	}

	if err := rewriteUserGUIDReferences(from.GUID, into.GUID); err != nil {
		return nil, err
	}

	if from.IsAdmin && !into.IsAdmin {
		if _, err := pubDB.Exec(
			`UPDATE users SET is_admin = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			true, into.ID,
		); err != nil {
			return nil, serr.Wrap(err, "failed to carry admin rights over in merge",
				"into", intoUsername)
		}
	}

	// Last: see CRASH SAFETY above.
	if _, err := pubDB.Exec(`DELETE FROM users WHERE id = ?`, from.ID); err != nil {
		return nil, serr.Wrap(err, "failed to delete the merged account", "from", fromUsername)
	}

	logger.Info("Merged local accounts",
		"from", fromUsername, "from_guid", from.GUID,
		"into", intoUsername, "into_guid", into.GUID)
	return GetUserByID(into.ID)
}

// AlignLocalUserWithHub adopts the recorded hub GUID for username, if this
// instance has one recorded under that name. It is the same repair a sync
// login performs, run on demand so a rename or merge takes effect now rather
// than at the next server start. Reports whether the GUID changed.
func AlignLocalUserWithHub(username string) (bool, error) {
	hubGUID, err := hubIdentityForUsername(username)
	if err != nil {
		return false, err
	}
	if hubGUID == "" {
		return false, nil // Not a spoke, or not this account's hub name
	}
	return ReconcileHubUserGUID(hubGUID, username)
}

// CategoryNameCollisions lists category names (compared ignoring case) that
// both users own. A merge does not combine categories — doing so would mean
// refiling notes and deleting a category, which are sync-visible changes —
// so after one, these names appear twice for the surviving account. Callers
// show them so the operator can tidy up in the app.
func CategoryNameCollisions(guidA, guidB string) ([]string, error) {
	namesA, err := categoryNamesFor(guidA)
	if err != nil {
		return nil, err
	}
	namesB, err := categoryNamesFor(guidB)
	if err != nil {
		return nil, err
	}

	var out []string
	for key, name := range namesA {
		if _, ok := namesB[key]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// categoryNamesFor returns a user's category names keyed by lower-case form.
// Folding happens in Go rather than SQL to avoid depending on LOWER() support
// in the embedded engine.
func categoryNamesFor(guid string) (map[string]string, error) {
	rows, err := pubDB.Query(`SELECT name FROM categories WHERE created_by = ?`, guid)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list category names")
	}
	defer rows.Close()

	names := map[string]string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, serr.Wrap(err, "failed to scan category name")
		}
		names[strings.ToLower(name)] = name
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "failed to iterate category names")
	}
	return names, nil
}
