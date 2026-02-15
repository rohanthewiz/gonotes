package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// SpokeExportConfig is the JSON structure written to the export file and read
// during spoke import. It contains every environment variable a spoke needs
// to configure sync with the hub.
type SpokeExportConfig struct {
	HubURL       string `json:"hub_url"`
	Username     string `json:"username"`
	PasswordB64  string `json:"password_b64"`  // base64-encoded login password
	JWTSecret    string `json:"jwt_secret"`    // hub's JWT signing secret
	InviteToken  string `json:"invite_token"`  // single-use token for spoke registration
	SyncInterval string `json:"sync_interval"` // e.g. "5m"
	ExportedAt   string `json:"exported_at"`   // RFC3339 timestamp
}

// ExportSpokeConfig handles POST /api/v1/admin/export-spoke-config
// Admin-only endpoint that generates a JSON config file for spoke setup.
// The admin must re-enter their login password to authorize the export —
// this prevents unauthorized extraction of credentials even with a stolen JWT.
//
// The endpoint auto-generates a fresh invite token (72h expiry) so the
// spoke can self-register on first sync without separate token management.
//
// Request body:
//
//	{ "password": "current-admin-password" }
//
// Response: JSON file download containing hub URL, credentials, JWT secret,
// and the freshly generated invite token.
func ExportSpokeConfig(ctx rweb.Context) error {
	// Admin authorization — only admins can export spoke configs
	if !IsAdmin(ctx) {
		return writeError(ctx, http.StatusForbidden, "admin access required")
	}

	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Parse the password from the request body
	var req struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.Request().Body(), &req); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid request body")
	}
	if req.Password == "" {
		return writeError(ctx, http.StatusBadRequest, "password is required for config export")
	}

	// Re-verify the admin's password against the bcrypt hash in the database.
	// This is a second factor beyond the JWT — if the token was compromised,
	// the attacker still can't extract credentials without the password.
	user, err := models.GetUserByGUID(userGUID)
	if err != nil || user == nil {
		logger.LogErr(serr.Wrap(err, "failed to get user for config export"), "user_guid", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to verify user")
	}

	if !models.CheckPassword(req.Password, user.PasswordHash) {
		return writeError(ctx, http.StatusUnauthorized, "incorrect password")
	}

	// Auto-generate an invite token so the spoke can self-register.
	// 72h expiry gives plenty of time to transfer and apply the config.
	inviteToken, err := models.CreateInviteToken(userGUID, 72*time.Hour)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to create invite token for export"), "user_guid", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to create invite token")
	}

	// Build the hub URL from the incoming request headers.
	// Behind a reverse proxy, X-Forwarded-Proto and Host should be set.
	hubURL := determineHubURL(ctx)

	// Build the export config with base64-encoded password
	exportCfg := SpokeExportConfig{
		HubURL:       hubURL,
		Username:     user.Username,
		PasswordB64:  base64.StdEncoding.EncodeToString([]byte(req.Password)),
		JWTSecret:    os.Getenv("GONOTES_JWT_SECRET"),
		InviteToken:  inviteToken.Token,
		SyncInterval: "5m",
		ExportedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	configJSON, err := json.MarshalIndent(exportCfg, "", "  ")
	if err != nil {
		return writeError(ctx, http.StatusInternalServerError, "failed to serialize config")
	}

	logger.Info("Spoke config exported", "admin", userGUID, "username", user.Username)

	// Return as a downloadable JSON file
	filename := fmt.Sprintf("gonotes-spoke-%s.json", user.Username)
	ctx.Response().SetHeader("Content-Type", "application/json")
	ctx.Response().SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	ctx.SetStatus(http.StatusOK)
	return ctx.Bytes(configJSON)
}

// determineHubURL constructs the hub's base URL from the incoming request.
// Uses X-Forwarded-Proto for the scheme (common behind reverse proxies)
// and falls back to "http" when not proxied. Host is read from the
// standard Host header.
func determineHubURL(ctx rweb.Context) string {
	scheme := ctx.Request().Header("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
	}
	host := ctx.Request().Header("Host")
	if host == "" {
		host = ctx.Request().Header("X-Forwarded-Host")
	}
	return scheme + "://" + host
}
