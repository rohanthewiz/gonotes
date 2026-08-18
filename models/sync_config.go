package models

import (
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Configuration
//
// Loads sync settings from environment variables. When GONOTES_SYNC_ENABLED
// is true, the spoke instance starts a sync client — but "started" no longer
// means "syncing on its own". Mode decides that:
//
//	prompt (default)  nothing leaves this machine until someone says so. The
//	                  client tracks how long it has been since the last
//	                  successful cycle and reports itself DUE once PromptAfter
//	                  has elapsed; a UI turns that into a question. A cycle
//	                  also runs on exit, where there is no one left to ask.
//	auto              the original behaviour: a background goroutine runs a
//	                  cycle every Interval, unasked.
//
// The default flipped deliberately. Background sync is a write to another
// machine that the user did not initiate and cannot see, and on a laptop that
// sleeps, roams networks, and holds private notes, "when did this last leave
// here" is a question the answer to which should be a decision rather than a
// timer.
// ============================================================================

// SyncConfig holds the configuration for the sync client.
// All values are loaded from environment variables to keep
// deployment configuration external to the binary.
type SyncConfig struct {
	Enabled     bool          // Whether sync is active (GONOTES_SYNC_ENABLED)
	HubURL      string        // Base URL of the hub instance (GONOTES_SYNC_HUB_URL)
	Username    string        // Authentication username (GONOTES_SYNC_USERNAME)
	Password    string        // Authentication password (decoded from GONOTES_SYNC_PASSWORD_B64)
	Interval    time.Duration // Polling interval between sync cycles in auto mode (GONOTES_SYNC_INTERVAL)
	InviteToken string        // One-time token for auto-registration on hub (GONOTES_SYNC_INVITE_TOKEN)

	// Mode selects whether cycles run on a timer or on request
	// (GONOTES_SYNC_MODE). Defaults to SyncModePrompt.
	Mode SyncMode

	// PromptAfter is how long a spoke may go unsynced in prompt mode before
	// the client reports itself due (GONOTES_SYNC_PROMPT_AFTER). Ignored in
	// auto mode, where Interval is the only clock that matters.
	PromptAfter time.Duration

	// SyncOnExit runs one final cycle during shutdown
	// (GONOTES_SYNC_ON_EXIT, default true). This is the half of "prompt or on
	// exit" that needs no user present: a headless server being stopped, or a
	// TUI whose user answered the quit dialog, both end here.
	SyncOnExit bool

	// CompactBeforePush collapses the pending change log before every push
	// (GONOTES_SYNC_COMPACT, default false). Off by default because it
	// discards local change history — see CompactPendingChanges. Prompt mode
	// makes this worth having: hours between syncs is hours of accumulated
	// per-keystroke-session change rows for the same handful of notes.
	CompactBeforePush bool
}

// SyncMode selects what triggers a sync cycle.
type SyncMode string

const (
	// SyncModePrompt is the default: cycles run only when asked for — by a UI
	// acting on Due(), by SyncNow, or by the shutdown path.
	SyncModePrompt SyncMode = "prompt"

	// SyncModeAuto is the pre-existing behaviour, kept for hub-adjacent
	// spokes and servers where nobody is watching a UI to answer a prompt.
	SyncModeAuto SyncMode = "auto"
)

// ParseSyncMode maps an environment string to a SyncMode. An empty string
// yields the default (prompt); anything unrecognized is an error rather than
// a silent fallback, because the two modes differ in whether data leaves the
// machine unasked — not a difference to guess at.
func ParseSyncMode(s string) (SyncMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return SyncModePrompt, nil
	case string(SyncModePrompt):
		return SyncModePrompt, nil
	case string(SyncModeAuto):
		return SyncModeAuto, nil
	default:
		return "", serr.New("invalid sync mode " + strconv.Quote(s) + ", expected 'prompt' or 'auto'")
	}
}

// defaultSyncInterval is used when GONOTES_SYNC_INTERVAL is not set.
// 5 minutes balances freshness with network overhead for a typical
// single-user sync setup. Only auto mode reads it.
const defaultSyncInterval = 5 * time.Minute

// defaultPromptAfter is how long prompt mode lets a spoke drift before it
// starts asking. Two hours is long enough that a working session is not
// interrupted by it and short enough that a day's writing is never the unit
// of loss if the machine goes away.
const defaultPromptAfter = 2 * time.Hour

// minPromptAfter floors the configurable prompt interval. Anything under a
// minute is a nag rather than a reminder, and prompt mode's whole value is
// that the question is rare enough to be worth reading.
const minPromptAfter = time.Minute

// LoadSyncConfig reads sync configuration from environment variables.
// Returns a config even when sync is disabled so callers can inspect
// the state without nil checks.
func LoadSyncConfig() (*SyncConfig, error) {
	cfg := &SyncConfig{
		Interval:    defaultSyncInterval,
		Mode:        SyncModePrompt,
		PromptAfter: defaultPromptAfter,
		SyncOnExit:  true,
	}

	// Parse enabled flag — defaults to false (opt-in design)
	if enabledStr := os.Getenv("GONOTES_SYNC_ENABLED"); enabledStr != "" {
		enabled, err := strconv.ParseBool(enabledStr)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_ENABLED value, expected true/false")
		}
		cfg.Enabled = enabled
	}

	cfg.HubURL = os.Getenv("GONOTES_SYNC_HUB_URL")
	cfg.Username = os.Getenv("GONOTES_SYNC_USERNAME")
	cfg.InviteToken = os.Getenv("GONOTES_SYNC_INVITE_TOKEN")

	// Password is base64-encoded in the env var to prevent casual exposure
	// (e.g. shoulder-surfing, screenshots). Fall back to the legacy plaintext
	// var for backward compatibility during migration.
	if pwB64 := os.Getenv("GONOTES_SYNC_PASSWORD_B64"); pwB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(pwB64)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_PASSWORD_B64 value: not valid base64")
		}
		cfg.Password = string(decoded)
	} else {
		cfg.Password = os.Getenv("GONOTES_SYNC_PASSWORD")
	}

	// Parse interval — allow overriding the default for testing or
	// environments that need faster/slower sync cycles
	if intervalStr := os.Getenv("GONOTES_SYNC_INTERVAL"); intervalStr != "" {
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_INTERVAL value, expected duration like '5m' or '30s'")
		}
		cfg.Interval = interval
	}

	// Mode — the one setting that decides whether anything happens unasked.
	// An unset value keeps the prompt default rather than inheriting the old
	// auto behaviour, so an existing .env that only names an interval stops
	// syncing in the background on upgrade. That is the intended migration:
	// the interval it names becomes dormant until the mode says auto.
	mode, err := ParseSyncMode(os.Getenv("GONOTES_SYNC_MODE"))
	if err != nil {
		return nil, err
	}
	cfg.Mode = mode

	if promptStr := os.Getenv("GONOTES_SYNC_PROMPT_AFTER"); promptStr != "" {
		promptAfter, err := time.ParseDuration(promptStr)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_PROMPT_AFTER value, expected duration like '2h' or '30m'")
		}
		cfg.PromptAfter = promptAfter
	}

	// Exit sync defaults ON: it is the only cycle a spoke is guaranteed to
	// get if its user never answers a prompt, so opting out has to be typed.
	if exitStr := os.Getenv("GONOTES_SYNC_ON_EXIT"); exitStr != "" {
		onExit, err := strconv.ParseBool(exitStr)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_ON_EXIT value, expected true/false")
		}
		cfg.SyncOnExit = onExit
	}

	if compactStr := os.Getenv("GONOTES_SYNC_COMPACT"); compactStr != "" {
		compact, err := strconv.ParseBool(compactStr)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_COMPACT value, expected true/false")
		}
		cfg.CompactBeforePush = compact
	}

	return cfg, nil
}

// Validate checks that all required fields are present when sync is enabled.
// Called before starting the sync client to fail fast on misconfiguration
// rather than discovering missing credentials mid-cycle.
func (c *SyncConfig) Validate() error {
	if !c.Enabled {
		return nil // Nothing to validate when sync is disabled
	}

	if c.HubURL == "" {
		return serr.New("GONOTES_SYNC_HUB_URL is required when sync is enabled")
	}
	if c.Username == "" {
		return serr.New("GONOTES_SYNC_USERNAME is required when sync is enabled")
	}
	if c.Password == "" {
		return serr.New("GONOTES_SYNC_PASSWORD_B64 (or GONOTES_SYNC_PASSWORD) is required when sync is enabled")
	}
	if c.Interval < 10*time.Second {
		return serr.New("GONOTES_SYNC_INTERVAL must be at least 10s to avoid overwhelming the hub")
	}
	if c.Mode != SyncModePrompt && c.Mode != SyncModeAuto {
		return serr.New("GONOTES_SYNC_MODE must be 'prompt' or 'auto'")
	}
	if c.PromptAfter < minPromptAfter {
		return serr.New("GONOTES_SYNC_PROMPT_AFTER must be at least 1m")
	}

	return nil
}
