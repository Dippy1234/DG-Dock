//go:build windows

package main

// Fonts: the dock, menus, Settings and Launchpad can use any font installed in Windows.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
)

type fontFace struct{ regular, bold string } // file paths

var (
	fontsOnce sync.Once
	fontList  map[string]*fontFace // family → files
)

func fontDirs() []string {
	return []string{filepath.Join(os.Getenv("WINDIR"), "Fonts"), filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Windows\Fonts`)}
}

// installedFonts reads every .ttf/.otf font in Windows' font folders once.
func installedFonts() map[string]*fontFace {
	fontsOnce.Do(func() {
		fontList = map[string]*fontFace{}
		var buf sfnt.Buffer
		for _, dir := range fontDirs() {
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				ext := strings.ToLower(filepath.Ext(e.Name()))
				if e.IsDir() || (ext != ".ttf" && ext != ".otf") {
					continue
				}
				p := filepath.Join(dir, e.Name())
				b, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				f, err := sfnt.Parse(b)
				if err != nil {
					continue
				}
				fam, _ := f.Name(&buf, sfnt.NameIDTypographicFamily)
				sub, _ := f.Name(&buf, sfnt.NameIDTypographicSubfamily)
				if fam == "" {
					fam, _ = f.Name(&buf, sfnt.NameIDFamily)
					sub, _ = f.Name(&buf, sfnt.NameIDSubfamily)
				}
				fam = strings.TrimSpace(fam)
				if fam == "" || strings.Contains(strings.ToLower(fam), "icons") || strings.Contains(strings.ToLower(fam), "mdl2") ||
					strings.EqualFold(fam, "Marlett") || strings.EqualFold(fam, "Symbol") || strings.HasPrefix(fam, "Wingdings") || strings.EqualFold(fam, "Webdings") {
					continue
				}
				ff := fontList[fam]
				if ff == nil {
					ff = &fontFace{}
					fontList[fam] = ff
				}
				switch strings.ToLower(strings.TrimSpace(sub)) {
				case "regular", "normal", "book", "roman", "text":
					ff.regular = p
				case "bold", "semibold", "demibold":
					if ff.bold == "" || strings.EqualFold(sub, "semibold") {
						ff.bold = p
					}
				default:
					if ff.regular == "" && !strings.Contains(strings.ToLower(sub), "italic") {
						ff.regular = p // a font with one style (or a variable font)
					}
				}
			}
		}
		for k, v := range fontList {
			if v.regular == "" {
				delete(fontList, k)
			}
		}
	})
	return fontList
}

func fontFamilies() []string {
	var out []string
	for k := range installedFonts() {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// applyFont switches the dock's text to the chosen font ("" = Segoe UI).
func (a *App) applyFont() {
	reg, bold := loadFont(), loadBoldFont()
	if ff := installedFonts()[a.cfg.Font]; a.cfg.Font != "" && ff != nil {
		if b, err := os.ReadFile(ff.regular); err == nil {
			reg = b
			bold = b
			if ff.bold != "" {
				if bb, err := os.ReadFile(ff.bold); err == nil {
					bold = bb
				}
			}
		}
	}
	te := NewTextEngine(reg)
	if te.font == nil {
		te = NewTextEngine(loadFont())
	}
	bt := NewTextEngine(bold)
	if bt.font == nil {
		bt = te
	}
	a.te = te
	if a.rd != nil {
		a.rd.Text, a.rd.Bold = te, bt
	}
	if a.dock != nil {
		a.dock.Measure = te.Measure
		a.needRender = true
	}
}
