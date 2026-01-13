// GoNotes Application JavaScript
// Handles all client-side interactivity for the landing page

(function() {
  'use strict';

  // Application state
  const state = {
    notes: [],
    categories: [],
    tags: [],
    currentNote: null,
    selectedNotes: new Set(),
    isEditing: false,
    filters: {
      search: '',
      categories: [],
      tags: [],
      privacy: 'all',
      date: 'all',
      unsynced: false
    },
    sort: {
      field: 'updated_at',
      order: 'desc'
    },
    user: null,
    lastSync: null
  };

  // API configuration
  const API_BASE = '/api/v1';

  // ============================================
  // API Helper Functions
  // ============================================

  function getAuthToken() {
    return localStorage.getItem('token');
  }

  function setAuthToken(token) {
    localStorage.setItem('token', token);
  }

  function clearAuthToken() {
    localStorage.removeItem('token');
  }

  async function apiRequest(endpoint, options = {}) {
    const token = getAuthToken();
    const headers = {
      'Content-Type': 'application/json',
      ...options.headers
    };

    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    try {
      const response = await fetch(`${API_BASE}${endpoint}`, {
        ...options,
        headers
      });

      const data = await response.json();

      if (!response.ok) {
        if (response.status === 401) {
          // Token expired or invalid
          clearAuthToken();
          window.location.href = '/login';
          return null;
        }
        throw new Error(data.error || 'Request failed');
      }

      return data;
    } catch (error) {
      console.error('API request error:', error);
      showToast(error.message, 'error');
      throw error;
    }
  }

  // ============================================
  // Authentication Functions
  // ============================================

  async function checkAuth() {
    const token = getAuthToken();
    if (!token) {
      window.location.href = '/login';
      return false;
    }

    try {
      const response = await apiRequest('/auth/me');
      if (response && response.data) {
        state.user = response.data;
        updateUserDisplay();
        return true;
      }
    } catch (error) {
      clearAuthToken();
      window.location.href = '/login';
      return false;
    }
    return false;
  }

  function updateUserDisplay() {
    if (state.user) {
      const avatar = document.getElementById('user-avatar');
      const username = document.getElementById('username-display');
      if (avatar) {
        avatar.textContent = (state.user.username || 'U').charAt(0).toUpperCase();
      }
      if (username) {
        username.textContent = state.user.username || '';
      }
    }
  }

  window.app = window.app || {};
  window.app.logout = function() {
    clearAuthToken();
    window.location.href = '/login';
  };

  // ============================================
  // Notes CRUD Functions
  // ============================================

  async function loadNotes() {
    updateSyncStatus('syncing', 'Loading...');
    try {
      const response = await apiRequest('/notes?limit=100');
      if (response && response.data) {
        state.notes = response.data;
        extractTagsFromNotes();
        renderNoteList();
        updateResultCount();
        updateSyncStatus('synced', 'Synced');
      }
    } catch (error) {
      updateSyncStatus('error', 'Failed to load');
    }
  }

  async function loadCategories() {
    try {
      const response = await apiRequest('/categories');
      if (response && response.data) {
        state.categories = response.data;
        renderCategoriesList();
        populateCategorySelect();
      }
    } catch (error) {
      console.error('Failed to load categories:', error);
    }
  }

  function extractTagsFromNotes() {
    const tagCounts = {};
    state.notes.forEach(note => {
      if (note.tags) {
        const tags = note.tags.split(',').map(t => t.trim()).filter(t => t);
        tags.forEach(tag => {
          tagCounts[tag] = (tagCounts[tag] || 0) + 1;
        });
      }
    });
    state.tags = Object.entries(tagCounts)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 20);
    renderTagsList();
  }

  window.app.newNote = function() {
    state.currentNote = null;
    state.isEditing = true;
    clearEditForm();
    document.getElementById('edit-guid').value = generateGUID();
    showEditMode();
  };

  window.app.editNote = function(noteId) {
    const note = state.notes.find(n => n.id === noteId);
    if (note) {
      state.currentNote = note;
      state.isEditing = true;
      populateEditForm(note);
      showEditMode();
    }
  };

  window.app.editCurrentNote = function() {
    if (state.currentNote) {
      window.app.editNote(state.currentNote.id);
    }
  };

  window.app.saveNote = async function(event) {
    event.preventDefault();

    const form = document.getElementById('edit-form');
    const formData = new FormData(form);

    const noteData = {
      guid: formData.get('guid'),
      title: formData.get('title'),
      description: formData.get('description') || null,
      body: formData.get('body') || null,
      tags: formData.get('tags') || null,
      is_private: document.getElementById('edit-private').checked
    };

    if (!noteData.title.trim()) {
      showToast('Title is required', 'error');
      return false;
    }

    const saveBtn = document.getElementById('btn-save');
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving...';

    try {
      let response;
      if (state.currentNote && state.currentNote.id) {
        // Update existing note
        response = await apiRequest(`/notes/${state.currentNote.id}`, {
          method: 'PUT',
          body: JSON.stringify(noteData)
        });
      } else {
        // Create new note
        response = await apiRequest('/notes', {
          method: 'POST',
          body: JSON.stringify(noteData)
        });
      }

      if (response && response.data) {
        showToast('Note saved successfully', 'success');
        await loadNotes();

        // Select the saved note
        state.currentNote = response.data;
        window.app.selectNote(response.data.id);
        showPreviewMode();
      }
    } catch (error) {
      showToast('Failed to save note', 'error');
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save';
    }

    return false;
  };

  window.app.cancelEdit = function() {
    state.isEditing = false;
    if (state.currentNote) {
      showPreviewMode();
      renderPreview(state.currentNote);
    } else {
      showPreviewMode();
    }
  };

  window.app.deleteCurrentNote = async function() {
    if (!state.currentNote) return;

    if (!confirm('Are you sure you want to delete this note?')) {
      return;
    }

    try {
      await apiRequest(`/notes/${state.currentNote.id}`, {
        method: 'DELETE'
      });

      showToast('Note deleted', 'success');
      state.currentNote = null;
      await loadNotes();
      clearPreview();
    } catch (error) {
      showToast('Failed to delete note', 'error');
    }
  };

  window.app.duplicateCurrentNote = async function() {
    if (!state.currentNote) return;

    const noteData = {
      guid: generateGUID(),
      title: state.currentNote.title + ' (Copy)',
      description: state.currentNote.description,
      body: state.currentNote.body,
      tags: state.currentNote.tags,
      is_private: state.currentNote.is_private
    };

    try {
      const response = await apiRequest('/notes', {
        method: 'POST',
        body: JSON.stringify(noteData)
      });

      if (response && response.data) {
        showToast('Note duplicated', 'success');
        await loadNotes();
        window.app.selectNote(response.data.id);
      }
    } catch (error) {
      showToast('Failed to duplicate note', 'error');
    }
  };

  // ============================================
  // Note Selection and Preview
  // ============================================

  window.app.selectNote = function(noteId) {
    const note = state.notes.find(n => n.id === noteId);
    if (note) {
      state.currentNote = note;
      state.isEditing = false;

      // Update selected state in UI
      document.querySelectorAll('.note-row').forEach(row => {
        row.classList.remove('selected');
      });
      const selectedRow = document.querySelector(`.note-row[data-id="${noteId}"]`);
      if (selectedRow) {
        selectedRow.classList.add('selected');
      }

      showPreviewMode();
      renderPreview(note);
    }
  };

  window.app.previewNote = function(noteId) {
    window.app.selectNote(noteId);
  };

  function renderPreview(note) {
    document.getElementById('preview-title').textContent = note.title;
    document.getElementById('preview-footer').style.display = 'flex';

    // Render meta information
    const metaHtml = [];
    if (note.is_private) {
      metaHtml.push('<span class="preview-meta-item"><span>🔒</span> Private</span>');
    }
    if (note.tags) {
      const tags = note.tags.split(',').map(t => `<span class="note-tag">#${t.trim()}</span>`).join(' ');
      metaHtml.push(`<span class="preview-meta-item">${tags}</span>`);
    }
    metaHtml.push(`<span class="preview-meta-item">Modified: ${formatRelativeTime(note.updated_at)}</span>`);
    document.getElementById('preview-meta').innerHTML = metaHtml.join('');

    // Render markdown content
    const content = note.body || '';
    const html = DOMPurify.sanitize(marked.parse(content));
    document.getElementById('preview-content').innerHTML = html || '<p class="text-muted">No content</p>';
  }

  function clearPreview() {
    document.getElementById('preview-title').textContent = 'Select a note';
    document.getElementById('preview-meta').innerHTML = '';
    document.getElementById('preview-content').innerHTML = '<p class="text-muted">Select a note from the list to preview its content.</p>';
    document.getElementById('preview-footer').style.display = 'none';
  }

  // ============================================
  // Note List Rendering
  // ============================================

  function renderNoteList() {
    const container = document.getElementById('note-list');
    const loadingState = document.getElementById('loading-state');
    const emptyState = document.getElementById('empty-state');

    // Get filtered and sorted notes
    const filteredNotes = getFilteredNotes();

    // Hide loading state
    if (loadingState) loadingState.classList.add('hidden');

    // Show empty state if no notes
    if (filteredNotes.length === 0) {
      if (emptyState) emptyState.classList.remove('hidden');
      // Remove any existing note rows
      container.querySelectorAll('.note-row').forEach(row => row.remove());
      return;
    }

    // Hide empty state
    if (emptyState) emptyState.classList.add('hidden');

    // Create fragment for better performance
    const fragment = document.createDocumentFragment();

    filteredNotes.forEach(note => {
      const row = createNoteRow(note);
      fragment.appendChild(row);
    });

    // Remove old rows and append new ones
    container.querySelectorAll('.note-row').forEach(row => row.remove());
    container.appendChild(fragment);
  }

  function createNoteRow(note) {
    const row = document.createElement('div');
    row.className = 'note-row' + (state.currentNote?.id === note.id ? ' selected' : '');
    row.dataset.id = note.id;
    row.onclick = () => window.app.selectNote(note.id);

    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'note-checkbox';
    checkbox.checked = state.selectedNotes.has(note.id);
    checkbox.onclick = (e) => {
      e.stopPropagation();
      window.app.toggleNoteSelection(note.id);
    };

    const content = document.createElement('div');
    content.className = 'note-content';

    // Title row
    const titleRow = document.createElement('div');
    titleRow.className = 'note-title-row';
    if (note.is_private) {
      const privacyIcon = document.createElement('span');
      privacyIcon.className = 'note-privacy-icon';
      privacyIcon.title = 'Private note';
      privacyIcon.textContent = '🔒';
      titleRow.appendChild(privacyIcon);
    }
    const title = document.createElement('span');
    title.className = 'note-title';
    title.textContent = note.title;
    titleRow.appendChild(title);

    // Meta row (tags and categories)
    const meta = document.createElement('div');
    meta.className = 'note-meta';

    if (note.tags) {
      const tagsDiv = document.createElement('div');
      tagsDiv.className = 'note-tags';
      note.tags.split(',').forEach(tag => {
        const tagSpan = document.createElement('span');
        tagSpan.className = 'note-tag';
        tagSpan.textContent = '#' + tag.trim();
        tagSpan.onclick = (e) => {
          e.stopPropagation();
          window.app.filterByTag(tag.trim());
        };
        tagsDiv.appendChild(tagSpan);
      });
      meta.appendChild(tagsDiv);
    }

    // Preview
    const preview = document.createElement('div');
    preview.className = 'note-preview';
    preview.textContent = (note.body || '').substring(0, 100) + ((note.body?.length || 0) > 100 ? '...' : '');

    // Footer
    const footer = document.createElement('div');
    footer.className = 'note-footer';

    const timestamp = document.createElement('span');
    timestamp.className = 'note-timestamp';
    timestamp.textContent = formatRelativeTime(note.updated_at);

    const actions = document.createElement('div');
    actions.className = 'note-actions';

    const viewBtn = document.createElement('button');
    viewBtn.className = 'note-action-btn';
    viewBtn.title = 'Preview';
    viewBtn.textContent = '👁';
    viewBtn.onclick = (e) => {
      e.stopPropagation();
      window.app.previewNote(note.id);
    };

    const editBtn = document.createElement('button');
    editBtn.className = 'note-action-btn';
    editBtn.title = 'Edit';
    editBtn.textContent = 'Edit';
    editBtn.onclick = (e) => {
      e.stopPropagation();
      window.app.editNote(note.id);
    };

    actions.appendChild(viewBtn);
    actions.appendChild(editBtn);
    footer.appendChild(timestamp);
    footer.appendChild(actions);

    content.appendChild(titleRow);
    content.appendChild(meta);
    content.appendChild(preview);
    content.appendChild(footer);

    row.appendChild(checkbox);
    row.appendChild(content);

    return row;
  }

  // ============================================
  // Filtering and Search
  // ============================================

  function getFilteredNotes() {
    let notes = [...state.notes];

    // Apply search filter
    if (state.filters.search) {
      const searchLower = state.filters.search.toLowerCase();
      notes = notes.filter(note =>
        note.title.toLowerCase().includes(searchLower) ||
        (note.body && note.body.toLowerCase().includes(searchLower)) ||
        (note.tags && note.tags.toLowerCase().includes(searchLower)) ||
        (note.description && note.description.toLowerCase().includes(searchLower))
      );
    }

    // Apply tag filters (OR logic)
    if (state.filters.tags.length > 0) {
      notes = notes.filter(note => {
        if (!note.tags) return false;
        const noteTags = note.tags.split(',').map(t => t.trim().toLowerCase());
        return state.filters.tags.some(tag => noteTags.includes(tag.toLowerCase()));
      });
    }

    // Apply privacy filter
    if (state.filters.privacy !== 'all') {
      notes = notes.filter(note =>
        state.filters.privacy === 'private' ? note.is_private : !note.is_private
      );
    }

    // Apply date filter
    if (state.filters.date !== 'all') {
      const now = new Date();
      let cutoff;
      switch (state.filters.date) {
        case 'today':
          cutoff = new Date(now.getFullYear(), now.getMonth(), now.getDate());
          break;
        case 'week':
          cutoff = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
          break;
        case 'month':
          cutoff = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
          break;
      }
      if (cutoff) {
        notes = notes.filter(note => new Date(note.updated_at) >= cutoff);
      }
    }

    // Apply sorting
    notes.sort((a, b) => {
      let valueA, valueB;
      switch (state.sort.field) {
        case 'title':
          valueA = a.title.toLowerCase();
          valueB = b.title.toLowerCase();
          break;
        case 'created_at':
          valueA = new Date(a.created_at).getTime();
          valueB = new Date(b.created_at).getTime();
          break;
        case 'updated_at':
        default:
          valueA = new Date(a.updated_at).getTime();
          valueB = new Date(b.updated_at).getTime();
          break;
      }

      if (state.sort.order === 'asc') {
        return valueA > valueB ? 1 : -1;
      } else {
        return valueA < valueB ? 1 : -1;
      }
    });

    return notes;
  }

  let searchDebounceTimer;
  window.app.handleSearch = function(value) {
    clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(() => {
      state.filters.search = value;
      renderNoteList();
      updateResultCount();
      updateActiveFilters();
    }, 300);
  };

  window.app.clearSearch = function() {
    document.getElementById('search-input').value = '';
    state.filters.search = '';
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.filterByTag = function(tag) {
    if (!state.filters.tags.includes(tag)) {
      state.filters.tags.push(tag);
      renderNoteList();
      updateResultCount();
      updateActiveFilters();
      renderTagsList();
    }
  };

  window.app.removeTagFilter = function(tag) {
    state.filters.tags = state.filters.tags.filter(t => t !== tag);
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
    renderTagsList();
  };

  window.app.toggleTagFilter = function(tag, checked) {
    if (checked) {
      if (!state.filters.tags.includes(tag)) {
        state.filters.tags.push(tag);
      }
    } else {
      state.filters.tags = state.filters.tags.filter(t => t !== tag);
    }
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.setPrivacyFilter = function(value) {
    state.filters.privacy = value;
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.setDateFilter = function(value) {
    state.filters.date = value;
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.toggleUnsyncedFilter = function(checked) {
    state.filters.unsynced = checked;
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.clearAllFilters = function() {
    state.filters = {
      search: '',
      categories: [],
      tags: [],
      privacy: 'all',
      date: 'all',
      unsynced: false
    };

    // Reset UI
    document.getElementById('search-input').value = '';
    document.querySelectorAll('input[name="privacy"]')[0].checked = true;
    document.querySelectorAll('input[name="date"]')[0].checked = true;
    document.getElementById('filter-unsynced').checked = false;

    renderNoteList();
    updateResultCount();
    updateActiveFilters();
    renderTagsList();
    renderCategoriesList();
  };

  // ============================================
  // Sorting
  // ============================================

  window.app.setSort = function(field) {
    if (state.sort.field === field) {
      state.sort.order = state.sort.order === 'asc' ? 'desc' : 'asc';
    } else {
      state.sort.field = field;
      state.sort.order = 'desc';
    }

    // Update label
    const labels = { updated_at: 'Modified', created_at: 'Created', title: 'Title' };
    document.getElementById('sort-label').textContent = labels[field] || field;

    window.app.toggleSortMenu();
    renderNoteList();
  };

  window.app.toggleSortMenu = function() {
    const menu = document.getElementById('sort-menu');
    menu.classList.toggle('open');
  };

  // ============================================
  // Batch Operations
  // ============================================

  window.app.toggleNoteSelection = function(noteId) {
    if (state.selectedNotes.has(noteId)) {
      state.selectedNotes.delete(noteId);
    } else {
      state.selectedNotes.add(noteId);
    }
    updateBatchActions();
    renderNoteList();
  };

  window.app.toggleSelectAll = function(checked) {
    if (checked) {
      getFilteredNotes().forEach(note => state.selectedNotes.add(note.id));
    } else {
      state.selectedNotes.clear();
    }
    updateBatchActions();
    renderNoteList();
  };

  function updateBatchActions() {
    const batchBar = document.getElementById('batch-actions');
    const count = state.selectedNotes.size;

    if (count > 0) {
      batchBar.classList.add('visible');
      document.getElementById('batch-count').textContent = `${count} selected`;
    } else {
      batchBar.classList.remove('visible');
    }
  }

  window.app.deleteSelected = async function() {
    if (!confirm(`Delete ${state.selectedNotes.size} notes?`)) return;

    for (const noteId of state.selectedNotes) {
      try {
        await apiRequest(`/notes/${noteId}`, { method: 'DELETE' });
      } catch (error) {
        console.error('Failed to delete note:', noteId);
      }
    }

    state.selectedNotes.clear();
    updateBatchActions();
    await loadNotes();
    showToast('Notes deleted', 'success');
  };

  // ============================================
  // UI Helpers
  // ============================================

  function renderCategoriesList() {
    const container = document.getElementById('categories-list');
    if (!container) return;

    if (state.categories.length === 0) {
      container.innerHTML = '<div class="text-muted">No categories</div>';
      return;
    }

    container.innerHTML = state.categories.map(cat => `
      <label class="filter-item">
        <input type="checkbox" class="filter-checkbox"
               ${state.filters.categories.includes(cat.name) ? 'checked' : ''}
               onchange="app.toggleCategoryFilter('${cat.name}', this.checked)">
        <span class="filter-label">${cat.name}</span>
      </label>
    `).join('');
  }

  function renderTagsList() {
    const container = document.getElementById('tags-list');
    if (!container) return;

    if (state.tags.length === 0) {
      container.innerHTML = '<div class="text-muted">No tags</div>';
      return;
    }

    container.innerHTML = state.tags.map(([tag, count]) => `
      <label class="filter-item">
        <input type="checkbox" class="filter-checkbox"
               ${state.filters.tags.includes(tag) ? 'checked' : ''}
               onchange="app.toggleTagFilter('${tag}', this.checked)">
        <span class="filter-label">${tag}</span>
        <span class="filter-count">${count}</span>
      </label>
    `).join('');
  }

  function populateCategorySelect() {
    const select = document.getElementById('edit-category');
    if (!select) return;

    select.innerHTML = '<option value="">Select category...</option>' +
      state.categories.map(cat => `<option value="${cat.id}">${cat.name}</option>`).join('');
  }

  function populateEditForm(note) {
    document.getElementById('edit-id').value = note.id;
    document.getElementById('edit-guid').value = note.guid;
    document.getElementById('edit-title').value = note.title;
    document.getElementById('edit-description').value = note.description || '';
    document.getElementById('edit-tags').value = note.tags || '';
    document.getElementById('edit-body').value = note.body || '';
    document.getElementById('edit-private').checked = note.is_private;
  }

  function clearEditForm() {
    document.getElementById('edit-id').value = '';
    document.getElementById('edit-guid').value = '';
    document.getElementById('edit-title').value = '';
    document.getElementById('edit-description').value = '';
    document.getElementById('edit-tags').value = '';
    document.getElementById('edit-body').value = '';
    document.getElementById('edit-private').checked = false;
  }

  function showEditMode() {
    document.getElementById('preview-mode').classList.add('hidden');
    document.getElementById('edit-mode').classList.add('active');
    document.getElementById('edit-title').focus();
  }

  function showPreviewMode() {
    document.getElementById('edit-mode').classList.remove('active');
    document.getElementById('preview-mode').classList.remove('hidden');
  }

  window.app.toggleSection = function(sectionId) {
    const section = document.getElementById(sectionId);
    if (section) {
      section.classList.toggle('collapsed');
    }
  };

  window.app.toggleUserMenu = function() {
    const menu = document.getElementById('user-menu');
    menu.classList.toggle('open');
  };

  function updateResultCount() {
    const filtered = getFilteredNotes().length;
    const total = state.notes.length;
    const countEl = document.getElementById('result-count');
    const viewCount = document.getElementById('view-count');

    if (countEl) {
      countEl.textContent = filtered === total
        ? `${total} notes`
        : `${filtered} of ${total} notes`;
    }
    if (viewCount) {
      viewCount.textContent = ` (${filtered})`;
    }
  }

  function updateActiveFilters() {
    const container = document.getElementById('active-filters');
    if (!container) return;

    const badges = [];

    if (state.filters.search) {
      badges.push(`<span class="filter-badge">Search: "${state.filters.search}"</span>`);
    }
    state.filters.tags.forEach(tag => {
      badges.push(`<span class="filter-badge">#${tag} <button onclick="app.removeTagFilter('${tag}')" style="background:none;border:none;cursor:pointer;">×</button></span>`);
    });
    if (state.filters.privacy !== 'all') {
      badges.push(`<span class="filter-badge">${state.filters.privacy}</span>`);
    }
    if (state.filters.date !== 'all') {
      badges.push(`<span class="filter-badge">${state.filters.date}</span>`);
    }

    container.innerHTML = badges.length > 0
      ? '<span>Filters: </span>' + badges.join(' ')
      : '';
  }

  function updateSyncStatus(status, text) {
    const statusEl = document.getElementById('sync-status');
    const iconEl = document.getElementById('sync-status-icon');
    const textEl = document.getElementById('sync-status-text');

    if (!statusEl) return;

    statusEl.className = 'sync-status ' + status;

    const icons = { synced: '✓', syncing: '↻', pending: '⚠', error: '✕' };
    if (iconEl) iconEl.textContent = icons[status] || '?';
    if (textEl) textEl.textContent = text;
  }

  window.app.syncNotes = async function() {
    await loadNotes();
    await loadCategories();
  };

  // ============================================
  // Modal Dialogs
  // ============================================

  window.app.closeModal = function() {
    document.getElementById('modal-overlay').classList.remove('open');
  };

  window.app.confirmModal = function() {
    window.app.closeModal();
  };

  window.app.showSettings = function() {
    showToast('Settings coming soon', 'warning');
  };

  // ============================================
  // Toast Notifications
  // ============================================

  function showToast(message, type = 'info') {
    const container = document.getElementById('toast-container');
    if (!container) return;

    const toast = document.createElement('div');
    toast.className = 'toast ' + type;
    toast.innerHTML = `
      <span>${message}</span>
      <button class="toast-close" onclick="this.parentElement.remove()">×</button>
    `;
    container.appendChild(toast);

    setTimeout(() => {
      toast.style.animation = 'slideOut 0.3s ease forwards';
      setTimeout(() => toast.remove(), 300);
    }, 3000);
  }

  // ============================================
  // Utility Functions
  // ============================================

  function generateGUID() {
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
      const r = Math.random() * 16 | 0;
      const v = c === 'x' ? r : (r & 0x3 | 0x8);
      return v.toString(16);
    });
  }

  function formatRelativeTime(dateString) {
    const date = new Date(dateString);
    const now = new Date();
    const diffMs = now - date;
    const diffSec = Math.floor(diffMs / 1000);
    const diffMin = Math.floor(diffSec / 60);
    const diffHour = Math.floor(diffMin / 60);
    const diffDay = Math.floor(diffHour / 24);

    if (diffSec < 60) return 'Just now';
    if (diffMin < 60) return `${diffMin}m ago`;
    if (diffHour < 24) return `${diffHour}h ago`;
    if (diffDay < 7) return `${diffDay}d ago`;

    return date.toLocaleDateString();
  }

  // ============================================
  // Keyboard Shortcuts
  // ============================================

  document.addEventListener('keydown', function(e) {
    // Don't trigger shortcuts when typing in inputs
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') {
      // Allow Escape to cancel edit
      if (e.key === 'Escape' && state.isEditing) {
        window.app.cancelEdit();
      }
      // Allow Ctrl+S to save
      if ((e.ctrlKey || e.metaKey) && e.key === 's' && state.isEditing) {
        e.preventDefault();
        document.getElementById('edit-form').dispatchEvent(new Event('submit'));
      }
      return;
    }

    // Focus search with /
    if (e.key === '/') {
      e.preventDefault();
      document.getElementById('search-input').focus();
    }

    // New note with n
    if (e.key === 'n') {
      e.preventDefault();
      window.app.newNote();
    }

    // Edit current note with e
    if (e.key === 'e' && state.currentNote) {
      e.preventDefault();
      window.app.editCurrentNote();
    }
  });

  // ============================================
  // Close dropdowns when clicking outside
  // ============================================

  document.addEventListener('click', function(e) {
    if (!e.target.closest('.dropdown')) {
      document.querySelectorAll('.dropdown-menu.open').forEach(menu => {
        menu.classList.remove('open');
      });
    }
  });

  // ============================================
  // Initialize Application
  // ============================================

  async function init() {
    const isAuthenticated = await checkAuth();
    if (!isAuthenticated) return;

    await Promise.all([
      loadNotes(),
      loadCategories()
    ]);
  }

  // Start the app when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

})();
