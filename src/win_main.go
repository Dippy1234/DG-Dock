//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"image"
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
	appName       = "DG-Dock"
	runValueName  = "DG-Dock"
)

type App struct {
	cfg     *Config
	cfgPath string
	logPath string

	dock   *Dock
	rd     *Renderer
	te     *TextEngine
	glyphs *TextEngine

	hwnd, blur       uintptr
	memDC, dib       uintptr
	canvas           *image.RGBA
	dibW, dibH       int
	dibCapW, dibCapH int
	dibBits          []byte
	fullPresent      bool
	lastWinX         int32
	lastWinY         int32
	frameMode        bool
	quickLayout      bool

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

	mouseDown    bool
	downIdx      int
	downX, downY int32

	menuOpen            bool
	hiddenFullscreen    bool
	overAt              time.Time // intellihide: last window-overlap check
	overDock            bool
	searchHwnd          uintptr
	search              *webHost
	searchH             int
	searchShown         bool
	searchHiddenAt      time.Time
	searchAnimating     bool
	searchRevealPending bool
	animUntil           time.Time
	msgTaskbarCreated   uint32 // "TaskbarCreated": appbars must register again when it arrives
	workOverride        bool   // we set the work area ourselves (Windows didn't honour the appbar)
	workOrig            RECT   // the work area before that, to put back
	workCheckAt         time.Time
	searchKeyName       string
	volEP               uintptr // IAudioEndpointVolume
	volAt               time.Time
	brAcc               int // brightness scroll left over from touchpad steps
	page                int // current page of apps
	pageOut             []*Item
	pageDir             int
	stackItem           *Item  // folder card being browsed
	stackPath           string // folder shown inside it ("" = the pinned folder itself)
	stackScroll         int    // rows scrolled
	stackAcc            int
	drag                *dragged // what's being dragged over the dock from outside
	dropOn              bool     // the gap showing where it'll land is open
	dropBefore          *Item
	dropSection         int
	pageAcc             int
	pageAt              time.Time
	relayoutSoon        bool
	lastWins            []WinInfo // open windows from the last scan
	animInterval        int
	lastLeave           time.Time
	blurRect            RECT
	blurShown           bool
	trashFull           bool
	scanCount           int
	taskbarCreatedMsg   uint32
	menuDarkSet         int
	needRender          bool
	settings            *webHost
	pack                *IconPack
	pendingPicker       int
	pendingKey          [2]string // icon picker for a tray app or a file: key, name
	pickKey             string    // whose icon "Choose from a file…" is for
	dialogOpen          bool
	panelH              int // CSS px reported by the page
	panelHiddenAt       time.Time
	panelAnimating      bool
	panelAcrylic        bool
	panelHwnd           uintptr
	panelAnchor         [2]int32
	iconQueue           []string
	thumbWin            uintptr
	thumbs              []thumbReg
	thumbKey            string
	thumbShown          bool
	curMon              uintptr
	monDwell            time.Time
	mediaW              *mediaWatcher
	media               mediaState
	mediaArt            *IconSet
	mediaArtSrc         *image.RGBA
	mediaNames          map[string]string
	mediaAccent         [3]uint8
	mediaAccentSrc      *image.RGBA
	meter               uintptr
	wifi                wifiInfo
	wifiAt              time.Time
	meterAt             time.Time
	waveAt              time.Time
	waveT0              time.Time
	waveIdleAt          time.Time
	waveGrowAt          time.Time
	popDown             bool
	appsCache           []AppEntry
	appsCacheTime       time.Time
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
		json.Unmarshal(b, a.cfg)
		AnimSlow = AnimSpeedFactor(animSpeedOf(a.cfg.AnimSpeed))
	}
	if a.cfg.IconSize < 32 || a.cfg.IconSize > 128 {
		a.cfg.IconSize = 54
	}
	if a.cfg.Magnification < 1 || a.cfg.Magnification > 3 {
		a.cfg.Magnification = 1.0
	}
	a.cfg.Style = "pill" // the Pill dock is the only style now
	if !a.cfg.PillApplied {
		a.cfg.PillApplied = true
		a.cfg.Style = "pill"
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
	b, err := json.MarshalIndent(a.cfg, "", "  ")
	if err == nil {
		os.WriteFile(a.cfgPath, b, 0644)
	}
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
	if a.cfg.IconStyle == "original" {
		return "orig"
	}
	if it.Kind == KindStart || (it.Section == 0 && !it.IsDir) {
		if a.dock.Dark || a.cfg.Style != "glass" {
			return "tile-dark"
		}
		return "tile-light"
	}
	return "plain"
}

func (a *App) styleItem(it *Item) {
	if it.Raw == nil {
		return
	}
	style := a.itemStyle(it)
	ck := it.RawKey + "|" + style
	if is, ok := a.styled[ck]; ok {
		it.Icon = is
		return
	}
	var img *image.RGBA
	switch style {
	case "tile-dark":
		img = MacTile(it.Raw, true)
	case "tile-light":
		img = MacTile(it.Raw, false)
	case "plain":
		img = MacPlain(it.Raw)
	default:
		img = it.Raw
	}
	is := NewIconSet(img)
	a.styled[ck] = is
	it.Icon = is
}

func (a *App) restyleAll() {
	for _, it := range a.items {
		a.styleItem(it)
	}
	a.needRender = true
}

// customIcon loads a user-chosen icon file.
func (a *App) customIcon(path string) *image.RGBA {
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
	if p.Icon != "" {
		if img := a.customIcon(p.Icon); img != nil {
			it.Raw, it.RawKey = img, "custom:"+p.Icon
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
		case it.Section == 1:
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
	items := left
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
	if live > 0 && len(left) > 0 {
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
	}
	if sepRun != nil && (!sepRun.Leaving || sepRun.Appear > 0.01 || !a.dock.BudStart.IsZero()) {
		used["seprun"] = true
		items = append(items, sepRun)
	}
	items = append(items, running...)
	if len(right) > 0 && len(left) > 0 {
		sep, ok := a.items["sep"]
		if !ok {
			sep = &Item{Key: "sep", Kind: KindSep, Scale: 1, Appear: 1}
			a.items["sep"] = sep
		}
		used["sep"] = true
		items = append(items, sep)
	}
	items = append(items, right...)
	if !a.cfg.NoWidgets && a.dock.Notch {
		sep, ok := a.items["sep2"]
		if !ok {
			sep = &Item{Key: "sep2", Kind: KindSep, Scale: 1, Appear: 1}
			a.items["sep2"] = sep
		}
		used["sep2"] = true
		items = append(items, sep)
		for _, w := range a.widgetItems() {
			used[w.Key] = true
			items = append(items, w)
		}
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
			a.applyPack(it)
			a.assignIcon(it, it.RawKey, it.Raw)
			a.items[key] = it
		}
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
			pc.Title, pc.Subtitle = "Search", "Apps, settings and files · right-click for Start"
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
	blur := a.cfg.Blur
	if v, ok := readDWORD(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "EnableTransparency"); ok && v == 0 {
		blur = false
	}
	if a.blur == 0 || a.cfg.Style != "glass" {
		blur = false
	}
	if pal := a.dockPalette(dark); pal != a.dock.Pal {
		a.dock.Pal = pal
		a.needRender = true
	}
	if dark != a.dock.Dark || blur != a.dock.BlurOn {
		darkChanged := dark != a.dock.Dark
		a.dock.Dark = dark
		a.dock.BlurOn = blur
		a.needRender = true
		if darkChanged {
			a.restyleAll()
		}
	}
	mode := 3
	if dark {
		mode = 2
	}
	if mode != a.menuDarkSet {
		a.menuDarkSet = mode
		setMenuTheme(mode)
	}
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
	if a.cfg.HideTaskbar {
		if a.cfg.PrevTaskbarAutoHide < 0 {
			a.cfg.PrevTaskbarAutoHide = taskbarAutoHideState()
			a.saveConfig()
		}
		if taskbarAutoHideState() == 0 {
			setTaskbarAutoHide(1)
		}
		for _, h := range trayWindows() {
			if v, _, _ := pIsWindowVisible.Call(h); v != 0 {
				pShowWindow.Call(h, SW_HIDE)
			}
		}
	}
}

func (a *App) restoreTaskbar() {
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
	if h == 0 || a.cfg.MainDisplay {
		h, _, _ = pMonitorFromPoint.Call(0, 1)
		a.curMon = h
	}
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
	a.mon, a.work = mi.RcMonitor, mi.RcWork
}

func (a *App) dpiScale() float64 {
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
	d.SetMetrics(a.dpiScale(), a.cfg.IconSize, a.cfg.Magnification, a.cfg.IconStyle != "original", a.cfg.Style != "glass", a.cfg.Style != "notch")
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
		case OrientBottom:
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
	show := d.BlurOn && !d.Notch && !a.hiddenFullscreen && d.HideOffset < d.PlateH*0.5 && len(d.Items) > 0
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
	if r != a.blurRect {
		w, h := r.Right-r.Left, r.Bottom-r.Top
		pSetWindowPos.Call(a.blur, 0, uintptr(r.Left), uintptr(r.Top), uintptr(w), uintptr(h), SWP_NOACTIVATE|SWP_NOZORDER)
		if w != a.blurRect.Right-a.blurRect.Left || h != a.blurRect.Bottom-a.blurRect.Top {
			rad := int32(math.Round((d.PlateH - 2*inset) * 0.40 * 2))
			rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), uintptr(rad), uintptr(rad))
			pSetWindowRgn.Call(a.blur, rgn, 1)
		}
		a.blurRect = r
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

func (a *App) tick() {
	if a.hiddenFullscreen {
		a.setAnimInterval(250)
		return
	}
	d := a.dock
	cx, cy := getCursorPos()
	onMon := cx >= a.mon.Left && cx < a.mon.Right && cy >= a.mon.Top && cy < a.mon.Bottom
	d.MouseValid = onMon || d.Drag != nil
	d.MouseX = float64(cx - a.winX)
	d.MouseY = float64(cy - a.winY)
	a.checkMonitorSwitch(cx, cy)
	a.updateWave()
	if it := a.items["w:media"]; it != nil && time.Since(it.WVolAt) < 1600*time.Millisecond {
		a.needRender = true // volume overlay fading out
		a.animUntil = time.Now().Add(100 * time.Millisecond)
	}
	if it := a.items["w:status"]; it != nil && time.Since(it.WBrAt) < 1600*time.Millisecond {
		a.needRender = true // brightness ring fading back to battery
		a.animUntil = time.Now().Add(100 * time.Millisecond)
	}

	// auto-hide
	if a.cfg.AutoHide && !a.menuOpen && d.Drag == nil && !a.mouseDown {
		u, v := d.toUV(d.MouseX, d.MouseY)
		hidden := d.HideTarget > 0
		clear := a.searchShown || !a.cfg.HideAlways && !a.windowOverDock()
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
		if math.Hypot(dx, dy) > 7*d.Scale {
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
			if it.draggable() {
				d.Drag = it
				d.Pressed = nil
			}
		}
	}
	if d.Drag != nil {
		d.DragX, d.DragY = d.MouseX, d.MouseY
		d.DragRemove = d.DragOff()
		if !d.DragRemove {
			a.reorderDrag()
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
	a.setAnimInterval(30)
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

// ---------- scan tick ----------

func (a *App) scan() {
	a.scanCount++
	fg, _, _ := pGetForegroundWindow.Call()
	if fg != 0 && fg != a.hwnd && fg != a.blur {
		a.lastFg = fg
	}
	// full screen apps
	fs := false
	if a.cfg.HideFullscreen && fg != 0 && fg != a.hwnd && fg != a.blur {
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
		if np.Title != d.Pop.Title || np.Subtitle != d.Pop.Subtitle || len(np.Rows) != len(d.Pop.Rows) || (len(np.Rows) > 0 && np.Rows[0].Title != d.Pop.Rows[0].Title) {
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

func (a *App) click(it *Item, newInstance bool) {
	if it.Kind == KindWidget {
		a.widgetClick(it)
		return
	}
	if (it.Kind == KindApp || it.Kind == KindFile || it.Kind == KindRunning) && len(it.Windows) > 0 && !newInstance && !it.IsDir {
		if a.isFront(it) {
			for _, w := range it.Windows {
				pShowWindowAsync.Call(w, SW_MINIMIZE)
			}
			return
		}
		target := it.Windows[0]
		for _, w := range it.Windows {
			if ic, _, _ := pIsIconic.Call(w); ic == 0 {
				target = w
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
		exe, _ := os.Executable()
		k.SetStringValue(runValueName, `"`+exe+`"`)
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
	stopTray()
	a.removeAppbar()
	if a.cfg.HideTaskbar {
		a.restoreTaskbar()
	}
	a.saveConfig()
	pPostQuitMessage.Call(0)
}

func wndProc(hwnd, msg, wparam, lparam uintptr) (ret uintptr) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			app.logf("panic: %v\n%s", r, buf[:n])
			app.restoreTaskbar()
			app.removeAppbar()
			os.Exit(1)
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
			a.scan()
			if time.Since(a.workCheckAt) > 2*time.Second {
				a.checkWorkArea()
			}
		case timerRelayout:
			pKillTimer(hwnd, timerRelayout)
			a.relayout()
		case timerSave:
			pKillTimer(hwnd, timerSave)
			a.saveConfig()
		case timerQuickScan:
			pKillTimer(hwnd, timerQuickScan)
			a.scanRunning()
			a.refreshPop()
			if a.needRender {
				a.render()
			}
		case timerSearchReveal:
			a.revealSearch()
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
		if a.dock.InPop(float64(cx-a.winX), float64(cy-a.winY)) {
			a.popDown = true
			pSetCapture.Call(hwnd)
			return 0
		}
		a.mouseDown = true
		a.downX, a.downY = cx, cy
		a.downIdx = a.dock.ItemAt(float64(cx-a.winX), float64(cy-a.winY))
		if a.downIdx >= 0 {
			a.dock.Pressed = a.dock.Items[a.downIdx]
			a.needRender = true
		}
		pSetCapture.Call(hwnd)
		a.setAnimInterval(15)
		return 0
	case WM_LBUTTONUP:
		pReleaseCapture.Call()
		cx, cy := getCursorPos()
		if a.popDown {
			a.popDown = false
			d := a.dock
			lx, ly := float64(cx-a.winX), float64(cy-a.winY)
			if row := d.PopCloseAt(lx, ly); row >= 0 {
				pPostMessageW.Call(d.Pop.Rows[row].Hwnd, WM_CLOSE, 0, 0)
				a.refreshPopSoon()
			} else if row := d.PopRowAt(lx, ly); row >= 0 {
				if p := d.Pop.Rows[row].Path; strings.HasPrefix(p, "media:") {
					a.mediaKey(p)
				} else if strings.HasPrefix(p, "tray:") {
					a.closePop()
					trayClick(strings.TrimPrefix(p, "tray:"), false)
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
			if d.DragRemove && drag.Pin != nil {
				d.Poofs = append(d.Poofs, Poof{X: d.DragX, Y: d.DragY, Start: time.Now()})
				a.removePinAt(drag.PinIdx)
			} else {
				a.saveConfig()
			}
			d.DragRemove = false
			a.needRender = true
			a.tick()
			return 0
		}
		if wasDown {
			idx := d.ItemAt(float64(cx-a.winX), float64(cy-a.winY))
			if idx >= 0 && idx == a.downIdx {
				ctrl := (wparam & 0x0008) != 0
				a.click(d.Items[idx], ctrl)
			}
		}
		a.needRender = true
		return 0
	case WM_MBUTTONUP:
		cx, cy := getCursorPos()
		if idx := a.dock.ItemAt(float64(cx-a.winX), float64(cy-a.winY)); idx >= 0 {
			a.launch(a.dock.Items[idx])
		}
		return 0
	case WM_RBUTTONUP:
		cx, cy := getCursorPos()
		idx := a.dock.ItemAt(float64(cx-a.winX), float64(cy-a.winY))
		if a.dock.InPop(float64(cx-a.winX), float64(cy-a.winY)) && a.dock.PopItem != nil {
			if row := a.dock.PopRowAt(float64(cx-a.winX), float64(cy-a.winY)); row >= 0 && strings.HasPrefix(a.dock.Pop.Rows[row].Path, "tray:") {
				a.trayRowMenu(strings.TrimPrefix(a.dock.Pop.Rows[row].Path, "tray:"))
				return 0
			}
			if wparam&0x0004 != 0 { // shift: change a tile's icon (plain right-click opens it in File Explorer)
				if row := a.dock.PopRowAt(float64(cx-a.winX), float64(cy-a.winY)); row >= 0 && a.stackItem == a.dock.PopItem {
					if p := a.dock.Pop.Rows[row].Path; p != "" && p != "stack:back" {
						a.stackFileMenu(p)
						return 0
					}
				}
			}
			if a.stackExplorer(float64(cx-a.winX), float64(cy-a.winY)) {
				return 0
			}
			a.itemMenu(a.dock.PopItem)
		} else if idx >= 0 && a.dock.Items[idx].Kind == KindStart && !a.cfg.StartIsStart {
			a.hideSearch(false)
			openWindowsStart() // left-click is Search, right-click is the Windows Start menu
		} else if idx >= 0 && a.dock.Items[idx].Widget == "tray" {
			a.trayMenu()
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
		}
		return 0
	case wmSearchHide:
		a.hideSearch(true)
		return 0
	case wmSearchResize:
		a.resizeSearch()
		return 0
	case wmSearchReveal:
		a.revealSearch()
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
	case WM_DISPLAYCHANGE, WM_DPICHANGED, WM_SETTINGCHANGE:
		pSetTimer.Call(hwnd, timerRelayout, 400, 0)
		if uint32(msg) == WM_SETTINGCHANGE {
			a.updateTheme()
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

	if r, _, _ := pCreateMutexW.Call(0, 0, up(u16("Local\\DGDock_SingleInstance"))); r != 0 {
		if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
			return
		}
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
	a.logf("starting")
	a.te = NewTextEngine(loadFont())
	a.glyphs = NewTextEngine(loadIconFont())
	a.rd = &Renderer{Text: a.te, Bold: NewTextEngine(loadBoldFont())}
	a.dock = &Dock{Hover: -1, PopHot: -1}
	a.dock.Measure = a.te.Measure
	a.dock.HideDots = a.cfg.HideIndicators
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
		policy := ACCENTPOLICY{AccentState: 3}
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
		up(u16("DGDockWindow")), up(u16(appName)), WS_POPUP, 0, 0, 10, 10, a.blur, 0, hinst, 0)
	if a.hwnd == 0 {
		messageBox("DG-Dock could not create its window.", appName)
		return
	}
	pDragAcceptFiles.Call(a.hwnd, 1)
	a.startDropTarget()
	a.taskbarCreatedMsg = uint32(func() uintptr { r, _, _ := pRegisterWindowMessageW.Call(up(u16("TaskbarCreated"))); return r }())
	// allow drag & drop from Explorer even across integrity levels
	changeFilter := user32.NewProc("ChangeWindowMessageFilterEx")
	if changeFilter.Find() == nil {
		for _, m := range []uintptr{WM_DROPFILES, 0x0049, 0x004A} {
			changeFilter.Call(a.hwnd, m, 1, 0)
		}
	}

	a.loadPack()
	a.dock.Notch = a.cfg.Style != "glass"
	a.startWinEvents()
	if devBuild {
		a.startUpdateWatch() // test builds: offer newer exes dropped in Downloads
	}
	a.registerSearchKey()
	a.fixStartupPath()
	a.startMedia()
	a.updateTheme()
	if a.cfg.HideTaskbar {
		a.applyTaskbar()
	}
	a.rebuildItems()
	a.scanRunning()
	a.relayout()
	pShowWindow.Call(a.hwnd, SW_SHOWNA)
	pSetWindowPos.Call(a.hwnd, ^uintptr(0), 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	a.render()

	a.setAnimInterval(15)
	pSetTimer.Call(a.hwnd, timerScan, 700, 0)
	a.msgTaskbarCreated = uint32(registerMsg("TaskbarCreated"))
	if !a.cfg.NoTray {
		a.startTray()
	}

	// Message loop. While something is animating we render one frame per display refresh
	// (DwmFlush waits for the compositor, so it runs at 60/144/240 Hz as the screen does);
	// otherwise we sleep in GetMessage and a slow timer polls the mouse.
	var msg MSG
	quit := false
	for !quit {
		if a.wantFrames() {
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
			a.tick()
			if r, _, _ := pDwmFlush.Call(); r != 0 {
				time.Sleep(4 * time.Millisecond)
			}
			continue
		}
		a.frameMode = false
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	a.removeAppbar()
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
