// patch.js — apply a JSON firmware patch to a raw 0x60000 image, with SHA-256
// verification. Port of the apply path in ft891patch.
//
// Patch schema (cave/ft891_voltmeter.patch.json):
//   { name, version, note,
//     targets: { main: Target, panel: Target, ... } }
//   Target = { base_name, base_size, base_sha256, result_sha256,
//              regions: [ { offset: "0x1c883", hex: "09...", expect?: "aa..." }, ... ] }
// A region carries only the mod author's replacement bytes — no Yaesu code.
//
// Stacking: an optional per-region `expect` field holds the ORIGINAL bytes the
// region overwrites. It lets several independent patches be chained onto one
// image (see applyPatches): each region confirms it's landing on the bytes it
// expects — without pinning the whole-image base hash, which changes once an
// earlier patch has run. Regions from different patches that overlap are a
// conflict and throw. base_sha256/result_sha256 stay as the single-patch check.

/** SHA-256 of a byte array as lowercase hex (Web Crypto; needs a secure context). */
export async function sha256hex(bytes) {
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

/** Parse a hex string ("09f14a") into a Uint8Array. */
export function hexToBytes(hex) {
  const s = hex.trim();
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/**
 * Pick the patch target whose base_sha256 matches the image hash.
 * @returns {{name:string, target:object} | null}
 */
export function selectTarget(patch, baseHash, override) {
  const targets = patch.targets || {};
  if (override) {
    const t = targets[override];
    if (!t) throw new Error(`no target "${override}" in patch (have ${Object.keys(targets).join(', ')})`);
    if (t.base_sha256 && t.base_sha256 !== baseHash)
      throw new Error(`input does not match target "${override}"\n  expected base ${t.base_sha256}\n  got          ${baseHash}`);
    return { name: override, target: t };
  }
  for (const [name, t] of Object.entries(targets))
    if (t.base_sha256 === baseHash) return { name, target: t };
  return null;
}

/**
 * Overwrite `image` in place with each region's bytes.
 * @param {Uint8Array} image
 * @param {Array} regions
 * @param {object} [opts]
 * @param {Array<[number,number]>} [opts.written]  running list of [start,end) byte
 *   ranges already written this session; a new region overlapping one throws
 *   (two patches conflict). Appended in place so callers can chain patches.
 * @param {boolean} [opts.requireExpect]  throw if a region lacks `expect` — used
 *   when stacking onto an already-modified image, where the whole-image base
 *   hash can no longer vouch that the region lands on the right bytes.
 * @param {string} [opts.label]  patch name, for clearer error messages.
 * @returns {Array<[number,number]>} the ranges this call wrote.
 */
export function applyRegions(image, regions, opts = {}) {
  const written = opts.written || [];
  const pre = opts.label ? `${opts.label}: ` : '';
  const mine = [];
  for (const r of regions) {
    const off = parseInt(String(r.offset).replace(/^0x/i, ''), 16); // offsets are hex
    const data = hexToBytes(r.hex);
    const end = off + data.length;
    if (!(off >= 0) || end > image.length)
      throw new Error(`${pre}region at ${r.offset} (${data.length} bytes) is out of range`);
    if (r.expect != null) {
      const exp = hexToBytes(r.expect);
      if (exp.length !== data.length)
        throw new Error(`${pre}region at ${r.offset}: expect is ${exp.length} bytes but hex is ${data.length}`);
      for (let i = 0; i < exp.length; i++)
        if (image[off + i] !== exp[i])
          throw new Error(
            `${pre}region at ${r.offset} byte +${i}: image has 0x${image[off + i].toString(16).padStart(2, '0')}, ` +
            `patch expected 0x${exp[i].toString(16).padStart(2, '0')} ` +
            `(wrong firmware version, or a conflicting patch already wrote here)`);
    } else if (opts.requireExpect) {
      throw new Error(
        `${pre}region at ${r.offset} has no "expect" bytes, so it can't be safely ` +
        `stacked onto an already-patched image (apply it alone, or add expect bytes)`);
    }
    for (const [s, e] of written)
      if (off < e && end > s)
        throw new Error(
          `${pre}region at ${r.offset} (…0x${end.toString(16)}) overlaps a range already ` +
          `patched (0x${s.toString(16)}…0x${e.toString(16)}) — these patches conflict`);
    image.set(data, off);
    written.push([off, end]);
    mine.push([off, end]);
  }
  return mine;
}

/**
 * Apply a patch to a copy of `image`, auto-selecting the target by base hash
 * (unless `target` is given) and verifying base_sha256 / result_sha256.
 * @param {Uint8Array} image  the decoded 0x60000 baseline (not mutated)
 * @param {object} patch      parsed patch JSON
 * @param {{target?:string}} [opts]
 * @returns {Promise<{name:string, image:Uint8Array, baseHash:string, resultHash:string}>}
 */
export async function applyPatch(image, patch, opts = {}) {
  const baseHash = await sha256hex(image);
  const sel = selectTarget(patch, baseHash, opts.target);
  if (!sel)
    throw new Error(
      `image does not match any target in this patch\n  its SHA-256 is ${baseHash}\n` +
      `  patch targets: ${Object.keys(patch.targets || {}).join(', ')}\n` +
      `  (wrong firmware version, or already patched?)`);

  const patched = image.slice(); // copy; leave the original intact
  applyRegions(patched, sel.target.regions || [], { label: patch.name });

  const resultHash = await sha256hex(patched);
  if (sel.target.result_sha256 && sel.target.result_sha256 !== resultHash)
    throw new Error(
      `post-patch checksum mismatch for target "${sel.name}"\n` +
      `  expected ${sel.target.result_sha256}\n  got      ${resultHash}`);

  return { name: sel.name, image: patched, baseHash, resultHash };
}

/**
 * Apply several patches, in order, to ONE target — chaining onto a single
 * running image so independent mods (e.g. voltmeter + custom logo) can stack.
 *
 * Verification rule per patch: if its base_sha256 matches the current running
 * image it's verified normally; otherwise (the image was already modified by an
 * earlier patch) every region must carry `expect` bytes, which are checked. A
 * region overlapping one an earlier patch wrote is a conflict and throws. When
 * exactly one patch applies and it declares result_sha256, the whole-image
 * result is verified too (identical to applyPatch's guarantee for a lone patch).
 *
 * @param {Uint8Array} image  decoded baseline (not mutated)
 * @param {Array<{patch:object, name?:string, target:string}>} entries  applied in order
 * @returns {Promise<{image:Uint8Array, applied:Array<{name:string,resultHash:string}>}>}
 */
export async function applyPatches(image, entries) {
  const running = image.slice();
  const written = [];
  const applied = [];
  // keep only patches that actually touch this target, in the given order
  const relevant = entries.filter((e) => ((e.patch.targets || {})[e.target]));
  for (const { patch, name, target } of relevant) {
    const t = patch.targets[target];
    const label = name || patch.name || 'patch';
    const curHash = await sha256hex(running);
    const baseMatches = t.base_sha256 && t.base_sha256 === curHash;
    applyRegions(running, t.regions || [], { written, requireExpect: !baseMatches, label });
    const resultHash = await sha256hex(running);
    if (relevant.length === 1 && t.result_sha256 && t.result_sha256 !== resultHash)
      throw new Error(
        `${label}: post-patch checksum mismatch\n` +
        `  expected ${t.result_sha256}\n  got      ${resultHash}`);
    applied.push({ name: label, resultHash });
  }
  return { image: running, applied };
}
