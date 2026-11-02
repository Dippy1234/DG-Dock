//go:build windows

package main

// Scroll on the Now Playing tile to change the system volume.

import (
	"math"
	"time"
	"unsafe"
)

var IID_IAudioEndpointVolume = mustGUID("{5CDF2C82-841E-4546-9722-0CF74078229A}")

func (a *App) endpointVolume() uintptr {
	if a.volEP != 0 {
		return a.volEP
	}
	var en uintptr
	if hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)), 0, 23,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)), uintptr(unsafe.Pointer(&en))); !hresultOK(hr) || en == 0 {
		return 0
	}
	var dev uintptr
	hr := comCall(en, 4, 0, 1, uintptr(unsafe.Pointer(&dev))) // GetDefaultAudioEndpoint(eRender, eMultimedia)
	comRelease(en)
	if !hresultOK(hr) || dev == 0 {
		return 0
	}
	hr = comCall(dev, 3, uintptr(unsafe.Pointer(&IID_IAudioEndpointVolume)), 23, 0, uintptr(unsafe.Pointer(&a.volEP))) // Activate
	comRelease(dev)
	if !hresultOK(hr) {
		a.volEP = 0
	}
	return a.volEP
}

// scrollVolume nudges the volume: one wheel notch = 4%. Touchpads send smaller steps.
func (a *App) scrollVolume(delta int16) {
	// the default device can change (headphones plugged in): fetch it fresh if it's been a while
	if a.volEP != 0 && time.Since(a.volAt) > 5*time.Second {
		comRelease(a.volEP)
		a.volEP = 0
	}
	a.volAt = time.Now()
	ep := a.endpointVolume()
	if ep == 0 {
		return
	}
	var cur float32
	if hr := comCall(ep, 9, uintptr(unsafe.Pointer(&cur))); !hresultOK(hr) { // GetMasterVolumeLevelScalar
		comRelease(ep)
		a.volEP = 0
		return
	}
	var muted int32
	comCall(ep, 15, uintptr(unsafe.Pointer(&muted))) // GetMute
	v := math.Max(0, math.Min(1, float64(cur)+float64(delta)/120*0.04))
	v = math.Round(v*100) / 100
	if delta > 0 && muted != 0 {
		comCall(ep, 14, 0, 0) // SetMute(FALSE)
		muted = 0
	}
	// the float goes in XMM1; Go's syscall copies each argument into both register sets
	comCall(ep, 7, uintptr(math.Float32bits(float32(v))), 0) // SetMasterVolumeLevelScalar
	if it := a.items["w:media"]; it != nil {
		it.WVol, it.WVolAt = v, time.Now()
		if muted != 0 {
			it.WVol = -1
		}
		a.needRender = true
		a.render()
	}
}
