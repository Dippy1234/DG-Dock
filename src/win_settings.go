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
	"path/filepath"
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

func (a *App) setAppPadding(v int) {
	v = max(0, min(v, 20))
	if v == a.cfg.AppPadding {
		return
	}
	a.cfg.AppPadding = v
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

// applyAnimPref pushes the per-animation switches to the dock and redraws.
func (a *App) applyAnimPref() {
	a.dock.SetAnim(a.cfg)
	a.saveConfig()
	a.needRender = true
	a.render()
}

func (a *App) setIconStyle(v string) {
	a.cfg.IconStyle = v
	a.saveConfig()
	a.relayout() // the dock's own shape follows the style first…
	a.reloadItems()
	a.render()
}

// refreshDock re-reads every icon and lays the dock out again — what restarting
// DG-Dock would do, without restarting. The docks on other displays refresh too.
func (a *App) refreshDock() {
	a.icons = map[string]*image.RGBA{}
	a.loadPack()
	a.updateTheme()
	a.reloadItems()
	a.relayout()
	a.render()
	if !isSecondary() {
		notifyExtraDocks(msgRefreshDock)
		if a.lp.host != nil {
			a.lp.icons = map[string]string{}
			a.lp.host.eval("window.mdRefresh && window.mdRefresh()")
		}
	}
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
// setLockDock locks or unlocks the dock: while locked, icons can't be dragged to a new
// place or dragged off to remove them (in the dock or in Settings › Edit Dock).
func (a *App) setLockDock(on bool) {
	a.cfg.LockDock = on
	a.saveConfig()
	a.refreshSettings()
}

// displayMode is "each" (a dock on each chosen display), "main" (one dock on the main
// display) or "follow" (one dock that moves to the display the pointer is on).
func (a *App) displayMode() string {
	switch {
	case a.multiDock():
		return "each"
	case a.cfg.MainDisplay:
		return "main"
	}
	return "follow"
}

func (a *App) setDisplayMode(mode string) {
	switch mode {
	case "follow":
		a.cfg.MainDisplay, a.cfg.DockDisplays = false, nil
	case "main":
		a.cfg.MainDisplay, a.cfg.DockDisplays = true, nil
	case "each":
		if !a.multiDock() { // start with a dock on every connected display
			for _, d := range listDisplays() {
				a.cfg.DockDisplays = append(a.cfg.DockDisplays, d.Key)
			}
		}
		a.cfg.MainDisplay = false
	default:
		return
	}
	a.displaysChanged()
}

func (a *App) setDockOnDisplay(id string, on bool) {
	d, ok := displayByID(id)
	if !ok {
		return
	}
	var keep []string
	for _, x := range a.cfg.DockDisplays {
		if !strings.EqualFold(x, d.Key) && !strings.EqualFold(x, d.ID) {
			keep = append(keep, x)
		}
	}
	if on {
		keep = append(keep, d.Key)
	}
	if len(keep) == 0 {
		return // at least one display keeps a dock
	}
	a.cfg.DockDisplays = keep
	a.displaysChanged()
}

func (a *App) displaysChanged() {
	a.saveConfig()
	a.removeAppbar()
	a.curMon = 0
	a.relayout()
	a.syncDisplayDocks()
}

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
	if path != "" {
		path = a.keepCustomIcon(it.Key, path)
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
	Dark             bool              `json:"dark"`
	IconSize         int               `json:"iconSize"`
	AppIconScale     int               `json:"appIconScale"`
	IconPackScale    int               `json:"iconPackScale"`
	WidgetIconScale  int               `json:"widgetIconScale"`
	WidgetSpacing    int               `json:"widgetSpacing"`
	WidgetSize       int               `json:"widgetSize"`
	AppPadding       int               `json:"appPadding"`
	Connected        bool              `json:"connected"`
	DockTransparency int               `json:"dockTransparency"`
	DockColor        string            `json:"dockColor"`
	DockFill         string            `json:"dockFill"` // the dock colour in use (custom or the theme's)
	AccentColor      string            `json:"accentColor"`
	AccentNow        string            `json:"accentNow"` // the accent in use
	WinAccent        string            `json:"winAccent"` // Windows' own accent, for the swatch
	MenuTint         bool              `json:"menuTint"`
	WidgetColor      string            `json:"widgetColor"`
	WeatherLoc       string            `json:"weatherLoc"`
	Font             string            `json:"font"`
	WeatherStreet    string            `json:"weatherStreet"`
	WeatherTown      string            `json:"weatherTown"`
	WeatherPostcode  string            `json:"weatherPostcode"`
	WeatherCountry   string            `json:"weatherCountry"`
	WeatherPlace     string            `json:"weatherPlace"` // where the home address is
	WeatherUnits     string            `json:"weatherUnits"`
	WeatherAutoF     bool              `json:"weatherAutoF"` // what "Automatic" units means here
	WeatherStatus    string            `json:"weatherStatus"`
	WeatherOn        bool              `json:"weatherOn"`
	WeatherApprox    bool              `json:"weatherApprox"`
	WidgetFill       string            `json:"widgetFill"`   // the widget colour in use ("" = the dock's soft tint)
	WidgetColors     map[string]string `json:"widgetColors"` // each widget's own setting (absent = as all widgets)
	WidgetFills      map[string]string `json:"widgetFills"`  // the colour each widget is drawn in
	WidgetNames      [][2]string       `json:"widgetNames"`
	WidgetStyle      string            `json:"widgetStyle"`
	ClockFace        string            `json:"clockFace"`
	BuiltinPacks     []string          `json:"builtinPacks"`
	UIAlpha          float64           `json:"uiAlpha"` // Settings' background opacity (follows Colour strength)
	OnAccentNow      string            `json:"onAccentNow"`
	UIDark           bool              `json:"uiDark"`
	UIPanel          string            `json:"uiPanel"`
	Magnification    float64           `json:"magnification"`
	Theme            string            `json:"theme"`
	DockShape        string            `json:"dockShape"`
	IconStyle        string            `json:"iconStyle"`
	Position         string            `json:"position"`
	AutoHide         bool              `json:"autoHide"`
	Bounce           bool              `json:"bounce"`
	Indicators       bool              `json:"indicators"`
	ShowRunning      bool              `json:"showRunning"`
	ShowFolderFiles  bool              `json:"showFolderFiles"`
	HideTaskbar      bool              `json:"hideTaskbar"`
	SearchHotkey     bool              `json:"searchHotkey"`
	WinKey           bool              `json:"winKey"`
	StartButton      bool              `json:"startButton"`
	RecycleBin       bool              `json:"recycleBin"`
	HideFullscreen   bool              `json:"hideFullscreen"`
	LockDock         bool              `json:"lockDock"`
	LaunchpadCols    int               `json:"launchpadCols"`
	LaunchpadIcon    int               `json:"launchpadIcon"`
	LaunchpadNames   bool              `json:"launchpadNames"`
	LaunchpadTidy    bool              `json:"launchpadTidy"`
	LaunchpadPanel   string            `json:"launchpadPanel"`
	PillOrder        []string          `json:"pillOrder"`
	LaunchpadRecent  bool              `json:"launchpadRecent"`
	LaunchpadSorted  bool              `json:"launchpadSorted"` // you've moved apps or made folders
	UsbWidget        bool              `json:"usbWidget"`
	FolderActivity   bool              `json:"folderActivity"`
	Blur             bool              `json:"blur"`
	BlurAvailable    bool              `json:"blurAvailable"`
	Startup          bool              `json:"startup"`
	IconPack         string            `json:"iconPack"`
	MediaWidgetIcon  bool              `json:"mediaWidgetIcon"`
	TrayWidgetIcon   bool              `json:"trayWidgetIcon"`
	Previews         bool              `json:"previews"`
	Widgets          bool              `json:"widgets"`
	Tray             bool              `json:"tray"`
	FollowMouse      bool              `json:"followMouse"`
	DisplayMode      string            `json:"displayMode"` // follow, main, each
	DockDisplays     []string          `json:"dockDisplays"`
	Isolate          bool              `json:"isolate"`
	Displays         []displayInfo     `json:"displays"`
	Packs            []string          `json:"packs"`
	Version          int               `json:"version"`
	VersionName      string            `json:"versionName"`
	SmartHide        bool              `json:"smartHide"`
	PerPage          int               `json:"perPage"`   // 0 = all
	AnimSpeed        int               `json:"animSpeed"` // 1–5
	PageSize         int               `json:"pageSize"`  // apps actually on a page (pages grow so there are never more than 2)
	StartSearch      bool              `json:"startSearch"`
	Update           int               `json:"update"`
	Attention        bool              `json:"attention"`
	HoverLift        bool              `json:"hoverLift"`
	CardSpring       bool              `json:"cardSpring"`
	PageWave         bool              `json:"pageWave"`
	Jelly            bool              `json:"jelly"`
	HideSpring       bool              `json:"hideSpring"`
	DropSettle       bool              `json:"dropSettle"`
}

func (a *App) settingsState() settingsState {
	c := a.cfg
	pos := c.Position
	if pos == "" {
		pos = "bottom"
	}
	theme := c.Theme
	if theme == "" {
		theme = "auto"
	}
	is := c.IconStyle
	if is != "original" && is != "circle" {
		is = "tiles"
	}
	shape := c.Style
	if shape != "default" && shape != "pill" {
		shape = "pill"
	}
	return settingsState{
		Dark: a.dock.Dark, IconSize: c.IconSize, AppIconScale: c.AppIconScale, IconPackScale: c.IconPackScale, WidgetIconScale: c.WidgetIconScale,
		WidgetSpacing: max(0, min(c.WidgetSpacing, 20)), WidgetSize: c.WidgetSize, AppPadding: c.AppPadding, Connected: c.Connected,
		DockTransparency: c.DockTransparency, DockColor: c.DockColor, DockFill: hexOf(a.dock.Pal.Fill),
		AccentColor: c.AccentColor, AccentNow: hexOf([3]uint8{a.dock.Accent.R, a.dock.Accent.G, a.dock.Accent.B}), WinAccent: winAccentHex(a.uiDark()),
		WeatherLoc: c.WeatherLoc, Font: c.Font, WeatherStreet: c.WeatherStreet, WeatherTown: c.WeatherTown, WeatherPostcode: c.WeatherPostcode,
		WeatherCountry: c.WeatherCountry, WeatherPlace: c.WeatherHomePlace, WeatherUnits: c.WeatherUnits, WeatherAutoF: fahrenheitByRegion(),
		WeatherStatus: a.weatherStatus(), WeatherOn: a.weatherOn(), WeatherApprox: a.wx.w != nil && a.wx.w.Approx,
		MenuTint: c.MenuTint, WidgetColor: c.WidgetColor, WidgetFill: widgetFillHex(a),
		WidgetColors: c.WidgetColors, WidgetFills: widgetFillsBy(a), WidgetNames: widgetNameList(), WidgetStyle: c.WidgetStyle, ClockFace: c.ClockFace, BuiltinPacks: builtinPackNames(), UIAlpha: a.uiAlpha(), OnAccentNow: onColor([3]uint8{a.dock.Accent.R, a.dock.Accent.G, a.dock.Accent.B}), UIDark: a.uiDark(), UIPanel: a.uiPanel(), Magnification: c.Magnification, Theme: theme, DockShape: shape, IconStyle: is,
		Position: pos, AutoHide: c.AutoHide, Bounce: !c.NoBounce, Indicators: !c.HideIndicators,
		ShowRunning: c.ShowRunning, ShowFolderFiles: !c.NoFolderFiles, HideTaskbar: c.HideTaskbar,
		SearchHotkey: !c.DisableSearchHotkey, WinKey: !c.NoWinKeyLaunchpad,
		StartButton: a.hasPin(KindStart, "") >= 0, RecycleBin: a.hasPin(KindTrash, "") >= 0,
		HideFullscreen: c.HideFullscreen, LockDock: c.LockDock,
		LaunchpadCols: a.launchpadCols(), LaunchpadIcon: a.launchpadIconSize(), LaunchpadNames: !c.LaunchpadNoNames, LaunchpadTidy: !c.LaunchpadShowAll, PillOrder: pillOrder(c.PillOrder), LaunchpadPanel: map[bool]string{true: "icons", false: "text"}[c.LaunchpadIconPanel], LaunchpadRecent: !c.LaunchpadNoRecent,
		LaunchpadSorted: len(c.LaunchpadGroups) > 0 || len(c.LaunchpadFolders) > 0 || len(c.LaunchpadOrder) > 0, UsbWidget: !c.NoUsbWidget, FolderActivity: !c.NoFolderActivity,
		Blur: c.Blur, BlurAvailable: a.blur != 0, Startup: isStartupEnabled(),
		IconPack: c.IconPack, MediaWidgetIcon: c.MediaWidgetIcon != "", TrayWidgetIcon: c.IconOverrides["widget:tray"] != "", Packs: ListIconPacks(packsRoot()),
		Attention: !c.NoAttention,
		HoverLift: !c.NoHoverLift, CardSpring: !c.NoCardSpring, PageWave: !c.NoPageStagger, Jelly: !c.NoJelly, HideSpring: !c.NoHideSpring, DropSettle: !c.NoDropSettle,
		Previews: !c.NoPreviews, Widgets: !c.NoWidgets, Tray: !c.NoTray, FollowMouse: !c.MainDisplay, DisplayMode: a.displayMode(), DockDisplays: c.DockDisplays, Isolate: c.IsolateDisplays, Displays: listDisplays(),
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
	case "iconPackScale":
		if f >= 0 && f <= 150 {
			a.setIconPackScale(int(math.Round(f)))
		}
	case "appIconScale":
		if f >= 0 && f <= 150 {
			a.setAppIconScale(int(math.Round(f)))
		}
	case "widgetIconScale":
		if f >= 0 && f <= 150 {
			a.setWidgetIconScale(int(math.Round(f)))
		}
	case "spacing": // the quick slider: apps and widgets together
		if f >= 0 && f <= 20 {
			v := int(math.Round(f))
			a.cfg.WidgetSpacing, a.cfg.WidgetSpacingSet = v, true
			if v == a.cfg.AppPadding { // only the widgets change
				a.saveConfigLater()
				a.relayout()
				a.render()
			} else {
				a.setAppPadding(v)
			}
		}
	case "widgetSize":
		if f >= 60 && f <= 160 {
			a.cfg.WidgetSize = int(math.Round(f))
			a.saveConfigLater()
			a.relayout()
			a.render()
		}
	case "widgetSpacing":
		if f >= 0 && f <= 20 {
			a.cfg.WidgetSpacing = int(math.Round(f))
			a.cfg.WidgetSpacingSet = true
			a.saveConfigLater()
			a.relayout()
			a.render()
		}
	case "magnification":
		if f >= 1 && f <= 3 {
			a.setMagnification(f)
		}
	case "theme":
		a.setTheme(s)
		a.applySettingsTheme()
	case "dockShape":
		if s == "default" || s == "pill" {
			a.cfg.Style = s
			a.cfg.DockShapeApplied = true
			a.saveConfig()
			a.rebuildItems()
			a.relayout()
			a.render()
		}
	case "iconStyle":
		a.setIconStyle(s)
	case "appPadding":
		if f >= 0 && f <= 20 {
			a.setAppPadding(int(f))
		}
	case "launchpadCols":
		if f >= 3 && f <= 12 {
			a.cfg.LaunchpadCols = int(f)
			a.saveConfigLater()
		}
	case "launchpadIcon":
		if f >= 32 && f <= 112 {
			a.cfg.LaunchpadIcon = int(f)
			a.saveConfigLater()
		}
	case "launchpadNames":
		a.cfg.LaunchpadNoNames = !b
		a.saveConfig()
	case "launchpadPanel":
		a.cfg.LaunchpadIconPanel = s == "icons"
		a.saveConfig()
	case "launchpadRecent":
		a.cfg.LaunchpadNoRecent = !b
		a.saveConfig()
	case "launchpadTidy":
		a.cfg.LaunchpadShowAll = !b
		a.saveConfig()
	case "launchpadResetGroups":
		a.cfg.LaunchpadGroups, a.cfg.LaunchpadFolders, a.cfg.LaunchpadOrder = nil, nil, nil
		a.saveConfig()
		a.refreshLaunchpad()
	case "lockDock":
		a.setLockDock(b)
	case "folderActivity":
		a.cfg.NoFolderActivity = !b
		a.saveConfig()
	case "usbWidget":
		a.cfg.NoUsbWidget = !b
		a.saveConfig()
		a.scanUSB()
	case "connected":
		a.cfg.Connected = b
		a.saveConfig()
		a.relayout()
	case "dockTransparency":
		if f >= 0 && f <= 100 {
			a.cfg.DockTransparency = int(math.Round(f))
			a.dock.Transparency = a.cfg.DockTransparency
			a.saveConfigLater()
			a.needRender = true // (the widgets follow it too)
			a.render()
			a.colourFollowUp() // the other Settings view's see-through background
		}
	case "dockColor":
		if _, ok := parseHex(s); ok || s == "" || s == "wallpaper" {
			a.cfg.DockColor = strings.ToLower(s)
			if s == "" && a.cfg.AccentColor == "dock" {
				a.cfg.AccentColor = "" // nothing left to match: back to the Windows accent
			}
			a.applyColours()
		}
	case "accentColor":
		if _, ok := parseHex(s); ok || s == "" || s == "dock" || s == "wallpaper" {
			a.cfg.AccentColor = strings.ToLower(s)
			a.applyColours()
		}
	case "menuTint":
		a.cfg.MenuTint = b
		a.applyColours()
	case "font":
		if s == "" || installedFonts()[s] != nil {
			a.cfg.Font = s
			a.saveConfig()
			a.applyFont()
			a.restyleAll()
			a.render()
			a.colourFollowUp() // Settings and Launchpad pick it up too
		}
	case "weatherLoc":
		if s == "" || s == "home" {
			a.cfg.WeatherLoc = s
			a.saveConfig()
			a.wx.w, a.wx.err, a.wx.fix = nil, "", time.Time{}
			a.applyWeatherToWidget()
			a.refreshWeather(true)
		}
	case "weatherUnits":
		if s == "" || s == "c" || s == "f" {
			a.cfg.WeatherUnits = s
			a.saveConfig()
			a.refreshWeather(true)
		}
	case "weatherRefresh":
		a.refreshWeather(true)
	case "clockFace":
		if s == "" || s == "digital" {
			a.cfg.ClockFace = s
			a.saveConfig()
			a.updateTheme()
			a.needRender = true
			a.render()
		}
	case "widgetStyle":
		if s == "" || s == "dg" {
			a.cfg.WidgetStyle = s
			a.saveConfig()
			a.updateTheme()
			a.needRender = true
			a.render()
		}
	case "widgetColorFor": // "clock|#rrggbb"; "clock|" = back to all widgets' colour
		w, v, _ := strings.Cut(s, "|")
		known := false
		for _, c := range widgetChoices {
			known = known || c.key == w
		}
		if _, ok := parseHex(v); known && (ok || v == "" || v == "dock" || v == "wallpaper") {
			if v == "" {
				delete(a.cfg.WidgetColors, w)
			} else {
				if a.cfg.WidgetColors == nil {
					a.cfg.WidgetColors = map[string]string{}
				}
				a.cfg.WidgetColors[w] = strings.ToLower(v)
			}
			a.applyColours()
		}
	case "widgetColor":
		if _, ok := parseHex(s); ok || s == "" || s == "wallpaper" {
			a.cfg.WidgetColor = strings.ToLower(s)
			a.applyColours()
		}
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
	case "startButton":
		a.setDockSpecialPin(KindStart, b)
	case "recycleBin":
		a.setDockSpecialPin(KindTrash, b)
	case "bounce":
		a.cfg.NoBounce = !b
		a.dock.SetAnim(a.cfg)
		a.saveConfig()
		a.needRender = true
		a.render()
	case "hoverLift":
		a.cfg.NoHoverLift = !b
		a.applyAnimPref()
	case "cardSpring":
		a.cfg.NoCardSpring = !b
		a.applyAnimPref()
	case "pageWave":
		a.cfg.NoPageStagger = !b
		a.applyAnimPref()
	case "jelly":
		a.cfg.NoJelly = !b
		a.applyAnimPref()
	case "hideSpring":
		a.cfg.NoHideSpring = !b
		a.applyAnimPref()
	case "dropSettle":
		a.cfg.NoDropSettle = !b
		a.applyAnimPref()
	case "indicators":
		a.cfg.HideIndicators = !b
		a.dock.HideDots = !b
		a.saveConfig()
		a.needRender = true
		a.render()
	case "showRunning":
		a.setShowRunning(b)
	case "showFolderFiles":
		a.cfg.NoFolderFiles = !b
		a.saveConfig()
		a.rebuildItems()
		a.relayout()
		a.render()
	case "hideTaskbar":
		a.setHideTaskbar(b)
	case "searchHotkey":
		a.cfg.DisableSearchHotkey = !b
		a.saveConfig()
		a.registerSearchKey()
	case "winKey":
		a.cfg.NoWinKeyLaunchpad = !b
		a.saveConfig()
		a.applyWinKey()
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
	case "attention":
		a.cfg.NoAttention = !b
		if !b {
			for _, it := range a.dock.Items {
				it.Attention = false
			}
		}
		a.saveConfig()
		a.render()
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
	case "isolate":
		a.cfg.IsolateDisplays = b
		a.saveConfig() // the other docks pick it up from the saved settings
		a.scanRunning()
		a.rebuildItems()
		a.relayout()
	case "displayMode":
		a.setDisplayMode(s)
	case "dockDisplay": // {"id": "\\.\DISPLAY2", "on": true}
		var x struct {
			ID string `json:"id"`
			On bool   `json:"on"`
		}
		if json.Unmarshal(raw, &x) == nil && x.ID != "" {
			a.setDockOnDisplay(x.ID, x.On)
		}
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

type widgetItemInfo struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Position string `json:"position"`
	Enabled  bool   `json:"enabled"`
	Order    int    `json:"order"`
}

var widgetChoices = []struct{ key, name string }{
	{"clock", "Clock"}, {"status", "Battery and Wi-Fi"}, {"media", "Now playing"}, {"weather", "Weather"}, {"tray", "System tray"},
}

func widgetNameList() [][2]string {
	out := make([][2]string, 0, len(widgetChoices))
	for _, w := range widgetChoices {
		out = append(out, [2]string{w.key, w.name})
	}
	return out
}

func (a *App) widgetItemInfos() []widgetItemInfo {
	order := make([]string, 0, len(widgetChoices))
	seen := map[string]bool{}
	for _, k := range a.cfg.WidgetOrder {
		for _, d := range widgetChoices {
			if k == d.key && !seen[k] {
				order = append(order, k)
				seen[k] = true
			}
		}
	}
	for _, d := range widgetChoices {
		if !seen[d.key] {
			order = append(order, d.key)
		}
	}
	hidden := map[string]bool{}
	for _, k := range a.cfg.HiddenWidgets {
		hidden[k] = true
	}
	var out []widgetItemInfo
	for i, k := range order {
		for _, d := range widgetChoices {
			if d.key == k {
				pos := a.cfg.WidgetPositions[k]
				if a.cfg.WidgetAnchors[k] != "" {
					pos = "custom"
				} else if pos != "left" && pos != "middle" && pos != "right" {
					pos = "middle"
				}
				enabled := !hidden[k] && !(k == "tray" && a.cfg.NoTray)
				out = append(out, widgetItemInfo{Key: k, Name: d.name, Position: pos, Enabled: enabled, Order: i})
			}
		}
	}
	return out
}

func (a *App) setWidget(key string, enabled bool, position string, order int) bool {
	known := false
	for _, d := range widgetChoices {
		if d.key == key {
			known = true
			break
		}
	}
	if !known {
		return false
	}
	if position == "left" || position == "middle" || position == "right" {
		if a.cfg.WidgetPositions == nil {
			a.cfg.WidgetPositions = map[string]string{}
		}
		a.cfg.WidgetPositions[key] = position
		delete(a.cfg.WidgetAnchors, key)
	}
	if key == "tray" {
		a.cfg.NoTray = !enabled
		if enabled {
			a.startTray()
		} else {
			stopTray()
		}
	} else {
		hidden := []string{}
		for _, k := range a.cfg.HiddenWidgets {
			if k != key {
				hidden = append(hidden, k)
			}
		}
		if !enabled {
			hidden = append(hidden, key)
		}
		a.cfg.HiddenWidgets = hidden
	}
	infos := a.widgetItemInfos()
	if order >= 0 && order < len(infos) {
		cur := -1
		for i, w := range infos {
			if w.Key == key {
				cur = i
				break
			}
		}
		if cur >= 0 && cur != order {
			seq := make([]string, len(infos))
			for i, w := range infos {
				seq[i] = w.Key
			}
			v := seq[cur]
			seq = append(seq[:cur], seq[cur+1:]...)
			seq = append(seq[:order], append([]string{v}, seq[order:]...)...)
			a.cfg.WidgetOrder = seq
		}
	}
	a.saveConfig()
	a.rebuildItems()
	a.relayout()
	a.render()
	a.refreshSettings()
	return true
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
			CanIcon: true, Removable: it.Kind != KindStart, Section: it.Section,
			Movable: it.Kind != KindTrash}
		if it.Kind == KindTrash {
			info.Section = 1
		}
		if it.Icon != nil {
			info.Icon = iconDataURL(it.Icon.Pick(64), 64)
		}
		out = append(out, info)
	}
	// Keep configured order for movable items; fixed items follow within each section.
	sorted := make([]dockItemInfo, 0, len(out))
	for _, sec := range []int{0, 1} {
		for _, x := range out {
			if x.Section == sec && x.Movable {
				sorted = append(sorted, x)
			}
		}
		for _, x := range out {
			if x.Section == sec && !x.Movable {
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
	if hs := a.settingsHosts(); len(hs) > 0 {
		owner = hs[len(hs)-1].hwnd
	}
	a.dialogOpen = true
	defer func() {
		a.dialogOpen = false
		if hs := a.settingsHosts(); len(hs) > 0 {
			pSetForegroundWindow.Call(hs[len(hs)-1].hwnd)
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
	case 8:
		if p, err := a.saveBackup(owner); err != nil {
			messageBox("Couldn't save your setup: "+err.Error(), appName)
		} else if p != "" {
			shellOpen("explorer.exe", `/select,"`+p+`"`)
		}
	case 9:
		if err := a.restoreBackup(owner); err != nil {
			messageBox("Couldn't restore that setup: "+err.Error(), appName)
		}
	case 6:
		a.changeMediaWidgetIconDialog(owner)
	case 7:
		a.changeTrayWidgetIconDialog(owner)
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
	hst.bind("mdWeatherHome", func(street, town, postcode, country string) bool {
		a.saveWeatherHome(street, town, postcode, country) // answers via window.mdWeatherSaved
		return true
	})
	hst.bind("mdOpenLocationSettings", func() bool {
		shellOpen("ms-settings:privacy-location", "")
		return true
	})
	hst.bind("mdWallColours", func() []string {
		var out []string
		for _, c := range wallPalette() {
			out = append(out, hexOf(c))
		}
		return out
	})
	hst.bind("mdSet", func(key string, v json.RawMessage) settingsState { return a.settingSet(key, v) })
	hst.bind("mdRefreshDock", func() bool { a.refreshDock(); return true })
	hst.bind("mdDockItems", func() []dockItemInfo { return a.dockItemInfos() })
	hst.bind("mdIdentify", func() bool { a.identifyDisplays(); return true })
	hst.bind("mdWidgetItems", func() []widgetItemInfo { return a.widgetItemInfos() })
	hst.bind("mdSetWidget", func(key string, enabled bool, position string, order int) bool {
		return a.setWidget(key, enabled, position, order)
	})
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
	hst.bind("mdLaunchApps", func() []launchpadApp {
		apps := a.launchpadApps()
		for i := range apps {
			apps[i].Custom = a.appCustomIcon(apps[i].ID) != ""
		}
		return apps
	})
	hst.bind("mdLaunchIcon", func(id, name string) string { return a.launchpadIcon(id, name) })
	hst.bind("mdLaunchGroups", func() []launchGroup { return a.launchpadGroups(a.launchpadApps()) })
	hst.bind("mdSetLaunchOrder", func(order []string) bool {
		a.cfg.LaunchpadOrder = order
		a.saveConfig()
		a.refreshLaunchpad()
		return true
	})
	hst.bind("mdOpenLaunchpad", func() bool { pPostMessageW.Call(a.hwnd, wmLaunchpadOpen, 0, 0); return true })
	hst.bind("mdTogglePin", func(id string) bool {
		if i := a.hasPin(KindApp, id); i >= 0 {
			a.removePinAt(i)
			return false
		}
		a.addPin(Pin{Kind: KindApp, Target: id})
		return true
	})
	hst.bind("mdReorder", func(section int, order []int) bool {
		if a.cfg.LockDock {
			return false
		}
		return a.reorderPins(section, order)
	})
	hst.bind("mdAddTrash", func() bool {
		a.setDockSpecialPin(KindTrash, true)
		return true
	})
	hst.bind("mdRemovePin", func(idx int) bool {
		if !a.cfg.LockDock && idx >= 0 && idx < len(a.cfg.Pins) && a.cfg.Pins[idx].Kind != KindStart {
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
		code := map[string]uintptr{"folder": 1, "file": 2, "icon": 3, "pack": 4, "mediaIcon": 6, "trayIcon": 7}[kind]
		pPostMessageW.Call(a.hwnd, wmPick, code, uintptr(idx))
		return true
	})
	hst.bind("mdSetPillOrder", func(order []string) settingsState {
		a.cfg.PillOrder = pillOrder(order)
		a.saveConfig()
		a.rebuildItems()
		a.relayout()
		return a.settingsState()
	})
	hst.bind("mdMyIcons", func(q string) []string { return listMyIcons(q) })
	hst.bind("mdMyIcon", func(name string) string {
		if img := a.customIcon(filepath.Join(iconsDir(), filepath.Base(name))); img != nil {
			return iconDataURL(img, 48)
		}
		return ""
	})
	hst.bind("mdSetMyIcon", func(idx int, key, name string) bool {
		path := filepath.Join(iconsDir(), filepath.Base(name))
		if a.customIcon(path) == nil {
			return false
		}
		switch {
		case key == "widget:media":
			return a.setMediaWidgetIcon(path)
		case key != "":
			a.setIconOverride(key, path)
		case idx >= 0 && idx < len(a.cfg.Pins):
			if it := a.items[pinKey(&a.cfg.Pins[idx])]; it != nil {
				a.setPinIcon(it, path)
			}
		}
		return true
	})
	hst.bind("mdOpenIconsFolder", func() bool { shellOpen(iconsDir(), ""); return true })
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
	hst.bind("mdRemovePack", func(name string) bool { return a.removeIconPack(name) })
	hst.bind("mdMediaWidgetIcon", func() string {
		if a.cfg.MediaWidgetIcon == "" {
			return "" // default: Settings draws the music note itself
		}
		if img := a.customIcon(a.cfg.MediaWidgetIcon); img != nil {
			return iconDataURL(img, 64)
		}
		return ""
	})
	hst.bind("mdResetMediaIcon", func() bool { return a.setMediaWidgetIcon("") })
	hst.bind("mdTrayWidgetIcon", func() string {
		if a.cfg.IconOverrides["widget:tray"] == "" {
			return "" // default: Settings draws the up arrow itself
		}
		if img := a.customIcon(a.cfg.IconOverrides["widget:tray"]); img != nil {
			return iconDataURL(img, 64)
		}
		return ""
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
		if key == "widget:media" {
			return a.setMediaWidgetIcon(a.pack.Path(name))
		}
		a.setIconOverride(key, a.pack.Path(name))
		return true
	})
	hst.bind("mdResetKeyIcon", func(key string) bool {
		if key == "widget:media" {
			return a.setMediaWidgetIcon("")
		}
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
	hst.bind("mdFonts", func() []string { return fontFamilies() })
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
	// Save my setup / Restore a setup (the pickers open after this call returns)
	hst.bind("mdBackup", func() bool { pPostMessageW.Call(a.hwnd, wmPick, 8, 0); return true })
	hst.bind("mdRestore", func() bool { pPostMessageW.Call(a.hwnd, wmPick, 9, 0); return true })
	hst.bind("mdQuit", func() bool {
		pPostMessageW.Call(a.hwnd, WM_CLOSE, 0, 0)
		return true
	})
}

// animSpeedOf is the 1–5 animation speed (0 or out of range = 5, the normal speed).
func animSpeedOf(n int) int {
	if n < 1 || n > 10 {
		return 5
	}
	return n
}

func builtinPackNames() []string {
	var out []string
	for _, v := range builtinPackVariants {
		out = append(out, v.name)
	}
	sort.Strings(out)
	return out
}
