package main

import (
	"encoding/json"
	"os"
	"strings"
)

// Audience presets: lists of folder names (as shown in the OnlyMonster audience picker) that the
// quick-paste menu can tick for you by typing each name into the picker's search box.

type audPreset struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Skip []string `json:"skip"` // "do not send to"
	Send []string `json:"send"` // "send to" (optional)
}

type audConfig struct {
	Presets []audPreset `json:"presets"`
	Key     string      `json:"key"`   // how a found folder is ticked: enter | down_enter | space | tab
	Pause   int         `json:"pause"` // ms between actions
}

func readAudience() audConfig {
	mu.Lock()
	b, _ := os.ReadFile(dataPath)
	mu.Unlock()
	var d struct {
		Audience audConfig `json:"audience"`
	}
	json.Unmarshal(b, &d)
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
)

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
