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

    const bodyContent = formData.get('body') || null;

    // Build note data object
    // When msgpack is enabled, body goes to body_encoded field instead of body
    const noteData = {
      guid: formData.get('guid'),
      title: formData.get('title'),
      description: formData.get('description') || null,
      tags: formData.get('tags') || null,
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

        // Handle category assignment if a category was entered
        // Design: Category is added to note after note save, with selected subcats
        // Supports dynamic creation of new categories and subcategories
        const categoryInput = document.getElementById('edit-category');
        const categoryName = categoryInput ? categoryInput.value.trim() : '';

        if (categoryName) {
          try {
            // Find existing category by name
            let category = state.categories.find(c =>
              c.name.toLowerCase() === categoryName.toLowerCase()
            );

            if (!category) {
              // Create new category with any new subcategories
              const createResponse = await apiRequest('/categories', {
                method: 'POST',
                body: JSON.stringify({
                  name: categoryName,
                  subcategories: newSubcategories.length > 0 ? newSubcategories : []
                })
              });
              if (createResponse && createResponse.data) {
                category = createResponse.data;
                showToast(`Category "${categoryName}" created`, 'success');
              }
            } else if (newSubcategories.length > 0) {
              // Existing category - add any new subcategories to it
              const existingSubcats = category.subcategories || [];
              const allSubcats = [...new Set([...existingSubcats, ...newSubcategories])];

              await apiRequest(`/categories/${category.id}`, {
                method: 'PUT',
                body: JSON.stringify({
                  name: category.name,
                  subcategories: allSubcats
                })
              });
              showToast(`Added new subcategories to "${categoryName}"`, 'success');
            }

            // Assign category to note with selected subcategories
            if (category && category.id) {
              const subcats = getSelectedSubcats();
              await apiRequest(`/notes/${savedNoteId}/categories/${category.id}`, {
                method: 'POST',
                body: JSON.stringify({ subcategories: subcats })
              });
            }
          } catch (catError) {
            // Log but don't fail the note save - category assignment is secondary
            console.error('Failed to handle category:', catError);
          }
        }

        showToast('Note saved successfully', 'success');
        await loadNotes();
        await loadCategories(); // Refresh categories to include any new ones

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
      tags: state.currentNote.tags,
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
    // Populate datalist for category autocomplete
    const datalist = document.getElementById('category-datalist');
    if (!datalist) return;

    datalist.innerHTML = state.categories.map(cat =>
      `<option value="${escapeHtml(cat.name)}">`
    ).join('');
  }

  // Track selected subcategories for current note editing session
  let selectedSubcats = [];

  // Track new subcategories added during current editing session
  let newSubcategories = [];

  // onCategoryChange - Called when user types/selects a category in the edit form
  // Shows subcategory checkboxes if the selected category has subcats defined
  // Also detects if user is entering a new category
  window.app.onCategoryChange = function(categoryName) {
    const subcatField = document.getElementById('subcat-field');
    const subcatSelect = document.getElementById('subcat-select');
    const newIndicator = document.getElementById('new-category-indicator');

    if (!subcatField || !subcatSelect) {
      return;
    }

    // Clear previous selection state
    selectedSubcats = [];
    newSubcategories = [];

    if (!categoryName || !categoryName.trim()) {
      // No category entered - hide subcat field and new indicator
      subcatField.style.display = 'none';
      subcatSelect.innerHTML = '';
      if (newIndicator) newIndicator.style.display = 'none';
      return;
    }

    const trimmedName = categoryName.trim();

    // Find the category by name (case-insensitive)
    const category = state.categories.find(c =>
      c.name.toLowerCase() === trimmedName.toLowerCase()
    );

    if (!category) {
      // This is a NEW category - show indicator and subcategory field for adding new subcats
      if (newIndicator) newIndicator.style.display = 'inline';

      // Show subcategory field with just the add input (no existing subcats)
      subcatSelect.innerHTML = '<span class="text-muted" style="font-size: var(--font-size-xs);">Add subcategories for this new category</span>';
      subcatField.style.display = 'block';
      return;
    }

    // Existing category - hide new indicator
    if (newIndicator) newIndicator.style.display = 'none';

    if (!category.subcategories || category.subcategories.length === 0) {
      // Category has no subcats defined - show field with just add input
      subcatSelect.innerHTML = '<span class="text-muted" style="font-size: var(--font-size-xs);">No subcategories yet</span>';
      subcatField.style.display = 'block';
      return;
    }

    // Render subcategory checkboxes for existing subcats
    // Design: Each subcat is displayed as a checkbox for multi-select
    subcatSelect.innerHTML = category.subcategories.map(subcat => `
      <label class="subcat-checkbox-label">
        <input type="checkbox" class="subcat-checkbox" value="${escapeHtml(subcat)}"
               onchange="app.toggleSubcat('${escapeHtml(subcat)}', this.checked)">
        <span>${escapeHtml(subcat)}</span>
      </label>
    `).join('');

    subcatField.style.display = 'block';
  };

  // addNewSubcatFromForm - Called when user adds a new subcategory from the note edit form
  window.app.addNewSubcatFromForm = function() {
    const input = document.getElementById('new-subcat-input');
    if (!input) return;

    const value = input.value.trim();
    if (!value) return;

    // Check if already in selected or new subcategories
    if (selectedSubcats.includes(value) || newSubcategories.includes(value)) {
      showToast('Subcategory already added', 'warning');
      return;
    }

    // Get current category to check if this subcat already exists
    const categoryInput = document.getElementById('edit-category');
    const categoryName = categoryInput ? categoryInput.value.trim() : '';
    const category = state.categories.find(c =>
      c.name.toLowerCase() === categoryName.toLowerCase()
    );

    // If category exists and already has this subcategory, add to selected
    if (category && category.subcategories && category.subcategories.includes(value)) {
      selectedSubcats.push(value);
      // Check the corresponding checkbox
      const checkbox = document.querySelector(`.subcat-checkbox[value="${escapeHtml(value)}"]`);
      if (checkbox) checkbox.checked = true;
    } else {
      // This is a new subcategory - add to both lists
      newSubcategories.push(value);
      selectedSubcats.push(value);
      // Add visual indicator for new subcategory
      addNewSubcatTag(value);
    }

    input.value = '';
    showToast(`Subcategory "${value}" will be created`, 'success');
  };

  // addNewSubcatTag - Adds a visual tag for a new subcategory
  function addNewSubcatTag(subcat) {
    const subcatSelect = document.getElementById('subcat-select');
    if (!subcatSelect) return;

    // Remove "no subcategories" message if present
    const noSubcatMsg = subcatSelect.querySelector('.text-muted');
    if (noSubcatMsg) noSubcatMsg.remove();

    const tag = document.createElement('label');
    tag.className = 'subcat-checkbox-label new-subcat';
    tag.innerHTML = `
      <input type="checkbox" class="subcat-checkbox" value="${escapeHtml(subcat)}" checked
             onchange="app.toggleSubcat('${escapeHtml(subcat)}', this.checked)">
      <span>${escapeHtml(subcat)} <em>(new)</em></span>
    `;
    subcatSelect.appendChild(tag);
  }

  // toggleSubcat - Called when user checks/unchecks a subcategory checkbox
  window.app.toggleSubcat = function(subcat, checked) {
    if (checked) {
      if (!selectedSubcats.includes(subcat)) {
        selectedSubcats.push(subcat);
      }
    } else {
      selectedSubcats = selectedSubcats.filter(s => s !== subcat);
    }
  };

  // getSelectedSubcats - Returns the currently selected subcategories
  function getSelectedSubcats() {
    return selectedSubcats.slice(); // Return a copy
  }

  // clearSubcatSelection - Clears subcategory selection and hides the field
  function clearSubcatSelection() {
    selectedSubcats = [];
    newSubcategories = [];
    const subcatField = document.getElementById('subcat-field');
    const subcatSelect = document.getElementById('subcat-select');
    const newSubcatInput = document.getElementById('new-subcat-input');
    const newIndicator = document.getElementById('new-category-indicator');
    if (subcatField) subcatField.style.display = 'none';
    if (subcatSelect) subcatSelect.innerHTML = '';
    if (newSubcatInput) newSubcatInput.value = '';
    if (newIndicator) newIndicator.style.display = 'none';
  }

  function populateEditForm(note) {
    document.getElementById('edit-id').value = note.id;
    document.getElementById('edit-guid').value = note.guid;
    document.getElementById('edit-title').value = note.title;
    document.getElementById('edit-description').value = note.description || '';
    document.getElementById('edit-tags').value = note.tags || '';
    document.getElementById('edit-body').value = note.body || '';
    document.getElementById('edit-private').checked = note.is_private;

    // Reset category and subcategory selection when populating edit form
    const categorySelect = document.getElementById('edit-category');
    if (categorySelect) categorySelect.value = '';
    clearSubcatSelection();
  }

  function clearEditForm() {
    document.getElementById('edit-id').value = '';
    document.getElementById('edit-guid').value = '';
    document.getElementById('edit-title').value = '';
    document.getElementById('edit-description').value = '';
    document.getElementById('edit-tags').value = '';
    document.getElementById('edit-body').value = '';
    document.getElementById('edit-private').checked = false;

    // Clear category and subcategory selection
    const categorySelect = document.getElementById('edit-category');
    if (categorySelect) categorySelect.value = '';
    clearSubcatSelection();
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
  // Category Management
  // ============================================

  // Track editing state for categories
  let editingCategoryId = null;
  let editingSubcategories = [];

  window.app.showCategoryManager = function() {
    const modalTitle = document.getElementById('modal-title');
    const modalBody = document.getElementById('modal-body');
    const modalFooter = document.getElementById('modal-footer');

    modalTitle.textContent = 'Manage Categories';

    // Build category manager HTML
    modalBody.innerHTML = `
      <div class="category-manager">
        <div class="category-manager-header">
          <input type="text" id="new-category-name" placeholder="New category name..." />
          <button class="btn btn-primary" onclick="app.createCategory()">Add</button>
        </div>
        <div class="category-list" id="category-list">
          ${renderCategoryList()}
        </div>
      </div>
    `;

    // Hide default footer buttons, we handle actions inline
    modalFooter.innerHTML = `
      <button class="btn btn-secondary" onclick="app.closeModal()">Close</button>
    `;

    document.getElementById('modal-overlay').classList.add('open');

    // Focus the input
    document.getElementById('new-category-name').focus();
  };

  function renderCategoryList() {
    if (state.categories.length === 0) {
      return '<div class="empty-categories">No categories yet. Create one above.</div>';
    }

    return state.categories.map(cat => {
      const subcats = cat.subcategories || [];
      const subcatText = subcats.length > 0 ? subcats.join(', ') : 'No subcategories';

      return `
        <div class="category-item" data-category-id="${cat.id}">
          <div class="category-item-header">
            <span class="category-name">${escapeHtml(cat.name)}</span>
            <span class="category-subcats">${escapeHtml(subcatText)}</span>
            <div class="category-actions">
              <button class="btn-edit-cat" onclick="app.editCategory(${cat.id})">Edit</button>
              <button class="btn-delete-cat" onclick="app.deleteCategory(${cat.id})">Delete</button>
            </div>
          </div>
          <div class="category-edit-form" id="category-edit-${cat.id}" style="display: none;">
          </div>
        </div>
      `;
    }).join('');
  }

  function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  window.app.createCategory = async function() {
    const input = document.getElementById('new-category-name');
    const name = input.value.trim();

    if (!name) {
      showToast('Please enter a category name', 'error');
      return;
    }

    try {
      const response = await apiRequest('/categories', {
        method: 'POST',
        body: JSON.stringify({ name: name })
      });

      if (response && response.data) {
        showToast('Category created', 'success');
        input.value = '';
        await loadCategories();
        // Refresh the modal list
        document.getElementById('category-list').innerHTML = renderCategoryList();
      }
    } catch (error) {
      showToast('Failed to create category', 'error');
    }
  };

  window.app.editCategory = function(categoryId) {
    const cat = state.categories.find(c => c.id === categoryId);
    if (!cat) return;

    // Close any other open edit forms
    document.querySelectorAll('.category-edit-form').forEach(form => {
      form.style.display = 'none';
    });

    editingCategoryId = categoryId;
    editingSubcategories = [...(cat.subcategories || [])];

    const editForm = document.getElementById(`category-edit-${categoryId}`);
    editForm.style.display = 'block';
    editForm.innerHTML = `
      <label>Category Name</label>
      <input type="text" id="edit-cat-name-${categoryId}" value="${escapeHtml(cat.name)}" />

      <label>Subcategories</label>
      <div class="subcategory-tags" id="subcategory-tags-${categoryId}">
        ${renderSubcategoryTags(categoryId)}
      </div>
      <div class="subcategory-input-group">
        <input type="text" id="new-subcat-${categoryId}" placeholder="Add subcategory..."
               onkeypress="if(event.key==='Enter'){app.addSubcategory(${categoryId}); event.preventDefault();}" />
        <button class="btn btn-secondary" onclick="app.addSubcategory(${categoryId})">Add</button>
      </div>

      <div class="category-edit-actions">
        <button class="btn btn-secondary" onclick="app.cancelEditCategory(${categoryId})">Cancel</button>
        <button class="btn btn-primary" onclick="app.saveCategory(${categoryId})">Save</button>
      </div>
    `;
  };

  function renderSubcategoryTags(categoryId) {
    if (editingSubcategories.length === 0) {
      return '<span class="text-muted" style="font-size: var(--font-size-xs);">None</span>';
    }

    return editingSubcategories.map((subcat, index) => `
      <span class="subcategory-tag">
        ${escapeHtml(subcat)}
        <button onclick="app.removeSubcategory(${categoryId}, ${index})">&times;</button>
      </span>
    `).join('');
  }

  window.app.addSubcategory = function(categoryId) {
    console.log('addSubcategory called with categoryId:', categoryId);

    const input = document.getElementById(`new-subcat-${categoryId}`);
    console.log('input element:', input);

    const value = input ? input.value.trim() : '';
    console.log('Adding subcategory:', value, 'to category:', categoryId);

    if (!value) return;

    if (editingSubcategories.includes(value)) {
      showToast('Subcategory already exists', 'warning');
      return;
    }

    editingSubcategories.push(value);
    console.log('editingSubcategories now:', editingSubcategories);
    input.value = '';

    // Re-render tags
    const tagsContainer = document.getElementById(`subcategory-tags-${categoryId}`);
    console.log('tagsContainer element:', tagsContainer);
    const renderedHtml = renderSubcategoryTags(categoryId);
    console.log('rendered HTML:', renderedHtml);
    if (tagsContainer) {
      tagsContainer.innerHTML = renderedHtml;
    }
  };

  window.app.removeSubcategory = function(categoryId, index) {
    editingSubcategories.splice(index, 1);
    document.getElementById(`subcategory-tags-${categoryId}`).innerHTML = renderSubcategoryTags(categoryId);
  };

  window.app.cancelEditCategory = function(categoryId) {
    const editForm = document.getElementById(`category-edit-${categoryId}`);
    editForm.style.display = 'none';
    editingCategoryId = null;
    editingSubcategories = [];
  };

  window.app.saveCategory = async function(categoryId) {
    const nameInput = document.getElementById(`edit-cat-name-${categoryId}`);
    const name = nameInput.value.trim();

    if (!name) {
      showToast('Category name is required', 'error');
      return;
    }

    // Debug: Log what we're sending
    console.log('Saving category:', categoryId, 'name:', name, 'subcategories:', editingSubcategories);

    try {
      const payload = {
        name: name,
        subcategories: editingSubcategories
      };
      console.log('Request payload:', JSON.stringify(payload));

      const response = await apiRequest(`/categories/${categoryId}`, {
        method: 'PUT',
        body: JSON.stringify(payload)
      });
      console.log('Response:', response);

      if (response && response.data) {
        showToast('Category updated', 'success');
        await loadCategories();
        document.getElementById('category-list').innerHTML = renderCategoryList();
        editingCategoryId = null;
        editingSubcategories = [];
      }
    } catch (error) {
      showToast('Failed to update category', 'error');
    }
  };

  window.app.deleteCategory = async function(categoryId) {
    const cat = state.categories.find(c => c.id === categoryId);
    if (!cat) return;

    if (!confirm(`Are you sure you want to delete the category "${cat.name}"?`)) {
      return;
    }

    try {
      await apiRequest(`/categories/${categoryId}`, {
        method: 'DELETE'
      });

      showToast('Category deleted', 'success');
      await loadCategories();
      document.getElementById('category-list').innerHTML = renderCategoryList();
    } catch (error) {
      showToast('Failed to delete category', 'error');
    }
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
    // Ensure markdown/highlight.js is configured (retry in case CDN scripts loaded late)
    initMarkdownIfReady();

    // Attach category input handler with debounce for autocomplete
    // Done here rather than inline to ensure app.onCategoryChange is defined
    const categoryInput = document.getElementById('edit-category');
    if (categoryInput) {
      let categoryDebounceTimer;
      categoryInput.addEventListener('input', function() {
        clearTimeout(categoryDebounceTimer);
        categoryDebounceTimer = setTimeout(() => {
          window.app.onCategoryChange(this.value);
        }, 150); // Short debounce for responsive feel
      });
      // Also trigger on blur to finalize selection
      categoryInput.addEventListener('blur', function() {
        clearTimeout(categoryDebounceTimer);
        window.app.onCategoryChange(this.value);
      });
    }

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
