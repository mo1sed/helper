package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	nurl "net/url"
	"strings"
	"time"
)

var trClient = &http.Client{Timeout: 12 * time.Second}

type trResult struct {
	Text     string `json:"text"`
	Detected string `json:"detected"`
	Engine   string `json:"engine"`
}

// translate turns text from `from` ("auto" allowed) into `to`.
// Primary: Google's free gtx endpoint (real Google Translate quality).
// Fallback: MyMemory, used only when the source language is explicit.
func translate(from, to, text string) (trResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return trResult{}, errors.New("empty")
	}
	if from == "" {
		from = "auto"
	}
	if r, err := googleTranslate(from, to, text); err == nil {
		return r, nil
	}
	if from != "auto" {
		if r, err := myMemoryTranslate(from, to, text); err == nil {
			return r, nil
		}
	}
	return trResult{}, errors.New("unreachable")
}

func googleTranslate(from, to, text string) (trResult, error) {
	u := "https://translate.googleapis.com/translate_a/single?client=gtx&dt=t&sl=" +
		nurl.QueryEscape(from) + "&tl=" + nurl.QueryEscape(to) + "&q=" + nurl.QueryEscape(text)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := trClient.Do(req)
	if err != nil {
		return trResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return trResult{}, errors.New("status")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return trResult{}, err
	}
	return parseGoogle(b)
}

// parseGoogle reads the nested-array response: [[["out","in",...],...], ..., "detectedLang", ...]
func parseGoogle(b []byte) (trResult, error) {
	var data []interface{}
	if err := json.Unmarshal(b, &data); err != nil || len(data) == 0 {
		return trResult{}, errors.New("parse")
	}
	var sb strings.Builder
	if arr, ok := data[0].([]interface{}); ok {
		for _, s := range arr {
			if seg, ok := s.([]interface{}); ok && len(seg) > 0 {
				if t, ok := seg[0].(string); ok {
					sb.WriteString(t)
				}
			}
		}
	}
	if sb.Len() == 0 {
		return trResult{}, errors.New("empty")
	}
	det := ""
	if len(data) > 2 {
		if s, ok := data[2].(string); ok {
			det = s
		}
	}
	return trResult{Text: sb.String(), Detected: det, Engine: "google"}, nil
}

func myMemoryTranslate(from, to, text string) (trResult, error) {
	if len([]rune(text)) > 480 { // anonymous MyMemory caps query length
		return trResult{}, errors.New("too long")
	}
	u := "https://api.mymemory.translated.net/get?q=" + nurl.QueryEscape(text) +
		"&langpair=" + nurl.QueryEscape(from+"|"+to)
	resp, err := trClient.Get(u)
	if err != nil {
		return trResult{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
	}
	if json.Unmarshal(b, &out) != nil || out.ResponseData.TranslatedText == "" {
		return trResult{}, errors.New("parse")
	}
	return trResult{Text: out.ResponseData.TranslatedText, Detected: from, Engine: "mymemory"}, nil
}
