//go:build windows

package main

// Live window previews, folder stacks, widgets, scroll-to-cycle and follow-the-mouse
// multi-monitor support.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	pDwmRegisterThumbnail         = dwmapi.NewProc("DwmRegisterThumbnail")
	pDwmUnregisterThumbnail       = dwmapi.NewProc("DwmUnregisterThumbnail")
	pDwmUpdateThumbnailProperties = dwmapi.NewProc("DwmUpdateThumbnailProperties")
	pDwmQueryThumbnailSourceSize  = dwmapi.NewProc("DwmQueryThumbnailSourceSize")
	pGetSystemPowerStatus         = kernel32.NewProc("GetSystemPowerStatus")
	pCreateRectRgn                = gdi32.NewProc("CreateRectRgn")
	pCombineRgn                   = gdi32.NewProc("CombineRgn")
	thumbProcCB                   uintptr
)

type dwmThumbProps struct {
	Flags      uint32
	Dest       RECT
	Src        RECT
	Opacity    byte
	_          [3]byte
	Visible    int32
	ClientOnly int32
}

// ---------- widgets ----------

type powerStatus struct {
	ACLine, Flag, Percent, Saver byte
	Life, FullLife               uint32
}

func batteryInfo() (pct int, charging bool, life int, ok bool) {
	var ps powerStatus
	if r, _, _ := pGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps))); r == 0 {
		return 0, false, 0, false
	}
	if ps.Flag&128 != 0 || ps.Percent == 255 { // no system battery
		return 0, false, 0, false
	}
	life = -1
	if ps.Life != 0xFFFFFFFF {
		life = int(ps.Life)
	}
	return int(ps.Percent), ps.ACLine == 1, life, true
}

func use12h() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\International`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("sShortTime")
	return err == nil && strings.Contains(v, "h") && !strings.Contains(v, "H")
}

func (a *App) widgetItems() []*Item {
	var out []*Item
	add := func(key, w, name string, width float64) {
		it, ok := a.items[key]
		if !ok {
			it = &Item{Key: key, Kind: KindWidget, Widget: w, Name: name, WidthB: width, Scale: 1, Appear: 1, WHot: -1, Section: 2}
			a.items[key] = it
		}
		out = append(out, it)
	}
	add("w:clock", "clock", "Clock", 1.55)
	add("w:status", "status", "Battery and Wi-Fi", 1.15)
	add("w:media", "media", "Now playing", 2.1)
	if !a.cfg.NoTray {
		add("w:tray", "tray", "System tray", 1.0)
	}
	add("w:desktop", "desktop", "Show desktop", 0.32)
	if it := a.items["w:media"]; it != nil {
		it.Art, it.WPlaying, it.WText1, it.WAccent = a.mediaArt, a.media.Playing, a.media.Title, a.mediaAccent
	}
	a.updateWidgets()
	return out
}

// updateWidgets refreshes clock and battery text; returns true when anything changed.
func (a *App) updateWidgets() bool {
	changed := false
	now := time.Now()
	if it := a.items["w:clock"]; it != nil {
		t1 := now.Format("15:04")
		if use12h() {
			t1 = now.Format("3:04")
		}
		t2 := now.Format("Mon 2 Jan")
		if t1 != it.WText1 || t2 != it.WText2 {
			it.WText1, it.WText2 = t1, t2
			changed = true
		}
	}
	if it := a.items["w:status"]; it != nil {
		pct, ch, _, ok := batteryInfo()
		if time.Since(a.wifiAt) > 2*time.Second {
			a.wifiAt = time.Now()
			a.wifi = readWifi()
		}
		dots := wifiDots(a.wifi)
		t := fmt.Sprintf("%d", pct)
		if t != it.WText1 || ch != it.WCharging || ok != it.WHasBt || dots != it.WWifi {
			it.WText1, it.WLevel, it.WCharging, it.WHasBt, it.WWifi = t, float64(pct)/100, ch, ok, dots
			changed = true
		}
	}
	if it := a.items["w:battery"]; it != nil {
		pct, ch, _, ok := batteryInfo()
		t := fmt.Sprintf("%d%%", pct)
		if ok && (t != it.WText1 || ch != it.WCharging) {
			it.WText1, it.WLevel, it.WCharging = t, float64(pct)/100, ch
			changed = true
		}
	}
	return changed
}

func (a *App) widgetPop(it *Item) PopContent {
	switch it.Widget {
	case "tray":
		icons := tray.list()
		if len(icons) == 0 {
			return PopContent{Title: "System tray", Subtitle: "Apps that sit in the system tray show up here"}
		}
		pc := PopContent{Title: "System tray", Subtitle: "Click to open · right-click for more", Mode: PopGrid}
		for _, ic := range icons {
			is, big := a.trayRowIcon(ic)
			pc.Rows = append(pc.Rows, PopRow{Title: trayAppName(ic), Path: "tray:" + ic.key, Icon: is, Small: !big})
		}
		return pc
	case "desktop":
		return PopContent{Title: "Show desktop", Subtitle: "Click again to bring your windows back"}
	case "clock":
		return PopContent{Title: time.Now().Format("Monday 2 January 2006"), Subtitle: "Click for calendar and notifications"}
	case "status":
		pct, ch, life, ok := batteryInfo()
		title := "Plugged in"
		if ok {
			sub := "on battery"
			switch {
			case ch && pct >= 100:
				sub = "fully charged"
			case ch:
				sub = "charging"
			case life > 0:
				sub = fmt.Sprintf("%d h %02d min left", life/3600, (life%3600)/60)
			}
			title = fmt.Sprintf("Battery %d%% · %s", pct, sub)
		}
		w := readWifi()
		wsub := "Offline"
		switch {
		case w.Wired:
			wsub = "Connected by cable"
		case w.Connected:
			wsub = "Wi-Fi · " + signalWord(w.Bars)
		}
		return PopContent{Title: title, Subtitle: wsub + " · scroll for brightness"}
	case "battery":
		pct, ch, life, _ := batteryInfo()
		sub := "On battery"
		switch {
		case ch && pct >= 100:
			sub = "Fully charged"
		case ch:
			sub = "Charging"
		case life > 0:
			sub = fmt.Sprintf("About %d h %02d min remaining", life/3600, (life%3600)/60)
		}
		return PopContent{Title: fmt.Sprintf("Battery %d%%", pct), Subtitle: sub}
	}
	return a.mediaPop()
}

func sendKey(vk uintptr) {
	pKeybdEvent.Call(vk, 0, 1, 0) // KEYEVENTF_EXTENDEDKEY
	pKeybdEvent.Call(vk, 0, 1|2, 0)
}

func (a *App) widgetClick(it *Item) {
	switch it.Widget {
	case "desktop":
		a.dock.PopItem, a.dock.PopTarget = nil, 0
		pKeybdEvent.Call(0x5B, 0, 0, 0) // Win+D
		pKeybdEvent.Call('D', 0, 0, 0)
		pKeybdEvent.Call('D', 0, 2, 0)
		pKeybdEvent.Call(0x5B, 0, 2, 0)
	case "clock":
		pKeybdEvent.Call(0x5B, 0, 0, 0) // Win+N: notification centre with calendar
		pKeybdEvent.Call('N', 0, 0, 0)
		pKeybdEvent.Call('N', 0, 2, 0)
		pKeybdEvent.Call(0x5B, 0, 2, 0)
	case "status":
		pKeybdEvent.Call(0x5B, 0, 0, 0) // Win+A: quick settings (Wi-Fi, battery)
		pKeybdEvent.Call('A', 0, 0, 0)
		pKeybdEvent.Call('A', 0, 2, 0)
		pKeybdEvent.Call(0x5B, 0, 2, 0)
	case "battery":
		shellOpen("ms-settings:batterysaver", "")
	case "media":
		cx, cy := getCursorPos()
		u, _ := a.dock.toUV(float64(cx-a.winX), float64(cy-a.winY))
		switch a.dock.MediaButtonAt(it, u) {
		case 3:
			a.showMediaApp()
		case 0:
			a.mediaKey("media:prev")
		case 1:
			a.mediaKey("media:play")
		case 2:
			a.mediaKey("media:next")
		}
	}
}

// ---------- folder stacks ----------

// stackPop is a folder's card: its contents as a grid, newest first. Click a folder
// in the grid to open it inside the card (a Back tile takes you up again), and scroll
// to see everything when there's more than one card's worth.
func (a *App) stackPop(it *Item) PopContent {
	if a.stackItem != it {
		a.stackItem, a.stackPath, a.stackScroll = it, "", 0
	}
	root := it.Browse
	if root == "" {
		root = it.Target
	}
	dir := root
	if a.stackPath != "" {
		dir = a.stackPath
	}
	title := it.Name
	if dir != root {
		title = filepath.Base(dir)
	}
	pc := PopContent{Title: title, Mode: PopGrid}
	entries, _ := os.ReadDir(dir)
	type ent struct {
		name string
		t    time.Time
	}
	var list []ent
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") || strings.EqualFold(n, "desktop.ini") || strings.HasPrefix(n, "~$") {
			continue
		}
		if info, err := e.Info(); err == nil {
			list = append(list, ent{n, info.ModTime()})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].t.After(list[j].t) })
	inside := dir != root
	where := "Folder"
	if inside {
		where = it.Name + " › " + title
	}
	addIcon := func(row *PopRow, p string) {
		if is, ok := a.styled["small:"+p]; ok {
			row.Icon = is
		} else {
			a.iconQueue = append(a.iconQueue, p)
		}
	}
	slots := maxGrid
	if inside {
		back := PopRow{Title: "‹ Back", Path: "stack:back"}
		addIcon(&back, filepath.Dir(dir))
		pc.Rows = append(pc.Rows, back)
		slots--
	}
	if len(list) == 0 {
		pc.Subtitle = where + " · empty"
		if !inside {
			pc.Mode = PopList
			pc.Subtitle = "This folder is empty"
		}
		return pc
	}
	// scrolling moves a whole row (4 tiles) at a time
	const cols = 4
	maxScroll := 0
	if len(list) > slots {
		maxScroll = (len(list) - slots + cols - 1) / cols
	}
	a.stackScroll = max(0, min(a.stackScroll, maxScroll))
	lo := a.stackScroll * cols
	hi := min(lo+slots, len(list))
	if len(list) > slots {
		pc.Subtitle = fmt.Sprintf("%s · %d–%d of %d · scroll for more", where, lo+1, hi, len(list))
		// scroll indicator: everything in the folder (plus the Back tile) as rows of 4
		extra := maxGrid - slots
		pc.ScrollRows = (len(list) + extra + cols - 1) / cols
		pc.ScrollVis = maxGrid / cols
		pc.ScrollTop = a.stackScroll
	} else {
		pc.Subtitle = fmt.Sprintf("%s · %d items · click a folder to open it", where, len(list))
	}
	for _, e := range list[lo:hi] {
		p := filepath.Join(dir, e.name)
		row := PopRow{Title: e.name, Path: p}
		addIcon(&row, p)
		pc.Rows = append(pc.Rows, row)
	}
	return pc
}

// stackOpen handles a click on a tile in a folder card. Returns false if the tile was a
// file (the caller opens it).
func (a *App) stackOpen(p string) bool {
	d := a.dock
	if d.PopItem == nil || d.Pop.Mode != PopGrid || a.stackItem != d.PopItem {
		return false
	}
	switch {
	case p == "stack:back":
		root := a.stackItem.Browse
		if root == "" {
			root = a.stackItem.Target
		}
		up := filepath.Dir(a.stackPath)
		if a.stackPath == "" || strings.EqualFold(filepath.Clean(up), filepath.Clean(root)) || len(up) < len(root) {
			up = ""
		}
		a.stackPath = up
	default:
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			return false
		}
		a.stackPath = p
	}
	a.stackScroll = 0
	d.Pop = a.popContent(d.PopItem)
	d.PopHot = -1
	a.needRender = true
	a.setAnimInterval(15)
	return true
}

// stackExplorer handles a right-click on a tile in a folder card: folders open in
// File Explorer, and files are shown selected in their folder.
func (a *App) stackExplorer(lx, ly float64) bool {
	d := a.dock
	if d.Pop.Mode != PopGrid || a.stackItem != d.PopItem {
		return false
	}
	row := d.PopRowAt(lx, ly)
	if row < 0 {
		return false
	}
	p := d.Pop.Rows[row].Path
	if p == "stack:back" {
		p = filepath.Dir(a.stackPath)
	}
	if p == "" {
		return false
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		shellOpen(p, "")
	} else {
		shellOpen("explorer.exe", `/select,"`+p+`"`)
	}
	return true
}

// stackWheel scrolls a folder card by rows. Touchpad steps add up first.
func (a *App) stackWheel(delta int) bool {
	d := a.dock
	cx, cy := getCursorPos()
	if d.PopItem == nil || d.Pop.Mode != PopGrid || !d.InPop(float64(cx-a.winX), float64(cy-a.winY)) {
		return false
	}
	a.stackAcc += delta
	step := 0
	for a.stackAcc >= 120 {
		a.stackAcc -= 120
		step--
	}
	for a.stackAcc <= -120 {
		a.stackAcc += 120
		step++
	}
	if step != 0 {
		old := a.stackScroll
		a.stackScroll += step
		d.Pop = a.popContent(d.PopItem)
		if a.stackScroll != old {
			d.PopHot = -1
			a.needRender = true
		}
	}
	return true
}

// loadQueuedIcons loads a couple of stack icons per frame so hovering a folder never stalls.
func (a *App) loadQueuedIcons() {
	d := a.dock
	for n := 0; n < 3 && len(a.iconQueue) > 0; n++ {
		p := a.iconQueue[0]
		a.iconQueue = a.iconQueue[1:]
		is := a.smallIcon(p)
		for i := range d.Pop.Rows {
			if d.Pop.Rows[i].Path == p || d.Pop.Rows[i].Path == "stack:back" && a.stackPath != "" && p == filepath.Dir(a.stackPath) {
				d.Pop.Rows[i].Icon = is
				a.needRender = true
			}
		}
	}
	if d.PopItem == nil || d.Pop.Mode != PopGrid {
		a.iconQueue = a.iconQueue[:0]
	}
}

// ---------- live window previews ----------

type thumbReg struct {
	src   uintptr
	thumb uintptr
}

func (a *App) ensureThumbWin() {
	if a.thumbWin != 0 {
		return
	}
	if thumbProcCB == 0 {
		thumbProcCB = windows.NewCallback(thumbWndProc)
	}
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	brush, _, _ := pCreateSolidBrush.Call(0x00090807) // card colour, so letterboxing is invisible
	wc := WNDCLASSEXW{LpfnWndProc: thumbProcCB, HInstance: hinst, HCursor: cur, HbrBackground: brush, LpszClassName: u16("DGDockPreviews")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	a.thumbWin, _, _ = pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TOPMOST, up(u16("DGDockPreviews")), 0, WS_POPUP,
		0, 0, 10, 10, a.hwnd, 0, hinst, 0)
	if a.thumbWin != 0 {
		corner := uint32(1)
		pDwmSetWindowAttribute.Call(a.thumbWin, 33, uintptr(unsafe.Pointer(&corner)), 4)
	}
}

func (a *App) clearThumbs() {
	for _, t := range a.thumbs {
		pDwmUnregisterThumbnail.Call(t.thumb)
	}
	a.thumbs = nil
	a.thumbKey = ""
	if a.thumbWin != 0 && a.thumbShown {
		pShowWindow.Call(a.thumbWin, SW_HIDE)
		a.thumbShown = false
	}
}

// updateThumbs shows live previews over the flyout's tiles once it has settled.
func (a *App) updateThumbs() {
	d := a.dock
	show := d.Notch && d.PopItem != nil && d.Pop.Mode == PopThumbs && d.PopT > 0.985 && d.PopTarget == 1 &&
		math.Abs(d.PopCU-d.PopItem.U) < 1 && !a.menuOpen && !a.hiddenFullscreen
	if !show {
		if a.thumbShown || len(a.thumbs) > 0 {
			a.clearThumbs()
		}
		return
	}
	// what should be shown, and where (screen coords)
	type want struct {
		src uintptr
		r   RECT
	}
	var ws []want
	key := ""
	for i, r := range d.Pop.Rows {
		if i >= maxThumbs || r.Min || r.Hwnd == 0 || r.TX1 <= r.TX0 {
			continue
		}
		rc := RECT{a.winX + int32(math.Round(r.TX0)), a.winY + int32(math.Round(r.TY0)), a.winX + int32(math.Round(r.TX1)), a.winY + int32(math.Round(r.TY1))}
		ws = append(ws, want{r.Hwnd, rc})
		key += fmt.Sprintf("%x:%d,%d,%d,%d;", r.Hwnd, rc.Left, rc.Top, rc.Right, rc.Bottom)
	}
	if len(ws) == 0 {
		a.clearThumbs()
		return
	}
	if key == a.thumbKey {
		return
	}
	a.clearThumbs()
	a.ensureThumbWin()
	if a.thumbWin == 0 {
		return
	}
	bb := ws[0].r
	for _, w := range ws[1:] {
		bb.Left, bb.Top = min(bb.Left, w.r.Left), min(bb.Top, w.r.Top)
		bb.Right, bb.Bottom = max(bb.Right, w.r.Right), max(bb.Bottom, w.r.Bottom)
	}
	pSetWindowPos.Call(a.thumbWin, ^uintptr(0), uintptr(bb.Left), uintptr(bb.Top), uintptr(bb.Right-bb.Left), uintptr(bb.Bottom-bb.Top), SWP_NOACTIVATE)
	// only the preview rectangles are part of the window, so labels and gaps stay visible
	rad := int32(math.Round(6 * d.Scale * 2))
	rgn, _, _ := pCreateRectRgn.Call(0, 0, 0, 0)
	for _, w := range ws {
		r, _, _ := pCreateRoundRectRgn.Call(uintptr(w.r.Left-bb.Left), uintptr(w.r.Top-bb.Top), uintptr(w.r.Right-bb.Left+1), uintptr(w.r.Bottom-bb.Top+1), uintptr(rad), uintptr(rad))
		pCombineRgn.Call(rgn, rgn, r, 2) // RGN_OR
		pDeleteObject.Call(r)
	}
	pSetWindowRgn.Call(a.thumbWin, rgn, 1)
	for _, w := range ws {
		var th uintptr
		if hr, _, _ := pDwmRegisterThumbnail.Call(a.thumbWin, w.src, uintptr(unsafe.Pointer(&th))); hr != 0 || th == 0 {
			continue
		}
		dst := RECT{w.r.Left - bb.Left, w.r.Top - bb.Top, w.r.Right - bb.Left, w.r.Bottom - bb.Top}
		var sz SIZE
		if hr, _, _ := pDwmQueryThumbnailSourceSize.Call(th, uintptr(unsafe.Pointer(&sz))); hr == 0 && sz.CX > 0 && sz.CY > 0 {
			// fit preserving aspect ratio
			bw, bh := float64(dst.Right-dst.Left), float64(dst.Bottom-dst.Top)
			sc := math.Min(bw/float64(sz.CX), bh/float64(sz.CY))
			fw, fh := float64(sz.CX)*sc, float64(sz.CY)*sc
			dst.Left += int32((bw - fw) / 2)
			dst.Top += int32((bh - fh) / 2)
			dst.Right, dst.Bottom = dst.Left+int32(fw), dst.Top+int32(fh)
		}
		props := dwmThumbProps{Flags: 0x1 | 0x4 | 0x8 | 0x10, Dest: dst, Opacity: 255, Visible: 1, ClientOnly: 0}
		pDwmUpdateThumbnailProperties.Call(th, uintptr(unsafe.Pointer(&props)))
		a.thumbs = append(a.thumbs, thumbReg{w.src, th})
	}
	a.thumbKey = key
	if !a.thumbShown {
		pShowWindow.Call(a.thumbWin, SW_SHOWNA)
		a.thumbShown = true
	}
}

// clicks on a live preview land on this window
func thumbWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	a := app
	switch uint32(msg) {
	case WM_MOUSEACTIVATE:
		return MA_NOACTIVATE
	case WM_LBUTTONUP, WM_MBUTTONUP:
		if a == nil {
			break
		}
		d := a.dock
		cx, cy := getCursorPos()
		if i := d.PopRowAt(float64(cx-a.winX), float64(cy-a.winY)); i >= 0 && i < len(d.Pop.Rows) {
			w := d.Pop.Rows[i].Hwnd
			if uint32(msg) == WM_MBUTTONUP {
				pPostMessageW.Call(w, WM_CLOSE, 0, 0)
				a.refreshPopSoon()
			} else {
				activateWindow(w)
			}
		}
		return 0
	case WM_RBUTTONUP:
		if a != nil && a.dock.PopItem != nil {
			a.itemMenu(a.dock.PopItem)
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

// refreshPopSoon re-reads the flyout content shortly after a window was closed.
func (a *App) refreshPopSoon() {
	pSetTimer.Call(a.hwnd, timerPopRefresh, 350, 0)
}

const timerPopRefresh = 5

// ---------- scroll to cycle windows ----------

func (a *App) onWheel(delta int16) {
	if a.stackWheel(int(delta)) {
		return
	}
	if a.pageWheel(int(delta)) {
		return
	}
	cx, cy := getCursorPos()
	idx := a.dock.ItemAt(float64(cx-a.winX), float64(cy-a.winY))
	if idx < 0 {
		return
	}
	it := a.dock.Items[idx]
	if it.Kind == KindWidget && it.Widget == "media" {
		a.scrollVolume(delta)
		return
	}
	if it.Kind == KindWidget && it.Widget == "status" {
		a.scrollBrightness(delta)
		return
	}
	n := len(it.Windows)
	if n == 0 {
		return
	}
	if n == 1 {
		if delta > 0 {
			activateWindow(it.Windows[0])
		} else {
			pShowWindowAsync.Call(it.Windows[0], SW_MINIMIZE)
		}
		return
	}
	step := 1
	if delta > 0 {
		step = -1
	}
	it.cycle = ((it.cycle+step)%n + n) % n
	activateWindow(it.Windows[it.cycle])
}

// ---------- follow the mouse across monitors ----------

func (a *App) checkMonitorSwitch(cx, cy int32) {
	if a.cfg.MainDisplay || a.dragging() {
		return
	}
	pt := uintptr(uint32(cx)) | uintptr(uint32(cy))<<32
	m, _, _ := pMonitorFromPoint.Call(pt, 2) // NEAREST
	if m == 0 || m == a.curMon {
		a.monDwell = time.Time{}
		return
	}
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(m, uintptr(unsafe.Pointer(&mi)))
	r := mi.RcMonitor
	atEdge := false
	switch orientOf(a.cfg.Position) {
	case OrientLeft:
		atEdge = cx <= r.Left+2
	case OrientRight:
		atEdge = cx >= r.Right-3
	default:
		atEdge = cy >= r.Bottom-3 || cy >= mi.RcWork.Bottom-2
	}
	if !atEdge {
		a.monDwell = time.Time{}
		return
	}
	if a.monDwell.IsZero() {
		a.monDwell = time.Now()
		return
	}
	if time.Since(a.monDwell) < 250*time.Millisecond {
		return
	}
	a.monDwell = time.Time{}
	a.removeAppbar()
	a.curMon = m
	a.dock.PopItem, a.dock.PopT, a.dock.PopTarget = nil, 0, 0
	a.clearThumbs()
	a.relayout()
	if a.cfg.AutoHide {
		a.dock.HideTarget = 0
	}
}

func (a *App) dragging() bool { return a.dock.Drag != nil || a.mouseDown }

// ---------- instant window tracking ----------

const timerQuickScan = 6

var (
	pSetWinEventHook = user32.NewProc("SetWinEventHook")
	winEventCB       uintptr
)

// startWinEvents asks Windows to tell us the moment top-level windows appear, close,
// minimise or get cloaked, so running dots and closed apps update immediately.
func (a *App) startWinEvents() {
	if winEventCB == 0 {
		winEventCB = windows.NewCallback(winEventProc)
	}
	for _, r := range [][2]uintptr{{0x8000, 0x8003}, {0x0016, 0x0017}, {0x8017, 0x8018}} {
		pSetWinEventHook.Call(r[0], r[1], 0, winEventCB, 0, 0, 0x0002) // OUTOFCONTEXT | SKIPOWNPROCESS
	}
}

func winEventProc(hook, event, hwnd, idObject, idChild, thread, ts uintptr) uintptr {
	if int32(idObject) != 0 || int32(idChild) != 0 || hwnd == 0 || app == nil {
		return 0
	}
	if root, _, _ := pGetAncestor.Call(hwnd, 2); root != hwnd { // GA_ROOT: top-level windows only
		return 0
	}
	// coalesce bursts of events into one quick rescan
	pSetTimer.Call(app.hwnd, timerQuickScan, 40, 0)
	return 0
}

// refreshPop re-reads the flyout content when windows change underneath it.
func (a *App) refreshPop() {
	d := a.dock
	if d.PopItem == nil || d.PopTarget == 0 || d.Pop.Mode == PopGrid {
		return
	}
	np := a.popContent(d.PopItem)
	same := np.Title == d.Pop.Title && np.Subtitle == d.Pop.Subtitle && len(np.Rows) == len(d.Pop.Rows)
	if same {
		for i := range np.Rows {
			if np.Rows[i].Hwnd != d.Pop.Rows[i].Hwnd || np.Rows[i].Title != d.Pop.Rows[i].Title || np.Rows[i].Min != d.Pop.Rows[i].Min {
				same = false
				break
			}
		}
	}
	if !same {
		d.Pop = np
		a.needRender = true
	}
}
