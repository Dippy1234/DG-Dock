//go:build windows

package main

// Installer and updater. Every DG-Dock exe is also its own setup:
//   - run a downloaded MacDock exe → it offers to Install (first time) or Update
//     (when an older version is installed). Settings, pins and icon packs are kept,
//     since they live in %APPDATA%\MacDock, not next to the exe.
//   - the installed dock watches Downloads; when a newer MacDock exe lands there it
//     offers to update itself (dock menu, settings panel and a one-time prompt).
//   - Apps & features → Uninstall works like any other app.
// Installs per-user (no admin) to %LOCALAPPDATA%\Programs\DG-Dock.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// appVersion is bumped for every build handed out. versionMarker sits in the exe as
// plain text so another copy can read a file's version without running it.
const (
	versionMarker = "DGDockBuildVersion=260;"
	uninstallKey  = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DG-Dock`
	installedName = "DG-Dock.exe"

	// what the app was called before it became DG-Dock, so an install or update can tidy up after it
	oldUninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\MacDock`
	oldRunValue     = "MacDock"
	oldFolder       = "Mac Dock" // %LOCALAPPDATA%\Programs\Mac Dock and the shortcuts' names
	oldDataFolder   = "MacDock"  // %APPDATA%\MacDock, %LOCALAPPDATA%\MacDock
)

// versionKeys are the markers a build number follows inside an exe (old builds use the second).
var versionKeys = [][]byte{[]byte("DGDockBuildVersion="), []byte("MacDockBuildVersion=")}

// dataDir is where settings, the log and icon packs live (%APPDATA%\DG-Dock).
// The first time, the old Mac Dock folder is moved across so nothing is lost.
func dataDir() string {
	roaming := os.Getenv("APPDATA")
	dir := filepath.Join(roaming, appName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if old := filepath.Join(roaming, oldDataFolder); dirExists(old) {
			if os.Rename(old, dir) != nil {
				return old // still in use (the old dock is running): move it next time
			}
			fixOldPaths(dir, old)
		}
	}
	os.MkdirAll(dir, 0o755)
	return dir
}

func dirExists(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

// fixOldPaths points custom icons saved under the old folder at their new place.
func fixOldPaths(dir, old string) {
	p := filepath.Join(dir, "settings.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	esc := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) } // as written in JSON
	nb := bytes.ReplaceAll(b, []byte(esc(old)), []byte(esc(dir)))
	if !bytes.Equal(nb, b) {
		writeFileAtomic(p, nb)
	}
}

// webDataDir is the WebView2 cache for the settings, search and setup windows.
func webDataDir() string {
	local, _ := os.UserCacheDir()
	return filepath.Join(local, appName, "WebView2")
}

var appVersion = func() int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(versionMarker, string(versionKeys[0])), ";"))
	return n
}()

//go:embed setup.html
var setupHTML string

//go:embed THIRD-PARTY-NOTICES.txt
var thirdPartyNotices string

//go:embed LICENSE.txt
var licenceText string

// Links shown in Settings › About. Leave a link empty to hide its button.
const (
	homeURL   = "https://github.com/Dippy1234/DG-Dock"
	donateURL = "https://github.com/sponsors/Dippy1234"
)

// releaseVersion is the version people see; appVersion (the build number) is what
// installs and updates compare.
const releaseVersion = "2.0.0"

func installDir() string {
	local, _ := os.UserCacheDir() // %LOCALAPPDATA%
	return filepath.Join(local, "Programs", appName)
}

func installedPath() string { return filepath.Join(installDir(), installedName) }

func exePath() string {
	p, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func isInstalledCopy() bool {
	return strings.EqualFold(filepath.Clean(exePath()), filepath.Clean(installedPath()))
}

// installedVersion is the installed build (0 = not installed).
func installedVersion() int {
	if _, err := os.Stat(installedPath()); err != nil {
		return 0
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return 0
	}
	defer k.Close()
	if b, _, err := k.GetIntegerValue("Build"); err == nil && b > 0 {
		return int(b)
	}
	v, _, _ := k.GetStringValue("DisplayVersion") // installs from before release numbers
	n, _ := strconv.Atoi(strings.TrimPrefix(v, "v"))
	return n
}

// oldInstalledVersion is the build of a copy installed as "Mac Dock" (0 = none).
func oldInstalledVersion() int {
	local, _ := os.UserCacheDir()
	return fileVersion(filepath.Join(local, "Programs", oldFolder, "MacDock.exe"))
}

// fileVersion reads the build number out of a MacDock exe (0 = not a DG-Dock exe,
// or one from before versions were stamped in).
func fileVersion(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	best := 0
	for _, key := range versionKeys {
		for off := 0; ; {
			i := bytes.Index(data[off:], key)
			if i < 0 {
				break
			}
			j := off + i + len(key)
			k := j
			for k < len(data) && k-j < 6 && data[k] >= '0' && data[k] <= '9' {
				k++
			}
			if k < len(data) && data[k] == ';' {
				if n, err := strconv.Atoi(string(data[j:k])); err == nil && n > best {
					best = n
				}
			}
			off = j
		}
	}
	return best
}

// makeShortcut writes a .lnk with Windows' own shell-link object (no scripts).
func makeShortcut(lnk, target, workDir string) error {
	pCoInitializeEx.Call(0, 2)
	var sl uintptr
	if hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_ShellLink)), 0, 1,
		uintptr(unsafe.Pointer(&IID_IShellLinkW)), uintptr(unsafe.Pointer(&sl))); !hresultOK(hr) || sl == 0 {
		return fmt.Errorf("shortcut: shell link unavailable (%x)", hr)
	}
	defer comRelease(sl)
	comCall(sl, 20, up(u16(target)))    // SetPath
	comCall(sl, 9, up(u16(workDir)))    // SetWorkingDirectory
	comCall(sl, 7, up(u16(appName)))    // SetDescription
	comCall(sl, 17, up(u16(target)), 0) // SetIconLocation
	var pf uintptr
	if hr := comCall(sl, 0, uintptr(unsafe.Pointer(&IID_IPersistFile)), uintptr(unsafe.Pointer(&pf))); !hresultOK(hr) || pf == 0 {
		return fmt.Errorf("shortcut: no IPersistFile (%x)", hr)
	}
	defer comRelease(pf)
	if hr := comCall(pf, 6, up(u16(lnk)), 1); !hresultOK(hr) { // Save
		return fmt.Errorf("shortcut: save failed (%x)", hr)
	}
	return nil
}

func startMenuLnk() string {
	roaming, _ := os.UserConfigDir()
	return filepath.Join(roaming, `Microsoft\Windows\Start Menu\Programs`, "DG-Dock.lnk")
}

func desktopLnk() string {
	d := knownFolder("{B4BFCC3A-DB2C-424C-B029-7FE99A87C641}")
	if d == "" {
		h, _ := os.UserHomeDir()
		d = filepath.Join(h, "Desktop")
	}
	return filepath.Join(d, "DG-Dock.lnk")
}

// stopRunningDocks closes every other running DG-Dock. It asks nicely first (so the
// dock gives the taskbar back and saves its settings), then ends any that remain.
func stopRunningDocks() {
	for _, class := range []string{"DGDockWindow", "MacDockWindow"} {
		for i := 0; i < 4; i++ {
			hw, _, _ := pFindWindowW.Call(up(u16(class)), 0)
			if hw == 0 {
				break
			}
			pPostMessageW.Call(hw, WM_CLOSE, 0, 0)
			for t := 0; t < 50; t++ { // up to 5 s to give the taskbar back and save
				time.Sleep(100 * time.Millisecond)
				if h2, _, _ := pFindWindowW.Call(up(u16(class)), 0); h2 != hw {
					break
				}
			}
		}
	}
	self := uint32(os.Getpid())
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if pe.ProcessID == self || !isDockExeName(name) {
			continue
		}
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pe.ProcessID); err == nil {
			windows.TerminateProcess(h, 0)
			windows.WaitForSingleObject(h, 2000)
			windows.CloseHandle(h)
		}
	}
	time.Sleep(300 * time.Millisecond)
}

func copyExe(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	old := dst + ".old"
	var lastErr error
	for i := 0; i < 30; i++ { // the old exe can stay locked for a moment after it exits
		os.Remove(old)
		movedOld := false
		if _, err := os.Stat(dst); err == nil {
			movedOld = os.Rename(dst, old) == nil
		}
		if lastErr = os.Rename(tmp, dst); lastErr == nil {
			os.Remove(old) // may still be in use; the dock tidies it up when it next starts
			return nil
		}
		if movedOld {
			// put the working copy back, so a failed update never leaves no DG-Dock.exe
			os.Rename(old, dst)
		}
		time.Sleep(150 * time.Millisecond)
	}
	os.Remove(tmp)
	return lastErr
}

// tidyInstallLeftovers removes the files an update leaves when the old exe was still
// in use at the time.
func tidyInstallLeftovers() {
	if !isInstalledCopy() {
		return
	}
	exe := installedPath()
	os.Remove(exe + ".old")
	os.Remove(exe + ".new")
}

// restoreTaskbarAfterDock gives the Windows taskbar back without the dock's help, for
// when the dock was force-closed or had crashed while hiding it (used by uninstall).
func restoreTaskbarAfterDock() {
	for _, h := range trayWindows() {
		pShowWindow.Call(h, SW_SHOWNA)
	}
	p := filepath.Join(dataDir(), "settings.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(b, &cfg) != nil {
		return
	}
	mm := -1
	if raw, ok := cfg["prevMMTaskbar"]; ok {
		json.Unmarshal(raw, &mm)
	}
	if mm >= 0 { // the dock had turned on taskbars on all displays: put that back too
		const adv = `Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced`
		if k, err := registry.OpenKey(registry.CURRENT_USER, adv, registry.SET_VALUE); err == nil {
			k.SetDWordValue("MMTaskbarEnabled", uint32(mm))
			k.Close()
			broadcastTraySettings()
		}
		cfg["prevMMTaskbar"] = json.RawMessage("-1")
		if nb, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			writeFileAtomic(p, nb)
		}
	}
	prev := -1
	if raw, ok := cfg["prevTaskbarAutoHide"]; ok {
		json.Unmarshal(raw, &prev)
	}
	if prev < 0 {
		return // the dock already put auto-hide back (or never changed it)
	}
	setTaskbarAutoHide(prev)
	cfg["prevTaskbarAutoHide"] = json.RawMessage("-1")
	if nb, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		writeFileAtomic(p, nb)
	}
}

type installOpts struct {
	Startup bool `json:"startup"`
	Desktop bool `json:"desktop"`
}

// doInstall copies this exe into place and registers it. Used for install and update.
func doInstall(o installOpts, src string) error {
	stopRunningDocks()
	removeOldInstall()
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe := installedPath()
	if !strings.EqualFold(filepath.Clean(src), filepath.Clean(exe)) {
		if err := copyExe(src, exe); err != nil {
			return fmt.Errorf("couldn't copy DG-Dock into place: %v", err)
		}
	}
	if err := makeShortcut(startMenuLnk(), exe, dir); err != nil {
		logLine("%v", err)
	}
	if o.Desktop {
		if err := makeShortcut(desktopLnk(), exe, dir); err != nil {
			logLine("%v", err)
		}
	} else {
		os.Remove(desktopLnk())
	}
	if k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		if o.Startup {
			k.SetStringValue(runValueName, `"`+exe+`"`)
		} else {
			k.DeleteValue(runValueName)
		}
		k.Close()
	}
	u, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("couldn't register DG-Dock: %v", err)
	}
	defer u.Close()
	size := uint32(0)
	if fi, err := os.Stat(exe); err == nil {
		size = uint32(fi.Size() / 1024)
	}
	u.SetStringValue("DisplayName", appName)
	u.SetStringValue("DisplayVersion", releaseVersion)
	u.SetDWordValue("Build", uint32(appVersion))
	os.WriteFile(filepath.Join(dir, "THIRD-PARTY-NOTICES.txt"), []byte(thirdPartyNotices), 0o644)
	os.WriteFile(filepath.Join(dir, "LICENSE.txt"), []byte(licenceText), 0o644)
	u.SetStringValue("Publisher", appName)
	u.SetStringValue("DisplayIcon", exe+",0")
	u.SetStringValue("InstallLocation", dir)
	u.SetStringValue("UninstallString", fmt.Sprintf(`"%s" --uninstall`, exe))
	u.SetStringValue("QuietUninstallString", fmt.Sprintf(`"%s" --uninstall --quiet`, exe))
	u.SetDWordValue("NoModify", 1)
	u.SetDWordValue("NoRepair", 1)
	u.SetDWordValue("EstimatedSize", size)
	u.SetStringValue("InstallDate", time.Now().Format("20060102"))
	return nil
}

// currentOpts keeps the user's earlier choices when updating (including ones made
// when the app was still called Mac Dock).
func currentOpts() installOpts {
	_, err := os.Stat(desktopLnk())
	_, errOld := os.Stat(oldLnk(desktopLnk()))
	return installOpts{Startup: isStartupEnabled() || oldStartupEnabled() || installedVersion() == 0, Desktop: err == nil || errOld == nil}
}

// oldLnk is a shortcut's path under the old name.
func oldLnk(p string) string { return filepath.Join(filepath.Dir(p), oldFolder+".lnk") }

func oldStartupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(oldRunValue)
	return err == nil
}

// removeOldInstall takes away the copy installed under the old name: its folder,
// shortcuts, start-with-Windows entry and Apps & features entry. Settings and icon
// packs are kept (dataDir moves them across).
func removeOldInstall() {
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		k.DeleteValue(oldRunValue)
		k.Close()
	}
	registry.DeleteKey(registry.CURRENT_USER, oldUninstallKey)
	os.Remove(oldLnk(startMenuLnk()))
	os.Remove(oldLnk(desktopLnk()))
	local, _ := os.UserCacheDir()
	old := filepath.Join(local, "Programs", oldFolder)
	if dirExists(old) && !strings.EqualFold(filepath.Clean(old), filepath.Clean(installDir())) {
		self := exePath()
		if !strings.HasPrefix(strings.ToLower(self), strings.ToLower(old)+`\`) {
			os.RemoveAll(old)
		}
	}
	os.RemoveAll(filepath.Join(local, oldDataFolder, "WebView2"))
	os.Remove(filepath.Join(local, oldDataFolder))
	dataDir()
}

func logLine(format string, args ...any) {
	dir := dataDir()
	f, err := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s setup: %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

func launchInstalled() {
	exec.Command(installedPath()).Start()
}

// ---------- setup window ----------

type setupInfo struct {
	Name      string `json:"name"` // release version, e.g. "1.0.0"
	Version   int    `json:"version"`
	Installed int    `json:"installed"`
	Running   bool   `json:"running"`
	Dir       string `json:"dir"`
	Startup   bool   `json:"startup"`
	Desktop   bool   `json:"desktop"`
	Dark      bool   `json:"dark"`
	Accent    string `json:"accent"`
	OnAccent  string `json:"onAccent"`
}

func systemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err != nil || v == 0
}

// runSetup shows the Install / Update window. Returns true if the dock should start
// in this process instead (the "Run without installing" choice).
func runSetup() (runHere bool) {
	if pSetProcessDpiAwarenessContext.Find() == nil {
		pSetProcessDpiAwarenessContext.Call(^uintptr(3))
	}
	pCoInitializeEx.Call(0, 2)
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	wc := WNDCLASSEXW{LpfnWndProc: webWndProcCB, HInstance: hinst, HCursor: cur, LpszClassName: u16("DGDockSetup")}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	dpi := 96
	if r, _, _ := user32.NewProc("GetDpiForSystem").Call(); r > 0 {
		dpi = int(r)
	}
	w, h := int32(440*dpi/96), int32(500*dpi/96)
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	mon, _, _ := pMonitorFromPoint.Call(0, 1)
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	x := mi.RcWork.Left + (mi.RcWork.Right-mi.RcWork.Left-w)/2
	y := mi.RcWork.Top + (mi.RcWork.Bottom-mi.RcWork.Top-h)/2
	const style = 0x00C80000 | 0x00020000 // WS_CAPTION|WS_SYSMENU|WS_MINIMIZEBOX
	hwnd, _, _ := pCreateWindowExW.Call(0, up(u16("DGDockSetup")), up(u16("DG-Dock Setup")), style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, hinst, 0)
	if hwnd == 0 {
		return true
	}
	dark := systemDark()
	webHosts[hwnd] = &webHost{hwnd: hwnd, binds: map[string]reflect.Value{}}
	dwmInt(hwnd, 20, boolI(dark))
	dwmInt(hwnd, 38, 2) // Mica
	pShowWindow.Call(hwnd, 5)
	pSetForegroundWindow.Call(hwnd)

	bg := edge.COREWEBVIEW2_COLOR{A: 255, R: 0x20, G: 0x20, B: 0x20}
	if !dark {
		bg = edge.COREWEBVIEW2_COLOR{A: 255, R: 0xf3, G: 0xf3, B: 0xf3}
	}
	hst, err := newWebHost(hwnd, webDataDir(), bg)
	if err != nil {
		pShowWindow.Call(hwnd, SW_HIDE)
		// No WebView2: fall back to a plain question.
		if installedVersion() == 0 {
			if askYesNo("Install DG-Dock on this PC?\n\nChoose No to just run it this time.", "DG-Dock Setup") {
				if err := doInstall(currentOpts(), exePath()); err == nil {
					launchInstalled()
					return false
				}
			}
			return true
		}
		if askYesNo(fmt.Sprintf("Update DG-Dock to version %d?", appVersion), "DG-Dock Setup") {
			if err := doInstall(currentOpts(), exePath()); err == nil {
				launchInstalled()
			} else {
				messageBox(err.Error(), "DG-Dock Setup")
			}
		}
		return false
	}
	quit := func() { pPostQuitMessage.Call(0) }
	hst.onClose = quit
	launch := ""
	hst.bind("mdLicence", func() string { return licenceText })
	hst.bind("mdSetupInfo", func() setupInfo {
		o := currentOpts()
		hw, _, _ := pFindWindowW.Call(up(u16("DGDockWindow")), 0)
		if hw == 0 {
			hw, _, _ = pFindWindowW.Call(up(u16("MacDockWindow")), 0)
		}
		acc, on := accentColors(dark)
		// a copy installed under the old name counts, so this shows as an update
		return setupInfo{Name: releaseVersion, Version: appVersion, Installed: max(installedVersion(), oldInstalledVersion()), Running: hw != 0, Dir: installDir(),
			Startup: o.Startup, Desktop: o.Desktop, Dark: dark, Accent: acc, OnAccent: on}
	})
	hst.bind("mdInstall", func(o installOpts) string {
		if err := installKeepingWindowAlive(o); err != nil {
			logLine("install failed: %v", err)
			return err.Error()
		}
		launch = "installed"
		return ""
	})
	hst.bind("mdFinish", func(open bool) bool {
		if !open {
			launch = ""
		}
		quit()
		return true
	})
	// the setup page's "Uninstall old version": the same full uninstall as Settings › Apps
	hst.bind("mdUninstallOld", func() bool { uninstallAll(false, true); return true })
	hst.bind("mdOpenInstalled", func() bool { launch = "installed"; quit(); return true })
	hst.bind("mdRunHere", func() bool { launch = "here"; quit(); return true })
	hst.load(setupHTML)

	var msg MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	// The setup window can be installing a replacement and launching it immediately.
	// Close WebView2 before starting that process so Chromium tears down its child
	// windows while this setup process still has a message pump and a live parent.
	hst.close()
	pShowWindow.Call(hwnd, SW_HIDE)
	pDestroyWindow(hwnd)
	switch launch {
	case "installed":
		launchInstalled()
	case "here":
		return true
	}
	return false
}

// installKeepingWindowAlive runs doInstall on its own thread and keeps this window's
// messages flowing meanwhile, so Setup doesn't show "Not responding" while it waits
// for a running dock to close.
func installKeepingWindowAlive(o installOpts) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // COM (shortcuts) is set up per thread
		defer runtime.UnlockOSThread()
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("setup stopped unexpectedly: %v", r)
			}
		}()
		done <- doInstall(o, exePath())
	}()
	var msg MSG
	quitLater := false
	for {
		select {
		case err := <-done:
			if quitLater {
				pPostQuitMessage.Call(0) // the window was closed during the install
			}
			return err
		default:
		}
		for {
			r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1) // PM_REMOVE
			if r == 0 {
				break
			}
			if msg.Message == 0x0012 { // WM_QUIT: finish installing first
				quitLater = true
				continue
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func askYesNo(text, title string) bool {
	r, _, _ := pMessageBoxW.Call(0, up(u16(text)), up(u16(title)), 0x4|0x20)
	return r == 6
}

// runQuietUpdate is used by the dock's own "Update" button: it launches the new exe
// with --update, which installs itself with the current choices and starts again.
func runQuietUpdate() {
	if err := doInstall(currentOpts(), exePath()); err != nil {
		logLine("update failed: %v", err)
		messageBox("DG-Dock couldn't update: "+err.Error(), appName)
		return
	}
	launchInstalled()
}

// ---------- uninstall ----------

func runUninstall(quiet bool) { uninstallAll(quiet, false) }

// uninstallAll removes DG-Dock completely. fromSetup: the setup window is open and is
// using DG-Dock's WebView2 folder, so that folder is left for setup to use.
func uninstallAll(quiet, fromSetup bool) {
	if !quiet && !askYesNo("Remove DG-Dock from this PC?\n\n"+
		"Everything DG-Dock added is removed: the app, its settings, icon packs, Launchpad folders and the "+
		"Pictures › DG-Dock Icon folder (with any icons you put in it). The Windows taskbar is put back.\n\n"+
		"Photos imported from USB drives (Pictures › Imported) are yours and are kept.\n\n"+
		"To keep your setup for later, cancel and use Settings › About › Save my setup first.", "Uninstall DG-Dock") {
		return
	}
	stopRunningDocks()
	restoreTaskbarAfterDock() // in case a dock had to be force-closed, or had crashed
	// start with Windows, and Settings › Apps (also the entries from when it was called Mac Dock)
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		k.DeleteValue(runValueName)
		k.DeleteValue(oldRunValue)
		k.Close()
	}
	registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	registry.DeleteKey(registry.CURRENT_USER, oldUninstallKey)
	// shortcuts
	for _, l := range []string{startMenuLnk(), desktopLnk()} {
		os.Remove(l)
		os.Remove(oldLnk(l))
	}
	// settings, icon packs, the log and the WebView2 cache, under both names
	roaming := os.Getenv("APPDATA")
	local, _ := os.UserCacheDir()
	for _, d := range []string{
		filepath.Join(roaming, appName), filepath.Join(roaming, oldDataFolder),
		filepath.Join(local, oldDataFolder),
		filepath.Join(local, "Programs", oldFolder),
	} {
		if d != "" && filepath.Base(d) != "" && !strings.EqualFold(d, roaming) && !strings.EqualFold(d, local) {
			os.RemoveAll(d)
		}
	}
	if fromSetup { // all but the WebView2 cache setup is using right now
		if ents, err := os.ReadDir(filepath.Join(local, appName)); err == nil {
			for _, e := range ents {
				if !strings.EqualFold(e.Name(), "WebView2") {
					os.RemoveAll(filepath.Join(local, appName, e.Name()))
				}
			}
		}
	} else {
		os.RemoveAll(filepath.Join(local, appName))
	}
	// the icons folder DG-Dock made in Pictures
	pics := knownFolder("{33E28130-4E1E-4676-835A-98395C3BC3BB}")
	if pics == "" {
		pics = filepath.Join(os.Getenv("USERPROFILE"), "Pictures")
	}
	if pics != "" {
		os.RemoveAll(filepath.Join(pics, "DG-Dock Icon"))
	}
	// leftovers from interrupted updates
	for _, p := range []string{installedPath() + ".old", installedPath() + ".new"} {
		os.Remove(p)
	}
	// A running program can't delete itself: step into the temp folder, remove the install
	// folder, then have a hidden helper delete this last copy once it has closed.
	dir := installDir()
	self := exePath()
	if strings.HasPrefix(strings.ToLower(self), strings.ToLower(dir)+`\`) {
		moved := filepath.Join(os.TempDir(), fmt.Sprintf("DG-Dock-uninstalled-%d.exe", time.Now().Unix()))
		if os.Rename(self, moved) == nil {
			self = moved
		}
	}
	os.RemoveAll(dir)
	if !strings.HasPrefix(strings.ToLower(self), strings.ToLower(os.TempDir())) {
		self = "" // never delete a setup file you ran from somewhere else
	}
	if !fromSetup { // (setup is about to install again into that folder)
		cleanupAfterExit(self, dir)
	}
	if !quiet {
		messageBox("DG-Dock has been uninstalled, and nothing of it is left on this PC.", appName)
	}
}

// cleanupAfterExit starts a hidden helper that waits for this process to close, then
// deletes its exe (and the install folder, if anything in it was still in use).
func cleanupAfterExit(exe, dir string) {
	cmd := fmt.Sprintf(`/c ping -n 4 127.0.0.1 >nul & rmdir /s /q "%s"`, dir)
	if exe != "" {
		cmd = fmt.Sprintf(`/c ping -n 4 127.0.0.1 >nul & del /f /q "%s" & rmdir /s /q "%s"`, exe, dir)
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	var si windows.StartupInfo
	var pi windows.ProcessInformation
	si.Cb = uint32(unsafe.Sizeof(si))
	line, err := windows.UTF16PtrFromString(`"` + comspec + `" ` + cmd)
	if err != nil {
		return
	}
	if windows.CreateProcess(nil, line, nil, nil, false, windows.CREATE_NO_WINDOW, nil, nil, &si, &pi) == nil {
		windows.CloseHandle(pi.Thread)
		windows.CloseHandle(pi.Process)
	}
}

// handleSetupArgs runs before the dock starts. Returns true when the dock should run.
func handleSetupArgs() bool {
	args := os.Args[1:]
	has := func(s string) bool {
		for _, a := range args {
			if strings.EqualFold(a, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("--uninstall"):
		runUninstall(has("--quiet"))
		return false
	case has("--update"):
		runQuietUpdate()
		return false
	case has("--display"): // an extra dock for another display, started by the main dock
		return true
	case has("--wait"): // the dock restarting itself after restoring a setup
		return true
	case has("--portable"), isInstalledCopy():
		return true
	}
	return runSetup()
}
