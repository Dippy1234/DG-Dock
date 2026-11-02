//go:build windows

package main

// Drag and drop onto the dock with full OLE support (an IDropTarget), so it takes
// apps dragged from Start / All apps as well as files and folders from File Explorer
// or the desktop. While you hover, the icons part to show where it will land (like
// macOS), and the system drag image stays visible over the dock.
//
// Drop on the Bin to delete files; drop files onto an app to open them with it;
// drop anywhere else to pin at that spot.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pOleInitialize            = ole32.NewProc("OleInitialize")
	pRegisterDragDrop         = ole32.NewProc("RegisterDragDrop")
	pReleaseStgMedium         = ole32.NewProc("ReleaseStgMedium")
	pILCombine                = shell32.NewProc("ILCombine")
	pILFree                   = shell32.NewProc("ILFree")
	pSHCreateItemFromIDList   = shell32.NewProc("SHCreateItemFromIDList")
	pRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")

	IID_IDropTarget       = mustGUID("{00000122-0000-0000-C000-000000000046}")
	IID_IUnknownDT        = mustGUID("{00000000-0000-0000-C000-000000000046}")
	CLSID_DragDropHelper  = mustGUID("{4657278A-411B-11D2-839A-00C04FD918D0}")
	IID_IDropTargetHelper = mustGUID("{4657278B-411B-11D2-839A-00C04FD918D0}")
)

const (
	appsFolderCLSID = "{4234D49B-0245-4DF3-B780-3893943456E1}"
	dropLink        = 4
	dropCopy        = 1
	dropMove        = 2
)

type formatEtc struct {
	cf     uint16
	_      [6]byte
	ptd    uintptr
	aspect uint32
	lindex int32
	tymed  uint32
	_      uint32
}

type stgMedium struct {
	tymed   uint32
	_       uint32
	hGlobal uintptr
	unk     uintptr
}

// dropTarget is the COM object Windows calls while something is dragged over the dock.
type dropTarget struct {
	vtbl *[7]uintptr
}

var (
	theDropTarget *dropTarget
	dropVtbl      [7]uintptr
	cfIDList      uint16
	dropHelper    uintptr
)

// dragged is what's being dragged over the dock right now.
type dragged struct {
	pins  []Pin    // what to pin
	files []string // plain file paths (for the Bin, or to open with an app)
	dirs  bool     // all folders (they go with the folders on the right)
}

func (a *App) startDropTarget() {
	pOleInitialize.Call(0)
	cf, _, _ := pRegisterClipboardFormatW.Call(up(u16("Shell IDList Array")))
	cfIDList = uint16(cf)
	pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_DragDropHelper)), 0, 1,
		uintptr(unsafe.Pointer(&IID_IDropTargetHelper)), uintptr(unsafe.Pointer(&dropHelper)))
	dropVtbl = [7]uintptr{
		syscall.NewCallback(dtQueryInterface),
		syscall.NewCallback(dtAddRef),
		syscall.NewCallback(dtRelease),
		syscall.NewCallback(dtDragEnter),
		syscall.NewCallback(dtDragOver),
		syscall.NewCallback(dtDragLeave),
		syscall.NewCallback(dtDrop),
	}
	theDropTarget = &dropTarget{vtbl: &dropVtbl}
	if hr, _, _ := pRegisterDragDrop.Call(a.hwnd, uintptr(unsafe.Pointer(theDropTarget))); !hresultOK(hr) {
		a.logf("drop target: %x (falling back to file drops only)", hr)
	}
}

func dtQueryInterface(this, riid, ppv uintptr) uintptr {
	g := (*windows.GUID)(unsafe.Pointer(riid))
	if *g == IID_IDropTarget || *g == IID_IUnknownDT {
		*(*uintptr)(unsafe.Pointer(ppv)) = this
		return 0
	}
	*(*uintptr)(unsafe.Pointer(ppv)) = 0
	return 0x80004002 // E_NOINTERFACE
}

func dtAddRef(this uintptr) uintptr  { return 1 }
func dtRelease(this uintptr) uintptr { return 1 }

func ptOf(pt uintptr) (int32, int32) { return int32(uint32(pt)), int32(uint32(pt >> 32)) }

func dtDragEnter(this, obj, keys, pt, effect uintptr) uintptr {
	a := app
	a.drag = readDataObject(obj)
	x, y := ptOf(pt)
	e := a.dragOver(x, y, *(*uint32)(unsafe.Pointer(effect)))
	*(*uint32)(unsafe.Pointer(effect)) = e
	if dropHelper != 0 {
		p := POINT{x, y}
		comCall(dropHelper, 3, a.hwnd, obj, uintptr(unsafe.Pointer(&p)), uintptr(e))
	}
	return 0
}

func dtDragOver(this, keys, pt, effect uintptr) uintptr {
	a := app
	x, y := ptOf(pt)
	e := a.dragOver(x, y, *(*uint32)(unsafe.Pointer(effect)))
	*(*uint32)(unsafe.Pointer(effect)) = e
	if dropHelper != 0 {
		p := POINT{x, y}
		comCall(dropHelper, 5, uintptr(unsafe.Pointer(&p)), uintptr(e))
	}
	return 0
}

func dtDragLeave(this uintptr) uintptr {
	a := app
	a.drag = nil
	a.closeDropGap()
	if dropHelper != 0 {
		comCall(dropHelper, 4)
	}
	return 0
}

func dtDrop(this, obj, keys, pt, effect uintptr) uintptr {
	a := app
	x, y := ptOf(pt)
	e := a.dragOver(x, y, *(*uint32)(unsafe.Pointer(effect)))
	*(*uint32)(unsafe.Pointer(effect)) = e
	if dropHelper != 0 {
		p := POINT{x, y}
		comCall(dropHelper, 6, obj, uintptr(unsafe.Pointer(&p)), uintptr(e))
	}
	if a.drag != nil && e != 0 {
		a.doDrop(x, y)
	}
	a.drag = nil
	a.closeDropGap()
	return 0
}

// readDataObject works out what's being dragged: shell items (Start menu apps, files,
// folders) or, failing that, a plain file list.
func readDataObject(obj uintptr) *dragged {
	dg := &dragged{dirs: true}
	if cfIDList != 0 {
		fe := formatEtc{cf: cfIDList, aspect: 1, lindex: -1, tymed: 1}
		var sm stgMedium
		if hr := comCall(obj, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&sm))); hresultOK(hr) && sm.hGlobal != 0 {
			readCIDA(sm.hGlobal, dg)
			pReleaseStgMedium.Call(uintptr(unsafe.Pointer(&sm)))
		}
	}
	if len(dg.pins) == 0 {
		fe := formatEtc{cf: 15, aspect: 1, lindex: -1, tymed: 1} // CF_HDROP
		var sm stgMedium
		if hr := comCall(obj, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&sm))); hresultOK(hr) && sm.hGlobal != 0 {
			n, _, _ := pDragQueryFileW.Call(sm.hGlobal, 0xFFFFFFFF, 0, 0)
			for i := uintptr(0); i < n && i < 64; i++ {
				buf := make([]uint16, 1024)
				pDragQueryFileW.Call(sm.hGlobal, i, uintptr(unsafe.Pointer(&buf[0])), 1024)
				dg.addFile(windows.UTF16ToString(buf))
			}
			pReleaseStgMedium.Call(uintptr(unsafe.Pointer(&sm)))
		}
	}
	if len(dg.pins) == 0 {
		return nil
	}
	return dg
}

func (dg *dragged) addFile(p string) {
	if p == "" {
		return
	}
	dg.pins = append(dg.pins, Pin{Kind: KindFile, Target: p})
	dg.files = append(dg.files, p)
	if !isDir(p) {
		dg.dirs = false
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// readCIDA reads a "Shell IDList Array": a parent folder plus child items.
func readCIDA(h uintptr, dg *dragged) {
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return
	}
	defer pGlobalUnlock.Call(h)
	n := *(*uint32)(unsafe.Pointer(p))
	if n == 0 || n > 64 {
		return
	}
	off := func(i uint32) uintptr { return uintptr(*(*uint32)(unsafe.Pointer(p + 4 + uintptr(i)*4))) }
	parent := p + off(0)
	for i := uint32(1); i <= n; i++ {
		abs, _, _ := pILCombine.Call(parent, p+off(i))
		if abs == 0 {
			continue
		}
		var si uintptr
		pSHCreateItemFromIDList.Call(abs, uintptr(unsafe.Pointer(&IID_IShellItem)), uintptr(unsafe.Pointer(&si)))
		pILFree.Call(abs)
		if si == 0 {
			continue
		}
		name := func(sigdn uint32) string {
			var s uintptr
			if hr := comCall(si, 5, uintptr(sigdn), uintptr(unsafe.Pointer(&s))); hresultOK(hr) && s != 0 {
				defer pCoTaskMemFree.Call(s)
				return u16ToString(s)
			}
			return ""
		}
		if fp := name(0x80058000); fp != "" { // SIGDN_FILESYSPATH: a real file or folder
			dg.addFile(fp)
		} else if abs := name(0x80028000); strings.Contains(strings.ToUpper(abs), appsFolderCLSID) { // an app from Start
			if id := name(0x80018001); id != "" {
				dg.pins = append(dg.pins, Pin{Kind: KindApp, Target: id})
				dg.dirs = false
			}
		}
		comRelease(si)
	}
}

// dragOver updates the gap in the dock for the current pointer position and returns
// the drop effect to show (0 = can't drop here).
func (a *App) dragOver(x, y int32, allowed uint32) uint32 {
	if a.drag == nil {
		return 0
	}
	d := a.dock
	if d.HideTarget > 0 {
		d.HideTarget = 0 // auto-hide: bring the dock up for the drop
	}
	lx, ly := float64(x-a.winX), float64(y-a.winY)
	target := a.dropTargetItem(lx, ly)
	pick := func(prefer ...uint32) uint32 {
		for _, e := range prefer {
			if allowed&e != 0 {
				return e
			}
		}
		return 0
	}
	if target != nil {
		a.closeDropGap()
		if target.Kind == KindTrash {
			return pick(dropMove, dropCopy, dropLink)
		}
		return pick(dropCopy, dropLink, dropMove) // open with this app
	}
	before, section := a.dropSlot(lx, ly)
	a.openDropGap(before, section)
	return pick(dropLink, dropCopy, dropMove)
}

// dropTargetItem is the Bin or an app under the pointer when files are dragged right
// onto it (the middle of the icon); between icons means "pin here" instead.
func (a *App) dropTargetItem(lx, ly float64) *Item {
	if len(a.drag.files) == 0 {
		return nil
	}
	d := a.dock
	idx := d.ItemAt(lx, ly)
	if idx < 0 {
		return nil
	}
	it := d.Items[idx]
	u, _ := d.toUV(lx, ly)
	if it.Kind == KindGap || it.Slot <= 0 || absF(u-it.U) > it.Slot*0.3 {
		return nil
	}
	if it.Kind == KindTrash {
		return it
	}
	if it.ExePath != "" && !it.IsDir && !a.drag.dirs {
		for _, f := range a.drag.files {
			switch strings.ToLower(filepath.Ext(f)) {
			case ".exe", ".lnk", ".url":
				return nil // pin programs, don't "open" them with another app
			}
		}
		return it
	}
	return nil
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// dropSlot finds where a drop would go: before which item (nil = end), in which pill.
func (a *App) dropSlot(lx, ly float64) (*Item, int) {
	d := a.dock
	section := 0
	if a.drag.dirs {
		section = 1
	}
	u, _ := d.toUV(lx, ly)
	var before *Item
	for _, it := range d.Items {
		if it.Kind == KindGap || it.Kind == KindSep || it.PageOut || it.Kind == KindWidget {
			continue
		}
		if it.Section != section || it.Kind == KindStart {
			continue
		}
		if it.Kind == KindTrash {
			before = it // folders go before the Bin
			break
		}
		if u < it.U {
			before = it
			break
		}
	}
	return before, section
}

func (a *App) openDropGap(before *Item, section int) {
	g := a.items["dropgap"]
	if g == nil {
		g = &Item{Key: "dropgap", Kind: KindGap, Scale: 1, WidthB: 1}
		a.items["dropgap"] = g
	}
	if a.dropOn && !g.Leaving && a.dropBefore == before && a.dropSection == section {
		return
	}
	if !a.dropOn || g.Leaving {
		g.Appear = 0
	}
	g.Leaving = false
	a.dropOn, a.dropBefore, a.dropSection = true, before, section
	a.rebuildItems()
	a.needRender = true
	a.setAnimInterval(15)
}

func (a *App) closeDropGap() {
	if g := a.items["dropgap"]; g != nil && a.dropOn {
		g.Leaving = true
		a.needRender = true
		a.setAnimInterval(15)
	}
}

// finishDropGap removes the gap once it has closed.
func (a *App) finishDropGap() {
	g := a.items["dropgap"]
	if g == nil || !a.dropOn || !g.Leaving || g.Appear > 0.01 {
		return
	}
	a.dropOn, a.dropBefore = false, nil
	a.rebuildItems()
}

// placeDropGap puts the gap into the item list (called by rebuildItems).
func (a *App) placeDropGap(items []*Item, used map[string]bool) []*Item {
	if !a.dropOn {
		return items
	}
	g := a.items["dropgap"]
	if g == nil {
		return items
	}
	used["dropgap"] = true
	at := -1
	if a.dropBefore != nil {
		for i, it := range items {
			if it == a.dropBefore {
				at = i
				break
			}
		}
	}
	if at < 0 { // end of the pill
		at = len(items)
		for i, it := range items {
			if a.dropSection == 0 && it.Kind == KindSep {
				at = i
				break
			}
			if a.dropSection == 1 && (it.Kind == KindTrash || it.Key == "sep2") {
				at = i
				break
			}
		}
		if a.dropSection == 1 && at == len(items) {
			for i, it := range items {
				if it.Kind == KindWidget {
					at = i
					break
				}
			}
		}
	}
	out := append([]*Item{}, items[:at]...)
	out = append(out, g)
	return append(out, items[at:]...)
}

// doDrop does the drop: recycle, open with an app, or pin where the gap is.
func (a *App) doDrop(x, y int32) {
	dg := a.drag
	lx, ly := float64(x-a.winX), float64(y-a.winY)
	if t := a.dropTargetItem(lx, ly); t != nil {
		if t.Kind == KindTrash {
			recycleFiles(dg.files)
			return
		}
		for _, f := range dg.files {
			shellOpen(t.ExePath, `"`+f+`"`)
		}
		if !a.cfg.NoBounce {
			t.Bouncing, t.BounceStart = true, time.Now()
		}
		return
	}
	before := a.dropBefore
	for _, p := range dg.pins {
		a.insertPinBefore(p, before)
	}
	a.dropOn = false // the new icon takes the gap's place straight away
	if g := a.items["dropgap"]; g != nil {
		g.Leaving = true
	}
	a.rebuildItems()
	a.scanRunning()
	a.render()
	a.refreshSettings()
}

// insertPinBefore pins p just before a dock item (nil = end of its pill).
func (a *App) insertPinBefore(p Pin, before *Item) {
	if a.hasPin(p.Kind, p.Target) >= 0 {
		return
	}
	at := -1
	if before != nil {
		// first pinned item from "before" onwards in the same pill
		found := false
		for _, it := range a.dock.Items {
			if it == before {
				found = true
			}
			if found && it.Pin != nil && it.Section == before.Section {
				at = it.PinIdx
				break
			}
		}
	}
	if at < 0 || at > len(a.cfg.Pins) {
		at = len(a.cfg.Pins)
		for i := range a.cfg.Pins {
			if a.cfg.Pins[i].Kind == KindTrash {
				at = i
			}
		}
	}
	np := append([]Pin{}, a.cfg.Pins[:at]...)
	np = append(np, p)
	np = append(np, a.cfg.Pins[at:]...)
	a.cfg.Pins = np
	a.saveConfig()
	a.rebuildItems()
}
