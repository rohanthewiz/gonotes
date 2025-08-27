// Editor-specific JavaScript for Monaco Editor

let monacoEditor = null;
let autoSaveTimer = null;
let isDirty = false;

// Initialize Monaco Editor
function initMonacoEditor() {
  if (typeof monaco === 'undefined' || !document.getElementById('editor-container')) {
    return;
  }
  
  const editorContainer = document.getElementById('editor-container');
  const bodyTextarea = document.getElementById('body');
  
  if (!editorContainer || !bodyTextarea) return;
  
  // Create Monaco Editor instance
  monacoEditor = monaco.editor.create(editorContainer, {
    value: bodyTextarea.value || '',
    language: 'markdown',
    theme: getEditorTheme(),
    minimap: { enabled: false },
    wordWrap: 'on',
    lineNumbers: 'on',
    fontSize: 14,
    automaticLayout: true,
    scrollBeyondLastLine: false,
    renderWhitespace: 'selection',
    rulers: [80, 120],
    quickSuggestions: {
      other: true,
      comments: true,
      strings: true
    }
  });
  
  // Sync changes to hidden textarea
  monacoEditor.onDidChangeModelContent(() => {
    bodyTextarea.value = monacoEditor.getValue();
    setDirty(true);
    scheduleAutoSave();
  });
  
  // Register keyboard shortcuts
  registerEditorShortcuts();
  
  // Setup toolbar actions
  setupToolbarActions();
}

// Get editor theme based on user preference
function getEditorTheme() {
  const isDarkMode = localStorage.getItem('editorTheme') === 'dark';
  return isDarkMode ? 'vs-dark' : 'vs';
}

// Toggle editor theme
window.toggleEditorTheme = function() {
  const currentTheme = monacoEditor.getModel()._options.theme;
  const newTheme = currentTheme === 'vs-dark' ? 'vs' : 'vs-dark';
  monaco.editor.setTheme(newTheme);
  localStorage.setItem('editorTheme', newTheme === 'vs-dark' ? 'dark' : 'light');
};

// Register editor keyboard shortcuts
function registerEditorShortcuts() {
  // Bold - Ctrl/Cmd + B
  monacoEditor.addAction({
    id: 'markdown-bold',
    label: 'Bold',
    keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyB],
    run: () => insertMarkdown('**', '**')
  });
  
  // Italic - Ctrl/Cmd + I
  monacoEditor.addAction({
    id: 'markdown-italic',
    label: 'Italic',
    keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyI],
    run: () => insertMarkdown('*', '*')
  });
  
  // Code - Ctrl/Cmd + E
  monacoEditor.addAction({
    id: 'markdown-code',
    label: 'Code',
    keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyE],
    run: () => insertMarkdown('`', '`')
  });
  
  // Save - Ctrl/Cmd + S
  monacoEditor.addAction({
    id: 'save-note',
    label: 'Save',
    keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS],
    run: () => saveNote()
  });
}

// Insert markdown formatting
window.insertMarkdown = function(before, after) {
  if (!monacoEditor) return;
  
  const selection = monacoEditor.getSelection();
  const text = monacoEditor.getModel().getValueInRange(selection);
  
  monacoEditor.executeEdits('', [{
    range: selection,
    text: before + text + after,
    forceMoveMarkers: true
  }]);
  
  // Move cursor to end of inserted text if no selection
  if (!text) {
    const position = monacoEditor.getPosition();
    monacoEditor.setPosition({
      lineNumber: position.lineNumber,
      column: position.column + before.length
    });
  }
  
  monacoEditor.focus();
};

// Insert code block
window.insertCodeBlock = function(language = '') {
  if (!monacoEditor) return;
  
  const position = monacoEditor.getPosition();
  const lineContent = monacoEditor.getModel().getLineContent(position.lineNumber);
  
  // Insert on new line if current line has content
  const prefix = lineContent.trim() ? '\n' : '';
  const codeBlock = prefix + '```' + language + '\n\n```';
  
  monacoEditor.executeEdits('', [{
    range: new monaco.Range(
      position.lineNumber,
      position.column,
      position.lineNumber,
      position.column
    ),
    text: codeBlock
  }]);
  
  // Move cursor inside code block
  const newPosition = {
    lineNumber: position.lineNumber + (prefix ? 2 : 1),
    column: 1
  };
  monacoEditor.setPosition(newPosition);
  monacoEditor.focus();
};

// Insert list
window.insertList = function(ordered = false) {
  if (!monacoEditor) return;
  
  const position = monacoEditor.getPosition();
  const lineContent = monacoEditor.getModel().getLineContent(position.lineNumber);
  
  // Insert on new line if current line has content
  const prefix = lineContent.trim() ? '\n' : '';
  const listMarker = ordered ? '1. ' : '- ';
  
  monacoEditor.executeEdits('', [{
    range: new monaco.Range(
      position.lineNumber,
      position.column,
      position.lineNumber,
      position.column
    ),
    text: prefix + listMarker
  }]);
  
  monacoEditor.focus();
};

// Insert link
window.insertLink = function() {
  if (!monacoEditor) return;
  
  const selection = monacoEditor.getSelection();
  const text = monacoEditor.getModel().getValueInRange(selection) || 'link text';
  
  monacoEditor.executeEdits('', [{
    range: selection,
    text: '[' + text + '](url)',
    forceMoveMarkers: true
  }]);
  
  monacoEditor.focus();
};

// Setup toolbar actions
function setupToolbarActions() {
  // Toolbar button handlers are set via onclick in HTML
  // This function can be used to add additional setup if needed
}

// Set dirty state
function setDirty(dirty) {
  isDirty = dirty;
  updateStatusBar();
}

// Update status bar
function updateStatusBar() {
  const statusElement = document.querySelector('.editor-status');
  if (!statusElement) return;
  
  const statusText = isDirty ? 'Modified' : 'Saved';
  const statusClass = isDirty ? 'status-modified' : 'status-saved';
  
  const statusItem = statusElement.querySelector('.status-item');
  if (statusItem) {
    statusItem.textContent = statusText;
    statusItem.className = 'status-item ' + statusClass;
  }
}

// Schedule auto-save
function scheduleAutoSave() {
  // Clear existing timer
  if (autoSaveTimer) {
    clearTimeout(autoSaveTimer);
  }
  
  // Schedule new auto-save after 2 seconds of inactivity
  autoSaveTimer = setTimeout(() => {
    if (isDirty && window.location.pathname !== '/notes/new') {
      saveDraft();
    }
  }, 2000);
}

// Save draft
window.saveDraft = async function() {
  const form = document.getElementById('note-form');
  if (!form) return;
  
  const noteGuid = form.querySelector('[name="guid"]')?.value;
  if (!noteGuid) return; // Don't auto-save new notes
  
  const formData = new FormData(form);
  
  try {
    const response = await fetch(`/api/notes/${noteGuid}/save`, {
      method: 'POST',
      body: formData
    });
    
    if (response.ok) {
      setDirty(false);
      showSaveIndicator();
    }
  } catch (error) {
    console.error('Auto-save failed:', error);
  }
};

// Save note (manual save)
window.saveNote = function() {
  const form = document.getElementById('note-form');
  if (form) {
    // Trigger form submission
    const submitButton = form.querySelector('button[type="submit"]');
    if (submitButton) {
      submitButton.click();
    }
  }
};

// Show save indicator
function showSaveIndicator() {
  const indicator = document.createElement('div');
  indicator.className = 'save-indicator';
  indicator.textContent = 'Draft saved';
  document.body.appendChild(indicator);
  
  setTimeout(() => {
    indicator.style.animation = 'slideOut 0.3s ease';
    setTimeout(() => indicator.remove(), 300);
  }, 2000);
}

// Initialize when Monaco is loaded
if (typeof require !== 'undefined') {
  require.config({ paths: { 'vs': '/static/vendor/monaco/min/vs' }});
  require(['vs/editor/editor.main'], function() {
    initMonacoEditor();
  });
} else {
  // Fallback for pages without Monaco
  document.addEventListener('DOMContentLoaded', () => {
    if (window.monaco) {
      initMonacoEditor();
    }
  });
}