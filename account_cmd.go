package main

import (
	"fmt"
	"gonotes/models"
	"gonotes/tui"
	"os"
	"strings"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
	"github.com/urfave/cli/v2"
)

// accountCommand is `gonotes account`: list, rename and merge local accounts.
//
// It exists to repair one situation the sync code diagnoses but deliberately
// will not fix by itself — a local account named differently from
// GONOTES_SYNC_USERNAME, whose pulled notes are therefore owned by a hub GUID
// nobody here can log in as. See models/account_admin.go for the mechanism.
//
// It is a CLI command rather than a web endpoint on purpose: it is a rare,
// operator-level repair, and it rewrites the identity of the very account a
// web session would be authenticated as. The cost is that the server must be
// stopped first — withAccountDB checks, since nothing at the storage layer
// would stop two processes opening the same files.
//
// Each leaf command declares its own --dir so `gonotes account list -d <dir>`
// works; urfave/cli only accepts parent flags before the subcommand name.
func accountCommand(defaultDir string) *cli.Command {
	dirFlag := func() cli.Flag {
		return &cli.StringFlag{
			Name:    "dir",
			Aliases: []string{"d"},
			Value:   defaultDir,
			Usage:   "working directory for data and config",
		}
	}
	// Constructors rather than shared values: a flag instance records
	// parse state, so each command gets its own.
	dryRunFlag := func() cli.Flag {
		return &cli.BoolFlag{
			Name:  "dry-run",
			Usage: "show what would change without changing anything",
		}
	}

	return &cli.Command{
		Name:  "account",
		Usage: "List, rename or merge local accounts (e.g. to match GONOTES_SYNC_USERNAME)",
		Subcommands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List local accounts, what each owns, and the recorded hub identity",
				Flags: []cli.Flag{dirFlag()},
				Action: func(c *cli.Context) error {
					return withAccountDB(c.String("dir"), runAccountList)
				},
			},
			{
				Name:  "rename",
				Usage: "Rename a local account; if the new name is the sync username, it adopts the hub identity",
				Flags: []cli.Flag{
					dirFlag(),
					&cli.StringFlag{Name: "from", Usage: "current username", Required: true},
					&cli.StringFlag{Name: "to", Usage: "new username", Required: true},
					dryRunFlag(),
				},
				Action: func(c *cli.Context) error {
					return withAccountDB(c.String("dir"), func() error {
						return runAccountRename(c.String("from"), c.String("to"), c.Bool("dry-run"))
					})
				},
			},
			{
				Name:  "merge",
				Usage: "Move everything one local account owns into another, then delete the first",
				Flags: []cli.Flag{
					dirFlag(),
					&cli.StringFlag{Name: "from", Usage: "account to fold in and delete", Required: true},
					&cli.StringFlag{Name: "into", Usage: "account that survives", Required: true},
					dryRunFlag(),
				},
				Action: func(c *cli.Context) error {
					return withAccountDB(c.String("dir"), func() error {
						return runAccountMerge(c.String("from"), c.String("into"), c.Bool("dry-run"))
					})
				},
			},
		},
	}
}

// withAccountDB opens the data directory's databases around fn.
//
// Unlike import commands it does NOT create the directory: repairing accounts
// in a directory that does not exist yet can only be a mistyped -d, and
// creating it would answer with an empty, confusing "no accounts".
func withAccountDB(dir string, fn func() error) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("data directory %q does not exist", dir)
	}
	if err := os.Chdir(dir); err != nil {
		return serr.Wrap(err, "failed to change to directory", "dir", dir)
	}

	// The env file carries the encryption key; without it the private
	// database would not open, and its rows (private notes, saved queries)
	// could not be moved.
	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}

	if url, busy := serverHoldingDataDir(models.ResolvedDataDir()); busy {
		return fmt.Errorf("a GoNotes server at %s is using the notes in %q; stop it (or the MacApp) first, then re-run", url, dir)
	}

	if err := models.InitDB(); err != nil {
		return serr.Wrap(err, "failed to open the databases")
	}
	defer models.CloseDB()

	return fn()
}

// serverHoldingDataDir reports whether a live GoNotes server on this machine
// is serving dataDir, and at which URL.
//
// WHY THIS IS NEEDED. Neither bytdb nor btypedb takes an OS-level file lock,
// so nothing stops a second process from opening databases a server already
// has open — verified by running `account list` beside a live server. Two
// writers on one WAL (and two background compactors) is corruption, and a
// rename or merge is exactly a burst of writes. So the command checks first.
//
// HOW. The same /api/v1/health probe the TUI uses, at every URL a local
// server is likely to be on: GONOTES_URL, the port from GONOTES_PORT / PORT
// (read after the .env load, so the directory's own config counts), and the
// default. A server that answers and reports the same resolved directory is
// holding these files. One that answers WITHOUT reporting a directory (a
// build that predates the field) cannot be ruled out, so it counts too —
// refusing a repair is cheap, corrupting the notes is not.
//
// It is best-effort: a server on a port none of these name is not found.
func serverHoldingDataDir(dataDir string) (string, bool) {
	candidates := []string{}
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("GONOTES_URL")), "/"); u != "" {
		candidates = append(candidates, u)
	}
	for _, env := range []string{"GONOTES_PORT", "PORT"} {
		if p := strings.TrimSpace(os.Getenv(env)); p != "" {
			candidates = append(candidates, "http://localhost:"+p)
		}
	}
	candidates = append(candidates, tui.DefaultServerURL)

	seen := map[string]bool{}
	for _, u := range candidates {
		if seen[u] {
			continue
		}
		seen[u] = true
		info, up := tui.ProbeServer(u, tuiProbeTimeout)
		if !up {
			continue
		}
		if info.DataDir == "" || dataDir == "" || sameDir(info.DataDir, dataDir) {
			return u, true
		}
	}
	return "", false
}

// runAccountList prints every local account with a summary of what it owns,
// and the hub identity sync has recorded, flagging the mismatch N-025 is about.
func runAccountList() error {
	users, err := models.ListLocalUsers()
	if err != nil {
		return err
	}
	hubs, err := models.RecordedHubIdentities()
	if err != nil {
		return err
	}

	if len(users) == 0 {
		fmt.Println("No local accounts.")
	}
	for _, u := range users {
		counts, err := models.CountUserGUIDReferences(u.GUID)
		if err != nil {
			return err
		}
		tag := ""
		if u.IsAdmin {
			tag = " [admin]"
		}
		fmt.Printf("%s%s\n  guid: %s\n  owns: %s\n", u.Username, tag, u.GUID, summarizeCounts(counts))
	}

	if len(hubs) == 0 {
		fmt.Println("\nNo hub identity recorded (not a sync spoke, or never logged in to a hub).")
		return nil
	}

	fmt.Println()
	for _, h := range hubs {
		fmt.Printf("Hub %s: signed in as %q (guid %s)\n", h.HubURL, h.Username, h.UserGUID)
		describeHubMatch(users, h)
	}
	return nil
}

// describeHubMatch explains, for one hub identity, whether a local account is
// aligned with it and, if not, which command would align it.
func describeHubMatch(users []models.User, h models.HubIdentity) {
	var named, holder *models.User
	for i := range users {
		if users[i].Username == h.Username {
			named = &users[i]
		}
		if users[i].GUID == h.UserGUID {
			holder = &users[i]
		}
	}

	switch {
	case named != nil && named.GUID == h.UserGUID:
		fmt.Println("  ✓ aligned: the local account of that name holds the hub GUID")
		// Aligned, but other accounts may still own notes — the leftover from
		// re-registering under the sync name, per the old advice. Only the
		// operator knows whether they are the same person, so suggest, don't
		// assume.
		for _, u := range users {
			if u.ID != named.ID {
				fmt.Printf("    if %q is also you: gonotes account merge --from %s --into %s\n",
					u.Username, u.Username, named.Username)
			}
		}
	case named != nil:
		// Name matches but GUID does not: the next sync login or server start
		// reconciles it automatically, unless another account holds the GUID.
		if holder != nil {
			fmt.Printf("  ✗ %q holds the hub GUID but %q is the sync account — merge one into the other:\n"+
				"      gonotes account merge --from %s --into %s\n",
				holder.Username, named.Username, holder.Username, named.Username)
		} else {
			fmt.Println("  … name matches; the hub GUID is adopted on the next server start or sync login")
		}
	case len(users) == 0:
		fmt.Println("  … no local accounts; register under that name and it adopts the hub GUID")
	default:
		// No account under the sync name: the case the reconcile refuses to
		// guess about. Suggest the rename when there is one obvious candidate.
		fmt.Printf("  ✗ no local account is named %q, so pulled notes are invisible here\n", h.Username)
		if len(users) == 1 {
			fmt.Printf("      gonotes account rename --from %s --to %s\n", users[0].Username, h.Username)
		} else {
			fmt.Printf("      gonotes account rename --from <yours> --to %s\n", h.Username)
		}
	}
}

// runAccountRename renames an account, then immediately runs the hub
// alignment the new name may now qualify for.
func runAccountRename(from, to string, dryRun bool) error {
	if dryRun {
		u, err := models.GetUserByUsername(from)
		if err != nil {
			return err
		}
		if u == nil {
			return fmt.Errorf("no local account named %q", from)
		}
		if other, err := models.GetUserByUsername(to); err != nil {
			return err
		} else if other != nil {
			return fmt.Errorf("a local account named %q already exists; use `gonotes account merge`", to)
		}
		if err := models.ValidateUsername(to); err != nil {
			return err
		}
		counts, err := models.CountUserGUIDReferences(u.GUID)
		if err != nil {
			return err
		}
		fmt.Printf("Would rename %q → %q (guid %s, owns %s).\n", from, to, u.GUID, summarizeCounts(counts))
		printHubAlignmentPreview(to)
		return nil
	}

	user, err := models.RenameLocalUser(from, to)
	if err != nil {
		return err
	}
	fmt.Printf("Renamed %q → %q.\n", from, to)
	return alignAndReport(user.Username)
}

// runAccountMerge folds one account into another, then aligns the survivor
// with the hub identity if it is the sync account.
func runAccountMerge(from, into string, dryRun bool) error {
	src, err := models.GetUserByUsername(from)
	if err != nil {
		return err
	}
	dst, err := models.GetUserByUsername(into)
	if err != nil {
		return err
	}
	// Collisions are looked up before the merge, while both GUIDs still
	// own their own categories; afterwards they are indistinguishable.
	var collisions []string
	if src != nil && dst != nil && src.ID != dst.ID {
		if collisions, err = models.CategoryNameCollisions(src.GUID, dst.GUID); err != nil {
			return err
		}
	}

	if dryRun {
		if src == nil {
			return fmt.Errorf("no local account named %q", from)
		}
		if dst == nil {
			return fmt.Errorf("no local account named %q; use `gonotes account rename`", into)
		}
		if src.ID == dst.ID {
			return fmt.Errorf("cannot merge an account into itself")
		}
		counts, err := models.CountUserGUIDReferences(src.GUID)
		if err != nil {
			return err
		}
		fmt.Printf("Would move from %q to %q: %s\n", from, into, summarizeCounts(counts))
		fmt.Printf("Would then delete account %q.\n", from)
		if src.IsAdmin && !dst.IsAdmin {
			fmt.Printf("Would make %q an admin (carried over from %q).\n", into, from)
		}
		printCollisions(collisions)
		printHubAlignmentPreview(into)
		return nil
	}

	if _, err := models.MergeLocalUsers(from, into); err != nil {
		return err
	}
	fmt.Printf("Merged %q into %q; %q is deleted.\n", from, into, from)
	printCollisions(collisions)
	return alignAndReport(into)
}

// alignAndReport runs the hub alignment for username and tells the operator
// what happened, including the one follow-up a GUID change requires.
func alignAndReport(username string) error {
	changed, err := models.AlignLocalUserWithHub(username)
	if err != nil {
		// The rename/merge itself succeeded; only the alignment did not.
		// Report it as a failure so scripts notice, with the state stated.
		return serr.Wrap(err, "account change succeeded, but aligning with the hub identity failed")
	}
	if changed {
		fmt.Printf("%q adopted its hub identity; notes pulled from the hub are now visible to it.\n", username)
	}
	// Login tokens carry the user GUID and the auth middleware trusts it
	// without a lookup, so a session opened before a rename-with-adoption or
	// a merge keeps acting as the old GUID until it signs in again.
	fmt.Println("Sign out and back in on any open browser or TUI session.")
	return nil
}

// printHubAlignmentPreview says whether the account would adopt a hub GUID
// after the change, for --dry-run.
func printHubAlignmentPreview(username string) {
	hubs, err := models.RecordedHubIdentities()
	if err != nil {
		logger.LogErr(err, "could not read recorded hub identities")
		return
	}
	for _, h := range hubs {
		if h.Username != username {
			continue
		}
		// After a merge the survivor may already hold the hub GUID (it was
		// registered under the sync name); only a change is worth announcing.
		if u, err := models.GetUserByUsername(username); err == nil && u != nil && u.GUID == h.UserGUID {
			fmt.Printf("%q already holds the hub GUID for %s.\n", username, h.HubURL)
			return
		}
		fmt.Printf("%q matches the sync account on %s; it would adopt hub guid %s.\n",
			username, h.HubURL, h.UserGUID)
		return
	}
}

func printCollisions(names []string) {
	if len(names) == 0 {
		return
	}
	fmt.Printf("Note: both accounts have categories named %s; the merged account will list each twice.\n"+
		"Rename or combine them in the app.\n", strings.Join(quoteAll(names), ", "))
}

// summarizeCounts renders the non-zero reference counts compactly, e.g.
// "notes.created_by=12, saved_queries.user_guid=3", or "nothing".
func summarizeCounts(counts []models.UserRefCount) string {
	var parts []string
	for _, c := range counts {
		if c.Rows > 0 {
			parts = append(parts, fmt.Sprintf("%s.%s=%d", c.Table, c.Column, c.Rows))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
