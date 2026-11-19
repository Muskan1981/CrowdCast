import { useCallback, useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { USE_MOCKS, EVENT_ID } from '../config';
import { useSocketEvent } from './useRealtime';

const LIFECYCLE = { pause: 'pause', resume: 'resume', end: 'end', start: 'start' };

/**
 * useEventState loads the event (title, host, date, status, live timer) and
 * follows event_state frames. Lifecycle actions use the admin endpoints.
 */
export function useEventState(socket) {
  const [event, setEvent] = useState(null);
  const [error, setError] = useState(null);

  const refresh = useCallback(async () => {
    if (USE_MOCKS) return;
    try {
      setEvent(await apiFetch(`/events/${EVENT_ID}`));
      setError(null);
    } catch (err) {
      setError(err);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useSocketEvent(socket, 'open', refresh);
  useSocketEvent(socket, 'event_state', (frame) => {
    setEvent((prev) => (prev ? { ...prev, status: frame.status, live_for_formatted: frame.live_for_formatted, live_for_seconds: frame.live_for_seconds } : prev));
  });
  useSocketEvent(socket, 'welcome', (frame) => {
    if (frame.event_state) {
      setEvent((prev) => (prev ? { ...prev, status: frame.event_state.status, live_for_formatted: frame.event_state.live_for, live_for_seconds: frame.event_state.live_for_seconds } : prev));
    }
  });

  /** action is one of pause | resume | end | start. The admin key is attached by apiFetch. */
  const lifecycle = async (action) => {
    const data = await apiFetch(`/events/${EVENT_ID}/${LIFECYCLE[action]}`, { method: 'POST', admin: true, body: {} });
    setEvent((prev) => (prev ? { ...prev, status: data.status } : prev));
    return data;
  };

  return { event, error, refresh, lifecycle, eventLive: event ? event.status === 'live' : true };
}
