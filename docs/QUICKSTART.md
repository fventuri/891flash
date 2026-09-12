# 891flash — Quick Start

Everything in this folder is original, open-source code. **No Yaesu firmware is
included.** You supply the official firmware you download from Yaesu; it never
leaves your machine.

> ⚠️ **Flashing firmware can permanently damage a radio.** Read the power-on
> instructions the app shows for each board, and proceed at your own risk.

## What you need

- The official **FT-891 firmware** from Yaesu, unzipped into a folder (it contains
  files such as `AH065_M_V0110.SFL` and `AH065_P_V0101.SFL`).
- The radio's **CAT (Enhanced) USB port** connected to your computer (a Silicon
  Labs **CP210x** adapter).
- For the browser flasher: **Chrome/Edge**, or **Firefox 151+** (WebSerial).

---

## Option A — the browser flasher (recommended)

1. Run **`891flash`**:
   - Windows: double-click `891flash.exe`.
   - macOS/Linux: `./891flash` in a terminal (on macOS you may need to allow it in
     *System Settings → Privacy & Security* the first time).
   Your default browser opens `http://localhost:8891/`.
2. **Firmware** — point the folder picker at your unzipped Yaesu firmware folder.
3. **Patches** — the patches bundled in this release are already listed. Tick any
   you want (independent ones can be combined). This step is optional.
4. **Port** — click *Select port…* and choose the CP210x.
5. **Flash** — pick a target (MAIN / PANEL / DSP), follow the exact power-on
   sequence the app shows, then flash. One board per pass; power-cycle and
   re-select the port between boards.

Flags: `891flash -port 9000` (change port), `-no-open` (don't launch a browser),
`-patches DIR` (use a different patches folder).

---

## Option B — headless / command line (e.g. Raspberry Pi)

No browser required. `ft891flash` writes a raw 384 KB `.bin`, so decode the Yaesu
`.SFL` first (and optionally apply a patch), then flash.

```sh
# 1. decode the official .SFL to a raw .bin
./ft891patch decode -in AH065_M_V0110.SFL -out AH065_M_V0110.bin

# 2. (optional) apply a patch — auto-selects MAIN/PANEL by SHA-256, verifies both hashes
./ft891patch patch -patch patches/ft891_voltmeter.patch.json \
    -in AH065_M_V0110.bin -out AH065_M_V0110_voltmeter.bin

# 3. flash the board (put it in programming mode first — see below)
./ft891flash --main /dev/ttyUSB0 AH065_M_V0110_voltmeter.bin
```

`ft891flash --list` shows serial ports; add `--dry-run` for a read-only handshake.
`ft891flash --dsp PORT firmware.dat` flashes the DSP (raw `.dat`, no decode).
Run each tool with `-h` for full options.

### Power-on / programming modes

| Board | How to enter programming mode |
|-------|-------------------------------|
| MAIN  | Rear **PGM** slide switch LEFT, then power on (display stays blank; do NOT press POWER). |
| PANEL | PGM switch RIGHT; hold **[PWR/LOCK]+[BAND]+[FAST]** at power-on (TX/BUSY LED orange). |
| DSP   | PGM switch RIGHT; hold **[PWR/LOCK]+[F]+[C]** at power-on (display reads "DSP WRITE MODE"). |

---

## Verifying your download

A `SHA256SUMS.txt` accompanies the release zips. Check your download with:

```sh
sha256sum -c SHA256SUMS.txt      # Linux
shasum -a 256 -c SHA256SUMS.txt  # macOS
```

## Patches

See `patches/README.md` for what each bundled patch does, and
`docs/CREATING_A_PATCH.md` to make your own. Patches contain only original code
and firmware fingerprints; the tools refuse to apply a patch to the wrong
firmware version.
