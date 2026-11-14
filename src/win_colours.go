//go:build windows

package main

// Settings › Colours: the dock's own colour, the accent, and menus that match the dock.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// accentChoice is the accent from the settings, as "#rrggbb" ("" = the Windows accent).
func (a *App) accentChoice(uiDark bool) string {
	switch v := a.cfg.AccentColor; {
	case v == "dock":
		if c, ok := a.dockColour(); ok {
			return hexOf(readableAccent(c, uiDark))
		}
		return ""
	case v == "wallpaper":
		if c, ok := wallAccent(wallPalette()); ok {
			return hexOf(readableAccent(c, uiDark))
		}
		return ""
	default:
		if c, ok := parseHex(v); ok {
			return hexOf(c)
		}
		return ""
	}
}

// uiTinted: menus, Launchpad and Settings take the dock colour.
func (a *App) uiTinted() bool {
	_, ok := a.dockColour()
	return a.cfg.MenuTint && ok
}

// uiDark is whether menus, Launchpad and Settings are dark: the theme, or, when they
// match the dock, whatever reads on the dock colour.
func (a *App) uiDark() bool { return a.uiDarkFor(a.dock.Dark) }

func (a *App) uiDarkFor(themeDark bool) bool {
	if a.uiTinted() {
		return !a.dock.Pal.Light
	}
	return themeDark
}

// uiPanel is the background colour for Launchpad and Settings.
func (a *App) uiPanel() string {
	if _, custom := a.dockColour(); a.uiTinted() || !custom {
		return hexOf(a.dock.Pal.Fill) // (the theme's colour, or Windows' accent tint)
	}
	if a.dock.Dark {
		return "#141416"
	}
	return "#f3f3f5"
}

// uiAlpha is how solid Settings' background is: it follows the dock's Colour strength
// (never below 40%, so text stays readable over the blur behind it).
func (a *App) uiAlpha() float64 {
	strength := 1 - float64(max(0, min(100, a.cfg.DockTransparency)))/100
	return 0.4 + 0.6*strength
}

// applyColours re-reads the colour settings and redraws everything that shows them.
// The dock redraws now; Settings and Launchpad catch up a moment later, once, however
// many colours were clicked in a row (refreshing them on every click made it feel laggy).
func (a *App) applyColours() {
	a.saveConfigLater()
	a.updateTheme()
	a.needRender = true // (updateTheme restyles the icons itself if light/dark flipped)
	a.render()
	a.colourFollowUp()
}

const wmColourFollowUp = WM_APP + 50

func (a *App) colourFollowUp() {
	a.colourSeq++
	seq, hwnd := a.colourSeq, a.hwnd
	go func() {
		time.Sleep(160 * time.Millisecond)
		pPostMessageW.Call(hwnd, wmColourFollowUp, seq, 0)
	}()
}

func (a *App) colourFollowUpDone(seq uintptr) {
	if seq != a.colourSeq {
		return // another click since: its follow-up does it
	}
	a.applySettingsTheme()
	if a.lp.host != nil {
		a.lp.host.eval("window.mdEnvRefresh && window.mdEnvRefresh()") // colours only: keep its icons
	}
}

// winAccentHex is Windows' own accent (ignoring any picked in Settings).
func winAccentHex(dark bool) string {
	saved := accentOverride
	accentOverride = ""
	acc, _ := accentColors(dark)
	accentOverride = saved
	return acc
}

// ---------- colours from the wallpaper ----------

// wallpaperFile is the picture Windows shows as the wallpaper. DG-Wallpaper sets the
// video's first frame as this, so live wallpapers work too.
// wallMon is the display this dock is on: with a wallpaper per display, its colours
// come from that display's picture (Build 249).
var wallMon RECT

var (
	clsidDesktopWallpaper = mustGUID("{C2CF3110-460E-4fc1-B9D0-8A1C0C9CC4BD}")
	iidDesktopWallpaper   = mustGUID("{B92B56A9-8B55-4E14-9A89-0199BBB6F93B}")
)

// monitorWallpaper asks Windows (IDesktopWallpaper) for the picture on wallMon's display.
func monitorWallpaper() string {
	if wallMon == (RECT{}) {
		return ""
	}
	pCoInitializeEx.Call(0, 2)
	var dw uintptr
	if hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidDesktopWallpaper)), 0, 4,
		uintptr(unsafe.Pointer(&iidDesktopWallpaper)), uintptr(unsafe.Pointer(&dw))); !hresultOK(hr) || dw == 0 {
		return ""
	}
	defer comRelease(dw)
	var n uint32
	if hr := comCall(dw, 6, uintptr(unsafe.Pointer(&n))); !hresultOK(hr) { // GetMonitorDevicePathCount
		return ""
	}
	for i := uint32(0); i < n; i++ {
		var id uintptr
		if hr := comCall(dw, 5, uintptr(i), uintptr(unsafe.Pointer(&id))); !hresultOK(hr) || id == 0 { // GetMonitorDevicePathAt
			continue
		}
		var r RECT
		hr := comCall(dw, 7, id, uintptr(unsafe.Pointer(&r))) // GetMonitorRECT
		path := ""
		if hresultOK(hr) && r == wallMon {
			var p uintptr
			if hr := comCall(dw, 4, id, uintptr(unsafe.Pointer(&p))); hresultOK(hr) && p != 0 { // GetWallpaper
				path = windows.UTF16PtrToString((*uint16)(unsafe.Pointer(p)))
				pCoTaskMemFree.Call(p)
			}
		}
		pCoTaskMemFree.Call(id)
		if path != "" {
			return path
		}
	}
	return ""
}

func wallpaperFile() string {
	if p := monitorWallpaper(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	buf := make([]uint16, 520)
	pSystemParametersInfoW.Call(0x0073, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), 0) // SPI_GETDESKWALLPAPER
	return windows.UTF16ToString(buf)
}

// transcodedWallpaper is Windows' own copy of the current wallpaper (also covers
// slideshows and Spotlight, which have no single file).
func transcodedWallpaper() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Themes\TranscodedWallpaper`)
}

func decodeImageFile(p string) image.Image {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return img
}

var wallCache struct {
	key string
	pal [][3]uint8
}

// wallPalette is the wallpaper's colours (main colour first), read again only when
// the wallpaper changes.
func wallPalette() [][3]uint8 {
	file, tc := wallpaperFile(), transcodedWallpaper()
	key := file
	for _, p := range []string{file, tc} {
		if st, err := os.Stat(p); err == nil {
			key += fmt.Sprintf("|%d|%d", st.ModTime().UnixNano(), st.Size())
		}
	}
	if file == "" {
		// a solid colour background
		if k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\Colors`, registry.QUERY_VALUE); err == nil {
			v, _, _ := k.GetStringValue("Background")
			k.Close()
			key += "|solid " + v
			if key != wallCache.key {
				var r, g, b int
				if n, _ := fmt.Sscanf(v, "%d %d %d", &r, &g, &b); n == 3 {
					wallCache.key, wallCache.pal = key, [][3]uint8{{uint8(r), uint8(g), uint8(b)}}
				}
			}
			return wallCache.pal
		}
	}
	if key == wallCache.key {
		return wallCache.pal
	}
	img := decodeImageFile(file)
	if img == nil {
		img = decodeImageFile(tc) // a format Go can't read (BMP…): Windows' copy is a JPEG
	}
	wallCache.key, wallCache.pal = key, wallpaperPalette(img, 6)
	return wallCache.pal
}

// dockColour is the custom dock colour in use: picked, or from the wallpaper.
func (a *App) dockColour() ([3]uint8, bool) {
	if a.cfg.DockColor == "wallpaper" {
		return wallDockColour(wallPalette())
	}
	return parseHex(a.cfg.DockColor)
}

// widgetColour is the widgets' own colour, if one is set.
func (a *App) widgetColour() ([3]uint8, bool) { return resolveWidgetColour(a.cfg.WidgetColor) }

func resolveWidgetColour(v string) ([3]uint8, bool) {
	if v == "wallpaper" {
		return wallWidgetColour(wallPalette())
	}
	return parseHex(v) // "" and "dock": the dock's soft tint
}

// widgetTintsBy is each widget's own colour, for the widgets that have one.
func (a *App) widgetTintsBy() map[string]WidgetTint {
	if len(a.cfg.WidgetColors) == 0 {
		return nil
	}
	m := map[string]WidgetTint{}
	for w, v := range a.cfg.WidgetColors {
		c, on := resolveWidgetColour(v)
		m[w] = WidgetTint{c, on}
	}
	return m
}

func sameTints(x, y map[string]WidgetTint) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if w, ok := y[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func widgetFillHex(a *App) string {
	if a.dock.WidgetTintOn {
		return hexOf(a.dock.WidgetTint)
	}
	return ""
}

// widgetFillsBy is the colour each widget is drawn in ("" = the dock's soft tint).
func widgetFillsBy(a *App) map[string]string {
	m := map[string]string{}
	for _, w := range widgetChoices {
		if c, on := a.dock.widgetTint(w.key); on {
			m[w.key] = hexOf(c)
		} else {
			m[w.key] = ""
		}
	}
	return m
}
