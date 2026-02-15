# Plan: Base64 Password Encoding + Spoke Config Export/Import

## Context
The sync password (`GONOTES_SYNC_PASSWORD`) is stored as plaintext in the `.env` file, making it vulnerable to casual exposure. Additionally, setting up a new spoke requires manually copying multiple env vars. This plan adds:
1. Base64 encoding for the password env var (obfuscation, not encryption)
2. A hub-side admin action to export a ready-to-use spoke config JSON file (password-protected)
3. A spoke-side `/setup` page to import that config and write the `.env` file

## Step 1: Base64 Password in sync_config.go

**File: `models/sync_config.go`**
- Add `"encoding/base64"` import
- Read `GONOTES_SYNC_PASSWORD_B64` env var, base64-decode it into `cfg.Password`
- Backward compat: fall back to `GONOTES_SYNC_PASSWORD` if `_B64` is empty (migration path)
- Update `Validate()` error message to reference new var name

**File: `config/cfg_files/.env-sample`**
- Replace `GONOTES_SYNC_PASSWORD=MySecurePass123!` with `GONOTES_SYNC_PASSWORD_B64=TXlTZWN1cmVQYXNzMTIzIQ==`
- Add comment: `# Base64 encoded. Generate with: echo -n 'YourPassword' | base64`
- Add `GONOTES_SYNC_INVITE_TOKEN=` line (currently missing from sample)

## Step 2: Hub-Side Export API

**New file: `web/api/config_export.go`**

Define `SpokeExportConfig` struct (shared by export and import):
```go
type SpokeExportConfig struct {
    HubURL       string `json:"hub_url"`
    Username     string `json:"username"`
    PasswordB64  string `json:"password_b64"`
    JWTSecret    string `json:"jwt_secret"`
    InviteToken  string `json:"invite_token"`
    SyncInterval string `json:"sync_interval"`
    ExportedAt   string `json:"exported_at"`
}
```

`ExportSpokeConfig` handler — `POST /api/v1/admin/export-spoke-config`:
1. Check `IsAdmin(ctx)` — 403 if not admin
2. Parse `{"password": "..."}` from request body
3. Verify password: `models.GetUserByGUID(userGUID)` then `models.CheckPassword(req.Password, user.PasswordHash)`
4. Auto-generate invite token: `models.CreateInviteToken(userGUID, 72*time.Hour)`
5. Build hub URL from request headers (`X-Forwarded-Proto` + `Host`)
6. Read `GONOTES_JWT_SECRET` from `os.Getenv`
7. Base64-encode the password: `base64.StdEncoding.EncodeToString([]byte(req.Password))`
8. Return JSON as file download with `Content-Disposition: attachment; filename="gonotes-spoke-<username>.json"`

Reuse: `models.GetUserByGUID` (user.go:265), `models.CheckPassword` (user.go:131), `models.CreateInviteToken` (invite_token.go:93)

## Step 3: Settings Modal with Export Button (Hub UI)

**File: `web/static/js/app.js`**
- Replace `showSettings()` placeholder with real modal content
- If `state.user.is_admin`: show "Spoke Configuration Export" section with password input + export button
- Add `exportSpokeConfig()` function: POST to `/api/v1/admin/export-spoke-config`, trigger blob download
- Fix `closeModal()` to restore modal footer visibility (since settings modal hides it)

## Step 4: Spoke-Side Setup Page

**New file: `web/pages/setup/page.go`**
- Standalone page following `auth/login.go` pattern (auth-container, auth-card layout)
- "Setup Sync" heading, file upload input (`.json`), preview area, "Apply" button
- Loads `/static/js/setup.js`

**New file: `web/static/js/setup.js`**
- `handleFileSelect()`: read JSON file via FileReader, parse, display preview (hub_url, username, password masked, interval)
- `applyConfig()`: POST parsed JSON to `/api/v1/setup/apply`
- On success: show "Configuration saved. Restart GoNotes to activate sync."

**New file: `web/api/config_import.go`**
- `ApplySpokeConfig` handler — `POST /api/v1/setup/apply` (no auth required — first-run)
- Guard: only available when `GONOTES_SYNC_ENABLED != "true"` (prevents reconfiguring a running spoke)
- Parse `SpokeExportConfig` from body, validate required fields
- Write to `config/cfg_files/.env` with `os.WriteFile(..., 0600)`

## Step 5: Route Registration

**File: `web/routes.go`**
- Add import: `"gonotes/web/pages/setup"`
- Admin export route: `s.Post("/api/v1/admin/export-spoke-config", api.ExportSpokeConfig)`
- Setup page route: `s.Get("/setup", ...)` rendering `setup.NewPage()`
- Setup apply route: `s.Post("/api/v1/setup/apply", api.ApplySpokeConfig)`

## Step 6: CSS for Setup Page

**File: `web/static/css/app.css`**
- `.settings-section` — padding/border for settings modal sections
- `.settings-description` — muted explanatory text
- `.config-preview` / `.config-field` — preview area on /setup page
- Reuse existing `.auth-container`, `.auth-card`, `.form-group`, `.form-input` classes for /setup page

## Verification
1. `go build ./...` — compiles cleanly
2. `go test ./models/ ./web/api/` — all existing tests pass
3. Manual test — hub side:
   - Log in as admin, open Settings modal, enter password, click Export
   - Verify JSON file downloads with correct fields (hub_url, username, password_b64, jwt_secret, invite_token)
4. Manual test — spoke side:
   - Visit `/setup` on a fresh spoke instance
   - Upload the exported JSON, verify preview shows correct fields
   - Click Apply, verify `.env` file is written correctly
   - Verify `/setup` returns 403 after sync is configured
