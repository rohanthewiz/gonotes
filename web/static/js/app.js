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
      regex: false,            // when true, search term is treated as a regular expression
      categoryId: null,        // selected category ID from search bar dropdown
      categoryName: '',        // selected category name (for display)
      subcategories: [],       // selected subcategory chips (AND logic)
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
  // Mermaid code blocks are rendered as diagrams instead of code.
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

      // Mermaid diagrams: render as a special div that mermaid.js will process
      if (lang === 'mermaid') {
        const id = 'mermaid-' + Math.random().toString(36).substr(2, 9);
        return `<div class="mermaid-diagram" id="${id}">${escapeHtmlForCode(code)}</div>`;
      }

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
  // Theme Toggle
  // ============================================

  window.app.toggleTheme = function() {
    const html = document.documentElement;
    const current = html.getAttribute('data-theme');
    const next = current === 'dark-green' ? 'light' : 'dark-green';
    html.setAttribute('data-theme', next);
    localStorage.setItem('gonotes-theme', next);
    // Update toggle button icon
    const btn = document.getElementById('btn-theme-toggle');
    if (btn) btn.textContent = next === 'dark-green' ? '\u2600' : '\u263E';
    // Update highlight.js theme for code blocks
    const hljsLink = document.getElementById('hljs-theme');
    if (hljsLink) {
      hljsLink.href = next === 'dark-green'
        ? 'https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/styles/github-dark.min.css'
        : 'https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/styles/github.min.css';
    }
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
        renderNoteList();

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

    // Render markdown content.
    // DOMPurify must allow data: URIs for base64 embedded images.
    const content = note.body || '';
    const html = DOMPurify.sanitize(marked.parse(content), {
      ADD_ATTR: ['class', 'id', 'data-note-guid'],
      ADD_TAGS: ['div', 'a'],
    });
    document.getElementById('preview-content').innerHTML = html || '<p class="text-muted">No content</p>';

    // Convert note link syntax [[note:UUID|Title]] to clickable links
    renderNoteLinks();

    // Render any mermaid diagrams found in the preview content
    renderMermaidDiagrams();
  }

  // ============================================
  // Note Linking
  // ============================================

  // Convert [[note:UUID|Title]] syntax in the preview content to clickable links.
  // The links navigate to the referenced note when clicked.
  function renderNoteLinks() {
    const container = document.getElementById('preview-content');
    if (!container) return;

    // Pattern: [[note:UUID|Display Text]]
    // UUID is a standard v4 UUID format
    const noteLinkRegex = /\[\[note:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\|([^\]]+)\]\]/gi;

    // Walk text nodes and replace note link patterns with anchor elements
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, null, false);
    const textNodes = [];
    let node;
    while ((node = walker.nextNode())) {
      if (noteLinkRegex.test(node.textContent)) {
        textNodes.push(node);
      }
      noteLinkRegex.lastIndex = 0; // Reset regex state
    }

    textNodes.forEach(textNode => {
      const fragment = document.createDocumentFragment();
      let text = textNode.textContent;
      let match;
      let lastIndex = 0;
      noteLinkRegex.lastIndex = 0;

      while ((match = noteLinkRegex.exec(text)) !== null) {
        // Add text before the match
        if (match.index > lastIndex) {
          fragment.appendChild(document.createTextNode(text.substring(lastIndex, match.index)));
        }

        // Create the note link anchor
        const noteGuid = match[1];
        const displayText = match[2];
        const anchor = document.createElement('a');
        anchor.href = '#';
        anchor.className = 'note-link';
        anchor.setAttribute('data-note-guid', noteGuid);
        anchor.textContent = displayText;
        anchor.title = 'Note: ' + displayText + ' (' + noteGuid.substring(0, 8) + '...)';
        anchor.onclick = function(e) {
          e.preventDefault();
          navigateToNoteByGuid(noteGuid);
        };
        fragment.appendChild(anchor);

        lastIndex = noteLinkRegex.lastIndex;
      }

      // Add remaining text after last match
      if (lastIndex < text.length) {
        fragment.appendChild(document.createTextNode(text.substring(lastIndex)));
      }

      textNode.parentNode.replaceChild(fragment, textNode);
    });
  }

  // Navigate to a note by its GUID. Finds the note in the current state
  // or fetches it if not loaded. Used when clicking note links.
  function navigateToNoteByGuid(guid) {
    const note = state.notes.find(n => n.guid === guid);
    if (note) {
      window.app.selectNote(note.id);
    } else {
      showToast('Linked note not found in current view', 'warning');
    }
  }

  // Show the "Link to Note" popup with autocomplete search.
  // Inserts [[note:UUID|Title]] syntax at the cursor position in the body textarea.
  window.app.showLinkNotePopup = function() {
    const textarea = document.getElementById('edit-body');
    if (!textarea) return;

    // Create overlay
    const overlay = document.createElement('div');
    overlay.className = 'note-link-overlay';

    const dialog = document.createElement('div');
    dialog.className = 'note-link-dialog';

    // Header
    const title = document.createElement('h3');
    title.textContent = 'Link to Note';

    // Search input
    const searchInput = document.createElement('input');
    searchInput.type = 'text';
    searchInput.className = 'note-link-search';
    searchInput.placeholder = 'Search notes by title...';
    searchInput.autocomplete = 'off';

    // Results container
    const resultsContainer = document.createElement('div');
    resultsContainer.className = 'note-link-results';
    resultsContainer.innerHTML = '<div class="note-link-hint">Type to search for notes</div>';

    // Debounced search
    let searchTimer;
    searchInput.addEventListener('input', function() {
      clearTimeout(searchTimer);
      const query = this.value.trim();
      if (!query) {
        resultsContainer.innerHTML = '<div class="note-link-hint">Type to search for notes</div>';
        return;
      }
      searchTimer = setTimeout(async () => {
        try {
          const response = await apiRequest(`/notes/search?q=${encodeURIComponent(query)}`);
          if (response && response.data) {
            renderLinkSearchResults(response.data, resultsContainer, textarea, overlay);
          }
        } catch (err) {
          resultsContainer.innerHTML = '<div class="note-link-hint">Search failed</div>';
        }
      }, 250);
    });

    // Keyboard navigation
    searchInput.addEventListener('keydown', function(e) {
      const items = resultsContainer.querySelectorAll('.note-link-result');
      const active = resultsContainer.querySelector('.note-link-result.active');
      let activeIndex = -1;
      items.forEach((item, i) => { if (item === active) activeIndex = i; });

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        if (active) active.classList.remove('active');
        const next = items[activeIndex + 1] || items[0];
        if (next) { next.classList.add('active'); next.scrollIntoView({ block: 'nearest' }); }
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        if (active) active.classList.remove('active');
        const prev = items[activeIndex - 1] || items[items.length - 1];
        if (prev) { prev.classList.add('active'); prev.scrollIntoView({ block: 'nearest' }); }
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (active) active.click();
      } else if (e.key === 'Escape') {
        overlay.remove();
      }
    });

    // Cancel button
    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'btn btn-secondary';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.onclick = function() { overlay.remove(); };

    const actions = document.createElement('div');
    actions.className = 'note-link-actions';
    actions.appendChild(cancelBtn);

    dialog.appendChild(title);
    dialog.appendChild(searchInput);
    dialog.appendChild(resultsContainer);
    dialog.appendChild(actions);
    overlay.appendChild(dialog);

    // Close on overlay click
    overlay.addEventListener('click', function(e) {
      if (e.target === overlay) overlay.remove();
    });

    document.body.appendChild(overlay);
    searchInput.focus();
  };

  // Render search results in the link popup
  function renderLinkSearchResults(results, container, textarea, overlay) {
    if (results.length === 0) {
      container.innerHTML = '<div class="note-link-hint">No notes found</div>';
      return;
    }

    container.innerHTML = '';
    results.forEach((result, index) => {
      const item = document.createElement('div');
      item.className = 'note-link-result' + (index === 0 ? ' active' : '');

      const titleSpan = document.createElement('span');
      titleSpan.className = 'note-link-result-title';
      titleSpan.textContent = result.title;

      const guidSpan = document.createElement('span');
      guidSpan.className = 'note-link-result-guid';
      guidSpan.textContent = result.guid.substring(0, 8) + '...';

      item.appendChild(titleSpan);
      item.appendChild(guidSpan);

      item.onclick = function() {
        insertNoteLink(result.guid, result.title, textarea);
        overlay.remove();
      };

      item.onmouseenter = function() {
        container.querySelectorAll('.note-link-result').forEach(r => r.classList.remove('active'));
        item.classList.add('active');
      };

      container.appendChild(item);
    });
  }

  // Insert a note link at the cursor position in the textarea
  function insertNoteLink(guid, title, textarea) {
    const linkSyntax = `[[note:${guid}|${title}]]`;
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const before = textarea.value.substring(0, start);
    const after = textarea.value.substring(end);

    textarea.value = before + linkSyntax + after;

    // Move cursor after the inserted link
    const newPos = start + linkSyntax.length;
    textarea.selectionStart = textarea.selectionEnd = newPos;
    textarea.focus();

    textarea.dispatchEvent(new Event('input', { bubbles: true }));
    showToast('Note link inserted', 'success');
  }

  // ============================================
  // Mermaid Diagram Rendering
  // ============================================

  // Process all mermaid-diagram divs in the preview content.
  // Each div contains escaped mermaid source; mermaid.run() renders them as SVG.
  function renderMermaidDiagrams() {
    if (typeof mermaid === 'undefined') return;

    const container = document.getElementById('preview-content');
    if (!container) return;

    const diagrams = container.querySelectorAll('.mermaid-diagram');
    if (diagrams.length === 0) return;

    // Decode HTML entities back to plain text for mermaid processing
    diagrams.forEach(el => {
      const tmp = document.createElement('textarea');
      tmp.innerHTML = el.innerHTML;
      el.textContent = tmp.value;
    });

    // Use mermaid.run() to render the specific elements
    try {
      mermaid.run({ nodes: diagrams });
    } catch (err) {
      console.warn('Mermaid rendering error:', err);
    }
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

    // Title row — compact: title, categories, timestamp, and actions all on one line
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

    // Inline categories — shown right after title
    const noteCats = state.noteCategoryMap[note.id];
    if (noteCats && noteCats.length > 0) {
      const catsSpan = document.createElement('span');
      catsSpan.className = 'note-categories-inline';
      catsSpan.textContent = noteCats.map(c => c.categoryName).join(', ');
      titleRow.appendChild(catsSpan);
    }

    // Right-side group: timestamp + action buttons
    const titleRight = document.createElement('div');
    titleRight.className = 'note-title-right';

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
    titleRight.appendChild(timestamp);
    titleRight.appendChild(actions);
    titleRow.appendChild(titleRight);

    // Preview
    const preview = document.createElement('div');
    preview.className = 'note-preview';
    preview.textContent = (note.body || '').substring(0, 100) + ((note.body?.length || 0) > 100 ? '...' : '');

    content.appendChild(titleRow);
    content.appendChild(preview);

    row.appendChild(checkbox);
    row.appendChild(content);

    return row;
  }

  // ============================================
  // Filtering and Search
  // ============================================

  function getFilteredNotes() {
    let notes = [...state.notes];

    // Apply search filter — supports text match, numeric ID match, and regex.
    // If the search term is purely numeric, also match against note.id
    // so users can jump directly to a note by its database ID.
    if (state.filters.search) {
      const searchTerm = state.filters.search.trim();
      const isNumericSearch = /^\d+$/.test(searchTerm);

      if (state.filters.regex) {
        // Regex mode: compile the search term as a case-insensitive regex
        let re;
        try {
          re = new RegExp(searchTerm, 'i');
        } catch (e) {
          // Invalid regex — skip filtering until the pattern is valid
          re = null;
        }
        if (re) {
          notes = notes.filter(note => {
            if (isNumericSearch && note.id === parseInt(searchTerm, 10)) {
              return true;
            }
            return re.test(note.title) ||
              (note.body && re.test(note.body)) ||
              (note.description && re.test(note.description));
          });
        }
      } else {
        // Substring mode (default): case-insensitive .includes()
        const searchLower = searchTerm.toLowerCase();
        notes = notes.filter(note => {
          if (isNumericSearch && note.id === parseInt(searchTerm, 10)) {
            return true;
          }
          return note.title.toLowerCase().includes(searchLower) ||
            (note.body && note.body.toLowerCase().includes(searchLower)) ||
            (note.description && note.description.toLowerCase().includes(searchLower));
        });
      }
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

  window.app.toggleRegex = function() {
    state.filters.regex = !state.filters.regex;
    const btn = document.getElementById('regex-toggle');
    if (btn) {
      btn.classList.toggle('active', state.filters.regex);
    }
    const input = document.getElementById('search-input');
    if (input) {
      input.placeholder = state.filters.regex
        ? 'Search by regex pattern...'
        : 'Search by text or ID...';
    }
    // Re-apply the current search with updated mode
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.clearSearch = function() {
    document.getElementById('search-input').value = '';
    state.filters.search = '';
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  // clearSearchBar — resets all search bar state: text input, regex, category dropdown, subcats
  window.app.clearSearchBar = function() {
    // Reset text search
    document.getElementById('search-input').value = '';
    state.filters.search = '';

    // Reset regex toggle
    state.filters.regex = false;
    const regexBtn = document.getElementById('regex-toggle');
    if (regexBtn) regexBtn.classList.remove('active');

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
      regex: false,
      categoryId: null,
      categoryName: '',
      subcategories: [],
      privacy: 'all',
      date: 'all',
      unsynced: false
    };

    // Reset search bar UI
    document.getElementById('search-input').value = '';
    const regexBtn = document.getElementById('regex-toggle');
    if (regexBtn) regexBtn.classList.remove('active');
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
      const mode = state.filters.regex ? 'Regex' : 'Search';
      badges.push(`<span class="filter-badge">${mode}: "${escapeHtml(state.filters.search)}"</span>`);
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
    renderNoteList();
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
  // Embedded Image Support (base64)
  // ============================================

  // Convert a File/Blob to a base64 data URI and insert as markdown image
  // at the cursor position in the body textarea.
  // Shows a resize dialog so the user can scale the image before embedding.
  function insertImageAsBase64(file, textarea) {
    // Validate file type
    if (!file.type.startsWith('image/')) {
      showToast('Only image files can be embedded', 'warning');
      return;
    }

    const reader = new FileReader();
    reader.onload = function(e) {
      const originalDataUri = e.target.result;
      const altText = file.name ? file.name.replace(/\.[^/.]+$/, '') : 'image';

      // Load image to get dimensions
      const img = new Image();
      img.onload = function() {
        showImageResizeDialog(img, originalDataUri, altText, file.type, textarea);
      };
      img.onerror = function() {
        // Fallback: embed without resize if we can't load the image
        doInsertImage(originalDataUri, altText, textarea);
      };
      img.src = originalDataUri;
    };

    reader.onerror = function() {
      showToast('Failed to read image file', 'error');
    };

    reader.readAsDataURL(file);
  }

  // Show a dialog to let the user resize an image before embedding
  function showImageResizeDialog(img, originalDataUri, altText, mimeType, textarea) {
    const origWidth = img.naturalWidth;
    const origHeight = img.naturalHeight;

    // Build overlay
    const overlay = document.createElement('div');
    overlay.className = 'image-resize-overlay';

    const dialog = document.createElement('div');
    dialog.className = 'image-resize-dialog';

    const title = document.createElement('h3');
    title.textContent = 'Resize Image';

    const preview = document.createElement('div');
    preview.className = 'image-resize-preview';
    const previewImg = document.createElement('img');
    previewImg.src = originalDataUri;
    preview.appendChild(previewImg);

    const controls = document.createElement('div');
    controls.className = 'image-resize-controls';

    const label = document.createElement('label');
    label.textContent = 'Scale';

    const row = document.createElement('div');
    row.className = 'image-resize-row';

    const slider = document.createElement('input');
    slider.type = 'range';
    slider.min = '10';
    slider.max = '100';
    slider.value = '100';

    // Default to a smaller size if image is very large
    if (origWidth > 1600 || origHeight > 1200) {
      slider.value = String(Math.round(Math.min(1200 / origWidth, 900 / origHeight) * 100));
    }

    const valueDisplay = document.createElement('span');
    valueDisplay.className = 'resize-value';
    valueDisplay.textContent = slider.value + '% (' + Math.round(origWidth * slider.value / 100) + 'x' + Math.round(origHeight * slider.value / 100) + ')';

    slider.addEventListener('input', function() {
      const pct = parseInt(slider.value, 10);
      const w = Math.round(origWidth * pct / 100);
      const h = Math.round(origHeight * pct / 100);
      valueDisplay.textContent = pct + '% (' + w + 'x' + h + ')';
    });

    row.appendChild(slider);
    row.appendChild(valueDisplay);

    const info = document.createElement('div');
    info.className = 'image-resize-info';
    info.textContent = 'Original: ' + origWidth + 'x' + origHeight;

    controls.appendChild(label);
    controls.appendChild(row);
    controls.appendChild(info);

    const actions = document.createElement('div');
    actions.className = 'image-resize-actions';

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'btn btn-secondary';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.onclick = function() { overlay.remove(); };

    const embedOrigBtn = document.createElement('button');
    embedOrigBtn.className = 'btn btn-secondary';
    embedOrigBtn.textContent = 'Original Size';
    embedOrigBtn.onclick = function() {
      overlay.remove();
      doInsertImage(originalDataUri, altText, textarea);
    };

    const embedBtn = document.createElement('button');
    embedBtn.className = 'btn btn-primary';
    embedBtn.textContent = 'Embed';
    embedBtn.onclick = function() {
      const pct = parseInt(slider.value, 10);
      overlay.remove();

      if (pct >= 100) {
        doInsertImage(originalDataUri, altText, textarea);
        return;
      }

      // Resize using canvas
      const w = Math.round(origWidth * pct / 100);
      const h = Math.round(origHeight * pct / 100);
      const canvas = document.createElement('canvas');
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext('2d');
      ctx.drawImage(img, 0, 0, w, h);

      // Use original mime type; fall back to PNG for lossless types
      const outputType = (mimeType === 'image/jpeg' || mimeType === 'image/webp') ? mimeType : 'image/png';
      const quality = (outputType === 'image/jpeg' || outputType === 'image/webp') ? 0.85 : undefined;
      const resizedDataUri = canvas.toDataURL(outputType, quality);
      doInsertImage(resizedDataUri, altText, textarea);
    };

    actions.appendChild(cancelBtn);
    actions.appendChild(embedOrigBtn);
    actions.appendChild(embedBtn);

    dialog.appendChild(title);
    dialog.appendChild(preview);
    dialog.appendChild(controls);
    dialog.appendChild(actions);
    overlay.appendChild(dialog);

    // Close on overlay click (outside dialog)
    overlay.addEventListener('click', function(e) {
      if (e.target === overlay) overlay.remove();
    });

    document.body.appendChild(overlay);
  }

  // Insert a base64 data URI as a markdown image at the textarea cursor
  function doInsertImage(dataUri, altText, textarea) {
    const markdownImage = `![${altText}](${dataUri})`;

    // Insert at cursor position
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const before = textarea.value.substring(0, start);
    const after = textarea.value.substring(end);

    // Add newlines around the image for clean markdown
    const prefix = before.length > 0 && !before.endsWith('\n') ? '\n' : '';
    const suffix = after.length > 0 && !after.startsWith('\n') ? '\n' : '';

    textarea.value = before + prefix + markdownImage + suffix + after;

    // Move cursor after the inserted image
    const newPos = start + prefix.length + markdownImage.length + suffix.length;
    textarea.selectionStart = textarea.selectionEnd = newPos;
    textarea.focus();

    // Trigger input event so any listeners (e.g. dirty state) pick up the change
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
    showToast('Image embedded', 'success');
  }

  // Set up paste handler on the edit body textarea to intercept pasted images
  function setupImagePasteHandler() {
    const textarea = document.getElementById('edit-body');
    if (!textarea) return;

    textarea.addEventListener('paste', function(e) {
      const items = e.clipboardData && e.clipboardData.items;
      if (!items) return;

      for (let i = 0; i < items.length; i++) {
        if (items[i].type.startsWith('image/')) {
          e.preventDefault();
          const file = items[i].getAsFile();
          if (file) {
            insertImageAsBase64(file, textarea);
          }
          return; // Only handle the first image
        }
      }
      // If no image items, let the default paste behavior proceed (text paste)
    });
  }

  // Set up drag-and-drop handler on the edit body textarea
  function setupImageDropHandler() {
    const textarea = document.getElementById('edit-body');
    if (!textarea) return;

    textarea.addEventListener('dragover', function(e) {
      // Check if the drag contains files
      if (e.dataTransfer && e.dataTransfer.types.includes('Files')) {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
        textarea.classList.add('drag-over');
      }
    });

    textarea.addEventListener('dragleave', function(e) {
      textarea.classList.remove('drag-over');
    });

    textarea.addEventListener('drop', function(e) {
      textarea.classList.remove('drag-over');
      const files = e.dataTransfer && e.dataTransfer.files;
      if (!files || files.length === 0) return;

      // Process image files from the drop
      let hasImage = false;
      for (let i = 0; i < files.length; i++) {
        if (files[i].type.startsWith('image/')) {
          if (!hasImage) {
            e.preventDefault();
            hasImage = true;
          }
          insertImageAsBase64(files[i], textarea);
        }
      }
    });
  }

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

    // Initialize mermaid with sensible defaults (no auto-render — we trigger manually)
    if (typeof mermaid !== 'undefined') {
      mermaid.initialize({
        startOnLoad: false,
        theme: 'default',
        securityLevel: 'loose',
      });
    }

    // Initialize category input handlers (defined in cats_subcats.js)
    window.app._initCategoryHandlers();

    // Set up image paste and drag-and-drop handlers on the body textarea
    setupImagePasteHandler();
    setupImageDropHandler();

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
    // Re-render after mappings are loaded so categories show in the list
    renderNoteList();
  }

  // Start the app when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

})();
