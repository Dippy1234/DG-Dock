package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
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
	IconSize         int               `json:"iconSize"`
	Magnification    float64           `json:"magnification"`
	AutoHide         bool              `json:"autoHide"`
	HideTaskbar      bool              `json:"hideTaskbar"`
	ReserveSpace     bool              `json:"reserveSpace"`
	Blur             bool              `json:"blur"`
	DockTransparency int               `json:"dockTransparency,omitempty"` // 0 = opaque, 100 = transparent
	DockColor        string            `json:"dockColor,omitempty"`        // "#rrggbb"; "" = the theme's own dock colour
	AccentColor      string            `json:"accentColor,omitempty"`      // "" = Windows accent, "dock" = from the dock colour, or "#rrggbb"
	MenuTint         bool              `json:"menuTint,omitempty"`         // menus, Launchpad and Settings take the dock colour
	WidgetColor      string            `json:"widgetColor,omitempty"`      // "" = a soft tint of the dock, "wallpaper", or "#rrggbb"
	WidgetColors     map[string]string `json:"widgetColors,omitempty"`     // one widget's own colour: "dock", "wallpaper" or "#rrggbb" (absent = as all widgets)
	ClockFace        string            `json:"clockFace,omitempty"`        // round icons' clock: "" = analogue, "digital"
	WidgetStyle      string            `json:"widgetStyle,omitempty"`      // "" = Windows style, "dg" = DG style (dot-matrix, line glyphs, a red accent)
	// weather widget: "" = this PC's location, "home" = the home address (no house number)
	WeatherLoc         string            `json:"weatherLoc,omitempty"`
	Font               string            `json:"font,omitempty"` // a font installed in Windows ("" = Segoe UI)
	WeatherStreet      string            `json:"weatherStreet,omitempty"`
	WeatherTown        string            `json:"weatherTown,omitempty"`
	WeatherPostcode    string            `json:"weatherPostcode,omitempty"`
	WeatherCountry     string            `json:"weatherCountry,omitempty"`
	WeatherHomeLat     float64           `json:"weatherHomeLat,omitempty"`
	WeatherHomeLon     float64           `json:"weatherHomeLon,omitempty"`
	WeatherHomePlace   string            `json:"weatherHomePlace,omitempty"`
	WeatherUnits       string            `json:"weatherUnits,omitempty"` // "" = by Windows' region, "c" or "f"
	Theme              string            `json:"theme"`                  // auto, light, dark
	IconStyle          string            `json:"iconStyle"`              // tiles, original
	ShowRunning        bool              `json:"showRunning"`
	HideFullscreen     bool              `json:"hideFullscreen"`
	NoUsbWidget        bool              `json:"noUsbWidget,omitempty"`        // don't show USB drives on the dock
	NoFolderActivity   bool              `json:"noFolderActivity,omitempty"`   // don't show downloads/copies on folder icons
	LaunchpadCols      int               `json:"launchpadCols,omitempty"`      // apps across (0 = 6)
	LaunchpadIcon      int               `json:"launchpadIcon,omitempty"`      // app size in pixels (0 = 60)
	LaunchpadNoNames   bool              `json:"launchpadNoNames,omitempty"`   // icons only
	LaunchpadShowAll   bool              `json:"launchpadShowAll,omitempty"`   // include duplicates and uninstallers, readmes…
	LaunchpadOrder     []string          `json:"launchpadOrder,omitempty"`     // tab order you chose
	LaunchpadIconPanel bool              `json:"launchpadIconPanel,omitempty"` // side panel shows icons only
	LaunchpadNoRecent  bool              `json:"launchpadNoRecent,omitempty"`  // no Recent row on Home
	LaunchpadFavs      []string          `json:"launchpadFavs"`                // Favourites: app IDs and "folder:Name", in order (nil = not set up yet)
	LaunchpadRecent    []string          `json:"launchpadRecent,omitempty"`    // apps opened from Launchpad, newest first
	PillOrder          []string          `json:"pillOrder,omitempty"`          // the dock's pills left to right: apps, running, files, widgets
	ImportedApks       map[string]int64  `json:"importedApks,omitempty"`       // icon packs dropped in the APK folder: name → when imported (file time)
	LaunchpadGroups    map[string]string `json:"launchpadGroups,omitempty"`    // app ID (lower case) → category or folder you chose
	LaunchpadFolders   []string          `json:"launchpadFolders,omitempty"`   // folders you made, in order
	LockDock           bool              `json:"lockDock,omitempty"`           // icons can't be moved or removed from the dock
	Position           string            `json:"position"`                     // bottom, left, right
	Style              string            `json:"style"`                        // default, pill
	DockShapeApplied   bool              `json:"dockShapeApplied,omitempty"`
	Connected          bool              `json:"connectedDock,omitempty"`
	AppPadding         int               `json:"appPadding,omitempty"`
	WidgetSpacing      int               `json:"widgetSpacing,omitempty"`
	WidgetSize         int               `json:"widgetSize,omitempty"` // widget width, % (60–160; 0 = 100)
	WidgetSpacingSet   bool              `json:"widgetSpacingSet,omitempty"`
	// WidgetSpacingV2 records the one-time migration to the unified widget rhythm.
	WidgetSpacingV2 bool `json:"widgetSpacingV2,omitempty"`
	// WidgetSpacingV3: widgets use the same spacing scale as app icons (one rhythm).
	WidgetSpacingV3 bool   `json:"widgetSpacingV3,omitempty"`
	AppIconScale    int    `json:"appIconScale"`    // 0–150% full app icon tile size within its dock slot
	IconPackScale   int    `json:"iconPackScale"`   // 0–150% artwork scale for selected icon-pack and custom icons
	WidgetIconScale int    `json:"widgetIconScale"` // 0–150% custom widget artwork scale
	MediaWidgetIcon string `json:"mediaWidgetIcon,omitempty"`
	Pins            []Pin  `json:"pins"`
	Initialized     bool   `json:"initialized"`
	MagOffApplied   bool   `json:"magOffApplied"`
	PillApplied     bool   `json:"pillApplied"`
	NoBounce        bool   `json:"noBounce"`
	// per-animation off switches (all default on; the master NoBounce turns everything off)
	NoHoverLift     bool   `json:"noHoverLift,omitempty"`
	NoCardSpring    bool   `json:"noCardSpring,omitempty"`
	NoPageStagger   bool   `json:"noPageStagger,omitempty"`
	NoJelly         bool   `json:"noJelly,omitempty"`
	NoHideSpring    bool   `json:"noHideSpring,omitempty"`
	NoDropSettle    bool   `json:"noDropSettle,omitempty"`
	NoAttention     bool   `json:"noAttention,omitempty"` // don't bounce/badge apps that ask for attention
	IconPack        string `json:"iconPack"`
	BuiltinPacksVer int    `json:"builtinPacksVer,omitempty"` // the built-in icon packs put in place
	NoPreviews      bool   `json:"noPreviews"`
	NoWidgets       bool   `json:"noWidgets"`
	NoTray          bool   `json:"noTray"`
	NoFolderFiles   bool   `json:"noFolderFiles,omitempty"`
	AnimSpeed       int    `json:"animSpeed,omitempty"` // 1 (slowest) to 5 (normal); 0 = 5
	// custom icons for things that aren't pins: "tray:<app.exe>" and "file:<path>" → icon file
	IconOverrides   map[string]string `json:"iconOverrides,omitempty"`
	MainDisplay     bool              `json:"mainDisplayOnly"`
	DockDisplays    []string          `json:"dockDisplays,omitempty"`    // a dock on each of these displays (\\.\DISPLAYn); empty = one dock
	IsolateDisplays bool              `json:"isolateDisplays,omitempty"` // each dock only shows apps open on its own display
	HideIndicators  bool              `json:"hideIndicators"`
	UpdatePrompted  int               `json:"updatePrompted"`  // newest version we've already asked about
	HideAlways      bool              `json:"hideAlways"`      // classic auto-hide (off = smart hide)
	StartIsStart    bool              `json:"startOpensStart"` // Start icon opens Windows Start instead of Search
	SearchHits      map[string]int    `json:"searchHits,omitempty"`
	AppsPerPage     int               `json:"appsPerPage"` // 0 = default (10), -1 = all on one page
	// taskbar auto-hide state before we touched it (-1 = untouched)
	PrevTaskbarAutoHide int               `json:"prevTaskbarAutoHide"`
	PrevMMTaskbar       int               `json:"prevMMTaskbar"`
	DisableSearchHotkey bool              `json:"disableSearchHotkey,omitempty"`
	NoWinKeyLaunchpad   bool              `json:"noWinKeyLaunchpad,omitempty"` // Windows key opens Start instead of Launchpad
	WidgetOrder         []string          `json:"widgetOrder,omitempty"`
	HiddenWidgets       []string          `json:"hiddenWidgets,omitempty"`
	WidgetPositions     map[string]string `json:"widgetPositions,omitempty"`
	WidgetAnchors       map[string]string `json:"widgetAnchors,omitempty"`
	WidgetCenterApplied bool              `json:"widgetCenterApplied,omitempty"`
	WidgetGroupCentered bool              `json:"widgetGroupCentered,omitempty"`
}

func defaultConfig() *Config {
	// These are the starting values for a brand-new install (no settings.json yet).
	// An existing settings file is layered on top, so changing them never disturbs
	// anyone who has already set the dock up. They match the maintainer's own setup.
	return &Config{
		IconSize:         46,
		AppIconScale:     110,
		IconPackScale:    100,
		WidgetIconScale:  99,
		Magnification:    1.0,
		ReserveSpace:     false,
		AutoHide:         true,
		HideTaskbar:      true,
		HideFullscreen:   true,
		Blur:             true,
		DockTransparency: 1,
		// colours from the wallpaper, menus matching the dock, DG style widgets (Build 251)
		DockColor:           "wallpaper",
		AccentColor:         "wallpaper",
		WidgetColor:         "wallpaper",
		MenuTint:            true,
		WidgetStyle:         "dg",
		IconPack:            "Black and White Icons (Auto)",
		AppPadding:          1,
		WidgetSize:          90,
		LaunchpadCols:       5,
		LaunchpadIcon:       50,
		AppsPerPage:         4,
		IsolateDisplays:     true,
		Theme:               "auto",
		IconStyle:           "tiles",
		ShowRunning:         true,
		Position:            "bottom",
		Style:               "default", // the maintainer's own dock shape (rounded)
		DockShapeApplied:    true,      // so a fresh install keeps it rather than being forced to pill
		MagOffApplied:       true,      // magnification already migrated off (maintainer's setup)
		PillApplied:         true,      // pill-shape migration already applied
		DisableSearchHotkey: true,
		// unified spacing rhythm: widgets share the app-icon spacing scale (all migrations done)
		WidgetSpacing:    7,
		WidgetSpacingSet: true,
		WidgetSpacingV2:  true,
		WidgetSpacingV3:  true,
		// Launchpad Favourites (Home): standard Windows 11 apps present on every install
		LaunchpadFavs: []string{
			"Microsoft.WindowsNotepad_8wekyb3d8bbwe!App",
			"Microsoft.WindowsCalculator_8wekyb3d8bbwe!App",
			"Microsoft.WindowsStore_8wekyb3d8bbwe!App",
			"windows.immersivecontrolpanel_cw5n1h2txyewy!microsoft.windows.immersivecontrolpanel",
			"Microsoft.ZuneMusic_8wekyb3d8bbwe!Microsoft.ZuneMusic",
		},
		// widgets grouped on the right (and the one-time centering migration already done)
		WidgetPositions:     map[string]string{"clock": "right", "status": "right", "media": "right", "tray": "right", "weather": "right"},
		WidgetAnchors:       map[string]string{"clock": "@end", "usb": "@end"},
		WidgetCenterApplied: true,
		WidgetGroupCentered: true,
		PrevTaskbarAutoHide: -1,
		PrevMMTaskbar:       -1,
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
	Key      string
	Kind     string
	Pin      *Pin // nil for running-only and separator
	PinIdx   int
	Name     string
	Target   string // path or AppsFolder id
	ExePath  string // lower-case normalised exe used for matching windows
	AUMID    string // app user model id used for matching windows
	IsDir    bool
	Section  int // 0 = apps, 1 = folders/files/trash (right of separator)
	Icon     *IconSet
	Raw      *image.RGBA // unstyled icon
	RawKey   string
	PackIcon bool // icon was supplied by the active Android icon pack

	Running bool
	Leaving bool // app just closed: shrink out, then drop
	Windows []uintptr
	cycle   int // scroll-to-cycle position

	// widgets
	Widget              string // "clock", "battery", "media"
	WidthB              float64
	UsbRoot             string    // USB drive tile: its drive (E:\)
	XferBusy            bool      // files are arriving (folder icons, USB tiles)
	XferLevel           float64   // how far, 0..1; below 0 when unknown (moving sweep)
	XferSince           time.Time // when the current transfer started showing
	XferDoneAt          time.Time // when it finished (the border fills, then fades)
	WText1, WText2      string
	WLevel              float64
	WCharging, WHasBt   bool
	WCode               int      // weather widget: WMO weather code (-1: nothing yet)
	WNight              bool     // weather widget: it's night there
	WWifi               int      // Wi-Fi strength dots lit (0..4)
	WHot                int      // hovered media button
	Art                 *IconSet // now playing cover
	DefaultArt          *IconSet // user-selected fallback artwork for the media widget
	WPlaying            bool
	WLevels             [12]float64 // sound-wave bars, 0..1
	Tray                []*IconSet  // system tray icons (tray widget)
	WWave               float64     // 0 = wave hidden (tile is just the cover), 1 = wave shown while music plays
	WAccent             [3]uint8    // colour picked from the cover art
	WVol                float64     // volume shown while scrolling on the player (0..1, -1 = muted)
	WVolAt              time.Time
	WBr                 float64 // brightness shown while scrolling on the battery ring (0..1)
	WBrDim              float64 // extra dimming below the lowest backlight level (0..1)
	WBrShow, WBrDimShow float64 // what the ring shows: glides to WBr / WBrDim
	WBrAt               time.Time
	Paged               bool   // on the current page of apps
	Browse              string // folder shown by this item's card (pinned folder, or a shortcut's target)
	PageOut             bool   // on the page being left: shrinking away
	InRun               bool   // in the running-apps pill (or its separator)

	// animation state
	Scale       float64
	Appear      float64
	Bouncing    bool
	BounceStart time.Time
	Attention   bool // the app asked for attention: red dot until it's opened

	// springs (see stepSprings): hover lift, the running dot popping in, jelly squash
	HovT, hovV float64   // 0 = resting, 1 = under the pointer (can overshoot a little)
	DotT, dotV float64   // the running dot's size, 0..1
	Sq         float64   // squash: + is wide and short, − is tall and thin
	JellyStart time.Time // a single settle (after a drop)
	JellyAmp   float64
	springing  bool // a spring on this item is still moving

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
	OrientTop    = 3
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
	PopList    = 0 // text rows
	PopThumbs  = 1 // live window previews
	PopGrid    = 2 // folder stack
	PopMedia   = 3 // now playing
	PopTray    = 4 // system tray app list
	PopDrive   = 5 // USB drive card (laid out like Now Playing)
	PopWeather = 6 // weather: now, and the next days
)

type PopContent struct {
	Title    string
	Subtitle string
	Line3    string
	Mode     int
	Rows     []PopRow
	Art      *IconSet
	Playing  bool
	Level    float64 // USB card: bar (space used, or copy progress)
	Busy     bool    // USB card: a copy is running
	// folder cards: rows of tiles in the folder, how many fit, and the first one shown
	ScrollRows, ScrollVis, ScrollTop int
	Weather                          *Weather // weather card
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

	Orient       int
	Notch        bool // supports widgets and flyouts
	Pill         bool // pill-group surface renderer
	PillRound    bool // use fully rounded capsule ends for pill shape
	Connected    bool // join the split pill groups into one continuous dock
	WidgetGap    float64
	WidgetScale  float64 // the Widget size setting: how wide widgets are (1 = normal)
	Dark         bool
	BlurOn       bool
	CircleIcons  bool
	Transparency int
	Accent       color.RGBA // Windows accent colour (copy progress)
	Tiles        bool

	W, H    int // layered window size
	CenterU float64

	HideOffset, HideTarget                                               float64
	hideV                                                                float64 // the show spring's speed
	Calm                                                                 bool    // animations off: no springs, lifts or jelly
	noLift, noCardSpring, noStagger, noJelly, noHideSpring, noDropSettle bool

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
	// WidgetTint is the widgets' own colour (Settings › Colours), when WidgetTintOn
	WidgetTint   [3]uint8
	WidgetTintOn bool
	WidgetTintBy map[string]WidgetTint
	WallTone     [3]uint8 // the wallpaper's main colour: what shows through see-through widgets
	HasWallTone  bool
	CardStd      bool // Menus and panels: Standard: the dock's cards and labels are Windows' black or white
	ClockDigital bool // round icons: the clock shows digits, not hands
	WidgetDG     bool // DG style widgets (dot-matrix, line glyphs, a red accent) // a widget\'s own colour, by widget ("clock"…), over the one above
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

	// dock flyout
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

	// the flyout's animated shape: size, gap, card-ness (1 card, 0 name tag) and how
	// settled it is (text fades in as it reaches 1)
	popAW, popAH, popGapNow, popCard, popMorph float64
	popMoving                                  bool
	SearchT                                    float64 // the Launchpad arrow growing in

	lastStep                         time.Time
	baseLeft, baseSlots, layoutSlots []float64
}

func (d *Dock) SetMetrics(scale float64, iconSize int, mag float64, appPadding int, connected, tiles bool, shape string) {
	d.Pill = true
	d.PillRound = shape == "pill"
	d.Connected = connected
	d.Scale = scale
	d.Tiles = tiles
	// Popovers and widgets are supported with every dock shape.
	d.Notch = true
	d.B = math.Round(float64(iconSize) * scale)
	// Keep the gap between app slots consistent across icon styles. Artwork
	// masks can change the visible icon bounds, but must not change slot spacing.
	d.G = math.Round((3 + float64(max(0, min(appPadding, 20)))) * scale)
	// Widgets have their own fixed visual rhythm. It stays independent from
	// app padding so Clock, Battery, Now Playing and System tray stay evenly spaced.
	d.WidgetGap = d.G // until the widget spacing setting is applied
	d.P = math.Round(d.B*0.10 + 4*scale)
	d.M = math.Round(4 * scale)
	if d.PageN > 1 {
		d.M = math.Round(9 * scale) // just enough room for the page dots under the pills
	}
	d.Mag = mag
	d.PlateH = d.B + 2*d.P
}

func (d *Dock) slotGap(it *Item) float64 {
	if it.Kind == KindWidget {
		// widgets use the same spacing scale as app icons, so equal settings give
		// equal gaps across the whole dock
		return d.WidgetGap
	}
	return d.G
}

// Length along the dock and thickness across it, in window pixels.
func (d *Dock) L() float64 {
	if d.Horizontal() {
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
	case OrientTop:
		return u, v
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
	case OrientTop:
		return x, y
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

// isTag: a plain hover label (no rows, previews or player), shown as a small name tag.
func (pc PopContent) isTag() bool {
	return pc.Mode == 0 && len(pc.Rows) == 0
}

func (d *Dock) popSize(pc PopContent) (float64, float64) {
	s := d.Scale
	tp, sp, rp, rowH, pad := d.popPx()
	if pc.isTag() {
		w := 2 * 10 * s
		if d.Measure != nil {
			w += d.Measure(pc.Title, tp)
		}
		return math.Min(w, 260*s), math.Round(float64(tp)*1.3 + 12*s)
	}
	if pc.Mode == PopMedia || pc.Mode == PopDrive {
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
	if pc.Mode == PopWeather && pc.Weather != nil {
		return d.weatherCardSize(pc)
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
		limit := maxPopRows
		if pc.Mode == PopTray {
			limit = maxGrid
		}
		for i, r := range pc.Rows {
			if i >= limit {
				break
			}
			w = math.Max(w, d.Measure(r.Title, rp)+14*s)
		}
	}
	w = math.Min(math.Max(w+2*pad, 180*s), 340*s)
	h := 12*s + float64(tp)*1.3 + 2*s + float64(sp)*1.3 + 10*s
	n := len(pc.Rows)
	limit := maxPopRows
	if pc.Mode == PopTray {
		limit = maxGrid
	}
	if n > limit {
		n = limit
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
		if d.Horizontal() {
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

// popTextA is how visible the flyout's text is: it fades in as the card opens and
// again after the card changes shape.
func (d *Dock) popTextA() float64 {
	return math.Max(0, math.Min((d.PopT-0.35)/0.3, 1)) * d.popMorph // text arrives sooner, so the card never sits empty
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
		return math.Round(2*d.P + 8*d.Scale) // compact, even gap between dock sections
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
		return (d.B*d.widgetWidth(it) + d.slotGap(it)) * it.Appear
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

	// hiding drops away quickly; showing springs back with a small bounce at the top,
	// unless the hide spring (or all animation) is switched off
	nh := d.HideOffset
	switch {
	case d.HideTarget > 0: // hiding: quick, no bounce
		nh, d.hideV = approach(d.HideOffset, d.HideTarget, dt, 0.045), 0
	case d.Calm || d.noHideSpring: // showing without a spring
		nh, d.hideV = approach(d.HideOffset, d.HideTarget, dt, 0.06), 0
	case d.HideOffset != 0 || d.hideV != 0: // showing with the spring bounce
		springStep(&nh, &d.hideV, 0, dt, 230, 19)
		if math.Abs(nh) < 0.15 && math.Abs(d.hideV) < 3 {
			nh, d.hideV = 0, 0
		}
	}
	if nh != d.HideOffset {
		d.HideOffset = nh
		changed = true
	}
	if d.stepSprings(now, dt) {
		changed = true
	}
	d.PlateV0 = d.M - d.HideOffset
	d.PlateV1 = d.PlateV0 + d.PlateH
	d.mu, d.mv = d.toUV(d.MouseX, d.MouseY)
	L := d.L()

	n := len(d.Items)
	if cap(d.baseLeft) < n {
		d.baseLeft = make([]float64, n)
	} else {
		d.baseLeft = d.baseLeft[:n]
	}
	if cap(d.baseSlots) < n {
		d.baseSlots = make([]float64, n)
	} else {
		d.baseSlots = d.baseSlots[:n]
	}
	baseLeft, baseSlots := d.baseLeft, d.baseSlots
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
	pagedIn, pagedTotal := 0, 0
	for _, it := range d.Items {
		if it.Paged && !it.PageOut && !it.Leaving {
			pagedTotal++
		}
	}
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
			// the new page arrives as a quick wave, in the direction you scrolled
			if !d.Calm && !d.noStagger && !d.PageT0.IsZero() && it.Appear < 0.02 {
				k := pagedIn
				if d.PageDir < 0 {
					k = pagedTotal - 1 - pagedIn
				}
				if since(now, d.PageT0) < time.Duration(k)*pageStagger {
					target = it.Appear // not its turn yet
				}
			}
			pagedIn++
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

	if cap(d.layoutSlots) < n {
		d.layoutSlots = make([]float64, n)
	} else {
		d.layoutSlots = d.layoutSlots[:n]
	}
	slots := d.layoutSlots
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
			slots[i] = (d.B*d.widgetWidth(it) + d.slotGap(it)) * it.Appear
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
		it.Lift, it.Sq = 0, 0
		if it.Bouncing {
			const cycle = 0.62
			t := since(now, it.BounceStart).Seconds()
			cyc := math.Floor(t / cycle)
			if cyc >= 1 && (it.Running || cyc >= 7) {
				it.Bouncing = false
			} else if d.Calm || d.noJelly {
				ph := math.Mod(t, cycle) / cycle
				it.Lift = d.B * 0.42 * math.Sin(math.Pi*ph)
			} else {
				// jelly: squashes wide, springs tall, settles — once per cycle while the app starts
				it.Sq = jelly(math.Mod(t, cycle)/cycle, 0.18)
			}
			changed = true
		}
		if !it.JellyStart.IsZero() { // an icon landing after a drop
			t := since(now, it.JellyStart).Seconds() / 0.45
			if t >= 1 || d.Calm {
				it.JellyStart = time.Time{}
			} else {
				it.Sq += jelly(t, it.JellyAmp)
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

	// the Launchpad's arrow grows in once its card is on screen (SearchGap is set then)
	st := 0.0
	if d.SearchItem != nil && d.SearchGap > 0 {
		st = approach(d.SearchT, 1, dt, 0.05)
	}
	if st != d.SearchT {
		d.SearchT = st
		changed = true
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
	if !d.Horizontal() {
		along, across = ch, cw
	}
	gap, card := d.popGap(), 1.0
	if d.Pop.isTag() {
		gap, card = math.Round(10*s), 0 // a name tag sits closer, with no arrow
	}
	// Moving between a card and a name tag (or two different cards) morphs the box
	// rather than snapping it; the text fades back in once the shape has settled.
	if d.popAW == 0 || d.PopT < 0.05 {
		d.popAW, d.popAH, d.popGapNow, d.popCard = along, across, gap, card
	} else {
		ow, oh, og, oc := d.popAW, d.popAH, d.popGapNow, d.popCard
		d.popAW = approach(d.popAW, along, dt, 0.045)
		d.popAH = approach(d.popAH, across, dt, 0.045)
		d.popGapNow = approach(d.popGapNow, gap, dt, 0.045)
		d.popCard = approach(d.popCard, card, dt, 0.045)
		if math.Abs(d.popAW-along) < 0.5 && math.Abs(d.popAH-across) < 0.5 {
			d.popAW, d.popAH = along, across
		}
		if math.Abs(d.popGapNow-gap) < 0.3 {
			d.popGapNow = gap
		}
		d.popMoving = d.popAW != ow || d.popAH != oh || d.popGapNow != og || d.popCard != oc
		changed = changed || d.popMoving
	}
	d.popMorph = 1 - math.Min((math.Abs(d.popAW-along)+math.Abs(d.popAH-across))/(18*s), 1)
	e := d.popEase()
	it := d.PopItem
	slot := math.Max(it.Size, d.B)
	aw := slot + (d.popAW-slot)*e
	// floating card above the dock, joined to its icon by a soft arrow
	topAt := d.PlateV0 + math.Max(d.PlateH, d.P+it.Size+d.P*0.9)
	base := topAt + d.popGapNow*e
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
	d.PopV1 = base + d.popAH*e
	// final box for text
	fb := topAt + gap
	x, y, w, h := d.rectUV(cu-along/2, fb, cu+along/2, fb+across)
	d.PopX0, d.PopY0, d.PopX1, d.PopY1 = x, y, x+w, y+h
	_, _, _, rowH, pad := d.popPx()
	ry := y + d.popHeader() + 6*s
	hot := -1
	inside := func(x0, y0, x1, y1 float64) bool {
		return d.PopInside && d.MouseX >= x0 && d.MouseX <= x1 && d.MouseY >= y0 && d.MouseY < y1
	}
	if d.Pop.Mode == PopMedia || d.Pop.Mode == PopDrive {
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
			limit := maxPopRows
			if d.Pop.Mode == PopTray {
				limit = maxGrid
			}
			if i >= limit {
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
	if d.PopItem != nil && (math.Abs(d.PopCU-d.PopItem.U) > 0.5 || d.popMoving) {
		return true
	}
	if d.SearchItem != nil && d.SearchGap > 0 && d.SearchT < 1 {
		return true
	}
	if d.Active || d.Drag != nil || math.Abs(d.HideOffset-d.HideTarget) > 0.01 || d.hideV != 0 {
		return true
	}
	for _, it := range d.Items {
		if it.Bouncing || !it.JellyStart.IsZero() || (it.Appear < 1 && !(it.Leaving && it.Appear == 0)) {
			return true
		}
		if it.springing {
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
	k := d.WidgetScale
	if k <= 0 || (d.CircleIcons && it.Widget != "media") {
		k = 1 // round widgets stay round
	}
	return d.widgetWidth0(it) * k
}

func (d *Dock) widgetWidth0(it *Item) float64 {
	if d.CircleIcons && (it.Widget == "clock" || it.Widget == "battery" || it.Widget == "status" || it.Widget == "tray" || it.Widget == "weather") {
		return 1.0
	}
	if d.Horizontal() {
		if it.Widget == "media" {
			return 1 + 1.7*smooth01(it.WWave)
		}
		return it.WidthB
	}
	switch it.Widget {
	case "clock":
		return 1.0
	case "battery", "status", "weather":
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

// parseHex reads "#rrggbb" (or "rrggbb").
func parseHex(s string) ([3]uint8, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return [3]uint8{}, false
	}
	var c [3]uint8
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return [3]uint8{}, false
		}
		c[i] = uint8(v)
	}
	return c, true
}

func hexOf(c [3]uint8) string { return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]) }

// luma is perceived brightness, 0–255.
func luma(c [3]uint8) float64 { return 0.299*float64(c[0]) + 0.587*float64(c[1]) + 0.114*float64(c[2]) }

func mixC(c, to [3]uint8, t float64) [3]uint8 {
	var o [3]uint8
	for i := range c {
		o[i] = uint8(math.Round(float64(c[i]) + (float64(to[i])-float64(c[i]))*t))
	}
	return o
}

// readableAccent nudges a colour until it stands out on a dark (or light) UI: a dark
// dock colour becomes a lighter shade of itself for highlights, and so on.
func readableAccent(c [3]uint8, darkUI bool) [3]uint8 {
	for i := 0; i < 12; i++ {
		l := luma(c)
		if darkUI && l >= 120 || !darkUI && l <= 150 {
			break
		}
		if darkUI {
			c = mixC(c, [3]uint8{255, 255, 255}, 0.18)
		} else {
			c = mixC(c, [3]uint8{0, 0, 0}, 0.18)
		}
	}
	return c
}

// onColor is the text colour that reads on c.
func onColor(c [3]uint8) string {
	if !wantsDarkText(c) {
		return "#ffffff"
	}
	return "#000000"
}

// Palette is the dock's colour scheme: dark, light, or the taskbar's accent colour.
type Palette struct {
	Fill  [3]uint8 // pill and card background
	Alpha float64
	Light bool // light background, so dark text and marks
}

// cardFillC is the background of the dock's cards and name tags.
func (d *Dock) cardFillC() color.RGBA {
	if d.CardStd { // Menus and panels: Standard: Windows' own dark or light, not the dock colour
		if d.Dark {
			return pc(40, 40, 42, 0.97)
		}
		return pc(249, 249, 250, 0.97)
	}
	return d.fillC()
}

func (d *Dock) fillC() color.RGBA {
	p := d.Pal
	if p.Alpha == 0 {
		return pc(4, 4, 6, 0.97)
	}
	return pc(p.Fill[0], p.Fill[1], p.Fill[2], p.Alpha)
}

func (d *Dock) dockFillC() color.RGBA {
	p := d.Pal
	alpha := p.Alpha
	if alpha == 0 {
		alpha = 0.97
	}
	transparency := max(0, min(d.Transparency, 100))
	alpha *= float64(100-transparency) / 100
	return pc(p.Fill[0], p.Fill[1], p.Fill[2], alpha)
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

const (
	pageSlideDur = 340 * time.Millisecond
	pageStagger  = 32 * time.Millisecond // between one icon and the next arriving
)

// AnimSlow stretches every animation: 1 = normal (speed 5), 2.2 = slowest (speed 1),
// 0.2 = five times faster (speed 10).
var AnimSlow = 1.0

// AnimSpeedFactor turns the 1–10 speed setting into AnimSlow. 1–4 slow things down;
// 6–10 speed them up, evenly, to five times normal at 10.
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
	if speed > 5 && speed <= 10 {
		return 1 / math.Pow(5, float64(speed-5)/5)
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

// Horizontal is true for a dock along the bottom or top of the screen.
func (d *Dock) Horizontal() bool { return d.Orient == OrientBottom || d.Orient == OrientTop }

// pillOrder is the order of the dock's pills, with anything missing added in the usual place.
func pillOrder(saved []string) []string {
	def := []string{"apps", "running", "files", "widgets"}
	seen := map[string]bool{}
	var out []string
	for _, g := range saved {
		for _, d := range def {
			if g == d && !seen[g] {
				out = append(out, g)
				seen[g] = true
			}
		}
	}
	for _, d := range def {
		if !seen[d] {
			out = append(out, d)
		}
	}
	return out
}

// SetAnim copies the animation preferences from the config onto the dock.
func (d *Dock) SetAnim(c *Config) {
	d.Calm = c.NoBounce
	d.noLift = c.NoHoverLift
	d.noCardSpring = c.NoCardSpring
	d.noStagger = c.NoPageStagger
	d.noJelly = c.NoJelly
	d.noHideSpring = c.NoHideSpring
	d.noDropSettle = c.NoDropSettle
}

// springStep advances a damped spring (stiffness k, damping c) towards target.
func springStep(x, v *float64, target, dt, k, c float64) {
	for dt > 0 { // small steps keep it stable when a frame runs long
		h := math.Min(dt, 1.0/120)
		*v += (k*(target-*x) - c**v) * h
		*x += *v * h
		dt -= h
	}
}

// jelly is a squash that swings wide, then tall, and dies away over t = 0..1.
func jelly(t, amp float64) float64 {
	if t <= 0 || t >= 1 {
		return 0
	}
	return amp * math.Exp(-3.2*t) * math.Sin(2*math.Pi*1.7*t) * 1.5 * (1 - t*t*t)
}

// popEase is how open the flyout is: it grows out of its icon with a small overshoot
// and shrinks straight back when closing.
func (d *Dock) popEase() float64 {
	t := d.PopT
	if d.Calm || d.noCardSpring || d.PopTarget < 1 || t <= 0 || t >= 1 {
		return easeOut(t)
	}
	const c1 = 1.25 // overshoot of about 4%
	u := t - 1
	return 1 + (c1+1)*u*u*u + c1*u*u
}

// stepSprings moves the per-icon springs: the hover lift (the icon under the pointer
// rises, its neighbours nudge) and the running dot popping in.
func (d *Dock) stepSprings(now time.Time, dt float64) bool {
	moved := false
	hov := -1
	if !d.Calm && !d.noLift && d.Active && d.Drag == nil && !d.Frozen && d.HideOffset < 1 && d.Hover >= 0 && d.Hover < len(d.Items) {
		if k := d.Items[d.Hover].Kind; k != KindSep && k != KindGap && k != KindWidget {
			hov = d.Hover
		}
	}
	for i, it := range d.Items {
		if it.Kind == KindSep || it.Kind == KindGap || it.Kind == KindWidget {
			continue
		}
		target := 0.0
		switch {
		case hov < 0:
		case i == hov:
			target = 1
		case i == hov-1 || i == hov+1:
			target = 0.3
		}
		it.springing = false
		if it.HovT != target || it.hovV != 0 {
			springStep(&it.HovT, &it.hovV, target, dt, 300, 22)
			if math.Abs(it.HovT-target) < 0.002 && math.Abs(it.hovV) < 0.02 {
				it.HovT, it.hovV = target, 0
			}
			moved, it.springing = true, it.hovV != 0
		}
		dot := 0.0
		if it.Running {
			dot = 1
		}
		if d.Calm || d.noJelly {
			it.DotT, it.dotV = dot, 0
		} else if it.DotT != dot || it.dotV != 0 {
			springStep(&it.DotT, &it.dotV, dot, dt, 320, 17)
			if math.Abs(it.DotT-dot) < 0.004 && math.Abs(it.dotV) < 0.03 {
				it.DotT, it.dotV = dot, 0
			}
			moved = true
			it.springing = it.springing || it.dotV != 0
		}
	}
	return moved
}

// ---------- colours from the wallpaper ----------

// satOf is a colour's saturation, 0–1.
func satOf(c [3]uint8) float64 {
	mx := math.Max(float64(c[0]), math.Max(float64(c[1]), float64(c[2])))
	mn := math.Min(float64(c[0]), math.Min(float64(c[1]), float64(c[2])))
	if mx == 0 {
		return 0
	}
	return (mx - mn) / mx
}

func colourDist(a, b [3]uint8) float64 {
	dr, dg, db := float64(a[0])-float64(b[0]), float64(a[1])-float64(b[1]), float64(a[2])-float64(b[2])
	return math.Sqrt(dr*dr*0.3 + dg*dg*0.59 + db*db*0.11)
}

// wallpaperPalette picks up to n colours from an image: the main colour first, then
// the ones that stand out (common and colourful), all clearly different from each other.
func wallpaperPalette(img image.Image, n int) [][3]uint8 {
	if img == nil || n <= 0 {
		return nil
	}
	b := img.Bounds()
	if b.Empty() {
		return nil
	}
	type bucket struct {
		n        float64
		r, g, bl float64
	}
	var hist [4096]bucket // 4 bits a channel
	const grid = 96
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x := b.Min.X + (gx*b.Dx()+b.Dx()/2)/grid
			y := b.Min.Y + (gy*b.Dy()+b.Dy()/2)/grid
			r, g, bl, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			r8, g8, b8 := r>>8, g>>8, bl>>8
			k := (r8>>4)<<8 | (g8>>4)<<4 | b8>>4
			h := &hist[k]
			h.n++
			h.r += float64(r8)
			h.g += float64(g8)
			h.bl += float64(b8)
		}
	}
	type cand struct {
		c     [3]uint8
		n     float64
		score float64
	}
	var cs []cand
	for _, h := range hist {
		if h.n == 0 {
			continue
		}
		c := [3]uint8{uint8(h.r / h.n), uint8(h.g / h.n), uint8(h.bl / h.n)}
		cs = append(cs, cand{c, h.n, h.n * (0.25 + satOf(c))})
	}
	if len(cs) == 0 {
		return nil
	}
	// merge each candidate's near neighbours into it, so a gradient counts as one colour
	// (worked out once: ~the square of a few thousand buckets)
	wN, wS := make([]float64, len(cs)), make([]float64, len(cs))
	for i := range cs {
		for j := range cs {
			if colourDist(cs[i].c, cs[j].c) < 28 {
				wN[i] += cs[j].n
				wS[i] += cs[j].score
			}
		}
	}
	var out [][3]uint8
	taken := func(c [3]uint8) bool {
		for _, o := range out {
			if colourDist(c, o) < 48 {
				return true
			}
		}
		return false
	}
	pick := func(w []float64) bool {
		best, bw := -1, 0.0
		for i, o := range cs {
			if !taken(o.c) && w[i] > bw {
				best, bw = i, w[i]
			}
		}
		if best < 0 {
			return false
		}
		out = append(out, cs[best].c)
		return true
	}
	pick(wN) // the main colour
	for len(out) < n && pick(wS) {
	}
	return out
}

// wallDockColour is the dock colour that matches a wallpaper palette: its main colour.
func wallDockColour(pal [][3]uint8) ([3]uint8, bool) {
	if len(pal) == 0 {
		return [3]uint8{}, false
	}
	return pal[0], true
}

// wallWidgetColour is the widget colour that matches a wallpaper: its second colour,
// so the widgets stand apart from a dock that matches the main one.
func wallWidgetColour(pal [][3]uint8) ([3]uint8, bool) {
	if len(pal) > 1 {
		return pal[1], true
	}
	return wallDockColour(pal)
}

// wallAccent is the most vivid colour in a wallpaper palette (the main colour if none is).
func wallAccent(pal [][3]uint8) ([3]uint8, bool) {
	best, bs := -1, 0.0
	for i, c := range pal {
		l := luma(c)
		s := satOf(c) * (1 - math.Abs(l-140)/200) // colourful, and not near black or white
		if s > bs {
			best, bs = i, s
		}
	}
	if best < 0 || bs < 0.12 {
		return wallDockColour(pal)
	}
	return pal[best], true
}

// weatherCardMetrics: padding, the big glyph, the header's height, a forecast column's
// width and the forecast row's height.
func (d *Dock) weatherCardMetrics() (pad, glyph, head, col, row float64) {
	s := d.Scale
	_, _, rp, _, p := d.popPx()
	return p, math.Round(46 * s), math.Round(62 * s), math.Round(58 * s), float64(rp)*2.2 + 58*s
}

func (d *Dock) weatherCardSize(pc PopContent) (float64, float64) {
	s := d.Scale
	pad, _, head, col, row := d.weatherCardMetrics()
	n := len(pc.Weather.Days)
	w := math.Max(float64(n)*col+2*pad, 300*s)
	h := 10*s + head + 8*s
	if n > 0 {
		h += row + 6*s
	}
	return w, h
}

// WidgetTint is a widget's own colour (On false: the dock's soft tint).
type WidgetTint struct {
	C  [3]uint8
	On bool
}

// widgetTint is the colour a widget is drawn in.
func (d *Dock) widgetTint(w string) ([3]uint8, bool) {
	if t, ok := d.WidgetTintBy[w]; ok {
		return t.C, t.On
	}
	return d.WidgetTint, d.WidgetTintOn
}

// wantsDarkText: black text reads at least as well as white on c. Light colours, and
// strong mid-bright ones like a vivid red or orange, which read better in black.
func wantsDarkText(c [3]uint8) bool {
	if luma(c) > 150 {
		return true
	}
	cb, cw := contrast(c, [3]uint8{0, 0, 0}), contrast(c, [3]uint8{255, 255, 255})
	return cb >= 3.5 && cb >= 0.7*cw
}

// contrast is the WCAG contrast ratio of two colours (1–21).
func contrast(a, b [3]uint8) float64 {
	lin := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	L := func(c [3]uint8) float64 { return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2]) }
	x, y := L(a)+0.05, L(b)+0.05
	if x < y {
		x, y = y, x
	}
	return x / y
}

// readableOn moves c towards white or black (whichever bg is further from) until it
// stands out on bg (3:1, the bar for icons and marks), keeping its hue.
func readableOn(c, bg [3]uint8) [3]uint8 {
	if contrast(c, bg) >= 3 {
		return c
	}
	t := 255.0
	if luma(bg) > 140 {
		t = 0
	}
	out := c
	for k := 0.1; k <= 1.0001 && contrast(out, bg) < 3; k += 0.1 {
		for i := range out {
			out[i] = uint8(math.Round(float64(c[i])*(1-k) + t*k))
		}
	}
	return out
}
