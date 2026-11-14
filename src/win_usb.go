//go:build windows

package main

// USB drives widget: a tile appears on the dock for each USB drive that's plugged in
// (memory sticks, external drives, card readers; never internal or network drives).
// Its card, styled like Now Playing, shows the drive's name and free space with Open,
// Import photos (when there's a camera DCIM folder) and Eject. Files dropped on the
// tile are copied to the drive, and copies show their progress on the tile and card.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	cfgmgr32                  = windows.NewLazySystemDLL("cfgmgr32.dll")
	pCMLocateDevNode          = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	pCMGetParent              = cfgmgr32.NewProc("CM_Get_Parent")
	pCMRequestDeviceEject     = cfgmgr32.NewProc("CM_Request_Device_EjectW")
	guidDevInterfaceDisk      = windows.GUID{Data1: 0x53F56307, Data2: 0xB6BF, Data3: 0x11D0, Data4: [8]byte{0x94, 0xF2, 0x00, 0xA0, 0xC9, 0x1E, 0xFB, 0x8B}}
	photoExts                 = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".heic": true, ".heif": true, ".dng": true, ".cr2": true, ".cr3": true, ".nef": true, ".arw": true, ".raf": true, ".orf": true, ".rw2": true, ".mp4": true, ".mov": true, ".m4v": true, ".avi": true, ".mts": true}
	usbMu                     sync.Mutex
	usbJobs                   = map[string]*usbJob{} // drive root → its copy
	ioctlStorageQueryProperty = uint32(0x2D1400)
	ioctlStorageGetDevNumber  = uint32(0x2D1080)
)

const (
	timerUsb        = 22
	busTypeUSB      = 7
	wmUsbChanged    = WM_APP + 30
	WM_DEVICECHANGE = 0x0219
)

type usbDrive struct {
	Root     string // E:\
	Letter   string // E:
	Label    string // SanDisk Ultra
	Total    uint64
	Free     uint64
	HasPhoto bool // a camera DCIM folder
}

func (d usbDrive) Name() string {
	if d.Label == "" {
		return "USB drive (" + d.Letter + ")"
	}
	return d.Label + " (" + d.Letter + ")"
}

// usbJob is one copy: files dropped onto the drive, or photos imported from it.
type usbJob struct {
	Kind        string // "copy" (to the drive) or "import" (photos from it)
	Dest        string
	Files       int
	Total       int64
	done        atomic.Int64
	filesDone   atomic.Int64
	cancel      atomic.Bool
	finished    atomic.Bool
	Err         error
	Started     time.Time
	FinishedAt  time.Time
	Copied      int      // files actually copied (imports skip ones already there)
	RemoveAfter []string // a move between drives: delete these once everything is copied
}

func (j *usbJob) progress() float64 {
	if j.Total <= 0 {
		if j.finished.Load() {
			return 1
		}
		return 0
	}
	return float64(j.done.Load()) / float64(j.Total)
}

// isUSBVolume asks the drive what bus it's on: only USB counts.
func isUSBVolume(letter string) bool {
	p, _ := windows.UTF16PtrFromString(`\\.\` + letter)
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	query := [3]uint32{0, 0, 0} // StorageDeviceProperty, PropertyStandardQuery
	var out [1024]byte
	var n uint32
	if err := windows.DeviceIoControl(h, ioctlStorageQueryProperty, (*byte)(unsafe.Pointer(&query[0])), 12, &out[0], uint32(len(out)), &n, nil); err != nil || n < 32 {
		return false
	}
	return *(*uint32)(unsafe.Pointer(&out[28])) == busTypeUSB // STORAGE_DEVICE_DESCRIPTOR.BusType
}

// listUSBDrives returns the USB drives that are plugged in and ready.
func listUSBDrives() []usbDrive {
	var out []usbDrive
	mask, _ := windows.GetLogicalDrives()
	for i := 2; i < 26; i++ { // skip A: and B:
		if mask&(1<<i) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		root := letter + `\`
		rp, _ := windows.UTF16PtrFromString(root)
		t := windows.GetDriveType(rp)
		if t != windows.DRIVE_REMOVABLE && t != windows.DRIVE_FIXED {
			continue // network, CD, RAM disks
		}
		if !isUSBVolume(letter) {
			continue
		}
		var label [261]uint16
		if windows.GetVolumeInformation(rp, &label[0], 261, nil, nil, nil, nil, 0) != nil {
			continue // card reader with no card in it
		}
		d := usbDrive{Root: root, Letter: letter, Label: windows.UTF16ToString(label[:])}
		var free, total, tfree uint64
		windows.GetDiskFreeSpaceEx(rp, &free, &total, &tfree)
		d.Total, d.Free = total, free
		if fi, err := os.Stat(filepath.Join(root, "DCIM")); err == nil && fi.IsDir() {
			d.HasPhoto = true
		}
		out = append(out, d)
	}
	return out
}

// scanUSB refreshes the drive list; when drives come or go the dock is rebuilt.
func (a *App) scanUSB() {
	if a.cfg.NoUsbWidget {
		if len(a.usbDrives) > 0 {
			a.usbDrives = nil
			a.rebuildItems()
			a.relayout()
		}
		return
	}
	ds := listUSBDrives()
	key := func(list []usbDrive) string {
		var b strings.Builder
		for _, d := range list {
			b.WriteString(d.Root + d.Label + "|")
		}
		return b.String()
	}
	changed := key(ds) != key(a.usbDrives)
	a.usbDrives = ds
	for _, d := range ds {
		if it := a.items["w:usb:"+d.Letter]; it != nil {
			it.Name = d.Name()
		}
	}
	if changed {
		a.rebuildItems()
		a.relayout()
		a.render()
	}
}

func (a *App) usbDrive(root string) (usbDrive, bool) {
	for _, d := range a.usbDrives {
		if d.Root == root {
			return d, true
		}
	}
	return usbDrive{}, false
}

func usbJobFor(root string) *usbJob {
	usbMu.Lock()
	defer usbMu.Unlock()
	return usbJobs[root]
}

func fmtBytes(n float64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TB", n/(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", n/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", n/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", n/(1<<10))
}

// usbPop is the drive's card (Now Playing layout).
func (a *App) usbPop(it *Item) PopContent {
	d, ok := a.usbDrive(it.UsbRoot)
	pc := PopContent{Mode: PopDrive, Title: it.Name}
	if !ok {
		pc.Subtitle = "Drive removed"
		return pc
	}
	pc.Level = 1 - float64(d.Free)/float64(max(d.Total, 1))
	pc.Subtitle = fmtBytes(float64(d.Free)) + " free of " + fmtBytes(float64(d.Total))
	pc.Line3 = "USB drive"
	if d.HasPhoto {
		pc.Line3 = "USB drive · camera photos found"
	}
	if j := usbJobFor(d.Root); j != nil {
		p := j.progress()
		pc.Level, pc.Busy, pc.Playing = p, !j.finished.Load(), true
		verb := "Copying"
		if j.Kind == "import" {
			verb = "Importing"
		}
		switch {
		case j.finished.Load() && j.Err != nil:
			pc.Subtitle = "Couldn't finish: " + j.Err.Error()
			pc.Line3 = ""
		case j.finished.Load() && j.cancel.Load():
			pc.Subtitle = "Stopped"
			pc.Line3 = fmt.Sprintf("%d of %d files done", j.filesDone.Load(), j.Files)
		case j.finished.Load():
			pc.Subtitle = "Done"
			if j.Kind == "import" {
				pc.Subtitle = fmt.Sprintf("Imported %d photos and videos", j.Copied)
				if j.Copied == 0 {
					pc.Subtitle = "Everything was already imported"
				}
			}
			pc.Line3 = fmt.Sprintf("%d files · %s", j.Files, fmtBytes(float64(j.Total)))
		default:
			pc.Subtitle = fmt.Sprintf("%s %d files · %.0f%%", verb, j.Files, p*100)
			speed := float64(j.done.Load()) / max(time.Since(j.Started).Seconds(), 0.1)
			pc.Line3 = fmt.Sprintf("%s of %s · %s/s", fmtBytes(float64(j.done.Load())), fmtBytes(float64(j.Total)), fmtBytes(speed))
		}
		if pc.Busy {
			pc.Rows = []PopRow{{Title: "Stop", Path: "usb:cancel:" + d.Root}, {Title: "Open", Path: "usb:open:" + d.Root}}
			return pc
		}
		if j.Kind == "import" && j.Dest != "" {
			pc.Rows = []PopRow{{Title: "Show photos", Path: "usb:showdest:" + d.Root}, {Title: "Open", Path: "usb:open:" + d.Root}, {Title: "Eject", Path: "usb:eject:" + d.Root}}
			return pc
		}
	}
	pc.Rows = []PopRow{{Title: "Open", Path: "usb:open:" + d.Root}}
	if d.HasPhoto {
		pc.Rows = append(pc.Rows, PopRow{Title: "Import photos", Path: "usb:import:" + d.Root})
	}
	pc.Rows = append(pc.Rows, PopRow{Title: "Eject", Path: "usb:eject:" + d.Root})
	return pc
}

// usbAction runs a button on the drive card.
func (a *App) usbAction(p string) {
	parts := strings.SplitN(p, ":", 3) // usb:verb:E:\
	if len(parts) < 3 {
		return
	}
	verb, root := parts[1], parts[2]
	switch verb {
	case "open":
		a.closePop()
		shellOpen(root, "")
	case "showdest":
		if j := usbJobFor(root); j != nil && j.Dest != "" {
			a.closePop()
			shellOpen(j.Dest, "")
		}
	case "cancel":
		if j := usbJobFor(root); j != nil {
			j.cancel.Store(true)
		}
	case "import":
		a.importPhotos(root)
	case "eject":
		a.ejectDrive(root)
	}
	a.refreshUsbPop()
}

func (a *App) refreshUsbPop() {
	if d := a.dock; d.PopItem != nil && d.PopItem.Widget == "usb" {
		d.Pop = a.usbPop(d.PopItem)
		a.needRender = true
	}
}

// tickUSB keeps the tile and card up to date while something is copying.
func (a *App) tickUSB() {
	usbMu.Lock()
	var busy bool
	for root, j := range usbJobs {
		if j.finished.Load() && !j.FinishedAt.IsZero() && time.Since(j.FinishedAt) > 8*time.Second {
			delete(usbJobs, root) // the "done" state has been on show long enough
			continue
		}
		busy = true
	}
	usbMu.Unlock()
	if !busy && !a.usbWasBusy {
		return
	}
	a.usbWasBusy = busy
	for _, it := range a.items {
		if it.Widget != "usb" {
			continue
		}
		lvl, on := 0.0, false
		if j := usbJobFor(it.UsbRoot); j != nil {
			lvl, on = j.progress(), true
			if j.finished.Load() {
				lvl = 1
			}
		}
		if lvl != it.WLevel || on != it.WPlaying {
			it.WLevel, it.WPlaying = lvl, on
			a.needRender = true
		}
	}
	if time.Since(a.usbPopAt) > 250*time.Millisecond {
		a.usbPopAt = time.Now()
		a.refreshUsbPop()
	}
}

// ---------- copying ----------

type copyItem struct {
	src, dst string
	size     int64
}

func (a *App) startJob(root string, j *usbJob, list []copyItem) {
	usbMu.Lock()
	if cur := usbJobs[root]; cur != nil && !cur.finished.Load() {
		// one copy at a time per place, so two can never write the same names at
		// once: this one waits its turn
		jobQueue[root] = append(jobQueue[root], queuedJob{j, list})
		usbMu.Unlock()
		return
	}
	a.runJobLocked(root, j, list)
	usbMu.Unlock()
}

type queuedJob struct {
	j    *usbJob
	list []copyItem
}

var jobQueue = map[string][]queuedJob{}

// runJobLocked starts a copy (usbMu held).
func (a *App) runJobLocked(root string, j *usbJob, list []copyItem) {
	j.Started = time.Now()
	j.Files = len(list)
	for _, c := range list {
		j.Total += c.size
	}
	usbJobs[root] = j
	a.usbWasBusy = true
	go func() {
		defer func() {
			if r := recover(); r != nil {
				j.Err = fmt.Errorf("%v", r)
			}
			// a move to another drive: the originals go only once every copy is safe
			if j.Err == nil && !j.cancel.Load() {
				for _, src := range j.RemoveAfter {
					os.RemoveAll(src)
				}
			}
			j.FinishedAt = time.Now()
			j.finished.Store(true)
			usbMu.Lock()
			if q := jobQueue[root]; len(q) > 0 {
				jobQueue[root] = q[1:]
				a.runJobLocked(root, q[0].j, q[0].list)
			}
			usbMu.Unlock()
			pPostMessageW.Call(a.hwnd, wmUsbChanged, 0, 0)
		}()
		buf := make([]byte, 1<<20)
		for _, c := range list {
			if j.cancel.Load() {
				return
			}
			if err := copyOneFile(c, buf, j); err != nil {
				if j.cancel.Load() {
					return
				}
				j.Err = err
				return
			}
			j.filesDone.Add(1)
		}
	}()
}

// copyOneFile copies to a temporary name first, so a stopped or failed copy never
// leaves a half file looking like the real one.
func copyOneFile(c copyItem, buf []byte, j *usbJob) error {
	if err := os.MkdirAll(filepath.Dir(c.dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(c.src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := c.dst + ".dgdock-part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for {
		if j.cancel.Load() {
			out.Close()
			os.Remove(tmp)
			return io.ErrUnexpectedEOF
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(tmp)
				return werr
			}
			j.done.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if fi, err := os.Stat(c.src); err == nil {
		os.Chtimes(tmp, fi.ModTime(), fi.ModTime()) // keep the original date
	}
	dst := c.dst
	if fileExists(dst) {
		dst = freeName(dst) // something else saved that name meanwhile: never replace it
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	j.Copied++
	return nil
}

// freeName returns path, or "name (2).ext" etc. when it's taken.
func freeName(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		p := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(p); err != nil {
			return p
		}
	}
}

// copyToDrive copies dropped files and folders onto the drive.
func (a *App) copyToDrive(root string, files []string) {
	var list []copyItem
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			list = append(list, copyItem{f, freeName(filepath.Join(root, fi.Name())), fi.Size()})
			continue
		}
		top := freeName(filepath.Join(root, fi.Name()))
		filepath.Walk(f, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(f, p)
			list = append(list, copyItem{p, filepath.Join(top, rel), info.Size()})
			return nil
		})
	}
	if len(list) == 0 {
		return
	}
	d, _ := a.usbDrive(root)
	var need int64
	for _, c := range list {
		need += c.size
	}
	if d.Total > 0 && uint64(need) > d.Free {
		messageBox(fmt.Sprintf("There isn't enough space on %s: these need %s and there's %s free.", d.Name(), fmtBytes(float64(need)), fmtBytes(float64(d.Free))), appName)
		return
	}
	a.startJob(root, &usbJob{Kind: "copy", Dest: root}, list)
	a.refreshUsbPop()
}

// importPhotos copies camera photos and videos (DCIM) into Pictures\Imported\<date>,
// skipping any that were imported before.
func (a *App) importPhotos(root string) {
	pics := knownFolder("{33E28130-4E1E-4676-835A-98395C3BC3BB}")
	if pics == "" {
		pics = filepath.Join(os.Getenv("USERPROFILE"), "Pictures")
	}
	d, _ := a.usbDrive(root)
	dest := filepath.Join(pics, "Imported", time.Now().Format("2006-01-02"))
	seen := map[string]bool{} // already in an earlier import (same name and size)
	filepath.Walk(filepath.Join(pics, "Imported"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			seen[strings.ToLower(info.Name())+fmt.Sprint(info.Size())] = true
		}
		return nil
	})
	var list []copyItem
	filepath.Walk(filepath.Join(root, "DCIM"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !photoExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		if seen[strings.ToLower(info.Name())+fmt.Sprint(info.Size())] {
			return nil
		}
		list = append(list, copyItem{p, filepath.Join(dest, info.Name()), info.Size()})
		return nil
	})
	sort.Slice(list, func(i, k int) bool { return list[i].src < list[k].src })
	// names can repeat across camera folders (100CANON\IMG_0001, 101CANON\IMG_0001)
	used := map[string]bool{}
	for i := range list {
		p := list[i].dst
		for n := 2; used[strings.ToLower(p)] || fileExists(p); n++ {
			ext := filepath.Ext(list[i].dst)
			p = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(list[i].dst, ext), n, ext)
		}
		used[strings.ToLower(p)] = true
		list[i].dst = p
	}
	j := &usbJob{Kind: "import", Dest: dest}
	if len(list) == 0 {
		j.finished.Store(true)
		j.FinishedAt = time.Now()
		j.Started = time.Now()
		usbMu.Lock()
		usbJobs[root] = j
		usbMu.Unlock()
		a.usbWasBusy = true
		_ = d
		return
	}
	a.startJob(root, j, list)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---------- eject ----------

// ejectDrive safely removes the drive, like "Safely Remove Hardware": it finds the
// disk behind the drive letter and asks Windows to eject the USB device it's on.
func (a *App) ejectDrive(root string) {
	if j := usbJobFor(root); j != nil && !j.finished.Load() {
		messageBox("Files are still copying. Stop the copy first, then eject.", appName)
		return
	}
	d, _ := a.usbDrive(root)
	a.closePop()
	vol := volOf(root)
	releaseVolume(vol) // the dock's own activity watch would otherwise block the eject
	go func() {
		letter := strings.TrimSuffix(root, `\`)
		err := ejectVolume(letter)
		if err != nil { // something (indexing, antivirus) often lets go a moment later
			time.Sleep(900 * time.Millisecond)
			err = ejectVolume(letter)
		}
		if err != nil {
			unreleaseVolume(vol)
			messageBox("Windows couldn't eject "+d.Name()+": "+err.Error()+"\n\nClose any windows or files that are using the drive and try again.", appName)
			return
		}
		pPostMessageW.Call(a.hwnd, wmUsbChanged, 1, 0)
	}()
}

func ejectVolume(letter string) error {
	p, _ := windows.UTF16PtrFromString(`\\.\` + letter)
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return err
	}
	var num [3]uint32 // STORAGE_DEVICE_NUMBER: type, number, partition
	var n uint32
	err = windows.DeviceIoControl(h, ioctlStorageGetDevNumber, nil, 0, (*byte)(unsafe.Pointer(&num[0])), 12, &n, nil)
	windows.CloseHandle(h)
	if err != nil {
		return err
	}
	// find the disk with that number among the disk devices
	paths, err := windows.CM_Get_Device_Interface_List("", &guidDevInterfaceDisk, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return fmt.Errorf("couldn't list disks")
	}
	instance := ""
	for _, path := range paths {
		pp, _ := windows.UTF16PtrFromString(path)
		dh, err := windows.CreateFile(pp, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			continue
		}
		var dn [3]uint32
		e := windows.DeviceIoControl(dh, ioctlStorageGetDevNumber, nil, 0, (*byte)(unsafe.Pointer(&dn[0])), 12, &n, nil)
		windows.CloseHandle(dh)
		if e == nil && dn[0] == num[0] && dn[1] == num[1] {
			// \\?\usbstor#disk&ven_x#123&0#{guid} → USBSTOR\DISK&VEN_X\123&0
			s := strings.TrimPrefix(path, `\\?\`)
			if i := strings.LastIndex(s, "#{"); i >= 0 {
				s = s[:i]
			}
			instance = strings.ReplaceAll(s, "#", `\`)
			break
		}
	}
	if instance == "" {
		return fmt.Errorf("couldn't find the device")
	}
	var dev uint32
	ip, _ := windows.UTF16PtrFromString(instance)
	if r, _, _ := pCMLocateDevNode.Call(uintptr(unsafe.Pointer(&dev)), uintptr(unsafe.Pointer(ip)), 0); r != 0 {
		return fmt.Errorf("couldn't find the device")
	}
	try := func(node uint32) error {
		var veto uint32
		var name [260]uint16
		r, _, _ := pCMRequestDeviceEject.Call(uintptr(node), uintptr(unsafe.Pointer(&veto)), uintptr(unsafe.Pointer(&name[0])), 260, 0)
		if r != 0 || veto != 0 {
			return fmt.Errorf("it's still in use")
		}
		return nil
	}
	// eject the USB device the disk sits on (that's what Safely Remove does)…
	var parent uint32
	if r, _, _ := pCMGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(dev), 0); r == 0 {
		if err := try(parent); err == nil {
			return nil
		}
	}
	return try(dev) // …or the disk itself
}
