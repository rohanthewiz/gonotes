package models

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Backup tests. They pin the properties a backup is worth having for: a set
// restores to the notes as they were when it was taken, it is never visible
// half-written, unchanged data does not use up a rotation slot, and rotation
// removes only its own sets.

// backupTestDB opens a scratch pair of databases and returns a config whose
// backup directory sits beside them.
func backupTestDB(t *testing.T) BackupConfig {
	t.Helper()
	dir := t.TempDir()
	if err := InitTestDB(dir); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	t.Cleanup(func() {
		if pubDB != nil {
			CloseDB()
		}
	})
	return BackupConfig{Dir: filepath.Join(dir, "backups"), Keep: 3}
}

// addBackupTestNote creates one note so the next backup differs from the last.
func addBackupTestNote(t *testing.T, guid, title string, private bool) {
	t.Helper()
	body := "body of " + title
	if _, err := CreateNote(NoteInput{GUID: guid, Title: title, Body: &body, IsPrivate: private}, "user-1"); err != nil {
		t.Fatalf("CreateNote(%s): %v", title, err)
	}
}

// The point of the whole feature: a set, opened as a database, holds the notes
// that existed when it was taken and none written afterwards. Both databases
// are checked, since each is copied by its own call.
func TestBackupRestoresTheNotesAsTheyWere(t *testing.T) {
	cfg := backupTestDB(t)
	addBackupTestNote(t, "g-pub", "public before", false)
	addBackupTestNote(t, "g-priv", "private before", true)

	res, err := BackupDatabases(cfg)
	if err != nil {
		t.Fatalf("BackupDatabases: %v", err)
	}
	if res.Unchanged {
		t.Fatal("first backup reported Unchanged")
	}
	if res.Set.Public.Bytes == 0 || res.Set.Private.Bytes == 0 {
		t.Fatalf("empty backup files: %+v", res.Set)
	}

	// Written after the backup: must not be in the restored copy.
	addBackupTestNote(t, "g-late", "written after the backup", false)
	CloseDB()

	// "Restore" is opening the set's files as the databases.
	restoreDir := t.TempDir()
	for _, name := range []string{"notes_public.bytdb", "notes_private.bytdb"} {
		data, err := os.ReadFile(filepath.Join(res.Set.Path, name))
		if err != nil {
			t.Fatalf("read backup file %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(restoreDir, name), data, 0o600); err != nil {
			t.Fatalf("write restore file %s: %v", name, err)
		}
	}
	if err := InitTestDB(restoreDir); err != nil {
		t.Fatalf("opening the restored databases: %v", err)
	}

	notes, err := ListNotes("user-1", 0, 0)
	if err != nil {
		t.Fatalf("ListNotes on the restored databases: %v", err)
	}
	got := map[string]bool{}
	for _, n := range notes {
		got[n.GUID] = true
	}
	if !got["g-pub"] || !got["g-priv"] {
		t.Errorf("restored databases are missing notes taken before the backup: %v", got)
	}
	if got["g-late"] {
		t.Error("restored databases hold a note written after the backup")
	}
}

// A second backup of the same data keeps the existing set instead of adding a
// byte-identical one, so rotation's slots hold distinct states.
func TestBackupOfUnchangedDataKeepsNoNewSet(t *testing.T) {
	cfg := backupTestDB(t)
	addBackupTestNote(t, "g-1", "one", false)

	first, err := BackupDatabases(cfg)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := BackupDatabases(cfg)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if !second.Unchanged {
		t.Error("second backup of unchanged data was kept as a new set")
	}
	if second.Set.Name != first.Set.Name {
		t.Errorf("Unchanged result names %q, want the existing set %q", second.Set.Name, first.Set.Name)
	}
	sets, err := ListBackups(cfg.Dir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(sets) != 1 {
		t.Errorf("have %d sets after two backups of the same data, want 1", len(sets))
	}

	// And nothing is left staged.
	entries, _ := os.ReadDir(cfg.Dir)
	if len(entries) != 1 {
		t.Errorf("backup directory holds %d entries, want only the one set", len(entries))
	}
}

// Rotation keeps the newest Keep sets, removes the rest, and leaves anything
// in the directory that is not one of its sets alone.
func TestBackupRotationKeepsTheNewestAndOnlyItsOwn(t *testing.T) {
	cfg := backupTestDB(t)
	cfg.Keep = 2

	// Something a person put there. Rotation must not treat it as a set.
	keepsake := filepath.Join(cfg.Dir, "before-the-big-reorg")
	if err := os.MkdirAll(keepsake, 0o700); err != nil {
		t.Fatal(err)
	}

	var names []string
	for i, title := range []string{"a", "b", "c", "d"} {
		addBackupTestNote(t, "g-"+title, title, i%2 == 0)
		res, err := BackupDatabases(cfg)
		if err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
		names = append(names, res.Set.Name)
	}

	sets, err := ListBackups(cfg.Dir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(sets) != 2 {
		t.Fatalf("have %d sets, want Keep = 2", len(sets))
	}
	// Newest first, and they are the last two taken. The four backups land
	// within one second, so this also covers the "-N" same-second names.
	if sets[0].Name != names[3] || sets[1].Name != names[2] {
		t.Errorf("kept %q and %q, want the newest two %q and %q",
			sets[0].Name, sets[1].Name, names[3], names[2])
	}
	if _, err := os.Stat(keepsake); err != nil {
		t.Errorf("rotation removed a directory that is not a backup set: %v", err)
	}
}

// A staging directory abandoned by a killed run is swept by the next backup,
// and is never listed as a set in the meantime.
func TestBackupSweepsAbandonedStagingDirectories(t *testing.T) {
	cfg := backupTestDB(t)
	addBackupTestNote(t, "g-1", "one", false)

	stale := filepath.Join(cfg.Dir, backupTmpPrefix+"20200101T000000Z")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleBackupTempAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if sets, _ := ListBackups(cfg.Dir); len(sets) != 0 {
		t.Fatalf("a staging directory was listed as a set: %+v", sets)
	}
	if _, err := BackupDatabases(cfg); err != nil {
		t.Fatalf("BackupDatabases: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("abandoned staging directory survived a backup run (stat err: %v)", err)
	}
}

// The schedule times its first pass from the newest set: with none, it backs
// up at once; and stop waits for the goroutine, so nothing runs after it.
func TestBackupScheduleBacksUpWhenNoSetExists(t *testing.T) {
	cfg := backupTestDB(t)
	cfg.Interval = time.Hour
	addBackupTestNote(t, "g-1", "one", false)

	stop := StartBackupSchedule(cfg)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if sets, _ := ListBackups(cfg.Dir); len(sets) == 1 {
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("the schedule took no backup with no set present")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()

	// A fresh set exists, so a new schedule has an hour to wait and must not
	// take another one, even though the data has since changed.
	addBackupTestNote(t, "g-2", "two", false)
	stop = StartBackupSchedule(cfg)
	time.Sleep(150 * time.Millisecond)
	stop()
	if sets, _ := ListBackups(cfg.Dir); len(sets) != 1 {
		t.Errorf("have %d sets; a schedule with a fresh set should have waited", len(sets))
	}
}

func TestLoadBackupConfig(t *testing.T) {
	t.Setenv(BackupIntervalEnv, "")
	t.Setenv(BackupKeepEnv, "")
	t.Setenv(BackupDirEnv, "")
	if got := LoadBackupConfig(); got.Interval != DefaultBackupInterval || got.Keep != DefaultBackupKeep || got.Dir != DefaultBackupDir {
		t.Errorf("defaults = %+v", got)
	}

	t.Setenv(BackupIntervalEnv, "off")
	if got := LoadBackupConfig(); got.Interval != 0 {
		t.Errorf("interval %q should turn the schedule off, got %v", "off", got.Interval)
	}

	t.Setenv(BackupIntervalEnv, "6h")
	t.Setenv(BackupKeepEnv, "30")
	t.Setenv(BackupDirEnv, "/Volumes/other/gonotes")
	got := LoadBackupConfig()
	if got.Interval != 6*time.Hour || got.Keep != 30 || got.Dir != "/Volumes/other/gonotes" {
		t.Errorf("configured = %+v", got)
	}

	// A typo falls back to the default instead of turning backups off or
	// failing startup.
	t.Setenv(BackupIntervalEnv, "daily")
	t.Setenv(BackupKeepEnv, "0")
	got = LoadBackupConfig()
	if got.Interval != DefaultBackupInterval || got.Keep != DefaultBackupKeep {
		t.Errorf("malformed values gave %+v, want the defaults", got)
	}
}

// With an encryption key set, the private database's backup must be as
// unreadable on disk as the live file: the backup directory is exactly the
// kind of place that gets copied to another disk or a synced folder. The
// public database's copy is the control - the same marker is findable there,
// which shows the search itself works.
func TestBackupOfAnEncryptedPrivateDatabaseStaysEncrypted(t *testing.T) {
	t.Setenv(EncryptionKeyEnvVar, "0123456789abcdef0123456789abcdef")
	t.Cleanup(ResetEncryption) // the key is a package global; do not leak it into other tests
	cfg := backupTestDB(t)

	const marker = "MARKER-7f3a-only-in-these-test-notes"
	for _, private := range []bool{false, true} {
		body := marker
		guid := "g-enc-public"
		if private {
			guid = "g-enc-private"
		}
		if _, err := CreateNote(NoteInput{GUID: guid, Title: "enc", Body: &body, IsPrivate: private}, "user-1"); err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
	}

	res, err := BackupDatabases(cfg)
	if err != nil {
		t.Fatalf("BackupDatabases: %v", err)
	}
	if !res.Set.Private.Encrypted || res.Set.Public.Encrypted {
		t.Errorf("manifest says private encrypted=%v, public encrypted=%v; want true, false",
			res.Set.Private.Encrypted, res.Set.Public.Encrypted)
	}

	pub, err := os.ReadFile(filepath.Join(res.Set.Path, "notes_public.bytdb"))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := os.ReadFile(filepath.Join(res.Set.Path, "notes_private.bytdb"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pub, []byte(marker)) {
		t.Fatal("control failed: the marker is not findable in the plaintext public backup")
	}
	if bytes.Contains(priv, []byte(marker)) {
		t.Error("the private database's backup holds a private note body in plaintext")
	}
}
