// summarize.js — the web UI's two doors into the summarizer.
//
//   toolbar     Summarize clipboard → a NEW note, prefilled and unsaved
//   edit footer Summarize           → replaces the body being edited, in place
//
// Both post to /api/v1/summarize, which runs the `claude` CLI on the machine
// the SERVER runs on (see web/api/summarize.go and package summarize). Two
// consequences shape this file:
//
//   - WHETHER THE FEATURE EXISTS IS A SERVER PROPERTY. A host without the CLI
//     cannot summarize no matter what the browser can do, so both buttons ship
//     hidden in the HTML and are revealed only after the GET says "available".
//     Revealing rather than hiding avoids a button that flashes on every load.
//   - THE CLIPBOARD IS A BROWSER PROPERTY. navigator.clipboard.readText needs a
//     secure context (localhost counts; a plain-http LAN address does not) and
//     a permission the user may refuse. That failure is not the summarizer's,
//     and is reported as its own thing with a usable fallback.
//
// Nothing here saves. A summary lands in the form and waits for Save — the same
// rule the TUI keeps (tui/summarize.go), and for the same reason: what a model
// wrote about a text is a proposal, and the person who pasted the text is the
// one who decides whether it is worth keeping.
(function() {
  'use strict';

  window.app = window.app || {};

  // Both buttons, resolved lazily: this file loads with the page, but the probe
  // that reveals them runs later.
  function clipBtn() { return document.getElementById('btn-summarize-clip'); }
  function bodyBtn() { return document.getElementById('btn-summarize-body'); }

  function internals() { return (window.app && window.app._internal) || null; }

  function toast(message, kind) {
    const api = internals();
    if (api && api.showToast) api.showToast(message, kind || 'info');
    else console.log('[summarize]', message);
  }

  // ============================================
  // Availability probe
  // ============================================

  // Asked once per page load. A failure (offline, 401 on the way to the login
  // redirect) leaves the buttons hidden, which is the safe direction: a hidden
  // button costs a user who has the CLI one reload, whereas a visible one on a
  // server without it costs every click.
  async function probeAvailability() {
    const api = internals();
    if (!api) return;
    try {
      const res = await api.apiRequest('/summarize');
      if (res && res.data && res.data.available) {
        if (clipBtn()) clipBtn().style.display = '';
        if (bodyBtn()) bodyBtn().style.display = '';
      }
    } catch (err) {
      console.warn('summarize: availability probe failed', err);
    }
  }

  // ============================================
  // The call
  // ============================================

  // requestSummary posts text and returns {title, description, body}, or null
  // after reporting why not. The server's own message is what gets shown: it
  // names what the model said back, or that the CLI is missing, and a generic
  // "summarize failed" would leave the user with nothing to act on.
  async function requestSummary(text) {
    const api = internals();
    if (!api) return null;
    try {
      const res = await api.apiRequest('/summarize', {
        method: 'POST',
        body: JSON.stringify({ text: text })
      });
      if (!res || !res.data || !res.data.body) {
        toast('The summarizer returned nothing usable', 'error');
        return null;
      }
      return res.data;
    } catch (err) {
      // No toast here: apiRequest already showed the server's message on its
      // way out, and a second one saying the same thing reads as two failures.
      console.warn('summarize: request failed', err);
      return null;
    }
  }

  // busy disables a button and says what it is doing, so a second click cannot
  // start a second model call over the same text. The label is restored from
  // what was there, not from a literal, so this cannot rename a button.
  function busy(btn, on, label) {
    if (!btn) return;
    if (on) {
      btn.dataset.prevLabel = btn.dataset.prevLabel || btn.textContent;
      btn.disabled = true;
      if (label && btn.textContent.trim()) btn.textContent = label;
    } else {
      btn.disabled = false;
      if (btn.dataset.prevLabel) {
        btn.textContent = btn.dataset.prevLabel;
        delete btn.dataset.prevLabel;
      }
    }
  }

  // ============================================
  // Body helpers
  // ============================================
  //
  // The body lives in the textarea; Monaco, when active, is a second surface
  // over the same value. Reading goes through _syncMonacoToBody and writing
  // through _syncBodyToMonaco, because assigning textarea.value fires no event
  // the editor could notice.

  function readBody() {
    if (window.app._syncMonacoToBody) window.app._syncMonacoToBody();
    const ta = document.getElementById('edit-body');
    return ta ? ta.value : '';
  }

  function writeBody(text) {
    const ta = document.getElementById('edit-body');
    if (ta) ta.value = text;
    if (window.app._syncBodyToMonaco) window.app._syncBodyToMonaco();
  }

  // ============================================
  // Door 1 — the clipboard
  // ============================================

  window.app.summarizeClipboard = async function() {
    const btn = clipBtn();

    let text = '';
    try {
      text = await navigator.clipboard.readText();
    } catch (err) {
      // A refused or unavailable clipboard is not a failure of this feature,
      // and the useful answer is the way around it: open an empty note, let the
      // user paste with ⌘V, and summarize that with the other button.
      toast('The browser would not give up the clipboard — paste into a new note and use Summarize', 'error');
      if (window.app.newNote) window.app.newNote();
      return;
    }

    if (!text || !text.trim()) {
      toast('Nothing to summarize — the clipboard is empty', 'info');
      return;
    }

    // The toolbar button is icon-only, so busy() has nothing to relabel — all
    // it can show is the disabled state (.btn:disabled in app.css). The toast
    // carries the rest, and says the same thing the TUI's status line says on
    // ctrl+r. It auto-dismisses after 3s and a model call can outlast that;
    // past then the dimmed button is what tells the user the wait is real.
    toast('Summarizing the clipboard…', 'info');
    busy(btn, true);
    const res = await requestSummary(text);
    busy(btn, false);
    if (!res) return;

    // A NEW note, deliberately: the clipboard door never touches an existing
    // one. newNote() clears the form and mints the guid, so everything below is
    // filling in blanks.
    window.app.newNote();
    const title = document.getElementById('edit-title');
    const desc = document.getElementById('edit-description');
    if (title) title.value = res.title || '';
    if (desc) desc.value = res.description || '';
    writeBody(res.body);
    if (title) title.focus();

    toast('Summarized from the clipboard — Save to keep it', 'success');
  };

  // ============================================
  // Door 2 — the body on screen
  // ============================================

  window.app.summarizeBody = async function() {
    const btn = bodyBtn();
    const text = readBody();
    if (!text || !text.trim()) {
      toast('Nothing to summarize — the body is empty', 'info');
      return;
    }

    // Announced as well as relabelled: the footer button can be scrolled out of
    // view or simply not where the eye is after a click, and this door is the
    // one that REPLACES text the user wrote. Being told it started is what
    // makes the replacement, when it lands, an expected event.
    toast('Summarizing the note body…', 'info');
    busy(btn, true, 'Summarizing…');
    const res = await requestSummary(text);
    busy(btn, false);
    if (!res) return;

    // The body is REPLACED — that is the request — but the note on disk is
    // untouched until Save, and Cancel still walks away from all of it. The
    // toast says so, because a replaced body is the one change here the user
    // did not watch happen.
    writeBody(res.body);

    // Title and description are only FILLED IN, never overwritten: a title the
    // user typed is their own words, and this request was only about condensing
    // the text. Same rule as gn-clip.sh's -t and the TUI's applySummary.
    const filled = [];
    const title = document.getElementById('edit-title');
    const desc = document.getElementById('edit-description');
    if (title && !title.value.trim() && res.title) { title.value = res.title; filled.push('title'); }
    if (desc && !desc.value.trim() && res.description) { desc.value = res.description; filled.push('description'); }

    const suffix = filled.length ? ' (' + filled.join(' and ') + ' filled in)' : '';
    toast('Body replaced with the summary' + suffix + ' — Save to keep it', 'success');
  };

  // ============================================
  // Wiring
  // ============================================

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', probeAvailability);
  } else {
    probeAvailability();
  }
})();
