package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// AuthResponse contains the user and token returned on successful authentication
type AuthResponse struct {
	User  models.UserOutput `json:"user"`
	Token string            `json:"token"`
}

// Register creates a new user account and returns a JWT token.
// POST /api/v1/auth/register
//
// Request body:
//
//	{
//	  "username": "johndoe",
//	  "password": "SecurePass123!",
//	  "email": "john@example.com",      // optional
//	  "display_name": "John Doe"        // optional
//	}
//
// Success (201):
//
//	{ "success": true, "data": { "user": {...}, "token": "..." } }
//
// Errors:
//   - 400: Invalid input (missing/weak password, invalid username)
//   - 409: Username or email already exists
func Register(ctx rweb.Context) error {
	var input models.UserRegisterInput
	if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid request body")
	}

	// Validate required fields
	if input.Username == "" {
		return writeError(ctx, http.StatusBadRequest, "username is required")
	}
	if input.Password == "" {
		return writeError(ctx, http.StatusBadRequest, "password is required")
	}

	// Check if this is the first user (for orphaned notes migration)
	isFirst, err := models.IsFirstUser()
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to check first user"), "registration check")
		// Continue anyway - migration is best-effort
	}

	// Create the user
	user, err := models.CreateUser(input)
	if err != nil {
		errMsg := err.Error()
		// Check for duplicate username/email
		if strings.Contains(errMsg, "already exists") {
			return writeError(ctx, http.StatusConflict, errMsg)
		}
		// Check for validation errors
		if strings.Contains(errMsg, "must be") || strings.Contains(errMsg, "can only") {
			return writeError(ctx, http.StatusBadRequest, errMsg)
		}
		logger.LogErr(serr.Wrap(err, "failed to create user"), "username", input.Username)
		return writeError(ctx, http.StatusInternalServerError, "failed to create user")
	}

	// Migrate orphaned notes if this is the first user
	if isFirst {
		migratedCount, err := models.MigrateOrphanedNotes(user.GUID)
		if err != nil {
			logger.LogErr(serr.Wrap(err, "failed to migrate orphaned notes"), "user_guid", user.GUID)
		} else if migratedCount > 0 {
			logger.Info("Migrated orphaned notes to first user",
				"count", migratedCount, "user", user.Username)
		}
	}

	// Generate JWT token
	token, err := models.GenerateToken(user)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to generate token"), "user_id", user.ID)
		return writeError(ctx, http.StatusInternalServerError, "failed to generate token")
	}

	// Return success with user and token
	response := AuthResponse{
		User:  user.ToOutput(),
		Token: token,
	}

	return writeSuccess(ctx, http.StatusCreated, response)
}

// Login authenticates a user and returns a JWT token.
// POST /api/v1/auth/login
//
// Request body:
//
//	{
//	  "username": "johndoe",
//	  "password": "SecurePass123!"
//	}
//
// Success (200):
//
//	{ "success": true, "data": { "user": {...}, "token": "..." } }
//
// Errors:
//   - 400: Missing username or password
//   - 401: Invalid credentials
//   - 403: Account is disabled
func Login(ctx rweb.Context) error {
	var input models.UserLoginInput
	if err := json.Unmarshal(ctx.Request().Body(), &input); err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid request body")
	}

	// Validate required fields
	if input.Username == "" {
		return writeError(ctx, http.StatusBadRequest, "username is required")
	}
	if input.Password == "" {
		return writeError(ctx, http.StatusBadRequest, "password is required")
	}

	// Authenticate user
	user, err := models.AuthenticateUser(input)
	if err != nil {
		errMsg := err.Error()
		// Check for disabled account
		if strings.Contains(errMsg, "disabled") {
			return writeError(ctx, http.StatusForbidden, "account is disabled")
		}
		logger.LogErr(serr.Wrap(err, "authentication error"), "username", input.Username)
		return writeError(ctx, http.StatusInternalServerError, "authentication error")
	}

	if user == nil {
		// Invalid credentials - don't reveal whether username exists
		return writeError(ctx, http.StatusUnauthorized, "invalid credentials")
	}

	// Generate JWT token
	token, err := models.GenerateToken(user)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to generate token"), "user_id", user.ID)
		return writeError(ctx, http.StatusInternalServerError, "failed to generate token")
	}

	// Return success with user and token
	response := AuthResponse{
		User:  user.ToOutput(),
		Token: token,
	}

	return writeSuccess(ctx, http.StatusOK, response)
}

// GetCurrentUser returns the authenticated user's profile.
// GET /api/v1/auth/me
//
// Headers required:
//
//	Authorization: Bearer <jwt_token>
//
// Success (200):
//
//	{ "success": true, "data": { "id": 1, "guid": "...", "username": "..." } }
//
// Errors:
//   - 401: Missing or invalid token
func GetCurrentUser(ctx rweb.Context) error {
	// Get user GUID from context (set by JWTAuthMiddleware)
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Look up the user
	user, err := models.GetUserByGUID(userGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get user"), "user_guid", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to get user")
	}

	if user == nil {
		return writeError(ctx, http.StatusUnauthorized, "user not found")
	}

	return writeSuccess(ctx, http.StatusOK, user.ToOutput())
}

// RefreshToken generates a new JWT token for the authenticated user.
// POST /api/v1/auth/refresh
//
// Headers required:
//
//	Authorization: Bearer <jwt_token>
//
// Success (200):
//
//	{ "success": true, "data": { "token": "..." } }
//
// Errors:
//   - 401: Missing or invalid token
//   - 403: Account is disabled
func RefreshToken(ctx rweb.Context) error {
	// Get user GUID from context (set by JWTAuthMiddleware)
	userGUID := GetCurrentUserGUID(ctx)
	if userGUID == "" {
		return writeError(ctx, http.StatusUnauthorized, "authentication required")
	}

	// Look up the user to verify they're still active
	user, err := models.GetUserByGUID(userGUID)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to get user"), "user_guid", userGUID)
		return writeError(ctx, http.StatusInternalServerError, "failed to get user")
	}

	if user == nil {
		return writeError(ctx, http.StatusUnauthorized, "user not found")
	}

	if !user.IsActive {
		return writeError(ctx, http.StatusForbidden, "account is disabled")
	}

	// Generate new token
	token, err := models.GenerateToken(user)
	if err != nil {
		logger.LogErr(serr.Wrap(err, "failed to generate token"), "user_id", user.ID)
		return writeError(ctx, http.StatusInternalServerError, "failed to generate token")
	}

	return writeSuccess(ctx, http.StatusOK, map[string]string{"token": token})
}

// GetCurrentUserGUID extracts the user GUID from the request context.
// Returns empty string if not authenticated.
func GetCurrentUserGUID(ctx rweb.Context) string {
	guid, _ := ctx.Get("user_guid").(string)
	return guid
}

// IsAuthenticated checks if the request has valid authentication.
func IsAuthenticated(ctx rweb.Context) bool {
	auth, _ := ctx.Get("authenticated").(bool)
	return auth
}

