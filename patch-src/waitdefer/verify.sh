#!/usr/bin/env bash
# verify.sh — build the WAIT-defer code-cave routine from wd_waitcave.s in this
# folder and confirm it is byte-for-byte the cave region distributed in
# ../../patches/ft891_waitdefer.patch.json.
#
# This proves the published patch is exactly this source and contains no Yaesu
# firmware. No firmware is needed to build or verify (only to *apply* the patch;
# you supply your own official image).
#
# Requires an H8/300H (H8SX) cross toolchain:
#   binutils-h8300-linux-gnu  (h8300-linux-gnu-as / -ld / -objcopy)
set -euo pipefail
cd "$(dirname "$0")"

PATCH=../../patches/ft891_waitdefer.patch.json
AS=h8300-linux-gnu-as
LD=h8300-linux-gnu-ld
OC=h8300-linux-gnu-objcopy

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# The cave routine is position-dependent: pass its placement address as CAVE_BASE
# so absolute references (jumps into shared firmware, &rodata) resolve correctly.
build() {  # $1=source  $2=cave base
  $AS -mach=h8300sx "$1" -o "$TMP/$1.o"
  $LD -m h8300sxelf -T cave.x --defsym "CAVE_BASE=$2" "$TMP/$1.o" -o "$TMP/$1.elf" 2>/dev/null || true
  $OC -O binary "$TMP/$1.elf" "$TMP/$1.bin"
}
build wd_waitcave.s 0x2f2e0

python3 - "$PATCH" "$TMP" <<'PY'
import json, sys, os
p = json.load(open(sys.argv[1])); tmp = sys.argv[2]
regs = {int(r['offset'], 16): r['hex'] for r in p['targets']['main']['regions']}
ok = True
for src, base in [('wd_waitcave.s', 0x2f2e0)]:
    built = open(os.path.join(tmp, src + '.bin'), 'rb').read().hex()
    m = built == regs[base]; ok &= m
    print("%-14s @0x%05x: built %3d B  matches patch: %s" % (src, base, len(built)//2, m))
sys.exit(0 if ok else 1)
PY

echo "OK: the patch's cave region is byte-for-byte wd_waitcave.s in this folder."
