package main

import "testing"

func TestEvalCalc(t *testing.T) {
	cases := []struct {
		q, want string
		ok      bool
	}{
		{"1+1", "2", true},
		{"1,000+500", "1500", true},
		{"1,000,000/4", "250000", true},
		{"1,5+1", "2.5", true},
		{"2x3", "6", true},
		{"0.1+0.2", "0.3", true},
		{"-2^2", "-4", true},
		{"(-2)^2", "4", true},
		{"2^-1", "0.5", true},
		{"2^3^2", "512", true},
		{"-3*-2", "6", true},
		{"10%3", "1", true},
		{"10-5-2", "3", true},
		{"= 7 - 2", "5", true},
		{"2026-11-07", "", false},
		{"07/11/2026", "", false},
		{"020-7946-0958", "", false},
		{"+44 20 7946 0958", "", false},
		{"1/0", "", false},
		{"chrome", "", false},
		{"-5", "", false},
	}
	for _, c := range cases {
		got, ok := evalCalc(c.q)
		if ok != c.ok || got != c.want {
			t.Errorf("evalCalc(%q) = %q, %v; want %q, %v", c.q, got, ok, c.want, c.ok)
		}
	}
}

func TestIconResourceKey(t *testing.T) {
	for name, want := range map[string]string{
		"res/drawable/whatsapp.png":        "whatsapp",
		"res/mipmap/ic_launcher.png":       "ic_launcher",
		"res/drawable-nodpi/whatsapp.webp": "whatsapp",
		"res/AB.png":                       "AB",
		"res/drawable/frame.9.png":         "",
	} {
		if got, _ := iconResourceKey(name); got != want {
			t.Errorf("iconResourceKey(%q) = %q; want %q", name, got, want)
		}
	}
}
