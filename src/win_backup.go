//go:build windows

package main

// Backup and restore: "Save my setup" writes one zip with the settings, icon packs and
// the Pictures › DG-Dock Icon folder; "Restore a setup" puts them back (also on another
// PC or Windows account: paths inside the settings are moved to this account's folders).

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type backupManifest struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	Build    int    `json:"build"`
	Saved    string `json:"saved"`
	DataDir  string `json:"dataDir"`
	IconsDir string `json:"iconsDir"`
}

const backupMaxBytes = 2 << 30 // refuse absurd archives

// saveBackup writes the backup into a folder the user picks and returns its path.
func (a *App) saveBackup(owner uintptr) (string, error) {
	dir := pickPath(owner, true, "Choose where to save your DG-Dock setup", nil)
	if dir == "" {
		return "", nil
	}
	a.saveConfig()
	out := filepath.Join(dir, "DG-Dock setup "+time.Now().Format("2006-01-02")+".zip")
	for n := 2; fileExists(out); n++ {
		out = filepath.Join(dir, fmt.Sprintf("DG-Dock setup %s (%d).zip", time.Now().Format("2006-01-02"), n))
	}
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	fail := func(err error) (string, error) {
		zw.Close()
		f.Close()
		os.Remove(out)
		return "", err
	}
	man, _ := json.MarshalIndent(backupManifest{App: appName, Version: releaseVersion, Build: appVersion,
		Saved: time.Now().Format(time.RFC3339), DataDir: dataDir(), IconsDir: iconsDir()}, "", "  ")
	w, err := zw.Create("manifest.json")
	if err != nil {
		return fail(err)
	}
	w.Write(man)
	addTree := func(root, prefix string, skip func(rel string) bool) error {
		return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !info.Mode().IsRegular() {
				return nil // a file that can't be read is left out rather than failing the backup
			}
			rel, err := filepath.Rel(root, p)
			if err != nil || skip(rel) {
				return nil
			}
			src, err := os.Open(p)
			if err != nil {
				return nil
			}
			defer src.Close()
			h, _ := zip.FileInfoHeader(info)
			h.Name = prefix + "/" + filepath.ToSlash(rel)
			h.Method = zip.Deflate
			dst, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			_, err = io.Copy(dst, src)
			return err
		})
	}
	if err := addTree(dataDir(), "data", func(rel string) bool {
		l := strings.ToLower(rel)
		return l == "log.txt" || strings.HasSuffix(l, ".tmp")
	}); err != nil {
		return fail(err)
	}
	if d := iconsDir(); d != "" {
		if err := addTree(d, "icons", func(string) bool { return false }); err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		os.Remove(out)
		return "", err
	}
	return out, f.Close()
}

// jsonPath is a Windows path as it appears inside JSON text (backslashes doubled).
func jsonPath(p string) string {
	b, _ := json.Marshal(p)
	return strings.Trim(string(b), `"`)
}

// restoreBackup replaces the current setup with the one in a backup zip, then
// restarts the dock. It returns "" when nothing was chosen.
func (a *App) restoreBackup(owner uintptr) error {
	p := pickPath(owner, false, "Choose a DG-Dock setup to restore", [][2]string{{"DG-Dock setup", "*.zip"}})
	if p == "" {
		return nil
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return fmt.Errorf("that file couldn't be opened as a DG-Dock setup")
	}
	defer zr.Close()
	var man backupManifest
	var total uint64
	hasSettings := false
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if f.Name == "manifest.json" {
			if rc, err := f.Open(); err == nil {
				json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&man)
				rc.Close()
			}
		}
		if f.Name == "data/settings.json" {
			hasSettings = true
		}
	}
	if man.App != appName || !hasSettings {
		return fmt.Errorf("that zip isn't a DG-Dock setup")
	}
	if total > backupMaxBytes {
		return fmt.Errorf("that setup is too large to restore")
	}
	if man.Build > appVersion {
		return fmt.Errorf("that setup was saved by a newer DG-Dock (build %d). Update DG-Dock first, then restore it", man.Build)
	}
	if !askYesNo("Restore this setup?\n\nYour current pins, settings, icon packs and icons are replaced by the ones saved on "+
		strings.SplitN(man.Saved, "T", 2)[0]+". DG-Dock restarts by itself.", "Restore a setup") {
		return nil
	}
	data, icons := dataDir(), iconsDir()
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		var dst string
		switch {
		case strings.HasPrefix(f.Name, "data/"):
			dst = filepath.Join(data, name[len("data/"):])
		case strings.HasPrefix(f.Name, "icons/") && icons != "":
			dst = filepath.Join(icons, name[len("icons/"):])
		default:
			continue
		}
		root := data
		if strings.HasPrefix(f.Name, "icons/") {
			root = icons
		}
		// never write outside the two folders, whatever the zip says
		if f.FileInfo().IsDir() || strings.Contains(f.Name, "..") || !strings.HasPrefix(strings.ToLower(filepath.Clean(dst)), strings.ToLower(filepath.Clean(root))+`\`) {
			continue
		}
		os.MkdirAll(filepath.Dir(dst), 0755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		var body []byte
		if f.Name == "data/settings.json" {
			// paths saved under another account or PC now point at this one's folders
			body, err = io.ReadAll(io.LimitReader(rc, 16<<20))
			if err == nil {
				s := string(body)
				for _, r := range [][2]string{{man.IconsDir, icons}, {man.DataDir, data}} {
					if r[0] != "" && r[1] != "" && !strings.EqualFold(r[0], r[1]) {
						s = strings.ReplaceAll(s, jsonPath(r[0]), jsonPath(r[1]))
					}
				}
				if !json.Valid([]byte(s)) {
					rc.Close()
					return fmt.Errorf("the settings in that setup are damaged")
				}
				err = writeFileAtomic(dst, []byte(s))
			}
		} else {
			var out *os.File
			if out, err = os.Create(dst); err == nil {
				_, err = io.Copy(out, io.LimitReader(rc, backupMaxBytes))
				out.Close()
			}
		}
		rc.Close()
		if err != nil {
			return err
		}
	}
	// start again from the restored files (without saving the old settings over them)
	a.noSave = true
	exec.Command(exePath(), "--wait").Start()
	a.quit()
	return nil
}
