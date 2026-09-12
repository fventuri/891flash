// ft891flash - Yaesu FT-891 firmware writer for Linux, Windows and macOS.
//
// A Go port of ft891_flash.py. Writes the MAIN, front-panel, or DSP firmware
// over the radio's serial programming interface. Protocol reverse-engineered
// from YFSW023.exe; see the Python reference and PROTOCOL.md for details.
//
// Usage:
//
//	ft891flash --main  /dev/ttyUSB0 AH065_M_V0110.bin
//	ft891flash --panel /dev/ttyUSB0 AH065_P_V0101.bin
//	ft891flash --dsp   /dev/ttyUSB0 AH065_V0205.dat
//	ft891flash --main --dry-run /dev/ttyUSB0     (read-only handshake, no write)
//	ft891flash --list                            (list serial ports)
//
// On Windows the port is a COM name, e.g.  ft891flash --main COM5 fw.bin
//
// Entry (physical, done before running):
//
//	MAIN  - rear PGM slide switch LEFT, then power on (blank display).
//	PANEL - slide switch RIGHT (normal); hold [PWR/LOCK]+[BAND]+[FAST] at
//	        power-on (TX/BUSY LED glows orange).
//	DSP   - slide switch RIGHT (normal); hold [PWR/LOCK]+[F]+[C] at
//	        power-on (display reads "DSP WRITE MODE").
//
// WARNING: Experimental. MAIN, PANEL and DSP are all verified on hardware.
// Use at your own risk and keep the original updater available.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"go.bug.st/serial"
)

const (
	firmwareSize = 0x60000 // 384 KB (same for MAIN and PANEL)
	blockSize    = 128     // bytes per data block
	baudSync     = 19200   // boot ROM's fixed initial rate (no negotiation)

	xferCmd = 0x50 // data-transfer command byte (0x496c14)
	xferAck = 0x06 // data-transfer ACK byte     (0x496c54)

	dspBaud  = 38400
	dspSync  = 0x07
	dspBlock = 1024
	dspAck   = 0x06
)

// Post-handshake program baud (step 6). MAIN switches up to 57600; PANEL stays
// at 19200 (YFSW023 FUN_0048e7e8: 0xe100 vs 0x4b00, per target flag @+0x3cc).
var programBaud = map[string]int{"main": 57600, "panel": 19200}

// Line format: 8-N-2 (TWO stop bits) for every target. YFSW023 sets this
// unconditionally (FUN_0048ddcc); a USB capture confirms CP210x
// SET_LINE_CTL=0x0802. It matters for PANEL: that CPU is flashed by the MAIN
// relaying each byte to it over SSI, and the extra stop bit's idle time lets the
// relay keep up with a 134-byte block sent back-to-back. MAIN tolerates 8-N-1
// but we match the official updater anyway.
const stopBits = serial.TwoStopBits

// Expected model ID bytes in response to 0x20 (ASCII "4C03" = FT-891).
var modelID = []byte{0x34, 0x43, 0x30, 0x33}

// Step 3: 7-byte model-echo packet  [cmd] [len] ["4C03"] [cksum]
var packetEchoModel = []byte{0x10, 0x04, 0x34, 0x43, 0x30, 0x33, 0x12}

// Step 4: 4-byte segment-select packet  [cmd] [0x01] [0x00] [cksum]
var packetSegmentSel = []byte{0x11, 0x01, 0x00, 0xEE}

// Step 5: 10-byte memory/baud config packet - differs by target (YFSW023
// FUN_0048e6b4, tables at 0x953f0 / 0x953fc). Byte 9 is the checksum.
var packetConfig = map[string][]byte{
	"main":  {0x3F, 0x07, 0x02, 0x40, 0x04, 0x56, 0x02, 0x04, 0x02, 0x16},
	"panel": {0x3F, 0x07, 0x00, 0xC0, 0x04, 0x56, 0x02, 0x04, 0x02, 0x98},
}

// ---------------------------------------------------------------------------
// Low-level serial helpers
// ---------------------------------------------------------------------------

func openPort(name string, baud int) (serial.Port, error) {
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: stopBits,
	}
	return serial.Open(name, mode)
}

// isEINTR reports whether err is (or wraps) an "interrupted system call". Go's
// scheduler delivers SIGURG for async goroutine preemption, which interrupts
// blocking syscalls. go.bug.st/serial retries EINTR inside Read but NOT inside
// Write or Drain, so we handle those here (as CPython/pyserial do transparently).
func isEINTR(err error) bool {
	return errors.Is(err, syscall.EINTR) ||
		strings.Contains(err.Error(), "interrupted system call")
}

func send(p serial.Port, data ...byte) error {
	// Write everything, retrying on EINTR and advancing past partial writes.
	// write(2) reports EINTR only when zero bytes were written, so retrying the
	// remaining slice never re-sends bytes the radio already received.
	for len(data) > 0 {
		n, err := p.Write(data)
		data = data[n:]
		if err != nil {
			if isEINTR(err) {
				continue
			}
			return err
		}
	}
	// Drain (tcdrain) waits until fully transmitted (pyserial flush()); it too
	// can be interrupted by a signal.
	for {
		err := p.Drain()
		if err == nil {
			return nil
		}
		if !isEINTR(err) {
			return err
		}
	}
}

// recv reads exactly n bytes within the timeout. It returns (nil, nil) on
// timeout (the Python None), and a non-nil error only for real I/O errors.
func recv(p serial.Port, n int, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, n)
	for len(buf) < n {
		rem := time.Until(deadline)
		if rem <= 0 {
			break
		}
		if err := p.SetReadTimeout(rem); err != nil {
			return nil, err
		}
		tmp := make([]byte, n-len(buf))
		m, err := p.Read(tmp)
		if err != nil {
			return nil, err
		}
		if m == 0 {
			break // timeout
		}
		buf = append(buf, tmp[:m]...)
	}
	if len(buf) != n {
		return nil, nil
	}
	return buf, nil
}

// drainTail reads up to max bytes with a short timeout, for diagnostics.
func drainTail(p serial.Port, max int, timeout time.Duration) []byte {
	_ = p.SetReadTimeout(timeout)
	buf := make([]byte, max)
	n, err := p.Read(buf)
	if err != nil || n <= 0 {
		return nil
	}
	return buf[:n]
}

func expectAck(p serial.Port, context string, timeout time.Duration) error {
	resp, err := recv(p, 1, timeout)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("%s: timeout waiting for ACK", context)
	}
	if resp[0] != 0x06 {
		return fmt.Errorf("%s: expected 0x06, got 0x%02X", context, resp[0])
	}
	return nil
}

func hexSpace(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02x", v)
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Protocol steps (MAIN / PANEL)
// ---------------------------------------------------------------------------

func step0Sync(portName, target string) (serial.Port, error) {
	entry := map[string]string{
		"main":  "rear slide switch LEFT, supply ON (blank display, do NOT press POWER)",
		"panel": "supply ON while holding [PWR/LOCK]+[BAND]+[FAST] (TX/BUSY LED orange)",
	}[target]
	fmt.Printf("  Syncing at %d baud ... ", baudSync)
	port, err := openPort(portName, baudSync)
	if err != nil {
		return nil, err
	}
	_ = port.ResetInputBuffer()

	got := false
	for i := 0; i < 20; i++ {
		if err := send(port, 0x00); err != nil {
			port.Close()
			return nil, err
		}
		// Listen ~20 ms for any reply (the Python in_waiting probe).
		resp, err := recv(port, 1, 20*time.Millisecond)
		if err != nil {
			port.Close()
			return nil, err
		}
		if resp != nil {
			_ = port.ResetInputBuffer()
			got = true
			break
		}
	}
	if !got {
		port.Close()
		return nil, fmt.Errorf(
			"no response at 19200 baud. The radio is not in a fresh programming "+
				"state. Power-cycle it: supply OFF ~10 s, then %s, then retry", entry)
	}

	time.Sleep(100 * time.Millisecond)
	if err := send(port, 0x55); err != nil {
		port.Close()
		return nil, err
	}
	resp, err := recv(port, 1, 1000*time.Millisecond)
	if err != nil {
		port.Close()
		return nil, err
	}
	if resp != nil && resp[0] == 0xE6 {
		fmt.Println("OK (0xE6)")
		return port, nil
	}
	got2 := "timeout"
	if resp != nil {
		got2 = fmt.Sprintf("0x%02X", resp[0])
	}
	port.Close()
	return nil, fmt.Errorf(
		"sync failed at 19200 baud: expected 0xE6, got %s. "+
			"Power-cycle into a fresh programming state and retry", got2)
}

func step1ModelID(port serial.Port) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, 0x20); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	resp, err := recv(port, 18, 500*time.Millisecond)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("model ID query: timeout")
	}
	if resp[0] != 0x30 {
		return fmt.Errorf("unexpected inquiry response (no 0x30 marker): %x", resp)
	}
	nchars := int(resp[3])
	if 4+nchars > len(resp) {
		nchars = len(resp) - 4
	}
	code := resp[4 : 4+nchars]
	if len(code) < 4 || !bytesEqual(code[:4], modelID) {
		return fmt.Errorf("model ID mismatch: device code starts %x, expected %x (full code %q)",
			code, modelID, string(code))
	}
	fmt.Printf("  Device: %s  (FT-891 '4C03' confirmed)\n", string(code))
	return nil
}

func step2Readiness(port serial.Port) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, 0x27); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	resp, err := recv(port, 5, 500*time.Millisecond)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("readiness query: timeout")
	}
	if resp[2] != 0x00 || resp[3] != 0x80 {
		return fmt.Errorf("readiness check failed: %x", resp)
	}
	fmt.Println("  Ready")
	return nil
}

func sendPacketExpectAck(port serial.Port, packet []byte, context string) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, packet...); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := expectAck(port, context, 500*time.Millisecond); err != nil {
		return err
	}
	fmt.Println("  ACK")
	return nil
}

func step6BaudSwitch(port serial.Port, portName, target string) (serial.Port, error) {
	pb := programBaud[target]
	port.Close()
	time.Sleep(50 * time.Millisecond)
	newPort, err := openPort(portName, pb)
	if err != nil {
		return nil, err
	}
	time.Sleep(50 * time.Millisecond)
	if err := send(newPort, 0x06); err != nil {
		newPort.Close()
		return nil, err
	}
	time.Sleep(2 * time.Millisecond)
	if err := expectAck(newPort, "Baud-rate echo", 500*time.Millisecond); err != nil {
		newPort.Close()
		return nil, err
	}
	fmt.Printf("  Now at %d baud\n", pb)
	return newPort, nil
}

func step7Erase(port serial.Port) error {
	fmt.Print("  Erasing flash (up to 5 s) ... ")
	if err := send(port, 0x40); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := expectAck(port, "Erase trigger", 5000*time.Millisecond); err != nil {
		return err
	}
	fmt.Println("OK")
	return nil
}

func stepPollStatus(port serial.Port, label string) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, 0x4F); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	resp, err := recv(port, 5, 500*time.Millisecond)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("status poll %s: timeout", label)
	}
	if resp[3] != 0x00 {
		return fmt.Errorf("status poll %s: error code 0x%02X", label, resp[3])
	}
	fmt.Printf("  OK (%x)\n", resp)
	return nil
}

// stepRedirectable handles 0x4C/0x4D which reply 0x06, or 0xCC/0xCD + a byte.
func stepRedirectable(port serial.Port, cmd, redirect byte, context string) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, cmd); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	resp, err := recv(port, 1, 500*time.Millisecond)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("%s: timeout", context)
	}
	if resp[0] == redirect {
		sub, _ := recv(port, 1, 100*time.Millisecond)
		subStr := "??"
		if sub != nil {
			subStr = fmt.Sprintf("%02X", sub[0])
		}
		return fmt.Errorf("%s: redirect 0x%02X n=0x%s", context, redirect, subStr)
	}
	if resp[0] != 0x06 {
		return fmt.Errorf("%s: unexpected 0x%02X", context, resp[0])
	}
	fmt.Println("  ACK")
	return nil
}

func stepWriteConfirm(port serial.Port) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, 0x43); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := expectAck(port, "Write confirm", 500*time.Millisecond); err != nil {
		return err
	}
	fmt.Println("  ACK")
	return nil
}

func stepTransfer(port serial.Port, firmware []byte) error {
	total := len(firmware) / blockSize
	fmt.Printf("  Sending %d × %d B blocks:\n", total, blockSize)
	for i := 0; i < total; i++ {
		addr := i * blockSize
		block := firmware[addr : addr+blockSize]
		body := make([]byte, 0, 5+blockSize)
		body = append(body, xferCmd)
		body = append(body, byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr))
		body = append(body, block...) // 133 bytes so far
		cks := checksum(body)
		if err := send(port, append(body, cks)...); err != nil { // 134 bytes
			return err
		}
		resp, err := recv(port, 1, 10000*time.Millisecond)
		if err != nil {
			return err
		}
		if resp == nil || resp[0] != xferAck {
			got := "timeout"
			if resp != nil {
				got = fmt.Sprintf("0x%02X", resp[0])
			}
			tail := drainTail(port, 16, 200*time.Millisecond)
			hint := "; nothing further"
			if len(tail) > 0 {
				hint = "; late bytes: " + hexSpace(tail)
			}
			return fmt.Errorf("block %d/%d (addr 0x%05X): no ACK (%s)%s", i+1, total, addr, got, hint)
		}
		if (i+1)%256 == 0 || i == total-1 {
			pct := (i + 1) * 100 / total
			fmt.Printf("    %3d%%  addr 0x%05X\n", pct, addr+blockSize)
		}
	}

	// End-of-transfer marker: address 0xFFFFFFFF, no data.
	end := []byte{xferCmd, 0xFF, 0xFF, 0xFF, 0xFF}
	if err := send(port, append(end, checksum(end))...); err != nil {
		return err
	}
	resp, err := recv(port, 1, 10000*time.Millisecond)
	if err != nil {
		return err
	}
	if resp == nil || resp[0] != xferAck {
		got := "timeout"
		if resp != nil {
			got = fmt.Sprintf("0x%02X", resp[0])
		}
		fmt.Printf("  Warning: end-of-transfer ack was %s (expected 0x06); completion poll will confirm.\n", got)
	}
	return nil
}

func stepCompletion(port serial.Port) error {
	time.Sleep(10 * time.Millisecond)
	if err := send(port, 0x4B); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	resp, err := recv(port, 7, 500*time.Millisecond)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("completion poll: timeout")
	}
	addr := uint32(resp[2])<<24 | uint32(resp[3])<<16 | uint32(resp[4])<<8 | uint32(resp[5])
	fmt.Printf("  Radio reports write address: 0x%08X\n", addr)
	return nil
}

// checksum is the two's-complement sum-to-zero over body: (-sum) & 0xFF.
func checksum(body []byte) byte {
	var sum int
	for _, b := range body {
		sum += int(b)
	}
	return byte(-sum)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// DSP write path
// ---------------------------------------------------------------------------

func dspSyncStep(portName string) (serial.Port, error) {
	fmt.Printf("  Syncing at %d baud ... ", dspBaud)
	port, err := openPort(portName, dspBaud)
	if err != nil {
		return nil, err
	}
	_ = port.ResetInputBuffer()
	got := "timeout"
	for attempt := 0; attempt < 3; attempt++ {
		if err := send(port, dspSync); err != nil {
			port.Close()
			return nil, err
		}
		resp, err := recv(port, 1, 3000*time.Millisecond)
		if err != nil {
			port.Close()
			return nil, err
		}
		if resp != nil && resp[0] == dspAck {
			fmt.Printf("OK (0x%02X)\n", dspAck)
			return port, nil
		}
		if resp != nil {
			got = fmt.Sprintf("0x%02X", resp[0])
		} else {
			got = "timeout"
		}
		if attempt < 2 {
			fmt.Printf("retry (%s) ... ", got)
			_ = port.ResetInputBuffer()
		}
	}
	port.Close()
	return nil, fmt.Errorf(
		"DSP sync failed: sent 0x%02X, expected 0x%02X, got %s. "+
			"Enter DSP WRITE MODE (PGM switch RIGHT; hold [PWR/LOCK]+[F]+[C] at power-on) and retry",
		dspSync, dspAck, got)
}

func dspTransfer(port serial.Port, data []byte) error {
	if pad := (-len(data)) % dspBlock; pad != 0 {
		pad = (pad + dspBlock) % dspBlock
		data = append(append([]byte{}, data...), make([]byte, pad)...)
	}
	total := len(data) / dspBlock
	fmt.Printf("  Sending %d × %d B blocks:\n", total, dspBlock)
	for i := 0; i < total; i++ {
		block := data[i*dspBlock : (i+1)*dspBlock]
		if err := send(port, block...); err != nil {
			return err
		}
		resp, err := recv(port, 1, 3000*time.Millisecond)
		if err != nil {
			return err
		}
		if resp == nil || resp[0] != dspAck {
			got := "timeout"
			if resp != nil {
				got = fmt.Sprintf("0x%02X", resp[0])
			}
			tail := drainTail(port, 16, 200*time.Millisecond)
			hint := "; nothing further"
			if len(tail) > 0 {
				hint = "; late bytes: " + hexSpace(tail)
			}
			return fmt.Errorf("block %d/%d: no ACK (%s)%s", i+1, total, got, hint)
		}
		if (i+1)%16 == 0 || i == total-1 {
			fmt.Printf("    %3d%%  block %d/%d\n", (i+1)*100/total, i+1, total)
		}
	}
	return nil
}

func writeDSP(portName, firmwarePath string, dryRun bool) error {
	var firmware []byte
	if !dryRun {
		var err error
		firmware, err = os.ReadFile(firmwarePath)
		if err != nil {
			return err
		}
	}

	fmt.Println("FT-891 DSP firmware writer  —  DSP WRITE MODE")
	if dryRun {
		fmt.Printf("Port: %s   *** DRY RUN: sync only, no data written ***\n", portName)
	} else {
		fmt.Printf("Port: %s   Firmware: %s  (%d bytes)\n", portName, firmwarePath, len(firmware))
	}
	fmt.Println()

	fmt.Println("Step 0: DSP sync")
	port, err := dspSyncStep(portName)
	if err != nil {
		return err
	}
	defer port.Close()

	if dryRun {
		fmt.Println()
		fmt.Println(strings.Repeat("=", 50))
		fmt.Println("DRY RUN OK — radio answered the DSP sync (0x07 -> 0x06). No data")
		fmt.Println("was written. Power-cycle back to normal use, or re-run without")
		fmt.Println("--dry-run to perform the real write.")
		return nil
	}

	fmt.Println("Step 1: Firmware data transfer")
	if err := dspTransfer(port, firmware); err != nil {
		return err
	}
	fmt.Println("  Transfer complete")

	fmt.Println()
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("DSP firmware written successfully.")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Turn the power supply OFF, then disconnect USB.")
	fmt.Println("  2. Power-cycle normally and confirm via Menu 18-02 [DSP VERSION].")
	return nil
}

// ---------------------------------------------------------------------------
// MAIN / PANEL write path
// ---------------------------------------------------------------------------

func writeFirmware(portName, firmwarePath, target string, dryRun bool) error {
	var firmware []byte
	if !dryRun {
		var err error
		firmware, err = os.ReadFile(firmwarePath)
		if err != nil {
			return err
		}
		if len(firmware) != firmwareSize {
			return fmt.Errorf("expected %d bytes (%d KB), got %d",
				firmwareSize, firmwareSize/1024, len(firmware))
		}
	}

	modeDesc := map[string]string{"main": "PGM SW mode", "panel": "[BAND]+[FAST] mode"}[target]
	fmt.Printf("FT-891 %s firmware writer  —  %s\n", strings.ToUpper(target), modeDesc)
	if dryRun {
		fmt.Printf("Port: %s   *** DRY RUN: read-only handshake, no erase/write ***\n", portName)
	} else {
		fmt.Printf("Port: %s   Firmware: %s  (%d KB)\n", portName, firmwarePath, len(firmware)/1024)
	}
	fmt.Println()

	fmt.Println("Step 0: Sync at 19200 baud")
	port, err := step0Sync(portName, target)
	if err != nil {
		return err
	}
	defer func() {
		if port != nil {
			port.Close()
		}
	}()

	fmt.Println("Step 1: Model identification")
	if err := step1ModelID(port); err != nil {
		return err
	}

	fmt.Println("Step 2: Readiness query")
	if err := step2Readiness(port); err != nil {
		return err
	}

	if dryRun {
		fmt.Println()
		fmt.Println(strings.Repeat("=", 50))
		fmt.Println("DRY RUN OK — radio reached programming mode and answered the")
		fmt.Println("read-only handshake (steps 0-2). No erase or write was sent.")
		fmt.Println("The flasher and serial timing work against this radio.")
		fmt.Println()
		fmt.Println("Power-cycle back to normal use, or re-run without")
		fmt.Println("--dry-run to perform the real write.")
		return nil
	}

	fmt.Println("Step 3: Echo model code")
	if err := sendPacketExpectAck(port, packetEchoModel, "Echo model"); err != nil {
		return err
	}

	fmt.Println("Step 4: Segment select")
	if err := sendPacketExpectAck(port, packetSegmentSel, "Segment select"); err != nil {
		return err
	}

	fmt.Println("Step 5: Memory/baud config")
	if err := sendPacketExpectAck(port, packetConfig[target], "Config"); err != nil {
		return err
	}

	fmt.Printf("Step 6: Switch to %d baud\n", programBaud[target])
	port, err = step6BaudSwitch(port, portName, target)
	if err != nil {
		return err
	}

	fmt.Println("Step 7: Trigger flash erase")
	if err := step7Erase(port); err != nil {
		return err
	}

	fmt.Println("Step 8a: Status poll #1")
	if err := stepPollStatus(port, "#1"); err != nil {
		return err
	}

	fmt.Println("Step 8b: Status poll #2")
	if err := stepPollStatus(port, "#2"); err != nil {
		return err
	}

	fmt.Println("Step 9: Sector write command")
	if err := stepRedirectable(port, 0x4C, 0xCC, "Sector write"); err != nil {
		return err
	}

	fmt.Println("Step 10: Page finalise")
	if err := stepRedirectable(port, 0x4D, 0xCD, "Page finalise"); err != nil {
		return err
	}

	fmt.Println("Step 8c: Status poll #3")
	if err := stepPollStatus(port, "#3"); err != nil {
		return err
	}

	fmt.Println("Step 11: Write confirmation")
	if err := stepWriteConfirm(port); err != nil {
		return err
	}

	fmt.Println("Step 12: Firmware data transfer")
	if err := stepTransfer(port, firmware); err != nil {
		return err
	}
	fmt.Println("  Transfer complete")

	fmt.Println("Step 13: Completion poll")
	if err := stepCompletion(port); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("Firmware written successfully.")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Turn the power supply OFF.")
	if target == "main" {
		fmt.Println("  2. Slide the rear PGM switch back to the RIGHT.")
	}
	fmt.Println("  Then disconnect the USB cable and power-cycle normally.")
	return nil
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	var (
		fMain  = flag.Bool("main", false, "Write the MAIN CPU firmware (rear PGM switch LEFT at power-on)")
		fPanel = flag.Bool("panel", false, "Write the front-panel firmware ([PWR/LOCK]+[BAND]+[FAST] at power-on)")
		fDSP   = flag.Bool("dsp", false, "Write the DSP firmware (hold [PWR/LOCK]+[F]+[C] at power-on -> \"DSP WRITE MODE\"; firmware is the raw .dat)")
		fDry   = flag.Bool("dry-run", false, "Run only the read-only handshake and stop BEFORE any erase/write")
		fList  = flag.Bool("list", false, "List available serial ports and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *fList {
		listPorts()
		return
	}

	targets := 0
	target := ""
	if *fMain {
		targets++
		target = "main"
	}
	if *fPanel {
		targets++
		target = "panel"
	}
	if *fDSP {
		targets++
		target = "dsp"
	}
	if targets != 1 {
		fail("exactly one of --main, --panel or --dsp is required")
	}

	args := flag.Args()
	if len(args) < 1 {
		fail("serial port argument is required (e.g. /dev/ttyUSB0 or COM5)")
	}
	portName := args[0]
	firmwarePath := ""
	if len(args) >= 2 {
		firmwarePath = args[1]
	}
	if !*fDry && firmwarePath == "" {
		fail("firmware argument is required unless --dry-run is given")
	}

	var err error
	if target == "dsp" {
		err = writeDSP(portName, firmwarePath, *fDry)
	} else {
		err = writeFirmware(portName, firmwarePath, target, *fDry)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nERROR: %v\n", err)
		os.Exit(1)
	}
}

func listPorts() {
	ports, err := serial.GetPortsList()
	if err != nil {
		fail(err.Error())
	}
	if len(ports) == 0 {
		fmt.Println("No serial ports found.")
		return
	}
	fmt.Println("Available serial ports:")
	for _, p := range ports {
		fmt.Println("  " + p)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	fmt.Fprintln(os.Stderr, "")
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, `ft891flash - write FT-891 MAIN, panel or DSP firmware over serial

Usage:
  ft891flash --main  PORT firmware.bin
  ft891flash --panel PORT firmware.bin
  ft891flash --dsp   PORT firmware.dat
  ft891flash --main --dry-run PORT      (read-only handshake, no erase/write)
  ft891flash --list                     (list serial ports)

PORT is /dev/ttyUSB0 (Linux), /dev/tty.usbserial-XXXX (macOS) or COMx (Windows).
Decode an .SFL to .bin first, or apply a patch, with ft891patch. MAIN/PANEL
firmware must be exactly 384 KB; DSP firmware is the raw .dat.

Flags must come before the PORT/firmware arguments.
`)
}
