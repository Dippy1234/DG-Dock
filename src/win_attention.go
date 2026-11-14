//go:build windows

package main

// Attention: when an app flashes its taskbar button (a new message, a finished
// download), its dock icon bounces once and wears a red dot until you go to it. The
// taskbar is usually hidden, so this is how those alerts reach you.

import "time"

func (a *App) itemOfWindow(h uintptr) *Item {
	if h == 0 {
		return nil
	}
	root, _, _ := pGetAncestor.Call(h, 3) // GA_ROOTOWNER
	for _, it := range a.dock.Items {
		for _, w := range it.Windows {
			if w == h || (root != 0 && w == root) {
				return it
			}
		}
	}
	return nil
}

// onShellHook handles the shell's window notifications.
func (a *App) onShellHook(code, hwnd uintptr) {
	switch code {
	case 0x8006: // HSHELL_FLASH
		if a.cfg.NoAttention {
			return
		}
		if fg, _, _ := pGetForegroundWindow.Call(); fg == hwnd {
			return // it's already in front of you
		}
		it := a.itemOfWindow(hwnd)
		if it == nil || it.Attention {
			return // (apps flash several times: one bounce is enough)
		}
		it.Attention = true
		if !a.cfg.NoBounce {
			it.Bouncing, it.BounceStart = true, time.Now()
			a.setAnimInterval(15)
		}
		if a.dock.HideTarget > 0 { // a hidden dock peeks out so the alert is seen
			a.peekUntil = time.Now().Add(3 * time.Second)
			a.animUntil = a.peekUntil.Add(time.Second)
			a.setAnimInterval(15)
		}
		a.needRender = true
		a.render()
	case 4, 0x8004: // HSHELL_WINDOWACTIVATED, HSHELL_RUDEAPPACTIVATED
		if it := a.itemOfWindow(hwnd); it != nil && it.Attention {
			it.Attention = false
			a.needRender = true
			a.render()
		}
	}
}
