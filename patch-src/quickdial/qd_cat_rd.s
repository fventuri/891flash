	;; qd_cat_rd.s — CAT "EX 1401" read handler for QUICK DIAL, 6-option range.
	;;
	;; The stock CAT EX-read formatter entry at 0x17368 is SHARED by many 2-bit
	;; menu items (it masks the value with and#0x3, then falls into the shared
	;; digit formatter 0x17322), so it must not be edited. This dedicated copy
	;; masks 3 bits so values 3..5 report correctly; wired only to QUICK DIAL by
	;; repointing that item's CAT-read stub branch at 0x178bc (stock: bra 0x17368)
	;; to  jmp @0x2f2d0 (this routine). On entry er5 = 0xFF8DAA (set by the stub
	;; at 0x178b6). Tail-jumps to the shared formatter 0x17322 ('0'+val + ';').
	.h8300s
	.text
_qd_cat_rd:
	mov.b	@er5, r0l		; er5 = 0xFF8DAA
	and.b	#0x07, r0l		; 3-bit value (stock shared path used #0x3)
	jmp	@0x17322:24		; shared digit formatter
