# GoNotes Landing Page Design Plan

## Design Philosophy

GoNotes landing page prioritizes:
- **Knowledge density**: Maximum information and functionality in viewport
- **Utility**: Every element serves a purpose, no wasted space
- **Feature richness**: Full access to all backend capabilities
- **Responsive**: Optimized for 7.6"+ screens (tablets and desktop)

## Chosen Design: "Inbox Pro" - Enhanced Search

Email client inspired three-pane layout with advanced search/filter controls.

---

## Visual Layout

```
┌──────────────────────────────────────────────────────────┐
│ [+New Note]  | All Notes (127) | Sort:Modified ▾ | ⟳ | 👤 │
├────────┬─────────────────────────────┬───────────────────┤
│SEARCH  │ NOTE INBOX (142 notes)      │  PREVIEW PANEL    │
│& FILTER│☐ Title checkbox row         │                   │
│        │☐ 🔒 K8s Deployment Guide    │ K8s Deployment    │
│🔍_____ │   #k8s #pods #docker        │ Guide             │
│ [Go]   │   k8s › pods, deployment    │ ─────────────────│
│        │   Quick preview: Deploy...  │ #k8s #pods #docker│
│Categories│   Modified: 2h ago  👁 Edit │                   │
│[All ▾] │                             │ Categories:       │
│☐ k8s(12│☐ Error Handling in Go       │ k8s › pods        │
│☐ Go (8)│   #go #debugging            │ k8s › deployment  │
│☐ Linux │   Go                        │                   │
│  (6)   │   Log errors properly...   │ [Markdown Preview]│
│☐ APIs  │   Modified: 5h ago  👁 Edit │ or                │
│  (4)   │                             │ [Quick Edit Mode] │
│        │☐ Linux Networking Basics    │ Title: _______    │
│Subcat: │   #linux #network           │ Body: _________   │
│☐ pods  │   Linux                     │ Tags: _________   │
│☐ deploy│   ifconfig vs ip command... │ 🔒 Private [Save] │
│☐ svc   │   Modified: 1d ago  👁 Edit │                   │
│        │                             │                   │
│Tags    │ [...more notes, infinite]   │                   │
│☐ docker│                             │                   │
│☐ api   │ [Batch actions bar shows    │                   │
│☐ debug │  when checkboxes selected]  │                   │
│☐ k8s   │ Delete | Tag | Move | 🔒    │                   │
│☐ linux │                             │                   │
│        │                             │                   │
│Privacy │                             │                   │
│☐ Private only                        │                   │
│☐ Public only                         │                   │
│        │                             │                   │
│Date    │                             │                   │
│[Last 7d▾]                            │                   │
│○ Today │                             │                   │
│○ Week  │                             │                   │
│○ Month │                             │                   │
│○ Custom│                             │                   │
│        │                             │                   │
│Sync    │                             │                   │
│☐ Unsynced                            │                   │
│        │                             │                   │
│[Clear] │                             │                   │
├────────┴─────────────────────────────┴───────────────────┤
│ Status: ⟳ Synced 2m ago | Filters: k8s, docker | 12 shown│
└──────────────────────────────────────────────────────────┘
```

---

## Component Breakdown

### 1. Top Toolbar (Full Width)

**Elements (Left to Right)**:
- **[+New Note]** button - Primary action, always accessible
- **Active View Indicator** - "All Notes (127)" or "Search Results (12)"
- **Sort Dropdown** - Modified/Created/Title/Category
- **Sync Button** - Icon with status indicator (⟳ or ⚠)
- **User Menu** - Avatar/username, dropdown for logout/settings

**Height**: 48-56px
**Behavior**: Fixed position, always visible on scroll

---

### 2. Left Panel: Search & Filter Controls (200-220px)

#### 2.1 Search Box
- Full-text search input
- **Searches**: title, description, body, tags
- Live filtering as you type
- Clear button (×) when active
- **Keyboard shortcut**: `/` to focus

#### 2.2 Categories Section
- Collapsible section header: "Categories"
- Dropdown: "All" or "Selected (2)"
- **Checkbox list** with note counts: `☐ k8s (12)`
- Multi-select (AND logic): k8s + Go = notes in BOTH
- Selecting a category reveals subcategories below

#### 2.3 Subcategories Section
- **Dynamic**: Only shows when parent category selected
- Example: Select "k8s" → shows pods, deployment, service
- Multi-select within parent
- Indented for visual hierarchy

#### 2.4 Tags Section
- Collapsible section header: "Tags"
- Multi-select checkbox list
- Shows top 20 tags by usage frequency
- "Show all..." link expands to full list
- Each tag shows usage count: `☐ docker (15)`
- **OR logic**: #docker OR #api = notes with either tag

#### 2.5 Privacy Filter
- Collapsible section header: "Privacy"
- Radio options:
  - ○ All (default)
  - ○ Private only (🔒)
  - ○ Public only (🌐)

#### 2.6 Date Filter
- Collapsible section header: "Date"
- Dropdown: Last 7d / 30d / 90d / All / Custom
- Radio buttons for quick picks:
  - ○ Today
  - ○ Last week
  - ○ Last month
  - ○ Custom range (opens date picker)

#### 2.7 Sync Filter
- Collapsible section header: "Sync"
- Checkbox: `☐ Unsynced only`
- Shows notes pending sync to peers

#### 2.8 Action Buttons
- **[Clear All Filters]** - Reset to default state
- **[Save Search]** (future) - Save current filter combination

**Filter Logic**:
- Combines all active filters with AND logic
- Tags use OR within, then AND with other filters
- Example: Search "deploy" + Category "k8s" + Tag "#docker" + Privacy "Private"
  - Result: Private notes containing "deploy" in k8s category tagged with docker
- Active filters displayed in bottom status bar
- Result count updates live

**Behavior**:
- Fixed position, scrollable if content overflows
- Collapsible sections save space
- Filter state persists in localStorage

---

### 3. Center Panel: Note List (Flexible Width)

#### 3.1 List Header
- Checkbox for select all
- Column headers: Title | Modified | Actions
- Sort indicators (▲▼)

#### 3.2 Note Row (60px height)

**Row Structure** (3-line layout):
```
☐ 🔒 Note Title Here
   #tag1 #tag2 #tag3 | category › subcategory1, subcategory2
   Preview of note body text showing first 80 characters...
   Modified: 2h ago  👁 Edit
```

**Elements**:
- **Checkbox** - Multi-select for batch operations
- **Privacy Icon** - 🔒 for private notes, blank for public
- **Title** - Bold, primary text
- **Tags** - Inline, clickable (adds to tag filter)
- **Categories** - Format: `category › subcat1, subcat2`
- **Body Preview** - First 80 chars, truncated with ellipsis
- **Timestamp** - Relative time (2h ago, 1d ago)
- **Quick Actions** - Eye icon for preview, Edit button

**Interaction**:
- Click anywhere on row → Open in preview panel
- Checkbox → Select for batch operations
- Hover → Highlight with subtle background color
- Double-click → Open in edit mode

#### 3.3 Batch Operations Bar

Appears when 1+ notes selected (slides in from top of list):

```
[Delete] [Add Tags] [Remove Tags] [Set Category] [Toggle Privacy] [x selected]
```

**Actions**:
- Delete - Soft delete selected notes
- Add Tags - Modal to add tags to all selected
- Remove Tags - Modal to remove tags from all selected
- Set Category - Assign category to all selected
- Toggle Privacy - Encrypt/decrypt all selected

#### 3.4 Performance

- **Virtual scrolling** - Handle 1000+ notes smoothly
- Render only visible rows + buffer
- Lazy load on scroll
- Infinite scroll - Load more on reaching bottom

**Keyboard Navigation**:
- `j/k` - Move down/up
- `x` - Toggle checkbox on current row
- `Enter` - Open in preview panel
- `e` - Open in edit mode
- `Shift+j/k` - Select range

---

### 4. Right Panel: Preview/Edit (35-40% width)

#### 4.1 Preview Mode (Default)

**Header**:
- Note title (large, bold)
- Divider line
- Metadata row: Tags, Categories, Modified date

**Body**:
- Markdown rendered content
- Syntax highlighting for code blocks
- Images displayed inline
- Links clickable

**Footer Actions**:
- [Edit] button - Switch to edit mode
- [Delete] button - Soft delete
- [Duplicate] button - Create copy
- [Share] button (future) - Generate share link

#### 4.2 Edit Mode

**Form Fields**:
```
Title: ___________________________________

Tags: #___________ (autocomplete dropdown)

Categories: [k8s ×] [+ Add Category]
  Subcategories: [pods ×] [deployment ×]

Body:
┌─────────────────────────────────────┐
│ [Markdown editor with toolbar]      │
│ - Bold, Italic, Code, Link buttons  │
│ - Preview toggle                    │
│ - Full-height textarea              │
└─────────────────────────────────────┘

☐ Private (Encrypt this note)

[Cancel] [Save]
```

**Features**:
- **Live markdown preview** - Toggle split view
- **Tag autocomplete** - Suggests existing tags as you type
- **Category picker** - Dropdown with existing categories
- **Subcategory checkboxes** - Multi-select under each category
- **Privacy toggle** - Checkbox with encryption indicator
- **Auto-save draft** - Save to localStorage every 10s
- **Keyboard shortcuts**:
  - `Ctrl+S` - Save
  - `Ctrl+B` - Bold
  - `Ctrl+I` - Italic
  - `Ctrl+K` - Insert link
  - `Esc` - Cancel edit

**Validation**:
- Title required (show error if empty)
- GUID auto-generated on create
- Optimistic UI update (show immediately, rollback on error)

---

### 5. Bottom Status Bar (Full Width)

**Left Side**:
- **Sync Status**:
  - `⟳ Synced 2m ago` (green)
  - `⚠ 3 unsynced notes` (yellow/orange)
  - `❌ Sync failed` (red) with retry button

**Center**:
- **Active Filters**: `Filters: k8s, docker, private`
- Shows first 3 filters, `+2 more` if additional
- Click to open filter panel

**Right Side**:
- **Result Count**: `12 shown of 127 total`
- **Storage Usage**: `2.4 MB` (optional)

**Height**: 32-36px
**Behavior**: Fixed position, always visible

---

## Responsive Breakpoints

### Desktop (>1280px)
- Three-pane full layout as designed
- Left panel: 220px
- Center panel: Flexible (min 400px)
- Right panel: 40% (max 600px)

### Tablet Landscape (1024-1280px)
- Left panel: 200px
- Center panel: Flexible
- Right panel: 35%

### Tablet Portrait (768-1024px)
- Left panel: Collapsible drawer (hamburger menu)
- Center panel: Full width
- Right panel: Slides over center (modal style)

### Mobile (<768px)
- Single column stack
- Top bar with hamburger menu
- Filter button shows active filter count badge
- List view fills screen
- Preview/edit opens as full-screen modal
- Swipe actions on note rows:
  - Swipe right → Edit
  - Swipe left → Delete

---

## Technical Implementation Notes

### Frontend Stack Recommendation
- **Framework**: React or Svelte (lightweight)
- **Styling**: Tailwind CSS for rapid UI development
- **State**: Context API or Zustand (lightweight state management)
- **Router**: React Router or Svelte Router
- **Markdown**: `marked.js` or `remark` for rendering
- **Editor**: `CodeMirror` or `Monaco Editor` for markdown editing
- **Virtual Scroll**: `react-virtual` or `svelte-virtual-list`

### Data Flow

#### Initial Load
1. Check localStorage for JWT token
2. If no token → Redirect to login page
3. If token exists:
   - Call `GET /api/v1/auth/me` to validate
   - Call `GET /api/v1/notes?limit=50` for initial batch
   - Call `GET /api/v1/categories` for filter options
   - Extract tags from loaded notes for tag filter

#### Authentication Flow
1. User submits login form
2. `POST /api/v1/auth/login` with credentials
3. Store JWT in localStorage
4. Redirect to home page
5. Set up token refresh timer (6.5 days)

#### Note Operations
- **Create**: POST to `/api/v1/notes` → Prepend to list
- **Update**: PUT to `/api/v1/notes/:id` → Update in list
- **Delete**: DELETE to `/api/v1/notes/:id` → Remove from list
- **Search**: Client-side filter on loaded notes (later: server-side search API)

#### Sync Status
- Poll `GET /api/v1/sync/changes?since=<last_sync>` every 30s
- Update sync indicator in status bar
- Show notification if remote changes detected

### Performance Optimizations

1. **Virtual Scrolling**
   - Render only visible note rows (10-15 at a time)
   - Buffer 5 rows above/below viewport
   - Dramatically reduces DOM nodes

2. **Lazy Loading**
   - Initial load: 50 notes
   - Load next 50 on scroll to bottom
   - Preload next batch when 10 rows from bottom

3. **Client-Side Caching**
   - Cache note list in memory
   - Filter/search operates on cached data
   - Invalidate cache on create/update/delete
   - Persist to IndexedDB for offline support

4. **Debouncing**
   - Search input: 300ms debounce
   - Filter checkboxes: Immediate (no debounce needed)
   - Auto-save in editor: 2s debounce

5. **Optimistic UI**
   - Show changes immediately
   - Rollback on API error
   - Show saving indicator during API call

### Accessibility (a11y)

- **Keyboard Navigation**: Full keyboard support (no mouse required)
- **ARIA Labels**: Proper labels for screen readers
- **Focus Management**: Clear focus indicators, logical tab order
- **Color Contrast**: WCAG AA compliance (4.5:1 minimum)
- **Skip Links**: "Skip to main content" link for screen readers

### Security Considerations

1. **XSS Prevention**
   - Sanitize markdown before rendering (use DOMPurify)
   - Escape user input in search/filters
   - CSP headers to prevent script injection

2. **Token Management**
   - Store JWT in localStorage (acceptable for web apps)
   - Include token in Authorization header
   - Clear token on logout
   - Auto-logout on token expiration

3. **Private Notes**
   - Show 🔒 indicator clearly
   - Client receives decrypted content (encryption is at-rest on server)
   - Consider client-side encryption in future (zero-knowledge)

---

## Future Enhancements (Phase 2)

1. **Saved Searches**
   - Save filter combinations with names
   - Quick access to frequent searches

2. **Note Linking**
   - Wiki-style `[[links]]` between notes
   - Backlinks panel showing incoming links

3. **Rich Text Editor**
   - WYSIWYG option alongside markdown
   - Image upload and embedding

4. **Collaboration**
   - Share notes with other users
   - Real-time collaborative editing

5. **Mobile Apps**
   - Native iOS/Android apps
   - Offline-first with sync

6. **Advanced Sync**
   - Conflict resolution UI
   - Sync history/timeline
   - Per-device sync status

7. **Search Improvements**
   - Full-text search on server (SQL FTS)
   - Search highlights in results
   - Search within note bodies in preview

8. **Bulk Import/Export**
   - Import from Evernote, Notion, OneNote
   - Export to markdown files, PDF

---

## Implementation Priority

### Phase 1 (MVP)
1. Authentication (login/register/logout)
2. Note list view with basic filtering
3. Note preview panel
4. Note editor (create/edit/delete)
5. Category filter
6. Tag filter
7. Sync status indicator

### Phase 2 (Enhanced)
1. Advanced search (full-text)
2. Batch operations
3. Keyboard shortcuts
4. Subcategory support
5. Privacy filter
6. Date range filter

### Phase 3 (Polish)
1. Responsive design for mobile
2. Offline support (PWA)
3. Performance optimizations
4. Accessibility audit
5. User settings/preferences

---

## Design System

### Colors (Suggested)
- **Primary**: #3B82F6 (Blue) - Actions, links, focus
- **Success**: #10B981 (Green) - Sync status, save
- **Warning**: #F59E0B (Orange) - Unsynced, alerts
- **Danger**: #EF4444 (Red) - Delete, errors
- **Neutral**:
  - Background: #FFFFFF (light) / #1F2937 (dark)
  - Text: #111827 (light) / #F9FAFB (dark)
  - Border: #E5E7EB (light) / #374151 (dark)

### Typography
- **Font**: System font stack (native, fast)
  - `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial`
- **Sizes**:
  - Title: 24px (1.5rem)
  - Note Title: 16px (1rem)
  - Body: 14px (0.875rem)
  - Small: 12px (0.75rem)

### Spacing
- **Base unit**: 4px
- **Common**: 8px, 12px, 16px, 24px, 32px
- **Panel padding**: 16px
- **Row padding**: 12px vertical, 16px horizontal

### Icons
- **Library**: Heroicons or Lucide (consistent, modern)
- **Size**: 20px default, 16px for inline

---

## Conclusion

This design provides:
- ✅ **Knowledge density**: 10-12 notes visible, plus filters, plus preview
- ✅ **Utility**: Every pixel serves search, filter, or content display
- ✅ **Feature richness**: Full CRUD, categories, subcategories, tags, privacy, sync
- ✅ **Responsive**: Adapts from 7.6" tablets to desktop (1920px+)
- ✅ **Performance**: Virtual scroll, lazy load, client-side filtering
- ✅ **Proven UX**: Email client pattern (familiar to users)

The three-pane layout with enhanced search/filter panel maximizes information density while maintaining usability and performance.
