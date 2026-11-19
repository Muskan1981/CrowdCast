import { useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { EVENT_ID, USE_MOCKS } from '../config';
import { trafficData } from '../data/dashboardData';

/** useTrafficSeries returns 5-minute request buckets for the traffic chart, refreshed every 10 s. */
export function useTrafficSeries(range = '60m') {
  const [points, setPoints] = useState(USE_MOCKS ? trafficData : []);
  const [source, setSource] = useState(USE_MOCKS ? 'mock' : '');

  useEffect(() => {
    if (USE_MOCKS) return undefined;
    let cancelled = false;
    const load = async () => {
      try {
        const data = await apiFetch(`/events/${EVENT_ID}/stats/traffic?range=${range}`);
        if (!cancelled) setPoints(data);
      } catch {
        /* keep the last good series */
      }
    };
    load();
    const timer = setInterval(load, 10000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [range]);

  return { points, source };
}
