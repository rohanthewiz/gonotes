// Sync module for GoNotes
// Handles: peer sync (pull/push), auto-sync timer, sync stats, conflict resolution
//
// Dependencies: Loaded after app.js. Accesses shared internals via
// window.app._internal which app.js exposes before DOMContentLoaded.

(function() {
  'use strict';

  // Lazy accessors for shared internals
  function getState()              { return window.app._internal.state; }
  function apiRequest(ep, opts)    { return window.app._internal.apiRequest(ep, opts); }
  function showToast(msg, type)    { return window.app._internal.showToast(msg, type); }
  function escapeHtml(t)           { return window.app._internal.escapeHtml(t); }
  function updateSyncStatus(s, t)  { return window.app._internal.updateSyncStatus(s, t); }
  function loadNotes()             { return window.app._internal.loadNotes(); }
  function renderNoteList()        { return window.app._internal.renderNoteList(); }
  function generateGUID()          { return window.app._internal.generateGUID(); }
  function formatRelativeTime(d)   { return window.app._internal.formatRelativeTime(d); }

  // ============================================
  // LocalStorage Persistence
  // ============================================

  function saveSyncPrefs() {
    const s = getState().sync;
    localStorage.setItem('sync_auto_enabled', JSON.stringify(s.autoEnabled));
    localStorage.setItem('sync_interval_ms', JSON.stringify(s.intervalMs));
    localStorage.setItem('sync_peer_url', s.peerUrl);
    localStorage.setItem('sync_peer_id', s.peerId);
    if (s.lastSyncAt) {
      localStorage.setItem('sync_last_sync_at', s.lastSyncAt.toISOString());
    }
  }

  function restoreSyncPrefs() {
    const s = getState().sync;

    const autoEnabled = localStorage.getItem('sync_auto_enabled');
    if (autoEnabled !== null) {
      s.autoEnabled = JSON.parse(autoEnabled);
    }

    const intervalMs = localStorage.getItem('sync_interval_ms');
    if (intervalMs !== null) {
      s.intervalMs = JSON.parse(intervalMs);
    }

    const peerUrl = localStorage.getItem('sync_peer_url');
    if (peerUrl !== null) {
      s.peerUrl = peerUrl;
    }

    let peerId = localStorage.getItem('sync_peer_id');
    if (!peerId) {
      peerId = generateGUID();
      localStorage.setItem('sync_peer_id', peerId);
    }
    s.peerId = peerId;

    const lastSyncAt = localStorage.getItem('sync_last_sync_at');
    if (lastSyncAt) {
      s.lastSyncAt = new Date(lastSyncAt);
    }
  }

  // ============================================
  // Auto-Sync Timer Management
  // ============================================

  function startTimer() {
    const s = getState().sync;
    stopTimer();
    s.timerId = setInterval(function() {
      window.app.syncNotes();
    }, s.intervalMs);
  }

  function stopTimer() {
    const s = getState().sync;
    if (s.timerId !== null) {
      clearInterval(s.timerId);
      s.timerId = null;
    }
  }

  // ============================================
  // Peer Configuration
  // ============================================

  window.app.setPeerUrl = function(url) {
    const s = getState().sync;
    // Basic URL validation
    url = url.trim();
    // Remove trailing slash
    if (url.endsWith('/')) {
      url = url.slice(0, -1);
    }
    s.peerUrl = url;
    saveSyncPrefs();
  };

  window.app.testPeerConnection = async function() {
    const s = getState().sync;
    if (!s.peerUrl) {
      showToast('Enter a peer URL first', 'warning');
      return;
    }

    const testBtn = document.getElementById('sync-test-btn');
    if (testBtn) {
      testBtn.disabled = true;
      testBtn.textContent = '...';
    }

    try {
      const response = await fetch(s.peerUrl + '/api/v1/health', {
        method: 'GET',
        mode: 'cors'
      });
      const data = await response.json();
      if (response.ok && data.data && data.data.status === 'ok') {
        showToast('Peer connection successful', 'success');
      } else {
        showToast('Peer responded but status is not OK', 'warning');
      }
    } catch (err) {
      showToast('Cannot reach peer: ' + err.message, 'error');
    } finally {
      if (testBtn) {
        testBtn.disabled = false;
        testBtn.textContent = 'Test';
      }
    }
  };

  // ============================================
  // Auto-Sync Toggle & Interval
  // ============================================

  window.app.toggleAutoSync = function(enabled) {
    const s = getState().sync;
    s.autoEnabled = enabled;

    if (enabled) {
      if (!s.peerUrl) {
        showToast('Configure peer URL first', 'warning');
        s.autoEnabled = false;
        const toggle = document.getElementById('auto-sync-toggle');
        if (toggle) toggle.checked = false;
        return;
      }
      startTimer();
      showToast('Auto-sync enabled', 'success');
    } else {
      stopTimer();
      showToast('Auto-sync disabled', 'info');
    }
    saveSyncPrefs();
  };

  window.app.setSyncInterval = function(minutes) {
    const s = getState().sync;
    s.intervalMs = parseInt(minutes, 10) * 60 * 1000;
    if (s.autoEnabled) {
      startTimer(); // restarts with new interval
    }
    saveSyncPrefs();
  };

  // ============================================
  // Core Sync Protocol (Pull + Push)
  // ============================================

  async function _pullFromPeer() {
    const s = getState().sync;
    const token = localStorage.getItem('token');
    let totalAccepted = 0;
    let totalRejected = 0;
    let hasMore = true;

    while (hasMore) {
      // Pull changes from peer
      const pullResp = await fetch(s.peerUrl + '/api/v1/sync/pull?peer_id=' + encodeURIComponent(s.peerId) + '&limit=100', {
        method: 'GET',
        mode: 'cors',
        headers: {
          'Authorization': 'Bearer ' + token,
          'Content-Type': 'application/json'
        }
      });

      if (!pullResp.ok) {
        throw new Error('Pull failed: ' + pullResp.status);
      }

      const pullData = await pullResp.json();
      const changes = pullData.data ? pullData.data.changes : (pullData.changes || []);
      hasMore = pullData.data ? pullData.data.has_more : (pullData.has_more || false);

      if (changes.length === 0) break;

      // Push each pulled change to local server
      const pushResp = await apiRequest('/sync/push', {
        method: 'POST',
        body: JSON.stringify({
          peer_id: s.peerId,
          changes: changes
        })
      });

      if (pushResp && pushResp.data) {
        totalAccepted += (pushResp.data.accepted || []).length;
        totalRejected += (pushResp.data.rejected || []).length;

        // Collect conflicts from rejected changes
        (pushResp.data.rejected || []).forEach(function(rej) {
          s.conflicts.push({
            guid: rej.guid,
            reason: rej.reason,
            // Find the corresponding change for context
            change: changes.find(function(c) { return c.guid === rej.guid; }) || null
          });
        });
      }
    }

    return { accepted: totalAccepted, rejected: totalRejected };
  }

  async function _pushToPeer() {
    const s = getState().sync;
    const token = localStorage.getItem('token');
    let totalAccepted = 0;
    let totalRejected = 0;
    let hasMore = true;

    while (hasMore) {
      // Get local changes to push (unsent to the remote peer)
      const localResp = await apiRequest('/sync/pull?peer_id=' + encodeURIComponent(s.peerUrl) + '&limit=100');

      if (!localResp || !localResp.data) break;

      const changes = localResp.data.changes || [];
      hasMore = localResp.data.has_more || false;

      if (changes.length === 0) break;

      // Push to remote peer
      const pushResp = await fetch(s.peerUrl + '/api/v1/sync/push', {
        method: 'POST',
        mode: 'cors',
        headers: {
          'Authorization': 'Bearer ' + token,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          peer_id: s.peerId,
          changes: changes
        })
      });

      if (!pushResp.ok) {
        throw new Error('Push failed: ' + pushResp.status);
      }

      const pushData = await pushResp.json();
      const result = pushData.data || pushData;

      totalAccepted += (result.accepted || []).length;
      totalRejected += (result.rejected || []).length;

      // Collect conflicts from rejected pushes
      (result.rejected || []).forEach(function(rej) {
        s.conflicts.push({
          guid: rej.guid,
          reason: rej.reason,
          change: changes.find(function(c) { return c.guid === rej.guid; }) || null
        });
      });
    }

    return { accepted: totalAccepted, rejected: totalRejected };
  }

  async function _runSync() {
    const s = getState().sync;

    // PULL phase
    const pullResult = await _pullFromPeer();

    // PUSH phase
    const pushResult = await _pushToPeer();

    // Update stats
    s.stats.pulled = pullResult.accepted;
    s.stats.pushed = pushResult.accepted;
    s.stats.conflicts = s.conflicts.length;
    s.lastSyncAt = new Date();
    saveSyncPrefs();

    // Reload local data
    await loadNotes();
    await window.app._loadCategories();
    await window.app._loadNoteCategoryMappings();
    renderNoteList();
  }

  window.app.syncNotes = async function() {
    const s = getState().sync;

    // If no peer URL is configured, fall back to local reload
    if (!s.peerUrl) {
      await loadNotes();
      await window.app._loadCategories();
      await window.app._loadNoteCategoryMappings();
      renderNoteList();
      return;
    }

    // Guard against concurrent syncs
    if (s._running) return;
    s._running = true;

    // Update UI
    const syncBtn = document.getElementById('btn-sync');
    if (syncBtn) syncBtn.classList.add('syncing');
    updateSyncStatus('syncing', 'Syncing...');

    try {
      await _runSync();
      updateSyncStatus('synced', 'Synced');

      const totalChanges = s.stats.pulled + s.stats.pushed;
      if (totalChanges > 0) {
        showToast('Sync complete: ' + s.stats.pulled + ' received, ' + s.stats.pushed + ' sent', 'success');
      } else {
        showToast('Already up to date', 'success');
      }
    } catch (err) {
      console.error('Sync error:', err);
      updateSyncStatus('error', 'Sync failed');
      showToast('Sync failed: ' + err.message, 'error');
    } finally {
      s._running = false;
      const syncBtn = document.getElementById('btn-sync');
      if (syncBtn) syncBtn.classList.remove('syncing');
      renderSyncStats();
    }
  };

  // ============================================
  // Sync Stats Rendering
  // ============================================

  function renderSyncStats() {
    const s = getState().sync;

    // Status bar
    const timeText = s.lastSyncAt
      ? formatRelativeTime(s.lastSyncAt.toISOString())
      : 'Never';

    const statusText = document.getElementById('sync-status-text');
    if (statusText && s.lastSyncAt) {
      statusText.textContent = 'Synced ' + timeText;
    }

    const pulledEl = document.getElementById('sync-stat-pulled');
    if (pulledEl) {
      pulledEl.textContent = s.stats.pulled > 0 ? '\u2193' + s.stats.pulled : '';
    }

    const pushedEl = document.getElementById('sync-stat-pushed');
    if (pushedEl) {
      pushedEl.textContent = s.stats.pushed > 0 ? '\u2191' + s.stats.pushed : '';
    }

    // Conflict indicator in status bar
    const conflictEl = document.getElementById('sync-stat-conflicts');
    if (conflictEl) {
      if (s.conflicts.length > 0) {
        conflictEl.textContent = '\u26A0 ' + s.conflicts.length + ' conflict' +
          (s.conflicts.length > 1 ? 's' : '');
        conflictEl.style.display = '';
      } else {
        conflictEl.textContent = '';
        conflictEl.style.display = 'none';
      }
    }

    // Filter panel stats
    const lastTimeEl = document.getElementById('sync-last-time');
    if (lastTimeEl) {
      lastTimeEl.textContent = 'Last sync: ' + timeText;
    }

    const receivedEl = document.getElementById('sync-received');
    if (receivedEl) {
      receivedEl.textContent = 'Received: ' + s.stats.pulled;
    }

    const sentEl = document.getElementById('sync-pushed');
    if (sentEl) {
      sentEl.textContent = 'Pushed: ' + s.stats.pushed;
    }

    const conflictRow = document.getElementById('sync-conflict-row');
    if (conflictRow) {
      if (s.conflicts.length > 0) {
        conflictRow.style.display = '';
        const countEl = document.getElementById('sync-conflict-count');
        if (countEl) {
          countEl.textContent = 'Conflicts: ' + s.conflicts.length;
        }
      } else {
        conflictRow.style.display = 'none';
      }
    }
  }

  // ============================================
  // Conflict Resolution UI
  // ============================================

  let currentConflictIndex = 0;

  window.app.showConflicts = function() {
    const s = getState().sync;
    if (s.conflicts.length === 0) {
      showToast('No conflicts to resolve', 'info');
      return;
    }

    currentConflictIndex = 0;
    renderConflictModal();
  };

  function renderConflictModal() {
    const s = getState().sync;
    const conflict = s.conflicts[currentConflictIndex];
    if (!conflict) return;

    const modalTitle = document.getElementById('modal-title');
    const modalBody = document.getElementById('modal-body');
    const modalFooter = document.getElementById('modal-footer');

    modalTitle.textContent = 'Sync Conflicts (' + s.conflicts.length + ')';

    const entityType = conflict.change ? conflict.change.entity_type : 'unknown';
    const entityGuid = conflict.change ? conflict.change.entity_guid : 'unknown';
    const reason = conflict.reason || 'Unknown conflict';

    let fragmentPreview = '';
    if (conflict.change && conflict.change.fragment) {
      const frag = conflict.change.fragment;
      if (frag.title) {
        fragmentPreview = escapeHtml(frag.title);
      } else if (frag.name) {
        fragmentPreview = escapeHtml(frag.name);
      } else {
        fragmentPreview = 'Entity: ' + escapeHtml(entityGuid);
      }
    } else {
      fragmentPreview = 'Entity: ' + escapeHtml(entityGuid);
    }

    modalBody.innerHTML =
      '<div style="margin-bottom: 12px;">' +
        '<strong>Conflict ' + (currentConflictIndex + 1) + ' of ' + s.conflicts.length + '</strong>' +
      '</div>' +
      '<div style="margin-bottom: 12px;">' +
        '<div><strong>Type:</strong> ' + escapeHtml(entityType) + '</div>' +
        '<div><strong>Entity:</strong> ' + fragmentPreview + '</div>' +
        '<div><strong>Reason:</strong> ' + escapeHtml(reason) + '</div>' +
      '</div>' +
      '<div class="conflict-actions">' +
        '<button class="btn btn-secondary" onclick="app.resolveConflict(' + currentConflictIndex + ', \'skip\')">Skip</button>' +
        '<button class="btn btn-primary" onclick="app.resolveConflict(' + currentConflictIndex + ', \'dismiss\')">Dismiss</button>' +
      '</div>' +
      (s.conflicts.length > 1 ?
        '<div style="margin-top: 16px; text-align: center;">' +
          (currentConflictIndex > 0 ?
            '<button class="btn btn-secondary" onclick="app._prevConflict()" style="margin-right: 8px;">\u2190 Previous</button>' : '') +
          (currentConflictIndex < s.conflicts.length - 1 ?
            '<button class="btn btn-secondary" onclick="app._nextConflict()">Next \u2192</button>' : '') +
        '</div>' : '');

    modalFooter.innerHTML =
      '<button class="btn btn-secondary" onclick="app.closeModal()">Done</button>';

    document.getElementById('modal-overlay').classList.add('open');
  }

  window.app._prevConflict = function() {
    if (currentConflictIndex > 0) {
      currentConflictIndex--;
      renderConflictModal();
    }
  };

  window.app._nextConflict = function() {
    const s = getState().sync;
    if (currentConflictIndex < s.conflicts.length - 1) {
      currentConflictIndex++;
      renderConflictModal();
    }
  };

  window.app.resolveConflict = async function(index, choice) {
    const s = getState().sync;

    if (choice === 'dismiss') {
      // Remove the conflict from the list
      s.conflicts.splice(index, 1);
      s.stats.conflicts = s.conflicts.length;

      if (s.conflicts.length === 0) {
        window.app.closeModal();
        renderSyncStats();
        showToast('All conflicts resolved', 'success');
        return;
      }

      // Adjust index if needed
      if (currentConflictIndex >= s.conflicts.length) {
        currentConflictIndex = s.conflicts.length - 1;
      }
      renderConflictModal();
      renderSyncStats();
    } else if (choice === 'skip') {
      // Move to next conflict
      if (currentConflictIndex < s.conflicts.length - 1) {
        currentConflictIndex++;
        renderConflictModal();
      } else {
        window.app.closeModal();
        showToast(s.conflicts.length + ' conflict' + (s.conflicts.length > 1 ? 's' : '') + ' remaining', 'warning');
      }
    }
  };

  // ============================================
  // Init Handler
  // ============================================

  // ============================================
  // Spoke sync: the prompt, the compaction, the exit
  //
  // This is a DIFFERENT sync from everything above. The code above is
  // browser-driven peer sync (this tab talking to another GoNotes over CORS).
  // What follows drives the SERVER's sync client — the hub-spoke background
  // client in models/sync_client.go — which since prompt mode became the
  // default no longer syncs on its own.
  //
  // The contract is one endpoint family:
  //
  //   GET  /sync/control/status    the clock: due, pending, mode, last sync
  //   POST /sync/control/sync-now  {compact:bool} — run a cycle
  //   POST /sync/control/snooze    defer the prompt without syncing
  //   POST /sync/control/compact   collapse the pending log without syncing
  //
  // Everything here is inert when the server reports enabled:false, which is
  // every installation that has not configured a hub.
  // ============================================

  // spokeStatus is the last status the server reported, or null before the
  // first poll. Kept module-level rather than on app state because nothing
  // else reads it and it is replaced wholesale on every poll.
  let spokeStatus = null;

  // spokePollMs is how often the clock is re-read. A minute is far finer than
  // the two-hour prompt interval it watches for and coarse enough to be one
  // request per minute.
  const spokePollMs = 60000;

  async function pollSpokeSync() {
    try {
      const resp = await apiRequest('/sync/control/status');
      spokeStatus = (resp && resp.data) ? resp.data : null;
    } catch (err) {
      // An unreachable server is a condition the rest of the UI already
      // reports. Leaving the last known status in place beats blanking the
      // banner on one failed poll.
      return;
    }
    renderSpokeBanner();
  }

  // spokeSummary phrases the state in the two terms a person holds: how much
  // is waiting, and how long it has been.
  function spokeSummary() {
    if (!spokeStatus) return '';
    const parts = [];
    if (spokeStatus.pending_changes > 0) {
      parts.push(spokeStatus.pending_changes +
        (spokeStatus.pending_changes === 1 ? ' change' : ' changes') + ' not synced');
    }
    if (!spokeStatus.last_sync) {
      parts.push('never synced with the hub');
    } else {
      parts.push('last synced ' + formatRelativeTime(spokeStatus.last_sync));
    }
    return parts.join(', ');
  }

  function renderSpokeBanner() {
    const el = document.getElementById('sync-due-banner');
    if (!el) return;

    const show = spokeStatus && spokeStatus.enabled && spokeStatus.due;
    if (!show) {
      el.hidden = true;
      el.innerHTML = '';
      return;
    }

    // The compacting answer is only offered when there is something to
    // compact, and never when the server compacts on every push anyway —
    // a button that does what is already happening is a button that teaches
    // the wrong thing about what it does.
    const offerCompact = spokeStatus.pending_changes > 1 && !spokeStatus.compact_before_push;

    el.innerHTML =
      '<span class="sync-due-text">\u27F2 Sync is due \u2014 ' + escapeHtml(spokeSummary()) + '.</span>' +
      '<span class="sync-due-actions">' +
        '<button class="btn btn-primary" onclick="app.spokeSyncNow(false)">Sync now</button>' +
        (offerCompact
          ? '<button class="btn btn-secondary" onclick="app.spokeSyncNow(true)" ' +
            'title="Collapse the pending change log to one change per note, then sync">Compact &amp; sync</button>'
          : '') +
        '<button class="btn btn-secondary" onclick="app.spokeSnooze()">Later</button>' +
      '</span>';
    el.hidden = false;
  }

  window.app.spokeSyncNow = async function(compact) {
    const banner = document.getElementById('sync-due-banner');
    if (banner) banner.hidden = true; // no second click while it runs
    updateSyncStatus('syncing', compact ? 'Compacting and syncing...' : 'Syncing...');

    try {
      const resp = await apiRequest('/sync/control/sync-now', {
        method: 'POST',
        body: JSON.stringify({ compact: !!compact })
      });
      spokeStatus = (resp && resp.data) ? resp.data : spokeStatus;
      updateSyncStatus('synced', 'Synced');
      showToast(compact ? 'Compacted and synced with the hub' : 'Synced with the hub', 'success');
      // The hub may have sent notes back, so the list on screen is stale.
      await loadNotes();
      renderNoteList();
    } catch (err) {
      updateSyncStatus('error', 'Sync failed');
      showToast('Sync failed: ' + err.message, 'error');
    } finally {
      renderSpokeBanner();
    }
  };

  window.app.spokeSnooze = async function() {
    try {
      const resp = await apiRequest('/sync/control/snooze', { method: 'POST', body: JSON.stringify({}) });
      spokeStatus = (resp && resp.data) ? resp.data : spokeStatus;
    } catch (err) {
      showToast('Could not defer the sync prompt: ' + err.message, 'error');
    }
    renderSpokeBanner();
  };

  // spokeCompactOnly collapses the log without syncing. Worth having on its
  // own precisely when the hub is unreachable, which is when the log grows.
  window.app.spokeCompactChanges = async function() {
    try {
      const resp = await apiRequest('/sync/control/compact', { method: 'POST', body: JSON.stringify({}) });
      const c = resp && resp.data ? resp.data.compaction : null;
      if (resp && resp.data && resp.data.status) spokeStatus = resp.data.status;
      if (c && c.changes_before > c.changes_after) {
        showToast('Packed ' + c.changes_before + ' pending changes into ' + c.changes_after, 'success');
      } else {
        showToast('Nothing to compact', 'info');
      }
    } catch (err) {
      showToast('Compaction failed: ' + err.message, 'error');
    }
    renderSpokeBanner();
  };

  // The exit half of "prompt after two hours, or on exit". A tab closing is
  // the web UI's exit, and an ordinary fetch there is cancelled the moment the
  // page goes away.
  //
  // fetch(keepalive) is the mechanism built for this: the browser keeps the
  // request in flight after the document is gone, and unlike sendBeacon it can
  // still carry the Authorization header — which matters, because the
  // alternative would be putting a JWT in a query string to work around a
  // header restriction.
  //
  // Fired only when something is actually pending, and best-effort throughout:
  // the server's own shutdown cycle and the next prompt both still cover the
  // case where it never arrives.
  function syncOnUnload() {
    if (!spokeStatus || !spokeStatus.enabled || spokeStatus.pending_changes === 0) return;

    const token = localStorage.getItem('token');
    if (!token) return;

    try {
      fetch('/api/v1/sync/control/sync-now', {
        method: 'POST',
        keepalive: true,
        headers: {
          'Authorization': 'Bearer ' + token,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ compact: false })
      });
    } catch (err) {
      // Nothing to report to a user who has already closed the tab.
    }
  }

  window.app._initSpokeSync = function() {
    pollSpokeSync();
    setInterval(pollSpokeSync, spokePollMs);
    window.addEventListener('pagehide', syncOnUnload);
  };

  window.app._initSyncHandlers = function() {
    // Initialize sync state on the shared app state
    getState().sync = {
      autoEnabled: false,
      intervalMs: 300000,       // 5 min default
      peerUrl: '',
      peerId: '',               // generated once, stored in localStorage
      timerId: null,            // setInterval handle
      lastSyncAt: null,         // Date object
      _running: false,          // guard against concurrent syncs
      stats: { pulled: 0, pushed: 0, conflicts: 0 },
      conflicts: []             // unresolved conflict objects
    };

    // Restore preferences from localStorage
    restoreSyncPrefs();

    // Populate UI from restored state
    const toggle = document.getElementById('auto-sync-toggle');
    if (toggle) {
      toggle.checked = getState().sync.autoEnabled;
    }

    const intervalSelect = document.getElementById('sync-interval');
    if (intervalSelect) {
      intervalSelect.value = String(getState().sync.intervalMs / 60000);
    }

    const peerInput = document.getElementById('sync-peer-url');
    if (peerInput) {
      peerInput.value = getState().sync.peerUrl;
    }

    // If auto-sync was enabled and peer URL is set, restart the timer
    if (getState().sync.autoEnabled && getState().sync.peerUrl) {
      startTimer();
    }

    // Render initial sync stats from persisted state
    renderSyncStats();

    // The server-side spoke sync is independent of everything above and has
    // its own clock to watch.
    window.app._initSpokeSync();
  };

})();
