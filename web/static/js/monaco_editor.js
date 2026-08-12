// GoNotes Monaco Editor Integration
// Provides an optional, full-featured Monaco editor for the note body.
//
// Design overview:
//   - Opt-in: the plain <textarea id="edit-body"> remains the default editor and
//     the single source of truth for form submission. Monaco is only loaded
//     lazily when the user enables it (vendored copy first, CDN fallback), so
//     users who never turn it on pay zero cost — no extra requests, no memory.
//   - "Full programmatic mode": we load the complete Monaco build via its AMD
//     loader (loader.js + vs/editor/editor.main), not a slimmed-down custom
//     bundle. That exposes the entire API surface at window.monaco
//     (monaco.editor, monaco.languages, monaco.Range, commands, actions,
//     keybindings, etc.) and the live editor instance via app.getMonacoEditor(),
//     so behavior can be extended from the console or other scripts at runtime.
//   - Sync strategy: while Monaco is active it is the editing surface, but every
//     content change is mirrored into the hidden textarea. This keeps the
//     existing save path (FormData reads name="body") working unchanged, and
//     means a mid-edit toggle back to the textarea loses nothing.
//
//   Data flow while Monaco is active:
//
//     populateEditForm/clearEditForm --> textarea.value --> _syncBodyToMonaco()
//                                                              |
//                                                              v
//         saveNote (FormData) <-- textarea.value <-- onDidChangeModelContent
//
// Persistence: preference stored in localStorage under 'gonotes-editor-mode'
// ('monaco' or 'plain', defaulting to 'plain').

(function() {
  'use strict';

  // Pinned Monaco version. Keep in sync with scripts/vendor_monaco.sh, which
  // fetches this exact version into web/static/vendor/monaco. Pinning avoids
  // surprise breakage from a floating "latest" tag; bump deliberately.
  const MONACO_VERSION = '0.52.2';

  // Load sources, tried in order. The vendored copy is embedded in the gonotes
  // binary (go:embed of web/static) so the editor works with no connectivity;
  // the CDN is a fallback for builds where the vendor directory was trimmed.
  // Each base points at the directory containing vs/loader.js.
  const MONACO_SOURCES = [
    '/static/vendor/monaco',
    'https://cdn.jsdelivr.net/npm/monaco-editor@' + MONACO_VERSION + '/min'
  ];

  const STORAGE_KEY = 'gonotes-editor-mode';

  // Module state
  let editor = null;          // IStandaloneCodeEditor instance (null until first activation)
  let loaderPromise = null;   // Promise for the one-time AMD loader + editor.main load
  let syncingFromTextarea = false; // Guard against sync feedback loops

  function isMonacoPreferred() {
    return localStorage.getItem(STORAGE_KEY) === 'monaco';
  }

  function isActive() {
    const wrapper = document.querySelector('.edit-body-wrapper');
    return !!(editor && wrapper && wrapper.classList.contains('monaco-active'));
  }

  // Map the app theme to a Monaco theme. The app has two themes:
  // 'dark-green' (default) and 'light' — same mapping highlight.js uses.
  function monacoThemeForApp() {
    const t = localStorage.getItem('gonotes-theme') || 'dark-green';
    return t === 'dark-green' ? 'vs-dark' : 'vs';
  }

  // ============================================
  // Lazy Loading
  // ============================================

  // Resolve a source base to an absolute URL. Workers loaded via the data:
  // URI shim below run without a document base, so relative paths (the
  // vendored copy) must be expanded against the current origin.
  function absoluteBase(base) {
    return base.startsWith('/') ? window.location.origin + base : base;
  }

  // Attempt to load Monaco from a single source base. Resolves once
  // window.monaco is ready; rejects if the loader script or the editor
  // module fails to load from that source.
  function loadMonacoFrom(base) {
    return new Promise(function(resolve, reject) {
      const absBase = absoluteBase(base);

      // Monaco spawns web workers for language services. Workers cannot be
      // created directly from a cross-origin URL, and even same-origin worker
      // scripts need an absolute base to resolve their internal imports — so
      // we hand Monaco a data: URI shim whose only job is to set the base and
      // importScripts the real worker (the standard pattern for AMD Monaco).
      window.MonacoEnvironment = {
        getWorkerUrl: function() {
          const proxy =
            "self.MonacoEnvironment={baseUrl:'" + absBase + "/'};" +
            "importScripts('" + absBase + "/vs/base/worker/workerMain.js');";
          return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(proxy);
        }
      };

      const script = document.createElement('script');
      script.src = base + '/vs/loader.js';
      script.onload = function() {
        // 'require' here is Monaco's AMD loader, not Node's
        window.require.config({ paths: { vs: base + '/vs' } });
        window.require(['vs/editor/editor.main'], function() {
          resolve();
        }, function(err) {
          reject(err);
        });
      };
      script.onerror = function() {
        script.remove();
        reject(new Error('Failed to load Monaco loader from ' + base));
      };
      document.head.appendChild(script);
    });
  }

  // Try each source in MONACO_SOURCES until one succeeds: the vendored,
  // embedded copy first (works offline), then the pinned CDN build.
  // Idempotent: concurrent/repeat calls share the same promise.
  function loadMonaco() {
    if (loaderPromise) return loaderPromise;

    loaderPromise = MONACO_SOURCES.reduce(function(attempt, base) {
      return attempt.catch(function(prevErr) {
        if (prevErr) console.warn('Monaco source failed, trying next:', prevErr.message || prevErr);
        return loadMonacoFrom(base);
      });
      // Seed rejection so the first .catch fires the first real attempt;
      // null distinguishes the seed from an actual load failure
    }, Promise.reject(null));

    // Allow a retry after a transient failure of all sources
    loaderPromise.catch(function() { loaderPromise = null; });

    return loaderPromise;
  }

  // ============================================
  // Editor Lifecycle
  // ============================================

  // Create the editor instance inside the container (first activation only).
  // Subsequent activations reuse the same instance/model so undo history
  // survives toggling within an editing session.
  function createEditor() {
    const container = document.getElementById('monaco-body-container');
    const textarea = document.getElementById('edit-body');
    if (!container || !textarea || editor) return;

    editor = monaco.editor.create(container, {
      value: textarea.value,
      language: 'markdown',
      theme: monacoThemeForApp(),
      // Notes are prose-first markdown, so wrap instead of horizontal scroll
      wordWrap: 'on',
      // Minimap adds little for prose and eats horizontal space in the panel
      minimap: { enabled: false },
      lineNumbers: 'on',
      folding: true,
      // The edit panel is resized by the splitter and focus mode;
      // automaticLayout re-measures the container so the editor follows
      automaticLayout: true,
      fontSize: 13,
      scrollBeyondLastLine: false,
      padding: { top: 12, bottom: 12 },
      // Markdown-friendly: don't auto-surround selections aggressively
      quickSuggestions: false
    });

    // Mirror every edit into the hidden textarea so FormData-based save and
    // any 'input' listeners keep working without knowing about Monaco
    editor.onDidChangeModelContent(function() {
      if (syncingFromTextarea) return;
      textarea.value = editor.getValue();
    });

    setupImageHandlers(container, textarea);
  }

  // Intercept image paste and drag-drop on the Monaco container, routing files
  // through the existing image_embed.js resize dialog. Capture phase is used
  // so we see the paste before Monaco's internal handler consumes it.
  // Text paste/drop is left to Monaco's own handling.
  function setupImageHandlers(container, textarea) {
    container.addEventListener('paste', function(e) {
      const items = e.clipboardData && e.clipboardData.items;
      if (!items || typeof window.app._insertImageFile !== 'function') return;
      for (let i = 0; i < items.length; i++) {
        if (items[i].type.startsWith('image/')) {
          e.preventDefault();
          e.stopPropagation();
          const file = items[i].getAsFile();
          // The dialog's insert path routes back through Monaco when active
          if (file) window.app._insertImageFile(file, textarea);
          return; // Only handle the first image, matching textarea behavior
        }
      }
    }, true);

    container.addEventListener('dragover', function(e) {
      if (e.dataTransfer && e.dataTransfer.types.includes('Files')) {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
      }
    }, true);

    container.addEventListener('drop', function(e) {
      const files = e.dataTransfer && e.dataTransfer.files;
      if (!files || files.length === 0 || typeof window.app._insertImageFile !== 'function') return;
      for (let i = 0; i < files.length; i++) {
        if (files[i].type.startsWith('image/')) {
          e.preventDefault();
          e.stopPropagation();
          window.app._insertImageFile(files[i], textarea);
        }
      }
    }, true);
  }

  // Show Monaco / hide the textarea (or the reverse). Content is synced in the
  // direction of the surface being revealed so nothing is lost mid-edit.
  function activate() {
    const wrapper = document.querySelector('.edit-body-wrapper');
    const textarea = document.getElementById('edit-body');
    if (!wrapper || !textarea) return;

    loadMonaco().then(function() {
      createEditor();
      if (!editor) return;
      // Pull whatever is currently in the textarea (edit form may have been
      // populated while Monaco was still loading)
      setEditorValue(textarea.value);
      wrapper.classList.add('monaco-active');
      editor.layout();
    }).catch(function(err) {
      console.error('Monaco failed to load, falling back to plain editor:', err);
      localStorage.setItem(STORAGE_KEY, 'plain');
      syncToggleUI();
      if (window.app._internal && window.app._internal.showToast) {
        window.app._internal.showToast('Monaco editor failed to load — using plain editor', 'error');
      }
    });
  }

  function deactivate() {
    const wrapper = document.querySelector('.edit-body-wrapper');
    const textarea = document.getElementById('edit-body');
    if (!wrapper) return;
    // Push the latest Monaco content into the textarea before hiding
    if (editor && textarea) {
      textarea.value = editor.getValue();
    }
    wrapper.classList.remove('monaco-active');
  }

  // Replace editor content without polluting the undo stack across notes.
  // setValue (vs. executeEdits) intentionally resets undo history — undo
  // should not walk back into a previously edited note's content.
  function setEditorValue(value) {
    if (!editor) return;
    syncingFromTextarea = true;
    editor.setValue(value || '');
    syncingFromTextarea = false;
  }

  // Keep the footer checkbox in agreement with the stored preference
  function syncToggleUI() {
    const checkbox = document.getElementById('edit-monaco-toggle');
    if (checkbox) checkbox.checked = isMonacoPreferred();
  }

  // ============================================
  // Public API (window.app)
  // ============================================

  window.app = window.app || {};

  // Checkbox handler: persist the preference and switch surfaces immediately
  window.app.toggleMonacoEditor = function(enabled) {
    localStorage.setItem(STORAGE_KEY, enabled ? 'monaco' : 'plain');
    if (enabled) {
      activate();
    } else {
      deactivate();
    }
  };

  // Full programmatic access to the editor instance (IStandaloneCodeEditor).
  // Combined with window.monaco this exposes the complete Monaco API, e.g.:
  //   app.getMonacoEditor().updateOptions({ minimap: { enabled: true } })
  //   app.getMonacoEditor().addAction({...})
  //   monaco.editor.setTheme('hc-black')
  // Returns null if Monaco has not been activated yet.
  window.app.getMonacoEditor = function() {
    return editor;
  };

  // True when Monaco is the currently visible editing surface.
  // Used by note_links.js and image_embed.js to route text insertion.
  window.app._monacoActive = isActive;

  // Insert text at the current cursor position (replacing any selection).
  // padNewlines mirrors the textarea image-insert behavior: ensure the
  // insertion sits on its own line for clean markdown.
  window.app._monacoInsertText = function(text, opts) {
    if (!editor) return;
    const padNewlines = opts && opts.padNewlines;
    let insertText = text;

    if (padNewlines) {
      const model = editor.getModel();
      const sel = editor.getSelection();
      const offset = model.getOffsetAt(sel.getStartPosition());
      const full = model.getValue();
      const before = full.substring(0, offset);
      const after = full.substring(model.getOffsetAt(sel.getEndPosition()));
      const prefix = before.length > 0 && !before.endsWith('\n') ? '\n' : '';
      const suffix = after.length > 0 && !after.startsWith('\n') ? '\n' : '';
      insertText = prefix + text + suffix;
    }

    // executeEdits (not setValue) preserves undo history and cursor semantics
    editor.executeEdits('gonotes-insert', [{
      range: editor.getSelection(),
      text: insertText,
      forceMoveMarkers: true
    }]);
    editor.focus();
  };

  // Called after populateEditForm/clearEditForm set the textarea directly
  // (the value setter fires no events, so an explicit sync is required)
  window.app._syncBodyToMonaco = function() {
    const textarea = document.getElementById('edit-body');
    if (editor && textarea) {
      setEditorValue(textarea.value);
    }
  };

  // Called at the top of saveNote as a safety net. Normally redundant because
  // onDidChangeModelContent already mirrors every edit, but this guarantees
  // the textarea holds the final content (e.g. in-flight IME composition).
  window.app._syncMonacoToBody = function() {
    const textarea = document.getElementById('edit-body');
    if (isActive() && textarea) {
      textarea.value = editor.getValue();
    }
  };

  // Called by showEditMode: if the user prefers Monaco, make sure it is
  // loaded and visible now that the edit panel is on screen
  window.app._monacoOnEditShown = function() {
    syncToggleUI();
    if (isMonacoPreferred()) {
      activate();
    } else {
      deactivate();
    }
  };

  // Keep the Monaco theme in step with the app theme toggle. We wrap (not
  // replace) app.toggleTheme so this file stays independent of app.js
  // internals; wrapping at load time is safe because app.js loads first.
  const originalToggleTheme = window.app.toggleTheme;
  if (typeof originalToggleTheme === 'function') {
    window.app.toggleTheme = function() {
      originalToggleTheme.apply(this, arguments);
      if (window.monaco) {
        monaco.editor.setTheme(monacoThemeForApp());
      }
    };
  }
})();
