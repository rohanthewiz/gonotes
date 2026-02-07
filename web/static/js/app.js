// GoNotes Application JavaScript
// Handles all client-side interactivity for the landing page

(function() {
  'use strict';

  // Application state
  const state = {
    notes: [],
    categories: [],
    noteCategoryMap: {},  // { noteId: [{ categoryId, categoryName, subcategories }] }
    currentNote: null,
    selectedNotes: new Set(),
    isEditing: false,
    filters: {
      search: '',
      categoryId: null,      // selected category ID from search bar dropdown
      categoryName: '',       // selected category name (for display)
      subcategories: [],      // selected subcategory chips (AND logic)
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
  // Markdown Configuration with Syntax Highlighting
  // ============================================

  // Configure Marked.js to use highlight.js for code blocks.
  // This provides syntax highlighting for Go, Python, JavaScript, TypeScript,
  // HTML, CSS, JSON, SQL, and Bash code blocks in note previews.
  function configureMarkdown() {
    const renderer = new marked.Renderer();

    // Custom code block renderer that integrates highlight.js
    // Design: Marked calls this synchronously for each fenced code block
    // Note: Marked v5+ passes a token object {text, lang, escaped} instead of separate params
    renderer.code = function(token) {
      const code = token.text || token;  // Handle both v5+ (object) and older (string) API
      const language = token.lang || '';
      // Normalize language identifier - handle null/undefined and trim whitespace
      const lang = (language || '').trim().toLowerCase();

      // Map common language aliases to highlight.js recognized names
      // This improves UX by accepting variations users commonly type
      const langMap = {
        'js': 'javascript',
        'ts': 'typescript',
        'sh': 'bash',
        'shell': 'bash',
        'py': 'python',
        'golang': 'go'
      };
      const resolvedLang = langMap[lang] || lang;

      // Apply syntax highlighting if highlight.js is available and knows the language
      let highlighted;
      if (typeof hljs !== 'undefined' && resolvedLang && hljs.getLanguage(resolvedLang)) {
        try {
          highlighted = hljs.highlight(code, { language: resolvedLang }).value;
        } catch (err) {
          // Fallback gracefully - log warning and show plain code
          console.warn('Highlight.js error for language:', resolvedLang, err);
          highlighted = escapeHtmlForCode(code);
        }
      } else {
        // No highlighting available - escape HTML for safe display
        highlighted = escapeHtmlForCode(code);
      }

      // Return pre/code block with hljs class for default styling
      return `<pre><code class="hljs language-${resolvedLang || 'plaintext'}">${highlighted}</code></pre>`;
    };

    // Configure Marked options for GitHub Flavored Markdown
    marked.setOptions({
      renderer: renderer,
      gfm: true,           // Enable GitHub Flavored Markdown
      breaks: true,        // Convert single newlines to <br>
      pedantic: false,     // Don't be overly strict about markdown spec
      smartLists: true     // Better list handling
    });
  }

  // HTML escape for code blocks - separate from escapeHtml to avoid circular dependency
  function escapeHtmlForCode(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  // Initialize markdown configuration when marked library is available
  // Note: Configuration is deferred since marked is loaded from CDN
  function initMarkdownIfReady() {
    if (typeof marked !== 'undefined' && typeof hljs !== 'undefined') {
      configureMarkdown();
      return true;
    }
    return false;
  }

  // Try immediately, then retry in init() if not ready
  initMarkdownIfReady();

  // ============================================
  // MsgPack Body Encoding Utilities
  // ============================================

  // Enable/disable msgpack encoding for API requests
  // When enabled, note body content is encoded as msgpack for efficient transport
  // Can be toggled via settings or feature flag for backwards compatibility
  const USE_MSGPACK_ENCODING = true;

  // Encode note body to Base64-encoded msgpack format
  // Used before sending note data to server to reduce payload size
  // Design: Only the body field is msgpack-encoded; metadata stays as JSON for debugging
  function encodeMsgPackBody(body) {
    if (!body || typeof MessagePack === 'undefined') {
      return null;
    }

    try {
      // Encode string to msgpack bytes using @msgpack/msgpack library
      const encoded = MessagePack.encode(body);
      // Convert Uint8Array to Base64 string for JSON transport
      // Using btoa with String.fromCharCode for browser compatibility
      const base64 = btoa(String.fromCharCode.apply(null, encoded));
      return base64;
    } catch (err) {
      console.error('MsgPack encode error:', err);
      return null;
    }
  }

  // Decode Base64-encoded msgpack to string
  // Used after receiving note data from server
  function decodeMsgPackBody(base64Encoded) {
    if (!base64Encoded || typeof MessagePack === 'undefined') {
      return null;
    }

    try {
      // Convert Base64 to Uint8Array
      const binaryString = atob(base64Encoded);
      const bytes = new Uint8Array(binaryString.length);
      for (let i = 0; i < binaryString.length; i++) {
        bytes[i] = binaryString.charCodeAt(i);
      }
      // Decode msgpack to string
      return MessagePack.decode(bytes);
    } catch (err) {
      console.error('MsgPack decode error:', err);
      return null;
    }
  }

  // Transform single note response from msgpack format to standard format
  // Handles body_encoded -> body conversion transparently
  function transformNoteFromMsgPack(note) {
    if (note && note.body_encoded !== undefined) {
      note.body = decodeMsgPackBody(note.body_encoded);
      delete note.body_encoded;
    }
    return note;
  }

  // Transform array of note responses from msgpack format
  function transformNotesFromMsgPack(notes) {
    if (!Array.isArray(notes)) {
      return notes;
    }
    return notes.map(transformNoteFromMsgPack);
  }

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

    // Add msgpack encoding header if enabled and MessagePack library is available
    // This signals to the server that we want body_encoded in responses
    const useMsgPack = USE_MSGPACK_ENCODING && typeof MessagePack !== 'undefined';
    if (useMsgPack) {
      headers['X-Body-Encoding'] = 'msgpack';
    }

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

      // Transform msgpack-encoded responses back to standard format
      // This handles body_encoded -> body conversion transparently
      if (useMsgPack && data.data) {
        if (Array.isArray(data.data)) {
          data.data = transformNotesFromMsgPack(data.data);
        } else if (data.data.body_encoded !== undefined) {
          data.data = transformNoteFromMsgPack(data.data);
        }
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
        renderNoteList();
        updateResultCount();
        updateSyncStatus('synced', 'Synced');
      }
    } catch (error) {
      updateSyncStatus('error', 'Failed to load');
    }
  }

  window.app.newNote = function() {
    state.currentNote = null;
    state.isEditing = true;
    clearEditForm();
    document.getElementById('edit-guid').value = generateGUID();
    showEditMode();
  };

  window.app.editNote = async function(noteId) {
    const note = state.notes.find(n => n.id === noteId);
    if (note) {
      state.currentNote = note;
      state.isEditing = true;
      populateEditForm(note);
      showEditMode();

      // Fetch note's categories from the API and populate multi-category entries.
      // Done after showEditMode so the form is visible while categories load.
      await window.app._loadEditNoteCategories(noteId);
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

    const bodyContent = formData.get('body') || null;

    // Build note data object
    // When msgpack is enabled, body goes to body_encoded field instead of body
    // Tags field is still sent for backward compatibility but we no longer collect it from UI
    const noteData = {
      guid: formData.get('guid'),
      title: formData.get('title'),
      description: formData.get('description') || null,
      tags: null,
      is_private: document.getElementById('edit-private').checked
    };

    // Add body field based on encoding mode
    // If msgpack is enabled and we can encode, use body_encoded; otherwise use plain body
    const useMsgPack = USE_MSGPACK_ENCODING && typeof MessagePack !== 'undefined';
    if (useMsgPack && bodyContent) {
      const encodedBody = encodeMsgPackBody(bodyContent);
      if (encodedBody) {
        noteData.body_encoded = encodedBody;
      } else {
        // Fallback to plain body if encoding fails
        noteData.body = bodyContent;
      }
    } else {
      noteData.body = bodyContent;
    }

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
        const savedNoteId = response.data.id;

        // Multi-category diff-based save — delegated to cats_subcats.js
        try {
          await window.app._saveCategoryAssignments(savedNoteId);
        } catch (catError) {
          // Log but don't fail the note save — category assignment is secondary
          console.error('Failed to handle categories:', catError);
        }

        showToast('Note saved successfully', 'success');
        await loadNotes();
        await window.app._loadCategories();
        await window.app._loadNoteCategoryMappings();

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

    const bodyContent = state.currentNote.body;

    // Build note data object with msgpack support
    const noteData = {
      guid: generateGUID(),
      title: state.currentNote.title + ' (Copy)',
      description: state.currentNote.description,
      tags: null,
      is_private: state.currentNote.is_private
    };

    // Add body field based on encoding mode
    const useMsgPack = USE_MSGPACK_ENCODING && typeof MessagePack !== 'undefined';
    if (useMsgPack && bodyContent) {
      const encodedBody = encodeMsgPackBody(bodyContent);
      if (encodedBody) {
        noteData.body_encoded = encodedBody;
      } else {
        noteData.body = bodyContent;
      }
    } else {
      noteData.body = bodyContent;
    }

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

    // Render meta information (tags removed — categories shown separately)
    const metaHtml = [];
    if (note.is_private) {
      metaHtml.push('<span class="preview-meta-item"><span>🔒</span> Private</span>');
    }
    metaHtml.push(`<span class="preview-meta-item">Modified: ${formatRelativeTime(note.updated_at)}</span>`);
    document.getElementById('preview-meta').innerHTML = metaHtml.join('');

    // Fetch and render category rows for this note.
    // Each row displays a bold category name followed by its selected subcategories.
    window.app._renderPreviewCategories(note.id);

    // Render markdown content
    const content = note.body || '';
    const html = DOMPurify.sanitize(marked.parse(content));
    document.getElementById('preview-content').innerHTML = html || '<p class="text-muted">No content</p>';
  }

  function clearPreview() {
    document.getElementById('preview-title').textContent = 'Select a note';
    document.getElementById('preview-meta').innerHTML = '';
    document.getElementById('preview-categories').innerHTML = '';
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

    // Meta row — show categories from the lookup map instead of tags
    const meta = document.createElement('div');
    meta.className = 'note-meta';

    const noteCats = state.noteCategoryMap[note.id];
    if (noteCats && noteCats.length > 0) {
      const catsSpan = document.createElement('span');
      catsSpan.className = 'note-categories';
      catsSpan.textContent = noteCats.map(c => c.categoryName).join(', ');
      meta.appendChild(catsSpan);
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

    // Apply search filter — supports both text match and numeric ID match.
    // If the search term is purely numeric, also match against note.id
    // so users can jump directly to a note by its database ID.
    if (state.filters.search) {
      const searchTerm = state.filters.search.trim();
      const searchLower = searchTerm.toLowerCase();
      const isNumericSearch = /^\d+$/.test(searchTerm);

      notes = notes.filter(note => {
        // ID match: if the search term is a number, check note.id
        if (isNumericSearch && note.id === parseInt(searchTerm, 10)) {
          return true;
        }
        // Text match across title, description, and body
        return note.title.toLowerCase().includes(searchLower) ||
          (note.body && note.body.toLowerCase().includes(searchLower)) ||
          (note.description && note.description.toLowerCase().includes(searchLower));
      });
    }

    // Apply category filter from search bar dropdown.
    // Uses the pre-loaded noteCategoryMap for instant lookups.
    if (state.filters.categoryId) {
      const catId = state.filters.categoryId;
      notes = notes.filter(note => {
        const mappings = state.noteCategoryMap[note.id];
        if (!mappings) return false;
        return mappings.some(m => m.categoryId === catId);
      });

      // Apply subcategory filter — AND logic: note must have ALL selected subcats
      if (state.filters.subcategories.length > 0) {
        notes = notes.filter(note => {
          const mappings = state.noteCategoryMap[note.id];
          if (!mappings) return false;
          // Find the mapping for the selected category
          const catMapping = mappings.find(m => m.categoryId === catId);
          if (!catMapping) return false;
          // Check that every selected subcategory is present in the mapping
          return state.filters.subcategories.every(
            sub => catMapping.subcategories.includes(sub)
          );
        });
      }
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

  // clearSearchBar — resets all search bar state: text input, category dropdown, subcats
  window.app.clearSearchBar = function() {
    // Reset text search
    document.getElementById('search-input').value = '';
    state.filters.search = '';

    // Reset category dropdown
    const select = document.getElementById('search-category-select');
    if (select) select.value = '';
    state.filters.categoryId = null;
    state.filters.categoryName = '';
    state.filters.subcategories = [];

    // Clear subcategory chips
    window.app._renderSubcategoryChips([]);

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
      categoryId: null,
      categoryName: '',
      subcategories: [],
      privacy: 'all',
      date: 'all',
      unsynced: false
    };

    // Reset search bar UI
    document.getElementById('search-input').value = '';
    const select = document.getElementById('search-category-select');
    if (select) select.value = '';
    window.app._renderSubcategoryChips([]);

    // Reset filter panel UI
    document.querySelectorAll('input[name="privacy"]')[0].checked = true;
    document.querySelectorAll('input[name="date"]')[0].checked = true;
    const unsyncedEl = document.getElementById('filter-unsynced');
    if (unsyncedEl) unsyncedEl.checked = false;

    renderNoteList();
    updateResultCount();
    updateActiveFilters();
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

  function populateEditForm(note) {
    document.getElementById('edit-id').value = note.id;
    document.getElementById('edit-guid').value = note.guid;
    document.getElementById('edit-title').value = note.title;
    document.getElementById('edit-description').value = note.description || '';
    document.getElementById('edit-body').value = note.body || '';
    document.getElementById('edit-private').checked = note.is_private;

    // Reset multi-category entries when populating edit form
    window.app._clearCategoryEntries();
  }

  function clearEditForm() {
    document.getElementById('edit-id').value = '';
    document.getElementById('edit-guid').value = '';
    document.getElementById('edit-title').value = '';
    document.getElementById('edit-description').value = '';
    document.getElementById('edit-body').value = '';
    document.getElementById('edit-private').checked = false;

    // Clear multi-category entries
    window.app._clearCategoryEntries();
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
    if (state.filters.categoryName) {
      let catBadge = state.filters.categoryName;
      if (state.filters.subcategories.length > 0) {
        catBadge += ' > ' + state.filters.subcategories.join(', ');
      }
      badges.push(`<span class="filter-badge">${escapeHtml(catBadge)}</span>`);
    }
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
    await window.app._loadCategories();
    await window.app._loadNoteCategoryMappings();
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

  function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

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

  // Expose shared internals for cats_subcats.js to access.
  // Set before DOMContentLoaded fires so the category module can resolve references.
  window.app._internal = {
    state,
    apiRequest,
    showToast,
    escapeHtml,
    renderNoteList,
    updateResultCount,
    updateActiveFilters
  };

  async function init() {
    // Ensure markdown/highlight.js is configured (retry in case CDN scripts loaded late)
    initMarkdownIfReady();

    // Initialize category input handlers (defined in cats_subcats.js)
    window.app._initCategoryHandlers();

    const isAuthenticated = await checkAuth();
    if (!isAuthenticated) return;

    // Load notes, categories, and category mappings.
    // Notes and categories load in parallel; mappings depend on auth but
    // can run concurrently with the other two since it's a separate endpoint.
    await Promise.all([
      loadNotes(),
      window.app._loadCategories(),
      window.app._loadNoteCategoryMappings()
    ]);
  }

  // Start the app when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

})();
