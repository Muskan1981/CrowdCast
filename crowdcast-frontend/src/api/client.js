import { API_BASE, ADMIN_KEY } from '../config';

// ApiError carries the HTTP status and the backend's error code, so callers
// can react to 401 (re-create the session) differently from 503 (node down).
export class ApiError extends Error {
  constructor(status, code, message) {
    super(message || `HTTP ${status}`);
    this.status = status;
    this.code = code || 'HTTP_ERROR';
  }
}

const SESSION_KEY = 'crowdcast.session';

// readSession and writeSession wrap localStorage. Private windows and blocked
// storage throw, so every access is guarded and the app still works without it.
export function readSession() {
  try {
    const raw = window.localStorage.getItem(SESSION_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

export function writeSession(session) {
  try {
    if (session) window.localStorage.setItem(SESSION_KEY, JSON.stringify(session));
    else window.localStorage.removeItem(SESSION_KEY);
  } catch {
    /* storage unavailable: keep the session in memory only */
  }
}

// apiFetch calls the REST API, adds the bearer token when given, and turns error
// envelopes ({error:{code,message}}) into ApiError. Admin calls add X-Admin-Key.
export async function apiFetch(path, { method = 'GET', body, token, admin = false, signal } = {}) {
  const headers = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = `Bearer ${token}`;
  if (admin) headers['X-Admin-Key'] = ADMIN_KEY;

  let response;
  try {
    response = await fetch(`${API_BASE}/api/v1${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal,
    });
  } catch (err) {
    if (err.name === 'AbortError') throw err;
    throw new ApiError(0, 'NETWORK', 'Cannot reach the server');
  }

  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!response.ok) {
    const envelope = data && typeof data === 'object' ? data.error : null;
    throw new ApiError(response.status, envelope?.code, envelope?.message);
  }
  return data;
}
