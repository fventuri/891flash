# Creating a patch

A patch is a small JSON file that the tools apply to **your own** official FT-891
firmware. It contains only the mod author's own bytes plus SHA-256 fingerprints of
the firmware it targets — **never any Yaesu firmware**. This guide covers the
format, how to author one, and how to keep it stackable with other patches.

## The patch format

```jsonc
{
  "name": "FT-891 Something Mod",
  "version": "1.0",
  "note": "What it does; that it carries no Yaesu firmware.",
  "targets": {
    "main": {                       // a board: "main", "panel" (DSP not yet)
      "base_name": "AH065_M_V0110.bin",
      "base_size": 393216,
      "base_sha256": "…",           // SHA-256 of the exact official image this targets
      "result_sha256": "…",         // optional: SHA-256 after patching (single-patch check)
      "regions": [
        { "offset": "0x2f130", "expect": "ff…", "hex": "5e02…" }
      ]
    }
  }
}
```

A **region** writes `hex` (the replacement bytes) at file `offset`. `expect` (the
original bytes it overwrites) is optional but recommended — see *Stacking* below.

### Two kinds of region

- **In-place edits** overwrite existing firmware: a jump/branch that redirects the
  stock code into your new routine, a changed constant, a replaced bitmap. Offset
  is *below* the used/free boundary.
- **Cave allocations** are new code or data appended into the erased (`0xFF`) free
  region at the top of the image — "the cave". This is shared space, so you must
  place yours where no other patch has claimed. See
  [`CAVE_ALLOCATION.md`](CAVE_ALLOCATION.md) and the ledgers
  [`cave_main.tsv`](cave_main.tsv) / [`cave_panel.tsv`](cave_panel.tsv).

### Safety the tools enforce

- **`base_sha256`** — a patch is only applied to firmware whose hash matches, so it
  can't be applied to the wrong version.
- **`result_sha256`** (optional) — when a single patch declares it, the tools verify
  the whole image afterward. Omit it for patches meant purely for stacking.
- **`expect`** — verified against the current bytes before writing.
- **Collision** — when stacking, two regions that overlap are rejected.

## Authoring a patch

### The easy path: diff two images with `ft891patch make`

If you already have a modified image (built however you like), let the tool compute
the regions — it records `expect` automatically, so the patch is stackable:

```sh
ft891patch make -base AH065_M_V0110.bin -mod AH065_M_V0110_mymod.bin \
    -patch mymod.patch.json -label main -base-name AH065_M_V0110.bin
```

Run it again with a different `-label`/`-mod` to add another board to the same
patch file. Then delete `result_sha256` from the JSON if you want the patch to be
freely stackable (it's only meaningful for a lone patch).

### Boot logos: `ft891customlogo`

A boot-logo patch needs no firmware at all — draw a 92×16 PNG and generate the JSON
directly (the offset, base hash, and original logo bytes are built in):

```sh
ft891customlogo decode --out logo.png          # stock logo as a template
ft891customlogo patch  --png logo.png --out mylogo.patch.json --name "My Call"
```

### Writing new code (the cave)

1. Write your routine (H8SX asm/C for MAIN/PANEL) and a small in-place redirect
   (usually a `jmp`/`jsr`) from the stock code into it. See `../patch-src/voltmeter/`
   for a complete worked example (asm + C + linker scripts + `verify.sh`).
2. Pick a free cave offset from the ledger. The free-gap finder in
   [`CAVE_ALLOCATION.md`](CAVE_ALLOCATION.md) lists the gaps; link your blob there.
3. Build the blob, splice it into a copy of your own firmware, and diff with
   `ft891patch make` (above) to produce the JSON.
4. **Record your cave allocation** in `cave_main.tsv` / `cave_panel.tsv` (a new row
   `from  to  size  <your patch name>  <note>`, half-open `[from, to)`), so the next
   author doesn't overlap you.

## Stacking

Two patches can be applied together when **no region of one overlaps a region of
the other** — for additive patches that means using different cave space. Give
every region an `expect` (both `ft891patch make` and `ft891customlogo` do this) so
a patch can be applied in any order: a patch whose `base_sha256` still matches the
running image is verified normally; once the image is already modified, `expect`
is what confirms each region still lands on the bytes it was built against.

Apply a stack:

```sh
# command line — repeat -patch (applied in order)
ft891patch patch -in AH065_M_V0110.bin -patch a.patch.json -patch b.patch.json -out out.bin
```

or tick multiple patches in the `891flash` web app. Overlapping or wrong-firmware
patches fail loudly before anything is flashed.

## Checklist before publishing

- [ ] The patch carries **no Yaesu firmware** — only your bytes, offsets, and hashes.
- [ ] `base_sha256` matches the official image; `base_size` is `393216`.
- [ ] Every region has `expect` (for stackability).
- [ ] Any cave use is recorded in the ledger and doesn't overlap an existing entry.
- [ ] Source for any new code is included under `patch-src/<name>/` with a way to
      rebuild and verify it matches the JSON (see the voltmeter's `verify.sh`).
- [ ] Applying it to your own firmware produces a radio that works as intended.
