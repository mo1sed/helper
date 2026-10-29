//go:build windows

// Standalone WebView2 window for Vice Dolls Helper. Launched by the main app with
// the local URL as the first argument. Gives the app its own window & icon
// (no browser), using the WebView2 runtime that ships with Windows 10/11.
package main

import (
	"os"

	"github.com/jchv/go-webview2"
)

func main() {
	url := "http://127.0.0.1:47615/"
	if len(os.Args) > 1 && os.Args[1] != "" {
		url = os.Args[1]
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "Vice Dolls Helper",
			Width:  1180,
			Height: 820,
			Center: true,
		},
	})
	if w == nil {
		return
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
}
