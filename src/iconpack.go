package main

// Android icon pack support: import an icon pack APK (assets/appfilter.xml +
// drawables) into a folder of PNGs, then match dock items to icons by app name.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	_ "golang.org/x/image/webp"
)

type IconPack struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"` // normalised app name -> drawable
	Icons  []string          `json:"icons"`
	Dir    string            `json:"-"`
	keys   map[string]string
}

func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// packName turns "My_Icon_Pack_v1_2.apk" into "My Icon Pack".
func packName(apk string) string {
	b := strings.TrimSuffix(filepath.Base(apk), filepath.Ext(apk))
	b = strings.ReplaceAll(b, "_", " ")
	if i := regexp.MustCompile(`(?i)\s+v?\d+(\s|\.|$)`).FindStringIndex(b); i != nil && i[0] > 0 {
		b = b[:i[0]]
	}
	b = regexp.MustCompile(`\s*[\(\[].*$`).ReplaceAllString(b, "")
	b = strings.TrimSpace(b)
	if b == "" {
		b = "Icon pack"
	}
	return b
}

var (
	reFilterTok = regexp.MustCompile(`<!--\s*([\s\S]*?)\s*-->|<item\s[^>]*?component="([^"]*)"[^>]*?drawable="([^"]*)"`)
	reDrawable  = regexp.MustCompile(`^res/drawable[^/]*/([a-z0-9_]+)\.(png|webp)$`)
)

func dpiRank(path string) int {
	switch {
	case strings.Contains(path, "nodpi"):
		return 6
	case strings.Contains(path, "xxxhdpi"):
		return 5
	case strings.Contains(path, "xxhdpi"):
		return 4
	case strings.Contains(path, "xhdpi"):
		return 3
	case strings.Contains(path, "hdpi"):
		return 2
	}
	return 1
}

// ImportIconPack extracts an icon pack APK into root/<pack name>.
func ImportIconPack(apk, root string) (*IconPack, error) {
	zr, err := zip.OpenReader(apk)
	if err != nil {
		return nil, errors.New("that file isn't a valid APK")
	}
	defer zr.Close()
	var filter []byte
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if f.Name == "assets/appfilter.xml" {
			rc, err := f.Open()
			if err == nil {
				filter, _ = io.ReadAll(rc)
				rc.Close()
			}
		}
		if m := reDrawable.FindStringSubmatch(f.Name); m != nil {
			if old, ok := files[m[1]]; !ok || dpiRank(f.Name) > dpiRank(old.Name) {
				files[m[1]] = f
			}
		}
	}
	if len(filter) == 0 {
		return nil, errors.New("this APK doesn't look like an icon pack (no appfilter.xml)")
	}
	pack := &IconPack{Name: packName(apk), Labels: map[string]string{}}
	label := ""
	wanted := map[string]bool{}
	for _, t := range reFilterTok.FindAllSubmatch(filter, -1) {
		if t[1] != nil {
			label = string(t[1])
			continue
		}
		d := string(t[3])
		if _, ok := files[d]; !ok {
			continue
		}
		wanted[d] = true
		if k := normName(label); k != "" && len(label) < 60 {
			if _, exists := pack.Labels[k]; !exists {
				pack.Labels[k] = d
			}
		}
	}
	if len(wanted) == 0 {
		return nil, errors.New("no icons found in this icon pack")
	}
	// offer every full-size drawable in the picker too, not just the mapped ones
	for d, f := range files {
		if strings.Contains(f.Name, "nodpi") {
			wanted[d] = true
		}
	}
	dir := filepath.Join(root, pack.Name)
	os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "icons"), 0755); err != nil {
		return nil, err
	}
	for d := range wanted {
		f := files[d]
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		if strings.HasSuffix(f.Name, ".webp") {
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				continue
			}
			var buf bytes.Buffer
			if png.Encode(&buf, img) != nil {
				continue
			}
			data = buf.Bytes()
		}
		if os.WriteFile(filepath.Join(dir, "icons", d+".png"), data, 0644) == nil {
			pack.Icons = append(pack.Icons, d)
		}
	}
	sort.Strings(pack.Icons)
	b, _ := json.Marshal(pack)
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), b, 0644); err != nil {
		return nil, err
	}
	pack.Dir = dir
	pack.index()
	return pack, nil
}

func LoadIconPack(dir string) (*IconPack, error) {
	b, err := os.ReadFile(filepath.Join(dir, "pack.json"))
	if err != nil {
		return nil, err
	}
	p := &IconPack{}
	if err := json.Unmarshal(b, p); err != nil {
		return nil, err
	}
	p.Dir = dir
	p.index()
	return p, nil
}

func ListIconPacks(root string) []string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "pack.json")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

var vendorPrefixes = []string{"microsoft", "google", "apple", "samsung", "adobe", "amazon"}

func (p *IconPack) index() {
	p.keys = map[string]string{}
	for _, d := range p.Icons {
		k := normName(d)
		p.keys[k] = d
	}
	for _, d := range p.Icons { // "google_chrome" also answers to "chrome"
		k := normName(d)
		for _, v := range vendorPrefixes {
			if strings.HasPrefix(k, v) && len(k) > len(v)+2 {
				if _, ok := p.keys[k[len(v):]]; !ok {
					p.keys[k[len(v):]] = d
				}
			}
		}
	}
	for k, d := range p.Labels {
		if _, ok := p.keys[k]; !ok {
			p.keys[k] = d
		}
	}
}

// Windows names that differ from their Android counterparts.
var packAliases = map[string][]string{
	"start":             {"appdrawer_iphone", "app_drawer", "launcher"},
	"fileexplorer":      {"files", "google_files", "file_manager"},
	"windowsexplorer":   {"files", "google_files", "file_manager"},
	"notepad":           {"notes", "colornote"},
	"microsoftstore":    {"store", "apple_appstore", "google_play_store"},
	"photos":            {"photos", "google_photos"},
	"settings":          {"settings"},
	"calculator":        {"calculator"},
	"camera":            {"camera"},
	"clock":             {"clock"},
	"mail":              {"mail"},
	"outlook":           {"microsoft_outlook"},
	"windowsterminal":   {"terminal_emulator", "terminal"},
	"terminal":          {"terminal_emulator", "terminal"},
	"commandprompt":     {"terminal_emulator", "terminal"},
	"mediaplayer":       {"apple_music", "music"},
	"snippingtool":      {"screenshot", "screenshots"},
	"paint":             {"paint", "sketchbook"},
	"weather":           {"weather"},
	"maps":              {"google_maps", "apple_maps", "maps"},
	"xbox":              {"xbox"},
	"gamebar":           {"xbox"},
	"googlechrome":      {"google_chrome"},
	"microsoftedge":     {"microsoft_edge"},
	"microsoftteams":    {"microsoft_teams"},
	"teams":             {"microsoft_teams"},
	"onedrive":          {"microsoft_onedrive"},
	"word":              {"microsoft_word"},
	"excel":             {"microsoft_excel"},
	"powerpoint":        {"microsoft_powerpoint"},
	"onenote":           {"microsoft_onenote"},
	"copilot":           {"microsoft_copilot"},
	"epicgameslauncher": {"epic_games"},
	"downloads":         {"downloads"},
	"documents":         {"documents"},
	"recyclebin":        {"trash", "recycle_bin", "bin"},
}

// Match returns the drawable for an app name, or "".
func (p *IconPack) Match(name string) string {
	n := normName(name)
	if n == "" {
		return ""
	}
	if al, ok := packAliases[n]; ok {
		for _, d := range al {
			if p.has(d) {
				return d
			}
		}
	}
	if d, ok := p.keys[n]; ok {
		return d
	}
	// "Spotify Premium", "Steam Client Bootstrapper": try the first word(s)
	words := strings.Fields(strings.ToLower(name))
	for i := len(words) - 1; i >= 1; i-- {
		if d, ok := p.keys[normName(strings.Join(words[:i], " "))]; ok && len(normName(strings.Join(words[:i], " "))) >= 3 {
			return d
		}
	}
	return ""
}

func (p *IconPack) has(d string) bool {
	i := sort.SearchStrings(p.Icons, d)
	return i < len(p.Icons) && p.Icons[i] == d
}

func (p *IconPack) Path(d string) string { return filepath.Join(p.Dir, "icons", d+".png") }

// Search returns icon names containing q (all icons when q is empty), capped at limit.
func (p *IconPack) Search(q string, limit int) []string {
	q = normName(q)
	var out []string
	for _, d := range p.Icons {
		if q == "" || strings.Contains(normName(d), q) {
			out = append(out, d)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
