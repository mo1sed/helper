// Vice Dolls Helper — desktop wrapper: serves the app on localhost, opens it in an
// app window of the chosen browser and keeps the data in vicedolls-data.json next to the exe.
package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var page []byte

const addr = "127.0.0.1:47615"

// appVersion is shown in the app; marker lets an update check it got a real build of this app.
const appVersion = "3.7"
const marker = "VDH-APP-BUILD-MARKER-7f3a91"
const url = "http://" + addr + "/"

// Supabase project for accounts + cloud backup. Filled in once the project exists
// (the anon key is public — it's protected by row-level security + per-user login).
// Env vars override for testing.
const supaURL = "https://gxzuqshogkkrhyajumjg.supabase.co"
const supaKey = "sb_publishable_AnV5zgfOPW0Dpp6bM7rqWg_CzD2hGTT"

func cloudURL() string {
	if v := os.Getenv("VDH_SUPA_URL"); v != "" {
		return v
	}
	return supaURL
}
func cloudKey() string {
	if v := os.Getenv("VDH_SUPA_KEY"); v != "" {
		return v
	}
	return supaKey
}

var supaClient = &http.Client{Timeout: 25 * time.Second}

// proxySupa forwards /api/supa/... from the page to the Supabase project, injecting
// the api key server-side. This avoids browser CORS and surfaces real errors.
func proxySupa(w http.ResponseWriter, r *http.Request) {
	base := cloudURL()
	if base == "" {
		http.Error(w, "cloud not configured", 503)
		return
	}
	up := base + strings.TrimPrefix(r.URL.Path, "/api/supa")
	if r.URL.RawQuery != "" {
		up += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequest(r.Method, up, r.Body)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	req.Header.Set("apikey", cloudKey())
	req.Header.Set("Content-Type", "application/json")
	if a := r.Header.Get("Authorization"); a != "" {
		req.Header.Set("Authorization", a)
	}
	if p := r.Header.Get("Prefer"); p != "" {
		req.Header.Set("Prefer", p)
	}
	resp, err := supaClient.Do(req)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

var (
	mu       sync.Mutex
	lastPing = time.Now()
	byeAt    time.Time
	dataPath string
	browser  *exec.Cmd
	bmu      sync.Mutex
	exePath  string
	dataRev  int // bumped when the data file is changed outside the page (quick paste)
)

type settings struct {
	Browser    string `json:"browser,omitempty"`
	QuickPaste *bool  `json:"quickPaste,omitempty"`
}

func loadSettings() settings {
	var st settings
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveSettings(st settings) {
	b, _ := json.Marshal(st)
	os.WriteFile(settingsPath(), b, 0644)
}

// quickPasteOn: Ctrl+right-click menu everywhere; the app then stays in the tray after the window closes.
func quickPasteOn() bool {
	st := loadSettings()
	return st.QuickPaste == nil || *st.QuickPaste
}

type msg struct {
	ID        string   `json:"id"`
	Folder    string   `json:"folder"`
	Sub       string   `json:"sub"`
	Text      string   `json:"text"`
	Label     string   `json:"label"`
	Order     *float64 `json:"order"`
	CreatedAt float64  `json:"createdAt"`
	Hot       bool     `json:"hot"`
}
type folder struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Hotkey string `json:"hotkey"`
	Subs   []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Hotkey string `json:"hotkey"`
	} `json:"subs"`
}

var defaultFolders = `[{"id":"mass","name":"Рассылки"},{"id":"stories","name":"Stories"},{"id":"priority","name":"Priority messages"},{"id":"spam","name":"👑 No spam"},{"id":"returns","name":"Возвраты"},{"id":"sexting","name":"Секстинг-сценарии","subs":[{"id":"tits","name":"Tits"},{"id":"pussy","name":"Pussy"}]}]`

// readLibrary returns folders and messages for the quick paste menu.
func readLibrary() ([]folder, []msg) {
	mu.Lock()
	b, _ := os.ReadFile(dataPath)
	mu.Unlock()
	var d struct {
		Folders  []folder `json:"folders"`
		Messages []msg    `json:"messages"`
	}
	json.Unmarshal(b, &d)
	if len(d.Folders) == 0 {
		json.Unmarshal([]byte(defaultFolders), &d.Folders)
	}
	return d.Folders, d.Messages
}

func (m msg) sortKey() float64 {
	if m.Order != nil {
		return *m.Order
	}
	return -m.CreatedAt
}

// markUsed stamps a message as used, keeping every other field untouched.
func markUsed(id string) {
	mu.Lock()
	defer mu.Unlock()
	b, err := os.ReadFile(dataPath)
	if err != nil {
		return
	}
	var d map[string]any
	if json.Unmarshal(b, &d) != nil {
		return
	}
	list, _ := d["messages"].([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		if m != nil && m["id"] == id {
			uses, _ := m["uses"].(float64)
			m["uses"] = uses + 1
			m["lastUsedAt"] = float64(time.Now().UnixMilli())
		}
	}
	out, err := json.Marshal(d)
	if err != nil {
		return
	}
	writeData(out)
	dataRev++
}

// keepUsage: a save from the window must not wipe a "used" mark that quick paste
// wrote a moment earlier, so usage counters only ever go up. Caller holds mu.
func keepUsage(incoming []byte) []byte {
	old, err := os.ReadFile(dataPath)
	if err != nil {
		return incoming
	}
	var cur, in map[string]any
	if json.Unmarshal(old, &cur) != nil || json.Unmarshal(incoming, &in) != nil {
		return incoming
	}
	prev := map[any]map[string]any{}
	if list, ok := cur["messages"].([]any); ok {
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				prev[m["id"]] = m
			}
		}
	}
	changed := false
	if list, ok := in["messages"].([]any); ok {
		for _, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			p := prev[m["id"]]
			if p == nil {
				continue
			}
			for _, k := range []string{"uses", "lastUsedAt"} {
				a, _ := m[k].(float64)
				if b, _ := p[k].(float64); b > a {
					m[k] = b
					changed = true
				}
			}
		}
	}
	if !changed {
		return incoming
	}
	out, err := json.Marshal(in)
	if err != nil {
		return incoming
	}
	dataRev++ // the window's copy is now behind; it reloads on its next ping
	return out
}

// writeData saves atomically and keeps one previous version. Caller holds mu.
func writeData(b []byte) error {
	tmp := dataPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	if _, err := os.Stat(dataPath); err == nil {
		os.Remove(dataPath + ".bak")
		os.Rename(dataPath, dataPath+".bak")
	}
	return os.Rename(tmp, dataPath)
}

func cleanupOld(exe string) {
	for i := 0; i < 20; i++ {
		if err := os.Remove(exe + ".old"); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(time.Second)
	}
}

func isMachO(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	// thin arm64/x86_64 (CF FA ED FE) or fat/universal (CA FE BA BE)
	return (b[0] == 0xCF && b[1] == 0xFA && b[2] == 0xED && b[3] == 0xFE) ||
		(b[0] == 0xCA && b[1] == 0xFE && b[2] == 0xBA && b[3] == 0xBE)
}

// applyUpdateDarwin replaces the server binaries inside the .app bundle, then exits;
// the WebView wrapper relaunches the server and reloads the page.
func applyUpdateDarwin(body []byte) (string, error) {
	if !bytes.HasPrefix(body, []byte("PK")) {
		return "Нужен .zip с приложением для Mac", io.EOF
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "Архив повреждён", err
	}
	dir := filepath.Dir(exePath) // <App>/Contents/MacOS
	found := false
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != "vd-arm64" && base != "vd-amd64" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		bin, err := io.ReadAll(io.LimitReader(rc, 100<<20))
		rc.Close()
		if err != nil || !isMachO(bin) || !bytes.Contains(bin, []byte(marker)) {
			continue
		}
		dst := filepath.Join(dir, base)
		tmp := dst + ".new"
		if os.WriteFile(tmp, bin, 0755) != nil {
			continue
		}
		if os.Rename(tmp, dst) == nil { // macOS allows replacing a running binary
			found = true
		}
	}
	if !found {
		return "Это не обновление Vice Dolls Helper для Mac", io.EOF
	}
	go func() { time.Sleep(400 * time.Millisecond); quit() }()
	return "", nil
}

// applyUpdate takes an uploaded update and swaps it in, then restarts.
func applyUpdate(body []byte) (string, error) {
	if runtime.GOOS == "darwin" {
		return applyUpdateDarwin(body)
	}
	bin := body
	if bytes.HasPrefix(body, []byte("PK")) {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return "Архив повреждён", err
		}
		bin = nil
		for _, f := range zr.File {
			if filepath.Ext(f.Name) == ".exe" {
				rc, err := f.Open()
				if err != nil {
					return "Архив повреждён", err
				}
				bin, err = io.ReadAll(io.LimitReader(rc, 100<<20))
				rc.Close()
				if err != nil {
					return "Архив повреждён", err
				}
				break
			}
		}
		if bin == nil {
			return "В архиве нет программы (.exe)", io.EOF
		}
	}
	if !bytes.HasPrefix(bin, []byte("MZ")) || !bytes.Contains(bin, []byte(marker)) {
		return "Это не файл Vice Dolls Helper", io.EOF
	}
	old := exePath + ".old"
	os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		return "Не удалось заменить программу: " + err.Error(), err
	}
	if err := os.WriteFile(exePath, bin, 0755); err != nil {
		os.Rename(old, exePath)
		return "Не удалось записать новую версию: " + err.Error(), err
	}
	go func() {
		time.Sleep(600 * time.Millisecond)
		cmd := exec.Command(exePath, "--after-update")
		cmd.Dir = workDir()
		cmd.Start()
		quit()
	}()
	return "", nil
}

type browserInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"-"`
	AppUI bool   `json:"app"` // supports a separate app window
}

type browserCand struct {
	id, name string
	app      bool
	paths    []string
}

func findBrowsers() []browserInfo {
	var cands []browserCand
	if runtime.GOOS == "darwin" {
		cands = macBrowsers()
	} else {
		cands = winBrowsers()
	}
	var out []browserInfo
	if wx := windowExe(); wx != "" { // own standalone window (WebView2) — no browser
		out = append(out, browserInfo{"window", "Отдельное окно (своё, без браузера)", wx, true})
	}
	for _, c := range cands {
		for _, p := range c.paths {
			if _, err := os.Stat(p); err == nil {
				out = append(out, browserInfo{c.id, c.name, p, c.app})
				break
			}
		}
	}
	out = append(out, browserInfo{"default", "Браузер по умолчанию (обычной вкладкой)", "", false})
	return out
}

// firstRealBrowserID is the fallback used if the standalone window can't open.
func firstRealBrowserID() string {
	for _, b := range findBrowsers() {
		if b.ID != "window" {
			return b.ID
		}
	}
	return "default"
}

func macBrowsers() []browserCand {
	return []browserCand{
		{"chrome", "Google Chrome", true, []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}},
		{"edge", "Microsoft Edge", true, []string{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}},
		{"brave", "Brave", true, []string{"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"}},
		{"vivaldi", "Vivaldi", true, []string{"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi"}},
		{"opera", "Opera", true, []string{"/Applications/Opera.app/Contents/MacOS/Opera"}},
		{"yandex", "Яндекс Браузер", true, []string{"/Applications/Yandex.app/Contents/MacOS/Yandex"}},
	}
}

func winBrowsers() []browserCand {
	pf, pf86, la := os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")
	return []browserCand{
		{"edge", "Microsoft Edge", true, []string{filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`), filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`)}},
		{"chrome", "Google Chrome", true, []string{filepath.Join(pf, `Google\Chrome\Application\chrome.exe`), filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`), filepath.Join(la, `Google\Chrome\Application\chrome.exe`)}},
		{"brave", "Brave", true, []string{filepath.Join(pf, `BraveSoftware\Brave-Browser\Application\brave.exe`), filepath.Join(pf86, `BraveSoftware\Brave-Browser\Application\brave.exe`), filepath.Join(la, `BraveSoftware\Brave-Browser\Application\brave.exe`)}},
		{"opera", "Opera", true, []string{filepath.Join(la, `Programs\Opera\opera.exe`), filepath.Join(pf, `Opera\opera.exe`)}},
		{"operagx", "Opera GX", true, []string{filepath.Join(la, `Programs\Opera GX\opera.exe`)}},
		{"yandex", "Яндекс Браузер", true, []string{filepath.Join(la, `Yandex\YandexBrowser\Application\browser.exe`), filepath.Join(pf, `Yandex\YandexBrowser\Application\browser.exe`)}},
		{"vivaldi", "Vivaldi", true, []string{filepath.Join(la, `Vivaldi\Application\vivaldi.exe`), filepath.Join(pf, `Vivaldi\Application\vivaldi.exe`)}},
		{"firefox", "Firefox (обычной вкладкой)", false, []string{filepath.Join(pf, `Mozilla Firefox\firefox.exe`), filepath.Join(pf86, `Mozilla Firefox\firefox.exe`)}},
	}
}

func settingsPath() string { return filepath.Join(workDir(), "settings.json") }

func chosenBrowser() string {
	st := loadSettings()
	list := findBrowsers()
	for _, b := range list {
		if b.ID == st.Browser {
			return b.ID
		}
	}
	return list[0].ID // first found: Edge on a stock Windows
}

// workDir is a neutral place outside the app folder, so neither this
// process nor the browser keeps the app folder busy (it must stay deletable).
func workDir() string {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, "Library", "Application Support")
		}
	default:
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			base = x
		} else if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".local", "share")
		}
	}
	d := filepath.Join(base, "ViceDollsHelper")
	os.MkdirAll(d, 0755)
	return d
}

func quit() {
	bmu.Lock()
	b := browser
	bmu.Unlock()
	if b != nil && b.Process != nil {
		b.Process.Kill()
	}
	nativeStop()
	os.Exit(0)
}

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	os.Chdir(workDir())
	// On Windows the data file sits next to the program (users expect it in the folder).
	// On macOS the program lives inside a read-only .app bundle, so it goes to Application Support.
	if runtime.GOOS == "darwin" {
		dataPath = filepath.Join(workDir(), "vicedolls-data.json")
	} else {
		dataPath = filepath.Join(dir, "vicedolls-data.json")
	}
	// carry over data from the earlier OnlyK build
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		if b, err := os.ReadFile(filepath.Join(dir, "onlyk-data.json")); err == nil {
			os.WriteFile(dataPath, b, 0644)
		}
	}

	exePath = exe
	extractWindowExe() // unpack the standalone-window helper (Windows)
	go cleanupOld(exe)

	ln, err := net.Listen("tcp", addr)
	afterUpdate := len(os.Args) > 1 && os.Args[1] == "--after-update"
	for i := 0; err != nil && afterUpdate && i < 30; i++ { // old version is still shutting down
		time.Sleep(500 * time.Millisecond)
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil { // already running: just open another window
		openWindow(false)
		return
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(page)
	})
	http.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastPing = time.Now()
		rev := dataRev
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"rev":` + strconv.Itoa(rev) + `}`))
	})
	http.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req struct {
				QuickPaste *bool `json:"quickPaste"`
			}
			json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
			st := loadSettings()
			if req.QuickPaste != nil {
				st.QuickPaste = req.QuickPaste
			}
			saveSettings(st)
			nativeRebind()
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"quickPaste": quickPasteOn(), "native": nativeAvailable, "saveServer": saveServer()})
	})
	http.HandleFunc("/api/cloud", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"configured": cloudURL() != "" && cloudKey() != ""})
	})
	http.HandleFunc("/api/supa/", proxySupa)
	http.HandleFunc("/api/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var req struct{ Name, Data string }
		if json.NewDecoder(io.LimitReader(r.Body, 80<<20)).Decode(&req) != nil {
			http.Error(w, "bad", 400)
			return
		}
		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			http.Error(w, "bad data", 400)
			return
		}
		path, err := saveToDownloads(req.Name, data)
		if err != nil {
			http.Error(w, "Не удалось сохранить файл", 500)
			return
		}
		if runtime.GOOS == "darwin" {
			exec.Command("open", "-R", path).Start()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"path": path})
	})
	http.HandleFunc("/api/hotkeys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hotkeyStatus())
	})
	http.HandleFunc("/ocr/", serveOCR)
	http.HandleFunc("/api/ocr-status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if ocrCached() {
			w.Write([]byte(`{"cached":true}`))
		} else {
			w.Write([]byte(`{"cached":false}`))
		}
	})
	http.HandleFunc("/api/translate", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ From, To, Q string }
		json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
		res, err := translate(req.From, req.To, req.Q)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]string{"error": "no_connection"})
			return
		}
		json.NewEncoder(w).Encode(res)
	})
	http.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		go func() { time.Sleep(200 * time.Millisecond); quit() }()
	})
	// the window was closed (or is reloading): quit soon unless it pings again
	http.HandleFunc("/api/bye", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		byeAt = time.Now()
		mu.Unlock()
		w.WriteHeader(204)
	})
	http.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": appVersion, "build": marker[len(marker)-6:]})
	})
	http.HandleFunc("/api/latest", handleLatest)
	http.HandleFunc("/api/selfupdate", handleSelfUpdate)
	http.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 150<<20))
		if err != nil {
			http.Error(w, "Не удалось прочитать файл", 400)
			return
		}
		mu.Lock()
		msg, err := applyUpdate(body)
		mu.Unlock()
		if err != nil {
			http.Error(w, msg, 400)
			return
		}
		w.WriteHeader(204)
	})
	http.HandleFunc("/api/browsers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var req struct {
				Browser string `json:"browser"`
			}
			json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
			ok := false
			for _, b := range findBrowsers() {
				ok = ok || b.ID == req.Browser
			}
			if !ok {
				http.Error(w, "unknown browser", 400)
				return
			}
			st := loadSettings()
			st.Browser = req.Browser
			saveSettings(st)
			w.WriteHeader(204)
			go func() { // reopen in the new browser, then close the old window
				time.Sleep(300 * time.Millisecond)
				bmu.Lock()
				old := browser
				bmu.Unlock()
				openWindow(true)
				if old != nil && old.Process != nil {
					old.Process.Kill()
				}
			}()
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"list": findBrowsers(), "current": chosenBrowser()})
	})
	http.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		lastPing = time.Now()
		switch r.Method {
		case http.MethodGet:
			b, err := os.ReadFile(dataPath)
			if err != nil || len(b) == 0 {
				b = []byte(`{"folders":null,"messages":[]}`)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Rev", strconv.Itoa(dataRev))
			w.Write(b)
		case http.MethodPut, http.MethodPost:
			b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			if err != nil || !json.Valid(b) {
				http.Error(w, "bad data", 400)
				return
			}
			b = keepUsage(b)
			if err := writeData(b); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			nativeRebind()
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	})

	go http.Serve(ln, nil)
	nativeStart()
	// Launched by the macOS WebView wrapper: just serve; the wrapper owns the window and lifecycle.
	if os.Getenv("VDH_SERVE_ONLY") == "1" {
		select {}
	}
	openWindow(true)

	// exit as soon as the window is closed
	for {
		time.Sleep(1 * time.Second)
		mu.Lock()
		idle := time.Since(lastPing)
		closing := !byeAt.IsZero() && lastPing.Before(byeAt) && time.Since(byeAt) > 6*time.Second
		mu.Unlock()
		if (closing || idle > 3*time.Minute) && !(nativeAvailable && quickPasteOn()) { // tray mode keeps running
			quit()
		}
	}
}

func openWindow(track bool) { openBrowser(chosenBrowser(), track) }

func openBrowser(id string, track bool) {
	var bi browserInfo
	for _, b := range findBrowsers() {
		if b.ID == id {
			bi = b
		}
	}
	var cmd *exec.Cmd
	switch {
	case bi.ID == "window": // our own WebView2 window, no browser
		cmd = exec.Command(bi.Path, url)
		// a fixed data folder so the window keeps localStorage (login session, theme) between launches
		cmd.Env = append(os.Environ(), "WEBVIEW2_USER_DATA_FOLDER="+filepath.Join(workDir(), "webview"))
	case bi.AppUI:
		profile := filepath.Join(workDir(), "window-"+bi.ID)
		cmd = exec.Command(bi.Path, "--app="+url, "--user-data-dir="+profile,
			"--window-size=1180,820", "--no-first-run", "--no-default-browser-check",
			"--disable-background-mode", "--disable-extensions",
			"--disable-features=msEdgeStartupBoost,BackgroundModeAllowRestart")
	case bi.Path != "":
		cmd = exec.Command(bi.Path, "-new-window", url)
		track = false // hands off to the running browser, can't follow it
	default:
		if runtime.GOOS == "darwin" {
			cmd = exec.Command("open", url)
		} else {
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		}
		track = false
	}
	cmd.Dir = workDir()
	if cmd.Start() != nil {
		if bi.ID == "window" { // couldn't even launch the window — use a browser
			openBrowser(firstRealBrowserID(), track)
		}
		return
	}
	bmu.Lock()
	if track {
		browser = cmd
	} else {
		browser = nil
	}
	bmu.Unlock()
	if !track {
		return
	}
	isWindow := bi.ID == "window"
	// the window's own process ends when the window is closed
	go func(start time.Time) {
		cmd.Wait()
		bmu.Lock()
		current := browser == cmd
		bmu.Unlock()
		if !current {
			return
		}
		if isWindow && time.Since(start) < 3*time.Second { // WebView2 missing/failed → fall back to a browser
			openBrowser(firstRealBrowserID(), track)
			return
		}
		if time.Since(start) > 5*time.Second && !(nativeAvailable && quickPasteOn()) {
			quit()
		}
	}(time.Now())
}

// saveServer reports whether the page should hand downloads to the server to write
// (the macOS WebView wrapper, where browser blob-downloads don't work) instead of
// doing a browser download itself.
func saveServer() bool {
	return runtime.GOOS == "darwin" && os.Getenv("VDH_SERVE_ONLY") == "1"
}

func sanitizeName(name string) string {
	name = filepath.Base(name)
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name)
	if name == "" || name == "." || name == ".." {
		name = "vicedolls.dat"
	}
	return name
}

// saveToDownloads writes data to the user's Downloads folder with a unique name.
func saveToDownloads(name string, data []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Downloads")
	os.MkdirAll(dir, 0755)
	name = sanitizeName(name)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, base+" ("+strconv.Itoa(i)+")"+ext)
	}
	return path, os.WriteFile(path, data, 0644)
}
