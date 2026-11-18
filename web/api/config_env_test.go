package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/rutil/fileops"
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

// Applying a spoke config must add the sync keys and keep everything else.
// The file is where the encryption key usually lives; the old whole-file
// overwrite deleted it, and the private database then would not open.
func TestWriteEnvFileMergesAndKeepsUnrelatedKeys(t *testing.T) {
	inEnvDir(t)
	writeEnv(t, "# my settings\nGONOTES_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef\nGONOTES_PORT=9000\nGONOTES_SYNC_MODE=auto\n")

	err := writeEnvFile(SpokeExportConfig{
		HubURL: "https://hub.example.com", Username: "me", PasswordB64: "cGFzcw==",
		SyncInterval: "5m", SyncMode: "prompt", PromptAfter: "2h", JWTSecret: "a-secret-that-is-long-enough-0123456789",
	})
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}

	got := readEnv(t)
	for _, want := range []string{
		"# my settings",
		"GONOTES_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef",
		"GONOTES_PORT=9000",
		"GONOTES_SYNC_ENABLED=true",
		"GONOTES_SYNC_HUB_URL=https://hub.example.com",
		"GONOTES_SYNC_MODE=prompt", // replaced in place, not duplicated
		"GONOTES_JWT_SECRET=a-secret-that-is-long-enough-0123456789",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("env file is missing the line %q\n---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "GONOTES_SYNC_MODE="); n != 1 {
		t.Errorf("GONOTES_SYNC_MODE appears %d times, want 1", n)
	}
}

// A fresh data directory has no config/cfg_files yet; setup must create it
// rather than fail with "failed to write configuration file".
func TestWriteEnvFileCreatesTheConfigDirectory(t *testing.T) {
	inEnvDir(t)
	err := writeEnvFile(SpokeExportConfig{HubURL: "http://hub", Username: "me", PasswordB64: "cA==",
		SyncInterval: "5m", SyncMode: "prompt", PromptAfter: "2h"})
	if err != nil {
		t.Fatalf("writeEnvFile on a fresh directory: %v", err)
	}
	if !strings.Contains(readEnv(t), "GONOTES_SYNC_HUB_URL=http://hub\n") {
		t.Error("the hub URL was not written")
	}
}

// A value containing '#' has to survive the loader that will read it back,
// which treats '#' in an unquoted value as the start of a comment.
func TestEnvFileValueSurvivesTheLoader(t *testing.T) {
	for _, secret := range []string{
		"plain-secret-0123456789-0123456789-01",
		"has#hash-0123456789-0123456789-0123456",
		`has#hash-and-"double"-quotes-0123456789`,
		" leading-space-0123456789-0123456789-01",
	} {
		inEnvDir(t)
		if err := setEnvFileValue("GONOTES_TEST_ROUNDTRIP", secret); err != nil {
			t.Fatalf("setEnvFileValue(%q): %v", secret, err)
		}
		t.Setenv("GONOTES_TEST_ROUNDTRIP", "")
		if _, err := fileops.EnvFromFile(envFilePath); err != nil {
			t.Fatalf("loader failed on the written file: %v", err)
		}
		if got := os.Getenv("GONOTES_TEST_ROUNDTRIP"); got != secret {
			t.Errorf("wrote %q, the loader read back %q", secret, got)
		}
	}

	// Needs quoting and contains both quote characters: refused, not altered.
	inEnvDir(t)
	if err := setEnvFileValue("GONOTES_TEST_ROUNDTRIP", `a#b"c'd`); err == nil {
		t.Error("a value that cannot round-trip was written anyway")
	}
}
