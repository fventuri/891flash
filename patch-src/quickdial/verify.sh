#!/usr/bin/env bash
# verify.sh — build the five quickdial code-cave routines from the source in this
# folder and confirm they are byte-for-byte the cave regions distributed in
# ../../patches/ft891_quickdial_fine.patch.json. Also regenerates the menu label
# table from its documented ASCII strings and checks it too.
#
# This proves the published patch is exactly this source (plus generic ASCII menu
# labels) and contains no Yaesu firmware. No firmware is needed to build or verify
# (only to *apply* the patch; you supply your own official image).
#
# Requires an H8/300H (H8SX) cross toolchain:
#   binutils-h8300-linux-gnu  (h8300-linux-gnu-as / -ld / -objcopy)
set -euo pipefail
cd "$(dirname "$0")"

PATCH=../../patches/ft891_quickdial_fine.patch.json
AS=h8300-linux-gnu-as
LD=h8300-linux-gnu-ld
OC=h8300-linux-gnu-objcopy

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Each cave routine is position-dependent: pass its placement address as CAVE_BASE
# so absolute references (jumps into shared firmware, &rodata) resolve correctly.
build() {  # $1=source  $2=cave base
  $AS -mach=h8300sx "$1" -o "$TMP/$1.o"
  $LD -m h8300sxelf -T cave.x --defsym "CAVE_BASE=$2" "$TMP/$1.o" -o "$TMP/$1.elf" 2>/dev/null || true
  $OC -O binary "$TMP/$1.elf" "$TMP/$1.bin"
}
build qd_inc6.s  0x2f160
build qd_dec6.s  0x2f180
build qd_step6.s 0x2f1a0
build qd_cat_wr.s 0x2f2a0
build qd_cat_rd.s 0x2f2d0

python3 - "$PATCH" "$TMP" <<'PY'
import json, sys, os
p = json.load(open(sys.argv[1])); tmp = sys.argv[2]
regs = {int(r['offset'], 16): r['hex'] for r in p['targets']['main']['regions']}
ok = True
for src, base in [('qd_inc6.s',0x2f160), ('qd_dec6.s',0x2f180), ('qd_step6.s',0x2f1a0),
                  ('qd_cat_wr.s',0x2f2a0), ('qd_cat_rd.s',0x2f2d0)]:
    built = open(os.path.join(tmp, src + '.bin'), 'rb').read().hex()
    m = built == regs[base]; ok &= m
    print("%-12s @0x%05x: built %3d B  matches patch: %s" % (src, base, len(built)//2, m))
# Menu labels are generic ASCII, 6 x 15-byte space-padded entries — regenerate & check.
labels = ['1kHz', '5kHz', '10kHz', '50kHz', '100kHz', '500kHz']
gen = ''.join((s + ' '*(15-len(s))) for s in labels).encode('ascii').hex()
m = gen == regs[0x2f240]; ok &= m
print("qd_labels    @0x2f240: gen   %3d B  matches patch: %s" % (len(gen)//2, m))
sys.exit(0 if ok else 1)
PY

echo "OK: the patch's cave regions are byte-for-byte the source in this folder."
