# User Authentication and Permissions Plan

## Overview
Add user login, session management, and user-scoped CRUD/sync for notes. API-focused (no UI).

**Authentication**: Username/Password with bcrypt
**Sessions**: JWT tokens (stateless, 7-day expiration)
**Existing Data**: Migrate orphaned notes to first registered user

---

## Data Models

### User Model (`models/user.go`)
```go
type User struct {
    ID           int64          `json:"id"`
    GUID         string         `json:"guid"`           // For sync
    Username     string         `json:"username"`       // Unique login
    Email        sql.NullString `json:"email"`          // Optional
    PasswordHash string         `json:"-"`              // bcrypt, never in JSON
    DisplayName  sql.NullString `json:"display_name"`
    IsActive     bool           `json:"is_active"`      // Soft disable
    CreatedAt    time.Time      `json:"created_at"`
    UpdatedAt    time.Time      `json:"updated_at"`
    LastLoginAt  sql.NullTime   `json:"last_login_at"`
}
```

### Token Claims (`models/token.go`)
```go
type TokenClaims struct {
    jwt.RegisteredClaims
    UserGUID string `json:"user_guid"`
    Username string `json:"username"`
}
```

---

## API Endpoints

### Authentication (`web/api/auth.go`)
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/v1/auth/register` | POST | No | Create account, returns JWT |
| `/api/v1/auth/login` | POST | No | Authenticate, returns JWT |
| `/api/v1/auth/me` | GET | Yes | Get current user profile |
| `/api/v1/auth/refresh` | POST | Yes | Refresh JWT token |

### Sync (`web/api/sync.go`)
| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/v1/sync/changes` | GET | Yes | Get user's changes since timestamp |

---

## Middleware Changes

Replace `SessionMiddleware` with `JWTAuthMiddleware`:

```go
// JWTAuthMiddleware - extracts and validates JWT from Authorization header
func JWTAuthMiddleware(c rweb.Context) error {
    authHeader := c.Request().Header("Authorization")
    if strings.HasPrefix(authHeader, "Bearer ") {
        tokenString := strings.TrimPrefix(authHeader, "Bearer ")
        claims, err := models.ValidateToken(tokenString)
        if err == nil {
            c.Set("user_guid", claims.UserGUID)
            c.Set("authenticated", true)
            return c.Next()
        }
    }
    c.Set("user_guid", "")
    c.Set("authenticated", false)
    return c.Next()
}

// RequireAuth - returns 401 if not authenticated
func RequireAuth(c rweb.Context) error {
    if !IsAuthenticated(c) {
        return writeError(c, http.StatusUnauthorized, "authentication required")
    }
    return c.Next()
}
```

---

## Note CRUD Modifications

All note functions gain `userGUID` parameter:

```go
// Models layer - add user scoping
func GetNoteByID(id int64, userGUID string) (*Note, error)
func ListNotes(userGUID string, limit, offset int) ([]Note, error)
func CreateNote(input NoteInput, userGUID string) (*Note, error)  // Sets created_by
func UpdateNote(id int64, input NoteInput, userGUID string) (*Note, error)  // Verifies ownership
func DeleteNote(id int64, userGUID string) (bool, error)  // Verifies ownership
```

API handlers extract user from context:
```go
userGUID := GetCurrentUserGUID(ctx)
note, err := models.CreateNote(input, userGUID)
```

---

## NoteChange User Tracking

Update CRUD functions to pass actual user to `insertNoteChange()`:

```go
// Currently: insertNoteChange(..., "")
// After:    insertNoteChange(..., userGUID)
```

Add sync query for user's changes:
```go
func GetUserChangesSince(userGUID string, since time.Time, limit int) ([]NoteChangeOutput, error)
```

---

## First User Migration

```go
// In Register handler, after first user creates account:
if isFirstUser {
    count, _ := models.MigrateOrphanedNotes(user.GUID)
    // Updates notes WHERE created_by IS NULL
}
```

---

## Dependencies

```bash
go get golang.org/x/crypto/bcrypt
go get github.com/golang-jwt/jwt/v5
```

Environment variable: `GONOTES_JWT_SECRET` (min 32 chars)

---

## Security Considerations

1. **Password**: Min 8 chars, bcrypt cost 12
2. **JWT**: 7-day expiration, HMAC-SHA256 signing
3. **Rate Limiting**: Apply `RateLimitMiddleware(10)` to auth endpoints
4. **Ownership**: All note operations verify `created_by = userGUID`

---

## Implementation Phases

### Phase 1: Foundation
1. Add bcrypt and jwt dependencies
2. Create `models/user.go` - User struct, DDL, CRUD
3. Create `models/token.go` - JWT functions
4. Update `models/db.go` - create users table
5. Add JWT secret initialization

### Phase 2: Authentication
6. Create `web/api/auth.go` - Register/Login handlers
7. Update `web/middleware.go` - JWTAuthMiddleware
8. Update `web/routes.go` - auth routes

### Phase 3: User Scoping
9. Modify `models/note.go` - add userGUID to all functions
10. Update `web/api/notes.go` - extract user from context
11. Add orphaned notes migration

### Phase 4: Sync Support
12. Update `insertNoteChange()` calls to pass userGUID
13. Create `web/api/sync.go` - sync endpoints
14. Add sync routes

### Phase 5: Testing
15. `models/user_test.go` - user CRUD tests
16. `models/token_test.go` - JWT tests
17. `web/api/auth_test.go` - auth integration tests

---

## Files Summary

### New Files
| File | Purpose |
|------|---------|
| `models/user.go` | User model, DDL, CRUD, password hashing |
| `models/token.go` | JWT generation and validation |
| `models/user_test.go` | User model tests |
| `models/token_test.go` | Token tests |
| `web/api/auth.go` | Register, Login, Me, Refresh handlers |
| `web/api/auth_test.go` | Auth integration tests |
| `web/api/sync.go` | Sync changes endpoint |

### Modified Files
| File | Changes |
|------|---------|
| `go.mod` | Add bcrypt, jwt dependencies |
| `models/db.go` | Create users table, init JWT |
| `models/note.go` | Add userGUID param to all functions |
| `models/note_change.go` | Pass userGUID to insertNoteChange |
| `web/middleware.go` | Replace SessionMiddleware with JWT |
| `web/routes.go` | Add auth routes, apply RequireAuth |
| `web/api/notes.go` | Extract user from context |
| `main.go` | Call InitJWT on startup |

---

## Verification

1. **Unit tests**: `go test ./models -run "User|Token"`
2. **Integration tests**: `go test ./web/api -run Auth`
3. **Manual verification**:
   - Register: `curl -X POST localhost:8000/api/v1/auth/register -d '{"username":"test","password":"password123"}'`
   - Login: `curl -X POST localhost:8000/api/v1/auth/login -d '{"username":"test","password":"password123"}'`
   - Create note with token: `curl -H "Authorization: Bearer <token>" -X POST localhost:8000/api/v1/notes -d '{"guid":"test","title":"My Note"}'`
   - Verify user B cannot see user A's notes
