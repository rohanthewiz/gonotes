# Sync UI Enhancement Plan

## Overview

Add a sync control panel to the GoNotes UI with three capabilities: automatic sync toggle, manual sync button, and sync statistics display. The design integrates into the existing filter panel's Sync section and status bar, keeping the UI concise while surfacing essential sync information.

---

## Current State

| Component | File | What exists today |
|-----------|------|-------------------|
| Toolbar sync button | `web/pages/landing/toolbar.go` | `↻` icon button calls `app.syncNotes()` which just reloads notes from the local API |
| Filter panel Sync section | `web/pages/landing/filter_panel.go` | Collapsed section with a single "Unsynced only" checkbox filter |
| Status bar | `web/pages/landing/status_bar.go` | Left: sync-status icon+text ("Ready"), Center: active filters, Right: result count |
| JS sync logic | `web/static/js/app.js` | `syncNotes()` reloads notes+categories from local API; `state.lastSync` tracked but never populated from a real peer sync |
| Backend sync API | `web/api/sync.go` | Full P2P pull/push/snapshot/status endpoints exist; `SyncPushResponse` returns `accepted[]` and `rejected[]` counts |

**Key gap:** The UI has no real peer sync — `syncNotes()` only reloads from the local server. There is no auto-sync, no peer URL configuration, no sync stats tracking, and no conflict resolution UI.

---

## 1. Automatic Sync Toggle

### Goal
Let users enable/disable periodic background sync with a configured peer. When enabled, sync runs on an interval without manual intervention.

### UI Location
**Filter panel > Sync section** (expand the existing collapsed section in `filter_panel.go`)

### Design

```
┌─ Sync ──────────────────────────────┐
│  Auto-sync    [━━━○ Off]            │
│  Interval     [5 min ▾]             │
│  Peer URL     [https://... ]  [Test]│
│  ☐ Unsynced only                    │
└─────────────────────────────────────┘
```

### Implementation Steps

#### a. Filter panel markup (`filter_panel.go`)

Add to `renderSyncSection()`, above the existing "Unsynced only" checkbox:

- **Toggle row:** Label "Auto-sync" + a CSS toggle switch (`<input type="checkbox" id="auto-sync-toggle">`) calling `app.toggleAutoSync(this.checked)`
- **Interval selector:** A compact `<select id="sync-interval">` with options: 1 min, 5 min (default), 15 min, 30 min. `onchange="app.setSyncInterval(this.value)"`
- **Peer URL input:** A short text input `<input id="sync-peer-url" placeholder="https://peer:port">` with a small "Test" button (`app.testPeerConnection()`) that hits `GET /api/v1/health` on the peer

#### b. JavaScript state & logic (`app.js`)

Add to `state`:
```js
sync: {
  autoEnabled: false,
  intervalMs: 300000,       // 5 min default
  peerUrl: '',
  peerId: '',               // generated once, stored in localStorage
  timerId: null,            // setInterval handle
  lastSyncAt: null,         // Date object
  stats: { pulled: 0, pushed: 0, conflicts: 0 },
  conflicts: []             // unresolved conflict objects
}
```

New functions:
- `app.toggleAutoSync(enabled)` — Starts/stops the interval timer. Persists preference to `localStorage`.
- `app.setSyncInterval(minutes)` — Updates interval, restarts timer if running. Persists to `localStorage`.
- `app.setPeerUrl(url)` — Validates URL format, stores in `localStorage`, updates `state.sync.peerUrl`.
- `app.testPeerConnection()` — `fetch(peerUrl + '/api/v1/health')`, shows toast success/error.
- `app._runSync()` — Internal: executes pull then push cycle (see Section 2).
- On `init()`: restore sync preferences from `localStorage`; if auto-sync was enabled, restart the timer.

#### c. Persistence

All sync preferences stored in `localStorage` under keys:
- `sync_auto_enabled` (boolean)
- `sync_interval_ms` (number)
- `sync_peer_url` (string)
- `sync_peer_id` (string — generated once via `generateGUID()`)

---

## 2. Manual Sync Button

### Goal
Replace the current no-op toolbar sync button with a real peer sync that pulls remote changes and pushes local changes.

### UI Location
**Toolbar** (existing `↻` button) + **Filter panel Sync section** (secondary "Sync Now" button)

### Design

Toolbar button behavior changes:
- **Idle:** Shows `↻` icon
- **Syncing:** Rotates icon with CSS animation, button disabled
- **Error:** Brief red flash, then returns to idle

Filter panel addition:
```
│  [↻ Sync Now]                       │
```
A small secondary button below the auto-sync controls for explicit trigger.

### Implementation Steps

#### a. Toolbar update (`toolbar.go`)

No markup change needed — the existing button already calls `app.syncNotes()`. Add a CSS class `.syncing` that applies a rotation animation to `#sync-icon`.

#### b. Filter panel button (`filter_panel.go`)

Add a `<button class="btn btn-secondary btn-sm" onclick="app.syncNotes()">↻ Sync Now</button>` inside `renderSyncSection()`, below the peer URL row.

#### c. Sync protocol implementation (`app.js`)

Rewrite `app.syncNotes()`:

```
async syncNotes():
  1. Guard: if no peerUrl configured, show toast "Configure peer URL first" and return
  2. Guard: if already syncing (state.sync._running), return
  3. Set state.sync._running = true
  4. Update UI: add .syncing to #btn-sync, updateSyncStatus('syncing', 'Syncing...')
  5. PULL phase:
     a. GET peerUrl + /api/v1/sync/pull?peer_id={peerId}&limit=100
     b. For each change in response.changes:
        - POST local /api/v1/sync/push with {peer_id: peerId, changes: [change]}
        - Track accepted/rejected counts
     c. If response.has_more, repeat pull
  6. PUSH phase:
     a. GET local /api/v1/sync/pull?peer_id={remotePeerId}&limit=100
        (get local changes unsent to remote)
     b. POST peerUrl + /api/v1/sync/push with {peer_id: peerId, changes: [...]}
     c. Track accepted/rejected from response
     d. If has_more, repeat push
  7. Update stats:
     - state.sync.stats.pulled += accepted count from pull
     - state.sync.stats.pushed += accepted count from push
     - state.sync.stats.conflicts += rejected count (both directions)
     - state.sync.lastSyncAt = new Date()
     - Persist lastSyncAt to localStorage
  8. Detect conflicts:
     - Rejected changes with reason containing "conflict" go to state.sync.conflicts[]
  9. Reload local data: loadNotes(), _loadCategories(), _loadNoteCategoryMappings()
  10. Update UI: remove .syncing, updateSyncStatus('synced', 'Synced'), renderSyncStats()
  11. Set state.sync._running = false
```

#### d. CSS (`app.css`)

```css
#btn-sync.syncing #sync-icon {
  animation: spin 1s linear infinite;
}
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
```

---

## 3. Sync Stats Display

### Goal
Show at-a-glance sync health: when the last sync happened, how many notes moved in each direction, and whether there are unresolved conflicts.

### UI Locations

**A. Status bar (always visible)** — compact one-line summary
**B. Filter panel Sync section** — expanded stats
**C. Conflict resolution popup** — modal for resolving conflicts

### Design

#### A. Status Bar (`status_bar.go`)

Replace the current left-section sync status with a richer display:

```
┌────────────────────────────────────────────────────────────────┐
│ ✓ Synced 3m ago  ↓2 ↑5        │ Filters: ...  │   42 notes   │
└────────────────────────────────────────────────────────────────┘
```

- `✓ Synced 3m ago` — relative time since last sync (or "Never" if no sync yet)
- `↓2` — notes received (pulled) in last sync
- `↑5` — notes pushed in last sync
- If conflicts > 0: append `⚠ 1 conflict` in warning color, clickable to open conflict modal

Updated markup in `status_bar.go`:
```go
b.DivClass("status-left").R(
  b.Div("class", "sync-status synced", "id", "sync-status").R(
    b.Span("id", "sync-status-icon").T("✓"),
    b.Span("id", "sync-status-text").T("Ready"),
  ),
  b.Span("class", "sync-stat", "id", "sync-stat-pulled", "title", "Notes received").T(""),
  b.Span("class", "sync-stat", "id", "sync-stat-pushed", "title", "Notes pushed").T(""),
  b.Span("class", "sync-stat sync-conflicts", "id", "sync-stat-conflicts",
    "onclick", "app.showConflicts()", "title", "Unresolved conflicts").T(""),
)
```

#### B. Filter Panel Stats (`filter_panel.go`)

Below the "Sync Now" button, add a stats summary block:

```
│  Last sync: 2 min ago               │
│  Received: 2  Pushed: 5             │
│  Conflicts: 1  [Resolve]            │
```

Markup:
```go
b.Div("class", "sync-stats", "id", "sync-stats").R(
  b.Div("class", "sync-stat-row", "id", "sync-last-time").T("Last sync: Never"),
  b.Div("class", "sync-stat-row").R(
    b.Span("id", "sync-received").T("Received: 0"),
    b.Span("class", "sync-stat-sep").T(" "),
    b.Span("id", "sync-pushed").T("Pushed: 0"),
  ),
  b.Div("class", "sync-stat-row", "id", "sync-conflict-row", "style", "display:none").R(
    b.Span("class", "text-warning", "id", "sync-conflict-count").T("Conflicts: 0"),
    b.Button("class", "btn-link text-warning", "onclick", "app.showConflicts()").T("Resolve"),
  ),
)
```

#### C. Conflict Resolution Modal

Reuse the existing modal overlay (`#modal-overlay`). When user clicks "Resolve" or the conflict badge:

```
┌─ Sync Conflicts ─────────────────────────────────────────┐
│                                                           │
│  Note: "Meeting Notes 2024-01-15"                        │
│  ┌──────────────────┬──────────────────┐                 │
│  │ Local version    │ Remote version   │                 │
│  │ Modified 2m ago  │ Modified 5m ago  │                 │
│  │ ...body preview..│ ...body preview..│                 │
│  └──────────────────┴──────────────────┘                 │
│  ( Keep Local )  ( Keep Remote )  ( Skip )               │
│                                                           │
│  ─────────────────────────────────────                   │
│  [Next conflict: "Project Ideas" →]                      │
│                                                           │
├───────────────────────────────────────────────────────────┤
│                                              [ Done ]     │
└───────────────────────────────────────────────────────────┘
```

### Implementation Steps

#### a. JS: `renderSyncStats()` function (`app.js`)

Called after every sync completes and on page load (from localStorage):

```js
function renderSyncStats() {
  const s = state.sync;

  // Status bar
  const timeText = s.lastSyncAt
    ? formatRelativeTime(s.lastSyncAt.toISOString())
    : 'Never';
  document.getElementById('sync-status-text').textContent = 'Synced ' + timeText;
  document.getElementById('sync-stat-pulled').textContent =
    s.stats.pulled > 0 ? '↓' + s.stats.pulled : '';
  document.getElementById('sync-stat-pushed').textContent =
    s.stats.pushed > 0 ? '↑' + s.stats.pushed : '';

  // Conflict indicator
  const conflictEl = document.getElementById('sync-stat-conflicts');
  if (s.conflicts.length > 0) {
    conflictEl.textContent = '⚠ ' + s.conflicts.length + ' conflict' +
      (s.conflicts.length > 1 ? 's' : '');
    conflictEl.style.display = '';
  } else {
    conflictEl.style.display = 'none';
  }

  // Filter panel stats
  document.getElementById('sync-last-time').textContent = 'Last sync: ' + timeText;
  document.getElementById('sync-received').textContent = 'Received: ' + s.stats.pulled;
  document.getElementById('sync-pushed').textContent = 'Pushed: ' + s.stats.pushed;

  const conflictRow = document.getElementById('sync-conflict-row');
  if (s.conflicts.length > 0) {
    conflictRow.style.display = '';
    document.getElementById('sync-conflict-count').textContent =
      'Conflicts: ' + s.conflicts.length;
  } else {
    conflictRow.style.display = 'none';
  }
}
```

#### b. JS: `app.showConflicts()` — Conflict resolution modal (`app.js`)

```
function showConflicts():
  1. Set modal title to "Sync Conflicts (N)"
  2. Build modal body HTML with side-by-side comparison for first unresolved conflict
  3. Each conflict object: { entityType, entityGuid, localSnapshot, remoteSnapshot, reason }
  4. "Keep Local" — POST the local version as a push to peer, remove from conflicts[]
  5. "Keep Remote" — POST /api/v1/sync/push locally with remote snapshot, remove from conflicts[]
  6. "Skip" — leave in conflicts[], move to next
  7. Navigation: show current index / total, "Next →" button
  8. On close / "Done": renderSyncStats() to update badges
```

Uses the existing snapshot endpoint (`GET /api/v1/sync/snapshot?entity_type=note&entity_guid=...`) to fetch full entity for comparison.

#### c. CSS additions (`app.css`)

```css
/* Sync stats in status bar */
.sync-stat { margin-left: 8px; font-size: 12px; color: var(--text-secondary); }
.sync-stat:empty { display: none; }
.sync-conflicts { color: var(--warning); cursor: pointer; }
.sync-conflicts:hover { text-decoration: underline; }

/* Sync stats in filter panel */
.sync-stats { padding: 8px 0; font-size: 12px; color: var(--text-secondary); }
.sync-stat-row { margin-bottom: 4px; }
.sync-stat-sep { margin: 0 8px; }

/* Auto-sync toggle */
.sync-toggle { position: relative; display: inline-block; width: 36px; height: 20px; }
.sync-toggle input { opacity: 0; width: 0; height: 0; }
.sync-toggle .slider { position: absolute; inset: 0; background: var(--border-dark);
  border-radius: 20px; transition: var(--transition-fast); cursor: pointer; }
.sync-toggle .slider::before { content: ''; position: absolute; left: 2px; bottom: 2px;
  width: 16px; height: 16px; background: white; border-radius: 50%;
  transition: var(--transition-fast); }
.sync-toggle input:checked + .slider { background: var(--primary); }
.sync-toggle input:checked + .slider::before { transform: translateX(16px); }

/* Conflict resolution modal */
.conflict-compare { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin: 16px 0; }
.conflict-version { border: 1px solid var(--border-light); border-radius: var(--radius-md);
  padding: 12px; max-height: 300px; overflow-y: auto; }
.conflict-version h4 { font-size: 13px; margin-bottom: 8px; color: var(--text-secondary); }
.conflict-actions { display: flex; gap: 8px; justify-content: center; margin-top: 12px; }
```

---

## File Change Summary

| File | Changes |
|------|---------|
| `web/pages/landing/filter_panel.go` | Expand `renderSyncSection()`: add auto-sync toggle, interval select, peer URL input, "Sync Now" button, stats block, conflict resolve link |
| `web/pages/landing/status_bar.go` | Add `sync-stat-pulled`, `sync-stat-pushed`, `sync-stat-conflicts` spans to status-left |
| `web/pages/landing/toolbar.go` | No markup changes needed (existing button is sufficient) |
| `web/static/js/app.js` | Add `state.sync` object; rewrite `syncNotes()` with real pull/push; add `toggleAutoSync()`, `setSyncInterval()`, `setPeerUrl()`, `testPeerConnection()`, `renderSyncStats()`, `showConflicts()`, `resolveConflict()`; restore sync prefs on init |
| `web/static/css/app.css` | Add sync toggle switch, sync stats, spin animation, conflict modal styles |

---

## Implementation Order

1. **State & persistence** — Add `state.sync`, localStorage save/restore in `app.js`
2. **Filter panel markup** — Build the expanded Sync section in `filter_panel.go`
3. **CSS** — Add all new styles in `app.css`
4. **Manual sync** — Implement `syncNotes()` with real pull/push protocol
5. **Stats display** — Implement `renderSyncStats()`, wire to status bar and filter panel
6. **Auto-sync** — Implement `toggleAutoSync()`, timer management, interval selector
7. **Conflict detection** — Collect rejected changes during sync, populate `state.sync.conflicts`
8. **Conflict resolution modal** — Build `showConflicts()` with side-by-side compare and resolve actions
9. **Polish** — Spin animation, toast feedback, edge cases (peer offline, auth failure, network errors)
