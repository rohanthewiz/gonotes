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
//   - Saved queries and history are the server's too (/query/saved,
//     /query/history; models/saved_query.go). They arrive as the first rows of
//     an empty box's completion — kinds "saved" and "history" — so this file
//     only records runs, saves names and forgets rows. History used to live in
//     localStorage; a browser that still has that list hands it to the server
//     once (migrateLocalHistory) and drops its copy.
(function() {
  'use strict';
  if (!window.app) window.app = {};

  const internal = () => window.app._internal;
  const API = '/api/v1';
  // The pre-server history list. Read once, uploaded, then removed.
  const LEGACY_HISTORY_KEY = 'gonotes-query-history';
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
    helpOpen: false,
    saving: false,         // the name field is showing
    historyMigrated: false // the legacy localStorage list has been handled
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
    save:     () => document.getElementById('advanced-query-save'),
    saveName: () => document.getElementById('advanced-query-save-name'),
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
  //
  // method and body are optional; a body is sent as JSON.
  async function authedFetch(path, method, body) {
    const token = internal() && internal().getAuthToken
      ? internal().getAuthToken()
      : localStorage.getItem('token');
    const headers = { 'Content-Type': 'application/json' };
    if (token) headers['Authorization'] = 'Bearer ' + token;
    const init = { headers, method: method || 'GET' };
    if (body !== undefined) init.body = JSON.stringify(body);
    let resp;
    try {
      resp = await fetch(API + path, init);
    } catch (err) {
      // A network failure is reported like a server one, so callers have one
      // shape to handle instead of a thrown error on top of a status code.
      return { ok: false, status: 0, body: { error: 'network error' } };
    }
    let parsed = null;
    try {
      parsed = await resp.json();
    } catch (_) {
      parsed = null;
    }
    return { ok: resp.ok, status: resp.status, body: parsed || {} };
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
      migrateLocalHistory();
      const input = el.input();
      if (input) {
        input.focus();
        input.select();
      }
      // An empty box is where the language most needs to introduce itself.
      requestCompletion(true);
    } else {
      hidePopup();
      window.app.cancelSaveAdvancedQuery();
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

    // Recorded only here, on a deliberate run. Fire-and-forget: history is a
    // convenience, and a failure to record must not turn a successful run
    // into an error the user sees.
    authedFetch('/notes/query/history', 'POST', { query: text });
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
    // Saved queries and recent runs are already at the top of an empty box's
    // list — the server puts them there, so the TUI gets the same rows.
    const suggestions = data.suggestions || [];

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
    history: '↺',
    saved: '☆'
  };

  // Kinds that are a whole query rather than a fragment: accepting one
  // replaces the line and runs it. Saved and history rows are also the ones a
  // user can forget from the popup.
  const WHOLE_QUERY = { example: true, history: true, saved: true };
  const FORGETTABLE = { history: true, saved: true };

  function renderPopup() {
    const popup = el.popup();
    if (!popup) return;
    if (!state.suggestions.length) {
      hidePopup();
      return;
    }

    const rows = state.suggestions.map((s, i) => {
      const cls = 'aq-suggestion aq-suggestion-' + escapeHtml(s.kind || '') +
        (i === state.highlighted ? ' highlighted' : '');
      const icon = KIND_ICON[s.kind] || '·';
      const forget = FORGETTABLE[s.kind] && s.id
        ? '<button type="button" class="aq-forget" data-forget="' + i + '"' +
          ' title="Forget this query (Shift+Delete)" aria-label="Forget">×</button>'
        : '';
      return '<div class="' + cls + '" data-index="' + i + '" role="option"' +
        ' aria-selected="' + (i === state.highlighted) + '">' +
        '<span class="aq-kind aq-kind-' + escapeHtml(s.kind || '') + '">' + icon + '</span>' +
        '<span class="aq-label">' + escapeHtml(s.label || s.text) + '</span>' +
        (s.detail ? '<span class="aq-detail">' + escapeHtml(s.detail) + '</span>' : '') +
        forget +
        '</div>';
    }).join('');

    const hiSugg = state.highlighted >= 0 ? state.suggestions[state.highlighted] : null;
    const doc = hiSugg ? (hiSugg.doc || '') : '';
    const keys = 'Tab accept · ↑↓ move · Enter run · Esc close' +
      (hiSugg && FORGETTABLE[hiSugg.kind] ? ' · ⇧Del forget' : '');
    popup.innerHTML =
      '<div class="aq-suggestions" role="listbox">' + rows + '</div>' +
      '<div class="aq-popup-footer">' +
        '<span class="aq-context">' + escapeHtml(state.context) + '</span>' +
        (doc ? '<span class="aq-doc">' + escapeHtml(doc) + '</span>' : '') +
        '<span class="aq-keys">' + keys + '</span>' +
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

    // An example, a saved query or a history entry is a whole query, not a
    // fragment.
    if (WHOLE_QUERY[s.kind]) {
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

  // forgetSuggestion deletes the saved or history row behind a suggestion and
  // asks for the list again, so the popup reflects the server rather than a
  // locally edited copy that could disagree with it.
  async function forgetSuggestion(index) {
    const s = state.suggestions[index];
    if (!s || !FORGETTABLE[s.kind] || !s.id) return;
    const res = await authedFetch('/notes/query/saved/' + encodeURIComponent(s.id), 'DELETE');
    if (!res.ok && res.status !== 404) {
      showError(res.body.error || ('could not forget that query (' + res.status + ')'), null);
      return;
    }
    const input = el.input();
    if (input) input.focus();
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

      case 'Delete':
        // Shift+Delete forgets the highlighted saved/history row — the
        // browser convention for removing an entry from an autocomplete list.
        // A plain Delete keeps editing the text.
        if (e.shiftKey && popupOpen && state.highlighted >= 0 &&
            FORGETTABLE[state.suggestions[state.highlighted].kind]) {
          e.preventDefault();
          forgetSuggestion(state.highlighted);
        }
        return;

      case 's':
      case 'S':
        // Ctrl/⌘+S saves the query in the box instead of the browser's "save
        // page", which is never what someone typing a query meant.
        if (e.metaKey || e.ctrlKey) {
          e.preventDefault();
          window.app.startSaveAdvancedQuery();
        }
        return;
    }
  }

  function onInput() {
    showError(null);
    requestCompletion(false);
  }

  // ============================================
  // History — legacy migration
  // ============================================

  // migrateLocalHistory hands a pre-server history list to the server once,
  // then removes it, so upgrading does not lose the queries someone already
  // relied on.
  //
  // It runs on the bar's first open rather than at page load, because that is
  // when the user is certainly signed in. Entries are posted oldest first:
  // each POST moves its query to the top, so posting in reverse order of
  // recency rebuilds the list in the order it had. The local copy is removed
  // only after every POST succeeded — a 401 or a dropped connection leaves it
  // for the next open.
  async function migrateLocalHistory() {
    if (state.historyMigrated) return;
    state.historyMigrated = true;

    let list;
    try {
      const raw = localStorage.getItem(LEGACY_HISTORY_KEY);
      if (!raw) return;
      list = JSON.parse(raw);
    } catch (_) {
      return; // private browsing, or a value we cannot read: nothing to move
    }
    if (!Array.isArray(list) || list.length === 0) {
      try { localStorage.removeItem(LEGACY_HISTORY_KEY); } catch (_) { /* ignore */ }
      return;
    }

    for (const q of list.slice().reverse()) {
      if (typeof q !== 'string' || !q.trim()) continue;
      const res = await authedFetch('/notes/query/history', 'POST', { query: q });
      if (!res.ok) {
        state.historyMigrated = false; // try again on the next open
        return;
      }
    }
    try { localStorage.removeItem(LEGACY_HISTORY_KEY); } catch (_) { /* ignore */ }
    // If the box is still empty, refresh the popup so the migrated rows show.
    const input = el.input();
    if (state.open && input && !input.value.trim()) requestCompletion(true);
  }

  // ============================================
  // Saving a named query
  // ============================================

  // startSaveAdvancedQuery shows the name field. If the box's text is already
  // a saved query, the name is prefilled — saving over it is how a saved
  // query is renamed in case or edited, and retyping the name to do that
  // would be a chore.
  window.app.startSaveAdvancedQuery = function() {
    const input = el.input();
    const row = el.save();
    const name = el.saveName();
    if (!input || !row || !name) return;
    if (!input.value.trim()) {
      showError('Type a query first, then save it.', null);
      input.focus();
      return;
    }
    showError(null);
    hidePopup();
    state.saving = true;
    row.hidden = false;
    const match = state.suggestions.find(s => s.kind === 'saved' && s.text === input.value.trim());
    name.value = match ? match.label : '';
    name.focus();
    name.select();
  };

  window.app.cancelSaveAdvancedQuery = function() {
    state.saving = false;
    const row = el.save();
    if (row) row.hidden = true;
  };

  // saveAdvancedQuery stores the box's text under the typed name. The server
  // parses it first; a query that will not run is refused with the same
  // positioned error as a run, which is shown against the query input so the
  // mistake is selected and the name the user typed is kept.
  window.app.saveAdvancedQuery = async function() {
    const input = el.input();
    const name = el.saveName();
    if (!input || !name) return;
    const text = input.value.trim();
    const label = name.value.trim();
    if (!label) {
      name.focus();
      return;
    }

    const res = await authedFetch('/notes/query/saved', 'POST', { name: label, query: text });
    if (!res.ok) {
      const qe = res.body.data;
      if (res.status === 400 && qe && typeof qe.position === 'number') {
        showError(res.body.error || 'invalid query', qe);
      } else {
        showError(res.body.error || ('could not save (' + res.status + ')'), null);
        name.focus();
      }
      return;
    }

    window.app.cancelSaveAdvancedQuery();
    setStatus('Saved as \u201c' + label + '\u201d — it leads the list when the box is empty.');
    const toast = internal() && internal().showToast;
    if (toast) toast('Query saved', 'success');
    input.focus();
  };

  function onSaveNameKeyDown(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      window.app.saveAdvancedQuery();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      window.app.cancelSaveAdvancedQuery();
      const input = el.input();
      if (input) input.focus();
    }
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

    const saveName = el.saveName();
    if (saveName) saveName.addEventListener('keydown', onSaveNameKeyDown);

    const popup = el.popup();
    if (popup) {
      popup.addEventListener('mousedown', e => {
        // The forget button sits inside a row, so it is checked first —
        // otherwise the click would also accept (and run) the row it removes.
        const forget = e.target.closest('.aq-forget');
        if (forget) {
          e.preventDefault();
          forgetSuggestion(parseInt(forget.dataset.forget, 10));
          return;
        }
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
