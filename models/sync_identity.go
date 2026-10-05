package models

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Spoke/Hub User Identity
//
// THE PROBLEM
//
// Every note and category is owned by a user GUID (notes.created_by), and
// every read query filters on it. Sync carries that ownership across the
// wire: a change's change_user becomes created_by wherever it lands.
//
// A spoke has two ways of acquiring a user, and until this file they
// produced two different GUIDs for the same person:
//
//	spoke ──register(username,password)──► HUB    creates user, GUID = H
//	spoke ──register locally (web/TUI)───► SPOKE  creates user, GUID = S
//
// Notes pulled from the hub arrive owned by H. The person sitting at the
// spoke is logged in as S. Same account, same password, same human — and
// their own notes are invisible, because H ≠ S and every SELECT says
// `WHERE created_by = ?`.
//
// (The push direction was never broken: the hub's push handler overwrites
// change.User with the authenticated user's GUID, so a spoke's notes land on
// the hub owned by H regardless of what the spoke called itself. That
// asymmetry is why the bug reads as "sync works, but I can't see anything".)
//
// THE FIX: ONE ACCOUNT, ONE GUID — THE HUB'S
//
// The hub is where the account is of record; it is the identity every other
// machine already agrees on, because it is the one stamped onto every change
// that fans out. So the spoke adopts it, in three places:
//
//	1. The spoke LEARNS H. Login (and the JWT it caches) already carry the
//	   hub user's GUID and username; they are now recorded in sync_state.
//	2. A local account created AFTER that is BORN with H — CreateUser draws
//	   the recorded GUID instead of a fresh one. No rewrite, nothing to fix.
//	3. A local account that already exists under a different GUID ADOPTS H,
//	   and every row that named the old GUID is re-pointed at it.
//
// Case 3 is the repair path for databases that already have the mismatch;
// case 2 is what keeps new ones from acquiring it.
//
// WHAT IS DELIBERATELY NOT DONE
//
// Ownership is not part of a note fragment — the hub sets created_by from
// change.User, never from fragment content — so re-pointing created_by is
// purely local bookkeeping and records NO sync changes. Nothing about this
// realignment needs to (or should) travel.
// ============================================================================

// RecordHubIdentity stores who this spoke is on the given hub. Called after
// every successful login, because it is cheap and because the hub is free to
// be re-registered under a new account; the last successful login is the
// truth about which hub identity this spoke is currently carrying.
//
// It also stamps updated_at: a login is activity on that hub, and the most
// recently active row is what currentHubState treats as the current hub.
func RecordHubIdentity(hubURL, hubUserGUID, hubUsername string) error {
	if hubURL == "" || hubUserGUID == "" {
		return nil // Nothing learned — not an error, just no news
	}
	_, err := pubDB.Exec(
		`UPDATE sync_state SET hub_user_guid = ?, hub_username = ?, updated_at = ? WHERE hub_url = ?`,
		hubUserGUID, hubUsername, time.Now(), hubURL,
	)
	if err != nil {
		return serr.Wrap(err, "failed to record hub user identity", "hub_url", hubURL)
	}
	return nil
}

// ---- Which hub is current ---------------------------------------------------
//
// sync_state is keyed by hub_url, so a spoke keeps one row per hub URL it
// has ever synced with, and nothing removes the old ones. The client syncs
// with exactly one hub at a time (SyncConfig.HubURL), so after a move:
//
//	sync_state
//	  http://old-hub:8444   hub_user_guid = G1   updated_at = March   ← stale
//	  https://new-hub       hub_user_guid = G2   updated_at = today   ← current
//
// Same username on both, different GUIDs if the new hub is a different
// instance. Notes pulled from now on carry G2. An identity lookup that
// returned G1 (the old `LIMIT 1` did, whenever the old URL sorted first)
// would give a new local account a GUID that no incoming note uses. The next
// server start would then reconcile it to G2 and rewrite every row it owned.
//
// The current hub is the most recently active row. Every write to a row
// comes from the sync client for its configured hub, so updated_at follows
// the active hub: NewSyncClient stamps it at startup (MarkCurrentHub), and
// login (RecordHubIdentity), token saves and completed cycles stamp it too.
// No hub URL from the environment is consulted, because `gonotes account` runs
// from a shell that may not have the server's sync settings.

// currentHubState returns the sync_state row of the hub this spoke synced
// with most recently, and false when there are no rows (not a spoke).
//
// The row can carry no identity yet: a hub just switched to, before its first
// login. That is reported as-is rather than skipped. Falling back to an
// older row would hand out the previous hub's identity, which is the bug this
// function exists to prevent.
//
// The choice is made in Go over every row rather than with ORDER BY ...
// LIMIT 1: there are only a handful of rows, and doing it here makes the tie
// break (hub_url) and a NULL updated_at (treated as oldest) explicit rather
// than up to the engine.
func currentHubState() (HubIdentity, bool, error) {
	rows, err := pubDB.Query(`SELECT hub_url, hub_user_guid, hub_username, updated_at FROM sync_state`)
	if err != nil {
		return HubIdentity{}, false, serr.Wrap(err, "failed to read sync state")
	}
	defer rows.Close()

	var (
		best     HubIdentity
		bestTime time.Time
		found    bool
	)
	for rows.Next() {
		var url string
		var guid, name sql.NullString
		var updated sql.NullTime
		if err := rows.Scan(&url, &guid, &name, &updated); err != nil {
			return HubIdentity{}, false, serr.Wrap(err, "failed to scan sync state")
		}
		ts := updated.Time // zero when NULL, so a NULL row loses to any stamped one
		if !found || ts.After(bestTime) || (ts.Equal(bestTime) && url > best.HubURL) {
			best = HubIdentity{HubURL: url, UserGUID: guid.String, Username: name.String}
			bestTime = ts
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return HubIdentity{}, false, serr.Wrap(err, "failed to iterate sync state")
	}
	return best, found, nil
}

// MarkCurrentHub stamps hubURL's sync_state row as the most recently active,
// making it the current hub for identity lookups. NewSyncClient calls it at
// startup. Without it, a spoke switched back to a hub it used before would
// keep treating the other hub as current until the first login or cycle.
func MarkCurrentHub(hubURL string) error {
	if _, err := pubDB.Exec(`UPDATE sync_state SET updated_at = ? WHERE hub_url = ?`,
		time.Now(), hubURL); err != nil {
		return serr.Wrap(err, "failed to mark the current hub", "hub_url", hubURL)
	}
	return nil
}

// hubIdentityForUsername returns the hub user GUID this spoke has recorded
// for the given username on its CURRENT hub (see currentHubState), or "" if
// there is none.
//
// Two checks, both safety catches:
//   - Current hub only. An identity recorded on a hub this spoke no longer
//     syncs with is not the GUID incoming notes carry, so it is never offered,
//     even when the current hub has no identity recorded yet.
//   - Username match. Adopting a GUID is only correct when the two accounts
//     are the same account, and the username is the only local evidence.
func hubIdentityForUsername(username string) (string, error) {
	cur, ok, err := currentHubState()
	if err != nil {
		return "", serr.Wrap(err, "failed to look up recorded hub identity", "username", username)
	}
	if !ok || cur.UserGUID == "" || cur.Username != username {
		return "", nil
	}
	return cur.UserGUID, nil
}

// adoptableHubUserGUID reports the GUID a newly created local user named
// `username` should take, or "" to generate a fresh one. Used by CreateUser.
//
// The GUID is only offered if no local user already holds it — a GUID is
// unique in the users table, so handing out a taken one would turn a
// registration into a constraint violation.
func adoptableHubUserGUID(username string) string {
	guid, err := hubIdentityForUsername(username)
	if err != nil {
		logger.LogErr(err, "could not check for a hub identity to adopt", "username", username)
		return ""
	}
	if guid == "" {
		return ""
	}

	existing, err := GetUserByGUID(guid)
	if err != nil {
		logger.LogErr(err, "could not check whether the hub GUID is already taken locally")
		return ""
	}
	if existing != nil {
		return "" // Already in use locally; let this registration get its own
	}

	logger.Info("New local user adopts its hub identity",
		"username", username, "user_guid", guid)
	return guid
}

// ReconcileHubUserGUID aligns an EXISTING local account with the hub identity
// this spoke authenticates as, repairing a database that already carries the
// mismatch. Reports whether anything was changed.
//
// It is safe to call on every login and every startup: the sweep is keyed on
// the old GUID, so once it has run there is nothing left for it to match.
func ReconcileHubUserGUID(hubUserGUID, hubUsername string) (bool, error) {
	if hubUserGUID == "" || hubUsername == "" {
		return false, nil
	}

	local, err := GetUserByUsername(hubUsername)
	if err != nil {
		return false, serr.Wrap(err, "failed to load local user for identity reconciliation")
	}
	if local == nil {
		// No local account under the hub's name. Usually that just means
		// nobody has registered on this spoke yet, which needs no repair —
		// whoever registers next is born with the hub GUID via
		// adoptableHubUserGUID.
		//
		// If accounts DO exist here, though, none of them is the hub account,
		// and pulled notes are owned by a user nobody can log in as. That is
		// the visible symptom, and it is worth naming, along with the fix:
		// `gonotes account` renames or merges the existing account into the
		// sync username (see account_admin.go), after which it adopts the GUID.
		if n, err := countLocalUsers(); err == nil && n > 0 {
			logger.Info("No local account matches the sync username — notes pulled from the hub "+
				"will not be visible until one does. Stop the server and run `gonotes account list` "+
				"for the rename or merge that fixes it",
				"sync_username", hubUsername, "local_users", n)
		}
		return false, nil
	}
	if local.GUID == hubUserGUID {
		return false, nil // Already aligned; the common case after the first pass
	}

	// A different local account is sitting on the hub's GUID. Rewriting would
	// collide on users.guid, and guessing which of the two is "really" the hub
	// account is not a call this code can make. Say so and change nothing.
	if holder, err := GetUserByGUID(hubUserGUID); err != nil {
		return false, serr.Wrap(err, "failed to check the hub GUID's local holder")
	} else if holder != nil {
		return false, serr.New(
			"cannot adopt the hub user GUID: local user " + strconv.Quote(holder.Username) +
				" already holds it, while the sync account is " + strconv.Quote(hubUsername))
	}

	oldGUID := local.GUID
	if err := rewriteUserGUIDReferences(oldGUID, hubUserGUID); err != nil {
		return false, err
	}

	// The users row goes LAST, and that ordering is the crash story. The
	// sweep spans two databases, so no single transaction covers it; what
	// makes a half-finished pass recoverable is that the *trigger* survives
	// it. Leave the user on the old GUID and the next call sees the mismatch
	// again and re-runs the (idempotent) sweep to completion. Flip the user
	// first and a crash strands the remaining rows under a GUID nobody
	// remembers, with the mismatch check now reporting all clear.
	if _, err := pubDB.Exec(
		`UPDATE users SET guid = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		hubUserGUID, local.ID,
	); err != nil {
		return false, serr.Wrap(err, "failed to adopt hub user GUID", "username", hubUsername)
	}

	logger.Info("Local user adopted its hub identity — pulled notes are now visible to it",
		"username", hubUsername, "old_guid", oldGUID, "new_guid", hubUserGUID)
	return true, nil
}

// countLocalUsers reports how many accounts exist on this instance. Used only
// to decide whether a missing username match is "nothing registered yet" or
// "registered under the wrong name".
func countLocalUsers() (int, error) {
	var n int
	if err := pubDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, serr.Wrap(err, "failed to count local users")
	}
	return n, nil
}

// userGUIDRef is one column that names a user by GUID, and the databases
// that hold it.
type userGUIDRef struct {
	engines []*dbEngine
	table   string
	column  string
}

// userGUIDRefs is the single inventory of every column that stores a user
// GUID. Both the identity sweep (rewriteUserGUIDReferences) and the account
// admin tooling (CountUserGUIDReferences, MergeLocalUsers) walk it, so a new
// per-user table has exactly one place to be registered — miss it here and
// a reconcile or merge silently strands that table's rows under a GUID no
// account holds.
//
// Notes and their change log are split across the public and private
// databases (see noteEngine), so those run against both engines; the
// categories catalog, its change log, and invite tokens live only in the
// public database; saved_queries lives only in the private one (see
// createPrivateOnlySchema). It is built per call because pubDB/privDB are
// assigned when the databases open.
func userGUIDRefs() []userGUIDRef {
	both := []*dbEngine{pubDB, privDB}
	pub := []*dbEngine{pubDB}
	priv := []*dbEngine{privDB}
	return []userGUIDRef{
		{both, "notes", "created_by"},
		{both, "notes", "updated_by"},
		{both, "note_changes", "change_user"},
		{pub, "categories", "created_by"},
		{pub, "category_changes", "change_user"},
		{pub, "invite_tokens", "created_by"},
		{pub, "invite_tokens", "used_by"},
		{priv, "saved_queries", "user_guid"},
	}
}

// rewriteUserGUIDReferences re-points every row that names oldGUID at
// newGUID, across every column in userGUIDRefs.
//
// Each statement is `WHERE <col> = oldGUID`, which makes the whole sweep
// idempotent and re-runnable — see the ordering note in ReconcileHubUserGUID.
func rewriteUserGUIDReferences(oldGUID, newGUID string) error {
	var rewritten int64
	for _, ref := range userGUIDRefs() {
		// Table and column names come from the fixed inventory above, never
		// from input, so building the statement by concatenation is safe.
		stmt := `UPDATE ` + ref.table + ` SET ` + ref.column + ` = ? WHERE ` + ref.column + ` = ?`
		for _, en := range ref.engines {
			res, err := en.Exec(stmt, newGUID, oldGUID)
			if err != nil {
				return serr.Wrap(err, "failed to re-point user references",
					"statement", stmt)
			}
			n, _ := res.RowsAffected()
			rewritten += n
		}
	}

	if rewritten > 0 {
		logger.Info("Re-pointed rows onto a new user GUID",
			"rows", rewritten, "old_guid", oldGUID, "new_guid", newGUID)
	}
	return nil
}

// hubIdentityFromToken reads the user GUID and username out of a cached hub
// JWT WITHOUT verifying its signature.
//
// Unverified is correct here, not a shortcut. The signing key belongs to the
// hub; a spoke need not share it (it only has one if the deployment happens
// to reuse GONOTES_JWT_SECRET), so verification would fail on exactly the
// installations this is for. And there is nothing to defend: the token came
// out of this spoke's own sync_state, it is only ever replayed back to the
// hub as a bearer credential, and the hub validates it there. What we read
// from it is a hint about which local account to align — a claim the hub
// re-asserts, correctly signed, on the next login.
//
// This exists for the upgrade path: a spoke that logged in before
// sync_state learned to record the hub identity holds a valid token and may
// not log in again for a week.
func hubIdentityFromToken(tokenString string) (guid, username string) {
	if strings.TrimSpace(tokenString) == "" {
		return "", ""
	}
	claims := &TokenClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(tokenString, claims); err != nil {
		return "", "" // Unreadable cache entry — the next login supplies the truth
	}
	return claims.UserGUID, claims.Username
}
