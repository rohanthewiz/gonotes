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
  };

})();
