//go:build windows

package main

// Scroll on the battery ring to change the built-in screen's brightness (same method
// as a laptop's Fn keys: the display driver's \\.\LCD control, with Windows' WMI
// brightness method as a slower fallback).

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type displayBrightness struct{ Policy, AC, DC byte }

func onBattery() bool {
	_, _, _, ok := batteryInfo()
	if !ok {
		return false
	}
	var s struct {
		ACLine, Flag, Percent, Saver byte
		Life, FullLife               uint32
	}
	pGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s)))
	return s.ACLine == 0
}

// setBrightnessBy changes brightness by delta percent and returns the new level (0..1).
func setBrightnessBy(delta int, guess float64) (float64, bool) {
	p, _ := windows.UTF16PtrFromString(`\\.\LCD`)
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		defer windows.CloseHandle(h)
		var levels [256]byte
		var n, got uint32
		var cur displayBrightness
		if windows.DeviceIoControl(h, 0x230494, nil, 0, &levels[0], uint32(len(levels)), &n, nil) == nil && n > 0 &&
			windows.DeviceIoControl(h, 0x230498, nil, 0, (*byte)(unsafe.Pointer(&cur)), 3, &got, nil) == nil {
			top := int(levels[n-1])
			if top <= 0 {
				top = 100
			}
			now := int(cur.AC)
			if onBattery() {
				now = int(cur.DC)
			}
			target := now + delta*top/100
			best := int(levels[0])
			for i := 0; i < int(n); i++ { // nearest supported level in the right direction
				l := int(levels[i])
				if (delta > 0 && l >= target && (best < target || l < best)) || (delta < 0 && l <= target && (best > target || l > best)) {
					best = l
				}
			}
			if delta > 0 && best < now {
				best = top
			}
			if delta < 0 && best > now {
				best = int(levels[0])
			}
			nb := displayBrightness{Policy: 3, AC: byte(best), DC: byte(best)}
			if windows.DeviceIoControl(h, 0x23049C, (*byte)(unsafe.Pointer(&nb)), 3, nil, 0, &got, nil) == nil {
				return float64(best) / float64(top), true
			}
		}
	}
	// no driver brightness control (e.g. an external monitor): show the change we'd make
	g := guess + float64(delta)/100
	if g < 0 {
		g = 0
	}
	if g > 1 {
		g = 1
	}
	return g, true
}

// scrollBrightness: one wheel notch = 5%. Touchpad scrolls add up until they make a step.
func (a *App) scrollBrightness(delta int16) {
	it := a.items["w:status"]
	if it == nil {
		return
	}
	a.brAcc += int(delta) * 5
	step := a.brAcc / 120
	if step == 0 {
		return
	}
	a.brAcc -= step * 120
	guess := it.WBr
	if it.WBrAt.IsZero() {
		guess = 0.5
	}
	if v, ok := setBrightnessBy(step, guess); ok {
		it.WBr = v
	}
	it.WBrAt = time.Now()
	a.needRender = true
	a.render()
}
