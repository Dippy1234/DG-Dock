//go:build windows

package main

// Uninstall from Launchpad, the way Windows' own Start menu does it: a Store app opens
// its page in Settings › Apps (with the Uninstall button); a desktop app runs its own
// uninstaller, found in the list Windows keeps of installed programs. When an app
// can't be matched, Settings › Installed apps opens instead.

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type uninstallEntry struct {
	name, cmd, location, icon string
}

// installedPrograms reads the programs Windows lists under Installed apps.
func installedPrograms() []uninstallEntry {
	var out []uninstallEntry
	roots := []struct {
		k    registry.Key
		path string
		view uint32
	}{
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_32KEY},
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, 0},
	}
	for _, r := range roots {
		k, err := registry.OpenKey(r.k, r.path, registry.READ|r.view)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		for _, n := range names {
			sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE|r.view)
			if err != nil {
				continue
			}
			e := uninstallEntry{}
			e.name, _, _ = sk.GetStringValue("DisplayName")
			e.cmd, _, _ = sk.GetStringValue("UninstallString")
			e.location, _, _ = sk.GetStringValue("InstallLocation")
			e.icon, _, _ = sk.GetStringValue("DisplayIcon")
			if sys, _, err := sk.GetIntegerValue("SystemComponent"); err == nil && sys == 1 {
				e.cmd = ""
			}
			sk.Close()
			if e.name != "" && e.cmd != "" {
				out = append(out, e)
			}
		}
		k.Close()
	}
	return out
}

// findUninstaller matches an app (its exe and name) to an installed program.
func findUninstaller(exe, name string) (uninstallEntry, bool) {
	exe = strings.ToLower(filepath.Clean(exe))
	dir := ""
	if exe != "." && exe != "" {
		dir = strings.ToLower(filepath.Dir(exe)) + `\`
	}
	ln := strings.ToLower(strings.TrimSpace(name))
	var byName, byPrefix uninstallEntry
	nameOK, prefixOK := false, false
	for _, e := range installedPrograms() {
		icon := strings.ToLower(strings.Trim(strings.SplitN(e.icon, ",", 2)[0], `" `))
		loc := strings.ToLower(strings.TrimRight(strings.Trim(e.location, `" `), `\`))
		switch {
		case exe != "" && icon != "" && filepath.Clean(icon) == exe:
			return e, true // its icon is this very program
		case dir != "" && loc != "" && strings.HasPrefix(dir, loc+`\`):
			if !prefixOK || len(loc) > len(strings.TrimRight(byPrefix.location, `\`)) {
				byPrefix, prefixOK = e, true // the deepest install folder that holds it
			}
		case ln != "" && strings.EqualFold(strings.TrimSpace(e.name), ln):
			byName, nameOK = e, true
		}
	}
	if prefixOK {
		return byPrefix, true
	}
	if nameOK {
		return byName, true
	}
	// a looser name match: "Affinity Photo 2" for "Affinity Photo"
	if ln != "" {
		for _, e := range installedPrograms() {
			en := strings.ToLower(e.name)
			if strings.HasPrefix(en, ln+" ") || strings.HasPrefix(ln, en+" ") {
				return e, true
			}
		}
	}
	return uninstallEntry{}, false
}

// splitCommand splits an uninstall command into the program and its arguments.
func splitCommand(cmd string) (string, string) {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.Index(cmd[1:], `"`); i >= 0 {
			return cmd[1 : i+1], strings.TrimSpace(cmd[i+2:])
		}
	}
	low := strings.ToLower(cmd)
	if i := strings.Index(low, ".exe"); i >= 0 {
		return cmd[:i+4], strings.TrimSpace(cmd[i+4:])
	}
	if i := strings.Index(cmd, " "); i >= 0 {
		return cmd[:i], strings.TrimSpace(cmd[i+1:])
	}
	return cmd, ""
}

// uninstallApp starts uninstalling a Launchpad app (after asking).
func (a *App) uninstallApp(id, name string) {
	a.hideLaunchpad()
	if fam, _, ok := strings.Cut(id, "!"); ok && !strings.ContainsAny(fam, `\/:`) {
		// a Store app: its own page in Settings, with Uninstall on it
		shellOpen("ms-settings:appsfeatures-app?"+fam, "")
		return
	}
	exe := appTargetPath(id)
	e, ok := findUninstaller(exe, name)
	if !ok {
		messageBox(fmt.Sprintf("DG-Dock couldn't find the uninstaller for %s, so Settings › Installed apps will open — find %s there and choose Uninstall.", name, name), appName)
		shellOpen("ms-settings:appsfeatures", "")
		return
	}
	go func() {
		if !askYesNo(fmt.Sprintf("Uninstall %s?\n\nIts uninstaller will open. Windows may ask for permission.", e.name), "Uninstall "+name) {
			return
		}
		prog, args := splitCommand(e.cmd)
		if strings.EqualFold(filepath.Base(prog), "msiexec.exe") || strings.EqualFold(prog, "msiexec") {
			args = strings.Replace(args, "/I{", "/X{", 1) // some installers list "repair" (/I) as their uninstall
		}
		shellOpen(prog, args)
	}()
}
