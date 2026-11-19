import { useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { EVENT_ID, USE_MOCKS } from '../config';
import { useSocketEvent } from './useRealtime';

/**
 * useReactions keeps the running reaction total. Reactions are sent over the
 * socket; the server batches them every 250 ms and broadcasts reaction_batch.
 */
export function useReactions(socket, token, initial = 0) {
  const [total, setTotal] = useState(initial);

  useEffect(() => {
    if (USE_MOCKS) return;
    apiFetch(`/events/${EVENT_ID}`)
      .then((ev) => setTotal(ev.total_reactions || 0))
      .catch(() => {});
  }, []);

  useSocketEvent(socket, 'reaction_batch', (frame) => {
    if (typeof frame.total === 'number') setTotal(frame.total);
  });

  /** Sends one reaction. The total updates when the batch comes back. */
  const send = async (emoji) => {
    if (socket && socket.send({ t: 'reaction', emoji })) return true;
    if (USE_MOCKS) return true;
    const res = await apiFetch(`/events/${EVENT_ID}/reactions`, { method: 'POST', token, body: { emoji } });
    setTotal(res.reactions_sent);
    return true;
  };

  return { total, send };
}
