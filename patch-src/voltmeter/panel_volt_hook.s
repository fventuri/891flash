	;; panel_volt_hook.s — trampoline entry for the panel voltage overlay.
	;; Installed at the LCD flush entry 0x5A72 (jmp @cave). Runs the C renderer,
	;; then the displaced watchdog-kick (0x83c6) and falls back into the flush at
	;; 0x5A76. Placed FIRST in the cave (.text.hook) so the cave base IS this.
	;; er0-er3 are caller-saved and the flush re-inits its own regs, so no saves.

	.h8300sx
	.section .text.hook,"ax"
	.global _volt_hook
	.extern _volt_overlay

_volt_hook:
	jsr	@_volt_overlay:24	; draw "NN.NV" into the framebuffer
	jsr	@0x83c6:24		; displaced: watchdog kick
	jmp	@0x5a76:24		; continue the flush
