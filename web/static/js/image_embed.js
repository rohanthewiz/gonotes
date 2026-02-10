// GoNotes Embedded Image Support
// Extracted from app.js — handles base64 image paste, drop, and resize.
//
// Dependencies: Loaded after app.js. Accesses shared internals via
// window.app._internal (showToast).

(function() {
  'use strict';

  function showToast(message, type) { return window.app._internal.showToast(message, type); }

  // ============================================
  // Embedded Image Support (base64)
  // ============================================

  // Convert a File/Blob to a base64 data URI and insert as markdown image
  // at the cursor position in the body textarea.
  // Shows a resize dialog so the user can scale the image before embedding.
  function insertImageAsBase64(file, textarea) {
    // Validate file type
    if (!file.type.startsWith('image/')) {
      showToast('Only image files can be embedded', 'warning');
      return;
    }

    const reader = new FileReader();
    reader.onload = function(e) {
      const originalDataUri = e.target.result;
      const altText = file.name ? file.name.replace(/\.[^/.]+$/, '') : 'image';

      // Load image to get dimensions
      const img = new Image();
      img.onload = function() {
        showImageResizeDialog(img, originalDataUri, altText, file.type, textarea);
      };
      img.onerror = function() {
        // Fallback: embed without resize if we can't load the image
        doInsertImage(originalDataUri, altText, textarea);
      };
      img.src = originalDataUri;
    };

    reader.onerror = function() {
      showToast('Failed to read image file', 'error');
    };

    reader.readAsDataURL(file);
  }

  // Show a dialog to let the user resize an image before embedding
  function showImageResizeDialog(img, originalDataUri, altText, mimeType, textarea) {
    const origWidth = img.naturalWidth;
    const origHeight = img.naturalHeight;

    // Build overlay
    const overlay = document.createElement('div');
    overlay.className = 'image-resize-overlay';

    const dialog = document.createElement('div');
    dialog.className = 'image-resize-dialog';

    const title = document.createElement('h3');
    title.textContent = 'Resize Image';

    const preview = document.createElement('div');
    preview.className = 'image-resize-preview';
    const previewImg = document.createElement('img');
    previewImg.src = originalDataUri;
    preview.appendChild(previewImg);

    const controls = document.createElement('div');
    controls.className = 'image-resize-controls';

    const label = document.createElement('label');
    label.textContent = 'Scale';

    const row = document.createElement('div');
    row.className = 'image-resize-row';

    const slider = document.createElement('input');
    slider.type = 'range';
    slider.min = '10';
    slider.max = '100';
    slider.value = '100';

    // Default to a smaller size if image is very large
    if (origWidth > 1600 || origHeight > 1200) {
      slider.value = String(Math.round(Math.min(1200 / origWidth, 900 / origHeight) * 100));
    }

    const valueDisplay = document.createElement('span');
    valueDisplay.className = 'resize-value';
    valueDisplay.textContent = slider.value + '% (' + Math.round(origWidth * slider.value / 100) + 'x' + Math.round(origHeight * slider.value / 100) + ')';

    slider.addEventListener('input', function() {
      const pct = parseInt(slider.value, 10);
      const w = Math.round(origWidth * pct / 100);
      const h = Math.round(origHeight * pct / 100);
      valueDisplay.textContent = pct + '% (' + w + 'x' + h + ')';
    });

    row.appendChild(slider);
    row.appendChild(valueDisplay);

    const info = document.createElement('div');
    info.className = 'image-resize-info';
    info.textContent = 'Original: ' + origWidth + 'x' + origHeight;

    controls.appendChild(label);
    controls.appendChild(row);
    controls.appendChild(info);

    const actions = document.createElement('div');
    actions.className = 'image-resize-actions';

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'btn btn-secondary';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.onclick = function() { overlay.remove(); };

    const embedOrigBtn = document.createElement('button');
    embedOrigBtn.className = 'btn btn-secondary';
    embedOrigBtn.textContent = 'Original Size';
    embedOrigBtn.onclick = function() {
      overlay.remove();
      doInsertImage(originalDataUri, altText, textarea);
    };

    const embedBtn = document.createElement('button');
    embedBtn.className = 'btn btn-primary';
    embedBtn.textContent = 'Embed';
    embedBtn.onclick = function() {
      const pct = parseInt(slider.value, 10);
      overlay.remove();

      if (pct >= 100) {
        doInsertImage(originalDataUri, altText, textarea);
        return;
      }

      // Resize using canvas
      const w = Math.round(origWidth * pct / 100);
      const h = Math.round(origHeight * pct / 100);
      const canvas = document.createElement('canvas');
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext('2d');
      ctx.drawImage(img, 0, 0, w, h);

      // Use original mime type; fall back to PNG for lossless types
      const outputType = (mimeType === 'image/jpeg' || mimeType === 'image/webp') ? mimeType : 'image/png';
      const quality = (outputType === 'image/jpeg' || outputType === 'image/webp') ? 0.85 : undefined;
      const resizedDataUri = canvas.toDataURL(outputType, quality);
      doInsertImage(resizedDataUri, altText, textarea);
    };

    actions.appendChild(cancelBtn);
    actions.appendChild(embedOrigBtn);
    actions.appendChild(embedBtn);

    dialog.appendChild(title);
    dialog.appendChild(preview);
    dialog.appendChild(controls);
    dialog.appendChild(actions);
    overlay.appendChild(dialog);

    // Close on overlay click (outside dialog)
    overlay.addEventListener('click', function(e) {
      if (e.target === overlay) overlay.remove();
    });

    document.body.appendChild(overlay);
  }

  // Insert a base64 data URI as a markdown image at the textarea cursor
  function doInsertImage(dataUri, altText, textarea) {
    const markdownImage = `![${altText}](${dataUri})`;

    // Insert at cursor position
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const before = textarea.value.substring(0, start);
    const after = textarea.value.substring(end);

    // Add newlines around the image for clean markdown
    const prefix = before.length > 0 && !before.endsWith('\n') ? '\n' : '';
    const suffix = after.length > 0 && !after.startsWith('\n') ? '\n' : '';

    textarea.value = before + prefix + markdownImage + suffix + after;

    // Move cursor after the inserted image
    const newPos = start + prefix.length + markdownImage.length + suffix.length;
    textarea.selectionStart = textarea.selectionEnd = newPos;
    textarea.focus();

    // Trigger input event so any listeners (e.g. dirty state) pick up the change
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
    showToast('Image embedded', 'success');
  }

  // Set up paste handler on the edit body textarea to intercept pasted images
  function setupImagePasteHandler() {
    const textarea = document.getElementById('edit-body');
    if (!textarea) return;

    textarea.addEventListener('paste', function(e) {
      const items = e.clipboardData && e.clipboardData.items;
      if (!items) return;

      for (let i = 0; i < items.length; i++) {
        if (items[i].type.startsWith('image/')) {
          e.preventDefault();
          const file = items[i].getAsFile();
          if (file) {
            insertImageAsBase64(file, textarea);
          }
          return; // Only handle the first image
        }
      }
      // If no image items, let the default paste behavior proceed (text paste)
    });
  }

  // Set up drag-and-drop handler on the edit body textarea
  function setupImageDropHandler() {
    const textarea = document.getElementById('edit-body');
    if (!textarea) return;

    textarea.addEventListener('dragover', function(e) {
      // Check if the drag contains files
      if (e.dataTransfer && e.dataTransfer.types.includes('Files')) {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
        textarea.classList.add('drag-over');
      }
    });

    textarea.addEventListener('dragleave', function(e) {
      textarea.classList.remove('drag-over');
    });

    textarea.addEventListener('drop', function(e) {
      textarea.classList.remove('drag-over');
      const files = e.dataTransfer && e.dataTransfer.files;
      if (!files || files.length === 0) return;

      // Process image files from the drop
      let hasImage = false;
      for (let i = 0; i < files.length; i++) {
        if (files[i].type.startsWith('image/')) {
          if (!hasImage) {
            e.preventDefault();
            hasImage = true;
          }
          insertImageAsBase64(files[i], textarea);
        }
      }
    });
  }

  // ============================================
  // Expose Functions for app.js
  // ============================================

  window.app._setupImagePasteHandler = setupImagePasteHandler;
  window.app._setupImageDropHandler = setupImageDropHandler;

})();
