package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setEnvFileValue edits a file that holds the hub password and the JWT secret.
// Everything below is one property said four ways: it changes the line it was
// asked to change and nothing else.

// inEnvDir runs the test with the working directory somewhere disposable, so
// the relative envFilePath lands in a temp tree rather than in the repo.
func inEnvDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to read working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to enter temp directory: %v", err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
	return dir
}

func writeEnv(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(envFilePath), 0o700); err != nil {
		t.Fatalf("failed to create env directory: %v", err)
	}
	if err := os.WriteFile(envFilePath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to seed env file: %v", err)
	}
}

func readEnv(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(envFilePath)
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}
	return string(b)
}

func TestSetEnvFileValueReplacesInPlace(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "GONOTES_SYNC_ENABLED=true\nGONOTES_SYNC_MODE=prompt\nGONOTES_JWT_SECRET=keep-me\n")

	if err := setEnvFileValue("GONOTES_SYNC_MODE", "auto"); err != nil {
		t.Fatalf("setEnvFileValue failed: %v", err)
	}

	got := readEnv(t)
	if !strings.Contains(got, "GONOTES_SYNC_MODE=auto") {
		t.Errorf("the key was not updated:\n%s", got)
	}
	if strings.Contains(got, "GONOTES_SYNC_MODE=prompt") {
		t.Errorf("the old value survives:\n%s", got)
	}
	if !strings.Contains(got, "GONOTES_JWT_SECRET=keep-me") {
		t.Errorf("an unrelated secret was lost:\n%s", got)
	}
	if !strings.Contains(got, "GONOTES_SYNC_ENABLED=true") {
		t.Errorf("an unrelated setting was lost:\n%s", got)
	}
}

func TestSetEnvFileValueAppendsWhenAbsent(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "GONOTES_SYNC_ENABLED=true\n")

	if err := setEnvFileValue("GONOTES_SYNC_MODE", "auto"); err != nil {
		t.Fatalf("setEnvFileValue failed: %v", err)
	}

	got := readEnv(t)
	if !strings.Contains(got, "GONOTES_SYNC_MODE=auto") {
		t.Errorf("the key was not appended:\n%s", got)
	}
	if !strings.Contains(got, "GONOTES_SYNC_ENABLED=true") {
		t.Errorf("the existing setting was lost:\n%s", got)
	}
	if strings.Count(got, "\n\n") > 0 {
		t.Errorf("appending left a blank gap:\n%q", got)
	}
}

// A key name inside a comment or another value is not an assignment, and
// rewriting it would corrupt a line the caller never named.
func TestSetEnvFileValueIgnoresMentionsThatAreNotAssignments(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "# GONOTES_SYNC_MODE is prompt or auto\nGONOTES_SYNC_MODE=prompt\n")

	if err := setEnvFileValue("GONOTES_SYNC_MODE", "auto"); err != nil {
		t.Fatalf("setEnvFileValue failed: %v", err)
	}

	got := readEnv(t)
	if !strings.Contains(got, "# GONOTES_SYNC_MODE is prompt or auto") {
		t.Errorf("the comment was rewritten:\n%s", got)
	}
	if strings.Count(got, "GONOTES_SYNC_MODE=") != 1 {
		t.Errorf("expected exactly one assignment:\n%s", got)
	}
}

func TestSetEnvFileValueCreatesTheFileAndKeepsItPrivate(t *testing.T) {
	inEnvDir(t)

	if err := setEnvFileValue("GONOTES_SYNC_MODE", "prompt"); err != nil {
		t.Fatalf("setEnvFileValue failed on a missing file: %v", err)
	}

	info, err := os.Stat(envFilePath)
	if err != nil {
		t.Fatalf("the env file was not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("env file mode = %v, want 0600 — it holds the hub password", perm)
	}
	if got := readEnv(t); !strings.Contains(got, "GONOTES_SYNC_MODE=prompt") {
		t.Errorf("the value was not written:\n%s", got)
	}
}
