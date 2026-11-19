import { useEffect, useRef, useState } from 'react';
import { ReconnectingSocket } from '../api/ws';
import { API_BASE, USE_MOCKS } from '../config';

// buildWsUrl turns the API base (or the page origin, when empty) into the event WebSocket URL.
export function buildWsUrl(eventId, token) {
  const base = new URL(API_BASE || window.location.origin);
  base.protocol = base.protocol === 'https:' ? 'wss:' : 'ws:';
  base.pathname = '/ws';
  base.search = new URLSearchParams({ event: eventId, token }).toString();
  return base.toString();
}

/**
 * useRealtime owns the single WebSocket for this tab. It reconnects on its own;
 * the returned state drives the "Reconnecting..." and "Back online" toasts.
 */
export function useRealtime(eventId, token) {
  const [socket, setSocket] = useState(null);
  const [state, setState] = useState('idle');

  useEffect(() => {
    if (USE_MOCKS || !token) return undefined;
    const sock = new ReconnectingSocket({ url: () => buildWsUrl(eventId, token) }).connect();
    const offState = sock.on('state', setState);
    setSocket(sock);
    return () => {
      offState();
      sock.close();
      setSocket(null);
    };
  }, [eventId, token]);

  return { socket, state };
}

/** useSocketEvent calls handler for every frame of type `type`. The handler may change each render. */
export function useSocketEvent(socket, type, handler) {
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => {
    if (!socket) return undefined;
    return socket.on(type, (frame) => ref.current(frame));
  }, [socket, type]);
}
