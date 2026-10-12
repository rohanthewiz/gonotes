package models

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/bytdb"
	"github.com/rohanthewiz/serr"
)

// Database-lock tests (N-050). The README says the databases are
// single-writer, and runTui's fallback, `gonotes account` and the import /
// export commands all assume a second open fails. Until bytdb v0.17 nothing
// enforced it. These pin the behavior the callers rely on: a held database
// makes InitDB fail with an error IsDBLocked recognizes, worded for a person,
// and a failed open leaves nothing of its own locked behind.
//
// The holder here is a bare bytdb engine in this process rather than a second
// process. btypedb's flock is per open file description, so a second Open in
// the same process conflicts exactly as one from another process would.

// holdDB opens path directly, standing in for a server that has it open.
func holdDB(t *testing.T, path string) *bytdb.Engine {
	t.Helper()
	eng, err := bytdb.Open(path)
	if err != nil {
		t.Fatalf("holder could not open %s: %v", path, err)
	}
	return eng
}

func TestInitDBFailsWhenPublicDatabaseIsHeld(t *testing.T) {
	dir := t.TempDir()
	holder := holdDB(t, filepath.Join(dir, "notes_public.bytdb"))
	defer holder.Close()

	err := InitTestDB(dir)
	if err == nil {
		CloseDB()
		t.Fatal("InitTestDB opened a database another engine holds")
	}
	if !IsDBLocked(err) {
		t.Fatalf("IsDBLocked = false for %v", err)
	}
	// serr keeps wrap messages in its fields (Error() is the root error), so
	// look at the enriched form, which is what the log line prints.
	if !strings.Contains(serr.StringFromErr(err), LockedMessage) {
		t.Errorf("error does not say who holds the files: %v", err)
	}
	if pubDB != nil || privDB != nil {
		t.Error("a failed open left an engine assigned")
	}
}

// When only the private database is held, the public one has already been
// opened (and locked) by the time the private open fails. It has to be
// released, or the caller's fallback, and any retry in this process, would
// find this process holding half the pair.
func TestInitDBReleasesPublicWhenPrivateIsHeld(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "notes_private.bytdb")
	holder := holdDB(t, privPath)

	err := InitTestDB(dir)
	if err == nil {
		CloseDB()
		holder.Close()
		t.Fatal("InitTestDB opened a private database another engine holds")
	}
	if !IsDBLocked(err) {
		t.Fatalf("IsDBLocked = false for %v", err)
	}

	// The public file must be free again.
	pub := holdDB(t, filepath.Join(dir, "notes_public.bytdb"))
	pub.Close()

	// And once the holder lets go, the pair opens normally.
	holder.Close()
	if err := InitTestDB(dir); err != nil {
		t.Fatalf("InitTestDB after the holder closed: %v", err)
	}
	CloseDB()
}

func TestIsDBLockedIgnoresOtherErrors(t *testing.T) {
	if IsDBLocked(nil) {
		t.Error("IsDBLocked(nil) = true")
	}
	if IsDBLocked(errString("disk full")) {
		t.Error("IsDBLocked matched an unrelated error")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
