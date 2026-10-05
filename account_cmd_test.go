package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"gonotes/models"
)

// captureStdout runs fn and returns what it printed. The account commands
// print their report straight to stdout, and that report is what an operator
// acts on.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = orig
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("command failed: %v\noutput:\n%s", runErr, out)
	}
	return string(out)
}

// With two hubs recorded, `account list` gives alignment advice only for the
// current one. The earlier hub is listed and labelled as not adopted. Advice
// about it would send the operator to a rename that align then ignores.
func TestAccountListAdvisesOnlyTheCurrentHub(t *testing.T) {
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	t.Cleanup(func() { models.CloseDB() })

	if _, err := models.CreateUser(models.UserRegisterInput{Username: "bob", Password: "correct-horse"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	for _, h := range []struct{ url, guid, name string }{
		{"http://old-hub:8444", "guid-old", "oldname"},
		{"http://new-hub:8444", "guid-new", "newname"},
	} {
		if _, err := models.GetOrCreateSyncState(h.url); err != nil {
			t.Fatalf("GetOrCreateSyncState: %v", err)
		}
		if err := models.RecordHubIdentity(h.url, h.guid, h.name); err != nil {
			t.Fatalf("RecordHubIdentity: %v", err)
		}
	}

	out := captureStdout(t, runAccountList)

	for _, want := range []string{
		"Hub http://new-hub:8444 (current)",
		"gonotes account rename --from bob --to newname",
		"Hub http://old-hub:8444 (earlier hub)",
		"its identity is not adopted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--to oldname") {
		t.Errorf("output advises a rename to the earlier hub's account:\n%s", out)
	}
}
