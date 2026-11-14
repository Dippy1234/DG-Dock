//go:build windows

package main

// Transfer progress on dock folders (and USB drive tiles): the icon's border fills up
// in the Windows accent colour while files arrive.
//
//   - Files you drop on a dock folder are copied by the dock itself, so it shows the
//     real percentage. Names that are taken get " (2)" etc. — nothing is ever
//     replaced — and drops onto the same folder wait their turn.
//   - Downloads from a browser and copies made in File Explorer are noticed by
//     watching the folder. Those apps don't say how far along they are, so the border
//     shows a moving sweep while files are arriving, then fills once they stop.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// partial-download names used by browsers
var partialPatterns = []string{"*.crdownload", "*.part", "*.partial", "*.download", "*.opdownload", "*.!ut"}

func xferKey(path string) string { return strings.ToLower(filepath.Clean(path)) }

// ---------- dropping onto a dock folder ----------

// dropIntoFolder moves or copies files into a dock folder.
func (a *App) dropIntoFolder(it *Item, files []string, move bool) {
	if !a.cfg.NoBounce {
		it.Bouncing, it.BounceStart = true, time.Now()
	}
	dir := it.Target
	key := xferKey(dir)
	var list []copyItem
	var removeAfter []string
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		dst := freeName(filepath.Join(dir, fi.Name()))
		if move && strings.EqualFold(filepath.VolumeName(f), filepath.VolumeName(dir)) {
			// same drive: a move is just a rename, instant
			if err := os.Rename(f, dst); err == nil {
				a.flashDone(key)
				continue
			}
		}
		if !fi.IsDir() {
			list = append(list, copyItem{f, dst, fi.Size()})
		} else {
			filepath.Walk(f, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(f, p)
				list = append(list, copyItem{p, filepath.Join(dst, rel), info.Size()})
				return nil
			})
		}
		if move {
			removeAfter = append(removeAfter, f)
		}
	}
	if len(list) == 0 {
		return
	}
	a.startJob(key, &usbJob{Kind: "copy", Dest: dir, RemoveAfter: removeAfter}, list)
}

// flashDone shows the full border briefly (an instant move).
func (a *App) flashDone(key string) {
	for _, it := range a.items {
		if it.IsDir && xferKey(it.Target) == key {
			it.XferDoneAt = time.Now()
			a.animUntil = time.Now().Add(1900 * time.Millisecond)
		}
	}
}

// ---------- noticing other apps' transfers ----------

type folderWatch struct {
	path    string
	h       windows.Handle
	last    atomic.Int64 // last write seen (UnixNano)
	burst   atomic.Int64 // when the current run of writes began
	stopped atomic.Bool
	notify  uintptr // device notification, so Windows can ask us to let go of the drive
}

var (
	watchMu  sync.Mutex
	watches  = map[string]*folderWatch{}
	watchAt  time.Time
	partials = map[string]bool{} // folder key → a download is in progress
	// drives being ejected ("d:"): nothing on them is watched, so the dock never holds
	// the drive open while Windows (or the dock) safely removes it
	released = map[string]bool{}
)

var (
	pRegisterDeviceNotificationW  = user32.NewProc("RegisterDeviceNotificationW")
	pUnregisterDeviceNotification = user32.NewProc("UnregisterDeviceNotification")
)

// devBroadcastHandle is DEV_BROADCAST_HANDLE (64-bit layout).
type devBroadcastHandle struct {
	Size, DeviceType, Reserved uint32
	Handle                     uintptr
	HDevNotify                 uintptr
	EventGUID                  windows.GUID
	NameOffset                 int32
	Data                       [4]byte
}

const dbtDevtypHandle = 6

func volOf(path string) string { return strings.ToLower(filepath.VolumeName(path)) }

// watchedPaths are the folders shown on the dock, and the USB drives.
func (a *App) watchedPaths() map[string]string {
	out := map[string]string{}
	if a.cfg.NoFolderActivity {
		return out
	}
	for _, it := range a.dock.Items {
		switch {
		case it.IsDir && it.Target != "":
			out[xferKey(it.Target)] = it.Target
		case it.Widget == "usb" && it.UsbRoot != "":
			out[xferKey(it.UsbRoot)] = it.UsbRoot
		}
	}
	return out
}

// syncWatches starts watching new dock folders and stops watching removed ones.
func (a *App) syncWatches() {
	want := a.watchedPaths()
	watchMu.Lock()
	defer watchMu.Unlock()
	for k, w := range watches {
		if _, ok := want[k]; !ok {
			w.stop()
			delete(watches, k)
		}
	}
	for k, p := range want {
		if released[volOf(p)] {
			if w := watches[k]; w != nil {
				w.stop()
				delete(watches, k)
			}
			continue
		}
		if watches[k] == nil {
			if w := startWatch(a.hwnd, p); w != nil {
				watches[k] = w
			}
		}
	}
}

func startWatch(hwnd uintptr, path string) *folderWatch {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil
	}
	w := &folderWatch{path: path, h: h}
	// ask Windows to tell us before the drive is removed (Safely Remove, Eject in
	// Explorer), so we can close the handle instead of blocking it
	f := devBroadcastHandle{DeviceType: dbtDevtypHandle, Handle: uintptr(h)}
	f.Size = uint32(unsafe.Sizeof(f))
	w.notify, _, _ = pRegisterDeviceNotificationW.Call(hwnd, uintptr(unsafe.Pointer(&f)), 0)
	go w.run()
	return w
}

func (w *folderWatch) stop() {
	if w.stopped.Swap(true) {
		return
	}
	if w.notify != 0 {
		pUnregisterDeviceNotification.Call(w.notify)
	}
	windows.CancelIoEx(w.h, nil)
	windows.CloseHandle(w.h)
}

// ignoreName: files that change all the time without being a transfer.
func ignoreName(name string) bool {
	n := strings.ToLower(filepath.Base(name))
	return n == "desktop.ini" || n == "thumbs.db" || strings.HasPrefix(n, "~$") ||
		strings.HasSuffix(n, ".dgdock-part") || // the dock's own copies show real progress
		strings.HasSuffix(n, ".tmp") || strings.HasSuffix(n, ".lnk")
}

func (w *folderWatch) run() {
	buf := make([]byte, 64*1024)
	const filter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME | windows.FILE_NOTIFY_CHANGE_SIZE
	for !w.stopped.Load() {
		var n uint32
		if err := windows.ReadDirectoryChanges(w.h, &buf[0], uint32(len(buf)), true, filter, &n, nil, 0); err != nil {
			return
		}
		seen := n == 0 // buffer overflow: lots happened
		for off := uint32(0); off < n; {
			info := (*windows.FileNotifyInformation)(unsafe.Pointer(&buf[off]))
			name := windows.UTF16ToString(unsafe.Slice(&info.FileName, info.FileNameLength/2))
			switch info.Action {
			case windows.FILE_ACTION_ADDED, windows.FILE_ACTION_MODIFIED, windows.FILE_ACTION_RENAMED_NEW_NAME:
				if !ignoreName(name) {
					seen = true
				}
			}
			if info.NextEntryOffset == 0 {
				break
			}
			off += info.NextEntryOffset
		}
		if seen {
			now := time.Now().UnixNano()
			if now-w.last.Load() > int64(2*time.Second) {
				w.burst.Store(now)
			}
			w.last.Store(now)
		}
	}
}

// hasPartialDownload: a browser is still downloading into this folder.
func hasPartialDownload(dir string) bool {
	for _, pat := range partialPatterns {
		p, _ := windows.UTF16PtrFromString(filepath.Join(dir, pat))
		var fd windows.Win32finddata
		h, err := windows.FindFirstFile(p, &fd)
		if err == nil {
			windows.FindClose(h)
			return true
		}
	}
	return false
}

// activity reports whether files are arriving in a watched place.
func activity(key string) bool {
	watchMu.Lock()
	w, part := watches[key], partials[key]
	watchMu.Unlock()
	if part {
		return true
	}
	if w == nil {
		return false
	}
	now := time.Now().UnixNano()
	last, burst := w.last.Load(), w.burst.Load()
	// writes in the last moment, going on for a little while (a single save is not
	// a transfer)
	return now-last < int64(1200*time.Millisecond) && last-burst > int64(700*time.Millisecond)
}

// tickTransfers updates the borders on folder icons and USB tiles.
func (a *App) tickTransfers() {
	now := time.Now()
	if now.Sub(watchAt) > 1500*time.Millisecond {
		watchAt = now
		a.syncWatches()
		watchMu.Lock()
		for k := range watches {
			partials[k] = hasPartialDownload(watches[k].path)
		}
		watchMu.Unlock()
	}
	anim := false
	for _, it := range a.dock.Items {
		var key string
		switch {
		case it.IsDir && it.Target != "":
			key = xferKey(it.Target)
		case it.Widget == "usb" && it.UsbRoot != "":
			key = xferKey(it.UsbRoot)
		default:
			continue
		}
		busy, level := false, -1.0
		jobKey := key
		if it.Widget == "usb" {
			jobKey = it.UsbRoot // USB jobs are keyed by the drive root
		}
		if j := usbJobFor(jobKey); j != nil && !j.finished.Load() {
			busy, level = true, j.progress()
		} else if activity(key) {
			busy = true
		}
		// finished: the border fills, then fades (always for the dock's own copies;
		// for other apps only after a real transfer, not a quick save)
		if it.XferBusy && !busy && (it.XferLevel >= 0 || now.Sub(it.XferSince) > 1200*time.Millisecond) {
			it.XferDoneAt = now // finished: the border fills, then fades
		}
		if busy && !it.XferBusy {
			it.XferSince = now
		}
		if busy != it.XferBusy || level != it.XferLevel {
			it.XferBusy, it.XferLevel = busy, level
			a.needRender = true
		}
		if busy || now.Sub(it.XferDoneAt) < 1800*time.Millisecond {
			anim = true
		}
	}
	if anim {
		a.animUntil = now.Add(120 * time.Millisecond) // keep the border moving
		a.needRender = true
	}
}

// stopAllWatches is called when the dock quits.
func stopAllWatches() {
	watchMu.Lock()
	defer watchMu.Unlock()
	for k, w := range watches {
		w.stop()
		delete(watches, k)
	}
}

// releaseVolume stops watching anything on a drive ("d:") so it can be ejected.
func releaseVolume(vol string) {
	if vol == "" {
		return
	}
	watchMu.Lock()
	defer watchMu.Unlock()
	released[vol] = true
	for k, w := range watches {
		if volOf(w.path) == vol {
			w.stop()
			delete(watches, k)
		}
	}
}

// unreleaseVolume: the eject didn't happen; watching resumes on the next tick.
func unreleaseVolume(vol string) {
	watchMu.Lock()
	delete(released, vol)
	watchMu.Unlock()
}

// onWatchDeviceChange handles WM_DEVICECHANGE for our watch handles: Windows asks
// before removing the drive (we let go), and says if the removal was cancelled.
func onWatchDeviceChange(wparam, lparam uintptr) {
	if lparam == 0 {
		return
	}
	hdr := (*devBroadcastHandle)(unsafe.Pointer(lparam))
	if hdr.DeviceType != dbtDevtypHandle {
		return
	}
	watchMu.Lock()
	vol := ""
	for _, w := range watches {
		if uintptr(w.h) == hdr.Handle || (w.notify != 0 && w.notify == hdr.HDevNotify) {
			vol = volOf(w.path)
			break
		}
	}
	watchMu.Unlock()
	switch wparam {
	case 0x8001: // DBT_DEVICEQUERYREMOVE
		releaseVolume(vol)
	}
}
