//go:build windows

package main

// Update watcher: the dock looks in Downloads for a newer DG-Dock exe (any file
// named DG-Dock*.exe, or MacDock*.exe from before the rename) and offers to update to it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const wmUpdate = WM_APP + 12

type updateWatch struct {
	mu   sync.Mutex
	path string
	ver  int
	seen map[string]string // path -> size|mtime already checked
}

var updates = &updateWatch{seen: map[string]string{}}

func (u *updateWatch) found() (string, int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.path != "" {
		if _, err := os.Stat(u.path); err != nil { // deleted since
			u.path, u.ver = "", 0
		}
	}
	return u.path, u.ver
}

func (u *updateWatch) scan() bool {
	dl := knownFolder("{374DE290-123F-4565-9164-39C4925E467B}")
	if dl == "" {
		return false
	}
	bestPath, best := "", appVersion
	for _, dir := range []string{dl, filepath.Join(dl, "MacDock"), filepath.Join(dl, appName)} {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			n := strings.ToLower(e.Name())
			if e.IsDir() || !isDockExeName(n) {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Size() < 1<<20 || info.Size() > 200<<20 {
				continue
			}
			p := filepath.Join(dir, e.Name())
			stamp := fmt.Sprintf("%d|%d", info.Size(), info.ModTime().UnixNano())
			u.mu.Lock()
			prev, ok := u.seen[p]
			u.mu.Unlock()
			var v int
			if ok && strings.HasPrefix(prev, stamp+"|") {
				fmt.Sscanf(strings.TrimPrefix(prev, stamp+"|"), "%d", &v)
			} else {
				if time.Since(info.ModTime()) < 3*time.Second {
					continue // still downloading
				}
				v = fileVersion(p)
				u.mu.Lock()
				u.seen[p] = fmt.Sprintf("%s|%d", stamp, v)
				u.mu.Unlock()
			}
			if v > best {
				best, bestPath = v, p
			}
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	changed := bestPath != u.path
	u.path = bestPath
	if bestPath == "" {
		u.ver = 0
	} else {
		u.ver = best
	}
	return changed
}

// startUpdateWatch checks Downloads shortly after start and then every 30 seconds.
func (a *App) startUpdateWatch() {
	hwnd := a.hwnd
	go func() {
		time.Sleep(4 * time.Second)
		for {
			if updates.scan() {
				pPostMessageW.Call(hwnd, wmUpdate, 0, 0)
			}
			time.Sleep(30 * time.Second)
		}
	}()
}

// onUpdateFound runs on the UI thread when a newer exe appears (or goes away).
func (a *App) onUpdateFound() {
	a.refreshSettings()
	path, ver := updates.found()
	if path == "" || a.cfg.UpdatePrompted >= ver {
		return
	}
	a.cfg.UpdatePrompted = ver
	a.saveConfig()
	go func() {
		if askYesNo(fmt.Sprintf("DG-Dock version %d is in your Downloads.\n\nUpdate now? Your dock, settings and icon packs stay as they are.", ver), appName) {
			pPostMessageW.Call(a.hwnd, wmUpdate, 1, 0)
		}
	}()
}

// startUpdate hands over to the new exe, which closes this dock, installs itself
// and starts again.
func (a *App) startUpdate() {
	path, _ := updates.found()
	if path == "" {
		return
	}
	if err := exec.Command(path, "--update").Start(); err != nil {
		messageBox("Couldn't start the update: "+err.Error(), appName)
	}
}

// isDockExeName reports whether a (lower-case) file name looks like one of our exes.
func isDockExeName(n string) bool {
	if !strings.HasSuffix(n, ".exe") {
		return false
	}
	for _, p := range []string{"dg-dock", "dgdock", "dg dock", "macdock"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
