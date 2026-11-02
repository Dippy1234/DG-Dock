//go:build windows

package main

// Network status for the battery/Wi-Fi indicator, read the way the taskbar's own
// network icon does it (Windows.Networking.Connectivity): connection type and signal
// bars only. It never asks for the network's name, which since Windows 11 24H2 needs
// location access and made Windows show a location prompt.

import (
	"unsafe"
)

type wifiInfo struct {
	Connected bool // on Wi-Fi
	Wired     bool // online over Ethernet (or another non-Wi-Fi link)
	Bars      int  // 0..5, as in the taskbar
	Quality   int  // 0..100 (from the bars)
}

var (
	IID_INetworkInformationStatics = mustGUID("{5074F851-950D-4165-9C15-365619481EEA}")
	IID_IConnectionProfile2        = mustGUID("{E2045145-4C9F-400C-9150-7EC7D6E2888A}")
	netStatics                     uintptr
	netStaticsTried                bool
)

func readWifi() wifiInfo {
	if netStatics == 0 {
		if netStaticsTried || pRoGetActivationFactory.Find() != nil {
			return wifiInfo{}
		}
		netStaticsTried = true
		cls := hstr("Windows.Networking.Connectivity.NetworkInformation")
		pRoGetActivationFactory.Call(cls, uintptr(unsafe.Pointer(&IID_INetworkInformationStatics)), uintptr(unsafe.Pointer(&netStatics)))
		pWindowsDeleteString.Call(cls)
		if netStatics == 0 {
			return wifiInfo{}
		}
	}
	var prof uintptr
	if hr := comCall(netStatics, 7, uintptr(unsafe.Pointer(&prof))); !hresultOK(hr) || prof == 0 { // GetInternetConnectionProfile
		return wifiInfo{} // offline
	}
	defer comRelease(prof)
	var p2 uintptr
	if hr := comCall(prof, 0, uintptr(unsafe.Pointer(&IID_IConnectionProfile2)), uintptr(unsafe.Pointer(&p2))); !hresultOK(hr) || p2 == 0 {
		return wifiInfo{Wired: true}
	}
	defer comRelease(p2)
	var wlan, wwan bool
	comCall(p2, 7, uintptr(unsafe.Pointer(&wlan))) // IsWlanConnectionProfile
	comCall(p2, 6, uintptr(unsafe.Pointer(&wwan))) // IsWwanConnectionProfile
	if !wlan && !wwan {
		return wifiInfo{Wired: true}
	}
	w := wifiInfo{Connected: true, Bars: 3}
	var ref uintptr
	if hr := comCall(p2, 11, uintptr(unsafe.Pointer(&ref))); hresultOK(hr) && ref != 0 { // GetSignalBars → IReference<UInt8>
		var b uint8
		if hr := comCall(ref, 6, uintptr(unsafe.Pointer(&b))); hresultOK(hr) { // get_Value
			w.Bars = int(b)
		}
		comRelease(ref)
	}
	if w.Bars > 5 {
		w.Bars = 5
	}
	w.Quality = w.Bars * 20
	return w
}

// wifiDots maps signal bars (0..5) to the indicator's four dots.
func wifiDots(w wifiInfo) int {
	switch {
	case w.Wired:
		return 4
	case !w.Connected:
		return 0
	case w.Bars >= 5:
		return 4
	case w.Bars >= 4:
		return 3
	case w.Bars >= 2:
		return 2
	}
	return 1
}

func signalWord(bars int) string {
	switch {
	case bars >= 5:
		return "excellent signal"
	case bars >= 4:
		return "strong signal"
	case bars >= 2:
		return "fair signal"
	}
	return "weak signal"
}
