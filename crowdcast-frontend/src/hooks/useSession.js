import { useEffect, useState } from 'react';
import { apiFetch, readSession, writeSession, ApiError } from '../api/client';
import { USE_MOCKS } from '../config';

/**
 * useSession returns a viewer token. It reuses the stored session when there is
 * one, and creates a new one when the stored token is missing or rejected.
 */
export function useSession() {
  const [session, setSession] = useState(() => (USE_MOCKS ? { token: '', viewer: null } : readSession()));
  const [status, setStatus] = useState(USE_MOCKS || session ? 'ready' : 'loading');
  const [error, setError] = useState(null);

  useEffect(() => {
    if (USE_MOCKS || session) return undefined;
    let cancelled = false;
    apiFetch('/session', { method: 'POST', body: {} })
      .then((data) => {
        if (cancelled) return;
        const next = { token: data.token, viewer: data.viewer };
        writeSession(next);
        setSession(next);
        setStatus('ready');
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof ApiError ? err : new ApiError(0, 'SESSION', 'Could not start a session'));
        setStatus('error');
      });
    return () => {
      cancelled = true;
    };
    // session is only read once here; the empty dependency list is intentional
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** Drops the stored session so the next render creates a fresh one. */
  const reset = () => {
    writeSession(null);
    setSession(null);
    setStatus('loading');
  };

  return { token: session?.token || '', viewer: session?.viewer || null, status, error, reset };
}
