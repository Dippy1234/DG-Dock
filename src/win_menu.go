//go:build windows

package main

// Custom-drawn, macOS-style context menus (replace the stock Win32 menus).

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---------- menu windows ----------

// Each open (sub)menu is a layered window drawn by us, sitting on top of a plain
// popup window that carries the Windows acrylic blur, rounded corners and shadow.
type menuWin struct {
	hwnd, back uintptr
	memDC, dib uintptr
	canvas     *image.RGBA
	w, h       int
	x, y       int32
	menu       *macMenu
	hot        int
	open       int // index of the item whose submenu is open
	parent     int // index in the parent menu
	alpha      float64
	shown      time.Time
	acrylic    bool
}

type menuSession struct {
	stack  []*menuWin
	done   bool
	chosen *mItem
}

var (
	session      *menuSession
	menuWndProcC uintptr
	menuClassOK  bool
)

const (
	WM_KEYDOWN        = 0x0100
	WM_ACTIVATE       = 0x0006
	WM_CAPTURECHANGED = 0x0215
	WM_RBUTTONDOWN    = 0x0204
)

func (a *App) menuDark() bool { return a.dock.Dark }

func (a *App) renderMenu(mw *menuWin) {
	renderMenuPure(a.te, a.glyphs, a.dock.Scale, a.menuDark(), mw.acrylic, mw.canvas, mw.w, mw.h, mw.menu, mw.hot, mw.open)
}

func (a *App) presentMenu(mw *menuWin) {
	pt := POINT{mw.x, mw.y}
	sz := SIZE{int32(mw.w), int32(mw.h)}
	src := POINT{}
	blend := BLENDFUNCTION{SourceConstantAlpha: byte(math.Min(mw.alpha, 1) * 255), AlphaFormat: 1}
	pUpdateLayeredWindow.Call(mw.hwnd, 0, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)),
		mw.memDC, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
}

func (mw *menuWin) contains(sx, sy int32) bool {
	return sx >= mw.x && sy >= mw.y && sx < mw.x+int32(mw.w) && sy < mw.y+int32(mw.h)
}

func (mw *menuWin) hitTest(sx, sy int32) int {
	lx, ly := float64(sx-mw.x), float64(sy-mw.y)
	if lx < 0 || lx >= float64(mw.w) {
		return -1
	}
	for i, it := range mw.menu.items {
		if it.Sep || it.Header {
			continue
		}
		if ly >= it.y0 && ly < it.y1 {
			return i
		}
	}
	return -1
}

func (a *App) ensureMenuClasses() {
	if menuClassOK {
		return
	}
	menuWndProcC = windows.NewCallback(menuWndProc)
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	wc := WNDCLASSEXW{LpfnWndProc: menuWndProcC, HInstance: hinst, HCursor: cur, LpszClassName: u16("DGDockMenu")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	black, _, _ := pGetStockObject.Call(4)
	bc := WNDCLASSEXW{Style: 0x00020000, LpfnWndProc: pDefWindowProcW.Addr(), HInstance: hinst, HCursor: cur,
		HbrBackground: black, LpszClassName: u16("DGDockMenuBack")} // CS_DROPSHADOW
	bc.CbSize = uint32(unsafe.Sizeof(bc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&bc)))
	menuClassOK = true
}

// openMenuWin creates the windows for one menu level at screen position (x, y).
func (a *App) openMenuWin(m *macMenu, x, y int32, owner uintptr) *menuWin {
	a.ensureMenuClasses()
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cw, ch := layoutMenuPure(a.te, a.dock.Scale, m)
	W, H := int(cw), int(ch)
	mw := &menuWin{menu: m, w: W, h: H, x: x, y: y, hot: -1, open: -1, parent: -1}

	// acrylic backing window
	back, _, _ := pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TOPMOST, up(u16("DGDockMenuBack")), 0, WS_POPUP,
		uintptr(x), uintptr(y), uintptr(W), uintptr(H), owner, 0, hinst, 0)
	if back != 0 {
		corner := uint32(2) // DWMWCP_ROUND (8px, like Windows menus)
		pDwmSetWindowAttribute.Call(back, 33, uintptr(unsafe.Pointer(&corner)), 4)
		tint := uint32(0x99202020)
		if !a.menuDark() {
			tint = 0x99F3F3F3
		}
		policy := ACCENTPOLICY{AccentState: 4, GradientColor: tint} // ACCENT_ENABLE_ACRYLICBLURBEHIND
		data := WINCOMPATTRDATA{Attrib: 19, Data: uintptr(unsafe.Pointer(&policy)), Size: unsafe.Sizeof(policy)}
		ok := uintptr(0)
		if pSetWindowCompositionAttribute.Find() == nil && a.transparencyOn() {
			ok, _, _ = pSetWindowCompositionAttribute.Call(back, uintptr(unsafe.Pointer(&data)))
		}
		if ok == 0 {
			pDestroyWindowProc.Call(back)
			back = 0
		}
	}
	mw.back = back
	mw.acrylic = back != 0
	contentOwner := owner
	if back != 0 {
		contentOwner = back
	}
	hwnd, _, _ := pCreateWindowExW.Call(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST, up(u16("DGDockMenu")), 0, WS_POPUP,
		uintptr(x), uintptr(y), uintptr(W), uintptr(H), contentOwner, 0, hinst, 0)
	if hwnd == 0 {
		if back != 0 {
			pDestroyWindowProc.Call(back)
		}
		return nil
	}
	mw.hwnd = hwnd
	var bi BITMAPINFO
	bi.Header.BiSize = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.BiWidth = int32(W)
	bi.Header.BiHeight = -int32(H)
	bi.Header.BiPlanes = 1
	bi.Header.BiBitCount = 32
	mw.memDC, _, _ = pCreateCompatibleDC.Call(0)
	var bits uintptr
	mw.dib, _, _ = pCreateDIBSection.Call(mw.memDC, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if mw.dib == 0 || bits == 0 {
		a.closeMenuWin(mw)
		return nil
	}
	pSelectObject.Call(mw.memDC, mw.dib)
	mw.canvas = &image.RGBA{Pix: unsafe.Slice((*byte)(unsafe.Pointer(bits)), W*H*4), Stride: W * 4, Rect: image.Rect(0, 0, W, H)}
	mw.shown = time.Now()
	mw.alpha = 0.2
	a.renderMenu(mw)
	a.presentMenu(mw)
	if back != 0 {
		pShowWindow.Call(back, SW_SHOWNA)
	}
	pShowWindow.Call(hwnd, SW_SHOWNA)
	pSetTimer.Call(hwnd, 1, 15, 0)
	return mw
}

func (a *App) transparencyOn() bool {
	v, ok := readDWORD(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "EnableTransparency")
	return !ok || v != 0
}

func (a *App) closeMenuWin(mw *menuWin) {
	if mw.hwnd != 0 {
		pDestroyWindowProc.Call(mw.hwnd)
	}
	if mw.back != 0 {
		pDestroyWindowProc.Call(mw.back)
	}
	if mw.memDC != 0 {
		pDeleteDC.Call(mw.memDC)
	}
	if mw.dib != 0 {
		pDeleteObject.Call(mw.dib)
	}
}

// closeAbove closes every submenu deeper than level k.
func (a *App) closeAbove(k int) {
	ss := session
	for len(ss.stack) > k+1 {
		top := ss.stack[len(ss.stack)-1]
		ss.stack = ss.stack[:len(ss.stack)-1]
		a.closeMenuWin(top)
	}
	if mw := ss.stack[k]; mw.open != -1 {
		mw.open = -1
		a.renderMenu(mw)
		a.presentMenu(mw)
	}
}

func (a *App) openSub(k, idx int) {
	ss := session
	parent := ss.stack[k]
	it := parent.menu.items[idx]
	if it.Sub == nil || len(it.Sub.items) == 0 {
		return
	}
	a.closeAbove(k)
	s := a.dock.Scale
	cw, ch := layoutMenuPure(a.te, s, it.Sub)
	x := parent.x + int32(parent.w) - int32(4*s)
	if x+int32(cw) > a.mon.Right {
		x = parent.x - int32(cw) + int32(4*s)
	}
	y := parent.y + int32(it.y0) - int32(4*s)
	if y+int32(ch) > a.mon.Bottom {
		y = a.mon.Bottom - int32(ch)
	}
	if y < a.mon.Top {
		y = a.mon.Top
	}
	mw := a.openMenuWin(it.Sub, x, y, parent.hwnd)
	if mw == nil {
		return
	}
	mw.parent = idx
	parent.open = idx
	ss.stack = append(ss.stack, mw)
	a.renderMenu(parent)
	a.presentMenu(parent)
}

func (a *App) menuAt(sx, sy int32) int {
	ss := session
	for k := len(ss.stack) - 1; k >= 0; k-- {
		if ss.stack[k].contains(sx, sy) {
			return k
		}
	}
	return -1
}

func (a *App) setHot(mw *menuWin, h int) {
	if h != mw.hot {
		mw.hot = h
		a.renderMenu(mw)
		a.presentMenu(mw)
	}
}

func menuWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	ss := session
	if ss == nil || len(ss.stack) == 0 {
		r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	a := app
	root := ss.stack[0]
	switch uint32(msg) {
	case WM_TIMER:
		for _, mw := range ss.stack {
			if mw.hwnd == hwnd {
				if mw.alpha < 1 {
					mw.alpha = math.Min(1, 0.2+time.Since(mw.shown).Seconds()/0.11)
					a.presentMenu(mw)
				} else {
					pKillTimer(hwnd, 1)
				}
			}
		}
		return 0
	case WM_MOUSEACTIVATE:
		return MA_NOACTIVATE
	}
	if hwnd != root.hwnd {
		r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	switch uint32(msg) {
	case WM_MOUSEMOVE:
		sx, sy := getCursorPos()
		k := a.menuAt(sx, sy)
		if k < 0 {
			deep := ss.stack[len(ss.stack)-1]
			if deep.open == -1 {
				a.setHot(deep, -1)
			}
			return 0
		}
		mw := ss.stack[k]
		h := mw.hitTest(sx, sy)
		a.setHot(mw, h)
		if h >= 0 && mw.menu.items[h].Sub != nil && !mw.menu.items[h].Disabled {
			if mw.open != h {
				a.openSub(k, h)
			}
		} else if h >= 0 && len(ss.stack) > k+1 {
			a.closeAbove(k)
		}
		return 0
	case WM_LBUTTONUP, WM_RBUTTONUP:
		sx, sy := getCursorPos()
		k := a.menuAt(sx, sy)
		if k < 0 {
			if time.Since(root.shown) > 200*time.Millisecond || uint32(msg) == WM_LBUTTONUP {
				ss.done = true
			}
			return 0
		}
		if time.Since(root.shown) < 200*time.Millisecond && uint32(msg) == WM_RBUTTONUP {
			return 0 // the right-click that opened the menu
		}
		mw := ss.stack[k]
		if h := mw.hitTest(sx, sy); h >= 0 {
			it := mw.menu.items[h]
			if it.Disabled {
				return 0
			}
			if it.Sub != nil {
				a.openSub(k, h)
				return 0
			}
			ss.chosen = it
			ss.done = true
		}
		return 0
	case WM_LBUTTONDOWN, WM_RBUTTONDOWN:
		sx, sy := getCursorPos()
		if a.menuAt(sx, sy) < 0 {
			ss.done = true
		}
		return 0
	case WM_KEYDOWN:
		deep := ss.stack[len(ss.stack)-1]
		k := len(ss.stack) - 1
		switch wp {
		case 0x1B: // Esc closes one level
			if k == 0 {
				ss.done = true
			} else {
				a.closeAbove(k - 1)
			}
		case 0x25: // Left
			if k > 0 {
				a.closeAbove(k - 1)
			}
		case 0x27: // Right
			if deep.hot >= 0 && deep.menu.items[deep.hot].Sub != nil {
				a.openSub(k, deep.hot)
				nm := ss.stack[len(ss.stack)-1]
				for i, it := range nm.menu.items {
					if !it.Sep && !it.Header && !it.Disabled {
						a.setHot(nm, i)
						break
					}
				}
			}
		case 0x26, 0x28: // Up, Down
			n := len(deep.menu.items)
			dir := 1
			if wp == 0x26 {
				dir = -1
			}
			i := deep.hot
			if i < 0 && dir < 0 {
				i = 0
			}
			for c := 0; c < n; c++ {
				i = (i + dir + n) % n
				it := deep.menu.items[i]
				if !it.Sep && !it.Header && !it.Disabled {
					break
				}
			}
			a.setHot(deep, i)
		case 0x0D: // Enter
			if deep.hot >= 0 {
				it := deep.menu.items[deep.hot]
				if it.Sub != nil {
					a.openSub(k, deep.hot)
				} else if !it.Disabled {
					ss.chosen = it
					ss.done = true
				}
			}
		}
		return 0
	case WM_ACTIVATE:
		if wp&0xFFFF == 0 {
			ss.done = true
		}
	case WM_CAPTURECHANGED:
		if lp != hwnd {
			ss.done = true
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

// runMenu shows the menu next to the dock and blocks until it closes.
func (a *App) runMenu(m *macMenu) {
	if len(m.items) == 0 || session != nil {
		return
	}
	s := a.dock.Scale
	cw, ch := layoutMenuPure(a.te, s, m)
	W, H := cw, ch
	gap := 8 * s

	d := a.dock
	cx, cy := getCursorPos()
	mu, _ := d.toUV(float64(cx-a.winX), float64(cy-a.winY))
	fx, fy := d.toXY(mu, d.PlateV1+gap)
	ax, ay := float64(a.winX)+fx, float64(a.winY)+fy
	var x, y float64
	switch d.Orient {
	case OrientLeft:
		x, y = ax, float64(cy)-H/2
	case OrientRight:
		x, y = ax-W, float64(cy)-H/2
	default:
		x, y = float64(cx)-W/2, ay-H
	}
	x = math.Max(float64(a.mon.Left)+4, math.Min(x, float64(a.mon.Right)-W-4))
	y = math.Max(float64(a.mon.Top)+4, math.Min(y, float64(a.mon.Bottom)-H-4))

	session = &menuSession{}
	root := a.openMenuWin(m, int32(x), int32(y), a.hwnd)
	if root == nil {
		session = nil
		return
	}
	session.stack = []*menuWin{root}
	a.menuOpen = true
	d.Frozen = true
	pSetForegroundWindow.Call(root.hwnd)
	pSetCapture.Call(root.hwnd)

	var msg MSG
	quit := false
	for !session.done {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			quit = true
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	chosen := session.chosen
	stack := session.stack
	session = nil
	pReleaseCapture.Call()
	for i := len(stack) - 1; i >= 0; i-- {
		a.closeMenuWin(stack[i])
	}
	a.menuOpen = false
	d.Frozen = false
	a.lastLeave = time.Time{}
	if quit {
		pPostQuitMessage.Call(0)
		return
	}
	if chosen != nil && chosen.Action != nil {
		chosen.Action()
	}
	a.needRender = true
	a.tick()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ---------- menu contents ----------

func (a *App) smallIcon(path string) *IconSet {
	k := "small:" + path
	if is, ok := a.styled[k]; ok {
		return is
	}
	var is *IconSet
	if ov := a.cfg.IconOverrides[fileKey(path)]; ov != "" {
		is = a.overrideIcon(ov)
	} else if a.pack != nil {
		st, err := os.Stat(path)
		if d := a.packForFile(path, err == nil && st.IsDir()); d != "" {
			is = a.overrideIcon(a.pack.Path(d))
		}
	}
	if is == nil {
		if img := loadShellIcon(path, 48); img != nil {
			is = NewIconSet(img)
		}
	}
	a.styled[k] = is
	return is
}

func (a *App) itemMenu(it *Item) {
	m := &macMenu{}
	m.header(it.Name)

	if len(it.Windows) > 0 {
		for i, w := range it.Windows {
			if i >= 10 {
				break
			}
			hw := w
			t := truncate(windowText(hw), 60)
			if t == "" {
				t = it.Name
			}
			m.add(t, func() { activateWindow(hw) }).Icon = it.Icon
		}
		m.sep()
	}

	switch {
	case it.Kind == KindTrash:
		m.addG(GlyphOpen, "Open", func() { launchItem(it) })
		e := m.addG(GlyphDelete, "Empty Recycle Bin", func() { pSHEmptyRecycleBinW.Call(a.hwnd, 0, 0) })
		e.Disabled = recycleBinCount() == 0
	case it.IsDir:
		entries, _ := os.ReadDir(it.Target)
		type ent struct {
			name string
			t    time.Time
		}
		var list []ent
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || strings.EqualFold(e.Name(), "desktop.ini") {
				continue
			}
			if info, err := e.Info(); err == nil {
				list = append(list, ent{e.Name(), info.ModTime()})
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].t.After(list[j].t) })
		recent := m
		if len(list) > 0 {
			recent = m.sub(GlyphFile, "Recent items")
		}
		for i, e := range list {
			if i >= 15 {
				break
			}
			p := filepath.Join(it.Target, e.name)
			recent.add(truncate(e.name, 48), func() { shellOpen(p, "") }).Icon = a.smallIcon(p)
		}
		if len(list) == 0 {
			m.add("This folder is empty", nil).Disabled = true
		}
		m.addG(GlyphFolder, "Open in File Explorer", func() { shellOpen(it.Target, "") })
	case it.Kind == KindStart:
		m.addG(GlyphSearch, "Search", func() { a.showSearch() })
		m.addG(GlyphStart, "Open Windows Start", func() { launchItem(it) })
	default:
		if len(it.Windows) > 0 {
			m.addG(GlyphNewWindow, "New window", func() { a.launch(it) })
		} else {
			m.addG(GlyphOpen, "Open", func() { a.launch(it) })
		}
	}
	if it.Kind == KindFile && !it.IsDir {
		t := it.Target
		m.addG(GlyphExplorer, "Show in File Explorer", func() { shellOpen("explorer.exe", `/select,"`+t+`"`) })
	}

	m.sep()
	if it.Pin != nil {
		m.addG(GlyphBrush, "Change icon", func() {
			if a.pack != nil {
				a.openIconPicker(it.PinIdx)
			} else {
				a.changeIconDialog(it)
			}
		})
		if it.Pin.Icon != "" {
			m.addG(GlyphReset, "Reset icon", func() { a.setPinIcon(it, "") })
		}
	}
	if it.Pin != nil && it.Kind != KindTrash {
		idx := it.PinIdx
		m.addG(GlyphUnpin, "Remove from Dock", func() { a.removePinAt(idx) })
	} else if it.Kind == KindRunning {
		m.addG(GlyphPin, "Keep in Dock", func() { a.keepInDock(it) })
	}
	if len(it.Windows) > 0 {
		ws := append([]uintptr{}, it.Windows...)
		label := "Close window"
		if len(ws) > 1 {
			label = "Close all windows"
		}
		m.addG(GlyphClose, label, func() {
			for _, w := range ws {
				pPostMessageW.Call(w, WM_CLOSE, 0, 0)
			}
		})
	}
	m.sep()
	m.addG(GlyphSettings, "Dock settings", func() { a.openSettings() })
	a.runMenu(m)
}

func (a *App) dockMenu() {
	m := &macMenu{}
	if _, ver := updates.found(); ver > 0 {
		m.addG(GlyphReset, fmt.Sprintf("Update to version %d", ver), func() { a.startUpdate() })
		m.sep()
	}
	m.addG(GlyphSearch, "Search", func() { a.showSearch() }).Key = a.searchKeyName
	m.sep()
	if a.cfg.AutoHide {
		m.addG(GlyphView, "Always show the Dock", func() { a.setAutoHide(false) })
	} else {
		m.addG(GlyphHide, "Automatically hide the Dock", func() { a.setAutoHide(true) })
	}
	if a.cfg.HideTaskbar {
		m.addG(GlyphMonitor, "Show the Windows taskbar", func() { a.setHideTaskbar(false) })
	} else {
		m.addG(GlyphMonitor, "Hide the Windows taskbar", func() { a.setHideTaskbar(true) })
	}
	m.sep()
	pos := m.sub(GlyphDockBottom, "Position on screen")
	for _, o := range []struct {
		n, v string
		g    rune
	}{{"Bottom", "bottom", GlyphDockBottom}, {"Left", "left", GlyphDockLeft}, {"Right", "right", GlyphDockRight}} {
		v := o.v
		pos.addG(o.g, o.n, func() { a.setPosition(v) }).Checked = orientOf(a.cfg.Position) == orientOf(v)
	}
	sz := m.sub(GlyphSize, "Icon size")
	for _, o := range []struct {
		n string
		v int
	}{{"Small", 44}, {"Medium", 54}, {"Large", 64}, {"Extra large", 76}} {
		v := o.v
		sz.add(o.n, func() { a.setIconSize(v) }).Checked = a.cfg.IconSize == v
	}
	m.sep()
	m.addG(GlyphNewFolder, "Add folder…", func() { a.addFolderDialog(a.hwnd) })
	m.addG(GlyphAdd, "Add program or file…", func() { a.addFileDialog(a.hwnd) })
	m.sep()
	m.addG(GlyphSettings, "Dock settings", func() { a.openSettings() })
	m.addG(GlyphPower, "Quit DG-Dock", func() { a.quit() })
	a.runMenu(m)
}
