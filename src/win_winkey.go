//go:build windows

package main

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows key opens Launchpad.
//
// A low-level keyboard hook (on its own thread, so a busy dock never slows typing)
// holds each Windows-key press back, so Windows never sees a lone Win: no taskbar
// reveal, no Start menu half-opening (that was the flicker). If another key joins it
// (Win+E, Win+Shift+S…) the hook hands Windows the Win press and then that key, so
// shortcuts work as usual; Win+L is done here, since Windows ignores it from software.
// Released on its own, the hook sends F24, a hotkey the dock owns, which opens
// Launchpad and gives it the right to take the keyboard focus.
// Ctrl+Esc still opens the Windows Start menu.

var (
	pSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
)

const (
	hotkeyWinKey = 0x4D45
	wmWinKey     = WM_APP + 46
	winKeyTag    = 0x44474457 // marks the keys the hook sends itself
)

type kbdLLHook struct {
	vk, scan, flags, time uint32
	extra                 uintptr
}

var winKey struct {
	on      atomic.Bool // setting on (main dock only)
	pass    atomic.Bool // a full-screen app is in front: leave Win to Windows
	hotkey  atomic.Bool // the F24 hotkey is registered
	dock    atomic.Uintptr
	started bool
	st      wkState // the current press (hook thread only)
}

// applyWinKey turns the Windows-key takeover on or off to match the settings.
func (a *App) applyWinKey() {
	if isSecondary() {
		return
	}
	on := !a.cfg.NoWinKeyLaunchpad
	winKey.dock.Store(a.hwnd)
	pUnregisterHotKey.Call(a.hwnd, hotkeyWinKey)
	winKey.hotkey.Store(false)
	if on {
		const modNoRepeat = 0x4000
		r, _, _ := pRegisterHotKey.Call(a.hwnd, hotkeyWinKey, modNoRepeat, vkF24)
		winKey.hotkey.Store(r != 0)
	}
	winKey.on.Store(on)
	if on && !winKey.started {
		winKey.started = true
		go winKeyThread()
	}
}

func winKeyThread() {
	runtime.LockOSThread()
	cb := windows.NewCallback(winKeyProc)
	mod, _, _ := pGetModuleHandleW.Call(0)
	h, _, _ := pSetWindowsHookExW.Call(13, cb, mod, 0) // WH_KEYBOARD_LL
	if h == 0 {
		return
	}
	defer pUnhookWindowsHookEx.Call(h)
	var m MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
	}
}

// winKeySend sends a key marked as the dock's own (so the hook lets it through).
func winKeySend(vk uint32, up bool, scan, llFlags uint32) {
	var fl uintptr
	if up {
		fl = 2 // KEYEVENTF_KEYUP
	}
	if vk == 0x5B || vk == 0x5C || llFlags&1 != 0 {
		fl |= 1 // KEYEVENTF_EXTENDEDKEY
	}
	pKeybdEvent.Call(uintptr(vk), uintptr(scan), fl, winKeyTag)
}

func winKeyProc(code int, wp, lp uintptr) uintptr {
	if code == 0 && winKey.on.Load() {
		k := (*kbdLLHook)(unsafe.Pointer(lp))
		if k.extra != winKeyTag {
			down := wp == 0x0100 || wp == 0x0104 // WM_KEYDOWN, WM_SYSKEYDOWN
			act := winKey.st.step(k.vk, down, k.flags&0x10 != 0, winKey.pass.Load(), k.scan, k.flags)
			for _, ks := range act.sends {
				winKeySend(ks.vk, ks.up, ks.scan, ks.flags)
			}
			if act.lock {
				pLockWorkStation.Call()
			}
			if act.launch && !winKey.hotkey.Load() {
				pPostMessageW.Call(winKey.dock.Load(), wmWinKey, 0, 0) // no F24 hotkey: ask directly
			}
			if act.block {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return r
}
