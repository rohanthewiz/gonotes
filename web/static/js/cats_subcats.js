// Category and Subcategory Management for GoNotes
// Extracted from app.js to isolate category-specific logic.
// Handles: category CRUD, note-category associations, search filtering
// by category/subcategory, edit form multi-category entries, preview
// category display, and the category manager modal.
//
// Dependencies: Loaded after app.js. Accesses shared internals via
// window.app._internal which app.js exposes before DOMContentLoaded.

(function() {
  'use strict';

  // Lazy accessors for shared internals exposed by app.js.
  // Resolved at call time — app.js's IIFE runs first and sets _internal,
  // then this IIFE runs, and finally DOMContentLoaded triggers init().
  function getState() { return window.app._internal.state; }
  function apiRequest(endpoint, options) { return window.app._internal.apiRequest(endpoint, options); }
  function showToast(message, type) { return window.app._internal.showToast(message, type); }
  function escapeHtml(text) { return window.app._internal.escapeHtml(text); }
  function renderNoteList() { return window.app._internal.renderNoteList(); }
  function updateResultCount() { return window.app._internal.updateResultCount(); }
  function updateActiveFilters() { return window.app._internal.updateActiveFilters(); }

  // ============================================
  // Category Loading
  // ============================================

  async function loadCategories() {
    try {
      const response = await apiRequest('/categories');
      if (response && response.data) {
        getState().categories = response.data;
        populateCategorySelect();
        populateSearchCategoryDropdown();
      }
    } catch (error) {
      console.error('Failed to load categories:', error);
    }
  }

  // Fetch all note-category mappings in one bulk call and build a lookup map.
  // The map is keyed by note ID so getFilteredNotes() can check category membership
  // instantly without per-note API calls. Called once on init and after saves.
  async function loadNoteCategoryMappings() {
    try {
      const response = await apiRequest('/note-category-mappings');
      if (response && response.data) {
        // Build lookup: { noteId: [{ categoryId, categoryName, subcategories }] }
        const map = {};
        response.data.forEach(m => {
          if (!map[m.note_id]) {
            map[m.note_id] = [];
          }
          map[m.note_id].push({
            categoryId: m.category_id,
            categoryName: m.category_name,
            subcategories: m.selected_subcategories || []
          });
        });
        getState().noteCategoryMap = map;
      }
    } catch (error) {
      console.error('Failed to load note-category mappings:', error);
    }
  }

  // ============================================
  // Category Select / Dropdown Population
  // ============================================

  // Populate the search bar's category dropdown from state.categories.
  // Called after loadCategories() and after saves that may create new categories.
  function populateSearchCategoryDropdown() {
    const select = document.getElementById('search-category-select');
    if (!select) return;

    // Preserve current selection if it still exists
    const currentValue = select.value;

    // Clear and rebuild options — first option is always "All Categories"
    select.innerHTML = '<option value="">All Categories</option>';
    getState().categories.forEach(cat => {
      const option = document.createElement('option');
      option.value = cat.id;
      option.textContent = cat.name;
      select.appendChild(option);
    });

    // Restore selection if the category still exists
    if (currentValue) {
      select.value = currentValue;
    }
  }

  // Populate datalist for category autocomplete in the edit form
  function populateCategorySelect() {
    const datalist = document.getElementById('category-datalist');
    if (!datalist) return;

    datalist.innerHTML = getState().categories.map(cat =>
      `<option value="${escapeHtml(cat.name)}">`
    ).join('');
  }

  // ============================================
  // Search Bar Category Filtering
  // ============================================

  // handleCategoryFilter — called when the search bar category dropdown changes.
  // Reads the selected category ID, looks up subcategories from state.categories,
  // and renders toggleable subcategory chips if the category has any.
  window.app.handleCategoryFilter = function(categoryIdStr) {
    const state = getState();
    if (!categoryIdStr) {
      // "All Categories" selected — clear category filter
      state.filters.categoryId = null;
      state.filters.categoryName = '';
      state.filters.subcategories = [];
      renderSubcategoryChips([]);
    } else {
      const categoryId = parseInt(categoryIdStr, 10);
      const cat = state.categories.find(c => c.id === categoryId);
      state.filters.categoryId = categoryId;
      state.filters.categoryName = cat ? cat.name : '';
      state.filters.subcategories = [];
      // Render subcategory chips from the category definition
      const subcats = (cat && cat.subcategories) ? cat.subcategories : [];
      renderSubcategoryChips(subcats);
    }

    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  // Render toggleable subcategory chips in the search bar.
  // Each chip toggles on/off to narrow results within the selected category.
  function renderSubcategoryChips(subcategories) {
    const container = document.getElementById('search-subcats-container');
    if (!container) return;

    container.innerHTML = '';

    subcategories.forEach(sub => {
      const chip = document.createElement('span');
      chip.className = 'subcat-chip';
      chip.textContent = sub;
      chip.onclick = () => window.app.toggleSubcategoryFilter(sub);
      container.appendChild(chip);
    });
  }

  // Toggle a subcategory chip on/off in the search bar filter
  window.app.toggleSubcategoryFilter = function(subcat) {
    const state = getState();
    const idx = state.filters.subcategories.indexOf(subcat);
    if (idx >= 0) {
      state.filters.subcategories.splice(idx, 1);
    } else {
      state.filters.subcategories.push(subcat);
    }

    // Update chip active state in the DOM
    const container = document.getElementById('search-subcats-container');
    if (container) {
      container.querySelectorAll('.subcat-chip').forEach(chip => {
        if (chip.textContent === subcat) {
          chip.classList.toggle('active');
        }
      });
    }

    renderNoteList();
    updateResultCount();
    updateActiveFilters();
  };

  // ============================================
  // Preview Categories
  // ============================================

  // Fetches the note's assigned categories and renders each as a row:
  // [Category Name]  subcat1  subcat2  subcat3
  // Uses bold + primary color for category, muted style for subcategories.
  async function renderPreviewCategories(noteId) {
    const container = document.getElementById('preview-categories');
    container.innerHTML = '';

    try {
      const resp = await apiRequest(`/notes/${noteId}/categories`);
      if (!resp || !resp.data || resp.data.length === 0) return;

      resp.data.forEach(cat => {
        const row = document.createElement('div');
        row.className = 'preview-cat-row';

        // Bold category label
        const catSpan = document.createElement('span');
        catSpan.className = 'preview-cat-name';
        catSpan.textContent = cat.name;
        row.appendChild(catSpan);

        // Append each selected subcategory as a lighter chip
        const subcats = cat.selected_subcategories || [];
        subcats.forEach(sub => {
          const subSpan = document.createElement('span');
          subSpan.className = 'preview-subcat';
          subSpan.textContent = sub;
          row.appendChild(subSpan);
        });

        container.appendChild(row);
      });
    } catch (err) {
      console.error('Failed to load preview categories:', err);
    }
  }

  // ============================================
  // Multi-Category Edit State
  // ============================================

  // Multi-category editing state.
  // Each Map entry: key = lowercase category name,
  // value = { categoryId, categoryName, selectedSubcats, newSubcategories, isNew }
  let categoryEntries = new Map();         // current editing state
  let originalCategoryEntries = new Map(); // snapshot at edit start (for save diff)

  // ============================================
  // Edit Form Category Helpers
  // ============================================

  // onCategoryChange - Called when user types in the category add-input.
  // Only toggles the "(new)" indicator; subcategory rendering is per-entry card.
  window.app.onCategoryChange = function(categoryName) {
    const newIndicator = document.getElementById('new-category-indicator');

    if (!categoryName || !categoryName.trim()) {
      if (newIndicator) newIndicator.style.display = 'none';
      return;
    }

    const trimmedName = categoryName.trim();
    const category = getState().categories.find(c =>
      c.name.toLowerCase() === trimmedName.toLowerCase()
    );

    // Show "(new)" when the typed name doesn't match any existing category
    if (newIndicator) {
      newIndicator.style.display = category ? 'none' : 'inline';
    }
  };

  // ============================================
  // Multi-Category Entry Functions
  // ============================================

  // addCategoryEntry - Read the add-input, validate, add to Map, render card
  window.app.addCategoryEntry = function() {
    const input = document.getElementById('edit-category');
    if (!input) return;

    const rawName = input.value.trim();
    if (!rawName) {
      showToast('Enter a category name', 'warning');
      return;
    }

    const key = rawName.toLowerCase();
    if (categoryEntries.has(key)) {
      showToast('Category already added', 'warning');
      return;
    }

    // Look up existing category to get id and subcategories
    const existing = getState().categories.find(c => c.name.toLowerCase() === key);

    const entry = {
      categoryId: existing ? existing.id : null,
      categoryName: existing ? existing.name : rawName,
      selectedSubcats: [],
      newSubcategories: [],
      isNew: !existing
    };

    categoryEntries.set(key, entry);
    renderCategoryEntry(key, entry);

    input.value = '';
    const newIndicator = document.getElementById('new-category-indicator');
    if (newIndicator) newIndicator.style.display = 'none';
  };

  // removeCategoryEntry - Remove from Map and DOM
  window.app.removeCategoryEntry = function(key) {
    categoryEntries.delete(key);
    const card = document.getElementById('cat-entry-' + CSS.escape(key));
    if (card) card.remove();
  };

  // renderCategoryEntry - Build DOM for one entry card and append to container
  function renderCategoryEntry(key, entry) {
    const container = document.getElementById('category-entries-container');
    if (!container) return;

    const card = document.createElement('div');
    card.className = 'category-entry';
    card.id = 'cat-entry-' + key;

    // Header row: name + new indicator + remove button
    const header = document.createElement('div');
    header.className = 'category-entry-header';

    const nameSpan = document.createElement('span');
    nameSpan.className = 'category-entry-name';
    nameSpan.textContent = entry.categoryName;
    if (entry.isNew) {
      const indicator = document.createElement('span');
      indicator.className = 'new-indicator';
      indicator.style.marginLeft = '6px';
      indicator.textContent = '(new)';
      nameSpan.appendChild(indicator);
    }
    header.appendChild(nameSpan);

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'category-entry-remove';
    removeBtn.innerHTML = '&times;';
    removeBtn.onclick = () => window.app.removeCategoryEntry(key);
    header.appendChild(removeBtn);

    card.appendChild(header);

    // Subcategory section
    const subcatsDiv = document.createElement('div');
    subcatsDiv.className = 'category-entry-subcats';

    // Find existing category definition to get available subcats
    const catDef = getState().categories.find(c => c.name.toLowerCase() === key);
    const availableSubcats = (catDef && catDef.subcategories) ? catDef.subcategories : [];

    if (availableSubcats.length > 0) {
      // Render checkbox for each defined subcategory
      const selectDiv = document.createElement('div');
      selectDiv.className = 'subcat-select';

      availableSubcats.forEach(subcat => {
        const label = document.createElement('label');
        label.className = 'subcat-checkbox-label';
        const isSelected = entry.selectedSubcats.includes(subcat);

        label.innerHTML = `
          <input type="checkbox" class="subcat-checkbox" value="${escapeHtml(subcat)}"
                 ${isSelected ? 'checked' : ''}
                 onchange="app.toggleEntrySubcat('${escapeHtml(key)}', '${escapeHtml(subcat)}', this.checked)">
          <span>${escapeHtml(subcat)}</span>
        `;
        selectDiv.appendChild(label);
      });

      // Also render any new subcats that were dynamically added
      entry.newSubcategories.forEach(subcat => {
        const label = document.createElement('label');
        label.className = 'subcat-checkbox-label new-subcat';
        const isSelected = entry.selectedSubcats.includes(subcat);

        label.innerHTML = `
          <input type="checkbox" class="subcat-checkbox" value="${escapeHtml(subcat)}"
                 ${isSelected ? 'checked' : ''}
                 onchange="app.toggleEntrySubcat('${escapeHtml(key)}', '${escapeHtml(subcat)}', this.checked)">
          <span>${escapeHtml(subcat)} <em>(new)</em></span>
        `;
        selectDiv.appendChild(label);
      });

      subcatsDiv.appendChild(selectDiv);
    } else if (entry.newSubcategories.length > 0) {
      // No existing subcats but has new ones — render them
      const selectDiv = document.createElement('div');
      selectDiv.className = 'subcat-select';

      entry.newSubcategories.forEach(subcat => {
        const label = document.createElement('label');
        label.className = 'subcat-checkbox-label new-subcat';
        const isSelected = entry.selectedSubcats.includes(subcat);

        label.innerHTML = `
          <input type="checkbox" class="subcat-checkbox" value="${escapeHtml(subcat)}"
                 ${isSelected ? 'checked' : ''}
                 onchange="app.toggleEntrySubcat('${escapeHtml(key)}', '${escapeHtml(subcat)}', this.checked)">
          <span>${escapeHtml(subcat)} <em>(new)</em></span>
        `;
        selectDiv.appendChild(label);
      });

      subcatsDiv.appendChild(selectDiv);
    }

    // New subcategory input row for this entry
    const newSubcatRow = document.createElement('div');
    newSubcatRow.className = 'new-subcat-input';

    const newSubcatInput = document.createElement('input');
    newSubcatInput.type = 'text';
    newSubcatInput.className = 'edit-input subcat-input';
    newSubcatInput.placeholder = 'Add subcategory...';
    newSubcatInput.onkeypress = function(e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        window.app.addNewSubcatToEntry(key, this);
      }
    };

    const addSubcatBtn = document.createElement('button');
    addSubcatBtn.type = 'button';
    addSubcatBtn.className = 'btn btn-secondary btn-sm';
    addSubcatBtn.textContent = 'Add';
    addSubcatBtn.onclick = function() {
      window.app.addNewSubcatToEntry(key, newSubcatInput);
    };

    newSubcatRow.appendChild(newSubcatInput);
    newSubcatRow.appendChild(addSubcatBtn);
    subcatsDiv.appendChild(newSubcatRow);

    card.appendChild(subcatsDiv);
    container.appendChild(card);
  }

  // renderAllCategoryEntries - Clear container and render all entries from Map
  function renderAllCategoryEntries() {
    const container = document.getElementById('category-entries-container');
    if (!container) return;
    container.innerHTML = '';

    categoryEntries.forEach((entry, key) => {
      renderCategoryEntry(key, entry);
    });
  }

  // toggleEntrySubcat - Update a specific entry's selectedSubcats list
  window.app.toggleEntrySubcat = function(catKey, subcat, checked) {
    const entry = categoryEntries.get(catKey);
    if (!entry) return;

    if (checked) {
      if (!entry.selectedSubcats.includes(subcat)) {
        entry.selectedSubcats.push(subcat);
      }
    } else {
      entry.selectedSubcats = entry.selectedSubcats.filter(s => s !== subcat);
    }
  };

  // addNewSubcatToEntry - Add a new subcategory to a specific entry
  window.app.addNewSubcatToEntry = function(catKey, inputEl) {
    const entry = categoryEntries.get(catKey);
    if (!entry || !inputEl) return;

    const value = inputEl.value.trim();
    if (!value) return;

    // Check for duplicates in both existing and new subcategories
    const catDef = getState().categories.find(c => c.name.toLowerCase() === catKey);
    const existingSubcats = (catDef && catDef.subcategories) ? catDef.subcategories : [];

    if (existingSubcats.includes(value) || entry.newSubcategories.includes(value)) {
      showToast('Subcategory already exists', 'warning');
      return;
    }

    // Add to the entry's new subcategories and select it
    entry.newSubcategories.push(value);
    entry.selectedSubcats.push(value);

    inputEl.value = '';

    // Re-render this entry card to show the new subcategory checkbox
    const card = document.getElementById('cat-entry-' + CSS.escape(catKey));
    if (card) card.remove();
    renderCategoryEntry(catKey, entry);

    showToast(`Subcategory "${value}" added`, 'success');
  };

  // clearCategoryEntries - Reset both Maps and clear the container
  function clearCategoryEntries() {
    categoryEntries.clear();
    originalCategoryEntries.clear();
    const container = document.getElementById('category-entries-container');
    if (container) container.innerHTML = '';
    const newIndicator = document.getElementById('new-category-indicator');
    if (newIndicator) newIndicator.style.display = 'none';
    const catInput = document.getElementById('edit-category');
    if (catInput) catInput.value = '';
  }

  // ============================================
  // Edit Note Category Loading
  // ============================================

  // Fetch note's categories from API and populate multi-category entries.
  // Called from editNote() after the form is shown.
  async function loadEditNoteCategories(noteId) {
    try {
      const resp = await apiRequest(`/notes/${noteId}/categories`);
      if (resp && resp.data && resp.data.length > 0) {
        // Populate both Maps from all categories assigned to this note
        resp.data.forEach(noteCategory => {
          const key = noteCategory.name.toLowerCase();
          const entry = {
            categoryId: noteCategory.id,
            categoryName: noteCategory.name,
            selectedSubcats: (noteCategory.selected_subcategories || []).slice(),
            newSubcategories: [],
            isNew: false
          };
          categoryEntries.set(key, entry);
          // Deep copy for original state so save can compute diff
          originalCategoryEntries.set(key, {
            categoryId: entry.categoryId,
            categoryName: entry.categoryName,
            selectedSubcats: entry.selectedSubcats.slice(),
            newSubcategories: [],
            isNew: false
          });
        });
        renderAllCategoryEntries();
      }
    } catch (err) {
      console.error('Failed to load note categories:', err);
    }
  }

  // ============================================
  // Save Note Category Assignments
  // ============================================

  // Multi-category diff-based save: compare originalCategoryEntries vs categoryEntries.
  // Computes removed, added, and kept (with possible subcat changes).
  // Called from saveNote() after the note itself is saved.
  async function saveCategoryAssignments(savedNoteId) {
    const state = getState();

    // Removed: in original but not in current
    for (const [key, origEntry] of originalCategoryEntries) {
      if (!categoryEntries.has(key)) {
        await apiRequest(`/notes/${savedNoteId}/categories/${origEntry.categoryId}`, {
          method: 'DELETE'
        });
      }
    }

    // Added & kept entries
    for (const [key, entry] of categoryEntries) {
      const isAdded = !originalCategoryEntries.has(key);

      // Ensure category exists — create if new
      let categoryId = entry.categoryId;
      if (entry.isNew && !categoryId) {
        const createResp = await apiRequest('/categories', {
          method: 'POST',
          body: JSON.stringify({
            name: entry.categoryName,
            subcategories: entry.newSubcategories.length > 0 ? entry.newSubcategories : []
          })
        });
        if (createResp && createResp.data) {
          categoryId = createResp.data.id;
          entry.categoryId = categoryId;
          showToast(`Category "${entry.categoryName}" created`, 'success');
        }
      } else if (entry.newSubcategories.length > 0 && categoryId) {
        // Merge new subcategories into existing category definition
        const catDef = state.categories.find(c => c.id === categoryId);
        const existingSubcats = (catDef && catDef.subcategories) ? catDef.subcategories : [];
        const allSubcats = [...new Set([...existingSubcats, ...entry.newSubcategories])];

        await apiRequest(`/categories/${categoryId}`, {
          method: 'PUT',
          body: JSON.stringify({
            name: entry.categoryName,
            subcategories: allSubcats
          })
        });
      }

      if (!categoryId) continue;

      if (isAdded) {
        // New association — POST to create the note-category link
        await apiRequest(`/notes/${savedNoteId}/categories/${categoryId}`, {
          method: 'POST',
          body: JSON.stringify({ subcategories: entry.selectedSubcats })
        });
      } else {
        // Kept entry — check if subcategories changed
        const origEntry = originalCategoryEntries.get(key);
        const subcatsChanged =
          JSON.stringify(origEntry.selectedSubcats.slice().sort()) !==
          JSON.stringify(entry.selectedSubcats.slice().sort());

        if (subcatsChanged) {
          await apiRequest(`/notes/${savedNoteId}/categories/${categoryId}`, {
            method: 'PUT',
            body: JSON.stringify({ subcategories: entry.selectedSubcats })
          });
        }
      }
    }
  }

  // ============================================
  // Category Manager Modal
  // ============================================

  // Track editing state for categories in the manager modal
  let editingCategoryId = null;
  let editingSubcategories = [];

  window.app.showCategoryManager = function() {
    const modalTitle = document.getElementById('modal-title');
    const modalBody = document.getElementById('modal-body');
    const modalFooter = document.getElementById('modal-footer');

    modalTitle.textContent = 'Manage Categories';

    // Build category manager HTML
    modalBody.innerHTML = `
      <div class="category-manager">
        <div class="category-manager-header">
          <input type="text" id="new-category-name" placeholder="New category name..." />
          <button class="btn btn-primary" onclick="app.createCategory()">Add</button>
        </div>
        <div class="category-list" id="category-list">
          ${renderCategoryList()}
        </div>
      </div>
    `;

    // Hide default footer buttons, we handle actions inline
    modalFooter.innerHTML = `
      <button class="btn btn-secondary" onclick="app.closeModal()">Close</button>
    `;

    document.getElementById('modal-overlay').classList.add('open');

    // Focus the input
    document.getElementById('new-category-name').focus();
  };

  function renderCategoryList() {
    const state = getState();
    if (state.categories.length === 0) {
      return '<div class="empty-categories">No categories yet. Create one above.</div>';
    }

    return state.categories.map(cat => {
      const subcats = cat.subcategories || [];
      const subcatText = subcats.length > 0 ? subcats.join(', ') : 'No subcategories';

      return `
        <div class="category-item" data-category-id="${cat.id}">
          <div class="category-item-header">
            <span class="category-name">${escapeHtml(cat.name)}</span>
            <span class="category-subcats">${escapeHtml(subcatText)}</span>
            <div class="category-actions">
              <button class="btn-edit-cat" onclick="app.editCategory(${cat.id})">Edit</button>
              <button class="btn-delete-cat" onclick="app.deleteCategory(${cat.id})">Delete</button>
            </div>
          </div>
          <div class="category-edit-form" id="category-edit-${cat.id}" style="display: none;">
          </div>
        </div>
      `;
    }).join('');
  }

  window.app.createCategory = async function() {
    const input = document.getElementById('new-category-name');
    const name = input.value.trim();

    if (!name) {
      showToast('Please enter a category name', 'error');
      return;
    }

    try {
      const response = await apiRequest('/categories', {
        method: 'POST',
        body: JSON.stringify({ name: name })
      });

      if (response && response.data) {
        showToast('Category created', 'success');
        input.value = '';
        await loadCategories();
        // Refresh the modal list
        document.getElementById('category-list').innerHTML = renderCategoryList();
      }
    } catch (error) {
      showToast('Failed to create category', 'error');
    }
  };

  window.app.editCategory = function(categoryId) {
    const state = getState();
    const cat = state.categories.find(c => c.id === categoryId);
    if (!cat) return;

    // Close any other open edit forms
    document.querySelectorAll('.category-edit-form').forEach(form => {
      form.style.display = 'none';
    });

    editingCategoryId = categoryId;
    editingSubcategories = [...(cat.subcategories || [])];

    const editForm = document.getElementById(`category-edit-${categoryId}`);
    editForm.style.display = 'block';
    editForm.innerHTML = `
      <label>Category Name</label>
      <input type="text" id="edit-cat-name-${categoryId}" value="${escapeHtml(cat.name)}" />

      <label>Subcategories</label>
      <div class="subcategory-tags" id="subcategory-tags-${categoryId}">
        ${renderSubcategoryTags(categoryId)}
      </div>
      <div class="subcategory-input-group">
        <input type="text" id="new-subcat-${categoryId}" placeholder="Add subcategory..."
               onkeypress="if(event.key==='Enter'){app.addSubcategory(${categoryId}); event.preventDefault();}" />
        <button class="btn btn-secondary" onclick="app.addSubcategory(${categoryId})">Add</button>
      </div>

      <div class="category-edit-actions">
        <button class="btn btn-secondary" onclick="app.cancelEditCategory(${categoryId})">Cancel</button>
        <button class="btn btn-primary" onclick="app.saveCategory(${categoryId})">Save</button>
      </div>
    `;
  };

  function renderSubcategoryTags(categoryId) {
    if (editingSubcategories.length === 0) {
      return '<span class="text-muted" style="font-size: var(--font-size-xs);">None</span>';
    }

    return editingSubcategories.map((subcat, index) => `
      <span class="subcategory-tag">
        ${escapeHtml(subcat)}
        <button onclick="app.removeSubcategory(${categoryId}, ${index})">&times;</button>
      </span>
    `).join('');
  }

  window.app.addSubcategory = function(categoryId) {
    const input = document.getElementById(`new-subcat-${categoryId}`);
    const value = input ? input.value.trim() : '';

    if (!value) return;

    if (editingSubcategories.includes(value)) {
      showToast('Subcategory already exists', 'warning');
      return;
    }

    editingSubcategories.push(value);
    input.value = '';

    // Re-render tags
    const tagsContainer = document.getElementById(`subcategory-tags-${categoryId}`);
    if (tagsContainer) {
      tagsContainer.innerHTML = renderSubcategoryTags(categoryId);
    }
  };

  window.app.removeSubcategory = function(categoryId, index) {
    editingSubcategories.splice(index, 1);
    document.getElementById(`subcategory-tags-${categoryId}`).innerHTML = renderSubcategoryTags(categoryId);
  };

  window.app.cancelEditCategory = function(categoryId) {
    const editForm = document.getElementById(`category-edit-${categoryId}`);
    editForm.style.display = 'none';
    editingCategoryId = null;
    editingSubcategories = [];
  };

  window.app.saveCategory = async function(categoryId) {
    const nameInput = document.getElementById(`edit-cat-name-${categoryId}`);
    const name = nameInput.value.trim();

    if (!name) {
      showToast('Category name is required', 'error');
      return;
    }

    try {
      const payload = {
        name: name,
        subcategories: editingSubcategories
      };

      const response = await apiRequest(`/categories/${categoryId}`, {
        method: 'PUT',
        body: JSON.stringify(payload)
      });

      if (response && response.data) {
        showToast('Category updated', 'success');
        await loadCategories();
        document.getElementById('category-list').innerHTML = renderCategoryList();
        editingCategoryId = null;
        editingSubcategories = [];
      }
    } catch (error) {
      showToast('Failed to update category', 'error');
    }
  };

  window.app.deleteCategory = async function(categoryId) {
    const state = getState();
    const cat = state.categories.find(c => c.id === categoryId);
    if (!cat) return;

    if (!confirm(`Are you sure you want to delete the category "${cat.name}"?`)) {
      return;
    }

    try {
      await apiRequest(`/categories/${categoryId}`, {
        method: 'DELETE'
      });

      showToast('Category deleted', 'success');
      await loadCategories();
      document.getElementById('category-list').innerHTML = renderCategoryList();
    } catch (error) {
      showToast('Failed to delete category', 'error');
    }
  };

  // ============================================
  // Category Init (called by app.js init)
  // ============================================

  // Set up category input handlers in the edit form.
  // Called from app.js init() after DOM is ready.
  function initCategoryHandlers() {
    const categoryInput = document.getElementById('edit-category');
    if (categoryInput) {
      let categoryDebounceTimer;
      categoryInput.addEventListener('input', function() {
        clearTimeout(categoryDebounceTimer);
        categoryDebounceTimer = setTimeout(() => {
          window.app.onCategoryChange(this.value);
        }, 150); // Short debounce for responsive feel
      });
      // Also trigger on blur to finalize indicator
      categoryInput.addEventListener('blur', function() {
        clearTimeout(categoryDebounceTimer);
        window.app.onCategoryChange(this.value);
      });
      // Enter key triggers addCategoryEntry for quick keyboard-driven workflow
      categoryInput.addEventListener('keypress', function(e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          window.app.addCategoryEntry();
        }
      });
    }
  }

  // ============================================
  // Expose Functions for app.js
  // ============================================

  // Prefixed with underscore to distinguish from user-facing window.app methods.
  // Called internally by app.js during init, save, edit, preview, etc.
  window.app._loadCategories = loadCategories;
  window.app._loadNoteCategoryMappings = loadNoteCategoryMappings;
  window.app._renderPreviewCategories = renderPreviewCategories;
  window.app._clearCategoryEntries = clearCategoryEntries;
  window.app._renderAllCategoryEntries = renderAllCategoryEntries;
  window.app._renderSubcategoryChips = renderSubcategoryChips;
  window.app._loadEditNoteCategories = loadEditNoteCategories;
  window.app._saveCategoryAssignments = saveCategoryAssignments;
  window.app._initCategoryHandlers = initCategoryHandlers;

})();