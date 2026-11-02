//go:build windows

package main

// Intellihide: with auto-hide on, the dock only gets out of the way when a window
// actually overlaps it. On an empty desktop (or beside a small window) it stays put.

import (
	"time"
	"unsafe"
)

var smartSkip = map[string]bool{
	"Progman": true, "WorkerW": true, "Shell_TrayWnd": true, "Shell_SecondaryTrayWnd": true,
	"NotifyIconOverflowWindow": true, "Windows.UI.Core.CoreWindow": true, "XamlExplorerHostIslandWindow": true,
	"TopLevelWindowForOverflowXamlIsland": true, "ForegroundStaging": true, "MultitaskingViewFrame": true,
}

// dockScreenRect is where the dock sits when it's shown, in screen pixels.
func (a *App) dockScreenRect() RECT {
	d := a.dock
	top := d.PlateH + d.M
	x0, y0 := d.toXY(d.PlateU0, 0)
	x1, y1 := d.toXY(d.PlateU1, top)
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return RECT{a.winX + int32(x0), a.winY + int32(y0), a.winX + int32(x1), a.winY + int32(y1)}
}

// windowOverDock reports whether any normal window overlaps the dock (checked at most
// every 150 ms; visible frame bounds, so the invisible resize borders don't count).
func (a *App) windowOverDock() bool {
	if !a.overAt.IsZero() && time.Since(a.overAt) < 150*time.Millisecond {
		return a.overDock
	}
	a.overAt = time.Now()
	dr := a.dockScreenRect()
	over := false
	enumResults = enumResults[:0]
	pEnumWindows.Call(enumCB, 0)
	for _, h := range enumResults {
		if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
			continue
		}
		if ic, _, _ := pIsIconic.Call(h); ic != 0 {
			continue
		}
		if isCloaked(h) {
			continue
		}
		var pid uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == ownPID {
			continue
		}
		ex, _, _ := pGetWindowLongPtrW.Call(h, GWL_EXSTYLE)
		if ex&WS_EX_TOOLWINDOW != 0 && ex&WS_EX_APPWINDOW == 0 {
			continue
		}
		if ex&0x20 != 0 { // WS_EX_TRANSPARENT: click-through overlays
			continue
		}
		cls := className(h)
		if smartSkip[cls] || skipClasses[cls] {
			continue
		}
		var r RECT
		if hr, _, _ := pDwmGetWindowAttribute.Call(h, 9, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)); hr != 0 {
			pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
		}
		if r.Right-r.Left < 40 || r.Bottom-r.Top < 40 {
			continue
		}
		if r.Left < dr.Right && r.Right > dr.Left && r.Top < dr.Bottom && r.Bottom > dr.Top {
			over = true
			break
		}
	}
	a.overDock = over
	return over
}
