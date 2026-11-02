//go:build windows

package main

import (
	"path/filepath"
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
	if a.pack == nil || it.Kind == KindSep {
		return
	}
	d := a.pack.Match(it.Name)
	if d == "" {
		return
	}
	if img := a.customIcon(a.pack.Path(d)); img != nil {
		it.Raw, it.RawKey = img, "pack:"+a.cfg.IconPack+":"+d
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
