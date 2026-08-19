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
      unsynced: false,
      flagged: false
    },
    sort: {
      field: 'updated_at',
      order: 'desc'
    },
    user: null
  };

  // API configuration
  const API_BASE = '/api/v1';

  // ============================================
  // Markdown Configuration with Syntax Highlighting
  // ============================================

  // Copy icon: two offset rounded rectangles (the familiar "copy to clipboard"
  // glyph). Shared by the fenced-code-block button and the inline-codespan
  // button so both read as the same affordance at their different sizes.
  function copyIconSvg(size) {
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`;
  }

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

      // Wrap in a container with a copy button overlay (top-right).
      // Raw code is stashed in a data attribute so the copy handler can read
      // the original (unescaped, un-highlighted) text.
      const rawB64 = btoa(unescape(encodeURIComponent(code)));
      const copyIcon = copyIconSvg(16);
      return `<div class="code-block-wrapper"><button type="button" class="code-copy-btn" data-code="${rawB64}" onclick="app.copyCodeBlock(this)" title="Copy code" aria-label="Copy code">${copyIcon}</button><pre><code class="hljs language-${resolvedLang || 'plaintext'}">${highlighted}</code></pre></div>`;
    };

    // Inline code (`backticked` spans) gets its own small copy button so short
    // snippets — commands, paths, identifiers — can be lifted out of a note in
    // the read-only view without a fiddly selection drag.
    //
    // Marked API note: current marked passes a token object whose `text` is the
    // RAW source and expects the renderer to escape it; older builds passed an
    // already-escaped plain string. Normalizing to raw text up front lets one
    // renderer serve both, and keeps the clipboard payload un-escaped.
    renderer.codespan = function(token) {
      const raw = typeof token === 'string'
        ? unescapeHtmlEntities(token)
        : (token.text || '');

      // Same stash-as-base64 trick as the block renderer: the button carries
      // the original text so the copy handler never has to reverse escaping or
      // syntax highlighting out of the DOM.
      const rawB64 = btoa(unescape(encodeURIComponent(raw)));
      const copyIcon = copyIconSvg(12);
      return `<span class="inline-code-wrapper"><code>${escapeHtmlForCode(raw)}</code>` +
        `<button type="button" class="inline-copy-btn" data-code="${rawB64}" title="Copy" aria-label="Copy code">${copyIcon}</button></span>`;
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

  // Inverse of escapeHtmlForCode: turns "&lt;div&gt;" back into "<div>".
  // A <textarea> is used deliberately — its content model is raw text, so
  // assigning innerHTML decodes entities without parsing any markup (no tags
  // are constructed, no scripts can run).
  function unescapeHtmlEntities(text) {
    const ta = document.createElement('textarea');
    ta.innerHTML = text;
    return ta.value;
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
        const err = new Error(data.error || 'Request failed');
        // A 409 is a conflict, not a failure: either another session has the
        // note open (a GoNotes TUI in a cats pane, say) or somebody saved it
        // while this form was open. Both carry a detail object explaining
        // which, and both need the SERVER's message shown rather than a
        // generic one — "note is locked by pane w1:p3 since 2m ago" tells the
        // user what to do; "failed to save" does not. Callers test isConflict.
        if (response.status === 409) {
          err.isConflict = true;
          err.conflict = data.data || {};
        }
        throw err;
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

  // loadNotes pulls the WHOLE library into state.notes — no limit parameter.
  //
  // That is deliberate, not careless. Every filter this UI offers (search,
  // regex, category, subcategory, flagged, privacy) runs client-side in
  // getFilteredNotes over state.notes; there is no pagination control and no
  // server-side search. So whatever this request leaves behind is not merely
  // "below the fold" — it is invisible to search, uncountable in the result
  // count, and unreachable by select-all. The old `?limit=100` therefore did
  // not page the library, it silently truncated it: with 400 notes, a search
  // combed 100 of them and select-all-then-delete removed at most 100.
  //
  // Asking for everything costs the server nothing extra either. ListNotes
  // reads both databases in full, merges, and sorts before it applies
  // limit/offset in memory (models/note.go), so N paged requests would be N
  // full scans to obtain what one request already has in hand.
  //
  // Omitting `limit` is how the API spells "no limit" — see ListNotes, where
  // an absent parameter leaves limit at 0 and 0 means unbounded.
  async function loadNotes() {
    updateSyncStatus('syncing', 'Loading...');
    try {
      const response = await apiRequest('/notes');
      if (response && response.data) {
        state.notes = response.data;
        // renderNoteList prunes the batch selection against what it renders,
        // so ids that vanished server-side (deleted elsewhere, synced away)
        // drop out of the selection here rather than lingering as phantoms.
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

    // If the optional Monaco editor is active, flush its content into the
    // hidden textarea so the FormData read below sees the latest body
    if (window.app._syncMonacoToBody) window.app._syncMonacoToBody();

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
      is_private: document.getElementById('edit-private').checked,
      is_flagged: state.currentNote ? (state.currentNote.is_flagged || false) : false
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
        // Name the version this form was opened on, so a save built on a note
        // somebody else has since changed is refused rather than silently
        // overwriting them. Omitted when the note predates the field (an older
        // server), where the server reads a missing version as "do not check"
        // and behaves exactly as it did before.
        if (state.currentNote.version) {
          noteData.expected_version = state.currentNote.version;
        }
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
      if (error.isConflict) {
        // Say what actually happened, and leave the form exactly as it is: the
        // user's text is the only copy of their edit, and a refused save must
        // never be the reason they lose it. A stale conflict also reloads the
        // list, so the winning version is visible alongside the form.
        showToast(error.message, 'error');
        if (error.conflict && error.conflict.reason === 'stale') {
          await loadNotes();
          renderNoteList();
        }
      } else {
        showToast('Failed to save note', 'error');
      }
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

  // ============================================
  // Duplicate Note
  // ============================================

  // COPY_PREFIX marks a duplicate at a glance. It leads the title rather than
  // trailing it so copies group together when the list is sorted or scanned,
  // instead of hiding behind a long title.
  const COPY_PREFIX = 'COPY ';

  // duplicateCurrentNote opens a "what should be copied" dialog instead of
  // duplicating straight away. Categories are the reason it exists: they live
  // in a junction table rather than on the note, so they have to be fetched
  // deliberately — and once fetched they are worth showing, because a copy that
  // silently loses (or silently keeps) a category is the surprising outcome.
  window.app.duplicateCurrentNote = async function() {
    if (!state.currentNote) return;
    const note = state.currentNote;

    // The notes list payload carries no categories, so ask for this note's.
    // A failure here is not fatal: the dialog simply offers no category row.
    let noteCategories = [];
    try {
      const resp = await apiRequest(`/notes/${note.id}/categories`);
      if (resp && Array.isArray(resp.data)) noteCategories = resp.data;
    } catch (error) {
      console.error('Failed to load categories for duplicate:', error);
    }

    showDuplicateDialog(note, noteCategories);
  };

  // categoryDetailLabel renders one note-category link the way the rest of the
  // app writes categories — "Work/backend" — listing every subcategory this
  // note selected so the dialog shows exactly what would carry over.
  function categoryDetailLabel(cat) {
    const subs = cat.selected_subcategories || [];
    if (subs.length === 0) return cat.name;
    return subs.map(sub => `${cat.name}/${sub}`).join(', ');
  }

  // showDuplicateDialog builds the copy options. Only fields the source note
  // actually has get a row: an unchecked-by-default or permanently empty
  // checkbox is noise in a dialog whose whole point is being quick to confirm.
  // Everything shown starts checked — "duplicate" means duplicate, and the
  // user unticks what they don't want.
  function showDuplicateDialog(note, noteCategories) {
    const modalTitle = document.getElementById('modal-title');
    const modalBody = document.getElementById('modal-body');
    const modalFooter = document.getElementById('modal-footer');

    modalTitle.textContent = 'Duplicate Note';

    const option = (id, label, detail) => `
      <label class="dup-option">
        <input type="checkbox" class="dup-checkbox" id="${id}" checked>
        <span class="dup-option-label">${label}</span>
        ${detail ? `<span class="dup-option-detail">${detail}</span>` : ''}
      </label>`;

    const rows = [];
    if (noteCategories.length > 0) {
      rows.push(option('dup-categories', 'Categories &amp; subcategories',
        escapeHtml(noteCategories.map(categoryDetailLabel).join(', '))));
    }
    if (note.body) rows.push(option('dup-body', 'Body'));
    if (note.description) rows.push(option('dup-description', 'Description'));
    if (note.tags) rows.push(option('dup-tags', 'Tags', escapeHtml(note.tags)));
    if (note.is_private) rows.push(option('dup-private', 'Private'));
    if (note.is_flagged) rows.push(option('dup-flagged', 'Follow-up flag'));

    modalBody.innerHTML = `
      <div class="form-group">
        <label class="form-label" for="dup-title">Title</label>
        <input type="text" class="form-input" id="dup-title" placeholder="Note title...">
      </div>
      ${rows.length > 0
        ? `<p class="settings-description">Also copy from the original:</p>
           <div class="dup-options">${rows.join('')}</div>`
        : ''}
    `;

    // The title is assigned as a property, not interpolated into the markup:
    // escapeHtml() escapes element text, not attribute quotes, so a title
    // containing a double quote would otherwise break out of value="…".
    const titleInput = document.getElementById('dup-title');
    titleInput.value = COPY_PREFIX + note.title;

    // Enter anywhere in the title is the same as clicking Duplicate — this is
    // a two-field dialog, not a form worth tabbing through.
    titleInput.addEventListener('keydown', function(e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        window.app.confirmModal();
      }
    });

    modalFooter.style.display = '';
    const confirmBtn = document.getElementById('modal-confirm');
    if (confirmBtn) confirmBtn.textContent = 'Duplicate';
    modalConfirmHandler = () => performDuplicate(note, noteCategories);

    document.getElementById('modal-overlay').classList.add('open');
    titleInput.focus();
    // Caret at the end rather than a full selection: the prefix is the default
    // answer, so the next keystroke should extend the title, not wipe it.
    titleInput.setSelectionRange(titleInput.value.length, titleInput.value.length);
  }

  // performDuplicate creates the copy from whatever the dialog left checked.
  // A missing checkbox reads as "not offered, so not copied", which is what
  // showDuplicateDialog means by omitting a row for an empty field.
  async function performDuplicate(note, noteCategories) {
    const checked = (id) => {
      const el = document.getElementById(id);
      return !!(el && el.checked);
    };

    const titleInput = document.getElementById('dup-title');
    const title = titleInput ? titleInput.value.trim() : '';
    if (!title) {
      showToast('Title is required', 'error');
      return;
    }

    const copyCategories = checked('dup-categories');
    const bodyContent = checked('dup-body') ? note.body : null;

    const noteData = {
      guid: generateGUID(),
      title: title,
      description: checked('dup-description') ? note.description : null,
      tags: checked('dup-tags') ? note.tags : null,
      is_private: checked('dup-private'),
      is_flagged: checked('dup-flagged')
    };

    // Body field selection mirrors saveNote: msgpack when available, plain
    // text otherwise, and plain text again if the encoder declines.
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

    const confirmBtn = document.getElementById('modal-confirm');
    if (confirmBtn) {
      confirmBtn.disabled = true;
      confirmBtn.textContent = 'Duplicating...';
    }

    try {
      const response = await apiRequest('/notes', {
        method: 'POST',
        body: JSON.stringify(noteData)
      });
      if (!response || !response.data) throw new Error('empty create response');

      const newNoteId = response.data.id;

      if (copyCategories) {
        // Category links are created one at a time, after the note exists.
        // The POST body carries this note's own subcategory selection, so the
        // copy keeps not just the categories but which subcategories were
        // ticked for the original — that pairing is the point of the feature.
        for (const cat of noteCategories) {
          try {
            await apiRequest(`/notes/${newNoteId}/categories/${cat.id}`, {
              method: 'POST',
              body: JSON.stringify({ subcategories: cat.selected_subcategories || [] })
            });
          } catch (catError) {
            // Secondary to the note itself: keep the copy, name what it lost.
            console.error('Failed to copy category to duplicate:', catError);
            showToast(`Could not copy category "${cat.name}"`, 'warning');
          }
        }
      }

      // closeModal also restores the shared footer button's label and state.
      window.app.closeModal();
      showToast('Note duplicated', 'success');

      await loadNotes();
      if (copyCategories) await window.app._loadNoteCategoryMappings();
      renderNoteList();
      window.app.selectNote(newNoteId);
    } catch (error) {
      // Leave the dialog open with the user's title intact so they can retry.
      console.error('Duplicate failed:', error);
      showToast('Failed to duplicate note', 'error');
      if (confirmBtn) {
        confirmBtn.disabled = false;
        confirmBtn.textContent = 'Duplicate';
      }
    }
  }

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

    const descEl = document.getElementById('preview-description');
    if (note.description && note.description.trim()) {
      descEl.textContent = note.description;
      descEl.style.display = '';
    } else {
      descEl.textContent = '';
      descEl.style.display = 'none';
    }

    // Render meta information (tags removed — categories shown separately)
    const metaHtml = [];
    if (note.is_flagged) {
      metaHtml.push('<span class="preview-meta-item preview-flag-indicator"><span class="flag-icon-red">&#9873;</span> Flagged</span>');
    }
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
    // (delegated to note_links.js)
    if (window.app._renderNoteLinks) window.app._renderNoteLinks();

    // Render any mermaid diagrams found in the preview content
    renderMermaidDiagrams();

    // If the in-note search bar is open, re-run the query against the new content.
    if (window.app.refreshNoteSearch) window.app.refreshNoteSearch();
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
    const descEl = document.getElementById('preview-description');
    descEl.textContent = '';
    descEl.style.display = 'none';
    document.getElementById('preview-meta').innerHTML = '';
    document.getElementById('preview-categories').innerHTML = '';
    document.getElementById('preview-content').innerHTML = '<p class="text-muted">Select a note from the list to preview its content.</p>';
    document.getElementById('preview-footer').style.display = 'none';
    if (window.app.closeNoteSearch) window.app.closeNoteSearch();
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

    // The batch selection is reconciled against the rows about to be drawn, on
    // every render, so the invariant "what is selected is what is visible"
    // holds no matter which path got us here.
    reconcileSelection(filteredNotes);
    updateBatchActions(filteredNotes.length);

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
    // Flag icon — always present, toggles on click
    const flagIcon = document.createElement('span');
    flagIcon.className = 'note-flag-icon' + (note.is_flagged ? ' flagged' : '');
    flagIcon.title = note.is_flagged ? 'Remove flag' : 'Flag for follow-up';
    flagIcon.innerHTML = '&#9873;'; // Unicode flag character U+2691
    flagIcon.onclick = (e) => {
      e.stopPropagation();
      window.app.toggleNoteFlag(note.id);
    };
    titleRow.appendChild(flagIcon);

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

    // Apply flagged filter
    if (state.filters.flagged) {
      notes = notes.filter(note => note.is_flagged);
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

  window.app.toggleFlaggedFilter = function(checked) {
    state.filters.flagged = checked;
    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  window.app.toggleNoteFlag = async function(noteId) {
    try {
      const response = await apiRequest(`/notes/${noteId}/flag`, { method: 'PUT' });
      if (response && response.data) {
        // Update in state.notes
        const idx = state.notes.findIndex(n => n.id === noteId);
        if (idx !== -1) {
          state.notes[idx].is_flagged = response.data.is_flagged;
        }
        // Update currentNote if it's the same
        if (state.currentNote && state.currentNote.id === noteId) {
          state.currentNote.is_flagged = response.data.is_flagged;
          renderPreview(state.currentNote);
        }
        renderNoteList();
      }
    } catch (error) {
      showToast('Failed to toggle flag', 'error');
    }
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
      unsynced: false,
      flagged: false
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
    const flaggedEl = document.getElementById('filter-flagged');
    if (flaggedEl) flaggedEl.checked = false;

    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  // ============================================
  // Sorting
  // ============================================

  // setSort — selects a sort field from the dropdown, resets direction to desc
  window.app.setSort = function(field) {
    state.sort.field = field;
    state.sort.order = 'desc';

    const labels = { updated_at: 'Modified', created_at: 'Created', title: 'Title' };
    document.getElementById('sort-label').textContent = labels[field] || field;

    updateSortDirIcon();
    window.app.toggleSortMenu();
    renderNoteList();
    updateActiveFilters();
  };

  // cycleSortDir — cycles through desc → asc → off (default updated_at desc)
  // "off" resets to the default sort (Modified descending)
  window.app.cycleSortDir = function() {
    if (state.sort.order === 'desc') {
      state.sort.order = 'asc';
    } else if (state.sort.order === 'asc') {
      // Reset to default sort
      state.sort.field = 'updated_at';
      state.sort.order = 'desc';
      document.getElementById('sort-label').textContent = 'Modified';
    }
    updateSortDirIcon();
    renderNoteList();
    updateActiveFilters();
  };

  // updateSortDirIcon — syncs the arrow icon with current sort direction
  function updateSortDirIcon() {
    var icon = document.getElementById('sort-dir-icon');
    if (!icon) return;
    icon.textContent = state.sort.order === 'desc' ? '\u25BC' : '\u25B2';
  }

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

  // reconcileSelection drops any batch-selected note the list is not showing.
  //
  // The selection is a set of note ids kept across re-renders, which is what
  // makes ticking several boxes possible at all. Kept across a change of *view*,
  // though, it turns into a trap: select twenty notes, then search or switch
  // category, and the batch bar still reads "20 selected" above three rows — so
  // "Delete 20 notes?" quietly removes seventeen notes that are nowhere on
  // screen. Same trap in the other direction after a reload: an id deleted from
  // another session (a TUI, a sync pull) stays in the set as a phantom that
  // every later batch re-attempts and fails on.
  //
  // Reconciling here, at the single choke point every filter/sort/search/reload
  // path funnels through, covers those paths by construction — including ones
  // added later, which is the reason it lives here rather than as a clear()
  // sprinkled over a dozen filter mutation sites.
  //
  // The resulting rule: a note that falls out of the filter is deselected, and
  // re-widening the filter does not bring the tick back. That is the honest
  // reading of "Delete N notes" — N is what you can see.
  function reconcileSelection(visibleNotes) {
    if (state.selectedNotes.size === 0) return;
    const visible = new Set(visibleNotes.map(note => note.id));
    for (const id of state.selectedNotes) {
      // Deleting the entry the loop is standing on is defined behaviour for a
      // Set iterator: it simply is not revisited.
      if (!visible.has(id)) state.selectedNotes.delete(id);
    }
  }

  // updateBatchActions shows/hides the batch bar and syncs the header tick.
  //
  // visibleCount is how many rows the list is currently showing. Callers that
  // already filtered pass it in; the rest omit it and pay for one more filter
  // pass, which is cheap next to the full re-render that follows.
  function updateBatchActions(visibleCount) {
    const batchBar = document.getElementById('batch-actions');
    const count = state.selectedNotes.size;

    batchBar.classList.toggle('visible', count > 0);
    // Written even when the bar is hidden, so an emptied selection can never
    // flash a stale "20 selected" the moment the bar comes back.
    document.getElementById('batch-count').textContent = `${count} selected`;

    // The header checkbox has to follow the selection, not lead it. Left
    // ticked after reconcileSelection has dropped notes, the user's next click
    // on it registers as "deselect all" when they meant "select all".
    const selectAll = document.getElementById('select-all');
    if (!selectAll) return;
    const visible = visibleCount === undefined ? getFilteredNotes().length : visibleCount;
    selectAll.checked = visible > 0 && count >= visible;
    // Indeterminate is the partial-selection state a plain checkbox cannot
    // otherwise express — and the state a batch bar is usually in.
    selectAll.indeterminate = count > 0 && count < visible;
  }

  // deleteSelected removes every checked note, one request at a time.
  //
  // Serial rather than parallel on purpose: each delete is gated by the
  // note-lock registry and recorded as a sync change against a single-process
  // store, so firing fifty concurrent DELETEs buys no throughput and scrambles
  // the order failures come back in.
  //
  // The part that matters is what happens to the notes that DON'T delete. A
  // note another session holds open (a GoNotes TUI in a cats pane, say) answers
  // 409 and stays exactly where it was. Reporting a flat "Notes deleted" made a
  // batch that deleted nothing look identical to one that deleted everything —
  // which is precisely what "delete isn't working" looks like from the outside.
  // So survivors stay in the selection (their checkboxes come back ticked after
  // the reload, ready to retry once the other session lets go) and the toast
  // says how many were left behind and why.
  window.app.deleteSelected = async function() {
    const total = state.selectedNotes.size;
    if (total === 0) return;
    if (!confirm(`Delete ${total} notes?`)) return;

    // Iterate a snapshot: the live set is edited as each delete lands, so that
    // a mid-batch failure leaves exactly the survivors selected.
    const targets = [...state.selectedNotes];
    const failures = [];

    for (const noteId of targets) {
      try {
        await apiRequest(`/notes/${noteId}`, { method: 'DELETE' });
        state.selectedNotes.delete(noteId);
        // The preview pane may be showing a note that no longer exists.
        if (state.currentNote && state.currentNote.id === noteId) {
          state.currentNote = null;
          clearPreview();
        }
      } catch (error) {
        // apiRequest has already toasted the server's own wording ("note is
        // locked by pane w1:p3 since 2m ago"); keep it so the summary can
        // repeat the first one instead of the useless "some failed".
        console.error('Failed to delete note:', noteId, error);
        failures.push({ noteId: noteId, message: error.message });
      }
    }

    updateBatchActions();
    await loadNotes();

    if (failures.length === 0) {
      showToast(total === 1 ? 'Note deleted' : `${total} notes deleted`, 'success');
    } else if (failures.length === total) {
      showToast(`Nothing deleted — ${failures[0].message}`, 'error');
    } else {
      showToast(
        `Deleted ${total - failures.length} of ${total}; ${failures.length} still selected — ${failures[0].message}`,
        'error');
    }
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

    // Setting .value fires no events, so push the body into Monaco explicitly
    // when the optional Monaco editor is present (monaco_editor.js)
    if (window.app._syncBodyToMonaco) window.app._syncBodyToMonaco();

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

    // Mirror the cleared body into Monaco when the optional editor is present
    if (window.app._syncBodyToMonaco) window.app._syncBodyToMonaco();

    // Clear multi-category entries
    window.app._clearCategoryEntries();
  }

  function showEditMode() {
    document.getElementById('preview-mode').classList.add('hidden');
    document.getElementById('edit-mode').classList.add('active');
    document.getElementById('edit-title').focus();

    // Activate the Monaco editor if the user has opted into it
    // (monaco_editor.js — loads Monaco lazily on first use)
    if (window.app._monacoOnEditShown) window.app._monacoOnEditShown();
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

  // buildQueryString — produces a human-readable query string from the current
  // filter and sort state, e.g.: search:"golang" regex:on cat:"Programming" sort:updated_at↓
  // When no filters are active, returns a minimal string like "all notes sort:updated_at↓"
  function buildQueryString() {
    const parts = [];

    if (state.filters.search) {
      parts.push(`search:"${state.filters.search}"`);
    }
    if (state.filters.regex) {
      parts.push('regex:on');
    }
    if (state.filters.categoryName) {
      parts.push(`cat:"${state.filters.categoryName}"`);
      if (state.filters.subcategories.length > 0) {
        parts.push(`subcats:"${state.filters.subcategories.join(',')}"`);
      }
    }
    if (state.filters.privacy !== 'all') {
      parts.push(`privacy:${state.filters.privacy}`);
    }
    if (state.filters.date !== 'all') {
      parts.push(`date:${state.filters.date}`);
    }

    // Always include sort so the user knows the ordering
    const arrow = state.sort.order === 'desc' ? '\u2193' : '\u2191';
    parts.push(`sort:${state.sort.field}${arrow}`);

    // If the only part is sort, prefix with "all notes" for clarity
    if (parts.length === 1) {
      return 'all notes ' + parts[0];
    }
    return parts.join(' ');
  }

  function updateActiveFilters() {
    const queryDisplay = document.getElementById('query-display');
    const queryPopupText = document.getElementById('query-popup-text');
    if (!queryDisplay) return;

    const fullQuery = buildQueryString();

    // Truncate the visible display to ~60 chars with ellipsis
    const maxLen = 60;
    queryDisplay.textContent = fullQuery.length > maxLen
      ? fullQuery.substring(0, maxLen) + '\u2026'
      : fullQuery;

    // Popup always shows the full, untruncated query
    if (queryPopupText) {
      queryPopupText.textContent = fullQuery;
    }
  }

  // copyCodeBlock — copies a rendered code block's original source to clipboard.
  // Invoked from the copy button overlay injected by the marked code renderer.
  window.app.copyCodeBlock = function(btn) {
    try {
      const code = decodeURIComponent(escape(atob(btn.dataset.code || '')));
      navigator.clipboard.writeText(code).then(() => {
        btn.classList.add('copied');
        setTimeout(() => btn.classList.remove('copied'), 1200);
      }).catch(() => {
        showToast('Failed to copy code', 'error');
      });
    } catch (err) {
      showToast('Failed to copy code', 'error');
    }
  };

  // copyQuery — copies the full query condition string to the clipboard
  window.app.copyQuery = function() {
    const fullQuery = buildQueryString();
    navigator.clipboard.writeText(fullQuery).then(() => {
      showToast('Query copied', 'success');
    }).catch(() => {
      showToast('Failed to copy query', 'error');
    });
  };

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

  // ============================================
  // Modal Dialogs
  // ============================================

  // modalConfirmHandler lets one dialog own the shared footer's primary button.
  // Null means the generic modal, whose Confirm does nothing but dismiss. The
  // handler closes the modal itself, so a failed action can keep the dialog
  // open with the user's input still in it.
  let modalConfirmHandler = null;

  window.app.closeModal = function() {
    document.getElementById('modal-overlay').classList.remove('open');
    modalConfirmHandler = null;
    // Restore default footer visibility in case a modal hid it
    const footer = document.getElementById('modal-footer');
    if (footer) footer.style.display = '';
    // ...and the shared button's default label/state, since a dialog may have
    // renamed it ("Duplicate") or disabled it while its action was in flight.
    const confirmBtn = document.getElementById('modal-confirm');
    if (confirmBtn) {
      confirmBtn.disabled = false;
      confirmBtn.textContent = 'Confirm';
    }
  };

  window.app.confirmModal = function() {
    if (modalConfirmHandler) {
      modalConfirmHandler();
      return;
    }
    window.app.closeModal();
  };

  // ============================================
  // Settings Modal
  // ============================================

  window.app.showSettings = function() {
    const modalTitle = document.getElementById('modal-title');
    const modalBody = document.getElementById('modal-body');
    const modalFooter = document.getElementById('modal-footer');

    modalTitle.textContent = 'Settings';

    // Build settings content — export section visible only to admins
    let html = '';
    if (state.user && state.user.is_admin) {
      html += `
        <div class="settings-section">
          <h3>Spoke Configuration Export</h3>
          <p class="settings-description">
            Generate a config file for setting up a new spoke instance.
            This creates an invite token and packages all required settings.
          </p>
          <div class="form-group">
            <label class="form-label" for="export-password">Confirm your password</label>
            <input type="password" class="form-input" id="export-password"
                   placeholder="Enter your login password" autocomplete="current-password">
          </div>
          <button class="btn btn-primary" onclick="app.exportSpokeConfig()">
            Export Spoke Config
          </button>
        </div>
      `;
    }
    if (!html) {
      html = '<p>No settings available.</p>';
    }

    modalBody.innerHTML = html;
    // Hide default footer buttons — this modal uses its own inline buttons
    modalFooter.style.display = 'none';
    document.getElementById('modal-overlay').classList.add('open');
  };

  // exportSpokeConfig sends the admin's password to the hub, which verifies
  // it and returns a downloadable JSON file containing all spoke env vars.
  window.app.exportSpokeConfig = async function() {
    const passwordInput = document.getElementById('export-password');
    const password = passwordInput ? passwordInput.value : '';
    if (!password) {
      showToast('Please enter your password', 'warning');
      return;
    }

    const token = localStorage.getItem('token');
    try {
      const response = await fetch(`${API_BASE}/admin/export-spoke-config`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${token}`
        },
        body: JSON.stringify({ password })
      });

      if (!response.ok) {
        // The error response is JSON even though success is a file download
        const data = await response.json();
        showToast(data.error || 'Export failed', 'error');
        return;
      }

      // Trigger browser file download from the response blob
      const blob = await response.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      // Extract filename from Content-Disposition header, fall back to default
      const disposition = response.headers.get('Content-Disposition') || '';
      const match = disposition.match(/filename="?([^"]+)"?/);
      a.download = match ? match[1] : 'gonotes-spoke-config.json';
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.URL.revokeObjectURL(url);

      showToast('Spoke config exported successfully', 'success');
      window.app.closeModal();
    } catch (err) {
      console.error('Export error:', err);
      showToast('Network error during export', 'error');
    }
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
    // A modal owns the screen while it is open: Escape dismisses it (including
    // from inside its own inputs, where the edit-form branch below would not
    // fire), and every other shortcut is swallowed so keys like 'n' can't act
    // on the list hidden behind the dialog.
    const overlay = document.getElementById('modal-overlay');
    if (overlay && overlay.classList.contains('open')) {
      if (e.key === 'Escape') {
        e.preventDefault();
        window.app.closeModal();
      }
      return;
    }

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
  // Copy buttons inside rendered markdown
  // ============================================

  // Delegated because the preview HTML is regenerated on every note render and
  // then passed through DOMPurify, which strips inline on* handlers from the
  // sanitized output. A listener on `document` survives both.
  //
  // The `btn.onclick` check keeps this compatible with markup whose inline
  // handler did survive sanitization: there the attribute handler has already
  // copied, so bailing out avoids writing the clipboard twice.
  document.addEventListener('click', function(e) {
    const btn = e.target.closest('.code-copy-btn, .inline-copy-btn');
    if (!btn || typeof btn.onclick === 'function') return;
    // An inline codespan can sit inside a markdown link; suppress the
    // navigation that clicking through the button would otherwise trigger.
    e.preventDefault();
    window.app.copyCodeBlock(btn);
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
    updateActiveFilters,
    updateSyncStatus,
    loadNotes,
    generateGUID,
    formatRelativeTime
  };

  // ============================================
  // Panel Splitter — drag to resize notes list / preview
  // ============================================
  function initPanelSplitter() {
    const splitter = document.getElementById('panel-splitter');
    const rightPanel = document.getElementById('right-panel');
    const appMain = document.querySelector('.app-main');
    if (!splitter || !rightPanel || !appMain) return;

    let startX, startWidth;

    function onMouseDown(e) {
      e.preventDefault();
      startX = e.clientX;
      startWidth = rightPanel.getBoundingClientRect().width;
      splitter.classList.add('dragging');
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      document.addEventListener('mousemove', onMouseMove);
      document.addEventListener('mouseup', onMouseUp);
    }

    function onMouseMove(e) {
      // Dragging left increases right panel width, dragging right decreases it.
      // Max is capped so the center list still has room to breathe, but is
      // permissive enough to let the preview dominate the viewport.
      const delta = startX - e.clientX;
      const containerWidth = appMain.getBoundingClientRect().width;
      const newWidth = Math.min(
        Math.max(250, startWidth + delta),
        Math.max(250, containerWidth - 240)
      );
      rightPanel.style.width = newWidth + 'px';
    }

    function onMouseUp() {
      splitter.classList.remove('dragging');
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      document.removeEventListener('mousemove', onMouseMove);
      document.removeEventListener('mouseup', onMouseUp);
      // Persist preference
      localStorage.setItem('gonotes-splitter-width', rightPanel.style.width);
    }

    splitter.addEventListener('mousedown', onMouseDown);

    // Restore saved width
    const saved = localStorage.getItem('gonotes-splitter-width');
    if (saved) {
      rightPanel.style.width = saved;
    }
  }

  // ============================================
  // Focus Mode — collapse filter/list, expand preview to full width
  // ============================================
  // Toggles `.focus-mode` on `.app-main`. State is persisted so the layout
  // survives reloads. A left-edge handle (rendered in page.go) calls this
  // same toggle to restore the normal three-pane layout.
  window.app.toggleFocusMode = function() {
    const appMain = document.getElementById('app-main');
    if (!appMain) return;
    const enabled = !appMain.classList.contains('focus-mode');
    appMain.classList.toggle('focus-mode', enabled);
    localStorage.setItem('gonotes-focus-mode', enabled ? '1' : '0');
    const btn = document.getElementById('btn-focus-mode');
    if (btn) {
      btn.title = enabled ? 'Exit focus mode' : 'Toggle focus mode (expand preview)';
    }
  };

  function initFocusMode() {
    if (localStorage.getItem('gonotes-focus-mode') === '1') {
      const appMain = document.getElementById('app-main');
      if (appMain) appMain.classList.add('focus-mode');
      const btn = document.getElementById('btn-focus-mode');
      if (btn) btn.title = 'Exit focus mode';
    }
  }

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

    // Initialize draggable splitter between notes list and preview panel
    initPanelSplitter();

    // Restore focus-mode state from localStorage (must run after DOM ready)
    initFocusMode();

    // Initialize category input handlers (defined in cats_subcats.js)
    window.app._initCategoryHandlers();

    // Initialize sync module (defined in sync.js)
    window.app._initSyncHandlers();
    // Set up image paste and drag-and-drop handlers on the body textarea
    // (delegated to image_embed.js)
    if (window.app._setupImagePasteHandler) window.app._setupImagePasteHandler();
    if (window.app._setupImageDropHandler) window.app._setupImageDropHandler();

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
