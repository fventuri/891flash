/* panel_volt.c — render the DC supply voltage "NN.NV" into the panel LCD
 * framebuffer, called from the flush hook (see panel_volt_hook.s).
 *
 * Framebuffer: 0xFF24F6, 6 pages x 160 cols; byte(page P,col C) at +P*160+C;
 * bit b = pixel row P*8+b (LSB = top).  Confirmed on hardware 2026-09-07.
 *
 * Voltage source: panel RAM 0xFF2A0F = 0x0T (tens BCD), 0xFF2A10 = 0xUD (units
 * high nibble, tenths low nibble).  e.g. 13.8V -> 0xFF2A0F=0x01, 0xFF2A10=0x38.
 * These are the last two general-display bytes; the MAIN companion firmware
 * writes a FRESH reading into them on every operating-screen message, so the
 * value is LIVE (updates continuously) -- REQUIRES the MAIN companion.
 *
 * Screen gate: the current display message tag is at 0xFF2345.  The operating
 * screen is tag 0x20; menu/function screens are other tags (0x40/0x43/0x70...).
 * We draw ONLY on 0x20 so the overlay never lands on menu text (and never on the
 * boot screen 0x70, which shows the voltage natively).
 *
 * Placement: small 3x5 font, top-right corner (page 0, starting col 140).
 */

typedef volatile unsigned char vu8;

#define OPER_TAG  0x20       /* operating-screen message tag (0xFF2345) */
#define COL0      140        /* top-right start column; page 0 (rows 0..4) */

/* 3x5 font, column-major, bit0 = top.  0-9 = digits, 10 = '.', 11 = 'V' */
static const unsigned char font[12][3] = {
  {0x1F,0x11,0x1F}, {0x12,0x1F,0x10}, {0x1D,0x15,0x17}, {0x15,0x15,0x1F},
  {0x07,0x04,0x1F}, {0x17,0x15,0x1D}, {0x1F,0x15,0x1D}, {0x01,0x01,0x1F},
  {0x1F,0x15,0x1F}, {0x17,0x15,0x1F}, {0x00,0x18,0x00}, {0x0F,0x10,0x0F},
};

static void putc3(vu8 *p, int idx) {          /* 3 glyph cols + 1 blank gap */
  int i; const unsigned char *g = font[idx];
  for (i = 0; i < 3; i++) p[i] = g[i];
  p[3] = 0;
}

void volt_overlay(void) {
  unsigned char t, lo, u, d;
  vu8 *fb;
  int i;

  if (*(vu8 *)0xFF2345L != OPER_TAG) return;   /* operating screen only */

  t  = (*(vu8 *)0xFF2A0FL) & 0x0F;              /* tens  */
  lo =  *(vu8 *)0xFF2A10L;
  u  = (lo >> 4) & 0x0F;                        /* units  */
  d  =  lo       & 0x0F;                        /* tenths */
  if (t > 9) t = 0;
  if (u > 9) u = 0;
  if (d > 9) d = 0;

  fb = (vu8 *)0xFF24F6L + COL0;                 /* page 0, col COL0 */
  if (t) putc3(fb, t);                          /* tens, blanked if leading zero */
  else   for (i = 0; i < 4; i++) fb[i] = 0;
  putc3(fb +  4, u);
  putc3(fb +  8, 10);                           /* '.' */
  putc3(fb + 12, d);
  putc3(fb + 16, 11);                           /* 'V' */
}
