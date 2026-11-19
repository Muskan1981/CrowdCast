import { useState } from 'react';
import { useSocketEvent } from './useRealtime';

/** usePresence tracks the live attendee count from the presence frames sent every 1.5 s. */
export function usePresence(socket, initial = 0) {
  const [presence, setPresence] = useState({ attendees: initial, delta: '', bars: [] });

  useSocketEvent(socket, 'presence', (frame) => {
    setPresence({ attendees: frame.attendees, delta: frame.delta_10m, bars: frame.bars || [] });
  });
  useSocketEvent(socket, 'welcome', (frame) => {
    if (frame.presence) {
      setPresence({ attendees: frame.presence.attendees, delta: frame.presence.delta_10m, bars: frame.presence.bars || [] });
    }
  });

  return presence;
}
