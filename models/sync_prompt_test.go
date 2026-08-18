package models_test

import (
	"context"
	"os"
	"testing"
	"time"

	"gonotes/models"
)

// Prompt-mode tests: the configuration defaults, and the clock the UI reads.
//
// What is deliberately NOT tested here is a full cycle — that needs a hub, and
// the sync protocol tests already cover what a cycle does. What these cover is
// the decision to run one, which is the part that changed.

// syncEnvVars is every environment variable LoadSyncConfig reads. The tests
// clear all of them rather than the ones they set, because a developer's own
// .env exported into a shell would otherwise silently change what "default"
// means here.
var syncEnvVars = []string{
	"GONOTES_SYNC_ENABLED",
	"GONOTES_SYNC_HUB_URL",
	"GONOTES_SYNC_USERNAME",
	"GONOTES_SYNC_PASSWORD",
	"GONOTES_SYNC_PASSWORD_B64",
	"GONOTES_SYNC_INVITE_TOKEN",
	"GONOTES_SYNC_INTERVAL",
	"GONOTES_SYNC_MODE",
	"GONOTES_SYNC_PROMPT_AFTER",
	"GONOTES_SYNC_ON_EXIT",
	"GONOTES_SYNC_COMPACT",
}

// withCleanSyncEnv clears the sync environment for the duration of a test and
// restores it afterwards.
func withCleanSyncEnv(t *testing.T) {
	t.Helper()
	for _, name := range syncEnvVars {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

// TestSyncDefaultsToPromptMode is the headline change: an installation that
// says nothing about mode does not sync in the background.
func TestSyncDefaultsToPromptMode(t *testing.T) {
	withCleanSyncEnv(t)

	cfg, err := models.LoadSyncConfig()
	if err != nil {
		t.Fatalf("failed to load sync config: %v", err)
	}
	if cfg.Mode != models.SyncModePrompt {
		t.Errorf("Mode = %q, want %q", cfg.Mode, models.SyncModePrompt)
	}
	if cfg.PromptAfter != 2*time.Hour {
		t.Errorf("PromptAfter = %s, want 2h", cfg.PromptAfter)
	}
	if !cfg.SyncOnExit {
		t.Error("SyncOnExit = false, want true — the exit cycle is the one a user never has to ask for")
	}
	if cfg.CompactBeforePush {
		t.Error("CompactBeforePush = true, want false — compaction discards history and must be opted into")
	}
}

// TestSyncConfigReadsOverrides checks every new variable, including the one
// that restores the old behaviour.
func TestSyncConfigReadsOverrides(t *testing.T) {
	withCleanSyncEnv(t)
	t.Setenv("GONOTES_SYNC_MODE", "auto")
	t.Setenv("GONOTES_SYNC_PROMPT_AFTER", "45m")
	t.Setenv("GONOTES_SYNC_ON_EXIT", "false")
	t.Setenv("GONOTES_SYNC_COMPACT", "true")

	cfg, err := models.LoadSyncConfig()
	if err != nil {
		t.Fatalf("failed to load sync config: %v", err)
	}
	if cfg.Mode != models.SyncModeAuto {
		t.Errorf("Mode = %q, want %q", cfg.Mode, models.SyncModeAuto)
	}
	if cfg.PromptAfter != 45*time.Minute {
		t.Errorf("PromptAfter = %s, want 45m", cfg.PromptAfter)
	}
	if cfg.SyncOnExit {
		t.Error("SyncOnExit = true, want false")
	}
	if !cfg.CompactBeforePush {
		t.Error("CompactBeforePush = false, want true")
	}
}

// TestSyncConfigRejectsAnUnknownMode: the two modes differ in whether data
// leaves the machine unasked, so a typo must not be resolved by guessing.
func TestSyncConfigRejectsAnUnknownMode(t *testing.T) {
	withCleanSyncEnv(t)
	t.Setenv("GONOTES_SYNC_MODE", "atuo")

	if _, err := models.LoadSyncConfig(); err == nil {
		t.Error("a misspelled sync mode was accepted; it must be an error")
	}
}

// TestValidateRejectsATooShortPromptInterval keeps the prompt a reminder
// rather than a nag.
func TestValidateRejectsATooShortPromptInterval(t *testing.T) {
	cfg := &models.SyncConfig{
		Enabled:     true,
		HubURL:      "http://hub.example",
		Username:    "me",
		Password:    "pw",
		Interval:    5 * time.Minute,
		Mode:        models.SyncModePrompt,
		PromptAfter: 10 * time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("a 10s prompt interval validated; the floor is 1m")
	}
}

// newTestSyncClient builds a client against a temp database with no hub
// behind it. Nothing here runs a cycle, so the unreachable URL never matters.
func newTestSyncClient(t *testing.T, mode models.SyncMode, promptAfter time.Duration) *models.SyncClient {
	t.Helper()
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}
	t.Cleanup(func() { models.CloseDB() })

	client, err := models.NewSyncClient(&models.SyncConfig{
		Enabled:     true,
		HubURL:      "http://127.0.0.1:1", // deliberately dead: no cycle may run
		Username:    "tester",
		Password:    "secret",
		Interval:    5 * time.Minute,
		Mode:        mode,
		PromptAfter: promptAfter,
		SyncOnExit:  true,
	})
	if err != nil {
		t.Fatalf("failed to create sync client: %v", err)
	}
	return client
}

// TestFreshSpokeIsNotImmediatelyDue pins the startup anchor. A client that has
// never synced measures from process start, so a fresh install does not open
// with a prompt before the user has typed anything.
func TestFreshSpokeIsNotImmediatelyDue(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Hour)

	if client.Due() {
		t.Error("a spoke that just started reports itself due; it should measure from startup")
	}
	if d := client.DueIn(); d <= 0 || d > time.Hour {
		t.Errorf("DueIn() = %s, want a positive value no greater than the 1h interval", d)
	}
}

// TestAutoModeIsNeverDue: due-ness is a question about prompting, and auto
// mode does not prompt.
func TestAutoModeIsNeverDue(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModeAuto, time.Minute)

	if client.Due() {
		t.Error("auto mode reports itself due; only prompt mode prompts")
	}
	if client.DueIn() != 0 {
		t.Errorf("DueIn() = %s, want 0 in auto mode", client.DueIn())
	}
}

// TestSnoozeDefersAndClears covers both directions of the deferral.
func TestSnoozeDefersAndClears(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Minute)

	base := client.DueIn()
	client.Snooze(time.Hour)
	deferred := client.DueIn()
	if deferred <= base {
		t.Errorf("after a 1h snooze DueIn() = %s, want more than the un-snoozed %s", deferred, base)
	}

	client.Snooze(0)
	if cleared := client.DueIn(); cleared > time.Minute {
		t.Errorf("after clearing the snooze DueIn() = %s, want back within the 1m interval", cleared)
	}
}

// TestSnoozeCannotPullTheDeadlineIn: "ask me later" is a deferral, never a way
// to be asked sooner than the configured interval.
func TestSnoozeCannotPullTheDeadlineIn(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Hour)

	client.Snooze(time.Minute)
	if d := client.DueIn(); d < 30*time.Minute {
		t.Errorf("DueIn() = %s after a 1m snooze on a 1h interval; a snooze must not shorten it", d)
	}
}

// TestDisabledSyncIsNeverDue: a spoke with sync switched off has nothing to
// prompt about.
func TestDisabledSyncIsNeverDue(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Minute)

	client.SetEnabled(false)
	if client.Due() {
		t.Error("a disabled sync client reports itself due")
	}
}

// TestSetModeRejectsNonsense guards the runtime switch the API exposes.
func TestSetModeRejectsNonsense(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Minute)

	if err := client.SetMode("sometimes"); err == nil {
		t.Error("SetMode accepted an unknown mode")
	}
	if err := client.SetMode(models.SyncModeAuto); err != nil {
		t.Fatalf("SetMode(auto) failed: %v", err)
	}
	if client.Mode() != models.SyncModeAuto {
		t.Errorf("Mode() = %q after SetMode(auto)", client.Mode())
	}
}

// TestPendingChangesCountsLocalWrites ties the number the prompt shows to the
// writes it is about.
func TestPendingChangesCountsLocalWrites(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Hour)

	if n, err := client.PendingChanges(); err != nil || n != 0 {
		t.Fatalf("PendingChanges() = (%d, %v) on a fresh spoke, want (0, nil)", n, err)
	}

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "prompt-pending",
		Title: "Written while offline",
	}, "prompt-test-user"); err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	n, err := client.PendingChanges()
	if err != nil {
		t.Fatalf("PendingChanges() failed: %v", err)
	}
	if n == 0 {
		t.Error("PendingChanges() = 0 after a local write")
	}
}

// TestExitSyncSkipsWhenThereIsNothingToDo: the exit cycle must not turn every
// quit into a round trip to a hub that has nothing waiting either way.
func TestExitSyncSkipsWhenThereIsNothingToDo(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Hour)

	ran, err := client.SyncOnExit(context.Background())
	if err != nil {
		t.Fatalf("SyncOnExit reported an error with nothing to do: %v", err)
	}
	if ran {
		t.Error("SyncOnExit ran a cycle with nothing pending and nothing overdue")
	}
}

// TestExitSyncHonorsADecline is what makes the TUI's quit dialog meaningful:
// a user who chose "quit without syncing" must not be synced anyway on the way
// out.
func TestExitSyncHonorsADecline(t *testing.T) {
	client := newTestSyncClient(t, models.SyncModePrompt, time.Hour)

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "prompt-declined",
		Title: "Stays here",
	}, "prompt-test-user"); err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	client.DeclineExitSync()

	ran, err := client.SyncOnExit(context.Background())
	if err != nil {
		t.Fatalf("SyncOnExit reported an error after a decline: %v", err)
	}
	if ran {
		t.Error("SyncOnExit ran a cycle the user had already declined")
	}
}
