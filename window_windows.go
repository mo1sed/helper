//go:build windows

package main

import (
	_ "embed"
	"os"
	"path/filepath"
)

// vdhwindow.exe is the standalone WebView2 window, built from ./winwindow and
// embedded here so the app still ships as a single file.
//
//go:embed vdhwindow.exe
var windowBin []byte

// extractWindowExe writes the embedded window helper next to the data dir, so the
// app can open its own window instead of a browser. Called once at startup.
func extractWindowExe() {
	p := windowExePath()
	if p == "" {
		return
	}
	// only rewrite when missing or a different size (cheap version check)
	if st, err := os.Stat(p); err == nil && st.Size() == int64(len(windowBin)) {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, windowBin, 0755) == nil {
		os.Rename(tmp, p)
	}
}

func windowExePath() string {
	d := workDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "vdhwindow.exe")
}

// windowExe returns the window helper path if it's ready to use.
func windowExe() string {
	p := windowExePath()
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}
