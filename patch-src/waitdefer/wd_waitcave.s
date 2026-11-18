	;; wd_waitcave.s — "defer the [WAIT] overlay while the main dial is moving".
	;;
	;; Requested by M5TEA (FC-50 ATU + SOTA band-scanning): while you spin the
	;; VFO across a band the stock firmware paints "WAIT" over the frequency
	;; digits (the FC-50 isn't matched for the new freq yet), so you can't read
	;; where you are.  This routine hides WAIT WHILE the main dial is turning and
	;; for a short hold-off afterwards, then lets WAIT reappear once you settle —
	;; so it still tells you "tuner catching up, don't key up yet" before TX.
	;;
	;; Wiring: the stock status-overlay builder at 0x242b0 decides Hi-SWR / WAIT /
	;; nothing for the 10-char field that overlays the frequency.  Hi-SWR is fully
	;; handled at 0x242c2..0x242d2 (unaffected).  The WAIT decision block at
	;; 0x242de..0x242fa is replaced by  jmp @_wait_cave  (the ATU gate 0xff202b.0
	;; at 0x242d4 still guards entry, exactly as stock, so with no FC-50 attached
	;; this routine never runs and the radio is byte-identical to stock).  We
	;; replicate the stock WAIT decision and reuse its draw tails, gated by our own
	;; dial-motion detector.
	;;
	;; Motion detection: we do NOT use the firmware's 0xff200d.3 "dial moved" bit —
	;; on hardware that bit proved transient/noisy (it toggles even at rest).
	;; Instead we watch the raw dial counter 0xff23c4 (the byte the firmware itself
	;; diffs against its shadow 0xff23c5 at 0x2c7e8) against our OWN shadow at
	;; 0xff8f01: if it changed since last refresh the dial is turning.  A hold-off
	;; counter at 0xff8f00 keeps WAIT suppressed for K refreshes after the last
	;; change, so brief counter jitter can't flicker WAIT and WAIT returns a beat
	;; after you stop.  Both RAM bytes are unused (used RAM ends ~0xff8e37; stack
	;; top 0xffc000 grows down; neither byte is referenced anywhere in the image).
	;;
	;; K = hold-off in display refreshes after the last detent (the immediate
	;; below).  *** NOTE: untested on hardware — no FC-50 here to make WAIT appear —
	;; so K is an educated default; Mark may want to retune it. ***
	;;
	;; On entry er3 = the 10-char overlay buffer (set up by 0x242b0; preserved).
	;; r0/er1 are free scratch (the stock draw tail reloads r0h itself).
	.h8300s
	.text
	.globl	_wait_cave
_wait_cave:
	mov.b	@0xff23c4:32, r0l	; live main-dial counter
	mov.b	@0xff8f01:32, r0h	; our shadow of it
	cmp.b	r0h, r0l
	beq	.Lidle			; unchanged -> dial not turning this pass
	;; dial moving -> update shadow, arm hold-off, suppress WAIT
	mov.b	r0l, @0xff8f01:32
	mov.b	#0x0a, r0l		; K = 10 refreshes  (<-- tune here)
	mov.b	r0l, @0xff8f00:32
	rts
.Lidle:
	mov.b	@0xff8f00:32, r0l	; hold-off remaining?
	beq	.Lwait			; 0 -> dial settled, allow WAIT
	dec.b	r0l			; still winding down -> suppress
	mov.b	r0l, @0xff8f00:32
	rts
.Lwait:
	;; --- stock WAIT decision, replicated from 0x242de..0x242fa ---
	btst	#6, @0xff2029:32	; timed WAIT flag active?
	bne	.Ldraw
	btst	#2, @0xff2027:32	; WAIT state flag?
	beq	.Lnone
	btst	#0, @0xff2011:32	; draw text, or set override flag only?
	beq	.Lflag
.Ldraw:
	mov.l	#0x2431e, er1		; "WAIT" message pointer
	jmp	@0x24302:24		; stock tail: copy 10B -> buffer, set 0xff249b.1, rts
.Lflag:
	jmp	@0x2430a:24		; stock tail: set override flag only (no text), rts
.Lnone:
	rts
