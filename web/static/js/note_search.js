// In-note text search — highlights matches of a query within the currently
// previewed note's rendered markdown content. Exposes toggle/prev/next/close
// on window.app. State is per-session; closing the bar clears all highlights.
(function() {
  'use strict';
  if (!window.app) window.app = {};

  const state = {
    matches: [],
    currentIdx: -1,
    lastQuery: '',
    caseSensitive: false,
    wholeWord: false,
  };

  function escapeRegex(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  function buildMatcher(query) {
    if (!query) return null;
    const flags = state.caseSensitive ? 'g' : 'gi';
    const pattern = state.wholeWord
      ? '\\b' + escapeRegex(query) + '\\b'
      : escapeRegex(query);
    try {
      return new RegExp(pattern, flags);
    } catch (_) {
      return null;
    }
  }

  function getContainer() {
    return document.getElementById('preview-content');
  }

  function clearHighlights() {
    const container = getContainer();
    if (!container) return;
    container.querySelectorAll('mark.note-search-hit').forEach(m => {
      const text = document.createTextNode(m.textContent);
      m.parentNode.replaceChild(text, m);
    });
    container.normalize();
    state.matches = [];
    state.currentIdx = -1;
  }

  // Walk text nodes under the preview container and wrap substrings matching
  // `query` (case-insensitive). Skips SVG/script/style so we don't corrupt
  // rendered mermaid diagrams or embedded code tooling.
  function highlightMatches(query) {
    const container = getContainer();
    if (!container || !query) return [];
    const re = buildMatcher(query);
    if (!re) return [];
    const matches = [];
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        let p = node.parentNode;
        while (p && p !== container) {
          const tag = p.nodeName ? p.nodeName.toLowerCase() : '';
          if (tag === 'svg' || tag === 'script' || tag === 'style') return NodeFilter.FILTER_REJECT;
          p = p.parentNode;
        }
        return node.nodeValue && node.nodeValue.length > 0
          ? NodeFilter.FILTER_ACCEPT
          : NodeFilter.FILTER_REJECT;
      },
    });
    const nodes = [];
    let n;
    while ((n = walker.nextNode())) nodes.push(n);

    nodes.forEach(node => {
      const text = node.nodeValue;
      re.lastIndex = 0;
      let match;
      let idx = 0;
      const pieces = [];
      while ((match = re.exec(text)) !== null) {
        // Guard against zero-length matches (shouldn't happen here, but safe).
        if (match.index === re.lastIndex) { re.lastIndex++; continue; }
        const start = match.index;
        const end = start + match[0].length;
        if (start > idx) pieces.push(document.createTextNode(text.slice(idx, start)));
        const mark = document.createElement('mark');
        mark.className = 'note-search-hit';
        mark.textContent = text.slice(start, end);
        pieces.push(mark);
        matches.push(mark);
        idx = end;
      }
      if (pieces.length === 0) return;
      if (idx < text.length) pieces.push(document.createTextNode(text.slice(idx)));
      const frag = document.createDocumentFragment();
      pieces.forEach(p => frag.appendChild(p));
      node.parentNode.replaceChild(frag, node);
    });
    return matches;
  }

  function updateCount() {
    const countEl = document.getElementById('note-search-count');
    if (!countEl) return;
    if (state.matches.length === 0) {
      countEl.textContent = state.lastQuery ? '0/0' : '';
    } else {
      countEl.textContent = (state.currentIdx + 1) + '/' + state.matches.length;
    }
  }

  function setCurrent(idx) {
    if (state.matches.length === 0) {
      state.currentIdx = -1;
      updateCount();
      return;
    }
    state.matches.forEach(m => m.classList.remove('note-search-hit-current'));
    const wrapped = ((idx % state.matches.length) + state.matches.length) % state.matches.length;
    state.currentIdx = wrapped;
    const el = state.matches[wrapped];
    el.classList.add('note-search-hit-current');
    el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    updateCount();
  }

  function runSearch(query) {
    clearHighlights();
    state.lastQuery = query;
    if (!query) {
      updateCount();
      return;
    }
    state.matches = highlightMatches(query);
    if (state.matches.length > 0) setCurrent(0);
    else updateCount();
  }

  window.app.toggleNoteSearch = function() {
    const bar = document.getElementById('note-search-bar');
    if (!bar) return;
    const hidden = bar.style.display === 'none' || bar.style.display === '';
    if (hidden) {
      bar.style.display = 'flex';
      const input = document.getElementById('note-search-input');
      if (input) {
        input.focus();
        input.select();
        if (input.value) runSearch(input.value);
      }
    } else {
      window.app.closeNoteSearch();
    }
  };

  window.app.closeNoteSearch = function() {
    const bar = document.getElementById('note-search-bar');
    if (bar) bar.style.display = 'none';
    clearHighlights();
    state.lastQuery = '';
    updateCount();
  };

  window.app.noteSearchNext = function() {
    if (state.matches.length === 0) return;
    setCurrent(state.currentIdx + 1);
  };

  window.app.noteSearchPrev = function() {
    if (state.matches.length === 0) return;
    setCurrent(state.currentIdx - 1);
  };

  function syncToggleButtons() {
    const caseBtn = document.getElementById('btn-search-case');
    const wordBtn = document.getElementById('btn-search-word');
    if (caseBtn) caseBtn.classList.toggle('active', state.caseSensitive);
    if (wordBtn) wordBtn.classList.toggle('active', state.wholeWord);
  }

  window.app.toggleNoteSearchCase = function() {
    state.caseSensitive = !state.caseSensitive;
    syncToggleButtons();
    const input = document.getElementById('note-search-input');
    runSearch(input ? input.value : '');
    if (input) input.focus();
  };

  window.app.toggleNoteSearchWord = function() {
    state.wholeWord = !state.wholeWord;
    syncToggleButtons();
    const input = document.getElementById('note-search-input');
    runSearch(input ? input.value : '');
    if (input) input.focus();
  };

  // Re-run the current query after the preview content is re-rendered
  // (e.g. the user picks a different note while the search bar is open).
  window.app.refreshNoteSearch = function() {
    const bar = document.getElementById('note-search-bar');
    if (!bar || bar.style.display === 'none') return;
    const input = document.getElementById('note-search-input');
    runSearch(input ? input.value : '');
  };

  function init() {
    const input = document.getElementById('note-search-input');
    if (!input) return;
    input.addEventListener('input', () => runSearch(input.value));
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        window.app.closeNoteSearch();
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (e.shiftKey) window.app.noteSearchPrev();
        else window.app.noteSearchNext();
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();