// ft891patch - apply a JSON firmware patch to a Yaesu FT-891 firmware image,
// working directly on either a raw decoded .bin (what the mod author uses) or an
// official Yaesu .SFL file (what most people flash with YFSW023 on Windows).
//
// The JSON patch contains ONLY the mod author's own bytes (code-cave routines +
// redirect bytes) plus offsets and SHA-256 hashes - no Yaesu firmware is
// redistributed. You apply it to your own official firmware.
//
// Commands:
//
//	patch    apply a patch to a .bin or .SFL, producing a patched file of the same type
//	decode   convert a .SFL to a raw .bin
//	encode   convert a raw .bin to a .SFL
//	make     build/update a patch JSON from a baseline .bin and a modified .bin
//
// The SFL container format and its stream cipher were reverse-engineered from
// YFSW023.exe; see decode_sfl.py / encode_sfl.py for the annotated reference.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func newFlags(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// stringList collects a repeatable string flag (e.g. -patch a.json -patch b.json).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func today() string { return time.Now().Format("2006/01/02") }

const imageSize = 0x60000 // 384 KB flash address space (0xFF = erased)

// 43-byte stream-cipher table (VA 0x496c28 in YFSW023.exe).
var cipherTable = []byte{
	0x1e, 0x27, 0x1b, 0x12, 0x11, 0x16, 0x19, 0x17,
	0x24, 0x25, 0x29, 0x1c, 0x0d, 0x0b, 0x06, 0x04,
	0x03, 0x01, 0x02, 0x05, 0x07, 0x0c, 0x0e, 0x0f,
	0x18, 0x1d, 0x20, 0x22, 0x2a, 0x2b, 0x08, 0x09,
	0x0a, 0x10, 0x13, 0x14, 0x15, 0x1a, 0x1f, 0x21,
	0x28, 0x23, 0x26,
}

// ---------------------------------------------------------------------------
// Patch file schema (matches cave/ft891_voltmeter.patch.json)
// ---------------------------------------------------------------------------

type Region struct {
	Offset string `json:"offset"`           // e.g. "0x1c883"
	Hex    string `json:"hex"`              // replacement bytes, lowercase hex
	Expect string `json:"expect,omitempty"` // original bytes this overwrites (optional)
}

type Target struct {
	BaseName     string   `json:"base_name"`
	BaseSize     int      `json:"base_size"`
	BaseSHA256   string   `json:"base_sha256"`
	ResultSHA256 string   `json:"result_sha256"`
	Regions      []Region `json:"regions"`
}

type PatchFile struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Note    string            `json:"note"`
	Targets map[string]Target `json:"targets"`
}

// ---------------------------------------------------------------------------
// SFL cipher + Motorola S-record helpers
// ---------------------------------------------------------------------------

// cipherEncode returns (plain + table[ctr]) & 0xFF for each byte, advancing ctr.
func cipherEncode(s []byte, ctr int) ([]byte, int) {
	out := make([]byte, len(s))
	for i, b := range s {
		out[i] = byte(int(b)+int(cipherTable[ctr])) & 0xFF
		ctr = (ctr + 1) % len(cipherTable)
	}
	return out, ctr
}

// cipherDecode returns (enc - table[ctr]) & 0xFF for each byte, advancing ctr.
func cipherDecode(s []byte, ctr int) ([]byte, int) {
	out := make([]byte, len(s))
	for i, b := range s {
		out[i] = byte(int(b)-int(cipherTable[ctr])) & 0xFF
		ctr = (ctr + 1) % len(cipherTable)
	}
	return out, ctr
}

// buildSrec builds an uppercase-hex Motorola S-record with checksum.
func buildSrec(rtype byte, addr int, data []byte) string {
	var addrBytes []byte
	switch rtype {
	case '0', '1', '9':
		addrBytes = []byte{byte(addr >> 8), byte(addr)}
	case '2':
		addrBytes = []byte{byte(addr >> 16), byte(addr >> 8), byte(addr)}
	default:
		panic("unsupported record type")
	}
	count := len(addrBytes) + len(data) + 1
	body := append(append([]byte{}, addrBytes...), data...)
	sum := count
	for _, b := range body {
		sum += int(b)
	}
	chk := ^sum & 0xFF
	var sb strings.Builder
	sb.WriteByte('S')
	sb.WriteByte(rtype)
	fmt.Fprintf(&sb, "%02X", count)
	for _, b := range body {
		fmt.Fprintf(&sb, "%02X", b)
	}
	fmt.Fprintf(&sb, "%02X", chk)
	return sb.String()
}

// ---------------------------------------------------------------------------
// SFL decode / encode
// ---------------------------------------------------------------------------

var reCheckSum = regexp.MustCompile(`[0-9A-Fa-f]{8}(\()`)

// decodeSFL decodes an SFL file into a 0x60000 image plus the literal comment
// header lines (everything before the first "VS" record).
func decodeSFL(data []byte) (image []byte, header [][]byte, err error) {
	lines := bytes.Split(data, []byte("\r\n"))
	image = bytes.Repeat([]byte{0xFF}, imageSize)

	// Header = literal lines preceding the first VS record.
	vsIdx := -1
	for i, ln := range lines {
		if bytes.HasPrefix(ln, []byte("VS")) {
			vsIdx = i
			break
		}
	}
	if vsIdx < 0 {
		return nil, nil, fmt.Errorf("no VS record found - not a valid SFL file")
	}
	for _, ln := range lines[:vsIdx] {
		header = append(header, append([]byte{}, ln...))
	}

	ctr := 0
	vsSeen := false
	for _, ln := range lines {
		if len(ln) == 0 || ln[0] == ';' {
			continue
		}
		var prefix string
		var enc []byte
		switch {
		case bytes.HasPrefix(ln, []byte("VS")):
			vsSeen = true
			prefix = "S0"
			enc = ln[2:]
		case vsSeen:
			prefix = ""
			enc = ln
		default:
			continue
		}
		dec, nctr := cipherDecode(enc, ctr)
		ctr = nctr
		rec := prefix + string(dec)
		kind, addr, rdata, perr := parseSrec(rec)
		if perr != nil {
			return nil, nil, fmt.Errorf("bad S-record %q: %w", rec, perr)
		}
		switch kind {
		case "end":
			return image, header, nil
		case "data":
			for i, b := range rdata {
				if addr+i >= 0 && addr+i < len(image) {
					image[addr+i] = b
				}
			}
		}
	}
	return image, header, nil
}

// parseSrec parses a decoded S-record string.
func parseSrec(rec string) (kind string, addr int, data []byte, err error) {
	if len(rec) < 4 || rec[0] != 'S' {
		return "", 0, nil, fmt.Errorf("not an S-record")
	}
	rtype := rec[1]
	count64, err := strconv.ParseInt(rec[2:4], 16, 0)
	if err != nil {
		return "", 0, nil, err
	}
	count := int(count64)
	var addrLen, dataLen, addrEnd int
	switch rtype {
	case '0':
		return "skip", 0, nil, nil
	case '7', '8', '9':
		return "end", 0, nil, nil
	case '1':
		addrLen, dataLen = 4, count-3
	case '2':
		addrLen, dataLen = 6, count-4
	case '3':
		addrLen, dataLen = 8, count-5
	default:
		return "skip", 0, nil, nil
	}
	addrEnd = 4 + addrLen
	a, err := strconv.ParseInt(rec[4:addrEnd], 16, 0)
	if err != nil {
		return "", 0, nil, err
	}
	if dataLen < 0 || addrEnd+dataLen*2 > len(rec) {
		return "", 0, nil, fmt.Errorf("truncated data field")
	}
	data = make([]byte, dataLen)
	for i := 0; i < dataLen; i++ {
		b, err := strconv.ParseInt(rec[addrEnd+i*2:addrEnd+i*2+2], 16, 0)
		if err != nil {
			return "", 0, nil, err
		}
		data[i] = byte(b)
	}
	return "data", int(a), data, nil
}

// encodeSFL encodes a 0x60000 image into SFL bytes. If header is non-nil those
// literal comment lines are reused (with the [CHECK SUM] value refreshed);
// otherwise a fresh header is generated from meta.
func encodeSFL(image []byte, header [][]byte, meta headerMeta) []byte {
	var checksum uint32
	for _, b := range image {
		checksum += uint32(b)
	}

	// Store address 0 up to the last non-0xFF byte.
	end := 0
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] != 0xFF {
			end = i + 1
			break
		}
	}

	// Header lines (literal, not ciphered).
	var lines [][]byte
	if header != nil {
		for _, h := range header {
			if bytes.Contains(h, []byte("[CHECK SUM]")) {
				h = reCheckSum.ReplaceAll(h, []byte(fmt.Sprintf("%08X$1", checksum)))
			}
			lines = append(lines, append([]byte{}, h...))
		}
	} else {
		for _, s := range buildHeader(meta, checksum) {
			lines = append(lines, []byte(s))
		}
	}

	// Ciphered records; the counter runs continuously across all of them.
	ctr := 0

	// S0 "HEW     mot": the literal "VS" replaces the leading "S0".
	s0 := buildSrec('0', 0, []byte("HEW     mot"))
	enc, nctr := cipherEncode([]byte(s0[2:]), ctr)
	ctr = nctr
	lines = append(lines, append([]byte("VS"), enc...))

	// Data records: 16 bytes each; S1 below 0x10000, S2 at/above.
	for addr := 0; addr < end; addr += 16 {
		hi := addr + 16
		if hi > end {
			hi = end
		}
		rtype := byte('1')
		if addr >= 0x10000 {
			rtype = '2'
		}
		rec := buildSrec(rtype, addr, image[addr:hi])
		enc, nctr := cipherEncode([]byte(rec), ctr)
		ctr = nctr
		lines = append(lines, enc)
	}

	// S9 end record.
	end9 := buildSrec('9', 0, nil)
	enc9, _ := cipherEncode([]byte(end9), ctr)
	lines = append(lines, enc9)

	return append(bytes.Join(lines, []byte("\r\n")), '\r', '\n')
}

type headerMeta struct {
	filename    string
	firmVersion string
	rCode       string
	date        string
}

func buildHeader(m headerMeta, checksum uint32) []string {
	line := func(label, value string) string {
		inner := fmt.Sprintf("%-15s%s", label, value)
		return fmt.Sprintf("; ** %-36s **", inner)
	}
	stars := "; " + strings.Repeat("*", 42)
	rCode := m.rCode
	if rCode == "" {
		rCode = "YR"
	}
	return []string{
		stars,
		line("[FILENAME]", m.filename),
		line("[DATE]", m.date),
		line("[CPU]", "R5F61653RN50FPV"),
		line("[PRODUCT NAME]", "FT-891"),
		line("[FIRM Version]", m.firmVersion),
		line("[R code]", rCode),
		line("[CHECK SUM]", fmt.Sprintf("%08X(00000-5FFFF)", checksum)),
		stars,
		"", // blank separator before the VS record
	}
}

// ---------------------------------------------------------------------------
// Image helpers
// ---------------------------------------------------------------------------

func padImage(b []byte) ([]byte, error) {
	if len(b) > imageSize {
		return nil, fmt.Errorf("image is 0x%X bytes, larger than the 0x%X address space", len(b), imageSize)
	}
	img := make([]byte, imageSize)
	for i := range img {
		img[i] = 0xFF
	}
	copy(img, b)
	return img, nil
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// byteRange is a half-open [start, end) span of image bytes written by a patch.
type byteRange struct{ start, end int }

// applyRegions overwrites image in place with each region's bytes.
//
// If written != nil it is used to chain patches: a region overlapping a range
// already in *written is a conflict (two patches collide) and errors; each
// region's span is appended. When requireExpect is true (stacking onto an
// already-modified image, where the whole-image base hash no longer vouches for
// it) every region must carry `expect`, which is verified against the current
// bytes. A region's `expect` is always verified when present. label names the
// patch in error messages.
func applyRegions(image []byte, regions []Region, written *[]byteRange, requireExpect bool, label string) error {
	pre := ""
	if label != "" {
		pre = label + ": "
	}
	for _, r := range regions {
		off, err := strconv.ParseInt(strings.TrimSpace(r.Offset), 0, 64)
		if err != nil {
			return fmt.Errorf("%sbad offset %q: %w", pre, r.Offset, err)
		}
		data, err := hex.DecodeString(strings.TrimSpace(r.Hex))
		if err != nil {
			return fmt.Errorf("%sbad hex at %s: %w", pre, r.Offset, err)
		}
		start, end := int(off), int(off)+len(data)
		if start < 0 || end > len(image) {
			return fmt.Errorf("%sregion at %s (%d bytes) is out of range", pre, r.Offset, len(data))
		}
		if r.Expect != "" {
			exp, err := hex.DecodeString(strings.TrimSpace(r.Expect))
			if err != nil {
				return fmt.Errorf("%sbad expect hex at %s: %w", pre, r.Offset, err)
			}
			if len(exp) != len(data) {
				return fmt.Errorf("%sregion at %s: expect is %d bytes but hex is %d", pre, r.Offset, len(exp), len(data))
			}
			for i := range exp {
				if image[start+i] != exp[i] {
					return fmt.Errorf("%sregion at %s byte +%d: image has 0x%02x, patch expected 0x%02x "+
						"(wrong firmware version, or a conflicting patch already wrote here)", pre, r.Offset, i, image[start+i], exp[i])
				}
			}
		} else if requireExpect {
			return fmt.Errorf("%sregion at %s has no \"expect\" bytes, so it can't be safely stacked onto an already-patched image", pre, r.Offset)
		}
		if written != nil {
			for _, w := range *written {
				if start < w.end && end > w.start {
					return fmt.Errorf("%sregion at %s (…0x%x) overlaps a range already patched (0x%x…0x%x) — these patches conflict",
						pre, r.Offset, end, w.start, w.end)
				}
			}
			*written = append(*written, byteRange{start, end})
		}
		copy(image[off:], data)
	}
	return nil
}

// selectTarget picks the patch target whose base_sha256 matches the image.
func selectTarget(pf *PatchFile, baseHash, override string) (string, *Target, error) {
	if override != "" {
		t, ok := pf.Targets[override]
		if !ok {
			return "", nil, fmt.Errorf("no target %q in patch (have %s)", override, targetNames(pf))
		}
		if t.BaseSHA256 != "" && t.BaseSHA256 != baseHash {
			return "", nil, fmt.Errorf("input does not match target %q:\n  expected base %s\n  got          %s",
				override, t.BaseSHA256, baseHash)
		}
		return override, &t, nil
	}
	for name, t := range pf.Targets {
		if t.BaseSHA256 == baseHash {
			return name, &t, nil
		}
	}
	return "", nil, fmt.Errorf("input image does not match any target in this patch\n  its SHA-256 is %s\n  patch targets: %s\n  (wrong firmware version, or already patched?)",
		baseHash, targetNames(pf))
}

func targetNames(pf *PatchFile) string {
	var n []string
	for k := range pf.Targets {
		n = append(n, k)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

func loadPatch(path string) (*PatchFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf PatchFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &pf, nil
}

func detectType(path, override string) (string, error) {
	if override != "" {
		switch strings.ToLower(override) {
		case "bin", "sfl":
			return strings.ToLower(override), nil
		}
		return "", fmt.Errorf("-type must be bin or sfl")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sfl":
		return "sfl", nil
	case ".bin":
		return "bin", nil
	}
	return "", fmt.Errorf("cannot tell file type of %q from its extension; pass -type bin|sfl", path)
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

func cmdPatch(args []string) error {
	var inPath, outPath, typeOverride, targetOverride string
	var patchPaths stringList
	fs := newFlags("patch")
	fs.StringVar(&inPath, "in", "", "input firmware file (.bin or .SFL)")
	fs.Var(&patchPaths, "patch", "patch JSON file (repeat -patch to stack several)")
	fs.StringVar(&outPath, "out", "", "output file (defaults next to input with _patched suffix)")
	fs.StringVar(&typeOverride, "type", "", "force input type: bin|sfl")
	fs.StringVar(&targetOverride, "target", "", "force patch target (e.g. main|panel); default: auto by hash")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if inPath == "" || len(patchPaths) == 0 {
		return fmt.Errorf("patch requires -in and -patch")
	}
	ftype, err := detectType(inPath, typeOverride)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}

	var image []byte
	var header [][]byte
	if ftype == "sfl" {
		image, header, err = decodeSFL(raw)
		if err != nil {
			return err
		}
	} else {
		image, err = padImage(raw)
		if err != nil {
			return err
		}
	}

	baseHash := sha256hex(image)

	// Apply each patch in order, chaining onto the one running image. The first
	// patch selects the target (by base hash, or -target); later patches reuse
	// that target and rely on per-region `expect` (the base hash no longer
	// matches once the image is modified). Overlapping regions across patches
	// conflict and error.
	var written []byteRange
	tgt := targetOverride
	for _, pp := range patchPaths {
		pf, err := loadPatch(pp)
		if err != nil {
			return err
		}
		cur := sha256hex(image)
		var t *Target
		var name string
		if tgt == "" {
			name, t, err = selectTarget(pf, cur, "")
			if err != nil {
				return err
			}
			tgt = name
		} else {
			tt, ok := pf.Targets[tgt]
			if !ok {
				fmt.Printf("  skipping %s: no target %q (has %s)\n", filepath.Base(pp), tgt, targetNames(pf))
				continue
			}
			t, name = &tt, tgt
		}
		baseMatches := t.BaseSHA256 != "" && t.BaseSHA256 == cur
		if err := applyRegions(image, t.Regions, &written, !baseMatches, patchLabel(pf, pp)); err != nil {
			return err
		}
		res := sha256hex(image)
		// Verify the whole-image result only for a lone patch that declares one
		// (matches single-patch behavior; a chain's intermediate hashes won't match).
		if len(patchPaths) == 1 && t.ResultSHA256 != "" && t.ResultSHA256 != res {
			return fmt.Errorf("post-patch checksum mismatch for target %q:\n  expected %s\n  got      %s\n  (the patch did not produce the expected result)",
				name, t.ResultSHA256, res)
		}
		fmt.Printf("  applied %-28s target %q  -> %s\n", filepath.Base(pp), name, res[:16]+"…")
	}
	resultHash := sha256hex(image)
	name := tgt

	if outPath == "" {
		outPath = defaultOut(inPath, ftype)
	}

	var outData []byte
	if ftype == "sfl" {
		outData = encodeSFL(image, header, headerMeta{})
		// Safety: the re-encoded SFL must decode back to the patched image.
		if chk, _, derr := decodeSFL(outData); derr != nil || sha256hex(chk) != resultHash {
			return fmt.Errorf("internal error: re-encoded SFL does not round-trip")
		}
	} else {
		outData = image
	}
	if err := os.WriteFile(outPath, outData, 0644); err != nil {
		return err
	}

	fmt.Printf("Done: %d patch(es) on target %q (%s)\n", len(patchPaths), name, ftype)
	fmt.Printf("  base   SHA-256 %s\n", baseHash)
	fmt.Printf("  result SHA-256 %s\n", resultHash)
	fmt.Printf("  wrote %s\n", outPath)
	return nil
}

// patchLabel names a patch in messages: its "name" field, else the file's base name.
func patchLabel(pf *PatchFile, path string) string {
	if pf.Name != "" {
		return pf.Name
	}
	return filepath.Base(path)
}

func cmdDecode(args []string) error {
	var inPath, outPath string
	fs := newFlags("decode")
	fs.StringVar(&inPath, "in", "", "input .SFL")
	fs.StringVar(&outPath, "out", "", "output .bin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if inPath == "" {
		return fmt.Errorf("decode requires -in")
	}
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	image, _, err := decodeSFL(raw)
	if err != nil {
		return err
	}
	if outPath == "" {
		outPath = strings.TrimSuffix(inPath, filepath.Ext(inPath)) + ".bin"
	}
	if err := os.WriteFile(outPath, image, 0644); err != nil {
		return err
	}
	fmt.Printf("Decoded %s -> %s (0x%X bytes, SHA-256 %s)\n", inPath, outPath, len(image), sha256hex(image))
	return nil
}

func cmdEncode(args []string) error {
	var inPath, outPath, firm, rcode, date string
	fs := newFlags("encode")
	fs.StringVar(&inPath, "in", "", "input .bin")
	fs.StringVar(&outPath, "out", "", "output .SFL")
	fs.StringVar(&firm, "firm", "", "[FIRM Version] header field, e.g. V0110")
	fs.StringVar(&rcode, "rcode", "YR", "[R code] header field")
	fs.StringVar(&date, "date", "", "[DATE] header field (default: today)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if inPath == "" {
		return fmt.Errorf("encode requires -in")
	}
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	image, err := padImage(raw)
	if err != nil {
		return err
	}
	if outPath == "" {
		outPath = strings.TrimSuffix(inPath, filepath.Ext(inPath)) + ".SFL"
	}
	if date == "" {
		date = today()
	}
	meta := headerMeta{filename: filepath.Base(outPath), firmVersion: firm, rCode: rcode, date: date}
	out := encodeSFL(image, nil, meta)
	if err := os.WriteFile(outPath, out, 0644); err != nil {
		return err
	}
	fmt.Printf("Encoded %s -> %s\n", inPath, outPath)
	return nil
}

func cmdMake(args []string) error {
	var basePath, modPath, outPath, label, baseName string
	fs := newFlags("make")
	fs.StringVar(&basePath, "base", "", "baseline (original) .bin")
	fs.StringVar(&modPath, "mod", "", "modified .bin")
	fs.StringVar(&outPath, "patch", "", "patch JSON to create or update")
	fs.StringVar(&label, "label", "", "target label, e.g. main|panel")
	fs.StringVar(&baseName, "base-name", "", "base_name field (default: baseline filename)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if basePath == "" || modPath == "" || outPath == "" || label == "" {
		return fmt.Errorf("make requires -base, -mod, -patch and -label")
	}
	baseRaw, err := os.ReadFile(basePath)
	if err != nil {
		return err
	}
	modRaw, err := os.ReadFile(modPath)
	if err != nil {
		return err
	}
	base, err := padImage(baseRaw)
	if err != nil {
		return err
	}
	mod, err := padImage(modRaw)
	if err != nil {
		return err
	}

	// Contiguous change runs.
	var regions []Region
	i := 0
	for i < len(base) {
		if base[i] != mod[i] {
			j := i
			for j < len(base) && base[j] != mod[j] {
				j++
			}
			regions = append(regions, Region{
				Offset: fmt.Sprintf("0x%x", i),
				Expect: hex.EncodeToString(base[i:j]), // original bytes → keeps the patch stackable
				Hex:    hex.EncodeToString(mod[i:j]),
			})
			i = j
		} else {
			i++
		}
	}

	// Load or start the patch file, preserving the other target.
	pf := &PatchFile{
		Name:    "FT-891 firmware patch",
		Version: "1.0",
		Note:    "Contains ONLY the mod author's bytes (code-cave routines + redirect bytes). No Yaesu firmware. Apply to your own official firmware.",
		Targets: map[string]Target{},
	}
	if existing, err := loadPatch(outPath); err == nil {
		pf = existing
		if pf.Targets == nil {
			pf.Targets = map[string]Target{}
		}
	}
	if baseName == "" {
		baseName = filepath.Base(basePath)
	}
	pf.Targets[label] = Target{
		BaseName:     baseName,
		BaseSize:     imageSize,
		BaseSHA256:   sha256hex(base),
		ResultSHA256: sha256hex(mod),
		Regions:      regions,
	}

	buf, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, append(buf, '\n'), 0644); err != nil {
		return err
	}
	nb := 0
	for _, r := range regions {
		nb += len(r.Hex) / 2
	}
	fmt.Printf("Wrote target %q to %s: %d regions, %d bytes changed\n", label, outPath, len(regions), nb)
	return nil
}

// ---------------------------------------------------------------------------
// plumbing
// ---------------------------------------------------------------------------

func defaultOut(inPath, ftype string) string {
	ext := filepath.Ext(inPath)
	stem := strings.TrimSuffix(inPath, ext)
	if ftype == "sfl" {
		return stem + "_patched.SFL"
	}
	return stem + "_patched.bin"
}

func okword(ok bool) string {
	if ok {
		return "OK"
	}
	return "MISMATCH"
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "patch":
		err = cmdPatch(os.Args[2:])
	case "decode":
		err = cmdDecode(os.Args[2:])
	case "encode":
		err = cmdEncode(os.Args[2:])
	case "make":
		err = cmdMake(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ft891patch - apply a JSON firmware patch to an FT-891 .bin or .SFL

Usage:
  ft891patch patch  -patch p.json [-patch q.json ...] -in FILE(.bin|.SFL) [-out FILE] [-target main|panel] [-type bin|sfl]
  ft891patch decode -in FILE.SFL  [-out FILE.bin]
  ft891patch encode -in FILE.bin  [-out FILE.SFL] [-firm V0110] [-rcode YR] [-date YYYY/MM/DD]
  ft891patch make   -base ORIG.bin -mod MOD.bin -patch p.json -label main [-base-name NAME]

'patch' auto-detects .bin vs .SFL and auto-selects the matching target by the
base image's SHA-256. For an .SFL it decodes, patches, and re-encodes a valid
SFL (checksum refreshed) that YFSW023 will accept. Repeat -patch to STACK several
independent patches onto one image (applied in order; each is checked against the
bytes it expects, and patches that touch the same bytes are rejected as a conflict).
Apply only to firmware you obtained yourself; the patch carries no Yaesu code.
`)
}
