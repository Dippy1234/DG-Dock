//go:build windows

package main

// Spotlight-style search: Alt+Space (or the Start button on the dock) opens a search
// bar. Finds apps, settings pages, recent files and Desktop/Documents/Downloads,
// does quick sums, and falls back to a web search. Learns what you pick.

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	_ "embed"

	"golang.org/x/sys/windows"
)

const (
	hotkeySearch = 0x4D44
	WM_HOTKEY    = 0x0312
)

var (
	pRegisterHotKey   = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pEmptyClipboard   = user32.NewProc("EmptyClipboard")
	pSetClipboardData = user32.NewProc("SetClipboardData")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
)

var settingsPages = []searchEntry{
	{Title: "Wi-Fi", ID: "ms-settings:network-wifi", Words: "network internet wireless"},
	{Title: "Bluetooth & devices", ID: "ms-settings:bluetooth", Words: "headphones mouse keyboard pair"},
	{Title: "Display", ID: "ms-settings:display", Words: "screen resolution scale brightness monitor refresh rate hz"},
	{Title: "Night light", ID: "ms-settings:nightlight", Words: "blue light warm"},
	{Title: "Sound", ID: "ms-settings:sound", Words: "audio volume speakers microphone output"},
	{Title: "Power & battery", ID: "ms-settings:batterysaver", Words: "battery saver power sleep energy"},
	{Title: "Notifications", ID: "ms-settings:notifications", Words: "focus do not disturb"},
	{Title: "Storage", ID: "ms-settings:storagesense", Words: "disk space drive clean"},
	{Title: "Personalisation", ID: "ms-settings:personalization", Words: "wallpaper background theme colours colors personalization"},
	{Title: "Colours", ID: "ms-settings:colors", Words: "accent dark mode light mode colors"},
	{Title: "Installed apps", ID: "ms-settings:appsfeatures", Words: "uninstall programs apps"},
	{Title: "Default apps", ID: "ms-settings:defaultapps", Words: "browser default"},
	{Title: "Startup apps", ID: "ms-settings:startupapps", Words: "startup boot launch"},
	{Title: "Mouse", ID: "ms-settings:mousetouchpad", Words: "pointer cursor scroll"},
	{Title: "Touchpad", ID: "ms-settings:devices-touchpad", Words: "trackpad gestures"},
	{Title: "Keyboard", ID: "ms-settings:keyboard", Words: "typing language input"},
	{Title: "Windows Update", ID: "ms-settings:windowsupdate", Words: "updates upgrade patch"},
	{Title: "VPN", ID: "ms-settings:network-vpn", Words: "network"},
	{Title: "Mobile hotspot", ID: "ms-settings:network-mobilehotspot", Words: "tether share internet"},
	{Title: "Date & time", ID: "ms-settings:dateandtime", Words: "clock time zone"},
	{Title: "Privacy & security", ID: "ms-settings:privacy", Words: "permissions camera location microphone"},
	{Title: "Accounts", ID: "ms-settings:yourinfo", Words: "user profile sign in"},
	{Title: "About this PC", ID: "ms-settings:about", Words: "system specs device name"},
}

type searchRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Sub   string `json:"sub"`
	Kind  string `json:"kind"`
	Icon  string `json:"icon,omitempty"`  // data: URI
	Glyph string `json:"glyph,omitempty"` // Segoe Fluent Icons character
	File  bool   `json:"file,omitempty"`
}

type searchIndex struct {
	built   time.Time
	entries []searchEntry
}

var (
	sIndex     searchIndex
	sIconCache = map[string]string{}
)

func (a *App) registerSearchKey() {
	pUnregisterHotKey.Call(a.hwnd, hotkeySearch)
	a.searchKeyName = ""
	if a.cfg.DisableSearchHotkey {
		return
	}
	const modAlt, modCtrl, modNoRepeat, vkSpace = 0x1, 0x2, 0x4000, 0x20
	if r, _, _ := pRegisterHotKey.Call(a.hwnd, hotkeySearch, modAlt|modNoRepeat, vkSpace); r != 0 {
		a.searchKeyName = "Alt+Space"
		return
	}
	if r, _, _ := pRegisterHotKey.Call(a.hwnd, hotkeySearch, modAlt|modCtrl|modNoRepeat, vkSpace); r != 0 {
		a.searchKeyName = "Ctrl+Alt+Space"
		return
	}
	a.logf("search: no free shortcut (Alt+Space and Ctrl+Alt+Space are taken)")
}

// buildSearchIndex collects apps, settings and files. Apps come from the Start menu's
// AppsFolder (same list as Start), so it's run rarely: at first use and after closing.
func (a *App) buildSearchIndex() {
	var es []searchEntry
	for _, ap := range listInstalledApps() {
		es = append(es, searchEntry{Kind: "app", ID: ap.ID, Title: ap.Name})
	}
	for _, s := range settingsPages {
		s.Kind = "set"
		es = append(es, s)
	}
	if rec := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Recent`); rec != "" {
		if ents, err := os.ReadDir(rec); err == nil {
			type rf struct {
				e  searchEntry
				mt time.Time
			}
			var list []rf
			for _, e := range ents {
				n := e.Name()
				if !strings.HasSuffix(strings.ToLower(n), ".lnk") {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				list = append(list, rf{searchEntry{Kind: "recent", ID: filepath.Join(rec, n), Title: strings.TrimSuffix(n, filepath.Ext(n))}, info.ModTime()})
			}
			// newest first, at most 250
			for i := 1; i < len(list); i++ {
				for j := i; j > 0 && list[j].mt.After(list[j-1].mt); j-- {
					list[j], list[j-1] = list[j-1], list[j]
				}
			}
			for i, r := range list {
				if i >= 250 {
					break
				}
				r.e.Boost = 30 - i/10
				es = append(es, r.e)
			}
		}
	}
	for _, f := range []struct{ guid, name string }{
		{"{B4BFCC3A-DB2C-424C-B029-7FE99A87C641}", "Desktop"},
		{"{FDD39AD0-238F-46AF-ADB4-6C85480369C7}", "Documents"},
		{"{374DE290-123F-4565-9164-39C4925E467B}", "Downloads"},
	} {
		dir := knownFolder(f.guid)
		if dir == "" {
			continue
		}
		es = append(es, searchEntry{Kind: "file", ID: dir, Title: f.name, Sub: "Folder", Boost: 20})
		ents, _ := os.ReadDir(dir)
		for i, e := range ents {
			if i >= 400 || strings.HasPrefix(e.Name(), ".") || strings.EqualFold(e.Name(), "desktop.ini") {
				continue
			}
			es = append(es, searchEntry{Kind: "file", ID: filepath.Join(dir, e.Name()), Title: e.Name(), Sub: f.name})
		}
	}
	sIndex = searchIndex{built: time.Now(), entries: es}
}

// iconURI returns an icon as a data: URI, styled the way the dock styles it: the icon
// pack's art if it has one, otherwise the app icon on the dock's rounded tile.
func (a *App) iconURI(key, parsing, name string) string {
	style := a.cfg.IconStyle
	tile := name != "" && style != "original"
	key += "|" + style + "|" + strconv.FormatBool(tile && a.dock.Dark) + strconv.FormatBool(tile) + a.packSigIf()
	if u, ok := sIconCache[key]; ok {
		return u
	}
	var data []byte
	if a.pack != nil && name != "" {
		if d := a.pack.Match(name); d != "" {
			if a.pack.Dynamic {
				if img := a.customIcon(a.pack.Path(d)); img != nil {
					if u := iconDataURL(img, img.Bounds().Dx()); u != "" {
						sIconCache[key] = u
						return u
					}
				}
			} else {
				data, _ = os.ReadFile(a.pack.Path(d))
			}
		}
	}
	if data == nil {
		if img := loadShellIcon(parsing, 96); img != nil {
			if tile {
				img = MacTile(img, a.dock.Dark)
				if style == "circle" {
					img = CircleMaskIcon(img)
				}
			}
			if u := iconDataURL(img, img.Bounds().Dx()); u != "" { // right colour order for a PNG
				sIconCache[key] = u
				return u
			}
		}
	}
	u := ""
	if data != nil {
		u = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	}
	if len(sIconCache) > 600 {
		sIconCache = map[string]string{}
	}
	sIconCache[key] = u
	return u
}

func (a *App) runSearch(q string) []searchRow {
	q = strings.TrimSpace(q)
	if q == "" {
		return []searchRow{}
	}
	if sIndex.entries == nil {
		a.buildSearchIndex()
	}
	var rows []searchRow
	if v, ok := evalCalc(q); ok {
		rows = append(rows, searchRow{ID: "calc:" + v, Title: v, Sub: q + " =", Kind: "Copy", Glyph: ""})
	}
	hits := a.cfg.SearchHits
	for _, e := range rankSearch(sIndex.entries, q, hits, 8) {
		r := searchRow{Title: e.Title}
		switch e.Kind {
		case "app":
			r.ID, r.Kind, r.Sub = "app:"+e.ID, "App", "Application"
			r.Icon = a.iconURI("app:"+e.ID, `shell:AppsFolder\`+e.ID, e.Title)
		case "set":
			r.ID, r.Kind, r.Sub, r.Glyph = "set:"+e.ID, "Settings", "Windows Settings", ""
		case "recent":
			r.ID, r.Kind, r.Sub, r.File = "file:"+e.ID, "Recent", "Recently opened", true
			r.Icon = a.iconURI("f:"+e.ID, e.ID, "")
		case "file":
			r.ID, r.Kind, r.Sub, r.File = "file:"+e.ID, "File", e.Sub, true
			if e.Sub == "Folder" {
				r.Kind, r.Sub = "Folder", e.ID
			}
			r.Icon = a.iconURI("f:"+e.ID, e.ID, "")
		}
		rows = append(rows, r)
	}
	rows = append(rows, searchRow{ID: "web:" + q, Title: "Search the web for “" + q + "”", Sub: "Opens in your browser", Kind: "Web", Glyph: ""})
	return rows
}

func (a *App) runSearchItem(id string, alt bool) bool {
	kind, val, _ := strings.Cut(id, ":")
	switch kind {
	case "app":
		shellOpen("explorer.exe", `shell:AppsFolder\`+val)
	case "set":
		shellOpen(val, "")
	case "file":
		if alt {
			target := val
			if strings.HasSuffix(strings.ToLower(val), ".lnk") {
				if t := resolveShortcut(val); t != "" {
					target = t
				}
			}
			shellOpen("explorer.exe", `/select,"`+target+`"`)
		} else {
			shellOpen(val, "")
		}
	case "calc":
		setClipboardText(val)
	case "web":
		shellOpen("https://www.google.com/search?q="+url.QueryEscape(val), "")
	default:
		return false
	}
	if kind != "web" && kind != "calc" {
		if a.cfg.SearchHits == nil {
			a.cfg.SearchHits = map[string]int{}
		}
		key := val
		if kind == "app" {
			key = val
		}
		a.cfg.SearchHits[key]++
		if len(a.cfg.SearchHits) > 300 { // keep the map small: drop one-offs
			for k, n := range a.cfg.SearchHits {
				if n <= 1 {
					delete(a.cfg.SearchHits, k)
				}
			}
		}
		a.saveConfigLater()
	}
	return true
}

func setClipboardText(s string) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	n := len(u) * 2
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	h, _, _ := pGlobalAlloc.Call(0x0002, uintptr(n)) // GMEM_MOVEABLE
	if h == 0 {
		return
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	pSetClipboardData.Call(13, h) // CF_UNICODETEXT
}

// ---------- window ----------

func (a *App) toggleSearch() {
	if isSecondary() {
		a.showSearch()
		return
	}
	if a.lp.shown {
		a.hideLaunchpad()
		return
	}
	a.showSearch()
}

// showSearch opens Launchpad with its search box ready (Search lives in Launchpad).
func (a *App) showSearch() {
	if isSecondary() {
		// shown by the main dock, on this display
		a.forwardLaunchpadAnchor()
		forwardToMainAt(msgShowSearch, a.mon.Left, a.mon.Top, a.mon.Right, a.mon.Bottom)
		return
	}
	on := a.searchOnMon
	a.searchOnMon = RECT{}
	a.lp.searchFirst = true
	a.showLaunchpad(on)
}
