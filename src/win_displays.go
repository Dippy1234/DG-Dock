//go:build windows

package main

// Docks on more than one display.
//
// The dock you start is the main dock: it owns Settings, Search, the system tray,
// updates and hiding the taskbar. When you choose "a dock on each display I pick",
// it starts one extra DG-Dock process per extra display ("--display \\.\DISPLAY2").
// Each extra dock shows the same pins, widgets and settings. It never saves
// settings itself: it reloads settings.json whenever the main dock saves it, and
// passes Settings and Search on to the main dock. Extra docks can't be edited
// directly (they behave as if locked), so the two can never fight over your pins.

import (
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// dockDisplay is this process's display when it's an extra dock ("" for the main dock).
var dockDisplay string

func isSecondary() bool { return dockDisplay != "" }

var (
	pEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	pGetDpiForMonitor    = shcoreDLL.NewProc("GetDpiForMonitor")
)

// messages an extra dock sends to the main dock
var (
	msgOpenSettings = uint32(registerMsg("DGDock.OpenSettings"))
	msgShowSearch   = uint32(registerMsg("DGDock.ShowSearch"))
)

type monitorInfoEx struct {
	MONITORINFO
	Device [32]uint16
}

type displayInfo struct {
	ID       string `json:"id"`  // \\.\DISPLAY2 (Windows can renumber these)
	Key      string `json:"key"` // stable: the monitor and the port it's plugged into
	internal bool   // the laptop's own screen
	Name     string `json:"name"`  // Display 2
	Model    string `json:"model"` // DELL U2723QE
	Main     bool   `json:"main"`
	Size     string `json:"size"` // 2560 × 1440
	handle   uintptr
	rect     RECT
	ordinal  int
}

var (
	pGetDisplayConfigBufferSizes = user32.NewProc("GetDisplayConfigBufferSizes")
	pQueryDisplayConfig          = user32.NewProc("QueryDisplayConfig")
	pDisplayConfigGetDeviceInfo  = user32.NewProc("DisplayConfigGetDeviceInfo")
)

// DISPLAYCONFIG_PATH_INFO (72 bytes)
type dcPath struct {
	SrcAdapter [2]uint32
	SrcID      uint32
	SrcModeIdx uint32
	SrcFlags   uint32
	TgtAdapter [2]uint32
	TgtID      uint32
	TgtModeIdx uint32
	OutputTech uint32
	Rotation   uint32
	Scaling    uint32
	RefreshN   uint32
	RefreshD   uint32
	ScanLine   uint32
	TgtAvail   int32
	TgtFlags   uint32
	Flags      uint32
}

type dcHeader struct {
	Type    uint32
	Size    uint32
	Adapter [2]uint32
	ID      uint32
}

type dcSourceName struct {
	dcHeader
	GDIName [32]uint16
}

type dcTargetName struct {
	dcHeader
	Flags      uint32
	OutputTech uint32
	EdidMfg    uint16
	EdidProd   uint16
	Connector  uint32
	Friendly   [64]uint16
	Path       [128]uint16
}

type displayOrder struct {
	n          int
	model, key string
	internal   bool
}

// windowsDisplayOrder reads the active displays from the Windows display configuration
// (what Settings › Display uses), in its order: \\.\DISPLAYn → (position, monitor name).
// The \\.\DISPLAYn names themselves don't follow Settings' numbers, so they aren't used.
func windowsDisplayOrder() map[string]displayOrder {
	out := map[string]displayOrder{}
	var np, nm uint32
	if r, _, _ := pGetDisplayConfigBufferSizes.Call(2, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&nm))); r != 0 || np == 0 {
		return out // QDC_ONLY_ACTIVE_PATHS
	}
	paths := make([]dcPath, np)
	modes := make([]byte, int(nm)*64+64)
	if r, _, _ := pQueryDisplayConfig.Call(2, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nm)), uintptr(unsafe.Pointer(&modes[0])), 0); r != 0 {
		return out
	}
	n := 0
	for _, p := range paths[:np] {
		var src dcSourceName
		src.Type, src.Size, src.Adapter, src.ID = 1, uint32(unsafe.Sizeof(src)), p.SrcAdapter, p.SrcID
		if r, _, _ := pDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&src))); r != 0 {
			continue
		}
		dev := strings.ToUpper(syscall.UTF16ToString(src.GDIName[:]))
		if _, dup := out[dev]; dup {
			continue // a duplicated (mirrored) display counts once
		}
		var tgt dcTargetName
		tgt.Type, tgt.Size, tgt.Adapter, tgt.ID = 2, uint32(unsafe.Sizeof(tgt)), p.TgtAdapter, p.TgtID
		model, key, internal := "", "", false
		if r, _, _ := pDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&tgt))); r == 0 {
			model = syscall.UTF16ToString(tgt.Friendly[:])
			key = strings.ToLower(syscall.UTF16ToString(tgt.Path[:]))
			switch tgt.OutputTech {
			case 0x80000000, 6, 11, 13: // internal, LVDS, embedded DisplayPort/UDI
				internal = true
				if model == "" {
					model = "Built-in display"
				}
			}
		}
		n++
		out[dev] = displayOrder{n, model, key, internal}
	}
	return out
}

// listDisplays returns the connected displays, main display first.
func listDisplays() []displayInfo {
	var out []displayInfo
	cb := syscall.NewCallback(func(h, hdc, rc, lp uintptr) uintptr {
		var mi monitorInfoEx
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
			return 1
		}
		dev := strings.ToUpper(syscall.UTF16ToString(mi.Device[:]))
		d := displayInfo{ID: dev, handle: h, rect: mi.RcMonitor, Main: mi.DwFlags&1 != 0,
			Size: fmt.Sprintf("%d × %d", mi.RcMonitor.Right-mi.RcMonitor.Left, mi.RcMonitor.Bottom-mi.RcMonitor.Top)}
		out = append(out, d)
		return 1
	})
	pEnumDisplayMonitors.Call(0, 0, cb, 0)
	// number them the way Windows does (Settings › Display, Identify)
	order := windowsDisplayOrder()
	next := len(order) + 1
	for i := range out {
		if o, ok := order[out[i].ID]; ok {
			out[i].ordinal, out[i].Model, out[i].Key, out[i].internal = o.n, o.model, o.key, o.internal
		} else {
			out[i].ordinal = next // not in the configuration (rare): after the others
			next++
		}
		out[i].Name = "Display " + strconv.Itoa(out[i].ordinal)
		if out[i].Key == "" {
			out[i].Key = strings.ToLower(out[i].ID)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ordinal < out[j-1].ordinal; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func monitorByDevice(dev string) uintptr {
	for _, d := range listDisplays() {
		if strings.EqualFold(d.ID, dev) {
			return d.handle
		}
	}
	return 0
}

// multiDock reports whether "a dock on each display I pick" is on.
func (a *App) multiDock() bool { return len(a.cfg.DockDisplays) > 0 }

// fixedDisplay is the display this dock must stay on ("" = the usual behaviour:
// follow the pointer, or the main display).
func (a *App) fixedDisplay() string {
	if isSecondary() {
		return dockDisplay
	}
	if !a.multiDock() {
		return ""
	}
	ds := listDisplays()
	for _, d := range ds { // the main dock goes on the main display when it's chosen…
		if d.Main && a.dockOnDisplay(d) {
			return d.ID
		}
	}
	for _, d := range ds { // …otherwise on the first chosen display that's connected
		if a.dockOnDisplay(d) {
			return d.ID
		}
	}
	return ""
}

// dockOnDisplay: the display is one of those chosen. Displays are remembered by
// monitor (Key), because Windows renumbers \\.\DISPLAYn after sleep, docking or a
// driver update; older settings that still hold a \\.\DISPLAYn name also match.
func (a *App) dockOnDisplay(d displayInfo) bool {
	for _, x := range a.cfg.DockDisplays {
		if strings.EqualFold(x, d.Key) || strings.EqualFold(x, d.ID) {
			return true
		}
	}
	return false
}

// displayByID finds a connected display by its \\.\DISPLAYn name.
func displayByID(id string) (displayInfo, bool) {
	for _, d := range listDisplays() {
		if strings.EqualFold(d.ID, id) {
			return d, true
		}
	}
	return displayInfo{}, false
}

// upgradeDisplayKeys swaps old \\.\DISPLAYn names in the settings for monitor keys.
func (a *App) upgradeDisplayKeys() bool {
	changed := false
	for i, x := range a.cfg.DockDisplays {
		if !strings.HasPrefix(x, `\\.\`) {
			continue
		}
		if d, ok := displayByID(x); ok && d.Key != "" && !strings.EqualFold(d.Key, x) {
			a.cfg.DockDisplays[i] = d.Key
			changed = true
		}
	}
	return changed
}

// monitorDPI is the scale of the display the dock is on (so a dock moving to a
// display with different scaling is laid out at the right size straight away).
func monitorDPI(h uintptr) float64 {
	if h == 0 || pGetDpiForMonitor.Find() != nil {
		return 0
	}
	var x, y uint32
	if r, _, _ := pGetDpiForMonitor.Call(h, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); r != 0 || x < 72 {
		return 0
	}
	return float64(x) / 96
}

// ---------- main dock: starting and stopping the extra docks ----------

type extraDock struct {
	proc *os.Process
	done chan struct{}
}

var (
	extrasMu sync.Mutex
	extras   = map[string]*extraDock{} // display id → its dock
)

func extraTitle(dev string) string { return appName + " " + strings.TrimPrefix(dev, `\\.\`) }

// syncDisplayDocks starts a dock for each chosen display that doesn't have one and
// closes the ones no longer wanted (or whose display was unplugged).
func (a *App) syncDisplayDocks() {
	if isSecondary() {
		return
	}
	if a.upgradeDisplayKeys() {
		a.saveConfig()
	}
	want := map[string]bool{}
	if a.multiDock() {
		mainOn := a.fixedDisplay()
		for _, d := range listDisplays() {
			if a.dockOnDisplay(d) && !strings.EqualFold(d.ID, mainOn) {
				want[strings.ToUpper(d.ID)] = true
			}
		}
	}
	extrasMu.Lock()
	defer extrasMu.Unlock()
	for id, x := range extras {
		select {
		case <-x.done: // exited on its own
			delete(extras, id)
			continue
		default:
		}
		if !want[id] {
			stopExtraDock(id, x)
			delete(extras, id)
		}
	}
	for id := range want {
		if extras[id] != nil {
			continue
		}
		args := []string{"--display", id}
		if !isInstalledCopy() {
			args = append([]string{"--portable"}, args...)
		}
		cmd := exec.Command(exePath(), args...)
		if err := cmd.Start(); err != nil {
			a.logf("couldn't start the dock for %s: %v", id, err)
			continue
		}
		x := &extraDock{proc: cmd.Process, done: make(chan struct{})}
		go func() { cmd.Wait(); close(x.done) }()
		extras[id] = x
		a.logf("started a dock on %s", id)
	}
}

// stopExtraDock asks an extra dock to close, and ends it if it doesn't.
func stopExtraDock(id string, x *extraDock) {
	if h, _, _ := pFindWindowW.Call(up(u16("DGDockWindow")), up(u16(extraTitle(id)))); h != 0 {
		pPostMessageW.Call(h, WM_CLOSE, 0, 0)
	}
	go func() {
		select {
		case <-x.done:
		case <-time.After(2 * time.Second):
			x.proc.Kill()
		}
	}()
}

// stopAllDisplayDocks closes every extra dock (the main dock is quitting).
func stopAllDisplayDocks() {
	extrasMu.Lock()
	defer extrasMu.Unlock()
	for id, x := range extras {
		stopExtraDock(id, x)
		delete(extras, id)
	}
}

// ---------- extra dock: following the main dock ----------

func mainDockWindow() uintptr {
	h, _, _ := pFindWindowW.Call(up(u16("DGDockWindow")), up(u16(appName)))
	return h
}

// forwardToMainAt passes Settings or Search on to the main dock, with two screen
// points (packed into wParam and lParam) so it can show them on this display.
func forwardToMainAt(msg uint32, x1, y1, x2, y2 int32) {
	if h := mainDockWindow(); h != 0 {
		// this dock got the click, so Windows lets it pass on the right to bring a
		// window to the front: without it Settings/Search can't take focus, and then
		// never close when you click elsewhere
		var pid uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		pAllowSetForegroundWindow.Call(uintptr(pid))
		pPostMessageW.Call(h, uintptr(msg), packPoint(x1, y1), packPoint(x2, y2))
	}
}

var pGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")

func (a *App) anyButtonHeld() bool {
	for _, vk := range []uintptr{0x01, 0x02, 0x04} {
		if r, _, _ := pGetAsyncKeyState.Call(vk); r&0x8000 != 0 {
			return true
		}
	}
	return false
}

// checkPanelOutsideClick closes Settings when you click outside it while it doesn't
// have focus (Windows can refuse it focus, and then it never hears that you clicked
// elsewhere). Runs on the dock's animation tick.
func (a *App) checkPanelOutsideClick() {
	if a.panelHwnd == 0 || !a.panelVisible() || a.panelAnimating || a.dialogOpen {
		a.panelBtnWasDown = false
		return
	}
	down := false
	for _, vk := range []uintptr{0x01, 0x02, 0x04} { // left, right, middle button
		// held now, or pressed and let go since the last check (a quick click)
		if r, _, _ := pGetAsyncKeyState.Call(vk); r&0x8001 != 0 {
			down = true
		}
	}
	pressed := down && !a.panelBtnWasDown
	a.panelBtnWasDown = down && a.anyButtonHeld()
	if !pressed || time.Since(a.panelShownAt) < 300*time.Millisecond {
		return // the click that opened it is still held
	}
	if fg, _, _ := pGetForegroundWindow.Call(); fg == a.panelHwnd {
		return // it has focus: it closes itself when it loses it
	}
	var r RECT
	pGetWindowRect.Call(a.panelHwnd, uintptr(unsafe.Pointer(&r)))
	cx, cy := getCursorPos()
	if cx < r.Left || cx >= r.Right || cy < r.Top || cy >= r.Bottom {
		a.hidePanel(true)
	}
}

func packPoint(x, y int32) uintptr         { return uintptr(uint32(x)) | uintptr(uint32(y))<<32 }
func unpackPoint(v uintptr) (int32, int32) { return int32(uint32(v)), int32(uint32(v >> 32)) }

// secondaryOverrides keeps an extra dock to its role: it never hosts the tray or
// hides the taskbar (the main dock does both) and can't be edited directly.
func (a *App) secondaryOverrides() {
	a.cfg.NoTray = true
	a.cfg.LockDock = true
	// HideTaskbar stays as set (it decides where the dock sits); only the main dock
	// actually hides or restores the taskbar (applyTaskbar / restoreTaskbar)
}

const timerFollow = 25

// msgConfigChanged: the main dock saved settings; extra docks reload them now
// rather than on their next check.
var msgConfigChanged = uint32(registerMsg("DGDock.ConfigChanged"))

// msgRefreshDock: the main dock was refreshed; extra docks re-read their icons too.
var msgRefreshDock = uint32(registerMsg("DGDock.RefreshDock"))

func notifyExtraDocks(msgs ...uint32) {
	msg := msgConfigChanged
	if len(msgs) > 0 {
		msg = msgs[0]
	}
	extrasMu.Lock()
	ids := make([]string, 0, len(extras))
	for id := range extras {
		ids = append(ids, id)
	}
	extrasMu.Unlock()
	for _, id := range ids {
		if h, _, _ := pFindWindowW.Call(up(u16("DGDockWindow")), up(u16(extraTitle(id)))); h != 0 {
			pPostMessageW.Call(h, uintptr(msg), 0, 0)
		}
	}
}

// followMain runs on the extra dock's scan timer (and straight after the main dock
// saves): it reloads settings after the main
// dock saves them, and closes when the main dock has gone or its display was removed.
func (a *App) followMain() {
	if mainDockWindow() == 0 {
		a.mainMissing++
		if a.mainMissing >= 4 { // ~3 s: the main dock quit or crashed
			a.quit()
		}
		return
	}
	a.mainMissing = 0
	if info, err := os.Stat(a.cfgPath); err == nil && !info.ModTime().Equal(a.cfgSeen) {
		a.cfgSeen = info.ModTime()
		oldPack := a.cfg.IconPack
		look := func() string {
			c := a.cfg
			return fmt.Sprintf("%s|%s|%d|%d|%d|%d|%s", c.IconStyle, c.IconPack, c.IconPackScale, c.AppIconScale, c.WidgetIconScale, len(c.IconOverrides), c.Theme)
		}
		oldLook := look()
		a.loadConfig()
		a.secondaryOverrides()
		if d, ok := displayByID(dockDisplay); !ok || !a.dockOnDisplay(d) {
			a.quit()
			return
		}
		a.updateTheme()
		if a.cfg.IconPack != oldPack { // reading an icon pack is slow: only when it changed
			a.loadPack()
		}
		AnimSlow = AnimSpeedFactor(animSpeedOf(a.cfg.AnimSpeed))
		a.dock.SetAnim(a.cfg)
		if look() != oldLook { // icon style, pack or sizes changed: redraw every icon
			a.relayout()
			a.reloadItems()
		} else {
			a.rebuildItems()
		}
		a.relayout()
		a.render()
	}
	if monitorByDevice(dockDisplay) == 0 {
		a.quit() // display unplugged; the main dock starts this again when it's back
	}
}

// ---------- Identify: show each display's number on it, like Settings › Display ----------

const timerIdentify = 21

var identifyWins []uintptr

func (a *App) identifyDisplays() {
	a.closeIdentify()
	hinst, _, _ := pGetModuleHandleW.Call(0)
	wc := WNDCLASSEXW{LpfnWndProc: pDefWindowProcW.Addr(), HInstance: hinst, LpszClassName: u16("DGDockIdentify")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	for _, d := range listDisplays() {
		s := monitorDPI(d.handle)
		if s <= 0 {
			s = 1
		}
		W, H := int(math.Round(150*s)), int(math.Round(150*s))
		x, y := d.rect.Left+int32(40*s), d.rect.Bottom-int32(40*s)-int32(H)
		hwnd, _, _ := pCreateWindowExW.Call(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST|WS_EX_NOACTIVATE|0x20, // WS_EX_TRANSPARENT
			up(u16("DGDockIdentify")), 0, WS_POPUP, uintptr(x), uintptr(y), uintptr(W), uintptr(H), 0, 0, hinst, 0)
		if hwnd == 0 {
			continue
		}
		var bi BITMAPINFO
		bi.Header.BiSize = uint32(unsafe.Sizeof(bi.Header))
		bi.Header.BiWidth, bi.Header.BiHeight = int32(W), -int32(H)
		bi.Header.BiPlanes, bi.Header.BiBitCount = 1, 32
		dc, _, _ := pCreateCompatibleDC.Call(0)
		var bits uintptr
		dib, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
		if dib == 0 || bits == 0 {
			pDeleteDC.Call(dc)
			pDestroyWindow(hwnd)
			continue
		}
		old, _, _ := pSelectObject.Call(dc, dib)
		img := &image.RGBA{Pix: unsafe.Slice((*byte)(unsafe.Pointer(bits)), W*H*4), Stride: W * 4, Rect: image.Rect(0, 0, W, H)}
		fillRoundRect(img, 0, 0, float64(W), float64(H), 18*s, image.NewUniform(pc(24, 24, 28, 0.92)))
		strokeRoundRect(img, 0, 0, float64(W), float64(H), 18*s, 1.5*s, pc(255, 255, 255, 0.18))
		num := strconv.Itoa(d.ordinal)
		px := int(math.Round(96 * s))
		te := a.rd.Bold
		if te == nil {
			te = a.te
		}
		tw := te.Measure(num, px)
		te.Draw(img, num, px, (float64(W)-tw)/2, float64(H)/2+float64(px)*0.36, pc(255, 255, 255, 1))
		pt, src := POINT{x, y}, POINT{}
		sz := SIZE{int32(W), int32(H)}
		blend := BLENDFUNCTION{SourceConstantAlpha: 255, AlphaFormat: 1}
		pUpdateLayeredWindow.Call(hwnd, 0, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)), dc,
			uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
		pSelectObject.Call(dc, old)
		pDeleteObject.Call(dib)
		pDeleteDC.Call(dc)
		pShowWindow.Call(hwnd, SW_SHOWNA)
		identifyWins = append(identifyWins, hwnd)
	}
	pSetTimer.Call(a.hwnd, timerIdentify, 3000, 0)
}

func (a *App) closeIdentify() {
	pKillTimer(a.hwnd, timerIdentify)
	for _, h := range identifyWins {
		pDestroyWindow(h)
	}
	identifyWins = nil
}
