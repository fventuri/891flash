# FT-891 WAIT Defer (FC-50 scanning)

Stops the **`WAIT`** status message from covering the frequency digits **while you
turn the main dial**. Requested by Mark, M5TEA (FC-50 ATU + SOTA band-scanning):
spinning the VFO from one end of a band to the other paints `WAIT` straight over
the frequency, so you can only see the bottom of the digits.

With this mod, `WAIT` is **hidden while the dial is moving** (and for a short
hold-off afterward), then **reappears once the dial settles** — so it still tells
you *"the tuner is catching up, don't key up yet"* before you transmit. Exactly the
"defer it till the end of scanning" behaviour Mark asked for.

> ✅ **Confirmed working on hardware** by Mark, M5TEA (2026-11-19) on a radio with an
> FC-50 ATU: the frequency is no longer covered while scanning a band. In his
> operating he reported seeing no `WAIT` at all (which suited him) — so whether
> `WAIT` also *reappears once the dial settles* has not been separately confirmed.
> With **no FC-50 attached the image is functionally identical to stock** (the
> routine sits behind the stock ATU gate and never runs). The author has no FC-50, so
> the hold-off value **K** — how long `WAIT` stays hidden after you stop turning — is
> still at its untuned default and may want adjusting (see *Tuning K*).

## What it does (and doesn't) touch

- **Display only.** It replaces just the `WAIT` decision inside the status-overlay
  builder. The ATU control state machine, the WAIT/TX-inhibit flags, and the
  **Hi-SWR** warning are all left exactly as stock.
- When the dial is **idle**, the `WAIT` overlay behaves **byte-for-byte like stock**
  (same message, same trigger conditions, same override of the frequency field).
- When the dial is **moving**, the overlay is suppressed, so the frequency field
  (pre-filled with spaces by the builder) shows through = frequency stays readable.

## How it works

The stock status-overlay builder at `0x242b0` fills a 10-char field that overlays
the frequency, choosing Hi-SWR / WAIT / nothing:

```
0x242c2  btst #4,@0xff2013      ; Hi-SWR?  -> draw "Hi-SWR"   (untouched)
0x242d4  btst #0,@0xff202b      ; ATU gate (tuner active)
0x242dc  beq  0x24312           ; gate off -> nothing   (no FC-50 => this mod never runs)
0x242de  <WAIT decision block>  ; <-- replaced
...
0x24302  <draw tail: copy 10B -> buffer, set 0xff249b.1 (override freq), rts>
```

`WAIT` is shown when `0xff202b.0 && (0xff2029.6 || (0xff2027.2 && 0xff2011.0))`.
`0xff2029.6` is a **25-count WAIT timer** (armed at `0x2e5b8`); with an FC-50 attached
each dial detent into un-matched territory re-arms it, so it stays lit the whole time
you scan.

The mod repoints `0x242de` to a cave routine `wait_cave` (`0x2f2e0`) that:

1. detects dial motion itself, by comparing the **raw main-dial counter `0xff23c4`**
   (the byte the firmware diffs against its own shadow at `0x2c7e8`) to a private
   shadow at `0xff8f01`. (The firmware's `0xff200d.3` "dial moved" bit was tried
   first but proved too transient to gate on — it toggles even at rest.)
2. while the counter is changing: arms a hold-off counter (free RAM `0xff8f00`) to
   **K** and returns without drawing WAIT;
3. while the counter is steady: counts the hold-off down one per display refresh;
   until it reaches 0, still suppress; at 0, run the **stock** WAIT decision (reusing
   the stock draw tails at `0x24302` / `0x2430a`).

So continuous dialing keeps re-arming the hold-off (WAIT hidden); a fraction of a
second after you stop, WAIT returns.

Both RAM bytes (`0xff8f00` hold-off, `0xff8f01` dial shadow) are unused by the stock
firmware (used RAM ends ~`0xff8e37`; the stack grows down from `0xffc000`).

### Tuning K

`K` is the hold-off in **display refreshes** after the last dial movement. It is a
single immediate byte in the cave: the `0x0a` in the `f8 0a` (`mov.b #0x0a,r0l`) at
**file offset `0x2f2f7`**. Default **10** (an untested educated guess).

- WAIT flickers on during slow, deliberate tuning → **raise** K (try 15–20).
- WAIT takes too long to come back after you stop → **lower** K.

To change it, edit the `0x0a` byte in the cave region `hex` of
`ft891_waitdefer.patch.json`: it is the **24th byte** of that hex string — the `0a`
of the `f8 0a` pair — i.e. `...f8`**`0a`**`6aa800ff8f00...`. Then update
`result_sha256` to match, or just change the `#0x0a` immediate in `wd_waitcave.s`
and rebuild (see below).

## Build / verify

Cave source: `wd_waitcave.s`. `./verify.sh` reassembles + relinks it and confirms it
reproduces the cave region byte-for-byte:

```
h8300-linux-gnu-as  -mach=h8300sx wd_waitcave.s -o wd.o
h8300-linux-gnu-ld  -m h8300sxelf -T cave.x --defsym CAVE_BASE=0x2f2e0 wd.o -o wd.elf
h8300-linux-gnu-objcopy -O binary wd.elf wd.bin   # == the cave region hex
```

The patch (`../../patches/ft891_waitdefer.patch.json`):
- base: official `AH065_M_V0110.bin` (sha256 `ce63d7…`)
- **result sha256** `f230cf755b309907975c138defb1a569894cfc44bcb0c6f2c397fbea020e4c25`
- changes only 2 regions: the 8-byte hook at `0x242de` and the 96-byte cave at `0x2f2e0`.

## Flashing

Apply with the `891flash` web app (tick it in the patch list) or `ft891patch` on the
command line. The tools verify the base SHA-256 first and refuse the wrong firmware.

### Stacking with the other mods

This patch is **stackable** with the Supply-Voltage and QUICK DIAL Fine Steps mods:
its in-place edit (`0x242de`) and cave (`0x2f2e0–0x2f340`) don't overlap any of
theirs. Tick several together in the web app, or chain `ft891patch patch` per patch.

Requested by M5TEA. Contains only the mod author's code — no Yaesu firmware bytes are
copied. Apply to your own official firmware image.
