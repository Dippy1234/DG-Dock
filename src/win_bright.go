//go:build windows

package main

// Brightness from the battery ring: scroll on it to change the brightness of the
// display the dock is on — any display:
//
//   - the laptop's own screen: the display driver's \\.\LCD control (what the Fn keys use)
//   - an external monitor: DDC/CI, the monitor's own brightness setting over the cable
//     (most monitors; it's in the monitor's menu if it's been turned off)
//   - anything else (or below the lowest backlight level): the screen is dimmed with a
//     see-through black layer that clicks pass through and screenshots leave out
//
// Turning it back up takes the dimming away first, then raises the backlight.

import (
	"math"
	"sync"
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

// panelBrightnessBy changes the laptop screen's backlight by delta percent. It returns
// the new level (0..1), whether it was already as low as it goes (for delta < 0), and
// false if there's no backlight control.
func panelBrightnessBy(delta int) (level float64, atMin, ok bool) {
	p, _ := windows.UTF16PtrFromString(`\\.\LCD`)
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, false, false
	}
	defer windows.CloseHandle(h)
	var levels [256]byte
	var n, got uint32
	var cur displayBrightness
	if windows.DeviceIoControl(h, 0x230494, nil, 0, &levels[0], uint32(len(levels)), &n, nil) != nil || n == 0 ||
		windows.DeviceIoControl(h, 0x230498, nil, 0, (*byte)(unsafe.Pointer(&cur)), 3, &got, nil) != nil {
		return 0, false, false
	}
	top := int(levels[n-1])
	if top <= 0 {
		top = 100
	}
	now := int(cur.AC)
	if onBattery() {
		now = int(cur.DC)
	}
	if delta < 0 && now <= int(levels[0]) {
		return float64(now) / float64(top), true, true
	}
	if delta == 0 {
		return float64(now) / float64(top), false, true
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
	if windows.DeviceIoControl(h, 0x23049C, (*byte)(unsafe.Pointer(&nb)), 3, nil, 0, &got, nil) != nil {
		return float64(now) / float64(top), false, true
	}
	return float64(best) / float64(top), false, true
}

// ---------- external monitors: DDC/CI ----------

var (
	dxva2                        = windows.NewLazySystemDLL("dxva2.dll")
	pGetNumberOfPhysicalMonitors = dxva2.NewProc("GetNumberOfPhysicalMonitorsFromHMONITOR")
	pGetPhysicalMonitors         = dxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	pDestroyPhysicalMonitors     = dxva2.NewProc("DestroyPhysicalMonitors")
	pGetVCPFeature               = dxva2.NewProc("GetVCPFeatureAndVCPFeatureReply")
	pSetVCPFeature               = dxva2.NewProc("SetVCPFeature")
	pSetLayeredWindowAttributes  = user32.NewProc("SetLayeredWindowAttributes")
	pSetWindowDisplayAffinity    = user32.NewProc("SetWindowDisplayAffinity")
)

const vcpBrightness = 0x10

type physicalMonitor struct {
	H    uintptr
	Desc [128]uint16
}

// monBright is one display's brightness state.
type monBright struct {
	internal bool
	ddc      []physicalMonitor
	ddcState int // 0 not checked yet, 1 works, -1 doesn't (or checking)
	ddcCur   int
	ddcMax   int
	ddcWant  int     // a level waiting to be sent (-1 none)
	dim      float64 // 0..1 of the strongest dimming
	overlay  uintptr
}

var bright = struct {
	sync.Mutex
	mons map[uintptr]*monBright
	kick chan struct{}
}{mons: map[uintptr]*monBright{}, kick: make(chan struct{}, 1)}

const maxDim = 0.85 // the dimming layer's strongest opacity

// brightFor returns a display's state, and starts checking for DDC/CI in the
// background the first time (talking to a monitor takes a moment).
func (a *App) brightFor(mon uintptr) *monBright {
	bright.Lock()
	defer bright.Unlock()
	mb := bright.mons[mon]
	if mb != nil {
		return mb
	}
	mb = &monBright{ddcWant: -1}
	for _, d := range listDisplays() {
		if d.handle == mon {
			mb.internal = d.internal
		}
	}
	bright.mons[mon] = mb
	if !mb.internal && dxva2.Load() == nil {
		mb.ddcState = -1 // checking
		go probeDDC(mon, mb)
	}
	startBrightWorker()
	return mb
}

func probeDDC(mon uintptr, mb *monBright) {
	var n uint32
	if r, _, _ := pGetNumberOfPhysicalMonitors.Call(mon, uintptr(unsafe.Pointer(&n))); r == 0 || n == 0 {
		return
	}
	pms := make([]physicalMonitor, n)
	if r, _, _ := pGetPhysicalMonitors.Call(mon, uintptr(n), uintptr(unsafe.Pointer(&pms[0]))); r == 0 {
		return
	}
	var cur, max uint32
	if r, _, _ := pGetVCPFeature.Call(pms[0].H, vcpBrightness, 0, uintptr(unsafe.Pointer(&cur)), uintptr(unsafe.Pointer(&max))); r == 0 || max == 0 {
		pDestroyPhysicalMonitors.Call(uintptr(n), uintptr(unsafe.Pointer(&pms[0])))
		return
	}
	bright.Lock()
	mb.ddc, mb.ddcState, mb.ddcCur, mb.ddcMax = pms, 1, int(cur), int(max)
	bright.Unlock()
}

var brightWorkerOnce sync.Once

// startBrightWorker sends DDC/CI levels from one background thread: each command
// takes tens of milliseconds, so scrolling never waits for the monitor, and a fast
// scroll sends only the latest level.
func startBrightWorker() {
	brightWorkerOnce.Do(func() {
		go func() {
			for range bright.kick {
				for {
					bright.Lock()
					var hs []uintptr
					val := -1
					for _, mb := range bright.mons {
						if mb.ddcWant >= 0 && mb.ddcState == 1 {
							val, mb.ddcWant = mb.ddcWant, -1
							for _, p := range mb.ddc {
								hs = append(hs, p.H)
							}
							break
						}
					}
					bright.Unlock()
					if val < 0 {
						break
					}
					for _, h := range hs {
						pSetVCPFeature.Call(h, vcpBrightness, uintptr(val))
					}
				}
			}
		}()
	})
}

// refreshBrightness runs after displays change: dimming layers follow their screen
// (or go, if it was unplugged), and monitors are asked again about DDC/CI, since
// their handles don't survive a display change or sleep.
func (a *App) refreshBrightness(displaysChanged bool) {
	bright.Lock()
	defer bright.Unlock()
	for mon, mb := range bright.mons {
		var mi MONITORINFO
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		gone := false
		if r, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
			gone = true
		}
		if mb.overlay != 0 {
			if gone {
				pDestroyWindow(mb.overlay)
				mb.overlay = 0
			} else {
				m := mi.RcMonitor
				pSetWindowPos.Call(mb.overlay, ^uintptr(0), uintptr(m.Left), uintptr(m.Top), uintptr(m.Right-m.Left), uintptr(m.Bottom-m.Top), SWP_NOACTIVATE)
			}
		}
		if gone || (displaysChanged && mb.overlay == 0) {
			if len(mb.ddc) > 0 && mb.ddcWant < 0 {
				pDestroyPhysicalMonitors.Call(uintptr(len(mb.ddc)), uintptr(unsafe.Pointer(&mb.ddc[0])))
				mb.ddc = nil
			}
			delete(bright.mons, mon)
		}
	}
}

// ---------- the dimming layer ----------

var dimClassDone bool

func (a *App) setDim(mon uintptr, mb *monBright, dim float64) {
	mb.dim = math.Max(0, math.Min(1, dim))
	if mb.dim < 0.001 {
		mb.dim = 0
		if mb.overlay != 0 {
			pDestroyWindow(mb.overlay)
			mb.overlay = 0
		}
		return
	}
	if mb.overlay == 0 {
		hinst, _, _ := pGetModuleHandleW.Call(0)
		if !dimClassDone {
			dimClassDone = true
			black, _, _ := pGetStockObject.Call(4) // BLACK_BRUSH
			wc := WNDCLASSEXW{LpfnWndProc: pDefWindowProcW.Addr(), HInstance: hinst, HbrBackground: black, LpszClassName: u16("DGDockDim")}
			wc.CbSize = uint32(unsafe.Sizeof(wc))
			pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		}
		var mi MONITORINFO
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
			return
		}
		m := mi.RcMonitor
		// layered + transparent: see-through and clicks go straight through it
		mb.overlay, _, _ = pCreateWindowExW.Call(WS_EX_LAYERED|0x20|WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TOPMOST,
			up(u16("DGDockDim")), up(u16("DG-Dock dimming")), WS_POPUP,
			uintptr(m.Left), uintptr(m.Top), uintptr(m.Right-m.Left), uintptr(m.Bottom-m.Top), 0, 0, hinst, 0)
		if mb.overlay == 0 {
			return
		}
		pSetWindowDisplayAffinity.Call(mb.overlay, 0x11) // WDA_EXCLUDEFROMCAPTURE: screenshots aren't darkened
		pSetLayeredWindowAttributes.Call(mb.overlay, 0, uintptr(math.Round(mb.dim*maxDim*255)), 2)
		pShowWindow.Call(mb.overlay, 4) // SW_SHOWNOACTIVATE
	}
	pSetLayeredWindowAttributes.Call(mb.overlay, 0, uintptr(math.Round(mb.dim*maxDim*255)), 2)
	pSetWindowPos.Call(mb.overlay, ^uintptr(0), 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
}

// keepDimOnTop keeps the dimming layers above windows that make themselves
// always-on-top later. Runs on the scan timer.
func (a *App) keepDimOnTop() {
	bright.Lock()
	defer bright.Unlock()
	for _, mb := range bright.mons {
		if mb.overlay != 0 {
			pSetWindowPos.Call(mb.overlay, ^uintptr(0), 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
		}
	}
}

// ---------- scrolling on the ring ----------

// changeBrightness moves the dock's display brightness by step percent. It returns what
// the ring shows: the level (0..1) and how much extra dimming there is (0..1).
func (a *App) changeBrightness(step int) (level, dim float64) {
	mon := a.curMon
	mb := a.brightFor(mon)
	bright.Lock()
	ddc, cur, mx := mb.ddcState == 1, mb.ddcCur, mb.ddcMax
	bright.Unlock()
	dimStep := float64(step) / 100
	switch {
	case mb.internal:
		if step > 0 && mb.dim > 0 { // undo the dimming first
			a.setDim(mon, mb, mb.dim-dimStep)
			lv, _, _ := panelBrightnessBy(0)
			return lv, mb.dim
		}
		lv, atMin, ok := panelBrightnessBy(step)
		if ok {
			if atMin && step < 0 {
				a.setDim(mon, mb, mb.dim-dimStep)
			}
			return lv, mb.dim
		}
	case ddc:
		if step > 0 && mb.dim > 0 {
			a.setDim(mon, mb, mb.dim-dimStep)
			return float64(cur) / float64(mx), mb.dim
		}
		if step < 0 && cur <= 0 {
			a.setDim(mon, mb, mb.dim-dimStep)
			return 0, mb.dim
		}
		d := int(math.Round(float64(step*mx) / 100))
		if d == 0 {
			d = step // a monitor with a small range still moves
		}
		nv := max(0, min(mx, cur+d))
		bright.Lock()
		mb.ddcCur, mb.ddcWant = nv, nv
		bright.Unlock()
		select {
		case bright.kick <- struct{}{}:
		default:
		}
		return float64(nv) / float64(mx), mb.dim
	}
	// no brightness control at all: dimming only (the ring shows how bright it looks)
	a.setDim(mon, mb, mb.dim-dimStep)
	return 1 - mb.dim, 0
}

// scrollBrightness: one wheel notch = 5%; a touchpad moves it a percent at a time.
func (a *App) scrollBrightness(delta int16) {
	it := a.items["w:status"]
	if it == nil {
		return
	}
	// 1% for every 24 units of scroll: a wheel notch (120) is 5%, and a touchpad
	// changes it smoothly, a percent at a time, instead of in 5% jumps
	a.brAcc += int(delta)
	step := a.brAcc / 24
	if step == 0 {
		return
	}
	a.brAcc -= step * 24
	fresh := time.Since(it.WBrAt) > 1600*time.Millisecond
	it.WBr, it.WBrDim = a.changeBrightness(step)
	if fresh { // the ring appears at the current level rather than sweeping up from 0
		it.WBrShow, it.WBrDimShow = it.WBr, it.WBrDim
	}
	it.WBrAt = time.Now()
	a.needRender = true
	a.render()
}
