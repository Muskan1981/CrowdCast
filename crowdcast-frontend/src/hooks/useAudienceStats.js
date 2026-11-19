import { useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { EVENT_ID, USE_MOCKS } from '../config';
import { mockAudience } from '../data/mockState';

/** useAudienceStats loads the audience page data and refreshes it every 10 s. */
export function useAudienceStats() {
  const [data, setData] = useState(USE_MOCKS ? mockAudience : null);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (USE_MOCKS) return undefined;
    let cancelled = false;
    const load = () =>
      apiFetch(`/events/${EVENT_ID}/stats/audience`)
        .then((d) => { if (!cancelled) { setData(d); setError(null); } })
        .catch((err) => { if (!cancelled) setError(err); });
    load();
    const timer = setInterval(load, 10000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  return { data, error };
}
