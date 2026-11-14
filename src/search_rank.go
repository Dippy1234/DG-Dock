package main

// Ranking for the Spotlight-style search. In order of strength:
//   exact title › first word exact › title prefix › whole word › word start ›
//   initials ("vsc" → Visual Studio Code) › well-known short names ("ppt", "cmd") ›
//   substring › one-letter typos ("chorme") › loose in-order letters.
// Apps rank above settings, which rank above files, and things you pick often move up.

import (
	"sort"
	"strings"
	"unicode"
)

type searchEntry struct {
	Kind  string // app, set, recent, file
	ID    string
	Title string
	Sub   string
	Words string // extra keywords (settings pages)
	Boost int
}

// words splits a title into lower-case words. Joined-up names are split at their
// capitals ("PowerShell" → power, shell; "WhatsApp" → whats, app) and letters are
// split from digits ("7-Zip" → 7, zip). Accents are folded ("Café" → cafe).
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(cur) > 0 {
			prev := rs[i-1]
			camel := unicode.IsLower(prev) && unicode.IsUpper(r) ||
				// "XMLParser": an upper followed by upper+lower starts a new word
				unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			digit := unicode.IsDigit(prev) != unicode.IsDigit(r)
			if camel || digit {
				flush()
			}
		}
		cur = append(cur, foldRune(unicode.ToLower(r)))
	}
	flush()
	return out
}

// plainWords splits only at spaces and punctuation (keeps "powershell" whole).
func plainWords(s string) []string {
	return strings.FieldsFunc(fold(strings.ToLower(s)), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func foldRune(r rune) rune {
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å':
		return 'a'
	case 'ç':
		return 'c'
	case 'è', 'é', 'ê', 'ë':
		return 'e'
	case 'ì', 'í', 'î', 'ï':
		return 'i'
	case 'ñ':
		return 'n'
	case 'ò', 'ó', 'ô', 'õ', 'ö':
		return 'o'
	case 'ù', 'ú', 'û', 'ü':
		return 'u'
	case 'ý', 'ÿ':
		return 'y'
	}
	return r
}

func fold(s string) string { return strings.Map(foldRune, s) }

// squash drops everything but letters and digits: "wi fi", "wi-fi" and "Wi-Fi" all
// become "wifi"; "7zip" matches "7-Zip".
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return foldRune(unicode.ToLower(r))
		}
		return -1
	}, s)
}

// editDistance is the Damerau-Levenshtein distance (a swap of two neighbouring
// letters counts as one edit), stopping early once it's over max.
func editDistance(a, b string, max int) int {
	ar, br := []rune(a), []rune(b)
	if d := len(ar) - len(br); d > max || -d > max {
		return max + 1
	}
	prev2 := make([]int, len(br)+1)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
			best = min(best, cur[j])
		}
		if best > max {
			return max + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(br)]
}

// typoScore allows one wrong, missing, extra or swapped letter in words of 4+ letters
// (two in words of 8+), against whole words and against the start of a word.
func typoScore(q string, ws []string) int {
	n := len([]rune(q))
	if n < 4 {
		return 0
	}
	allowed := 1
	if n >= 8 {
		allowed = 2
	}
	best := 0
	qr := []rune(q)
	for i, w := range ws {
		if []rune(w)[0] != qr[0] {
			continue // real typos almost always get the first letter right
		}
		d := editDistance(q, w, allowed)
		if wr := []rune(w); d > allowed && len(wr) > n {
			// typo while still typing: compare with the start of the word
			d = min(editDistance(q, string(wr[:n]), allowed), editDistance(q, string(wr[:min(n+1, len(wr))]), allowed))
		}
		if d <= allowed {
			best = max(best, 340-d*50-i*5)
		}
	}
	return best
}

// matchScore scores one query word against a title (0 = no match).
func matchScore(q, title string) int {
	q = fold(strings.ToLower(strings.TrimSpace(q)))
	if q == "" {
		return 0
	}
	t := fold(strings.ToLower(title))
	pw := plainWords(title)
	ws := words(title)
	lenPen := min(len(t), 60) / 6
	switch {
	case t == q:
		return 1000
	case len(pw) > 0 && pw[0] == q:
		return 900 - lenPen // "steam" → "Steam", "git" → "Git Bash"
	case strings.HasPrefix(t, q):
		return 860 - min(len(t)-len(q), 60)
	}
	if sq := squash(q); len(sq) >= 3 && sq == squash(title) {
		return 950 // "wi fi" → "Wi-Fi", "one drive" → "OneDrive"
	}
	for i, w := range pw {
		if w == q {
			return 820 - i*6 - lenPen // "chrome" → "Google Chrome"
		}
	}
	if sq := squash(q); len(sq) >= 3 {
		// words typed joined up: "todo" → "Microsoft To Do", "onenote" → "One Note"
		for i := range pw {
			joined := ""
			for j := i; j < len(pw) && j < i+3; j++ {
				joined += pw[j]
				if j > i && joined == sq {
					return 800 - i*6 - lenPen
				}
			}
		}
	}
	for i, w := range pw {
		if strings.HasPrefix(w, q) {
			return 720 - i*6 - lenPen // "chr" → "Google Chrome"
		}
	}
	for i, w := range ws { // parts of joined-up names: "shell" → PowerShell
		if strings.HasPrefix(w, q) {
			return 650 - i*6 - lenPen
		}
	}
	if len(q) >= 2 && len(ws) >= 2 {
		var ini strings.Builder
		for _, w := range ws {
			ini.WriteRune([]rune(w)[0])
		}
		if s := ini.String(); strings.HasPrefix(s, q) {
			return 620 - (len(s)-len(q))*4 // "vsc" → Visual Studio Code, "wa" → WhatsApp
		}
	}
	if sq := squash(q); len(sq) >= 3 && strings.HasPrefix(squash(title), sq) {
		return 600 // "7zip" → 7-Zip File Manager
	}
	if len(q) >= 3 {
		if i := strings.Index(t, q); i >= 0 {
			return 450 - min(i, 50)
		}
	}
	if s := typoScore(q, pw); s > 0 {
		return s
	}
	if len(q) >= 3 { // letters in order, few gaps ("ntpd" → Notepad)
		qi, gaps, last := 0, 0, -1
		qr := []rune(q)
		tr := []rune(t)
		if len(tr) > 0 && tr[0] != qr[0] {
			return 0 // must start where the title starts, or this matches nearly anything
		}
		for i, r := range tr {
			if qi < len(qr) && r == qr[qi] {
				if last >= 0 && i-last > 1 {
					gaps++
				}
				last = i
				qi++
			}
		}
		if qi == len(qr) && gaps <= len(qr)-1 && gaps <= 3 {
			return 250 - gaps*30
		}
	}
	return 0
}

// appKeywords are the names people type for apps that don't contain them: the
// Run-box commands ("cmd", "regedit"), short forms ("ppt") and what an app is for
// ("screenshot" → Snipping Tool). Keyed by the start of the app's lower-case title.
var appKeywords = map[string]string{
	"command prompt":              "cmd terminal console",
	"windows powershell":          "ps posh shell terminal",
	"powershell":                  "ps posh shell terminal",
	"terminal":                    "wt shell console",
	"task manager":                "taskmgr processes kill",
	"control panel":               "control",
	"file explorer":               "explorer files folders",
	"registry editor":             "regedit registry",
	"device manager":              "devmgmt drivers hardware",
	"disk management":             "diskmgmt partition drives",
	"remote desktop connection":   "rdp mstsc remote",
	"windows security":            "defender antivirus virus firewall",
	"snipping tool":               "screenshot screen capture snip",
	"powerpoint":                  "ppt pptx slides presentation",
	"word":                        "doc docx",
	"excel":                       "xls xlsx spreadsheet",
	"microsoft edge":              "browser web internet",
	"google chrome":               "browser web internet",
	"firefox":                     "browser web internet",
	"adobe acrobat":               "pdf reader",
	"calculator":                  "calc maths math",
	"notepad":                     "text editor txt",
	"paint":                       "mspaint draw",
	"microsoft store":             "store apps",
	"settings":                    "preferences options control",
	"event viewer":                "eventvwr logs",
	"services":                    "services.msc",
	"system information":          "msinfo32 specs",
	"resource monitor":            "resmon",
	"task scheduler":              "taskschd",
	"character map":               "charmap symbols",
	"on-screen keyboard":          "osk",
	"windows tools":               "admin tools",
	"camera":                      "webcam",
	"media player":                "music videos",
	"microsoft teams":             "chat meetings",
	"battle.net":                  "battlenet blizzard",
	"vlc media player":            "video player",
	"obs studio":                  "record stream",
	"7-zip file manager":          "zip archive unzip",
	"winrar":                      "rar zip archive unzip",
	"visual studio code":          "vscode code editor",
	"phone link":                  "android phone texts",
	"xbox game bar":               "game bar overlay",
	"windows media player legacy": "wmp",
}

// keywordsFor returns the extra names for an app title.
func keywordsFor(title string) string {
	t := strings.ToLower(title)
	if k, ok := appKeywords[t]; ok {
		return k
	}
	// "Adobe Acrobat (64-bit)", "PowerShell 7 (x64)": match on the start of the name
	best, bestLen := "", 0
	for name, k := range appKeywords {
		if len(name) > bestLen && strings.HasPrefix(t, name+" ") {
			best, bestLen = k, len(name)
		}
	}
	return best
}

// keywordScore scores a query word against keywords: exact keyword, or the start of
// one (3+ letters).
func keywordScore(q, kw string) int {
	q = fold(strings.ToLower(q))
	for _, k := range strings.Fields(kw) {
		if k == q {
			return 1
		}
		if len(q) >= 3 && strings.HasPrefix(k, q) {
			return 2
		}
	}
	return 0
}

// wordScore scores one query word against an entry: its title, then its keywords.
func wordScore(e *searchEntry, w string) int {
	s := matchScore(w, e.Title)
	if s >= 700 {
		return s
	}
	if e.Kind == "app" {
		// a well-known name for the app ("ppt", "regedit") is nearly as good as its title
		switch keywordScore(w, keywordsFor(e.Title)) {
		case 1:
			return max(s, 780)
		case 2:
			return max(s, 680)
		}
	}
	if e.Words != "" {
		switch keywordScore(w, e.Words) {
		case 1:
			return max(s, 700) // "brightness" → Display
		case 2:
			return max(s, 600)
		}
		if s == 0 && matchScore(w, e.Words) >= 450 {
			return 380
		}
	}
	return s
}

// scoreEntry scores an entry for a (possibly multi-word) query.
func scoreEntry(e *searchEntry, q string) int {
	qs := strings.Fields(strings.ToLower(q))
	if len(qs) == 0 {
		return 0
	}
	whole := matchScore(strings.TrimSpace(q), e.Title)
	if len(qs) == 1 {
		return wordScore(e, qs[0])
	}
	// "dark mode" as a keyword phrase
	if e.Words != "" && strings.Contains(" "+fold(strings.ToLower(e.Words))+" ", " "+strings.Join(qs, " ")+" ") {
		whole = max(whole, 760)
	}
	total := 0
	for _, w := range qs {
		s := wordScore(e, w)
		if s == 0 {
			return whole
		}
		total += s
	}
	return max(whole, total/len(qs)-20)
}

// Apps first, then Windows settings, recent files, then other files.
var kindWeight = map[string]int{"app": 150, "set": 70, "recent": 30, "file": -20}

// rankSearch returns the best limit entries for q.
func rankSearch(es []searchEntry, q string, hits map[string]int, limit int) []searchEntry {
	type sc struct {
		i, s int
	}
	var out []sc
	seen := map[string]bool{}
	for i := range es {
		s := scoreEntry(&es[i], q)
		if s == 0 {
			continue
		}
		// things you open often move up, but only when they actually match well:
		// a frequent app shouldn't jump over an exact match with a loose one
		boost := min(hits[es[i].ID]*40, 240)
		if s < 600 {
			boost /= 3
		}
		s += kindWeight[es[i].Kind] + es[i].Boost + boost
		out = append(out, sc{i, s})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].s != out[b].s {
			return out[a].s > out[b].s
		}
		return len(es[out[a].i].Title) < len(es[out[b].i].Title) // ties: the shorter name
	})
	var res []searchEntry
	for _, o := range out {
		e := es[o.i]
		key := e.Kind + "|" + strings.ToLower(e.Title)
		if e.Kind == "recent" || e.Kind == "file" {
			key = "f|" + strings.ToLower(e.Title)
		}
		if seen[key] { // same app listed twice, or a recent link to a file we already show
			continue
		}
		seen[key] = true
		res = append(res, e)
		if len(res) >= limit {
			break
		}
	}
	return res
}
