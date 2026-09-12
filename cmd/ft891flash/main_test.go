package main

import "testing"

// The trailing byte of each fixed packet is its checksum; verify our
// two's-complement checksum() reproduces them exactly.
func TestChecksumMatchesKnownPackets(t *testing.T) {
	cases := []struct {
		name string
		pkt  []byte
	}{
		{"echo-model", packetEchoModel},
		{"segment-sel", packetSegmentSel},
		{"config-main", packetConfig["main"]},
		{"config-panel", packetConfig["panel"]},
	}
	for _, c := range cases {
		body := c.pkt[:len(c.pkt)-1]
		want := c.pkt[len(c.pkt)-1]
		if got := checksum(body); got != want {
			t.Errorf("%s: checksum=0x%02X, want 0x%02X", c.name, got, want)
		}
	}
}

// End-of-transfer marker checksum (0x50 FF FF FF FF -> 0xB4).
func TestEndMarkerChecksum(t *testing.T) {
	end := []byte{xferCmd, 0xFF, 0xFF, 0xFF, 0xFF}
	if got := checksum(end); got != 0xB4 {
		t.Errorf("end-marker checksum=0x%02X, want 0xB4", got)
	}
}

// A data block frames to 134 bytes: [0x50][addr:4 BE][128 data][cksum],
// and the checksum makes the whole packet sum to 0 (mod 256).
func TestTransferBlockFraming(t *testing.T) {
	block := make([]byte, blockSize)
	for i := range block {
		block[i] = byte(i)
	}
	addr := 3 * blockSize // 0x180
	body := []byte{xferCmd, byte(addr >> 24), byte(addr >> 16), byte(addr >> 8), byte(addr)}
	body = append(body, block...)
	if len(body) != 133 {
		t.Fatalf("body len=%d, want 133", len(body))
	}
	cks := checksum(body)
	full := append(body, cks)
	if len(full) != 134 {
		t.Fatalf("packet len=%d, want 134", len(full))
	}
	var sum int
	for _, b := range full {
		sum += int(b)
	}
	if sum%256 != 0 {
		t.Errorf("packet does not sum to 0 mod 256 (got %d)", sum%256)
	}
	// Address encodes big-endian at 0x180.
	if full[1] != 0x00 || full[2] != 0x00 || full[3] != 0x01 || full[4] != 0x80 {
		t.Errorf("addr bytes = %02X %02X %02X %02X, want 00 00 01 80",
			full[1], full[2], full[3], full[4])
	}
}

// DSP zero-padding rounds up to a 1024 multiple.
func TestDSPPadLength(t *testing.T) {
	for _, n := range []int{1, 1023, 1024, 1025, 124260} {
		pad := (-n) % dspBlock
		pad = (pad + dspBlock) % dspBlock
		total := n + pad
		if total%dspBlock != 0 || total < n || total-n >= dspBlock {
			t.Errorf("n=%d pad=%d total=%d invalid", n, pad, total)
		}
	}
	// AH065_V0205.dat: 124260 -> 122 blocks.
	n := 124260
	pad := ((-n)%dspBlock + dspBlock) % dspBlock
	if (n+pad)/dspBlock != 122 {
		t.Errorf("124260 bytes -> %d blocks, want 122", (n+pad)/dspBlock)
	}
}
