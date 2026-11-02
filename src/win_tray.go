//go:build windows

package main

// System tray icons in the dock.
//
// Apps add their notification-area icons with Shell_NotifyIcon, which finds the window
// of class "Shell_TrayWnd" and sends it a WM_COPYDATA. DG-Dock keeps a hidden window of
// that class at the top of the z-order on its own thread, so those messages reach it
// first: it remembers each icon (picture, tooltip, who to tell about clicks) and then
// passes every message on to Explorer's real taskbar, so Windows keeps working exactly
// as before. This is the same approach other taskbar replacements use.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pSendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	pSendNotifyMessageW       = user32.NewProc("SendNotifyMessageW")
	pAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	pInSendMessage            = user32.NewProc("InSendMessage")
	pGetIconInfo              = user32.NewProc("GetIconInfo")
	pDrawIconEx               = user32.NewProc("DrawIconEx")
)

const wmTray = WM_APP + 14

type trayIcon struct {
	key      string
	hwnd     uintptr
	uid      uint32
	callback uint32
	version  uint32
	tip      string
	exe      string // owning app, lower-case file name (e.g. "steam.exe")
	icon     *IconSet
	hidden   bool
	order    int
}

type trayStore struct {
	mu      sync.Mutex
	icons   map[string]*trayIcon
	next    int
	hwnd    uintptr // our Shell_TrayWnd
	notify  uintptr // dock window to tell about changes
	pending bool
	created uint32 // "TaskbarCreated"
}

var tray = &trayStore{icons: map[string]*trayIcon{}}

// explorerTray is Explorer's real taskbar window (not ours).
func explorerTray() uintptr {
	var prev uintptr
	for i := 0; i < 8; i++ {
		h, _, _ := pFindWindowExW.Call(0, prev, up(u16("Shell_TrayWnd")), 0)
		if h == 0 {
			return 0
		}
		if h != tray.hwnd {
			return h
		}
		prev = h
	}
	return 0
}

var trayProcCB = syscall.NewCallback(trayWndProc)

// startTray starts the tray host on its own thread (so apps adding icons never wait on
// the dock's drawing).
func (a *App) startTray() {
	if tray.hwnd != 0 {
		return
	}
	tray.notify = a.hwnd
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		hinst, _, _ := pGetModuleHandleW.Call(0)
		wc := WNDCLASSEXW{LpfnWndProc: trayProcCB, HInstance: hinst, LpszClassName: u16("Shell_TrayWnd")}
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		h, _, _ := pCreateWindowExW.Call(WS_EX_TOOLWINDOW|WS_EX_TOPMOST, up(u16("Shell_TrayWnd")), 0, WS_POPUP, 0, 0, 0, 0, 0, 0, hinst, 0)
		tray.hwnd = h
		close(ready)
		if h == 0 {
			return
		}
		tray.created = uint32(registerMsg("TaskbarCreated"))
		trayOnTop()
		pSetTimer.Call(h, 1, 1000, 0)
		// ask every app to add its icons again, so the ones already there reach us too
		pSendNotifyMessageW.Call(0xFFFF, uintptr(tray.created), 0, 0)
		var msg MSG
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	<-ready
}

// stopTray hands the tray back to Explorer.
func stopTray() {
	if h := tray.hwnd; h != 0 {
		pPostMessageW.Call(h, WM_CLOSE, 0, 0)
	}
}

func registerMsg(name string) uintptr {
	r, _, _ := pRegisterWindowMessageW.Call(up(u16(name)))
	return r
}

// trayOnTop keeps our window above Explorer's (FindWindow returns the topmost one), and
// the same size and place as the real taskbar for anything that asks where it is.
func trayOnTop() {
	h := tray.hwnd
	var r RECT
	if ex := explorerTray(); ex != 0 {
		pGetWindowRect.Call(ex, uintptr(unsafe.Pointer(&r)))
	}
	pSetWindowPos.Call(h, ^uintptr(0), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), SWP_NOACTIVATE)
}

// forward passes a message on to Explorer's taskbar and returns its answer.
func trayForward(msg uint32, wp, lp uintptr) uintptr {
	ex := explorerTray()
	if ex == 0 {
		return 0
	}
	var res uintptr
	r, _, _ := pSendMessageTimeoutW.Call(ex, uintptr(msg), wp, lp, 0x0002, 3000, uintptr(unsafe.Pointer(&res))) // SMTO_ABORTIFHUNG
	if r == 0 {
		return 0
	}
	return res
}

func trayWndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case 0x004A: // WM_COPYDATA
		type copyData struct {
			Data uintptr
			Size uint32
			Ptr  uintptr
		}
		cd := (*copyData)(unsafe.Pointer(lp))
		res := trayForward(msg, wp, lp)
		if cd != nil && cd.Data == 1 && cd.Ptr != 0 && cd.Size >= 8+24 {
			if tray.handle(unsafe.Slice((*byte)(unsafe.Pointer(cd.Ptr)), cd.Size)) {
				return 1 // Shell_NotifyIcon succeeded as far as the app is concerned
			}
		}
		return res
	case WM_TIMER:
		trayOnTop() // stay first in line, and the same size as the taskbar
		tray.prune()
		return 0
	case WM_CLOSE:
		pDestroyWindowProc.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		tray.hwnd = 0
		tray.mu.Lock()
		tray.icons = map[string]*trayIcon{}
		tray.mu.Unlock()
		pPostQuitMessage.Call(0)
		return 0
	case 0x0111: // WM_COMMAND (e.g. "show desktop" sent by other apps)
		return trayForward(msg, wp, lp)
	}
	if msg >= 0x0400 && msg < 0x8000 {
		// the taskbar's own private messages go on to it (registered/broadcast messages
		// don't: Explorer gets those itself)
		if r, _, _ := pInSendMessage.Call(); r != 0 {
			return trayForward(msg, wp, lp)
		}
		if ex := explorerTray(); ex != 0 {
			pPostMessageW.Call(ex, uintptr(msg), wp, lp)
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

// NOTIFYICONDATA as sent to the taskbar (32-bit handles, same on 64-bit Windows).
const (
	nidHwnd     = 4
	nidUID      = 8
	nidFlags    = 12
	nidCallback = 16
	nidIcon     = 20
	nidTip      = 24  // 128 WCHARs
	nidState    = 280 // dwState, then dwStateMask
	nidVersion  = 800
	nidGUID     = 936
)

// handle reads one Shell_NotifyIcon call. Returns true when it was understood.
func (t *trayStore) handle(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	u32 := func(off int) uint32 {
		if off+4 > len(b) {
			return 0
		}
		return *(*uint32)(unsafe.Pointer(&b[off]))
	}
	cmd := u32(4)
	nid := b[8:]
	n32 := func(off int) uint32 {
		if off+4 > len(nid) {
			return 0
		}
		return *(*uint32)(unsafe.Pointer(&nid[off]))
	}
	flags := n32(nidFlags)
	key := fmt.Sprintf("%x/%d", n32(nidHwnd), n32(nidUID))
	if flags&0x20 != 0 && nidGUID+16 <= len(nid) { // NIF_GUID
		g := (*windows.GUID)(unsafe.Pointer(&nid[nidGUID]))
		key = g.String()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ic := t.icons[key]
	switch cmd {
	case 0, 1: // NIM_ADD, NIM_MODIFY
		if ic == nil {
			// (a modify for an icon added before we were listening counts as an add)
			t.next++
			ic = &trayIcon{key: key, order: t.next}
			t.icons[key] = ic
		}
		ic.hwnd = uintptr(n32(nidHwnd))
		ic.uid = n32(nidUID)
		if ic.exe == "" {
			ic.exe = strings.ToLower(filepath.Base(windowExe(ic.hwnd)))
		}
		if flags&0x1 != 0 { // NIF_MESSAGE
			ic.callback = n32(nidCallback)
		}
		if flags&0x2 != 0 { // NIF_ICON
			if h := uintptr(n32(nidIcon)); h != 0 {
				if img := hiconImage(h); img != nil {
					ic.icon = NewIconSet(img)
				}
			} else {
				ic.icon = nil
			}
		}
		if flags&0x4 != 0 && nidTip+256 <= len(nid) { // NIF_TIP
			ic.tip = windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&nid[nidTip])), 128))
		}
		if flags&0x8 != 0 { // NIF_STATE
			st, mask := n32(nidState), n32(nidState+4)
			if mask&1 != 0 {
				ic.hidden = st&1 != 0
			}
		}
	case 2: // NIM_DELETE
		if ic == nil {
			return true
		}
		delete(t.icons, key)
	case 4: // NIM_SETVERSION
		if ic != nil {
			ic.version = n32(nidVersion)
		}
		return true
	default:
		return true
	}
	t.changed()
	return true
}

// changed tells the dock (once per batch of changes).
func (t *trayStore) changed() {
	if !t.pending && t.notify != 0 {
		t.pending = true
		pPostMessageW.Call(t.notify, wmTray, 0, 0)
	}
}

// prune drops icons whose app has gone without removing them.
func (t *trayStore) prune() {
	t.mu.Lock()
	defer t.mu.Unlock()
	gone := false
	for k, ic := range t.icons {
		if r, _, _ := pIsWindow.Call(ic.hwnd); r == 0 {
			delete(t.icons, k)
			gone = true
		}
	}
	if gone {
		t.changed()
	}
}

// list is the visible icons, in the order they were added.
func (t *trayStore) list() []trayIcon {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = false
	var out []trayIcon
	for _, ic := range t.icons {
		if ic.exe == "explorer.exe" && !strings.Contains(strings.ToLower(ic.tip), "remove") && !strings.Contains(strings.ToLower(ic.tip), "eject") {
			continue // Windows' battery / volume / network icons: the dock's widgets cover those
		}
		if !ic.hidden && ic.icon != nil {
			out = append(out, *ic)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].order < out[j].order })
	return out
}

func (t *trayStore) get(key string) (trayIcon, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ic, ok := t.icons[key]
	if !ok {
		return trayIcon{}, false
	}
	return *ic, true
}

// trayClick tells an app its tray icon was clicked (left) or right-clicked, the way the
// taskbar would, so it opens its window or its menu at the pointer.
func trayClick(key string, right bool) {
	ic, ok := tray.get(key)
	if !ok || ic.callback == 0 {
		return
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(ic.hwnd, uintptr(unsafe.Pointer(&pid)))
	pAllowSetForegroundWindow.Call(uintptr(pid))
	cx, cy := getCursorPos()
	send := func(m uint32) {
		var wp, lp uintptr
		if ic.version >= 4 {
			wp = uintptr(uint16(cx)) | uintptr(uint16(cy))<<16
			lp = uintptr(m&0xFFFF) | uintptr(ic.uid&0xFFFF)<<16
		} else {
			wp, lp = uintptr(ic.uid), uintptr(m)
		}
		pSendNotifyMessageW.Call(ic.hwnd, uintptr(ic.callback), wp, lp)
	}
	if right {
		send(0x0204) // WM_RBUTTONDOWN
		send(0x0205) // WM_RBUTTONUP
		if ic.version >= 4 {
			send(0x007B) // WM_CONTEXTMENU
		}
	} else {
		send(0x0201) // WM_LBUTTONDOWN
		send(0x0202) // WM_LBUTTONUP
		if ic.version >= 4 {
			send(0x0400) // NIN_SELECT
		}
	}
}

// trayTip is the first line of a tooltip (they often carry a status on later lines).
func trayTip(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" {
		s = "App"
	}
	return s
}

// hiconImage draws an icon handle (from any process) into a premultiplied BGRA image.
// Icons without an alpha channel are drawn on black and on white to recover it.
func hiconImage(h uintptr) *image.RGBA {
	type iconInfo struct {
		FIcon    int32
		XHot     uint32
		YHot     uint32
		HbmMask  uintptr
		HbmColor uintptr
	}
	var ii iconInfo
	if r, _, _ := pGetIconInfo.Call(h, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil
	}
	size := 0
	if ii.HbmColor != 0 {
		var bm BITMAP
		pGetObjectW.Call(ii.HbmColor, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
		size = int(bm.BmWidth)
		pDeleteObject.Call(ii.HbmColor)
	}
	if ii.HbmMask != 0 {
		if size == 0 {
			var bm BITMAP
			pGetObjectW.Call(ii.HbmMask, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
			size = int(bm.BmWidth)
		}
		pDeleteObject.Call(ii.HbmMask)
	}
	if size <= 0 || size > 256 {
		size = 32
	}
	draw := func(fill byte) []byte {
		var bi BITMAPINFO
		bi.Header.BiSize = uint32(unsafe.Sizeof(bi.Header))
		bi.Header.BiWidth = int32(size)
		bi.Header.BiHeight = -int32(size)
		bi.Header.BiPlanes = 1
		bi.Header.BiBitCount = 32
		dc, _, _ := pCreateCompatibleDC.Call(0)
		defer pDeleteDC.Call(dc)
		var bits uintptr
		dib, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
		if dib == 0 || bits == 0 {
			return nil
		}
		defer pDeleteObject.Call(dib)
		old, _, _ := pSelectObject.Call(dc, dib)
		px := unsafe.Slice((*byte)(unsafe.Pointer(bits)), size*size*4)
		for i := range px {
			px[i] = fill
		}
		pDrawIconEx.Call(dc, 0, 0, h, uintptr(size), uintptr(size), 0, 0, 0x0003) // DI_NORMAL
		pSelectObject.Call(dc, old)
		return append([]byte(nil), px...)
	}
	black := draw(0)
	if black == nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	hasAlpha := false
	for i := 3; i < len(black); i += 4 {
		if black[i] != 0 {
			hasAlpha = true
			break
		}
	}
	if hasAlpha {
		copy(img.Pix, black)
		return img
	}
	white := draw(255)
	if white == nil {
		return nil
	}
	for i := 0; i < len(black); i += 4 {
		// on black a pixel is c·a; on white it's c·a + (1-a)
		d := int(white[i+1]) - int(black[i+1])
		a := 255 - d
		if a < 0 {
			a = 0
		} else if a > 255 {
			a = 255
		}
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = min(black[i], byte(a)), min(black[i+1], byte(a)), min(black[i+2], byte(a)), byte(a)
	}
	return img
}

// onTrayChanged refreshes the tray widget (and its card, if it's open).
func (a *App) onTrayChanged() {
	it := a.items["w:tray"]
	tray.list() // (clears the "changed" flag)
	if it == nil {
		return
	}
	if d := a.dock; d.PopItem == it {
		d.Pop = a.popContent(it)
	}
	a.needRender = true
}

// closePop puts the hover card away straight away (before an app's menu opens).
func (a *App) closePop() {
	d := a.dock
	d.PopItem, d.PopT, d.PopTarget = nil, 0, 0
	d.PopHot = -1
	a.needRender = true
	a.render()
}

// openWindowsStart opens the Windows Start menu.
func openWindowsStart() {
	pKeybdEvent.Call(0x5B, 0, 0, 0)
	pKeybdEvent.Call(0x5B, 0, 2, 0)
}

// windowExe is the full path of the program that owns a window.
func windowExe(h uintptr) string {
	var pid uint32
	pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(ph)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(ph, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// trayAppName is a friendly name for a tray icon's app: its tooltip's first line, or
// the program's name.
func trayAppName(ic trayIcon) string {
	if t := trayTip(ic.tip); t != "App" {
		return t
	}
	return strings.TrimSuffix(ic.exe, ".exe")
}

// trayRowIcon is the picture for a tray icon in the card: your own icon if you've set
// one, else the icon pack's, else the app's own tray icon. big = a full-size tile icon.
func (a *App) trayRowIcon(ic trayIcon) (is *IconSet, big bool) {
	if p := a.cfg.IconOverrides["tray:"+ic.exe]; p != "" {
		if is := a.overrideIcon(p); is != nil {
			return is, true
		}
	}
	if a.pack != nil {
		names := []string{trayTip(ic.tip), strings.TrimSuffix(ic.exe, ".exe")}
		if t := trayTip(ic.tip); strings.HasPrefix(strings.ToLower(t), "windows ") {
			names = append(names, t[8:]) // "Windows Security" → "Security"
		}
		for _, n := range names {
			if d := a.pack.Match(n); d != "" {
				if is := a.overrideIcon(a.pack.Path(d)); is != nil {
					return is, true
				}
			}
		}
	}
	return ic.icon, false
}

// overrideIcon loads (and keeps) a full-size icon from a file.
func (a *App) overrideIcon(path string) *IconSet {
	k := "ov:" + path
	if is, ok := a.styled[k]; ok {
		return is
	}
	var is *IconSet
	if img := a.customIcon(path); img != nil {
		is = NewIconSet(img)
	}
	a.styled[k] = is
	return is
}

// setIconOverride sets (path) or clears ("") the custom icon for a tray app or a file.
func (a *App) setIconOverride(key, path string) {
	if a.cfg.IconOverrides == nil {
		a.cfg.IconOverrides = map[string]string{}
	}
	if path == "" {
		delete(a.cfg.IconOverrides, key)
	} else {
		a.cfg.IconOverrides[key] = path
	}
	a.saveConfig()
	if strings.HasPrefix(key, "file:") {
		delete(a.styled, "small:"+key[5:])
		// the card keys files by their real path; drop any cached copy whatever its case
		for k := range a.styled {
			if strings.HasPrefix(k, "small:") && strings.EqualFold(k[6:], key[5:]) {
				delete(a.styled, k)
			}
		}
	}
	if d := a.dock; d.PopItem != nil {
		d.Pop = a.popContent(d.PopItem)
	}
	a.needRender = true
	a.render()
}

// fileKey is the override key for a file or folder shown in a folder card.
func fileKey(p string) string { return "file:" + strings.ToLower(filepath.Clean(p)) }

// packForFile finds an icon-pack icon for a file or folder in a folder card: by its
// name ("Steam.lnk" → steam), then a few well-known folders, then a plain folder icon.
func (a *App) packForFile(p string, dir bool) string {
	if a.pack == nil {
		return ""
	}
	name := filepath.Base(p)
	if !dir {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	low := strings.ToLower(name)
	try := []string{name}
	if strings.HasPrefix(low, "my ") {
		try = append(try, name[3:])
	}
	if dir {
		switch strings.TrimPrefix(low, "my ") {
		case "music":
			try = append(try, "music", "samsung_music", "apple_music", "google_youtube_music")
		case "pictures", "photos", "screenshots":
			try = append(try, "photos", "gallery", "google_photos")
		case "videos", "movies":
			try = append(try, "videos", "video_player")
		case "documents":
			try = append(try, "documents")
		case "downloads":
			try = append(try, "downloads")
		case "desktop":
			try = append(try, "desktop", "launcher")
		}
	}
	for _, n := range try {
		if d := a.pack.Match(n); d != "" {
			return d
		}
		if a.pack.has(n) {
			return n
		}
	}
	if dir {
		for _, n := range []string{"folder", "files", "file_manager", "my_files"} {
			if a.pack.has(n) {
				return n
			}
		}
	}
	return ""
}

// changeKeyIcon lets you pick a new icon for a tray app or a file in a folder card: from
// the icon pack when there is one, otherwise from a file.
func (a *App) changeKeyIcon(key, name string) {
	if a.pack == nil {
		a.keyIconDialog(key, name)
		return
	}
	a.pendingKey = [2]string{key, name}
	a.openSettings()
	if a.settings != nil {
		a.settings.eval("window.mdCheckPending && window.mdCheckPending()")
	}
}

func (a *App) keyIconDialog(key, name string) {
	p := pickPath(a.hwnd, false, "Choose a new icon for "+name, [][2]string{
		{"Icons and images", "*.png;*.ico;*.jpg;*.jpeg;*.exe;*.dll;*.lnk"},
		{"All files", "*.*"},
	})
	if p == "" {
		return
	}
	if a.customIcon(p) == nil {
		messageBox("That file doesn't contain an icon or image DG-Dock can use.", appName)
		return
	}
	a.setIconOverride(key, p)
}

// trayMenu is the tray tile's right-click menu: change any tray app's icon.
func (a *App) trayMenu() {
	m := &macMenu{}
	icons := tray.list()
	if len(icons) == 0 {
		m.header("Nothing in the system tray yet")
	} else {
		m.header("Change a system tray icon")
		seen := map[string]bool{}
		for _, ic := range icons {
			if seen[ic.exe] || ic.exe == "" {
				continue
			}
			seen[ic.exe] = true
			key, name := "tray:"+ic.exe, trayAppName(ic)
			sm := m.sub(GlyphBrush, name)
			sm.addG(GlyphBrush, "Change icon…", func() { a.changeKeyIcon(key, name) })
			if a.cfg.IconOverrides[key] != "" {
				sm.addG(GlyphReset, "Use original icon", func() { a.setIconOverride(key, "") })
			}
		}
	}
	m.sep()
	m.addG(GlyphSettings, "Dock settings", func() { a.openSettings() })
	a.runMenu(m)
}

// stackFileMenu is the shift + right-click menu for a tile in a folder card.
func (a *App) stackFileMenu(p string) {
	key, name := fileKey(p), filepath.Base(p)
	m := &macMenu{}
	m.header(name)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		m.addG(GlyphExplorer, "Open in File Explorer", func() { shellOpen(p, "") })
	} else {
		m.addG(GlyphOpen, "Open", func() { shellOpen(p, "") })
		m.addG(GlyphExplorer, "Show in File Explorer", func() { shellOpen("explorer.exe", `/select,"`+p+`"`) })
	}
	m.sep()
	m.addG(GlyphBrush, "Change icon…", func() { a.changeKeyIcon(key, name) })
	if a.cfg.IconOverrides[key] != "" {
		m.addG(GlyphReset, "Use original icon", func() { a.setIconOverride(key, "") })
	}
	a.runMenu(m)
}

// protectedExe: Windows' own programs that must never be closed from the dock.
func protectedExe(exe string) bool {
	switch exe {
	case "explorer.exe", "dg-dock.exe", "securityhealthsystray.exe", "csrss.exe", "winlogon.exe",
		"svchost.exe", "dwm.exe", "sihost.exe", "ctfmon.exe", "lsass.exe", "services.exe":
		return true
	}
	return strings.HasPrefix(exe, "dg-dock")
}

// quitTrayApp asks a tray app to close, the polite way first: it's sent the same "close"
// message as when Windows shuts down, to all its windows. If it's still running a few
// seconds later (many tray apps just hide when asked), you're offered "Force quit".
func quitTrayApp(ic trayIcon, name string) {
	var pid uint32
	pGetWindowThreadProcessId.Call(ic.hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 || pid == uint32(os.Getpid()) {
		return
	}
	// every top-level window that belongs to the app, including its hidden tray window
	enumResults = enumResults[:0]
	pEnumWindows.Call(enumCB, 0)
	var wins []uintptr
	for _, h := range enumResults {
		var p uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&p)))
		if p == pid {
			wins = append(wins, h)
		}
	}
	if len(wins) == 0 {
		wins = []uintptr{ic.hwnd}
	}
	for _, h := range wins {
		pPostMessageW.Call(h, 0x0011, 0, 0)  // WM_QUERYENDSESSION
		pPostMessageW.Call(h, 0x0016, 1, 0)  // WM_ENDSESSION (yes, ending)
		pPostMessageW.Call(h, WM_CLOSE, 0, 0)
	}
	go func() {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			return
		}
		defer windows.CloseHandle(h)
		if ev, _ := windows.WaitForSingleObject(h, 4000); ev == windows.WAIT_OBJECT_0 {
			return // it closed
		}
		if askYesNo(name+" is still running — it may have just hidden itself.\n\nForce it to quit? Anything unsaved in "+name+" will be lost.", "Quit "+name) {
			windows.TerminateProcess(h, 1)
		}
	}()
}

// trayRowMenu is the right-click menu for an app in the tray card.
func (a *App) trayRowMenu(k string) {
	ic, ok := tray.get(k)
	if !ok {
		return
	}
	key, name := "tray:"+ic.exe, trayAppName(ic)
	m := &macMenu{}
	m.header(name)
	m.addG(GlyphOpen, "Open", func() { a.closePop(); trayClick(k, false) })
	m.addG(GlyphChevron, name+" menu", func() { a.closePop(); trayClick(k, true) })
	m.sep()
	m.addG(GlyphBrush, "Change icon…", func() { a.changeKeyIcon(key, name) })
	if a.cfg.IconOverrides[key] != "" {
		m.addG(GlyphReset, "Use original icon", func() { a.setIconOverride(key, "") })
	}
	if ic.exe != "" && !protectedExe(ic.exe) {
		m.sep()
		m.addG(GlyphClose, "Quit "+name, func() { a.closePop(); quitTrayApp(ic, name) })
	}
	a.runMenu(m)
}
