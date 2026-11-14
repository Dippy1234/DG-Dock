//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func packsRoot() string { return filepath.Join(dataDir(), "IconPacks") }

func (a *App) loadPack() {
	a.pack = nil
	if a.cfg.IconPack == "" {
		return
	}
	p, err := LoadIconPack(filepath.Join(packsRoot(), a.cfg.IconPack))
	if err != nil {
		a.logf("icon pack %q: %v", a.cfg.IconPack, err)
		return
	}
	a.pack = p
}

// applyPack swaps an item's icon for the icon pack's version, when the pack has one.
func (a *App) applyPack(it *Item) {
	it.PackIcon = false
	if a.pack == nil || it.Kind == KindSep {
		return
	}
	d := a.pack.Match(it.Name)
	if d == "" && (it.IsDir || it.Kind == KindFile) {
		d = a.packFileGlyph(it.Target, it.IsDir) // a folder or file pinned to the dock
	}
	if d == "" {
		return
	}
	if img := a.customIcon(a.pack.Path(d)); img != nil {
		it.Raw, it.RawKey = img, "pack:"+a.cfg.IconPack+":"+d+a.packSigIf()
		it.PackIcon = true
	}
}

// customPinIconForWindow finds a user-selected pin icon for a window even when
// that pin is on another app page and the window is shown in the running group.
func (a *App) customPinIconForWindow(w WinInfo) string {
	if w.AUMID != "" {
		if p := a.cfg.IconOverrides[appIconKey(w.AUMID)]; p != "" {
			return p // chosen in Launchpad
		}
	}
	for _, p := range a.cfg.Pins {
		if p.Icon == "" {
			continue
		}
		matched := false
		switch p.Kind {
		case KindApp:
			matched = w.AUMID != "" && strings.EqualFold(p.Target, w.AUMID)
			if !matched && w.Exe != "" {
				if target := appTargetPath(p.Target); target != "" && normPath(target) == normPath(w.Exe) {
					matched = true
				}
			}
		case KindFile:
			target := p.Target
			if strings.EqualFold(filepath.Ext(target), ".lnk") {
				target = resolveShortcut(target)
			}
			matched = target != "" && w.Exe != "" && normPath(target) == normPath(w.Exe)
		}
		if matched {
			return p.Icon
		}
	}
	return ""
}

func (a *App) refreshRunningIcon(it *Item, w WinInfo) {
	oldKey, oldPack := it.RawKey, it.PackIcon
	if strings.Contains(w.AUMID, "!") {
		it.RawKey = `shell:AppsFolder\` + w.AUMID
		it.Raw = a.rawIcon(it.RawKey, false)
	} else {
		it.RawKey = w.Exe
		it.Raw = a.rawIcon(w.Exe, false)
	}
	it.PackIcon = false
	if path := a.customPinIconForWindow(w); path != "" {
		if img := a.customIcon(path); img != nil {
			it.Raw, it.RawKey = img, "custom:"+path
			it.PackIcon = true
		}
	} else {
		a.applyPack(it)
	}
	if oldKey != it.RawKey || oldPack != it.PackIcon || it.Icon == nil {
		a.assignIcon(it, it.RawKey, it.Raw)
	}
}

// reloadItems rebuilds every dock icon (after the icon pack changes).
func (a *App) reloadItems() {
	for k := range a.items {
		if k != "sep" {
			delete(a.items, k)
		}
	}
	a.styled = map[string]*IconSet{}
	a.rebuildItems()
	a.scanRunning()
	for _, it := range a.dock.Items {
		it.Appear = 1
	}
	a.relayout()
	a.refreshSettings()
}

func (a *App) setIconPack(name string) {
	a.cfg.IconPack = name
	a.saveConfig()
	a.loadPack()
	a.reloadItems()
}

func (a *App) setIconPackScale(value int) {
	value = max(0, min(value, 150))
	if value == a.cfg.IconPackScale {
		return
	}
	a.cfg.IconPackScale = value
	a.restyleDockIcons()
	a.saveConfigLater()
	a.render()
}

func (a *App) setAppIconScale(value int) {
	value = max(0, min(value, 150))
	if value == a.cfg.AppIconScale {
		return
	}
	a.cfg.AppIconScale = value
	a.restyleDockIcons()
	a.saveConfigLater()
	a.render()
}

func (a *App) restyleDockIcons() {
	a.styled = map[string]*IconSet{}
	for _, it := range a.items {
		if it.Raw != nil {
			it.scaled, it.scaledSrc = nil, nil
			it.scaledSize = 0
			a.styleItem(it)
		}
	}
	if it := a.items["w:tray"]; it != nil {
		it.Icon = a.trayWidgetIcon()
	}
	a.needRender = true
}

func (a *App) setWidgetIconScale(value int) {
	value = max(0, min(value, 150))
	if value == a.cfg.WidgetIconScale {
		return
	}
	a.cfg.WidgetIconScale = value
	a.mediaFallbackPath, a.mediaFallbackArt = "", nil
	for key := range a.styled {
		if strings.HasPrefix(key, "widget-tray:") {
			delete(a.styled, key)
		}
	}
	if it := a.items["w:media"]; it != nil {
		it.DefaultArt = a.mediaFallbackIcon()
		it.scaled, it.scaledSrc = nil, nil
		it.scaledSize = 0
	}
	if it := a.items["w:tray"]; it != nil {
		it.Icon = a.trayWidgetIcon()
		it.scaled, it.scaledSrc = nil, nil
		it.scaledSize = 0
	}
	a.saveConfigLater()
	a.needRender = true
	a.render()
}

func (a *App) removeIconPack(name string) bool {
	if isBuiltinPack(name) {
		return false // comes with DG-Dock
	}
	root, err := filepath.Abs(packsRoot())
	if err != nil || name == "" {
		return false
	}
	actual := ""
	for _, installed := range ListIconPacks(root) {
		if strings.EqualFold(installed, name) {
			actual = installed
			break
		}
	}
	if actual == "" {
		return false
	}
	target, err := filepath.Abs(filepath.Join(root, actual))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if err := os.RemoveAll(target); err != nil {
		a.logf("remove icon pack %q: %v", actual, err)
		return false
	}
	if strings.EqualFold(a.cfg.IconPack, actual) {
		a.cfg.IconPack = ""
		a.saveConfig()
		a.loadPack()
		a.reloadItems()
	} else {
		a.refreshSettings()
	}
	return true
}

func (a *App) importPackDialog(owner uintptr) {
	p := pickPath(owner, false, "Choose an Android icon pack (.apk)", [][2]string{{"Icon pack APK", "*.apk"}})
	if p == "" {
		return
	}
	pack, err := ImportIconPack(p, packsRoot())
	if err != nil {
		messageBox("Couldn't import that icon pack: "+err.Error(), appName)
		return
	}
	a.logf("imported icon pack %q (%d icons)", pack.Name, len(pack.Icons))
	a.setIconPack(pack.Name)
}

type apkResult struct {
	file  string
	mod   int64
	name  string
	icons int
	err   error
}

// checkApkFolder imports icon packs dropped into Pictures\DG-Dock Icon\APK, one at a
// time and off the dock's thread (unpacking a pack takes a moment).
func (a *App) checkApkFolder() {
	if a.apkDone == nil {
		a.apkDone = make(chan apkResult, 4)
	}
	select {
	case r := <-a.apkDone:
		a.apkBusy = false
		if a.cfg.ImportedApks == nil {
			a.cfg.ImportedApks = map[string]int64{}
		}
		a.cfg.ImportedApks[r.file] = r.mod // also on failure, so a bad file isn't tried again and again
		a.saveConfig()
		if r.err != nil {
			a.logf("icon pack %s: %v", r.file, r.err)
			return
		}
		a.logf("imported icon pack %q from the APK folder (%d icons)", r.name, r.icons)
		if a.cfg.IconPack == "" {
			a.setIconPack(r.name) // the first pack: use it straight away
		} else {
			a.refreshSettings()
		}
	default:
	}
	if a.apkBusy || a.scanCount%3 != 0 {
		return
	}
	ents, err := os.ReadDir(apkDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".apk") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < 3*time.Second {
			continue // still being copied in
		}
		mod := info.ModTime().Unix()
		if a.cfg.ImportedApks[e.Name()] == mod {
			continue
		}
		a.apkBusy = true
		file, path := e.Name(), filepath.Join(apkDir(), e.Name())
		done := a.apkDone
		go func() {
			r := apkResult{file: file, mod: mod}
			if pack, err := ImportIconPack(path, packsRoot()); err != nil {
				r.err = err
			} else {
				r.name, r.icons = pack.Name, len(pack.Icons)
			}
			done <- r
		}()
		return
	}
}

// packFileGlyph is the pack's icon for a folder or file (by folder name or file type),
// for packs that have them (the built-in Black and White Icons do), or "".
func (a *App) packFileGlyph(p string, isDir bool) string {
	if a.pack == nil || p == "" {
		return ""
	}
	pick := func(names ...string) string {
		for _, n := range names {
			if a.pack.has(n) {
				return n
			}
		}
		return ""
	}
	base := filepath.Base(p)
	if isDir {
		switch normName(base) {
		case "music", "mymusic":
			return pick("foldermusic", "folder")
		case "pictures", "mypictures", "photos", "screenshots", "camerraroll", "cameraroll":
			return pick("folderpictures", "folder")
		case "videos", "myvideos", "captures":
			return pick("foldervideos", "folder")
		case "desktop":
			return pick("folderdesktop", "folder")
		case "downloads":
			return pick("downloads", "folder")
		case "documents", "mydocuments":
			return pick("documents", "folder")
		case "onedrive":
			return pick("onedrive", "folder")
		}
		return pick("folder")
	}
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".lnk", ".url", ".appref-ms":
		if d := a.pack.Match(strings.TrimSuffix(base, filepath.Ext(base))); d != "" {
			return d // a shortcut to an app the pack knows
		}
		return pick("fileshortcut", "file")
	case ".txt", ".log", ".md", ".ini", ".cfg", ".conf", ".json", ".xml", ".yaml", ".yml", ".csv", ".nfo":
		return pick("filetext", "file")
	case ".pdf":
		return pick("filepdf", "file")
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".heic", ".heif", ".avif", ".svg", ".ico", ".tif", ".tiff", ".raw", ".psd":
		return pick("fileimage", "file")
	case ".mp4", ".mkv", ".mov", ".avi", ".wmv", ".webm", ".m4v", ".flv", ".mpg", ".mpeg":
		return pick("filevideo", "file")
	case ".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wma", ".aiff":
		return pick("fileaudio", "file")
	case ".zip", ".7z", ".rar", ".tar", ".gz", ".xz", ".bz2", ".zst", ".apk", ".iso", ".img", ".cab":
		return pick("filearchive", "file")
	case ".exe", ".msi", ".msix", ".appx", ".msixbundle", ".appxbundle", ".bat", ".cmd":
		return pick("fileapp", "file")
	case ".doc", ".docx", ".rtf", ".odt", ".pages":
		return pick("filedoc", "file")
	case ".xls", ".xlsx", ".ods", ".numbers":
		return pick("filesheet", "file")
	case ".ppt", ".pptx", ".odp", ".key":
		return pick("fileslides", "file")
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".html", ".htm", ".css", ".c", ".h", ".cpp", ".hpp", ".cs", ".java", ".kt", ".rs", ".swift", ".sh", ".ps1", ".lua", ".rb", ".php", ".sql", ".vue":
		return pick("filecode", "file")
	}
	return pick("file")
}

// packFileIcon is a folder card tile's icon from the pack, shaped like the dock's tiles.
func (a *App) packFileIcon(p string, isDir bool) *IconSet {
	d := a.packFileGlyph(p, isDir)
	if d == "" {
		return nil
	}
	style := a.itemStyle(nil)
	key := "packfile:" + a.cfg.IconPack + ":" + d + "|" + style + a.packSigIf()
	if is, ok := a.styled[key]; ok {
		return is
	}
	raw := a.customIcon(a.pack.Path(d))
	if raw == nil {
		return nil
	}
	is := NewIconSet(styleIcon(raw, style))
	a.styled[key] = is
	return is
}

// packSigIf is packSig when the icon pack in use is a dynamic one, else "".
func (a *App) packSigIf() string {
	if a.pack != nil && a.pack.Dynamic {
		return a.packSig()
	}
	return ""
}
