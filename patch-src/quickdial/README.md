# Quick Dial Fine Steps patch — source

The **FT-891 Quick Dial Fine Steps Mod**: it expands Menu **14-01 QUICK DIAL**
(the MULTI / lower-left knob's VFO tuning step in SSB/CW/RTTY/DATA) from the stock
three options (50/100/500 kHz) to six ascending ones — **1 / 5 / 10 / 50 / 100 /
500 kHz** — with full CAT `EX1401` read/write parity across the new range. MAIN
image only; the panel firmware is untouched.

The value order is chosen so value 2 = 10 kHz = the stock factory-default byte, so
the power-on/reset default becomes **10 kHz** with no defaults-table edit.

Everything here is the mod author's own code. It compiles to the five small
code-cave routines distributed in `../../patches/ft891_quickdial_fine.patch.json`
(the menu label strings are generic ASCII). No Yaesu firmware is needed to build
or verify — only to *apply* the patch (you supply your own official image).

> **Engaging the step on the radio:** the MULTI knob applies the QUICK DIAL step
> only in quick-dial mode — activate the Spectrum Scope, **press** the MULTI knob,
> then rotate it. Plain rotation uses the firmware's fixed fine step (~100 Hz) and
> does not read 14-01.

## Files

| File | Role |
|------|------|
| `qd_step6.s` | 6-way step selector (140 B @ `0x2F1A0`): reads the 3-bit menu value at `0xFF8DAA`, returns the step in Hz in `er1`, and sets the downstream rounding-tier status bit in `0xFF201C` (bit0 for 1/5/10/50 kHz, bit1 = 100 kHz, bit2 = 500 kHz). Replaces the stock selector entry at `0x27734`. |
| `qd_inc6.s` | Value increment (24 B @ `0x2F160`): 3-bit field, clamp 5. Dedicated copy so the stock shared clamp-to-2 (used by 6 menu items) is left alone. |
| `qd_dec6.s` | Value decrement (18 B @ `0x2F180`): 3-bit field, clamp 0. |
| `qd_cat_wr.s` | CAT `EX1401` write (44 B @ `0x2F2A0`): accepts 0–5, 3-bit store. |
| `qd_cat_rd.s` | CAT `EX1401` read (8 B @ `0x2F2D0`): 3-bit mask → shared digit formatter. |
| `cave.x` | Linker script placing a blob at a fixed cave base (pass `--defsym CAVE_BASE=…`). |
| `verify.sh` | Builds the five blobs (and regenerates the label table) and checks them byte-for-byte against the patch JSON. |

The sixth cave region, `qd_labels` (90 B @ `0x2F240`), is six 15-byte
space-padded ASCII strings (`1kHz`…`500kHz`); `verify.sh` regenerates and checks
it from those strings.

## Toolchain

An H8/300H (H8SX) cross toolchain — on Debian/Ubuntu:

```sh
sudo apt install binutils-h8300-linux-gnu
```

Provides `h8300-linux-gnu-as` / `-ld` / `-objcopy`.

## Build

Each routine is position-dependent — link it at the address it is placed at:

```sh
h8300-linux-gnu-as -mach=h8300sx qd_step6.s -o qd_step6.o
h8300-linux-gnu-ld -m h8300sxelf -T cave.x --defsym CAVE_BASE=0x2f1a0 qd_step6.o -o qd_step6.elf
h8300-linux-gnu-objcopy -O binary qd_step6.elf qd_step6.bin
```

(Ignore the linker's "cannot find entry symbol" warning — a cave blob has no `_start`.)

## Verify it matches the published patch

```sh
./verify.sh
# qd_inc6.s    @0x2f160: built  24 B  matches patch: True
# qd_dec6.s    @0x2f180: built  18 B  matches patch: True
# qd_step6.s   @0x2f1a0: built 140 B  matches patch: True
# qd_cat_wr.s  @0x2f2a0: built  44 B  matches patch: True
# qd_cat_rd.s  @0x2f2d0: built   8 B  matches patch: True
# qd_labels    @0x2f240: gen    90 B  matches patch: True
# OK: the patch's cave regions are byte-for-byte the source in this folder.
```

## How it's wired into the firmware

The patch also flips a few stock bytes to redirect execution into the caves above
and to widen the menu's 2-bit value field to 3 bits. These are the mod author's
own redirect bytes (with `expect` = the original bytes they replace, so the patch
refuses to apply to the wrong firmware):

| Offset | Stock → Patched | Meaning |
|--------|-----------------|---------|
| `0x1075E` | `58 00 FC AE` → `5A 02 F1 60` | Menu 14-01 inc stub → `jmp qd_inc6` |
| `0x112E8` | `58 00 FA 66` → `5A 02 F1 80` | Menu 14-01 dec stub → `jmp qd_dec6` |
| `0x13576` | `00 01 58 FD` → `00 02 F2 40` | Menu 14-01 label-table pointer → `qd_labels` |
| `0x1357A` | `58 00 FC 5C` → `58 00 00 50` | Label-render branch → an `and #7` index-mask snippet (`0x135CE`) so the display stops masking the index to 2 bits (otherwise values 4/5 alias to 1kHz/5kHz) |
| `0x27734` | `6A 38 00 FF` → `5A 02 F1 A0` | QUICK DIAL step selector entry → `jmp qd_step6` |
| `0x16C84` | `58 00 F8 FE` → `5A 02 F2 A0` | Menu 14-01 CAT-write stub → `jmp qd_cat_wr` |
| `0x178BC` | `58 00 FA A8` → `5A 02 F2 D0` | Menu 14-01 CAT-read stub → `jmp qd_cat_rd` |
| `0x11D0C` | `E8 03 A8 02` → `E8 07 A8 05` | value/max predicate: `and#3/cmp#2` → `and#7/cmp#5` |

The cave allocations (`0x2F160`–`0x2F2D7` on MAIN) are recorded in
`../../docs/cave_main.tsv` so other patches don't collide with them — see
`../../docs/CAVE_ALLOCATION.md`.
