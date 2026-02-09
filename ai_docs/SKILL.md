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
  "tags": "string",           // Deprecated — kept for backward compat, no longer used by UI
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
  "tags": "string",           // Deprecated — use categories/subcategories instead
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

Categories organize notes into logical groups. Each category can define a set of subcategories.
A note can belong to **multiple categories**, and for each assigned category the user selects
which subcategories apply to that specific note. This creates a flexible two-level taxonomy:

```
Category "Kubernetes"
  └─ subcategories: pod, deployment, service, ingress

Note "K8s Networking" assigned to Kubernetes
  └─ selected_subcategories: service, ingress   (subset chosen per-note)
```

### Data Model

There are three levels of category data:

1. **CategoryInput / CategoryOutput** — the category definition (name, description, full list of available subcategories)
2. **note_categories junction table** — links a note to a category and stores which subcategories the user selected for that note
3. **NoteCategoryDetailOutput** — enriched response that combines category definition with the per-note subcategory selections

**CategoryInput (Request):**
```json
{
  "name": "string",           // Required
  "description": "string",    // Optional
  "subcategories": ["string"] // Optional, the full set of available subcategory names
}
```

**CategoryOutput (Response):**
```json
{
  "id": 1,
  "name": "string",
  "description": "string",
  "subcategories": ["string"],  // All available subcategories for this category
  "created_at": "RFC3339 timestamp",
  "updated_at": "RFC3339 timestamp"
}
```

**NoteCategoryDetailOutput (Response from GET /notes/:id/categories):**
```json
{
  "id": 1,
  "name": "Kubernetes",
  "description": "Container orchestration",
  "subcategories": ["pod", "deployment", "service", "ingress"],  // All available
  "selected_subcategories": ["pod", "deployment"],                // Chosen for this note
  "created_at": "RFC3339 timestamp",
  "updated_at": "RFC3339 timestamp"
}
```

### Category CRUD Endpoints

#### Create Category
```
POST /api/v1/categories
```
**Request Body:** CategoryInput (name required)
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
- `limit` (int): Maximum number of results (0 = no limit)
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
**Request Body:** CategoryInput (name required)

Use this to rename a category, update its description, or modify its subcategory list.
Adding new subcategory names to the array makes them available for selection;
removing names does **not** automatically unlink them from existing notes.

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

Notes can be assigned to categories. Each note-category link can carry a subset of
the category's subcategories indicating which apply to that particular note.

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
If no body is provided, the category is added without any subcategories selected.

**Response (201 Created):**
```json
{
  "success": true,
  "data": {
    "note_id": 1,
    "category_id": 2,
    "subcategories": ["pod", "deployment"],
    "added": true
  }
}
```

#### Update Subcategories for a Note-Category Link
```
PUT /api/v1/notes/:id/categories/:category_id
```
Changes which subcategories are selected for an existing note-category relationship
without removing and re-adding.

**Request Body:**
```json
{
  "subcategories": ["pod", "service"]
}
```

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "note_id": 1,
    "category_id": 2,
    "subcategories": ["pod", "service"],
    "updated": true
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
    "note_id": 1,
    "category_id": 2,
    "removed": true
  }
}
```

#### Get Categories for a Note (with subcategory details)
```
GET /api/v1/notes/:id/categories
```
Returns **NoteCategoryDetailOutput** objects — each includes the full list of
available subcategories *and* which ones are selected for this note. This is the
endpoint the UI uses to render preview and edit views.

**Response (200 OK):**
```json
{
  "success": true,
  "data": [
    {
      "id": 5,
      "name": "Kubernetes",
      "description": "Container orchestration",
      "subcategories": ["pod", "deployment", "service", "ingress"],
      "selected_subcategories": ["pod", "deployment"],
      "created_at": "RFC3339 timestamp",
      "updated_at": "RFC3339 timestamp"
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

#### Bulk Note-Category Mappings
```
GET /api/v1/note-category-mappings
```
Returns **all** note-category relationships for the authenticated user in one call.
The UI caches this as a lookup map keyed by note ID so the search-bar category
filter can operate instantly without per-note API calls.

**Response (200 OK):**
```json
{
  "success": true,
  "data": [
    {
      "note_id": 1,
      "category_id": 5,
      "category_name": "Kubernetes",
      "selected_subcategories": ["pod", "deployment"]
    }
  ]
}
```

### Filtering Notes by Category and Subcategories

The List Notes endpoint supports filtering by category:

```
GET /api/v1/notes?cat=Kubernetes
GET /api/v1/notes?cat=Kubernetes&subcats[]=pod&subcats[]=deployment
```

When `subcats[]` is provided alongside `cat`, only notes that have **all** the
specified subcategories selected are returned.

---

## Sync API

The sync API enables peer-to-peer synchronization between devices using a hub-and-spoke
topology. Spokes pull changes from the hub first (rebase model), then push their local changes.

### Change Tracking

Every note and category create/update/delete automatically creates a change record with:
- Delta storage: Only changed fields are recorded in fragments
- Bitmask: Indicates which fields changed
- User tracking: Who made the change
- Timestamps: When the change occurred
- Body diffs: Note body updates may store unified diff patches instead of full text

### Unified SyncChange Envelope

The sync protocol uses a unified `SyncChange` envelope that wraps both note and category
changes into a single chronologically ordered stream:

```json
{
  "id": 42,
  "guid": "change-uuid",
  "entity_type": "note",
  "entity_guid": "note-uuid",
  "operation": 1,
  "fragment": {
    "bitmask": 224,
    "title": "New Title",
    "body": "Body content",
    "body_is_diff": false
  },
  "authored_at": "RFC3339 timestamp",
  "user": "user-guid",
  "created_at": "RFC3339 timestamp"
}
```

**Entity Types:** `"note"` or `"category"`

**Operation Values:**
- `1`: Create — New entity created
- `2`: Update — Entity fields modified
- `3`: Delete — Entity deleted (soft delete for notes, hard delete for categories)
- `9`: Sync — Change received from a peer

**Note Fragment Bitmask Values:**
- `0x80` (128): Title changed
- `0x40` (64): Description changed
- `0x20` (32): Body changed
- `0x10` (16): Tags changed (deprecated — tags no longer used by UI)
- `0x08` (8): IsPrivate changed
- `0x04` (4): Categories changed

**Category Fragment Bitmask Values:**
- `0x80` (128): Name changed
- `0x40` (64): Description changed
- `0x20` (32): Subcategories changed

### Sync Endpoints

#### Get User Changes Since Timestamp
```
GET /api/v1/sync/changes
```
Returns note-only changes for the authenticated user since a timestamp.
This is the older, note-specific endpoint. For unified note+category sync, use Pull/Push below.

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

---

#### Pull Changes (Unified)
```
GET /api/v1/sync/pull
```
Returns a unified, chronologically ordered stream of note and category changes that
haven't been sent to the requesting peer yet. Changes are automatically marked as
synced to this peer after delivery.

**Query Parameters:**
- `peer_id` (string, required): Unique identifier for the requesting peer
- `limit` (int, optional, default: 100): Maximum number of changes to return

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "changes": [
      {
        "id": 1,
        "guid": "change-uuid",
        "entity_type": "category",
        "entity_guid": "cat-uuid",
        "operation": 1,
        "fragment": {
          "bitmask": 224,
          "name": "Kubernetes",
          "description": "Container orchestration",
          "subcategories": "[\"pod\",\"deployment\"]"
        },
        "authored_at": "RFC3339 timestamp",
        "user": "user-guid",
        "created_at": "RFC3339 timestamp"
      },
      {
        "id": 2,
        "guid": "change-uuid-2",
        "entity_type": "note",
        "entity_guid": "note-uuid",
        "operation": 1,
        "fragment": {
          "bitmask": 224,
          "title": "My Note",
          "body": "Note content",
          "body_is_diff": false
        },
        "authored_at": "RFC3339 timestamp",
        "user": "user-guid",
        "created_at": "RFC3339 timestamp"
      }
    ],
    "has_more": false
  }
}
```

**Notes:**
- Categories are sorted before notes at the same timestamp so category definitions exist before note-category mappings reference them
- `has_more: true` indicates the client should issue another pull for remaining changes
- Changes are marked as synced to this peer after delivery

---

#### Push Changes (Unified)
```
POST /api/v1/sync/push
```
Accepts a batch of SyncChanges from a peer and applies them locally. Each change is
checked for idempotency — duplicate change GUIDs and duplicate entity GUIDs on creates
are accepted silently (not rejected).

**Request Body:**
```json
{
  "peer_id": "spoke-laptop-001",
  "changes": [
    {
      "guid": "change-uuid",
      "entity_type": "note",
      "entity_guid": "note-uuid",
      "operation": 1,
      "fragment": {
        "bitmask": 224,
        "title": "New Note from Spoke",
        "body": "Content",
        "body_is_diff": false
      },
      "authored_at": "2025-01-15T10:00:00Z",
      "user": "user-guid"
    }
  ]
}
```

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "accepted": ["change-uuid"],
    "rejected": []
  }
}
```

**Rejected changes include a reason:**
```json
{
  "rejected": [
    {
      "guid": "change-uuid-2",
      "reason": "failed to apply sync note update: note not found"
    }
  ]
}
```

---

#### Get Entity Snapshot
```
GET /api/v1/sync/snapshot
```
Returns the full current state of a single entity as a SyncChange with operation=Create
and all fields populated. Useful for initial sync or conflict resolution.

**Query Parameters:**
- `entity_type` (string, required): `"note"` or `"category"`
- `entity_guid` (string, required): GUID of the entity to snapshot

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "guid": "snapshot-guid",
    "entity_type": "note",
    "entity_guid": "note-uuid",
    "operation": 1,
    "fragment": {
      "bitmask": 248,
      "title": "Full Note Title",
      "description": "Full description",
      "body": "Full body content",
      "body_is_diff": false,
      "is_private": false
    },
    "authored_at": "RFC3339 timestamp",
    "user": "user-guid",
    "created_at": "RFC3339 timestamp"
  }
}
```

**Errors:**
- `400`: Missing or invalid `entity_type`/`entity_guid`
- `404`: Entity not found

---

#### Get Sync Status
```
GET /api/v1/sync/status
```
Returns note/category counts and a content-based checksum. Peers compare checksums to
quickly detect whether their data sets have diverged without exchanging records.

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "note_count": 42,
    "category_count": 5,
    "checksum": "a3f2b8c9d1e4..."
  }
}
```

The checksum is SHA-256 of sorted note GUIDs + sorted category GUIDs (non-deleted entities only).

---

#### Health Check
```
GET /api/v1/health
```
Lightweight, unauthenticated endpoint. Returns 200 OK if the server is running.
Used by peers for connectivity checks and monitoring systems.

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "status": "ok"
  }
}
```

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

1. **users** — User accounts (id, guid, username, password_hash, email, ...)
2. **notes** — User notes with soft delete (id, guid, title, body, authored_at*, ...)
3. **categories** — Category definitions (id, guid, name, subcategories, ...)
4. **note_categories** — Many-to-many note-category junction (note_id, category_id, subcategories)
5. **note_fragments** — Delta storage for note changes (bitmask, changed fields, body_is_diff)
6. **note_changes** — Note change log (guid, note_guid, operation, note_fragment_id)
7. **note_change_sync_peers** — Per-peer note sync tracking (note_change_id, peer_id, synced_at)
8. **category_fragments** — Delta storage for category changes (bitmask, changed fields)
9. **category_changes** — Category change log (guid, category_guid, operation, category_fragment_id)
10. **category_change_sync_peers** — Per-peer category sync tracking (category_change_id, peer_id, synced_at)

*`authored_at` exists only in the disk database, not in the in-memory cache.

### Key Design Patterns

- **Disk + Cache**: DuckDB disk database is source of truth; in-memory cache for fast reads
- **Soft Deletes**: Notes use `deleted_at` timestamp (categories use hard delete)
- **User Scoping**: All note operations filter by `created_by` to enforce ownership
- **Delta Sync**: Only changed fields stored in fragments with bitmask indicators
- **Body Diffs**: Note body updates store unified diff patches (`body_is_diff=true`) to minimize transfer size
- **Per-Peer Tracking**: Each sync peer has a stable ID; `*_sync_peers` tables track which changes have been sent to which peer

---

## UI Development Notes

### Authentication Flow
1. User registers or logs in
2. Store JWT token (localStorage/sessionStorage)
3. Include token in all API requests via Authorization header
4. Refresh token before expiration (7 days)

### Page Layout

```
Toolbar  (new note, title, note count)
SearchBar  (full-width: text/ID input, category dropdown, subcategory chips, clear)
┌──────────────┬────────────────┬──────────────────┐
│ FilterPanel  │   NoteList     │  PreviewPanel    │
│ (categories  │   (scrollable) │  (preview/edit)  │
│  manage link,│                │                  │
│  privacy,    │                │                  │
│  date, sync) │                │                  │
└──────────────┴────────────────┴──────────────────┘
StatusBar
```

### Search Bar

The search bar sits between the toolbar and the three-pane content area. It supports
three combinable (AND logic) search dimensions:

1. **Text/ID search** — free-text input matches title, description, body; purely numeric input also matches note database ID
2. **Category dropdown** — filters notes to those assigned to the selected category (client-side via cached `noteCategoryMap`)
3. **Subcategory chips** — appear when the selected category has subcategories; toggling chips further narrows results (AND logic)

The category mapping is fetched once via `GET /api/v1/note-category-mappings` and cached
client-side. All filtering happens in the browser for instant response.

### Implemented UI Features
1. **Login/Register Forms** — Username, password, optional email
2. **Note List View** — Paginated list with category labels per note
3. **Note Editor** — Title, body (markdown), description, privacy toggle, multi-category with subcategory checkboxes
4. **Note Preview** — Right panel shows rendered markdown and category/subcategory rows (bold category name + subcategory chips)
5. **Category Management** — Create/edit/delete categories and subcategories via modal or inline during note editing
6. **Search & Filtering** — Full-width search bar (text/ID + category + subcategory); left panel filters for privacy, date range, sync status
7. **Sync Status** — Show last sync time, manual sync button
8. **Offline Support** — Queue changes when offline, sync when online

> **Note:** Tags have been removed from the UI. The `tags` column remains in the DB
> schema for backward compatibility but is no longer written to or displayed. The
> category/subcategory system fully replaces tags.

### Private Notes
- When `is_private: true`, note body is encrypted on disk
- Cache stores plaintext for fast reads
- Encryption requires `GONOTES_ENCRYPTION_KEY` environment variable
