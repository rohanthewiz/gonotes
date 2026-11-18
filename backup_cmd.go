package main

import (
	"fmt"
	"gonotes/models"
	"os"
	"path/filepath"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
	"github.com/urfave/cli/v2"
)

// backupCommand is `gonotes backup`: take a backup of the note databases now,
// or list the ones that exist. See models/backup.go for what a backup is and
// why unchanged data is not backed up twice.
//
// Two commands, with different needs:
//
//	gonotes backup        opens the databases, so nothing else may hold them
//	gonotes backup list   reads the backup directory only, so it works any time
//
// A running server (or a local-mode TUI) takes its own backups on a schedule,
// which is why the first command refusing beside one is not a gap: the process
// holding the files is the one that can copy them without stopping, and
// POST /api/v1/admin/backup asks it to do so on demand.
//
// Each command declares its own --dir so `gonotes backup -d <dir>` works;
// urfave/cli only accepts parent flags before the subcommand name.
func backupCommand(defaultDir string) *cli.Command {
	dirFlag := func() cli.Flag {
		return &cli.StringFlag{
			Name:    "dir",
			Aliases: []string{"d"},
			Value:   defaultDir,
			Usage:   "working directory for data and config",
		}
	}
	outFlag := func() cli.Flag {
		return &cli.StringFlag{
			Name:    "out",
			Aliases: []string{"o"},
			Usage:   "backup directory (default: $" + models.BackupDirEnv + ", else <dir>/data/backups)",
		}
	}

	return &cli.Command{
		Name:  "backup",
		Usage: "Back up the note databases now (server stopped), or list existing backups",
		Flags: []cli.Flag{
			dirFlag(),
			outFlag(),
			&cli.IntFlag{
				Name:  "keep",
				Usage: "how many backups to keep (default: $" + models.BackupKeepEnv + ", else 7)",
			},
		},
		Action: func(c *cli.Context) error {
			return runBackupNow(c.String("dir"), c.String("out"), c.Int("keep"))
		},
		Subcommands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List existing backups, newest first (works while the server runs)",
				Flags: []cli.Flag{dirFlag(), outFlag()},
				Action: func(c *cli.Context) error {
					return runBackupList(c.String("dir"), c.String("out"))
				},
			},
		},
	}
}

// enterDataDir switches into the working directory and loads its .env, the
// same two steps every command performs before touching the data. The .env
// matters here twice over: it may carry the encryption key the private
// database needs to open, and it may carry the backup settings themselves.
//
// It does not create the directory. Backing up a directory that does not
// exist can only be a mistyped -d, and creating it would produce a backup of
// two empty databases that looks like a successful run.
func enterDataDir(dir string) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("data directory %q does not exist", dir)
	}
	if err := os.Chdir(dir); err != nil {
		return serr.Wrap(err, "failed to change to directory", "dir", dir)
	}
	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}
	return nil
}

// backupConfigFor builds the effective config: environment first, then the
// command's own flags on top. out is resolved against the directory the
// command was typed in, not the data directory enterDataDir switches to, so
// `-o ./snapshots` means what it reads as.
func backupConfigFor(out string, keep int, invokedFrom string) models.BackupConfig {
	cfg := models.LoadBackupConfig()
	if out != "" {
		if !filepath.IsAbs(out) {
			out = filepath.Join(invokedFrom, out)
		}
		cfg.Dir = out
	}
	if keep > 0 {
		cfg.Keep = keep
	}
	return cfg
}

// runBackupNow opens the databases and takes one backup.
func runBackupNow(dir, out string, keep int) error {
	invokedFrom, _ := os.Getwd()
	if err := enterDataDir(dir); err != nil {
		return err
	}
	cfg := backupConfigFor(out, keep, invokedFrom)

	// Name the server when one holds the files: the lock underneath can only
	// say "another process", and here the useful answer is that the holder
	// already backs these notes up and can be asked to do it now.
	if url, busy := serverHoldingDataDir(models.ResolvedDataDir()); busy {
		return fmt.Errorf("a GoNotes server at %s has these notes open and backs them up itself "+
			"(see `gonotes backup list`); to take one now, POST %s/api/v1/admin/backup as an admin, "+
			"or stop the server and re-run", url, url)
	}

	// Info lines from opening and closing the databases would bury the three
	// lines this command prints.
	logger.SetLogLevel("error")
	if err := models.InitDB(); err != nil {
		if models.IsDBLocked(err) {
			return serr.Wrap(err, "another GoNotes process has these notes open; it takes its own backups "+
				"(see `gonotes backup list`), or stop it and re-run")
		}
		return serr.Wrap(err, "failed to open the databases")
	}
	defer models.CloseDB()

	res, err := models.BackupDatabases(cfg)
	if err != nil {
		return err
	}

	if res.Unchanged {
		fmt.Printf("Notes unchanged since the last backup; kept %s\n", res.Set.Path)
		return nil
	}
	fmt.Printf("Backed up to %s (%s)\n", res.Set.Path, humanBytes(res.Set.TotalBytes()))
	if res.Set.Private.Encrypted {
		fmt.Println("The private database copy is encrypted; restoring it needs the same " + models.EncryptionKeyEnvVar + ".")
	}
	if n := len(res.Pruned); n > 0 {
		fmt.Printf("Removed %d older backup(s), keeping %d.\n", n, cfg.Keep)
	}
	return nil
}

// runBackupList prints the existing sets. It opens no database.
func runBackupList(dir, out string) error {
	invokedFrom, _ := os.Getwd()
	if err := enterDataDir(dir); err != nil {
		return err
	}
	cfg := backupConfigFor(out, 0, invokedFrom)

	sets, err := models.ListBackups(cfg.Dir)
	if err != nil {
		return err
	}
	absDir, _ := filepath.Abs(cfg.Dir)
	if len(sets) == 0 {
		fmt.Printf("No backups in %s\n", absDir)
		return nil
	}

	fmt.Printf("%d backup(s) in %s, newest first:\n", len(sets), absDir)
	for _, s := range sets {
		enc := ""
		if s.Private.Encrypted {
			enc = "  private: encrypted"
		}
		fmt.Printf("  %s  %s  %8s%s\n", s.Name, s.TakenAt.Local().Format("2006-01-02 15:04"), humanBytes(s.TotalBytes()), enc)
	}
	fmt.Println()
	fmt.Println("To restore one, stop GoNotes and copy its two .bytdb files over those in " +
		filepath.Join(dir, "data") + ".")
	if age := time.Since(sets[0].TakenAt); cfg.Interval > 0 && age > 2*cfg.Interval {
		fmt.Printf("Note: the newest backup is %s old, more than twice the %s schedule. "+
			"Backups are taken by a running server or local TUI.\n",
			age.Round(time.Hour), cfg.Interval)
	}
	return nil
}

// humanBytes renders a byte count in binary units, one decimal place.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
