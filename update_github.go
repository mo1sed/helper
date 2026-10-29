package main

// Auto-update from public GitHub Releases: the app checks the latest release,
// compares versions, and (on request) downloads the asset and applies it with the
// existing update mechanism. Reads are public — no token needed on the client.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const ghOwner = "mo1sed"
const ghRepo = "helper"

var ghClient = &http.Client{Timeout: 40 * time.Second}

func verNum(s string) []int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	var out []int
	for _, p := range strings.Split(s, ".") {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}

// verNewer reports whether version a is newer than b (e.g. "3.5" > "3.4").
func verNewer(a, b string) bool {
	x, y := verNum(a), verNum(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		ai, bi := 0, 0
		if i < len(x) {
			ai = x[i]
		}
		if i < len(y) {
			bi = y[i]
		}
		if ai != bi {
			return ai > bi
		}
	}
	return false
}

// latestRelease returns the newest release's version, a downloadable .exe/.zip asset URL, and notes.
func latestRelease() (version, assetURL, notes string, err error) {
	u := "https://api.github.com/repos/" + ghOwner + "/" + ghRepo + "/releases/latest"
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ViceDollsHelper")
	resp, e := ghClient.Do(req)
	if e != nil {
		return "", "", "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", "", fmt.Errorf("github %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var d struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.NewDecoder(resp.Body).Decode(&d) != nil {
		return "", "", "", fmt.Errorf("bad json")
	}
	ver := d.TagName
	if ver == "" {
		ver = d.Name
	}
	var pick string
	for _, a := range d.Assets { // prefer a Windows .exe
		if strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
			pick = a.URL
			break
		}
	}
	if pick == "" {
		for _, a := range d.Assets {
			if strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
				pick = a.URL
				break
			}
		}
	}
	return ver, pick, d.Body, nil
}

func handleLatest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ver, assetURL, notes, err := latestRelease()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "version": strings.TrimPrefix(strings.TrimSpace(ver), "v"),
		"current": appVersion, "newer": verNewer(ver, appVersion), "url": assetURL, "notes": notes,
	})
}

func handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	// only download from our own release path (no arbitrary URLs)
	if !strings.HasPrefix(req.URL, "https://github.com/"+ghOwner+"/"+ghRepo+"/") {
		http.Error(w, "недопустимая ссылка", 400)
		return
	}
	rq, _ := http.NewRequest("GET", req.URL, nil)
	rq.Header.Set("User-Agent", "ViceDollsHelper")
	resp, err := ghClient.Do(rq)
	if err != nil {
		http.Error(w, "скачивание: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		http.Error(w, fmt.Sprintf("скачивание: http %d", resp.StatusCode), 502)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		http.Error(w, "чтение", 500)
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
}
