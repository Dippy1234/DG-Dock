//go:build windows

package main

// Spotlight-style search: Alt+Space (or the Start button on the dock) opens a search
// bar. Finds apps, settings pages, recent files and Desktop/Documents/Downloads,
// does quick sums, and falls back to a web search. Learns what you pick.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	_ "embed"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

//go:embed search.html
var searchHTML string

type searchEnv struct {
	Dark     bool   `json:"dark"`
	Accent   string `json:"accent"`
	OnAccent string `json:"onAccent"`
	Panel    string `json:"panel"`
	Docked   bool   `json:"docked"` // popping out of the Start icon: search box at the bottom
}

const (
	hotkeySearch      = 0x4D44
	wmSearchHide      = WM_APP + 10
	wmSearchResize    = WM_APP + 11
	wmSearchReveal    = WM_APP + 13
	timerSearchReveal = 7
	WM_HOTKEY         = 0x0312
	searchWidth       = 640
)

var (
	pRegisterHotKey   = user32.NewProc("RegisterHotKey")
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
	tile := name != "" && a.cfg.IconStyle != "original"
	key += "|" + strconv.FormatBool(tile && a.dock.Dark) + strconv.FormatBool(tile)
	if u, ok := sIconCache[key]; ok {
		return u
	}
	var data []byte
	if a.pack != nil && name != "" {
		if d := a.pack.Match(name); d != "" {
			data, _ = os.ReadFile(a.pack.Path(d))
		}
	}
	if data == nil {
		if img := loadShellIcon(parsing, 96); img != nil {
			if tile {
				img = MacTile(img, a.dock.Dark)
			}
			var buf bytes.Buffer
			if png.Encode(&buf, img) == nil {
				data = buf.Bytes()
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
	pPostMessageW.Call(a.hwnd, wmSearchHide, 0, 0)
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

func (a *App) createSearchWindow() {
	if a.searchHwnd != 0 {
		return
	}
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	wc := WNDCLASSEXW{LpfnWndProc: webWndProcCB, HInstance: hinst, HCursor: cur, LpszClassName: u16("DGDockSearch")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_TOPMOST, up(u16("DGDockSearch")), up(u16("DG-Dock search")),
		WS_POPUP|0x00040000|0x00C00000, 0, 0, searchWidth, 64, 0, 0, hinst, 0)
	if hwnd == 0 {
		return
	}
	a.searchHwnd = hwnd
	webHosts[hwnd] = &webHost{hwnd: hwnd, binds: map[string]reflect.Value{}, borderless: true}
	pSetWindowPos.Call(hwnd, 0, 0, 0, searchWidth, 64, 0x0020|0x0002|0x0001|0x0010|0x0004)
	dwmInt(hwnd, 20, boolI(a.dock.Dark))
	dwmInt(hwnd, 33, 1) // no DWM rounding: the window region gives it the dock's 14px corners
	margins := [4]int32{-1, -1, -1, -1}
	pDwmExtendFrameIntoClientArea.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
	dwmInt(hwnd, 34, -2) // DWMWA_BORDER_COLOR = none: the card edge is just its colour, like the dock's
}

// searchAnchor is the Start icon the search card pops out of (nil = no Start icon
// showing, so the card sits centred near the top of the screen instead).
func (a *App) searchAnchor() *Item {
	if a.hiddenFullscreen {
		return nil
	}
	for _, it := range a.dock.Items {
		if it.Kind == KindStart {
			return it
		}
	}
	return nil
}

func (a *App) searchRect(offset bool) RECT {
	d := a.dock
	s := d.Scale
	h := int32(a.searchH)
	if h <= 0 {
		h = 60
	}
	h = int32(math.Round(float64(h) * s))
	m := a.mon
	clampX := func(x, w int32) int32 {
		mg := int32(8 * s)
		return max(m.Left+mg, min(x, m.Right-mg-w))
	}
	clampY := func(y, hh int32) int32 {
		mg := int32(8 * s)
		return max(m.Top+mg, min(y, m.Bottom-mg-hh))
	}
	if st := a.searchAnchor(); st != nil {
		w := int32(math.Round(540 * s))
		slide := int32(0)
		if offset {
			slide = int32(12 * s)
		}
		// the card's near edge sits SearchGap above the (shown) pill
		fx, fy := d.toXY(st.U, d.M+d.PlateH+d.SearchGap)
		px, py := a.winX+int32(fx), a.winY+int32(fy)
		switch d.Orient {
		case OrientLeft:
			y := clampY(py-h/2, h)
			return RECT{px - slide, y, px - slide + w, y + h}
		case OrientRight:
			y := clampY(py-h/2, h)
			return RECT{px + slide - w, y, px + slide, y + h}
		default:
			x := clampX(px-w/2, w)
			return RECT{x, py + slide - h, x + w, py + slide}
		}
	}
	w := int32(math.Round(searchWidth * s))
	x := m.Left + (m.Right-m.Left-w)/2
	y := m.Top + (m.Bottom-m.Top)*22/100
	if offset {
		y -= int32(10 * s)
	}
	return RECT{x, y, x + w, y + h}
}

func (a *App) ensureSearchHost() bool {
	if a.search != nil {
		return true
	}
	bgc := edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
	if !a.dock.Dark {
		bgc = edge.COREWEBVIEW2_COLOR{A: 255, R: 0xf3, G: 0xf3, B: 0xf3}
	}
	if p := a.dock.Pal; p.Alpha > 0 {
		bgc = edge.COREWEBVIEW2_COLOR{A: 255, R: p.Fill[0], G: p.Fill[1], B: p.Fill[2]}
	}
	h, err := newWebHost(a.searchHwnd, webDataDir(), bgc)
	if err != nil {
		a.logf("search: %v", err)
		return false
	}
	h.borderless = true
	a.search = h
	h.onActivate = func(active bool) {
		if !active && a.searchShown && !a.searchAnimating {
			pPostMessageW.Call(a.hwnd, wmSearchHide, 0, 0)
		}
	}
	h.onClose = func() { a.hideSearch(false) }
	h.bind("mdEnv", func() searchEnv {
		acc, on := accentColors(a.dock.Dark)
		p := a.dock.Pal
		return searchEnv{Dark: !p.Light, Accent: acc, OnAccent: on, Docked: a.searchAnchor() != nil,
			Panel: fmt.Sprintf("#%02x%02x%02x", p.Fill[0], p.Fill[1], p.Fill[2])}
	})
	h.bind("mdSearch", func(q string) []searchRow { return a.runSearch(q) })
	h.bind("mdRun", func(id string, alt bool) bool { return a.runSearchItem(id, alt) })
	h.bind("mdSearchSize", func(px int) bool {
		if px >= 40 && px < 1200 && px != a.searchH {
			a.searchH = px
			pPostMessageW.Call(a.hwnd, wmSearchResize, 0, 0)
		}
		return true
	})
	h.bind("mdHide", func() bool { pPostMessageW.Call(a.hwnd, wmSearchHide, 0, 0); return true })
	h.bind("mdReady", func() bool { pPostMessageW.Call(a.hwnd, wmSearchReveal, 0, 0); return true })
	h.load(searchHTML)
	return true
}

func (a *App) toggleSearch() {
	if a.searchShown {
		a.hideSearch(true)
		return
	}
	a.showSearch()
}

func (a *App) showSearch() {
	if time.Since(a.searchHiddenAt) < 200*time.Millisecond {
		return // the click that closed it (focus loss) shouldn't reopen it
	}
	a.hidePanel(false)
	a.createSearchWindow()
	if a.searchHwnd == 0 {
		return
	}
	if sIndex.entries == nil {
		a.buildSearchIndex()
	}
	dwmInt(a.searchHwnd, 20, boolI(a.dock.Dark))
	a.dock.SearchGap = math.Round(16 * a.dock.Scale)
	a.dock.SearchItem = a.searchAnchor()
	if a.dock.SearchItem != nil {
		a.dock.PopItem, a.dock.PopT, a.dock.PopTarget = nil, 0, 0
		a.dock.HideTarget = 0 // auto-hide: bring the dock up under the card
	}
	a.needRender = true
	a.render()
	// The card stays cloaked (drawn but invisible) until the page has applied the dock's
	// colours and layout, so it never flashes a blank or default-styled window first.
	dwmInt(a.searchHwnd, 13, 1)
	from := a.searchRect(true)
	pSetWindowPos.Call(a.searchHwnd, ^uintptr(0), uintptr(from.Left), uintptr(from.Top), uintptr(from.Right-from.Left), uintptr(from.Bottom-from.Top), 0x0010)
	a.roundSearch()
	first := a.search == nil
	pShowWindow.Call(a.searchHwnd, 5)
	pSetForegroundWindow.Call(a.searchHwnd)
	if !a.ensureSearchHost() {
		pShowWindow.Call(a.searchHwnd, SW_HIDE)
		dwmInt(a.searchHwnd, 13, 0)
		messageBox("Search needs the Microsoft Edge WebView2 Runtime (microsoft.com/edge/webview2).", appName)
		return
	}
	a.searchShown = true
	a.searchRevealPending = true
	a.search.web.Show()
	a.search.web.Resize()
	if !first {
		a.search.eval("window.mdShown && window.mdShown()")
	}
	// the page says when it's ready (mdReady); this is only a fallback
	wait := uintptr(250)
	if first {
		wait = 1500
	}
	pSetTimer.Call(a.hwnd, timerSearchReveal, wait, 0)
}

// revealSearch uncloaks the card once its page is ready and slides it up out of the dock.
func (a *App) revealSearch() {
	pKillTimer(a.hwnd, timerSearchReveal)
	if !a.searchShown || !a.searchRevealPending {
		return
	}
	a.searchRevealPending = false
	from, to := a.searchRect(true), a.searchRect(false)
	pSetWindowPos.Call(a.searchHwnd, 0, uintptr(from.Left), uintptr(from.Top), uintptr(from.Right-from.Left), uintptr(from.Bottom-from.Top), 0x0010|0x0200|0x0004)
	a.roundSearch()
	a.search.web.Resize()
	pDwmFlush.Call() // let the resized page land before it's shown
	dwmInt(a.searchHwnd, 13, 0)
	a.searchAnimating = true
	a.animateWin(a.searchHwnd, from, to, time.Duration(float64(140*time.Millisecond)*AnimSlow), easeDecel)
	a.searchAnimating = false
	a.search.web.Focus()
}

func (a *App) hideSearch(animated bool) {
	if !a.searchShown {
		return
	}
	a.searchShown = false
	if a.searchRevealPending { // never shown: nothing to animate
		a.searchRevealPending = false
		animated = false
	}
	if animated {
		var cur RECT
		pGetWindowRect.Call(a.searchHwnd, uintptr(unsafe.Pointer(&cur)))
		to := cur
		d := int32(6 * a.dock.Scale)
		if a.dock.SearchItem != nil { // sink back toward the dock
			switch a.dock.Orient {
			case OrientLeft:
				to.Left, to.Right = to.Left-d, to.Right-d
			case OrientRight:
				to.Left, to.Right = to.Left+d, to.Right+d
			default:
				to.Top, to.Bottom = to.Top+d, to.Bottom+d
			}
		} else {
			to.Top, to.Bottom = to.Top-d, to.Bottom-d
		}
		a.searchAnimating = true
		a.animateWin(a.searchHwnd, cur, to, time.Duration(float64(80*time.Millisecond)*AnimSlow), easeAccel)
		a.searchAnimating = false
	}
	if a.search != nil && a.search.web != nil {
		a.search.eval("window.mdHidden && window.mdHidden()")
		a.search.web.Hide()
	}
	pShowWindow.Call(a.searchHwnd, SW_HIDE)
	dwmInt(a.searchHwnd, 13, 1) // stays cloaked until the next reveal
	a.searchHiddenAt = time.Now()
	a.dock.SearchItem = nil
	a.needRender = true
	a.render()
	if time.Since(sIndex.built) > 5*time.Minute {
		a.buildSearchIndex() // while nobody's looking
	}
}

// roundSearch clips the window to a rounded rectangle with the dock cards' corner radius.
func (a *App) roundSearch() {
	var r RECT
	pGetWindowRect.Call(a.searchHwnd, uintptr(unsafe.Pointer(&r)))
	rad := uintptr(math.Round(28 * a.dock.Scale)) // ellipse size = 2 × 14px
	rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(r.Right-r.Left+1), uintptr(r.Bottom-r.Top+1), rad, rad)
	if rgn != 0 {
		pSetWindowRgn.Call(a.searchHwnd, rgn, 1) // the window owns rgn now
	}
}

func (a *App) resizeSearch() {
	if !a.searchShown || a.searchAnimating || a.searchRevealPending {
		return
	}
	r := a.searchRect(false)
	pSetWindowPos.Call(a.searchHwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x0010|0x0200|0x0004)
	a.roundSearch()
	if a.search != nil {
		a.search.web.Resize()
	}
}

// animateWin slides a window between two rectangles, one step per display refresh.
func (a *App) animateWin(hwnd uintptr, from, to RECT, dur time.Duration, ease func(float64) float64) {
	start := time.Now()
	lerp := func(p, q int32, e float64) int32 { return p + int32(math.Round(float64(q-p)*e)) }
	for {
		t := float64(time.Since(start)) / float64(dur)
		if t > 1 {
			t = 1
		}
		e := ease(t)
		l, tp := lerp(from.Left, to.Left, e), lerp(from.Top, to.Top, e)
		r, b := lerp(from.Right, to.Right, e), lerp(from.Bottom, to.Bottom, e)
		pSetWindowPos.Call(hwnd, 0, uintptr(l), uintptr(tp), uintptr(r-l), uintptr(b-tp), 0x0010|0x0200|0x0004)
		if t >= 1 {
			return
		}
		if r, _, _ := pDwmFlush.Call(); r != 0 {
			time.Sleep(4 * time.Millisecond)
		}
	}
}
