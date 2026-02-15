package api

import (
	"encoding/json"
	"net/http"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// CreateInviteToken handles POST /api/v1/admin/invites
// Admin-only endpoint that generates a single-use invite token for onboarding
// new users. The token can be shared out-of-band (email, chat, etc.) and the
// recipient uses it during registration.
//
// Request body (optional):
//
//	{ "expires_in_hours": 72 }
//
// Defaults to 72 hours if not specified.
func CreateInviteToken(ctx rweb.Context) error {
	// Admin authorization check
	if !IsAdmin(ctx) {
		return writeError(ctx, http.StatusForbidden, "admin access required")
	}

	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Parse optional expiry duration from request body
	var req struct {
		ExpiresInHours int `json:"expires_in_hours"`
	}

	body := ctx.Request().Body()
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, http.StatusBadRequest, "invalid request body")
		}
	}

	// Convert hours to duration — zero means use the default (72h)
	var expiresIn time.Duration
	if req.ExpiresInHours > 0 {
		expiresIn = time.Duration(req.ExpiresInHours) * time.Hour
	}

	token, err := models.CreateInviteToken(userGUID, expiresIn)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to create invite token"), "admin", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to create invite token")
	}

	logger.Info("Invite token created", "admin", userGUID, "expires_at", token.ExpiresAt)
	return writeSuccess(ctx, http.StatusCreated, token.ToOutput())
}

// ListInviteTokens handles GET /api/v1/admin/invites
// Admin-only endpoint that returns all invite tokens created by the
// authenticated admin, with usage status and expiry information.
func ListInviteTokens(ctx rweb.Context) error {
	// Admin authorization check
	if !IsAdmin(ctx) {
		return writeError(ctx, http.StatusForbidden, "admin access required")
	}

	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	tokens, err := models.ListInviteTokens(userGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to list invite tokens"), "admin", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to list invite tokens")
	}

	return writeSuccess(ctx, http.StatusOK, tokens)
}
