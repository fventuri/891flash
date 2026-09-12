// sfl.js — decode a Yaesu FT-891 .SFL firmware file into a raw 0x60000 image.
//
// Port of decode_sfl.py. The web flasher writes the decoded .bin directly over
// serial, so only decoding is needed here (no SFL encoder).
//
// Algorithm (from YFSW023.exe): lines starting "VS" are encoded S0 records
// (drop "VS", prepend "S0"); every line after the first "VS" is decoded too.
// Stream cipher: plain = (encoded - table[ctr]) & 0xFF, ctr cycles 0..42 across
// all decoded characters. The payload is then parsed as Motorola S-records.

export const IMAGE_SIZE = 0x60000; // 384 KB address space, 0xFF = erased

// 43-byte decode table (VA 0x496c28 in YFSW023.exe).
const DECODE_TABLE = [
  0x1e, 0x27, 0x1b, 0x12, 0x11, 0x16, 0x19, 0x17,
  0x24, 0x25, 0x29, 0x1c, 0x0d, 0x0b, 0x06, 0x04,
  0x03, 0x01, 0x02, 0x05, 0x07, 0x0c, 0x0e, 0x0f,
  0x18, 0x1d, 0x20, 0x22, 0x2a, 0x2b, 0x08, 0x09,
  0x0a, 0x10, 0x13, 0x14, 0x15, 0x1a, 0x1f, 0x21,
  0x28, 0x23, 0x26,
];

/** Split a byte array on CRLF, returning subarray views (no copies). */
function splitCRLF(bytes) {
  const out = [];
  let start = 0;
  for (let i = 0; i + 1 < bytes.length; i++) {
    if (bytes[i] === 0x0d && bytes[i + 1] === 0x0a) {
      out.push(bytes.subarray(start, i));
      start = i + 2;
      i++;
    }
  }
  out.push(bytes.subarray(start));
  return out;
}

/**
 * Parse one decoded Motorola S-record string.
 * @returns {null | 'END' | {addr:number, data:Uint8Array}}
 */
function parseSrec(rec) {
  if (rec.length < 4 || rec[0] !== 'S') return null;
  const rtype = rec[1];
  const count = parseInt(rec.slice(2, 4), 16);
  if (rtype === '0') return null;          // header, skip
  if (rtype === '7' || rtype === '8' || rtype === '9') return 'END';

  let addr, dataLen, dataStart;
  if (rtype === '1') { addr = parseInt(rec.slice(4, 8), 16);  dataLen = count - 3; dataStart = 8; }
  else if (rtype === '2') { addr = parseInt(rec.slice(4, 10), 16); dataLen = count - 4; dataStart = 10; }
  else if (rtype === '3') { addr = parseInt(rec.slice(4, 12), 16); dataLen = count - 5; dataStart = 12; }
  else return null;

  if (dataLen < 0 || dataStart + dataLen * 2 > rec.length) return null;
  const data = new Uint8Array(dataLen);
  for (let i = 0; i < dataLen; i++) data[i] = parseInt(rec.slice(dataStart + i * 2, dataStart + i * 2 + 2), 16);
  return { addr, data };
}

/**
 * Decode an .SFL file.
 * @param {Uint8Array} fileBytes raw .SFL contents
 * @returns {{image:Uint8Array, records:number, bytesWritten:number, checksum:number, expected:(number|null)}}
 *   image: the 0x60000 firmware; checksum: sum of all image bytes;
 *   expected: the [CHECK SUM] value from the header (or null if absent).
 */
export function decodeSFL(fileBytes) {
  const lines = splitCRLF(fileBytes);
  const latin1 = new TextDecoder('latin1');

  // Expected checksum from the header comment block.
  let expected = null;
  for (const ln of lines) {
    const m = latin1.decode(ln).match(/CHECK SUM\]\s+([0-9A-Fa-f]+)\(/);
    if (m) { expected = parseInt(m[1], 16) >>> 0; break; }
  }

  let counter = 0;
  let vsSeen = false;
  const image = new Uint8Array(IMAGE_SIZE).fill(0xff);
  let records = 0;
  let bytesWritten = 0;

  for (const ln of lines) {
    if (ln.length === 0 || ln[0] === 0x3b /* ; */) continue;

    let prefix, encoded;
    if (ln.length >= 2 && ln[0] === 0x56 && ln[1] === 0x53 /* "VS" */) {
      vsSeen = true; prefix = 'S0'; encoded = ln.subarray(2);
    } else if (vsSeen) {
      prefix = ''; encoded = ln;
    } else {
      continue;
    }

    let tail = '';
    for (let i = 0; i < encoded.length; i++) {
      tail += String.fromCharCode((encoded[i] - DECODE_TABLE[counter]) & 0xff);
      counter = (counter + 1) % DECODE_TABLE.length;
    }

    const r = parseSrec(prefix + tail);
    if (r === null) continue;
    if (r === 'END') break;

    for (let i = 0; i < r.data.length; i++) {
      const pos = r.addr + i;
      if (pos >= 0 && pos < image.length) { image[pos] = r.data[i]; bytesWritten++; }
    }
    records++;
  }

  let sum = 0;
  for (let i = 0; i < image.length; i++) sum += image[i];
  return { image, records, bytesWritten, checksum: sum >>> 0, expected };
}
