package main

import (
	"image"
	"image/color"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"
)

// ---------- configuration ----------

type Pin struct {
	Kind   string `json:"kind"`           // "app" (AppsFolder id), "file" (path), "start", "trash"
	Target string `json:"target"`         // AppsFolder id or file system path
	Name   string `json:"name,omitempty"` // optional display name override
	Icon   string `json:"icon,omitempty"` // optional custom icon file (.png/.ico/.exe/.dll)
}

type Config struct {
	IconSize       int     `json:"iconSize"`
	Magnification  float64 `json:"magnification"`
	AutoHide       bool    `json:"autoHide"`
	HideTaskbar    bool    `json:"hideTaskbar"`
	ReserveSpace   bool    `json:"reserveSpace"`
	Blur           bool    `json:"blur"`
	Theme          string  `json:"theme"`     // auto, light, dark
	IconStyle      string  `json:"iconStyle"` // tiles, original
	ShowRunning    bool    `json:"showRunning"`
	HideFullscreen bool    `json:"hideFullscreen"`
	Position       string  `json:"position"` // bottom, left, right
	Style          string  `json:"style"`    // pill, notch, glass
	Pins           []Pin   `json:"pins"`
	Initialized    bool    `json:"initialized"`
	MagOffApplied  bool    `json:"magOffApplied"`
	PillApplied    bool    `json:"pillApplied"`
	NoBounce       bool    `json:"noBounce"`
	IconPack       string  `json:"iconPack"`
	NoPreviews     bool    `json:"noPreviews"`
	NoWidgets      bool    `json:"noWidgets"`
	NoTray         bool    `json:"noTray"`
	AnimSpeed      int     `json:"animSpeed,omitempty"` // 1 (slowest) to 5 (normal); 0 = 5
	// custom icons for things that aren't pins: "tray:<app.exe>" and "file:<path>" → icon file
	IconOverrides  map[string]string `json:"iconOverrides,omitempty"`
	MainDisplay    bool              `json:"mainDisplayOnly"`
	HideIndicators bool              `json:"hideIndicators"`
	UpdatePrompted int               `json:"updatePrompted"`  // newest version we've already asked about
	HideAlways     bool              `json:"hideAlways"`      // classic auto-hide (off = smart hide)
	StartIsStart   bool              `json:"startOpensStart"` // Start icon opens Windows Start instead of Search
	SearchHits     map[string]int    `json:"searchHits,omitempty"`
	AppsPerPage    int               `json:"appsPerPage"` // 0 = default (10), -1 = all on one page
	// taskbar auto-hide state before we touched it (-1 = untouched)
	PrevTaskbarAutoHide int `json:"prevTaskbarAutoHide"`
}

func defaultConfig() *Config {
	return &Config{
		IconSize:            54,
		Magnification:       1.0,
		ReserveSpace:        true,
		Blur:                true,
		Theme:               "auto",
		IconStyle:           "tiles",
		ShowRunning:         true,
		HideFullscreen:      true,
		Position:            "bottom",
		Style:               "pill",
		PrevTaskbarAutoHide: -1,
	}
}

// ---------- icons ----------

// IconSet keeps a few pre-scaled copies so per-frame scaling stays sharp and fast.
type IconSet struct {
	levels []*image.RGBA // largest first
}

func NewIconSet(src *image.RGBA) *IconSet {
	if src == nil {
		return nil
	}
	is := &IconSet{levels: []*image.RGBA{src}}
	cur := src
	for cur.Bounds().Dx() > 48 {
		w := cur.Bounds().Dx() / 2
		h := cur.Bounds().Dy() / 2
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), cur, cur.Bounds(), xdraw.Src, nil)
		is.levels = append(is.levels, dst)
		cur = dst
	}
	return is
}

// Pick returns the smallest level that is still >= size pixels.
func (is *IconSet) Pick(size float64) *image.RGBA {
	best := is.levels[0]
	for _, l := range is.levels {
		if float64(l.Bounds().Dx()) >= size*0.98 {
			best = l
		}
	}
	return best
}

// ---------- dock items ----------

const (
	KindApp     = "app"
	KindFile    = "file"
	KindStart   = "start"
	KindTrash   = "trash"
	KindSep     = "sep"
	KindGap     = "gap"     // invisible filler so every page of apps is the same width
	KindRunning = "running" // running but not pinned
	KindWidget  = "widget"  // clock, battery, media controls
)

type Item struct {
	Key     string
	Kind    string
	Pin     *Pin // nil for running-only and separator
	PinIdx  int
	Name    string
	Target  string // path or AppsFolder id
	ExePath string // lower-case normalised exe used for matching windows
	AUMID   string // app user model id used for matching windows
	IsDir   bool
	Section int // 0 = apps, 1 = folders/files/trash (right of separator)
	Icon    *IconSet
	Raw     *image.RGBA // unstyled icon
	RawKey  string

	Running bool
	Leaving bool // app just closed: shrink out, then drop
	Windows []uintptr
	cycle   int // scroll-to-cycle position

	// widgets
	Widget            string // "clock", "battery", "media"
	WidthB            float64
	WText1, WText2    string
	WLevel            float64
	WCharging, WHasBt bool
	WWifi             int      // Wi-Fi strength dots lit (0..4)
	WHot              int      // hovered media button
	Art               *IconSet // now playing cover
	WPlaying          bool
	WLevels           [12]float64 // sound-wave bars, 0..1
	Tray              []*IconSet  // system tray icons (tray widget)
	WWave             float64     // 0 = wave hidden (tile is just the cover), 1 = wave shown while music plays
	WAccent           [3]uint8    // colour picked from the cover art
	WVol              float64     // volume shown while scrolling on the player (0..1, -1 = muted)
	WVolAt            time.Time
	WBr               float64 // brightness shown while scrolling on the battery ring (0..1)
	WBrAt             time.Time
	Paged             bool   // on the current page of apps
	Browse            string // folder shown by this item's card (pinned folder, or a shortcut's target)
	PageOut           bool   // on the page being left: shrinking away
	InRun             bool   // in the running-apps pill (or its separator)

	// animation state
	Scale       float64
	Appear      float64
	Bouncing    bool
	BounceStart time.Time

	// cached icon at the current pixel size
	scaled     *image.RGBA
	scaledSize int
	scaledSrc  *IconSet

	// layout output: U = centre along the dock, Size = icon size, Lift = bounce
	U, Size, Slot, Lift float64
}

func (it *Item) draggable() bool {
	return it.Pin != nil && it.Kind != KindTrash
}

// ---------- dock model ----------
//
// Layout is computed in dock space: u runs along the dock, v is the distance
// from the screen edge (growing towards the middle of the screen). toXY maps
// dock space onto the layered window for the chosen screen edge.

const (
	OrientBottom = 0
	OrientLeft   = 1
	OrientRight  = 2
)

type PopRow struct {
	Title          string
	Hwnd           uintptr
	Path           string
	Icon           *IconSet
	Min            bool    // minimised window (no live preview)
	Small          bool    // small icon (tray) drawn on a tile
	X0, Y0, X1, Y1 float64 // window-local hit box
	TX0, TY0       float64 // preview / icon area
	TX1, TY1       float64
	CX0, CY0       float64 // close button (previews)
	CX1, CY1       float64
}

const (
	PopList   = 0 // text rows
	PopThumbs = 1 // live window previews
	PopGrid   = 2 // folder stack
	PopMedia  = 3 // now playing
)

type PopContent struct {
	Title    string
	Subtitle string
	Line3    string
	Mode     int
	Rows     []PopRow
	Art      *IconSet
	Playing  bool
	// folder cards: rows of tiles in the folder, how many fit, and the first one shown
	ScrollRows, ScrollVis, ScrollTop int
}

type Poof struct {
	X, Y  float64
	Start time.Time
}

type Dock struct {
	Items []*Item

	// metrics in physical px
	Scale  float64 // dpi/96
	B      float64 // base icon size
	G      float64 // gap between icons
	P      float64 // plate padding
	M      float64 // margin from screen edge
	Mag    float64
	PlateH float64

	Orient int
	Notch  bool // dark flyout styles (notch and pill)
	Pill   bool // split floating pills
	Dark   bool
	BlurOn bool
	Tiles  bool

	W, H    int // layered window size
	CenterU float64

	HideOffset, HideTarget float64

	MouseX, MouseY float64 // window-local
	MouseValid     bool
	mu, mv         float64
	Active         bool
	Frozen         bool // keep current state (menu open)
	Hover          int

	Drag       *Item
	DragX      float64
	DragY      float64
	DragRemove bool

	Pressed  *Item
	Poofs    []Poof
	HideDots bool
	Pal      Palette
	// pages of apps: PageN pages, showing PageCur; PageF follows it for the indicator
	PageN   int
	PageCur int
	PageF   float64
	// page slide: the old page's icons (Ghosts) slide out while the new ones slide in
	PageT0 time.Time
	// search card: while it's open the dock draws its tail above the Start icon
	// the running-apps pill budding off (In) or melting back: one slow, eased timeline
	// that the pill's shape, its separator and its icons all follow
	BudStart   time.Time
	BudIn      bool
	SearchItem *Item
	SearchGap  float64 // gap between the pill and the card (the tail spans it)
	PageDir    int     // +1 = went forward, -1 = went back
	Ghosts     []Ghost

	PlateU0, PlateU1, PlateV0, PlateV1 float64

	// notch flyout
	PopFn         func(*Item) PopContent
	Measure       func(s string, px int) float64
	PopItem       *Item
	PopT          float64
	PopTarget     float64
	PopCU         float64
	Pop           PopContent
	PopInside     bool
	PopHot        int
	PopU0, PopU1  float64 // current box in dock space
	PopV0, PopV1  float64
	PopX0, PopY0  float64 // final box in window space (for text)
	PopX1, PopY1  float64
	popCand       *Item
	popCandSince  time.Time
	popLeaveSince time.Time

	lastStep time.Time
}

func (d *Dock) SetMetrics(scale float64, iconSize int, mag float64, tiles, notch, pill bool) {
	d.Pill = pill && notch
	d.Scale = scale
	d.Tiles = tiles
	d.Notch = notch
	d.B = math.Round(float64(iconSize) * scale)
	if tiles {
		// tile artwork carries its own margin (like macOS icons), so slots sit tight
		d.G = math.Round(1 * scale)
		d.P = math.Round(d.B*0.07 + 2*scale)
	} else {
		d.G = math.Round(5 * scale)
		d.P = math.Round(d.B*0.13 + 3*scale)
	}
	if notch {
		d.P = math.Round(d.B*0.10 + 4*scale)
		d.M = 0
		if d.Pill {
			d.M = math.Round(4 * scale)
			if d.PageN > 1 {
				d.M = math.Round(9 * scale) // just enough room for the page dots under the pills
			}
		}
	} else {
		d.M = math.Round(5 * scale)
	}
	d.Mag = mag
	d.PlateH = d.B + 2*d.P
}

// Length along the dock and thickness across it, in window pixels.
func (d *Dock) L() float64 {
	if d.Orient == OrientBottom {
		return float64(d.W)
	}
	return float64(d.H)
}

func (d *Dock) toXY(u, v float64) (float64, float64) {
	switch d.Orient {
	case OrientLeft:
		return v, u
	case OrientRight:
		return float64(d.W) - v, u
	default:
		return u, float64(d.H) - v
	}
}

func (d *Dock) toUV(x, y float64) (float64, float64) {
	switch d.Orient {
	case OrientLeft:
		return y, x
	case OrientRight:
		return y, float64(d.W) - x
	default:
		return x, float64(d.H) - y
	}
}

// rectUV maps a dock-space rectangle to a window-space x,y,w,h.
func (d *Dock) rectUV(u0, v0, u1, v1 float64) (float64, float64, float64, float64) {
	x0, y0 := d.toXY(u0, v0)
	x1, y1 := d.toXY(u1, v1)
	return math.Min(x0, x1), math.Min(y0, y1), math.Abs(x1 - x0), math.Abs(y1 - y0)
}

// Popover text metrics
func (d *Dock) popPx() (title, sub, row int, rowH, pad float64) {
	s := d.Scale
	return int(math.Round(15 * s)), int(math.Round(12 * s)), int(math.Round(13 * s)), math.Round(28 * s), math.Round(16 * s)
}

const maxPopRows = 6

func (d *Dock) popGap() float64 {
	if d.Pill {
		return math.Round(22 * d.Scale)
	}
	return math.Round(20 * d.Scale)
}

// tile metrics for the preview and stack layouts
func (d *Dock) tileMetrics(mode, n int) (cols, rows int, tw, th, lab, gap float64) {
	s := d.Scale
	if mode == PopThumbs {
		if n > maxThumbs {
			n = maxThumbs
		}
		cols = min(n, 3)
		tw, th, lab, gap = math.Round(200*s), math.Round(124*s), math.Round(26*s), math.Round(10*s)
	} else {
		if n > maxGrid {
			n = maxGrid
		}
		cols = min(n, 4)
		tw, th, lab, gap = math.Round(84*s), math.Round(56*s), math.Round(26*s), math.Round(4*s)
	}
	if cols < 1 {
		cols = 1
	}
	rows = (n + cols - 1) / cols
	return
}

const (
	maxThumbs = 6
	maxGrid   = 12
)

func (d *Dock) popHeader() float64 {
	s := d.Scale
	tp, sp, _, _, _ := d.popPx()
	return 12*s + float64(tp)*1.3 + 2*s + float64(sp)*1.3 + 10*s
}

func (d *Dock) mediaMetrics() (pad, art, gap, bw, bh float64) {
	s := d.Scale
	return math.Round(14 * s), math.Round(104 * s), math.Round(14 * s), math.Round(42 * s), math.Round(34 * s)
}

func (d *Dock) popSize(pc PopContent) (float64, float64) {
	s := d.Scale
	tp, sp, rp, rowH, pad := d.popPx()
	if pc.Mode == PopMedia {
		mp, art, gap, bw, _ := d.mediaMetrics()
		tw := 3*bw + 8*s
		if d.Measure != nil {
			tw = math.Max(tw, d.Measure(pc.Title, tp))
			tw = math.Max(tw, d.Measure(pc.Subtitle, sp+1))
			tw = math.Max(tw, d.Measure(pc.Line3, sp))
		}
		tw = math.Min(math.Max(tw, 160*s), 240*s)
		return mp + art + gap + tw + mp, mp + art + mp
	}
	if (pc.Mode == PopThumbs || pc.Mode == PopGrid) && len(pc.Rows) > 0 {
		cols, rows, tw, th, lab, gap := d.tileMetrics(pc.Mode, len(pc.Rows))
		w := float64(cols)*tw + float64(cols-1)*gap + 2*pad
		if d.Measure != nil {
			w = math.Max(w, d.Measure(pc.Title, tp)+2*pad)
		}
		h := d.popHeader() + 2*s + float64(rows)*(th+lab) + float64(rows-1)*gap + pad*0.6
		return math.Max(w, 180*s), h
	}
	w := 0.0
	if d.Measure != nil {
		w = d.Measure(pc.Title, tp)
		w = math.Max(w, d.Measure(pc.Subtitle, sp))
		for i, r := range pc.Rows {
			if i >= maxPopRows {
				break
			}
			w = math.Max(w, d.Measure(r.Title, rp)+14*s)
		}
	}
	w = math.Min(math.Max(w+2*pad, 180*s), 340*s)
	h := 12*s + float64(tp)*1.3 + 2*s + float64(sp)*1.3 + 10*s
	n := len(pc.Rows)
	if n > maxPopRows {
		n = maxPopRows
	}
	if n > 0 {
		h += 6*s + float64(n)*rowH
	}
	return w, h
}

// Thickness is the layered window size across the dock.
func (d *Dock) Thickness() int {
	m := math.Max(d.Mag, 1)
	t := d.M + d.P + d.B*m + d.B*0.55 + 46*d.Scale + d.P
	if d.Notch {
		var across float64
		if d.Orient == OrientBottom {
			// header + two rows of window previews
			across = d.popHeader() + 2*d.Scale + 2*(124+26)*d.Scale + 10*d.Scale + 16*d.Scale
		} else {
			across = math.Max(3*200*d.Scale+2*10*d.Scale+32*d.Scale, (14+104+14+240+14)*d.Scale)
		}
		t = math.Max(t, d.P+d.B*m+d.P*0.9+20*d.Scale+across+12*d.Scale)
	}
	return int(math.Ceil(t))
}

func smoothstepFall(u float64) float64 {
	if u >= 1 {
		return 0
	}
	return (math.Cos(math.Pi*u) + 1) / 2
}

func approach(cur, target, dt, tau float64) float64 {
	k := 1 - math.Exp(-dt/tau)
	v := cur + (target-cur)*k
	if math.Abs(v-target) < 0.002 {
		v = target
	}
	return v
}

func easeOut(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return 1 - math.Pow(1-t, 3)
}

func (d *Dock) sepWidth() float64 {
	if d.Pill {
		return math.Round(2*d.P + 20*d.Scale) // gap between the two pills
	}
	return math.Round(14 * d.Scale)
}

func (d *Dock) baseSlot(it *Item) float64 {
	if it.Kind == KindSep {
		return d.sepWidth() * it.Appear
	}
	if it.Kind == KindGap {
		return (d.B + d.G) * it.WidthB * it.Appear
	}
	if it.Kind == KindWidget {
		return (d.B*d.widgetWidth(it) + d.G) * it.Appear
	}
	return (d.B + d.G) * it.Appear
}

func (d *Dock) indexOf(it *Item) int {
	for i, x := range d.Items {
		if x == it {
			return i
		}
	}
	return -1
}

func (d *Dock) inPopover(u, v float64) bool {
	if !d.Notch || d.PopItem == nil || d.PopT < 0.3 {
		return false
	}
	e := 6 * d.Scale
	if u >= d.PopU0-e && u <= d.PopU1+e && v >= d.PopV0-e && v <= d.PopV1+e {
		return true
	}
	// the neck between the icon and the flyout
	it := d.PopItem
	if math.Abs(u-it.U) <= it.Slot/2 && v >= d.PlateV0 && v <= d.PopV1 {
		return true
	}
	return false
}

// Step advances animations and recomputes layout. Returns true if anything moved.
func (d *Dock) Step(now time.Time) bool {
	dt := 1.0 / 60
	if !d.lastStep.IsZero() {
		dt = now.Sub(d.lastStep).Seconds() / AnimSlow
		if dt > 0.1 {
			dt = 0.1
		}
		if dt < 0.001 {
			dt = 0.001
		}
	}
	d.lastStep = now
	changed := false
	if pf := approach(d.PageF, float64(d.PageCur), dt, 0.06); pf != d.PageF {
		d.PageF = pf
		changed = true
	}
	if !d.PageT0.IsZero() && !d.Sliding(now) {
		d.PageT0 = time.Time{}
	}

	nh := approach(d.HideOffset, d.HideTarget, dt, 0.06)
	if nh != d.HideOffset {
		d.HideOffset = nh
		changed = true
	}
	d.PlateV0 = d.M - d.HideOffset
	d.PlateV1 = d.PlateV0 + d.PlateH
	d.mu, d.mv = d.toUV(d.MouseX, d.MouseY)
	L := d.L()

	n := len(d.Items)
	baseLeft := make([]float64, n)
	baseSlots := make([]float64, n)
	total := 0.0
	for i, it := range d.Items {
		baseSlots[i] = d.baseSlot(it)
		total += baseSlots[i]
	}
	x := d.CenterU - total/2
	for i := range d.Items {
		baseLeft[i] = x
		x += baseSlots[i]
	}

	// flyout hover keeps the dock "active" around its icon
	wasInside := d.PopInside
	d.PopInside = d.MouseValid && !d.Frozen && d.Drag == nil && d.inPopover(d.mu, d.mv)
	if d.PopInside != wasInside {
		changed = true
	}

	if !d.Frozen {
		active := false
		if d.MouseValid && n > 0 {
			l := baseLeft[0] + d.G/2 - d.P
			r := x - d.G/2 + d.P
			if d.Active {
				l, r = d.PlateU0, d.PlateU1
			}
			top := d.PlateV1
			if d.Active {
				maxs := 0.0
				for _, it := range d.Items {
					maxs = math.Max(maxs, it.Size)
				}
				top = math.Max(d.PlateV1, d.PlateV0+d.P+maxs+4*d.Scale)
			}
			if d.mu >= l && d.mu <= r && d.mv <= top && d.mv >= -3 {
				active = true
			}
		}
		if d.Drag != nil || d.PopInside {
			active = true
		}
		if active != d.Active {
			d.Active = active
			changed = true
		}
	}

	// effective pointer position along the dock
	mu := d.mu
	if d.PopInside {
		if i := d.indexOf(d.PopItem); i >= 0 {
			mu = baseLeft[i] + baseSlots[i]/2
		}
	}

	rng := d.B * 2.7
	maxScale := 1.0
	for i, it := range d.Items {
		target := 1.0
		if it.Kind != KindSep && it.Kind != KindWidget && d.Active && d.Mag > 1.01 && d.HideOffset < 1 {
			c := baseLeft[i] + baseSlots[i]/2
			target = 1 + (d.Mag-1)*smoothstepFall(math.Abs(mu-c)/rng)
		}
		if !d.Frozen {
			ns := approach(it.Scale, target, dt, 0.045)
			if ns != it.Scale {
				it.Scale = ns
				changed = true
			}
		}
		target, tau := 1.0, 0.07
		if it.Leaving {
			target, tau = 0, 0.045
		}
		if it.PageOut {
			target, tau = 0, 0.06
		} else if it.Paged && !it.Leaving {
			tau = 0.06 // same pace in and out, so the pill's width changes smoothly
		}
		na := approach(it.Appear, target, dt, tau)
		if na != it.Appear {
			it.Appear = na
			changed = true
		}
		maxScale = math.Max(maxScale, it.Scale)
	}
	if d.stepBud(now) {
		changed = true
	}

	slots := make([]float64, n)
	stotal := 0.0
	for i, it := range d.Items {
		if it.Kind == KindSep {
			slots[i] = d.sepWidth() * it.Appear
			it.Size = d.B * 0.62
		} else if it.Kind == KindGap {
			slots[i] = (d.B + d.G) * it.WidthB * it.Appear
			it.Size = 0
		} else if it.Kind == KindWidget {
			it.Size = d.B * it.Appear
			slots[i] = (d.B*d.widgetWidth(it) + d.G) * it.Appear
		} else {
			it.Size = d.B * it.Scale * it.Appear
			slots[i] = (d.B*it.Scale + d.G) * it.Appear
		}
		stotal += slots[i]
	}
	left := d.CenterU - stotal/2
	if d.Mag > 1.01 && n > 0 && d.MouseValid {
		blend := math.Min((maxScale-1)/(d.Mag-1), 1)
		k := 0
		for i := range d.Items {
			if mu >= baseLeft[i] {
				k = i
			}
		}
		t := math.Min(math.Max((mu-baseLeft[k])/baseSlots[k], 0), 1)
		nl := 0.0
		for i := 0; i < k; i++ {
			nl += slots[i]
		}
		la := mu - (nl + t*slots[k])
		left = left + (la-left)*blend
	}
	edge := 8 * d.Scale
	if d.Notch && !d.Pill {
		edge = d.PlateH * 0.45
	}
	minL := edge - d.G/2 + d.P
	maxL := L - edge - stotal + d.G/2 - d.P
	left = math.Max(math.Min(left, maxL), minL)
	cu := left
	for i, it := range d.Items {
		it.Slot = slots[i]
		it.U = cu + slots[i]/2
		cu += slots[i]
	}
	if n > 0 {
		d.PlateU0 = left + d.G/2 - d.P
		d.PlateU1 = cu - d.G/2 + d.P
	} else {
		d.PlateU0, d.PlateU1 = d.CenterU-d.P, d.CenterU+d.P
	}

	for _, it := range d.Items {
		it.Lift = 0
		if it.Bouncing {
			const cycle = 0.62
			t := since(now, it.BounceStart).Seconds()
			cyc := math.Floor(t / cycle)
			if cyc >= 1 && (it.Running || cyc >= 7) {
				it.Bouncing = false
			} else {
				ph := math.Mod(t, cycle) / cycle
				it.Lift = d.B * 0.42 * math.Sin(math.Pi*ph)
			}
			changed = true
		}
	}

	if len(d.Poofs) > 0 {
		keep := d.Poofs[:0]
		for _, p := range d.Poofs {
			if now.Sub(p.Start) < 450*time.Millisecond {
				keep = append(keep, p)
			}
		}
		d.Poofs = keep
		changed = true
	}

	hover := -1
	if d.PopInside {
		hover = d.indexOf(d.PopItem)
	} else if d.Active && d.Drag == nil && !d.Sliding(now) {
		for i, it := range d.Items {
			if it.Kind == KindSep || it.Kind == KindGap || it.PageOut {
				continue
			}
			if math.Abs(d.mu-it.U) <= it.Slot/2 {
				hover = i
			}
		}
	}
	if hover != d.Hover {
		d.Hover = hover
		changed = true
	}
	for i, it := range d.Items {
		if it.Kind == KindWidget && it.Widget == "media" {
			h := -1
			if i == hover {
				h = d.MediaButtonAt(it, d.mu)
			}
			if h != it.WHot {
				it.WHot = h
				changed = true
			}
		}
	}

	if d.Notch && d.stepPopover(now, dt) {
		changed = true
	}
	return changed
}

func (d *Dock) stepPopover(now time.Time, dt float64) bool {
	var cand *Item
	if d.PopInside {
		cand = d.PopItem
	} else if !d.Frozen && d.Drag == nil && d.Pressed == nil && d.Hover >= 0 && d.Hover < len(d.Items) {
		cand = d.Items[d.Hover]
	}
	if d.Frozen && d.PopItem != nil {
		cand = nil
	}
	if cand != nil && cand == d.SearchItem { // its card is the search box
		cand = nil
	}
	setItem := func(it *Item) {
		d.PopItem = it
		if d.PopFn != nil {
			d.Pop = d.PopFn(it)
		} else {
			d.Pop = PopContent{Title: it.Name}
		}
	}
	if cand != nil {
		d.popLeaveSince = time.Time{}
		switch {
		case cand == d.PopItem:
			d.PopTarget = 1
		case d.PopItem != nil && d.PopT > 0.15:
			setItem(cand) // morph straight across to the next icon
			d.PopTarget = 1
		default:
			if d.popCand != cand {
				d.popCand = cand
				d.popCandSince = now
			}
			if now.Sub(d.popCandSince) > 180*time.Millisecond {
				setItem(cand)
				d.PopCU = cand.U
				d.PopTarget = 1
			}
		}
	} else {
		d.popCand = nil
		if d.PopItem != nil {
			if d.popLeaveSince.IsZero() {
				d.popLeaveSince = now
			} else if now.Sub(d.popLeaveSince) > 160*time.Millisecond {
				d.PopTarget = 0
			}
		}
	}
	if d.Frozen {
		d.PopTarget = 0
	}
	changed := false
	nt := approach(d.PopT, d.PopTarget, dt, 0.065)
	if nt != d.PopT {
		d.PopT = nt
		changed = true
	}
	if d.PopItem == nil {
		return changed
	}
	if d.PopT == 0 && d.PopTarget == 0 {
		d.PopItem = nil
		return true
	}
	if d.indexOf(d.PopItem) < 0 {
		d.PopItem = nil
		d.PopT, d.PopTarget = 0, 0
		return true
	}
	ncu := approach(d.PopCU, d.PopItem.U, dt, 0.05)
	if ncu != d.PopCU {
		d.PopCU = ncu
		changed = true
	}

	// geometry
	s := d.Scale
	cw, ch := d.popSize(d.Pop)
	along, across := cw, ch
	if d.Orient != OrientBottom {
		along, across = ch, cw
	}
	e := easeOut(d.PopT)
	it := d.PopItem
	slot := math.Max(it.Size, d.B)
	aw := slot + (along-slot)*e
	// floating card above the dock, joined by a pinched neck
	topAt := d.PlateV0 + math.Max(d.PlateH, d.P+it.Size+d.P*0.9)
	base := topAt + d.popGap()*e
	L := d.L()
	// keep the flyout inside the dock's own length (inset past the rounded ends)
	inset := d.PlateH * 0.45
	if d.Pill {
		inset = 0
	}
	lo, hi := d.PlateU0+inset+along/2, d.PlateU1-inset-along/2
	cu := d.PopCU
	if lo > hi {
		cu = (d.PlateU0 + d.PlateU1) / 2
	} else {
		cu = math.Min(math.Max(cu, lo), hi)
	}
	cu = math.Min(math.Max(cu, along/2+6*s), L-along/2-6*s)
	cuNow := it.U + (cu-it.U)*e
	d.PopU0, d.PopU1 = cuNow-aw/2, cuNow+aw/2
	d.PopV0 = base
	d.PopV1 = base + across*e
	// final box for text
	fb := topAt + d.popGap()
	x, y, w, h := d.rectUV(cu-along/2, fb, cu+along/2, fb+across)
	d.PopX0, d.PopY0, d.PopX1, d.PopY1 = x, y, x+w, y+h
	_, _, _, rowH, pad := d.popPx()
	ry := y + d.popHeader() + 6*s
	hot := -1
	inside := func(x0, y0, x1, y1 float64) bool {
		return d.PopInside && d.MouseX >= x0 && d.MouseX <= x1 && d.MouseY >= y0 && d.MouseY < y1
	}
	if d.Pop.Mode == PopMedia {
		mp, art, gap, bw, bh := d.mediaMetrics()
		tx := x + mp + art + gap
		by := y + mp + art - bh
		for i := range d.Pop.Rows {
			r := &d.Pop.Rows[i]
			r.X0, r.Y0 = tx+float64(i)*(bw+4*s)-6*s, by
			r.X1, r.Y1 = r.X0+bw, by+bh
			if inside(r.X0, r.Y0, r.X1, r.Y1) {
				hot = i
			}
		}
	} else if (d.Pop.Mode == PopThumbs || d.Pop.Mode == PopGrid) && len(d.Pop.Rows) > 0 {
		lim := maxThumbs
		if d.Pop.Mode == PopGrid {
			lim = maxGrid
		}
		cols, _, tw, th, lab, gap := d.tileMetrics(d.Pop.Mode, len(d.Pop.Rows))
		gw := float64(cols)*tw + float64(cols-1)*gap
		gx := x + (w-gw)/2
		gy := y + d.popHeader() + 2*s
		for i := range d.Pop.Rows {
			r := &d.Pop.Rows[i]
			if i >= lim {
				*r = PopRow{Title: r.Title, Hwnd: r.Hwnd, Path: r.Path, Icon: r.Icon, Min: r.Min, Small: r.Small}
				continue
			}
			c, rw := i%cols, i/cols
			r.X0 = gx + float64(c)*(tw+gap)
			r.Y0 = gy + float64(rw)*(th+lab+gap)
			r.X1, r.Y1 = r.X0+tw, r.Y0+th+lab
			if d.Pop.Mode == PopThumbs {
				r.TX0, r.TY0, r.TX1, r.TY1 = r.X0+4*s, r.Y0+4*s, r.X1-4*s, r.Y0+th-2*s
				cs := 20 * s
				r.CX1, r.CY0 = r.X1-4*s, r.Y0+th+(lab-cs)/2
				r.CX0, r.CY1 = r.CX1-cs, r.CY0+cs
			} else {
				is := math.Round(44 * s)
				r.TX0, r.TY0 = r.X0+(tw-is)/2, r.Y0+(th-is)/2+4*s
				r.TX1, r.TY1 = r.TX0+is, r.TY0+is
			}
			if inside(r.X0, r.Y0, r.X1, r.Y1) {
				hot = i
			}
		}
	} else {
		for i := range d.Pop.Rows {
			r := &d.Pop.Rows[i]
			if i >= maxPopRows {
				r.X0, r.Y0, r.X1, r.Y1 = 0, 0, 0, 0
				continue
			}
			r.X0, r.X1 = x+pad*0.5, x+w-pad*0.5
			r.Y0, r.Y1 = ry+float64(i)*rowH, ry+float64(i+1)*rowH
			if inside(r.X0, r.Y0, r.X1, r.Y1) {
				hot = i
			}
		}
	}
	if hot != d.PopHot {
		d.PopHot = hot
		changed = true
	}
	return changed
}

// PopRowAt returns the flyout row under a window-local point, or -1.
func (d *Dock) PopRowAt(x, y float64) int {
	if d.PopItem == nil || d.PopT < 0.9 {
		return -1
	}
	for i, r := range d.Pop.Rows {
		if r.X1 > r.X0 && x >= r.X0 && x <= r.X1 && y >= r.Y0 && y < r.Y1 {
			return i
		}
	}
	return -1
}

// PopCloseAt returns the preview whose close button is under a point, or -1.
func (d *Dock) PopCloseAt(x, y float64) int {
	if d.PopItem == nil || d.PopT < 0.9 || d.Pop.Mode != PopThumbs {
		return -1
	}
	for i, r := range d.Pop.Rows {
		if r.CX1 > r.CX0 && i == d.PopHot && x >= r.CX0 && x <= r.CX1 && y >= r.CY0 && y <= r.CY1 {
			return i
		}
	}
	return -1
}

// InPop reports whether a window-local point is on the flyout.
func (d *Dock) InPop(x, y float64) bool {
	u, v := d.toUV(x, y)
	return d.PopItem != nil && d.PopT > 0.5 && u >= d.PopU0 && u <= d.PopU1 && v >= d.PopV0 && v <= d.PopV1
}

func (d *Dock) Busy() bool {
	if len(d.Poofs) > 0 || d.PopT != d.PopTarget || (d.popCand != nil && d.popCand != d.PopItem) {
		return true
	}
	if d.PopItem != nil && math.Abs(d.PopCU-d.PopItem.U) > 0.5 {
		return true
	}
	if d.Active || d.Drag != nil || math.Abs(d.HideOffset-d.HideTarget) > 0.01 {
		return true
	}
	for _, it := range d.Items {
		if it.Bouncing || (it.Appear < 1 && !(it.Leaving && it.Appear == 0)) {
			return true
		}
		if it.Kind != KindSep && math.Abs(it.Scale-1) > 0.001 {
			return true
		}
	}
	return false
}

// ItemAt returns the index of the item under a window-local point, or -1.
func (d *Dock) ItemAt(x, y float64) int {
	u, v := d.toUV(x, y)
	if v < math.Min(d.PlateV0, 0)-3 {
		return -1
	}
	for i, it := range d.Items {
		if it.Kind == KindSep || it.Kind == KindGap || it.PageOut {
			continue
		}
		top := math.Max(d.PlateV1, d.PlateV0+d.P+it.Size+it.Lift+4*d.Scale)
		if math.Abs(u-it.U) <= it.Slot/2 && v <= top {
			return i
		}
	}
	return -1
}

func (d *Dock) InPlate(x, y float64) bool {
	u, v := d.toUV(x, y)
	return u >= d.PlateU0 && u <= d.PlateU1 && v >= math.Min(d.PlateV0, 0)-3 && v <= d.PlateV1
}

// DragAlong returns the dragged icon's position along the dock.
func (d *Dock) DragAlong() float64 {
	u, _ := d.toUV(d.DragX, d.DragY)
	return u
}

// DragOff reports whether the dragged icon is far enough from the dock to be removed.
func (d *Dock) DragOff() bool {
	_, v := d.toUV(d.DragX, d.DragY)
	return v > d.PlateV1+d.B*0.9
}

// widgetWidth is a widget's length along the dock, in icon sizes.
func (d *Dock) widgetWidth(it *Item) float64 {
	if d.Orient == OrientBottom {
		if it.Widget == "media" {
			return 1 + 1.7*smooth01(it.WWave)
		}
		return it.WidthB
	}
	switch it.Widget {
	case "clock":
		return 1.0
	case "battery", "status":
		return 0.95
	case "media":
		return 1 + 0.55*smooth01(it.WWave)
	}
	return it.WidthB
}

// smooth01 eases 0..1 in and out.
func smooth01(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return t * t * (3 - 2*t)
}

// MediaPlayerLayout gives the wide (bottom dock) player's parts as offsets from its left edge,
// in pixels: art size, text start, the three button centres and the wave start.
func (d *Dock) MediaPlayerLayout() (art, tx float64, btn [3]float64, wave float64) {
	B := d.B
	art = math.Round(B * 0.84)
	tx = (B-art)/2 + art + B*0.2
	btn = [3]float64{tx + B*0.2, tx + B*0.66, tx + B*1.12}
	wave = tx + B*1.6
	return
}

// MediaButtonAt reports whether a position along the dock is on the Now Playing tile:
// 1 (the whole tile plays/pauses; previous/next live in the hover card) or -1.
func (d *Dock) MediaButtonAt(it *Item, u float64) int {
	w := d.B * d.widgetWidth(it)
	if lx := u - (it.U - w/2); lx < 0 || lx > w {
		return -1
	}
	return 1
}

// ---------- colours ----------

// Palette is the dock's colour scheme: dark, light, or the taskbar's accent colour.
type Palette struct {
	Fill  [3]uint8 // pill and card background
	Alpha float64
	Light bool // light background, so dark text and marks
}

func (d *Dock) fillC() color.RGBA {
	p := d.Pal
	if p.Alpha == 0 {
		return pc(4, 4, 6, 0.97)
	}
	return pc(p.Fill[0], p.Fill[1], p.Fill[2], p.Alpha)
}

// ink is a neutral overlay that shows up on the background (white on dark, black on light).
func (d *Dock) ink(a float64) color.RGBA {
	if d.Pal.Light {
		return pc(0, 0, 0, a*0.75)
	}
	return pc(255, 255, 255, a)
}

func (d *Dock) fg(a float64) color.RGBA {
	if d.Pal.Light {
		return pc(22, 22, 26, a)
	}
	return pc(246, 246, 248, a)
}

func (d *Dock) fg2(a float64) color.RGBA {
	if d.Pal.Light {
		return pc(60, 60, 68, a)
	}
	return pc(205, 205, 212, a)
}

func (d *Dock) dimC(a float64) color.RGBA {
	if d.Pal.Light {
		return pc(105, 105, 115, a)
	}
	return pc(150, 150, 160, a)
}

// Ghost is an icon from the page you just left, drawn while it slides away.
type Ghost struct {
	It *Item
	U  float64
}

const pageSlideDur = 340 * time.Millisecond

// AnimSlow stretches every animation: 1 = normal (speed 5), up to 2.2 (speed 1).
var AnimSlow = 1.0

// AnimSpeedFactor turns the 1–5 speed setting into AnimSlow.
func AnimSpeedFactor(speed int) float64 {
	switch speed {
	case 1:
		return 2.2
	case 2:
		return 1.8
	case 3:
		return 1.45
	case 4:
		return 1.2
	}
	return 1
}

// since is how far an animation started at t0 has got, on the (possibly slowed) animation clock.
func since(now, t0 time.Time) time.Duration {
	return time.Duration(float64(now.Sub(t0)) / AnimSlow)
}

func (d *Dock) Sliding(now time.Time) bool {
	return !d.PageT0.IsZero() && since(now, d.PageT0) < pageSlideDur
}

// SlideT is the eased progress of the page slide (1 when not sliding).
func (d *Dock) SlideT(now time.Time) float64 {
	if d.PageT0.IsZero() {
		return 1
	}
	t := float64(since(now, d.PageT0)) / float64(pageSlideDur)
	if t >= 1 {
		return 1
	}
	t = math.Max(0, t)
	return 1 - math.Pow(1-t, 3) // ease out: quick start, gentle landing
}

const (
	budSplitDur = 300 * time.Millisecond // the pill opening (with the first app sliding in)
	budEachGap  = 60 * time.Millisecond  // then the other open apps slide in one by one
	budGrowDur  = 240 * time.Millisecond
	budOutDur   = 300 * time.Millisecond
)

func easeSine(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return 0.5 - 0.5*math.Cos(math.Pi*t)
}

// stepBud drives the running-apps pill's entrance and exit. In: a drop buds off the
// end of the apps pill carrying the first open app, then the pill widens as the
// other open apps pop in one after another. Out: everything melts back together.
func (d *Dock) stepBud(now time.Time) bool {
	if d.BudStart.IsZero() {
		return false
	}
	t := since(now, d.BudStart)
	var run []*Item
	var sep *Item
	for _, it := range d.Items {
		if !it.InRun {
			continue
		}
		if it.Kind == KindSep {
			sep = it
		} else {
			run = append(run, it)
		}
	}
	if !d.BudIn {
		p := 1 - easeSine(float64(t)/float64(budOutDur))
		for _, it := range d.Items {
			if it.InRun {
				it.Appear = p
			}
		}
		if t >= budOutDur {
			d.BudStart = time.Time{}
		}
		return true
	}
	split := easeSine(float64(t) / float64(budSplitDur))
	if sep != nil {
		sep.Appear = split
	}
	end := budSplitDur
	for k, it := range run {
		if it.Leaving {
			continue
		}
		if k == 0 {
			it.Appear = split
			continue
		}
		start := budSplitDur - 120*time.Millisecond + time.Duration(k-1)*budEachGap
		it.Appear = easeSine(float64(t-start) / float64(budGrowDur))
		if e := start + budGrowDur; e > end {
			end = e
		}
	}
	if t >= end {
		d.BudStart = time.Time{}
	}
	return true
}
