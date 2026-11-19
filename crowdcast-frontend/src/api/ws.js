// ReconnectingSocket keeps one WebSocket to an event alive. After a drop it
// waits with exponential backoff and full jitter, then reconnects. On every
// successful open it emits "open" so hooks can re-hydrate their state, and the
// server's "welcome" frame carries the snapshot.

export const BACKOFF_BASE_MS = 500;
export const BACKOFF_CAP_MS = 15000;

// backoffDelay returns a random delay in [0, min(cap, base * 2^attempt)).
// Full jitter spreads reconnects out so thousands of clients do not stampede.
export function backoffDelay(attempt, random = Math.random) {
  const ceiling = Math.min(BACKOFF_CAP_MS, BACKOFF_BASE_MS * 2 ** Math.min(attempt, 10));
  return Math.floor(random() * ceiling);
}

// createEmitter is a tiny event emitter: on/off/emit by name.
export function createEmitter() {
  const listeners = new Map();
  return {
    on(name, fn) {
      if (!listeners.has(name)) listeners.set(name, new Set());
      listeners.get(name).add(fn);
      return () => listeners.get(name)?.delete(fn);
    },
    emit(name, payload) {
      listeners.get(name)?.forEach((fn) => {
        try {
          fn(payload);
        } catch (err) {
          console.error(`listener for ${name} failed`, err);
        }
      });
    },
  };
}

export class ReconnectingSocket {
  /**
   * @param {object} opts
   * @param {() => string} opts.url      builds the ws URL on each attempt
   * @param {(ms:number)=>void} [opts.setTimer]   injectable for tests
   * @param {typeof WebSocket} [opts.WebSocketImpl]
   * @param {() => number} [opts.random]
   */
  constructor({ url, setTimer = setTimeout, clearTimer = clearTimeout, WebSocketImpl, random = Math.random }) {
    this.urlFn = url;
    this.setTimer = setTimer;
    this.clearTimer = clearTimer;
    this.WebSocketImpl = WebSocketImpl || (typeof WebSocket !== 'undefined' ? WebSocket : null);
    this.random = random;
    this.emitter = createEmitter();
    this.ws = null;
    this.attempt = 0;
    this.timer = null;
    this.closedByUser = false;
    this.state = 'idle'; // idle | connecting | open | reconnecting | closed
    this.hasOpened = false;
  }

  on(name, fn) {
    return this.emitter.on(name, fn);
  }

  setState(state) {
    if (this.state === state) return;
    this.state = state;
    this.emitter.emit('state', state);
  }

  connect() {
    this.closedByUser = false;
    this.open();
    return this;
  }

  open() {
    if (this.closedByUser) return;
    this.setState(this.hasOpened ? 'reconnecting' : 'connecting');
    let ws;
    try {
      ws = new this.WebSocketImpl(this.urlFn());
    } catch (err) {
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.attempt = 0;
      const wasDown = this.hasOpened;
      this.hasOpened = true;
      this.setState('open');
      this.emitter.emit('open', { reconnected: wasDown });
    };
    ws.onmessage = (event) => {
      let frame;
      try {
        frame = JSON.parse(event.data);
      } catch {
        return;
      }
      if (frame && typeof frame.t === 'string') {
        this.emitter.emit(frame.t, frame);
      }
      this.emitter.emit('message', frame);
    };
    ws.onerror = () => {
      /* the close event that follows handles recovery */
    };
    ws.onclose = (event) => {
      this.ws = null;
      this.emitter.emit('close', { code: event?.code, reason: event?.reason });
      if (this.closedByUser) {
        this.setState('closed');
        return;
      }
      this.scheduleReconnect();
    };
  }

  scheduleReconnect() {
    if (this.closedByUser) return;
    this.setState('reconnecting');
    const delay = backoffDelay(this.attempt, this.random);
    this.attempt += 1;
    this.timer = this.setTimer(() => {
      this.timer = null;
      this.open();
    }, delay);
  }

  /** Sends a JSON frame if the socket is open. Returns false otherwise. */
  send(frame) {
    if (!this.ws || this.ws.readyState !== 1) return false;
    this.ws.send(JSON.stringify(frame));
    return true;
  }

  close() {
    this.closedByUser = true;
    if (this.timer) this.clearTimer(this.timer);
    this.timer = null;
    if (this.ws) this.ws.close(1000, 'client closed');
    else this.setState('closed');
  }
}
