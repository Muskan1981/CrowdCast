import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ReconnectingSocket, backoffDelay, BACKOFF_CAP_MS, createEmitter } from './ws';

// FakeWS mimics the parts of the browser WebSocket the client uses.
class FakeWS {
  static instances = [];
  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.sent = [];
    FakeWS.instances.push(this);
  }
  send(data) { this.sent.push(data); }
  close() { this.readyState = 3; this.onclose?.({ code: 1000, reason: 'bye' }); }
  // Test helpers
  open() { this.readyState = 1; this.onopen?.(); }
  receive(frame) { this.onmessage?.({ data: JSON.stringify(frame) }); }
  drop(code = 1012) { this.readyState = 3; this.onclose?.({ code, reason: 'service restarting' }); }
}

// fakeTimers records scheduled callbacks so tests run the backoff without waiting.
function fakeTimers() {
  const queue = [];
  return {
    setTimer: (fn, ms) => { const t = { fn, ms, cleared: false }; queue.push(t); return t; },
    clearTimer: (t) => { if (t) t.cleared = true; },
    queue,
    runNext() {
      const next = queue.find((t) => !t.cleared && !t.ran);
      if (!next) return null;
      next.ran = true;
      next.fn();
      return next;
    },
  };
}

beforeEach(() => {
  FakeWS.instances = [];
});

describe('backoffDelay', () => {
  it('stays inside the exponential ceiling and the cap', () => {
    expect(backoffDelay(0, () => 0.999)).toBeLessThan(500);
    expect(backoffDelay(2, () => 0.999)).toBeLessThan(2000);
    expect(backoffDelay(50, () => 0.999)).toBeLessThan(BACKOFF_CAP_MS);
  });

  it('uses full jitter: a random value of 0 gives no delay', () => {
    expect(backoffDelay(5, () => 0)).toBe(0);
  });
});

describe('ReconnectingSocket', () => {
  it('reconnects after a drop and reports the reconnect on open', () => {
    const timers = fakeTimers();
    const states = [];
    const socket = new ReconnectingSocket({
      url: () => 'ws://test/ws?event=cc-0826&token=t',
      WebSocketImpl: FakeWS,
      setTimer: timers.setTimer,
      clearTimer: timers.clearTimer,
      random: () => 0.5,
    });
    socket.on('state', (s) => states.push(s));
    const opens = [];
    socket.on('open', (o) => opens.push(o));

    socket.connect();
    FakeWS.instances[0].open();
    expect(opens).toEqual([{ reconnected: false }]);

    FakeWS.instances[0].drop(1012);
    expect(states).toContain('reconnecting');
    expect(timers.queue).toHaveLength(1);

    timers.runNext();
    expect(FakeWS.instances).toHaveLength(2);
    FakeWS.instances[1].open();
    expect(opens[1]).toEqual({ reconnected: true });
    expect(states[states.length - 1]).toBe('open');
  });

  it('routes frames by their "t" type and also emits "message"', () => {
    const socket = new ReconnectingSocket({ url: () => 'ws://x', WebSocketImpl: FakeWS, setTimer: () => 0, clearTimer: () => {} });
    const chat = vi.fn();
    const any = vi.fn();
    socket.on('chat', chat);
    socket.on('message', any);
    socket.connect();
    FakeWS.instances[0].open();
    FakeWS.instances[0].receive({ t: 'chat', message: { id: 1, text: 'hi' } });
    expect(chat).toHaveBeenCalledWith(expect.objectContaining({ t: 'chat' }));
    expect(any).toHaveBeenCalledTimes(1);
  });

  it('does not reconnect after close() is called by the user', () => {
    const timers = fakeTimers();
    const socket = new ReconnectingSocket({ url: () => 'ws://x', WebSocketImpl: FakeWS, setTimer: timers.setTimer, clearTimer: timers.clearTimer });
    socket.connect();
    FakeWS.instances[0].open();
    socket.close();
    expect(socket.state).toBe('closed');
    expect(timers.queue).toHaveLength(0);
  });

  it('send() refuses frames while the socket is not open', () => {
    const socket = new ReconnectingSocket({ url: () => 'ws://x', WebSocketImpl: FakeWS, setTimer: () => 0, clearTimer: () => {} });
    socket.connect();
    expect(socket.send({ t: 'ping' })).toBe(false);
    FakeWS.instances[0].open();
    expect(socket.send({ t: 'ping' })).toBe(true);
    expect(JSON.parse(FakeWS.instances[0].sent[0])).toEqual({ t: 'ping' });
  });

  it('backs off longer after repeated failures', () => {
    const timers = fakeTimers();
    const socket = new ReconnectingSocket({ url: () => 'ws://x', WebSocketImpl: FakeWS, setTimer: timers.setTimer, clearTimer: timers.clearTimer, random: () => 0.999 });
    socket.connect();
    for (let i = 0; i < 4; i += 1) {
      FakeWS.instances[FakeWS.instances.length - 1].drop();
      timers.runNext();
    }
    const delays = timers.queue.map((t) => t.ms);
    expect(delays[3]).toBeGreaterThan(delays[0]);
  });
});

describe('createEmitter', () => {
  it('unsubscribes with the returned function', () => {
    const e = createEmitter();
    const fn = vi.fn();
    const off = e.on('x', fn);
    e.emit('x', 1);
    off();
    e.emit('x', 2);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
