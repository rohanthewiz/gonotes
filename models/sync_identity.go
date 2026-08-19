package models

import (
	"database/sql"
	"strconv"
	"strings"

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
func RecordHubIdentity(hubURL, hubUserGUID, hubUsername string) error {
	if hubURL == "" || hubUserGUID == "" {
		return nil // Nothing learned — not an error, just no news
	}
	_, err := pubDB.Exec(
		`UPDATE sync_state SET hub_user_guid = ?, hub_username = ? WHERE hub_url = ?`,
		hubUserGUID, hubUsername, hubURL,
	)
	if err != nil {
		return serr.Wrap(err, "failed to record hub user identity", "hub_url", hubURL)
	}
	return nil
}

// hubIdentityForUsername returns the hub user GUID this spoke has recorded
// for the given username, or "" if there is none. Matching on the username
// rather than just taking the single sync_state row is the safety catch:
// adopting a GUID is only correct when the two accounts are the same account,
// and the username is the only evidence of that we have locally.
func hubIdentityForUsername(username string) (string, error) {
	var guid sql.NullString
	err := pubDB.QueryRow(
		`SELECT hub_user_guid FROM sync_state
		 WHERE hub_username = ? AND hub_user_guid IS NOT NULL AND hub_user_guid <> ''
		 LIMIT 1`, username,
	).Scan(&guid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", serr.Wrap(err, "failed to look up recorded hub identity", "username", username)
	}
	if !guid.Valid {
		return "", nil
	}
	return guid.String, nil
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
		// the visible symptom, and it is worth naming: the fix is to register
		// locally under the sync username, which will then adopt the GUID.
		if n, err := countLocalUsers(); err == nil && n > 0 {
			logger.Info("No local account matches the sync username — notes pulled from the hub "+
				"will not be visible until one is registered under that name",
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

// rewriteUserGUIDReferences re-points every row that names oldGUID at
// newGUID. Notes and their change log are split across the public and
// private databases (see noteEngine), so those run against both engines;
// the categories catalog, its change log, and invite tokens live only in
// the public database.
//
// Each statement is `WHERE <col> = oldGUID`, which makes the whole sweep
// idempotent and re-runnable — see the ordering note in ReconcileHubUserGUID.
func rewriteUserGUIDReferences(oldGUID, newGUID string) error {
	type stmt struct {
		engines []*dbEngine
		sql     string
	}
	both := []*dbEngine{pubDB, privDB}
	pub := []*dbEngine{pubDB}

	sweeps := []stmt{
		{both, `UPDATE notes SET created_by = ? WHERE created_by = ?`},
		{both, `UPDATE notes SET updated_by = ? WHERE updated_by = ?`},
		{both, `UPDATE note_changes SET change_user = ? WHERE change_user = ?`},
		{pub, `UPDATE categories SET created_by = ? WHERE created_by = ?`},
		{pub, `UPDATE category_changes SET change_user = ? WHERE change_user = ?`},
		{pub, `UPDATE invite_tokens SET created_by = ? WHERE created_by = ?`},
		{pub, `UPDATE invite_tokens SET used_by = ? WHERE used_by = ?`},
	}

	var rewritten int64
	for _, s := range sweeps {
		for _, en := range s.engines {
			res, err := en.Exec(s.sql, newGUID, oldGUID)
			if err != nil {
				return serr.Wrap(err, "failed to re-point user references during identity adoption",
					"statement", s.sql)
			}
			n, _ := res.RowsAffected()
			rewritten += n
		}
	}

	if rewritten > 0 {
		logger.Info("Re-pointed rows onto the hub user GUID",
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
