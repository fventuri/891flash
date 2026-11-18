# Patches

Each patch is a JSON file that contains **only the mod author's own bytes** — new
code and the few bytes that redirect the stock firmware to it — plus the SHA-256
fingerprints of the official firmware it targets. **No Yaesu firmware is included.**
You apply a patch to your own official image; the tools verify the base hash first
and refuse to touch the wrong firmware version.

Apply a patch with the **`891flash`** web app (bundled patches load automatically —
just tick them) or with **`ft891patch`** on the command line. See the top-level
`README.md` and `docs/QUICKSTART.md`.

## Available patches

| File | Name | Boards | Firmware | What it does |
|------|------|--------|----------|--------------|
| `ft891_voltmeter.patch.json` | FT-891 Live Supply-Voltage Mod | MAIN + PANEL | MAIN `V0110`, PANEL `V0101` | Overlays the live DC supply voltage (`NN.NV`) in the top-right corner of the operating screen, and adds a `RM8;` CAT command that returns the supply voltage in decivolts. Source in `patch-src/voltmeter/`. |
| `ft891_quickdial_fine.patch.json` | FT-891 Quick Dial Fine Steps | MAIN | MAIN `V0110` | Expands Menu 14-01 QUICK DIAL (the MULTI-knob VFO step in SSB/CW/RTTY/DATA) from 50/100/500 kHz to **1 / 5 / 10 / 50 / 100 / 500 kHz**, with full CAT `EX1401` read/write parity. Default becomes 10 kHz. Source in `patch-src/quickdial/`. |
| `ft891_waitdefer.patch.json` | FT-891 WAIT Defer (FC-50 scanning) | MAIN | MAIN `V0110` | Keeps the **`WAIT`** status message from covering the frequency digits **while you turn the main dial** (an FC-50 ATU paints `WAIT` over the VFO the whole time you scan a band). `WAIT` is hidden while the dial moves and reappears once it settles, so the "tuner catching up, don't key up yet" cue still shows before TX. Display-only; Hi-SWR and the ATU/TX-inhibit logic are untouched. **Not yet hardware-tested** (author has no FC-50; inert with no tuner) — hold-off timing may need tuning. Source in `patch-src/waitdefer/`. |

## Stacking

Independent patches can be applied together. In the `891flash` web app, tick more
than one; on the command line, run `ft891patch patch` once per patch, feeding each
output into the next. Two patches are compatible when they don't modify the same
bytes — see `docs/CAVE_ALLOCATION.md` for how shared "code-cave" space is tracked
so additive patches don't collide.

## Verifying a patch is only original code

A patch's cave regions can be rebuilt from source and compared byte-for-byte
against the JSON, so you can confirm it carries exactly the published source and no
Yaesu firmware. See each patch's folder under `patch-src/`.
