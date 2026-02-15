# GoNotes: Multi-User Hub + Invite Token System

## Context

GoNotes uses a hub-spoke sync model. The hub currently has user authentication but **no data isolation** — sync endpoints return all users' changes. Categories have no user ownership at all. There's no role system and no way to onboard new users securely beyond a shared registration secret.

**Goal**: Make the hub properly multi-user (per-user notes AND categories), add an admin role, and implement an invite token flow so admins can securely onboard new users/spokes.

**Design principle**: Hub is multi-user with full data isolation. Spokes are single-user (sync as one user). Future note sharing/publishing is out of scope but the design must not block it.

---

## Step 1: Admin Role on Users

**Files**: `models/user.go`, `models/token.go`, `models/db.go`, `web/middleware.go`, `web/api/auth.go`

### Schema migration in `db.go` (after users table creation):
```sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN DEFAULT false
```

### `models/user.go`:
- Add `IsAdmin bool` to `User` struct and `UserOutput`
- Update `ToOutput()` to copy `IsAdmin`
- Update all `Scan()` calls in `GetUserByUsername`, `GetUserByGUID`, `GetUserByID`, `CreateUser` to include `is_admin`
- In `CreateUser`: check `IsFirstUser()` — if true, set `is_admin = true` in INSERT

### `models/token.go`:
- Add `IsAdmin bool` to `TokenClaims`
- Update `GenerateToken` to populate it

### `web/middleware.go`:
- Set `"is_admin"` in context from JWT claims in `JWTAuthMiddleware`

### `web/api/auth.go`:
- Add `IsAdmin(ctx) bool` helper (reads `"is_admin"` from context)

---

## Step 2: Invite Token System

**New files**: `models/invite_token.go`, `web/api/admin.go`
**Modified**: `models/db.go`, `web/api/auth.go`, `web/routes.go`, `models/sync_config.go`, `models/sync_client.go`

### New table in `db.go`:
```sql
CREATE SEQUENCE IF NOT EXISTS invite_tokens_id_seq START 1;
CREATE TABLE IF NOT EXISTS invite_tokens (
    id         BIGINT PRIMARY KEY DEFAULT nextval('invite_tokens_id_seq'),
    token      VARCHAR NOT NULL UNIQUE,
    created_by VARCHAR NOT NULL,
    used_by    VARCHAR,
    expires_at TIMESTAMP NOT NULL,
    used_at    TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_invite_tokens_token ON invite_tokens(token);
```

### `models/invite_token.go`:
- `InviteToken` struct, `InviteTokenOutput` struct
- `CreateInviteToken(createdByGUID string, expiresIn time.Duration) (*InviteToken, error)` — generates 32-byte crypto/rand hex token
- `ValidateInviteToken(token string) (*InviteToken, error)` — checks exists, not expired, not used
- `RedeemInviteToken(token, usedByGUID string) error` — marks used
- `ListInviteTokens(createdByGUID string) ([]InviteTokenOutput, error)`
- Default expiry: 72 hours

### `web/api/admin.go`:
- `POST /api/v1/admin/invites` → `CreateInviteToken` — admin-only, body: `{"expires_in_hours": 72}`
- `GET /api/v1/admin/invites` → `ListInviteTokens` — admin-only

### Update `web/api/auth.go` Register handler:
- Accept `invite_token` field in request body alongside existing `registration_secret`
- Gating logic (in order):
  1. First user → allow freely (becomes admin)
  2. `invite_token` provided → validate via `ValidateInviteToken()`, redeem after creation
  3. `registration_secret` provided → check env var (backward compatible)
  4. Neither → reject 403

### `web/routes.go`:
```go
s.Post("/api/v1/admin/invites", api.CreateInviteToken)
s.Get("/api/v1/admin/invites", api.ListInviteTokens)
```

### Spoke-side auto-registration in `models/sync_config.go`:
- Add `InviteToken string` field, loaded from `GONOTES_SYNC_INVITE_TOKEN`

### Spoke-side auto-registration in `models/sync_client.go`:
- Update `authenticate()`: try login first; on failure, if invite token is configured, POST to `/api/v1/auth/register` with the invite token, then login
- `registerWithInviteToken(ctx) error` — new method

### End-to-end flow:
1. Admin on hub: `POST /api/v1/admin/invites` → gets token string
2. Admin gives token to spoke user
3. Spoke user sets env vars: `GONOTES_SYNC_INVITE_TOKEN=<token>`, `_USERNAME`, `_PASSWORD`, `_HUB_URL`, `_ENABLED=true`
4. First sync cycle: login fails → registers with invite token → login succeeds → sync proceeds
5. Token is single-use; subsequent restarts use cached JWT

---

## Step 3: Category User-Scoping

**Files**: `models/category.go`, `models/db.go`, `web/api/categories.go`, `models/sync_apply.go`, `models/sync_protocol.go`, `models/category_change.go`

### Schema migration in `db.go`:
```sql
ALTER TABLE categories ADD COLUMN IF NOT EXISTS created_by VARCHAR
```
Backfill: `UPDATE categories SET created_by = (SELECT guid FROM users ORDER BY id LIMIT 1) WHERE created_by IS NULL`

### Update cache DDL:
- `initCacheDB()` category table must include `created_by VARCHAR`
- `syncCategoriesFromDisk()` must SELECT/INSERT `created_by`

### `models/category.go`:
- Add `CreatedBy sql.NullString` to `Category` struct
- Add `userGUID string` parameter to: `CreateCategory`, `ListCategories`, `GetCategory`, `UpdateCategory`, `DeleteCategory`, `GetCategoryByName`, `GetCategoryNotes`
- All queries add `WHERE created_by = ?` (or `AND created_by = ?`)
- Keep `GetCategoryByGUID` without user filter (used by sync internals, future sharing)

### `web/api/categories.go`:
- All handlers extract `userGUID`, return 401 if empty, pass to model functions

### `models/category_change.go`:
- `recordCategoryCreateChange`, `recordCategoryUpdateChange`, `recordCategoryDeleteChange` accept and record `userGUID`

### `models/sync_apply.go`:
- `ApplySyncCategoryCreate` accepts `userGUID` from `SyncChange.User`, stores as `created_by`

---

## Step 4: Sync User-Scoping on Hub

**Files**: `models/note_change.go`, `models/category_change.go`, `models/sync_protocol.go`, `web/api/sync.go`

### `models/note_change.go`:
- `GetUnsentChangesForPeer(peerID, userGUID string, limit int)` — add optional `userGUID` filter:
  - When non-empty: `INNER JOIN notes n ON nc.note_guid = n.guid ... AND n.created_by = ?`
  - When empty: no filter (spoke calling locally, all notes are one user's)

### `models/category_change.go`:
- `GetUnsentCategoryChangesForPeer(peerID, userGUID string, limit int)` — same pattern with `INNER JOIN categories c ON ... AND c.created_by = ?`

### `models/sync_protocol.go`:
- `GetUnifiedChangesForPeer(peerID, userGUID string, limit int)` — passes `userGUID` to both unsent-change functions
- `GetSyncStatus(userGUID string)` — filter counts and checksum by user when non-empty

### `web/api/sync.go`:
- `PullChanges`: pass `userGUID` to `GetUnifiedChangesForPeer(peerID, userGUID, limit)`
- `PushChanges`: override `change.User = userGUID` on each incoming change (prevents impersonation)
- `GetSyncStatus`: pass `userGUID` to `GetSyncStatus(userGUID)`

### Spoke `pushChanges` in `models/sync_client.go`:
- Pass `""` as `userGUID` to local `GetUnifiedChangesForPeer` (spoke has one user, no filter needed)

---

## Implementation Order

```
Step 1: Admin role          (independent — schema + user model + JWT)
Step 2: Invite tokens       (depends on Step 1 for is_admin)
Step 3: Category scoping    (independent of Step 2 but logically sequential)
Step 4: Sync scoping        (depends on Step 3 for created_by on categories)
```

---

## Verification

1. `go build ./...` — compiles
2. `go test ./models/... ./web/...` — existing tests pass (will need userGUID params added)
3. Register first user → verify `is_admin = true`
4. Admin creates invite token → new user registers with it → token is marked used
5. Spoke with `GONOTES_SYNC_INVITE_TOKEN` auto-registers and syncs
6. Create notes/categories as User A → verify User B cannot see them via API
7. Sync as User A → verify only User A's changes are pulled
8. Push changes from spoke → verify `change.User` is overridden to authenticated user
