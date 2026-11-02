//go:build windows

package main

// Now Playing: reads the current media session from Windows (the same source as the
// volume flyout / lock screen), so it works with Spotify, YouTube Music in Chrome/Edge,
// browsers, VLC, Apple Music and anything else that reports to Windows.

import (
	"bytes"
	"image"
	"image/draw"
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

var (
	combase                          = windows.NewLazySystemDLL("combase.dll")
	pRoInitialize                    = combase.NewProc("RoInitialize")
	pRoGetActivationFactory          = combase.NewProc("RoGetActivationFactory")
	pWindowsCreateString             = combase.NewProc("WindowsCreateString")
	pWindowsDeleteString             = combase.NewProc("WindowsDeleteString")
	pWindowsGetStringRawBuffer       = combase.NewProc("WindowsGetStringRawBuffer")
	shcoreDLL                        = windows.NewLazySystemDLL("shcore.dll")
	pCreateStreamOverRandomAccessStr = shcoreDLL.NewProc("CreateStreamOverRandomAccessStream")

	IID_GSMTCManagerStatics = mustGUID("{2050C4EE-11A0-57DE-AED7-C97C70338245}")
	IID_IAsyncInfo          = mustGUID("{00000036-0000-0000-C000-000000000046}")
	IID_IStream             = mustGUID("{0000000C-0000-0000-C000-000000000046}")
)

const wmMedia = WM_APP + 8

type mediaState struct {
	Has     bool
	Title   string
	Artist  string
	Album   string
	AUMID   string
	Playing bool
	Art     *image.RGBA // premultiplied BGRA, square
	artKey  string
}

type mediaWatcher struct {
	mu    sync.Mutex
	cur   mediaState
	poke  chan struct{}
	hwnd  uintptr
	alive bool
}

func hstr(s string) uintptr {
	u, _ := windows.UTF16FromString(s)
	var h uintptr
	pWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h)))
	return h
}

func hstrString(h uintptr) string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := pWindowsGetStringRawBuffer.Call(h, uintptr(unsafe.Pointer(&n)))
	s := ""
	if p != 0 && n > 0 {
		s = windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(p)), n))
	}
	pWindowsDeleteString.Call(h)
	return s
}

// awaitOp waits for a WinRT IAsyncOperation and returns its result (or 0).
func awaitOp(op uintptr, timeout time.Duration) uintptr {
	if op == 0 {
		return 0
	}
	defer comRelease(op)
	var info uintptr
	if hr := comCall(op, 0, uintptr(unsafe.Pointer(&IID_IAsyncInfo)), uintptr(unsafe.Pointer(&info))); !hresultOK(hr) || info == 0 {
		return 0
	}
	defer comRelease(info)
	deadline := time.Now().Add(timeout)
	for {
		var st int32
		comCall(info, 7, uintptr(unsafe.Pointer(&st))) // get_Status
		switch st {
		case 1: // Completed
			var res uintptr
			if hr := comCall(op, 8, uintptr(unsafe.Pointer(&res))); hresultOK(hr) { // GetResults
				return res
			}
			return 0
		case 2, 3: // Canceled, Error
			return 0
		}
		if time.Now().After(deadline) {
			comCall(info, 9) // Cancel
			return 0
		}
		time.Sleep(8 * time.Millisecond)
	}
}

func getStr(obj uintptr, idx int) string {
	var h uintptr
	if hr := comCall(obj, idx, uintptr(unsafe.Pointer(&h))); !hresultOK(hr) {
		return ""
	}
	return hstrString(h)
}

// readArt opens the session thumbnail and decodes it into a square icon image.
func readArt(props uintptr) *image.RGBA {
	var ref uintptr
	if hr := comCall(props, 15, uintptr(unsafe.Pointer(&ref))); !hresultOK(hr) || ref == 0 { // Thumbnail
		return nil
	}
	defer comRelease(ref)
	var op uintptr
	if hr := comCall(ref, 6, uintptr(unsafe.Pointer(&op))); !hresultOK(hr) { // OpenReadAsync
		return nil
	}
	ras := awaitOp(op, 3*time.Second)
	if ras == 0 {
		return nil
	}
	defer comRelease(ras)
	var stm uintptr
	if hr, _, _ := pCreateStreamOverRandomAccessStr.Call(ras, uintptr(unsafe.Pointer(&IID_IStream)), uintptr(unsafe.Pointer(&stm))); !hresultOK(hr) || stm == 0 {
		return nil
	}
	defer comRelease(stm)
	var data []byte
	buf := make([]byte, 64*1024)
	for len(data) < 16<<20 {
		var n uint32
		hr := comCall(stm, 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n))) // ISequentialStream::Read
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if !hresultOK(hr) || n == 0 || hr == 1 {
			break
		}
	}
	if len(data) == 0 {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	// centre-crop to a square (video thumbnails are 16:9) and scale to 256
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	sq := image.Rect(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2, 0, 0)
	sq.Max = sq.Min.Add(image.Pt(side, side))
	out := image.NewRGBA(image.Rect(0, 0, 256, 256))
	xdraw.CatmullRom.Scale(out, out.Bounds(), src, sq, draw.Src, nil)
	if swapRB {
		for i := 0; i < len(out.Pix); i += 4 {
			out.Pix[i], out.Pix[i+2] = out.Pix[i+2], out.Pix[i]
		}
	}
	return out
}

func (m *mediaWatcher) run() {
	runtime.LockOSThread()
	defer func() {
		if r := recover(); r != nil {
			app.logf("media watcher stopped: %v", r)
		}
	}()
	pRoInitialize.Call(1) // multithreaded
	if pRoGetActivationFactory.Find() != nil {
		return
	}
	cls := hstr("Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager")
	var statics uintptr
	hr, _, _ := pRoGetActivationFactory.Call(cls, uintptr(unsafe.Pointer(&IID_GSMTCManagerStatics)), uintptr(unsafe.Pointer(&statics)))
	pWindowsDeleteString.Call(cls)
	if !hresultOK(hr) || statics == 0 {
		app.logf("media: no session manager (%x)", hr)
		return
	}
	var op uintptr
	if hr := comCall(statics, 6, uintptr(unsafe.Pointer(&op))); !hresultOK(hr) { // RequestAsync
		return
	}
	mgr := awaitOp(op, 5*time.Second)
	comRelease(statics)
	if mgr == 0 {
		app.logf("media: RequestAsync failed")
		return
	}
	m.alive = true
	for {
		m.poll(mgr)
		select {
		case <-m.poke:
			time.Sleep(250 * time.Millisecond) // let the player react to a button first
		case <-time.After(450 * time.Millisecond):
		}
	}
}

func (m *mediaWatcher) poll(mgr uintptr) {
	st := mediaState{}
	var sess uintptr
	if hr := comCall(mgr, 6, uintptr(unsafe.Pointer(&sess))); hresultOK(hr) && sess != 0 { // GetCurrentSession
		st.Has = true
		st.AUMID = getStr(sess, 6)
		var pinfo uintptr
		if hr := comCall(sess, 9, uintptr(unsafe.Pointer(&pinfo))); hresultOK(hr) && pinfo != 0 { // GetPlaybackInfo
			var status int32
			comCall(pinfo, 7, uintptr(unsafe.Pointer(&status)))
			st.Playing = status == 4
			comRelease(pinfo)
		}
		var pop uintptr
		if hr := comCall(sess, 7, uintptr(unsafe.Pointer(&pop))); hresultOK(hr) { // TryGetMediaPropertiesAsync
			if props := awaitOp(pop, 2*time.Second); props != 0 {
				st.Title = getStr(props, 6)
				st.Artist = getStr(props, 9)
				if st.Artist == "" {
					st.Artist = getStr(props, 8) // AlbumArtist
				}
				st.Album = getStr(props, 10)
				st.artKey = st.AUMID + "|" + st.Title + "|" + st.Artist + "|" + st.Album
				m.mu.Lock()
				prevKey, prevArt := m.cur.artKey, m.cur.Art
				m.mu.Unlock()
				if st.artKey == prevKey {
					st.Art = prevArt
				} else {
					st.Art = readArt(props)
				}
				comRelease(props)
			}
		}
		comRelease(sess)
	}
	m.mu.Lock()
	changed := st.Has != m.cur.Has || st.Title != m.cur.Title || st.Artist != m.cur.Artist || st.Playing != m.cur.Playing ||
		st.AUMID != m.cur.AUMID || st.Art != m.cur.Art
	m.cur = st
	m.mu.Unlock()
	if changed {
		pPostMessageW.Call(m.hwnd, wmMedia, 0, 0)
	}
}

func (m *mediaWatcher) snapshot() mediaState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}

func (m *mediaWatcher) nudge() {
	select {
	case m.poke <- struct{}{}:
	default:
	}
}

// ---------- app side ----------

func (a *App) startMedia() {
	a.mediaW = &mediaWatcher{poke: make(chan struct{}, 1), hwnd: a.hwnd}
	go a.mediaW.run()
}

func (a *App) mediaAppName(aumid string) string {
	if aumid == "" {
		return ""
	}
	if n, ok := a.mediaNames[aumid]; ok {
		return n
	}
	n := shellDisplayName(`shell:AppsFolder\` + aumid)
	if n == "" || strings.Contains(n, "\\") {
		b := aumid
		if i := strings.LastIndexAny(b, `\/`); i >= 0 {
			b = b[i+1:]
		}
		b = strings.TrimSuffix(strings.TrimSuffix(b, ".exe"), ".EXE")
		if i := strings.Index(b, "!"); i > 0 {
			b = b[:i]
		}
		if i := strings.Index(b, "_"); i > 0 {
			b = b[:i]
		}
		if i := strings.LastIndex(b, "."); i > 0 && i < len(b)-1 {
			b = b[i+1:]
		}
		n = b
	}
	a.mediaNames[aumid] = n
	return n
}

// onMedia applies a new Now Playing snapshot from the watcher thread.
func (a *App) onMedia() {
	st := a.mediaW.snapshot()
	a.media = st
	if st.Art != a.mediaArtSrc {
		a.mediaArtSrc = st.Art
		a.mediaArt = nil
		if st.Art != nil {
			a.mediaArt = NewIconSet(st.Art)
		}
	}
	if st.Art != nil && a.mediaAccentSrc != st.Art {
		a.mediaAccentSrc = st.Art
		a.mediaAccent = accentFrom(st.Art)
	}
	if it := a.items["w:media"]; it != nil {
		it.Art = a.mediaArt
		it.WPlaying = st.Playing
		it.WText1 = st.Title
		it.WAccent = a.mediaAccent
	}
	if d := a.dock; d.PopItem != nil && d.PopItem.Widget == "media" {
		hot := d.PopHot
		d.Pop = a.popContent(d.PopItem)
		d.PopHot = hot
	}
	a.needRender = true
	a.render()
}

func (a *App) mediaPop() PopContent {
	st := a.media
	pc := PopContent{Mode: PopMedia, Art: a.mediaArt, Playing: st.Playing}
	if !st.Has || (st.Title == "" && st.Artist == "") {
		pc.Title = "Nothing playing"
		pc.Subtitle = "Play something in Spotify, YouTube Music or any player"
	} else {
		pc.Title = st.Title
		pc.Subtitle = st.Artist
		pc.Line3 = a.mediaAppName(st.AUMID)
		if st.Album != "" && st.Album != st.Title && pc.Line3 != "" {
			pc.Line3 = st.Album + " · " + pc.Line3
		}
	}
	pc.Rows = []PopRow{{Title: "Previous", Path: "media:prev"}, {Title: "Play/Pause", Path: "media:play"}, {Title: "Next", Path: "media:next"}}
	return pc
}

func (a *App) mediaKey(which string) {
	switch which {
	case "media:prev":
		sendKey(0xB1)
	case "media:play":
		sendKey(0xB3)
		// flip the icon straight away; the watcher confirms a moment later
		a.media.Playing = !a.media.Playing
		if it := a.items["w:media"]; it != nil {
			it.WPlaying = a.media.Playing
		}
		if d := a.dock; d.PopItem != nil && d.Pop.Mode == PopMedia {
			d.Pop.Playing = a.media.Playing
		}
		a.needRender = true
	case "media:next":
		sendKey(0xB0)
	}
	if a.mediaW != nil {
		a.mediaW.nudge()
	}
}

// accentFrom picks a bright, saturated colour from cover art for the sound wave.
func accentFrom(img *image.RGBA) [3]uint8 {
	var br, bg, bb, bw float64
	p := img.Pix
	for i := 0; i < len(p); i += 4 * 7 {
		var r, g, b float64
		if swapRB {
			b, g, r = float64(p[i]), float64(p[i+1]), float64(p[i+2])
		} else {
			r, g, b = float64(p[i]), float64(p[i+1]), float64(p[i+2])
		}
		mx := math.Max(r, math.Max(g, b))
		mn := math.Min(r, math.Min(g, b))
		sat := 0.0
		if mx > 0 {
			sat = (mx - mn) / mx
		}
		w := sat*sat*(mx/255) + 0.0001
		br, bg, bb, bw = br+r*w, bg+g*w, bb+b*w, bw+w
	}
	r, g, b := br/bw, bg/bw, bb/bw
	// lift it so it reads on the black dock
	mx := math.Max(r, math.Max(g, b))
	if mx < 1 {
		return [3]uint8{255, 92, 120}
	}
	k := 235 / mx
	if (r+g+b)/3/mx > 0.85 { // greyscale art: fall back to the Music pink
		return [3]uint8{255, 92, 120}
	}
	return [3]uint8{uint8(math.Min(255, r*k)), uint8(math.Min(255, g*k)), uint8(math.Min(255, b*k))}
}

// ---------- live sound level for the wave ----------

var (
	CLSID_MMDeviceEnumerator = mustGUID("{BCDE0395-E52F-467C-8E3D-C4579291692E}")
	IID_IMMDeviceEnumerator  = mustGUID("{A95664D2-9614-4F35-A746-DE8DB63617E6}")
	IID_IAudioMeter          = mustGUID("{C02216F6-8C67-4B5B-9D00-D008E73E0064}")
)

func (a *App) audioPeak() float64 {
	if a.meter == 0 || time.Since(a.meterAt) > 15*time.Second {
		if a.meter != 0 {
			comRelease(a.meter)
			a.meter = 0
		}
		a.meterAt = time.Now()
		var en uintptr
		if hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)), 0, 23,
			uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)), uintptr(unsafe.Pointer(&en))); !hresultOK(hr) || en == 0 {
			return -1
		}
		var dev uintptr
		hr := comCall(en, 4, 0, 0, uintptr(unsafe.Pointer(&dev))) // GetDefaultAudioEndpoint(eRender, eConsole)
		comRelease(en)
		if !hresultOK(hr) || dev == 0 {
			return -1
		}
		hr = comCall(dev, 3, uintptr(unsafe.Pointer(&IID_IAudioMeter)), 23, 0, uintptr(unsafe.Pointer(&a.meter))) // Activate
		comRelease(dev)
		if !hresultOK(hr) {
			a.meter = 0
			return -1
		}
	}
	var f float32
	if hr := comCall(a.meter, 3, uintptr(unsafe.Pointer(&f))); !hresultOK(hr) { // GetPeakValue
		comRelease(a.meter)
		a.meter = 0
		return -1
	}
	return float64(f)
}

// growWave slides the wave (and the tile's width) in and out, every frame.
func (a *App) growWave(it *Item) {
	// the wave slides out when music starts and tucks away just after it stops (a short
	// grace so the gap between tracks doesn't flicker it)
	if it.WPlaying {
		a.waveIdleAt = time.Time{}
	} else if a.waveIdleAt.IsZero() {
		a.waveIdleAt = time.Now()
	}
	want := 0.0
	if it.WPlaying || time.Since(a.waveIdleAt) < 500*time.Millisecond || time.Since(it.WVolAt) < 1400*time.Millisecond {
		want = 1
	}
	if it.WWave != want {
		// time-based, so it runs smoothly at whatever rate frames come
		step := time.Since(a.waveGrowAt).Seconds() / (0.32 * AnimSlow) // about 0.32 s end to end
		if step > 0.2 || a.waveGrowAt.IsZero() {
			step = 0.05
		}
		if want > it.WWave {
			it.WWave = math.Min(want, it.WWave+step)
		} else {
			it.WWave = math.Max(want, it.WWave-step)
		}
		a.animUntil = time.Now().Add(100 * time.Millisecond)
		a.needRender = true
	}
	a.waveGrowAt = time.Now()
}

// updateWave animates the sound-wave bars (about 30 times a second while music plays).
func (a *App) updateWave() {
	it := a.items["w:media"]
	if it == nil || a.hiddenFullscreen {
		return
	}
	a.growWave(it)
	if time.Since(a.waveAt) < 33*time.Millisecond {
		return
	}
	a.waveAt = time.Now()
	t := time.Since(a.waveT0).Seconds()
	peak := 0.0
	if it.WPlaying {
		if p := a.audioPeak(); p >= 0 {
			peak = math.Min(1, math.Sqrt(p)*1.25)
		} else {
			peak = 0.6 // no meter: keep a gentle animation
		}
	}
	moving := false
	for i := range it.WLevels {
		ph := float64(i) * 1.37
		target := 0.12
		if it.WPlaying {
			target = 0.12 + 0.88*peak*(0.35+0.65*math.Abs(math.Sin(t*(5.3+float64(i%5)*1.1)+ph)))
		}
		nv := it.WLevels[i] + (target-it.WLevels[i])*0.45
		if math.Abs(nv-it.WLevels[i]) > 0.004 {
			moving = true
		}
		it.WLevels[i] = nv
	}
	if moving {
		a.needRender = true
	}
}

// showMediaApp brings the player's window forward (clicking the cover art).
func (a *App) showMediaApp() {
	id := strings.ToLower(a.media.AUMID)
	if id == "" {
		return
	}
	for _, it := range a.items {
		if len(it.Windows) == 0 {
			continue
		}
		if strings.EqualFold(it.AUMID, a.media.AUMID) || (it.ExePath != "" && strings.HasSuffix(it.ExePath, `\`+id)) {
			activateWindow(it.Windows[0])
			return
		}
	}
	for _, w := range listAppWindows() {
		if strings.EqualFold(w.AUMID, a.media.AUMID) || strings.HasSuffix(w.Exe, `\`+id) {
			activateWindow(w.Hwnd)
			return
		}
	}
}

var _ = syscall.EINVAL
