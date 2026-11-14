//go:build windows

package main

// Opening apps on the dock's own display.
//
// Windows opens a new window on the main display (or wherever the app last saved
// it), not where you clicked. So when something is opened from a dock, the dock
// watches for the new window for a few seconds and moves it to the dock's display,
// keeping it maximised if it was.

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	placeTarget   uintptr // the display new windows should go to
	placeUntil    time.Time
	placeBefore   map[uintptr]bool // windows that were already open
	placeQueue    []uintptr
	placeHooked   bool
	placeTimerCB  uintptr
	placeTimerID  uintptr
	placeMovedAny bool
)

// placeNextWindows arms the watcher before something is opened.
func placeNextWindows() {
	a := app
	if a == nil || a.hwnd == 0 || len(listDisplays()) < 2 {
		return
	}
	placeTarget = 0
	// the display you're working on: where Search is open, or else where the pointer is
	// (on the dock you just clicked)
	if a.lp.hwnd != 0 {
		if v, _, _ := pIsWindowVisible.Call(a.lp.hwnd); v != 0 {
			placeTarget, _, _ = pMonitorFromWindow.Call(a.lp.hwnd, 2)
		}
	}
	if placeTarget == 0 || time.Now().After(placeUntil) {
		cx, cy := getCursorPos()
		placeTarget, _, _ = pMonitorFromPoint.Call(uintptr(uint32(cx))|uintptr(uint32(cy))<<32, 2)
	}
	placeUntil = time.Now().Add(10 * time.Second)
	placeMovedAny = false
	placeBefore = map[uintptr]bool{}
	cb := syscall.NewCallback(func(h, lp uintptr) uintptr {
		if v, _, _ := pIsWindowVisible.Call(h); v != 0 && !isCloaked(h) {
			placeBefore[h] = true
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	if !placeHooked {
		placeHooked = true
		ev := syscall.NewCallback(func(hook, event, hwnd uintptr, idObject, idChild int32, thread, t uint32) uintptr {
			if idObject == 0 && hwnd != 0 && placeTarget != 0 {
				placeSoon(hwnd)
			}
			return 0
		})
		pSetWinEventHook.Call(0x0003, 0x0003, 0, ev, 0, 0, 0) // a window came to the front
		pSetWinEventHook.Call(0x8002, 0x8002, 0, ev, 0, 0, 0) // a window was shown
		pSetWinEventHook.Call(0x8018, 0x8018, 0, ev, 0, 0, 0) // a window was uncloaked (Store apps)
		placeTimerCB = syscall.NewCallback(func(hwnd, msg, id, t uintptr) uintptr {
			placeRun()
			return 0
		})
	}
}

// placeSoon moves a new window the moment it appears (so it doesn't flash up on the
// wrong display first), then checks it again a moment later in case the app moved it
// back to where it saved it.
func placeSoon(h uintptr) {
	if time.Now().After(placeUntil) {
		placeTarget = 0
		placeBefore = nil
		return
	}
	if placeBefore[h] {
		return
	}
	for _, q := range placeQueue {
		if q == h {
			return
		}
	}
	if isMovableAppWindow(h) && moveToMonitor(h, placeTarget) {
		placeMoved()
	}
	placeQueue = append(placeQueue, h)
	if placeTimerID == 0 {
		placeTimerID, _, _ = pSetTimer.Call(0, 0, 250, placeTimerCB)
	}
}

// placeMoved: once the first window is placed, an app's main window often follows
// its splash screen: allow a little longer, not the full ten seconds.
func placeMoved() {
	if placeMovedAny {
		return
	}
	placeMovedAny = true
	if until := time.Now().Add(3 * time.Second); until.Before(placeUntil) {
		placeUntil = until
	}
}

func placeRun() {
	if placeTimerID != 0 {
		pKillTimer(0, placeTimerID)
		placeTimerID = 0
	}
	q := placeQueue
	placeQueue = nil
	for _, h := range q {
		if placeTarget == 0 || placeBefore == nil || placeBefore[h] {
			continue
		}
		if !isMovableAppWindow(h) {
			continue
		}
		placeBefore[h] = true // checked once more; after that it's the user's
		if moveToMonitor(h, placeTarget) {
			placeMoved()
		}
	}
}

// isMovableAppWindow: an ordinary, visible app window (not a menu, tooltip, the
// taskbar or one of the dock's own windows).
func isMovableAppWindow(h uintptr) bool {
	if ok, _, _ := pIsWindow.Call(h); ok == 0 {
		return false
	}
	if root, _, _ := pGetAncestor.Call(h, 2); root != h { // GA_ROOT
		return false
	}
	if v, _, _ := pIsWindowVisible.Call(h); v == 0 || isCloaked(h) {
		return false
	}
	if ic, _, _ := pIsIconic.Call(h); ic != 0 {
		return false
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	if pid == uint32(syscall.Getpid()) {
		return false
	}
	style, _, _ := pGetWindowLongPtrW.Call(h, ^uintptr(15))                   // GWL_STYLE
	ex, _, _ := pGetWindowLongPtrW.Call(h, ^uintptr(19))                      // GWL_EXSTYLE
	if ex&(WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE) != 0 || style&0x40000000 != 0 { // WS_CHILD
		return false
	}
	if style&0x00C00000 != 0x00C00000 && ex&0x00040000 == 0 { // needs a title bar, or WS_EX_APPWINDOW
		return false
	}
	var cls [64]uint16
	pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&cls[0])), 64)
	switch syscall.UTF16ToString(cls[:]) {
	case "Shell_TrayWnd", "Shell_SecondaryTrayWnd", "Progman", "WorkerW", "Windows.UI.Core.CoreWindow", "#32768", "#32770":
		return false
	}
	return true
}

type windowPlacement struct {
	length           uint32
	flags            uint32
	showCmd          uint32
	ptMinPosition    POINT
	ptMaxPosition    POINT
	rcNormalPosition RECT
}

var (
	pGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	pSetWindowPlacement = user32.NewProc("SetWindowPlacement")
)

// moveToMonitor moves a window onto a display, centred in its work area, keeping it
// maximised if it was. It returns false if the window was already there.
func moveToMonitor(h, target uintptr) bool {
	src, _, _ := pMonitorFromWindow.Call(h, 2)
	if src == target {
		return false
	}
	var smi, tmi MONITORINFO
	smi.CbSize = uint32(unsafe.Sizeof(smi))
	tmi.CbSize = uint32(unsafe.Sizeof(tmi))
	if r, _, _ := pGetMonitorInfoW.Call(src, uintptr(unsafe.Pointer(&smi))); r == 0 {
		return false
	}
	if r, _, _ := pGetMonitorInfoW.Call(target, uintptr(unsafe.Pointer(&tmi))); r == 0 {
		return false
	}
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := pGetWindowPlacement.Call(h, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return false
	}
	tw, sw := tmi.RcWork, smi.RcWork
	var cur RECT
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&cur)))
	if wp.showCmd == 3 { // maximised: its restored size, centred
		cur = wp.rcNormalPosition
	}
	w, hh := cur.Right-cur.Left, cur.Bottom-cur.Top
	// keep where the window sits within its display, as a fraction of the free space
	fx, fy := 0.5, 0.5
	if span := (sw.Right - sw.Left) - w; span > 0 && wp.showCmd != 3 {
		fx = clamp01(float64(cur.Left-sw.Left) / float64(span))
	}
	if span := (sw.Bottom - sw.Top) - hh; span > 0 && wp.showCmd != 3 {
		fy = clamp01(float64(cur.Top-sw.Top) / float64(span))
	}
	w = min(w, tw.Right-tw.Left)
	hh = min(hh, tw.Bottom-tw.Top)
	x := tw.Left + int32(fx*float64((tw.Right-tw.Left)-w))
	y := tw.Top + int32(fy*float64((tw.Bottom-tw.Top)-hh))
	if wp.showCmd == 3 {
		// maximised: one SetWindowPlacement moves its restored size to the new display
		// and maximises it there in a single step (restore + move + maximise flickered).
		// Placement rects are in workspace coordinates: relative to the main display's
		// work area.
		var pmi MONITORINFO
		pmi.CbSize = uint32(unsafe.Sizeof(pmi))
		prim, _, _ := pMonitorFromPoint.Call(0, 1) // MONITOR_DEFAULTTOPRIMARY
		pGetMonitorInfoW.Call(prim, uintptr(unsafe.Pointer(&pmi)))
		ox, oy := pmi.RcWork.Left-pmi.RcMonitor.Left, pmi.RcWork.Top-pmi.RcMonitor.Top
		wp.rcNormalPosition = RECT{x - ox, y - oy, x - ox + w, y - oy + hh}
		wp.flags = 0x0004 // WPF_ASYNCWINDOWPLACEMENT: never wait on a busy app
		wp.showCmd = 3    // SW_SHOWMAXIMIZED
		if r, _, _ := pSetWindowPlacement.Call(h, uintptr(unsafe.Pointer(&wp))); r != 0 {
			return true
		}
		// fallback: the old three steps
		pShowWindow.Call(h, SW_RESTORE)
		pSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(hh), SWP_NOZORDER|SWP_NOACTIVATE)
		pShowWindow.Call(h, 3) // SW_MAXIMIZE
		return true
	}
	pSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(hh), SWP_NOZORDER|SWP_NOACTIVATE|0x4000) // SWP_ASYNCWINDOWPOS
	return true
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
