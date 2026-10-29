package main

// OCR engine loader: the page asks for /ocr/<file>; the first time we fetch it
// from a CDN and cache it next to the app, afterwards we serve the cached copy
// so recognition works offline. The screenshot itself is never sent anywhere —
// Tesseract runs in the page, on the user's machine.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ocrClient = &http.Client{Timeout: 90 * time.Second}
	ocrLock   sync.Mutex
)

const (
	ocrDist  = "https://cdn.jsdelivr.net/npm/tesseract.js@5.1.1/dist/"
	ocrDist2 = "https://unpkg.com/tesseract.js@5.1.1/dist/"
	ocrCore  = "https://cdn.jsdelivr.net/npm/tesseract.js-core@5.1.0/"
	ocrCore2 = "https://unpkg.com/tesseract.js-core@5.1.0/"
	ocrLang  = "https://tessdata.projectnaptha.com/4.0.0/"
)

// ocrUpstream maps a requested path to its CDN URLs (primary, fallback) and content type.
func ocrUpstream(p string) (urls []string, ctype string, ok bool) {
	if strings.Contains(p, "..") {
		return nil, "", false
	}
	switch {
	case p == "tesseract.min.js" || p == "worker.min.js":
		return []string{ocrDist + p, ocrDist2 + p}, "text/javascript; charset=utf-8", true
	case strings.HasPrefix(p, "core/"):
		rest := strings.TrimPrefix(p, "core/")
		if rest == "" {
			return nil, "", false
		}
		return []string{ocrCore + rest, ocrCore2 + rest}, ocrCtype(rest), true
	case strings.HasPrefix(p, "lang/"):
		rest := strings.TrimPrefix(p, "lang/")
		if rest == "" {
			return nil, "", false
		}
		return []string{ocrLang + rest}, "application/octet-stream", true
	}
	return nil, "", false
}

func ocrCtype(name string) string {
	switch {
	case strings.HasSuffix(name, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".gz"):
		return "application/octet-stream"
	}
	return "application/octet-stream"
}

func ocrCacheDir() string {
	d := filepath.Join(workDir(), "ocr")
	os.MkdirAll(filepath.Join(d, "core"), 0755)
	os.MkdirAll(filepath.Join(d, "lang"), 0755)
	return d
}

func serveOCR(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/ocr/")
	urls, ctype, ok := ocrUpstream(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	cache := filepath.Join(ocrCacheDir(), filepath.FromSlash(p))
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "max-age=31536000")
		w.Write(b)
		return
	}
	// one download at a time keeps parallel asset requests from racing on the same file
	ocrLock.Lock()
	defer ocrLock.Unlock()
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		w.Header().Set("Content-Type", ctype)
		w.Write(b)
		return
	}
	var data []byte
	var lastErr error
	for _, u := range urls {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := ocrClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = &ocrErr{resp.StatusCode, u}
			continue
		}
		data, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err == nil && len(data) > 0 {
			lastErr = nil
			break
		}
		lastErr = err
	}
	if lastErr != nil || len(data) == 0 {
		http.Error(w, "Не удалось скачать распознаватель — проверь интернет", 502)
		return
	}
	os.MkdirAll(filepath.Dir(cache), 0755)
	tmp := cache + ".tmp"
	if os.WriteFile(tmp, data, 0644) == nil {
		os.Rename(tmp, cache)
	}
	w.Header().Set("Content-Type", ctype)
	w.Write(data)
}

type ocrErr struct {
	code int
	url  string
}

func (e *ocrErr) Error() string { return "bad status" }

// ocrCached reports whether the core engine files are already downloaded.
func ocrCached() bool {
	d := ocrCacheDir()
	for _, f := range []string{"tesseract.min.js", "worker.min.js"} {
		if st, err := os.Stat(filepath.Join(d, f)); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}
