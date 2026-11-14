//go:build windows

package main

// All settings in their own window: a normal, resizable app window with a title bar,
// in the taskbar and Alt-Tab. Quick settings stays the small panel by the dock.

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
)

const wmOpenSetWin = WM_APP + 47

var pLoadIconW = user32.NewProc("LoadIconW")

func (a *App) setWinVisible() bool {
	if a.swHwnd == 0 {
		return false
	}
	v, _, _ := pIsWindowVisible.Call(a.swHwnd)
	return v != 0
}

func (a *App) createSetWin() {
	if a.swHwnd != 0 {
		return
	}
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	ico, _, _ := pLoadIconW.Call(hinst, 2) // the app icon (icon group 2 in the exe)
	bg := [3]uint8{0x20, 0x20, 0x20}
	if c, ok := parseHex(a.uiPanel()); ok && (!a.uiDark() || a.uiTinted()) {
		bg = c
	}
	brush, _, _ := pCreateSolidBrush.Call(uintptr(bg[0]) | uintptr(bg[1])<<8 | uintptr(bg[2])<<16)
	wc := WNDCLASSEXW{LpfnWndProc: webWndProcCB, HInstance: hinst, HCursor: cur, HIcon: ico, HIconSm: ico, HbrBackground: brush, LpszClassName: u16("DGDockSettingsWindow")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	const wsOverlappedWindow = 0x00CF0000
	hwnd, _, _ := pCreateWindowExW.Call(WS_EX_APPWINDOW, up(u16("DGDockSettingsWindow")), up(u16("DG-Dock Settings")),
		wsOverlappedWindow, 0, 0, 980, 700, 0, 0, hinst, 0)
	if hwnd == 0 {
		return
	}
	a.swHwnd = hwnd
	webHosts[hwnd] = &webHost{hwnd: hwnd, binds: map[string]reflect.Value{}}
	dwmInt(hwnd, 20, boolI(a.uiDark()))
	// a blurred backdrop (as the panel has) so it can follow the dock's Colour strength
	if a.transparencyOn() && dwmInt(hwnd, 38, 3) { // DWMSBT_TRANSIENTWINDOW (acrylic)
		margins := [4]int32{-1, -1, -1, -1}
		pDwmExtendFrameIntoClientArea.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
		a.swAcrylic = true
	}
}

// placeSetWin sizes the window for the display under the pointer and centres it there.
func (a *App) placeSetWin() {
	cx, cy := getCursorPos()
	pt := uintptr(uint32(cx)) | uintptr(uint32(cy))<<32
	m, _, _ := pMonitorFromPoint.Call(pt, 2)
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := pGetMonitorInfoW.Call(m, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return
	}
	s := monitorDPI(m)
	if s <= 0 {
		s = a.dock.Scale
	}
	wa := mi.RcWork
	w := min(int32(math.Round(960*s)), wa.Right-wa.Left-int32(40*s))
	h := min(int32(math.Round(680*s)), wa.Bottom-wa.Top-int32(40*s))
	x, y := wa.Left+(wa.Right-wa.Left-w)/2, wa.Top+(wa.Bottom-wa.Top-h)/2
	pSetWindowPos.Call(a.swHwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0x0010|0x0004)
}

// openSettingsWindow shows All settings in its window, on page (or where it was).
func (a *App) openSettingsWindow(page string) {
	if a.panelVisible() {
		a.hidePanel(false)
	}
	a.swPage = page
	a.createSetWin()
	if a.swHwnd == 0 {
		return
	}
	if a.setWinVisible() {
		if ic, _, _ := pIsIconic.Call(a.swHwnd); ic != 0 {
			pShowWindow.Call(a.swHwnd, SW_RESTORE)
		}
		pSetForegroundWindow.Call(a.swHwnd)
		if a.swHost != nil {
			if page != "" {
				a.swHost.eval("window.mdGo && window.mdGo(" + strconv.Quote(page) + ")")
			} else {
				a.swHost.eval("window.mdRefresh && window.mdRefresh()")
			}
		}
		return
	}
	if !a.swPlaced {
		a.placeSetWin()
		a.swPlaced = true
	}
	dwmInt(a.swHwnd, 20, boolI(a.uiDark()))
	pShowWindow.Call(a.swHwnd, 5) // SW_SHOW
	pSetForegroundWindow.Call(a.swHwnd)
	if !a.ensureSetWinHost() {
		pShowWindow.Call(a.swHwnd, SW_HIDE)
		return
	}
	a.swHost.web.Show()
	a.swHost.web.Resize()
	a.swHost.eval("window.mdWinShown && window.mdWinShown(" + strconv.Quote(page) + ")")
	a.swHost.web.Focus()
}

func (a *App) hideSetWin() {
	if a.setWinVisible() {
		pShowWindow.Call(a.swHwnd, SW_HIDE)
	}
}

func (a *App) ensureSetWinHost() bool {
	if a.swHost != nil {
		return true
	}
	bg := edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
	if c, ok := parseHex(a.uiPanel()); ok && !a.uiDark() || ok && a.uiTinted() {
		bg = edge.COREWEBVIEW2_COLOR{A: 255, R: c[0], G: c[1], B: c[2]}
	}
	if a.swAcrylic {
		bg = edge.COREWEBVIEW2_COLOR{} // transparent: the page paints its own see-through background
	}
	hst, err := newWebHost(a.swHwnd, webDataDir(), bg)
	if err != nil {
		a.logf("settings window: %v", err)
		messageBox("Couldn't open Settings ("+err.Error()+"). Install the Microsoft Edge WebView2 Runtime from microsoft.com/edge/webview2.", appName)
		return false
	}
	a.swHost = hst
	hst.minW, hst.minH = 640, 460
	hst.onClose = func() { a.hideSetWin() } // kept, so it opens again instantly
	a.bindSettings(hst)
	hst.bind("mdEnv", func() panelEnv {
		acc, on := accentColors(a.uiDark())
		return panelEnv{Dark: a.uiDark(), Accent: acc, OnAccent: on, Windowed: true, Acrylic: a.swAcrylic}
	})
	hst.bind("mdTakePage", func() string { p := a.swPage; a.swPage = ""; return p })
	hst.bind("mdHide", func() bool { pPostMessageW.Call(a.swHwnd, 0x0010, 0, 0); return true })
	hst.load(strings.Replace(settingsHTML, "<script>", "<script>window.WINDOWED = true;</script><script>", 1))
	return true
}

// settingsHosts is every Settings page that's open: the panel and the window.
func (a *App) settingsHosts() []*webHost {
	var out []*webHost
	if a.settings != nil && a.panelVisible() {
		out = append(out, a.settings)
	}
	if a.swHost != nil && a.setWinVisible() {
		out = append(out, a.swHost)
	}
	return out
}

// openPendingInWindow opens the icon picker that was just asked for, in the window.
func (a *App) openPendingInWindow() {
	was := a.swHost != nil && a.setWinVisible()
	a.openSettingsWindow("")
	if was {
		a.swHost.eval("window.mdCheckPending && window.mdCheckPending()")
	}
	// a window that just opened checks by itself once its page has loaded
}
