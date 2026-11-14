//go:build windows

package main

// Launchpad: all your apps, and search, in a card that pops out of the dock's Start
// icon. A side panel (words or just icons) has Home (Favourites and Recent), the
// categories and All apps; folders hold apps you group yourself.
//
// Icons match the dock's: your icon pack, your own icons and the dock's icon style.
// Right-click an app to change its icon; it changes in the dock too.

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
	"unsafe"

	_ "embed"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

//go:embed launchpad.html
var launchpadHTML string

const (
	wmLaunchpadHide   = WM_APP + 40
	wmLaunchpadReveal = WM_APP + 41
	wmLaunchpadMenu   = WM_APP + 42
	wmLaunchpadOpen   = WM_APP + 43
	wmLaunchpadPower  = WM_APP + 44
	timerLaunchpad    = 23
	timerPrewarm      = 24
)

var (
	msgShowLaunchpad = uint32(registerMsg("DGDock.ShowLaunchpad"))
	msgHideLaunchpad = uint32(registerMsg("DGDock.HideLaunchpad"))
	// another display's dock says where its Start icon is (so the card pops out of it)…
	msgLaunchpadAnchor = uint32(registerMsg("DGDock.LaunchpadAnchor"))
	// …and the main dock tells that dock to draw (1) or drop (0) the arrow to the card
	msgLaunchpadArrow = uint32(registerMsg("DGDock.LaunchpadArrow"))
)

type launchpadEnv struct {
	Dark       bool    `json:"dark"`
	Accent     string  `json:"accent"`
	OnAccent   string  `json:"onAccent"`
	Panel      string  `json:"panel"`
	Alpha      float64 `json:"alpha"` // how solid the panel is (Colour strength); 1 without a blurred backdrop
	Scale      float64 `json:"scale"`
	Look       string  `json:"look"`       // changes when icons should be drawn again
	Cols       int     `json:"cols"`       // apps across
	Icon       int     `json:"icon"`       // app size, page pixels
	Names      bool    `json:"names"`      // show app names
	SidePanel  string  `json:"sidePanel"`  // side panel: "text" or "icons"
	Recent     bool    `json:"recent"`     // show the Recent row on Home
	Search     bool    `json:"search"`     // put the cursor in the search box
	Dock       string  `json:"dock"`       // which edge the dock is on, so the card rises out of it
	CardSpring bool    `json:"cardSpring"` // the card content settles with a small spring on open
	Font       string  `json:"font"`       // the font picked in Settings ("" = Segoe UI)
}

type launchpadApp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Pinned bool   `json:"pinned"`
	Custom bool   `json:"custom,omitempty"` // has your own icon
	Group  string `json:"group"`
}

// launchpad state, kept on the App
type launchpadState struct {
	hwnd          uintptr
	host          *webHost
	shown         bool
	revealPending bool
	acrylic       bool      // a blurred backdrop, so Colour strength makes it see-through
	onMon         RECT      // opened from another display's dock
	anchor        POINT     // …at that dock's Start icon
	anchorWin     uintptr   // …that dock's window (it draws the arrow)
	anchorAt      time.Time // when the anchor arrived
	hiddenAt      time.Time
	menuOpen      bool
	searchFirst   bool // opened to search: the search box is ready
	menuID        string
	menuName      string
	menuGroup     string            // a folder's menu rather than an app's
	dragOn        bool              // an app from Launchpad is over the dock
	icons         map[string]string // app ID + look → data: URI
}

// toggleLaunchpad opens or closes the Launchpad (right-click on Start).
func (a *App) toggleLaunchpad() {
	if isSecondary() {
		// the main dock shows it, on this display, out of this dock's Start icon
		a.forwardLaunchpadAnchor()
		forwardToMainAt(msgShowLaunchpad, a.mon.Left, a.mon.Top, a.mon.Right, a.mon.Bottom)
		return
	}
	if a.lp.shown {
		a.hideLaunchpad()
		return
	}
	a.showLaunchpad(RECT{})
}

func (a *App) createLaunchpadWindow() {
	if a.lp.hwnd != 0 {
		return
	}
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	wc := WNDCLASSEXW{LpfnWndProc: webWndProcCB, HInstance: hinst, HCursor: cur, LpszClassName: u16("DGDockLaunchpad")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_TOPMOST, up(u16("DGDockLaunchpad")), up(u16("Launchpad")),
		WS_POPUP|0x00040000|0x00C00000, 0, 0, 600, 500, 0, 0, hinst, 0)
	if hwnd == 0 {
		return
	}
	a.lp.hwnd = hwnd
	webHosts[hwnd] = &webHost{hwnd: hwnd, binds: map[string]reflect.Value{}, borderless: true}
	pSetWindowPos.Call(hwnd, 0, 0, 0, 600, 500, 0x0020|0x0002|0x0001|0x0010|0x0004) // FRAMECHANGED
	dwmInt(hwnd, 20, boolI(a.dock.Dark))
	// like the Search card: the dock's own colour and 14px corners (a window region),
	// with no Windows border, so it and its pointer read as part of the dock
	dwmInt(hwnd, 33, 1)
	dwmInt(hwnd, 34, -2)
	margins := [4]int32{-1, -1, -1, -1}
	pDwmExtendFrameIntoClientArea.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
	// (no blurred backdrop: on this window Windows drew its title and frame through it,
	// and ignored the rounded shape; Build 245 took it back out)
}

func (a *App) ensureLaunchpadHost() bool {
	if a.lp.host != nil {
		return true
	}
	bgc := edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
	if !a.dock.Dark {
		bgc = edge.COREWEBVIEW2_COLOR{A: 255, R: 0xf3, G: 0xf3, B: 0xf3}
	}
	if p := a.dock.Pal; p.Alpha > 0 {
		bgc = edge.COREWEBVIEW2_COLOR{A: 255, R: p.Fill[0], G: p.Fill[1], B: p.Fill[2]} // the dock's colour, so it never flashes grey
	}
	if a.lp.acrylic {
		bgc = edge.COREWEBVIEW2_COLOR{} // transparent: the page paints its see-through panel
	}
	h, err := newWebHost(a.lp.hwnd, webDataDir(), bgc)
	if err != nil {
		a.logf("launchpad: %v", err)
		return false
	}
	h.borderless = true
	a.lp.host = h
	h.onActivate = func(active bool) {
		if !active && a.lp.shown && !a.lp.menuOpen {
			pPostMessageW.Call(a.hwnd, wmLaunchpadHide, 0, 0)
		}
	}
	h.onClose = func() { a.hideLaunchpad() }
	h.bind("mdEnv", func() launchpadEnv {
		acc, on := accentColors(a.uiDark())
		return launchpadEnv{Font: a.cfg.Font, Dark: a.uiDark(), Accent: acc, OnAccent: on,
			Panel: a.uiPanel(), Alpha: a.lpAlpha(), Scale: a.launchpadScale(),
			Look: fmt.Sprintf("%s|%v|%s|%d|%d|%d", a.cfg.IconPack, a.dock.Dark, a.cfg.IconStyle, a.cfg.IconPackScale, a.cfg.AppIconScale, len(a.cfg.IconOverrides)),
			Cols: a.launchpadCols(), Icon: a.launchpadIconSize(), Names: !a.cfg.LaunchpadNoNames, SidePanel: map[bool]string{true: "icons", false: "text"}[a.cfg.LaunchpadIconPanel], Recent: !a.cfg.LaunchpadNoRecent, Search: a.lp.searchFirst,
			Dock:       [...]string{"bottom", "left", "right", "top"}[min(max(a.dock.Orient, 0), 3)],
			CardSpring: !a.cfg.NoBounce && !a.cfg.NoCardSpring}
	})
	h.bind("mdApps", func() []launchpadApp { return a.launchpadApps() })
	h.bind("mdGroups", func() []launchGroup { return a.launchpadGroups(a.launchpadApps()) })
	h.bind("mdSetGroup", func(ids, names []string, group string) bool {
		a.setAppGroups(ids, names, group)
		return true
	})
	h.bind("mdNewFolder", func(name string, ids, names []string) string {
		n := a.uniqueFolderName(name)
		a.cfg.LaunchpadFolders = append(a.cfg.LaunchpadFolders, n)
		a.setAppGroups(ids, names, n)
		return n
	})
	h.bind("mdRenameFolder", func(old, name string) string { return a.renameFolder(old, name) })
	h.bind("mdDeleteFolder", func(name string) bool { a.deleteFolder(name); return true })
	h.bind("mdDrag", func(phase string, x, y float64, id string) bool { return a.launchpadDrag(phase, x, y, id) })
	h.bind("mdGroupMenu", func(name string) bool {
		a.lp.menuGroup = name
		pPostMessageW.Call(a.hwnd, wmLaunchpadMenu, 1, 0)
		return true
	})
	h.bind("mdIcons", func(ids []string, names []string) map[string]string {
		out := map[string]string{}
		for i, id := range ids {
			if i < len(names) {
				out[id] = a.launchpadIcon(id, names[i])
			}
		}
		return out
	})
	h.bind("mdRecent", func() []string { return a.cfg.LaunchpadRecent })
	h.bind("mdFavs", func() []string { return a.launchpadFavs() })
	h.bind("mdSetFavs", func(list []string) bool {
		if list == nil {
			list = []string{}
		}
		a.cfg.LaunchpadFavs = list
		a.saveConfig()
		return true
	})
	h.bind("mdLaunch", func(id string) bool {
		a.noteLaunched(id)
		pPostMessageW.Call(a.hwnd, wmLaunchpadHide, 0, 0)
		shellOpen("explorer.exe", `shell:AppsFolder\`+id)
		return true
	})
	h.bind("mdMenu", func(id, name string) bool {
		a.lp.menuID, a.lp.menuName = id, name
		pPostMessageW.Call(a.hwnd, wmLaunchpadMenu, 0, 0) // menus run their own loop: not inside this callback
		return true
	})
	h.bind("mdPower", func(action string) bool {
		powerActs := map[string]uintptr{"sleep": 1, "lock": 2, "signout": 3, "restart": 4, "shutdown": 5}
		if n := powerActs[action]; n != 0 {
			pPostMessageW.Call(a.hwnd, wmLaunchpadPower, n, 0) // off the WebView2 callback
		}
		return true
	})
	h.bind("mdHide", func() bool { pPostMessageW.Call(a.hwnd, wmLaunchpadHide, 0, 0); return true })
	h.bind("mdSearch", func(q string) []searchRow { return a.runSearch(q) })
	h.bind("mdRun", func(id string, alt bool) bool {
		if strings.HasPrefix(id, "app:") {
			a.noteLaunched(id[4:])
		}
		ok := a.runSearchItem(id, alt)
		pPostMessageW.Call(a.hwnd, wmLaunchpadHide, 0, 0)
		return ok
	})
	h.bind("mdReady", func() bool { pPostMessageW.Call(a.hwnd, wmLaunchpadReveal, 0, 0); return true })
	h.load(launchpadHTML)
	return true
}

// launchpadScale is the display scale where the Launchpad is showing.
func (a *App) launchpadScale() float64 {
	if a.lp.onMon != (RECT{}) {
		pt := uintptr(uint32(a.lp.onMon.Left+1)) | uintptr(uint32(a.lp.onMon.Top+1))<<32
		m, _, _ := pMonitorFromPoint.Call(pt, 2)
		if k := monitorDPI(m); k > 0 {
			return k
		}
	}
	return a.dock.Scale
}

// launchpadRect: the card, popping out of the Start icon (or above the dock's edge).
func (a *App) launchpadRect() RECT {
	m := a.mon
	if a.lp.onMon != (RECT{}) {
		m = a.lp.onMon
	}
	d := a.dock
	s := a.launchpadScale()
	// as wide as the grid you chose
	side := 184.0 // the side panel, and the gap after it
	if a.cfg.LaunchpadIconPanel {
		side = 64
	}
	cw := float64(a.launchpadCols())*launchCellWidth(a.launchpadIconSize(), !a.cfg.LaunchpadNoNames) + side + 2*12 + 14
	cw = math.Max(cw, 440)
	w := min(int32(math.Round(cw*s)), m.Right-m.Left-int32(16*s))
	h := min(int32(math.Round(620*s)), m.Bottom-m.Top-int32(120*s))
	clampX := func(x int32) int32 { return max(m.Left+int32(8*s), min(x, m.Right-int32(8*s)-w)) }
	clampY := func(y int32) int32 { return max(m.Top+int32(8*s), min(y, m.Bottom-int32(8*s)-h)) }
	if a.lp.onMon != (RECT{}) && a.lp.anchorWin != 0 { // out of the other dock's Start icon
		px, py := a.lp.anchor.X, a.lp.anchor.Y
		switch orientOf(a.cfg.Position) {
		case OrientLeft:
			return RECT{px, clampY(py - h/2), px + w, clampY(py-h/2) + h}
		case OrientRight:
			return RECT{px - w, clampY(py - h/2), px, clampY(py-h/2) + h}
		case OrientTop:
			return RECT{clampX(px - w/2), py, clampX(px-w/2) + w, py + h}
		default:
			return RECT{clampX(px - w/2), py - h, clampX(px-w/2) + w, py}
		}
	}
	if a.lp.onMon == (RECT{}) {
		for _, st := range d.Items {
			if st.Kind != KindStart {
				continue
			}
			// out of the Start icon, like Search
			fx, fy := d.toXY(st.U, d.M+d.PlateH+math.Round(16*d.Scale))
			px, py := a.winX+int32(fx), a.winY+int32(fy)
			switch d.Orient {
			case OrientLeft:
				return RECT{px, clampY(py - h/2), px + w, clampY(py-h/2) + h}
			case OrientRight:
				return RECT{px - w, clampY(py - h/2), px, clampY(py-h/2) + h}
			case OrientTop:
				return RECT{clampX(px - w/2), py, clampX(px-w/2) + w, py + h}
			default:
				return RECT{clampX(px - w/2), py - h, clampX(px-w/2) + w, py}
			}
		}
	}
	// opened from another display (or no Start icon): centred along the dock's edge
	off := int32(math.Round((d.M+d.PlateH)/d.Scale*s + 16*s))
	cx, cy := (m.Left+m.Right)/2, (m.Top+m.Bottom)/2
	switch orientOf(a.cfg.Position) {
	case OrientLeft:
		return RECT{m.Left + off, cy - h/2, m.Left + off + w, cy + h/2}
	case OrientRight:
		return RECT{m.Right - off - w, cy - h/2, m.Right - off, cy + h/2}
	case OrientTop:
		return RECT{cx - w/2, m.Top + off, cx + w/2, m.Top + off + h}
	default:
		return RECT{cx - w/2, m.Bottom - off - h, cx + w/2, m.Bottom - off}
	}
}

func (a *App) showLaunchpad(onMon RECT) {
	if time.Since(a.lp.hiddenAt) < 200*time.Millisecond {
		return // the click that closed it shouldn't reopen it
	}
	a.hidePanel(false)
	a.lp.onMon = onMon
	if onMon == (RECT{}) || time.Since(a.lp.anchorAt) > time.Second {
		a.lp.anchorWin = 0 // no fresh anchor from the other dock: centre the card
	}
	a.createLaunchpadWindow()
	if a.lp.hwnd == 0 {
		return
	}
	dwmInt(a.lp.hwnd, 20, boolI(a.dock.Dark))
	// cloaked (drawn but invisible) until the page is ready, so it never flashes
	dwmInt(a.lp.hwnd, 13, 1)
	r := a.launchpadRect()
	pSetWindowPos.Call(a.lp.hwnd, ^uintptr(0), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x0010)
	a.roundLaunchpad()
	// the dock draws a pointer from the Start icon up to the card, as it does for Search
	if onMon == (RECT{}) {
		for _, it := range a.dock.Items {
			if it.Kind == KindStart {
				a.dock.SearchGap = 0 // the arrow appears with the card (revealLaunchpad)
				a.dock.SearchItem, a.dock.SearchT = it, 0
				a.dock.PopItem, a.dock.PopT, a.dock.PopTarget = nil, 0, 0
				a.dock.HideTarget = 0
				a.needRender = true
				a.render()
				break
			}
		}
	}
	first := a.lp.host == nil
	pShowWindow.Call(a.lp.hwnd, 5)
	pSetForegroundWindow.Call(a.lp.hwnd)
	if !a.ensureLaunchpadHost() {
		pShowWindow.Call(a.lp.hwnd, SW_HIDE)
		dwmInt(a.lp.hwnd, 13, 0)
		messageBox("Launchpad needs the Microsoft Edge WebView2 Runtime (microsoft.com/edge/webview2).", appName)
		return
	}
	a.lp.shown, a.lp.revealPending = true, true
	a.lp.host.web.Show()
	a.lp.host.web.Resize()
	if !first {
		a.lp.host.eval("window.mdShown && window.mdShown()")
	}
	wait := uintptr(250)
	if first {
		wait = 1500
	}
	pSetTimer.Call(a.hwnd, timerLaunchpad, wait, 0) // fallback; the page says when it's ready
}

func (a *App) revealLaunchpad() {
	pKillTimer(a.hwnd, timerLaunchpad)
	if !a.lp.shown || !a.lp.revealPending {
		return
	}
	a.lp.revealPending = false
	pDwmFlush.Call()
	dwmInt(a.lp.hwnd, 13, 0)
	a.lp.host.eval("window.mdReveal && window.mdReveal()")
	if a.lp.anchorWin != 0 {
		pPostMessageW.Call(a.lp.anchorWin, uintptr(msgLaunchpadArrow), 1, 0)
	}
	if a.dock.SearchItem != nil {
		a.dock.SearchGap = math.Round(16 * a.dock.Scale)
		a.needRender = true
		a.animUntil = time.Now().Add(400 * time.Millisecond)
	}
	a.lp.host.web.Focus()
}

func (a *App) hideLaunchpad() {
	if !a.lp.shown {
		return
	}
	a.lp.shown, a.lp.revealPending = false, false
	if a.lp.host != nil && a.lp.host.web != nil {
		a.lp.host.eval("window.mdHidden && window.mdHidden()")
		a.lp.host.web.Hide()
	}
	pShowWindow.Call(a.lp.hwnd, SW_HIDE)
	dwmInt(a.lp.hwnd, 13, 1)
	a.lp.hiddenAt = time.Now()
	a.lp.onMon = RECT{}
	a.lp.searchFirst = false
	if a.lp.anchorWin != 0 {
		pPostMessageW.Call(a.lp.anchorWin, uintptr(msgLaunchpadArrow), 0, 0)
		a.lp.anchorWin = 0
	}
	if a.dock.SearchItem != nil {
		a.dock.SearchItem = nil // the pointer goes with the card
		a.needRender = true
		a.render()
	}
	if a.lp.dragOn {
		a.drag, a.lp.dragOn = nil, false
		a.closeDropGap()
	}
	a.pruneEmptyFolders()
}

// launchpadApps: every app in the Start menu's list, A to Z.
func (a *App) launchpadApps() []launchpadApp {
	apps := a.installedApps()
	out := make([]launchpadApp, 0, len(apps))
	seen := map[string]bool{}
	seenName := map[string]bool{}
	for _, ap := range apps {
		k := strings.ToLower(ap.ID)
		if seen[k] || ap.Name == "" {
			continue
		}
		seen[k] = true
		if !a.cfg.LaunchpadShowAll {
			if isHelperApp(ap.Name) {
				continue
			}
			n := sameAppName(ap.Name)
			if seenName[n] {
				continue // the 32-bit and 64-bit copies of the same tool, say
			}
			seenName[n] = true
		}
		out = append(out, launchpadApp{ID: ap.ID, Name: ap.Name, Pinned: a.hasPin(KindApp, ap.ID) >= 0, Group: a.appGroup(ap.ID, ap.Name)})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func appIconKey(id string) string { return "app:" + strings.ToLower(id) }

// appCustomIcon is the icon you chose for an app, in Launchpad or on its dock pin.
func (a *App) appCustomIcon(id string) string {
	if p := a.cfg.IconOverrides[appIconKey(id)]; p != "" {
		return p
	}
	if i := a.hasPin(KindApp, id); i >= 0 {
		return a.cfg.Pins[i].Icon
	}
	return ""
}

// launchpadIcon draws an app's icon exactly as the dock would: your own icon, else the
// icon pack's, else the app's, in the dock's icon style.
func (a *App) launchpadIcon(id, name string) string {
	custom := a.appCustomIcon(id)
	look := fmt.Sprintf("%s|%s|%s|%v|%s|%d|%d%s", id, custom, a.cfg.IconPack, a.dock.Dark, a.cfg.IconStyle, a.cfg.IconPackScale, a.cfg.AppIconScale, a.packSigIf())
	if a.lp.icons == nil {
		a.lp.icons = map[string]string{}
	}
	if u, ok := a.lp.icons[look]; ok {
		return u
	}
	it := &Item{Kind: KindApp, Name: name, Target: id}
	it.Raw, it.RawKey = a.rawIcon(`shell:AppsFolder\`+id, false), `shell:AppsFolder\`+id
	if custom != "" {
		if img := a.customIcon(custom); img != nil {
			it.Raw, it.RawKey = img, "custom:"+custom
			it.PackIcon = true
		}
	} else {
		a.applyPack(it)
	}
	a.assignIcon(it, it.RawKey, it.Raw)
	u := ""
	if it.Icon != nil && len(it.Icon.levels) > 0 {
		u = iconDataURL(it.Icon.levels[0], 144)
	}
	if len(a.lp.icons) > 800 {
		a.lp.icons = map[string]string{}
	}
	a.lp.icons[look] = u
	return u
}

// launchpadMenu is the right-click menu for an app in Launchpad.
func (a *App) launchpadMenu() {
	id, name := a.lp.menuID, a.lp.menuName
	if id == "" || !a.lp.shown {
		return
	}
	key := appIconKey(id)
	m := &macMenu{}
	m.header(name)
	m.addG(GlyphOpen, "Open", func() {
		a.noteLaunched(id)
		a.hideLaunchpad()
		shellOpen("explorer.exe", `shell:AppsFolder\`+id)
	})
	if a.isFavourite(id) {
		m.addG(GlyphStar, "Remove from Favourites", func() { a.setFavourite(id, false) })
	} else {
		m.addG(GlyphStar, "Add to Favourites", func() { a.setFavourite(id, true) })
	}
	if a.hasPin(KindApp, id) >= 0 {
		m.addG(GlyphUnpin, "Remove from Dock", func() {
			if i := a.hasPin(KindApp, id); i >= 0 {
				a.removePinAt(i)
			}
			a.refreshLaunchpad()
		})
	} else {
		m.addG(GlyphPin, "Keep in Dock", func() {
			a.addPin(Pin{Kind: KindApp, Target: id})
			a.refreshLaunchpad()
		})
	}
	group := a.appGroup(id, name)
	mv := m.sub(GlyphFolder, "Move to")
	for _, g := range a.launchpadGroups(a.launchpadApps()) {
		g := g
		it := mv.add(g.Name, func() {
			a.setAppGroups([]string{id}, []string{name}, g.Name)
			a.refreshLaunchpad()
		})
		if g.Name == group {
			it.Checked = true
		}
	}
	mv.sep()
	mv.addG(GlyphAdd, "New category", func() {
		n := a.uniqueFolderName("")
		a.cfg.LaunchpadFolders = append(a.cfg.LaunchpadFolders, n)
		a.setAppGroups([]string{id}, []string{name}, n)
		a.refreshLaunchpad()
	})
	if a.cfg.LaunchpadGroups[strings.ToLower(id)] != "" {
		mv.addG(GlyphReset, "Back to "+autoCategory(id, name), func() {
			a.setAppGroups([]string{id}, []string{name}, "")
			a.refreshLaunchpad()
		})
	}
	m.sep()
	m.addG(GlyphBrush, "Change icon…", func() {
		a.hideLaunchpad()
		a.changeKeyIcon(key, name)
	})
	if a.appCustomIcon(id) != "" {
		m.addG(GlyphReset, "Use original icon", func() {
			a.setAppIcon(id, "")
			a.refreshLaunchpad()
		})
	}
	m.sep()
	if t := appTargetPath(id); t != "" {
		m.addG(GlyphExplorer, "Show in File Explorer", func() {
			a.hideLaunchpad()
			shellOpen("explorer.exe", `/select,"`+t+`"`)
		})
	}
	m.addG(GlyphDelete, "Uninstall…", func() { a.uninstallApp(id, name) })
	var r RECT
	pGetWindowRect.Call(a.lp.hwnd, uintptr(unsafe.Pointer(&r)))
	cx, cy := getCursorPos()
	a.lp.menuOpen = true
	a.runMenuAt(m, float64(cx), float64(cy), r)
	a.lp.menuOpen = false
	if a.lp.shown {
		pSetForegroundWindow.Call(a.lp.hwnd)
		if a.lp.host != nil {
			a.lp.host.web.Focus()
		}
	}
}

// setAppIcon sets (or with "" resets) the icon for an app everywhere: Launchpad,
// its dock pin and its running windows.
func (a *App) setAppIcon(id, path string) { a.setIconOverride(appIconKey(id), path) }

// appIconChanged follows a changed app icon through to its dock pin, the dock and Launchpad.
func (a *App) appIconChanged(key string) {
	id := strings.TrimPrefix(key, "app:")
	for i := range a.cfg.Pins {
		if a.cfg.Pins[i].Kind == KindApp && strings.EqualFold(a.cfg.Pins[i].Target, id) {
			a.cfg.Pins[i].Icon = a.cfg.IconOverrides[key]
		}
	}
	a.saveConfig()
	a.reloadItems()
	a.refreshLaunchpad()
}

// refreshLaunchpad has an open Launchpad (or the next one) redraw its icons.
func (a *App) refreshLaunchpad() {
	if a.lp.host != nil {
		a.lp.host.eval("window.mdRefresh && window.mdRefresh()")
	}
}

func (a *App) launchpadCols() int {
	if c := a.cfg.LaunchpadCols; c >= 3 && c <= 12 {
		return c
	}
	return 6
}

func (a *App) launchpadIconSize() int {
	if v := a.cfg.LaunchpadIcon; v >= 32 && v <= 112 {
		return v
	}
	return 60
}

// launchCellWidth: how wide one app's space is (the page uses the same sum).
func launchCellWidth(icon int, names bool) float64 {
	if names {
		return math.Max(float64(icon)+44, float64(icon)*1.5)
	}
	return float64(icon) + 22
}

// launchpadDrag follows an app being dragged out of Launchpad: over the dock it opens
// a gap where it will go, and dropping it there keeps it in the dock. x and y are in
// pixels from the Launchpad window's corner. It returns true while over a dock.
func (a *App) launchpadDrag(phase string, x, y float64, id string) bool {
	var r RECT
	pGetWindowRect.Call(a.lp.hwnd, uintptr(unsafe.Pointer(&r)))
	sx, sy := r.Left+int32(math.Round(x)), r.Top+int32(math.Round(y))
	overMain := a.pointOverDock(sx, sy)
	pin := Pin{Kind: KindApp, Target: id}
	switch phase {
	case "move":
		if overMain && !a.hiddenFullscreen {
			if a.drag == nil || !a.lp.dragOn {
				a.drag, a.lp.dragOn = &dragged{pins: []Pin{pin}}, true
			}
			a.dragOver(sx, sy, dropLink)
			return true
		}
		if a.lp.dragOn {
			a.drag, a.lp.dragOn = nil, false
			a.closeDropGap()
		}
		return otherDockAt(sx, sy)
	case "end":
		done := false
		if overMain && a.lp.dragOn && a.drag != nil {
			a.doDrop(sx, sy)
			done = true
		} else if otherDockAt(sx, sy) && a.hasPin(KindApp, id) < 0 {
			a.addPin(pin) // a dock on another display: it goes at the end
			done = true
		}
		if a.lp.dragOn {
			a.drag, a.lp.dragOn = nil, false
			a.closeDropGap()
		}
		return done
	default: // cancel
		if a.lp.dragOn {
			a.drag, a.lp.dragOn = nil, false
			a.closeDropGap()
		}
	}
	return false
}

// pointOverDock: is a screen point over this dock's icons (not just its tall window)?
func (a *App) pointOverDock(sx, sy int32) bool {
	d := a.dock
	if sx < a.winX || sy < a.winY || sx >= a.winX+int32(d.W) || sy >= a.winY+int32(d.H) {
		return false
	}
	u, v := d.toUV(float64(sx-a.winX), float64(sy-a.winY))
	return v <= d.PlateV1+d.B*0.6 && u >= d.PlateU0-d.B && u <= d.PlateU1+d.B
}

// otherDockAt: is a screen point over a dock on another display (another DG-Dock process)?
func otherDockAt(sx, sy int32) bool {
	h, _, _ := pWindowFromPoint.Call(uintptr(uint32(sx)) | uintptr(uint32(sy))<<32)
	if h == 0 {
		return false
	}
	root, _, _ := pGetAncestor.Call(h, 2)
	if className(root) != "DGDockWindow" {
		return false
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(root, uintptr(unsafe.Pointer(&pid)))
	return pid != uint32(windows.GetCurrentProcessId())
}

var pWindowFromPoint = user32.NewProc("WindowFromPoint")

// launchpadFolderMenu is the right-click menu for one of your folders.
func (a *App) launchpadFolderMenu() {
	name := a.lp.menuGroup
	if name == "" || !a.lp.shown || isCategory(name) {
		return
	}
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	m := &macMenu{}
	m.header(name)
	m.addG(GlyphBrush, "Rename", func() {
		a.lp.host.eval("window.mdStartRename && window.mdStartRename(" + js(name) + ")")
	})
	m.addG(GlyphDelete, "Delete category", func() {
		a.deleteFolder(name)
		a.refreshLaunchpad()
	})
	var r RECT
	pGetWindowRect.Call(a.lp.hwnd, uintptr(unsafe.Pointer(&r)))
	cx, cy := getCursorPos()
	a.lp.menuOpen = true
	a.runMenuAt(m, float64(cx), float64(cy), r)
	a.lp.menuOpen = false
	if a.lp.shown {
		pSetForegroundWindow.Call(a.lp.hwnd)
		if a.lp.host != nil {
			a.lp.host.web.Focus()
		}
	}
}

// noteLaunched keeps the Recent list: the apps you last opened from Launchpad.
func (a *App) noteLaunched(id string) {
	list := []string{id}
	for _, r := range a.cfg.LaunchpadRecent {
		if !strings.EqualFold(r, id) && len(list) < 16 {
			list = append(list, r)
		}
	}
	a.cfg.LaunchpadRecent = list
	a.saveConfigLater()
}

// launchpadFavs is the Favourites list. The first time, it starts with the apps
// in your dock so Home isn't empty.
func (a *App) launchpadFavs() []string {
	if a.cfg.LaunchpadFavs != nil {
		return a.cfg.LaunchpadFavs
	}
	favs := []string{}
	for _, p := range a.cfg.Pins {
		if p.Kind == KindApp && len(favs) < 12 {
			favs = append(favs, p.Target)
		}
	}
	return favs
}

// roundLaunchpad gives the card the dock cards' 14px corners.
func (a *App) roundLaunchpad() {
	var r RECT
	pGetWindowRect.Call(a.lp.hwnd, uintptr(unsafe.Pointer(&r)))
	rad := uintptr(math.Round(28 * a.launchpadScale()))
	if rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(r.Right-r.Left+1), uintptr(r.Bottom-r.Top+1), rad, rad); rgn != 0 {
		pSetWindowRgn.Call(a.lp.hwnd, rgn, 1)
	}
}

func (a *App) isFavourite(id string) bool {
	for _, f := range a.launchpadFavs() {
		if strings.EqualFold(f, id) {
			return true
		}
	}
	return false
}

// setFavourite adds an app to the end of Favourites, or takes it off.
func (a *App) setFavourite(id string, on bool) {
	var list []string
	for _, f := range a.launchpadFavs() {
		if !strings.EqualFold(f, id) {
			list = append(list, f)
		}
	}
	if on {
		list = append(list, id)
	}
	if list == nil {
		list = []string{}
	}
	a.cfg.LaunchpadFavs = list
	a.saveConfig()
	if a.lp.host != nil {
		if on {
			// Jump to Home and confirm, so the add is always visible even when
			// the app was right-clicked from All apps or a category.
			a.lp.host.eval("window.mdFavAdded && window.mdFavAdded()")
		} else {
			a.lp.host.eval("window.mdReload && window.mdReload()")
		}
	}
}

// prewarmLaunchpad gets Launchpad (and search) ready in the background a few seconds
// after the dock starts, so the first click on Start opens it straight away instead
// of waiting for Edge WebView2 to start and the app list and icons to load.
func (a *App) prewarmLaunchpad() {
	if isSecondary() || a.lp.host != nil {
		return
	}
	a.installedApps()
	if sIndex.entries == nil {
		a.buildSearchIndex()
	}
	a.createLaunchpadWindow()
	if a.lp.hwnd == 0 {
		return
	}
	dwmInt(a.lp.hwnd, 13, 1) // cloaked: nothing shows
	r := a.launchpadRect()
	pSetWindowPos.Call(a.lp.hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x0010|0x0004)
	a.roundLaunchpad()
	a.ensureLaunchpadHost()
}

// forwardLaunchpadAnchor (extra docks) tells the main dock where this dock's Start
// icon is on screen, just before asking it to show the Launchpad here.
func (a *App) forwardLaunchpadAnchor() {
	h := mainDockWindow()
	if h == 0 {
		return
	}
	d := a.dock
	for _, st := range d.Items {
		if st.Kind == KindStart {
			fx, fy := d.toXY(st.U, d.M+d.PlateH+math.Round(16*d.Scale))
			pPostMessageW.Call(h, uintptr(msgLaunchpadAnchor), packPoint(a.winX+int32(fx), a.winY+int32(fy)), a.hwnd)
			return
		}
	}
}

// setLaunchpadArrow (extra docks): the main dock showed or hid the Launchpad over
// this dock; draw the arrow from the Start icon, as the main dock does.
func (a *App) setLaunchpadArrow(on bool) {
	d := a.dock
	d.SearchItem, d.SearchGap, d.SearchT = nil, 0, 0
	if on {
		for _, it := range d.Items {
			if it.Kind == KindStart {
				d.SearchItem, d.SearchGap = it, math.Round(16*d.Scale)
				d.PopItem, d.PopT, d.PopTarget = nil, 0, 0
				d.HideTarget = 0
				break
			}
		}
	}
	a.animUntil = time.Now().Add(400 * time.Millisecond)
	a.needRender = true
	a.render()
}

func (a *App) lpAlpha() float64 {
	if !a.lp.acrylic {
		return 1
	}
	return a.uiAlpha()
}
