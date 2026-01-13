// GoNotes Authentication JavaScript
// Handles login and registration forms

(function() {
  'use strict';

  const API_BASE = '/api/v1';

  // Show error message
  function showError(message) {
    const errorEl = document.getElementById('error-message');
    if (errorEl) {
      errorEl.textContent = message;
      errorEl.classList.remove('hidden');
    }
  }

  // Hide error message
  function hideError() {
    const errorEl = document.getElementById('error-message');
    if (errorEl) {
      errorEl.classList.add('hidden');
    }
  }

  // Set loading state on submit button
  function setLoading(isLoading) {
    const btn = document.getElementById('submit-btn');
    if (btn) {
      btn.disabled = isLoading;
      btn.textContent = isLoading ? 'Please wait...' : btn.dataset.originalText || 'Submit';
    }
  }

  // Store the original button text
  document.addEventListener('DOMContentLoaded', function() {
    const btn = document.getElementById('submit-btn');
    if (btn) {
      btn.dataset.originalText = btn.textContent;
    }

    // Check if already logged in
    const token = localStorage.getItem('token');
    if (token && window.location.pathname !== '/') {
      // Verify token is still valid
      fetch(`${API_BASE}/auth/me`, {
        headers: { 'Authorization': `Bearer ${token}` }
      }).then(response => {
        if (response.ok) {
          window.location.href = '/';
        }
      }).catch(() => {
        // Token invalid, stay on auth page
        localStorage.removeItem('token');
      });
    }
  });

  // Handle login form submission
  window.handleLogin = async function(event) {
    event.preventDefault();
    hideError();
    setLoading(true);

    const username = document.getElementById('username').value.trim();
    const password = document.getElementById('password').value;

    if (!username || !password) {
      showError('Please fill in all fields');
      setLoading(false);
      return false;
    }

    try {
      const response = await fetch(`${API_BASE}/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password })
      });

      const data = await response.json();

      if (!response.ok) {
        showError(data.error || 'Login failed');
        setLoading(false);
        return false;
      }

      if (data.data && data.data.token) {
        localStorage.setItem('token', data.data.token);
        window.location.href = '/';
      } else {
        showError('Invalid response from server');
        setLoading(false);
      }
    } catch (error) {
      console.error('Login error:', error);
      showError('Network error. Please try again.');
      setLoading(false);
    }

    return false;
  };

  // Handle registration form submission
  window.handleRegister = async function(event) {
    event.preventDefault();
    hideError();
    setLoading(true);

    const username = document.getElementById('username').value.trim();
    const email = document.getElementById('email')?.value.trim() || null;
    const password = document.getElementById('password').value;
    const confirmPassword = document.getElementById('confirm-password').value;

    // Validation
    if (!username || !password) {
      showError('Please fill in all required fields');
      setLoading(false);
      return false;
    }

    if (username.length < 3 || username.length > 50) {
      showError('Username must be 3-50 characters');
      setLoading(false);
      return false;
    }

    if (!/^[a-zA-Z0-9_]+$/.test(username)) {
      showError('Username can only contain letters, numbers, and underscores');
      setLoading(false);
      return false;
    }

    if (password.length < 8) {
      showError('Password must be at least 8 characters');
      setLoading(false);
      return false;
    }

    if (password !== confirmPassword) {
      showError('Passwords do not match');
      setLoading(false);
      return false;
    }

    try {
      const response = await fetch(`${API_BASE}/auth/register`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, email, password })
      });

      const data = await response.json();

      if (!response.ok) {
        showError(data.error || 'Registration failed');
        setLoading(false);
        return false;
      }

      // Registration successful - redirect to login
      window.location.href = '/login?registered=true';
    } catch (error) {
      console.error('Registration error:', error);
      showError('Network error. Please try again.');
      setLoading(false);
    }

    return false;
  };

  // Show success message on login page if just registered
  document.addEventListener('DOMContentLoaded', function() {
    const params = new URLSearchParams(window.location.search);
    if (params.get('registered') === 'true') {
      const errorEl = document.getElementById('error-message');
      if (errorEl) {
        errorEl.textContent = 'Account created successfully! Please sign in.';
        errorEl.classList.remove('hidden');
        errorEl.style.background = 'var(--success-light)';
        errorEl.style.color = 'var(--success)';
      }
    }
  });

})();
