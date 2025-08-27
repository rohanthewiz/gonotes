// Main application JavaScript

// Initialize Alpine.js data and components
document.addEventListener('alpine:init', () => {
  // Global Alpine data
  Alpine.data('app', () => ({
    sidebarOpen: true,
    searchQuery: '',
    showPreferences: false,
    viewMode: 'grid', // grid or list
    
    // Toggle sidebar
    toggleSidebar() {
      this.sidebarOpen = !this.sidebarOpen;
      localStorage.setItem('sidebarOpen', this.sidebarOpen);
    },
    
    // Switch view mode
    switchView(mode) {
      this.viewMode = mode;
      localStorage.setItem('viewMode', mode);
      this.updateViewClasses();
    },
    
    // Update view classes
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
    
    // Initialize from localStorage
    init() {
      this.sidebarOpen = localStorage.getItem('sidebarOpen') !== 'false';
      this.viewMode = localStorage.getItem('viewMode') || 'grid';
      this.updateViewClasses();
    }
  }));
});

// HTMX event handlers
document.addEventListener('DOMContentLoaded', () => {
  // Handle HTMX events
  document.body.addEventListener('htmx:afterSwap', (event) => {
    // Re-initialize Alpine components after HTMX swap
    if (window.Alpine) {
      Alpine.initTree(event.detail.target);
    }
    
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

// Import file handling
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
  
  // Escape to close modals
  if (event.key === 'Escape') {
    // Close any open modals or overlays
    const modals = document.querySelectorAll('.modal');
    modals.forEach(modal => modal.style.display = 'none');
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