// 891flash — self-hosting launcher for the WebSerial FT-891 flasher.
//
// The web app (web/index.html + web/src/*.js + manifest + favicon) is embedded
// into this binary with go:embed and served on http://localhost:<port>/. That
// gives an offline, field-ready flasher: one self-contained executable, no
// Python, no Node, no Internet. WebSerial needs a secure context, which over
// plain HTTP means localhost — so the server binds 127.0.0.1 on purpose
// (reaching it via a LAN IP would load a page that can never open the port).
//
// The browser does all the serial I/O; this process only serves static files.
//
// Bundled patches: on startup the launcher looks for a "patches" directory next
// to the executable (override with -patches DIR). If found, it serves those
// .json patches at /patches/ and a listing at /patches.json, and the web app
// loads them automatically — so an extracted release "just works": run the
// binary, point step 2 at your Yaesu firmware folder, and the bundled patches
// are already there to tick. No firmware is embedded or required to launch.
//
// Build:  go build -o 891flash ./cmd/891flash
// Run:    ./891flash            (opens the browser)
//
//	./891flash -no-open   (just print the URL)
//	./891flash -port 9000
//	./891flash -patches /path/to/patches
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

//go:embed all:web
var webFS embed.FS

func main() {
	port := flag.Int("port", 8891, "localhost port to serve on")
	noOpen := flag.Bool("no-open", false, "do not open the browser automatically")
	patchesDir := flag.String("patches", "", "directory of .json patches to offer (default: ./patches next to the binary)")
	flag.Parse()

	// http.FileServer guesses Content-Type from the extension; pin the few the
	// PWA needs so ES modules load and the manifest is served correctly on every
	// platform (Windows' registry-based guessing doesn't know several of these).
	mime.AddExtensionType(".js", "text/javascript")
	mime.AddExtensionType(".mjs", "text/javascript")
	mime.AddExtensionType(".json", "application/json")
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mime.AddExtensionType(".svg", "image/svg+xml")

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Offline/local tool: never let the HTTP layer hold a stale copy.
		w.Header().Set("Cache-Control", "no-store")
		fileServer.ServeHTTP(w, r)
	})

	// Bundled patches (optional). Serve them so the web app can auto-load.
	if dir := resolvePatchesDir(*patchesDir); dir != "" {
		names := listPatchJSON(dir)
		fmt.Printf("  bundled patches: %d from %s\n", len(names), dir)
		pf := http.StripPrefix("/patches/", http.FileServer(http.Dir(dir)))
		mux.HandleFunc("/patches/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			pf.ServeHTTP(w, r)
		})
		mux.HandleFunc("/patches.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(listPatchJSON(dir)) // re-read: pick up edits
		})
	}

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("could not bind %s (%v). Is the port already in use? Try -port.", addr, err)
	}
	url := fmt.Sprintf("http://localhost:%d/", *port)

	fmt.Println("  891flash — FT-891 firmware flasher")
	fmt.Println("  serving at " + url)
	fmt.Println("  open it in Chrome/Edge or Firefox 151+, connect the radio's CAT (Enhanced) USB port")
	fmt.Println("  press Ctrl+C to stop")
	if !*noOpen {
		openBrowser(url)
	}
	log.Fatal(http.Serve(ln, mux))
}

// resolvePatchesDir returns the patches directory to serve, or "" if none.
// Priority: the -patches flag, then ./patches next to the executable, then
// ./patches in the working directory.
func resolvePatchesDir(flagVal string) string {
	if flagVal != "" {
		if isDir(flagVal) {
			return flagVal
		}
		log.Printf("warning: -patches %q is not a directory; ignoring", flagVal)
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(exe), "patches"); isDir(cand) {
			return cand
		}
	}
	if isDir("patches") {
		return "patches"
	}
	return ""
}

// listPatchJSON returns the sorted .json filenames in dir (no path, no README).
func listPatchJSON(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// openBrowser best-effort launches the default browser; failure is non-fatal
// (the URL is printed either way).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
