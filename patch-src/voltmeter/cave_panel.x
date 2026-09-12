/* cave.x — linker script placing patch code into the FT-891 MAIN flash cave.
 *
 * The MAIN image maps at address 0 (file offset == memory address), real code
 * ends ~0x2f12e, and 0xc640..0x60000 is erased 0xFF flash we can append into.
 *
 * Set CAVE_BASE to the address you are placing THIS blob at. Absolute references
 * (function calls, &rodata) are resolved against it, so the emitted bytes are only
 * valid when written at that exact file offset. Bump it as you allocate more caves.
 *
 * Usage:  h8300-linux-gnu-ld -m h8300sxelf -T cave.x --defsym CAVE_BASE=0xc640 \
 *                            in.o -o out.elf
 * (ignore the "cannot find entry symbol" warning — a cave blob has no _start.)
 */
OUTPUT_FORMAT("elf32-h8300")
OUTPUT_ARCH(h8300sx)

CAVE_BASE = DEFINED(CAVE_BASE) ? CAVE_BASE : 0xc640;

SECTIONS
{
  . = CAVE_BASE;
  .text : {
    *(.text.hook)        /* put the entry hook first if you name a section */
    *(.text .text.*)
    *(.rodata .rodata.*) /* const tables/strings ride along, absolute-addressed */
  }
  /* Nothing may land in .data/.bss: no C runtime runs, so RAM globals are never
   * initialized. Use locals, fixed absolute addresses, or const .rodata only. */
  .discard (NOLOAD) : { *(.data) *(.bss) *(COMMON) }
}
