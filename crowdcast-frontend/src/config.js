// Runtime configuration, read from Vite env vars at build time.
//
// VITE_API_URL     base URL of the API. Empty means same origin (production behind nginx).
// VITE_USE_MOCKS   "true" renders the bundled demo data and makes no network calls.
// VITE_ADMIN_KEY   admin key for the demo's start/pause/chaos buttons. Demo only: anything
//                  in a frontend bundle is public. Never ship a real key this way.
export const API_BASE = (import.meta.env.VITE_API_URL || '').replace(/\/$/, '');
export const USE_MOCKS = import.meta.env.VITE_USE_MOCKS === 'true';
export const ADMIN_KEY = import.meta.env.VITE_ADMIN_KEY || '';
export const EVENT_ID = import.meta.env.VITE_EVENT_ID || 'cc-0826';
export const NODE_NAMES = ['crowdcast-api-01', 'crowdcast-api-02', 'crowdcast-api-03'];
