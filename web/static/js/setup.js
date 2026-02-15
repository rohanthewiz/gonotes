// setup.js — Handles spoke config import on the /setup page.
// Reads an exported JSON config file, previews it, then POSTs to
// /api/v1/setup/apply which writes the .env file on the server.
(function() {
  'use strict';

  let parsedConfig = null;

  // Called when the user selects a JSON file via the file input.
  // Parses the JSON and displays a human-readable preview.
  window.handleFileSelect = function(input) {
    const file = input.files[0];
    if (!file) return;

    // Reset any previous state
    hideMessage('setup-error');
    hideMessage('setup-success');

    const reader = new FileReader();
    reader.onload = function(e) {
      try {
        parsedConfig = JSON.parse(e.target.result);
        displayPreview(parsedConfig);
      } catch (err) {
        showError('Invalid JSON file. Please select a valid spoke config file.');
        parsedConfig = null;
      }
    };
    reader.readAsText(file);
  };

  // Renders a read-only preview of the parsed config fields.
  // The password is masked — users don't need to see the base64 blob.
  function displayPreview(config) {
    const preview = document.getElementById('config-preview');
    if (!preview) return;

    // Validate that the file looks like a spoke config
    if (!config.hub_url || !config.username || !config.password_b64) {
      showError('This file does not appear to be a valid spoke config. Missing required fields.');
      return;
    }

    preview.innerHTML =
      '<h3>Configuration Preview</h3>' +
      '<div class="config-field"><strong>Hub URL:</strong> ' + escapeHtml(config.hub_url) + '</div>' +
      '<div class="config-field"><strong>Username:</strong> ' + escapeHtml(config.username) + '</div>' +
      '<div class="config-field"><strong>Password:</strong> ••••••••</div>' +
      '<div class="config-field"><strong>Sync Interval:</strong> ' + escapeHtml(config.sync_interval || '5m') + '</div>' +
      '<div class="config-field"><strong>Invite Token:</strong> ' + (config.invite_token ? '✓ included' : '✗ missing') + '</div>' +
      '<div class="config-field"><strong>JWT Secret:</strong> ' + (config.jwt_secret ? '✓ included' : '✗ missing') + '</div>';

    preview.classList.remove('hidden');
    document.getElementById('btn-apply').classList.remove('hidden');
  }

  // POSTs the parsed config to the server to write the .env file.
  // No authentication is required — this endpoint is only available
  // when sync is not yet configured (first-run guard on the server).
  window.applyConfig = async function() {
    if (!parsedConfig) return;

    const btn = document.getElementById('btn-apply');
    if (btn) {
      btn.disabled = true;
      btn.textContent = 'Applying...';
    }

    try {
      const response = await fetch('/api/v1/setup/apply', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(parsedConfig)
      });

      const data = await response.json();
      if (!response.ok) {
        showError(data.error || 'Failed to apply configuration');
        if (btn) {
          btn.disabled = false;
          btn.textContent = 'Apply Configuration';
        }
        return;
      }

      // Success — show message and disable further interaction
      showSuccess('Configuration saved! Please restart GoNotes to activate sync.');
      if (btn) btn.style.display = 'none';
    } catch (err) {
      showError('Network error. Is the server running?');
      if (btn) {
        btn.disabled = false;
        btn.textContent = 'Apply Configuration';
      }
    }
  };

  // --- UI helpers ---

  function showError(message) {
    const el = document.getElementById('setup-error');
    if (el) {
      el.textContent = message;
      el.classList.remove('hidden');
    }
  }

  function showSuccess(message) {
    const el = document.getElementById('setup-success');
    if (el) {
      el.textContent = message;
      el.classList.remove('hidden');
    }
  }

  function hideMessage(id) {
    const el = document.getElementById(id);
    if (el) el.classList.add('hidden');
  }

  function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }
})();
