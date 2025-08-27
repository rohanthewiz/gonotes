# GoNotes Web Platform - Implementation Guide

## Project Overview

A modern web-based note-taking platform built with Go, featuring a clean web interface powered by Monaco Editor for rich markdown editing. This is a reimplementation of the original GoNotes system with focus on web-first experience while maintaining simplicity and avoiding build complexities.

## Core Design Principles

1. **No Node.js/NPM Required** - Everything runs from Go binary + static files
2. **Server-Side Rendering** - Use `github.com/rohanthewiz/element` for HTML generation
3. **Progressive Enhancement** - HTMX + Alpine.js for interactivity without heavy JS
4. **Modern Editor** - Monaco Editor for powerful markdown editing
5. **Simple Deployment** - Single binary with embedded/static assets

## Technology Stack

### Backend
- **Language**: Go 1.23+
- **Web Framework**: `github.com/rohanthewiz/rweb`
- **HTML Generation**: `github.com/rohanthewiz/element`
- **Database**: DuckDB (embedded mode)
- **Error Handling**: `github.com/rohanthewiz/serr`
- **Logging**: `github.com/rohanthewiz/logger`
- **Markdown**: `github.com/rohanthewiz/go_markdown` (existing)

### Frontend
- **Editor**: Monaco Editor (self-hosted)
- **Interactivity**: Alpine.js v3 (no build step)
- **Dynamic Updates**: HTMX
- **Data Encoding**: MessagePack for safe data transfer
- **Styling**: Modern CSS with CSS Variables
- **Icons**: Inline SVGs or simple icon font

## Project Structure

```
go_notes_web/
├── main.go                    # Application entry point
├── go.mod                     # Go module definition
├── go.sum                     # Go dependencies
├── config/
│   └── config.go             # Configuration management
├── server/
│   ├── server.go             # RWeb server setup
│   ├── routes.go             # Route definitions
│   └── middleware.go         # Custom middleware
├── models/
│   ├── note.go               # Note model definition
│   ├── db.go                 # DuckDB connection manager
│   └── migrations.go         # Database schema
├── handlers/
│   ├── notes.go              # Note CRUD operations
│   ├── search.go             # Search functionality
│   ├── api.go                # API endpoints
│   └── sse.go                # Server-sent events
├── views/
│   ├── layout.go             # Base HTML layout
│   ├── components/
│   │   ├── header.go         # Header component
│   │   ├── sidebar.go        # Sidebar with tags
│   │   ├── note_card.go      # Note card for lists
│   │   └── editor.go         # Monaco editor wrapper
│   ├── pages/
│   │   ├── dashboard.go      # Main dashboard
│   │   ├── note_view.go      # Single note view
│   │   ├── note_edit.go      # Note editor page
│   │   └── search.go         # Search results page
│   └── partials/             # HTMX partial responses
│       ├── notes_list.go     # Notes list partial
│       └── search_results.go # Search results partial
├── static/
│   ├── css/
│   │   ├── main.css         # Main styles
│   │   ├── editor.css       # Editor-specific styles
│   │   └── themes.css       # Theme definitions
│   ├── js/
│   │   ├── app.js           # Main application JS
│   │   ├── editor.js        # Monaco configuration
│   │   └── search.js        # Search functionality
│   └── vendor/
│       ├── monaco/          # Monaco editor files
│       ├── alpine.min.js    # Alpine.js library
│       ├── htmx.min.js      # HTMX library
│       └── msgpack.min.js   # MessagePack for data transfer
└── data/
    ├── notes.db              # DuckDB database file
    └── backups/              # Database backups

```

## Database Schema (DuckDB)

```sql
-- Users table
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    guid VARCHAR(40) UNIQUE NOT NULL,  -- SHA1 hash, primary identifier
    email VARCHAR(255) UNIQUE,
    name VARCHAR(128),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMP,
    is_active BOOLEAN DEFAULT true
);

-- Notes table
CREATE TABLE notes (
    id INTEGER PRIMARY KEY,
    guid VARCHAR(40) UNIQUE NOT NULL,
    title VARCHAR(255) UNIQUE NOT NULL,
    description TEXT,
    body TEXT,  -- Encrypted on disk for private notes, plain for public
    tags TEXT,  -- JSON array of tags
    is_private BOOLEAN DEFAULT false,  -- Flag for encryption
    encryption_iv TEXT,  -- Initialization vector for AES encryption
    created_by VARCHAR(40),  -- User GUID who created the note
    updated_by VARCHAR(40),  -- User GUID who last updated the note
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    synced_at TIMESTAMP,  -- Last synchronization timestamp
    deleted_at TIMESTAMP NULL
);

-- Note ownership/access table (for sharing notes between users)
CREATE TABLE note_users (
    note_guid VARCHAR(40) NOT NULL,
    user_guid VARCHAR(40) NOT NULL,
    permission VARCHAR(20) DEFAULT 'read',  -- 'read', 'write', 'owner'
    shared_by VARCHAR(40),  -- User GUID who shared the note
    shared_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (note_guid, user_guid)
);

-- Search indexes
CREATE INDEX idx_notes_title ON notes(title);
CREATE INDEX idx_notes_tags ON notes(tags);
CREATE INDEX idx_notes_updated ON notes(updated_at DESC);
CREATE INDEX idx_notes_created_by ON notes(created_by);
CREATE INDEX idx_notes_updated_by ON notes(updated_by);
CREATE INDEX idx_note_users_user ON note_users(user_guid);

-- Sessions table (for auth)
CREATE TABLE sessions (
    id VARCHAR(40) PRIMARY KEY,
    user_guid VARCHAR(40) NOT NULL,
    data TEXT,
    expires_at TIMESTAMP NOT NULL
);

-- Sync tracking table (for distributed sync)
CREATE TABLE sync_log (
    id INTEGER PRIMARY KEY,
    peer_guid VARCHAR(40) NOT NULL,
    sync_type VARCHAR(20),  -- 'push', 'pull', 'bidirectional'
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    notes_synced INTEGER DEFAULT 0,
    status VARCHAR(20)  -- 'in_progress', 'completed', 'failed'
);
```

## Implementation Phases

### Phase 1: Core Infrastructure (Week 1)

#### 1.1 Project Setup
```go
// main.go
package main

import (
    "log"
    "go_notes_web/server"
    "go_notes_web/models"
)

func main() {
    // Initialize database
    if err := models.InitDB(); err != nil {
        log.Fatal(err)
    }
    
    // Start server
    srv := server.NewServer()
    log.Fatal(srv.Run(":8080"))
}
```

#### 1.2 RWeb Server Configuration
```go
// server/server.go
package server

import (
    "github.com/rohanthewiz/rweb"
    "go_notes_web/handlers"
)

func NewServer() *rweb.Server {
    s := rweb.NewServer(rweb.ServerOptions{
        Address: ":8080",
        Verbose: true,
    })
    
    // Middleware
    s.Use(rweb.RequestInfo)
    s.Use(CorsMiddleware)
    
    // Routes
    setupRoutes(s)
    
    // Static files
    s.Static("/static", "./static")
    
    // SSE for real-time updates
    eventsCh := make(chan any, 8)
    s.Get("/events", func(c rweb.Context) error {
        return s.SetupSSE(c, eventsCh)
    })
    
    return s
}
```

#### 1.3 DuckDB Dual-Database Architecture

The system uses a dual-database architecture with an in-memory DuckDB for fast reads and a disk-based DuckDB for persistence. All writes go to both databases, ensuring data durability while maintaining high query performance.

```go
// models/db.go
package models

import (
    "database/sql"
    "sync"
    _ "github.com/marcboeker/go-duckdb"
    "github.com/rohanthewiz/serr"
    "github.com/rohanthewiz/logger"
)

var (
    memDB  *sql.DB  // In-memory cache for fast reads
    diskDB *sql.DB  // Persistent storage
    dbMu   sync.RWMutex // Protect concurrent access during writes
)

// InitDB initializes both in-memory and disk-based databases
func InitDB() error {
    var err error
    
    // Initialize disk-based database for persistence
    diskDB, err = sql.Open("duckdb", "./data/notes.db")
    if err != nil {
        return serr.Wrap(err, "failed to open disk database")
    }
    
    // Initialize in-memory database for fast queries
    memDB, err = sql.Open("duckdb", ":memory:")
    if err != nil {
        return serr.Wrap(err, "failed to open memory database")
    }
    
    // Run migrations on both databases
    if err := migrateBoth(); err != nil {
        return serr.Wrap(err, "failed to migrate databases")
    }
    
    // Load existing data from disk to memory
    if err := syncDiskToMemory(); err != nil {
        return serr.Wrap(err, "failed to sync data to memory")
    }
    
    // Start background sync worker (optional periodic sync)
    go startSyncWorker()
    
    return nil
}

// migrateBoth runs migrations on both databases
func migrateBoth() error {
    // Run migration on disk DB
    if err := migrateDB(diskDB); err != nil {
        return serr.Wrap(err, "disk migration failed")
    }
    
    // Run migration on memory DB
    if err := migrateDB(memDB); err != nil {
        return serr.Wrap(err, "memory migration failed")
    }
    
    return nil
}

// syncDiskToMemory loads all data from disk into memory cache
func syncDiskToMemory() error {
    // Copy notes table
    query := `
        ATTACH './data/notes.db' AS disk_db;
        INSERT INTO notes SELECT * FROM disk_db.notes;
        INSERT INTO users SELECT * FROM disk_db.users;
        INSERT INTO note_users SELECT * FROM disk_db.note_users;
        INSERT INTO sessions SELECT * FROM disk_db.sessions;
        DETACH disk_db;
    `
    
    _, err := memDB.Exec(query)
    if err != nil {
        // If attach doesn't work, fall back to manual copy
        return manualSync()
    }
    
    logger.Info("Successfully synced disk data to memory cache")
    return nil
}

// manualSync performs manual table-by-table sync
func manualSync() error {
    tables := []string{"users", "notes", "note_users", "sessions"}
    
    for _, table := range tables {
        // Read from disk
        rows, err := diskDB.Query("SELECT * FROM " + table)
        if err != nil {
            logger.LogErr(err, "failed to read from disk", "table", table)
            continue
        }
        
        // Get column names
        cols, err := rows.Columns()
        if err != nil {
            rows.Close()
            continue
        }
        
        // Prepare insert statement for memory DB
        placeholders := ""
        for i := range cols {
            if i > 0 {
                placeholders += ","
            }
            placeholders += "?"
        }
        
        stmt, err := memDB.Prepare(
            "INSERT INTO " + table + " VALUES (" + placeholders + ")")
        if err != nil {
            rows.Close()
            continue
        }
        
        // Copy rows
        values := make([]interface{}, len(cols))
        valuePtrs := make([]interface{}, len(cols))
        for i := range values {
            valuePtrs[i] = &values[i]
        }
        
        for rows.Next() {
            if err := rows.Scan(valuePtrs...); err != nil {
                continue
            }
            if _, err := stmt.Exec(values...); err != nil {
                logger.LogErr(err, "failed to insert into memory", "table", table)
            }
        }
        
        stmt.Close()
        rows.Close()
    }
    
    return nil
}

// WriteThrough writes to both databases ensuring consistency
func WriteThrough(query string, args ...interface{}) error {
    dbMu.Lock()
    defer dbMu.Unlock()
    
    // Write to disk first for durability
    _, err := diskDB.Exec(query, args...)
    if err != nil {
        return serr.Wrap(err, "failed to write to disk")
    }
    
    // Then update memory cache
    _, err = memDB.Exec(query, args...)
    if err != nil {
        // Log error but don't fail - disk write succeeded
        logger.LogErr(err, "failed to update memory cache")
        // Mark cache as dirty for resync
        markCacheDirty()
    }
    
    return nil
}

// ReadFromCache performs fast reads from memory
func ReadFromCache(query string, args ...interface{}) (*sql.Rows, error) {
    dbMu.RLock()
    defer dbMu.RUnlock()
    
    rows, err := memDB.Query(query, args...)
    if err != nil {
        // Fallback to disk on cache miss
        logger.LogErr(err, "cache read failed, falling back to disk")
        return diskDB.Query(query, args...)
    }
    
    return rows, nil
}

// Transaction wrapper for dual-database writes
type DualTx struct {
    diskTx *sql.Tx
    memTx  *sql.Tx
}

func BeginDualTx() (*DualTx, error) {
    dbMu.Lock()
    
    diskTx, err := diskDB.Begin()
    if err != nil {
        dbMu.Unlock()
        return nil, serr.Wrap(err, "failed to begin disk transaction")
    }
    
    memTx, err := memDB.Begin()
    if err != nil {
        diskTx.Rollback()
        dbMu.Unlock()
        return nil, serr.Wrap(err, "failed to begin memory transaction")
    }
    
    return &DualTx{
        diskTx: diskTx,
        memTx:  memTx,
    }, nil
}

func (dt *DualTx) Exec(query string, args ...interface{}) error {
    // Execute on disk first
    if _, err := dt.diskTx.Exec(query, args...); err != nil {
        return err
    }
    
    // Then on memory
    if _, err := dt.memTx.Exec(query, args...); err != nil {
        // Log but don't fail
        logger.LogErr(err, "memory tx exec failed")
    }
    
    return nil
}

func (dt *DualTx) Commit() error {
    defer dbMu.Unlock()
    
    // Commit disk first
    if err := dt.diskTx.Commit(); err != nil {
        dt.memTx.Rollback()
        return serr.Wrap(err, "failed to commit disk transaction")
    }
    
    // Then memory
    if err := dt.memTx.Commit(); err != nil {
        logger.LogErr(err, "failed to commit memory transaction")
        markCacheDirty()
    }
    
    return nil
}

func (dt *DualTx) Rollback() error {
    defer dbMu.Unlock()
    
    dt.diskTx.Rollback()
    dt.memTx.Rollback()
    
    return nil
}

// Cache management
var (
    cacheDirty bool
    cacheMu    sync.Mutex
)

func markCacheDirty() {
    cacheMu.Lock()
    cacheDirty = true
    cacheMu.Unlock()
}

func isCacheDirty() bool {
    cacheMu.Lock()
    defer cacheMu.Unlock()
    return cacheDirty
}

// startSyncWorker periodically checks cache consistency
func startSyncWorker() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    
    for range ticker.C {
        if isCacheDirty() {
            logger.Info("Cache marked dirty, resyncing...")
            if err := resyncCache(); err != nil {
                logger.LogErr(err, "failed to resync cache")
            } else {
                cacheMu.Lock()
                cacheDirty = false
                cacheMu.Unlock()
            }
        }
    }
}

// resyncCache rebuilds the memory cache from disk
func resyncCache() error {
    dbMu.Lock()
    defer dbMu.Unlock()
    
    // Clear memory database
    tables := []string{"notes", "users", "note_users", "sessions"}
    for _, table := range tables {
        _, _ = memDB.Exec("DELETE FROM " + table)
    }
    
    // Reload from disk
    return manualSync()
}

// GetDB returns the appropriate database connection for the operation
func GetDB(forWrite bool) *sql.DB {
    if forWrite {
        // Writes always go through WriteThrough
        return nil // Force use of WriteThrough
    }
    return memDB // Reads use memory cache
}
```

### Phase 2: UI Components with Element (Week 1-2)

#### 2.1 Base Layout
```go
// views/layout.go
package views

import (
    "github.com/rohanthewiz/element"
)

func BaseLayout(title string, content element.Component) string {
    b := element.NewBuilder()
    
    b.Html().R(
        b.Head().R(
            b.Meta("charset", "utf-8"),
            b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1"),
            b.Title().T(title),
            
            // Styles
            b.Link("rel", "stylesheet", "href", "/static/css/main.css"),
            
            // Monaco Editor
            b.Link("rel", "stylesheet", "href", "/static/vendor/monaco/editor/editor.main.css"),
            
            // Scripts
            b.Script("src", "/static/vendor/alpine.min.js", "defer").R(),
            b.Script("src", "/static/vendor/htmx.min.js").R(),
            b.Script("src", "/static/vendor/msgpack.min.js").R(),
        ),
        b.Body("x-data", "{ darkMode: false }").R(
            b.Div("class", "container").R(
                element.RenderComponents(b, HeaderComponent{}),
                b.Main().R(
                    element.RenderComponents(b, content),
                ),
            ),
        ),
    )
    
    return b.String()
}
```

#### 2.2 Monaco Editor Integration
```go
// views/components/editor.go
package components

import (
    "encoding/base64"
    "github.com/rohanthewiz/element"
    "github.com/vmihailenco/msgpack/v5"
)

type EditorComponent struct {
    NoteID   string
    Content  string
    ReadOnly bool
}

func (e EditorComponent) Render(b *element.Builder) (x any) {
    // Encode content for safe transfer to JS
    mpkCode, _ := msgpack.Marshal(map[string]string{"code": e.Content})
    b64code := base64.StdEncoding.EncodeToString(mpkCode)
    
    b.Div("class", "editor-container", "x-data", "editorComponent()").R(
        b.Div("id", "monaco-editor", "class", "editor").R(),
        b.Input("type", "hidden", "id", "editor-content", "value", b64code),
        
        // Editor initialization script
        b.Script().T(`
            function editorComponent() {
                return {
                    editor: null,
                    init() {
                        require.config({ paths: { vs: '/static/vendor/monaco/vs' } });
                        require(['vs/editor/editor.main'], () => {
                            const bin = atob(document.getElementById('editor-content').value);
                            const codeObj = msgpack.decode(Uint8Array.from(bin, c => c.charCodeAt(0)));
                            
                            this.editor = monaco.editor.create(document.getElementById('monaco-editor'), {
                                value: codeObj.code,
                                language: 'markdown',
                                theme: this.darkMode ? 'vs-dark' : 'vs',
                                automaticLayout: true,
                                minimap: { enabled: false },
                                fontSize: 14,
                                wordWrap: 'on',
                                readOnly: ` + strconv.FormatBool(e.ReadOnly) + `
                            });
                            
                            // Auto-save with debouncing
                            let saveTimeout;
                            this.editor.onDidChangeModelContent(() => {
                                clearTimeout(saveTimeout);
                                saveTimeout = setTimeout(() => this.saveContent(), 2000);
                            });
                        });
                    },
                    saveContent() {
                        const content = this.editor.getValue();
                        // Send via HTMX or fetch
                        htmx.ajax('POST', '/api/notes/` + e.NoteID + `/save', {
                            values: { content: content }
                        });
                    }
                }
            }
        `),
    )
    return
}
```

### Phase 3: Core Functionality (Week 2)

#### 3.1 Note Model Definition
```go
// models/note.go
package models

import (
    "time"
    "database/sql"
    "github.com/rohanthewiz/serr"
)

type Note struct {
    ID          int64          `db:"id"`
    GUID        string         `db:"guid"`
    Title       string         `db:"title"`
    Description sql.NullString `db:"description"`
    Body        sql.NullString `db:"body"`
    Tags        string         `db:"tags"` // JSON array
    CreatedBy   sql.NullString `db:"created_by"`   // User GUID
    UpdatedBy   sql.NullString `db:"updated_by"`   // User GUID  
    CreatedAt   time.Time      `db:"created_at"`
    UpdatedAt   time.Time      `db:"updated_at"`
    SyncedAt    sql.NullTime   `db:"synced_at"`
    DeletedAt   sql.NullTime   `db:"deleted_at"`
}

type User struct {
    ID          int64          `db:"id"`
    GUID        string         `db:"guid"`
    Email       sql.NullString `db:"email"`
    Name        sql.NullString `db:"name"`
    CreatedAt   time.Time      `db:"created_at"`
    UpdatedAt   time.Time      `db:"updated_at"`
    LastLoginAt sql.NullTime   `db:"last_login_at"`
    IsActive    bool           `db:"is_active"`
}

type NoteUser struct {
    NoteGUID  string         `db:"note_guid"`
    UserGUID  string         `db:"user_guid"`
    Permission string        `db:"permission"` // read, write, owner
    SharedBy   sql.NullString `db:"shared_by"`
    SharedAt   time.Time      `db:"shared_at"`
}

// Save creates a new note using dual-database write-through
func (n *Note) Save(userGUID string) error {
    n.GUID = generateGUID()
    n.CreatedBy = sql.NullString{String: userGUID, Valid: true}
    n.UpdatedBy = sql.NullString{String: userGUID, Valid: true}
    n.CreatedAt = time.Now()
    n.UpdatedAt = time.Now()
    
    // Use transaction for atomicity
    tx, err := BeginDualTx()
    if err != nil {
        return serr.Wrap(err, "failed to begin transaction")
    }
    defer tx.Rollback()
    
    // Insert note
    query := `
        INSERT INTO notes (guid, title, description, body, tags, 
                          created_by, updated_by, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `
    
    if err := tx.Exec(query, n.GUID, n.Title, n.Description, n.Body, n.Tags,
                      n.CreatedBy, n.UpdatedBy, n.CreatedAt, n.UpdatedAt); err != nil {
        return serr.Wrap(err, "failed to save note")
    }
    
    // Create ownership record
    if err := tx.Exec(`
        INSERT INTO note_users (note_guid, user_guid, permission)
        VALUES (?, ?, 'owner')
    `, n.GUID, userGUID); err != nil {
        return serr.Wrap(err, "failed to create ownership record")
    }
    
    return tx.Commit()
}

// Update modifies an existing note using dual-database write-through
func (n *Note) Update(userGUID string) error {
    n.UpdatedBy = sql.NullString{String: userGUID, Valid: true}
    n.UpdatedAt = time.Now()
    
    query := `
        UPDATE notes 
        SET title = ?, description = ?, body = ?, tags = ?,
            updated_by = ?, updated_at = ?
        WHERE guid = ?
    `
    
    // Use WriteThrough for dual-database consistency
    err := WriteThrough(query, n.Title, n.Description, n.Body, n.Tags,
                       n.UpdatedBy, n.UpdatedAt, n.GUID)
    return serr.Wrap(err, "failed to update note")
}

// GetNotesForUser retrieves notes from the in-memory cache
func GetNotesForUser(userGUID string) ([]Note, error) {
    query := `
        SELECT n.id, n.guid, n.title, n.description, n.body, n.tags,
               n.created_by, n.updated_by, n.created_at, n.updated_at,
               n.synced_at, n.deleted_at
        FROM notes n
        JOIN note_users nu ON n.guid = nu.note_guid
        WHERE nu.user_guid = ? AND n.deleted_at IS NULL
        ORDER BY n.updated_at DESC
    `
    
    rows, err := ReadFromCache(query, userGUID)
    if err != nil {
        return nil, serr.Wrap(err, "failed to get notes")
    }
    defer rows.Close()
    
    var notes []Note
    for rows.Next() {
        var note Note
        if err := rows.Scan(&note.ID, &note.GUID, &note.Title, &note.Description,
                           &note.Body, &note.Tags, &note.CreatedBy, &note.UpdatedBy,
                           &note.CreatedAt, &note.UpdatedAt, &note.SyncedAt, &note.DeletedAt); err != nil {
            return nil, serr.Wrap(err, "failed to scan note")
        }
        notes = append(notes, note)
    }
    
    return notes, nil
}

// SearchByTitle performs fast title search using in-memory cache
func SearchByTitle(query string) ([]Note, error) {
    searchQuery := `
        SELECT id, guid, title, description, body, tags,
               created_by, updated_by, created_at, updated_at
        FROM notes
        WHERE title LIKE ? AND deleted_at IS NULL
        ORDER BY updated_at DESC
    `
    
    rows, err := ReadFromCache(searchQuery, "%"+query+"%")
    if err != nil {
        return nil, serr.Wrap(err, "failed to search notes by title")
    }
    defer rows.Close()
    
    return scanNotes(rows)
}

// Helper function to scan notes from rows
func scanNotes(rows *sql.Rows) ([]Note, error) {
    var notes []Note
    for rows.Next() {
        var note Note
        if err := rows.Scan(&note.ID, &note.GUID, &note.Title, &note.Description,
                           &note.Body, &note.Tags, &note.CreatedBy, &note.UpdatedBy,
                           &note.CreatedAt, &note.UpdatedAt); err != nil {
            continue
        }
        notes = append(notes, note)
    }
    return notes, nil
}
```

#### 3.2 Note CRUD Handlers
```go
// handlers/notes.go
package handlers

import (
    "github.com/rohanthewiz/rweb"
    "go_notes_web/models"
    "go_notes_web/views/pages"
)

func GetNotes(c rweb.Context) error {
    // Get user from session (to be implemented)
    userGUID := getUserGUID(c)
    
    notes, err := models.GetNotesForUser(userGUID)
    if err != nil {
        return err
    }
    
    html := pages.Dashboard(notes)
    return c.WriteHTML(html)
}

func CreateNote(c rweb.Context) error {
    userGUID := getUserGUID(c)
    
    title := c.Request().FormValue("title")
    body := c.Request().FormValue("body")
    tags := c.Request().FormValue("tags")
    
    note := &models.Note{
        Title: title,
        Body:  sql.NullString{String: body, Valid: body != ""},
        Tags:  tags,
    }
    
    if err := note.Save(userGUID); err != nil {
        return err
    }
    
    // Redirect to edit page
    return c.Redirect("/notes/" + note.GUID + "/edit")
}

func UpdateNote(c rweb.Context) error {
    userGUID := getUserGUID(c)
    guid := c.Request().Param("guid")
    
    // Check user has write permission
    hasPermission, err := models.UserCanEditNote(userGUID, guid)
    if err != nil {
        return err
    }
    if !hasPermission {
        return c.WriteStatus(403) // Forbidden
    }
    
    note, err := models.GetNoteByGUID(guid)
    if err != nil {
        return err
    }
    
    note.Title = c.Request().FormValue("title")
    note.Body = sql.NullString{
        String: c.Request().FormValue("body"),
        Valid: true,
    }
    note.Tags = c.Request().FormValue("tags")
    
    if err := note.Update(userGUID); err != nil {
        return err
    }
    
    // Return HTMX partial if requested
    if c.Request().Header.Get("HX-Request") == "true" {
        return c.WriteHTML(views.NoteCardPartial(note))
    }
    
    return c.Redirect("/notes/" + guid)
}

// Helper to get user GUID from session
func getUserGUID(c rweb.Context) string {
    // TODO: Implement session management
    // For now, return a default user GUID for development
    return "default-user-guid"
}
```

#### 3.2 Search Implementation
```go
// handlers/search.go
package handlers

import (
    "github.com/rohanthewiz/rweb"
    "go_notes_web/models"
)

func SearchNotes(c rweb.Context) error {
    query := c.Request().QueryParam("q")
    searchType := c.Request().QueryParam("type") // title, tag, body, all
    
    var notes []models.Note
    var err error
    
    switch searchType {
    case "title":
        notes, err = models.SearchByTitle(query)
    case "tag":
        notes, err = models.SearchByTag(query)
    case "body":
        notes, err = models.SearchByBody(query)
    default:
        notes, err = models.SearchAll(query)
    }
    
    if err != nil {
        return err
    }
    
    // Return results as HTMX partial
    return c.WriteHTML(views.SearchResultsPartial(notes))
}
```

### Phase 4: Routes Configuration

```go
// server/routes.go
package server

import (
    "github.com/rohanthewiz/rweb"
    "go_notes_web/handlers"
)

func setupRoutes(s *rweb.Server) {
    // Pages
    s.Get("/", handlers.GetNotes)
    s.Get("/notes/new", handlers.NewNoteForm)
    s.Get("/notes/:guid", handlers.ViewNote)
    s.Get("/notes/:guid/edit", handlers.EditNote)
    
    // API endpoints
    s.Post("/api/notes", handlers.CreateNote)
    s.Put("/api/notes/:guid", handlers.UpdateNote)
    s.Delete("/api/notes/:guid", handlers.DeleteNote)
    s.Post("/api/notes/:guid/save", handlers.AutoSaveNote)
    
    // Search
    s.Get("/api/search", handlers.SearchNotes)
    
    // Import/Export
    s.Get("/api/export", handlers.ExportNotes)
    s.Post("/api/import", handlers.ImportNotes)
}
```

## Key Implementation Details

### 1. Alpine.js Integration
```javascript
// static/js/app.js
document.addEventListener('alpine:init', () => {
    Alpine.data('notesApp', () => ({
        darkMode: localStorage.getItem('darkMode') === 'true',
        searchQuery: '',
        selectedTags: [],
        
        toggleDarkMode() {
            this.darkMode = !this.darkMode;
            localStorage.setItem('darkMode', this.darkMode);
            // Update Monaco theme
            if (window.monacoEditor) {
                monaco.editor.setTheme(this.darkMode ? 'vs-dark' : 'vs');
            }
        },
        
        filterByTag(tag) {
            if (this.selectedTags.includes(tag)) {
                this.selectedTags = this.selectedTags.filter(t => t !== tag);
            } else {
                this.selectedTags.push(tag);
            }
            this.updateResults();
        },
        
        updateResults() {
            // Trigger HTMX request with filters
            htmx.ajax('GET', `/api/search?q=${this.searchQuery}&tags=${this.selectedTags.join(',')}`, '#results');
        }
    }));
});
```

### 2. HTMX Configuration
```html
<!-- Example of HTMX usage in templates -->
<div hx-get="/api/notes" 
     hx-trigger="load, every 30s" 
     hx-target="#notes-list"
     hx-indicator="#loading">
    <!-- Notes list will be loaded here -->
</div>

<input type="search" 
       name="q" 
       hx-get="/api/search" 
       hx-trigger="input changed delay:500ms" 
       hx-target="#search-results"
       placeholder="Search notes...">
```

### 3. MessagePack for Safe Data Transfer

MessagePack is used throughout the application to safely encode/decode data between the Go backend and JavaScript frontend, particularly for handling note content with complex formatting, quotes, and special characters.

#### Backend MessagePack Encoding
```go
// handlers/api.go
package handlers

import (
    "encoding/base64"
    "github.com/rohanthewiz/rweb"
    "github.com/vmihailenco/msgpack/v5"
    "github.com/rohanthewiz/serr"
)

type NoteTransfer struct {
    Title       string `msgpack:"title"`
    Body        string `msgpack:"body"`
    Tags        []string `msgpack:"tags"`
    Description string `msgpack:"description"`
}

// AutoSaveNote handles auto-save requests with MessagePack encoding
func AutoSaveNote(c rweb.Context) error {
    guid := c.Request().Param("guid")
    
    // Decode MessagePack data from request
    b64Data := c.Request().FormValue("data")
    binData, err := base64.StdEncoding.DecodeString(b64Data)
    if err != nil {
        return serr.Wrap(err, "failed to decode base64 data")
    }
    
    var noteData NoteTransfer
    if err := msgpack.Unmarshal(binData, &noteData); err != nil {
        return serr.Wrap(err, "failed to unmarshal MessagePack data")
    }
    
    // Update note in database
    note, err := models.GetNoteByGUID(guid)
    if err != nil {
        return err
    }
    
    note.Body = sql.NullString{String: noteData.Body, Valid: true}
    note.Title = noteData.Title
    
    userGUID := getUserGUID(c)
    if err := note.Update(userGUID); err != nil {
        return err
    }
    
    return c.WriteJSON(map[string]bool{"success": true})
}

// SendNoteData sends note data encoded with MessagePack
func SendNoteData(c rweb.Context, note *models.Note) error {
    noteTransfer := NoteTransfer{
        Title:       note.Title,
        Body:        note.Body.String,
        Tags:        parseTags(note.Tags),
        Description: note.Description.String,
    }
    
    mpkData, err := msgpack.Marshal(noteTransfer)
    if err != nil {
        return serr.Wrap(err, "failed to marshal note data")
    }
    
    b64Data := base64.StdEncoding.EncodeToString(mpkData)
    
    return c.WriteJSON(map[string]string{
        "data": b64Data,
        "guid": note.GUID,
    })
}
```

#### Frontend MessagePack Handling
```javascript
// static/js/editor.js

// Initialize editor with MessagePack-encoded content
function initializeEditor(encodedData) {
    // Decode base64 to binary
    const bin = atob(encodedData);
    const uint8Array = Uint8Array.from(bin, c => c.charCodeAt(0));
    
    // Decode MessagePack
    const noteData = msgpack.decode(uint8Array);
    
    // Create Monaco editor with decoded content
    const editor = monaco.editor.create(document.getElementById('editor'), {
        value: noteData.body || '',
        language: 'markdown',
        theme: 'vs-dark',
        automaticLayout: true,
        wordWrap: 'on'
    });
    
    return editor;
}

// Save content with MessagePack encoding
function saveContent(editor, noteGuid) {
    const noteData = {
        body: editor.getValue(),
        title: document.querySelector('#title').value,
        tags: getSelectedTags()
    };
    
    // Encode to MessagePack
    const packed = msgpack.encode(noteData);
    
    // Convert to base64 for safe transport
    const b64 = btoa(String.fromCharCode.apply(null, packed));
    
    // Send to server
    fetch(`/api/notes/${noteGuid}/save`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
        },
        body: `data=${encodeURIComponent(b64)}`
    })
    .then(response => response.json())
    .then(data => {
        if (data.success) {
            showSaveIndicator();
        }
    });
}

// Handle incoming SSE messages with MessagePack
function handleSSEMessage(event) {
    const message = JSON.parse(event.data);
    
    if (message.type === 'note-update' && message.data) {
        // Decode MessagePack data
        const bin = atob(message.data);
        const uint8Array = Uint8Array.from(bin, c => c.charCodeAt(0));
        const noteData = msgpack.decode(uint8Array);
        
        // Update UI with decoded data
        updateNoteInList(noteData);
    }
}
```

#### Integration with Element Components
```go
// views/components/editor.go
package components

import (
    "encoding/base64"
    "strconv"
    "github.com/rohanthewiz/element"
    "github.com/vmihailenco/msgpack/v5"
)

type EditorComponent struct {
    NoteID   string
    Content  string
    Title    string
    Tags     []string
    ReadOnly bool
}

func (e EditorComponent) Render(b *element.Builder) (x any) {
    // Prepare note data for MessagePack encoding
    noteData := map[string]interface{}{
        "body":  e.Content,
        "title": e.Title,
        "tags":  e.Tags,
    }
    
    // Encode with MessagePack for safe transfer
    mpkData, err := msgpack.Marshal(noteData)
    if err != nil {
        // Handle error - log and use empty data
        mpkData = []byte{}
    }
    
    b64Data := base64.StdEncoding.EncodeToString(mpkData)
    
    b.Div("class", "editor-wrapper").R(
        // Hidden input with encoded data
        b.Input("type", "hidden", "id", "note-data", "value", b64Data),
        
        // Title input
        b.Input("type", "text", "id", "title", "class", "note-title",
                "placeholder", "Note Title"),
        
        // Tags selector
        b.Div("id", "tags-container", "class", "tags").R(),
        
        // Monaco editor container
        b.Div("id", "editor", "class", "editor-container").R(),
        
        // Initialize editor with MessagePack data
        b.Script().T(`
            document.addEventListener('DOMContentLoaded', function() {
                // Get encoded data
                const encodedData = document.getElementById('note-data').value;
                
                // Initialize editor with MessagePack-decoded content
                const editor = initializeEditor(encodedData);
                
                // Setup auto-save with MessagePack encoding
                let saveTimeout;
                editor.onDidChangeModelContent(() => {
                    clearTimeout(saveTimeout);
                    saveTimeout = setTimeout(() => {
                        saveContent(editor, '` + e.NoteID + `');
                    }, 2000);
                });
            });
        `),
    )
    
    return
}
```

### 4. SSE for Real-time Updates
```go
// handlers/sse.go
package handlers

func BroadcastNoteUpdate(note *models.Note) {
    event := map[string]interface{}{
        "type": "note-updated",
        "data": note,
    }
    eventsCh <- event
}

// Client-side listener
// static/js/app.js
const evtSource = new EventSource("/events");
evtSource.onmessage = (event) => {
    const data = JSON.parse(event.data);
    if (data.type === 'note-updated') {
        // Update UI via Alpine.js or HTMX
        htmx.trigger("#notes-list", "refresh");
    }
};
```

## Migration from Original GoNotes

### Import Existing SQLite Data
```go
// tools/migrate.go
package main

import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    _ "github.com/marcboeker/go-duckdb"
)

func migrateFromSQLite(sqlitePath, duckdbPath string) error {
    // Open SQLite
    oldDB, err := sql.Open("sqlite3", sqlitePath)
    if err != nil {
        return err
    }
    defer oldDB.Close()
    
    // Open DuckDB
    newDB, err := sql.Open("duckdb", duckdbPath)
    if err != nil {
        return err
    }
    defer newDB.Close()
    
    // Read notes from SQLite
    rows, err := oldDB.Query("SELECT id, guid, title, description, body, tag, created_at, updated_at FROM notes")
    if err != nil {
        return err
    }
    defer rows.Close()
    
    // Insert into DuckDB
    stmt, err := newDB.Prepare(`
        INSERT INTO notes (id, guid, title, description, body, tags, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `)
    if err != nil {
        return err
    }
    defer stmt.Close()
    
    for rows.Next() {
        var note struct {
            ID          int64
            GUID        string
            Title       string
            Description sql.NullString
            Body        sql.NullString
            Tag         sql.NullString
            CreatedAt   time.Time
            UpdatedAt   time.Time
        }
        
        if err := rows.Scan(&note.ID, &note.GUID, &note.Title, &note.Description, 
                           &note.Body, &note.Tag, &note.CreatedAt, &note.UpdatedAt); err != nil {
            continue
        }
        
        // Convert tags to JSON array
        tags := "[]"
        if note.Tag.Valid && note.Tag.String != "" {
            tags = `["` + note.Tag.String + `"]`
        }
        
        _, err = stmt.Exec(note.ID, note.GUID, note.Title, note.Description.String,
                          note.Body.String, tags, note.CreatedAt, note.UpdatedAt)
        if err != nil {
            log.Printf("Failed to migrate note %s: %v", note.Title, err)
        }
    }
    
    return nil
}
```

## CSS Architecture

```css
/* static/css/main.css */
:root {
    --bg-primary: #ffffff;
    --bg-secondary: #f5f5f5;
    --text-primary: #333333;
    --text-secondary: #666666;
    --accent: #4a90e2;
    --border: #e0e0e0;
    --shadow: 0 2px 4px rgba(0,0,0,0.1);
}

[x-cloak] { display: none !important; }

body[data-theme="dark"] {
    --bg-primary: #1e1e1e;
    --bg-secondary: #2d2d2d;
    --text-primary: #e0e0e0;
    --text-secondary: #a0a0a0;
    --accent: #569cd6;
    --border: #3e3e3e;
    --shadow: 0 2px 4px rgba(0,0,0,0.3);
}

.container {
    max-width: 1200px;
    margin: 0 auto;
    padding: 1rem;
}

.editor-container {
    height: 600px;
    border: 1px solid var(--border);
    border-radius: 4px;
    overflow: hidden;
}

/* Grid layout for dashboard */
.notes-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 1rem;
}

.note-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1rem;
    transition: transform 0.2s, box-shadow 0.2s;
}

.note-card:hover {
    transform: translateY(-2px);
    box-shadow: var(--shadow);
}
```

## Deployment

### Docker Support
```dockerfile
# Dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o gonotes-web .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/gonotes-web .
COPY --from=builder /app/static ./static
EXPOSE 8080
CMD ["./gonotes-web"]
```

### Systemd Service
```ini
# /etc/systemd/system/gonotes-web.service
[Unit]
Description=GoNotes Web Platform
After=network.target

[Service]
Type=simple
User=gonotes
WorkingDirectory=/opt/gonotes-web
ExecStart=/opt/gonotes-web/gonotes-web
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

## Performance Optimizations

1. **Static Asset Caching**: Set appropriate cache headers
2. **Database Indexing**: Proper indexes on search fields
3. **Lazy Loading**: Load notes in batches
4. **Debounced Search**: Prevent excessive queries
5. **SSE Throttling**: Rate limit real-time updates

## Note Encryption for Privacy

### Overview
Private notes are encrypted at rest (on disk) using AES-256-GCM encryption, but remain unencrypted in the in-memory database for fast access. This provides security for sensitive data while maintaining high performance.

### Encryption Architecture

```go
// models/encryption.go
package models

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "io"
    "github.com/rohanthewiz/serr"
    "golang.org/x/crypto/pbkdf2"
)

type NoteEncryption struct {
    masterKey []byte
    cipher    cipher.AEAD
}

var noteEncryption *NoteEncryption

// InitEncryption sets up the encryption system with user's master password
func InitEncryption(masterPassword string) error {
    // Derive encryption key from master password using PBKDF2
    salt := getSalt() // Retrieved from secure storage or config
    key := pbkdf2.Key([]byte(masterPassword), salt, 100000, 32, sha256.New)
    
    block, err := aes.NewCipher(key)
    if err != nil {
        return serr.Wrap(err, "failed to create cipher")
    }
    
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return serr.Wrap(err, "failed to create GCM")
    }
    
    noteEncryption = &NoteEncryption{
        masterKey: key,
        cipher:    gcm,
    }
    
    return nil
}

// EncryptNote encrypts a note's sensitive fields for disk storage
func (ne *NoteEncryption) EncryptNote(note *Note) (*EncryptedNote, error) {
    encrypted := &EncryptedNote{
        ID:          note.ID,
        GUID:        note.GUID,
        Title:       note.Title, // Title remains unencrypted for searching
        Tags:        note.Tags,   // Tags remain unencrypted for filtering
        IsPrivate:   note.IsPrivate,
        CreatedBy:   note.CreatedBy,
        UpdatedBy:   note.UpdatedBy,
        CreatedAt:   note.CreatedAt,
        UpdatedAt:   note.UpdatedAt,
    }
    
    if note.IsPrivate && note.Body.Valid {
        // Generate nonce for this encryption
        nonce := make([]byte, ne.cipher.NonceSize())
        if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
            return nil, serr.Wrap(err, "failed to generate nonce")
        }
        
        // Encrypt body
        ciphertext := ne.cipher.Seal(nil, nonce, []byte(note.Body.String), nil)
        
        // Store encrypted body and nonce
        encrypted.Body = sql.NullString{
            String: base64.StdEncoding.EncodeToString(ciphertext),
            Valid:  true,
        }
        encrypted.EncryptionIV = sql.NullString{
            String: base64.StdEncoding.EncodeToString(nonce),
            Valid:  true,
        }
        
        // Encrypt description if present
        if note.Description.Valid {
            descCiphertext := ne.cipher.Seal(nil, nonce, []byte(note.Description.String), nil)
            encrypted.Description = sql.NullString{
                String: base64.StdEncoding.EncodeToString(descCiphertext),
                Valid:  true,
            }
        }
    } else {
        // Not private - store as plain text
        encrypted.Body = note.Body
        encrypted.Description = note.Description
    }
    
    return encrypted, nil
}

// DecryptNote decrypts a note's sensitive fields for memory storage
func (ne *NoteEncryption) DecryptNote(encrypted *EncryptedNote) (*Note, error) {
    note := &Note{
        ID:          encrypted.ID,
        GUID:        encrypted.GUID,
        Title:       encrypted.Title,
        Tags:        encrypted.Tags,
        IsPrivate:   encrypted.IsPrivate,
        CreatedBy:   encrypted.CreatedBy,
        UpdatedBy:   encrypted.UpdatedBy,
        CreatedAt:   encrypted.CreatedAt,
        UpdatedAt:   encrypted.UpdatedAt,
    }
    
    if encrypted.IsPrivate && encrypted.Body.Valid && encrypted.EncryptionIV.Valid {
        // Decode nonce
        nonce, err := base64.StdEncoding.DecodeString(encrypted.EncryptionIV.String)
        if err != nil {
            return nil, serr.Wrap(err, "failed to decode nonce")
        }
        
        // Decode and decrypt body
        ciphertext, err := base64.StdEncoding.DecodeString(encrypted.Body.String)
        if err != nil {
            return nil, serr.Wrap(err, "failed to decode ciphertext")
        }
        
        plaintext, err := ne.cipher.Open(nil, nonce, ciphertext, nil)
        if err != nil {
            return nil, serr.Wrap(err, "failed to decrypt body")
        }
        
        note.Body = sql.NullString{
            String: string(plaintext),
            Valid:  true,
        }
        
        // Decrypt description if present
        if encrypted.Description.Valid {
            descCiphertext, err := base64.StdEncoding.DecodeString(encrypted.Description.String)
            if err == nil {
                descPlaintext, err := ne.cipher.Open(nil, nonce, descCiphertext, nil)
                if err == nil {
                    note.Description = sql.NullString{
                        String: string(descPlaintext),
                        Valid:  true,
                    }
                }
            }
        }
    } else {
        // Not encrypted - use as is
        note.Body = encrypted.Body
        note.Description = encrypted.Description
    }
    
    return note, nil
}
```

### Modified Dual-Database Architecture

```go
// models/db_encrypted.go
package models

// syncDiskToMemory loads and decrypts data from disk into memory cache
func syncDiskToMemoryWithDecryption() error {
    // Read encrypted notes from disk
    rows, err := diskDB.Query(`
        SELECT id, guid, title, description, body, tags, is_private, 
               encryption_iv, created_by, updated_by, created_at, updated_at
        FROM notes
    `)
    if err != nil {
        return serr.Wrap(err, "failed to read notes from disk")
    }
    defer rows.Close()
    
    // Prepare insert for memory DB
    stmt, err := memDB.Prepare(`
        INSERT INTO notes (id, guid, title, description, body, tags, is_private,
                          created_by, updated_by, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `)
    if err != nil {
        return serr.Wrap(err, "failed to prepare insert")
    }
    defer stmt.Close()
    
    for rows.Next() {
        var encrypted EncryptedNote
        err := rows.Scan(
            &encrypted.ID, &encrypted.GUID, &encrypted.Title,
            &encrypted.Description, &encrypted.Body, &encrypted.Tags,
            &encrypted.IsPrivate, &encrypted.EncryptionIV,
            &encrypted.CreatedBy, &encrypted.UpdatedBy,
            &encrypted.CreatedAt, &encrypted.UpdatedAt,
        )
        if err != nil {
            logger.LogErr(err, "failed to scan encrypted note")
            continue
        }
        
        // Decrypt note if private
        var note *Note
        if encrypted.IsPrivate && noteEncryption != nil {
            note, err = noteEncryption.DecryptNote(&encrypted)
            if err != nil {
                logger.LogErr(err, "failed to decrypt note", "guid", encrypted.GUID)
                continue
            }
        } else {
            // Not encrypted, convert directly
            note = &Note{
                ID:          encrypted.ID,
                GUID:        encrypted.GUID,
                Title:       encrypted.Title,
                Description: encrypted.Description,
                Body:        encrypted.Body,
                Tags:        encrypted.Tags,
                IsPrivate:   encrypted.IsPrivate,
                CreatedBy:   encrypted.CreatedBy,
                UpdatedBy:   encrypted.UpdatedBy,
                CreatedAt:   encrypted.CreatedAt,
                UpdatedAt:   encrypted.UpdatedAt,
            }
        }
        
        // Insert decrypted note into memory DB
        _, err = stmt.Exec(
            note.ID, note.GUID, note.Title, note.Description,
            note.Body, note.Tags, note.IsPrivate,
            note.CreatedBy, note.UpdatedBy,
            note.CreatedAt, note.UpdatedAt,
        )
        if err != nil {
            logger.LogErr(err, "failed to insert note into memory")
        }
    }
    
    logger.Info("Successfully synced and decrypted notes to memory cache")
    return nil
}

// WriteEncrypted writes to disk with encryption, memory without
func WriteEncrypted(note *Note, query string, args ...interface{}) error {
    dbMu.Lock()
    defer dbMu.Unlock()
    
    // Prepare data for disk (encrypted if private)
    var diskArgs []interface{}
    if note.IsPrivate && noteEncryption != nil {
        encrypted, err := noteEncryption.EncryptNote(note)
        if err != nil {
            return serr.Wrap(err, "failed to encrypt note")
        }
        
        // Build args with encrypted data
        diskArgs = buildArgsWithEncryption(encrypted, args)
    } else {
        diskArgs = args
    }
    
    // Write encrypted to disk
    _, err := diskDB.Exec(query, diskArgs...)
    if err != nil {
        return serr.Wrap(err, "failed to write to disk")
    }
    
    // Write unencrypted to memory
    _, err = memDB.Exec(query, args...)
    if err != nil {
        logger.LogErr(err, "failed to update memory cache")
        markCacheDirty()
    }
    
    return nil
}
```

### Key Management

```go
// models/key_management.go
package models

import (
    "crypto/rand"
    "encoding/hex"
    "os"
    "path/filepath"
)

type KeyManager struct {
    keyDerivationSalt []byte
    keyCheckValue     string // To verify correct password
}

var keyManager *KeyManager

// InitKeyManager initializes the key management system
func InitKeyManager() error {
    keyManager = &KeyManager{}
    
    // Load or generate salt
    saltPath := filepath.Join(getDataDir(), ".salt")
    if salt, err := os.ReadFile(saltPath); err == nil {
        keyManager.keyDerivationSalt = salt
    } else {
        // Generate new salt
        keyManager.keyDerivationSalt = make([]byte, 32)
        if _, err := rand.Read(keyManager.keyDerivationSalt); err != nil {
            return serr.Wrap(err, "failed to generate salt")
        }
        
        // Save salt
        if err := os.WriteFile(saltPath, keyManager.keyDerivationSalt, 0600); err != nil {
            return serr.Wrap(err, "failed to save salt")
        }
    }
    
    // Load key check value
    kcvPath := filepath.Join(getDataDir(), ".kcv")
    if kcv, err := os.ReadFile(kcvPath); err == nil {
        keyManager.keyCheckValue = string(kcv)
    }
    
    return nil
}

// SetMasterPassword sets and validates the master password
func (km *KeyManager) SetMasterPassword(password string) error {
    // Derive key
    key := pbkdf2.Key([]byte(password), km.keyDerivationSalt, 100000, 32, sha256.New)
    
    // Generate or verify key check value
    h := sha256.New()
    h.Write(key)
    h.Write([]byte("KCV"))
    kcv := hex.EncodeToString(h.Sum(nil))
    
    if km.keyCheckValue == "" {
        // First time - save KCV
        km.keyCheckValue = kcv
        kcvPath := filepath.Join(getDataDir(), ".kcv")
        if err := os.WriteFile(kcvPath, []byte(kcv), 0600); err != nil {
            return serr.Wrap(err, "failed to save key check value")
        }
    } else {
        // Verify password is correct
        if kcv != km.keyCheckValue {
            return serr.New("incorrect master password")
        }
    }
    
    // Initialize encryption with verified password
    return InitEncryption(password)
}

// RotateEncryptionKey re-encrypts all notes with a new key
func (km *KeyManager) RotateEncryptionKey(oldPassword, newPassword string) error {
    // Verify old password
    if err := km.SetMasterPassword(oldPassword); err != nil {
        return serr.Wrap(err, "old password verification failed")
    }
    
    // Load all private notes
    rows, err := diskDB.Query(`
        SELECT * FROM notes WHERE is_private = true
    `)
    if err != nil {
        return serr.Wrap(err, "failed to load private notes")
    }
    defer rows.Close()
    
    var notes []Note
    for rows.Next() {
        // Decrypt with old key
        var encrypted EncryptedNote
        // ... scan encrypted note ...
        
        note, err := noteEncryption.DecryptNote(&encrypted)
        if err != nil {
            return serr.Wrap(err, "failed to decrypt with old key")
        }
        notes = append(notes, *note)
    }
    
    // Initialize encryption with new password
    if err := InitEncryption(newPassword); err != nil {
        return serr.Wrap(err, "failed to init new encryption")
    }
    
    // Re-encrypt all notes
    tx, err := diskDB.Begin()
    if err != nil {
        return serr.Wrap(err, "failed to begin transaction")
    }
    defer tx.Rollback()
    
    for _, note := range notes {
        encrypted, err := noteEncryption.EncryptNote(&note)
        if err != nil {
            return serr.Wrap(err, "failed to re-encrypt note")
        }
        
        // Update encrypted note in database
        _, err = tx.Exec(`
            UPDATE notes 
            SET body = ?, description = ?, encryption_iv = ?
            WHERE guid = ?
        `, encrypted.Body, encrypted.Description, encrypted.EncryptionIV, note.GUID)
        
        if err != nil {
            return serr.Wrap(err, "failed to update encrypted note")
        }
    }
    
    // Update key check value
    h := sha256.New()
    h.Write(noteEncryption.masterKey)
    h.Write([]byte("KCV"))
    km.keyCheckValue = hex.EncodeToString(h.Sum(nil))
    
    kcvPath := filepath.Join(getDataDir(), ".kcv")
    if err := os.WriteFile(kcvPath, []byte(km.keyCheckValue), 0600); err != nil {
        return serr.Wrap(err, "failed to save new key check value")
    }
    
    return tx.Commit()
}
```

### UI Integration for Private Notes

```go
// handlers/private_notes.go
package handlers

// ToggleNotePrivacy changes a note's privacy setting
func ToggleNotePrivacy(c rweb.Context) error {
    guid := c.Request().Param("guid")
    isPrivate := c.Request().FormValue("private") == "true"
    
    // Get note from memory
    note, err := models.GetNoteByGUID(guid)
    if err != nil {
        return err
    }
    
    // Update privacy flag
    note.IsPrivate = isPrivate
    
    // Save with encryption if needed
    if err := note.SaveWithEncryption(getUserGUID(c)); err != nil {
        return err
    }
    
    return c.WriteJSON(map[string]interface{}{
        "success": true,
        "private": isPrivate,
    })
}

// UnlockPrivateNotes prompts for master password
func UnlockPrivateNotes(c rweb.Context) error {
    password := c.Request().FormValue("password")
    
    if err := models.SetMasterPassword(password); err != nil {
        return c.WriteStatus(401) // Unauthorized
    }
    
    // Re-sync database to decrypt notes
    if err := models.ResyncWithDecryption(); err != nil {
        return err
    }
    
    return c.WriteJSON(map[string]bool{"success": true})
}
```

### Benefits of This Approach

1. **Security at Rest**: Private notes are encrypted on disk using AES-256-GCM
2. **Performance**: Notes remain unencrypted in memory for fast access
3. **Selective Encryption**: Only notes marked as private are encrypted
4. **Key Derivation**: PBKDF2 with high iteration count prevents brute force
5. **Key Rotation**: Support for changing encryption keys without data loss

## Security Considerations

1. **Input Sanitization**: Clean all user inputs
2. **CSRF Protection**: Add CSRF tokens to forms
3. **Rate Limiting**: Implement rate limiting middleware
4. **Content Security Policy**: Set appropriate CSP headers
5. **SQL Injection Prevention**: Use parameterized queries
6. **Encryption at Rest**: Private notes encrypted with AES-256-GCM
7. **Key Management**: Secure key derivation and storage
8. **Memory Security**: Consider using secure memory allocation for keys

## Peer-to-Peer Sync Strategy

### Overview
The P2P sync system enables distributed synchronization of notes between multiple GoNotes instances without a central server. It uses git-like change tracking for efficient incremental syncs and proper conflict resolution.

### Change Tracking Architecture

#### 1. Note Change Events
```go
// models/sync_models.go
package models

import (
    "time"
    "crypto/sha256"
    "encoding/hex"
)

// ChangeOperation represents the type of change
type ChangeOperation int

const (
    OpCreate ChangeOperation = 1
    OpUpdate ChangeOperation = 2
    OpDelete ChangeOperation = 3
    OpMerge  ChangeOperation = 4  // Three-way merge result
    OpSync   ChangeOperation = 9  // Sync checkpoint
)

// NoteChange represents a single change event
type NoteChange struct {
    ID           int64          `db:"id"`
    ChangeGUID   string         `db:"change_guid"`     // Unique ID for this change
    NoteGUID     string         `db:"note_guid"`       // Note this change applies to
    Operation    ChangeOperation `db:"operation"`
    UserGUID     string         `db:"user_guid"`       // Who made the change
    ParentChange string         `db:"parent_change"`   // Previous change GUID (forms chain)
    
    // For OpCreate: full note content
    Note         *Note          `db:"-"`
    NoteID       int64          `db:"note_id"`
    
    // For OpUpdate: diff patch
    Patch        *NotePatch     `db:"-"`
    PatchID      int64          `db:"patch_id"`
    
    // Metadata
    CreatedAt    time.Time      `db:"created_at"`
    DeviceID     string         `db:"device_id"`       // Which device made change
    VectorClock  string         `db:"vector_clock"`    // JSON encoded vector clock
}

// NotePatch represents a diff for note updates (git-like)
type NotePatch struct {
    ID           int64          `db:"id"`
    FieldMask    uint16         `db:"field_mask"`      // Which fields changed
    
    // Diff patches for each field (using diff-match-patch algorithm)
    TitlePatch   string         `db:"title_patch"`
    BodyPatch    string         `db:"body_patch"`      // Git-like diff patch
    TagsPatch    string         `db:"tags_patch"`
    DescPatch    string         `db:"desc_patch"`
    
    // Full field values for non-diffable changes
    TitleFull    string         `db:"title_full"`
    TagsFull     string         `db:"tags_full"`
    
    // Checksums for verification
    BeforeHash   string         `db:"before_hash"`     // Hash before change
    AfterHash    string         `db:"after_hash"`      // Hash after change
}

// VectorClock for distributed consistency
type VectorClock map[string]int64

func (vc VectorClock) Increment(deviceID string) {
    vc[deviceID]++
}

func (vc VectorClock) Merge(other VectorClock) {
    for device, timestamp := range other {
        if timestamp > vc[device] {
            vc[device] = timestamp
        }
    }
}

// Determines causal ordering
func (vc VectorClock) HappensBefore(other VectorClock) bool {
    atLeastOneLess := false
    for device, timestamp := range vc {
        if timestamp > other[device] {
            return false
        }
        if timestamp < other[device] {
            atLeastOneLess = true
        }
    }
    return atLeastOneLess
}
```

#### 2. Change Tracking Implementation
```go
// models/change_tracker.go
package models

import (
    "github.com/sergi/go-diff/diffmatchpatch"
    "github.com/rohanthewiz/serr"
)

type ChangeTracker struct {
    dmp *diffmatchpatch.DiffMatchPatch
}

func NewChangeTracker() *ChangeTracker {
    return &ChangeTracker{
        dmp: diffmatchpatch.New(),
    }
}

// TrackNoteUpdate creates a change event for note update
func (ct *ChangeTracker) TrackNoteUpdate(oldNote, newNote *Note, userGUID string) (*NoteChange, error) {
    change := &NoteChange{
        ChangeGUID: generateGUID(),
        NoteGUID:   newNote.GUID,
        Operation:  OpUpdate,
        UserGUID:   userGUID,
        CreatedAt:  time.Now(),
        DeviceID:   getDeviceID(),
    }
    
    // Find parent change
    lastChange := GetLastChangeForNote(newNote.GUID)
    if lastChange != nil {
        change.ParentChange = lastChange.ChangeGUID
    }
    
    // Create patch
    patch := &NotePatch{
        BeforeHash: hashNote(oldNote),
        AfterHash:  hashNote(newNote),
    }
    
    // Generate diffs for text fields
    if oldNote.Title != newNote.Title {
        patch.FieldMask |= 0x08
        diffs := ct.dmp.DiffMain(oldNote.Title, newNote.Title, false)
        patch.TitlePatch = ct.dmp.DiffToDelta(diffs)
    }
    
    if oldNote.Body.String != newNote.Body.String {
        patch.FieldMask |= 0x02
        // Use line-based diff for body (like git)
        diffs := ct.dmp.DiffLinesToChars(oldNote.Body.String, newNote.Body.String)
        patch.BodyPatch = ct.createGitLikePatch(diffs)
    }
    
    if oldNote.Tags != newNote.Tags {
        patch.FieldMask |= 0x01
        patch.TagsFull = newNote.Tags // Tags use full replacement
    }
    
    change.Patch = patch
    
    // Update vector clock
    change.VectorClock = updateVectorClock(lastChange)
    
    return change, nil
}

// createGitLikePatch creates a git-style unified diff
func (ct *ChangeTracker) createGitLikePatch(diffs []diffmatchpatch.Diff) string {
    // Format as unified diff with context lines
    // @@ -start,count +start,count @@
    // -removed line
    // +added line
    //  context line
    return ct.dmp.PatchMake(diffs)
}

// ApplyPatch applies a change patch to a note
func (ct *ChangeTracker) ApplyPatch(note *Note, patch *NotePatch) error {
    // Verify checksum
    if hashNote(note) != patch.BeforeHash {
        return serr.New("checksum mismatch - note has been modified")
    }
    
    // Apply patches
    if patch.FieldMask&0x08 > 0 {
        patches, err := ct.dmp.PatchFromText(patch.TitlePatch)
        if err != nil {
            return serr.Wrap(err, "invalid title patch")
        }
        result, _ := ct.dmp.PatchApply(patches, note.Title)
        note.Title = result
    }
    
    if patch.FieldMask&0x02 > 0 {
        patches, err := ct.dmp.PatchFromText(patch.BodyPatch)
        if err != nil {
            return serr.Wrap(err, "invalid body patch")
        }
        result, _ := ct.dmp.PatchApply(patches, note.Body.String)
        note.Body.String = result
        note.Body.Valid = true
    }
    
    if patch.FieldMask&0x01 > 0 {
        note.Tags = patch.TagsFull
    }
    
    return nil
}

// ThreeWayMerge performs git-like three-way merge
func (ct *ChangeTracker) ThreeWayMerge(base, local, remote *Note) (*Note, []string, error) {
    merged := &Note{
        GUID: base.GUID,
    }
    conflicts := []string{}
    
    // Merge title
    if local.Title != base.Title && remote.Title != base.Title {
        if local.Title == remote.Title {
            merged.Title = local.Title
        } else {
            // Conflict - attempt auto-merge
            localDiff := ct.dmp.DiffMain(base.Title, local.Title, false)
            remoteDiff := ct.dmp.DiffMain(base.Title, remote.Title, false)
            
            mergedTitle, conflict := ct.autoMerge(base.Title, localDiff, remoteDiff)
            merged.Title = mergedTitle
            if conflict {
                conflicts = append(conflicts, "title")
            }
        }
    } else if local.Title != base.Title {
        merged.Title = local.Title
    } else {
        merged.Title = remote.Title
    }
    
    // Merge body with git-like algorithm
    if local.Body.String != base.Body.String && remote.Body.String != base.Body.String {
        mergedBody, conflict := ct.mergeBody(base.Body.String, local.Body.String, remote.Body.String)
        merged.Body = sql.NullString{String: mergedBody, Valid: true}
        if conflict {
            conflicts = append(conflicts, "body")
        }
    } else if local.Body.String != base.Body.String {
        merged.Body = local.Body
    } else {
        merged.Body = remote.Body
    }
    
    return merged, conflicts, nil
}
```

### Peer Discovery and Connection

#### 1. Peer Discovery Mechanisms
```go
// sync/discovery.go
package sync

import (
    "github.com/grandcat/zeroconf"
    "github.com/pion/webrtc/v3"
)

type PeerDiscovery struct {
    mdnsServer   *zeroconf.Server
    knownPeers   map[string]*PeerInfo
    peerChannels map[string]*PeerConnection
}

type PeerInfo struct {
    ID           string
    Name         string
    Address      string
    Port         int
    LastSeen     time.Time
    TrustLevel   int  // 0=unknown, 1=known, 2=trusted
    SharedSecret string
}

// StartDiscovery begins mDNS/Bonjour discovery
func (pd *PeerDiscovery) StartDiscovery() error {
    // Register our service
    server, err := zeroconf.Register(
        "gonotes-"+getDeviceID(),    // instance
        "_gonotes._tcp",              // service
        "local.",                     // domain
        8091,                         // port
        []string{"version=2.0"},      // txt records
        nil,
    )
    if err != nil {
        return serr.Wrap(err, "failed to register mDNS")
    }
    pd.mdnsServer = server
    
    // Discover peers
    go pd.discoverPeers()
    
    return nil
}

// discoverPeers continuously scans for peers
func (pd *PeerDiscovery) discoverPeers() {
    resolver, err := zeroconf.NewResolver(nil)
    if err != nil {
        return
    }
    
    entries := make(chan *zeroconf.ServiceEntry)
    go func() {
        for entry := range entries {
            pd.handleDiscoveredPeer(entry)
        }
    }()
    
    ctx := context.Background()
    err = resolver.Browse(ctx, "_gonotes._tcp", "local.", entries)
}

// handleDiscoveredPeer processes discovered peer
func (pd *PeerDiscovery) handleDiscoveredPeer(entry *zeroconf.ServiceEntry) {
    peerID := extractPeerID(entry.Instance)
    
    if _, exists := pd.knownPeers[peerID]; !exists {
        // New peer discovered
        peer := &PeerInfo{
            ID:       peerID,
            Name:     entry.Instance,
            Address:  entry.AddrIPv4[0].String(),
            Port:     entry.Port,
            LastSeen: time.Now(),
        }
        
        // Check if peer is in our trust database
        trust := checkPeerTrust(peerID)
        peer.TrustLevel = trust
        
        pd.knownPeers[peerID] = peer
        
        // Notify UI for user confirmation if untrusted
        if trust == 0 {
            notifyNewPeerDiscovered(peer)
        }
    }
}

// ConnectToPeer establishes connection after user confirmation
func (pd *PeerDiscovery) ConnectToPeer(peerID string, useWebRTC bool) error {
    peer, exists := pd.knownPeers[peerID]
    if !exists {
        return serr.New("peer not found")
    }
    
    if useWebRTC {
        // Use WebRTC for NAT traversal
        return pd.connectWebRTC(peer)
    } else {
        // Direct TCP connection
        return pd.connectTCP(peer)
    }
}
```

#### 2. Secure Peer Authentication
```go
// sync/auth.go
package sync

import (
    "crypto/rand"
    "crypto/sha256"
    "golang.org/x/crypto/nacl/box"
)

type PeerAuth struct {
    privateKey *[32]byte
    publicKey  *[32]byte
    peerKeys   map[string]*[32]byte  // Peer public keys
}

// ExchangeKeys performs Diffie-Hellman key exchange
func (pa *PeerAuth) ExchangeKeys(conn net.Conn) ([]byte, error) {
    // Send our public key
    _, err := conn.Write(pa.publicKey[:])
    if err != nil {
        return nil, err
    }
    
    // Receive peer's public key
    var peerPubKey [32]byte
    _, err = conn.Read(peerPubKey[:])
    if err != nil {
        return nil, err
    }
    
    // Generate shared secret
    var sharedSecret [32]byte
    box.Precompute(&sharedSecret, &peerPubKey, pa.privateKey)
    
    return sharedSecret[:], nil
}

// VerifyPeer performs mutual authentication
func (pa *PeerAuth) VerifyPeer(conn net.Conn, sharedSecret []byte) error {
    // Generate challenge
    challenge := make([]byte, 32)
    rand.Read(challenge)
    
    // Send challenge
    conn.Write(challenge)
    
    // Compute expected response
    h := sha256.New()
    h.Write(challenge)
    h.Write(sharedSecret)
    expected := h.Sum(nil)
    
    // Receive response
    response := make([]byte, 32)
    conn.Read(response)
    
    // Verify response
    if !bytes.Equal(response, expected) {
        return serr.New("peer authentication failed")
    }
    
    return nil
}
```

### Synchronization Protocol

#### 1. Efficient Sync with Merkle Trees
```go
// sync/merkle.go
package sync

import (
    "crypto/sha256"
    "encoding/hex"
)

type MerkleNode struct {
    Hash     string
    Children []*MerkleNode
    NoteGUID string  // Leaf nodes only
    ChangeID string  // Last change for this note
}

type MerkleTree struct {
    Root      *MerkleNode
    LeafMap   map[string]*MerkleNode
}

// BuildMerkleTree creates tree from note changes
func BuildMerkleTree(changes []NoteChange) *MerkleTree {
    tree := &MerkleTree{
        LeafMap: make(map[string]*MerkleNode),
    }
    
    // Group changes by note
    noteChanges := make(map[string][]NoteChange)
    for _, change := range changes {
        noteChanges[change.NoteGUID] = append(noteChanges[change.NoteGUID], change)
    }
    
    // Create leaf nodes
    var leaves []*MerkleNode
    for noteGUID, changes := range noteChanges {
        // Get latest change for note
        latest := changes[len(changes)-1]
        
        leaf := &MerkleNode{
            NoteGUID: noteGUID,
            ChangeID: latest.ChangeGUID,
            Hash:     hashChanges(changes),
        }
        
        leaves = append(leaves, leaf)
        tree.LeafMap[noteGUID] = leaf
    }
    
    // Build tree bottom-up
    tree.Root = buildTreeRecursive(leaves)
    
    return tree
}

// CompareTree finds differences between local and remote trees
func (tree *MerkleTree) CompareTree(remoteRoot *MerkleNode) []string {
    differences := []string{}
    compareNodes(tree.Root, remoteRoot, &differences)
    return differences
}

// compareNodes recursively compares tree nodes
func compareNodes(local, remote *MerkleNode, diffs *[]string) {
    if local.Hash == remote.Hash {
        return // Subtrees are identical
    }
    
    if local.NoteGUID != "" {
        // Leaf node - note differs
        *diffs = append(*diffs, local.NoteGUID)
        return
    }
    
    // Compare children
    for i, localChild := range local.Children {
        if i < len(remote.Children) {
            compareNodes(localChild, remote.Children[i], diffs)
        }
    }
}
```

#### 2. Sync Protocol Implementation
```go
// sync/protocol.go
package sync

import (
    "encoding/gob"
    "github.com/rohanthewiz/logger"
)

type SyncProtocol struct {
    conn         net.Conn
    encoder      *gob.Encoder
    decoder      *gob.Decoder
    changeTracker *ChangeTracker
}

type SyncMessage struct {
    Type       string
    Payload    interface{}
    Timestamp  time.Time
    Signature  []byte
}

// ExecuteSync performs full sync with peer
func (sp *SyncProtocol) ExecuteSync(peerID string) error {
    logger.Info("Starting sync with peer", "peer", peerID)
    
    // Phase 1: Exchange Merkle trees
    localTree := BuildMerkleTree(GetAllChanges())
    
    sp.sendMessage(SyncMessage{
        Type:    "MERKLE_ROOT",
        Payload: localTree.Root,
    })
    
    var remoteRoot MerkleNode
    sp.receiveMessage(&remoteRoot)
    
    // Find differences
    differences := localTree.CompareTree(&remoteRoot)
    logger.Info("Found differences", "count", len(differences))
    
    // Phase 2: Exchange change vectors
    for _, noteGUID := range differences {
        sp.syncNote(noteGUID)
    }
    
    // Phase 3: Create sync checkpoint
    checkpoint := &NoteChange{
        ChangeGUID: generateGUID(),
        Operation:  OpSync,
        CreatedAt:  time.Now(),
        UserGUID:   peerID,
    }
    
    SaveNoteChange(checkpoint)
    sp.sendMessage(SyncMessage{
        Type:    "SYNC_COMPLETE",
        Payload: checkpoint,
    })
    
    return nil
}

// syncNote synchronizes a single note
func (sp *SyncProtocol) syncNote(noteGUID string) error {
    // Get local changes for note
    localChanges := GetChangesForNote(noteGUID)
    
    // Request remote changes
    sp.sendMessage(SyncMessage{
        Type:    "REQUEST_CHANGES",
        Payload: noteGUID,
    })
    
    var remoteChanges []NoteChange
    sp.receiveMessage(&remoteChanges)
    
    // Find common ancestor
    commonAncestor := findCommonAncestor(localChanges, remoteChanges)
    
    if commonAncestor == "" {
        // No common history - full sync
        return sp.fullSyncNote(noteGUID, localChanges, remoteChanges)
    }
    
    // Incremental sync from common ancestor
    localNew := getChangesAfter(localChanges, commonAncestor)
    remoteNew := getChangesAfter(remoteChanges, commonAncestor)
    
    // Apply remote changes
    for _, change := range remoteNew {
        if err := sp.applyChange(change); err != nil {
            // Conflict - attempt three-way merge
            sp.resolveConflict(change, localNew)
        }
    }
    
    // Send local changes
    sp.sendMessage(SyncMessage{
        Type:    "PUSH_CHANGES",
        Payload: localNew,
    })
    
    return nil
}

// resolveConflict handles merge conflicts
func (sp *SyncProtocol) resolveConflict(remoteChange NoteChange, localChanges []NoteChange) error {
    // Get base note at common ancestor
    baseNote := GetNoteAtChange(remoteChange.ParentChange)
    
    // Get current local and remote versions
    localNote := GetNote(remoteChange.NoteGUID)
    remoteNote := ApplyChangesToNote(baseNote, []NoteChange{remoteChange})
    
    // Three-way merge
    merged, conflicts, err := sp.changeTracker.ThreeWayMerge(baseNote, localNote, remoteNote)
    if err != nil {
        return err
    }
    
    if len(conflicts) > 0 {
        // Manual conflict resolution needed
        merged = promptUserForConflictResolution(baseNote, localNote, remoteNote, conflicts)
    }
    
    // Save merged result
    mergeChange := &NoteChange{
        ChangeGUID:   generateGUID(),
        NoteGUID:     merged.GUID,
        Operation:    OpMerge,
        ParentChange: remoteChange.ChangeGUID,
        CreatedAt:    time.Now(),
    }
    
    SaveNoteChange(mergeChange)
    UpdateNote(merged)
    
    return nil
}
```

### Sync Configuration
```go
// config/sync_config.go
package config

type SyncConfig struct {
    // Discovery
    EnableMDNS      bool   `json:"enable_mdns"`
    EnableWebRTC    bool   `json:"enable_webrtc"`
    BroadcastName   string `json:"broadcast_name"`
    
    // Connection
    SyncPort        int    `json:"sync_port"`
    MaxPeers        int    `json:"max_peers"`
    ConnectionTimeout time.Duration `json:"connection_timeout"`
    
    // Sync behavior
    AutoSync        bool   `json:"auto_sync"`
    SyncInterval    time.Duration `json:"sync_interval"`
    ConflictStrategy string `json:"conflict_strategy"` // auto, manual, local, remote
    
    // Security
    RequireAuth     bool   `json:"require_auth"`
    EncryptTransfer bool   `json:"encrypt_transfer"`
    TrustOnFirstUse bool   `json:"trust_on_first_use"`
    
    // Performance
    BatchSize       int    `json:"batch_size"`
    CompressData    bool   `json:"compress_data"`
    MaxChangeHistory int   `json:"max_change_history"`
}

var DefaultSyncConfig = SyncConfig{
    EnableMDNS:      true,
    EnableWebRTC:    false,
    BroadcastName:   "GoNotes-" + getHostname(),
    SyncPort:        8091,
    MaxPeers:        10,
    ConnectionTimeout: 30 * time.Second,
    AutoSync:        false,
    SyncInterval:    15 * time.Minute,
    ConflictStrategy: "auto",
    RequireAuth:     true,
    EncryptTransfer: true,
    TrustOnFirstUse: true,
    BatchSize:       100,
    CompressData:    true,
    MaxChangeHistory: 1000,
}
```

### UI Integration for Sync

```go
// handlers/sync_handlers.go
package handlers

// HandlePeerDiscovered notifies UI of new peer
func HandlePeerDiscovered(peer *PeerInfo) {
    notification := map[string]interface{}{
        "type": "peer-discovered",
        "peer": peer,
        "action": "confirm-sync",
    }
    
    BroadcastToUI(notification)
}

// HandleSyncRequest processes user confirmation
func HandleSyncRequest(c rweb.Context) error {
    peerID := c.Request().FormValue("peer_id")
    approved := c.Request().FormValue("approved") == "true"
    
    if approved {
        // Save peer as trusted
        SaveTrustedPeer(peerID)
        
        // Initiate sync
        go StartSyncWithPeer(peerID)
        
        return c.WriteJSON(map[string]bool{"success": true})
    }
    
    return c.WriteJSON(map[string]bool{"success": false})
}

// HandleSyncStatus returns current sync status
func HandleSyncStatus(c rweb.Context) error {
    status := GetSyncStatus()
    return c.WriteJSON(status)
}
```

### Benefits of This Approach

1. **Git-like Change Tracking**
   - Diff patches minimize data transfer
   - Change chains enable history tracking
   - Three-way merge for conflict resolution

2. **Efficient Sync**
   - Merkle trees for fast difference detection
   - Only sync changed notes
   - Compression and batching for performance

3. **Security**
   - End-to-end encryption
   - Mutual authentication
   - Trust on first use (TOFU) model

4. **User Control**
   - Manual peer approval
   - Conflict resolution options
   - Selective sync capability

5. **Network Flexibility**
   - Local network discovery via mDNS
   - WebRTC for NAT traversal
   - Direct TCP for LAN connections

## Future Enhancements

1. **User Authentication**: Add login/signup
2. **Note Sharing**: Share notes with unique URLs
3. **Collaborative Editing**: Real-time collaboration
4. **Version History**: Track note changes
5. **API Keys**: For third-party integrations
6. **Mobile App**: Progressive Web App support
7. **Plugins**: Extension system for custom features
8. **Cloud Backup**: Optional encrypted cloud sync
9. **Federation**: Connect multiple GoNotes servers

## Development Commands

```bash
# Run development server
go run main.go

# Build for production
go build -o gonotes-web

# Run tests
go test ./...

# Format code
go fmt ./...

# Download vendor files
wget https://unpkg.com/alpinejs@3/dist/cdn.min.js -O static/vendor/alpine.min.js
wget https://unpkg.com/htmx.org@1.9/dist/htmx.min.js -O static/vendor/htmx.min.js
# Monaco requires downloading the full package from CDN

# Import from old database
go run tools/migrate.go -source=/path/to/old.db -dest=./data/notes.db
```

## Resources

- [RWeb Documentation](https://github.com/rohanthewiz/rweb)
- [Element Documentation](https://github.com/rohanthewiz/element)
- [DuckDB Go Driver](https://github.com/marcboeker/go-duckdb)
- [Monaco Editor](https://microsoft.github.io/monaco-editor/)
- [Alpine.js](https://alpinejs.dev/)
- [HTMX](https://htmx.org/)

---

This implementation guide provides a complete roadmap for building the new GoNotes web platform with modern technologies while maintaining simplicity and avoiding Node.js/build complexity.