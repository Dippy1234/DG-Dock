package main

import "testing"

func TestCalc(t *testing.T) {
	for q, want := range map[string]string{"2+2": "4", "12 x 3": "36", "(1+2)*3": "9", "2^10": "1024", "10/4": "2.5", "0.1+0.2": "0.3", "7%3": "1", "= 5*5": "25", "100/3": "33.3333333333"} {
		got, ok := evalCalc(q)
		if !ok || got != want {
			t.Errorf("%q = %q,%v want %q", q, got, ok, want)
		}
	}
	for _, q := range []string{"5", "-5", "xbox", "spotify", "1/0", "2+", "chrome 2"} {
		if v, ok := evalCalc(q); ok {
			t.Errorf("%q should not be a sum, got %q", q, v)
		}
	}
}

func TestRank(t *testing.T) {
	es := []searchEntry{
		{Kind: "app", ID: "vsc", Title: "Visual Studio Code"},
		{Kind: "app", ID: "vs", Title: "Visual Studio 2022"},
		{Kind: "app", ID: "spot", Title: "Spotify"},
		{Kind: "app", ID: "ytm", Title: "YouTube Music"},
		{Kind: "set", ID: "ms-settings:display", Title: "Display", Words: "screen resolution brightness"},
		{Kind: "file", ID: `C:\x\spotify-notes.txt`, Title: "spotify-notes.txt"},
		{Kind: "app", ID: "steam", Title: "Steam"},
		{Kind: "app", ID: "notepad", Title: "Notepad"},
	}
	top := func(q string, hits map[string]int) string {
		r := rankSearch(es, q, hits, 5)
		if len(r) == 0 {
			return ""
		}
		return r[0].ID
	}
	cases := map[string]string{"vsc": "vsc", "spo": "spot", "music": "ytm", "brightness": "ms-settings:display", "st": "steam", "visual code": "vsc", "ntpd": "notepad"}
	for q, want := range cases {
		if got := top(q, nil); got != want {
			t.Errorf("top(%q) = %q want %q", q, got, want)
		}
	}
	if got := top("s", map[string]int{"spot": 5}); got != "spot" {
		t.Errorf("hits should lift Spotify for 's', got %q", got)
	}
}
