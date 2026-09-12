	;; main_volt_inject.s — MAIN companion for the live panel voltmeter + CAT RM8.
	;;
	;; (1) _main_volt_inject — installed at 0x22280 (the operating-screen message
	;;     builder's `jsr @0x225a2`, which builds tag 0x20 + 182-byte general
	;;     display into scratch 0xFF2493).  We run the original build, take a FRESH
	;;     supply reading (sample ch4 0x2B360 -> scale, then bin->BCD 0x2A6CC) and
	;;     stash the two BCD bytes into general[180]=0xFF2548 / general[181]=0xFF2549.
	;;     Those ride to the panel (covered by MAIN's change-detect + checksum) and
	;;     land at panel 0xFF2A0F / 0xFF2A10 where the overlay reads them -> LIVE.
	;;
	;; (2) _rm8_hook — installed at the RM meter-fetch call 0x1c888 (was
	;;     `jsr @0x2b504`).  Adds CAT `RM8;` = supply voltage in DECIVOLTS: index 8
	;;     samples ch4 (0x2B360, returns decivolts 0-255, e.g. 138 = 13.8V) and
	;;     returns it in r1l like the stock meter dispatch; indices 0-7 fall through
	;;     to the original 0x2b504 unchanged.  Also requires the bounds check at
	;;     0x1c882 to be widened from `cmp #8` to `cmp #9` (patched separately).
	;;     Response: `RM8;` -> `RM8NNN;` (NNN = volts x10).

	.h8300sx
	.section .text
	.global _main_volt_inject
	.global _rm8_hook

_main_volt_inject:
	jsr	@0x225a2:24		; original: build operating msg into 0xFF2493
	jsr	@0x2b360:24		; sample ch4 -> scaled supply voltage in r0
	jsr	@0x2a6cc:24		; bin->BCD: r1h = 0x0T (tens), r1l = 0xUD
	mov.b	r1h,@0xff2548:32	; general[180] = tens BCD   -> panel 0xFF2A0F
	mov.b	r1l,@0xff2549:32	; general[181] = units.tenths -> panel 0xFF2A10
	rts

_rm8_hook:
	cmp.b	#8,r0l			; RM index (P1) in r0l
	beq	.Lvolt			; index 8 -> supply voltage
	jmp	@0x2b504:24		; indices 0-7 -> stock meter dispatch (returns r1l)
.Lvolt:
	jsr	@0x2b360:24		; sample ch4 -> decivolts in r0l
	mov.b	r0l,r1l			; return value in r1l (like 0x2b504)
	rts
