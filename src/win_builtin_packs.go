//go:build windows

package main

// The Black and White Icons that come with DG-Dock: line glyphs for Windows' own apps,
// other common apps, and folders and files, with one accent each. They're stored as
// colour layers (see composeLayerIcon) and painted in the colours in use, so the
// accent follows Settings › Colours › Accent and the tiles follow light or dark:
//   "Black and White Icons (Auto)"  dark or light tiles to suit the dock's background
//   "White Icons (Light)"           always light tiles
//   "Black Icons (Dark)"            always dark tiles
// Made by /home/claude/iconpack (glyphs.py, build.py). They can't be removed.

import (
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed builtin_packs
var builtinPacks embed.FS

// builtinPacksVersion goes up whenever the packs change, so the new icons are put in place.
const builtinPacksVersion = 6

var builtinPackVariants = []struct{ name, variant string }{
	{"Black and White Icons (Auto)", "auto"},
	{"White Icons (Light)", "light"},
	{"Black Icons (Dark)", "dark"},
}

// renamedBuiltinPacks: what the built-in packs were called before (Build 226).
var renamedBuiltinPacks = map[string]string{
	"Nothing Light":                 "White Icons (Light)",
	"Nothing Dark":                  "Black Icons (Dark)",
	"Black and White Icons":         "Black and White Icons (Auto)", // Build 238
	"Black and White Icons (Light)": "White Icons (Light)",
	"Black and White Icons (Dark)":  "Black Icons (Dark)",
}

// isBuiltinPack: a pack that comes with DG-Dock (it can't be removed).
func isBuiltinPack(name string) bool {
	for _, v := range builtinPackVariants {
		if v.name == name {
			return true
		}
	}
	return false
}

func (a *App) installBuiltinPacks() {
	missing := false
	for _, v := range builtinPackVariants {
		if _, err := os.Stat(filepath.Join(packsRoot(), v.name, "pack.json")); err != nil {
			missing = true // put back a built-in pack that's gone (they can't be removed)
		}
	}
	if a.cfg.BuiltinPacksVer >= builtinPacksVersion && !missing {
		return
	}
	for old, now := range renamedBuiltinPacks {
		os.RemoveAll(filepath.Join(packsRoot(), old))
		if a.cfg.IconPack == old {
			a.cfg.IconPack = now
		}
		// icons picked from the old pack folder: point them at the new one
		from := filepath.Join(packsRoot(), old) + string(filepath.Separator)
		to := filepath.Join(packsRoot(), now) + string(filepath.Separator)
		move := func(p string) string {
			if strings.HasPrefix(strings.ToLower(p), strings.ToLower(from)) {
				return to + p[len(from):]
			}
			return p
		}
		for k, v := range a.cfg.IconOverrides {
			a.cfg.IconOverrides[k] = move(v)
		}
		for i := range a.cfg.Pins {
			a.cfg.Pins[i].Icon = move(a.cfg.Pins[i].Icon)
		}
	}
	raw, err := builtinPacks.ReadFile("builtin_packs/bw/pack.json")
	if err != nil {
		return
	}
	var meta map[string]any
	if json.Unmarshal(raw, &meta) != nil {
		return
	}
	ok := true
	for _, v := range builtinPackVariants {
		dst := filepath.Join(packsRoot(), v.name)
		os.RemoveAll(dst) // the icons of an older version
		if err := os.MkdirAll(filepath.Join(dst, "icons"), 0o755); err != nil {
			ok = false
			continue
		}
		meta["name"], meta["variant"] = v.name, v.variant
		if b, err := json.MarshalIndent(meta, "", " "); err != nil || os.WriteFile(filepath.Join(dst, "pack.json"), b, 0o644) != nil {
			ok = false
		}
		fs.WalkDir(builtinPacks, "builtin_packs/bw/icons", func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			if b, err := builtinPacks.ReadFile(p); err == nil {
				if os.WriteFile(filepath.Join(dst, "icons", path.Base(p)), b, 0o644) != nil {
					ok = false
				}
			}
			return nil
		})
	}
	a.dynPacks = nil
	if ok {
		a.cfg.BuiltinPacksVer = builtinPacksVersion
		a.saveConfig()
	}
}

// ---------- painting the colour layers ----------

// dynVariant says whether an icon file belongs to a dynamic pack, and which variant.
func (a *App) dynVariant(iconPath string) (string, bool) {
	if !strings.EqualFold(filepath.Ext(iconPath), ".png") {
		return "", false
	}
	dir := filepath.Dir(filepath.Dir(iconPath)) // <pack>/icons/<name>.png
	if a.dynPacks == nil {
		a.dynPacks = map[string]string{}
	}
	v, seen := a.dynPacks[dir]
	if !seen {
		v = ""
		if b, err := os.ReadFile(filepath.Join(dir, "pack.json")); err == nil {
			var p IconPack
			if json.Unmarshal(b, &p) == nil && p.Dynamic {
				v = p.Variant
				if v == "" {
					v = "auto"
				}
			}
		}
		a.dynPacks[dir] = v
	}
	return v, v != ""
}

// layerDark: whether a dynamic pack's variant uses dark tiles right now.
func (a *App) layerDark(variant string) bool {
	switch variant {
	case "dark":
		return true
	case "light":
		return false
	}
	return a.dock.Dark // auto: Light or dark in Settings (Match Windows: Windows' own setting)
}

// packSig is the colours dynamic icons are painted in now; caches of them include it.
func (a *App) packSig() string {
	ac := a.dock.Accent
	return fmt.Sprintf("|%02x%02x%02x|%v", ac.R, ac.G, ac.B, a.dock.Dark)
}

// dynIcon paints a dynamic pack's icon in the colours in use.
func (a *App) dynIcon(iconPath, variant string) *image.RGBA {
	key := "dyn:" + iconPath + a.packSig()
	if img, ok := a.icons[key]; ok && img != nil {
		return img
	}
	mk := "mask:" + iconPath
	mask := a.icons[mk]
	if mask == nil {
		if mask = loadImageFile(iconPath); mask == nil {
			return nil
		}
		a.icons[mk] = mask
	}
	bg, ink := layerColours(a.layerDark(variant))
	ac := a.dock.Accent
	src := mask
	if swapRB { // loadImageFile hands back BGRA for Windows: read the layers as drawn
		src = image.NewRGBA(mask.Rect)
		copy(src.Pix, mask.Pix)
		swapPixRB(src.Pix)
	}
	img := composeLayerIcon(src, bg, ink, [3]uint8{ac.R, ac.G, ac.B})
	if swapRB {
		swapPixRB(img.Pix)
	}
	a.icons[key] = img
	return img
}

// repaintPackIcons repaints the dock's dynamic pack icons after the accent or the
// light/dark tiles change (cheap: only those items, not a whole reload).
func (a *App) repaintPackIcons() {
	sig := a.packSig()
	if sig == a.lastPackSig {
		return
	}
	old := a.lastPackSig
	a.lastPackSig = sig
	if old != "" { // drop the icons painted in the old colours (~256 KB each)
		for k := range a.icons {
			if strings.HasPrefix(k, "dyn:") && strings.HasSuffix(k, old) {
				delete(a.icons, k)
			}
		}
		for k := range a.styled {
			if strings.Contains(k, old) {
				delete(a.styled, k)
			}
		}
	}
	if old == "" || a.pack == nil || !a.pack.Dynamic {
		return
	}
	a.lp.icons = nil // Launchpad's copies, in the old colours
	for _, it := range a.items {
		if it.PackIcon {
			a.applyPack(it)
			a.styleItem(it)
		}
	}
	if it := a.items["w:tray"]; it != nil {
		it.Icon = a.trayWidgetIcon()
		it.scaled, it.scaledSrc = nil, nil
		it.scaledSize = 0
	}
	a.needRender = true
	if a.lp.host != nil {
		a.lp.host.eval("window.mdRefresh && window.mdRefresh()") // its icons too
	}
}

func swapPixRB(p []uint8) {
	for i := 0; i+2 < len(p); i += 4 {
		p[i], p[i+2] = p[i+2], p[i]
	}
}
