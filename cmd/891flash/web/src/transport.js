// transport.js — Web Serial transport for the FT-891 flasher.
//
// Wraps a SerialPort with a background read pump so readExactly() can time out
// without abandoning a pending read() (validated in the phase-1 smoke test).
// The flasher talks only to this interface, so a WebUSB CP210x backend could be
// dropped in later with the same shape: open / send / readExactly / clearInput
// / reopen / close.
//
// All FT-891 targets use 8-N-2 (see ft891flash: the extra stop bit paces the
// MAIN->panel SSI relay; MAIN tolerates 8-N-1 but we match the official tool).

import { sleep } from './util.js';

const LINE = { dataBits: 8, stopBits: 2, parity: 'none', flowControl: 'none' };

export class WebSerialTransport {
  /** @param {SerialPort} port a port from navigator.serial.requestPort() */
  constructor(port) {
    this.port = port;
    this._reader = null;
    this._writer = null;
    this._pumping = false;
    this.buf = new Uint8Array(0);
    this._waiter = null;
  }

  async open(baud) {
    await this.port.open({ baudRate: baud, ...LINE });
    this._start();
  }

  _start() {
    this.buf = new Uint8Array(0);
    this._waiter = null;
    this._writer = this.port.writable.getWriter();
    this._reader = this.port.readable.getReader();
    this._pumping = true;
    this._pump();
  }

  async _pump() {
    try {
      while (this._pumping) {
        const { value, done } = await this._reader.read();
        if (done) break;
        if (value && value.length) {
          const m = new Uint8Array(this.buf.length + value.length);
          m.set(this.buf); m.set(value, this.buf.length); this.buf = m;
          if (this._waiter) { const w = this._waiter; this._waiter = null; w(); }
        }
      }
    } catch (_) { /* reader cancelled on teardown */ }
  }

  async send(bytes) {
    await this._writer.write(bytes instanceof Uint8Array ? bytes : Uint8Array.from(bytes));
  }

  clearInput() { this.buf = new Uint8Array(0); }

  /** Read exactly n bytes within timeoutMs; Uint8Array, or null on timeout. */
  async readExactly(n, timeoutMs) {
    const deadline = performance.now() + timeoutMs;
    while (this.buf.length < n) {
      const rem = deadline - performance.now();
      if (rem <= 0) break;
      await new Promise((res) => {
        this._waiter = res;
        setTimeout(() => { if (this._waiter === res) { this._waiter = null; res(); } }, rem);
      });
    }
    if (this.buf.length < n) return null;
    const out = this.buf.slice(0, n);
    this.buf = this.buf.slice(n);
    return out;
  }

  /** Read up to max bytes with a short timeout (diagnostics only). */
  async drainTail(max, timeoutMs) {
    const r = await this.readExactly(max, timeoutMs);
    if (r) return r;
    const out = this.buf.slice(0, max);
    this.buf = this.buf.slice(out.length);
    return out;
  }

  async _teardown() {
    this._pumping = false;
    try { if (this._writer) this._writer.releaseLock(); } catch (_) {}
    try { await this._reader.cancel(); } catch (_) {}
    try { this._reader.releaseLock(); } catch (_) {}
    this._writer = null;
    this._reader = null;
  }

  /** Close and reopen the same port at a new baud (the step-6 baud switch). */
  async reopen(baud) {
    await this._teardown();
    await this.port.close();
    await sleep(50);
    await this.port.open({ baudRate: baud, ...LINE });
    this._start();
  }

  async close() {
    await this._teardown();
    try { await this.port.close(); } catch (_) {}
  }
}
