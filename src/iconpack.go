package main

// Android icon pack support: import an icon pack APK (assets/appfilter.xml +
// drawables) into a folder of PNGs, then match dock items to icons by app name.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path"
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
	// Dynamic packs (the built-in Black and White Icons) store colour layers, not
	// pictures: red = the lines, green = the accent. The dock paints them in the colours
	// in use: Variant "light" or "dark" tiles, or "auto" to follow the dock's background.
	Dynamic bool   `json:"dynamic,omitempty"`
	Variant string `json:"variant,omitempty"`
	Dir     string `json:"-"`
	keys    map[string]string
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
	reDrawable = regexp.MustCompile(`(?i)^(?:res/(?:drawable|mipmap)(?:-[^/]*)?/|assets/(?:[^/]+/)*|res/)([^/]+)\.(png|webp|jpe?g)$`)
)

func iconResourceKey(name string) (string, bool) {
	name = filepath.ToSlash(name)
	if strings.HasSuffix(strings.ToLower(name), ".9.png") {
		return "", false
	}
	m := reDrawable.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	base := m[1]
	var b strings.Builder
	underscore := false
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
			underscore = false
		} else if b.Len() > 0 && !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	key := strings.Trim(b.String(), "_")
	return key, key != ""
}

func dpiRank(path string) int {
	path = strings.ToLower(path)
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
		base := strings.ToLower(path.Base(f.Name))
		if (base == "appfilter.xml" || base == "app_filter.xml") && len(filter) == 0 {
			rc, err := f.Open()
			if err == nil {
				data, readErr := io.ReadAll(io.LimitReader(rc, 8<<20))
				rc.Close()
				if readErr == nil && len(data) > 0 && !bytes.HasPrefix(data, []byte{0x03, 0x00, 0x08, 0x00}) {
					filter = data
				}
			}
		}
		if key, ok := iconResourceKey(f.Name); ok {
			if old, exists := files[key]; !exists || dpiRank(f.Name) > dpiRank(old.Name) {
				files[key] = f
			}
		}
	}
	pack := &IconPack{Name: packName(apk), Labels: map[string]string{}}
	wanted := map[string]bool{}
	if len(filter) > 0 {
		dec := xml.NewDecoder(bytes.NewReader(filter))
		label := ""
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.Comment:
				label = strings.TrimSpace(string(t))
			case xml.StartElement:
				if !strings.EqualFold(t.Name.Local, "item") {
					continue
				}
				d := ""
				for _, attr := range t.Attr {
					if strings.EqualFold(attr.Name.Local, "drawable") {
						d = attr.Value
						break
					}
				}
				if _, ok := files[d]; !ok {
					continue
				}
				wanted[d] = true
				if k := normName(label); k != "" && len(label) < 60 {
					if _, exists := pack.Labels[k]; !exists {
						pack.Labels[k] = d
					}
				}
				label = ""
			}
		}
	}
	// Some packs omit appfilter.xml. Import their named image resources as a
	// manual icon set so users can still choose icons in the pack picker.
	if len(wanted) == 0 {
		for d := range files {
			wanted[d] = true
		}
	}
	if len(wanted) == 0 {
		return nil, errors.New("no PNG, WebP, or JPEG icon images were found in this APK")
	}
	// Include full-size drawable resources in the picker, not just mapped icons.
	for d, f := range files {
		if strings.Contains(strings.ToLower(f.Name), "nodpi") {
			wanted[d] = true
		}
	}
	dir := filepath.Join(root, pack.Name)
	tmpDir := dir + ".importing"
	os.RemoveAll(tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, "icons"), 0755); err != nil {
		return nil, err
	}
	completed := false
	defer func() {
		if !completed {
			os.RemoveAll(tmpDir)
		}
	}()
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
		if strings.ToLower(path.Ext(f.Name)) != ".png" {
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
		if os.WriteFile(filepath.Join(tmpDir, "icons", d+".png"), data, 0644) == nil {
			pack.Icons = append(pack.Icons, d)
		}
	}
	if len(pack.Icons) == 0 {
		return nil, errors.New("the APK contains no readable PNG or WebP icons")
	}
	sort.Strings(pack.Icons)
	b, _ := json.Marshal(pack)
	if err := os.WriteFile(filepath.Join(tmpDir, "pack.json"), b, 0644); err != nil {
		return nil, err
	}
	os.RemoveAll(dir)
	if err := os.Rename(tmpDir, dir); err != nil {
		return nil, err
	}
	completed = true
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
	if d, ok := p.keys[n]; ok { // the pack's own name for it wins (e.g. Command Prompt over Terminal)
		return d
	}
	if al, ok := packAliases[n]; ok {
		for _, d := range al {
			if p.has(d) {
				return d
			}
		}
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

// composeLayerIcon paints a dynamic pack's icon: the tile in bg, the lines (the mask's
// red channel) in ink, then the accent (its green channel) in acc.
func composeLayerIcon(mask image.Image, bg, ink, acc [3]uint8) *image.RGBA {
	b := mask.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	mix := func(x, y uint8, t uint32) uint8 { return uint8((uint32(x)*(255-t) + uint32(y)*t + 127) / 255) }
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, _, _ := mask.At(x, y).RGBA()
			ri, gi := r>>8, g>>8
			var c [3]uint8
			for k := 0; k < 3; k++ {
				c[k] = mix(mix(bg[k], ink[k], ri), acc[k], gi)
			}
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = c[0], c[1], c[2], 255
			i += 4
		}
	}
	return out
}

// layerColours is a dynamic pack's tile and line colours: dark tiles when dark.
func layerColours(dark bool) (bg, ink [3]uint8) {
	if dark {
		return [3]uint8{0x15, 0x15, 0x15}, [3]uint8{0xf2, 0xf2, 0xf2}
	}
	return [3]uint8{0xf4, 0xf4, 0xf4}, [3]uint8{0x14, 0x14, 0x14}
}
