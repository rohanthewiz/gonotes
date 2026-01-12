---
name: GoNotes
description: Note-taking application with user authentication, categories, encryption, and peer-to-peer sync support
---

# GoNotes API Reference

GoNotes is a note-taking application with user authentication, categories, and peer-to-peer sync support. This document describes the API capabilities for UI development.

## Architecture Overview

- **Database**: DuckDB with disk (source of truth) + in-memory cache pattern
- **Authentication**: JWT tokens (7-day expiration) with bcrypt password hashing
- **Server**: RWeb framework (Echo-like) on port 8000
- **Encryption**: AES-256 encryption for private notes (encrypted on disk, plaintext in cache)

## Base URL

```
http://localhost:8000
```

## Authentication

All note and sync endpoints require authentication via Bearer token in the Authorization header:

```
Authorization: Bearer <jwt_token>
```

### Auth Endpoints

#### Register New User
```
POST /api/v1/auth/register
```
**Request Body:**
```json
{
  "username": "string",       // Required, unique
  "password": "string",       // Required, min 8 characters
  "email": "string",          // Optional, unique if provided
  "display_name": "string"    // Optional
}
```
**Response (201 Created):**
```json
{
  "success": true,
  "data": {
    "user": {
      "id": 1,
      "guid": "uuid-string",
      "username": "string",
      "email": "string",
      "display_name": "string",
      "is_active": true,
      "created_at": "RFC3339 timestamp"
    },
    "token": "jwt-token-string"
  }
}
```

#### Login
```
POST /api/v1/auth/login
```
**Request Body:**
```json
{
  "username": "string",
  "password": "string"
}
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "user": { ... },
    "token": "jwt-token-string"
  }
}
```

#### Get Current User
```
GET /api/v1/auth/me
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "guid": "uuid-string",
    "username": "string",
    "email": "string",
    "display_name": "string",
    "is_active": true,
    "created_at": "RFC3339 timestamp",
    "last_login_at": "RFC3339 timestamp"
  }
}
```

#### Refresh Token
```
POST /api/v1/auth/refresh
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "token": "new-jwt-token-string"
  }
}
```

---

## Notes API

All note endpoints are user-scoped. Users can only access their own notes.

### Note Data Model

**NoteInput (Request):**
```json
{
  "guid": "string",           // Required for create, unique identifier
  "title": "string",          // Required
  "description": "string",    // Optional
  "body": "string",           // Optional, main content
  "tags": "string",           // Optional, comma-separated
  "is_private": false         // Optional, enables encryption if true
}
```

**NoteOutput (Response):**
```json
{
  "id": 1,
  "guid": "uuid-string",
  "title": "string",
  "description": "string",
  "body": "string",
  "tags": "string",
  "is_private": false,
  "encryption_iv": "string",  // Present if encrypted
  "created_by": "user-guid",
  "updated_by": "user-guid",
  "created_at": "RFC3339 timestamp",
  "updated_at": "RFC3339 timestamp",
  "authored_at": "RFC3339 timestamp",
  "synced_at": "RFC3339 timestamp",
  "deleted_at": "RFC3339 timestamp"
}
```

### Note Endpoints

#### Create Note
```
POST /api/v1/notes
```
**Request Body:** NoteInput (guid and title required)
**Response (201 Created):**
```json
{
  "success": true,
  "data": { NoteOutput }
}
```

#### List Notes
```
GET /api/v1/notes
```
**Query Parameters:**
- `limit` (int): Maximum number of results
- `offset` (int): Number of results to skip
- `cat` (string): Filter by category name
- `subcats[]` (string[]): Filter by subcategories (requires `cat`)

**Response (200 OK):**
```json
{
  "success": true,
  "data": [ NoteOutput, ... ]
}
```

**Examples:**
```
GET /api/v1/notes?limit=10&offset=0
GET /api/v1/notes?cat=k8s
GET /api/v1/notes?cat=k8s&subcats[]=pod&subcats[]=deployment
```

#### Get Note by ID
```
GET /api/v1/notes/:id
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": { NoteOutput }
}
```

#### Update Note
```
PUT /api/v1/notes/:id
```
**Request Body:** NoteInput (title required)
**Response (200 OK):**
```json
{
  "success": true,
  "data": { NoteOutput }
}
```

#### Delete Note (Soft Delete)
```
DELETE /api/v1/notes/:id
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "deleted": true,
    "id": 1
  }
}
```

---

## Categories API

Categories help organize notes. Notes can belong to multiple categories.

### Category Data Model

**CategoryInput (Request):**
```json
{
  "name": "string",           // Required
  "description": "string",    // Optional
  "subcategories": ["string"] // Optional, array of subcategory names
}
```

**CategoryOutput (Response):**
```json
{
  "id": 1,
  "name": "string",
  "description": "string",
  "subcategories": ["string"],
  "created_at": "RFC3339 timestamp",
  "updated_at": "RFC3339 timestamp"
}
```

### Category Endpoints

#### Create Category
```
POST /api/v1/categories
```
**Request Body:** CategoryInput
**Response (201 Created):**
```json
{
  "success": true,
  "data": { CategoryOutput }
}
```

#### List Categories
```
GET /api/v1/categories
```
**Query Parameters:**
- `limit` (int): Maximum number of results
- `offset` (int): Number of results to skip

**Response (200 OK):**
```json
{
  "success": true,
  "data": [ CategoryOutput, ... ]
}
```

#### Get Category by ID
```
GET /api/v1/categories/:id
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": { CategoryOutput }
}
```

#### Update Category
```
PUT /api/v1/categories/:id
```
**Request Body:** CategoryInput
**Response (200 OK):**
```json
{
  "success": true,
  "data": { CategoryOutput }
}
```

#### Delete Category
```
DELETE /api/v1/categories/:id
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "deleted": true,
    "id": 1
  }
}
```

---

## Note-Category Relationships

Notes can be assigned to categories with optional subcategories per relationship.

### Relationship Endpoints

#### Add Category to Note
```
POST /api/v1/notes/:id/categories/:category_id
```
**Request Body (optional):**
```json
{
  "subcategories": ["pod", "deployment"]
}
```
**Response (201 Created):**
```json
{
  "success": true,
  "data": {
    "note_id": 1,
    "category_id": 2,
    "subcategories": ["pod", "deployment"]
  }
}
```

#### Remove Category from Note
```
DELETE /api/v1/notes/:id/categories/:category_id
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "removed": true
  }
}
```

#### Get Categories for a Note
```
GET /api/v1/notes/:id/categories
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": [
    {
      "category": { CategoryOutput },
      "subcategories": ["pod", "deployment"]
    }
  ]
}
```

#### Get Notes for a Category
```
GET /api/v1/categories/:id/notes
```
**Response (200 OK):**
```json
{
  "success": true,
  "data": [ NoteOutput, ... ]
}
```

---

## Sync API

The sync API enables peer-to-peer synchronization between devices.

### Change Tracking

Every note create/update/delete automatically creates a change record with:
- Delta storage: Only changed fields are recorded
- Bitmask: Indicates which fields changed
- User tracking: Who made the change
- Timestamps: When the change occurred

### Sync Endpoints

#### Get User Changes Since Timestamp
```
GET /api/v1/sync/changes
```
**Query Parameters:**
- `since` (RFC3339 timestamp, required): Return changes after this time
- `limit` (int, optional): Maximum number of changes to return

**Response (200 OK):**
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "guid": "change-uuid",
      "note_guid": "note-uuid",
      "operation": 1,
      "fragment": {
        "bitmask": 160,
        "title": "New Title",
        "body": "New body content"
      },
      "user": "user-guid",
      "created_at": "RFC3339 timestamp"
    }
  ]
}
```

**Operation Values:**
- `1`: Create - New note created
- `2`: Update - Note fields modified
- `3`: Delete - Note soft-deleted
- `9`: Sync - Change received from peer

**Fragment Bitmask Values:**
- `0x80` (128): Title changed
- `0x40` (64): Description changed
- `0x20` (32): Body changed
- `0x10` (16): Tags changed
- `0x08` (8): IsPrivate changed
- `0x04` (4): Categories changed

---

## Error Responses

All error responses follow this format:

```json
{
  "success": false,
  "error": "error message"
}
```

### HTTP Status Codes

| Code | Description |
|------|-------------|
| 200 | Success |
| 201 | Created |
| 400 | Bad Request - Invalid input |
| 401 | Unauthorized - Missing or invalid token |
| 404 | Not Found - Resource doesn't exist |
| 409 | Conflict - Duplicate resource (e.g., duplicate GUID) |
| 500 | Internal Server Error |

---

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `GONOTES_JWT_SECRET` | JWT signing secret (min 32 chars) | Random (dev only) |
| `GONOTES_ENCRYPTION_KEY` | AES-256 key (exactly 32 chars) | Disabled if not set |

---

## Database Schema

### Tables

1. **users** - User accounts
2. **notes** - User notes with soft delete support
3. **categories** - Category definitions
4. **note_categories** - Many-to-many note-category relationships
5. **note_changes** - Change tracking for sync
6. **note_fragments** - Delta storage for changes
7. **note_change_sync_peers** - Per-peer sync tracking

### Key Design Patterns

- **Disk + Cache**: DuckDB disk database is source of truth; in-memory cache for fast reads
- **Soft Deletes**: Notes use `deleted_at` timestamp instead of hard delete
- **User Scoping**: All note operations filter by `created_by` to enforce ownership
- **Delta Sync**: Only changed fields are stored and transmitted

---

## UI Development Notes

### Authentication Flow
1. User registers or logs in
2. Store JWT token (localStorage/sessionStorage)
3. Include token in all API requests via Authorization header
4. Refresh token before expiration (7 days)

### Recommended UI Features
1. **Login/Register Forms** - Username, password, optional email
2. **Note List View** - Paginated list with search/filter
3. **Note Editor** - Title, body (markdown?), tags, privacy toggle
4. **Category Management** - Create, assign to notes, filter by
5. **Sync Status** - Show last sync time, manual sync button
6. **Offline Support** - Queue changes when offline, sync when online

### Private Notes
- When `is_private: true`, note body is encrypted on disk
- Cache stores plaintext for fast reads
- Encryption requires `GONOTES_ENCRYPTION_KEY` environment variable
