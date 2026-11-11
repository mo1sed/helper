package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Audience presets: lists of folder names (as shown in the OnlyMonster audience picker) that the
// quick-paste menu can tick for you by typing each name into the picker's search box.

type audPreset struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Skip []string `json:"skip"` // "do not send to"
	Send []string `json:"send"` // "send to" (optional)
	// Mode "macro": instead of typing names, replay recorded keys (down, up, enter, space, tab, wait:300)
	Mode  string   `json:"mode"`
	Macro []string `json:"macro"`
}

type audConfig struct {
	Presets []audPreset `json:"presets"`
	Key     string      `json:"key"`   // how a found folder is ticked: enter | down_enter | space | tab
	Pause   int         `json:"pause"` // ms between actions
	Wait    int         `json:"wait"`  // ms to wait after that click, so the list has time to load (default 1500)
	Click   *bool       `json:"click"` // click where the menu was opened before replaying (default on)
}

func readAudience() audConfig {
	mu.Lock()
	b, _ := os.ReadFile(dataPath)
	mu.Unlock()
	var d struct {
		Audience audConfig `json:"audience"`
	}
	json.Unmarshal(b, &d)
	if d.Audience.Key == "" {
		d.Audience.Key = "down_enter"
	}
	return d.Audience
}

// audStep is one thing the macro does: type a name, press a key, or wait.
type audStep struct {
	Kind string // "paste" | "key" | "ctrl" | "wait"
	Text string
	VK   uint16
	Ms   int
}

const (
	vkBack   = 0x08
	vkTab    = 0x09
	vkReturn = 0x0D
	vkSpace  = 0x20
	vkDown   = 0x28
	vkSelAll = 0x41 // 'A'
	vkUp     = 0x26
)

var macroKeys = map[string]uint16{"down": vkDown, "up": vkUp, "enter": vkReturn, "space": vkSpace, "tab": vkTab}

// macroSteps turns recorded tokens ("down", "enter", "wait:300" ...) into steps. Between two keys
// without an explicit wait a short default pause is used, so slow pickers keep up.
func macroSteps(tokens []string, pause int) []audStep {
	p := audPause(pause) / 3
	var st []audStep
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if strings.HasPrefix(t, "wait:") {
			ms := 0
			for _, c := range t[5:] {
				if c < '0' || c > '9' {
					ms = 0
					break
				}
				ms = ms*10 + int(c-'0')
			}
			if ms > 5000 {
				ms = 5000
			}
			if ms > 0 {
				st = append(st, audStep{Kind: "wait", Ms: ms})
			}
			continue
		}
		if vk, ok := macroKeys[t]; ok {
			st = append(st, audStep{Kind: "key", VK: vk}, audStep{Kind: "wait", Ms: p})
		}
	}
	return st
}

// stepsFor builds the macro for one preset and list kind.
func (p audPreset) stepsFor(kind string, key string, pause int) ([]audStep, int) {
	if p.Mode == "macro" {
		if kind == "send" {
			return nil, 0
		}
		return macroSteps(p.Macro, pause), len(p.Macro)
	}
	list := p.Skip
	if kind == "send" {
		list = p.Send
	}
	n := audNames(list)
	return audienceSteps(n, key, pause), len(n)
}

// ---- recording (Windows polls the keyboard; other systems cannot, the UI builds macros by buttons) ----

var (
	recMu     sync.Mutex
	recActive bool
	recTokens []string
)

func audRecState() (bool, []string) {
	recMu.Lock()
	defer recMu.Unlock()
	return recActive, append([]string(nil), recTokens...)
}

func audWait(w int) int {
	if w <= 0 {
		return 1500
	}
	if w > 10000 {
		return 10000
	}
	return w
}

func audPause(p int) int {
	if p < 100 {
		return 350
	}
	if p > 3000 {
		return 3000
	}
	return p
}

func audNames(list []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range list {
		n = strings.TrimSpace(n)
		if n != "" && !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	return out
}

// audienceSteps builds the macro for the given folder names: for each one, type it into the
// search box, tick it, then clear the search box.
func audienceSteps(names []string, key string, pause int) []audStep {
	p := audPause(pause)
	var st []audStep
	for _, n := range audNames(names) {
		st = append(st, audStep{Kind: "paste", Text: n}, audStep{Kind: "wait", Ms: p})
		switch key {
		case "down_enter":
			st = append(st, audStep{Kind: "key", VK: vkDown}, audStep{Kind: "wait", Ms: 80}, audStep{Kind: "key", VK: vkReturn})
		case "space":
			st = append(st, audStep{Kind: "key", VK: vkSpace})
		case "tab":
			st = append(st, audStep{Kind: "key", VK: vkTab})
		default:
			st = append(st, audStep{Kind: "key", VK: vkReturn})
		}
		st = append(st, audStep{Kind: "wait", Ms: p / 2},
			audStep{Kind: "ctrl", VK: vkSelAll}, audStep{Kind: "key", VK: vkBack}, audStep{Kind: "wait", Ms: p / 2})
	}
	return st
}

func handleAudRec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	supported := audRecSupported()
	switch req.Action {
	case "start":
		if supported {
			audRecStart()
		}
	case "stop":
		audRecStop()
	}
	act, toks := audRecState()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"supported": supported, "active": act, "tokens": toks})
}
