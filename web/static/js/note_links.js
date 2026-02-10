// GoNotes Note Linking Support
// Extracted from app.js — handles [[note:UUID|Title]] link rendering,
// note-to-note navigation, and the "Link to Note" popup dialog.
//
// Dependencies: Loaded after app.js. Accesses shared internals via
// window.app._internal (state, apiRequest, showToast).

(function() {
  'use strict';

  function getState() { return window.app._internal.state; }
  function apiRequest(endpoint, options) { return window.app._internal.apiRequest(endpoint, options); }
  function showToast(message, type) { return window.app._internal.showToast(message, type); }

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
    const note = getState().notes.find(n => n.guid === guid);
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
  // Expose Functions for app.js
  // ============================================

  window.app._renderNoteLinks = renderNoteLinks;

})();
