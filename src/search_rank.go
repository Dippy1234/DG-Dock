package main

// Ranking for the Spotlight-style search: prefix matches beat word starts, which beat
// initials ("vsc" → Visual Studio Code), substrings and loose in-order matches.
// Apps rank above settings and files, and things you pick often move up.

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

func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// matchScore scores one query word against a title (0 = no match).
func matchScore(q, title string) int {
	if q == "" {
		return 0
	}
	t := strings.ToLower(title)
	switch {
	case t == q:
		return 1000
	case strings.HasPrefix(t, q):
		return 850 - min(len(t)-len(q), 100)
	}
	ws := words(t)
	for i, w := range ws {
		if strings.HasPrefix(w, q) {
			return 700 - i*10 - min(len(t), 60)/4
		}
	}
	if len(q) >= 2 && len(ws) >= 2 {
		var ini strings.Builder
		for _, w := range ws {
			r := []rune(w)
			ini.WriteRune(r[0])
		}
		if strings.HasPrefix(ini.String(), q) {
			return 600
		}
	}
	if len(q) >= 2 {
		if i := strings.Index(t, q); i >= 0 {
			return 450 - min(i, 50)
		}
	}
	if len(q) >= 3 { // letters in order, few gaps
		qi, gaps, last := 0, 0, -1
		qr := []rune(q)
		for i, r := range []rune(t) {
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

// scoreEntry scores an entry for a (possibly multi-word) query.
func scoreEntry(e *searchEntry, q string) int {
	qs := strings.Fields(strings.ToLower(q))
	if len(qs) == 0 {
		return 0
	}
	whole := matchScore(strings.ToLower(strings.TrimSpace(q)), e.Title)
	if len(qs) == 1 {
		if whole == 0 && e.Words != "" {
			if s := matchScore(qs[0], e.Words); s >= 450 {
				return 380 // keyword hit ("brightness" → Display)
			}
		}
		return whole
	}
	total := 0
	for _, w := range qs {
		s := matchScore(w, e.Title)
		if s == 0 && e.Words != "" {
			if matchScore(w, e.Words) >= 450 {
				s = 380
			}
		}
		if s == 0 {
			return whole
		}
		total += s
	}
	return max(whole, total/len(qs)-20)
}

var kindWeight = map[string]int{"app": 120, "set": 60, "recent": 30, "file": 0}

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
		s += kindWeight[es[i].Kind] + es[i].Boost + min(hits[es[i].ID]*40, 240)
		out = append(out, sc{i, s})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].s > out[b].s })
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
