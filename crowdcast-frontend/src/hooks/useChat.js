import { useCallback, useEffect, useRef, useState } from 'react';
import { apiFetch } from '../api/client';
import { EVENT_ID, USE_MOCKS } from '../config';
import { mockChat } from '../data/mockState';
import { useSocketEvent } from './useRealtime';

const MAX_MESSAGES = 200;

// normalise maps a chat message from the API or the socket to the shape the chat panel renders.
function normalise(msg) {
  return {
    id: msg.id,
    name: msg.name,
    text: msg.text,
    time: msg.time || 'now',
    color: msg.color || 'mint',
  };
}

/**
 * useChat loads history, appends live messages from the socket, and sends
 * messages over the socket. It falls back to REST when the socket is down.
 */
export function useChat(socket, token, viewerName) {
  const [messages, setMessages] = useState(USE_MOCKS ? mockChat : []);
  const [total, setTotal] = useState(USE_MOCKS ? 3842 : 0);
  const seen = useRef(new Set());

  // add appends only messages not seen before (by id) and returns how many were new.
  const add = useCallback((list) => {
    const fresh = list.filter(m => !seen.current.has(m.id));
    fresh.forEach(m => seen.current.add(m.id));
    if (fresh.length) setMessages(prev => [...prev, ...fresh].slice(-MAX_MESSAGES));
    return fresh.length;
  }, []);

  useEffect(() => {
    if (USE_MOCKS) return;
    apiFetch(`/events/${EVENT_ID}/chat?limit=50`)
      .then((data) => {
        add((data.messages || []).map(normalise));
        setTotal(data.total_messages || 0);
      })
      .catch(() => {
        /* the chat panel stays empty until the socket delivers messages */
      });
  }, [add]);

  useSocketEvent(socket, 'welcome', (frame) => {
    if (frame.recent_chat) add(frame.recent_chat.map(normalise));
  });
  useSocketEvent(socket, 'chat', (frame) => {
    if (!frame.message) return;
    if (add([normalise(frame.message)]) > 0) setTotal((n) => n + 1);
  });

  /** Sends a chat message. Returns false when the text is empty. */
  const send = async (text) => {
    const clean = text.trim();
    if (!clean) return false;
    if (socket && socket.send({ t: 'chat', text: clean })) return true;
    // Socket is not open: send over REST. The server persists it and broadcasts it to viewers.
    const created = await apiFetch(`/events/${EVENT_ID}/chat`, { method: 'POST', token, body: { text: clean } });
    if (add([normalise({ ...created, name: created.name || viewerName })]) > 0) setTotal((n) => n + 1);
    return true;
  };

  return { messages, total, send };
}
