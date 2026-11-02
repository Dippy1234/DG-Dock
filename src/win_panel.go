//go:build windows

package main

// Dock settings as a compact Windows 11 Quick Settings-style panel (WebView2 page in a
// borderless, rounded, acrylic window that slides out of the dock).

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows/registry"
)

const (
	wmPanelHide   = WM_APP + 6
	wmPanelResize = WM_APP + 7
	panelWidth    = 360 // CSS px
)

var (
	pDwmFlush                     = dwmapi.NewProc("DwmFlush")
	pDwmExtendFrameIntoClientArea = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
)

type panelEnv struct {
	Dark     bool   `json:"dark"`
	Accent   string `json:"accent"`
	OnAccent string `json:"onAccent"`
	Acrylic  bool   `json:"acrylic"`
}

// accentColors returns the Windows 11 accent shade used by Quick Settings and the text colour on it.
func accentColors(dark bool) (string, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\Accent`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if b, _, err := k.GetBinaryValue("AccentPalette"); err == nil && len(b) >= 32 {
			i := 4 // AccentDark1 in light mode
			if dark {
				i = 1 // AccentLight2 in dark mode
			}
			r, g, bl := b[i*4], b[i*4+1], b[i*4+2]
			on := "#000000"
			if 0.299*float64(r)+0.587*float64(g)+0.114*float64(bl) < 150 {
				on = "#ffffff"
			}
			return fmt.Sprintf("#%02x%02x%02x", r, g, bl), on
		}
	}
	if dark {
		return "#60cdff", "#000000"
	}
	return "#005fb8", "#ffffff"
}

func dwmInt(hwnd uintptr, attr uint32, v int32) bool {
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&v)), 4)
	return r == 0
}

func (a *App) panelVisible() bool {
	if a.panelHwnd == 0 {
		return false
	}
	v, _, _ := pIsWindowVisible.Call(a.panelHwnd)
	return v != 0
}

func (a *App) refreshSettings() {
	if a.settings != nil && a.panelVisible() {
		a.settings.eval("window.mdRefresh && window.mdRefresh()")
	}
}

func (a *App) applySettingsTheme() {
	if a.panelHwnd != 0 {
		dwmInt(a.panelHwnd, 20, boolI(a.dock.Dark))
	}
	a.refreshSettings()
}

func boolI(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func (a *App) createPanelWindow() {
	if a.panelHwnd != 0 {
		return
	}
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	wc := WNDCLASSEXW{LpfnWndProc: webWndProcCB, HInstance: hinst, HCursor: cur, LpszClassName: u16("DGDockPanel")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_TOPMOST, up(u16("DGDockPanel")), up(u16("DG-Dock settings")),
		WS_POPUP|0x00040000|0x00C00000, 0, 0, 360, 400, 0, 0, hinst, 0) // WS_THICKFRAME|WS_CAPTION for the DWM frame
	if hwnd == 0 {
		return
	}
	a.panelHwnd = hwnd
	webHosts[hwnd] = &webHost{hwnd: hwnd, binds: map[string]reflect.Value{}, borderless: true}
	pSetWindowPos.Call(hwnd, 0, 0, 0, 360, 400, 0x0020|0x0002|0x0001|0x0010|0x0004) // FRAMECHANGED
	dwmInt(hwnd, 20, boolI(a.dock.Dark))
	dwmInt(hwnd, 33, 2) // rounded corners
	margins := [4]int32{-1, -1, -1, -1}
	pDwmExtendFrameIntoClientArea.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
	a.panelAcrylic = a.transparencyOn() && dwmInt(hwnd, 38, 3) // DWMSBT_TRANSIENTWINDOW (acrylic)
}

type panelGeom struct {
	x, y, w, h int32
	dx, dy     int32
}

func (a *App) panelGeometry() panelGeom {
	d := a.dock
	s := d.Scale
	sc := func(v float64) int32 { return int32(math.Round(v * s)) }
	h := a.panelH
	if h <= 0 {
		h = 330
	}
	g := panelGeom{w: sc(panelWidth), h: sc(float64(h))}
	gap, slide := 12*s, sc(30)
	ax, ay := a.panelAnchor[0], a.panelAnchor[1]
	mu, _ := d.toUV(float64(ax-a.winX), float64(ay-a.winY))
	top := d.PlateV1
	if d.HideOffset > 0 {
		top = d.PlateH + d.M // place it as if the dock were shown
	}
	fx, fy := d.toXY(mu, top+gap)
	px, py := a.winX+int32(fx), a.winY+int32(fy)
	switch d.Orient {
	case OrientLeft:
		g.x, g.y, g.dx = px, ay-g.h/2, -slide
	case OrientRight:
		g.x, g.y, g.dx = px-g.w, ay-g.h/2, slide
	default:
		g.x, g.y, g.dy = ax-g.w/2, py-g.h, slide
	}
	m := sc(8)
	if g.x < a.mon.Left+m {
		g.x = a.mon.Left + m
	}
	if g.x+g.w > a.mon.Right-m {
		g.x = a.mon.Right - m - g.w
	}
	if g.y < a.mon.Top+m {
		g.y = a.mon.Top + m
	}
	if g.y+g.h > a.mon.Bottom-m {
		g.y = a.mon.Bottom - m - g.h
	}
	return g
}

func (a *App) animatePanel(from, to RECT, dur time.Duration, ease func(float64) float64) {
	a.panelAnimating = true
	defer func() { a.panelAnimating = false }()
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
		pSetWindowPos.Call(a.panelHwnd, 0, uintptr(l), uintptr(tp), uintptr(r-l), uintptr(b-tp), 0x0010|0x0200|0x0004)
		if t >= 1 {
			return
		}
		if r, _, _ := pDwmFlush.Call(); r != 0 {
			time.Sleep(4 * time.Millisecond)
		}
	}
}

func easeDecel(t float64) float64 { return 1 - math.Pow(1-t, 4) }
func easeAccel(t float64) float64 { return t * t * t }

func geomRect(g panelGeom, offset bool) RECT {
	x, y := g.x, g.y
	if offset {
		x, y = x+g.dx, y+g.dy
	}
	return RECT{x, y, x + g.w, y + g.h}
}

// ensurePanelHost creates the WebView2 page inside the (visible) panel window.
func (a *App) ensurePanelHost() bool {
	if a.settings != nil {
		return true
	}
	hwnd := a.panelHwnd
	bgc := edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
	if !a.dock.Dark {
		bgc = edge.COREWEBVIEW2_COLOR{A: 255, R: 0xf3, G: 0xf3, B: 0xf3}
	}
	if a.panelAcrylic {
		bgc = edge.COREWEBVIEW2_COLOR{}
	}
	hst, err := newWebHost(hwnd, webDataDir(), bgc)
	if err != nil {
		a.logf("panel: %v", err)
		messageBox("Couldn't open Dock settings ("+err.Error()+"). Install the Microsoft Edge WebView2 Runtime from microsoft.com/edge/webview2, or use the dock's right-click menu.", appName)
		return false
	}
	hst.borderless = true
	a.settings = hst
	hst.onActivate = func(active bool) {
		if !active && a.panelVisible() && !a.panelAnimating && !a.dialogOpen {
			pPostMessageW.Call(a.hwnd, wmPanelHide, 0, 0)
		}
	}
	hst.onClose = func() { a.hidePanel(true) }
	a.bindSettings(hst)
	hst.bind("mdEnv", func() panelEnv {
		acc, on := accentColors(a.dock.Dark)
		return panelEnv{Dark: a.dock.Dark, Accent: acc, OnAccent: on, Acrylic: a.panelAcrylic}
	})
	hst.bind("mdPanelSize", func(h int) bool {
		if h > 100 && h < 1400 && h != a.panelH {
			a.panelH = h
			if a.panelVisible() {
				pPostMessageW.Call(a.hwnd, wmPanelResize, 0, 0)
			}
		}
		return true
	})
	hst.bind("mdHide", func() bool { pPostMessageW.Call(a.hwnd, wmPanelHide, 0, 0); return true })
	hst.load(settingsHTML)
	return true
}

// openSettings shows the panel next to the dock (or hides it if it's already open).
func (a *App) openSettings() {
	a.createPanelWindow()
	if a.panelHwnd == 0 {
		return
	}
	if a.panelVisible() {
		pSetForegroundWindow.Call(a.panelHwnd)
		a.refreshSettings()
		return
	}
	if time.Since(a.panelHiddenAt) < 250*time.Millisecond {
		return
	}
	cx, cy := getCursorPos()
	a.panelAnchor = [2]int32{cx, cy}
	dwmInt(a.panelHwnd, 20, boolI(a.dock.Dark))
	g := a.panelGeometry()
	from := geomRect(g, true)
	pSetWindowPos.Call(a.panelHwnd, ^uintptr(0), uintptr(from.Left), uintptr(from.Top), uintptr(g.w), uintptr(g.h), 0x0010)
	first := a.settings == nil
	pShowWindow.Call(a.panelHwnd, 5)
	pSetForegroundWindow.Call(a.panelHwnd)
	if !a.ensurePanelHost() {
		pShowWindow.Call(a.panelHwnd, SW_HIDE)
		return
	}
	a.settings.web.Show()
	a.settings.web.Resize()
	if !first {
		a.settings.eval("window.mdShown && window.mdShown()")
	}
	a.animatePanel(from, geomRect(g, false), time.Duration(float64(260*time.Millisecond)*AnimSlow), easeDecel)
	a.settings.web.Focus()
}

func (a *App) hidePanel(animated bool) {
	if !a.panelVisible() {
		return
	}
	if animated {
		var cur RECT
		pGetWindowRect.Call(a.panelHwnd, uintptr(unsafe.Pointer(&cur)))
		g := a.panelGeometry()
		to := RECT{cur.Left + g.dx/2, cur.Top + g.dy/2, cur.Right + g.dx/2, cur.Bottom + g.dy/2}
		a.animatePanel(cur, to, time.Duration(float64(110*time.Millisecond)*AnimSlow), easeAccel)
	}
	if a.settings != nil && a.settings.web != nil {
		a.settings.web.Hide()
	}
	pShowWindow.Call(a.panelHwnd, SW_HIDE)
	a.panelHiddenAt = time.Now()
}

func (a *App) resizePanel() {
	if !a.panelVisible() || a.panelAnimating {
		return
	}
	// snap to the new size in one step: resizing a WebView2 every frame is what makes panels stutter
	r := geomRect(a.panelGeometry(), false)
	pSetWindowPos.Call(a.panelHwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x0010|0x0200|0x0004)
	if a.settings != nil {
		a.settings.web.Resize()
	}
}

// openIconPicker opens the panel on the icon picker for pin idx.
func (a *App) openIconPicker(idx int) {
	a.pendingPicker = idx
	a.openSettings()
	if a.settings != nil {
		a.settings.eval("window.mdCheckPending && window.mdCheckPending()")
	}
}

var _ = json.Marshal
