# FT-891 Code-Cave Allocation & Patch Compatibility

How the free flash region (the "cave") is shared between firmware patches, and how
to tell whether two patches can be **stacked**. Companion to the apply-time checks
in the tools (`ft891patch` and the web app) and to
[`CREATING_A_PATCH.md`](CREATING_A_PATCH.md).

---

## The cave

Both the MAIN and PANEL images are full 384 KB flash dumps (0x60000 B) with a large
contiguous **erased (`0xFF`) region at the top** — free space we append new code and
data into ("the cave"). Measured from the official V0110/V0101 images:

| Target | Cave (free `0xFF`) | Size |
|--------|--------------------|------|
| MAIN  `AH065_M_V0110.bin` | `[0x2F12E, 0x60000)` | 200402 B (~196 KB) |
| PANEL `AH065_P_V0101.bin` | `[0x0C637, 0x60000)` | 342473 B (~334 KB) |

These bounds are the `FULL CAVE` rows in the ledgers below.

## The compatibility rule

A firmware patch touches two *kinds* of bytes:

- **cave allocations** — new routines / tables **appended into the free region**
  (offset ≥ the cave start above). This is the shared arena every additive patch
  draws from.
- **in-place edits** — bytes that **overwrite existing firmware**: JSR/branch
  detours and redirect vectors wired to the new code, version bytes, the power-on
  logo bitmap, etc. (offset *below* the cave start).

> **Two patches are stackable (compatible) iff none of their written byte ranges
> overlap — cave allocations *or* in-place edits.**

In practice the in-place edits are tiny and land on distinct, mod-specific spots,
so the binding constraint is the cave: **two patches that both add code/data are
compatible iff they occupy different regions of the cave.** That's what these
ledgers track — so when you author a new patch you can pick a free gap nobody else
uses, instead of colliding at flash time.

This bookkeeping is the *design-time* half. The *apply-time* half is enforced
automatically by the tools when applying patches: each region may carry
an `expect` field (the original bytes it overwrites), and stacking two patches whose
regions overlap throws a collision error. The ledger is how you avoid that overlap
in the first place; the `expect`/collision check is the seatbelt if you don't.

---

## The ledgers

`cave_main.tsv` and `cave_panel.tsv` — one per image, tab-separated:

```
from_hex   to_hex   size_bytes   patch   notes
```

- `from_hex` / `to_hex` — a **half-open** range `[from, to)`: `from` is the first byte
  the entry occupies, `to` is **one past the last** (`to` itself is *not* occupied);
  `size_bytes = to − from`. This mirrors the region math in `patch.js`
  (`[off, off+len)`), so one entry butts against the next when `prev.to == next.from`.
- `patch` — the patch's `name` (matches the `name` field in its `.patch.json`), or
  the special all-caps **`FULL CAVE`** for the arena-bounds reference row.
- Only **cave allocations** are listed (offset ≥ the cave start). In-place edits are
  *not* in these files — they don't consume the shared arena. (E.g. a custom boot
  logo overwrites the existing bitmap at panel `0x09AE8`, below the cave start, so it
  uses **zero** cave and appears in no ledger.)

The `FULL CAVE` row is the whole initial free region *before any patch*. Every other
row is a claim against it; the un-claimed gaps are where a new patch can go.

### Adding a patch

1. Open the ledger for each target the patch modifies.
2. Find a gap in `FULL CAVE` not covered by any existing (non-`FULL CAVE`) row that's
   big enough for your routine/table.
3. Place your cave region there; add a row `from  to  size  <patch name>  <note>`.
   Keep rows sorted by `from_hex`.
4. Add matching `expect` bytes to the patch's regions so it stays stackable in any
   order (see the flasher notes).

### Finding free gaps

```python
# free cave gaps for one target, from its TSV
import csv
rows=[r for r in csv.DictReader(open('cave_main.tsv'),delimiter='\t')]
full=next(r for r in rows if r['patch']=='FULL CAVE')
lo,hi=int(full['from_hex'],16),int(full['to_hex'],16)
used=sorted((int(r['from_hex'],16),int(r['to_hex'],16)) for r in rows if r['patch']!='FULL CAVE')
cur=lo
for a,b in used:                       # half-open [a, b)
    if a>cur: print(f'free [0x{cur:05X}, 0x{a:05X}) ({a-cur} B)')
    cur=max(cur,b)
if cur<hi: print(f'free [0x{cur:05X}, 0x{hi:05X}) ({hi-cur} B)')
```

---

## Current allocations (2026-09-12)

- **MAIN:** voltmeter routine `[0x2F130, 0x2F15A)` (42 B). ~196 KB still free above it.
- **PANEL:** voltmeter routine `[0x0C640, 0x0C73E)` (254 B). ~334 KB still free above it.
- A custom boot logo (via `ft891customlogo`) is in-place (panel `0x09AE8`, 184 B) —
  it consumes no cave.

See also: [`CREATING_A_PATCH.md`](CREATING_A_PATCH.md) (the patch format and the
`expect`/collision rules the tools enforce) and the per-patch source under
`../patch-src/`.
