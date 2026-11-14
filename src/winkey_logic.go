package main

// The Windows-key decisions, kept free of Win32 so they can be tested (see win_winkey.go).

const vkF24 = 0x87

// wkSend is one key the hook sends itself.
type wkSend struct {
	vk          uint32
	up          bool
	scan, flags uint32
}

// wkAction is what the hook does with one key event.
type wkAction struct {
	block  bool     // swallow the real event
	sends  []wkSend // keys to send (marked as the dock's own)
	lock   bool     // lock the PC (Win+L)
	launch bool     // open Launchpad (Win on its own)
}

// wkState follows one Windows-key press.
type wkState struct {
	held     uint32 // the Win key being held back (0x5B/0x5C)
	sentDown bool   // …handed to Windows because another key joined it
	combo    bool   // another key was pressed with it
}

// step decides one key event. pass means a full-screen app keeps the normal Win key.
func (st *wkState) step(vk uint32, down, injected, pass bool, scan, flags uint32) wkAction {
	isWin := vk == 0x5B || vk == 0x5C
	switch {
	case isWin && injected:
		// another program pressing Win: leave it
	case isWin && down:
		if st.held != 0 {
			if st.sentDown {
				return wkAction{} // Windows already has Win: let the repeats through
			}
			return wkAction{block: true} // still holding it back
		}
		if pass {
			return wkAction{}
		}
		// hold the press back: Windows never sees a lone Win, so there's no taskbar
		// reveal and no Start menu starting to open
		*st = wkState{held: vk}
		return wkAction{block: true}
	case isWin && st.held != 0: // released
		sent, combo := st.sentDown, st.combo
		*st = wkState{}
		if sent {
			return wkAction{} // Windows got Win+key: it gets the release too
		}
		if combo {
			return wkAction{block: true} // Win+L, done here
		}
		return wkAction{block: true, launch: true, sends: []wkSend{{vk: vkF24}, {vk: vkF24, up: true}}}
	case down && st.held != 0 && !st.sentDown:
		st.combo = true
		if vk == 0x4C { // Win+L: Windows ignores it from software, so lock here
			return wkAction{block: true, lock: true}
		}
		// Win+E, Win+Shift+S…: hand Windows the held-back Win, then this key
		st.sentDown = true
		return wkAction{block: true, sends: []wkSend{{vk: st.held}, {vk: vk, scan: scan, flags: flags}}}
	}
	return wkAction{}
}
