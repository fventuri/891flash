	;; qd_inc6.s — QUICK DIAL (Menu 14-01) value-increment, 3-bit field, clamp 5.
	;;
	;; The stock increment at 0x10410 clamps a 2-bit field to max 2 and is SHARED
	;; by six menu items, so it must not be edited. This dedicated copy widens the
	;; field to 3 bits (six options) and clamps to 5; wired only to QUICK DIAL by
	;; repointing that item's inc stub branch at 0x1075e (stock: bra 0x10410) to
	;; jmp @0x2f160 (this routine). On entry er1 = pointer to the value byte
	;; (0xFF8DAA, set by the stub at 0x10758). Low 3 bits = value; upper 5 bits
	;; are unrelated flags and are preserved. No wrap at max.
	.h8300s
	.text
_qd_inc6:
	mov.b	@er1, r0l
	mov.b	r0l, r0h
	and.b	#0xf8, r0h		; preserve bits 3-7
	and.b	#0x07, r0l		; value (0..7)
	cmp.b	#0x05, r0l
	bcc	.Lmax			; value >= 5 -> clamp to 5
	inc.b	r0l
	bra	.Lw
.Lmax:
	mov.b	#0x05, r0l
.Lw:
	or.b	r0h, r0l
	mov.b	r0l, @er1
	rts
