	;; qd_step6.s — QUICK DIAL (Menu 14-01) step selector, 6 options.
	;;
	;; Replaces the stock SSB/CW/RTTY/DATA MULTI-knob step selector at 0x27734
	;; (per-mode dispatch target from 0x27708). Reads the menu value in the low
	;; 3 bits of 0xFF8DAA, returns step-in-Hz in er1, and mirrors the active
	;; tier as a status bit in 0xFF201C that selects the downstream rounding
	;; algorithm: bit0 = 16-bit-divisor path at 0x27876 (rounds the VFO to the
	;; nearest multiple of er1, works for any step < 65536), bit1 = 100 kHz path,
	;; bit2 = 500 kHz path.
	;;
	;; Installed by overwriting the first 4 bytes at 0x27734 with  jmp @0x2f1a0.
	;; Entered by jump, exits via rts to the dispatcher's caller.
	;;
	;; Value map (ASCENDING — chosen so value 2 = 10 kHz = the stock 14-01
	;; factory-default byte, so the default becomes 10 kHz with no defaults-table
	;; edit). 1/5/10/50 kHz all use the bit0 16-bit rounding path:
	;;   0 -> 1 kHz   (0x3E8),   bit0
	;;   1 -> 5 kHz   (0x1388),  bit0
	;;   2 -> 10 kHz  (0x2710),  bit0   [default]
	;;   3 -> 50 kHz  (0xC350),  bit0
	;;   4 -> 100 kHz (0x186A0), bit1
	;;   5 -> 500 kHz (0x7A120), bit2
	.h8300s
	.text
_qd_step6:
	mov.b	@0xff8daa:32, r0l
	and.b	#0x07, r0l
	bclr	#0x0, @0xff201c:32
	bclr	#0x1, @0xff201c:32
	bclr	#0x2, @0xff201c:32
	cmp.b	#0x05, r0l		; 500 kHz
	bne	.Ls4
	mov.l	#500000, er1
	bset	#0x2, @0xff201c:32
	rts
.Ls4:
	cmp.b	#0x04, r0l		; 100 kHz
	bne	.Ls3
	mov.l	#100000, er1
	bset	#0x1, @0xff201c:32
	rts
.Ls3:
	cmp.b	#0x03, r0l		; 50 kHz
	bne	.Ls2
	mov.l	#50000, er1
	bset	#0x0, @0xff201c:32
	rts
.Ls2:
	cmp.b	#0x02, r0l		; 10 kHz
	bne	.Ls1
	mov.l	#10000, er1
	bset	#0x0, @0xff201c:32
	rts
.Ls1:
	cmp.b	#0x01, r0l		; 5 kHz
	bne	.Ls0
	mov.l	#5000, er1
	bset	#0x0, @0xff201c:32
	rts
.Ls0:					; value 0 -> 1 kHz
	mov.l	#1000, er1
	bset	#0x0, @0xff201c:32
	rts
