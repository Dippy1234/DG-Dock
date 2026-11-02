//go:build windows

package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

var (
	comctl32              = windows.NewLazySystemDLL("comctl32.dll")
	pInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pAdjustWindowRectEx   = user32.NewProc("AdjustWindowRectEx")
	pIsDialogMessageW     = user32.NewProc("IsDialogMessageW")
	pGetSysColorBrush     = user32.NewProc("GetSysColorBrush")
	pEnableWindow         = user32.NewProc("EnableWindow")
	pSetBkMode            = gdi32.NewProc("SetBkMode")
	pCreateFontW          = gdi32.NewProc("CreateFontW")
	CLSID_FileOpenDialog  = mustGUID("{DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7}")
	IID_IFileOpenDialog   = mustGUID("{d57c7288-d4ad-4768-be02-9d969532d960}")
)

const (
	WS_CHILD         = 0x40000000
	WS_VISIBLE       = 0x10000000
	WS_TABSTOP       = 0x00010000
	WS_VSCROLL       = 0x00200000
	WS_BORDER        = 0x00800000
	WS_CAPTION       = 0x00C00000
	WS_SYSMENU       = 0x00080000
	WS_MINIMIZEBOX   = 0x00020000
	WS_EX_CLIENTEDGE = 0x00000200

	WM_SETFONT        = 0x0030
	WM_COMMAND        = 0x0111
	WM_HSCROLL        = 0x0114
	WM_CTLCOLORSTATIC = 0x0138
	WM_CTLCOLORBTN    = 0x0135

	TBM_GETPOS      = 0x0400
	TBM_SETPOS      = 0x0405
	TBM_SETRANGEMIN = 0x0407
	TBM_SETRANGEMAX = 0x0408
	CB_ADDSTRING    = 0x0143
	CB_GETCURSEL    = 0x0147
	CB_SETCURSEL    = 0x014E
	BM_GETCHECK     = 0x00F0
	BM_SETCHECK     = 0x00F1
	LB_ADDSTRING    = 0x0180
	LB_RESETCONTENT = 0x0184
	LB_GETCURSEL    = 0x0188
	LB_SETCURSEL    = 0x0186
	EM_SETCUEBANNER = 0x1501

	idSize      = 1001
	idMag       = 1002
	idSizeVal   = 1003
	idMagVal    = 1004
	idTheme     = 1010
	idStyle     = 1011
	idDockStyle = 1012
	idPos       = 1013
	idCheckBase = 1020
	idSearch    = 1030
	idList      = 1031
	idAddBtn    = 1040
	idAddFolder = 1041
	idAddFile   = 1042
	idDone      = 1043
)

// ---------- shared setting changes (used by menus and the settings window) ----------

func (a *App) setIconSize(v int) {
	if v == a.cfg.IconSize {
		return
	}
	a.cfg.IconSize = v
	a.saveConfigLater()
	a.quickLayout = true
	a.relayout()
	a.quickLayout = false
}

func (a *App) setMagnification(v float64) {
	a.cfg.Magnification = v
	a.saveConfigLater()
	a.quickLayout = true
	a.relayout()
	a.quickLayout = false
}

func (a *App) setTheme(v string) {
	a.cfg.Theme = v
	a.saveConfig()
	a.updateTheme()
	a.render()
}

func (a *App) setIconStyle(v string) {
	a.cfg.IconStyle = v
	a.saveConfig()
	a.restyleAll()
	a.relayout()
}

func (a *App) setStyle(v string) {
	a.cfg.Style = v
	a.saveConfig()
	a.dock.PopItem, a.dock.PopT, a.dock.PopTarget = nil, 0, 0
	a.relayout()
	a.updateTheme()
	a.restyleAll()
	a.rebuildItems()
	a.relayout()
}

func (a *App) setPosition(v string) {
	if v == a.cfg.Position {
		return
	}
	a.cfg.Position = v
	a.saveConfig()
	a.removeAppbar()
	a.dock.PopItem, a.dock.PopT, a.dock.PopTarget = nil, 0, 0
	a.relayout()
}

// Auto-hide and Keep space can't both apply (a hidden dock has no space to keep), so
// turning one on turns the other off rather than leaving a switch that does nothing.
func (a *App) setAutoHide(on bool) {
	a.cfg.AutoHide = on
	if on {
		a.cfg.ReserveSpace = false
	}
	a.saveConfig()
	if on {
		a.dock.HideTarget = a.hiddenOffset()
	} else {
		a.dock.HideTarget = 0
	}
	a.relayout()
}

func (a *App) setHideTaskbar(on bool) {
	a.cfg.HideTaskbar = on
	a.saveConfig()
	if on {
		a.applyTaskbar()
	} else {
		a.restoreTaskbar()
	}
	pSetTimer.Call(a.hwnd, timerRelayout, 300, 0)
}

func (a *App) setReserve(on bool) {
	a.cfg.ReserveSpace = on
	if on && a.cfg.AutoHide {
		a.setAutoHide(false) // saves and lays out
		a.cfg.ReserveSpace = true
		a.saveConfig()
		a.relayout()
		return
	}
	a.saveConfig()
	a.relayout()
}

func (a *App) setShowRunning(on bool) {
	a.cfg.ShowRunning = on
	a.saveConfig()
	a.rebuildItems()
	a.render()
}

func (a *App) setBlur(on bool) {
	a.cfg.Blur = on
	a.saveConfig()
	a.updateTheme()
	a.render()
}

func (a *App) setHideFullscreen(on bool) {
	a.cfg.HideFullscreen = on
	a.saveConfig()
}

// ---------- pickers ----------

type filterSpec struct {
	Name *uint16
	Spec *uint16
}

// pickPath shows the Windows file/folder picker and returns the chosen path ("" if cancelled).
func pickPath(owner uintptr, folders bool, title string, filters [][2]string) string {
	var dlg uintptr
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_FileOpenDialog)), 0, 1,
		uintptr(unsafe.Pointer(&IID_IFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if !hresultOK(hr) || dlg == 0 {
		return ""
	}
	defer comRelease(dlg)
	var opts uint32
	comCall(dlg, 10, uintptr(unsafe.Pointer(&opts)))
	opts |= 0x40 // FOS_FORCEFILESYSTEM
	if folders {
		opts |= 0x20 // FOS_PICKFOLDERS
	} else {
		opts |= 0x400 // FOS_NODEREFERENCELINKS: let people pick .lnk shortcuts themselves
	}
	comCall(dlg, 9, uintptr(opts))
	comCall(dlg, 17, up(u16(title)))
	var specs []filterSpec
	if !folders && len(filters) > 0 {
		for _, f := range filters {
			specs = append(specs, filterSpec{u16(f[0]), u16(f[1])})
		}
		comCall(dlg, 4, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0])))
	}
	if hr := comCall(dlg, 3, owner); hr != 0 {
		return ""
	}
	var item uintptr
	if hr := comCall(dlg, 20, uintptr(unsafe.Pointer(&item))); !hresultOK(hr) || item == 0 {
		return ""
	}
	defer comRelease(item)
	var p uintptr
	if hr := comCall(item, 5, 0x80058000, uintptr(unsafe.Pointer(&p))); !hresultOK(hr) || p == 0 {
		return ""
	}
	s := u16ToString(p)
	pCoTaskMemFree.Call(p)
	return s
}

func (a *App) addFolderDialog(owner uintptr) {
	if p := pickPath(owner, true, "Choose a folder to add to the Dock", nil); p != "" {
		a.addPin(Pin{Kind: KindFile, Target: p})
	}
}

func (a *App) addFileDialog(owner uintptr) {
	p := pickPath(owner, false, "Choose a program, shortcut or file to add to the Dock", [][2]string{
		{"Programs and shortcuts", "*.exe;*.lnk;*.url;*.bat;*.cmd;*.appref-ms"},
		{"All files", "*.*"},
	})
	if p != "" {
		a.addPin(Pin{Kind: KindFile, Target: p})
	}
}

func (a *App) changeIconDialog(it *Item) {
	if it.Pin == nil {
		return
	}
	p := pickPath(a.hwnd, false, "Choose a new icon for "+it.Name, [][2]string{
		{"Icons and images", "*.png;*.ico;*.jpg;*.jpeg;*.exe;*.dll;*.lnk"},
		{"All files", "*.*"},
	})
	if p == "" {
		return
	}
	if a.customIcon(p) == nil {
		messageBox("That file doesn't contain an icon or image DG-Dock can use.", appName)
		return
	}
	a.setPinIcon(it, p)
}

func (a *App) setPinIcon(it *Item, path string) {
	if it.Pin == nil {
		return
	}
	a.cfg.Pins[it.PinIdx].Icon = path
	a.saveConfig()
	key := it.Key
	delete(a.items, key)
	a.rebuildItems()
	if n, ok := a.items[key]; ok {
		n.Appear = 1
		n.Running = it.Running
		n.Windows = it.Windows
	}
	a.render()
}

// loadImageFile decodes png/jpg/gif into a premultiplied BGRA icon of at most 256px.
func loadImageFile(path string) *image.RGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil
	}
	side := w
	if h > side {
		side = h
	}
	scale := 1.0
	if side > 256 {
		scale = 256 / float64(side)
	}
	dw, dh := int(math.Round(float64(w)*scale)), int(math.Round(float64(h)*scale))
	tw := dw
	if dh > tw {
		tw = dh
	}
	out := image.NewRGBA(image.Rect(0, 0, tw, tw))
	r := image.Rect((tw-dw)/2, (tw-dh)/2, (tw-dw)/2+dw, (tw-dh)/2+dh)
	xdraw.CatmullRom.Scale(out, r, src, b, draw.Over, nil)
	if swapRB {
		for i := 0; i < len(out.Pix); i += 4 {
			out.Pix[i], out.Pix[i+2] = out.Pix[i+2], out.Pix[i]
		}
	}
	return out
}

// ---------- settings window (WebView2, macOS "Desktop & Dock" look) ----------

//go:embed settings.html
var settingsHTML string

const wmPick = WM_APP + 5

type settingsState struct {
	Dark           bool     `json:"dark"`
	IconSize       int      `json:"iconSize"`
	Magnification  float64  `json:"magnification"`
	Theme          string   `json:"theme"`
	IconStyle      string   `json:"iconStyle"`
	Style          string   `json:"style"`
	Position       string   `json:"position"`
	AutoHide       bool     `json:"autoHide"`
	Bounce         bool     `json:"bounce"`
	Indicators     bool     `json:"indicators"`
	ShowRunning    bool     `json:"showRunning"`
	HideTaskbar    bool     `json:"hideTaskbar"`
	ReserveSpace   bool     `json:"reserveSpace"`
	HideFullscreen bool     `json:"hideFullscreen"`
	Blur           bool     `json:"blur"`
	BlurAvailable  bool     `json:"blurAvailable"`
	Startup        bool     `json:"startup"`
	IconPack       string   `json:"iconPack"`
	Previews       bool     `json:"previews"`
	Widgets        bool     `json:"widgets"`
	Tray           bool     `json:"tray"`
	FollowMouse    bool     `json:"followMouse"`
	Packs          []string `json:"packs"`
	Version        int      `json:"version"`
	VersionName    string   `json:"versionName"`
	SmartHide      bool     `json:"smartHide"`
	PerPage        int      `json:"perPage"`   // 0 = all
	AnimSpeed      int      `json:"animSpeed"` // 1–5
	PageSize       int      `json:"pageSize"`  // apps actually on a page (pages grow so there are never more than 2)
	StartSearch    bool     `json:"startSearch"`
	Update         int      `json:"update"`
}

func (a *App) settingsState() settingsState {
	c := a.cfg
	pos := c.Position
	if pos == "" {
		pos = "bottom"
	}
	style := c.Style
	if style == "" {
		style = "pill"
	}
	theme := c.Theme
	if theme == "" {
		theme = "auto"
	}
	is := c.IconStyle
	if is != "original" {
		is = "tiles"
	}
	return settingsState{
		Dark: a.dock.Dark, IconSize: c.IconSize, Magnification: c.Magnification, Theme: theme, IconStyle: is,
		Style: style, Position: pos, AutoHide: c.AutoHide, Bounce: !c.NoBounce, Indicators: !c.HideIndicators,
		ShowRunning: c.ShowRunning, HideTaskbar: c.HideTaskbar, ReserveSpace: c.ReserveSpace && !c.AutoHide,
		HideFullscreen: c.HideFullscreen, Blur: c.Blur, BlurAvailable: a.blur != 0, Startup: isStartupEnabled(),
		IconPack: c.IconPack, Packs: ListIconPacks(packsRoot()),
		Previews: !c.NoPreviews, Widgets: !c.NoWidgets, Tray: !c.NoTray, FollowMouse: !c.MainDisplay,
		SmartHide: !c.HideAlways, StartSearch: !c.StartIsStart, PerPage: a.perPage(), AnimSpeed: animSpeedOf(c.AnimSpeed), PageSize: a.pageSize(),
		Version: appVersion, VersionName: releaseVersion, Update: func() int { _, v := updates.found(); return v }(),
	}
}

func (a *App) settingSet(key string, raw json.RawMessage) settingsState {
	var b bool
	var f float64
	var s string
	json.Unmarshal(raw, &b)
	json.Unmarshal(raw, &f)
	json.Unmarshal(raw, &s)
	switch key {
	case "iconSize":
		if f >= 24 && f <= 128 {
			a.setIconSize(int(f))
		}
	case "magnification":
		if f >= 1 && f <= 3 {
			a.setMagnification(f)
		}
	case "theme":
		a.setTheme(s)
		a.applySettingsTheme()
	case "iconStyle":
		a.setIconStyle(s)
	case "style": // only the Pill style remains
	case "position":
		a.setPosition(s)
	case "autoHide":
		a.setAutoHide(b)
	case "animSpeed":
		n := animSpeedOf(int(f))
		if n != animSpeedOf(a.cfg.AnimSpeed) {
			a.cfg.AnimSpeed = n
			AnimSlow = AnimSpeedFactor(n)
			a.saveConfigLater()
		}
	case "perPage":
		n := int(f)
		if n <= 0 || n > 20 {
			n = -1
		}
		if n != a.cfg.AppsPerPage {
			a.cfg.AppsPerPage = n
			a.saveConfigLater()
			a.rebuildItems()
			a.relayout()
		}
	case "smartHide":
		a.cfg.HideAlways = !b
		a.overAt = time.Time{}
		a.saveConfig()
	case "startSearch":
		a.cfg.StartIsStart = !b
		a.saveConfig()
	case "bounce":
		a.cfg.NoBounce = !b
		a.saveConfig()
	case "indicators":
		a.cfg.HideIndicators = !b
		a.dock.HideDots = !b
		a.saveConfig()
		a.needRender = true
		a.render()
	case "showRunning":
		a.setShowRunning(b)
	case "hideTaskbar":
		a.setHideTaskbar(b)
	case "reserveSpace":
		a.setReserve(b)
	case "hideFullscreen":
		a.setHideFullscreen(b)
	case "blur":
		a.setBlur(b)
	case "startup":
		setStartup(b)
	case "iconPack":
		a.setIconPack(s)
	case "previews":
		a.cfg.NoPreviews = !b
		a.saveConfig()
	case "tray":
		a.cfg.NoTray = !b
		a.saveConfig()
		if b {
			a.startTray()
		} else {
			stopTray()
		}
		a.rebuildItems()
		a.relayout()
	case "widgets":
		a.cfg.NoWidgets = !b
		a.saveConfig()
		a.rebuildItems()
		a.relayout()
	case "followMouse":
		a.cfg.MainDisplay = !b
		a.saveConfig()
		if a.cfg.MainDisplay {
			a.removeAppbar()
			a.curMon = 0
			a.relayout()
		}
	}
	return a.settingsState()
}

type dockItemInfo struct {
	Idx       int    `json:"idx"`
	Name      string `json:"name"`
	KindLabel string `json:"kindLabel"`
	Icon      string `json:"icon"`
	Custom    bool   `json:"custom"`
	CanIcon   bool   `json:"canIcon"`
	Removable bool   `json:"removable"`
	Section   int    `json:"section"` // 0 = apps, 1 = folders and files
	Movable   bool   `json:"movable"` // Start and the Bin stay put
}

func (a *App) dockItemInfos() []dockItemInfo {
	var out []dockItemInfo
	for i := range a.cfg.Pins {
		p := &a.cfg.Pins[i]
		it := a.items[pinKey(p)]
		if it == nil {
			continue
		}
		label := "App"
		switch {
		case it.Kind == KindStart:
			label = "Start menu"
		case it.Kind == KindTrash:
			label = "Recycle Bin"
		case it.IsDir:
			label = "Folder"
		case it.Section == 1:
			label = "File"
		}
		info := dockItemInfo{Idx: i, Name: it.Name, KindLabel: label, Custom: p.Icon != "",
			CanIcon: true, Removable: it.Kind != KindTrash, Section: it.Section,
			Movable: it.Kind != KindStart && it.Kind != KindTrash}
		if it.Kind == KindTrash {
			info.Section = 1
		}
		if it.Icon != nil {
			info.Icon = iconDataURL(it.Icon.Pick(64), 64)
		}
		out = append(out, info)
	}
	// list apps first (Start, then in dock order), then folders/files with the Bin last
	sorted := make([]dockItemInfo, 0, len(out))
	for _, sec := range []int{0, 1} {
		for _, x := range out {
			if x.Section == sec && x.KindLabel == "Start menu" {
				sorted = append(sorted, x)
			}
		}
		for _, x := range out {
			if x.Section == sec && x.Movable {
				sorted = append(sorted, x)
			}
		}
		for _, x := range out {
			if x.Section == sec && !x.Movable && x.KindLabel != "Start menu" {
				sorted = append(sorted, x)
			}
		}
	}
	return sorted
}

// reorderPins puts one pill's movable pins in a new order (indices into cfg.Pins).
// Other pins keep their places, so only the order within that pill changes.
func (a *App) reorderPins(section int, order []int) bool {
	var slots []int
	for _, x := range a.dockItemInfos() {
		if x.Section == section && x.Movable {
			slots = append(slots, x.Idx)
		}
	}
	if len(order) != len(slots) {
		return false
	}
	seen := map[int]bool{}
	valid := map[int]bool{}
	for _, i := range slots {
		valid[i] = true
	}
	for _, i := range order {
		if !valid[i] || seen[i] {
			return false
		}
		seen[i] = true
	}
	sort.Ints(slots)
	old := append([]Pin{}, a.cfg.Pins...)
	for k, slot := range slots {
		a.cfg.Pins[slot] = old[order[k]]
	}
	a.saveConfig()
	a.rebuildItems()
	a.needRender = true
	a.render()
	return true
}

type appInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Pinned bool   `json:"pinned"`
}

// iconDataURL turns a premultiplied BGRA icon into a PNG data URL.
func iconDataURL(src *image.RGBA, size int) string {
	if src == nil {
		return ""
	}
	tmp := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(tmp, tmp.Bounds(), src, src.Bounds(), draw.Src, nil)
	out := image.NewNRGBA(tmp.Bounds())
	for i := 0; i < len(tmp.Pix); i += 4 {
		b0, g, r0, al := tmp.Pix[i], tmp.Pix[i+1], tmp.Pix[i+2], tmp.Pix[i+3]
		if !swapRB {
			b0, r0 = r0, b0
		}
		if al > 0 && al < 255 {
			r0 = uint8(min(255, int(r0)*255/int(al)))
			g = uint8(min(255, int(g)*255/int(al)))
			b0 = uint8(min(255, int(b0)*255/int(al)))
		}
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = r0, g, b0, al
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func (a *App) installedApps() []AppEntry {
	if a.appsCache == nil || time.Since(a.appsCacheTime) > 2*time.Minute {
		a.appsCache = listInstalledApps()
		a.appsCacheTime = time.Now()
	}
	return a.appsCache
}

// onPick runs a file/folder picker requested by the settings page.
func (a *App) onPick(code uintptr, idx int) {
	owner := a.hwnd
	if a.settings != nil {
		owner = a.settings.hwnd
	}
	a.dialogOpen = true
	defer func() {
		a.dialogOpen = false
		if a.settings != nil && a.panelVisible() {
			pSetForegroundWindow.Call(a.settings.hwnd)
		}
	}()
	switch code {
	case 1:
		a.addFolderDialog(owner)
	case 2:
		a.addFileDialog(owner)
	case 4:
		a.importPackDialog(owner)
	case 5:
		if k, n, ok := strings.Cut(a.pickKey, "\x00"); ok {
			a.keyIconDialog(k, n)
		}
	case 3:
		if idx >= 0 && idx < len(a.cfg.Pins) {
			if it := a.items[pinKey(&a.cfg.Pins[idx])]; it != nil {
				a.changeIconDialog(it)
			}
		}
	}
	a.render()
	a.refreshSettings()
}

// bindSettings exposes the dock's settings to the panel page.
func (a *App) bindSettings(hst *webHost) {
	hst.bind("mdState", func() settingsState { return a.settingsState() })
	hst.bind("mdSet", func(key string, v json.RawMessage) settingsState { return a.settingSet(key, v) })
	hst.bind("mdDockItems", func() []dockItemInfo { return a.dockItemInfos() })
	hst.bind("mdApps", func() []appInfo {
		var out []appInfo
		for _, ap := range a.installedApps() {
			out = append(out, appInfo{ID: ap.ID, Name: ap.Name, Pinned: a.hasPin(KindApp, ap.ID) >= 0})
		}
		return out
	})
	hst.bind("mdAppIcon", func(id string) string {
		img := a.rawIcon(`shell:AppsFolder\`+id, false)
		if img == nil {
			return ""
		}
		return iconDataURL(img, 48)
	})
	hst.bind("mdTogglePin", func(id string) bool {
		if i := a.hasPin(KindApp, id); i >= 0 {
			a.removePinAt(i)
			return false
		}
		a.addPin(Pin{Kind: KindApp, Target: id})
		return true
	})
	hst.bind("mdReorder", func(section int, order []int) bool { return a.reorderPins(section, order) })
	hst.bind("mdRemovePin", func(idx int) bool {
		if idx >= 0 && idx < len(a.cfg.Pins) && a.cfg.Pins[idx].Kind != KindTrash {
			a.removePinAt(idx)
		}
		return true
	})
	hst.bind("mdResetIcon", func(idx int) bool {
		if idx >= 0 && idx < len(a.cfg.Pins) {
			if it := a.items[pinKey(&a.cfg.Pins[idx])]; it != nil {
				a.setPinIcon(it, "")
			}
		}
		return true
	})
	hst.bind("mdPick", func(kind string, idx int) bool {
		// file dialogs run their own message loop, so open them outside this WebView2 callback
		code := map[string]uintptr{"folder": 1, "file": 2, "icon": 3, "pack": 4}[kind]
		pPostMessageW.Call(a.hwnd, wmPick, code, uintptr(idx))
		return true
	})
	hst.bind("mdPackIcons", func(q string) []string {
		if a.pack == nil {
			return nil
		}
		return a.pack.Search(q, 600)
	})
	hst.bind("mdPackIcon", func(name string) string {
		if a.pack == nil {
			return ""
		}
		img := a.customIcon(a.pack.Path(name))
		if img == nil {
			return ""
		}
		return iconDataURL(img, 48)
	})
	hst.bind("mdSetPackIcon", func(idx int, name string) bool {
		if a.pack == nil || idx < 0 || idx >= len(a.cfg.Pins) {
			return false
		}
		if it := a.items[pinKey(&a.cfg.Pins[idx])]; it != nil {
			a.setPinIcon(it, a.pack.Path(name))
		}
		return true
	})
	hst.bind("mdTakePendingKey", func() [2]string {
		k := a.pendingKey
		a.pendingKey = [2]string{}
		return k
	})
	hst.bind("mdSetKeyIcon", func(key, name string) bool {
		if a.pack == nil || key == "" {
			return false
		}
		a.setIconOverride(key, a.pack.Path(name))
		return true
	})
	hst.bind("mdResetKeyIcon", func(key string) bool {
		a.setIconOverride(key, "")
		return true
	})
	hst.bind("mdPickKeyFile", func(key, name string) bool {
		a.pickKey = key + "\x00" + name
		pPostMessageW.Call(a.hwnd, wmPick, 5, 0)
		return true
	})
	hst.bind("mdTakePending", func() int {
		i := a.pendingPicker
		a.pendingPicker = -1
		return i
	})
	hst.bind("mdUpdate", func() bool {
		pPostMessageW.Call(a.hwnd, wmUpdate, 1, 0)
		return true
	})
	hst.bind("mdAbout", func() map[string]string {
		return map[string]string{"name": releaseVersion, "build": strconv.Itoa(appVersion), "home": homeURL, "donate": donateURL,
			"licence": licenceText, "notices": thirdPartyNotices}
	})
	hst.bind("mdOpenLink", func(which string) bool {
		// only the app's own links, never a URL from the page
		switch which {
		case "home":
			if homeURL != "" {
				shellOpen(homeURL, "")
			}
		case "donate":
			if donateURL != "" {
				shellOpen(donateURL, "")
			}
		}
		return true
	})
	hst.bind("mdQuit", func() bool {
		pPostMessageW.Call(a.hwnd, WM_CLOSE, 0, 0)
		return true
	})
}

// animSpeedOf is the 1–5 animation speed (0 or out of range = 5, the normal speed).
func animSpeedOf(n int) int {
	if n < 1 || n > 5 {
		return 5
	}
	return n
}
