package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// envFilePath is the relative path to the .env file that the spoke reads on
// startup. Using a constant keeps this in sync with main.go's
// fileops.EnvFromFile("config/cfg_files/.env") call.
const envFilePath = "config/cfg_files/.env"

// ApplySpokeConfig handles POST /api/v1/setup/apply
// Writes sync configuration from an exported JSON file to the .env file.
// This endpoint does NOT require authentication on a server with no accounts,
// because it is used during initial spoke setup before any users exist on the
// spoke. Once an account exists it requires an admin (see the guard below).
//
// Guard: only available when sync is not yet configured. Once
// GONOTES_SYNC_ENABLED is "true", this endpoint returns 403 to prevent
// accidental reconfiguration of a running spoke.
//
// Request body: SpokeExportConfig JSON (same struct used by the export).
func ApplySpokeConfig(ctx rweb.Context) error {
	// First-run guard — if sync is already enabled, refuse to overwrite.
	// This prevents a rogue request from reconfiguring a live spoke.
	if strings.EqualFold(os.Getenv("GONOTES_SYNC_ENABLED"), "true") {
		return writeError(ctx, http.StatusForbidden,
			"sync is already configured; edit the .env file manually to reconfigure")
	}

	// Who may apply a config:
	//
	//	no accounts yet (first run)  ──▶ anyone      the case this endpoint exists for
	//	accounts exist               ──▶ admin only  everything else
	//
	// "Sync is not enabled" alone (the guard above) is true of every hub and
	// every standalone server, for as long as they run. Without this second
	// check, anyone who could reach the port could point such a server at a
	// hub of their choosing and hand it their JWT secret, taking effect at the
	// next restart. Once an account exists there is someone who can sign in,
	// so requiring them to is no obstacle to setup; before one exists there is
	// nothing here to take over.
	firstRun, err := models.IsFirstUser()
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to check for existing accounts during setup"))
		return writeError(ctx, http.StatusInternalServerError, "failed to check setup state")
	}
	if !firstRun && !IsAdmin(ctx) {
		return writeError(ctx, http.StatusForbidden,
			"this server already has accounts; sign in as an admin, then apply the config")
	}

	// Parse the import config
	var cfg SpokeExportConfig
	if err := json.Unmarshal(ctx.Request().Body(), &cfg); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid config JSON")
	}

	// Validate required fields — without these, sync can't function
	if cfg.HubURL == "" || cfg.Username == "" || cfg.PasswordB64 == "" {
		return writeError(ctx, http.StatusBadRequest,
			"hub_url, username, and password_b64 are required")
	}

	// Every value becomes one line of the .env file. A line break inside one
	// would start a new line there, which is to say an environment variable of
	// the sender's choosing, loaded over the real environment at next start.
	for name, v := range map[string]string{
		"hub_url": cfg.HubURL, "username": cfg.Username, "password_b64": cfg.PasswordB64,
		"sync_interval": cfg.SyncInterval, "sync_mode": cfg.SyncMode, "prompt_after": cfg.PromptAfter,
		"invite_token": cfg.InviteToken, "jwt_secret": cfg.JWTSecret,
	} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return writeError(ctx, http.StatusBadRequest, name+" must be a single line")
		}
	}

	// Default the sync interval if not provided
	if cfg.SyncInterval == "" {
		cfg.SyncInterval = "5m"
	}

	// Default the trigger to prompt mode, matching LoadSyncConfig. A config
	// file exported by an older hub says nothing about mode, and the safe
	// reading of silence is the one where nothing leaves the machine unasked.
	if cfg.SyncMode == "" {
		cfg.SyncMode = "prompt"
	}
	if cfg.PromptAfter == "" {
		cfg.PromptAfter = "2h"
	}

	// Write the .env file with restrictive permissions (owner read/write only)
	if err := writeEnvFile(cfg); err != nil {
		logger.LogErr(serr.Wrap(err, "failed to write .env file during setup"), "path", envFilePath)
		return writeError(ctx, http.StatusInternalServerError,
			"failed to write configuration file")
	}

	logger.Info("Spoke config applied via setup", "hub_url", cfg.HubURL, "username", cfg.Username)

	return writeSuccess(ctx, http.StatusOK, map[string]string{
		"message": "Configuration saved. Restart GoNotes to activate sync.",
	})
}

// writeEnvFile writes the spoke sync configuration to config/cfg_files/.env.
//
// It MERGES into the file: each sync key is set, and every other line is left
// as it was. It used to compose the whole file from the config and overwrite,
// which is only harmless when the file does not exist yet. On a server that
// already had one, applying a spoke config deleted whatever else lived there,
// and that file is where GONOTES_ENCRYPTION_KEY is usually kept: the private
// database would then fail to open at the next start, with the key gone.
//
// The write is one temp-file-plus-rename (see setEnvFileValues), with 0600
// permissions so only the owner can read the credentials.
func writeEnvFile(cfg SpokeExportConfig) error {
	kvs := []envKV{
		{"GONOTES_SYNC_ENABLED", "true"},
		{"GONOTES_SYNC_HUB_URL", cfg.HubURL},
		{"GONOTES_SYNC_USERNAME", cfg.Username},
		{"GONOTES_SYNC_PASSWORD_B64", cfg.PasswordB64},
		{"GONOTES_SYNC_INTERVAL", cfg.SyncInterval},
		// How a sync cycle is triggered: 'prompt' asks (after
		// GONOTES_SYNC_PROMPT_AFTER, and at exit); 'auto' runs one every
		// GONOTES_SYNC_INTERVAL with nobody asked.
		{"GONOTES_SYNC_MODE", cfg.SyncMode},
		{"GONOTES_SYNC_PROMPT_AFTER", cfg.PromptAfter},
	}

	// Invite token is optional — only include if present
	if cfg.InviteToken != "" {
		kvs = append(kvs, envKV{"GONOTES_SYNC_INVITE_TOKEN", cfg.InviteToken})
	}

	// JWT secret is required for the spoke to validate its own tokens
	if cfg.JWTSecret != "" {
		kvs = append(kvs, envKV{"GONOTES_JWT_SECRET", cfg.JWTSecret})
	}

	return setEnvFileValues(kvs)
}

// envKV is one KEY=value assignment for the .env file.
type envKV struct{ key, value string }

// envFileValue renders a value the way the .env loader will read it back.
//
// The loader (rutil fileops.EnvFromFile) cuts an unquoted value at the first
// '#', as a trailing comment, and trims surrounding space. A hub's JWT secret
// may well contain '#'; written bare it would come back shortened, possibly
// below the 32 characters InitJWT requires, and the spoke would not start.
// Quoted values are read verbatim up to the closing quote, so such a value is
// wrapped in whichever quote character it does not itself contain.
//
// A value needing quotes that contains both kinds cannot be written so that
// it reads back unchanged, and is refused rather than stored altered.
func envFileValue(v string) (string, error) {
	needsQuotes := strings.Contains(v, "#") || strings.TrimSpace(v) != v ||
		strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`)
	if !needsQuotes {
		return v, nil
	}
	switch {
	case !strings.Contains(v, `"`):
		return `"` + v + `"`, nil
	case !strings.Contains(v, `'`):
		return `'` + v + `'`, nil
	default:
		return "", serr.New("value cannot be written to the env file unchanged: it needs quoting and contains both quote characters")
	}
}

// setEnvFileValue sets one key in config/cfg_files/.env, leaving every other
// line — including the credentials — exactly as it found them.
//
// This is deliberately not writeEnvFile: that one composes the whole file from
// a SpokeExportConfig and is right for first-run setup, where there is nothing
// to preserve. Changing one setting on a configured spoke is the opposite
// situation, and rewriting the file from a partial view of it would quietly
// drop whatever the view did not carry.
//
// The write is temp-file-plus-rename so a failure midway cannot truncate a
// file holding the hub password and the JWT secret. Permissions stay 0600.
func setEnvFileValue(key, value string) error {
	return setEnvFileValues([]envKV{{key, value}})
}

// setEnvFileValues is setEnvFileValue for several keys at once: one read, one
// pass per key over the lines, one temp-file-plus-rename. Doing them together
// matters for more than speed. Applying a spoke config sets nine keys, and
// nine separate renames would leave the file half-configured (sync enabled,
// no hub URL yet) if the process died partway.
func setEnvFileValues(kvs []envKV) error {
	existing, err := os.ReadFile(envFilePath)
	if err != nil && !os.IsNotExist(err) {
		return serr.Wrap(err, "failed to read env file", "path", envFilePath)
	}

	out := strings.Split(string(existing), "\n")
	// A file ending in a newline splits into a final empty element; drop
	// trailing blanks so appended keys follow the last real line and the file
	// does not grow a gap on every new key.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}

	for _, kv := range kvs {
		rendered, err := envFileValue(kv.value)
		if err != nil {
			return serr.Wrap(err, "key", kv.key)
		}
		line := kv.key + "=" + rendered

		replaced := false
		kept := out[:0:0] // a fresh slice: out is still being read
		for _, l := range out {
			// Match on the assignment, not on a Contains: a key name appearing
			// inside a comment or another value must not be rewritten.
			if strings.HasPrefix(strings.TrimSpace(l), kv.key+"=") {
				if replaced {
					continue // a duplicate of a key we have already set: drop it
				}
				kept = append(kept, line)
				replaced = true
				continue
			}
			kept = append(kept, l)
		}
		if !replaced {
			kept = append(kept, line)
		}
		out = kept
	}

	content := strings.Join(out, "\n") + "\n"

	if err := os.MkdirAll(filepath.Dir(envFilePath), 0o700); err != nil {
		return serr.Wrap(err, "failed to create env file directory")
	}

	tmp := envFilePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return serr.Wrap(err, "failed to write temporary env file")
	}
	if err := os.Rename(tmp, envFilePath); err != nil {
		os.Remove(tmp)
		return serr.Wrap(err, "failed to replace env file")
	}
	return nil
}
