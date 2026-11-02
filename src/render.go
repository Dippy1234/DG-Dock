package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// On Windows the canvas memory is a BGRA DIB, so colours are stored with R and B swapped.
var swapRB = false

func pc(r, g, b uint8, a float64) color.RGBA {
	if a < 0 {
		a = 0
	}
	if a > 1 {
		a = 1
	}
	A := uint8(math.Round(a * 255))
	m := func(v uint8) uint8 { return uint8(math.Round(float64(v) * a)) }
	if swapRB {
		return color.RGBA{m(b), m(g), m(r), A}
	}
	return color.RGBA{m(r), m(g), m(b), A}
}

// ---------- paths ----------

const kappa = 0.5522847498

func roundRectPath(z *vector.Rasterizer, ox, oy float64, x, y, w, h, r float64, ccw bool) {
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	x -= ox
	y -= oy
	k := r * kappa
	f := func(v float64) float32 { return float32(v) }
	if !ccw {
		z.MoveTo(f(x+r), f(y))
		z.LineTo(f(x+w-r), f(y))
		z.CubeTo(f(x+w-r+k), f(y), f(x+w), f(y+r-k), f(x+w), f(y+r))
		z.LineTo(f(x+w), f(y+h-r))
		z.CubeTo(f(x+w), f(y+h-r+k), f(x+w-r+k), f(y+h), f(x+w-r), f(y+h))
		z.LineTo(f(x+r), f(y+h))
		z.CubeTo(f(x+r-k), f(y+h), f(x), f(y+h-r+k), f(x), f(y+h-r))
		z.LineTo(f(x), f(y+r))
		z.CubeTo(f(x), f(y+r-k), f(x+r-k), f(y), f(x+r), f(y))
	} else {
		z.MoveTo(f(x+r), f(y))
		z.CubeTo(f(x+r-k), f(y), f(x), f(y+r-k), f(x), f(y+r))
		z.LineTo(f(x), f(y+h-r))
		z.CubeTo(f(x), f(y+h-r+k), f(x+r-k), f(y+h), f(x+r), f(y+h))
		z.LineTo(f(x+w-r), f(y+h))
		z.CubeTo(f(x+w-r+k), f(y+h), f(x+w), f(y+h-r+k), f(x+w), f(y+h-r))
		z.LineTo(f(x+w), f(y+r))
		z.CubeTo(f(x+w), f(y+r-k), f(x+w-r+k), f(y), f(x+w-r), f(y))
	}
	z.ClosePath()
}

// bounds helper: integer rect covering float rect, clipped to dst
func coverRect(dst *image.RGBA, x, y, w, h float64) image.Rectangle {
	r := image.Rect(int(math.Floor(x))-1, int(math.Floor(y))-1, int(math.Ceil(x+w))+1, int(math.Ceil(y+h))+1)
	return r.Intersect(dst.Bounds())
}

func fillRoundRect(dst *image.RGBA, x, y, w, h, r float64, src image.Image) {
	rc := coverRect(dst, x, y, w, h)
	if rc.Empty() {
		return
	}
	z := vector.NewRasterizer(rc.Dx(), rc.Dy())
	z.DrawOp = draw.Over
	roundRectPath(z, float64(rc.Min.X), float64(rc.Min.Y), x, y, w, h, r, false)
	z.Draw(dst, rc, src, rc.Min)
}

// ring between outer rounded rect and inner rect inset by t
func strokeRoundRect(dst *image.RGBA, x, y, w, h, r, t float64, col color.RGBA) {
	rc := coverRect(dst, x, y, w, h)
	if rc.Empty() {
		return
	}
	z := vector.NewRasterizer(rc.Dx(), rc.Dy())
	z.DrawOp = draw.Over
	ox, oy := float64(rc.Min.X), float64(rc.Min.Y)
	roundRectPath(z, ox, oy, x, y, w, h, r, false)
	roundRectPath(z, ox, oy, x+t, y+t, w-2*t, h-2*t, math.Max(r-t, 0), true)
	z.Draw(dst, rc, image.NewUniform(col), rc.Min)
}

func fillCircle(dst *image.RGBA, cx, cy, rad float64, col color.RGBA) {
	fillRoundRect(dst, cx-rad, cy-rad, rad*2, rad*2, rad, image.NewUniform(col))
}

// vertical gradient source
type vGrad struct {
	y0, y1 float64
	c0, c1 color.RGBA
}

func (g *vGrad) ColorModel() color.Model { return color.RGBAModel }
func (g *vGrad) Bounds() image.Rectangle {
	return image.Rect(-1e9, -1e9, 1e9, 1e9)
}
func (g *vGrad) At(x, y int) color.Color {
	t := (float64(y) - g.y0) / (g.y1 - g.y0)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	l := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	return color.RGBA{l(g.c0.R, g.c1.R), l(g.c0.G, g.c1.G), l(g.c0.B, g.c1.B), l(g.c0.A, g.c1.A)}
}

// ---------- text ----------

type TextEngine struct {
	font  *opentype.Font
	faces map[int]font.Face
}

func NewTextEngine(ttf []byte) *TextEngine {
	te := &TextEngine{faces: map[int]font.Face{}}
	if len(ttf) > 0 {
		if f, err := opentype.Parse(ttf); err == nil {
			te.font = f
		}
	}
	return te
}

func (te *TextEngine) face(px int) font.Face {
	if te == nil || te.font == nil {
		return nil
	}
	if f, ok := te.faces[px]; ok {
		return f
	}
	f, err := opentype.NewFace(te.font, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	te.faces[px] = f
	return f
}

func (te *TextEngine) Measure(s string, px int) float64 {
	f := te.face(px)
	if f == nil {
		return 0
	}
	return float64(font.MeasureString(f, s)) / 64
}

func (te *TextEngine) Draw(dst *image.RGBA, s string, px int, x, baseline float64, col color.RGBA) {
	f := te.face(px)
	if f == nil {
		return
	}
	dr := font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: f,
		Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)}}
	dr.DrawString(s)
}

func truncateLabel(te *TextEngine, s string, px int, maxW float64) string {
	if te.Measure(s, px) <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		t := strings.TrimRightFunc(string(r), unicode.IsSpace) + "…"
		if te.Measure(t, px) <= maxW {
			return t
		}
	}
	return s
}

// ---------- renderer ----------

type Renderer struct {
	Text      *TextEngine
	Bold      *TextEngine
	lastDirty image.Rectangle

	// cached dock background (plates/pills/silhouette): only redrawn when its geometry changes
	bg     *image.RGBA
	bgRect image.Rectangle
	bgKey  string

	artImg   *image.RGBA
	artSrc   *IconSet
	artSize  int
	art2Img  *image.RGBA
	art2Src  *IconSet
	art2Size int
}

func (rd *Renderer) bgKeyFor(d *Dock) string {
	k := fmt.Sprintf("%d|%d|%d|%dx%d|%v%v%v%v|%.1f|%.1f|%.1f|%.1f|%.2f|%v|%d|%.3f", d.Orient, btoi(d.Pill), btoi(d.Notch), d.W, d.H,
		d.Dark, d.BlurOn, d.Tiles, d.Pill, d.PlateU0, d.PlateU1, d.PlateV0, d.PlateH, d.Scale, d.Pal, d.PageN, 0.0)
	if d.Pill {
		for _, g := range d.pillGroups() {
			k += fmt.Sprintf("|%.1f-%.1f-%.3f", g[0], g[1], g[2])
		}
	}
	if d.Notch && !d.Pill {
		for _, it := range d.Items { // the silhouette bulges with magnified icons
			if it.Kind != KindSep && it.Size > d.B*1.005 {
				k += fmt.Sprintf("|%.1f:%.1f", it.U, it.Size)
			}
		}
	}
	return k
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// drawBackground renders the static dock shapes into rd.bg.
func (rd *Renderer) drawBackground(d *Dock, b image.Rectangle) {
	if rd.bg == nil || rd.bg.Bounds() != b {
		rd.bg = image.NewRGBA(b)
		rd.bgRect = b
	}
	draw.Draw(rd.bg, rd.bgRect, image.Transparent, image.Point{}, draw.Src)
	s := d.Scale
	var r image.Rectangle
	switch {
	case d.Pill:
		r = rd.drawPills(d, rd.bg, false)
	case d.Notch:
		r = rd.drawNotch(d, rd.bg, false)
	default:
		x, y, w, h := d.rectUV(d.PlateU0, d.PlateV0, d.PlateU1, d.PlateV1)
		rd.drawGlass(d, rd.bg, x, y, w, h)
		r = image.Rect(int(x-14*s), int(y-14*s), int(x+w+14*s), int(y+h+14*s))
	}
	rd.bgRect = r.Intersect(b)
}

// Render draws a frame and returns the region of dst that changed (old ∪ new content).
func (rd *Renderer) Render(d *Dock, dst *image.RGBA) image.Rectangle {
	clr := rd.lastDirty
	if clr.Empty() {
		clr = dst.Bounds()
	}
	draw.Draw(dst, clr, image.Transparent, image.Point{}, draw.Src)
	dirty := image.Rectangle{}
	grow := func(x, y, w, h float64) {
		dirty = dirty.Union(image.Rect(int(x)-3, int(y)-3, int(x+w)+4, int(y+h)+4))
	}
	s := d.Scale

	if key := rd.bgKeyFor(d); key != rd.bgKey || rd.bg == nil || rd.bg.Bounds() != dst.Bounds() {
		rd.drawBackground(d, dst.Bounds())
		rd.bgKey = key
	}
	if !rd.bgRect.Empty() {
		draw.Draw(dst, rd.bgRect, rd.bg, rd.bgRect.Min, draw.Src) // area was just cleared
		dirty = dirty.Union(rd.bgRect)
	}
	if d.SearchItem != nil {
		dirty = dirty.Union(rd.drawSearchTail(d, dst))
	}
	if d.Notch && d.PopItem != nil && d.PopT > 0.01 {
		if d.Pill {
			dirty = dirty.Union(rd.drawBubble(d, dst))
		} else {
			dirty = dirty.Union(rd.drawCard(d, dst))
		}
	}

	dotCol := d.ink(0.82)
	if gs := d.pillGroups(); d.PageN > 1 && len(gs) > 0 {
		dirty = dirty.Union(rd.drawPageDots(d, dst, (gs[0][0]+gs[0][1])/2))
	}
	for _, it := range d.Items {
		if it.Kind == KindSep && d.Pill || it.Kind == KindGap {
			continue
		}
		if it.Kind == KindSep {
			lc := d.ink(0.22)
			mid := d.PlateV0 + d.PlateH/2
			x, y, w, h := d.rectUV(it.U-0.6*s, mid-it.Size/2, it.U+0.6*s, mid+it.Size/2)
			fillRoundRect(dst, x, y, w, h, 0.6*s, image.NewUniform(lc))
			continue
		}
		if it == d.Drag {
			continue
		}
		if it.Kind == KindWidget {
			dirty = dirty.Union(rd.drawWidget(d, dst, it))
			continue
		}
		sz := it.Size
		v := d.PlateV0 + d.P + it.Lift
		target, du, alpha := dst, 0.0, 1.0
		if (it.Paged || it.PageOut) && it.Appear < 1 {
			// page change: icons slide the way the page is going (forward: left to right,
			// back: right to left), fading, at nearly full size
			e := it.Appear
			dir := float64(d.PageDir)
			if dir == 0 {
				dir = 1
			}
			sz = d.B * (0.9 + 0.1*e)
			v = d.PlateV0 + d.P + (d.B-sz)/2
			if it.PageOut {
				du = dir * (1 - e) * d.B * 0.75 // carries on out
			} else {
				du = -dir * (1 - e) * d.B * 0.75 // comes in from behind
			}
			alpha = e * e
		}
		if it.InRun && it.Leaving && it.Appear < 1 && d.BudStart.IsZero() {
			// an open app leaving the pill (closed, or its page came back): slides
			// out to the left, towards the apps, fading
			e := it.Appear
			sz = d.B * (0.9 + 0.1*e)
			v = d.PlateV0 + d.P + (d.B-sz)/2
			du = -(1 - e) * d.B * 0.75
			alpha = e * e
		} else if it.InRun && !it.Leaving && it.Appear < 1 {
			// an open app arriving in its pill: slides in from the left, fading up,
			// at (nearly) full size rather than growing up from the bottom
			e := it.Appear
			sz = d.B * (0.9 + 0.1*e)
			v = d.PlateV0 + d.P + (d.B-sz)/2
			du = -(1 - e) * d.B * 0.75
			alpha = e * e
		}
		x, y, _, _ := d.rectUV(it.U+du-sz/2, v, it.U+du+sz/2, v+sz)
		if full := d.B * it.Scale; math.Abs(sz-full) > 0.5 && it.Appear < 1 {
			drawIconSmooth(target, it, x, y, sz, math.Max(full, d.B), alpha)
		} else {
			drawIcon(target, it, x, y, sz, alpha)
		}
		if it == d.Pressed {
			drawPressed(target, it, x, y, sz)
		}
		grow(x, y, sz, sz)
		if it.Running && !d.HideDots {
			cx, cy := d.toXY(it.U+du, d.PlateV0+d.P*0.62)
			fillCircle(target, cx, cy, 1.9*s*math.Max(it.Appear, 0.2), scaleC(dotCol, alpha))
		}
	}

	if d.Drag != nil {
		sz := d.B * 1.05
		x := d.DragX - sz/2
		y := d.DragY - sz/2
		alpha := 0.92
		if d.DragRemove {
			alpha = 0.55
		}
		drawIcon(dst, d.Drag, x, y, sz, alpha)
		grow(x, y, sz, sz)
		if d.DragRemove {
			dirty = dirty.Union(rd.labelAt(d, dst, "Remove", d.DragX, y-6*s, OrientBottom))
		}
	}

	for _, p := range d.Poofs {
		t := time.Since(p.Start).Seconds() / 0.45
		if t < 1 {
			dirty = dirty.Union(drawPoof(dst, p, t, s, d.Dark))
		}
	}

	if d.Notch {
		dirty = dirty.Union(rd.drawPopText(d, dst))
	} else if d.Hover >= 0 && d.Hover < len(d.Items) && d.Drag == nil {
		it := d.Items[d.Hover]
		if it.Name != "" {
			ax, ay := d.toXY(it.U, d.PlateV0+d.P+it.Size+it.Lift+7*s)
			dirty = dirty.Union(rd.labelAt(d, dst, it.Name, ax, ay, d.Orient))
		}
	}
	dirty = dirty.Intersect(dst.Bounds())
	changed := dirty.Union(clr).Intersect(dst.Bounds())
	rd.lastDirty = dirty
	return changed
}

func (rd *Renderer) drawGlass(d *Dock, dst *image.RGBA, L, T, pw, ph float64) {
	s := d.Scale
	thick := math.Min(pw, ph)
	rad := thick * 0.40
	for i := 6; i >= 1; i-- {
		e := float64(i) * 1.6 * s
		a := 0.022
		if d.Dark {
			a = 0.05
		}
		fillRoundRect(dst, L-e, T-e+2.5*s, pw+2*e, ph+2*e, rad+e, image.NewUniform(pc(0, 0, 0, a)))
	}
	var top, bot color.RGBA
	if d.Dark {
		if d.BlurOn {
			top, bot = pc(70, 70, 76, 0.42), pc(40, 40, 46, 0.50)
		} else {
			top, bot = pc(52, 52, 58, 0.90), pc(34, 34, 40, 0.93)
		}
	} else {
		if d.BlurOn {
			top, bot = pc(255, 255, 255, 0.34), pc(246, 246, 250, 0.22)
		} else {
			top, bot = pc(250, 250, 252, 0.88), pc(232, 232, 238, 0.86)
		}
	}
	fillRoundRect(dst, L, T, pw, ph, rad, &vGrad{y0: T, y1: T + ph, c0: top, c1: bot})
	if d.Dark {
		strokeRoundRect(dst, L, T, pw, ph, rad, 1*s, pc(0, 0, 0, 0.45))
		strokeRoundRect(dst, L+1*s, T+1*s, pw-2*s, ph-2*s, rad-1*s, 1*s, pc(255, 255, 255, 0.16))
	} else {
		strokeRoundRect(dst, L, T, pw, ph, rad, 1*s, pc(0, 0, 0, 0.13))
		strokeRoundRect(dst, L+1*s, T+1*s, pw-2*s, ph-2*s, rad-1*s, 1.2*s, pc(255, 255, 255, 0.62))
	}
}

// ---------- pill style ----------

// pillGroups returns the dock-space spans of the app pill and the folders pill.
// pillGroups returns each pill's span along the dock and how "present" it is (0..1):
// a pill whose icons are all growing in or shrinking away scales with them, so pills
// appear and disappear smoothly instead of popping.
func (d *Dock) pillGroups() [][3]float64 {
	var groups [][3]float64
	start, pres, split := -1.0, 0.0, -1.0
	var last *Item
	flush := func() {
		if last != nil {
			p := math.Min(1, pres)
			if split >= 0 { // the running-apps pill: its shape follows the split
				p = split
			}
			groups = append(groups, [3]float64{start, last.U + last.Slot/2 - d.G/2 + d.P, p})
		}
		start, pres, last = -1, 0, nil
	}
	for _, it := range d.Items {
		if it.Kind == KindSep {
			flush()
			split = -1
			if it.InRun {
				split = it.Appear
			}
			continue
		}
		if start < 0 {
			start = it.U - it.Slot/2 + d.G/2 - d.P
		}
		a := it.Appear
		if it.Kind == KindGap {
			a = 0
		}
		pres += a // summed, so a page change (some icons shrinking, others growing) keeps the pill whole
		last = it
	}
	flush()
	return groups
}

// pillShape is one pill as drawn: its extent in dock space, corner radius and presence.
type pillShape struct{ u0, u1, v0, v1, r, p float64 }

func (rd *Renderer) drawPills(d *Dock, dst *image.RGBA, withFlyout bool) image.Rectangle {
	s := d.Scale
	fill := image.NewUniform(d.fillC())
	dirty := image.Rectangle{}
	rad := d.PlateH * 0.26
	var shapes []pillShape
	for _, g := range d.pillGroups() {
		sh := pillShape{g[0], g[1], d.PlateV0, d.PlateV1, rad, math.Min(g[2], 1)}
		sh.r = math.Min(rad, math.Min(sh.u1-sh.u0, sh.v1-sh.v0)/2)
		shapes = append(shapes, sh)
	}
	for i, sh := range shapes {
		if sh.u1-sh.u0 < 1 {
			continue
		}
		// a pill coming or going (the open-apps pill) fades in/out while it widens or
		// narrows with its icons, the same feel as changing page
		a := 1.0
		if i > 0 && sh.p < 0.999 {
			a = sh.p * sh.p * (3 - 2*sh.p)
		}
		if a < 0.01 {
			continue
		}
		x, y, w, h := d.rectUV(sh.u0, sh.v0, sh.u1, sh.v1)
		f := fill
		if a < 1 {
			f = image.NewUniform(scaleC(d.fillC(), a))
		}
		fillRoundRect(dst, x-1*s, y-1*s+1.5*s, w+2*s, h+2*s, sh.r+1*s, image.NewUniform(pc(0, 0, 0, 0.18*a)))
		fillRoundRect(dst, x, y, w, h, sh.r, f)
		strokeRoundRect(dst, x, y, w, h, sh.r, 1*s, d.ink(0.07*a))
		dirty = dirty.Union(image.Rect(int(x)-4, int(y)-4, int(x+w)+5, int(y+h)+6))
	}
	if withFlyout && d.PopItem != nil && d.PopT > 0.01 {
		dirty = dirty.Union(rd.drawBubble(d, dst))
	}
	return dirty
}

// roundBoxSDF is the signed distance from (u, v) to a rounded rectangle (negative inside).
func roundBoxSDF(u, v, u0, u1, v0, v1, r float64) float64 {
	cu, cv := (u0+u1)/2, (v0+v1)/2
	hu, hv := (u1-u0)/2-r, (v1-v0)/2-r
	qu, qv := math.Abs(u-cu)-hu, math.Abs(v-cv)-hv
	out := math.Hypot(math.Max(qu, 0), math.Max(qv, 0))
	return out + math.Min(math.Max(qu, qv), 0) - r
}

// drawNeck fills the region between two neighbouring pills with their smooth union
// (a metaball-style blend), so a pill budding out of another looks like a drop of water
// pulling away. Nothing is drawn once they're far enough apart for the neck to snap.
func (rd *Renderer) drawNeck(d *Dock, dst *image.RGBA, a, b pillShape) image.Rectangle {
	s := d.Scale
	// blend reach: wide while a pill is budding off or melting back (long, stretchy neck),
	// shrinking to below the resting gap as it settles, so the neck thins and snaps
	e := b.p * b.p * (3 - 2*b.p) // only the pill that's budding off pulls on its parent
	rest := 20 * s * 1.5         // < 2 × the resting gap, so settled pills never join
	k := d.PlateH*0.75*(1-e) + rest*e
	gap := b.u0 - a.u1
	if gap >= k*0.5 || a.u1-a.u0 < 1 || b.u1-b.u0 < 1 {
		return image.Rectangle{}
	}
	// only the strip around the gap can change
	uL, uR := math.Min(a.u1-a.r, b.u0)-k*0.5, math.Max(b.u0+b.r, a.u1)+k*0.5
	vL, vR := math.Min(a.v0, b.v0)-k*0.3, math.Max(a.v1, b.v1)+k*0.3
	x, y, w, h := d.rectUV(uL, vL, uR, vR)
	r := image.Rect(int(math.Floor(x)), int(math.Floor(y)), int(math.Ceil(x+w)), int(math.Ceil(y+h))).Intersect(dst.Bounds())
	if r.Empty() {
		return r
	}
	bandV0, bandV1 := math.Min(a.v0, b.v0), math.Max(a.v1, b.v1)
	fc := d.fillC()
	ec := d.ink(0.07)
	sw := 1 * s
	for py := r.Min.Y; py < r.Max.Y; py++ {
		for px := r.Min.X; px < r.Max.X; px++ {
			u, v := d.toUV(float64(px)+0.5, float64(py)+0.5)
			da := roundBoxSDF(u, v, a.u0, a.u1, a.v0, a.v1, a.r)
			db := roundBoxSDF(u, v, b.u0, b.u1, b.v0, b.v1, b.r)
			hh := math.Max(k-math.Abs(da-db), 0) / k
			sd := math.Min(da, db) - hh*hh*k*0.25 // polynomial smooth-min
			// keep the liquid within the pills' height: a bridge that pinches, not a balloon
			band := math.Max(bandV0-v, v-bandV1)
			if kk := math.Max(a.r, 1); math.Abs(sd-band) < kk { // smooth max: rounded where they meet
				hb := (kk - math.Abs(sd-band)) / kk
				sd = math.Max(sd, band) + hb*hb*kk*0.25
			} else {
				sd = math.Max(sd, band)
			}
			sd = math.Min(sd, math.Min(da, db)) // the liquid only ever adds to the pills
			cov := math.Max(0, math.Min(1, 0.5-sd))
			if cov <= 0 {
				continue
			}
			o := (py-dst.Rect.Min.Y)*dst.Stride + (px-dst.Rect.Min.X)*4
			q := dst.Pix[o : o+4 : o+4]
			// coverage-weighted replace: where a pill is already drawn this changes nothing,
			// in the neck it adds the liquid
			ic := 1 - cov
			q[0] = uint8(float64(fc.R)*cov + float64(q[0])*ic)
			q[1] = uint8(float64(fc.G)*cov + float64(q[1])*ic)
			q[2] = uint8(float64(fc.B)*cov + float64(q[2])*ic)
			q[3] = uint8(float64(fc.A)*cov + float64(q[3])*ic)
			// the faint edge line follows the liquid outline too
			if e := math.Max(0, math.Min(1, sw*0.5+0.5-math.Abs(sd+sw*0.5))); e > 0 {
				ea := float64(ec.A) / 255 * e
				q[0] = uint8(float64(ec.R)*e + float64(q[0])*(1-ea))
				q[1] = uint8(float64(ec.G)*e + float64(q[1])*(1-ea))
				q[2] = uint8(float64(ec.B)*e + float64(q[2])*(1-ea))
				q[3] = uint8(float64(ec.A)*e + float64(q[3])*(1-ea))
			}
		}
	}
	return r
}

// drawBubble draws the flyout card with a small pointed tail aimed at the icon.
func (rd *Renderer) drawBubble(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	e := easeOut(d.PopT)
	fill := image.NewUniform(d.fillC())
	cx, cy, cw, ch := d.rectUV(d.PopU0, d.PopV0, d.PopU1, d.PopV1)
	if cw < 2 || ch < 2 {
		return image.Rectangle{}
	}
	rad := math.Min(14*s, math.Min(cw, ch)/2)
	fillRoundRect(dst, cx, cy+1.5*s, cw, ch, rad, image.NewUniform(pc(0, 0, 0, 0.16)))
	fillRoundRect(dst, cx, cy, cw, ch, rad, fill)
	strokeRoundRect(dst, cx, cy, cw, ch, rad, 1*s, d.ink(0.07))
	// tail
	it := d.PopItem
	th := 13 * s * e
	tw := 15 * s
	tu := math.Min(math.Max(it.U, d.PopU0+rad+tw), d.PopU1-rad-tw)
	if th > 1 {
		b := d.PopV0 + 1*s
		tip := d.PopV0 - th
		tr := 2.2 * s
		p := func(u, v float64) (float32, float32) {
			x, y := d.toXY(u, v)
			return float32(x), float32(y)
		}
		r := coverRect(dst, math.Min(cx, cx), math.Min(cy, cy)-30*s, cw, ch+60*s)
		minX, minY := float64(r.Min.X), float64(r.Min.Y)
		bx0, by0, bw, bh := d.rectUV(tu-tw, tip-2, tu+tw, b+2)
		r = coverRect(dst, bx0, by0, bw, bh)
		minX, minY = float64(r.Min.X), float64(r.Min.Y)
		if !r.Empty() {
			z := vector.NewRasterizer(r.Dx(), r.Dy())
			z.DrawOp = draw.Over
			mv := func(f func(x, y float32), u, v float64) {
				x, y := p(u, v)
				f(x-float32(minX), y-float32(minY))
			}
			// slope of each side
			k := tr / th * tw
			mv(z.MoveTo, tu-tw, b)
			mv(z.LineTo, tu-k, tip+tr)
			x1, y1 := p(tu, tip)
			x2, y2 := p(tu+k, tip+tr)
			z.QuadTo(x1-float32(minX), y1-float32(minY), x2-float32(minX), y2-float32(minY))
			mv(z.LineTo, tu+tw, b)
			z.ClosePath()
			z.Draw(dst, r, fill, r.Min)
		}
	}
	bx, by, bw, bh := d.rectUV(d.PopU0, d.PopV0-th-2*s, d.PopU1, d.PopV1)
	return image.Rect(int(bx)-3, int(by)-3, int(bx+bw)+4, int(by+bh)+5)
}

// ---------- notch silhouette ----------

// floatingCard draws the flyout as a separate card joined by a pinched neck.
const floatingCard = true

// bulgeAt smoothly interpolates the height needed by (magnified) icons.
func (d *Dock) bulgeAt(u float64) float64 {
	var pu, ph []float64
	for _, it := range d.Items {
		if it.Kind == KindSep {
			continue
		}
		pu = append(pu, it.U)
		ph = append(ph, d.P+it.Size+d.P*0.9)
	}
	if len(pu) == 0 {
		return 0
	}
	if u <= pu[0] {
		return ph[0]
	}
	for i := 1; i < len(pu); i++ {
		if u <= pu[i] {
			t := (u - pu[i-1]) / math.Max(pu[i]-pu[i-1], 1)
			ts := (1 - math.Cos(math.Pi*t)) / 2
			return ph[i-1] + (ph[i]-ph[i-1])*ts
		}
	}
	return ph[len(ph)-1]
}

// notchBase is the dock outline height (from the edge) at u, ignoring the flyout.
func (d *Dock) notchBase(u float64) float64 {
	PT := d.PlateH
	U0, U1 := d.PlateU0, d.PlateU1
	Rf := PT * 0.30
	switch {
	case u >= U0 && u <= U1:
		H := math.Max(PT, d.bulgeAt(u))
		Rc := H * 0.42
		if u < U0+Rc {
			dx := U0 + Rc - u
			return H - Rc + math.Sqrt(math.Max(Rc*Rc-dx*dx, 0))
		} else if u > U1-Rc {
			dx := u - (U1 - Rc)
			return H - Rc + math.Sqrt(math.Max(Rc*Rc-dx*dx, 0))
		}
		return H
	case u >= U0-Rf && u < U0:
		dx := u - (U0 - Rf)
		return Rf - math.Sqrt(math.Max(Rf*Rf-dx*dx, 0))
	case u > U1 && u <= U1+Rf:
		dx := (U1 + Rf) - u
		return Rf - math.Sqrt(math.Max(Rf*Rf-dx*dx, 0))
	}
	return -1
}

func (d *Dock) notchProfile(u float64) float64 {
	f := d.notchBase(u)
	if d.PopItem == nil || d.PopT <= 0 || floatingCard {
		return f
	}
	s := d.Scale
	top := d.PopV1 - d.PlateV0
	P0, P1 := d.PopU0, d.PopU1
	if u >= P0 && u <= P1 {
		rb := math.Min(math.Min(14*s, (P1-P0)/2), math.Max(top-d.PlateH, 0)/2)
		v := top
		if u < P0+rb {
			dx := P0 + rb - u
			v = top - rb + math.Sqrt(math.Max(rb*rb-dx*dx, 0))
		} else if u > P1-rb {
			dx := u - (P1 - rb)
			v = top - rb + math.Sqrt(math.Max(rb*rb-dx*dx, 0))
		}
		return math.Max(f, v)
	}
	// concave fillets where the flyout meets the dock
	rf := 12 * s
	if u >= P0-rf && u < P0 {
		b := d.notchBase(P0)
		r := math.Min(rf, math.Max(top-b, 0))
		dx := u - (P0 - r)
		if dx >= 0 && r > 0 {
			f = math.Max(f, b+r-math.Sqrt(math.Max(r*r-dx*dx, 0)))
		}
	} else if u > P1 && u <= P1+rf {
		b := d.notchBase(P1)
		r := math.Min(rf, math.Max(top-b, 0))
		dx := (P1 + r) - u
		if dx >= 0 && r > 0 {
			f = math.Max(f, b+r-math.Sqrt(math.Max(r*r-dx*dx, 0)))
		}
	}
	return f
}

func (rd *Renderer) drawNotch(d *Dock, dst *image.RGBA, withFlyout bool) image.Rectangle {
	PT := d.PlateH
	u0 := d.PlateU0 - PT*0.3 - 1
	u1 := d.PlateU1 + PT*0.3 + 1
	if !floatingCard && d.PopItem != nil && d.PopT > 0 {
		u0 = math.Min(u0, d.PopU0-14*d.Scale)
		u1 = math.Max(u1, d.PopU1+14*d.Scale)
	}
	type pt struct{ x, y float64 }
	var pts []pt
	baseV := math.Min(d.PlateV0, 0) - 2
	add := func(u, v float64) {
		x, y := d.toXY(u, v)
		pts = append(pts, pt{x, y})
	}
	add(u0, baseV)
	for u := u0; u <= u1; u += 1 {
		f := d.notchProfile(u)
		if f < 0 {
			add(u, baseV)
			continue
		}
		add(u, d.PlateV0+f)
	}
	add(u1, baseV)
	minX, minY, maxX, maxY := 1e9, 1e9, -1e9, -1e9
	for _, p := range pts {
		minX, minY = math.Min(minX, p.x), math.Min(minY, p.y)
		maxX, maxY = math.Max(maxX, p.x), math.Max(maxY, p.y)
	}
	r := image.Rect(int(minX)-2, int(minY)-2, int(maxX)+3, int(maxY)+3).Intersect(dst.Bounds())
	if r.Empty() {
		return r
	}
	z := vector.NewRasterizer(r.Dx(), r.Dy())
	z.DrawOp = draw.Over
	ox, oy := float64(r.Min.X), float64(r.Min.Y)
	z.MoveTo(float32(pts[0].x-ox), float32(pts[0].y-oy))
	for _, p := range pts[1:] {
		z.LineTo(float32(p.x-ox), float32(p.y-oy))
	}
	z.ClosePath()
	z.Draw(dst, r, image.NewUniform(pc(7, 7, 9, 0.97)), r.Min)
	if withFlyout && floatingCard && d.PopItem != nil && d.PopT > 0.01 {
		r = r.Union(rd.drawCard(d, dst))
	}
	return r
}

func (rd *Renderer) drawCard(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	e := easeOut(d.PopT)
	fill := image.NewUniform(d.fillC())
	// card
	cx, cy, cw, ch := d.rectUV(d.PopU0, d.PopV0, d.PopU1, d.PopV1)
	rad := math.Min(18*s, math.Min(cw, ch)/2)
	if cw > 1 && ch > 1 {
		fillRoundRect(dst, cx, cy, cw, ch, rad, fill)
	}
	// neck: an hourglass between the dock top and the card bottom
	it := d.PopItem
	va := d.PlateV0 + d.notchBase(it.U)
	vb := d.PopV0
	if vb <= va+1 {
		return image.Rect(int(cx), int(cy), int(cx+cw)+1, int(cy+ch)+1)
	}
	// stem with concave circular fillets at both ends (tangent to the flat surfaces)
	L := vb - va
	R := math.Min(10*s, L/2)
	minH := (9 + 4*e) * s
	flare := minH + R
	ua := it.U
	ub := math.Min(math.Max(it.U, d.PopU0+rad+flare), d.PopU1-rad-flare)
	if d.PopU1-d.PopU0 < 2*(rad+flare) {
		ub = (d.PopU0 + d.PopU1) / 2
	}
	const n = 40
	var left, right [][2]float64
	for i := 0; i <= n; i++ {
		t := float64(i) / n
		v := va + L*t
		dEnd := math.Min(v-va, vb-v) // distance to the nearer surface
		hw := minH
		if dEnd < R {
			k := R - dEnd
			hw = minH + R - math.Sqrt(math.Max(R*R-k*k, 0))
		}
		uc := ua + (ub-ua)*(1-math.Cos(math.Pi*t))/2
		x0, y0 := d.toXY(uc-hw, v)
		x1, y1 := d.toXY(uc+hw, v)
		left = append(left, [2]float64{x0, y0})
		right = append(right, [2]float64{x1, y1})
	}
	pts := append([][2]float64{}, left...)
	for i := len(right) - 1; i >= 0; i-- {
		pts = append(pts, right[i])
	}
	minX, minY, maxX, maxY := 1e9, 1e9, -1e9, -1e9
	for _, p := range pts {
		minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
		maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
	}
	nr := image.Rect(int(minX)-2, int(minY)-2, int(maxX)+3, int(maxY)+3).Intersect(dst.Bounds())
	if !nr.Empty() {
		z := vector.NewRasterizer(nr.Dx(), nr.Dy())
		z.DrawOp = draw.Over
		ox, oy := float64(nr.Min.X), float64(nr.Min.Y)
		z.MoveTo(float32(pts[0][0]-ox), float32(pts[0][1]-oy))
		for _, p := range pts[1:] {
			z.LineTo(float32(p[0]-ox), float32(p[1]-oy))
		}
		z.ClosePath()
		z.Draw(dst, nr, fill, nr.Min)
	}
	return image.Rect(int(cx)-2, int(cy)-2, int(cx+cw)+3, int(cy+ch)+3).Union(nr)
}

func (rd *Renderer) drawPopText(d *Dock, dst *image.RGBA) image.Rectangle {
	if d.PopItem == nil || d.PopT < 0.55 {
		return image.Rectangle{}
	}
	if d.Pop.Mode == PopMedia {
		return rd.drawMediaCard(d, dst)
	}
	s := d.Scale
	a := math.Min((d.PopT-0.55)/0.4, 1)
	tp, sp, rp, rowH, pad := d.popPx()
	x0, y0, x1 := d.PopX0, d.PopY0, d.PopX1
	w := x1 - x0
	y := y0 + 12*s + float64(tp)
	rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Title, tp, w-2*pad), tp, x0+pad, y, d.fg(a))
	y += float64(tp)*0.3 + 2*s + float64(sp)
	rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Subtitle, sp, w-2*pad), sp, x0+pad, y, d.dimC(a))
	if d.Pop.Mode == PopThumbs || d.Pop.Mode == PopGrid {
		rd.drawTiles(d, dst, a, rp)
		if d.Pop.Mode == PopGrid && d.Pop.ScrollRows > d.Pop.ScrollVis {
			rd.drawScrollBar(d, dst, a)
		}
	} else {
		for i, r := range d.Pop.Rows {
			if i >= maxPopRows {
				break
			}
			if i == d.PopHot {
				fillRoundRect(dst, r.X0, r.Y0+1*s, r.X1-r.X0, r.Y1-r.Y0-2*s, 7*s, image.NewUniform(d.ink(0.12*a)))
			}
			cy := (r.Y0 + r.Y1) / 2
			fillCircle(dst, r.X0+pad*0.5+2*s, cy, 2.6*s, pc(48, 209, 88, a))
			tx := r.X0 + pad*0.5 + 12*s
			rd.Text.Draw(dst, truncateLabel(rd.Text, r.Title, rp, r.X1-tx-6*s), rp, tx, cy+float64(rp)*0.36, d.fg2(a))
		}
	}
	_ = rowH
	return image.Rect(int(d.PopX0)-2, int(d.PopY0)-2, int(d.PopX1)+3, int(d.PopY1)+3)
}

// ---------- widgets ----------

func (rd *Renderer) drawWidget(d *Dock, dst *image.RGBA, it *Item) image.Rectangle {
	s := d.Scale
	w := d.B * d.widgetWidth(it) * it.Appear
	h := d.B * it.Appear
	v0 := d.PlateV0 + d.P
	x, y, bw, bh := d.rectUV(it.U-w/2, v0, it.U+w/2, v0+h)
	cx, cy := x+bw/2, y+bh/2
	light := d.Pal.Light
	fg, dim := pc(245, 245, 248, 1), pc(170, 170, 178, 1)
	tile := pc(255, 255, 255, 0.07)
	if light {
		fg, dim, tile = pc(25, 25, 30, 1), pc(95, 95, 105, 1), pc(0, 0, 0, 0.06)
	}
	if it.Widget != "desktop" {
		fillRoundRect(dst, x+2*s, y+2*s, bw-4*s, bh-4*s, math.Min(bw, bh)*0.22, image.NewUniform(tile))
	}
	switch it.Widget {
	case "clock":
		p1 := int(math.Round(d.B * 0.34))
		p2 := int(math.Round(d.B * 0.17))
		for p1 > 8 && rd.Text.Measure(it.WText1, p1) > bw-8*s {
			p1--
		}
		for p2 > 7 && rd.Text.Measure(it.WText2, p2) > bw-6*s {
			p2--
		}
		t1w := rd.Text.Measure(it.WText1, p1)
		t2w := rd.Text.Measure(it.WText2, p2)
		rd.Text.Draw(dst, it.WText1, p1, cx-t1w/2, cy+float64(p1)*0.18, fg)
		rd.Text.Draw(dst, it.WText2, p2, cx-t2w/2, cy+float64(p1)*0.18+float64(p2)*1.35, dim)
	case "status":
		rd.drawStatus(d, dst, it, x, y, bw, bh, fg)
	case "battery":
		// battery outline with fill level
		bwid, bhgt := d.B*0.5, d.B*0.26
		bx, by := cx-bwid/2-1.5*s, cy-bhgt/2-d.B*0.12
		strokeRoundRect(dst, bx, by, bwid, bhgt, bhgt*0.28, 1.6*s, fg)
		fillRoundRect(dst, bx+bwid+1*s, by+bhgt*0.3, 2.4*s, bhgt*0.4, 1*s, image.NewUniform(fg))
		lv := math.Max(0, math.Min(1, it.WLevel))
		fc := fg
		switch {
		case it.WCharging:
			fc = pc(48, 209, 88, 1)
		case lv <= 0.2:
			fc = pc(255, 69, 58, 1)
		}
		in := 3.2 * s
		if lv > 0 {
			fillRoundRect(dst, bx+in, by+in, (bwid-2*in)*lv, bhgt-2*in, 1.5*s, image.NewUniform(fc))
		}
		p2 := int(math.Round(d.B * 0.2))
		tw := rd.Text.Measure(it.WText1, p2)
		rd.Text.Draw(dst, it.WText1, p2, cx-tw/2, cy+d.B*0.26, fg)
	case "media":
		rd.drawPlayer(d, dst, it, x, y, bw, bh, fg, dim, light)
	case "tray":
		rd.drawTray(d, dst, it, cx, cy, math.Min(bw, bh), fg)
	case "desktop":
		// show desktop: a slim bar at the very end of the dock, like the taskbar's corner
		bl := d.B * 0.42
		bt := math.Round(3.4 * s)
		if d.Orient == OrientBottom {
			fillRoundRect(dst, cx-bt/2, cy-bl/2, bt, bl, bt/2, image.NewUniform(fg))
		} else {
			fillRoundRect(dst, cx-bl/2, cy-bt/2, bl, bt, bt/2, image.NewUniform(fg))
		}
	}
	return image.Rect(int(x)-2, int(y)-2, int(x+bw)+3, int(y+bh)+3)
}

// drawTray is the tray tile: a simple rounded square (the icons themselves are in its card).
func (rd *Renderer) drawTray(d *Dock, dst *image.RGBA, it *Item, cx, cy, h float64, fg color.RGBA) {
	s := d.Scale
	g := math.Round(h * 0.4)
	strokeRoundRect(dst, cx-g/2, cy-g/2, g, g, g*0.3, 2.2*s, fg)
}

// drawStatus is the battery ring (with the % inside) and Wi-Fi strength dots.
// After scrolling on it, the ring shows screen brightness (with a yellow dot below) for a
// second, then fades back to the battery.
func (rd *Renderer) drawStatus(d *Dock, dst *image.RGBA, it *Item, x, y, bw, bh float64, fg color.RGBA) {
	s := d.Scale
	h := math.Min(bw, bh)
	cx, cy := x+bw/2, y+bh*0.43
	r := h * 0.26
	track := d.ink(0.28)
	bo := 0.0 // brightness overlay strength
	if !it.WBrAt.IsZero() {
		if t := time.Since(it.WBrAt).Seconds(); t < 1.4 {
			bo = math.Min(1, (1.4-t)/0.4)
		}
	}
	ba := 1 - bo
	te := rd.Text
	if rd.Bold != nil && rd.Bold.font != nil {
		te = rd.Bold
	}
	label := func(t string, a float64) {
		px := int(math.Round(h * 0.2))
		for px > 7 && te.Measure(t, px) > r*1.55 {
			px--
		}
		tw := te.Measure(t, px)
		te.Draw(dst, t, px, cx-tw/2, cy+float64(px)*0.36, scaleC(fg, a))
	}
	strokeArc(dst, cx, cy, r, -math.Pi/2, math.Pi*1.5, 3.4*s, track)
	if ba > 0 {
		lv := math.Max(0, math.Min(1, it.WLevel))
		col := fg
		switch {
		case it.WCharging:
			col = pc(48, 209, 88, 1)
		case lv <= 0.2:
			col = pc(255, 69, 58, 1)
		}
		if it.WHasBt && lv > 0 {
			strokeArc(dst, cx, cy, r, -math.Pi/2, -math.Pi/2+2*math.Pi*lv, 3.4*s, scaleC(col, ba))
		}
		t := it.WText1
		if !it.WHasBt {
			t = "AC"
		}
		label(t, ba)
		// Wi-Fi strength: four dots (all dim when offline)
		for i := 0; i < 4; i++ {
			c := track
			if i < it.WWifi {
				c = fg
			}
			fillCircle(dst, cx+(float64(i)-1.5)*h*0.1, y+bh*0.84, 2.1*s, scaleC(c, ba))
		}
	}
	if bo > 0 {
		sun := scaleC(pc(255, 214, 10, 1), bo)
		bl := math.Max(0, math.Min(1, it.WBr))
		if bl > 0 {
			strokeArc(dst, cx, cy, r, -math.Pi/2, -math.Pi/2+2*math.Pi*bl, 3.4*s, sun)
		}
		label(strconv.Itoa(int(math.Round(bl*100))), bo)
		// one yellow dot where the Wi-Fi dots were
		fillCircle(dst, cx, y+bh*0.84, 2.4*s, sun)
	}
}

// drawPlayer is the Now Playing tile: cover art plus a live sound wave.
// Bottom dock: art on the left, wave to its right. Side dock: art on top, wave below.
func (rd *Renderer) drawPlayer(d *Dock, dst *image.RGBA, it *Item, x, y, bw, bh float64, fg, dim color.RGBA, light bool) {
	s := d.Scale
	vertical := d.Orient != OrientBottom
	ww := smooth01(it.WWave)
	var art, ax, ay float64
	if vertical {
		// cover centred when the wave is hidden, moving to the top as the wave grows in below it
		full := bw * 0.74
		art = math.Round(math.Min(full, full+(bh*0.55-full)*ww))
		ax = x + (bw-art)/2
		ay = y + (bh-art)/2 + (bw*0.13-(bh-art)/2)*ww
	} else {
		art = math.Round(bh * 0.74)
		ax, ay = x+(bh-art)/2, y+(bh-art)/2
	}
	if it.Art != nil {
		rd.drawArt(dst, it.Art, ax, ay, art, art*0.22, 1)
	} else {
		drawMusicTile(dst, ax, ay, art)
	}
	if it.WHot >= 0 {
		fillRoundRect(dst, ax, ay, art, art, art*0.22, image.NewUniform(pc(0, 0, 0, 0.42)))
		g := 3
		if it.WPlaying {
			g = 4
		}
		drawMediaGlyph(dst, g, ax+art/2, ay+art/2, art*0.17, s, pc(255, 255, 255, 1))
	}
	// sound wave in the cover's colour
	wc := pc(it.WAccent[0], it.WAccent[1], it.WAccent[2], 1)
	if it.WAccent == [3]uint8{} {
		wc = pc(255, 92, 120, 1)
	}
	// volume overlay after scrolling: holds for 1 s, then fades back to the wave in 0.4 s
	vo := 0.0
	if !it.WVolAt.IsZero() {
		if t := time.Since(it.WVolAt).Seconds(); t < 1.4 {
			vo = math.Min(1, (1.4-t)/0.4)
		}
	}
	waveC := scaleC(wc, (1-vo)*ww)
	n := len(it.WLevels)
	var wx0, wx1, wcy, maxH float64
	if vertical {
		wx0, wx1 = x+bw*0.16, x+bw*0.84
		wcy = ay + art + (y+bh-(ay+art))/2
		maxH = math.Min((y+bh)-(ay+art)-6*s, bw*0.3)
	} else {
		wx0, wx1 = ax+art+bh*0.2, x+bw-bh*0.2
		wcy = y + bh/2
		maxH = bh * 0.46
	}
	step := (wx1 - wx0) / float64(n)
	bwid := math.Max(1.5*s, math.Min(3.2*s, step*0.5))
	for i := 0; i < n; i++ {
		h := math.Max(bwid, maxH*it.WLevels[i])
		bx := wx0 + step*(float64(i)+0.5) - bwid/2
		h = math.Max(bwid*ww, h*ww)
		if vo < 1 && ww > 0.02 {
			fillRoundRect(dst, bx, wcy-h/2, bwid, h, bwid/2, image.NewUniform(waveC))
		}
	}
	if vo > 0 && ww > 0.02 {
		rd.drawVolume(dst, it, ax, ay, art, wx0, wx1, wcy, s, wc, fg, vo)
	}
	_, _, _ = fg, dim, light
}

// drawMusicTile is the placeholder cover: a music note on a pink tile.
func drawMusicTile(dst *image.RGBA, x, y, sz float64) {
	fillRoundRect(dst, x, y, sz, sz, sz*0.22, &vGrad{y0: y, y1: y + sz, c0: pc(252, 92, 125, 1), c1: pc(234, 44, 70, 1)})
	w := image.NewUniform(pc(255, 255, 255, 1))
	fillCircle(dst, x+sz*0.36, y+sz*0.68, sz*0.1, pc(255, 255, 255, 1))
	fillCircle(dst, x+sz*0.66, y+sz*0.62, sz*0.1, pc(255, 255, 255, 1))
	fillRoundRect(dst, x+sz*0.42, y+sz*0.27, sz*0.055, sz*0.42, sz*0.02, w)
	fillRoundRect(dst, x+sz*0.72, y+sz*0.22, sz*0.055, sz*0.42, sz*0.02, w)
	fillRoundRect(dst, x+sz*0.42, y+sz*0.22, sz*0.355, sz*0.09, sz*0.03, w)
}

// drawMediaGlyph draws previous (0), play/pause (1) or next (2).
func drawMediaGlyph(dst *image.RGBA, kind int, cx, cy, g, s float64, col color.RGBA) {
	tri := func(x0, y0, x1, y1, x2, y2 float64) {
		r := coverRect(dst, math.Min(x0, math.Min(x1, x2)), math.Min(y0, math.Min(y1, y2)),
			math.Abs(math.Max(x0, math.Max(x1, x2))-math.Min(x0, math.Min(x1, x2))), math.Abs(math.Max(y0, math.Max(y1, y2))-math.Min(y0, math.Min(y1, y2))))
		if r.Empty() {
			return
		}
		z := vector.NewRasterizer(r.Dx(), r.Dy())
		z.DrawOp = draw.Over
		ox, oy := float64(r.Min.X), float64(r.Min.Y)
		z.MoveTo(float32(x0-ox), float32(y0-oy))
		z.LineTo(float32(x1-ox), float32(y1-oy))
		z.LineTo(float32(x2-ox), float32(y2-oy))
		z.ClosePath()
		z.Draw(dst, r, image.NewUniform(col), r.Min)
	}
	bar := func(x, w float64) { fillRoundRect(dst, x, cy-g, w, 2*g, w/2, image.NewUniform(col)) }
	switch kind {
	case 0:
		bar(cx-g*1.05, g*0.32)
		tri(cx+g*0.95, cy-g, cx+g*0.95, cy+g, cx-g*0.6, cy)
	case 1:
		tri(cx-g*1.0, cy-g, cx-g*1.0, cy+g, cx+g*0.35, cy)
		bar(cx+g*0.5, g*0.3)
		bar(cx+g*0.98, g*0.3)
	case 2:
		tri(cx-g*0.95, cy-g, cx-g*0.95, cy+g, cx+g*0.6, cy)
		bar(cx+g*0.73, g*0.32)
	case 3: // play
		tri(cx-g*0.65, cy-g, cx-g*0.65, cy+g, cx+g*0.95, cy)
	case 4: // pause
		bar(cx-g*0.62, g*0.42)
		bar(cx+g*0.2, g*0.42)
	}
}

// drawArt draws cover art clipped to a rounded square (cached per size).
func (rd *Renderer) drawArt(dst *image.RGBA, art *IconSet, x, y, size, rad, alpha float64) {
	isz := int(math.Round(size))
	if isz < 2 {
		return
	}
	if rd.artSrc != art || rd.artSize != isz || rd.artImg == nil {
		src := art.Pick(float64(isz))
		tmp := image.NewRGBA(image.Rect(0, 0, isz, isz))
		xdraw.CatmullRom.Scale(tmp, tmp.Bounds(), src, src.Bounds(), xdraw.Src, nil)
		out := image.NewRGBA(tmp.Bounds())
		mask := squircleMask(isz, 0, 0, float64(isz), rad)
		draw.DrawMask(out, out.Bounds(), tmp, image.Point{}, mask, image.Point{}, draw.Src)
		rd.artImg, rd.artSrc, rd.artSize = out, art, isz
	}
	r := image.Rect(int(math.Round(x)), int(math.Round(y)), int(math.Round(x))+isz, int(math.Round(y))+isz)
	if alpha < 0.999 {
		draw.DrawMask(dst, r, rd.artImg, image.Point{}, image.NewUniform(color.Alpha{uint8(alpha * 255)}), image.Point{}, draw.Over)
		return
	}
	draw.Draw(dst, r, rd.artImg, image.Point{}, draw.Over)
}

// drawMediaCard is the Now Playing flyout: cover art, song, artist, app and controls.
func (rd *Renderer) drawMediaCard(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	a := math.Min((d.PopT-0.55)/0.4, 1)
	mp, art, gap, _, bh := d.mediaMetrics()
	x0, y0 := d.PopX0, d.PopY0
	ax, ay := x0+mp, y0+mp
	if d.Pop.Art != nil {
		rd.drawArt2(dst, d.Pop.Art, ax, ay, art, 10*s, a)
	} else {
		fillRoundRect(dst, ax, ay, art, art, 10*s, image.NewUniform(d.ink(0.08*a)))
		// music note placeholder
		fillCircle(dst, ax+art*0.42, ay+art*0.66, art*0.1, d.ink(0.5*a))
		fillRoundRect(dst, ax+art*0.48, ay+art*0.28, art*0.05, art*0.4, art*0.02, image.NewUniform(d.ink(0.5*a)))
		fillRoundRect(dst, ax+art*0.48, ay+art*0.28, art*0.2, art*0.07, art*0.03, image.NewUniform(d.ink(0.5*a)))
	}
	tp, sp, _, _, _ := d.popPx()
	tx := ax + art + gap
	tw := d.PopX1 - mp - tx
	y := ay + float64(tp)
	rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Title, tp, tw), tp, tx, y, d.fg(a))
	y += float64(sp+1) * 1.45
	rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Subtitle, sp+1, tw), sp+1, tx, y, d.fg2(a))
	if d.Pop.Line3 != "" {
		y += float64(sp) * 1.4
		rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Line3, sp, tw), sp, tx, y, d.dimC(a))
	}
	for i, r := range d.Pop.Rows {
		cx, cy := (r.X0+r.X1)/2, (r.Y0+r.Y1)/2
		if i == d.PopHot {
			fillRoundRect(dst, r.X0, r.Y0, r.X1-r.X0, r.Y1-r.Y0, 8*s, image.NewUniform(d.ink(0.12*a)))
		}
		g := i
		if i == 1 {
			g = 3
			if d.Pop.Playing {
				g = 4
			}
		}
		gs := bh * 0.27
		if i == 1 {
			gs = bh * 0.3
		}
		drawMediaGlyph(dst, g, cx, cy, gs, s, d.fg(a))
	}
	return image.Rect(int(d.PopX0)-2, int(d.PopY0)-2, int(d.PopX1)+3, int(d.PopY1)+3)
}

// drawArt2 is drawArt with its own cache slot (card art is a different size).
func (rd *Renderer) drawArt2(dst *image.RGBA, art *IconSet, x, y, size, rad, alpha float64) {
	saveI, saveS, saveZ := rd.artImg, rd.artSrc, rd.artSize
	rd.artImg, rd.artSrc, rd.artSize = rd.art2Img, rd.art2Src, rd.art2Size
	rd.drawArt(dst, art, x, y, size, rad, alpha)
	rd.art2Img, rd.art2Src, rd.art2Size = rd.artImg, rd.artSrc, rd.artSize
	rd.artImg, rd.artSrc, rd.artSize = saveI, saveS, saveZ
}

// drawTiles draws window-preview tiles or a folder-stack grid inside the flyout.
func (rd *Renderer) drawTiles(d *Dock, dst *image.RGBA, a float64, rp int) {
	s := d.Scale
	lp := int(math.Round(11.5 * s))
	lim := maxThumbs
	if d.Pop.Mode == PopGrid {
		lim = maxGrid
	}
	for i, r := range d.Pop.Rows {
		if i >= lim || r.X1 <= r.X0 {
			break
		}
		hot := i == d.PopHot
		if d.Pop.Mode == PopThumbs {
			bgA := 0.06
			if hot {
				bgA = 0.13
			}
			fillRoundRect(dst, r.X0, r.Y0, r.X1-r.X0, r.Y1-r.Y0, 9*s, image.NewUniform(d.ink(bgA*a)))
			// fallback picture (covered by the live preview when there is one)
			fillRoundRect(dst, r.TX0, r.TY0, r.TX1-r.TX0, r.TY1-r.TY0, 6*s, image.NewUniform(pc(0, 0, 0, 0.35*a)))
			icon := r.Icon
			if icon == nil && d.PopItem != nil {
				icon = d.PopItem.Icon
			}
			if icon != nil {
				isz := math.Round(44 * s)
				cx, cy := (r.TX0+r.TX1)/2, (r.TY0+r.TY1)/2
				drawIconSet(dst, icon, cx-isz/2, cy-isz/2-(6*s*btof(r.Min)), isz, a)
				if r.Min {
					t := "Minimised"
					tw := rd.Text.Measure(t, lp)
					rd.Text.Draw(dst, t, lp, cx-tw/2, cy+isz/2+6*s, d.dimC(a))
				}
			}
			ly := r.Y1 - (r.Y1-r.TY1)/2 + float64(lp)*0.36
			maxW := r.X1 - r.X0 - 12*s
			if hot {
				maxW -= 24 * s
			}
			rd.Text.Draw(dst, truncateLabel(rd.Text, r.Title, lp, maxW), lp, r.X0+7*s, ly, d.fg2(a))
			if hot && r.CX1 > r.CX0 {
				cx, cy, cr := (r.CX0+r.CX1)/2, (r.CY0+r.CY1)/2, (r.CX1-r.CX0)/2
				fillCircle(dst, cx, cy, cr, d.ink(0.16*a))
				drawX(dst, cx, cy, cr*0.42, s, d.fg(a))
			}
		} else {
			if hot {
				fillRoundRect(dst, r.X0, r.Y0, r.X1-r.X0, r.Y1-r.Y0, 8*s, image.NewUniform(d.ink(0.12*a)))
			}
			if r.Icon != nil && r.Small {
				// tray icons are small: draw them at a crisp size on a tile, like the dock
				ts := r.TX1 - r.TX0
				fillRoundRect(dst, r.TX0, r.TY0, ts, ts, ts*0.22, image.NewUniform(d.ink(0.09*a)))
				is := math.Round(ts * 0.52)
				drawIconSet(dst, r.Icon, r.TX0+(ts-is)/2, r.TY0+(ts-is)/2, is, a)
			} else if r.Icon != nil {
				drawIconSet(dst, r.Icon, r.TX0, r.TY0, r.TX1-r.TX0, a)
			}
			t := truncateLabel(rd.Text, r.Title, lp, r.X1-r.X0-6*s)
			tw := rd.Text.Measure(t, lp)
			rd.Text.Draw(dst, t, lp, (r.X0+r.X1)/2-tw/2, r.Y1-8*s, d.fg2(a))
		}
	}
}

func btof(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func drawIconSet(dst *image.RGBA, is *IconSet, x, y, sz, alpha float64) {
	if is == nil || sz < 1 {
		return
	}
	src := is.Pick(sz)
	r := image.Rect(int(math.Round(x)), int(math.Round(y)), int(math.Round(x+sz)), int(math.Round(y+sz)))
	var opts *xdraw.Options
	if alpha < 0.999 {
		opts = &xdraw.Options{SrcMask: image.NewUniform(color.Alpha{uint8(alpha * 255)})}
	}
	xdraw.BiLinear.Scale(dst, r, src, src.Bounds(), xdraw.Over, opts)
}

func drawX(dst *image.RGBA, cx, cy, r, s float64, col color.RGBA) {
	c := image.NewUniform(col)
	n := int(r * 2 / (0.6 * s))
	for k := 0; k <= n; k++ {
		t := float64(k)/float64(max(n, 1))*2 - 1
		fillRoundRect(dst, cx+t*r-0.9*s, cy+t*r-0.9*s, 1.8*s, 1.8*s, 0.9*s, c)
		fillRoundRect(dst, cx+t*r-0.9*s, cy-t*r-0.9*s, 1.8*s, 1.8*s, 0.9*s, c)
	}
}

// drawLine draws a round-capped line of thickness t.
func drawLine(dst *image.RGBA, x0, y0, x1, y1, t float64, col color.RGBA) {
	c := image.NewUniform(col)
	l := math.Hypot(x1-x0, y1-y0)
	n := int(l/(t*0.3)) + 1
	for k := 0; k <= n; k++ {
		f := float64(k) / float64(n)
		x, y := x0+(x1-x0)*f, y0+(y1-y0)*f
		fillRoundRect(dst, x-t/2, y-t/2, t, t, t/2, c)
	}
}

// labelAt draws a hover label whose edge-side touches (ax, ay).
func (rd *Renderer) labelAt(d *Dock, dst *image.RGBA, text string, ax, ay float64, orient int) image.Rectangle {
	s := d.Scale
	px := int(math.Round(13 * s))
	text = truncateLabel(rd.Text, text, px, 360*s)
	tw := rd.Text.Measure(text, px)
	if tw <= 0 {
		return image.Rectangle{}
	}
	padX, padY := 10*s, 5*s
	w := tw + 2*padX
	h := float64(px) + 2*padY + 2*s
	var x, y float64
	switch orient {
	case OrientLeft:
		x, y = ax, ay-h/2
	case OrientRight:
		x, y = ax-w, ay-h/2
	default:
		x, y = ax-w/2, ay-h
	}
	W, H := float64(dst.Bounds().Dx()), float64(dst.Bounds().Dy())
	x = math.Max(4*s, math.Min(x, W-4*s-w))
	y = math.Max(4*s, math.Min(y, H-4*s-h))
	var bg, fg, br color.RGBA
	if d.Dark {
		bg, fg, br = pc(44, 44, 50, 0.94), pc(242, 242, 245, 1), pc(255, 255, 255, 0.14)
	} else {
		bg, fg, br = pc(246, 246, 248, 0.95), pc(28, 28, 32, 1), pc(0, 0, 0, 0.12)
	}
	fillRoundRect(dst, x, y+1.5*s, w, h, h*0.32, image.NewUniform(pc(0, 0, 0, 0.12)))
	fillRoundRect(dst, x, y, w, h, h*0.32, image.NewUniform(bg))
	strokeRoundRect(dst, x, y, w, h, h*0.32, 1*s, br)
	rd.Text.Draw(dst, text, px, x+padX, y+padY+float64(px)*0.86+0.5*s, fg)
	return image.Rect(int(x)-2, int(y)-2, int(x+w)+3, int(y+h+3*s)+3)
}

// scaledIcon returns the item's icon at an exact pixel size, cached between frames.
func scaledIcon(it *Item, size int) *image.RGBA {
	if it.scaled != nil && it.scaledSize == size && it.scaledSrc == it.Icon {
		return it.scaled
	}
	src := it.Icon.Pick(float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if size == src.Bounds().Dx() {
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(img, img.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	}
	it.scaled, it.scaledSize, it.scaledSrc = img, size, it.Icon
	return img
}

// drawIconSmooth draws an icon that's mid-animation (growing, shrinking): from the
// cached full-size image with a fast filter, at an exact sub-pixel size and position, so
// it glides instead of re-resizing with the slow filter (and wobbling a pixel) every frame.
func drawIconSmooth(dst *image.RGBA, it *Item, x, y, sz, full float64, alpha float64) {
	if it.Icon == nil || sz < 0.5 || alpha < 0.004 {
		return
	}
	fs := int(math.Round(full))
	src := scaledIcon(it, fs)
	scaleOver(dst, src, x, y, sz/float64(fs), alpha)
}

// scaleOver draws src scaled by k with its top-left at (x, y) — fractional values
// allowed — blending over dst with an overall alpha. Bilinear, premultiplied, fixed
// point: several times faster than x/image/draw's general transform for this case.
func scaleOver(dst, src *image.RGBA, x, y, k, alpha float64) {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if k <= 0 || sw < 2 || sh < 2 {
		return
	}
	r := image.Rect(int(math.Floor(x)), int(math.Floor(y)), int(math.Ceil(x+float64(sw)*k)), int(math.Ceil(y+float64(sh)*k))).Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	inv := 1 / k
	ga := uint32(math.Round(alpha * 256))
	if ga > 256 {
		ga = 256
	}
	maxX, maxY := float64(sw-1), float64(sh-1)
	// per-column source positions and weights, worked out once
	n := r.Dx()
	cx0 := make([]int32, n)
	cwx := make([]uint32, n)
	cok := make([]bool, n)
	for i := 0; i < n; i++ {
		fx := (float64(r.Min.X+i)+0.5-x)*inv - 0.5
		if fx < -0.5 || fx > maxX+0.5 {
			continue
		}
		fx = math.Max(0, math.Min(maxX-0.0001, fx))
		x0 := int(fx)
		cx0[i], cwx[i], cok[i] = int32(x0*4), uint32((fx-float64(x0))*256), true
	}
	sp, ss := src.Pix, src.Stride
	for py := r.Min.Y; py < r.Max.Y; py++ {
		fy := (float64(py)+0.5-y)*inv - 0.5
		if fy < -0.5 || fy > maxY+0.5 {
			continue
		}
		fy = math.Max(0, math.Min(maxY-0.0001, fy))
		y0 := int(fy)
		wy := uint32((fy - float64(y0)) * 256)
		r0 := sp[y0*ss : y0*ss+ss]
		r1 := sp[(y0+1)*ss : (y0+1)*ss+ss]
		d := dst.Pix[(py-dst.Rect.Min.Y)*dst.Stride+(r.Min.X-dst.Rect.Min.X)*4:]
		for i := 0; i < n; i++ {
			if !cok[i] {
				continue
			}
			o := cx0[i]
			if r0[o+3]|r0[o+7]|r1[o+3]|r1[o+7] == 0 {
				continue // fully transparent here
			}
			wx := cwx[i]
			w00, w01 := (256-wx)*(256-wy), wx*(256-wy)
			w10, w11 := (256-wx)*wy, wx*wy
			a := (uint32(r0[o+3])*w00 + uint32(r0[o+7])*w01 + uint32(r1[o+3])*w10 + uint32(r1[o+7])*w11) >> 16 * ga >> 8
			if a == 0 {
				continue
			}
			rr := (uint32(r0[o])*w00 + uint32(r0[o+4])*w01 + uint32(r1[o])*w10 + uint32(r1[o+4])*w11) >> 16 * ga >> 8
			gg := (uint32(r0[o+1])*w00 + uint32(r0[o+5])*w01 + uint32(r1[o+1])*w10 + uint32(r1[o+5])*w11) >> 16 * ga >> 8
			bb := (uint32(r0[o+2])*w00 + uint32(r0[o+6])*w01 + uint32(r1[o+2])*w10 + uint32(r1[o+6])*w11) >> 16 * ga >> 8
			ia := 255 - a
			q := d[i*4 : i*4+4 : i*4+4]
			q[0] = uint8(rr + (uint32(q[0])*ia+127)/255)
			q[1] = uint8(gg + (uint32(q[1])*ia+127)/255)
			q[2] = uint8(bb + (uint32(q[2])*ia+127)/255)
			q[3] = uint8(a + (uint32(q[3])*ia+127)/255)
		}
	}
}

func drawIcon(dst *image.RGBA, it *Item, x, y, sz float64, alpha float64) {
	if it.Icon == nil || sz < 1 {
		return
	}
	isz := int(math.Round(sz))
	if isz < 1 {
		return
	}
	src := scaledIcon(it, isz)
	r := image.Rect(int(math.Round(x)), int(math.Round(y)), int(math.Round(x))+isz, int(math.Round(y))+isz)
	if alpha < 0.999 {
		draw.DrawMask(dst, r, src, image.Point{}, image.NewUniform(color.Alpha{uint8(alpha * 255)}), image.Point{}, draw.Over)
		return
	}
	draw.Draw(dst, r, src, image.Point{}, draw.Over)
}

// ---------- generated icons ----------

// StartIcon draws a launchpad-style grid icon.
func StartIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	m := s * 0.06
	fillRoundRect(img, m, m+s*0.01, s-2*m, s-2*m, s*0.225, image.NewUniform(pc(0, 0, 0, 0.18)))
	fillRoundRect(img, m, m, s-2*m, s-2*m, s*0.225,
		&vGrad{y0: m, y1: s - m, c0: pc(98, 92, 255, 1), c1: pc(40, 140, 255, 1)})
	strokeRoundRect(img, m, m, s-2*m, s-2*m, s*0.225, s*0.008, pc(255, 255, 255, 0.35))
	cell := (s - 2*m) * 0.2
	gap := (s - 2*m - 3*cell) / 4
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			x := m + gap + float64(c)*(cell+gap)
			y := m + gap + float64(r)*(cell+gap)
			fillRoundRect(img, x, y, cell, cell, cell*0.32, image.NewUniform(pc(255, 255, 255, 0.95)))
		}
	}
	return img
}

// LetterIcon is a fallback for items without an icon.
func LetterIcon(size int, name string, te *TextEngine) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	m := s * 0.06
	fillRoundRect(img, m, m, s-2*m, s-2*m, s*0.225,
		&vGrad{y0: m, y1: s - m, c0: pc(150, 156, 168, 1), c1: pc(100, 106, 120, 1)})
	letter := "?"
	for _, r := range name {
		letter = strings.ToUpper(string(r))
		break
	}
	px := int(s * 0.5)
	tw := te.Measure(letter, px)
	te.Draw(img, letter, px, s/2-tw/2, s*0.68, pc(255, 255, 255, 1))
	return img
}

// ---------- macOS-style tiles ----------

// squircleMask returns an alpha mask of a rounded tile.
func squircleMask(size int, x, y, w, r float64) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, size, size))
	z := vector.NewRasterizer(size, size)
	roundRectPath(z, 0, 0, x, y, w, w, r, false)
	z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

func contentBounds(img *image.RGBA) (image.Rectangle, float64) {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.Pix[img.PixOffset(x, y)+3] > 10 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < minX {
		return image.Rectangle{}, 0
	}
	r := image.Rect(minX, minY, maxX+1, maxY+1)
	opaque := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.Pix[img.PixOffset(x, y)+3] > 200 {
				opaque++
			}
		}
	}
	return r, float64(opaque) / float64(r.Dx()*r.Dy())
}

// MacTile places an icon on a macOS-style rounded tile (or reshapes an icon that
// is already a full square tile into the rounded shape).
func MacTile(src *image.RGBA, dark bool) *image.RGBA {
	const S = 256
	out := image.NewRGBA(image.Rect(0, 0, S, S))
	cb, coverage := contentBounds(src)
	if cb.Empty() {
		return src
	}
	sb := float64(src.Bounds().Dx())
	m := float64(S) * 0.095 // macOS icon grid margin
	tw := float64(S) - 2*m
	rad := tw * 0.225

	// drop shadow
	for i := 5; i >= 1; i-- {
		e := float64(i) * 1.4
		fillRoundRect(out, m-e, m-e+4, tw+2*e, tw+2*e, rad+e, image.NewUniform(pc(0, 0, 0, 0.035)))
	}

	aspect := float64(cb.Dx()) / float64(cb.Dy())
	isTile := coverage > 0.86 && aspect > 0.86 && aspect < 1.16 && float64(cb.Dx()) > sb*0.55
	mask := squircleMask(S, m, m, tw, rad)

	if isTile {
		tmp := image.NewRGBA(out.Bounds())
		tr := image.Rect(int(m), int(m), int(m+tw+0.5), int(m+tw+0.5))
		xdraw.CatmullRom.Scale(tmp, tr, src, cb, xdraw.Src, nil)
		draw.DrawMask(out, out.Bounds(), tmp, image.Point{}, mask, image.Point{}, draw.Over)
	} else {
		var c0, c1 color.RGBA
		if dark {
			c0, c1 = pc(64, 64, 70, 1), pc(34, 34, 38, 1)
		} else {
			c0, c1 = pc(255, 255, 255, 1), pc(232, 233, 238, 1)
		}
		fillRoundRect(out, m, m, tw, tw, rad, &vGrad{y0: m, y1: m + tw, c0: c0, c1: c1})
		// icon artwork centred at ~62% of the tile
		inner := tw * 0.64
		w, h := float64(cb.Dx()), float64(cb.Dy())
		sc := inner / math.Max(w, h)
		dw, dh := w*sc, h*sc
		x0 := m + (tw-dw)/2
		y0 := m + (tw-dh)/2
		r := image.Rect(int(x0+0.5), int(y0+0.5), int(x0+dw+0.5), int(y0+dh+0.5))
		xdraw.CatmullRom.Scale(out, r, src, cb, xdraw.Over, nil)
	}
	// glassy rim + top highlight
	if dark {
		strokeRoundRect(out, m, m, tw, tw, rad, 2, pc(255, 255, 255, 0.16))
	} else {
		strokeRoundRect(out, m, m, tw, tw, rad, 1.5, pc(0, 0, 0, 0.10))
		strokeRoundRect(out, m+1.5, m+1.5, tw-3, tw-3, rad-1.5, 2, pc(255, 255, 255, 0.55))
	}
	return out
}

// ---------- effects ----------

func drawPressed(dst *image.RGBA, it *Item, x, y, sz float64) {
	if it.Icon == nil || sz < 1 {
		return
	}
	src := it.Icon.Pick(sz)
	r := image.Rect(int(math.Round(x)), int(math.Round(y)), int(math.Round(x+sz)), int(math.Round(y+sz)))
	tmp := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	xdraw.BiLinear.Scale(tmp, tmp.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	draw.DrawMask(dst, r, image.NewUniform(pc(0, 0, 0, 0.38)), image.Point{}, tmp, image.Point{}, draw.Over)
}

func drawPoof(dst *image.RGBA, p Poof, t float64, s float64, dark bool) image.Rectangle {
	// t in 0..1: a ring of soft puffs expanding and fading
	r := (14 + 30*t) * s
	a := 0.55 * (1 - t)
	c := pc(235, 235, 240, a)
	if dark {
		c = pc(200, 200, 210, a)
	}
	for i := 0; i < 7; i++ {
		ang := float64(i)/7*2*math.Pi + t*1.2
		px := p.X + math.Cos(ang)*r
		py := p.Y + math.Sin(ang)*r*0.8
		fillCircle(dst, px, py, (10+6*t)*s, c)
	}
	fillCircle(dst, p.X, p.Y, (16*(1-t))*s+1, c)
	e := r + 18*s
	return image.Rect(int(p.X-e), int(p.Y-e), int(p.X+e), int(p.Y+e))
}

// MacPlain fits an icon inside the macOS icon grid margins without a tile (folders, trash).
func MacPlain(src *image.RGBA) *image.RGBA {
	const S = 256
	cb, _ := contentBounds(src)
	if cb.Empty() {
		return src
	}
	out := image.NewRGBA(image.Rect(0, 0, S, S))
	inner := float64(S) * 0.84
	w, h := float64(cb.Dx()), float64(cb.Dy())
	sc := inner / math.Max(w, h)
	dw, dh := w*sc, h*sc
	x0 := (S - dw) / 2
	y0 := (S - dh) / 2
	xdraw.CatmullRom.Scale(out, image.Rect(int(x0+0.5), int(y0+0.5), int(x0+dw+0.5), int(y0+dh+0.5)), src, cb, xdraw.Over, nil)
	return out
}

// strokeArc draws an arc with round caps. Angles in radians, 0 = right, clockwise (screen y down).
func strokeArc(dst *image.RGBA, cx, cy, r, a0, a1, w float64, col color.RGBA) {
	if a1 < a0 {
		a0, a1 = a1, a0
	}
	n := int(math.Max(8, (a1-a0)*r/2))
	ro, ri := r+w/2, r-w/2
	minx, miny := cx-ro-2, cy-ro-2
	b := image.Rect(int(minx), int(miny), int(cx+ro+3), int(cy+ro+3)).Intersect(dst.Bounds())
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	z.DrawOp = draw.Over
	ox, oy := float64(b.Min.X), float64(b.Min.Y)
	pt := func(a, rr float64) (float32, float32) {
		return float32(cx + math.Cos(a)*rr - ox), float32(cy + math.Sin(a)*rr - oy)
	}
	x, y := pt(a0, ro)
	z.MoveTo(x, y)
	for i := 1; i <= n; i++ {
		x, y = pt(a0+(a1-a0)*float64(i)/float64(n), ro)
		z.LineTo(x, y)
	}
	for i := n; i >= 0; i-- {
		x, y = pt(a0+(a1-a0)*float64(i)/float64(n), ri)
		z.LineTo(x, y)
	}
	z.ClosePath()
	z.Draw(dst, b, image.NewUniform(col), b.Min)
	for _, a := range []float64{a0, a1} {
		fillCircle(dst, cx+math.Cos(a)*r, cy+math.Sin(a)*r, w/2, col)
	}
}

// scaleC fades a premultiplied colour.
func scaleC(c color.RGBA, a float64) color.RGBA {
	a = math.Max(0, math.Min(1, a))
	m := func(v uint8) uint8 { return uint8(math.Round(float64(v) * a)) }
	return color.RGBA{m(c.R), m(c.G), m(c.B), m(c.A)}
}

// drawVolume is the volume readout shown while scrolling on the player: the level as a
// number over the cover, and a bar where the wave was.
func (rd *Renderer) drawVolume(dst *image.RGBA, it *Item, ax, ay, art, x0, x1, cy, s float64, wc, fg color.RGBA, a float64) {
	fillRoundRect(dst, ax, ay, art, art, art*0.22, image.NewUniform(pc(0, 0, 0, 0.55*a)))
	txt := "Mute"
	if it.WVol >= 0 {
		txt = strconv.Itoa(int(math.Round(it.WVol * 100)))
	}
	te := rd.Text
	if rd.Bold != nil && rd.Bold.font != nil {
		te = rd.Bold
	}
	px := int(math.Round(art * 0.36))
	for px > 7 && te.Measure(txt, px) > art*0.8 {
		px--
	}
	tw := te.Measure(txt, px)
	te.Draw(dst, txt, px, ax+(art-tw)/2, ay+art/2+float64(px)*0.36, scaleC(pc(255, 255, 255, 1), a))
	th := math.Max(3, 4*s)
	track := scaleC(fg, 0.22*a)
	fillRoundRect(dst, x0, cy-th/2, x1-x0, th, th/2, image.NewUniform(track))
	if it.WVol > 0 {
		w := math.Max(th, (x1-x0)*it.WVol)
		fillRoundRect(dst, x0, cy-th/2, w, th, th/2, image.NewUniform(scaleC(wc, a)))
	}
}

// drawPageDots is the page indicator under the apps pill: small dots, with the current
// page as a longer capsule that glides between them when you change page.
func (rd *Renderer) drawPageDots(d *Dock, dst *image.RGBA, cu float64) image.Rectangle {
	s := d.Scale
	v := d.PlateV0 - d.M/2
	gap := 10 * s
	r := 2.1 * s
	n := d.PageN
	u0 := cu - gap*float64(n-1)/2
	for i := 0; i < n; i++ {
		x, y := d.toXY(u0+gap*float64(i), v)
		fillCircle(dst, x, y, r, d.ink(0.34))
	}
	// active capsule, stretched a little while it moves
	pf := math.Max(0, math.Min(float64(n-1), d.PageF))
	move := math.Abs(pf - math.Round(pf))
	half := 6*s + move*4*s
	au := u0 + gap*pf
	x, y, w, h := d.rectUV(au-half, v-r, au+half, v+r)
	fillRoundRect(dst, x, y, w, h, r, image.NewUniform(d.fg(0.95)))
	bx, by, bw, bh := d.rectUV(u0-half-2, v-r-2, u0+gap*float64(n-1)+half+2, v+r+2)
	return image.Rect(int(bx)-2, int(by)-2, int(bx+bw)+3, int(by+bh)+3)
}

// drawSearchTail draws the little pointer joining the search card to the Start icon,
// in the same colour as the card, so the card reads as the Start icon's popover.
func (rd *Renderer) drawSearchTail(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	it := d.SearchItem
	base := d.PlateV1 + d.SearchGap + 1*s // just inside the card's edge
	th := d.SearchGap - 3*s
	if th < 3*s {
		return image.Rectangle{}
	}
	tw, tr := 15*s, 2.2*s
	tip := base - th
	tu := it.U
	bx, by, bw, bh := d.rectUV(tu-tw-2, tip-2, tu+tw+2, base+2)
	r := coverRect(dst, bx, by, bw, bh)
	if r.Empty() {
		return image.Rectangle{}
	}
	z := vector.NewRasterizer(r.Dx(), r.Dy())
	z.DrawOp = draw.Over
	ox, oy := float32(r.Min.X), float32(r.Min.Y)
	p := func(u, v float64) (float32, float32) {
		x, y := d.toXY(u, v)
		return float32(x) - ox, float32(y) - oy
	}
	k := tr / th * tw
	z.MoveTo(p(tu-tw, base))
	z.LineTo(p(tu-k, tip+tr))
	x1, y1 := p(tu, tip)
	x2, y2 := p(tu+k, tip+tr)
	z.QuadTo(x1, y1, x2, y2)
	z.LineTo(p(tu+tw, base))
	z.ClosePath()
	c := pc(4, 4, 6, 1) // the card behind is opaque; match it exactly
	if d.Pal.Alpha > 0 {
		c = pc(d.Pal.Fill[0], d.Pal.Fill[1], d.Pal.Fill[2], 1)
	}
	z.Draw(dst, r, image.NewUniform(c), r.Min)
	return r
}

// drawScrollBar is a slim Windows 11-style scroll indicator down the right edge of a
// folder card: a faint track with a rounded thumb showing which rows are in view.
func (rd *Renderer) drawScrollBar(d *Dock, dst *image.RGBA, a float64) {
	s := d.Scale
	y0, y1 := math.Inf(1), math.Inf(-1)
	for _, r := range d.Pop.Rows {
		if r.X1 > r.X0 {
			y0, y1 = math.Min(y0, r.Y0), math.Max(y1, r.Y1)
		}
	}
	if y1 <= y0 {
		return
	}
	n, vis, top := float64(d.Pop.ScrollRows), float64(d.Pop.ScrollVis), float64(d.Pop.ScrollTop)
	w := 3 * s
	x := d.PopX1 - 7*s - w
	th := y1 - y0
	fillRoundRect(dst, x, y0, w, th, w/2, image.NewUniform(d.ink(0.08*a)))
	h := math.Max(th*vis/n, 16*s)
	ty := y0 + (th-h)*top/math.Max(n-vis, 1)
	fillRoundRect(dst, x, ty, w, h, w/2, image.NewUniform(d.ink(0.5*a)))
}
