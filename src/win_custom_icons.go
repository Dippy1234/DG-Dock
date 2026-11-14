//go:build windows

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The icons folder: Pictures\DG-Dock Icon. Every custom icon lives here, and any
// image you drop in it shows up under "My icons" when you change an icon. Its APK
// folder takes Android icon packs, which are imported by themselves.
var iconsDirPath string // looked up once: asking Windows for Pictures isn't free

func iconsDir() string {
	if iconsDirPath == "" {
		pics := knownFolder("{33E28130-4E1E-4676-835A-98395C3BC3BB}") // Pictures
		if pics == "" {
			pics = filepath.Join(os.Getenv("USERPROFILE"), "Pictures")
		}
		iconsDirPath = filepath.Join(pics, "DG-Dock Icon")
	}
	d := iconsDirPath
	if _, err := os.Stat(d); err != nil { // (made again if you delete it)
		os.MkdirAll(filepath.Join(d, "APK"), 0o755)
		os.WriteFile(filepath.Join(d, "Read me.txt"), []byte(iconsReadme), 0o644)
	}
	return d
}

func apkDir() string {
	d := filepath.Join(iconsDir(), "APK")
	os.MkdirAll(d, 0o755)
	return d
}

const iconsReadme = `DG-Dock Icon

Put your own icons here (PNG works best; JPG, ICO, GIF and BMP work too).
They show up under "My icons" whenever you change an icon in DG-Dock.
DG-Dock uses these files exactly as they are, so their colours never change.

APK folder: drop an Android icon pack (.apk) in it and DG-Dock imports it by itself.
It then appears in Settings > Icons > Icon pack and artwork.
`

var myIconExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".ico": true, ".gif": true, ".bmp": true}

// listMyIcons: the images in the icons folder whose names contain q.
func listMyIcons(q string) []string {
	ents, err := os.ReadDir(iconsDir())
	if err != nil {
		return nil
	}
	q = strings.ToLower(strings.TrimSpace(q))
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !myIconExts[strings.ToLower(filepath.Ext(n))] {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(n), q) {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func inDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// keepCustomIcon makes sure an icon you chose is kept in the icons folder, so it still
// works if the original moves. Icons already there, or from an icon pack, are used as
// they are. Image files are copied byte for byte (never re-saved), so colours can't
// change; icons taken out of a program are saved once as a PNG.
func (a *App) keepCustomIcon(key, source string) string {
	if source == "" {
		return ""
	}
	dir := iconsDir()
	if inDir(source, dir) || inDir(source, packsRoot()) {
		return source
	}
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	if base == "" || len(base) > 60 {
		base = "icon"
	}
	ext := strings.ToLower(filepath.Ext(source))
	var content []byte
	if myIconExts[ext] {
		b, err := os.ReadFile(source)
		if err != nil {
			return source
		}
		content = b
	} else {
		img := a.customIcon(source)
		if img == nil {
			return source
		}
		var data bytes.Buffer
		if err := png.Encode(&data, toFileColours(img)); err != nil {
			return source
		}
		content, ext = data.Bytes(), ".png"
	}
	// the same picture already kept? use that file
	for i := 1; i < 100; i++ {
		name := base + ext
		if i > 1 {
			name = base + " (" + itoa(i) + ")" + ext
		}
		dest := filepath.Join(dir, name)
		old, err := os.ReadFile(dest)
		if err == nil {
			if bytes.Equal(old, content) {
				return dest
			}
			continue
		}
		if os.WriteFile(dest, content, 0o644) != nil {
			return source
		}
		return dest
	}
	return source
}

// toFileColours returns the icon with normal RGBA channel order for saving. In memory
// the dock keeps icons as BGRA (Windows' order); saving that directly swapped red and
// blue in the file, so a custom icon changed colour whenever it was loaded again.
func toFileColours(img *image.RGBA) *image.RGBA {
	if !swapRB {
		return img
	}
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	for i := 0; i+3 < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+2] = out.Pix[i+2], out.Pix[i]
	}
	return out
}

// repairCustomIconColours fixes icons saved by earlier builds, which had red and blue
// swapped. It runs once; a marker file records that it's done.
func repairCustomIconColours() {
	dir := filepath.Join(dataDir(), "CustomIcons")
	marker := filepath.Join(dir, ".colours-fixed")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return // no custom icons yet
	}
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		src, err := png.Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		b := src.Bounds()
		out := image.NewNRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
				out.SetNRGBA(x, y, color.NRGBA{c.B, c.G, c.R, c.A})
			}
		}
		var data bytes.Buffer
		if png.Encode(&data, out) == nil {
			writeFileAtomic(p, data.Bytes())
		}
	}
	os.WriteFile(marker, []byte("1"), 0o644)
}
