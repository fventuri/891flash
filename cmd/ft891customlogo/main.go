// ft891customlogo - read/replace the FT-891 power-on YAESU logo in the PANEL
// firmware. A Go port of cave/panel_logo.py (pure standard library).
//
// The logo is a 92x16 px, 1-bpp bitmap at file offset 0x9AE8 in the PANEL image
// (184 bytes). Routine 0x219C blits it to the LCD centre (cols 34-125, rows
// 16-31) and the boot code holds it ~35 frames. Storage: 92 columns, 2 bytes
// each -- byte[0] = lower 8 rows (px rows 8-15), byte[1] = upper 8 rows (px rows
// 0-7); within a byte, bit 0 = topmost pixel of its half.
//
// The PANEL .bin has NO image-level checksum, so patching needs no checksum fix.
// Image size must stay 393216 B.
//
// Pixel convention: a DARK pixel in the PNG (luminance < 128) = a LIT dot on the
// LCD = a set bit. decode and encode are exact inverses. If the result looks
// inverted on the radio, re-run with --invert.
//
// Usage:
//
//	ft891customlogo patch    --png mylogo.png --out mylogo.patch.json [--name "…"] [--invert]
//	ft891customlogo decode   [--in AH065_P_V0101.bin] --out logo.png [--scale 6] [--invert]
//	ft891customlogo encode   --png mylogo.png --out logo.bin [--invert]
//	ft891customlogo patchbin --in AH065_P_V0101.bin --png mylogo.png --out PANEL_custom.bin [--invert]
//
// The default "patch" emits a self-contained JSON patch straight from the PNG — no
// Yaesu firmware needed to make or share it (the offset, stock hash and original
// logo bytes are built in); apply it in the 891flash web app or with ft891patch.
// "patchbin" is the legacy path that splices the logo into a copy of a panel .bin,
// then flash:  ft891flash --panel /dev/ttyUSB0 PANEL_custom.bin
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

const (
	logoOff   = 0x9AE8
	cols      = 92
	rows      = 16
	blobSize  = cols * 2 // 184
	imageSize = 393216
)

// Constants for the "patchjson" command, so a JSON patch can be generated
// WITHOUT the original panel firmware. They describe the stock V0101 panel image:
// its SHA-256 (for version-gating at apply time) and the original 184 bytes at
// logoOff (used as each region's `expect`, which keeps the patch stackable in any
// order). Both are fixed properties of AH065_P_V0101.bin.
const (
	stockPanelName   = "AH065_P_V0101.bin"
	stockPanelSHA256 = "c1235055ea169461fe3377e0a13b917198ed8f7dc459a7acbd04f66694ca1aef"
	// original YAESU logo bytes at [logoOff, logoOff+blobSize) in the stock image.
	stockLogoHex = "c000c000c000c000c000c000c000c000c003c003c607c60fc61fc7bfc7fbc7f3c6e0c670c03bc01fc60fc607c603c703c783c7e0c6f0c678c05ec64fc64fc7ffc7ffc7fcc7c0c600c600c600c000c600c600c603c7c3c7ffc7ffc7ffc63fc623c6f3c6fbc67bc603c703c7c7c1cfc00fc001c600c780c39cc73ec63ec67bc673c673c673c673c773c3f3c1e6c0cfc003c000c003c1c3c3fbc7ffc7ffc60fc603c603c600c383c3e3c1ffc0ffc01fc003c003c003c000c000"
)

// JSON patch schema (mirrors cave/ft891_voltmeter.patch.json / patch.js).
type patchRegion struct {
	Offset string `json:"offset"`
	Expect string `json:"expect,omitempty"`
	Hex    string `json:"hex"`
}
type patchTarget struct {
	BaseName     string        `json:"base_name"`
	BaseSize     int           `json:"base_size"`
	BaseSHA256   string        `json:"base_sha256"`
	ResultSHA256 string        `json:"result_sha256,omitempty"`
	Regions      []patchRegion `json:"regions"`
}
type patchFile struct {
	Name    string                 `json:"name"`
	Version string                 `json:"version"`
	Note    string                 `json:"note"`
	Targets map[string]patchTarget `json:"targets"`
}

// decode turns firmware bytes into a cols x rows paletted image (black = lit).
func decode(data []byte, invert bool) *image.Paletted {
	pal := color.Palette{color.Gray{0xFF}, color.Gray{0x00}} // 0=white bg, 1=black lit
	img := image.NewPaletted(image.Rect(0, 0, cols, rows), pal)
	for x := 0; x < cols; x++ {
		b0 := data[logoOff+2*x]   // lower half, rows 8..15
		b1 := data[logoOff+2*x+1] // upper half, rows 0..7
		for bit := 0; bit < 8; bit++ {
			litTop := (b1>>bit)&1 == 1
			litBot := (b0>>bit)&1 == 1
			if invert {
				litTop = !litTop
				litBot = !litBot
			}
			if litTop {
				img.SetColorIndex(x, bit, 1)
			}
			if litBot {
				img.SetColorIndex(x, 8+bit, 1)
			}
		}
	}
	return img
}

// encode turns a cols x rows image into the 184-byte blob. Dark pixel = lit bit.
func encode(img image.Image, invert bool) ([]byte, error) {
	b := img.Bounds()
	if b.Dx() != cols || b.Dy() != rows {
		return nil, fmt.Errorf("PNG is %dx%d, must be %dx%d. Draw at exactly %dx%d (1 px = 1 LCD dot)",
			b.Dx(), b.Dy(), cols, rows, cols, rows)
	}
	lit := func(x, y int) bool {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		// ITU-R 601-2 luma on 8-bit channels (matches PIL "L").
		l := (r>>8)*299 + (g>>8)*587 + (bl>>8)*114
		dark := l/1000 < 128
		if invert {
			return !dark
		}
		return dark
	}
	out := make([]byte, 0, blobSize)
	for x := 0; x < cols; x++ {
		var b0, b1 byte
		for bit := 0; bit < 8; bit++ {
			if lit(x, bit) {
				b1 |= 1 << bit // upper half rows 0..7
			}
			if lit(x, 8+bit) {
				b0 |= 1 << bit // lower half rows 8..15
			}
		}
		out = append(out, b0, b1) // stored [lower][upper]
	}
	return out, nil
}

// scaleNearest enlarges a paletted image by an integer factor (nearest neighbor).
func scaleNearest(src *image.Paletted, factor int) *image.Paletted {
	if factor <= 1 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewPaletted(image.Rect(0, 0, w*factor, h*factor), src.Palette)
	for y := 0; y < h*factor; y++ {
		for x := 0; x < w*factor; x++ {
			dst.SetColorIndex(x, y, src.ColorIndexAt(x/factor, y/factor))
		}
	}
	return dst
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func cmdDecode(args []string) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	inp := fs.String("in", "", "panel .bin to read the logo from (default: the built-in stock YAESU logo)")
	out := fs.String("out", "", "output PNG")
	scale := fs.Int("scale", 6, "integer zoom for the output PNG")
	invert := fs.Bool("invert", false, "swap lit/unlit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("decode requires --out")
	}
	var data []byte
	if *inp == "" {
		// no firmware given → decode the embedded stock logo as the template.
		blob, _ := hex.DecodeString(stockLogoHex)
		data = make([]byte, logoOff+blobSize)
		copy(data[logoOff:], blob)
	} else {
		var err error
		if data, err = os.ReadFile(*inp); err != nil {
			return err
		}
		if len(data) < logoOff+blobSize {
			return fmt.Errorf("input is only %d bytes, too small to contain the logo at 0x%05X", len(data), logoOff)
		}
	}
	img := scaleNearest(decode(data, *invert), *scale)
	if err := savePNG(*out, img); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%dx%d @ %dx). Edit at native %dx%d; black = lit dot.\n",
		*out, cols, rows, *scale, cols, rows)
	return nil
}

func cmdEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	pngPath := fs.String("png", "", "92x16 PNG")
	out := fs.String("out", "", "output raw 184-byte blob")
	invert := fs.Bool("invert", false, "swap lit/unlit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pngPath == "" || *out == "" {
		return fmt.Errorf("encode requires --png and --out")
	}
	img, err := loadPNG(*pngPath)
	if err != nil {
		return err
	}
	blob, err := encode(img, *invert)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, blob, 0644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", *out, len(blob))
	return nil
}

func cmdPatchBin(args []string) error {
	fs := flag.NewFlagSet("patchbin", flag.ContinueOnError)
	inp := fs.String("in", "", "stock panel .bin")
	pngPath := fs.String("png", "", "92x16 PNG")
	out := fs.String("out", "", "patched panel .bin")
	invert := fs.Bool("invert", false, "swap lit/unlit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inp == "" || *pngPath == "" || *out == "" {
		return fmt.Errorf("patchbin requires --in, --png and --out")
	}
	data, err := os.ReadFile(*inp)
	if err != nil {
		return err
	}
	if len(data) != imageSize {
		fmt.Fprintf(os.Stderr, "warning: panel image is %d B, expected %d\n", len(data), imageSize)
	}
	if len(data) < logoOff+blobSize {
		return fmt.Errorf("input is only %d bytes, too small to contain the logo at 0x%05X", len(data), logoOff)
	}
	img, err := loadPNG(*pngPath)
	if err != nil {
		return err
	}
	blob, err := encode(img, *invert)
	if err != nil {
		return err
	}
	changed := 0
	for i := 0; i < blobSize; i++ {
		if data[logoOff+i] != blob[i] {
			changed++
		}
		data[logoOff+i] = blob[i]
	}
	if err := os.WriteFile(*out, data, 0644); err != nil {
		return err
	}
	fmt.Printf("patched logo at 0x%05X: %d/%d bytes changed.\n", logoOff, changed, blobSize)
	fmt.Printf("-> %s (%d B). No panel checksum to recompute.\n", *out, len(data))
	fmt.Printf("   flash: ft891flash --panel /dev/ttyUSB0 %s\n", *out)
	return nil
}

// cmdPatchJSON emits a JSON firmware patch straight from a PNG — no original
// panel firmware needed. The logo offset is hardcoded, and the stock hash and
// original logo bytes are embedded constants, so the patch is fully self-
// contained: base_sha256 version-gates it at apply time and each region's
// `expect` (the stock logo) keeps it stackable. result_sha256 is intentionally
// omitted (it would need the full 393216-byte image, and base+expect already
// pin the version and the exact bytes changed).
func cmdPatchJSON(args []string) error {
	fs := flag.NewFlagSet("patch", flag.ContinueOnError)
	pngPath := fs.String("png", "", "92x16 PNG")
	out := fs.String("out", "", "output .patch.json")
	name := fs.String("name", "FT-891 custom boot logo", "patch name (shown in the flasher)")
	invert := fs.Bool("invert", false, "swap lit/unlit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pngPath == "" || *out == "" {
		return fmt.Errorf("patch requires --png and --out")
	}
	img, err := loadPNG(*pngPath)
	if err != nil {
		return err
	}
	blob, err := encode(img, *invert)
	if err != nil {
		return err
	}
	p := patchFile{
		Name:    *name,
		Version: "1.0",
		Note: fmt.Sprintf("Replaces the power-on logo (92x16, 1-bpp) at panel file offset 0x%04X. "+
			"Panel image only; no checksum. Contains only the user's logo bitmap plus the "+
			"original bytes it overwrites (expect).", logoOff),
		Targets: map[string]patchTarget{
			"panel": {
				BaseName:   stockPanelName,
				BaseSize:   imageSize,
				BaseSHA256: stockPanelSHA256,
				Regions: []patchRegion{{
					Offset: fmt.Sprintf("0x%x", logoOff),
					Expect: stockLogoHex,
					Hex:    hex.EncodeToString(blob),
				}},
			},
		},
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("wrote %s — panel logo patch, %d-byte region @ 0x%05X (no firmware needed).\n", *out, blobSize, logoOff)
	fmt.Printf("   base_sha256 %s… (stock V0101 panel); result_sha256 omitted (base+expect verify).\n", stockPanelSHA256[:16])
	fmt.Printf("   apply it in the 891flash web app (step 4) or with ft891patch.\n")
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "decode":
		err = cmdDecode(os.Args[2:])
	case "encode":
		err = cmdEncode(os.Args[2:])
	case "patch", "patchjson": // default patch = firmware-free JSON patch
		err = cmdPatchJSON(os.Args[2:])
	case "patchbin": // legacy: splice into a copy of the panel .bin
		err = cmdPatchBin(os.Args[2:])
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
	fmt.Fprint(os.Stderr, `ft891customlogo - replace the FT-891 power-on logo in the PANEL firmware

Usage:
  ft891customlogo patch    --png logo.png --out logo.patch.json [--name "…"] [--invert]
  ft891customlogo decode    [--in PANEL.bin] --out logo.png [--scale 6] [--invert]
  ft891customlogo encode    --png logo.png --out logo.bin [--invert]
  ft891customlogo patchbin --in PANEL.bin --png logo.png --out PANEL_custom.bin [--invert]

The logo is a 92x16 1-bpp bitmap; draw/edit at exactly 92x16 (1 px = 1 LCD dot,
dark = lit).

'patch' (the default) emits a stackable JSON patch straight from the PNG WITHOUT
needing the original firmware — the offset, stock hash and original logo bytes are
built in. Apply it in the 891flash web app (step 4) or with ft891patch.
'decode' extracts a logo as an editing template (the built-in stock logo if no
--in is given). 'encode' emits the raw 184-byte blob.
'patchbin' is the legacy path: splice a PNG into a copy of the panel image (size
stays 393216 B, no checksum), then  ft891flash --panel /dev/ttyUSB0 PANEL_custom.bin
`)
}
