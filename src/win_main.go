//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	timerAnim     = 1
	timerScan     = 2
	timerRelayout = 3
	wmAppBar      = WM_APP + 1
	wmDPIRelayout = WM_APP + 15
	appName       = "DG-Dock"
	runValueName  = "DG-Dock"
)

type App struct {
	dynPacks                          map[string]string // pack folder → dynamic variant ("" = not dynamic)
	lastPackSig                       string
	colourSeq                         uintptr // the latest colour change (applyColours)
	swAcrylic                         bool    // the Settings window has a blurred, see-through background
	panelRgnW, panelRgnH, panelRgnDPI int32   // the panel's rounded shape was made for this size

	wx struct { // the weather widget (win_weather.go)
		w         *Weather
		err, note string
		busy      bool
		next      time.Time
		lat, lon  float64
		fix       time.Time
	}
	swHwnd   uintptr // the All settings window
	swHost   *webHost
	swPage   string
	swPlaced bool

	trayFollow    trayFollowState
	trayFollowSeq uintptr

	cfg     *Config
	cfgPath string
	logPath string

	dock   *Dock
	rd     *Renderer
	te     *TextEngine
	glyphs *TextEngine

	hwnd, blur                                                           uintptr
	memDC, dib                                                           uintptr
	canvas                                                               *image.RGBA
	dibW, dibH                                                           int
	dibCapW, dibCapH                                                     int
	dibBits                                                              []byte
	fullPresent                                                          bool
	lastWinX                                                             int32
	lastWinY                                                             int32
	frameMode                                                            bool
	quickLayout                                                          bool
	compositorClockChecked, compositorClockAvailable, compositorOccluded bool
	compositorBoostChecked, compositorBoostAvailable, compositorBoosted  bool

	winX, winY int32
	mon, work  RECT
	baseline   int32
	appbar     bool
	appbarRect RECT
	inRelayout bool

	items    map[string]*Item
	icons    map[string]*image.RGBA
	styled   map[string]*IconSet
	runOrder []string
	lastFg   uintptr

	mouseDown          bool
	downIdx            int
	downX, downY       int32
	downWinX, downWinY int32

	menuOpen          bool
	hiddenFullscreen  bool
	overAt            time.Time // intellihide: last window-overlap check
	overDock          bool
	animUntil         time.Time
	msgTaskbarCreated uint32 // "TaskbarCreated": appbars must register again when it arrives
	workOverride      bool   // we set the work area ourselves (Windows didn't honour the appbar)
	workOrig          RECT   // the work area before that, to put back
	workCheckAt       time.Time
	searchKeyName     string
	volEP             uintptr // IAudioEndpointVolume
	volAt             time.Time
	brAcc             int // brightness scroll left over from touchpad steps
	displaysMoved     bool
	panelW            int       // settings panel width in CSS px (0 = quick settings width)
	peekUntil         time.Time // a hidden dock stays out until then (an app wants attention)
	noSave            bool      // a setup was just restored: don't write the old settings over it
	msgShellHook      uint32    // "SHELLHOOK": apps asking for attention
	page              int       // current page of apps
	pageOut           []*Item
	pageDir           int
	stackItem         *Item  // folder card being browsed
	stackPath         string // folder shown inside it ("" = the pinned folder itself)
	stackScroll       int    // rows scrolled
	trayScroll        int    // system tray card: apps scrolled
	stackAcc          int
	drag              *dragged // what's being dragged over the dock from outside
	dropOn            bool     // the gap showing where it'll land is open
	dropBefore        *Item
	dropSection       int
	pageAcc           int
	pageAt            time.Time
	relayoutSoon      bool
	lastWins          []WinInfo // open windows from the last scan
	animInterval      int
	lastLeave         time.Time
	blurRect          RECT
	blurMaskKey       string
	blurShown         bool
	trashFull         bool
	scanCount         int
	taskbarCreatedMsg uint32
	menuDarkSet       int
	needRender        bool
	settings          *webHost
	pack              *IconPack
	pendingPicker     int
	pendingKey        [2]string // icon picker for a tray app or a file: key, name
	pickKey           string    // whose icon "Choose from a file…" is for
	dialogOpen        bool
	panelH            int // CSS px reported by the page
	panelHiddenAt     time.Time
	panelShownAt      time.Time
	usbDrives         []usbDrive // USB drives plugged in now
	usbWasBusy        bool
	lp                launchpadState
	apkBusy           bool
	apkDone           chan apkResult
	usbPopAt          time.Time
	panelBtnWasDown   bool
	panelAnimating    bool
	panelAcrylic      bool
	panelHwnd         uintptr
	panelAnchor       [2]int32
	panelExt          bool     // Settings was opened from an extra dock on another display
	panelExtEdge      [2]int32 // …and this is where its near edge sits (screen coordinates)
	searchOnMon       RECT     // Search was opened from an extra dock: show it centred on this display
	iconQueue         []string
	thumbWin          uintptr
	thumbs            []thumbReg
	thumbKey          string
	thumbShown        bool
	curMon            uintptr
	monDwell          time.Time
	mainMissing       int       // extra dock: checks in a row without the main dock
	cfgSeen           time.Time // extra dock: settings.json time last loaded
	mediaW            *mediaWatcher
	media             mediaState
	mediaArt          *IconSet
	mediaArtSrc       *image.RGBA
	mediaFallbackPath string
	mediaFallbackArt  *IconSet
	mediaNames        map[string]string
	mediaAccent       [3]uint8
	mediaAccentSrc    *image.RGBA
	meter             uintptr
	wifi              wifiInfo
	wifiAt            time.Time
	meterAt           time.Time
	waveAt            time.Time
	waveT0            time.Time
	waveFrameAt       time.Time
	wavePeak          float64
	waveIdleAt        time.Time
	waveGrowAt        time.Time
	popDown           bool
	appsCache         []AppEntry
	appsCacheTime     time.Time
}

var app *App

func (a *App) logf(format string, args ...interface{}) {
	f, err := os.OpenFile(a.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	f.Close()
}

// ---------- config ----------

func (a *App) loadConfig() {
	dir := dataDir()
	os.MkdirAll(dir, 0755)
	a.cfgPath = filepath.Join(dir, "settings.json")
	a.logPath = filepath.Join(dir, "log.txt")
	if st, err := os.Stat(a.logPath); err == nil && st.Size() > 512*1024 {
		os.Remove(a.logPath)
	}
	a.cfg = defaultConfig()
	if b, err := os.ReadFile(a.cfgPath); err == nil {
		if jerr := json.Unmarshal(b, a.cfg); jerr != nil {
			// A damaged settings file (e.g. power cut mid-save) must not quietly turn
			// into a fresh dock: keep the damaged copy and fall back to the last good one.
			a.cfg = defaultConfig()
			bad := a.cfgPath + ".damaged-" + time.Now().Format("20060102-150405")
			os.WriteFile(bad, b, 0644)
			a.logf("settings.json couldn't be read (%v); kept it as %s", jerr, filepath.Base(bad))
			if bk, err := os.ReadFile(a.cfgPath + ".bak"); err == nil && json.Unmarshal(bk, a.cfg) == nil {
				a.logf("restored settings from settings.json.bak")
			} else {
				a.cfg = defaultConfig()
			}
		} else {
			// last settings that loaded fine, for the case above
			if !isSecondary() {
				writeFileAtomic(a.cfgPath+".bak", b)
			}
		}
		AnimSlow = AnimSpeedFactor(animSpeedOf(a.cfg.AnimSpeed))
	}
	// Move older custom-icon references into the managed profile folder. This
	// preserves the icons if their original files later move or are removed.
	iconsUpdated := false
	for i := range a.cfg.Pins {
		p := &a.cfg.Pins[i]
		if p.Icon != "" {
			if kept := a.keepCustomIcon(pinKey(p), p.Icon); kept != p.Icon {
				p.Icon, iconsUpdated = kept, true
			}
		}
	}
	for key, path := range a.cfg.IconOverrides {
		if path != "" {
			if kept := a.keepCustomIcon(key, path); kept != path {
				a.cfg.IconOverrides[key], iconsUpdated = kept, true
			}
		}
	}
	if a.cfg.MediaWidgetIcon != "" {
		if kept := a.keepCustomIcon("widget:media", a.cfg.MediaWidgetIcon); kept != a.cfg.MediaWidgetIcon {
			a.cfg.MediaWidgetIcon, iconsUpdated = kept, true
		}
	}
	if iconsUpdated {
		a.saveConfig()
	}
	if !a.cfg.WidgetGroupCentered {
		// The requested centered widget layout supersedes saved right-side drag
		// anchors. Clear them once, then store the group in the middle section.
		if a.cfg.WidgetPositions == nil {
			a.cfg.WidgetPositions = map[string]string{}
		}
		if a.cfg.WidgetAnchors == nil {
			a.cfg.WidgetAnchors = map[string]string{}
		}
		for _, key := range []string{"clock", "status", "media", "tray"} {
			a.cfg.WidgetPositions[key] = "middle"
			delete(a.cfg.WidgetAnchors, key)
		}
		a.cfg.WidgetCenterApplied = true
		a.cfg.WidgetGroupCentered = true
		a.saveConfig()
	}
	if !a.cfg.WidgetSpacingV2 {
		// Earlier builds saved a gap that followed app padding. Apply the approved
		// 16 px widget rhythm once, including for people who had tried the old slider.
		a.cfg.WidgetSpacing = 16
		a.cfg.WidgetSpacingSet = true
		a.cfg.WidgetSpacingV2 = true
		a.saveConfig()
	}
	if !a.cfg.WidgetSpacingV3 {
		// widgets now share the app icons' spacing scale: start them level with the apps
		a.cfg.WidgetSpacing = a.cfg.AppPadding
		a.cfg.WidgetSpacingSet = true
		a.cfg.WidgetSpacingV3 = true
		a.saveConfig()
	}
	// Keep-space was removed because it was unreliable. Reset saved values from
	// older builds so maximised windows do not keep reserving dock space.
	if a.cfg.ReserveSpace {
		a.cfg.ReserveSpace = false
		a.saveConfig()
	}
	if a.cfg.IconSize < 32 || a.cfg.IconSize > 128 {
		a.cfg.IconSize = 54
	}
	if a.cfg.IconPackScale < 0 || a.cfg.IconPackScale > 150 {
		a.cfg.IconPackScale = 100
	}
	if a.cfg.AppIconScale < 0 || a.cfg.AppIconScale > 150 {
		a.cfg.AppIconScale = 100
	}
	if a.cfg.WidgetIconScale < 0 || a.cfg.WidgetIconScale > 150 {
		a.cfg.WidgetIconScale = 100
	}
	if a.cfg.Magnification < 1 || a.cfg.Magnification > 3 {
		a.cfg.Magnification = 1.0
	}
	a.cfg.AppPadding = max(0, min(a.cfg.AppPadding, 20))
	a.cfg.DockTransparency = max(0, min(a.cfg.DockTransparency, 100))
	if a.cfg.Theme == "liquid" {
		a.cfg.Theme = "auto"
	}
	if !a.cfg.DockShapeApplied {
		// Builds before dock-shape selection, and the first shape-selector build,
		// should keep the familiar pill layout as the initial choice.
		if a.cfg.Style != "pill" {
			a.cfg.Style = "pill"
		}
		a.cfg.DockShapeApplied = true
		a.saveConfig()
	} else if a.cfg.Style != "default" && a.cfg.Style != "pill" {
		a.cfg.Style = "pill"
		a.saveConfig()
	}
	if !a.cfg.PillApplied {
		a.cfg.PillApplied = true
		if docs := knownFolder("{FDD39AD0-238F-46AF-ADB4-6C85480369C7}"); docs != "" {
			has := false
			for _, p := range a.cfg.Pins {
				if strings.EqualFold(p.Target, docs) {
					has = true
				}
			}
			if !has {
				at := len(a.cfg.Pins)
				for i := range a.cfg.Pins {
					if a.cfg.Pins[i].Kind == KindTrash {
						at = i
					}
				}
				np := append([]Pin{}, a.cfg.Pins[:at]...)
				np = append(np, Pin{Kind: KindFile, Target: docs})
				a.cfg.Pins = append(np, a.cfg.Pins[at:]...)
			}
		}
		defer a.saveConfig()
	}
	if !a.cfg.MagOffApplied {
		a.cfg.Magnification = 1.0
		a.cfg.MagOffApplied = true
		defer a.saveConfig()
	}
	if !a.cfg.Initialized {
		a.cfg.Pins = defaultPins()
		a.cfg.Initialized = true
		a.saveConfig()
	}
}

const timerSave = 4

// saveConfigLater coalesces rapid changes (slider drags) into one write.
func (a *App) saveConfigLater() { pSetTimer.Call(a.hwnd, timerSave, 500, 0) }

func (a *App) saveConfig() {
	if isSecondary() || a.noSave {
		return // only the main dock saves settings; extra docks reload them
	}
	b, err := json.MarshalIndent(a.cfg, "", "  ")
	if err != nil {
		return
	}
	if err := writeFileAtomic(a.cfgPath, b); err != nil {
		a.logf("couldn't save settings: %v", err)
		return
	}
	notifyExtraDocks() // the docks on other displays update straight away
}

// writeFileAtomic writes to a temporary file first and then swaps it into place, so a
// crash or power cut mid-write leaves either the old file or the new one, never half.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Sync()
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil { // MoveFileEx(REPLACE_EXISTING) on Windows
		os.Remove(tmp)
		return err
	}
	return nil
}

func defaultPins() []Pin {
	var pins []Pin
	pins = append(pins, Pin{Kind: KindStart})
	explorer := filepath.Join(os.Getenv("WINDIR"), "explorer.exe")
	pins = append(pins, Pin{Kind: KindFile, Target: explorer, Name: "File Explorer"})
	edge := filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\Microsoft Edge.lnk`)
	if _, err := os.Stat(edge); err == nil {
		pins = append(pins, Pin{Kind: KindFile, Target: edge, Name: "Microsoft Edge"})
	}
	for _, id := range []string{
		"Microsoft.Windows.Photos_8wekyb3d8bbwe!App",
		"Microsoft.WindowsNotepad_8wekyb3d8bbwe!App",
		"Microsoft.WindowsCalculator_8wekyb3d8bbwe!App",
		"Microsoft.WindowsStore_8wekyb3d8bbwe!App",
		"windows.immersivecontrolpanel_cw5n1h2txyewy!microsoft.windows.immersivecontrolpanel",
	} {
		if shellDisplayName(`shell:AppsFolder\`+id) != "" {
			pins = append(pins, Pin{Kind: KindApp, Target: id})
		}
	}
	dl := filepath.Join(os.Getenv("USERPROFILE"), "Downloads")
	if _, err := os.Stat(dl); err == nil {
		pins = append(pins, Pin{Kind: KindFile, Target: dl})
	}
	if docs := knownFolder("{FDD39AD0-238F-46AF-ADB4-6C85480369C7}"); docs != "" {
		pins = append(pins, Pin{Kind: KindFile, Target: docs})
	}
	pins = append(pins, Pin{Kind: KindTrash})
	return pins
}

// ---------- items ----------

func pinKey(p *Pin) string { return "pin:" + p.Kind + ":" + strings.ToLower(p.Target) }

func (a *App) rawIcon(parsing string, force bool) *image.RGBA {
	if !force {
		if img, ok := a.icons[parsing]; ok {
			return img
		}
	}
	img := loadShellIcon(parsing, 256)
	a.icons[parsing] = img
	return img
}

// assignIcon sets an item's raw icon (falling back to a letter tile) and styles it.
func (a *App) assignIcon(it *Item, key string, raw *image.RGBA) {
	if raw == nil {
		key = "letter:" + it.Name
		if img, ok := a.icons[key]; ok && img != nil {
			raw = img
		} else {
			raw = LetterIcon(256, it.Name, a.te)
			a.icons[key] = raw
		}
	}
	it.Raw = raw
	it.RawKey = key
	a.styleItem(it)
}

func (a *App) itemStyle(it *Item) string {
	if a.cfg.IconStyle == "circle" {
		if a.dock.Dark || a.cfg.Style != "glass" {
			return "circle-dark"
		}
		return "circle-light"
	}
	if a.cfg.IconStyle == "original" {
		return "orig"
	}
	// Rounded-tiles style tiles everything on the dock, folders and the Recycle Bin
	// included (they used to fall through to "plain").
	if a.dock.Dark || a.cfg.Style != "glass" {
		return "tile-dark"
	}
	return "tile-light"
}

func (a *App) styleItem(it *Item) {
	if it.Raw == nil {
		return
	}
	style := a.itemStyle(it)
	baseKey := it.RawKey + "|" + style
	base := a.styled[baseKey]
	if base == nil {
		base = NewIconSet(styleIcon(it.Raw, style))
		a.styled[baseKey] = base
	}
	img := base.levels[0]
	ck := baseKey
	if it.PackIcon && a.cfg.IconPackScale != 100 {
		ck = fmt.Sprintf("%s|pack-scale-%d", baseKey, a.cfg.IconPackScale)
		if is := a.styled[ck]; is != nil {
			img = is.levels[0]
		} else {
			is := NewIconSet(ScaleIconArtwork(img, float64(a.cfg.IconPackScale)/100))
			a.styled[ck] = is
			img = is.levels[0]
		}
	}
	if a.cfg.AppIconScale != 100 {
		ck = fmt.Sprintf("%s|app-scale-%d", ck, a.cfg.AppIconScale)
		if is := a.styled[ck]; is != nil {
			it.Icon = is
			return
		}
		img = ScaleIconMask(img, float64(a.cfg.AppIconScale)/100)
	}
	is := NewIconSet(img)
	a.styled[ck] = is
	it.Icon = is
}

func styleIcon(raw *image.RGBA, style string) *image.RGBA {
	switch style {
	case "tile-dark":
		return MacTile(raw, true)
	case "tile-light":
		return MacTile(raw, false)
	case "circle-dark":
		return CircleMaskIcon(MacTile(raw, true))
	case "circle-light":
		return CircleMaskIcon(MacTile(raw, false))
	case "plain":
		return MacPlain(raw)
	default:
		return raw
	}
}

func (a *App) restyleAll() {
	for _, it := range a.items {
		a.styleItem(it)
	}
	if it := a.items["w:tray"]; it != nil {
		it.Icon = a.trayWidgetIcon()
		it.scaled, it.scaledSrc = nil, nil
		it.scaledSize = 0
	}
	a.needRender = true
}

// customIcon loads a user-chosen icon file.
func (a *App) customIcon(path string) *image.RGBA {
	if path == "" {
		return nil // an empty path would load Windows' generic "This PC" icon
	}
	if v, ok := a.dynVariant(path); ok { // a Black and White Icons icon: painted in today's colours
		return a.dynIcon(path, v)
	}
	if img, ok := a.icons["custom:"+path]; ok && img != nil {
		return img
	}
	var img *image.RGBA
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp":
		img = loadImageFile(path)
	default:
		img = loadShellIcon(path, 256)
	}
	if img != nil {
		a.icons["custom:"+path] = img
	}
	return img
}

func isAppFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".lnk", ".appref-ms", ".url", ".bat", ".cmd":
		return true
	}
	return false
}

func (a *App) pinItem(p *Pin, idx int) *Item {
	key := pinKey(p)
	it, ok := a.items[key]
	if ok {
		it.Pin = p
		it.PinIdx = idx
		if p.Name != "" {
			it.Name = p.Name
		}
		return it
	}
	it = &Item{Key: key, Kind: p.Kind, Pin: p, PinIdx: idx, Target: p.Target, Scale: 1, Appear: 1}
	switch p.Kind {
	case KindStart:
		it.Name = "Start"
		k := "start-icon"
		if a.icons[k] == nil {
			a.icons[k] = StartIcon(256)
		}
		it.Raw, it.RawKey = a.icons[k], k
	case KindTrash:
		it.Name = "Recycle Bin"
		it.Section = 1
		it.Raw, it.RawKey = a.rawIcon(recycleBinParsing, false), recycleBinParsing
	case KindApp:
		parsing := `shell:AppsFolder\` + p.Target
		it.Name = shellDisplayName(parsing)
		it.Raw, it.RawKey = a.rawIcon(parsing, false), parsing
		it.AUMID = p.Target
		if !strings.Contains(p.Target, "!") {
			it.ExePath = normPath(appTargetPath(p.Target))
		}
	default: // file
		it.Kind = KindFile
		ext := strings.ToLower(filepath.Ext(p.Target))
		if st, err := os.Stat(p.Target); err == nil && st.IsDir() {
			it.IsDir, it.Browse = true, p.Target
		} else if ext == ".lnk" { // a shortcut to a folder (e.g. dragged from Quick access) acts like the folder
			if t := resolveShortcut(p.Target); t != "" {
				if st, err := os.Stat(t); err == nil && st.IsDir() {
					it.IsDir, it.Browse = true, t
				}
			}
		}
		switch ext {
		case ".exe":
			it.ExePath = normPath(p.Target)
			it.Name = exeDisplayName(p.Target)
		case ".lnk":
			if t := resolveShortcut(p.Target); strings.HasSuffix(strings.ToLower(t), ".exe") {
				it.ExePath = normPath(t)
			}
			b := filepath.Base(p.Target)
			it.Name = strings.TrimSuffix(b, filepath.Ext(b))
		default:
			it.Name = shellDisplayName(p.Target)
		}
		if it.Name == "" {
			it.Name = filepath.Base(p.Target)
		}
		if it.IsDir || !isAppFile(p.Target) {
			it.Section = 1
		}
		it.Raw, it.RawKey = a.rawIcon(p.Target, false), p.Target
	}
	if p.Name != "" {
		it.Name = p.Name
	}
	icon := p.Icon
	if icon == "" && p.Kind == KindApp {
		icon = a.cfg.IconOverrides[appIconKey(p.Target)] // chosen in Launchpad
	}
	if icon != "" {
		if img := a.customIcon(icon); img != nil {
			it.Raw, it.RawKey = img, "custom:"+icon
			it.PackIcon = true
		}
	} else {
		a.applyPack(it)
	}
	a.assignIcon(it, it.RawKey, it.Raw)
	if a.dock != nil && len(a.dock.Items) > 0 {
		it.Appear = 0
	}
	a.items[key] = it
	return it
}

func (a *App) rebuildItems() {
	var left, right []*Item
	var trash *Item
	used := map[string]bool{}
	for i := range a.cfg.Pins {
		it := a.pinItem(&a.cfg.Pins[i], i)
		used[it.Key] = true
		switch {
		case it.Kind == KindTrash:
			trash = it
		case it.Section == 1 && !a.cfg.NoFolderFiles:
			right = append(right, it)
		default:
			left = append(left, it)
		}
	}
	// open apps that aren't pinned get their own pill between the apps and the folders,
	// so they never shuffle the pages around
	var running []*Item
	if a.cfg.ShowRunning {
		for _, k := range a.runOrder {
			if it, ok := a.items[k]; ok && (it.Running || it.Leaving) {
				running = append(running, it)
				used[k] = true
			}
		}
	}
	if trash != nil {
		right = append(right, trash)
	}
	left = a.pageItems(left, used)
	var widgetLeft, widgetMiddle, widgetRight, widgetAnchored []*Item
	if !a.cfg.NoWidgets && a.dock.Notch {
		for _, w := range a.widgetItems() {
			used[w.Key] = true
			if a.cfg.WidgetAnchors[w.Widget] != "" {
				widgetAnchored = append(widgetAnchored, w)
				continue
			}
			switch a.cfg.WidgetPositions[w.Widget] {
			case "left":
				widgetLeft = append(widgetLeft, w)
			case "middle", "":
				widgetMiddle = append(widgetMiddle, w)
			default:
				widgetRight = append(widgetRight, w)
			}
		}
	}
	// its separator opens and closes smoothly as the pill comes and goes
	sepRun := a.items["seprun"]
	live := 0
	for _, it := range running {
		if !it.Leaving {
			live++
		}
	}
	for _, it := range a.items {
		it.InRun = false
	}
	if live > 0 && len(left)+len(right)+len(widgetRight) > 0 {
		if sepRun == nil {
			sepRun = &Item{Key: "seprun", Kind: KindSep, Scale: 1}
			a.items["seprun"] = sepRun
			sepRun.Leaving = true // so the line below sees a fresh start
		}
		if sepRun.Leaving { // the pill buds off
			sepRun.Leaving = false
			a.dock.BudStart, a.dock.BudIn = time.Now(), true
			a.setAnimInterval(15)
		}
	} else if sepRun != nil && !sepRun.Leaving { // the last one closed: melt back
		sepRun.Leaving = true
		a.dock.BudStart, a.dock.BudIn = time.Now(), false
		a.setAnimInterval(15)
	}
	for _, it := range running {
		it.InRun = true
	}
	if sepRun != nil {
		sepRun.InRun = true
		if !sepRun.Leaving || sepRun.Appear > 0.01 || !a.dock.BudStart.IsZero() {
			used["seprun"] = true
		} else {
			sepRun = nil
		}
	}
	// the pills, in the order chosen in Settings › Layout (Apps, Open apps, Folders, Widgets)
	sepItem := func(key string) *Item {
		sep, ok := a.items[key]
		if !ok {
			sep = &Item{Key: key, Kind: KindSep, Scale: 1, Appear: 1}
			a.items[key] = sep
		}
		used[key] = true
		return sep
	}
	items := append([]*Item{}, widgetLeft...)
	started := false // left-hand widgets share the first pill
	for _, g := range pillOrder(a.cfg.PillOrder) {
		var part []*Item
		var sep *Item
		switch g {
		case "apps":
			part = append(append(part, left...), widgetMiddle...)
			if len(part) > 0 && started {
				sep = sepItem("sep0")
			}
		case "running":
			if len(running) == 0 {
				continue
			}
			part = running
			if started && sepRun != nil {
				sep = sepRun
			}
		case "files":
			part = right
			if len(part) > 0 && started {
				sep = sepItem("sep")
			}
		case "widgets":
			part = widgetRight
			if len(part) > 0 && started {
				sep = sepItem("sep2")
			}
		}
		if len(part) == 0 {
			continue
		}
		if sep != nil {
			items = append(items, sep)
		}
		items = append(items, part...)
		started = true
	}
	if len(widgetAnchored) > 0 {
		before := make(map[string][]*Item, len(widgetAnchored))
		for _, w := range widgetAnchored {
			anchor := a.cfg.WidgetAnchors[w.Widget]
			if anchor != "@end" {
				before[anchor] = append(before[anchor], w)
			}
		}
		placed := make(map[*Item]bool, len(widgetAnchored))
		ordered := make([]*Item, 0, len(items)+len(widgetAnchored))
		for _, it := range items {
			for _, w := range before[it.Key] {
				ordered = append(ordered, w)
				placed[w] = true
			}
			ordered = append(ordered, it)
		}
		for _, w := range widgetAnchored {
			if !placed[w] {
				ordered = append(ordered, w)
			}
		}
		items = ordered
	}
	items = a.placeDropGap(items, used)
	// forget items that are gone (but keep running-item objects alive)
	for k, it := range a.items {
		if !used[k] && it.Kind != KindRunning {
			delete(a.items, k)
		}
	}
	a.dock.Items = items
	a.needRender = true
	if a.relayoutSoon && a.hwnd != 0 {
		a.relayoutSoon = false
		pSetTimer.Call(a.hwnd, timerRelayout, 10, 0)
	}
}

// ---------- running apps ----------

func (a *App) scanRunning() {
	a.assignRunning(listAppWindows())
}

// assignRunning matches open windows to dock items: pinned apps on the page you're
// looking at get their windows; everything else goes in the open-apps pill. Page
// changes call it again with the last window list, so the swap happens with the slide.
func (a *App) assignRunning(wins []WinInfo) {
	a.lastWins = wins
	if a.isolated() { // like GNOME's "isolate monitors": only this display's apps
		here := make([]WinInfo, 0, len(wins))
		for _, w := range wins {
			if a.onThisDisplay(w.Hwnd) {
				here = append(here, w)
			}
		}
		wins = here
	}
	pinned := []*Item{}
	for _, it := range a.dock.Items {
		// icons sliding away from the page you just left don't count: their apps
		// belong in the open-apps pill straight away
		if it.Pin != nil && !it.PageOut {
			pinned = append(pinned, it)
		}
	}
	newWins := map[*Item][]uintptr{}
	assigned := make([]bool, len(wins))
	for i, w := range wins {
		if w.AUMID == "" {
			continue
		}
		for _, it := range pinned {
			if it.AUMID != "" && strings.EqualFold(it.AUMID, w.AUMID) {
				newWins[it] = append(newWins[it], w.Hwnd)
				assigned[i] = true
				break
			}
		}
	}
	for i, w := range wins {
		if assigned[i] || w.Exe == "" {
			continue
		}
		for _, it := range pinned {
			if it.ExePath != "" && it.ExePath == w.Exe {
				newWins[it] = append(newWins[it], w.Hwnd)
				assigned[i] = true
				break
			}
		}
	}
	// group the rest
	running := map[string]bool{}
	for i, w := range wins {
		if assigned[i] {
			continue
		}
		var key string
		packaged := strings.Contains(w.AUMID, "!")
		if packaged {
			key = "run:aumid:" + strings.ToLower(w.AUMID)
		} else if filepath.Base(w.Exe) == "applicationframehost.exe" || w.Exe == "" {
			continue
		} else {
			key = "run:exe:" + w.Exe
		}
		it, ok := a.items[key]
		if !ok {
			it = &Item{Key: key, Kind: KindRunning, Scale: 1, Appear: 0}
			if packaged {
				it.AUMID = w.AUMID
				it.Target = w.AUMID
				parsing := `shell:AppsFolder\` + w.AUMID
				it.Name = shellDisplayName(parsing)
				it.Raw, it.RawKey = a.rawIcon(parsing, false), parsing
			} else {
				it.ExePath = w.Exe
				it.Target = w.Exe
				it.Name = exeDisplayName(w.Exe)
				it.Raw, it.RawKey = a.rawIcon(w.Exe, false), w.Exe
			}
			if it.Name == "" {
				it.Name = w.Title
			}
			a.items[key] = it
		}
		a.refreshRunningIcon(it, w)
		newWins[it] = append(newWins[it], w.Hwnd)
		running[key] = true
	}

	changed := false
	// update running flags on everything we know
	for _, it := range a.items {
		ws := newWins[it]
		r := len(ws) > 0
		if r != it.Running {
			changed = true
		}
		it.Running = r
		it.Windows = ws
	}
	// apps that just closed shrink out smoothly instead of vanishing
	for k, it := range a.items {
		if it.Kind != KindRunning {
			continue
		}
		if running[k] {
			if it.Leaving {
				it.Leaving = false
				changed = true
			}
		} else if !it.Leaving {
			it.Leaving = true
			changed = true
		}
	}
	// maintain running order
	var order []string
	for _, k := range a.runOrder {
		if it, ok := a.items[k]; running[k] || (ok && it.Leaving) {
			order = append(order, k)
		}
	}
	for k := range running {
		found := false
		for _, o := range order {
			if o == k {
				found = true
				break
			}
		}
		if !found {
			order = append(order, k)
			changed = true
		}
	}
	if len(order) != len(a.runOrder) {
		changed = true
	}
	a.runOrder = order
	if changed {
		a.rebuildItems()
	}
}

// dropGone removes closed apps once their shrink-out animation has finished.
func (a *App) dropGone() {
	gone := false
	for k, it := range a.items {
		if it.Kind == KindRunning && it.Leaving && it.Appear <= 0.002 {
			delete(a.items, k)
			gone = true
		}
	}
	if !gone {
		return
	}
	var order []string
	for _, k := range a.runOrder {
		if _, ok := a.items[k]; ok {
			order = append(order, k)
		}
	}
	a.runOrder = order
	a.rebuildItems()
}

// ---------- flyout content ----------

func (a *App) popContent(it *Item) PopContent {
	pc := PopContent{Title: it.Name}
	switch {
	case it.Kind == KindTrash:
		n := recycleBinCount()
		if n == 0 {
			pc.Subtitle = "Empty"
		} else if n == 1 {
			pc.Subtitle = "1 item"
		} else {
			pc.Subtitle = fmt.Sprintf("%d items", n)
		}
	case it.Kind == KindStart:
		pc.Subtitle = "Open the Start menu"
		if !a.cfg.StartIsStart {
			pc.Title, pc.Subtitle = "Launchpad", ""
		}
	case it.Kind == KindWidget:
		return a.widgetPop(it)
	case it.IsDir:
		return a.stackPop(it)
	case len(it.Windows) > 0:
		if len(it.Windows) == 1 {
			pc.Subtitle = "1 window open"
		} else {
			pc.Subtitle = fmt.Sprintf("%d windows open", len(it.Windows))
		}
		if !a.cfg.NoPreviews {
			pc.Mode = PopThumbs
		}
		for _, w := range it.Windows {
			t := windowText(w)
			if t == "" {
				t = it.Name
			}
			ic, _, _ := pIsIconic.Call(w)
			pc.Rows = append(pc.Rows, PopRow{Title: t, Hwnd: w, Min: ic != 0})
		}
	case it.Bouncing:
		pc.Subtitle = "Opening…"
	default:
		pc.Subtitle = "Click to open"
	}
	return pc
}

// ---------- theme ----------

func readDWORD(path, name string) (uint32, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

func (a *App) updateTheme() {
	dark := false
	switch a.cfg.Theme {
	case "dark":
		dark = true
	case "light":
		dark = false
	default:
		if v, ok := readDWORD(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "SystemUsesLightTheme"); ok {
			dark = v == 0
		}
	}
	// The separate acrylic popup adds a rectangular DWM shadow on some Windows
	// builds, so leave it disabled.
	blur := false
	if v, ok := readDWORD(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "EnableTransparency"); ok && v == 0 {
		blur = false
	}
	if a.blur == 0 {
		blur = false
	}
	if pal := a.dockPalette(dark); pal != a.dock.Pal {
		a.dock.Pal = pal
		a.needRender = true
	}
	if wc, on := a.widgetColour(); wc != a.dock.WidgetTint || on != a.dock.WidgetTintOn {
		a.dock.WidgetTint, a.dock.WidgetTintOn = wc, on
		a.needRender = true
	}
	if wp := wallPalette(); len(wp) > 0 {
		var sum [3]float64
		for _, c := range wp {
			for i := range sum {
				sum[i] += float64(c[i])
			}
		}
		for i := range sum {
			a.dock.WallTone[i] = uint8(sum[i] / float64(len(wp)))
		}
		a.dock.HasWallTone = true
	}
	if cd := a.cfg.ClockFace == "digital"; cd != a.dock.ClockDigital {
		a.dock.ClockDigital = cd
		a.needRender = true
	}
	if dg := a.cfg.WidgetStyle == "dg"; dg != a.dock.WidgetDG {
		a.dock.WidgetDG = dg
		a.needRender = true
	}
	if by := a.widgetTintsBy(); !sameTints(by, a.dock.WidgetTintBy) {
		a.dock.WidgetTintBy = by
		a.needRender = true
	}
	uiDark := a.uiDarkFor(dark)
	accentOverride = a.accentChoice(uiDark)
	// keep the accent readable on what it sits on (an accent matching a red dock vanished)
	if acc, _ := accentColors(uiDark); len(acc) == 7 {
		if c, ok := parseHex(acc); ok {
			bg := [3]uint8{0x20, 0x20, 0x20}
			if !uiDark {
				bg = [3]uint8{0xf3, 0xf3, 0xf3}
			}
			if _, custom := a.dockColour(); custom {
				bg = a.dock.Pal.Fill
			}
			if r := readableOn(c, bg); r != c {
				accentOverride = hexOf(r)
			}
		}
	}
	menuTintOn, menuTintC = a.uiTinted(), a.dock.Pal.Fill
	if std := !a.uiTinted(); std != a.dock.CardStd {
		a.dock.CardStd = std
		a.needRender = true
	}
	menuAlpha = a.uiAlpha()
	if acc, _ := accentColors(uiDark); len(acc) == 7 {
		var r, g, b uint8
		fmt.Sscanf(acc, "#%02x%02x%02x", &r, &g, &b)
		menuAccent = [3]uint8{r, g, b}
		if c := (color.RGBA{r, g, b, 255}); c != a.dock.Accent {
			a.dock.Accent = c
			a.needRender = true
		}
	}
	if dark != a.dock.Dark || blur != a.dock.BlurOn {
		darkChanged := dark != a.dock.Dark
		a.dock.Dark = dark
		a.dock.BlurOn = blur
		a.needRender = true
		if darkChanged {
			a.restyleAll()
			a.applySettingsTheme() // an open Settings window or panel follows Windows' light/dark switch
		}
	}
	a.updateBlurMaterial(dark, false)
	mode := 3
	if dark {
		mode = 2
	}
	if mode != a.menuDarkSet {
		a.menuDarkSet = mode
		setMenuTheme(mode)
	}
	a.repaintPackIcons() // Black and White Icons follow the accent and light/dark
}

// updateBlurMaterial applies a tinted acrylic backdrop without the default popup shadow.
func (a *App) updateBlurMaterial(dark, frosted bool) {
	if a.blur == 0 {
		return
	}
	policy := ACCENTPOLICY{}
	if frosted {
		policy.AccentState = 4 // ACCENT_ENABLE_ACRYLICBLURBEHIND
		if dark {
			policy.GradientColor = 0x6620293A
		} else {
			policy.GradientColor = 0x66D8E8F4
		}
	}
	data := WINCOMPATTRDATA{Attrib: 19, Data: uintptr(unsafe.Pointer(&policy)), Size: unsafe.Sizeof(policy)}
	if pSetWindowCompositionAttribute.Find() == nil {
		pSetWindowCompositionAttribute.Call(a.blur, uintptr(unsafe.Pointer(&data)))
	}
	// The blur helper is a popup window; suppress its DWM non-client drop shadow.
	noFrame := uint32(1)                                                         // DWMNCRP_DISABLED
	pDwmSetWindowAttribute.Call(a.blur, 2, uintptr(unsafe.Pointer(&noFrame)), 4) // DWMWA_NCRENDERING_POLICY
}

func setMenuTheme(mode int) {
	h, err := windows.LoadLibrary("uxtheme.dll")
	if err != nil {
		return
	}
	if p, err := windows.GetProcAddressByOrdinal(h, 135); err == nil && p != 0 {
		syscall.SyscallN(p, uintptr(mode))
	}
	if p, err := windows.GetProcAddressByOrdinal(h, 136); err == nil && p != 0 {
		syscall.SyscallN(p)
	}
}

// ---------- taskbar ----------

func trayWindows() []uintptr {
	var out []uintptr
	if h := explorerTray(); h != 0 {
		out = append(out, h)
	}
	var prev uintptr
	for i := 0; i < 8; i++ {
		h, _, _ := pFindWindowExW.Call(0, prev, up(u16("Shell_SecondaryTrayWnd")), 0)
		if h == 0 {
			break
		}
		out = append(out, h)
		prev = h
	}
	return out
}

func taskbarAutoHideState() int {
	var abd APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	r, _, _ := pSHAppBarMessage.Call(4, uintptr(unsafe.Pointer(&abd)))
	return int(r & 1)
}

func setTaskbarAutoHide(on int) {
	var abd APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	if h := explorerTray(); h != 0 {
		abd.HWnd = h
	}
	abd.LParam = uintptr(on)
	pSHAppBarMessage.Call(10, uintptr(unsafe.Pointer(&abd)))
}

func (a *App) applyTaskbar() {
	if isSecondary() {
		return // the main dock looks after the taskbar
	}
	if a.cfg.HideTaskbar {
		if a.cfg.PrevTaskbarAutoHide < 0 {
			a.cfg.PrevTaskbarAutoHide = taskbarAutoHideState()
			a.saveConfig()
		}
		if taskbarAutoHideState() == 0 {
			setTaskbarAutoHide(1)
		}
		if a.displayMode() == "each" {
			a.ensureTaskbarsOnAllDisplays()
		} else {
			a.restoreTaskbarsOnAllDisplays()
		}
		for _, h := range trayWindows() {
			if v, _, _ := pIsWindowVisible.Call(h); v != 0 {
				pShowWindow.Call(h, SW_HIDE)
			}
		}
	}
}

func (a *App) restoreTaskbar() {
	if isSecondary() {
		return // the main dock looks after the taskbar
	}
	a.restoreTaskbarsOnAllDisplays()
	unclipTaskbars()
	for _, h := range trayWindows() {
		pShowWindow.Call(h, SW_SHOWNA)
	}
	if a.cfg.PrevTaskbarAutoHide >= 0 {
		setTaskbarAutoHide(a.cfg.PrevTaskbarAutoHide)
		a.cfg.PrevTaskbarAutoHide = -1
		a.saveConfig()
	}
}

// ---------- appbar (reserve space) ----------

func (a *App) appbarMsg(msg uintptr, abd *APPBARDATA) uintptr {
	abd.CbSize = uint32(unsafe.Sizeof(*abd))
	abd.HWnd = a.hwnd
	r, _, _ := pSHAppBarMessage.Call(msg, uintptr(unsafe.Pointer(abd)))
	return r
}

func (a *App) removeAppbar() {
	a.restoreWorkArea()
	if a.appbar {
		var abd APPBARDATA
		a.appbarMsg(1, &abd)
		a.appbar = false
		a.appbarRect = RECT{}
	}
}

// checkWorkArea makes sure maximised windows really stop above the dock. Normally Windows
// does this for any appbar; if it hasn't (some shells ignore third-party appbars), the
// dock sets the screen's work area itself, and puts it back when it no longer needs it.
func (a *App) checkWorkArea() {
	a.workCheckAt = time.Now()
	if !a.appbar || a.appbarRect == (RECT{}) {
		a.restoreWorkArea()
		return
	}
	// SPI_SETWORKAREA is system-wide and only changes the primary display. Do not
	// apply its fallback while the dock is on a secondary display, or it trims the
	// wrong monitor's work area and can fight the visible secondary taskbar.
	primary, _, _ := pMonitorFromPoint.Call(0, 1) // MONITOR_DEFAULTTOPRIMARY
	if primary != 0 && a.curMon != primary {
		a.restoreWorkArea()
		return
	}
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(a.curMon, uintptr(unsafe.Pointer(&mi)))
	w, r := mi.RcWork, a.appbarRect
	want := w
	switch a.dock.Orient {
	case OrientLeft:
		want.Left = max(w.Left, r.Right)
	case OrientRight:
		want.Right = min(w.Right, r.Left)
	case OrientTop:
		want.Top = max(w.Top, r.Bottom)
	default:
		want.Bottom = min(w.Bottom, r.Top)
	}
	if want == w {
		return // Windows is keeping the space
	}
	if !a.workOverride {
		a.workOrig = w
	}
	a.workOverride = true
	pSystemParametersInfoW.Call(0x002F, 0, uintptr(unsafe.Pointer(&want)), 0x2) // SPI_SETWORKAREA, SPIF_SENDCHANGE
	a.logf("work area %v didn't leave room for the dock %v: set it to %v", w, r, want)
}

// restoreWorkArea undoes checkWorkArea.
func (a *App) restoreWorkArea() {
	if !a.workOverride {
		return
	}
	a.workOverride = false
	orig := a.workOrig // what Windows had before the dock trimmed it
	pSystemParametersInfoW.Call(0x002F, 0, uintptr(unsafe.Pointer(&orig)), 0x2)
}

// ---------- layout / DIB ----------

func (a *App) readMonitor() {
	h := a.curMon
	if want := a.fixedDisplay(); want != "" {
		if m := monitorByDevice(want); m != 0 {
			h = m
		}
	} else if h == 0 || a.cfg.MainDisplay {
		h, _, _ = pMonitorFromPoint.Call(0, 1)
	}
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if ok, _, _ := pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		// that display was unplugged: go back to the main display
		h, _, _ = pMonitorFromPoint.Call(0, 1)
		pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
	}
	a.curMon = h
	a.mon, a.work = mi.RcMonitor, mi.RcWork
	wallMon = a.mon
}

func (a *App) localPoint(cx, cy int32) (float64, float64) {
	return float64(cx - a.winX), float64(cy - a.winY)
}

func (a *App) dpiScale() float64 {
	// the scale of the display the dock is going to, not the one the window is still on
	if s := monitorDPI(a.curMon); s > 0 {
		return s
	}
	if pGetDpiForWindow.Find() == nil && a.hwnd != 0 {
		if d, _, _ := pGetDpiForWindow.Call(a.hwnd); d >= 72 {
			return float64(d) / 96
		}
	}
	return 1
}

func orientOf(pos string) int {
	switch pos {
	case "left":
		return OrientLeft
	case "right":
		return OrientRight
	case "top":
		return OrientTop
	}
	return OrientBottom
}

func (a *App) relayout() {
	if a.inRelayout {
		return
	}
	a.inRelayout = true
	defer func() { a.inRelayout = false }()

	a.readMonitor()
	d := a.dock
	d.Orient = orientOf(a.cfg.Position)
	d.CircleIcons = a.cfg.IconStyle == "circle"
	d.SetMetrics(a.dpiScale(), a.cfg.IconSize, a.cfg.Magnification, a.cfg.AppPadding, a.cfg.Connected, a.cfg.IconStyle != "original", a.cfg.Style)
	// widget spacing on the same scale as app spacing (3 px + the setting)
	d.WidgetGap = math.Round(float64(3+max(0, min(a.cfg.WidgetSpacing, 20))) * d.Scale)
	d.WidgetScale = 1
	if a.cfg.WidgetSize > 0 {
		d.WidgetScale = float64(max(60, min(a.cfg.WidgetSize, 160))) / 100
	}
	d.Transparency = a.cfg.DockTransparency
	T := int32(d.Thickness())
	r := int32(math.Round(d.PlateH + d.M))
	// maximised windows stop a little above the dock rather than touching it
	r += int32(math.Round(d.Scale * 8))

	var edge, spanA, spanB int32 // edge coordinate and the span along it
	wantAppbar := a.cfg.ReserveSpace && !a.cfg.AutoHide
	if wantAppbar && a.quickLayout && a.appbar && a.baseline != 0 {
		// live change (slider drag): keep the current edge, fix up the reserved area afterwards
		edge = a.baseline
		switch d.Orient {
		case OrientBottom, OrientTop:
			spanA, spanB = a.mon.Left, a.mon.Right
		default:
			spanA, spanB = a.appbarRect.Top, a.appbarRect.Bottom
		}
		pSetTimer.Call(a.hwnd, timerRelayout, 350, 0)
	} else if wantAppbar {
		var abd APPBARDATA
		if !a.appbar {
			abd.UCallbackMessage = wmAppBar
			a.appbarMsg(0, &abd)
			a.appbar = true
		}
		abd = APPBARDATA{}
		switch d.Orient {
		case OrientLeft:
			abd.UEdge = 0
			abd.Rc = RECT{a.mon.Left, a.mon.Top, a.mon.Left + r, a.mon.Bottom}
		case OrientRight:
			abd.UEdge = 2
			abd.Rc = RECT{a.mon.Right - r, a.mon.Top, a.mon.Right, a.mon.Bottom}
		case OrientTop:
			abd.UEdge = 1
			abd.Rc = RECT{a.mon.Left, a.mon.Top, a.mon.Right, a.mon.Top + r}
		default:
			abd.UEdge = 3
			abd.Rc = RECT{a.mon.Left, a.mon.Bottom - r, a.mon.Right, a.mon.Bottom}
		}
		a.appbarMsg(2, &abd)
		switch d.Orient {
		case OrientLeft:
			abd.Rc.Right = abd.Rc.Left + r
			edge, spanA, spanB = abd.Rc.Left, abd.Rc.Top, abd.Rc.Bottom
		case OrientRight:
			abd.Rc.Left = abd.Rc.Right - r
			edge, spanA, spanB = abd.Rc.Right, abd.Rc.Top, abd.Rc.Bottom
		case OrientTop:
			abd.Rc.Bottom = abd.Rc.Top + r
			edge, spanA, spanB = abd.Rc.Top, abd.Rc.Left, abd.Rc.Right
		default:
			abd.Rc.Top = abd.Rc.Bottom - r
			edge, spanA, spanB = abd.Rc.Bottom, abd.Rc.Left, abd.Rc.Right
		}
		if abd.Rc != a.appbarRect {
			a.appbarMsg(3, &abd)
			a.appbarRect = abd.Rc
			a.workCheckAt = time.Time{} // check Windows took it, on the next scan
		}
	} else {
		a.removeAppbar()
		a.readMonitor()
		area := a.work
		if a.cfg.HideTaskbar {
			area = a.mon
		}
		switch d.Orient {
		case OrientLeft:
			edge, spanA, spanB = area.Left, area.Top, area.Bottom
		case OrientRight:
			edge, spanA, spanB = area.Right, area.Top, area.Bottom
		case OrientTop:
			edge, spanA, spanB = area.Top, a.mon.Left, a.mon.Right
		default:
			edge, spanA, spanB = area.Bottom, a.mon.Left, a.mon.Right
		}
	}
	a.baseline = edge

	var W, H int
	switch d.Orient {
	case OrientLeft:
		a.winX, a.winY = edge, spanA
		W, H = int(T), int(spanB-spanA)
	case OrientRight:
		a.winX, a.winY = edge-T, spanA
		W, H = int(T), int(spanB-spanA)
	case OrientTop:
		a.winX, a.winY = spanA, edge
		W, H = int(spanB-spanA), int(T)
	default:
		a.winX, a.winY = spanA, edge-T
		W, H = int(spanB-spanA), int(T)
	}
	if W != a.dibW || H != a.dibH || a.canvas == nil {
		a.ensureDIB(W, H)
	}
	if a.winX != a.lastWinX || a.winY != a.lastWinY {
		a.fullPresent = true
		a.lastWinX, a.lastWinY = a.winX, a.winY
	}
	d.W, d.H = W, H
	d.CenterU = d.L() / 2
	a.overAt = time.Time{}
	if a.cfg.AutoHide {
		if d.HideTarget > 0 || d.HideOffset == 0 && len(d.Items) == 0 {
			d.HideTarget = a.hiddenOffset()
			d.HideOffset = d.HideTarget
		}
	} else {
		d.HideTarget = 0
	}
	a.blurRect = RECT{}
	a.blurMaskKey = ""
	a.rd.lastDirty = image.Rectangle{}
	d.Step(time.Now())
	a.needRender = true
	a.render()
}

func (a *App) hiddenOffset() float64 {
	return a.dock.PlateH + a.dock.M + 8*a.dock.Scale
}

// ensureDIB makes sure the backing bitmap is at least w×h (with slack, so dragging the
// size slider doesn't reallocate every step) and points the canvas at a w×h view of it.
func (a *App) ensureDIB(w, h int) {
	if w > a.dibCapW || h > a.dibCapH || a.dib == 0 {
		cw, ch := w, (h+127)/128*128
		if a.memDC != 0 {
			pDeleteDC.Call(a.memDC)
			a.memDC = 0
		}
		if a.dib != 0 {
			pDeleteObject.Call(a.dib)
			a.dib = 0
		}
		var bi BITMAPINFO
		bi.Header.BiSize = uint32(unsafe.Sizeof(bi.Header))
		bi.Header.BiWidth = int32(cw)
		bi.Header.BiHeight = -int32(ch)
		bi.Header.BiPlanes = 1
		bi.Header.BiBitCount = 32
		a.memDC, _, _ = pCreateCompatibleDC.Call(0)
		var bits uintptr
		a.dib, _, _ = pCreateDIBSection.Call(a.memDC, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
		if a.dib == 0 || bits == 0 {
			a.logf("CreateDIBSection failed")
			a.canvas = nil
			return
		}
		pSelectObject.Call(a.memDC, a.dib)
		a.dibBits = unsafe.Slice((*byte)(unsafe.Pointer(bits)), cw*ch*4)
		a.dibCapW, a.dibCapH = cw, ch
	}
	a.canvas = &image.RGBA{Pix: a.dibBits[:(h-1)*a.dibCapW*4+w*4], Stride: a.dibCapW * 4, Rect: image.Rect(0, 0, w, h)}
	a.dibW, a.dibH = w, h
	a.fullPresent = true
}

type ULWINFO struct {
	CbSize   uint32
	HdcDst   uintptr
	PptDst   *POINT
	Psize    *SIZE
	HdcSrc   uintptr
	PptSrc   *POINT
	CrKey    uint32
	Pblend   *BLENDFUNCTION
	DwFlags  uint32
	PrcDirty *RECT
}

var pUpdateLayeredWindowIndirect = user32.NewProc("UpdateLayeredWindowIndirect")

func (a *App) render() {
	if a.canvas == nil {
		return
	}
	changed := a.rd.Render(a.dock, a.canvas)
	pt := POINT{a.winX, a.winY}
	sz := SIZE{int32(a.dibW), int32(a.dibH)}
	src := POINT{0, 0}
	blend := BLENDFUNCTION{SourceConstantAlpha: 255, AlphaFormat: 1}
	if a.fullPresent || pUpdateLayeredWindowIndirect.Find() != nil {
		pUpdateLayeredWindow.Call(a.hwnd, 0, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)),
			a.memDC, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
		a.fullPresent = false
	} else if !changed.Empty() {
		// only upload the part of the bitmap that changed this frame
		dirty := RECT{int32(changed.Min.X), int32(changed.Min.Y), int32(changed.Max.X), int32(changed.Max.Y)}
		info := ULWINFO{HdcSrc: a.memDC, PptSrc: &src, Pblend: &blend, DwFlags: ULW_ALPHA, PrcDirty: &dirty, Psize: &sz, PptDst: &pt}
		info.CbSize = uint32(unsafe.Sizeof(info))
		if r, _, _ := pUpdateLayeredWindowIndirect.Call(a.hwnd, uintptr(unsafe.Pointer(&info))); r == 0 {
			pUpdateLayeredWindow.Call(a.hwnd, 0, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)),
				a.memDC, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
		}
	}
	a.updateBlur()
	a.needRender = false
}

func (a *App) updateBlur() {
	if a.blur == 0 {
		return
	}
	d := a.dock
	show := d.BlurOn && !a.hiddenFullscreen && d.HideOffset < d.PlateH*0.5 && len(d.Items) > 0
	if !show {
		if a.blurShown {
			pShowWindow.Call(a.blur, SW_HIDE)
			a.blurShown = false
		}
		return
	}
	inset := 1.0 * d.Scale
	bx, by, bw, bh := d.rectUV(d.PlateU0+inset, d.PlateV0+inset, d.PlateU1-inset, d.PlateV1-inset)
	r := RECT{
		Left:   a.winX + int32(math.Round(bx)),
		Top:    a.winY + int32(math.Round(by)),
		Right:  a.winX + int32(math.Round(bx+bw)),
		Bottom: a.winY + int32(math.Round(by+bh)),
	}
	maskKey := fmt.Sprintf("%v|orient:%d|pill:%v|connected:%v", r, d.Orient, d.Pill, d.Connected)
	var groups [][3]float64
	if d.Pill && !d.Connected {
		groups = d.pillGroups()
		for _, g := range groups {
			maskKey += fmt.Sprintf("|%.1f:%.1f", g[0], g[1])
		}
	}
	if r != a.blurRect || maskKey != a.blurMaskKey {
		w, h := r.Right-r.Left, r.Bottom-r.Top
		if r != a.blurRect {
			pSetWindowPos.Call(a.blur, 0, uintptr(r.Left), uintptr(r.Top), uintptr(w), uintptr(h), SWP_NOACTIVATE|SWP_NOZORDER)
		}
		var region uintptr
		addRounded := func(x0, y0, x1, y1 float64) {
			if x1-x0 < 1 || y1-y0 < 1 {
				return
			}
			lx0, ly0 := int32(math.Round(x0)), int32(math.Round(y0))
			lx1, ly1 := int32(math.Round(x1)), int32(math.Round(y1))
			rad := uintptr(int32(math.Round(math.Min(float64(ly1-ly0), float64(lx1-lx0)) * 0.52)))
			part, _, _ := pCreateRoundRectRgn.Call(uintptr(lx0), uintptr(ly0), uintptr(lx1), uintptr(ly1), rad, rad)
			if part == 0 {
				return
			}
			if region == 0 {
				region = part
			} else {
				pCombineRgn.Call(region, region, part, 2) // RGN_OR
				pDeleteObject.Call(part)
			}
		}
		if len(groups) > 0 {
			for _, g := range groups {
				gx, gy, gw, gh := d.rectUV(g[0]+inset, d.PlateV0+inset, g[1]-inset, d.PlateV1-inset)
				addRounded(gx-bx, gy-by, gx-bx+gw, gy-by+gh)
			}
		} else {
			addRounded(0, 0, float64(w), float64(h))
		}
		if region != 0 {
			if result, _, _ := pSetWindowRgn.Call(a.blur, region, 1); result == 0 {
				pDeleteObject.Call(region)
			}
		}
		a.blurRect = r
		a.blurMaskKey = maskKey
	}
	if !a.blurShown {
		pShowWindow.Call(a.blur, SW_SHOWNA)
		a.blurShown = true
	}
}

// ---------- animation tick ----------

func (a *App) setAnimInterval(ms int) {
	if ms != a.animInterval {
		a.animInterval = ms
		pSetTimer.Call(a.hwnd, timerAnim, uintptr(ms), 0)
	}
}

func (a *App) setCompositorBoost(enable bool) {
	if !a.compositorBoostChecked {
		a.compositorBoostChecked = true
		a.compositorBoostAvailable = pDCompositionBoostCompositorClock.Find() == nil
	}
	if !a.compositorBoostAvailable || enable == a.compositorBoosted {
		return
	}
	if !enable {
		pDCompositionBoostCompositorClock.Call(0)
		a.compositorBoosted = false
		return
	}
	if hr, _, _ := pDCompositionBoostCompositorClock.Call(1); int32(hr) >= 0 {
		a.compositorBoosted = true
	} else {
		a.compositorBoostAvailable = false
	}
}

func (a *App) waitForDisplayFrame() {
	if !a.compositorClockChecked {
		a.compositorClockChecked = true
		a.compositorClockAvailable = pDCompositionWaitForCompositorClock.Find() == nil
	}
	if a.compositorClockAvailable {
		// The compositor clock follows the active display cadence, including
		// Dynamic Refresh Rate changes, without a fixed-rate polling timer.
		if result, _, _ := pDCompositionWaitForCompositorClock.Call(0, 0, 0xFFFFFFFF); result == 0 {
			a.compositorOccluded = false
			return
		}
		a.compositorOccluded = true // the display can return occluded when powered off
		a.setCompositorBoost(false)
		time.Sleep(16 * time.Millisecond)
		return
	}
	// Older Windows builds keep the DWM-synchronized fallback.
	if result, _, _ := pDwmFlush.Call(); result != 0 {
		time.Sleep(4 * time.Millisecond)
	}
}

func (a *App) shouldBoostCompositorClock() bool {
	d := a.dock
	return a.mouseDown || a.popDown || d.Drag != nil || d.Active && d.Busy() ||
		!d.PageT0.IsZero() || !d.BudStart.IsZero()
}

func (a *App) tick() {
	if a.hiddenFullscreen {
		a.checkPanelOutsideClick()
		// a full-screen app on this display mustn't stop the dock following the
		// pointer to another one
		cx, cy := getCursorPos()
		a.checkMonitorSwitch(cx, cy)
		a.setAnimInterval(250)
		return
	}
	d := a.dock
	a.checkPanelOutsideClick()
	a.tickUSB()
	a.tickTransfers()
	cx, cy := getCursorPos()
	onMon := cx >= a.mon.Left && cx < a.mon.Right && cy >= a.mon.Top && cy < a.mon.Bottom
	mx, my := float64(cx-a.winX), float64(cy-a.winY)
	if a.mouseDown {
		mx, my = float64(cx-a.downWinX), float64(cy-a.downWinY)
	}
	d.MouseValid = onMon || d.Drag != nil
	d.MouseX, d.MouseY = mx, my
	a.checkMonitorSwitch(cx, cy)
	a.updateWave()
	if it := a.items["w:media"]; it != nil && time.Since(it.WVolAt) < 1600*time.Millisecond {
		a.needRender = true // volume overlay fading out
		a.animUntil = time.Now().Add(100 * time.Millisecond)
	}
	if it := a.items["w:status"]; it != nil && time.Since(it.WBrAt) < 1600*time.Millisecond {
		// the ring and its number glide to the new level instead of jumping
		glide := func(v, to float64) float64 {
			v += (to - v) * 0.3
			if math.Abs(to-v) < 0.002 {
				v = to
			}
			return v
		}
		it.WBrShow, it.WBrDimShow = glide(it.WBrShow, it.WBr), glide(it.WBrDimShow, it.WBrDim)
		a.needRender = true // brightness ring fading back to battery
		a.animUntil = time.Now().Add(100 * time.Millisecond)
	}

	// auto-hide
	if a.cfg.AutoHide && !a.menuOpen && d.Drag == nil && !a.mouseDown {
		u, v := d.toUV(d.MouseX, d.MouseY)
		hidden := d.HideTarget > 0
		clear := a.lp.shown || time.Now().Before(a.peekUntil) || !a.cfg.HideAlways && !a.windowOverDock()
		if hidden {
			if clear || onMon && v <= 3 && v >= -60*d.Scale && u >= d.PlateU0-20 && u <= d.PlateU1+20 {
				d.HideTarget = 0
				a.lastLeave = time.Time{}
			}
		} else {
			inZone := clear || d.Active || d.PopInside || (u >= d.PlateU0 && u <= d.PlateU1 && v <= d.PlateV1+10*d.Scale)
			if !inZone {
				if a.lastLeave.IsZero() {
					a.lastLeave = time.Now()
				} else if time.Since(a.lastLeave) > 550*time.Millisecond {
					d.HideTarget = a.hiddenOffset()
				}
			} else {
				a.lastLeave = time.Time{}
			}
		}
	}

	// drag
	if a.mouseDown && d.Drag == nil && a.downIdx >= 0 && a.downIdx < len(d.Items) {
		dx, dy := float64(cx-a.downX), float64(cy-a.downY)
		if math.Hypot(dx, dy) > 7*d.Scale && a.cfg.LockDock {
			// locked: no moving or removing; a drag also shouldn't count as a click
			a.downIdx = -1
			d.Pressed = nil
		} else if math.Hypot(dx, dy) > 7*d.Scale {
			it := d.Items[a.downIdx]
			if !it.draggable() && it.Kind == KindRunning && !it.Leaving {
				// an open app that isn't pinned: dragging it pins it (drag it off to unpin)
				aumid, exe := it.AUMID, it.ExePath
				a.keepInDock(it)
				for _, p := range d.Items {
					if p.Pin != nil && (aumid != "" && p.AUMID == aumid || exe != "" && strings.EqualFold(p.ExePath, exe)) {
						it = p
						break
					}
				}
			}
			if it.draggable() || it.Kind == KindWidget {
				d.Drag = it
				d.Pressed = nil
			}
		}
	}
	if d.Drag != nil {
		d.DragX, d.DragY = d.MouseX, d.MouseY
		d.DragRemove = d.Drag.Kind != KindWidget && d.DragOff()
		if !d.DragRemove {
			if d.Drag.Kind == KindWidget {
				a.reorderWidgetDrag()
			} else {
				a.reorderDrag()
			}
		}
		a.needRender = true
	}

	now := time.Now()
	if d.Step(now) {
		// anything still moving keeps frames coming at the display's refresh rate
		a.animUntil = now.Add(150 * time.Millisecond)
		a.render()
	} else if a.needRender {
		a.render()
	}
	a.dropGone()
	a.finishPageOut()
	a.finishDropGap()
	if sr := a.items["seprun"]; sr != nil && sr.Leaving && sr.Appear <= 0.01 && a.dock.BudStart.IsZero() && a.dock.indexOf(sr) >= 0 {
		a.rebuildItems() // the running-apps pill has closed
	}
	if a.dock.PopItem == nil {
		a.trayScroll = 0
	}
	if a.dock.PopItem == nil && a.stackItem != nil {
		a.stackItem, a.stackPath, a.stackScroll = nil, "", 0 // next time the card opens at the top
	}
	if len(a.iconQueue) > 0 {
		a.loadQueuedIcons()
		if a.needRender {
			a.render()
		}
	}
	a.updateThumbs()
	// Poll at display rate while the dock is active or animating. At rest, a
	// 20 Hz pointer poll lowers idle wakeups while still entering hover promptly.
	if d.Active || d.Drag != nil || time.Now().Before(a.animUntil) {
		a.setAnimInterval(16)
	} else {
		a.setAnimInterval(50)
	}
}

func (a *App) reorderDrag() {
	d := a.dock
	drag := d.Drag
	var others []*Item
	for _, it := range d.Items {
		if it != drag && it.draggable() && it.Section == drag.Section && !it.PageOut {
			others = append(others, it)
		}
	}
	rank := 0
	for _, it := range others {
		if it.U < d.DragAlong() {
			rank++
		}
	}
	curRank := 0
	for _, it := range d.Items {
		if it == drag {
			break
		}
		if it.draggable() && it.Section == drag.Section && !it.PageOut {
			curRank++
		}
	}
	if rank == curRank {
		return
	}
	pins := a.cfg.Pins
	moving := pins[drag.PinIdx]
	rest := append([]Pin{}, pins[:drag.PinIdx]...)
	rest = append(rest, pins[drag.PinIdx+1:]...)
	insertAt := len(rest)
	if rank < len(others) {
		targetKey := others[rank].Key
		for i := range rest {
			if pinKey(&rest[i]) == targetKey {
				insertAt = i
				break
			}
		}
	} else if len(others) > 0 {
		lastKey := others[len(others)-1].Key
		for i := range rest {
			if pinKey(&rest[i]) == lastKey {
				insertAt = i + 1
				break
			}
		}
	}
	np := append([]Pin{}, rest[:insertAt]...)
	np = append(np, moving)
	np = append(np, rest[insertAt:]...)
	a.cfg.Pins = np
	a.rebuildItems()
}

func (a *App) reorderWidgetDrag() {
	d := a.dock
	drag := d.Drag
	if drag == nil || drag.Kind != KindWidget {
		return
	}
	anchor := "@end"
	best := math.Inf(1)
	u := d.DragAlong()
	for _, it := range d.Items {
		if it == drag || it.Kind == KindWidget || it.Kind == KindSep || it.Kind == KindGap || it.PageOut || it.Pin == nil {
			continue
		}
		if it.U >= u && it.U < best {
			best = it.U
			anchor = it.Key
		}
	}
	if a.cfg.WidgetAnchors == nil {
		a.cfg.WidgetAnchors = map[string]string{}
	}
	if a.cfg.WidgetAnchors[drag.Widget] == anchor {
		return
	}
	a.cfg.WidgetAnchors[drag.Widget] = anchor
	a.rebuildItems()
	a.needRender = true
}

// ---------- scan tick ----------

func (a *App) scan() {
	a.scanCount++
	fg, _, _ := pGetForegroundWindow.Call()
	if fg != 0 && fg != a.hwnd && fg != a.blur {
		a.lastFg = fg
	}
	// full screen apps
	fs := false
	if fg != 0 && fg != a.hwnd && fg != a.blur {
		cls := className(fg)
		style, _, _ := pGetWindowLongPtrW.Call(fg, ^uintptr(15)) // GWL_STYLE
		hasCaption := style&0x00C00000 == 0x00C00000
		if cls != "Progman" && cls != "WorkerW" && cls != "Shell_TrayWnd" && !hasCaption {
			var r RECT
			pGetWindowRect.Call(fg, uintptr(unsafe.Pointer(&r)))
			if r.Left <= a.mon.Left && r.Top <= a.mon.Top && r.Right >= a.mon.Right && r.Bottom >= a.mon.Bottom {
				fs = true
			}
		}
	}
	if !isSecondary() {
		winKey.pass.Store(fs) // a full-screen app or game keeps the Windows key
	}
	fs = fs && a.cfg.HideFullscreen
	if fs != a.hiddenFullscreen {
		a.hiddenFullscreen = fs
		if fs {
			pShowWindow.Call(a.hwnd, SW_HIDE)
			if a.blurShown {
				pShowWindow.Call(a.blur, SW_HIDE)
				a.blurShown = false
			}
		} else {
			pShowWindow.Call(a.hwnd, SW_SHOWNA)
			a.needRender = true
			a.render()
		}
	}
	if fs {
		return
	}
	a.scanRunning()
	if a.updateWidgets() {
		a.needRender = true
	}
	if d := a.dock; d.PopItem != nil && d.PopTarget > 0 && d.Pop.Mode != PopGrid {
		np := a.popContent(d.PopItem)
		if np.Title != d.Pop.Title || np.Subtitle != d.Pop.Subtitle || np.Weather != d.Pop.Weather || len(np.Rows) != len(d.Pop.Rows) || (len(np.Rows) > 0 && np.Rows[0].Title != d.Pop.Rows[0].Title) {
			d.Pop = np
			a.needRender = true
		}
	}
	if a.cfg.HideTaskbar && a.scanCount%2 == 0 {
		a.applyTaskbar()
	}
	if a.scanCount%4 == 0 {
		full := recycleBinCount() > 0
		if full != a.trashFull {
			a.trashFull = full
			for _, it := range a.dock.Items {
				if it.Kind == KindTrash {
					if it.PackIcon || (a.pack != nil && a.pack.Match(it.Name) != "") {
						continue // an icon pack's bin: keep it (the Windows icon would replace it)
					}
					if img := a.rawIcon(recycleBinParsing, true); img != nil && it.Pin != nil && it.Pin.Icon == "" {
						a.assignIcon(it, recycleBinParsing+fmt.Sprint(full), img)
					}
				}
			}
			a.needRender = true
		}
		a.updateTheme()
	}
	if a.needRender {
		a.render()
	}
}

// ---------- clicks ----------

func (a *App) isFront(it *Item) bool {
	fg := a.lastFg
	if cur, _, _ := pGetForegroundWindow.Call(); cur != 0 && cur != a.hwnd && cur != a.blur {
		fg = cur
	}
	root, _, _ := pGetAncestor.Call(fg, GA_ROOTOWNER)
	for _, w := range it.Windows {
		if w == fg || w == root {
			return true
		}
	}
	return false
}

// perDisplay: there's a dock on more than one display.
func (a *App) perDisplay() bool { return isSecondary() || a.multiDock() }

// isolated: each dock only shows the apps open on its own display.
func (a *App) isolated() bool { return a.perDisplay() && a.cfg.IsolateDisplays }

// onThisDisplay: the window is on this dock's display (a minimised window counts
// where it will come back).
func (a *App) onThisDisplay(h uintptr) bool {
	m, _, _ := pMonitorFromWindow.Call(h, 2)
	return m != 0 && m == a.curMon
}

func (a *App) hasWindowHere(it *Item) bool {
	for _, w := range it.Windows {
		if a.onThisDisplay(w) {
			return true
		}
	}
	return false
}

func (a *App) click(it *Item, newInstance bool) {
	it.Attention = false
	if it.Kind == KindWidget {
		a.widgetClick(it)
		return
	}
	if (it.Kind == KindApp || it.Kind == KindFile || it.Kind == KindRunning) && len(it.Windows) > 0 && !newInstance && !it.IsDir {
		if a.isFront(it) {
			for _, w := range it.Windows {
				// with a dock on each display, only this display's windows go down
				if !a.perDisplay() || a.onThisDisplay(w) || !a.hasWindowHere(it) {
					pShowWindowAsync.Call(w, SW_MINIMIZE)
				}
			}
			return
		}
		// with a dock on each display, a click brings up the app's window on this
		// display first (open before minimised), then any other
		ws := it.Windows
		if a.perDisplay() {
			var here, there []uintptr
			for _, w := range ws {
				if a.onThisDisplay(w) {
					here = append(here, w)
				} else {
					there = append(there, w)
				}
			}
			ws = append(here, there...)
		}
		target := ws[0]
		for _, w := range ws {
			if ic, _, _ := pIsIconic.Call(w); ic == 0 {
				target = w
				break
			}
			if a.perDisplay() && a.onThisDisplay(w) {
				target = w // a minimised window here beats an open one elsewhere
				break
			}
		}
		activateWindow(target)
		return
	}
	a.launch(it)
}

func (a *App) launch(it *Item) {
	if it.Kind == KindStart && !a.cfg.StartIsStart {
		a.toggleSearch()
		return
	}
	if it.Kind == KindWidget {
		a.widgetClick(it)
		return
	}
	switch it.Kind {
	case KindRunning:
		if strings.Contains(it.AUMID, "!") {
			shellOpen("explorer.exe", `shell:AppsFolder\`+it.AUMID)
		} else {
			shellOpen(it.ExePath, "")
		}
	default:
		launchItem(it)
	}
	if it.Kind != KindStart && it.Kind != KindTrash && !it.IsDir && it.Section == 0 && !a.cfg.NoBounce {
		it.Bouncing = true
		it.BounceStart = time.Now()
		it.Running = false
		a.setAnimInterval(15)
	}
}

// ---------- pins ----------

func (a *App) hasPin(kind, target string) int {
	for i := range a.cfg.Pins {
		if a.cfg.Pins[i].Kind == kind && strings.EqualFold(a.cfg.Pins[i].Target, target) {
			return i
		}
	}
	return -1
}

func (a *App) addPin(p Pin) {
	if a.hasPin(p.Kind, p.Target) >= 0 {
		return
	}
	at := len(a.cfg.Pins)
	for i := range a.cfg.Pins {
		if a.cfg.Pins[i].Kind == KindTrash {
			at = i
		}
	}
	np := append([]Pin{}, a.cfg.Pins[:at]...)
	np = append(np, p)
	np = append(np, a.cfg.Pins[at:]...)
	a.cfg.Pins = np
	a.saveConfig()
	a.rebuildItems()
	a.scanRunning()
	a.render()
	a.refreshSettings()
}

// setDockSpecialPin controls the two fixed system items. Start stays locked
// against drag and unpin actions, while these explicit settings can show or hide it.
func (a *App) setDockSpecialPin(kind string, on bool) {
	if kind != KindStart && kind != KindTrash {
		return
	}
	i := a.hasPin(kind, "")
	if on && i < 0 {
		if kind == KindStart {
			a.cfg.Pins = append([]Pin{{Kind: KindStart}}, a.cfg.Pins...)
			a.saveConfig()
			a.rebuildItems()
			a.scanRunning()
			a.render()
			a.refreshSettings()
		} else {
			a.addPin(Pin{Kind: KindTrash})
		}
	} else if !on && i >= 0 {
		a.removePinAt(i)
	}
}

func (a *App) removePinAt(i int) {
	if i < 0 || i >= len(a.cfg.Pins) {
		return
	}
	np := append([]Pin{}, a.cfg.Pins[:i]...)
	np = append(np, a.cfg.Pins[i+1:]...)
	a.cfg.Pins = np
	a.saveConfig()
	a.rebuildItems()
	a.scanRunning()
	a.refreshSettings()
}

func (a *App) keepInDock(it *Item) {
	if strings.Contains(it.AUMID, "!") {
		a.addPin(Pin{Kind: KindApp, Target: it.AUMID})
	} else if it.ExePath != "" {
		a.addPin(Pin{Kind: KindFile, Target: it.ExePath, Name: it.Name})
	}
}

// ---------- menus ----------

// ---------- startup ----------

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func isStartupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValueName)
	return err == nil
}

// fixStartupPath points the "start with Windows" entry at this copy (an older
// download may have registered itself from the Downloads folder).
func (a *App) fixStartupPath() {
	if !isStartupEnabled() || !isInstalledCopy() {
		return
	}
	setStartup(true)
}

func setStartup(on bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if on {
		// Point at the installed copy when there is one. A copy that isn't installed
		// (e.g. the setup file in Downloads, run with "Run without installing") would
		// otherwise open the Setup window at sign-in instead of the dock, so it's
		// registered with --portable.
		cmd := `"` + installedPath() + `"`
		if !isInstalledCopy() && installedVersion() == 0 {
			cmd = `"` + exePath() + `" --portable`
		}
		k.SetStringValue(runValueName, cmd)
	} else {
		k.DeleteValue(runValueName)
	}
}

// ---------- drop files ----------

func (a *App) onDrop(hdrop uintptr) {
	n, _, _ := pDragQueryFileW.Call(hdrop, 0xFFFFFFFF, 0, 0)
	var files []string
	for i := uintptr(0); i < n; i++ {
		buf := make([]uint16, 1024)
		pDragQueryFileW.Call(hdrop, i, uintptr(unsafe.Pointer(&buf[0])), 1024)
		files = append(files, windows.UTF16ToString(buf))
	}
	var pt POINT
	pDragQueryPoint.Call(hdrop, uintptr(unsafe.Pointer(&pt)))
	pDragFinish.Call(hdrop)
	if len(files) == 0 {
		return
	}
	idx := a.dock.ItemAt(float64(pt.X), float64(pt.Y))
	if idx >= 0 {
		it := a.dock.Items[idx]
		switch {
		case it.Kind == KindTrash:
			recycleFiles(files)
			return
		case it.Widget == "usb":
			a.copyToDrive(it.UsbRoot, files)
			return
		case it.IsDir && it.Target != "" && canDropInto(it.Target, files):
			a.dropIntoFolder(it, files, folderDropMoves(it.Target, files, 0))
			return
		case it.ExePath != "" && !it.IsDir:
			for _, f := range files {
				shellOpen(it.ExePath, `"`+f+`"`)
			}
			if !a.cfg.NoBounce {
				it.Bouncing = true
				it.BounceStart = time.Now()
			}
			return
		}
	}
	for _, f := range files {
		a.addPin(Pin{Kind: KindFile, Target: f})
	}
}

// ---------- window procedure ----------

func (a *App) quit() {
	if !isSecondary() {
		stopAllDisplayDocks()
	}
	stopAllWatches()
	closeWebHosts()
	stopTray()
	a.removeAppbar()
	if a.cfg.HideTaskbar {
		a.restoreTaskbar()
	}
	a.saveConfig()
	pPostQuitMessage.Call(0)
}

// crashed is the last thing the dock does when something panics: it writes the stack
// to the log and gives the taskbar and the screen space back before exiting, so a
// crash never leaves someone without a taskbar.
func crashed(r any) {
	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)
	if a := app; a != nil {
		a.logf("panic: %v\n%s", r, buf[:n])
		func() {
			defer func() { recover() }() // a second panic while tidying up must not stop the exit
			a.restoreTaskbar()
			a.removeAppbar()
		}()
	}
	os.Exit(1)
}

// guarded runs f with the crash handler, for work done outside the window procedure
// (the animation loop, other threads).
func guarded(f func()) {
	defer func() {
		if r := recover(); r != nil {
			crashed(r)
		}
	}()
	f()
}

func wndProc(hwnd, msg, wparam, lparam uintptr) (ret uintptr) {
	defer func() {
		if r := recover(); r != nil {
			crashed(r)
		}
	}()
	a := app
	switch uint32(msg) {
	case WM_MOUSEACTIVATE:
		return MA_NOACTIVATE
	case WM_TIMER:
		switch wparam {
		case timerAnim:
			if !a.frameMode {
				a.tick()
			}
		case timerScan:
			if isSecondary() {
				a.followMain()
			}
			a.scan()
			if a.scanCount%4 == 1 {
				a.scanUSB() // also checked straight away when Windows says a device came or went
			}
			if !isSecondary() {
				a.checkApkFolder()
			}
			if time.Since(a.workCheckAt) > 2*time.Second {
				a.checkWorkArea()
			}
			a.keepDimOnTop()
			if a.items["w:status"] != nil && a.curMon != 0 {
				a.brightFor(a.curMon) // ask the monitor about DDC/CI before the first scroll
			}
		case timerRelayout:
			pKillTimer(hwnd, timerRelayout)
			a.relayout()
			a.refreshBrightness(a.displaysMoved)
			a.displaysMoved = false
			a.syncDisplayDocks() // displays may have been plugged in or removed
		case timerSave:
			pKillTimer(hwnd, timerSave)
			a.saveConfig()
		case timerFollow:
			pKillTimer(hwnd, timerFollow)
			a.followMain()
		case timerIdentify:
			a.closeIdentify()
		case timerLaunchpad:
			a.revealLaunchpad()
		case timerPrewarm:
			pKillTimer(hwnd, timerPrewarm)
			a.prewarmLaunchpad()
		case timerUsb:
			pKillTimer(hwnd, timerUsb)
			a.scanUSB()
		case timerQuickScan:
			pKillTimer(hwnd, timerQuickScan)
			a.scanRunning()
			a.refreshPop()
			if a.needRender {
				a.render()
			}
		case timerPopRefresh:
			pKillTimer(hwnd, timerPopRefresh)
			a.scanRunning()
			if d := a.dock; d.PopItem != nil {
				d.Pop = a.popContent(d.PopItem)
				a.needRender = true
			}
		}
		return 0
	case WM_LBUTTONDOWN:
		cx, cy := getCursorPos()
		a.downWinX, a.downWinY = a.winX, a.winY
		lx, ly := a.localPoint(cx, cy)
		if a.dock.InPop(lx, ly) {
			a.popDown = true
			pSetCapture.Call(hwnd)
			return 0
		}
		a.mouseDown = true
		a.downX, a.downY = cx, cy
		a.downIdx = a.dock.ItemAt(lx, ly)
		if a.downIdx >= 0 {
			a.dock.Pressed = a.dock.Items[a.downIdx]
			a.needRender = true
		}
		pSetCapture.Call(hwnd)
		a.setAnimInterval(15)
		return 0
	case WM_LBUTTONUP:
		pReleaseCapture.Call()
		if a.lp.shown {
			a.hideLaunchpad() // a click on the dock closes Launchpad (the dock never takes the focus, so it wouldn't close by itself)
		} else if isSecondary() {
			if h, _, _ := pFindWindowW.Call(up(u16("DGDockLaunchpad")), 0); h != 0 {
				if v, _, _ := pIsWindowVisible.Call(h); v != 0 {
					forwardToMainAt(msgHideLaunchpad, 0, 0, 0, 0) // the main dock shows it
				}
			}
		}
		cx, cy := getCursorPos()
		if a.popDown {
			a.popDown = false
			d := a.dock
			lx, ly := a.localPoint(cx, cy)
			if row := d.PopCloseAt(lx, ly); row >= 0 {
				pPostMessageW.Call(d.Pop.Rows[row].Hwnd, WM_CLOSE, 0, 0)
				a.refreshPopSoon()
			} else if row := d.PopRowAt(lx, ly); row >= 0 {
				if p := d.Pop.Rows[row].Path; strings.HasPrefix(p, "media:") {
					a.mediaKey(p)
				} else if strings.HasPrefix(p, "usb:") {
					a.usbAction(p)
				} else if strings.HasPrefix(p, "tray:") {
					a.closePop()
					a.trayOpen(strings.TrimPrefix(p, "tray:"))
				} else if a.stackOpen(p) {
					// opened a folder inside the card
				} else if p != "" {
					shellOpen(p, "")
				} else {
					activateWindow(d.Pop.Rows[row].Hwnd)
				}
			} else if d.InPop(lx, ly) && d.PopItem != nil {
				if a.stackItem == d.PopItem && a.stackPath != "" {
					shellOpen(a.stackPath, "") // the folder you're looking at
				} else {
					a.click(d.PopItem, false)
				}
			}
			return 0
		}
		wasDown := a.mouseDown
		a.mouseDown = false
		d := a.dock
		d.Pressed = nil
		a.needRender = true
		if d.Drag != nil {
			drag := d.Drag
			d.Drag = nil
			if d.DragRemove && drag.Pin != nil && drag.Kind != KindStart {
				d.Poofs = append(d.Poofs, Poof{X: d.DragX, Y: d.DragY, Start: time.Now()})
				a.removePinAt(drag.PinIdx)
			} else {
				if !a.dock.Calm && !a.dock.noDropSettle {
					drag.JellyStart, drag.JellyAmp = time.Now(), 0.15 // lands with a small squash
				}
				a.saveConfig()
			}
			d.DragRemove = false
			a.needRender = true
			a.tick()
			return 0
		}
		if wasDown {
			lx, ly := a.localPoint(cx, cy)
			idx := d.ItemAt(lx, ly)
			if idx >= 0 && idx == a.downIdx {
				ctrl := (wparam & 0x0008) != 0
				a.click(d.Items[idx], ctrl)
			}
		}
		a.needRender = true
		return 0
	case WM_MBUTTONUP:
		cx, cy := getCursorPos()
		lx, ly := a.localPoint(cx, cy)
		if idx := a.dock.ItemAt(lx, ly); idx >= 0 {
			a.launch(a.dock.Items[idx])
		}
		return 0
	case WM_RBUTTONUP:
		cx, cy := getCursorPos()
		lx, ly := a.localPoint(cx, cy)
		idx := a.dock.ItemAt(lx, ly)
		if a.dock.InPop(lx, ly) && a.dock.PopItem != nil {
			if row := a.dock.PopRowAt(lx, ly); row >= 0 && strings.HasPrefix(a.dock.Pop.Rows[row].Path, "tray:") {
				a.trayRowMenu(strings.TrimPrefix(a.dock.Pop.Rows[row].Path, "tray:"))
				return 0
			}
			if wparam&0x0004 != 0 { // shift: change a tile's icon (plain right-click opens it in File Explorer)
				if row := a.dock.PopRowAt(lx, ly); row >= 0 && a.stackItem == a.dock.PopItem {
					if p := a.dock.Pop.Rows[row].Path; p != "" && p != "stack:back" {
						a.stackFileMenu(p)
						return 0
					}
				}
			}
			if a.stackExplorer(lx, ly) {
				return 0
			}
			if a.dock.PopItem.Kind == KindWidget {
				a.widgetMenu()
			} else {
				a.itemMenu(a.dock.PopItem)
			}
		} else if idx >= 0 && a.dock.Items[idx].Kind == KindWidget && a.dock.Items[idx].Widget == "tray" {
			a.showDesktop()
		} else if idx >= 0 && a.dock.Items[idx].Kind == KindWidget {
			a.widgetMenu()
		} else if idx >= 0 && a.dock.Items[idx].Kind != KindWidget {
			a.itemMenu(a.dock.Items[idx])
		} else {
			a.dockMenu()
		}
		return 0
	case wmPick:
		a.onPick(wparam, int(int32(lparam)))
		return 0
	case wmMedia:
		a.onMedia()
		return 0
	case WM_HOTKEY:
		if wparam == hotkeySearch {
			a.toggleSearch()
		} else if wparam == hotkeyWinKey {
			a.toggleLaunchpad()
		}
		return 0
	case wmWinKey:
		a.toggleLaunchpad()
		return 0
	case wmLaunchpadHide:
		a.hideLaunchpad()
		return 0
	case wmLaunchpadReveal:
		a.revealLaunchpad()
		return 0
	case wmLaunchpadMenu:
		if wparam == 1 {
			a.launchpadFolderMenu()
		} else {
			a.launchpadMenu()
		}
		return 0
	case wmWeatherDone:
		a.weatherDone()
		return 0
	case wmWeatherHome:
		a.weatherHomeDone()
		return 0
	case wmOpenSetWin:
		a.openSettingsWindow(a.swPage)
		return 0
	case wmColourFollowUp:
		a.colourFollowUpDone(wparam)
		return 0
	case wmTrayFollow:
		a.trayFollowUp(wparam, lparam)
		return 0
	case wmLaunchpadPower:
		if wparam >= 1 && wparam <= 5 {
			a.powerAction([]string{"", "sleep", "lock", "signout", "restart", "shutdown"}[wparam])
		}
		return 0
	case wmLaunchpadOpen:
		a.showLaunchpad(RECT{})
		return 0
	case wmTray:
		a.onTrayChanged()
		return 0
	case wmUpdate:
		if wparam == 1 {
			a.startUpdate()
		} else {
			a.onUpdateFound()
		}
		return 0
	case wmPanelHide:
		a.hidePanel(true)
		return 0
	case WM_DEVICECHANGE:
		switch wparam {
		case 0x8001: // query remove: close our handles on that drive so it can go
			onWatchDeviceChange(wparam, lparam)
		case 0x8002: // removal cancelled: watch it again
			watchMu.Lock()
			clear(released)
			watchMu.Unlock()
		case 0x8000: // a drive arrived: its letter may have been released before
			watchMu.Lock()
			clear(released)
			watchMu.Unlock()
		}
		if wparam == 0x8000 || wparam == 0x8004 || wparam == 0x0007 { // arrival, removal, devnodes changed
			pSetTimer.Call(hwnd, timerUsb, 700, 0)
		}
		return 1
	case wmUsbChanged:
		if wparam == 1 {
			a.scanUSB() // ejected
		}
		a.tickUSB()
		a.refreshUsbPop()
		a.render()
		return 0
	case wmPanelResize:
		a.resizePanel()
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		a.onWheel(int16(wparam >> 16))
		return 0
	case 0x020E: // WM_MOUSEHWHEEL: sideways swipe on a touchpad turns pages
		a.pageWheel(-int(int16(wparam >> 16)))
		return 0
	case WM_DROPFILES:
		a.onDrop(wparam)
		return 0
	case wmAppBar:
		switch wparam {
		case 1: // ABN_POSCHANGED
			pSetTimer.Call(hwnd, timerRelayout, 300, 0)
		}
		return 0
	case WM_DPICHANGED:
		if hwnd != a.hwnd {
			return 0
		}
		// Moving the dock to a different-DPI monitor sends this while a relayout
		// may still be running. Queue one immediate follow-up so dpiScale reads
		// the destination monitor before the next visible frame settles.
		pKillTimer(hwnd, timerRelayout)
		pPostMessageW.Call(hwnd, wmDPIRelayout, 0, 0)
		return 0
	case wmDPIRelayout:
		a.relayout()
		return 0
	case WM_DISPLAYCHANGE, WM_SETTINGCHANGE:
		if hwnd != a.hwnd {
			return 0
		}
		if uint32(msg) == WM_DISPLAYCHANGE {
			a.displaysMoved = true
		}
		pSetTimer.Call(hwnd, timerRelayout, 400, 0)
		if uint32(msg) == WM_SETTINGCHANGE {
			pal, acc := a.dock.Pal, a.dock.Accent
			a.updateTheme()
			if a.dock.Pal != pal || a.dock.Accent != acc { // e.g. a new wallpaper, with colours that follow it
				a.applySettingsTheme()
				if a.lp.host != nil {
					a.lp.host.eval("window.mdEnvRefresh && window.mdEnvRefresh()")
				}
			}
		}
		return 0
	case WM_QUERYENDSESSION:
		return 1
	case WM_ENDSESSION:
		if wparam != 0 {
			a.removeAppbar()
			if a.cfg.HideTaskbar {
				a.restoreTaskbar()
			}
			a.saveConfig()
		}
		return 0
	case WM_CLOSE:
		a.quit()
		return 0
	case WM_DESTROY:
		return 0
	}
	if a != nil && a.taskbarCreatedMsg != 0 && uint32(msg) == a.taskbarCreatedMsg {
		a.appbar = false
		a.appbarRect = RECT{}
		if a.cfg.HideTaskbar {
			a.applyTaskbar()
		}
		pSetTimer.Call(hwnd, timerRelayout, 1500, 0)
		return 0
	}
	if a != nil && !isSecondary() && hwnd == a.hwnd && msgOpenSettings != 0 && uint32(msg) == msgOpenSettings {
		if a.panelVisible() {
			a.hidePanel(false) // move it to the other display
		}
		a.panelHiddenAt = time.Time{}
		a.openSettingsWindow("")
		return 0
	}
	if a != nil && !isSecondary() && hwnd == a.hwnd && msgLaunchpadAnchor != 0 && uint32(msg) == msgLaunchpadAnchor {
		x, y := unpackPoint(wparam)
		a.lp.anchor, a.lp.anchorWin, a.lp.anchorAt = POINT{x, y}, lparam, time.Now()
		return 0
	}
	if a != nil && isSecondary() && hwnd == a.hwnd && msgRefreshDock != 0 && uint32(msg) == msgRefreshDock {
		a.refreshDock()
		return 0
	}
	if a != nil && isSecondary() && hwnd == a.hwnd && msgConfigChanged != 0 && uint32(msg) == msgConfigChanged {
		pSetTimer.Call(hwnd, timerFollow, 30, 0) // a burst of saves (a slider) reloads once
		return 0
	}
	if a != nil && isSecondary() && hwnd == a.hwnd && msgLaunchpadArrow != 0 && uint32(msg) == msgLaunchpadArrow {
		a.setLaunchpadArrow(wparam == 1)
		return 0
	}
	if a != nil && !isSecondary() && hwnd == a.hwnd && msgHideLaunchpad != 0 && uint32(msg) == msgHideLaunchpad {
		a.hideLaunchpad()
		return 0
	}
	if a != nil && !isSecondary() && hwnd == a.hwnd && msgShowLaunchpad != 0 && uint32(msg) == msgShowLaunchpad {
		l, t := unpackPoint(wparam)
		r, b := unpackPoint(lparam)
		if a.lp.shown {
			a.hideLaunchpad()
			return 0
		}
		a.showLaunchpad(RECT{l, t, r, b})
		return 0
	}
	if a != nil && !isSecondary() && hwnd == a.hwnd && msgShowSearch != 0 && uint32(msg) == msgShowSearch {
		l, t := unpackPoint(wparam)
		r, b := unpackPoint(lparam)
		if a.lp.shown {
			a.hideLaunchpad()
			return 0
		}
		a.searchOnMon = RECT{l, t, r, b}
		a.showSearch()
		return 0
	}
	if a != nil && hwnd == a.hwnd && a.msgShellHook != 0 && uint32(msg) == a.msgShellHook {
		a.onShellHook(wparam, lparam)
		return 0
	}
	if a != nil && a.msgTaskbarCreated != 0 && uint32(msg) == a.msgTaskbarCreated {
		// the taskbar (or our tray host) asked everyone to re-register: an appbar has to
		// register again too, or Windows forgets the space it keeps for the dock
		a.appbar = false
		a.appbarRect = RECT{}
		pSetTimer.Call(hwnd, timerRelayout, 300, 0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

var pKillTimerProc = user32.NewProc("KillTimer")

func pKillTimer(hwnd uintptr, id uintptr) { pKillTimerProc.Call(hwnd, id) }

// ---------- main ----------

func loadFont() []byte {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	for _, f := range []string{"segoeui.ttf", "seguisb.ttf", "arial.ttf", "tahoma.ttf"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			return b
		}
	}
	return nil
}

func loadBoldFont() []byte {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	for _, f := range []string{"seguisb.ttf", "segoeuib.ttf", "arialbd.ttf"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			return b
		}
	}
	return nil
}

func loadIconFont() []byte {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	for _, f := range []string{"SegoeIcons.ttf", "segmdl2.ttf"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			return b
		}
	}
	return nil
}

func main() {
	runtime.LockOSThread()
	swapRB = true
	if !handleSetupArgs() {
		return
	}

	for i, s := range os.Args {
		if strings.EqualFold(s, "--display") && i+1 < len(os.Args) {
			dockDisplay = strings.ToUpper(os.Args[i+1])
		}
	}
	for _, s := range os.Args {
		if s == "--wait" { // started by a dock that is closing (after restoring a setup)
			time.Sleep(2 * time.Second)
		}
	}
	mutexName := "Local\\DGDock_SingleInstance"
	if isSecondary() {
		mutexName = "Local\\DGDock_Display_" + strings.NewReplacer(`\`, "", ".", "").Replace(dockDisplay)
	}
	if r, _, _ := pCreateMutexW.Call(0, 0, up(u16(mutexName))); r != 0 {
		if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
			return
		}
	}
	if !isSecondary() {
		tidyInstallLeftovers()
		repairCustomIconColours() // before any icon is loaded
	}
	pCoInitializeEx.Call(0, 2)
	if pSetProcessDpiAwarenessContext.Find() == nil {
		pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	} else {
		pSetProcessDPIAware.Call()
	}

	a := &App{items: map[string]*Item{}, icons: map[string]*image.RGBA{}, styled: map[string]*IconSet{}, downIdx: -1, pendingPicker: -1, mediaNames: map[string]string{}, waveT0: time.Now()}
	app = a
	a.loadConfig()
	if isSecondary() {
		a.secondaryOverrides()
		if info, err := os.Stat(a.cfgPath); err == nil {
			a.cfgSeen = info.ModTime()
		}
		a.logf("starting the dock for %s", dockDisplay)
	} else {
		a.logf("starting")
	}
	a.te = NewTextEngine(loadFont())
	a.glyphs = NewTextEngine(loadIconFont())
	a.rd = &Renderer{Text: a.te, Bold: NewTextEngine(loadBoldFont())}
	a.dock = &Dock{Hover: -1, PopHot: -1}
	a.dock.Measure = a.te.Measure
	if a.cfg.Font != "" {
		a.applyFont()
	}
	go installedFonts() // read the font list early, so Settings shows it at once
	a.dock.HideDots = a.cfg.HideIndicators
	a.dock.SetAnim(a.cfg)
	a.dock.PopFn = a.popContent

	hinst, _, _ := pGetModuleHandleW.Call(0)
	cursor, _, _ := pLoadCursorW.Call(0, 32512)
	black, _, _ := pGetStockObject.Call(4)

	blurClass := WNDCLASSEXW{
		LpfnWndProc:   pDefWindowProcW.Addr(),
		HInstance:     hinst,
		HCursor:       cursor,
		HbrBackground: black,
		LpszClassName: u16("DGDockBlur"),
	}
	blurClass.CbSize = uint32(unsafe.Sizeof(blurClass))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&blurClass)))

	dockClass := WNDCLASSEXW{
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hinst,
		HCursor:       cursor,
		LpszClassName: u16("DGDockWindow"),
	}
	dockClass.CbSize = uint32(unsafe.Sizeof(dockClass))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&dockClass)))

	a.blur, _, _ = pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TOPMOST,
		up(u16("DGDockBlur")), up(u16("DG-Dock Blur")), WS_POPUP, 0, 0, 10, 10, 0, 0, hinst, 0)
	if a.blur != 0 {
		corner := uint32(1) // DWMWCP_DONOTROUND
		pDwmSetWindowAttribute.Call(a.blur, 33, uintptr(unsafe.Pointer(&corner)), 4)
		policy := ACCENTPOLICY{}
		data := WINCOMPATTRDATA{Attrib: 19, Data: uintptr(unsafe.Pointer(&policy)), Size: unsafe.Sizeof(policy)}
		ok := uintptr(0)
		if pSetWindowCompositionAttribute.Find() == nil {
			ok, _, _ = pSetWindowCompositionAttribute.Call(a.blur, uintptr(unsafe.Pointer(&data)))
		}
		if ok == 0 {
			a.logf("blur not available")
			pDestroyWindow(a.blur)
			a.blur = 0
		}
	}

	a.hwnd, _, _ = pCreateWindowExW.Call(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TOPMOST|WS_EX_ACCEPTFILES,
		up(u16("DGDockWindow")), up(u16(func() string {
			if isSecondary() {
				return extraTitle(dockDisplay) // the main dock is the one titled plain "DG-Dock"
			}
			return appName
		}())), WS_POPUP, 0, 0, 10, 10, a.blur, 0, hinst, 0)
	if a.hwnd == 0 {
		messageBox("DG-Dock could not create its window.", appName)
		return
	}
	if !isSecondary() { // extra docks can't be edited, so nothing can be dropped on them
		pDragAcceptFiles.Call(a.hwnd, 1)
		a.startDropTarget()
	}
	a.taskbarCreatedMsg = uint32(func() uintptr { r, _, _ := pRegisterWindowMessageW.Call(up(u16("TaskbarCreated"))); return r }())
	// allow drag & drop from Explorer even across integrity levels
	changeFilter := user32.NewProc("ChangeWindowMessageFilterEx")
	if changeFilter.Find() == nil {
		for _, m := range []uintptr{WM_DROPFILES, 0x0049, 0x004A} {
			changeFilter.Call(a.hwnd, m, 1, 0)
		}
	}

	if !isSecondary() {
		a.installBuiltinPacks()
	}
	a.loadPack()
	a.dock.Notch = true
	a.startWinEvents()
	// Offer newer DG-Dock exes that appear in Downloads. Installing from here (rather
	// than double-clicking the download) avoids the SmartScreen "Windows protected
	// your PC" warning, since the dock starts the new exe itself.
	if !isSecondary() {
		a.startUpdateWatch()
		a.registerSearchKey()
		a.applyWinKey()
		a.fixStartupPath()
	}
	a.startMedia()
	a.updateTheme()
	if !isSecondary() {
		unclipTaskbars() // undo a cut-down taskbar left from a previous run, before deciding again
	}
	if a.cfg.HideTaskbar {
		a.applyTaskbar()
	}
	a.rebuildItems()
	a.scanRunning()
	a.relayout()
	a.updateTheme()      // again, now the dock knows its display (its wallpaper's colours)
	a.syncDisplayDocks() // start the docks for other displays, if chosen
	pSetTimer.Call(a.hwnd, timerPrewarm, 4000, 0)
	go tidyOldWallpaper()
	pShowWindow.Call(a.hwnd, SW_SHOWNA)
	pSetWindowPos.Call(a.hwnd, ^uintptr(0), 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	a.render()

	a.setAnimInterval(15)
	pSetTimer.Call(a.hwnd, timerScan, 700, 0)
	a.msgTaskbarCreated = uint32(registerMsg("TaskbarCreated"))
	a.msgShellHook = uint32(registerMsg("SHELLHOOK"))
	pRegisterShellHookWindow.Call(a.hwnd)
	if !a.cfg.NoTray {
		a.startTray()
	}

	// The compositor clock supplies frames at the current display cadence, including
	// Dynamic Refresh Rate changes. Older Windows builds use DwmFlush as a fallback.
	var msg MSG
	quit := false
	for !quit {
		frames := a.wantFrames()
		a.setCompositorBoost(frames && a.shouldBoostCompositorClock() && !a.compositorOccluded)
		if frames {
			a.frameMode = true
			for {
				r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1) // PM_REMOVE
				if r == 0 {
					break
				}
				if msg.Message == 0x0012 { // WM_QUIT
					quit = true
					break
				}
				pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
				pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
			}
			if quit {
				break
			}
			// tick runs here, outside the window procedure, so it needs its own crash
			// handler: without it a panic mid-animation would leave the taskbar hidden
			guarded(func() {
				a.tick()
				a.waitForDisplayFrame()
			})
			continue
		}
		a.setCompositorBoost(false)
		a.frameMode = false
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	a.setCompositorBoost(false)
	closeWebHosts()
	stopTray()
	a.removeAppbar()
	// quit() normally does this already; this covers the loop ending any other way
	if a.cfg.HideTaskbar {
		a.restoreTaskbar()
	}
	a.saveConfig()
	a.logf("exit")
}

var (
	pDestroyWindowProc     = user32.NewProc("DestroyWindow")
	pSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	pPeekMessageW          = user32.NewProc("PeekMessageW")
)

// wantFrames reports whether the dock is animating or tracking the mouse closely.
func (a *App) wantFrames() bool {
	if a.hiddenFullscreen || a.canvas == nil || session != nil {
		return false
	}
	d := a.dock
	if d.Busy() || a.mouseDown || a.popDown || time.Now().Before(a.animUntil) {
		return true
	}
	// timed animations that Busy doesn't cover: page slide and dots, the running-apps pill
	// splitting off, and the volume / brightness overlays fading
	if !d.PageT0.IsZero() || !d.BudStart.IsZero() || math.Abs(d.PageF-float64(d.PageCur)) > 0.001 {
		return true
	}
	return false
}

func pDestroyWindow(h uintptr) { pDestroyWindowProc.Call(h) }
