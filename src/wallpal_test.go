package main

import (
	"image"
	"image/color"
	"testing"
)

func TestWallpaperPalette(t *testing.T) {
	// mostly deep blue, a band of orange, a little white
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{20, 40, 110, 255}
			if y > 200 {
				c = color.RGBA{240, 120, 40, 255}
			}
			if x > 380 {
				c = color.RGBA{250, 250, 250, 255}
			}
			img.Set(x, y, c)
		}
	}
	pal := wallpaperPalette(img, 6)
	if len(pal) < 3 {
		t.Fatal("expected 3 colours, got", pal)
	}
	if colourDist(pal[0], [3]uint8{20, 40, 110}) > 20 {
		t.Fatal("main colour should be the blue", pal[0])
	}
	acc, _ := wallAccent(pal)
	if colourDist(acc, [3]uint8{240, 120, 40}) > 20 {
		t.Fatal("accent should be the vivid orange", acc)
	}
	// a gradient is one colour family, not six near-identical swatches
	g := image.NewRGBA(image.Rect(0, 0, 256, 10))
	for x := 0; x < 256; x++ {
		for y := 0; y < 10; y++ {
			g.Set(x, y, color.RGBA{uint8(x / 8), uint8(60 + x/8), 140, 255})
		}
	}
	for i, a := range wallpaperPalette(g, 6) {
		for j, b := range wallpaperPalette(g, 6) {
			if i != j && colourDist(a, b) < 48 {
				t.Fatal("near-duplicate colours", a, b)
			}
		}
	}
	if wallpaperPalette(nil, 6) != nil {
		t.Fatal("nil image")
	}
}

func BenchmarkWallpaperPaletteNoisy(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for i := range img.Pix {
		img.Pix[i] = uint8((i*7919 + i/3*104729) % 256)
	}
	for i := range img.Pix {
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	for i := 0; i < b.N; i++ {
		wallpaperPalette(img, 6)
	}
}
