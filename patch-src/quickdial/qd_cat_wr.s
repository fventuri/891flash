	;; qd_cat_wr.s — CAT "EX 1401" write handler for QUICK DIAL, 6-option range.
	;;
	;; The stock CAT EX-write validator at 0x16586 is SHARED by every 3-option
	;; menu item (it parses the argument, rejects >=3, stores a 2-bit value with
	;; and#0xfc), so it must not be edited. This dedicated copy accepts 0..5 and
	;; stores a 3-bit value (and#0xf8); wired only to QUICK DIAL by repointing
	;; that item's CAT-write stub branch at 0x16c84 (stock: bra 0x16586) to
	;; jmp @0x2f2a0 (this routine). On entry er5 = 0xFF8DAA (set by the stub at
	;; 0x16c7e). Tail-jumps to the shared CAT completion points: 0x15A9A = ok,
	;; 0x15A96 = reject. 0x19716 = the shared ASCII->number parser.
	.h8300s
	.text
_qd_cat_wr:
	mov.l	#0xff81ce, er2		; CAT argument buffer
	jsr	@0x19716:24		; parse ASCII number -> 0xFF81CE (carry set = ok)
	bcs	1f
	jmp	@0x15a96:24		; parse error -> CAT reject
1:
	mov.b	@0xff81ce:32, r0l
	and.b	#0x0f, r0l
	cmp.b	#0x06, r0l		; accept 0..5 (stock shared path rejected >=3)
	bcs	2f
	jmp	@0x15a96:24		; value >= 6 -> CAT reject
2:
	mov.b	@er5, r0h		; er5 = 0xFF8DAA
	and.b	#0xf8, r0h		; preserve upper 5 bits (3-bit value field)
	or.b	r0h, r0l
	mov.b	r0l, @er5
	jmp	@0x15a9a:24		; CAT success
