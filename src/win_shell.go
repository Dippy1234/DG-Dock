//go:build windows

package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	IID_IShellItemImageFactory = mustGUID("{bcc18b79-ba16-442f-80c4-8a59c30c463b}")
	IID_IShellItem             = mustGUID("{43826d1e-e718-42ee-bc55-a1e261c37bfe}")
	IID_IShellItem2            = mustGUID("{7e9fb0d3-919f-4307-ab2e-9b1860310c93}")
	IID_IEnumShellItems        = mustGUID("{70629033-e363-4a28-a567-0db78006e6d7}")
	BHID_EnumItems             = mustGUID("{94f60519-2850-4924-aa5a-d15e84868039}")
	IID_IPropertyStore         = mustGUID("{886d8eeb-8cf2-4446-8d02-cdba1dbdcf99}")
	CLSID_ShellLink            = mustGUID("{00021401-0000-0000-C000-000000000046}")
	IID_IShellLinkW            = mustGUID("{000214F9-0000-0000-C000-000000000046}")
	IID_IPersistFile           = mustGUID("{0000010b-0000-0000-C000-000000000046}")

	PKEY_AppUserModel_ID        = PROPERTYKEY{mustGUID("{9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3}"), 5}
	PKEY_Link_TargetParsingPath = PROPERTYKEY{mustGUID("{B9B4B3FC-2B51-4A42-B5D8-324146AFCF25}"), 2}
)

const recycleBinParsing = "::{645FF040-5081-101B-9F08-00AA002F954E}"

func normPath(p string) string {
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(p))
}

// ---------- icons ----------

func createShellItem(parsing string, iid *windows.GUID) uintptr {
	var obj uintptr
	hr, _, _ := pSHCreateItemFromParsingName.Call(up(u16(parsing)), 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if !hresultOK(hr) {
		return 0
	}
	return obj
}

// loadShellIcon returns a premultiplied BGRA image (stored in image.RGBA) or nil.
func loadShellIcon(parsing string, size int) *image.RGBA {
	f := createShellItem(parsing, &IID_IShellItemImageFactory)
	if f == 0 {
		return nil
	}
	defer comRelease(f)
	const SIIGBF_ICONONLY = 0x4
	sz := uintptr(uint32(size)) | uintptr(uint32(size))<<32
	var hbm uintptr
	hr := comCall(f, 3, sz, SIIGBF_ICONONLY, uintptr(unsafe.Pointer(&hbm)))
	if !hresultOK(hr) || hbm == 0 {
		return nil
	}
	defer pDeleteObject.Call(hbm)
	img := hbitmapToImage(hbm)
	if img == nil {
		return nil
	}
	return cropToContent(img)
}

func hbitmapToImage(hbm uintptr) *image.RGBA {
	var bm BITMAP
	if r, _, _ := pGetObjectW.Call(hbm, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil
	}
	w, h := int(bm.BmWidth), int(bm.BmHeight)
	if h < 0 {
		h = -h
	}
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 {
		return nil
	}
	var bi BITMAPINFO
	bi.Header.BiSize = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.BiWidth = int32(w)
	bi.Header.BiHeight = -int32(h) // top-down
	bi.Header.BiPlanes = 1
	bi.Header.BiBitCount = 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	hdc, _, _ := pGetDC.Call(0)
	r, _, _ := pGetDIBits.Call(hdc, hbm, 0, uintptr(h), uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&bi)), 0)
	pReleaseDC.Call(0, hdc)
	if r == 0 {
		return nil
	}
	// fix alpha: all-zero alpha => opaque; non-premultiplied => premultiply
	pix := img.Pix
	anyAlpha := false
	notPremul := false
	for i := 0; i < len(pix); i += 4 {
		a := pix[i+3]
		if a != 0 {
			anyAlpha = true
		}
		if pix[i] > a || pix[i+1] > a || pix[i+2] > a {
			notPremul = true
		}
	}
	if !anyAlpha {
		for i := 0; i < len(pix); i += 4 {
			pix[i+3] = 255
		}
	} else if notPremul {
		for i := 0; i < len(pix); i += 4 {
			a := uint32(pix[i+3])
			pix[i] = uint8(uint32(pix[i]) * a / 255)
			pix[i+1] = uint8(uint32(pix[i+1]) * a / 255)
			pix[i+2] = uint8(uint32(pix[i+2]) * a / 255)
		}
	}
	return img
}

// Small legacy icons come back centred in a big transparent canvas; crop them.
func cropToContent(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Dx(), b.Dy(), -1, -1
	for y := 0; y < b.Dy(); y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < b.Dx(); x++ {
			if row[x*4+3] > 8 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return nil
	}
	cw, ch := maxX-minX+1, maxY-minY+1
	side := cw
	if ch > side {
		side = ch
	}
	if float64(side) >= float64(b.Dx())*0.55 {
		return img
	}
	// square crop centred on content, with a little margin
	side = int(float64(side) * 1.08)
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	x0, y0 := cx-side/2, cy-side/2
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		sy := y0 + y
		if sy < 0 || sy >= b.Dy() {
			continue
		}
		for x := 0; x < side; x++ {
			sx := x0 + x
			if sx < 0 || sx >= b.Dx() {
				continue
			}
			copy(out.Pix[y*out.Stride+x*4:y*out.Stride+x*4+4], img.Pix[sy*img.Stride+sx*4:sy*img.Stride+sx*4+4])
		}
	}
	return out
}

// ---------- names & paths ----------

func shellDisplayName(parsing string) string {
	si := createShellItem(parsing, &IID_IShellItem)
	if si == 0 {
		return ""
	}
	defer comRelease(si)
	var p uintptr
	if hr := comCall(si, 5, 0, uintptr(unsafe.Pointer(&p))); !hresultOK(hr) || p == 0 {
		return ""
	}
	s := u16ToString(p)
	pCoTaskMemFree.Call(p)
	return s
}

func shellItemString(si2 uintptr, key *PROPERTYKEY) string {
	var p uintptr
	if hr := comCall(si2, 17, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&p))); !hresultOK(hr) || p == 0 {
		return ""
	}
	s := u16ToString(p)
	pCoTaskMemFree.Call(p)
	return s
}

// appTargetPath finds the exe behind an AppsFolder entry (desktop apps only).
func appTargetPath(id string) string {
	si2 := createShellItem(`shell:AppsFolder\`+id, &IID_IShellItem2)
	if si2 != 0 {
		p := shellItemString(si2, &PKEY_Link_TargetParsingPath)
		comRelease(si2)
		if strings.HasSuffix(strings.ToLower(p), ".exe") {
			return p
		}
	}
	// "{KNOWNFOLDERID}\sub\path.exe"
	if strings.HasPrefix(id, "{") {
		if i := strings.Index(id, "}"); i > 0 {
			g, err := windows.GUIDFromString(id[:i+1])
			if err == nil {
				var p uintptr
				hr, _, _ := pSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&g)), 0, 0, uintptr(unsafe.Pointer(&p)))
				if hresultOK(hr) && p != 0 {
					base := u16ToString(p)
					pCoTaskMemFree.Call(p)
					return filepath.Join(base, strings.TrimLeft(id[i+1:], `\`))
				}
			}
		}
	}
	if len(id) > 2 && id[1] == ':' {
		return id
	}
	return ""
}

func resolveShortcut(lnk string) string {
	var sl uintptr
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_ShellLink)), 0, 1,
		uintptr(unsafe.Pointer(&IID_IShellLinkW)), uintptr(unsafe.Pointer(&sl)))
	if !hresultOK(hr) || sl == 0 {
		return ""
	}
	defer comRelease(sl)
	var pf uintptr
	if hr := comCall(sl, 0, uintptr(unsafe.Pointer(&IID_IPersistFile)), uintptr(unsafe.Pointer(&pf))); !hresultOK(hr) || pf == 0 {
		return ""
	}
	defer comRelease(pf)
	if hr := comCall(pf, 5, up(u16(lnk)), 0); !hresultOK(hr) {
		return ""
	}
	buf := make([]uint16, 1024)
	if hr := comCall(sl, 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0); !hresultOK(hr) {
		return ""
	}
	return windows.UTF16ToString(buf)
}

func fileDescription(exe string) string {
	p := u16(exe)
	n, _, _ := pGetFileVersionInfoSizeW.Call(up(p), 0)
	if n == 0 {
		return ""
	}
	data := make([]byte, n)
	if r, _, _ := pGetFileVersionInfoW.Call(up(p), 0, n, uintptr(unsafe.Pointer(&data[0]))); r == 0 {
		return ""
	}
	var tr uintptr
	var trLen uint32
	langs := []string{}
	if r, _, _ := pVerQueryValueW.Call(uintptr(unsafe.Pointer(&data[0])), up(u16(`\VarFileInfo\Translation`)),
		uintptr(unsafe.Pointer(&tr)), uintptr(unsafe.Pointer(&trLen))); r != 0 && trLen >= 4 {
		lang := *(*uint16)(unsafe.Pointer(tr))
		cp := *(*uint16)(unsafe.Pointer(tr + 2))
		langs = append(langs, fmt.Sprintf("%04x%04x", lang, cp))
	}
	langs = append(langs, "040904b0", "040904e4", "04090000")
	for _, l := range langs {
		var v uintptr
		var vl uint32
		if r, _, _ := pVerQueryValueW.Call(uintptr(unsafe.Pointer(&data[0])), up(u16(`\StringFileInfo\`+l+`\FileDescription`)),
			uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&vl))); r != 0 && vl > 1 {
			s := strings.TrimSpace(u16ToString(v))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func exeDisplayName(exe string) string {
	if d := fileDescription(exe); d != "" && len(d) < 60 {
		return d
	}
	b := filepath.Base(exe)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

// ---------- running windows ----------

type WinInfo struct {
	Hwnd  uintptr
	Exe   string // normalised
	AUMID string
	Title string
	Min   bool
}

type procInfo struct {
	exe   string
	aumid string
}

var (
	procCache   = map[uint32]procInfo{}
	enumResults []uintptr
	enumCB      = syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
		enumResults = append(enumResults, hwnd)
		return 1
	})
	ownPID = uint32(os.Getpid())
)

var skipClasses = map[string]bool{
	"Progman": true, "WorkerW": true, "Shell_TrayWnd": true, "Shell_SecondaryTrayWnd": true,
	"Windows.UI.Core.CoreWindow": true, "MacDockWindow": true, "MacDockBlur": true, "DGDockWindow": true, "DGDockBlur": true,
}

var skipExes = map[string]bool{
	"textinputhost.exe": true, "shellexperiencehost.exe": true, "searchhost.exe": true,
	"startmenuexperiencehost.exe": true, "lockapp.exe": true, "searchapp.exe": true,
}

func processInfo(pid uint32) procInfo {
	if pi, ok := procCache[pid]; ok {
		return pi
	}
	pi := procInfo{}
	h, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid))
	if h != 0 {
		buf := make([]uint16, 1024)
		n := uint32(len(buf))
		if r, _, _ := pQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r != 0 {
			pi.exe = normPath(windows.UTF16ToString(buf[:n]))
		}
		if pGetApplicationUserModelId.Find() == nil {
			ab := make([]uint16, 512)
			al := uint32(len(ab))
			if r, _, _ := pGetApplicationUserModelId.Call(h, uintptr(unsafe.Pointer(&al)), uintptr(unsafe.Pointer(&ab[0]))); r == 0 {
				pi.aumid = windows.UTF16ToString(ab)
			}
		}
		pCloseHandle.Call(h)
	}
	procCache[pid] = pi
	return pi
}

func windowAUMID(hwnd uintptr) string {
	var ps uintptr
	hr, _, _ := pSHGetPropertyStoreForWindow.Call(hwnd, uintptr(unsafe.Pointer(&IID_IPropertyStore)), uintptr(unsafe.Pointer(&ps)))
	if !hresultOK(hr) || ps == 0 {
		return ""
	}
	defer comRelease(ps)
	var pv PROPVARIANT
	if hr := comCall(ps, 5, uintptr(unsafe.Pointer(&PKEY_AppUserModel_ID)), uintptr(unsafe.Pointer(&pv))); !hresultOK(hr) {
		return ""
	}
	s := ""
	if pv.Vt == 31 { // VT_LPWSTR
		s = u16ToString(pv.Val)
	}
	pPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	return s
}

func isCloaked(hwnd uintptr) bool {
	var c uint32
	r, _, _ := pDwmGetWindowAttribute.Call(hwnd, 14, uintptr(unsafe.Pointer(&c)), 4)
	return r == 0 && c != 0
}

// listAppWindows returns taskbar-style app windows in z-order (topmost first).
func listAppWindows() []WinInfo {
	enumResults = enumResults[:0]
	pEnumWindows.Call(enumCB, 0)
	seenPID := map[uint32]bool{}
	var out []WinInfo
	for _, h := range enumResults {
		if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
			continue
		}
		if o, _, _ := pGetWindow.Call(h, GW_OWNER); o != 0 {
			continue
		}
		ex, _, _ := pGetWindowLongPtrW.Call(h, GWL_EXSTYLE)
		if ex&WS_EX_TOOLWINDOW != 0 && ex&WS_EX_APPWINDOW == 0 {
			continue
		}
		if isCloaked(h) {
			continue
		}
		title := windowText(h)
		if title == "" {
			continue
		}
		if skipClasses[className(h)] {
			continue
		}
		var pid uint32
		pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == ownPID {
			continue
		}
		seenPID[pid] = true
		pi := processInfo(pid)
		if skipExes[filepath.Base(pi.exe)] {
			continue
		}
		aumid := windowAUMID(h)
		if aumid == "" {
			aumid = pi.aumid
		}
		ic, _, _ := pIsIconic.Call(h)
		out = append(out, WinInfo{Hwnd: h, Exe: pi.exe, AUMID: aumid, Title: title, Min: ic != 0})
	}
	// drop dead pids from cache
	if len(procCache) > 400 {
		for pid := range procCache {
			if !seenPID[pid] {
				delete(procCache, pid)
			}
		}
	}
	return out
}

func activateWindow(h uintptr) {
	if ic, _, _ := pIsIconic.Call(h); ic != 0 {
		pShowWindowAsync.Call(h, SW_RESTORE)
	}
	pSetForegroundWindow.Call(h)
}

// ---------- launching ----------

func shellOpen(file, args string) {
	var a uintptr
	if args != "" {
		a = up(u16(args))
	}
	pShellExecuteW.Call(0, up(u16("open")), up(u16(file)), a, 0, SW_SHOWNORMAL)
}

func launchItem(it *Item) {
	switch it.Kind {
	case KindApp:
		shellOpen("explorer.exe", `shell:AppsFolder\`+it.Target)
	case KindStart:
		pKeybdEvent.Call(0x5B, 0, 0, 0)
		pKeybdEvent.Call(0x5B, 0, 2, 0)
	case KindTrash:
		shellOpen("explorer.exe", "shell:RecycleBinFolder")
	default:
		shellOpen(it.Target, "")
	}
}

func recycleFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	var buf []uint16
	for _, p := range paths {
		s, _ := windows.UTF16FromString(p)
		buf = append(buf, s...)
	}
	buf = append(buf, 0)
	op := SHFILEOPSTRUCTW{Func: 3, From: &buf[0], Flags: 0x40} // FO_DELETE, FOF_ALLOWUNDO
	pSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
}

func recycleBinCount() int64 {
	var q SHQUERYRBINFO
	q.CbSize = uint32(unsafe.Sizeof(q))
	hr, _, _ := pSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&q)))
	if !hresultOK(hr) {
		return 0
	}
	return q.I64NumItems
}

// ---------- installed apps (AppsFolder) ----------

type AppEntry struct {
	Name string
	ID   string
}

func listInstalledApps() []AppEntry {
	folder := createShellItem("shell:AppsFolder", &IID_IShellItem)
	if folder == 0 {
		return nil
	}
	defer comRelease(folder)
	var en uintptr
	if hr := comCall(folder, 3, 0, uintptr(unsafe.Pointer(&BHID_EnumItems)), uintptr(unsafe.Pointer(&IID_IEnumShellItems)), uintptr(unsafe.Pointer(&en))); !hresultOK(hr) || en == 0 {
		return nil
	}
	defer comRelease(en)
	var out []AppEntry
	seen := map[string]bool{}
	for i := 0; i < 3000; i++ {
		var si uintptr
		var fetched uint32
		hr := comCall(en, 3, 1, uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&fetched)))
		if hr != 0 || fetched == 0 || si == 0 {
			break
		}
		var pn, pid uintptr
		name, id := "", ""
		if hr := comCall(si, 5, 0, uintptr(unsafe.Pointer(&pn))); hresultOK(hr) && pn != 0 {
			name = u16ToString(pn)
			pCoTaskMemFree.Call(pn)
		}
		if hr := comCall(si, 5, 0x80018001, uintptr(unsafe.Pointer(&pid))); hresultOK(hr) && pid != 0 {
			id = u16ToString(pid)
			pCoTaskMemFree.Call(pid)
		}
		comRelease(si)
		if name == "" || id == "" || seen[id] {
			continue
		}
		low := strings.ToLower(name)
		if strings.HasPrefix(low, "uninstall") || strings.HasSuffix(id, ".url") || strings.HasSuffix(strings.ToLower(id), ".txt") || strings.HasSuffix(strings.ToLower(id), ".chm") {
			continue
		}
		seen[id] = true
		out = append(out, AppEntry{Name: name, ID: id})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// knownFolder returns the path of a Windows known folder (handles OneDrive redirection).
func knownFolder(guid string) string {
	g, err := windows.GUIDFromString(guid)
	if err != nil {
		return ""
	}
	var p uintptr
	hr, _, _ := pSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&g)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if !hresultOK(hr) || p == 0 {
		return ""
	}
	s := u16ToString(p)
	pCoTaskMemFree.Call(p)
	return s
}
