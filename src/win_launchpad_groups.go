//go:build windows

package main

import (
	"sort"
	"strings"
)

// appGroup is the category or folder an app is shown in.
func (a *App) appGroup(id, name string) string {
	if g := a.cfg.LaunchpadGroups[strings.ToLower(id)]; g != "" {
		return g
	}
	return autoCategory(id, name)
}

type launchGroup struct {
	Name  string `json:"name"`
	User  bool   `json:"user"` // a folder you made (can be renamed)
	Count int    `json:"count"`
}

// launchpadGroups lists the categories in use, then your folders.
func (a *App) launchpadGroups(apps []launchpadApp) []launchGroup {
	count := map[string]int{}
	for _, ap := range apps {
		count[ap.Group]++
	}
	user := map[string]bool{}
	for _, f := range a.cfg.LaunchpadFolders {
		user[f] = true
	}
	var out []launchGroup
	for _, c := range launchCategories {
		if count[c] > 0 && !user[c] {
			out = append(out, launchGroup{Name: c, Count: count[c]})
		}
	}
	for _, f := range a.cfg.LaunchpadFolders {
		out = append(out, launchGroup{Name: f, User: true, Count: count[f]})
	}
	// a group someone moved apps into that's neither (an old folder name, say)
	var extra []string
	known := map[string]bool{}
	for _, g := range out {
		known[g.Name] = true
	}
	for g := range count {
		if !known[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(extra)
	for _, g := range extra {
		out = append(out, launchGroup{Name: g, User: true, Count: count[g]})
	}
	// your order first; anything new keeps its usual place after those
	if len(a.cfg.LaunchpadOrder) > 0 {
		rank := map[string]int{}
		for i, n := range a.cfg.LaunchpadOrder {
			rank[n] = i
		}
		sort.SliceStable(out, func(i, j int) bool {
			ri, oki := rank[out[i].Name]
			rj, okj := rank[out[j].Name]
			if oki != okj {
				return oki
			}
			return oki && ri < rj
		})
	}
	return out
}

// setAppGroups moves apps into a category or folder ("" = back to where DG-Dock puts them).
func (a *App) setAppGroups(ids []string, names []string, group string) {
	if a.cfg.LaunchpadGroups == nil {
		a.cfg.LaunchpadGroups = map[string]string{}
	}
	for i, id := range ids {
		k := strings.ToLower(id)
		name := ""
		if i < len(names) {
			name = names[i]
		}
		if group == "" || group == autoCategory(id, name) {
			delete(a.cfg.LaunchpadGroups, k)
		} else {
			a.cfg.LaunchpadGroups[k] = group
		}
	}
	if group != "" && !isCategory(group) && !hasString(a.cfg.LaunchpadFolders, group) {
		a.cfg.LaunchpadFolders = append(a.cfg.LaunchpadFolders, group)
	}
	a.saveConfig()
}

// uniqueFolderName: "New category", "New category 2", …
func (a *App) uniqueFolderName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "New category"
	}
	taken := func(n string) bool {
		if isCategory(n) || hasString(a.cfg.LaunchpadFolders, n) {
			return true
		}
		for _, g := range a.cfg.LaunchpadGroups {
			if strings.EqualFold(g, n) {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		n := base + " " + itoa(i)
		if !taken(n) {
			return n
		}
	}
}

// renameFolder renames one of your folders, keeping its apps in it.
func (a *App) renameFolder(old, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == old {
		return old
	}
	name = a.uniqueFolderName(name)
	for i, f := range a.cfg.LaunchpadFolders {
		if f == old {
			a.cfg.LaunchpadFolders[i] = name
		}
	}
	for k, g := range a.cfg.LaunchpadGroups {
		if g == old {
			a.cfg.LaunchpadGroups[k] = name
		}
	}
	for i, f := range a.cfg.LaunchpadFavs {
		if f == "folder:"+old {
			a.cfg.LaunchpadFavs[i] = "folder:" + name
		}
	}
	a.saveConfig()
	return name
}

// deleteFolder removes one of your folders; its apps go back to their categories.
func (a *App) deleteFolder(name string) {
	var keep []string
	for _, f := range a.cfg.LaunchpadFolders {
		if f != name {
			keep = append(keep, f)
		}
	}
	a.cfg.LaunchpadFolders = keep
	for k, g := range a.cfg.LaunchpadGroups {
		if g == name {
			delete(a.cfg.LaunchpadGroups, k)
		}
	}
	var favs []string
	for _, f := range a.cfg.LaunchpadFavs {
		if f != "folder:"+name {
			favs = append(favs, f)
		}
	}
	if a.cfg.LaunchpadFavs != nil {
		if favs == nil {
			favs = []string{}
		}
		a.cfg.LaunchpadFavs = favs
	}
	a.saveConfig()
}

// pruneEmptyFolders drops folders with nothing in them (after Launchpad closes).
func (a *App) pruneEmptyFolders() {
	used := map[string]bool{}
	for _, g := range a.cfg.LaunchpadGroups {
		used[g] = true
	}
	var keep []string
	for _, f := range a.cfg.LaunchpadFolders {
		if used[f] {
			keep = append(keep, f)
		}
	}
	if len(keep) != len(a.cfg.LaunchpadFolders) {
		for _, f := range a.cfg.LaunchpadFolders {
			if !used[f] {
				a.deleteFolder(f)
			}
		}
		a.cfg.LaunchpadFolders = keep
		a.saveConfig()
	}
}
