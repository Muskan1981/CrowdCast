import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { createEmitter } from '../api/ws';
import { useChat } from './useChat';
import { usePoll } from './usePoll';

// fakeSocket is the part of ReconnectingSocket the hooks use: on() and send().
function fakeSocket() {
  const emitter = createEmitter();
  const sent = [];
  return {
    on: emitter.on,
    emit: emitter.emit,
    sent,
    send(frame) { sent.push(frame); return true; },
  };
}

function jsonResponse(body, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('useChat', () => {
  it('loads history, appends live messages once, and sends over the socket', async () => {
    vi.stubGlobal('fetch', vi.fn(() => jsonResponse({
      messages: [{ id: 1, name: 'Aarav', text: 'history', time: 'now', color: 'mint' }],
      total_messages: 10,
    })));
    const socket = fakeSocket();
    const { result } = renderHook(() => useChat(socket, 'token', 'Me'));

    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    expect(result.current.total).toBe(10);

    act(() => {
      socket.emit('chat', { t: 'chat', message: { id: 2, name: 'Ishita', text: 'live', time: 'now', color: 'peach' } });
      socket.emit('chat', { t: 'chat', message: { id: 2, name: 'Ishita', text: 'live', time: 'now', color: 'peach' } });
    });
    expect(result.current.messages.map(m => m.id)).toEqual([1, 2]);
    expect(result.current.total).toBe(11);

    await act(async () => { await result.current.send('  hello  '); });
    expect(socket.sent).toEqual([{ t: 'chat', text: 'hello' }]);
  });

  it('falls back to REST when the socket is not open', async () => {
    const fetchMock = vi.fn((url, init) => {
      if (init?.method === 'POST') return jsonResponse({ id: 9, name: 'Me', text: 'rest', time: 'now', color: 'mint' }, 201);
      return jsonResponse({ messages: [], total_messages: 0 });
    });
    vi.stubGlobal('fetch', fetchMock);
    const closedSocket = { on: () => () => {}, send: () => false };
    const { result } = renderHook(() => useChat(closedSocket, 'token', 'Me'));

    await act(async () => { await result.current.send('rest message'); });
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST');
    expect(post).toBeDefined();
    expect(post[1].headers.Authorization).toBe('Bearer token');
    expect(result.current.messages.some(m => m.text === 'rest')).toBe(true);
  });
});

describe('usePoll', () => {
  const poll = { id: 'poll-01', question: 'Energy?', status: 'active', options: ['A', 'B', 'C'], votes: [5, 3, 2], percentages: [50, 30, 20], total_votes: 10 };

  it('shows counts from the API and remembers the vote locally', async () => {
    const fetchMock = vi.fn((url, init) => {
      if (init?.method === 'POST') {
        return jsonResponse({ poll_id: 'poll-01', selected_option: 1, votes: [5, 4, 2], percentages: [42, 33, 17], total_votes: 11 });
      }
      return jsonResponse(poll);
    });
    vi.stubGlobal('fetch', fetchMock);
    const socket = fakeSocket();
    const { result } = renderHook(() => usePoll(socket, 'token'));

    await waitFor(() => expect(result.current.options).toHaveLength(3));
    expect(result.current.total).toBe(10);
    expect(result.current.selected).toBeNull();

    await act(async () => { await result.current.vote(1); });
    expect(result.current.selected).toBe(1);
    expect(result.current.total).toBe(11);
    expect(window.localStorage.getItem('crowdcast.vote.poll-01')).toBe('1');
  });

  it('applies poll_update frames for the same poll only', async () => {
    vi.stubGlobal('fetch', vi.fn(() => jsonResponse(poll)));
    const socket = fakeSocket();
    const { result } = renderHook(() => usePoll(socket, 'token'));
    await waitFor(() => expect(result.current.options).toHaveLength(3));

    act(() => socket.emit('poll_update', { t: 'poll_update', poll_id: 'other', votes: [0, 0, 0], percentages: [0, 0, 0], total_votes: 0 }));
    expect(result.current.total).toBe(10);

    act(() => socket.emit('poll_update', { t: 'poll_update', poll_id: 'poll-01', votes: [9, 9, 9], percentages: [33, 33, 34], total_votes: 27 }));
    expect(result.current.total).toBe(27);
  });
});
