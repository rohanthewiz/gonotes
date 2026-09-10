// Advanced search — an SQL-shaped query bar with server-driven autocomplete.
//
// The language, the completions and the results all come from the server
// (models/query*.go, exposed at /api/v1/notes/query{,/complete,/schema}), and
// this file is the keyboard and the popup around them. Nothing about the note
// schema is written here on purpose: the field list, the operators, the
// examples and even the "how this differs from SQL" notes are fetched from
// /query/schema, so adding a column to the note model does not need a line of
// JavaScript to become queryable.
//
//	 input ──keystroke──▶ /query/complete?q=…&pos=…  ──▶ popup
//	   │                                                  │
//	   └──Enter──▶ /query?q=…  ──▶ state.advanced  ──▶ the note list
//	                    │
//	                    └─400──▶ message + the offending range selected
//
// Two design notes worth knowing before editing:
//
//   - Completion is a round trip per keystroke, debounced. That is deliberate:
//     half the useful suggestions ARE the user's data (their categories,
//     subcategories, tags, note titles), which the browser has no authoritative
//     copy of, and the request is a few hundred bytes against localhost or a
//     LAN hub. A stale response is discarded by sequence number rather than
//     racing the input.
//   - Accepting a suggestion is a pure splice: the server returns the byte
//     range to replace and the exact text to put there, quoting and trailing
//     space included, so this file never decides how to quote a value. The TUI
//     does the same with the same numbers.
(function() {
  'use strict';
  if (!window.app) window.app = {};

  const internal = () => window.app._internal;
  const API = '/api/v1';
  const HISTORY_KEY = 'gonotes-query-history';
  const HISTORY_MAX = 25;
  const OPEN_KEY = 'gonotes-query-bar-open';

  const state = {
    open: false,
    schema: null,          // /query/schema, fetched once on first open
    suggestions: [],
    highlighted: -1,
    replaceStart: 0,
    replaceEnd: 0,
    completeSeq: 0,        // guards against out-of-order completion responses
    running: false,
    lastRun: '',            // the query text of the last successful run
    lastError: null,
    context: '',           // what the server says is being completed
    helpOpen: false
  };

  // ============================================
  // Element accessors
  // ============================================

  const el = {
    bar:      () => document.getElementById('advanced-search-bar'),
    input:    () => document.getElementById('advanced-query-input'),
    popup:    () => document.getElementById('advanced-query-popup'),
    status:   () => document.getElementById('advanced-query-status'),
    error:    () => document.getElementById('advanced-query-error'),
    help:     () => document.getElementById('advanced-query-help'),
    toggle:   () => document.getElementById('btn-advanced-search'),
    hint:     () => document.getElementById('advanced-query-hint')
  };

  function escapeHtml(s) {
    const fn = internal() && internal().escapeHtml;
    if (fn) return fn(s);
    return String(s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
    })[c]);
  }

  // authedFetch is used instead of app.js's apiRequest for one reason: a 400
  // from the query endpoint carries the POSITION of the syntax error in its
  // data, and apiRequest turns a non-ok response into an Error that keeps only
  // the message. Underlining the mistake is most of what makes the box usable,
  // so this path keeps the whole envelope.
  async function authedFetch(path) {
    const token = internal() && internal().getAuthToken
      ? internal().getAuthToken()
      : localStorage.getItem('token');
    const headers = { 'Content-Type': 'application/json' };
    if (token) headers['Authorization'] = 'Bearer ' + token;
    const resp = await fetch(API + path, { headers });
    let body = null;
    try {
      body = await resp.json();
    } catch (_) {
      body = null;
    }
    return { ok: resp.ok, status: resp.status, body: body || {} };
  }

  // ============================================
  // Opening and closing
  // ============================================

  window.app.toggleAdvancedSearch = function() {
    setOpen(!state.open);
  };

  function setOpen(open) {
    state.open = open;
    const bar = el.bar();
    if (bar) bar.hidden = !open;
    const btn = el.toggle();
    if (btn) {
      btn.classList.toggle('active', open);
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    }
    try {
      localStorage.setItem(OPEN_KEY, open ? '1' : '0');
    } catch (_) { /* private browsing */ }

    if (open) {
      loadSchema();
      const input = el.input();
      if (input) {
        input.focus();
        input.select();
      }
      // An empty box is where the language most needs to introduce itself.
      requestCompletion(true);
    } else {
      hidePopup();
    }
  }

  // The simple search bar and the query bar answer the same need in different
  // languages, so leaving the query bar restores the simple one's results
  // rather than leaving the list showing a query nobody can see any more.
  window.app.clearAdvancedQuery = function() {
    const st = internal().state;
    st.advanced.active = false;
    st.advanced.text = '';
    st.advanced.notes = null;
    st.advanced.ordered = false;
    st.advanced.matched = 0;
    st.advanced.scanned = 0;
    const input = el.input();
    if (input) input.value = '';
    showError(null);
    setStatus('');
    hidePopup();
    refreshList();
  };

  window.app.closeAdvancedSearch = function() {
    window.app.clearAdvancedQuery();
    setOpen(false);
  };

  function refreshList() {
    const i = internal();
    i.renderNoteList();
    i.updateResultCount();
    i.updateActiveFilters();
  }

  // ============================================
  // Running a query
  // ============================================

  window.app.runAdvancedQuery = async function() {
    const input = el.input();
    if (!input) return;
    const text = input.value.trim();
    const st = internal().state;

    if (!text) {
      window.app.clearAdvancedQuery();
      return;
    }

    state.running = true;
    setStatus('Running…');
    // Invalidate any completion still in flight. Without this, a request
    // issued by the keystroke that finished the query lands AFTER the run and
    // re-opens the popup over the results — visible as a suggestion list
    // hanging above a list it is no longer describing.
    state.completeSeq++;
    clearTimeout(completeTimer);
    const res = await authedFetch('/notes/query?q=' + encodeURIComponent(text));
    state.running = false;

    if (!res.ok) {
      // A syntax error is the user's and points at itself; anything else is
      // the server's and gets the message it sent.
      const qe = res.body.data;
      if (res.status === 400 && qe && typeof qe.position === 'number') {
        showError(res.body.error || 'invalid query', qe);
      } else {
        showError(res.body.error || ('request failed (' + res.status + ')'), null);
      }
      setStatus('');
      return;
    }

    const data = res.body.data || {};
    showError(null);

    st.advanced.active = true;
    st.advanced.text = text;
    st.advanced.notes = data.notes || [];
    st.advanced.ordered = !!data.ordered;
    st.advanced.matched = data.matched || 0;
    st.advanced.scanned = data.scanned || 0;

    rememberQuery(text);
    state.lastRun = text;
    setStatus(describeResult(data));
    hidePopup();
    refreshList();
  };

  function describeResult(data) {
    const bits = [];
    bits.push(data.matched === 1 ? '1 note' : (data.matched || 0) + ' notes');
    if (typeof data.scanned === 'number') bits.push('of ' + data.scanned + ' scanned');
    if (typeof data.elapsed_ms === 'number') bits.push(data.elapsed_ms.toFixed(1) + ' ms');
    if (data.includes_deleted) bits.push('including deleted');
    let out = bits.join(' · ');
    // The normalized form is the server's reading of what was typed, and it is
    // the cheapest possible answer to "why did that match?" — an alias that
    // resolved somewhere unexpected is visible at a glance. It is only worth
    // screen space when it actually differs from what was typed, so a query
    // already in canonical form does not get echoed back verbatim.
    if (data.normalized && !sameQueryText(data.normalized, state.lastRun || '')) {
      out += '  \u2192  ' + data.normalized;
    }
    return out;
  }

  // sameQueryText compares two renderings of a query ignoring case and
  // whitespace runs, which are exactly the differences normalization is
  // allowed to introduce on its own.
  function sameQueryText(a, b) {
    const norm = t => t.replace(/\s+/g, ' ').trim().toLowerCase();
    return norm(a) === norm(b);
  }

  function setStatus(text) {
    const s = el.status();
    if (s) s.textContent = text;
  }

  // showError displays a message and, when the error carries a position,
  // SELECTS the offending range in the input. Selecting rather than merely
  // reporting the offset means the fix starts with the next keystroke.
  function showError(message, qe) {
    state.lastError = qe || null;
    const e = el.error();
    const input = el.input();
    if (!e) return;
    if (!message) {
      e.hidden = true;
      e.textContent = '';
      if (input) input.classList.remove('has-error');
      return;
    }
    let text = message;
    if (qe && qe.hint) text += ' — ' + qe.hint;
    e.textContent = text;
    e.hidden = false;
    if (input) {
      input.classList.add('has-error');
      if (qe && typeof qe.position === 'number') {
        const start = Math.min(qe.position, input.value.length);
        const end = Math.min(start + Math.max(qe.length || 1, 1), input.value.length);
        input.focus();
        try {
          input.setSelectionRange(start, end);
        } catch (_) { /* not all inputs allow it */ }
      }
    }
  }

  // ============================================
  // Completion
  // ============================================

  let completeTimer = null;

  function requestCompletion(immediate) {
    clearTimeout(completeTimer);
    const delay = immediate ? 0 : 110;
    completeTimer = setTimeout(fetchCompletion, delay);
  }

  async function fetchCompletion() {
    const input = el.input();
    if (!input || !state.open) return;

    const q = input.value;
    const pos = input.selectionStart === null ? q.length : input.selectionStart;
    const seq = ++state.completeSeq;

    const res = await authedFetch(
      '/notes/query/complete?q=' + encodeURIComponent(q) + '&pos=' + pos);

    // A response that is no longer the latest is dropped rather than rendered:
    // typing faster than the round trip must not make an older suggestion list
    // win.
    if (seq !== state.completeSeq || !state.open) return;
    if (!res.ok) { hidePopup(); return; }

    const data = res.body.data || {};
    let suggestions = data.suggestions || [];

    // Local history leads when the box is empty — the most likely next query
    // is one already run, and the server cannot know them.
    if (!q.trim()) {
      const hist = loadHistory().map(h => ({
        text: h, label: h, kind: 'history', detail: 'recent'
      }));
      suggestions = hist.concat(suggestions);
    }

    state.suggestions = suggestions;
    state.replaceStart = data.replace_start || 0;
    state.replaceEnd = typeof data.replace_end === 'number' ? data.replace_end : pos;
    state.highlighted = suggestions.length > 0 ? 0 : -1;
    state.context = data.context || '';
    renderPopup();
  }

  const KIND_ICON = {
    field: '▤',
    operator: '=',
    keyword: 'ⓚ',
    value: '\u25c6',
    logic: '∧',
    example: '★',
    history: '↺'
  };

  function renderPopup() {
    const popup = el.popup();
    if (!popup) return;
    if (!state.suggestions.length) {
      hidePopup();
      return;
    }

    const rows = state.suggestions.map((s, i) => {
      const cls = 'aq-suggestion' + (i === state.highlighted ? ' highlighted' : '');
      const icon = KIND_ICON[s.kind] || '·';
      return '<div class="' + cls + '" data-index="' + i + '" role="option"' +
        ' aria-selected="' + (i === state.highlighted) + '">' +
        '<span class="aq-kind aq-kind-' + escapeHtml(s.kind || '') + '">' + icon + '</span>' +
        '<span class="aq-label">' + escapeHtml(s.label || s.text) + '</span>' +
        (s.detail ? '<span class="aq-detail">' + escapeHtml(s.detail) + '</span>' : '') +
        '</div>';
    }).join('');

    const doc = state.highlighted >= 0 ? (state.suggestions[state.highlighted].doc || '') : '';
    popup.innerHTML =
      '<div class="aq-suggestions" role="listbox">' + rows + '</div>' +
      '<div class="aq-popup-footer">' +
        '<span class="aq-context">' + escapeHtml(state.context) + '</span>' +
        (doc ? '<span class="aq-doc">' + escapeHtml(doc) + '</span>' : '') +
        '<span class="aq-keys">Tab accept · ↑↓ move · Enter run · Esc close</span>' +
      '</div>';
    popup.hidden = false;

    const hi = popup.querySelector('.aq-suggestion.highlighted');
    if (hi && hi.scrollIntoView) hi.scrollIntoView({ block: 'nearest' });
  }

  function hidePopup() {
    const popup = el.popup();
    if (popup) {
      popup.hidden = true;
      popup.innerHTML = '';
    }
    state.suggestions = [];
    state.highlighted = -1;
  }

  // acceptSuggestion splices the server's replacement into the input. The
  // server decided the range and the exact text (quotes and trailing space
  // included), so there is no client-side quoting rule to get wrong.
  function acceptSuggestion(index) {
    const input = el.input();
    const s = state.suggestions[index];
    if (!input || !s) return;

    // An example (or a history entry) is a whole query, not a fragment.
    if (s.kind === 'example' || s.kind === 'history') {
      input.value = s.text;
      input.setSelectionRange(input.value.length, input.value.length);
      hidePopup();
      window.app.runAdvancedQuery();
      return;
    }

    const before = input.value.slice(0, state.replaceStart);
    const after = input.value.slice(state.replaceEnd);
    input.value = before + s.text + after;
    const caret = before.length + s.text.length;
    input.setSelectionRange(caret, caret);
    showError(null);
    // Completing a field immediately asks what operator belongs next, which
    // is what makes the bar feel like it is leading rather than waiting.
    requestCompletion(true);
  }

  // ============================================
  // Keyboard
  // ============================================

  function onKeyDown(e) {
    const popupOpen = state.suggestions.length > 0 && !el.popup().hidden;

    switch (e.key) {
      case 'ArrowDown':
        if (popupOpen) {
          e.preventDefault();
          state.highlighted = (state.highlighted + 1) % state.suggestions.length;
          renderPopup();
        } else {
          e.preventDefault();
          requestCompletion(true);
        }
        return;

      case 'ArrowUp':
        if (popupOpen) {
          e.preventDefault();
          state.highlighted =
            (state.highlighted - 1 + state.suggestions.length) % state.suggestions.length;
          renderPopup();
        }
        return;

      case 'Tab':
        // Tab always means "accept", which is why it is bound even when
        // nothing is highlighted: the first suggestion is the useful default.
        if (popupOpen) {
          e.preventDefault();
          acceptSuggestion(state.highlighted >= 0 ? state.highlighted : 0);
        }
        return;

      case 'Enter':
        e.preventDefault();
        // Enter runs the query. It accepts a suggestion first only when the
        // user has moved off the default row — otherwise typing a complete
        // query and pressing Enter would insert a completion nobody asked
        // for instead of running what is on screen.
        if (popupOpen && state.highlighted > 0) {
          acceptSuggestion(state.highlighted);
          return;
        }
        hidePopup();
        window.app.runAdvancedQuery();
        return;

      case 'Escape':
        // First Escape dismisses the popup; a second closes the bar. Closing
        // the bar also drops the query, so the list is never left showing
        // results whose query is no longer visible.
        e.preventDefault();
        if (popupOpen) {
          hidePopup();
        } else {
          window.app.closeAdvancedSearch();
        }
        return;

      case ' ':
        if (e.ctrlKey) { // the conventional "complete now"
          e.preventDefault();
          requestCompletion(true);
        }
        return;
    }
  }

  function onInput() {
    showError(null);
    requestCompletion(false);
  }

  // ============================================
  // History
  // ============================================

  function loadHistory() {
    try {
      const raw = localStorage.getItem(HISTORY_KEY);
      const list = raw ? JSON.parse(raw) : [];
      return Array.isArray(list) ? list : [];
    } catch (_) {
      return [];
    }
  }

  function rememberQuery(text) {
    try {
      const list = loadHistory().filter(q => q !== text);
      list.unshift(text);
      localStorage.setItem(HISTORY_KEY, JSON.stringify(list.slice(0, HISTORY_MAX)));
    } catch (_) { /* private browsing: history is a convenience, not state */ }
  }

  // ============================================
  // Help panel — rendered from the server's schema
  // ============================================

  async function loadSchema() {
    if (state.schema) return;
    const res = await authedFetch('/notes/query/schema');
    if (!res.ok) return;
    state.schema = res.body.data || null;
    if (state.helpOpen) renderHelp();
  }

  window.app.toggleAdvancedHelp = function() {
    state.helpOpen = !state.helpOpen;
    const help = el.help();
    if (help) help.hidden = !state.helpOpen;
    if (state.helpOpen) renderHelp();
  };

  function renderHelp() {
    const help = el.help();
    if (!help) return;
    if (!state.schema) {
      help.innerHTML = '<div class="aq-help-loading">Loading the field list…</div>';
      loadSchema();
      return;
    }
    const s = state.schema;

    const fieldRows = (s.fields || []).map(f => {
      const type = f.kind + (f.multi ? ' · multi' : '') + (f.nullable ? ' · nullable' : '');
      const aliases = (f.aliases || []).length ? ' (' + f.aliases.join(', ') + ')' : '';
      return '<tr>' +
        '<td class="aq-help-field"><code>' + escapeHtml(f.name) + '</code>' +
          '<span class="aq-help-alias">' + escapeHtml(aliases) + '</span></td>' +
        '<td class="aq-help-type">' + escapeHtml(type) + '</td>' +
        '<td class="aq-help-desc">' + escapeHtml(f.description || '') +
          (f.example ? ' <code class="aq-help-ex">' + escapeHtml(f.example) + '</code>' : '') +
        '</td></tr>';
    }).join('');

    const opRows = (s.operators || []).map(o =>
      '<li><code>' + escapeHtml(o.token) + '</code> ' + escapeHtml(o.description) + '</li>'
    ).join('');

    const kwRows = (s.keywords || []).map(k =>
      '<li><code>' + escapeHtml(k.word) + '</code> ' + escapeHtml(k.description) + '</li>'
    ).join('');

    // Examples are clickable: the fastest way to learn the language is to run
    // one and edit it.
    const exRows = (s.examples || []).map(e =>
      '<li><button type="button" class="aq-help-example" data-query="' +
      escapeHtml(e) + '"><code>' + escapeHtml(e) + '</code></button></li>'
    ).join('');

    const semRows = (s.semantics || []).map(n => '<li>' + escapeHtml(n) + '</li>').join('');

    help.innerHTML =
      '<div class="aq-help-cols">' +
        '<div class="aq-help-col aq-help-fields">' +
          '<h4>Queryable attributes</h4>' +
          '<table class="aq-help-table"><tbody>' + fieldRows + '</tbody></table>' +
        '</div>' +
        '<div class="aq-help-col">' +
          '<h4>Operators</h4><ul class="aq-help-list">' + opRows + '</ul>' +
          '<h4>Keywords &amp; literals</h4><ul class="aq-help-list">' + kwRows + '</ul>' +
        '</div>' +
        '<div class="aq-help-col">' +
          '<h4>Examples</h4><ul class="aq-help-list aq-help-examples">' + exRows + '</ul>' +
          '<h4>Worth knowing</h4><ul class="aq-help-list aq-help-semantics">' + semRows + '</ul>' +
        '</div>' +
      '</div>';
  }

  // ============================================
  // Wiring
  // ============================================

  window.app._initAdvancedSearch = function() {
    const input = el.input();
    if (!input) return;

    input.addEventListener('keydown', onKeyDown);
    input.addEventListener('input', onInput);
    // A click moves the cursor, which changes what belongs at it.
    input.addEventListener('click', () => requestCompletion(true));
    input.addEventListener('blur', () => {
      // Delayed so a click ON a suggestion is not cancelled by the blur it
      // causes.
      setTimeout(() => { if (document.activeElement !== input) hidePopup(); }, 150);
    });

    const popup = el.popup();
    if (popup) {
      popup.addEventListener('mousedown', e => {
        const row = e.target.closest('.aq-suggestion');
        if (!row) return;
        e.preventDefault(); // keep focus in the input
        acceptSuggestion(parseInt(row.dataset.index, 10));
      });
      popup.addEventListener('mousemove', e => {
        const row = e.target.closest('.aq-suggestion');
        if (!row) return;
        const i = parseInt(row.dataset.index, 10);
        if (i === state.highlighted) return;
        state.highlighted = i;
        renderPopup();
      });
    }

    const help = el.help();
    if (help) {
      help.addEventListener('click', e => {
        const btn = e.target.closest('.aq-help-example');
        if (!btn) return;
        input.value = btn.dataset.query;
        input.focus();
        window.app.runAdvancedQuery();
      });
    }

    // The bar remembers whether it was open, because someone who works in the
    // query language works in it for a while.
    try {
      if (localStorage.getItem(OPEN_KEY) === '1') setOpen(true);
    } catch (_) { /* private browsing */ }

    // A global accelerator, matching the "/" that focuses the simple search:
    // Ctrl/⌘+Shift+F opens the query bar from anywhere that is not a text
    // field.
    document.addEventListener('keydown', e => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && (e.key === 'F' || e.key === 'f')) {
        e.preventDefault();
        if (!state.open) setOpen(true);
        else el.input().focus();
      }
    });
  };

  // Initialize once app.js has published its internals.
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => window.app._initAdvancedSearch());
  } else {
    window.app._initAdvancedSearch();
  }
})();
