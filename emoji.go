package main

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	_ "embed"
)

//go:embed emoji_dict.txt
var defaultEmojiDict string

type emojiEntry struct {
	words  []string // lower-case, stem marked by trailing *
	emojis []string
	prio   int // 1 = after a "#priority" line: wins over general words
}

func parseEmojiDict(text string) []emojiEntry {
	var out []emojiEntry
	prio := 0
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.EqualFold(ln, "#priority") {
			prio = 1
			continue
		}
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		i := strings.LastIndex(ln, " = ")
		if i < 0 {
			continue
		}
		e := emojiEntry{prio: prio}
		for _, w := range strings.Split(ln[:i], ",") {
			w = strings.ToLower(strings.TrimSpace(w))
			if w != "" {
				e.words = append(e.words, w)
			}
		}
		e.emojis = strings.Fields(ln[i+3:])
		if len(e.words) > 0 && len(e.emojis) > 0 {
			out = append(out, e)
		}
	}
	return out
}

func readEmojiDict() string {
	mu.Lock()
	b, _ := os.ReadFile(dataPath)
	mu.Unlock()
	var d struct {
		Emoji struct {
			Dict string `json:"dict"`
		} `json:"emoji"`
	}
	json.Unmarshal(b, &d)
	if strings.TrimSpace(d.Emoji.Dict) != "" {
		return d.Emoji.Dict
	}
	return defaultEmojiDict
}

func isEmojiRune(r rune) bool {
	return r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || r == 0x2B50 || r == 0x2B55 || r == 0xFE0F || r == 0x200D || r == 0x2764
}

func hasEmoji(s string) bool {
	for _, r := range s {
		if isEmojiRune(r) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’'
}

// findWordSpan returns the byte span of the first whole-word match of w in s (both lower-case), or -1,-1.
func findWordSpan(s, w string) (int, int) {
	stem := strings.HasSuffix(w, "*")
	w = strings.TrimSuffix(w, "*")
	if w == "" {
		return -1, -1
	}
	fr, _ := utf8.DecodeRuneInString(w)
	lr, _ := utf8.DecodeLastRuneInString(w)
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return -1, -1
		}
		i += from
		okL := !isWordRune(fr) || i == 0
		if !okL {
			pr, _ := utf8.DecodeLastRuneInString(s[:i])
			okL = !isWordRune(pr)
		}
		j := i + len(w)
		okR := stem || !isWordRune(lr) || j >= len(s)
		if !okR {
			nr, _ := utf8.DecodeRuneInString(s[j:])
			okR = !isWordRune(nr)
		}
		if okL && okR {
			if stem { // swallow the rest of the word
				for j < len(s) {
					r, n := utf8.DecodeRuneInString(s[j:])
					if !isWordRune(r) {
						break
					}
					j += n
				}
			}
			return i, j
		}
		from = i + 1
	}
	return -1, -1
}

func findWord(s, w string) int { i, _ := findWordSpan(s, w); return i }

var emojiStopWords = []string{"school*", "student*", "classmate*", "class", "classes", "teen*", "minor", "minors", "underage", "years old", "year old",
	"real life", "irl", "meet", "meeting", "address", "hotel", "kid", "kids", "child", "children", "grade*"}

var protectedRe = regexp.MustCompile(`\([^)]*\)`)

const emojiMax = 6

func pickEmoji(set []string, used map[string]bool, last string) string {
	var fresh []string
	for _, e := range set {
		if !used[e] && e != last {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		for _, e := range set {
			if e != last {
				fresh = append(fresh, e)
			}
		}
	}
	if len(fresh) == 0 {
		fresh = set
	}
	e := fresh[rand.Intn(len(fresh))]
	used[e] = true
	return e
}

// addEmoji puts fitting emoji into the text. stopped=true when a stop word was found (text returned unchanged).
func addEmoji(text, dictText string) (res string, added int, stopped bool) {
	low := strings.ToLower(text)
	for _, w := range emojiStopWords {
		if findWord(low, w) >= 0 {
			return text, 0, true
		}
	}
	dict := parseEmojiDict(dictText)
	used := map[string]bool{}
	last := ""
	var b strings.Builder
	locs := protectedRe.FindAllStringIndex(text, -1)
	// segments: alternating unprotected / protected
	type seg struct {
		s    string
		prot bool
	}
	var segs []seg
	pos := 0
	for _, l := range locs {
		if l[0] > pos {
			segs = append(segs, seg{text[pos:l[0]], false})
		}
		segs = append(segs, seg{text[l[0]:l[1]], true})
		pos = l[1]
	}
	if pos < len(text) {
		segs = append(segs, seg{text[pos:], false})
	}
	lastText := -1
	for i, sg := range segs {
		if !sg.prot && strings.IndexFunc(sg.s, unicode.IsLetter) >= 0 {
			lastText = i
		}
	}
	for si, sg := range segs {
		if sg.prot {
			b.WriteString(sg.s)
			continue
		}
		// split into sentence chunks
		rs := []rune(sg.s)
		start := 0
		var chunks []string
		for i := 0; i < len(rs); i++ {
			if rs[i] == '.' || rs[i] == '!' || rs[i] == '?' || rs[i] == '\n' {
				j := i
				for j+1 < len(rs) && (rs[j+1] == '.' || rs[j+1] == '!' || rs[j+1] == '?') {
					j++
				}
				chunks = append(chunks, string(rs[start:j+1]))
				start = j + 1
				i = j
			}
		}
		if start < len(rs) {
			chunks = append(chunks, string(rs[start:]))
		}
		for ci, ch := range chunks {
			isEnd := si == lastText && ci == len(chunks)-1
			out, n := decorateChunk(ch, dict, used, &last, emojiMax-added, isEnd)
			added += n
			b.WriteString(out)
		}
	}
	return b.String(), added, false
}

type emojiHit struct {
	end  int
	prio int
	pos  int
	set  []string
}

const emojiPerChunk = 3

func decorateChunk(ch string, dict []emojiEntry, used map[string]bool, last *string, room int, isEnd bool) (string, int) {
	body := strings.TrimRight(ch, " \t\r\n")
	trail := ch[len(body):]
	core := strings.TrimRight(body, ".!?")
	term := body[len(core):]
	if strings.TrimSpace(core) == "" || strings.IndexFunc(core, unicode.IsLetter) < 0 {
		return ch, 0
	}
	if hasEmoji(ch) { // the author already placed emoji here
		return ch, 0
	}
	low := strings.ToLower(core)
	if len(low) != len(core) {
		low = core
	}
	var hits []emojiHit
	if room > 0 {
		for _, e := range dict {
			bi, be := -1, -1
			for _, w := range e.words {
				i, j := findWordSpan(low, w)
				if i >= 0 && (bi < 0 || i < bi) {
					bi, be = i, j
				}
			}
			if bi < 0 {
				continue
			}
			// an emoji already follows this word: leave it alone
			rest := strings.TrimLeft(core[be:], " ")
			if r, _ := utf8.DecodeRuneInString(rest); rest != "" && isEmojiRune(r) {
				continue
			}
			hits = append(hits, emojiHit{end: be, prio: e.prio, pos: bi, set: e.emojis})
		}
	}
	// specific topics beat general words; without any topic the earliest general word is used once
	top := 0
	for _, h := range hits {
		if h.prio > top {
			top = h.prio
		}
	}
	var pick []emojiHit
	for _, h := range hits {
		if h.prio == top {
			pick = append(pick, h)
		}
	}
	sort.Slice(pick, func(a, b int) bool { return pick[a].pos < pick[b].pos })
	limit := emojiPerChunk
	if top == 0 {
		limit = 1
	}
	if limit > room {
		limit = room
	}
	var chosen []emojiHit
	for _, h := range pick {
		if len(chosen) >= limit {
			break
		}
		if len(chosen) > 0 && h.end <= chosen[len(chosen)-1].end+1 { // overlapping or touching
			continue
		}
		chosen = append(chosen, h)
	}
	if len(chosen) == 0 {
		if hasEmoji(ch) || !isEnd || room <= 0 {
			return ch, 0
		}
		set := endingSet(core, term)
		e := pickEmoji(set, used, *last)
		*last = e
		return withEnding(core, term, e) + trail, 1
	}
	if top == 0 { // a general word: the emoji closes the sentence instead of splitting it
		e := pickEmoji(chosen[0].set, used, *last)
		*last = e
		return withEnding(core, term, e) + trail, 1
	}
	var out strings.Builder
	prev := 0
	n := 0
	for k, h := range chosen {
		e := pickEmoji(h.set, used, *last)
		*last = e
		n++
		if h.end >= len(core) && k == len(chosen)-1 { // the word closes the sentence
			out.WriteString(core[prev:])
			prev = len(core)
			return withEnding(out.String(), term, e) + trail, n
		}
		out.WriteString(core[prev:h.end])
		out.WriteString(" " + e)
		prev = h.end
	}
	out.WriteString(core[prev:])
	out.WriteString(term)
	return out.String() + trail, n
}

// withEnding puts the emoji after the sentence: a plain "." disappears, "?" and "!" stay in front of it.
func withEnding(core, term, e string) string {
	t := term
	if strings.Trim(t, ".") == "" {
		t = ""
	}
	return core + t + " " + e
}

func endingSet(core, term string) []string {
	low := strings.ToLower(core)
	has := func(ws ...string) bool {
		for _, w := range ws {
			if findWord(low, w) >= 0 {
				return true
			}
		}
		return false
	}
	switch {
	case strings.Contains(term, "?"):
		return []string{"😏", "😈", "💋", "🔥"}
	case has("set*", "video*", "bundle*", "open*", "price*", "ppv", "buy*", "unlock*"):
		return []string{"🔥", "💦", "😈", "🥵"}
	case has("love*", "miss*", "thank*", "happy", "baby", "babe"):
		return []string{"💕", "😘", "🥰"}
	case strings.Contains(term, "!"):
		return []string{"🔥", "😍", "😘"}
	}
	return []string{"😘", "🔥", "😈"}
}

func handleEmoji(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var in struct {
		Text string `json:"text"`
		Dict string `json:"dict"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	d := in.Dict
	if strings.TrimSpace(d) == "" {
		d = defaultEmojiDict
	}
	res, n, stopped := addEmoji(in.Text, d)
	json.NewEncoder(w).Encode(map[string]interface{}{"result": res, "added": n, "stopped": stopped, "default": defaultEmojiDict})
}
