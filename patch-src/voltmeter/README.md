# Voltmeter patch — source

The **FT-891 Live Supply-Voltage Mod**: it overlays the live DC supply voltage
(`NN.NV`) in the top-right of the operating screen and adds a `RM8;` CAT command
that returns the supply voltage in decivolts.

Everything here is the mod author's own code. It compiles to the two small
code-cave routines distributed in `../../patches/ft891_voltmeter.patch.json`. No
Yaesu firmware is needed to build or verify — only to *apply* the patch (you
supply your own official image).

## Files

| File | Role |
|------|------|
| `main_volt_inject.s` | MAIN cave: `_main_volt_inject` (writes a fresh reading into the panel-bound display bytes) + `_rm8_hook` (the `RM8;` CAT handler). Linked at `0x2F130`. |
| `panel_volt.c` | PANEL cave: `volt_overlay()` — draws `NN.NV` into the LCD framebuffer on the operating screen only. |
| `panel_volt_hook.s` | PANEL cave: `_volt_hook` trampoline installed at the LCD flush entry; calls the renderer, runs the displaced watchdog kick, rejoins the flush. Linked at `0xC640`. |
| `cave.x`, `cave_panel.x` | Linker scripts placing a blob at a fixed cave base (`0x2F130` / `0xC640`). |
| `verify.sh` | Builds both blobs and checks them byte-for-byte against the patch JSON. |

## Toolchain

An H8/300H (H8SX) cross toolchain — on Debian/Ubuntu:

```sh
sudo apt install binutils-h8300-linux-gnu gcc-h8300-linux-gnu
```

Provides `h8300-linux-gnu-as` / `-ld` / `-objcopy` and `h8300-linux-gnu-gcc`.

## Build

```sh
# MAIN cave (42 bytes) at 0x2F130
h8300-linux-gnu-as -o main.o main_volt_inject.s
h8300-linux-gnu-ld -m h8300sxelf -T cave.x main.o -o main.elf
h8300-linux-gnu-objcopy -O binary main.elf main_volt_inject.bin

# PANEL cave (254 bytes) at 0xC640
h8300-linux-gnu-gcc -msx -Os -ffreestanding -fomit-frame-pointer -fno-jump-tables \
    -c panel_volt.c -o panel_volt.o
h8300-linux-gnu-as -o panel_hook.o panel_volt_hook.s
h8300-linux-gnu-ld -m h8300sxelf -T cave_panel.x panel_hook.o panel_volt.o -o panel.elf
h8300-linux-gnu-objcopy -O binary panel.elf panel_volt_full.bin
```

(Ignore the linker's "cannot find entry symbol" warning — a cave blob has no `_start`.)

## Verify it matches the published patch

```sh
./verify.sh
# MAIN  cave: built  42 B  matches patch: True
# PANEL cave: built 254 B  matches patch: True
# OK: the patch's cave regions are byte-for-byte the source in this folder.
```

## How it's wired into the firmware

The patch also flips a few stock bytes to redirect execution into the caves above.
These are the mod author's own redirect bytes (with `expect` = the original bytes
they replace, so the patch refuses to apply to the wrong firmware):

| Image | Offset | Stock → Patched | Meaning |
|-------|--------|-----------------|---------|
| MAIN  | `0x1C883` | `08` → `09` | widen the RM index bound to allow index 8 |
| MAIN  | `0x1C88A` | `B5 04` → `F1 4A` | RM meter fetch → `_rm8_hook` (`0x2F14A`) |
| MAIN  | `0x22282` | `25 A2` → `F1 30` | operating-msg build → `_main_volt_inject` (`0x2F130`) |
| PANEL | `0x5A72` | `5E 00 83 C6` → `5A 00 C6 40` | LCD flush → `jmp @0xC640` (the trampoline) |

The cave allocations (`0x2F130` on MAIN, `0xC640` on PANEL) are recorded in
`../../docs/cave_main.tsv` / `cave_panel.tsv` so other patches don't collide with
them — see `../../docs/CAVE_ALLOCATION.md`.
