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
    return wrapMatches(container, re, 'note-search-hit');
  }

  // wrapMatches is the text-node walker shared by the in-note search bar and
  // the list-search highlights (see highlightListSearch below). `re` must be
  // global; each match is wrapped in <mark class=className>. Returns the marks
  // in document order.
  function wrapMatches(container, re, className) {
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
        mark.className = className;
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

  // ============================================
  // List-search highlights
  // ============================================
  //
  // When the toolbar search is narrowing the note list, the previewed note's
  // description and body get the matched text marked, so it is obvious *why*
  // the note matched. This is separate from the in-note search bar above:
  //
  //   - different mark class (list-search-hit), so each side clears only its
  //     own marks and the two can be on at once without fighting;
  //   - no prev/next cursor: the list search is a filter, not a find, so the
  //     marks are passive; the first one is scrolled into view, and the
  //     in-note search bar (with its own counter) remains the way to step.
  //
  // Matching mirrors getFilteredNotes in app.js so what is marked is what
  // made the note match: case-insensitive substring by default, the term as
  // a case-insensitive regex in regex mode. An invalid regex marks nothing
  // (the list filter skips filtering for it too).

  function listSearchContainers() {
    return ['preview-description', 'preview-content']
      .map(id => document.getElementById(id))
      .filter(Boolean);
  }

  function clearListSearch() {
    listSearchContainers().forEach(container => {
      const marks = container.querySelectorAll('mark.list-search-hit');
      if (marks.length === 0) return;
      marks.forEach(m => m.parentNode.replaceChild(document.createTextNode(m.textContent), m));
      // Re-merge the text nodes the marks split, so a later search (either
      // kind) can match across where a mark used to be.
      container.normalize();
    });
  }

  function highlightListSearch(term, isRegex, scroll) {
    clearListSearch();
    term = (term || '').trim();
    if (!term) return 0;
    let re;
    try {
      re = new RegExp(isRegex ? term : escapeRegex(term), 'gi');
    } catch (_) {
      return 0;
    }
    // A pattern that can match the empty string (e.g. "a*") would otherwise
    // "match" between every character; wrapMatches skips zero-length hits,
    // so this just yields whatever non-empty matches exist.
    let first = null;
    let count = 0;
    listSearchContainers().forEach(container => {
      const marks = wrapMatches(container, re, 'list-search-hit');
      if (!first && marks.length) first = marks[0];
      count += marks.length;
    });
    // Bring the first hit into view when asked (app.js asks only when the
    // note or the term changed). 'nearest' leaves the scroll position alone
    // when the hit is already visible.
    if (scroll && first) first.scrollIntoView({ block: 'nearest' });
    return count;
  }

  // Called by app.js after a preview render and whenever the search text or
  // regex mode changes. app.js owns the decision of *whether* to highlight
  // (it knows about edit mode and the advanced query); this only does it.
  window.app._highlightListSearch = highlightListSearch;
  window.app._clearListSearch = clearListSearch;

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