// Search-specific JavaScript

// Initialize search functionality
document.addEventListener('DOMContentLoaded', () => {
  initSearchPage();
  initQuickSearch();
});

// Initialize search page functionality
function initSearchPage() {
  const searchForm = document.getElementById('advanced-search');
  if (!searchForm) return;
  
  // Handle search form submission
  searchForm.addEventListener('submit', (e) => {
    // Let HTMX handle the submission, just track it
    trackSearch();
  });
  
  // Handle search type change
  const searchType = document.getElementById('search-type');
  if (searchType) {
    searchType.addEventListener('change', () => {
      updateSearchPlaceholder(searchType.value);
    });
  }
  
  // Initialize date pickers
  initDateRangeFilter();
}

// Initialize quick search in header
function initQuickSearch() {
  const searchInputs = document.querySelectorAll('.search-input');
  
  searchInputs.forEach(input => {
    // Add search suggestions
    input.addEventListener('input', debounce(() => {
      if (input.value.length >= 2) {
        fetchSearchSuggestions(input.value);
      }
    }, 300));
    
    // Handle keyboard navigation
    input.addEventListener('keydown', (e) => {
      handleSearchKeyboard(e, input);
    });
  });
}

// Update search placeholder based on type
function updateSearchPlaceholder(type) {
  const searchQuery = document.getElementById('search-query');
  if (!searchQuery) return;
  
  const placeholders = {
    'all': 'Search in all fields...',
    'title': 'Search in titles...',
    'body': 'Search in note content...',
    'tags': 'Search for tags...'
  };
  
  searchQuery.placeholder = placeholders[type] || placeholders['all'];
}

// Initialize date range filter
function initDateRangeFilter() {
  const dateFrom = document.getElementById('date-from');
  const dateTo = document.getElementById('date-to');
  
  if (!dateFrom || !dateTo) return;
  
  // Set max date to today
  const today = new Date().toISOString().split('T')[0];
  dateFrom.max = today;
  dateTo.max = today;
  
  // Ensure from date is not after to date
  dateFrom.addEventListener('change', () => {
    if (dateFrom.value && dateTo.value && dateFrom.value > dateTo.value) {
      dateTo.value = dateFrom.value;
    }
    dateTo.min = dateFrom.value;
  });
  
  dateTo.addEventListener('change', () => {
    if (dateFrom.value && dateTo.value && dateTo.value < dateFrom.value) {
      dateFrom.value = dateTo.value;
    }
  });
}

// Fetch search suggestions
async function fetchSearchSuggestions(query) {
  try {
    const response = await fetch(`/api/search/suggestions?q=${encodeURIComponent(query)}`);
    if (response.ok) {
      const suggestions = await response.json();
      displaySearchSuggestions(suggestions);
    }
  } catch (error) {
    console.error('Failed to fetch search suggestions:', error);
  }
}

// Display search suggestions
function displaySearchSuggestions(suggestions) {
  // Remove existing suggestions
  const existingSuggestions = document.querySelector('.search-suggestions');
  if (existingSuggestions) {
    existingSuggestions.remove();
  }
  
  if (!suggestions || suggestions.length === 0) return;
  
  const searchInput = document.querySelector('.search-input:focus');
  if (!searchInput) return;
  
  // Create suggestions container
  const suggestionsDiv = document.createElement('div');
  suggestionsDiv.className = 'search-suggestions';
  suggestionsDiv.style.cssText = `
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    background: var(--bg-primary);
    border: 1px solid var(--border-light);
    border-top: none;
    border-radius: 0 0 var(--radius-md) var(--radius-md);
    max-height: 300px;
    overflow-y: auto;
    z-index: 100;
    box-shadow: var(--shadow-md);
  `;
  
  // Add suggestions
  suggestions.forEach(suggestion => {
    const item = document.createElement('div');
    item.className = 'suggestion-item';
    item.style.cssText = `
      padding: var(--spacing-sm) var(--spacing-md);
      cursor: pointer;
      transition: background var(--transition-fast);
    `;
    item.textContent = suggestion.title || suggestion.text;
    
    item.addEventListener('mouseenter', () => {
      item.style.background = 'var(--bg-tertiary)';
    });
    
    item.addEventListener('mouseleave', () => {
      item.style.background = 'transparent';
    });
    
    item.addEventListener('click', () => {
      searchInput.value = suggestion.title || suggestion.text;
      suggestionsDiv.remove();
      searchInput.form.requestSubmit();
    });
    
    suggestionsDiv.appendChild(item);
  });
  
  // Position relative to search input
  const inputRect = searchInput.getBoundingClientRect();
  searchInput.parentElement.style.position = 'relative';
  searchInput.parentElement.appendChild(suggestionsDiv);
  
  // Close on click outside
  document.addEventListener('click', (e) => {
    if (!searchInput.contains(e.target) && !suggestionsDiv.contains(e.target)) {
      suggestionsDiv.remove();
    }
  }, { once: true });
}

// Handle keyboard navigation in search
function handleSearchKeyboard(e, input) {
  const suggestions = document.querySelector('.search-suggestions');
  if (!suggestions) return;
  
  const items = suggestions.querySelectorAll('.suggestion-item');
  if (items.length === 0) return;
  
  let currentIndex = Array.from(items).findIndex(item => 
    item.classList.contains('suggestion-active')
  );
  
  switch(e.key) {
    case 'ArrowDown':
      e.preventDefault();
      currentIndex = (currentIndex + 1) % items.length;
      highlightSuggestion(items, currentIndex);
      break;
      
    case 'ArrowUp':
      e.preventDefault();
      currentIndex = currentIndex <= 0 ? items.length - 1 : currentIndex - 1;
      highlightSuggestion(items, currentIndex);
      break;
      
    case 'Enter':
      if (currentIndex >= 0) {
        e.preventDefault();
        items[currentIndex].click();
      }
      break;
      
    case 'Escape':
      suggestions.remove();
      break;
  }
}

// Highlight active suggestion
function highlightSuggestion(items, index) {
  items.forEach((item, i) => {
    if (i === index) {
      item.classList.add('suggestion-active');
      item.style.background = 'var(--bg-tertiary)';
    } else {
      item.classList.remove('suggestion-active');
      item.style.background = 'transparent';
    }
  });
}

// Track search for analytics
function trackSearch() {
  const query = document.getElementById('search-query')?.value;
  const type = document.getElementById('search-type')?.value;
  
  // Store recent searches
  const recentSearches = JSON.parse(localStorage.getItem('recentSearches') || '[]');
  recentSearches.unshift({ query, type, timestamp: Date.now() });
  
  // Keep only last 10 searches
  if (recentSearches.length > 10) {
    recentSearches.pop();
  }
  
  localStorage.setItem('recentSearches', JSON.stringify(recentSearches));
}

// Get recent searches
window.getRecentSearches = function() {
  return JSON.parse(localStorage.getItem('recentSearches') || '[]');
};

// Clear search history
window.clearSearchHistory = function() {
  localStorage.removeItem('recentSearches');
  showNotification('Search history cleared', 'success');
};

// Debounce function
function debounce(func, wait) {
  let timeout;
  return function executedFunction(...args) {
    const later = () => {
      clearTimeout(timeout);
      func(...args);
    };
    clearTimeout(timeout);
    timeout = setTimeout(later, wait);
  };
}

// Export search results
window.exportSearchResults = function() {
  const results = document.querySelectorAll('.note-card');
  if (results.length === 0) {
    showNotification('No results to export', 'warning');
    return;
  }
  
  const exportData = Array.from(results).map(card => ({
    guid: card.dataset.noteGuid,
    title: card.querySelector('.note-title')?.textContent,
    preview: card.querySelector('.note-preview')?.textContent,
    tags: card.querySelector('.note-tags')?.textContent
  }));
  
  // Create download link
  const blob = new Blob([JSON.stringify(exportData, null, 2)], { 
    type: 'application/json' 
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `search-results-${Date.now()}.json`;
  a.click();
  URL.revokeObjectURL(url);
  
  showNotification('Search results exported', 'success');
};