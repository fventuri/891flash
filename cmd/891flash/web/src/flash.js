// flash.js — FT-891 firmware writer state machine (MAIN / PANEL / DSP).
//
// Port of ft891flash (Go), which was reverse-engineered from YFSW023.exe. Drives
// a Transport (see transport.js). No EINTR concerns here — that was a native
// signal issue; the browser has none. Pacing comes from ACK-gating, as before.
//
// UI hooks (all optional): ui.log(msg, level) with level 'ok'|'err'|'dim';
// ui.progress(fraction 0..1, detail).

import { sleep, hex, hexSpace } from './util.js';

export const FIRMWARE_SIZE = 0x60000; // 384 KB (MAIN and PANEL)
const BLOCK_SIZE = 128;
const XFER_CMD = 0x50;
const XFER_ACK = 0x06;

const PROGRAM_BAUD = { main: 57600, panel: 19200 };
const DSP_BAUD = 38400, DSP_SYNC = 0x07, DSP_BLOCK = 1024, DSP_ACK = 0x06;

const MODEL_ID = [0x34, 0x43, 0x30, 0x33]; // "4C03"
const PKT_ECHO_MODEL = [0x10, 0x04, 0x34, 0x43, 0x30, 0x33, 0x12];
const PKT_SEGMENT_SEL = [0x11, 0x01, 0x00, 0xee];
const PKT_CONFIG = {
  main:  [0x3f, 0x07, 0x02, 0x40, 0x04, 0x56, 0x02, 0x04, 0x02, 0x16],
  panel: [0x3f, 0x07, 0x00, 0xc0, 0x04, 0x56, 0x02, 0x04, 0x02, 0x98],
};

const ENTRY = {
  main:  'rear PGM slide switch LEFT, supply ON (blank display, do NOT press POWER)',
  panel: 'supply ON while holding [PWR/LOCK]+[BAND]+[FAST] (TX/BUSY LED orange)',
  dsp:   'PGM switch RIGHT; hold [PWR/LOCK]+[F]+[C] at power-on ("DSP WRITE MODE")',
};

/** Two's-complement sum-to-zero checksum: (-sum) & 0xFF. */
function checksum(body) {
  let sum = 0;
  for (const b of body) sum += b;
  return (-sum) & 0xff;
}

function makeUI(ui = {}) {
  return {
    log: ui.log || (() => {}),
    progress: ui.progress || (() => {}),
  };
}

async function expectAck(t, ctx, timeoutMs = 500) {
  const r = await t.readExactly(1, timeoutMs);
  if (!r) throw new Error(`${ctx}: timeout waiting for ACK`);
  if (r[0] !== 0x06) throw new Error(`${ctx}: expected 0x06, got 0x${hex(r[0])}`);
}

// ---- MAIN / PANEL steps ----------------------------------------------------

async function step0Sync(t, target, ui) {
  ui.log('Step 0: sync at 19200 baud', 'dim');
  t.clearInput();
  let got = false;
  for (let i = 0; i < 20; i++) {
    await t.send([0x00]);
    const r = await t.readExactly(1, 20);
    if (r) { t.clearInput(); got = true; break; }
  }
  if (!got)
    throw new Error(`no response at 19200 — radio not in a fresh programming state. ` +
      `Power-cycle: supply OFF ~10 s, then ${ENTRY[target]}, then retry`);
  await sleep(100);
  await t.send([0x55]);
  const r = await t.readExactly(1, 1000);
  const got2 = r ? `0x${hex(r[0])}` : 'timeout';
  if (!r || r[0] !== 0xe6)
    throw new Error(`sync failed: expected 0xE6, got ${got2}. Power-cycle and retry`);
  ui.log('  sync OK (0xE6)', 'ok');
}

async function step1ModelId(t, ui) {
  await sleep(10); await t.send([0x20]); await sleep(2);
  const r = await t.readExactly(18, 500);
  if (!r) throw new Error('model ID query: timeout');
  if (r[0] !== 0x30) throw new Error('unexpected inquiry response (no 0x30): ' + hexSpace(r));
  const n = r[3];
  const code = r.slice(4, 4 + n);
  for (let i = 0; i < 4; i++)
    if (code[i] !== MODEL_ID[i])
      throw new Error('model ID mismatch: ' + hexSpace(code.slice(0, 4)) + ' (want 34 43 30 33)');
  ui.log(`  device: ${String.fromCharCode(...code)}  (FT-891 '4C03' confirmed)`, 'ok');
}

async function step2Readiness(t, ui) {
  await sleep(10); await t.send([0x27]); await sleep(2);
  const r = await t.readExactly(5, 500);
  if (!r) throw new Error('readiness query: timeout');
  if (r[2] !== 0x00 || r[3] !== 0x80) throw new Error('readiness check failed: ' + hexSpace(r));
  ui.log('  ready', 'ok');
}

async function sendPacketAck(t, packet, ctx, ui) {
  await sleep(10); await t.send(packet); await sleep(2);
  await expectAck(t, ctx);
  ui.log(`  ${ctx} ACK`, 'ok');
}

async function step6BaudSwitch(t, target, ui) {
  const baud = PROGRAM_BAUD[target];
  ui.log(`Step 6: switch to ${baud} baud`, 'dim');
  await t.reopen(baud);
  await sleep(50);
  await t.send([0x06]); await sleep(2);
  await expectAck(t, 'baud-rate echo');
  ui.log(`  now at ${baud} baud`, 'ok');
}

async function step7Erase(t, ui) {
  ui.log('Step 7: erase flash (up to 5 s) ...', 'dim');
  await t.send([0x40]); await sleep(2);
  await expectAck(t, 'erase trigger', 5000);
  ui.log('  erase OK', 'ok');
}

async function pollStatus(t, label, ui) {
  await sleep(10); await t.send([0x4f]); await sleep(2);
  const r = await t.readExactly(5, 500);
  if (!r) throw new Error(`status poll ${label}: timeout`);
  if (r[3] !== 0x00) throw new Error(`status poll ${label}: error code 0x${hex(r[3])}`);
  ui.log(`  status ${label} OK (${hexSpace(r)})`, 'ok');
}

async function redirectable(t, cmd, redirect, ctx, ui) {
  await sleep(10); await t.send([cmd]); await sleep(2);
  const r = await t.readExactly(1, 500);
  if (!r) throw new Error(`${ctx}: timeout`);
  if (r[0] === redirect) {
    const sub = await t.readExactly(1, 100);
    throw new Error(`${ctx}: redirect 0x${hex(redirect)} n=0x${sub ? hex(sub[0]) : '??'}`);
  }
  if (r[0] !== 0x06) throw new Error(`${ctx}: unexpected 0x${hex(r[0])}`);
  ui.log(`  ${ctx} ACK`, 'ok');
}

async function transfer(t, firmware, ui) {
  const total = Math.floor(firmware.length / BLOCK_SIZE);
  ui.log(`Step 12: data transfer — ${total} × ${BLOCK_SIZE} B blocks`, 'dim');
  for (let i = 0; i < total; i++) {
    const addr = i * BLOCK_SIZE;
    const body = new Uint8Array(5 + BLOCK_SIZE);
    body[0] = XFER_CMD;
    body[1] = (addr >>> 24) & 0xff; body[2] = (addr >>> 16) & 0xff;
    body[3] = (addr >>> 8) & 0xff;  body[4] = addr & 0xff;
    body.set(firmware.subarray(addr, addr + BLOCK_SIZE), 5);
    const pkt = new Uint8Array(body.length + 1);
    pkt.set(body); pkt[pkt.length - 1] = checksum(body);
    await t.send(pkt);

    const r = await t.readExactly(1, 10000);
    if (!r || r[0] !== XFER_ACK) {
      const got = r ? `0x${hex(r[0])}` : 'timeout';
      const tail = await t.drainTail(16, 200);
      const hint = tail.length ? `; late bytes: ${hexSpace(tail)}` : '; nothing further';
      throw new Error(`block ${i + 1}/${total} (addr 0x${addr.toString(16).toUpperCase().padStart(5, '0')}): no ACK (${got})${hint}`);
    }
    if ((i + 1) % 32 === 0 || i === total - 1)
      ui.progress((i + 1) / total, `addr 0x${(addr + BLOCK_SIZE).toString(16).toUpperCase().padStart(5, '0')}`);
  }

  // End-of-transfer marker: address 0xFFFFFFFF, no data.
  const end = [XFER_CMD, 0xff, 0xff, 0xff, 0xff];
  await t.send([...end, checksum(end)]);
  const r = await t.readExactly(1, 10000);
  if (!r || r[0] !== XFER_ACK)
    ui.log(`  warning: end-of-transfer ack was ${r ? '0x' + hex(r[0]) : 'timeout'} (completion poll will confirm)`, 'dim');
}

async function completion(t, ui) {
  await sleep(10); await t.send([0x4b]); await sleep(2);
  const r = await t.readExactly(7, 500);
  if (!r) throw new Error('completion poll: timeout');
  const addr = ((r[2] << 24) | (r[3] << 16) | (r[4] << 8) | r[5]) >>> 0;
  ui.log(`  radio reports write address: 0x${addr.toString(16).toUpperCase().padStart(8, '0')}`, 'ok');
}

/**
 * Flash MAIN or PANEL firmware.
 * @param {import('./transport.js').WebSerialTransport} t
 * @param {{target:'main'|'panel', firmware?:Uint8Array, dryRun?:boolean, ui?:object}} opts
 */
export async function flashFirmware(t, opts) {
  const { target, firmware, dryRun = false } = opts;
  const ui = makeUI(opts.ui);
  if (!dryRun) {
    if (!firmware) throw new Error('firmware is required unless dryRun');
    if (firmware.length !== FIRMWARE_SIZE)
      throw new Error(`expected ${FIRMWARE_SIZE} bytes (${FIRMWARE_SIZE / 1024} KB), got ${firmware.length}`);
  }
  ui.log(`FT-891 ${target.toUpperCase()} firmware writer${dryRun ? '  (DRY RUN: read-only handshake)' : ''}`);

  await t.open(19200);
  await step0Sync(t, target, ui);
  await step1ModelId(t, ui);
  await step2Readiness(t, ui);

  if (dryRun) {
    ui.log('DRY RUN OK — reached programming mode (steps 0–2). No erase/write sent.', 'ok');
    return;
  }

  await sendPacketAck(t, PKT_ECHO_MODEL, 'Step 3: echo model', ui);
  await sendPacketAck(t, PKT_SEGMENT_SEL, 'Step 4: segment select', ui);
  await sendPacketAck(t, PKT_CONFIG[target], 'Step 5: config', ui);
  await step6BaudSwitch(t, target, ui);
  await step7Erase(t, ui);
  await pollStatus(t, '#1', ui);
  await pollStatus(t, '#2', ui);
  await redirectable(t, 0x4c, 0xcc, 'Step 9: sector write', ui);
  await redirectable(t, 0x4d, 0xcd, 'Step 10: page finalise', ui);
  await pollStatus(t, '#3', ui);
  await sendPacketAck(t, [0x43], 'Step 11: write confirm', ui);
  await transfer(t, firmware, ui);
  await completion(t, ui);

  ui.log('Firmware written successfully.', 'ok');
  ui.log('Power OFF' + (target === 'main' ? ', slide the rear PGM switch back RIGHT,' : ',') +
    ' disconnect USB, then power-cycle normally.', 'dim');
}

// ---- DSP path --------------------------------------------------------------

async function dspSync(t, ui) {
  ui.log(`Step 0: DSP sync at ${DSP_BAUD} baud`, 'dim');
  await t.open(DSP_BAUD);
  t.clearInput();
  let got = 'timeout';
  for (let attempt = 0; attempt < 3; attempt++) {
    await t.send([DSP_SYNC]);
    const r = await t.readExactly(1, 3000);
    if (r && r[0] === DSP_ACK) { ui.log('  DSP sync OK (0x06)', 'ok'); return; }
    got = r ? `0x${hex(r[0])}` : 'timeout';
    if (attempt < 2) { ui.log(`  retry (${got}) ...`, 'dim'); t.clearInput(); }
  }
  throw new Error(`DSP sync failed: sent 0x07, expected 0x06, got ${got}. Enter DSP WRITE MODE and retry`);
}

async function dspTransfer(t, data, ui) {
  let padded = data;
  const pad = ((-data.length) % DSP_BLOCK + DSP_BLOCK) % DSP_BLOCK;
  if (pad) { padded = new Uint8Array(data.length + pad); padded.set(data); }
  const total = padded.length / DSP_BLOCK;
  ui.log(`Step 1: DSP data transfer — ${total} × ${DSP_BLOCK} B blocks`, 'dim');
  for (let i = 0; i < total; i++) {
    await t.send(padded.subarray(i * DSP_BLOCK, (i + 1) * DSP_BLOCK));
    const r = await t.readExactly(1, 3000);
    if (!r || r[0] !== DSP_ACK) {
      const got = r ? `0x${hex(r[0])}` : 'timeout';
      const tail = await t.drainTail(16, 200);
      const hint = tail.length ? `; late bytes: ${hexSpace(tail)}` : '; nothing further';
      throw new Error(`block ${i + 1}/${total}: no ACK (${got})${hint}`);
    }
    if ((i + 1) % 8 === 0 || i === total - 1) ui.progress((i + 1) / total, `block ${i + 1}/${total}`);
  }
}

/**
 * Flash DSP firmware (raw .dat).
 * @param {import('./transport.js').WebSerialTransport} t
 * @param {{firmware?:Uint8Array, dryRun?:boolean, ui?:object}} opts
 */
export async function flashDsp(t, opts) {
  const { firmware, dryRun = false } = opts;
  const ui = makeUI(opts.ui);
  if (!dryRun && !firmware) throw new Error('DSP firmware is required unless dryRun');
  ui.log(`FT-891 DSP firmware writer${dryRun ? '  (DRY RUN: sync only)' : ''}`);

  await dspSync(t, ui);
  if (dryRun) {
    ui.log('DRY RUN OK — radio answered the DSP sync (0x07 → 0x06). No data written.', 'ok');
    return;
  }
  await dspTransfer(t, firmware, ui);
  ui.log('DSP firmware written successfully.', 'ok');
  ui.log('Power OFF, disconnect USB, power-cycle, confirm via Menu 18-02 [DSP VERSION].', 'dim');
}
