// Main application JavaScript (vanilla JS - no Alpine.js)

// Application state
const AppState = {
  sidebarOpen: true,
  viewMode: 'grid',

  // Initialize from localStorage
  init() {
    this.sidebarOpen = localStorage.getItem('sidebarOpen') !== 'false';
    this.viewMode = localStorage.getItem('viewMode') || 'grid';
    this.updateSidebarState();
    this.updateViewClasses();
  },

  // Toggle sidebar
  toggleSidebar() {
    this.sidebarOpen = !this.sidebarOpen;
    localStorage.setItem('sidebarOpen', this.sidebarOpen);
    this.updateSidebarState();
  },

  // Update sidebar DOM state
  updateSidebarState() {
    const sidebar = document.getElementById('sidebar');
    if (sidebar) {
      if (this.sidebarOpen) {
        sidebar.classList.remove('sidebar-collapsed');
      } else {
        sidebar.classList.add('sidebar-collapsed');
      }
    }
  },

  // Switch view mode
  switchView(mode) {
    this.viewMode = mode;
    localStorage.setItem('viewMode', mode);
    this.updateViewClasses();
    this.updateViewButtons();
  },

  // Update view classes on notes grid
  updateViewClasses() {
    const grid = document.getElementById('notes-grid');
    if (grid) {
      if (this.viewMode === 'list') {
        grid.classList.remove('notes-grid');
        grid.classList.add('notes-list');
      } else {
        grid.classList.remove('notes-list');
        grid.classList.add('notes-grid');
      }
    }
  },

  // Update view toggle buttons active state
  updateViewButtons() {
    const buttons = document.querySelectorAll('.btn-view');
    buttons.forEach(btn => {
      if (btn.dataset.view === this.viewMode) {
        btn.classList.add('active');
      } else {
        btn.classList.remove('active');
      }
    });
  }
};

// Global functions for event handlers
window.toggleSidebar = function() {
  AppState.toggleSidebar();
};

window.switchView = function(mode) {
  AppState.switchView(mode);
};

window.triggerImportFile = function() {
  const importFile = document.getElementById('import-file-input');
  if (importFile) {
    importFile.click();
  }
};

window.handleImport = async function(event) {
  const file = event.target.files[0];
  if (!file) return;

  const formData = new FormData();
  formData.append('file', file);

  try {
    const response = await fetch('/api/import', {
      method: 'POST',
      body: formData
    });

    if (response.ok) {
      showNotification('Notes imported successfully', 'success');
      // Refresh the page to show imported notes
      setTimeout(() => window.location.reload(), 1000);
    } else {
      showNotification('Failed to import notes', 'error');
    }
  } catch (error) {
    console.error('Import error:', error);
    showNotification('Failed to import notes', 'error');
  }

  // Reset file input
  event.target.value = '';
};

window.clearSearchInput = function(el) {
  el.value = '';
};

window.showPreferences = function() {
  // TODO: Implement preferences modal
  console.log('Preferences clicked - not yet implemented');
};

// Notification component using vanilla JS
function createNotification(msgType, message) {
  const container = document.createElement('div');
  container.className = `notification notification-${msgType}`;
  container.innerHTML = `
    <span class="notification-message">${message}</span>
    <button class="notification-close" onclick="this.parentElement.remove()">×</button>
  `;

  // Auto-remove after 5 seconds
  setTimeout(() => {
    if (container.parentElement) {
      container.style.opacity = '0';
      container.style.transition = 'opacity 0.3s ease';
      setTimeout(() => container.remove(), 300);
    }
  }, 5000);

  return container;
}

// Show notification
function showNotification(message, type = 'info') {
  const notification = document.createElement('div');
  notification.className = `alert alert-${type} notification`;
  notification.textContent = message;

  // Add styles for positioning
  notification.style.cssText = `
    position: fixed;
    top: 80px;
    right: 20px;
    z-index: 1000;
    min-width: 250px;
    animation: slideIn 0.3s ease;
  `;

  document.body.appendChild(notification);

  // Remove after 3 seconds
  setTimeout(() => {
    notification.style.animation = 'slideOut 0.3s ease';
    setTimeout(() => notification.remove(), 300);
  }, 3000);
}

// Handle real-time updates
function handleRealtimeUpdate(event) {
  switch(event.type) {
    case 'note-created':
      // Trigger refresh of notes list
      htmx.trigger('#notes-grid', 'refresh');
      showNotification('New note created', 'success');
      break;

    case 'note-updated':
      // Update specific note card if visible
      const noteCard = document.querySelector(`[data-note-guid="${event.note.guid}"]`);
      if (noteCard) {
        htmx.trigger(noteCard, 'refresh');
      }
      break;

    case 'note-deleted':
      // Remove note card from view
      const deletedCard = document.querySelector(`[data-note-guid="${event.note.guid}"]`);
      if (deletedCard) {
        deletedCard.style.transition = 'opacity 0.3s';
        deletedCard.style.opacity = '0';
        setTimeout(() => deletedCard.remove(), 300);
      }
      break;

    case 'tag-update':
      // Refresh tags list
      htmx.trigger('#tags-list', 'tagUpdate');
      break;
  }
}

// Auto-dismiss notifications
function initAutoDismissNotifications() {
  document.querySelectorAll('.notification.auto-dismiss').forEach(notification => {
    if (!notification.dataset.initialized) {
      notification.dataset.initialized = 'true';
      setTimeout(() => {
        if (notification.parentElement) {
          notification.style.opacity = '0';
          notification.style.transition = 'opacity 0.3s ease';
          setTimeout(() => notification.remove(), 300);
        }
      }, 5000);
    }
  });
}

// Initialize on DOMContentLoaded
document.addEventListener('DOMContentLoaded', () => {
  // Initialize application state
  AppState.init();

  // Initialize auto-dismiss notifications
  initAutoDismissNotifications();

  // Handle HTMX events
  document.body.addEventListener('htmx:afterSwap', (event) => {
    // Re-apply state after HTMX swaps content
    AppState.updateViewClasses();
    AppState.updateViewButtons();

    // Re-initialize auto-dismiss notifications
    initAutoDismissNotifications();

    // Re-initialize syntax highlighting if present
    if (typeof Prism !== 'undefined') {
      Prism.highlightAll();
    }
  });

  // Handle HTMX errors
  document.body.addEventListener('htmx:responseError', (event) => {
    console.error('HTMX Error:', event.detail);
    showNotification('An error occurred. Please try again.', 'error');
  });

  // Handle SSE updates
  if (window.handleSSEUpdate === undefined) {
    window.handleSSEUpdate = function(data) {
      try {
        const event = JSON.parse(data);
        handleRealtimeUpdate(event);
      } catch (e) {
        console.error('Failed to parse SSE data:', e);
      }
    };
  }
});

// Keyboard shortcuts
document.addEventListener('keydown', (event) => {
  // Ctrl/Cmd + K for search focus
  if ((event.ctrlKey || event.metaKey) && event.key === 'k') {
    event.preventDefault();
    const searchInput = document.querySelector('.search-input');
    if (searchInput) {
      searchInput.focus();
    }
  }

  // Ctrl/Cmd + N for new note
  if ((event.ctrlKey || event.metaKey) && event.key === 'n') {
    event.preventDefault();
    window.location.href = '/notes/new';
  }

  // Escape to close modals and clear search
  if (event.key === 'Escape') {
    // Close any open modals or overlays
    const modals = document.querySelectorAll('.modal');
    modals.forEach(modal => modal.style.display = 'none');

    // Clear search input if focused
    const searchInput = document.querySelector('.search-input:focus');
    if (searchInput) {
      searchInput.value = '';
    }
  }
});

// Add CSS for notifications animation
const style = document.createElement('style');
style.textContent = `
  @keyframes slideIn {
    from {
      transform: translateX(100%);
      opacity: 0;
    }
    to {
      transform: translateX(0);
      opacity: 1;
    }
  }

  @keyframes slideOut {
    from {
      transform: translateX(0);
      opacity: 1;
    }
    to {
      transform: translateX(100%);
      opacity: 0;
    }
  }

  .notes-list {
    display: flex;
    flex-direction: column;
    gap: var(--spacing-md);
  }

  .notes-list .note-card {
    max-width: 100%;
  }
`;
document.head.appendChild(style);
