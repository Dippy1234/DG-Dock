package main

import "testing"

func TestColourHelpers(t *testing.T) {
	if c, ok := parseHex("#3A6AD8"); !ok || c != [3]uint8{0x3a, 0x6a, 0xd8} {
		t.Fatal("parseHex", c, ok)
	}
	if _, ok := parseHex("nope"); ok {
		t.Fatal("bad hex accepted")
	}
	if hexOf([3]uint8{1, 2, 255}) != "#0102ff" {
		t.Fatal("hexOf")
	}
	// a dark dock colour used as accent on a dark UI gets lighter, and vice versa
	if a := readableAccent([3]uint8{0x1f, 0x2a, 0x44}, true); luma(a) < 120 {
		t.Fatal("dark accent not lifted", a)
	}
	if a := readableAccent([3]uint8{0xf2, 0xf2, 0xf4}, false); luma(a) > 150 {
		t.Fatal("light accent not darkened", a)
	}
	if onColor([3]uint8{255, 255, 255}) != "#000000" || onColor([3]uint8{0, 0, 0}) != "#ffffff" {
		t.Fatal("onColor")
	}
}
