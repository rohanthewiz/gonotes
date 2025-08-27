# Vendor Libraries

This directory contains third-party libraries used by the application.

## Required Libraries

1. **Alpine.js v3** - Lightweight reactive framework
   - Download from: https://unpkg.com/alpinejs@3.x.x/dist/cdn.min.js
   - Save as: `alpine.min.js`

2. **HTMX** - HTML over the wire
   - Download from: https://unpkg.com/htmx.org@latest/dist/htmx.min.js
   - Save as: `htmx.min.js`

3. **MessagePack** - Binary serialization
   - Download from: https://unpkg.com/@msgpack/msgpack@latest/dist/msgpack.min.js
   - Save as: `msgpack.min.js`

4. **Monaco Editor** - Code editor
   - Download Monaco Editor from: https://microsoft.github.io/monaco-editor/
   - Extract to: `monaco/` directory
   - Required structure:
     ```
     monaco/
     ├── min/
     │   └── vs/
     │       ├── loader.js
     │       ├── editor/
     │       └── ...
     └── editor/
         └── editor.main.css
     ```

## CDN Alternative

For development, you can also update the layout.go file to use CDN versions:

```html
<script src="https://unpkg.com/alpinejs@3.x.x/dist/cdn.min.js" defer></script>
<script src="https://unpkg.com/htmx.org@latest"></script>
<script src="https://unpkg.com/@msgpack/msgpack@latest/dist/msgpack.min.js"></script>
```

For Monaco Editor, use the CDN loader:
```html
<script src="https://unpkg.com/monaco-editor@latest/min/vs/loader.js"></script>
```