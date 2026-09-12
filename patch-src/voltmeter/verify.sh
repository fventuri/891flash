#!/usr/bin/env bash
# verify.sh — build the two voltmeter code-cave blobs from the source in this
# folder and confirm they are byte-for-byte the cave regions distributed in
# ../../patches/ft891_voltmeter.patch.json.
#
# This proves the published patch is exactly this source and contains no Yaesu
# firmware. No firmware is needed to build or verify (only to *apply* the patch).
#
# Requires an H8/300H (H8SX) cross toolchain:
#   binutils-h8300-linux-gnu  (h8300-linux-gnu-as / -ld / -objcopy)
#   h8300-linux-gnu-gcc       (-msx)
set -euo pipefail
cd "$(dirname "$0")"

PATCH=../../patches/ft891_voltmeter.patch.json
AS=h8300-linux-gnu-as
LD=h8300-linux-gnu-ld
OC=h8300-linux-gnu-objcopy
GCC=h8300-linux-gnu-gcc

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# MAIN cave (_main_volt_inject + _rm8_hook), linked at 0x2F130 (cave.x default)
$AS -o "$TMP/main.o" main_volt_inject.s
$LD -m h8300sxelf -T cave.x "$TMP/main.o" -o "$TMP/main.elf" 2>/dev/null || true
$OC -O binary "$TMP/main.elf" "$TMP/main.bin"

# PANEL cave (_volt_hook trampoline + volt_overlay), linked at 0xC640 (cave_panel.x default)
$GCC -msx -Os -ffreestanding -fomit-frame-pointer -fno-jump-tables -c panel_volt.c -o "$TMP/panel_volt.o"
$AS -o "$TMP/panel_hook.o" panel_volt_hook.s
$LD -m h8300sxelf -T cave_panel.x "$TMP/panel_hook.o" "$TMP/panel_volt.o" -o "$TMP/panel.elf" 2>/dev/null || true
$OC -O binary "$TMP/panel.elf" "$TMP/panel.bin"

python3 - "$PATCH" "$TMP/main.bin" "$TMP/panel.bin" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))
def region(t, off):
    return next(r['hex'] for r in p['targets'][t]['regions'] if int(r['offset'], 16) == int(off, 16))
main  = open(sys.argv[2], 'rb').read().hex()
panel = open(sys.argv[3], 'rb').read().hex()
m_ok = region('main', '0x2f130') == main
p_ok = region('panel', '0xc640') == panel
print("MAIN  cave: built %3d B  matches patch: %s" % (len(main)//2,  m_ok))
print("PANEL cave: built %3d B  matches patch: %s" % (len(panel)//2, p_ok))
sys.exit(0 if (m_ok and p_ok) else 1)
PY

echo "OK: the patch's cave regions are byte-for-byte the source in this folder."
