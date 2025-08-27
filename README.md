# GoNotes Web

A modern, web-based note-taking platform built with Go, featuring server-side rendering, embedded assets, and real-time updates.

## Project Status
This is **pre-alpha** not ready for use!

## Features

- 📝 **Rich Markdown Editor** - Monaco Editor integration for powerful note editing
- 🔍 **Advanced Search** - Search by title, content, or tags
- 🏷️ **Tag Management** - Organize notes with flexible tagging
- 💾 **Auto-Save** - Automatic draft saving with intelligent debouncing
- 🔄 **Real-Time Updates** - Server-Sent Events for live notifications
- 🔒 **Private Notes** - Support for encrypted private notes
- ⚡ **Fast Performance** - DuckDB with dual-database architecture
- 📦 **Single Binary** - All assets embedded, no external dependencies
- 📱 **Responsive Design** - Works on desktop and mobile devices
- ⌨️ **Keyboard Shortcuts** - Productivity shortcuts (Ctrl+K search, Ctrl+N new)

## Architecture

### Technology Stack

- **Backend**: Go 1.21+
- **Web Framework**: [rohanthewiz/rweb](https://github.com/rohanthewiz/rweb)
- **HTML Generation**: [rohanthewiz/element](https://github.com/rohanthewiz/element)
- **Database**: DuckDB (embedded)
- **Frontend**: Alpine.js + HTMX (no build step)
- **Editor**: Monaco Editor
- **CSS**: Custom CSS with CSS Grid

### Project Structure

```
go_notes_web/
├── main.go                 # Application entry point
├── models/                 # Data models and database operations
│   ├── db.go              # DuckDB connection and dual-database setup
│   ├── migrations.go      # Database schema migrations
│   └── note.go            # Note model and CRUD operations
├── server/                 # Server configuration
│   ├── server.go          # RWeb server setup
│   ├── routes.go          # Route definitions
│   ├── middleware.go      # Custom middleware
│   ├── static.go          # Embedded static file serving
│   └── static/            # Static assets (embedded)
│       ├── css/           # Stylesheets
│       ├── js/            # JavaScript modules
│       └── vendor/        # Third-party libraries
├── handlers/               # Request handlers
│   ├── notes.go           # Note CRUD handlers
│   ├── search.go          # Search functionality
│   ├── tags.go            # Tag management
│   └── partials.go        # HTMX partial responses
├── views/                  # HTML views (server-side)
│   ├── layout.go          # Base layout
│   ├── components/        # Reusable components
│   └── pages/             # Page templates
└── scripts/                # Utility scripts
    └── download_vendor.sh # Download vendor libraries
```

## Installation

### Prerequisites

- Go 1.21 or higher
- Git

### Setup

1. **Clone the repository**
   ```bash
   git clone <repository-url>
   cd go_notes_web
   ```

2. **Download dependencies**
   ```bash
   go mod download
   ```

3. **Download vendor libraries**
   ```bash
   chmod +x scripts/download_vendor.sh
   ./scripts/download_vendor.sh
   ```

4. **Build the application**
   ```bash
   go build -o gonotes
   ```

5. **Run the application**
   ```bash
   ./gonotes
   ```

   The server will start on http://localhost:8080

## Configuration

### Environment Variables

- `PORT` - Server port (default: 8080)
- `DB_PATH` - Database file path (default: ./data/notes.db)
- `ENV` - Environment (development/production)

### Database

GoNotes uses a dual-database architecture with DuckDB:
- **Memory Database**: For fast read operations
- **Disk Database**: For persistent storage
- **Write-Through Cache**: Ensures consistency

## Usage

### Keyboard Shortcuts

- `Ctrl/Cmd + K` - Focus search
- `Ctrl/Cmd + N` - Create new note
- `Ctrl/Cmd + S` - Save note (in editor)
- `Ctrl/Cmd + B` - Bold (in editor)
- `Ctrl/Cmd + I` - Italic (in editor)
- `Ctrl/Cmd + E` - Code (in editor)
- `Escape` - Close modals/overlays

### API Endpoints

#### Notes
- `GET /` - Dashboard
- `GET /notes/new` - New note form
- `GET /notes/:guid` - View note
- `GET /notes/:guid/edit` - Edit note
- `POST /api/notes` - Create note
- `PUT /api/notes/:guid` - Update note
- `DELETE /api/notes/:guid` - Delete note
- `POST /api/notes/:guid/save` - Auto-save

#### Search
- `GET /search` - Search page
- `GET /api/search` - Search API
- `GET /api/search/title` - Search by title
- `GET /api/search/tag` - Search by tag
- `GET /api/search/body` - Search by content

#### Tags
- `GET /tags` - Tags overview
- `GET /api/tags` - Get all tags
- `GET /api/tags/:tag/notes` - Get notes by tag

#### Real-time
- `GET /events` - Server-Sent Events stream

## Development

### Running in Development Mode

```bash
# With hot reload (using air)
air

# Or standard go run
go run .
```

### Testing

```bash
go test ./...
```

### Building for Production

```bash
# Build with optimizations
go build -ldflags="-w -s" -o gonotes

# Cross-compile for different platforms
GOOS=linux GOARCH=amd64 go build -o gonotes-linux
GOOS=darwin GOARCH=amd64 go build -o gonotes-mac
GOOS=windows GOARCH=amd64 go build -o gonotes.exe
```

## Deployment

### Single Binary Deployment

GoNotes compiles to a single binary with all assets embedded:

```bash
# Copy binary to server
scp gonotes user@server:/path/to/deployment/

# Run with systemd (example service file)
[Unit]
Description=GoNotes Web
After=network.target

[Service]
Type=simple
User=gonotes
WorkingDirectory=/path/to/deployment
ExecStart=/path/to/deployment/gonotes
Restart=on-failure
Environment=PORT=8080
Environment=ENV=production

[Install]
WantedBy=multi-user.target
```

### Docker Deployment

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -ldflags="-w -s" -o gonotes

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/gonotes .
EXPOSE 8080
CMD ["./gonotes"]
```

## Security Considerations

- Session management with secure cookies
- CORS middleware for cross-origin requests
- Security headers (XSS, Frame Options, CSP)
- Rate limiting on API endpoints
- Input validation and sanitization
- Prepared statements for database queries
- Support for encrypted private notes

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Acknowledgments

- [rohanthewiz](https://github.com/rohanthewiz) for RWeb, Element, SErr, and other packages
- [DuckDB](https://duckdb.org/) for the embedded database
- [Monaco Editor](https://microsoft.github.io/monaco-editor/) for the code editor
- [Alpine.js](https://alpinejs.dev/) and [HTMX](https://htmx.org/) for frontend interactivity

## Support

For issues, questions, or suggestions, please open an issue on GitHub.

---

Built with ❤️ using Go, RWeb, Element, and other modern web technologies