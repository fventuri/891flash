	;; qd_dec6.s — QUICK DIAL (Menu 14-01) value-decrement, 3-bit field, clamp 0.
	;;
	;; The stock decrement at 0x10d52 masks the value to 2 bits (and #0x3) and is
	;; SHARED, so it would corrupt a 3-bit value (>=4). This dedicated copy masks
	;; 3 bits and clamps to 0; wired only to QUICK DIAL by repointing that item's
	;; dec stub branch at 0x112e8 (stock: bra 0x10d52) to  jmp @0x2f180.
	;; On entry er1 = pointer to the value byte (0xFF8DAA). Low 3 bits = value;
	;; upper 5 bits preserved.
	.h8300s
	.text
_qd_dec6:
	mov.b	@er1, r0l
	mov.b	r0l, r0h
	and.b	#0xf8, r0h		; preserve bits 3-7
	and.b	#0x07, r0l		; value (0..7)
	beq	.Lw			; already 0 -> keep
	dec.b	r0l
.Lw:
	or.b	r0h, r0l
	mov.b	r0l, @er1
	rts
