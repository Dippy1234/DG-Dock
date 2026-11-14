package main

import "testing"

func TestWinKeyAlone(t *testing.T) {
	var st wkState
	if a := st.step(0x5B, true, false, false, 0, 0); !a.block || a.launch {
		t.Fatal("press should be held back", a)
	}
	if a := st.step(0x5B, true, false, false, 0, 0); !a.block { // auto-repeat
		t.Fatal("repeat should be held back", a)
	}
	a := st.step(0x5B, false, false, false, 0, 0)
	if !a.block || !a.launch || len(a.sends) != 2 || a.sends[0].vk != vkF24 {
		t.Fatal("lone release should open Launchpad via F24", a)
	}
	if st.held != 0 {
		t.Fatal("state not reset")
	}
}

func TestWinKeyCombo(t *testing.T) {
	var st wkState
	st.step(0x5B, true, false, false, 0, 0)
	a := st.step(0x45, true, false, false, 0x12, 0) // E
	if !a.block || len(a.sends) != 2 || a.sends[0].vk != 0x5B || a.sends[0].up || a.sends[1].vk != 0x45 || a.sends[1].scan != 0x12 {
		t.Fatal("Win+E should replay Win then E", a)
	}
	if a := st.step(0x45, false, false, false, 0, 0); a.block { // E up passes
		t.Fatal("E release should pass", a)
	}
	if a := st.step(0x5B, true, false, false, 0, 0); a.block { // repeat after hand-off
		t.Fatal("Win repeat after hand-off should pass", a)
	}
	if a := st.step(0x5B, false, false, false, 0, 0); a.block || a.launch {
		t.Fatal("Win release after combo should pass to Windows", a)
	}
}

func TestWinKeyShiftS(t *testing.T) {
	var st wkState
	st.step(0x5C, true, false, false, 0, 0)
	a := st.step(0xA0, true, false, false, 0x2A, 0) // Shift joins first
	if len(a.sends) != 2 || a.sends[0].vk != 0x5C {
		t.Fatal("first extra key hands over the held Win", a)
	}
	if a := st.step(0x53, true, false, false, 0, 0); a.block { // S then goes straight through
		t.Fatal("later keys pass", a)
	}
}

func TestWinKeyLock(t *testing.T) {
	var st wkState
	st.step(0x5B, true, false, false, 0, 0)
	if a := st.step(0x4C, true, false, false, 0, 0); !a.lock || !a.block || len(a.sends) != 0 {
		t.Fatal("Win+L should lock directly", a)
	}
	if a := st.step(0x5B, false, false, false, 0, 0); !a.block || a.launch {
		t.Fatal("release after Win+L: swallowed, no Launchpad", a)
	}
}

func TestWinKeyPassAndInjected(t *testing.T) {
	var st wkState
	if a := st.step(0x5B, true, false, true, 0, 0); a.block { // full-screen game
		t.Fatal("full screen should pass", a)
	}
	if a := st.step(0x5B, false, false, true, 0, 0); a.block || a.launch {
		t.Fatal("full screen release should pass", a)
	}
	if a := st.step(0x5B, true, true, false, 0, 0); a.block { // another program's Win
		t.Fatal("injected Win should pass", a)
	}
}
