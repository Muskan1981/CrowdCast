import { useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { EVENT_ID, USE_MOCKS } from '../config';
import { mockOverview } from '../data/mockState';
import { useSocketEvent } from './useRealtime';

const REFRESH_MS = 10000;

/**
 * useOverviewStats loads /stats/overview, follows stats_tick for the four
 * metric cards, and refetches every 10 s to pick up distribution and activity.
 */
export function useOverviewStats(socket) {
  const [data, setData] = useState(USE_MOCKS ? mockOverview : null);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (USE_MOCKS) return undefined;
    let cancelled = false;
    const load = () =>
      apiFetch(`/events/${EVENT_ID}/stats/overview`)
        .then((d) => { if (!cancelled) { setData(d); setError(null); } })
        .catch((err) => { if (!cancelled) setError(err); });
    load();
    const timer = setInterval(load, REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  useSocketEvent(socket, 'stats_tick', (frame) => {
    if (!frame.metrics) return;
    setData((prev) => (prev ? { ...prev, metrics: frame.metrics, traffic_summary: { ...prev.traffic_summary, source: frame.source } } : prev));
  });

  return { data, error };
}
