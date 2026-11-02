package main

// Windows 11-style context menus: layout and drawing (platform independent).

import (
	"image"
	"image/color"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
)

// Segoe Fluent Icons / MDL2 glyphs
const (
	GlyphOpen       = '' // OpenInNewWindow
	GlyphNewWindow  = ''
	GlyphFolder     = '' // FolderOpen
	GlyphExplorer   = '' // FileExplorer
	GlyphBrush      = '' // Personalize
	GlyphReset      = '' // UpdateRestore
	GlyphUnpin      = ''
	GlyphPin        = ''
	GlyphClose      = '' // Cancel
	GlyphSettings   = ''
	GlyphHide       = ''
	GlyphView       = '' // RedEye
	GlyphMonitor    = '' // TVMonitor
	GlyphDockBottom = ''
	GlyphDockLeft   = ''
	GlyphDockRight  = ''
	GlyphStyle      = '' // Color
	GlyphNewFolder  = ''
	GlyphAdd        = ''
	GlyphDelete     = ''
	GlyphStart      = '' // Windows logo-ish (StartMenu? falls back gracefully)
	GlyphCheck      = ''
	GlyphChevron    = ''
	GlyphSize       = '' // FullScreen
	GlyphPower      = ''
	GlyphSearch     = '\uE721'
	GlyphFile       = '' // Document
)

type mItem struct {
	Text     string
	Key      string // shortcut shown on the right, dimmed
	Icon     *IconSet
	Glyph    rune
	Checked  bool
	Disabled bool
	Header   bool
	Sep      bool
	Danger   bool
	Sub      *macMenu
	Action   func()

	y0, y1 float64
}

type macMenu struct {
	items []*mItem
}

func (m *macMenu) add(text string, fn func()) *mItem {
	it := &mItem{Text: text, Action: fn}
	m.items = append(m.items, it)
	return it
}

func (m *macMenu) addG(g rune, text string, fn func()) *mItem {
	it := m.add(text, fn)
	it.Glyph = g
	return it
}

func (m *macMenu) sub(g rune, text string) *macMenu {
	s := &macMenu{}
	m.items = append(m.items, &mItem{Text: text, Glyph: g, Sub: s})
	return s
}

func (m *macMenu) header(text string) {
	m.items = append(m.items, &mItem{Text: text, Header: true})
}

func (m *macMenu) sep() {
	if n := len(m.items); n > 0 && !m.items[n-1].Sep {
		m.items = append(m.items, &mItem{Sep: true})
	}
}

type menuMetricsT struct {
	s, rowH, sepH, headH, padY, iconX, textX, padR float64
	px, hpx, gpx                                   int
}

func menuMetrics(sc float64) menuMetricsT {
	return menuMetricsT{
		s: sc, rowH: math.Round(34 * sc), sepH: math.Round(9 * sc), headH: math.Round(30 * sc), padY: math.Round(4 * sc),
		iconX: math.Round(16 * sc), textX: math.Round(44 * sc), padR: math.Round(18 * sc),
		px: int(math.Round(14 * sc)), hpx: int(math.Round(12 * sc)), gpx: int(math.Round(16 * sc)),
	}
}

func layoutMenuPure(te *TextEngine, sc float64, m *macMenu) (float64, float64) {
	mm := menuMetrics(sc)
	w := 0.0
	y := mm.padY
	hasSub := false
	for _, it := range m.items {
		if it.Sub != nil {
			hasSub = true
		}
	}
	for _, it := range m.items {
		it.y0 = y
		switch {
		case it.Sep:
			y += mm.sepH
		case it.Header:
			y += mm.headH
			w = math.Max(w, te.Measure(it.Text, mm.hpx)+mm.iconX+mm.padR)
		default:
			y += mm.rowH
			ww := te.Measure(it.Text, mm.px) + mm.textX + mm.padR
			if hasSub {
				ww += 26 * sc
			}
			if it.Key != "" {
				ww += te.Measure(it.Key, mm.px-1) + 28*sc
			}
			w = math.Max(w, ww)
		}
		it.y1 = y
	}
	y += mm.padY
	w = math.Min(math.Max(w, 220*sc), 420*sc)
	return math.Ceil(w), math.Ceil(y)
}

// renderMenuPure draws a Windows 11 menu. When acrylic is true the window behind
// provides the blurred backdrop, so the fill here is only a light tint.
func renderMenuPure(te, glyphs *TextEngine, sc float64, dark, acrylic bool, dst *image.RGBA, W, H int, menu *macMenu, hotIdx, openIdx int) {
	for i := range dst.Pix {
		dst.Pix[i] = 0
	}
	mm := menuMetrics(sc)
	s := mm.s
	w, h := float64(W), float64(H)
	rad := 8 * s

	tintA := 0.97
	if acrylic {
		tintA = 0.42
	}
	bg := pc(44, 44, 44, tintA)
	text, dim := pc(255, 255, 255, 1), pc(255, 255, 255, 0.55)
	hover, line, rim := pc(255, 255, 255, 0.075), pc(255, 255, 255, 0.09), pc(255, 255, 255, 0.10)
	if !dark {
		bg = pc(249, 249, 249, tintA)
		text, dim = pc(26, 26, 26, 1), pc(0, 0, 0, 0.55)
		hover, line, rim = pc(0, 0, 0, 0.05), pc(0, 0, 0, 0.08), pc(0, 0, 0, 0.10)
	}
	fillRoundRect(dst, 0, 0, w, h, rad, image.NewUniform(bg))
	if !acrylic {
		strokeRoundRect(dst, 0, 0, w, h, rad, 1*s, rim)
	}

	for i, it := range menu.items {
		y0, y1 := it.y0, it.y1
		cy := (y0 + y1) / 2
		switch {
		case it.Sep:
			fillRoundRect(dst, 0, cy-0.5*s, w, 1*s, 0, image.NewUniform(line))
			continue
		case it.Header:
			te.Draw(dst, truncateLabel(te, it.Text, mm.hpx, w-mm.iconX-mm.padR), mm.hpx, mm.iconX, cy+float64(mm.hpx)*0.36, dim)
			continue
		}
		tc := text
		if it.Danger && !it.Disabled {
			tc = pc(255, 99, 99, 1)
			if !dark {
				tc = pc(196, 43, 28, 1)
			}
		}
		if it.Disabled {
			tc = dim
		}
		if (i == hotIdx || i == openIdx) && !it.Disabled {
			fillRoundRect(dst, 4*s, y0+2*s, w-8*s, y1-y0-4*s, 4*s, image.NewUniform(hover))
		}
		// leading icon: app/file image, glyph, or check mark
		ix := mm.iconX
		gsz := float64(mm.gpx)
		switch {
		case it.Icon != nil:
			src := it.Icon.Pick(gsz + 2*s)
			r := image.Rect(int(ix-1*s), int(cy-gsz/2-1*s), int(ix+gsz+1*s), int(cy+gsz/2+1*s))
			xdraw.BiLinear.Scale(dst, r, src, src.Bounds(), xdraw.Over, nil)
		case it.Checked:
			drawGlyph(glyphs, dst, GlyphCheck, mm.gpx, ix, cy, tc)
		case it.Glyph != 0:
			drawGlyph(glyphs, dst, it.Glyph, mm.gpx, ix, cy, tc)
		}
		tw := w - mm.textX - mm.padR
		if it.Sub != nil {
			tw -= 22 * s
		}
		if it.Key != "" {
			kw := te.Measure(it.Key, mm.px-1)
			te.Draw(dst, it.Key, mm.px-1, w-mm.padR-kw, cy+float64(mm.px)*0.36, dim)
			tw -= kw + 20*s
		}
		te.Draw(dst, truncateLabel(te, it.Text, mm.px, tw), mm.px, mm.textX, cy+float64(mm.px)*0.36, tc)
		if it.Sub != nil {
			drawGlyph(glyphs, dst, GlyphChevron, int(math.Round(12*s)), w-mm.padR-10*s, cy, tc)
		}
	}
}

func drawGlyph(g *TextEngine, dst *image.RGBA, r rune, px int, x, cy float64, col color.RGBA) {
	if g == nil || g.font == nil {
		return
	}
	f := g.face(px)
	if f == nil {
		return
	}
	b, _ := font.BoundString(f, string(r))
	if b.Max.Y <= b.Min.Y {
		return
	}
	mid := float64(b.Min.Y+b.Max.Y) / 2 / 64
	g.Draw(dst, string(r), px, x, cy-mid, col)
}
