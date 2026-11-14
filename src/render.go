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

// roundRectPointAt returns a point on a rounded rectangle, starting at the top
// centre and moving clockwise. The parameter is a fraction of the perimeter.
func roundRectPointAt(x, y, w, h, r, u float64) (float64, float64) {
	if w <= 0 || h <= 0 {
		return x + w/2, y
	}
	r = math.Max(0, math.Min(r, math.Min(w, h)/2))
	perimeter := 2*(w+h-4*r) + 2*math.Pi*r
	s := math.Max(0, math.Min(1, u)) * perimeter
	line := func(x0, y0, x1, y1, length float64) (float64, float64, bool) {
		if s <= length || length <= 0 {
			f := 0.0
			if length > 0 {
				f = s / length
			}
			return x0 + (x1-x0)*f, y0 + (y1-y0)*f, true
		}
		s -= length
		return 0, 0, false
	}
	arc := func(cx, cy, a0, length float64) (float64, float64, bool) {
		if s <= length || length <= 0 {
			a := a0
			if r > 0 {
				a += s / r
			}
			return cx + math.Cos(a)*r, cy + math.Sin(a)*r, true
		}
		s -= length
		return 0, 0, false
	}
	quarter := math.Pi * r / 2
	segments := []func() (float64, float64, bool){
		func() (float64, float64, bool) { return line(x+w/2, y, x+w-r, y, w/2-r) },
		func() (float64, float64, bool) { return arc(x+w-r, y+r, -math.Pi/2, quarter) },
		func() (float64, float64, bool) { return line(x+w, y+r, x+w, y+h-r, h-2*r) },
		func() (float64, float64, bool) { return arc(x+w-r, y+h-r, 0, quarter) },
		func() (float64, float64, bool) { return line(x+w-r, y+h, x+r, y+h, w-2*r) },
		func() (float64, float64, bool) { return arc(x+r, y+h-r, math.Pi/2, quarter) },
		func() (float64, float64, bool) { return line(x, y+h-r, x, y+r, h-2*r) },
		func() (float64, float64, bool) { return arc(x+r, y+r, math.Pi, quarter) },
		func() (float64, float64, bool) { return line(x+r, y, x+w/2, y, w/2-r) },
	}
	for _, segment := range segments {
		if px, py, ok := segment(); ok {
			return px, py
		}
	}
	return x + w/2, y
}

// strokeRoundRectProgress paints a rounded-square gauge arc with a clean,
// rounded end cap. x/y/w/h/r describe the centreline of the border.
func strokeRoundRectProgress(dst *image.RGBA, x, y, w, h, r, t, progress float64, col color.RGBA) {
	progress = math.Max(0, math.Min(1, progress))
	if progress <= 0 || w <= t || h <= t || t <= 0 {
		return
	}
	outerX, outerY := x-t/2, y-t/2
	outerW, outerH, outerR := w+t, h+t, r+t/2
	innerX, innerY := x+t/2, y+t/2
	innerW, innerH, innerR := w-t, h-t, math.Max(0, r-t/2)
	rc := coverRect(dst, outerX, outerY, outerW, outerH)
	if rc.Empty() {
		return
	}
	perimeter := 2*(w+h-4*r) + 2*math.Pi*r
	n := int(math.Max(16, math.Ceil(perimeter/1.4)))
	if n > 512 {
		n = 512
	}
	z := vector.NewRasterizer(rc.Dx(), rc.Dy())
	z.DrawOp = draw.Over
	ox, oy := float64(rc.Min.X), float64(rc.Min.Y)
	for i := 0; i <= n; i++ {
		u := progress * float64(i) / float64(n)
		px, py := roundRectPointAt(outerX, outerY, outerW, outerH, outerR, u)
		if i == 0 {
			z.MoveTo(float32(px-ox), float32(py-oy))
		} else {
			z.LineTo(float32(px-ox), float32(py-oy))
		}
	}
	for i := n; i >= 0; i-- {
		u := progress * float64(i) / float64(n)
		px, py := roundRectPointAt(innerX, innerY, innerW, innerH, innerR, u)
		z.LineTo(float32(px-ox), float32(py-oy))
	}
	z.ClosePath()
	z.Draw(dst, rc, image.NewUniform(col), rc.Min)
	if progress < 1 {
		px, py := roundRectPointAt(x, y, w, h, r, progress)
		fillCircle(dst, px, py, t/2, col)
	}
}

// strokeRoundRectSegment paints part of a rounded-square border, from u0 to u1 of
// the way round (0 = top centre, clockwise).
func strokeRoundRectSegment(dst *image.RGBA, x, y, w, h, r, t, u0, u1 float64, col color.RGBA) {
	if u1 <= u0 || w <= t || h <= t || t <= 0 {
		return
	}
	outerX, outerY := x-t/2, y-t/2
	outerW, outerH, outerR := w+t, h+t, r+t/2
	innerX, innerY := x+t/2, y+t/2
	innerW, innerH, innerR := w-t, h-t, math.Max(0, r-t/2)
	rc := coverRect(dst, outerX, outerY, outerW, outerH)
	if rc.Empty() {
		return
	}
	perimeter := 2*(w+h-4*r) + 2*math.Pi*r
	n := int(math.Max(6, math.Ceil(perimeter*(u1-u0)/1.4)))
	z := vector.NewRasterizer(rc.Dx(), rc.Dy())
	z.DrawOp = draw.Over
	ox, oy := float64(rc.Min.X), float64(rc.Min.Y)
	for i := 0; i <= n; i++ {
		px, py := roundRectPointAt(outerX, outerY, outerW, outerH, outerR, u0+(u1-u0)*float64(i)/float64(n))
		if i == 0 {
			z.MoveTo(float32(px-ox), float32(py-oy))
		} else {
			z.LineTo(float32(px-ox), float32(py-oy))
		}
	}
	for i := n; i >= 0; i-- {
		px, py := roundRectPointAt(innerX, innerY, innerW, innerH, innerR, u0+(u1-u0)*float64(i)/float64(n))
		z.LineTo(float32(px-ox), float32(py-oy))
	}
	z.ClosePath()
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
	artRad   float64
	art2Img  *image.RGBA
	art2Src  *IconSet
	art2Size int
	art2Rad  float64
}

func (rd *Renderer) bgKeyFor(d *Dock) string {
	k := fmt.Sprintf("%d|%d|%d|%d|%d|%dx%d|%d|%v%v%v%v|%.1f|%.1f|%.1f|%.1f|%.2f|%v|%d|%.3f", d.Orient, btoi(d.Pill), btoi(d.Notch), btoi(d.Connected), btoi(d.PillRound), d.Transparency, d.W, d.H,
		d.Dark, d.BlurOn, d.Tiles, d.Pill, d.PlateU0, d.PlateU1, d.PlateV0, d.PlateH, d.Scale, d.Pal, d.PageN, 0.0)
	if d.Pill {
		for _, g := range d.pillGroups() {
			k += fmt.Sprintf("|%.1f-%.1f-%.3f", g[0], g[1], g[2])
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
	default:
		x, y, w, h := d.rectUV(d.PlateU0, d.PlateV0, d.PlateU1, d.PlateV1)
		rd.drawGlass(d, rd.bg, x, y, w, h)
		r = image.Rect(int(x-14*s), int(y-14*s), int(x+w+14*s), int(y+h+14*s))
	}
	rd.bgRect = r.Intersect(b)
}

// Render draws a frame and returns the region of dst that changed (old ∪ new content).
func (rd *Renderer) Render(d *Dock, dst *image.RGBA) image.Rectangle {
	dgRed = d.accentC(1) // DG style widgets' highlight follows the accent
	weatherMono, weatherMonoInk = d.WidgetDG, d.fg(1)
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
	if d.SearchItem != nil { // Launchpad card: soft arrow from the Start button
		dirty = dirty.Union(rd.drawLaunchpadArrow(d, dst))
	}
	if d.Notch && d.PopItem != nil && d.PopT > 0.01 {
		dirty = dirty.Union(rd.drawFlyout(d, dst))
	}

	dotCol := d.ink(0.82)
	if gs := d.pillGroups(); d.PageN > 1 && len(gs) > 0 {
		dirty = dirty.Union(rd.drawPageDots(d, dst, (gs[0][0]+gs[0][1])/2))
	}
	groups := d.pillGroups()
	for _, it := range d.Items {
		if it.Kind == KindSep && d.Pill && !d.Connected || it.Kind == KindGap {
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
			sz = d.B * (0.92 + 0.08*e)
			v = d.PlateV0 + d.P + (d.B-sz)/2
			if it.PageOut {
				du = dir * (1 - e) * d.B * 0.45 // carries on out
				alpha = e * e * e               // and is gone before the new page lands
			} else {
				du = -dir * (1 - e) * d.B * 0.45 // comes in from behind
				alpha = e * e
			}
			// never spill past the pill: fade out as the icon reaches its edge,
			// instead of being drawn over the next pill or cut off
			for _, g := range groups {
				if it.U >= g[0] && it.U <= g[1] {
					u0, u1 := g[0]+d.P*0.6, g[1]-d.P*0.6
					c := it.U + du
					vis := (math.Min(c+sz/2, u1) - math.Max(c-sz/2, u0)) / sz
					vis = math.Max(0, math.Min(1, vis))
					alpha *= vis * vis * (3 - 2*vis)
					break
				}
			}
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
		// hover lift: the icon under the pointer rises and grows a little
		if it.HovT != 0 && it.Appear >= 1 {
			sz *= 1 + 0.14*it.HovT
			v += 6.5 * s * it.HovT
		}
		x, y, _, _ := d.rectUV(it.U+du-sz/2, v, it.U+du+sz/2, v+sz)
		if math.Abs(it.Sq) > 0.003 && it.Icon != nil {
			// jelly: squashed along the dock, anchored where the icon stands
			w, h := sz*(1+it.Sq), sz*(1-it.Sq)
			jx, jy, jw, jh := d.rectUV(it.U+du-w/2, v, it.U+du+w/2, v+h)
			jr := image.Rect(int(math.Round(jx)), int(math.Round(jy)), int(math.Round(jx+jw)), int(math.Round(jy+jh)))
			src := scaledIcon(it, int(math.Round(d.B)))
			var opts *xdraw.Options
			if alpha < 0.999 {
				opts = &xdraw.Options{SrcMask: image.NewUniform(color.Alpha{uint8(alpha * 255)})}
			}
			xdraw.BiLinear.Scale(target, jr, src, src.Bounds(), xdraw.Over, opts)
			grow(jx, jy, jw, jh)
		} else if full := d.B * it.Scale; math.Abs(sz-full) > 0.5 && (it.Appear < 1 || it.springing) {
			drawIconSmooth(target, it, x, y, sz, math.Max(full, d.B), alpha)
		} else {
			drawIcon(target, it, x, y, sz, alpha)
		}
		if it == d.Pressed {
			drawPressed(target, it, x, y, sz)
		}
		if it.IsDir {
			drawXferBorder(d, target, it, x+sz/2, y+sz/2, sz, alpha)
		}
		grow(x, y, sz, sz)
		if it.Attention { // red dot on the icon's outer corner, ringed in the dock's colour
			r := math.Max(4*s, sz*0.11)
			bx, by := x+sz-r*0.9, y+r*0.9
			if !d.Horizontal() && d.Orient == OrientRight {
				bx = x + r*0.9
			}
			fillCircle(target, bx, by, r+1.5*s, scaleC(d.fillC(), alpha))
			fillCircle(target, bx, by, r, scaleC(pc(255, 69, 58, 1), alpha))
			grow(bx-r-2*s, by-r-2*s, 2*r+4*s, 2*r+4*s)
		}
		if (it.Running || it.DotT > 0.02) && !d.HideDots {
			cx, cy := d.toXY(it.U+du, d.PlateV0+d.P*0.62)
			if r := 1.9 * s * math.Max(it.Appear, 0.2) * math.Max(it.DotT, 0); r > 0.2 {
				fillCircle(target, cx, cy, r, scaleC(dotCol, alpha))
			}
		}
	}

	if d.Drag != nil && d.Drag.Kind == KindWidget {
		dirty = dirty.Union(rd.drawDraggedWidget(d, dst))
	} else if d.Drag != nil {
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
		top, bot = pc(52, 52, 58, 0.90), pc(34, 34, 40, 0.93)
	} else {
		if d.BlurOn {
			top, bot = pc(255, 255, 255, 0.34), pc(246, 246, 250, 0.22)
		} else {
			top, bot = pc(250, 250, 252, 0.88), pc(232, 232, 238, 0.86)
		}
	}
	fillRoundRect(dst, L, T, pw, ph, rad, &vGrad{y0: T, y1: T + ph, c0: top, c1: bot})
	// A narrow, soft highlight gives the acrylic plate a glassy reflection.
	fillRoundRect(dst, L+2*s, T+1*s, math.Max(0, pw-4*s), math.Max(1*s, ph*0.20), rad*0.72, image.NewUniform(d.ink(0.055)))
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
	lastGap := d.G
	var last *Item
	flush := func() {
		if last != nil {
			p := math.Min(1, pres)
			if split >= 0 { // the running-apps pill: its shape follows the split
				p = split
			}
			groups = append(groups, [3]float64{start, last.U + last.Slot/2 - lastGap/2 + d.P, p})
		}
		start, pres, last = -1, 0, nil
	}
	for _, it := range d.Items {
		if it.Kind == KindSep {
			if d.Connected {
				continue
			}
			flush()
			split = -1
			if it.InRun {
				split = it.Appear
			}
			continue
		}
		if start < 0 {
			start = it.U - it.Slot/2 + d.slotGap(it)/2 - d.P
		}
		a := it.Appear
		if it.Kind == KindGap {
			a = 0
		}
		pres += a // summed, so a page change (some icons shrinking, others growing) keeps the pill whole
		last = it
		lastGap = d.slotGap(it)
	}
	flush()
	return groups
}

// pillShape is one pill as drawn: its extent in dock space, corner radius and presence.
type pillShape struct{ u0, u1, v0, v1, r, p float64 }

func (rd *Renderer) drawPills(d *Dock, dst *image.RGBA, withFlyout bool) image.Rectangle {
	s := d.Scale
	fill := image.NewUniform(d.dockFillC())
	dirty := image.Rectangle{}
	surfaceAlpha := float64(100-max(0, min(d.Transparency, 100))) / 100
	rad := d.PlateH * 0.26
	if d.PillRound {
		rad = d.PlateH / 2
	}
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
			f = image.NewUniform(scaleC(d.dockFillC(), a))
		}
		fillRoundRect(dst, x, y, w, h, sh.r, f)
		strokeRoundRect(dst, x, y, w, h, sh.r, 1*s, d.ink(0.07*a*surfaceAlpha))
		dirty = dirty.Union(image.Rect(int(x)-4, int(y)-4, int(x+w)+5, int(y+h)+6))
	}
	if withFlyout && d.PopItem != nil && d.PopT > 0.01 {
		dirty = dirty.Union(rd.drawFlyout(d, dst))
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
	fc := d.dockFillC()
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

// drawFlyout draws the card above an icon, with a soft arrow pointing back to it.
// A plain hover is just a name tag.
func (rd *Renderer) drawFlyout(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	if d.CardStd {
		saved := d.Pal.Light
		d.Pal.Light = !d.Dark // its edge and marks follow the card, not the dock
		defer func() { d.Pal.Light = saved }()
	}
	cx, cy, cw, ch := d.rectUV(d.PopU0, d.PopV0, d.PopU1, d.PopV1)
	if cw < 2 || ch < 2 {
		return image.Rectangle{}
	}
	rad := math.Min((8+6*d.popCard)*s, math.Min(cw, ch)/2) // name tag 8, card 14
	fill := image.NewUniform(d.cardFillC())
	fillRoundRect(dst, cx, cy+1.5*s, cw, ch, rad, image.NewUniform(pc(0, 0, 0, 0.16)))
	fillRoundRect(dst, cx, cy, cw, ch, rad, fill)
	strokeRoundRect(dst, cx, cy, cw, ch, rad, 1*s, d.ink(0.07))
	out := image.Rect(int(cx)-3, int(cy)-3, int(cx+cw)+4, int(cy+ch)+5)
	if d.popCard > 0.02 { // cards keep a soft arrow back to their icon (tags don't)
		e := easeOut(d.PopT) * d.popCard
		th, tw := 12*s*e, 17*s
		if th > 1 {
			tu := math.Min(math.Max(d.PopItem.U, d.PopU0+rad+tw), d.PopU1-rad-tw)
			out = out.Union(softArrow(d, dst, tu, d.PopV0+1*s, d.PopV0-th, tw, fill))
		}
	}
	return out
}

// softArrow fills a soft pointer: a wide base, concave curved sides and a rounded tip
// (like macOS menus). base is the card edge, tip points back toward the dock.
func softArrow(d *Dock, dst *image.RGBA, tu, base, tip, tw float64, fill image.Image) image.Rectangle {
	bx, by, bw, bh := d.rectUV(tu-tw-2, math.Min(tip, base)-2, tu+tw+2, math.Max(tip, base)+2)
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
	tr := tw * 0.2 // how round the tip is
	z.MoveTo(p(tu-tw, base))
	a1, b1 := p(tu-tw*0.48, base)
	a2, b2 := p(tu-tr, tip)
	a3, b3 := p(tu, tip)
	z.CubeTo(a1, b1, a2, b2, a3, b3)
	c1, d1 := p(tu+tr, tip)
	c2, d2 := p(tu+tw*0.48, base)
	c3, d3 := p(tu+tw, base)
	z.CubeTo(c1, d1, c2, d2, c3, d3)
	z.ClosePath()
	z.Draw(dst, r, fill, r.Min)
	return r
}

// drawLaunchpadArrow points the Launchpad card back at the Start button.
func (rd *Renderer) drawLaunchpadArrow(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	base := d.PlateV1 + d.SearchGap + 1*s // just inside the card's edge
	th := (d.SearchGap - 3*s) * easeOut(d.SearchT)
	if th < 1*s {
		return image.Rectangle{}
	}
	c := pc(4, 4, 6, 1) // the card behind is opaque; match it exactly
	if d.Pal.Alpha > 0 {
		c = pc(d.Pal.Fill[0], d.Pal.Fill[1], d.Pal.Fill[2], 1)
	}
	return softArrow(d, dst, d.SearchItem.U, base, base-th, 17*s, image.NewUniform(c))
}

func (rd *Renderer) drawPopText(d *Dock, dst *image.RGBA) image.Rectangle {
	if d.CardStd {
		saved, savedInk := d.Pal.Light, weatherMonoInk
		d.Pal.Light = !d.Dark // the text follows the card's black or white
		weatherMonoInk = d.fg(1)
		defer func() { d.Pal.Light, weatherMonoInk = saved, savedInk }()
	}
	if d.PopItem == nil || d.PopT < 0.35 {
		return image.Rectangle{}
	}
	if d.Pop.Mode == PopMedia {
		return rd.drawMediaCard(d, dst)
	}
	if d.Pop.Mode == PopDrive {
		return rd.drawDriveCard(d, dst)
	}
	if d.Pop.Mode == PopWeather && d.Pop.Weather != nil {
		return rd.drawWeatherCard(d, dst)
	}
	s := d.Scale
	a := d.popTextA()
	tp, sp, rp, rowH, pad := d.popPx()
	x0, y0, x1 := d.PopX0, d.PopY0, d.PopX1
	w := x1 - x0
	if d.Pop.isTag() { // just the name, centred
		tw := rd.Text.Measure(d.Pop.Title, tp)
		th := d.PopY1 - y0
		rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Title, tp, w-2*10*s), tp, x0+math.Max(10*s, (w-tw)/2), y0+th/2+float64(tp)*0.36, d.fg(a))
		return image.Rect(int(x0), int(y0), int(x1)+1, int(d.PopY1)+1)
	}
	y := y0 + 12*s + float64(tp)
	dg := d.WidgetDG && d.PopItem != nil && d.PopItem.Widget != ""
	title, sub := d.Pop.Title, d.Pop.Subtitle
	if dg {
		title, sub = strings.ToUpper(title), strings.ToUpper(sub)
		fillCircle(dst, x1-pad+2*s, y0+12*s+float64(tp)*0.6, 2.6*s, scaleC(dgRed, a))
	}
	rd.Text.Draw(dst, truncateLabel(rd.Text, title, tp, w-2*pad), tp, x0+pad, y, d.fg(a))
	y += float64(tp)*0.3 + 2*s + float64(sp)
	rd.Text.Draw(dst, truncateLabel(rd.Text, sub, sp, w-2*pad), sp, x0+pad, y, d.dimC(a))
	if d.Pop.Mode == PopThumbs || d.Pop.Mode == PopGrid {
		rd.drawTiles(d, dst, a, rp)
		if d.Pop.Mode == PopGrid && d.Pop.ScrollRows > d.Pop.ScrollVis {
			rd.drawScrollBar(d, dst, a)
		}
	} else if d.Pop.Mode == PopTray {
		if d.Pop.ScrollRows > d.Pop.ScrollVis {
			defer rd.drawScrollBar(d, dst, a)
		}
		for i, r := range d.Pop.Rows {
			if i >= maxGrid {
				break
			}
			cy := (r.Y0 + r.Y1) / 2
			tx := r.X0 + pad*0.5 + 2*s
			if dg { // dotted bullets; the row you point at gets an accent bar and dot
				if i == d.PopHot {
					fillRoundRect(dst, r.X0, r.Y0+1*s, r.X1-r.X0, r.Y1-r.Y0-2*s, 7*s, image.NewUniform(scaleC(dgRed, 0.16*a)))
					fillRoundRect(dst, r.X0+2*s, cy-6*s, 3*s, 12*s, 1.5*s, image.NewUniform(scaleC(dgRed, a)))
				}
				bc := d.ink(0.35 * a)
				if i == d.PopHot {
					bc = scaleC(dgRed, a)
				}
				fillCircle(dst, tx+3*s, cy, 2*s, bc)
				tx += 12 * s
			} else if i == d.PopHot {
				fillRoundRect(dst, r.X0, r.Y0+1*s, r.X1-r.X0, r.Y1-r.Y0-2*s, 7*s, image.NewUniform(d.ink(0.12*a)))
			}
			rd.Text.Draw(dst, truncateLabel(rd.Text, r.Title, rp, r.X1-tx-6*s), rp, tx, cy+float64(rp)*0.36, d.fg2(a))
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
	dgRed = d.accentC(1)
	s := d.Scale
	w := d.B * d.widgetWidth(it) * it.Appear
	h := d.B * it.Appear
	// Keep the widget centred in its icon slot while it animates, matching the
	// vertical centring used for app icons as they grow and shrink.
	v0 := d.PlateV0 + d.P + (d.B-h)/2
	x, y, bw, bh := d.rectUV(it.U-w/2, v0, it.U+w/2, v0+h)
	cx, cy := x+bw/2, y+bh/2
	wtc, wtOn := d.widgetTint(it.Widget)
	matchDock := !wtOn
	if matchDock { // "Match dock": the dock colour, made lighter or darker by Light or dark
		f := d.Pal.Fill
		t, k := 255.0, 0.62
		if d.Dark {
			t, k = 0, 0.38
		}
		for i := range wtc {
			wtc[i] = uint8(math.Round(float64(f[i])*(1-k) + t*k))
		}
		wtOn = true
	}
	if wtOn {
		// a widget colour of its own: text and marks follow its brightness, not the dock's
		saved := d.Pal.Light
		// the text follows what it actually sits on: the tile, and the wallpaper through it
		ta := 0.95 * (1 - 0.8*math.Max(0, math.Min(1, float64(d.Transparency)/100)))
		under := wtc
		if d.HasWallTone {
			for i := range under {
				under[i] = uint8(float64(wtc[i])*ta + float64(d.WallTone[i])*(1-ta))
			}
		}
		d.Pal.Light = wantsDarkText(under)
		// (also for Match dock: the text follows the tile it sits on, so it always reads)
		defer func() { d.Pal.Light = saved }()
	}
	light := d.Pal.Light
	fg, dim := pc(245, 245, 248, 1), pc(170, 170, 178, 1)
	tile := pc(255, 255, 255, 0.07)
	if light {
		fg, dim, tile = pc(25, 25, 30, 1), pc(95, 95, 105, 1), pc(0, 0, 0, 0.06)
	}
	if d.CircleIcons && it.Widget == "media" {
		tile = d.ink(0.13)
	}
	if wtOn {
		// follows the Colour strength slider, like the dock (never fully clear: it's a card)
		c := wtc
		tile = pc(c[0], c[1], c[2], 0.95*(1-0.8*math.Max(0, math.Min(1, float64(d.Transparency)/100))))
	}
	if it.Widget != "desktop" || d.CircleIcons {
		bx, by, bwidth, bheight := x, y, bw, bh
		radius := math.Min(bw, bh) * 0.22
		inset := 0.0
		if !d.CircleIcons && (it.Widget == "status" || it.Widget == "battery") {
			maskSize := math.Min(bw, bh)
			bx, by = cx-maskSize/2, cy-maskSize/2
			bwidth, bheight = maskSize, maskSize
			radius = maskSize * 0.22
		}
		if d.CircleIcons && it.Widget == "media" && it.WWave > 0.02 {
			radius = (bh - 2*inset) / 2
		}
		if d.CircleIcons && !(it.Widget == "media" && it.WWave > 0.02) {
			diameter := math.Min(math.Min(bw, bh), d.B*it.Appear)
			bx, by, bwidth, bheight = cx-diameter/2, cy-diameter/2, diameter, diameter
			radius = diameter / 2
			inset = 0
		}
		if bwidth > 2*inset && bheight > 2*inset {
			fillRoundRect(dst, bx+inset, by+inset, bwidth-2*inset, bheight-2*inset, radius, image.NewUniform(tile))
		}
	}
	switch it.Widget {
	case "clock":
		if d.CircleIcons && !d.ClockDigital {
			rd.drawAnalogClock(d, dst, cx, y, bw, bh, fg, dim)
			break
		}
		if d.WidgetDG {
			rd.drawDGClock(d, dst, it, cx, cy, bw, bh, fg, dim)
			break
		}
		p1 := int(math.Round(d.B * 0.34))
		p2 := int(math.Round(d.B * 0.17))
		textW := bw - 8*s
		if d.CircleIcons {
			textW = d.B*it.Appear - 8*s
		}
		for p1 > 8 && rd.Text.Measure(it.WText1, p1) > textW {
			p1--
		}
		for p2 > 7 && rd.Text.Measure(it.WText2, p2) > textW-2*s {
			p2--
		}
		t1w := rd.Text.Measure(it.WText1, p1)
		t2w := rd.Text.Measure(it.WText2, p2)
		rd.Text.Draw(dst, it.WText1, p1, cx-t1w/2, cy+float64(p1)*0.18, fg)
		rd.Text.Draw(dst, it.WText2, p2, cx-t2w/2, cy+float64(p1)*0.18+float64(p2)*1.35, dim)
	case "status":
		if d.WidgetDG {
			rd.drawDGStatus(d, dst, it, x, y, bw, bh, fg)
			break
		}
		rd.drawStatus(d, dst, it, x, y, bw, bh, fg)
	case "weather":
		g := d.B * 0.5
		if it.WCode < 0 { // nothing yet: a faint cloud and a dash
			drawWeatherGlyph(dst, cx, cy-d.B*0.1, g, 3, false, light, 0.45)
		} else {
			drawWeatherGlyph(dst, cx, cy-d.B*0.1, g, it.WCode, it.WNight, light, 1)
		}
		p2 := int(math.Round(d.B * 0.22))
		t := it.WText1
		if t == "" {
			t = "–"
		}
		if d.WidgetDG && it.WText1 != "" {
			cell := fitDots(t, bw*0.62, d.B*0.26, d.B*0.052)
			tw := float64(dotCols(t)) * cell
			drawDots(dst, t, cx-tw/2+cell, cy+d.B*0.36-7*cell, cell, fg, false)
			break
		}
		tw := rd.Text.Measure(t, p2)
		rd.Text.Draw(dst, t, p2, cx-tw/2+float64(p2)*0.08, cy+d.B*0.3, fg)
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
			fc = d.accentC(1)
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
	case "usb":
		rd.drawUsbTile(d, dst, it, cx, cy, math.Min(bw, bh), fg)
	}
	return image.Rect(int(x)-2, int(y)-2, int(x+bw)+3, int(y+bh)+3)
}

// drawTray is the tray tile. A custom icon fills the tile's existing mask so it
// aligns with the widget bounds instead of appearing inset inside a second tile.
func (rd *Renderer) drawTray(d *Dock, dst *image.RGBA, it *Item, cx, cy, h float64, fg color.RGBA) {
	if d.WidgetDG {
		drawDGChevron(dst, cx, cy, h*0.62, d.Scale, fg)
		return
	}
	if it.Icon != nil {
		// Custom tray artwork shares the same visible footprint as Now Playing.
		// The outer widget tile stays full size, which keeps its hit target intact.
		size := math.Round(h * 0.84)
		drawIconSet(dst, it.Icon, cx-size/2, cy-size/2, size, it.Appear)
		return
	}
	s := d.Scale
	// an up arrow (the tray "show hidden icons" chevron), the same as Settings' preview
	g := math.Round(h * 0.56)
	drawChevronUpGlyph(dst, cx-g/2, cy-g/2, g, math.Max(1.6*s, g*0.09), fg)
}

// accentC is the Windows accent colour at alpha a (a soft blue if it isn't known).
func (d *Dock) accentC(a float64) color.RGBA {
	c := d.Accent
	if c.A == 0 {
		c = color.RGBA{95, 179, 255, 255}
	}
	return pc(c.R, c.G, c.B, a)
}

// drawUsbTile is a USB drive on the dock: a USB stick. While files arrive, the tile's
// border fills up in the Windows accent colour, and a tick shows when they're done.
func (rd *Renderer) drawUsbTile(d *Dock, dst *image.RGBA, it *Item, cx, cy, h float64, fg color.RGBA) {
	s := d.Scale
	g := math.Round(h * 0.64)
	drawXferBorder(d, dst, it, cx, cy, h, 1)
	if !it.XferBusy && !it.XferDoneAt.IsZero() && time.Since(it.XferDoneAt) < 1800*time.Millisecond {
		drawCheckGlyph(dst, cx-g*0.4, cy-g*0.4, g*0.8, math.Max(1.8*s, g*0.1), fg)
		return
	}
	drawUsbGlyph(dst, cx-g/2, cy-g/2, g, math.Max(1.5*s, g*0.085), fg)
}

// drawXferBorder draws transfer progress on the edge of an icon or tile (centre cx,
// cy, size sz): the border fills in the accent colour, a sweep goes round when the
// amount isn't known, and the full border fades out when it's done.
func drawXferBorder(d *Dock, dst *image.RGBA, it *Item, cx, cy, sz, alpha float64) {
	done := 0.0
	if !it.XferBusy && !it.XferDoneAt.IsZero() {
		if dt := time.Since(it.XferDoneAt).Seconds(); dt < 1.8 {
			done = math.Min(1, (1.8-dt)/0.6) // holds, then fades
		}
	}
	if !it.XferBusy && done == 0 {
		return
	}
	s := d.Scale
	t := math.Max(2.2*s, sz*0.05)
	acc := d.accentC(alpha)
	var u0, u1 float64
	switch {
	case done > 0:
		acc = d.accentC(alpha * done)
		u0, u1 = 0, 1
	case it.XferLevel >= 0:
		u0, u1 = 0, math.Max(0.02, math.Min(it.XferLevel, 1))
	default: // amount unknown: a sweep going round
		ph := math.Mod(float64(time.Now().UnixMilli())/1000*0.55, 1)
		u0, u1 = ph, ph+0.28
	}
	track := scaleC(d.ink(0.16), alpha)
	if d.CircleIcons {
		r := sz/2 - t/2
		if done == 0 {
			strokeArc(dst, cx, cy, r, 0, 2*math.Pi, t, track)
		}
		strokeArc(dst, cx, cy, r, -math.Pi/2+2*math.Pi*u0, -math.Pi/2+2*math.Pi*u1, t, acc)
		return
	}
	x0, y0, side := cx-sz/2+t/2, cy-sz/2+t/2, sz-t
	rad := math.Max(0, sz*0.225-t/2)
	if done == 0 {
		strokeRoundRect(dst, x0-t/2, y0-t/2, side+t, side+t, rad+t/2, t, track)
	}
	strokeRoundRectArc(dst, x0, y0, side, side, rad, t, u0, u1, acc)
}

// strokeRoundRectArc paints the part of a rounded square's border from u0 to u1
// (fractions of the way round, starting top centre, wrapping past 1), round-capped.
func strokeRoundRectArc(dst *image.RGBA, x, y, w, h, r, t, u0, u1 float64, col color.RGBA) {
	if u1-u0 >= 0.999 {
		strokeRoundRect(dst, x-t/2, y-t/2, w+t, h+t, r+t/2, t, col)
		return
	}
	if u1 > 1 {
		strokeRoundRectArc(dst, x, y, w, h, r, t, u0, 1, col)
		strokeRoundRectArc(dst, x, y, w, h, r, t, 0, u1-1, col)
		return
	}
	if u1 <= u0 || w <= t || h <= t || t <= 0 {
		return
	}
	outerX, outerY, outerW, outerH, outerR := x-t/2, y-t/2, w+t, h+t, r+t/2
	innerX, innerY, innerW, innerH, innerR := x+t/2, y+t/2, w-t, h-t, math.Max(0, r-t/2)
	rc := coverRect(dst, outerX, outerY, outerW, outerH)
	if rc.Empty() {
		return
	}
	perimeter := 2*(w+h-4*r) + 2*math.Pi*r
	n := int(math.Max(8, math.Ceil(perimeter*(u1-u0)/1.4)))
	z := vector.NewRasterizer(rc.Dx(), rc.Dy())
	z.DrawOp = draw.Over
	ox, oy := float64(rc.Min.X), float64(rc.Min.Y)
	for i := 0; i <= n; i++ {
		px, py := roundRectPointAt(outerX, outerY, outerW, outerH, outerR, u0+(u1-u0)*float64(i)/float64(n))
		if i == 0 {
			z.MoveTo(float32(px-ox), float32(py-oy))
		} else {
			z.LineTo(float32(px-ox), float32(py-oy))
		}
	}
	for i := n; i >= 0; i-- {
		px, py := roundRectPointAt(innerX, innerY, innerW, innerH, innerR, u0+(u1-u0)*float64(i)/float64(n))
		z.LineTo(float32(px-ox), float32(py-oy))
	}
	z.ClosePath()
	z.Draw(dst, rc, image.NewUniform(col), rc.Min)
	for _, u := range []float64{u0, u1} { // round ends
		px, py := roundRectPointAt(x, y, w, h, r, u)
		fillCircle(dst, px, py, t/2, col)
	}
}

// drawUsbGlyph draws a USB stick: the plug with its two contacts, and the body.
func drawUsbGlyph(dst *image.RGBA, x, y, size, w float64, col color.RGBA) {
	u := size / 16
	strokeRoundRect(dst, x+5.5*u, y+1.5*u, 5*u, 4.5*u, 0.8*u, w, col)            // plug
	fillRoundRect(dst, x+6.8*u, y+3*u, 1*u, 1.2*u, 0.2*u, image.NewUniform(col)) // contacts
	fillRoundRect(dst, x+8.2*u, y+3*u, 1*u, 1.2*u, 0.2*u, image.NewUniform(col))
	strokeRoundRect(dst, x+4*u, y+6*u, 8*u, 8.5*u, 1.8*u, w, col) // body
}

func drawCheckGlyph(dst *image.RGBA, x, y, size, w float64, col color.RGBA) {
	u := size / 16
	drawLine(dst, x+3*u, y+8.5*u, x+6.5*u, y+12*u, w, col)
	drawLine(dst, x+6.5*u, y+12*u, x+13*u, y+4.5*u, w, col)
}

// drawFolderGlyph, drawPhotoGlyph and drawEjectGlyph are the USB card's buttons.
func drawFolderGlyph(dst *image.RGBA, cx, cy, g, w float64, col color.RGBA) {
	x, y := cx-g, cy-g*0.8
	strokeRoundRect(dst, x, y+g*0.3, 2*g, g*1.3, g*0.2, w, col)
	strokeRoundRect(dst, x, y, g*0.9, g*0.5, g*0.15, w, col)
}

func drawPhotoGlyph(dst *image.RGBA, cx, cy, g, w float64, col color.RGBA) {
	strokeRoundRect(dst, cx-g, cy-g*0.8, 2*g, 1.6*g, g*0.25, w, col)
	drawLine(dst, cx-g*0.7, cy+g*0.5, cx-g*0.15, cy-g*0.1, w, col)
	drawLine(dst, cx-g*0.15, cy-g*0.1, cx+g*0.2, cy+g*0.25, w, col)
	drawLine(dst, cx+g*0.2, cy+g*0.25, cx+g*0.45, cy+g*0.05, w, col)
	drawLine(dst, cx+g*0.45, cy+g*0.05, cx+g*0.75, cy+g*0.4, w, col)
	fillCircle(dst, cx+g*0.45, cy-g*0.35, g*0.15, col)
}

func drawEjectGlyph(dst *image.RGBA, cx, cy, g, w float64, col color.RGBA) {
	drawLine(dst, cx-g*0.85, cy+g*0.15, cx, cy-g*0.75, w, col)
	drawLine(dst, cx, cy-g*0.75, cx+g*0.85, cy+g*0.15, w, col)
	drawLine(dst, cx-g*0.85, cy+g*0.15, cx+g*0.85, cy+g*0.15, w, col)
	fillRoundRect(dst, cx-g*0.85, cy+g*0.5, g*1.7, w*1.2, w*0.4, image.NewUniform(col))
}

// drawDriveCard is the USB drive card, laid out like the Now Playing card: a big
// tile on the left, the drive's name and details, a bar, and round buttons.
func (rd *Renderer) drawDriveCard(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	a := d.popTextA()
	mp, art, gap, _, bh := d.mediaMetrics()
	x0, y0 := d.PopX0, d.PopY0
	ax, ay := x0+mp, y0+mp
	fillRoundRect(dst, ax, ay, art, art, 10*s, image.NewUniform(d.ink(0.08*a)))
	acc := d.accentC(a)
	g := art * 0.5
	drawUsbGlyph(dst, ax+art/2-g/2, ay+art/2-g/2, g, 3*s, d.fg(0.55*a))
	if d.Pop.Playing { // copying (or just finished): the tile's border fills up
		bt := 4 * s
		strokeRoundRect(dst, ax, ay, art, art, 10*s, bt, d.ink(0.12*a))
		strokeRoundRectProgress(dst, ax+bt/2, ay+bt/2, art-bt, art-bt, 10*s-bt/2, bt, math.Min(d.Pop.Level, 1), acc)
	}
	tp, sp, _, _, _ := d.popPx()
	tx := ax + art + gap
	tw := d.PopX1 - mp - tx
	y := ay + float64(tp)
	rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Title, tp, tw), tp, tx, y, d.fg(a))
	y += float64(sp+1) * 1.45
	sub := d.Pop.Subtitle
	if d.WidgetDG {
		sub = strings.ToUpper(sub)
		fillCircle(dst, d.PopX1-mp+2*s, y0+mp*0.6, 2.6*s, scaleC(dgRed, a))
	}
	rd.Text.Draw(dst, truncateLabel(rd.Text, sub, sp+1, tw), sp+1, tx, y, d.fg2(a))
	if d.Pop.Line3 != "" {
		y += float64(sp) * 1.4
		rd.Text.Draw(dst, truncateLabel(rd.Text, d.Pop.Line3, sp, tw), sp, tx, y, d.dimC(a))
	}
	// bar: space used, or how far the copy has got
	by := ay + art - bh - 9*s
	bw := tw - 6*s
	fillRoundRect(dst, tx, by, bw, 4*s, 2*s, image.NewUniform(d.ink(0.14*a)))
	if lv := math.Max(0, math.Min(d.Pop.Level, 1)); lv > 0 {
		col := d.fg(0.75 * a)
		if d.Pop.Busy {
			col = acc
		}
		fillRoundRect(dst, tx, by, math.Max(4*s, bw*lv), 4*s, 2*s, image.NewUniform(col))
	}
	for i, r := range d.Pop.Rows {
		cx, cy := (r.X0+r.X1)/2, (r.Y0+r.Y1)/2
		if i == d.PopHot {
			fillRoundRect(dst, r.X0, r.Y0, r.X1-r.X0, r.Y1-r.Y0, 8*s, image.NewUniform(d.ink(0.12*a)))
		}
		g, w, col := bh*0.27, math.Max(1.6*s, 2*s), d.fg(a)
		switch {
		case strings.HasPrefix(r.Path, "usb:open"), strings.HasPrefix(r.Path, "usb:showdest"):
			drawFolderGlyph(dst, cx, cy, g, w, col)
		case strings.HasPrefix(r.Path, "usb:import"):
			drawPhotoGlyph(dst, cx, cy, g, w, col)
		case strings.HasPrefix(r.Path, "usb:eject"):
			drawEjectGlyph(dst, cx, cy, g, w, col)
		case strings.HasPrefix(r.Path, "usb:cancel"):
			drawX(dst, cx, cy, g*0.8, w, col)
		}
	}
	return image.Rect(int(d.PopX0)-2, int(d.PopY0)-2, int(d.PopX1)+3, int(d.PopY1)+3)
}

// The default widget glyphs are drawn on a 16-unit grid, matching the outline icons
// used in Settings (stroke width w in pixels).

// drawNoteGlyph draws a music note (two stems joined by a beam, two note heads).
func drawNoteGlyph(dst *image.RGBA, x, y, size, w float64, col color.RGBA) {
	u := size / 16
	p := func(a, b float64) (float64, float64) { return x + a*u, y + b*u }
	line := func(a0, b0, a1, b1 float64) {
		x0, y0 := p(a0, b0)
		x1, y1 := p(a1, b1)
		drawLine(dst, x0, y0, x1, y1, w, col)
	}
	line(6, 11.5, 6, 4)
	line(6, 4, 12, 2.5)
	line(12, 2.5, 12, 10)
	for _, c := range [][2]float64{{4.5, 11.5}, {10.5, 10}} {
		cx, cy := p(c[0], c[1])
		strokeArc(dst, cx, cy, 1.5*u, 0, 2*math.Pi, w, col)
	}
}

// drawChevronUpGlyph draws an upward chevron.
func drawChevronUpGlyph(dst *image.RGBA, x, y, size, w float64, col color.RGBA) {
	u := size / 16
	drawLine(dst, x+3.5*u, y+10*u, x+8*u, y+5.5*u, w, col)
	drawLine(dst, x+8*u, y+5.5*u, x+12.5*u, y+10*u, w, col)
}

// drawAnalogClock replaces the compact digital readout when circular tiles are selected.
func (rd *Renderer) drawAnalogClock(d *Dock, dst *image.RGBA, cx, y, bw, bh float64, fg, dim color.RGBA) {
	s := d.Scale
	h := math.Min(bw, bh)
	cy := y + bh*0.5
	r := h * 0.36
	strokeArc(dst, cx, cy, r, 0, 2*math.Pi, 1.35*s, scaleC(dim, 0.72))
	// Fine hour marks keep the dial legible at dock size without crowding it.
	for i := 0; i < 12; i++ {
		a := float64(i)*2*math.Pi/12 - math.Pi/2
		outer, inner := r*0.86, r*0.72
		thickness := 1.0 * s
		if i%3 == 0 {
			inner = r * 0.66
			thickness = 1.5 * s
		}
		drawLine(dst, cx+math.Cos(a)*inner, cy+math.Sin(a)*inner,
			cx+math.Cos(a)*outer, cy+math.Sin(a)*outer, thickness, fg)
	}
	now := time.Now()
	minute := float64(now.Minute()) + float64(now.Second())/60
	hour := float64(now.Hour()%12) + minute/60
	minuteA := minute*2*math.Pi/60 - math.Pi/2
	hourA := hour*2*math.Pi/12 - math.Pi/2
	drawLine(dst, cx, cy, cx+math.Cos(hourA)*r*0.46, cy+math.Sin(hourA)*r*0.46, 2.4*s, fg)
	drawLine(dst, cx, cy, cx+math.Cos(minuteA)*r*0.72, cy+math.Sin(minuteA)*r*0.72, 1.5*s, fg)
	fillCircle(dst, cx, cy, 1.8*s, fg)
}

// drawStatus is the battery indicator with the percentage centered inside.
// After scrolling on it, it shows screen brightness briefly, then fades back.
func (rd *Renderer) drawStatus(d *Dock, dst *image.RGBA, it *Item, x, y, bw, bh float64, fg color.RGBA) {
	s := d.Scale
	circle := d.CircleIcons
	h := math.Min(bw, bh)
	// Center both battery treatments inside the widget tile.
	cx, cy := x+bw/2, y+bh/2
	r := h * 0.26
	ringW := 3.4 * s
	// The rounded-square battery gauge should carry the same visual weight as
	// the Now Playing and System tray artwork inside their equally sized tiles.
	gaugeSide := h * 0.72
	gaugeX, gaugeY, gaugeR := cx-gaugeSide/2, cy-gaugeSide/2, gaugeSide*0.22
	if circle {
		cy = y + bh/2
		// The circular treatment uses more of its mask, matching the app icons.
		r = h * 0.33
		ringW = 2.8 * s
	}
	// Wi-Fi: a thin inner ring in four segments (lit = signal bars, all lit on a cable)
	gaugeSide = h * 0.78
	gaugeX, gaugeY, gaugeR = cx-gaugeSide/2, cy-gaugeSide/2, gaugeSide*0.22
	if circle {
		r = h * 0.35
	}
	innerW := math.Max(1.6*s, ringW*0.62)
	innerGap := 2.4 * s
	inset := ringW/2 + innerGap + innerW/2 // centre of the outer border → centre of the inner one
	var inSide, inR float64
	if circle {
		inR = r - inset
	} else {
		inSide = gaugeSide - ringW - 2*inset
		inR = math.Max(2*s, gaugeR-ringW/2-inset)
	}
	track := d.ink(0.28)
	if circle {
		track = d.ink(0.18)
	}
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
		if circle {
			px = int(math.Round(h * 0.29))
		}
		maxLabelW := (inR - innerW/2) * 1.62
		if !circle {
			maxLabelW = (inSide - innerW) * 0.86
		}
		for px > 7 && te.Measure(t, px) > maxLabelW {
			px--
		}
		tw := te.Measure(t, px)
		baseline := cy + float64(px)*0.36
		if circle {
			baseline = cy + float64(px)*0.34
		}
		te.Draw(dst, t, px, cx-tw/2, baseline, scaleC(fg, a))
	}
	if circle {
		strokeArc(dst, cx, cy, r, -math.Pi/2, math.Pi*1.5, ringW, track)
	} else {
		strokeRoundRect(dst, gaugeX, gaugeY, gaugeSide, gaugeSide, gaugeR, ringW, track)
	}
	if ba > 0 {
		lv := math.Max(0, math.Min(1, it.WLevel))
		col := fg
		switch {
		case it.WCharging:
			col = d.accentC(1)
		case lv <= 0.2:
			col = pc(255, 69, 58, 1)
		}
		if it.WHasBt && lv > 0 {
			if circle {
				strokeArc(dst, cx, cy, r, -math.Pi/2, -math.Pi/2+2*math.Pi*lv, ringW, scaleC(col, ba))
			} else {
				strokeRoundRectProgress(dst, gaugeX+ringW/2, gaugeY+ringW/2, gaugeSide-ringW, gaugeSide-ringW, gaugeR-ringW/2, ringW, lv, scaleC(col, ba))
			}
		}
		// the Wi-Fi segments, clockwise from the top
		lit := max(0, min(4, it.WWifi))
		for i := 0; i < 4; i++ {
			col := scaleC(track, ba)
			if i < lit {
				col = scaleC(fg, ba)
			}
			if circle {
				gapA := innerGap * 1.3 / inR
				a0 := -math.Pi/2 + float64(i)*math.Pi/2 + gapA/2
				strokeArc(dst, cx, cy, inR, a0, a0+math.Pi/2-gapA, innerW, col)
			} else {
				per := 2*(2*inSide-4*inR) + 2*math.Pi*inR
				gu := innerGap * 1.3 / per
				u0 := float64(i)*0.25 + gu/2
				strokeRoundRectSegment(dst, cx-inSide/2, cy-inSide/2, inSide, inSide, inR, innerW, u0, u0+0.25-gu, col)
			}
		}
		t := it.WText1
		if !it.WHasBt {
			t = "AC"
		}
		label(t, ba)
	}
	if bo > 0 {
		sun := scaleC(pc(255, 214, 10, 1), bo)
		bl := math.Max(0, math.Min(1, it.WBrShow))
		lbl := strconv.Itoa(int(math.Round(bl * 100)))
		if it.WBrDimShow > 0.004 { // dimmed below the backlight's lowest level: an indigo ring of what's left
			sun = scaleC(pc(94, 92, 230, 1), bo)
			bl = 1 - it.WBrDimShow
			lbl = "−" + strconv.Itoa(int(math.Round(it.WBrDimShow*100)))
		}
		if bl > 0 {
			if circle {
				strokeArc(dst, cx, cy, r, -math.Pi/2, -math.Pi/2+2*math.Pi*bl, ringW, sun)
			} else {
				strokeRoundRectProgress(dst, gaugeX+ringW/2, gaugeY+ringW/2, gaugeSide-ringW, gaugeSide-ringW, gaugeR-ringW/2, ringW, bl, sun)
			}
		}
		label(lbl, bo)
	}
}

// drawPlayer is the Now Playing tile: cover art plus a live sound wave.
// Bottom dock: art on the left, wave to its right. Side dock: art on top, wave below.
func (rd *Renderer) drawPlayer(d *Dock, dst *image.RGBA, it *Item, x, y, bw, bh float64, fg, dim color.RGBA, light bool) {
	s := d.Scale
	vertical := !d.Horizontal()
	ww := smooth01(it.WWave)
	var art, ax, ay float64
	if vertical {
		// cover centred when the wave is hidden, moving to the top as the wave grows in below it
		full := bw * 0.84
		if d.CircleIcons {
			full = math.Max(1, math.Min(bw, bh)*0.9)
		}
		art = math.Round(full)
		if !d.CircleIcons {
			art = math.Round(math.Min(full, full+(bh*0.55-full)*ww))
		}
		ax = x + (bw-art)/2
		ay = y + (bh-art)/2 + (bw*0.13-(bh-art)/2)*ww
	} else {
		// Match the visible artwork size used by the other compact widget tiles.
		art = math.Round(bh * 0.84)
		if d.CircleIcons {
			art = math.Max(1, math.Min(bw, bh)*0.9)
		}
		ax, ay = x+(bh-art)/2, y+(bh-art)/2
	}
	artRad := art * 0.22
	if d.CircleIcons {
		artRad = art / 2
	}
	if it.Art != nil {
		rd.drawArt(dst, it.Art, ax, ay, art, artRad, 1)
	} else if d.WidgetDG {
		drawDGNote(dst, ax+art/2, ay+art/2, art*0.58, s, fg)
	} else if it.DefaultArt != nil {
		rd.drawArt(dst, it.DefaultArt, ax, ay, art, artRad, 1)
	} else {
		// no album art: a quiet tile with a music note, the same as Settings' preview
		fillRoundRect(dst, ax, ay, art, art, artRad, image.NewUniform(d.ink(0.07)))
		drawNoteGlyph(dst, ax+art*0.2, ay+art*0.2, art*0.6, math.Max(1.4*s, art*0.06), fg)
	}
	if it.WHot >= 0 {
		fillRoundRect(dst, ax, ay, art, art, artRad, image.NewUniform(pc(0, 0, 0, 0.42)))
		g := 3
		if it.WPlaying {
			g = 4
		}
		drawMediaGlyph(dst, g, ax+art/2, ay+art/2, art*0.17, s, pc(255, 255, 255, 1))
	}
	// sound wave in the cover's colour
	wc := d.accentC(1) // the accent, like the icons and the rest of the widgets
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
		rd.drawVolume(dst, it, ax, ay, art, artRad, wx0, wx1, wcy, s, wc, fg, vo)
	}
	_, _, _ = fg, dim, light
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
	if rd.artSrc != art || rd.artSize != isz || rd.artRad != rad || rd.artImg == nil {
		src := art.Pick(float64(isz))
		tmp := image.NewRGBA(image.Rect(0, 0, isz, isz))
		xdraw.CatmullRom.Scale(tmp, tmp.Bounds(), src, src.Bounds(), xdraw.Src, nil)
		out := image.NewRGBA(tmp.Bounds())
		mask := squircleMask(isz, 0, 0, float64(isz), rad)
		draw.DrawMask(out, out.Bounds(), tmp, image.Point{}, mask, image.Point{}, draw.Src)
		rd.artImg, rd.artSrc, rd.artSize, rd.artRad = out, art, isz, rad
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
	a := d.popTextA()
	mp, art, gap, _, bh := d.mediaMetrics()
	x0, y0 := d.PopX0, d.PopY0
	ax, ay := x0+mp, y0+mp
	if d.Pop.Art != nil {
		rd.drawArt2(dst, d.Pop.Art, ax, ay, art, 10*s, a)
	} else if d.WidgetDG { // a dotted tile with the line note
		fillRoundRect(dst, ax, ay, art, art, 10*s, image.NewUniform(d.ink(0.05*a)))
		for gy := ay + 8*s; gy < ay+art-6*s; gy += 8 * s {
			for gx := ax + 8*s; gx < ax+art-6*s; gx += 8 * s {
				fillCircle(dst, gx, gy, 0.9*s, d.ink(0.16*a))
			}
		}
		drawDGNote(dst, ax+art/2, ay+art/2, art*0.5, s, d.fg(a))
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
		if d.WidgetDG && i == 1 { // play/pause: a solid accent button
			rr := math.Min(r.X1-r.X0, r.Y1-r.Y0) * 0.46
			fillCircle(dst, cx, cy, rr, scaleC(dgRed, a))
			drawMediaGlyph(dst, g, cx, cy, gs*0.82, s, scaleC(onColorC(dgRed), a))
			continue
		}
		if d.WidgetDG {
			strokeCircleDG(dst, cx, cy, math.Min(r.X1-r.X0, r.Y1-r.Y0)*0.4, 1.2*s, d.ink(0.25*a))
		}
		drawMediaGlyph(dst, g, cx, cy, gs, s, d.fg(a))
	}
	return image.Rect(int(d.PopX0)-2, int(d.PopY0)-2, int(d.PopX1)+3, int(d.PopY1)+3)
}

// drawArt2 is drawArt with its own cache slot (card art is a different size).
func (rd *Renderer) drawArt2(dst *image.RGBA, art *IconSet, x, y, size, rad, alpha float64) {
	saveI, saveS, saveZ, saveR := rd.artImg, rd.artSrc, rd.artSize, rd.artRad
	rd.artImg, rd.artSrc, rd.artSize, rd.artRad = rd.art2Img, rd.art2Src, rd.art2Size, rd.art2Rad
	rd.drawArt(dst, art, x, y, size, rad, alpha)
	rd.art2Img, rd.art2Src, rd.art2Size, rd.art2Rad = rd.artImg, rd.artSrc, rd.artSize, rd.artRad
	rd.artImg, rd.artSrc, rd.artSize, rd.artRad = saveI, saveS, saveZ, saveR
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
	case OrientTop:
		x, y = ax-w/2, ay
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

// CircleMaskIcon clips a styled icon tile to a clean circular silhouette.
func CircleMaskIcon(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	// MacTile leaves an icon-grid margin around its rounded square. Crop that
	// margin and scale the tile to the mask bounds so the circle is the visible
	// silhouette, rather than a small rounded square sitting inside a circle.
	mx, my := int(math.Round(float64(w)*0.095)), int(math.Round(float64(h)*0.095))
	crop := image.Rect(b.Min.X+mx, b.Min.Y+my, b.Max.X-mx, b.Max.Y-my)
	filled := image.NewRGBA(image.Rect(0, 0, w, h))
	// Leave a small transparent margin so circle-masked icons sit slightly
	// inside their dock slot instead of filling it edge to edge.
	inset := int(math.Round(float64(min(w, h)) * 0.04))
	target := image.Rect(inset, inset, w-inset, h-inset)
	xdraw.CatmullRom.Scale(filled, target, src, crop, xdraw.Src, nil)
	out := image.NewRGBA(filled.Bounds())
	cx, cy := float64(w)/2, float64(h)/2
	rad := math.Min(float64(target.Dx()), float64(target.Dy()))/2 - 0.5
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dist := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			coverage := math.Max(0, math.Min(1, rad+0.5-dist))
			if coverage == 0 {
				continue
			}
			si := filled.PixOffset(x, y)
			di := out.PixOffset(x, y)
			out.Pix[di] = uint8(float64(filled.Pix[si]) * coverage)
			out.Pix[di+1] = uint8(float64(filled.Pix[si+1]) * coverage)
			out.Pix[di+2] = uint8(float64(filled.Pix[si+2]) * coverage)
			out.Pix[di+3] = uint8(float64(filled.Pix[si+3]) * coverage)
		}
	}
	return out
}

// ScaleIconArtwork resizes icon artwork while preserving the styled icon's
// original alpha silhouette. When shrinking, the silhouette's edge colour is
// extended into the newly exposed area so the tile mask remains full-sized.
func ScaleIconArtwork(src *image.RGBA, scale float64) *image.RGBA {
	if src == nil {
		return nil
	}
	if scale < 0 {
		scale = 0
	} else if scale > 1.5 {
		scale = 1.5
	}
	if math.Abs(scale-1) < 0.0001 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return src
	}

	// Average the opaque boundary pixels to create a clean tile fill behind
	// reduced artwork. Un-premultiply before averaging partially covered edges.
	var sumR, sumG, sumB, count float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := src.PixOffset(b.Min.X+x, b.Min.Y+y)
			a := float64(src.Pix[i+3])
			if a < 224 {
				continue
			}
			edge := false
			for oy := -1; oy <= 1 && !edge; oy++ {
				for ox := -1; ox <= 1; ox++ {
					if ox == 0 && oy == 0 {
						continue
					}
					nx, ny := x+ox, y+oy
					if nx < 0 || nx >= w || ny < 0 || ny >= h || src.Pix[src.PixOffset(b.Min.X+nx, b.Min.Y+ny)+3] < 224 {
						edge = true
						break
					}
				}
			}
			if edge {
				weight := a / 255
				sumR += float64(src.Pix[i]) * 255 / a * weight
				sumG += float64(src.Pix[i+1]) * 255 / a * weight
				sumB += float64(src.Pix[i+2]) * 255 / a * weight
				count += weight
			}
		}
	}
	bgR, bgG, bgB := uint8(0), uint8(0), uint8(0)
	if count > 0 {
		bgR = uint8(math.Max(0, math.Min(255, math.Round(sumR/count))))
		bgG = uint8(math.Max(0, math.Min(255, math.Round(sumG/count))))
		bgB = uint8(math.Max(0, math.Min(255, math.Round(sumB/count))))
	} else {
		i := src.PixOffset(b.Min.X+w/2, b.Min.Y+h/2)
		if src.Pix[i+3] > 0 {
			bgR, bgG, bgB = src.Pix[i], src.Pix[i+1], src.Pix[i+2]
		}
	}

	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	dw, dh := float64(w)*scale, float64(h)*scale
	dst := image.Rect(int(math.Round((float64(w)-dw)/2)), int(math.Round((float64(h)-dh)/2)),
		int(math.Round((float64(w)+dw)/2)), int(math.Round((float64(h)+dh)/2)))
	if scale > 0 {
		xdraw.CatmullRom.Scale(scaled, dst, src, b, xdraw.Src, nil)
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mi := src.PixOffset(b.Min.X+x, b.Min.Y+y)
			maskA := uint32(src.Pix[mi+3])
			if maskA == 0 {
				continue
			}
			si := scaled.PixOffset(x, y)
			fgA := uint32(scaled.Pix[si+3])
			remain := maskA * (255 - fgA) / 255
			di := out.PixOffset(x, y)
			out.Pix[di] = uint8((uint32(scaled.Pix[si])*maskA/255 + uint32(bgR)*remain/255))
			out.Pix[di+1] = uint8((uint32(scaled.Pix[si+1])*maskA/255 + uint32(bgG)*remain/255))
			out.Pix[di+2] = uint8((uint32(scaled.Pix[si+2])*maskA/255 + uint32(bgB)*remain/255))
			out.Pix[di+3] = uint8(maskA)
		}
	}
	return out
}

// ScaleIconMask scales a complete icon, including its tile or circle mask,
// within the original canvas. The dock slot keeps its dimensions, so the
// icon can grow or shrink without changing dock or widget layout.
func ScaleIconMask(src *image.RGBA, scale float64) *image.RGBA {
	if src == nil {
		return nil
	}
	if scale < 0 {
		scale = 0
	} else if scale > 1.5 {
		scale = 1.5
	}
	if math.Abs(scale-1) < 0.0001 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return src
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	dw, dh := float64(w)*scale, float64(h)*scale
	dst := image.Rect(int(math.Round((float64(w)-dw)/2)), int(math.Round((float64(h)-dh)/2)),
		int(math.Round((float64(w)+dw)/2)), int(math.Round((float64(h)+dh)/2)))
	if scale > 0 {
		xdraw.CatmullRom.Scale(out, dst, src, b, xdraw.Src, nil)
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
func (rd *Renderer) drawVolume(dst *image.RGBA, it *Item, ax, ay, art, rad, x0, x1, cy, s float64, wc, fg color.RGBA, a float64) {
	fillRoundRect(dst, ax, ay, art, art, rad, image.NewUniform(pc(0, 0, 0, 0.55*a)))
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

// ---------- weather ----------

// fillShape fills whatever inside() says is in the shape, within (x0,y0)–(x1,y1),
// smoothing the edges by testing 4×4 points a pixel. For small glyphs only.
func fillShape(dst *image.RGBA, x0, y0, x1, y1 float64, col color.RGBA, inside func(x, y float64) bool) {
	b := dst.Bounds()
	ix0, iy0 := max(b.Min.X, int(math.Floor(x0))), max(b.Min.Y, int(math.Floor(y0)))
	ix1, iy1 := min(b.Max.X, int(math.Ceil(x1))), min(b.Max.Y, int(math.Ceil(y1)))
	for py := iy0; py < iy1; py++ {
		for px := ix0; px < ix1; px++ {
			n := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					if inside(float64(px)+(float64(sx)+0.5)/4, float64(py)+(float64(sy)+0.5)/4) {
						n++
					}
				}
			}
			if n == 0 {
				continue
			}
			a := float64(col.A) / 255 * float64(n) / 16
			i := dst.PixOffset(px, py)
			p := dst.Pix[i : i+4 : i+4]
			// col is premultiplied (pc does that): scale it by coverage, then "over"
			p[0] = uint8(float64(col.R)*float64(n)/16 + float64(p[0])*(1-a))
			p[1] = uint8(float64(col.G)*float64(n)/16 + float64(p[1])*(1-a))
			p[2] = uint8(float64(col.B)*float64(n)/16 + float64(p[2])*(1-a))
			p[3] = uint8(255*a + float64(p[3])*(1-a))
		}
	}
}

func inCircle(x, y, cx, cy, r float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r }

// inCloud: a cloud centred on (cx,cy), g wide.
func inCloud(x, y, cx, cy, g float64) bool {
	return inCircle(x, y, cx-0.2*g, cy+0.06*g, 0.19*g) ||
		inCircle(x, y, cx+0.03*g, cy-0.06*g, 0.25*g) ||
		inCircle(x, y, cx+0.25*g, cy+0.08*g, 0.17*g) ||
		(x >= cx-0.2*g && x <= cx+0.25*g && y >= cy+0.06*g && y <= cy+0.25*g)
}

func inPoly(x, y float64, pts [][2]float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		xi, yi, xj, yj := pts[i][0], pts[i][1], pts[j][0], pts[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

type weatherInk struct{ sun, moon, cloud, back, rain, snow, bolt color.RGBA }

// weatherMono: DG style draws weather in the dock's ink, with the accent for sun, rain and bolts.
var weatherMono bool
var weatherMonoInk color.RGBA

func weatherColours(light bool, a float64) weatherInk {
	if weatherMono {
		ink := scaleC(weatherMonoInk, a)
		acc := scaleC(dgRed, a)
		return weatherInk{acc, ink, ink, scaleC(weatherMonoInk, a*0.5), acc, ink, acc}
	}
	if light {
		return weatherInk{pc(250, 176, 20, a), pc(92, 98, 140, a), pc(112, 120, 138, a), pc(160, 166, 180, a),
			pc(40, 130, 230, a), pc(90, 140, 210, a), pc(240, 165, 0, a)}
	}
	return weatherInk{pc(255, 196, 46, a), pc(238, 230, 196, a), pc(236, 240, 247, a), pc(150, 156, 170, a),
		pc(90, 175, 255, a), pc(214, 232, 255, a), pc(255, 204, 40, a)}
}

func drawSun(dst *image.RGBA, cx, cy, g float64, col color.RGBA) {
	r := 0.17 * g
	fillCircle(dst, cx, cy, r, col)
	t := math.Max(1, 0.065*g)
	for k := 0; k < 8; k++ {
		an := float64(k) * math.Pi / 4
		c, sn := math.Cos(an), math.Sin(an)
		drawLine(dst, cx+c*r*1.5, cy+sn*r*1.5, cx+c*r*2.05, cy+sn*r*2.05, t, col)
	}
}

func drawMoon(dst *image.RGBA, cx, cy, g float64, col color.RGBA) {
	r := 0.27 * g
	ox, oy, r2 := cx+0.16*g, cy-0.1*g, 0.23*g
	fillShape(dst, cx-r, cy-r, cx+r, cy+r, col, func(x, y float64) bool {
		return inCircle(x, y, cx, cy, r) && !inCircle(x, y, ox, oy, r2)
	})
}

// drawWeatherGlyph draws the weather for a WMO code in a g-sized box centred on (cx,cy).
func drawWeatherGlyph(dst *image.RGBA, cx, cy, g float64, code int, night, light bool, a float64) {
	k := weatherColours(light, a)
	cloud := func(x, y, sz float64, col color.RGBA) {
		fillShape(dst, x-0.42*sz, y-0.33*sz, x+0.45*sz, y+0.27*sz, col, func(px, py float64) bool { return inCloud(px, py, x, y, sz) })
	}
	t := math.Max(1, 0.07*g)
	switch wmoKind(code) {
	case wkClear:
		if night {
			drawMoon(dst, cx, cy, g, k.moon)
		} else {
			drawSun(dst, cx, cy, g*1.05, k.sun)
		}
	case wkPartly:
		if night {
			drawMoon(dst, cx-0.14*g, cy-0.13*g, g*0.75, k.moon)
		} else {
			drawSun(dst, cx-0.14*g, cy-0.14*g, g*0.78, k.sun)
		}
		cloud(cx+0.06*g, cy+0.1*g, g*0.82, k.cloud)
	case wkCloudy:
		cloud(cx+0.12*g, cy-0.1*g, g*0.62, k.back)
		cloud(cx-0.03*g, cy+0.06*g, g*0.85, k.cloud)
	case wkFog:
		for i, w := range []float64{0.6, 0.72, 0.5} {
			y := cy + (float64(i)-1)*0.17*g
			drawLine(dst, cx-w*g/2, y, cx+w*g/2, y, t, k.cloud)
		}
	case wkDrizzle, wkRain:
		cloud(cx, cy-0.12*g, g*0.85, k.cloud)
		n, l := 3, 0.17*g
		if wmoKind(code) == wkDrizzle {
			n, l = 2, 0.1*g
		}
		for i := 0; i < n; i++ {
			x := cx + (float64(i)-float64(n-1)/2)*0.2*g
			y := cy + 0.22*g
			drawLine(dst, x+0.04*g, y, x-0.03*g, y+l, t, k.rain)
		}
	case wkSnow:
		cloud(cx, cy-0.12*g, g*0.85, k.cloud)
		for i := 0; i < 3; i++ {
			x := cx + (float64(i)-1)*0.2*g
			fillCircle(dst, x, cy+0.3*g+float64(i%2)*0.06*g, 0.05*g, k.snow)
		}
	case wkThunder:
		cloud(cx, cy-0.14*g, g*0.85, k.cloud)
		bolt := [][2]float64{{cx + 0.02*g, cy + 0.06*g}, {cx - 0.12*g, cy + 0.27*g}, {cx - 0.01*g, cy + 0.27*g},
			{cx - 0.07*g, cy + 0.46*g}, {cx + 0.13*g, cy + 0.2*g}, {cx + 0.02*g, cy + 0.2*g}, {cx + 0.1*g, cy + 0.06*g}}
		fillShape(dst, cx-0.15*g, cy+0.04*g, cx+0.16*g, cy+0.48*g, k.bolt, func(x, y float64) bool { return inPoly(x, y, bolt) })
	}
}

// drawWeatherCard is the weather card: now on top, the next days below.
func (rd *Renderer) drawWeatherCard(d *Dock, dst *image.RGBA) image.Rectangle {
	s := d.Scale
	a := d.popTextA()
	w := d.Pop.Weather
	_, sp, rp, _, _ := d.popPx()
	pad, glyph, head, col, row := d.weatherCardMetrics()
	x0, y0, x1 := d.PopX0, d.PopY0, d.PopX1
	light := d.Pal.Light
	top := y0 + 10*s
	drawWeatherGlyph(dst, x0+pad+glyph/2, top+head/2, glyph, w.Code, !w.IsDay, light, a)
	// big temperature and the condition, beside the glyph
	tx := x0 + pad + glyph + 12*s
	big := int(math.Round(30 * s))
	if d.WidgetDG { // the temperature in the dot-matrix font, the condition in small capitals
		t := tempText(w.Temp)
		cell := 3.6 * s
		drawDots(dst, t, tx, top+head/2-cell*7+float64(big)*0.12-2*s, cell, d.fg(a), false)
		cond := strings.ToUpper(wmoText(w.Code, w.IsDay))
		rd.Text.Draw(dst, truncateLabel(rd.Text, cond, sp-1, 120*s), sp-1, tx, top+head/2+float64(big)*0.12+float64(sp)*1.45, d.dimC(a))
		fillCircle(dst, x1-pad+4*s, y0+9*s, 2.6*s, scaleC(dgRed, a))
	} else {
		rd.Text.Draw(dst, tempText(w.Temp), big, tx, top+head/2+float64(big)*0.12, d.fg(a))
		rd.Text.Draw(dst, truncateLabel(rd.Text, wmoText(w.Code, w.IsDay), sp, 120*s), sp, tx, top+head/2+float64(big)*0.12+float64(sp)*1.45, d.dimC(a))
	}
	// the place and today's high and low, on the right
	place := w.Place
	if w.Approx && place != "" {
		place = "Near " + place
	}
	tp := int(math.Round(13 * s))
	place = truncateLabel(rd.Text, place, tp, (x1-x0)/2-pad)
	pw := rd.Text.Measure(place, tp)
	rd.Text.Draw(dst, place, tp, x1-pad-pw, top+head/2-2*s, d.fg(a))
	hl := fmt.Sprintf("H %s  L %s", tempText(w.Hi), tempText(w.Lo))
	hw := rd.Text.Measure(hl, sp)
	rd.Text.Draw(dst, hl, sp, x1-pad-hw, top+head/2-2*s+float64(sp)*1.5, d.dimC(a))
	if len(w.Days) == 0 {
		return image.Rect(int(x0)-2, int(y0)-2, int(x1)+3, int(d.PopY1)+3)
	}
	// the next days
	ry := top + head + 8*s
	fillRoundRect(dst, x0+pad, ry-4*s, x1-x0-2*pad, math.Max(1, 1*s), 0, image.NewUniform(d.ink(0.1*a)))
	n := len(w.Days)
	cw := math.Max(col, (x1-x0-2*pad)/float64(n))
	gx := x0 + (x1-x0-cw*float64(n))/2
	for i, day := range w.Days {
		cx := gx + cw*(float64(i)+0.5)
		nm := day.Name
		if d.WidgetDG {
			nm = strings.ToUpper(nm)
		}
		nw := rd.Text.Measure(nm, rp)
		y := ry + 6*s + float64(rp)
		c := d.dimC(a)
		if i == 0 {
			c = d.fg(a)
			if d.WidgetDG {
				c = scaleC(dgRed, a)
			}
		}
		rd.Text.Draw(dst, nm, rp, cx-nw/2, y, c)
		drawWeatherGlyph(dst, cx, y+8*s+13*s, 26*s, day.Code, false, light, a)
		hi, lo := tempText(day.Hi), tempText(day.Lo)
		hiW, loW := rd.Text.Measure(hi, rp), rd.Text.Measure(lo, rp)
		ty := y + 8*s + 26*s + 6*s + float64(rp)
		gap := 5 * s
		sx := cx - (hiW+gap+loW)/2
		rd.Text.Draw(dst, hi, rp, sx, ty, d.fg(a))
		rd.Text.Draw(dst, lo, rp, sx+hiW+gap, ty, d.dimC(a))
	}
	_ = row
	return image.Rect(int(x0)-2, int(y0)-2, int(x1)+3, int(d.PopY1)+3)
}

// drawDraggedWidget draws a widget being dragged: the widget itself (it has no icon
// image), on a solid card in the dock's colour so it doesn't turn see-through once it
// leaves the dock.
func (rd *Renderer) drawDraggedWidget(d *Dock, dst *image.RGBA) image.Rectangle {
	it := d.Drag
	s := d.Scale
	u, v := d.toUV(d.DragX, d.DragY)
	w := d.B * d.widgetWidth(it)
	pad := 4 * s
	x, y, bw, bh := d.rectUV(u-w/2-pad, v-d.B/2-pad, u+w/2+pad, v+d.B/2+pad)
	rad := math.Min(bw, bh) * 0.24
	alpha := 1.0
	if d.DragRemove {
		alpha = 0.6
	}
	fillRoundRect(dst, x, y+2*s, bw, bh, rad, image.NewUniform(pc(0, 0, 0, 0.22*alpha))) // soft shadow: it's lifted
	fc := d.fillC()
	fillRoundRect(dst, x, y, bw, bh, rad, image.NewUniform(scaleC(color.RGBA{fc.R, fc.G, fc.B, 255}, alpha)))
	strokeRoundRect(dst, x, y, bw, bh, rad, 1*s, d.ink(0.1*alpha))
	// draw it as if it sat in the dock right here
	savedU, savedV0, savedAppear := it.U, d.PlateV0, it.Appear
	it.U, it.Appear = u, 1
	d.PlateV0 = v - d.P - d.B/2
	r := rd.drawWidget(d, dst, it)
	it.U, d.PlateV0, it.Appear = savedU, savedV0, savedAppear
	r = r.Union(image.Rect(int(x)-2, int(y)-2, int(x+bw)+3, int(y+bh+2*s)+3))
	if d.DragRemove {
		r = r.Union(rd.labelAt(d, dst, "Remove", d.DragX, y-6*s, OrientBottom))
	}
	return r
}

// ---------- DG style widgets (Settings › Widgets › Widget style) ----------
// Dot-matrix numbers, thin line glyphs and one red accent, to match the Black and
// White Icons packs.

// dgRed is the DG style highlight: the accent colour (set as each frame is drawn)
var dgRed = color.RGBA{215, 25, 33, 255}

// dotFont is a 5×7 dot-matrix font: each row is 5 bits, left to right.
var dotFont = map[rune][7]uint8{
	'0': {0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110},
	'1': {0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'2': {0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111},
	'3': {0b11110, 0b00001, 0b00001, 0b01110, 0b00001, 0b00001, 0b11110},
	'4': {0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010},
	'5': {0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110},
	'6': {0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110},
	'7': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000},
	'8': {0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110},
	'9': {0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100},
	'-': {0, 0, 0, 0b01110, 0, 0, 0},
	'−': {0, 0, 0, 0b01110, 0, 0, 0},
	'A': {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'C': {0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110},
	'M': {0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001},
	'P': {0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000},
}

// dotCols is how many dot columns a string takes (characters 5 wide, ':' and '°' 1–2).
func dotCols(s string) int {
	n := 0
	for i, r := range []rune(s) {
		if i > 0 {
			n++ // gap
		}
		switch r {
		case ':', '.':
			n++
		case '°':
			n += 2
		case ' ':
			n += 2
		default:
			n += 5
		}
	}
	return n
}

// drawDots draws s in the dot-matrix font with its left edge at x and its top at y;
// cell is the dot pitch. accentColon draws ':' in red.
func drawDots(dst *image.RGBA, s string, x, y, cell float64, col color.RGBA, accentColon bool) {
	r := cell * 0.44
	cx := x
	for i, ch := range []rune(s) {
		if i > 0 {
			cx += cell
		}
		switch ch {
		case ':':
			c := col
			if accentColon {
				c = scaleC(dgRed, float64(col.A)/255)
			}
			fillCircle(dst, cx+cell/2, y+cell*2.5, r, c)
			fillCircle(dst, cx+cell/2, y+cell*4.5, r, c)
			cx += cell
		case '.':
			fillCircle(dst, cx+cell/2, y+cell*6.5, r, col)
			cx += cell
		case '°':
			fillCircle(dst, cx+cell, y+cell, r*1.05, col)
			cx += 2 * cell
		case ' ':
			cx += 2 * cell
		default:
			g, ok := dotFont[ch]
			if ok {
				for row := 0; row < 7; row++ {
					for c := 0; c < 5; c++ {
						if g[row]&(1<<(4-c)) != 0 {
							fillCircle(dst, cx+cell*(float64(c)+0.5), y+cell*(float64(row)+0.5), r, col)
						}
					}
				}
			}
			cx += 5 * cell
		}
	}
}

// fitDots is the dot pitch for s to fit in w×h, no bigger than maxCell.
func fitDots(s string, w, h, maxCell float64) float64 {
	c := math.Min(w/float64(max(1, dotCols(s))), h/7)
	return math.Min(c, maxCell)
}

// pointOnRoundRect is the point a fraction u (0–1) of the way round a rounded
// rectangle, clockwise from the middle of its top edge.
func pointOnRoundRect(x, y, w, h, r, u float64) (float64, float64) {
	r = math.Min(r, math.Min(w, h)/2)
	sw, sh := w-2*r, h-2*r
	arc := math.Pi / 2 * r
	per := 2*sw + 2*sh + 4*arc
	t := math.Mod(u, 1) * per
	segs := []float64{sw / 2, arc, sh, arc, sw, arc, sh, arc, sw / 2}
	for i, l := range segs {
		if t <= l || i == len(segs)-1 {
			f := 0.0
			if l > 0 {
				f = t / l
			}
			switch i {
			case 0:
				return x + w/2 + f*sw/2, y
			case 1:
				a := -math.Pi/2 + f*math.Pi/2
				return x + w - r + math.Cos(a)*r, y + r + math.Sin(a)*r
			case 2:
				return x + w, y + r + f*sh
			case 3:
				a := f * math.Pi / 2
				return x + w - r + math.Cos(a)*r, y + h - r + math.Sin(a)*r
			case 4:
				return x + w - r - f*sw, y + h
			case 5:
				a := math.Pi/2 + f*math.Pi/2
				return x + r + math.Cos(a)*r, y + h - r + math.Sin(a)*r
			case 6:
				return x, y + h - r - f*sh
			case 7:
				a := math.Pi + f*math.Pi/2
				return x + r + math.Cos(a)*r, y + r + math.Sin(a)*r
			default:
				return x + r + f*sw/2, y
			}
		}
		t -= l
	}
	return x + w/2, y
}

// drawDGClock: the time in dot-matrix with a red colon, the date small below.
func (rd *Renderer) drawDGClock(d *Dock, dst *image.RGBA, it *Item, cx, cy, bw, bh float64, fg, dim color.RGBA) {
	s := d.Scale
	t := it.WText1
	if bw/bh < 1.3 { // a square slot (side dock): hours over minutes
		hh, mm, _ := strings.Cut(t, ":")
		if len(hh) == 1 {
			hh = "0" + hh
		}
		cell := math.Min((bw-14*s)/11.8, (bh-14*s)/15.2)
		gap := cell * 1.2
		top := cy - (14*cell+gap)/2
		drawDots(dst, hh, cx-11*cell/2, top, cell, fg, false)
		drawDots(dst, mm, cx-11*cell/2, top+7*cell+gap, cell, fg, false)
		fillCircle(dst, cx+11*cell/2+cell*0.9, top+7*cell+gap/2, cell*0.42, dgRed)
		return
	}
	p2 := int(math.Round(d.B * 0.15))
	date := strings.ToUpper(it.WText2)
	cell := fitDots(t, bw-10*s, bh*0.5, d.B*0.075)
	tw, th := float64(dotCols(t))*cell, 7*cell
	gap := 3 * s
	total := th + gap + float64(p2)
	top := cy - total/2
	drawDots(dst, t, cx-tw/2, top, cell, fg, true)
	for p2 > 6 && rd.Text.Measure(date, p2) > bw-6*s {
		p2--
	}
	dw := rd.Text.Measure(date, p2)
	rd.Text.Draw(dst, date, p2, cx-dw/2, top+th+gap+float64(p2)*0.82, dim)
}

// drawDGStatus: battery as a ring of dots, the charge in dot-matrix, Wi-Fi as four dots.
func (rd *Renderer) drawDGStatus(d *Dock, dst *image.RGBA, it *Item, x, y, bw, bh float64, fg color.RGBA) {
	s := d.Scale
	h := math.Min(bw, bh)
	cx, cy := x+bw/2, y+bh/2
	side := h * 0.8
	rx, ry, rr := cx-side/2, cy-side/2, side*0.24
	circle := d.CircleIcons
	track := d.ink(0.2)
	const n = 24
	dotR := math.Max(1.1*s, h*0.027)
	ring := func(level float64, lit color.RGBA, a float64) {
		on := int(math.Round(level * n))
		for i := 0; i < n; i++ {
			u := (float64(i) + 0.5) / n
			var px, py float64
			if circle {
				ang := -math.Pi/2 + u*2*math.Pi
				px, py = cx+math.Cos(ang)*side/2, cy+math.Sin(ang)*side/2
			} else {
				px, py = pointOnRoundRect(rx, ry, side, side, rr, u)
			}
			c := track
			if i < on {
				c = lit
			}
			fillCircle(dst, px, py, dotR, scaleC(c, a))
		}
	}
	number := func(t string, col color.RGBA, a float64) {
		cell := fitDots(t, side*0.66, side*0.36, side*0.075)
		tw := float64(dotCols(t)) * cell
		drawDots(dst, t, cx-tw/2, cy-7*cell/2-side*0.06, cell, scaleC(col, a), false)
	}
	bo := 0.0 // brightness overlay
	if !it.WBrAt.IsZero() {
		if t := time.Since(it.WBrAt).Seconds(); t < 1.4 {
			bo = math.Min(1, (1.4-t)/0.4)
		}
	}
	if ba := 1 - bo; ba > 0 {
		lv := math.Max(0, math.Min(1, it.WLevel))
		lit, numC := fg, fg
		switch {
		case it.WCharging:
			lit = dgRed
		case lv <= 0.2:
			numC = dgRed
		}
		if it.WHasBt {
			ring(lv, lit, ba)
			number(strings.TrimSuffix(it.WText1, "%"), numC, ba)
		} else {
			ring(1, fg, ba*0.55)
			// plugged in: a plug
			g := side * 0.3
			w := math.Max(1.6*s, g*0.12)
			c := scaleC(fg, ba)
			strokeRoundRect(dst, cx-g*0.42, cy-g*0.45, g*0.84, g*0.55, g*0.14, w, c)
			drawLine(dst, cx-g*0.2, cy-g*0.45, cx-g*0.2, cy-g*0.72, w, c)
			drawLine(dst, cx+g*0.2, cy-g*0.45, cx+g*0.2, cy-g*0.72, w, c)
			drawLine(dst, cx, cy+g*0.1, cx, cy+g*0.32, w, scaleC(dgRed, ba))
		}
		// Wi-Fi: four dots below the number
		wl := max(0, min(4, it.WWifi))
		step := side * 0.1
		for i := 0; i < 4; i++ {
			c := track
			if i < wl {
				c = fg
			}
			if wl == 0 && i == 0 {
				c = dgRed // offline
			}
			fillCircle(dst, cx+(float64(i)-1.5)*step, cy+side*0.25, dotR*0.9, scaleC(c, ba))
		}
	}
	if bo > 0 {
		bl := math.Max(0, math.Min(1, it.WBrShow))
		lbl := strconv.Itoa(int(math.Round(bl * 100)))
		col := fg
		if it.WBrDimShow > 0.004 { // dimmed below the backlight's lowest: red, with what's left
			bl, col = 1-it.WBrDimShow, dgRed
			lbl = "−" + strconv.Itoa(int(math.Round(it.WBrDimShow*100)))
		}
		ring(bl, col, bo)
		number(lbl, col, bo)
	}
}

// drawDGNote: Now Playing with nothing playing, a line note with a red dot.
func drawDGNote(dst *image.RGBA, cx, cy, g, s float64, fg color.RGBA) {
	w := math.Max(1.5*s, g*0.07)
	drawLine(dst, cx-g*0.12, cy+g*0.28, cx-g*0.12, cy-g*0.3, w, fg)
	drawLine(dst, cx-g*0.12, cy-g*0.3, cx+g*0.26, cy-g*0.38, w, fg)
	drawLine(dst, cx+g*0.26, cy-g*0.38, cx+g*0.26, cy+g*0.18, w, fg)
	strokeArc(dst, cx-g*0.24, cy+g*0.28, g*0.12, 0, 2*math.Pi, w, fg)
	fillCircle(dst, cx+g*0.14, cy+g*0.18, g*0.12, dgRed)
}

// drawDGChevron: the System tray, a thin chevron with a red dot.
func drawDGChevron(dst *image.RGBA, cx, cy, g, s float64, fg color.RGBA) {
	w := math.Max(1.6*s, g*0.085)
	drawLine(dst, cx-g*0.3, cy+g*0.08, cx, cy-g*0.22, w, fg)
	drawLine(dst, cx, cy-g*0.22, cx+g*0.3, cy+g*0.08, w, fg)
	fillCircle(dst, cx, cy+g*0.3, g*0.075, dgRed)
}

// strokeCircleDG: a thin ring (DG style's outlined buttons).
func strokeCircleDG(dst *image.RGBA, cx, cy, r, w float64, col color.RGBA) {
	strokeArc(dst, cx, cy, r, 0, 2*math.Pi, w, col)
}

// onColorC: black or white, whichever reads on c.
func onColorC(c color.RGBA) color.RGBA {
	r, g, b := float64(c.R), float64(c.G), float64(c.B)
	if 0.299*r+0.587*g+0.114*b > 150 {
		return color.RGBA{0, 0, 0, 255}
	}
	return color.RGBA{255, 255, 255, 255}
}
