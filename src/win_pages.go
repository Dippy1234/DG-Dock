//go:build windows

package main

// Pages of apps, and dock colours that follow the Windows taskbar.

import (
	"time"
)

const defaultPerPage = 10

// perPage is how many apps one page shows (0 = all of them on one page).
func (a *App) perPage() int {
	switch n := a.cfg.AppsPerPage; {
	case n < 0:
		return 0
	case n == 0:
		return defaultPerPage
	default:
		return n
	}
}

// pageItems splits the left-hand apps into pages. Start stays put on every page.
// While the page changes, the old page's icons (a.pageOut) stay in the dock shrinking
// away on the side they leave from, as the new page's icons grow in on the other side,
// so the pill resizes smoothly to fit whatever is on the new page.
func (a *App) pageItems(left []*Item, used map[string]bool) []*Item {
	var fixed, pageable []*Item
	for _, it := range left {
		if it.Kind == KindStart {
			fixed = append(fixed, it)
		} else {
			pageable = append(pageable, it)
		}
	}
	per := a.perPage()
	pages := 1
	if per > 0 && len(pageable) > per {
		per = max(per, (len(pageable)+1)/2) // two pages at most: pages fill up instead
		pages = (len(pageable) + per - 1) / per
	}
	if a.page >= pages {
		a.page = pages - 1
	}
	if a.page < 0 {
		a.page = 0
	}
	a.setPageCount(pages)
	a.dock.PageCur = a.page
	for _, it := range pageable {
		it.Paged = false
	}
	vis := pageable
	if pages > 1 {
		lo := a.page * per
		hi := min(lo+per, len(pageable))
		vis = pageable[lo:hi]
		for _, it := range pageable {
			used[it.Key] = true // keep icons of other pages loaded
		}
	}
	for _, it := range vis {
		it.Paged = pages > 1
		it.PageOut = false
	}
	var out []*Item
	for _, it := range a.pageOut {
		if it.PageOut {
			out = append(out, it)
			used[it.Key] = true
		}
	}
	// the pages flow sideways: going forward the new page comes in from the left and
	// the old one leaves to the right; going back it's the other way round
	res := append([]*Item{}, fixed...)
	if a.pageDir > 0 {
		res = append(res, vis...)
		res = append(res, out...)
	} else {
		res = append(res, out...)
		res = append(res, vis...)
	}
	return res
}

// pageSize is how many apps a page really holds: the setting, or more if that would
// need a third page.
func (a *App) pageSize() int {
	per := a.perPage()
	n := 0 // pinned apps that go on pages (everything in the apps pill except Start)
	for i := range a.cfg.Pins {
		if it := a.items[pinKey(&a.cfg.Pins[i])]; it != nil && it.Section == 0 && it.Kind != KindStart {
			n++
		}
	}
	if per > 0 && n > per {
		per = max(per, (n+1)/2)
	}
	return per
}

func (a *App) setPageCount(n int) {
	if n == a.dock.PageN {
		return
	}
	wasPaged := a.dock.PageN > 1
	a.dock.PageN = n
	if (n > 1) != wasPaged {
		a.relayoutSoon = true // the pills move up to make room for the dots
	}
}

// flipPage moves to the next (+1) or previous (-1) page.
func (a *App) flipPage(dir int) {
	d := a.dock
	if d.PageN <= 1 {
		return
	}
	np := a.page + dir
	if np < 0 || np >= d.PageN {
		return
	}
	// anything still shrinking from an earlier flip goes now
	for _, it := range a.pageOut {
		it.PageOut = false
	}
	a.pageOut = a.pageOut[:0]
	for _, it := range d.Items {
		if it.Paged && !it.Leaving {
			it.PageOut = true
			a.pageOut = append(a.pageOut, it)
		}
	}
	a.page, a.pageDir = np, dir
	d.PageDir = dir
	d.PopItem, d.PopT, d.PopTarget = nil, 0, 0
	a.rebuildItems()
	// open apps trade places right away (not at the next window scan): ones from the
	// page you left move into the open-apps pill, ones on the new page go home
	a.assignRunning(a.lastWins)
	for _, it := range d.Items {
		if it.Paged && !it.PageOut {
			it.Appear = 0 // grows in
		}
	}
	d.PageT0 = time.Now()
	a.needRender = true
	a.setAnimInterval(15)
}

// finishPageOut drops the old page's icons once they've shrunk away.
func (a *App) finishPageOut() {
	if len(a.pageOut) == 0 {
		return
	}
	for _, it := range a.pageOut {
		if it.PageOut && it.Appear > 0.01 {
			return
		}
	}
	for _, it := range a.pageOut {
		it.PageOut = false
		it.Appear = 1 // full size next time its page is shown (flipPage sets 0 to grow it in)
	}
	a.pageOut = a.pageOut[:0]
	a.rebuildItems()
}

// pageWheel turns pages with the wheel over the apps pill. Touchpads send lots of
// small steps, so they add up and a page turns at most every 280 ms.
func (a *App) pageWheel(delta int) bool {
	d := a.dock
	if d.PageN <= 1 {
		return false
	}
	cx, cy := getCursorPos()
	u, v := d.toUV(float64(cx-a.winX), float64(cy-a.winY))
	gs := d.pillGroups()
	if len(gs) == 0 || u < gs[0][0] || u > gs[0][1] || v < 0 || v > d.PlateV1+4*d.Scale {
		return false
	}
	a.pageAcc += delta
	if a.pageAcc > -100 && a.pageAcc < 100 {
		return true
	}
	if time.Since(a.pageAt) < 360*time.Millisecond {
		a.pageAcc = 0
		return true
	}
	dir := 1
	if a.pageAcc > 0 {
		dir = -1
	}
	a.pageAcc = 0
	a.pageAt = time.Now()
	a.flipPage(dir)
	return true
}

// dockPalette picks the dock colours: the taskbar's own colours when following
// Windows (including the accent colour if "Show accent colour on Start and
// taskbar" is on), or plain light/dark when the theme is forced.
func (a *App) dockPalette(dark bool) Palette {
	const pers = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	if a.cfg.Theme != "dark" && a.cfg.Theme != "light" && dark {
		if v, ok := readDWORD(pers, "ColorPrevalence"); ok && v == 1 {
			if c, ok := readDWORD(`Software\Microsoft\Windows\CurrentVersion\Explorer\Accent`, "StartColorMenu"); ok {
				r, g, b := uint8(c), uint8(c>>8), uint8(c>>16)
				lum := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
				return Palette{Fill: [3]uint8{r, g, b}, Alpha: 0.96, Light: lum > 0.62}
			}
		}
	}
	if dark {
		return Palette{Fill: [3]uint8{32, 32, 34}, Alpha: 0.95}
	}
	return Palette{Fill: [3]uint8{243, 243, 245}, Alpha: 0.94, Light: true}
}
