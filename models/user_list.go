package models

import "github.com/rohanthewiz/serr"

// ListUsernames returns the usernames of all active users, oldest first.
//
// Added for the TUI login screen: with the names in hand it can prefill the
// username when there is exactly one account (the common self-hosted case)
// or switch to registration mode when the database is fresh. Only usernames
// are exposed — no hashes or emails — since this runs pre-authentication.
func ListUsernames() ([]string, error) {
	rows, err := db.Query(`SELECT username FROM users WHERE is_active = true ORDER BY created_at`)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query usernames")
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, serr.Wrap(err, "failed to scan username")
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
