package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// backup.go takes point-in-time copies of the two note databases and keeps a
// short rotation of them.
//
// WHY THIS EXISTS. Every note lives in two bytdb files and nowhere else. Sync
// is not a backup: a hub faithfully relays a bad edit, a mass delete or a
// botched migration to every spoke. A backup is the one copy that does not
// follow the live data, so it is what a mistake is recovered from.
//
// WHAT A BACKUP IS. One directory per snapshot (a "set"), holding a copy of
// each database plus a manifest:
//
//	<backup dir>/
//	  20261118T203000Z/            one set, named by its UTC time (sorts by age)
//	    notes_public.bytdb
//	    notes_private.bytdb        still ciphertext when the live one is
//	    manifest.json              sizes + SHA-256 of both files
//	  20261119T203000Z/
//	  .tmp-20261120T203000Z/       a set still being written (never listed)
//
// The copies come from bytdb's online backup (Engine.BackupTo): a byte copy of
// the append-only log up to the length captured at the call, so it holds every
// committed transaction whole and never blocks readers or writers. It needs no
// second process and no pause, which is why the running server takes its own
// backups instead of a cron job that would have to stop it first (the files
// are single-process: see IsDBLocked).
//
// Because the copy is of the log bytes, the private database's backup is
// encrypted exactly as the live file is. No key is written anywhere; restoring
// needs the same GONOTES_ENCRYPTION_KEY the live database needed.
//
// HOW A SET BECOMES VISIBLE. Both files are written and fsynced inside a
// ".tmp-" directory, and the directory is renamed to its final name last. A
// set therefore either exists complete or does not exist: a crash mid-backup
// leaves a ".tmp-" directory that the next run sweeps away, never a
// half-written set that looks restorable.
//
// WHY UNCHANGED DATA IS NOT BACKED UP AGAIN. Rotation keeps the newest N sets.
// If an idle week produced seven identical sets, they would push out the last
// set that differed, and the rotation would hold one distinct state instead of
// seven. So a new set whose two files hash the same as the newest existing set
// is discarded. The comparison is by content hash rather than by bytdb's log
// epoch and size, because the epoch restarts at zero on every open and so
// cannot be compared across process restarts.
//
// RESTORING is deliberately a file copy, with the server stopped:
//
//	cp <set>/notes_public.bytdb  <dir>/data/notes_public.bytdb
//	cp <set>/notes_private.bytdb <dir>/data/notes_private.bytdb
//
// A backup file is a complete database, so opening it is the restore.

// Backup environment variables. All optional.
const (
	// BackupIntervalEnv is how often a running process takes a backup, as a Go
	// duration ("24h", "6h"). "0" or "off" turns scheduled backups off; on-demand
	// ones still work.
	BackupIntervalEnv = "GONOTES_BACKUP_INTERVAL"
	// BackupKeepEnv is how many sets the rotation keeps.
	BackupKeepEnv = "GONOTES_BACKUP_KEEP"
	// BackupDirEnv moves the backup directory, typically onto another disk or
	// a synced folder. Relative paths resolve against the working directory.
	BackupDirEnv = "GONOTES_BACKUP_DIR"
)

// Backup defaults. A day and a week: enough history to notice a mistake and
// still have a copy from before it, at a disk cost of seven times a database
// that is megabytes in size.
const (
	DefaultBackupInterval = 24 * time.Hour
	DefaultBackupKeep     = 7
	DefaultBackupDir      = DataDir + "/backups"
)

// backupTmpPrefix marks a set that is still being written.
const backupTmpPrefix = ".tmp-"

// backupManifestName is the manifest file inside each set.
const backupManifestName = "manifest.json"

// backupStampLayout names a set by its UTC time. Lexical order is
// chronological order, which is what rotation and "newest" rely on.
const backupStampLayout = "20060102T150405Z"

// backupSetName matches a finished set's directory name: a stamp, optionally
// followed by "-N" when two sets were taken within the same second. Rotation
// deletes only directories matching this, so anything else a person keeps in
// the backup directory is left alone.
var backupSetName = regexp.MustCompile(`^\d{8}T\d{6}Z(-\d+)?$`)

// backupMu serializes backup runs. A scheduled pass and an on-demand one
// overlapping would each prune while the other is mid-write, and two sets
// taken in the same instant are worth nothing anyway.
var backupMu sync.Mutex

// BackupConfig is where backups go and how many are kept.
type BackupConfig struct {
	// Dir holds one sub-directory per set.
	Dir string
	// Interval is the scheduled cadence. Zero means no schedule.
	Interval time.Duration
	// Keep is how many sets rotation retains. Always at least 1.
	Keep int
}

// LoadBackupConfig reads the backup settings from the environment, falling
// back to the defaults. A malformed value is logged and replaced by its
// default rather than failing startup: a typo in a backup setting must not be
// the reason the notes do not open.
func LoadBackupConfig() BackupConfig {
	cfg := BackupConfig{
		Dir:      DefaultBackupDir,
		Interval: DefaultBackupInterval,
		Keep:     DefaultBackupKeep,
	}

	if raw := strings.TrimSpace(os.Getenv(BackupDirEnv)); raw != "" {
		cfg.Dir = raw
	}

	if raw := strings.TrimSpace(os.Getenv(BackupIntervalEnv)); raw != "" {
		switch strings.ToLower(raw) {
		case "0", "off", "false", "never", "none":
			cfg.Interval = 0
		default:
			d, err := time.ParseDuration(raw)
			if err != nil || d < 0 {
				logger.Warn("Ignoring invalid "+BackupIntervalEnv+"; expected a duration like 24h, or off",
					"value", raw, "using", cfg.Interval.String())
			} else {
				cfg.Interval = d
			}
		}
	}

	if raw := strings.TrimSpace(os.Getenv(BackupKeepEnv)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			logger.Warn("Ignoring invalid "+BackupKeepEnv+"; expected a whole number of at least 1",
				"value", raw, "using", strconv.Itoa(cfg.Keep))
		} else {
			cfg.Keep = n
		}
	}

	return cfg
}

// BackupFile describes one database copy inside a set.
type BackupFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	// Encrypted records whether the source database was key-encrypted, and so
	// whether this copy needs the key to open.
	Encrypted bool `json:"encrypted"`
}

// BackupSet describes one snapshot. It is also the manifest's JSON shape.
type BackupSet struct {
	// Name is the set's directory name (its UTC stamp).
	Name string `json:"name"`
	// Path is the set's directory. Filled in when listing; not stored, since a
	// backup directory can be moved.
	Path    string     `json:"path,omitempty"`
	TakenAt time.Time  `json:"taken_at"`
	Public  BackupFile `json:"public"`
	Private BackupFile `json:"private"`
}

// TotalBytes is the set's size on disk, manifest aside.
func (s BackupSet) TotalBytes() int64 { return s.Public.Bytes + s.Private.Bytes }

// sameContent reports whether two sets hold byte-identical databases.
func (s BackupSet) sameContent(o BackupSet) bool {
	return s.Public.SHA256 != "" && s.Public.SHA256 == o.Public.SHA256 &&
		s.Private.SHA256 == o.Private.SHA256
}

// BackupResult is the outcome of one backup run.
type BackupResult struct {
	// Set is the set that now represents the current data: the new one, or the
	// existing newest one when Unchanged.
	Set BackupSet `json:"set"`
	// Unchanged is true when the data matched the newest set and no new set
	// was kept.
	Unchanged bool `json:"unchanged"`
	// Pruned names the sets rotation removed.
	Pruned []string `json:"pruned,omitempty"`
}

// BackupDatabases takes a backup of both open databases into cfg.Dir and
// applies rotation. It is safe to call while the databases are in use.
//
// Order of work, and why:
//
//	sweep stale .tmp- dirs
//	      │
//	copy public + private into .tmp-<stamp>   (hashing while copying)
//	      │
//	same hashes as the newest set? ──yes──▶ delete the .tmp- dir, report Unchanged
//	      │ no
//	write manifest, fsync, rename to <stamp>   ◀── the set exists from here
//	      │
//	prune to cfg.Keep                          ◀── only after the new set is safe
//
// Pruning comes last so that a failed backup never costs an old one.
func BackupDatabases(cfg BackupConfig) (*BackupResult, error) {
	if pubDB == nil || privDB == nil {
		return nil, serr.New("database not initialized - call InitDB first")
	}
	if cfg.Dir == "" {
		cfg.Dir = DefaultBackupDir
	}
	if cfg.Keep < 1 {
		cfg.Keep = DefaultBackupKeep
	}

	backupMu.Lock()
	defer backupMu.Unlock()

	// 0700/0600 throughout: the public database carries password hashes, and
	// the private one is plaintext on disk when no encryption key is set.
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, serr.Wrap(err, "failed to create backup directory", "dir", cfg.Dir)
	}
	sweepStaleBackupTemps(cfg.Dir)

	now := time.Now().UTC()
	stamp := now.Format(backupStampLayout)
	tmpDir := filepath.Join(cfg.Dir, backupTmpPrefix+stamp)
	// A leftover temp dir with this exact stamp can only be from a run that
	// died within the same second; start clean either way.
	_ = os.RemoveAll(tmpDir)
	if err := os.Mkdir(tmpDir, 0o700); err != nil {
		return nil, serr.Wrap(err, "failed to create backup staging directory", "dir", tmpDir)
	}
	// Any early return below leaves nothing behind. After the rename the path
	// no longer exists, so this is a no-op on success.
	defer os.RemoveAll(tmpDir)

	set := BackupSet{TakenAt: now}
	var err error
	if set.Public, err = backupEngineTo(pubDB, filepath.Join(tmpDir, filepath.Base(PublicDBPath))); err != nil {
		return nil, serr.Wrap(err, "failed to back up the public database")
	}
	if set.Private, err = backupEngineTo(privDB, filepath.Join(tmpDir, filepath.Base(PrivateDBPath))); err != nil {
		return nil, serr.Wrap(err, "failed to back up the private database")
	}

	existing, err := ListBackups(cfg.Dir)
	if err != nil {
		return nil, err
	}

	// Identical to the newest set: keep that one and drop this copy, so the
	// rotation's N slots hold N distinct states.
	if len(existing) > 0 && existing[0].sameContent(set) {
		return &BackupResult{Set: existing[0], Unchanged: true}, nil
	}

	// The new set must sort AFTER every existing one, because rotation keeps
	// the names that sort newest. A stamp alone does not guarantee that: two
	// sets within one second share it, and a clock stepped backwards produces
	// an earlier one. In both cases the new set takes the newest existing
	// stamp with the next "-N", so it can never be the set its own rotation
	// pass removes. (TakenAt in the manifest still records the real time.)
	set.Name = stamp
	if len(existing) > 0 {
		newestStamp, newestSeq := splitBackupSetName(existing[0].Name)
		if stamp <= newestStamp {
			set.Name = newestStamp + "-" + strconv.Itoa(newestSeq+1)
		}
	}
	finalDir := filepath.Join(cfg.Dir, set.Name)

	if err := writeBackupManifest(tmpDir, set); err != nil {
		return nil, err
	}
	syncDir(tmpDir)
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return nil, serr.Wrap(err, "failed to publish backup set", "dir", finalDir)
	}
	syncDir(cfg.Dir)
	set.Path = finalDir

	res := &BackupResult{Set: set}
	res.Pruned = pruneBackups(cfg.Dir, cfg.Keep)
	return res, nil
}

// backupEngineTo streams one engine's backup into path, hashing as it goes so
// the copy is read once. The file is fsynced before it is closed: the rename
// that later publishes the set must not be able to outlive the bytes it
// points at.
func backupEngineTo(en *dbEngine, path string) (BackupFile, error) {
	bf := BackupFile{Name: filepath.Base(path), Encrypted: en.encrypted}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return bf, serr.Wrap(err, "failed to create backup file", "path", path)
	}
	hasher := sha256.New()
	n, err := en.eng.BackupTo(io.MultiWriter(f, hasher))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return bf, serr.Wrap(err, "failed to write backup file", "path", path)
	}

	bf.Bytes = n
	bf.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return bf, nil
}

// writeBackupManifest writes the set's manifest into dir and fsyncs it.
func writeBackupManifest(dir string, set BackupSet) error {
	set.Path = "" // a location is not a property of the set; see BackupSet.Path
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return serr.Wrap(err, "failed to encode backup manifest")
	}
	path := filepath.Join(dir, backupManifestName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return serr.Wrap(err, "failed to create backup manifest", "path", path)
	}
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return serr.Wrap(err, "failed to write backup manifest", "path", path)
	}
	return nil
}

// syncDir fsyncs a directory so a rename or create inside it is durable.
// Best-effort: some filesystems refuse to fsync a directory, and the backup is
// no worse for it than any other freshly written file.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// ListBackups returns the finished sets in dir, newest first. A directory that
// does not exist yet is an empty list, not an error.
//
// A set whose manifest is missing or unreadable is still listed, by name and
// with its time read from the name: the database files are what restore needs,
// and hiding a restorable set over a damaged sidecar would be the wrong way to
// fail. Such a set simply never matches as "unchanged".
func ListBackups(dir string) ([]BackupSet, error) {
	if dir == "" {
		dir = DefaultBackupDir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, serr.Wrap(err, "failed to read backup directory", "dir", dir)
	}

	var sets []BackupSet
	for _, e := range entries {
		if !e.IsDir() || !backupSetName.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		set := BackupSet{}
		if data, rerr := os.ReadFile(filepath.Join(path, backupManifestName)); rerr == nil {
			_ = json.Unmarshal(data, &set) // a bad manifest leaves the zero set
		}
		set.Name = e.Name()
		set.Path = path
		if set.TakenAt.IsZero() {
			// The name's first 16 characters are the stamp; a "-N" suffix follows.
			if t, perr := time.Parse(backupStampLayout, e.Name()[:len(backupStampLayout)]); perr == nil {
				set.TakenAt = t
			}
		}
		sets = append(sets, set)
	}

	// Newest first: by stamp, then by the numeric "-N". The suffix is compared
	// as a number because "-10" sorts before "-9" as text.
	sort.Slice(sets, func(i, j int) bool {
		si, ni := splitBackupSetName(sets[i].Name)
		sj, nj := splitBackupSetName(sets[j].Name)
		if si != sj {
			return si > sj
		}
		return ni > nj
	})
	return sets, nil
}

// splitBackupSetName splits a set's directory name into its stamp and its
// sequence within that stamp. A bare stamp is sequence 1, so the first "-N"
// suffix handed out is "-2".
func splitBackupSetName(name string) (stamp string, seq int) {
	stamp, suffix, found := strings.Cut(name, "-")
	if !found {
		return stamp, 1
	}
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 1 {
		return stamp, 1
	}
	return stamp, n
}

// pruneBackups removes all but the newest keep sets and returns the names it
// removed. Failures are logged and skipped: an undeletable old set is a disk
// space problem for later, not a reason to report the new backup as failed.
func pruneBackups(dir string, keep int) []string {
	sets, err := ListBackups(dir)
	if err != nil || len(sets) <= keep {
		return nil
	}
	var pruned []string
	for _, s := range sets[keep:] {
		if err := os.RemoveAll(s.Path); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to remove an old backup set", "set", s.Path))
			continue
		}
		pruned = append(pruned, s.Name)
	}
	return pruned
}

// staleBackupTempAge is how old a ".tmp-" directory must be before a sweep
// removes it. Backups are serialized within a process by backupMu, so the age
// only matters for a temp dir belonging to another process on the same backup
// directory, and no backup of a notes database runs for an hour.
const staleBackupTempAge = time.Hour

// sweepStaleBackupTemps removes staging directories abandoned by a run that
// crashed or was killed.
func sweepStaleBackupTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), backupTmpPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < staleBackupTempAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// StartBackupSchedule takes a backup every cfg.Interval for as long as the
// process runs, and returns a stop function that waits for a pass in progress
// to finish. Callers stop it before CloseDB so a pass is never mid-copy when
// the engines close. A zero interval starts nothing.
//
// The first pass is timed from the newest existing set, not from process
// start:
//
//	newest set age ≥ interval (or no set)  ──▶ back up now
//	newest set age <  interval             ──▶ wait out the remainder
//
// Timing from process start would mean a laptop that runs the server for a few
// hours a day never reaches a 24h tick and never backs up. Timing from the
// newest set also means a restart loop cannot produce a backup per restart.
func StartBackupSchedule(cfg BackupConfig) (stop func()) {
	if cfg.Interval <= 0 {
		logger.Info("Scheduled backups are off", "env", BackupIntervalEnv)
		return func() {}
	}

	done := make(chan struct{})
	quit := make(chan struct{})
	go func() {
		defer close(done)
		for {
			wait := time.Duration(0)
			if sets, err := ListBackups(cfg.Dir); err == nil && len(sets) > 0 {
				if age := time.Since(sets[0].TakenAt); age < cfg.Interval {
					wait = cfg.Interval - age
				}
			}
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-quit:
					timer.Stop()
					return
				case <-timer.C:
				}
			}

			res, err := BackupDatabases(cfg)
			switch {
			case err != nil:
				logger.LogErr(serr.Wrap(err, "scheduled backup failed"))
			case res.Unchanged:
				logger.Info("Backup skipped: notes unchanged since the last one", "set", res.Set.Name)
			default:
				logger.Info("Backup taken", "set", res.Set.Path,
					"bytes", strconv.FormatInt(res.Set.TotalBytes(), 10),
					"pruned", strconv.Itoa(len(res.Pruned)))
			}

			// After a pass that kept no new set (a failure, or unchanged data)
			// the newest set is still old, so the age computed at the top of
			// the loop would be "due" again at once. Wait a full interval here
			// instead; this is the only thing between that case and a busy loop.
			if err != nil || res.Unchanged {
				timer := time.NewTimer(cfg.Interval)
				select {
				case <-quit:
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}()
	logger.Info("Backups scheduled", "every", cfg.Interval.String(),
		"keep", strconv.Itoa(cfg.Keep), "dir", cfg.Dir)

	return func() {
		close(quit)
		<-done
	}
}
