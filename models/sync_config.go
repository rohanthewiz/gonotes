package models

import (
	"os"
	"strconv"
	"time"

	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Configuration
//
// Loads sync settings from environment variables. When GONOTES_SYNC_ENABLED
// is true, the spoke instance will run a background goroutine that
// periodically pulls from and pushes to the hub.
// ============================================================================

// SyncConfig holds the configuration for the sync client.
// All values are loaded from environment variables to keep
// deployment configuration external to the binary.
type SyncConfig struct {
	Enabled  bool          // Whether sync is active (GONOTES_SYNC_ENABLED)
	HubURL   string        // Base URL of the hub instance (GONOTES_SYNC_HUB_URL)
	Username string        // Authentication username (GONOTES_SYNC_USERNAME)
	Password string        // Authentication password (GONOTES_SYNC_PASSWORD)
	Interval time.Duration // Polling interval between sync cycles (GONOTES_SYNC_INTERVAL)
}

// defaultSyncInterval is used when GONOTES_SYNC_INTERVAL is not set.
// 5 minutes balances freshness with network overhead for a typical
// single-user sync setup.
const defaultSyncInterval = 5 * time.Minute

// LoadSyncConfig reads sync configuration from environment variables.
// Returns a config even when sync is disabled so callers can inspect
// the state without nil checks.
func LoadSyncConfig() (*SyncConfig, error) {
	cfg := &SyncConfig{
		Interval: defaultSyncInterval,
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
	cfg.Password = os.Getenv("GONOTES_SYNC_PASSWORD")

	// Parse interval — allow overriding the default for testing or
	// environments that need faster/slower sync cycles
	if intervalStr := os.Getenv("GONOTES_SYNC_INTERVAL"); intervalStr != "" {
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			return nil, serr.Wrap(err, "invalid GONOTES_SYNC_INTERVAL value, expected duration like '5m' or '30s'")
		}
		cfg.Interval = interval
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
		return serr.New("GONOTES_SYNC_PASSWORD is required when sync is enabled")
	}
	if c.Interval < 10*time.Second {
		return serr.New("GONOTES_SYNC_INTERVAL must be at least 10s to avoid overwhelming the hub")
	}

	return nil
}
