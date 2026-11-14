//go:build windows

package main

// The weather widget: where you are, and the forecast there.
//
// Location, in Settings › Widgets › Weather:
//   - This PC's location (default): Windows' own location service, as Windows' apps use
//     it. If location is off for desktop apps, an approximate place from the internet
//     connection instead, marked "Near …".
//   - Home address: a street (never a house number), town and postcode, turned into a
//     place once when saved (OpenStreetMap), then kept on this PC.
// The forecast is from Open-Meteo (no account or key), every 30 minutes.

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

const (
	wmWeatherDone = WM_APP + 48
	wmWeatherHome = WM_APP + 49
	weatherEvery  = 30 * time.Minute
	weatherRetry  = 5 * time.Minute
	weatherUA     = "DG-Dock (https://github.com/Dippy1234/DG-Dock)"
)

var weatherClient = &http.Client{Timeout: 15 * time.Second}

func weatherGet(u string) ([]byte, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", weatherUA) // OpenStreetMap asks apps to say who they are
	req.Header.Set("Accept-Language", "en")
	resp, err := weatherClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// ---------- where ----------

// windowsLocation asks Windows' location service, the way Windows' own apps do. It
// fails if location is off (for this PC, or for desktop apps) in Windows Settings.
func windowsLocation() (lat, lon float64, err error) {
	const script = `Add-Type -AssemblyName System.Device
$w = New-Object System.Device.Location.GeoCoordinateWatcher('Default')
$w.Start()
$n = 0
while (($w.Status -ne 'Ready') -and ($w.Permission -ne 'Denied') -and ($n -lt 120)) { Start-Sleep -Milliseconds 100; $n++ }
$c = $w.Position.Location
if (($w.Permission -eq 'Denied') -or $c.IsUnknown) { 'off' } else {
  $i = [Globalization.CultureInfo]::InvariantCulture
  $c.Latitude.ToString($i) + ',' + $c.Longitude.ToString($i) }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	done := make(chan struct{})
	var out []byte
	go func() { out, err = cmd.Output(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return 0, 0, errors.New("location timed out")
	}
	if err != nil {
		return 0, 0, err
	}
	s := strings.TrimSpace(string(out))
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, errors.New("location off")
	}
	lat, e1 := strconv.ParseFloat(parts[0], 64)
	lon, e2 := strconv.ParseFloat(parts[1], 64)
	if e1 != nil || e2 != nil {
		return 0, 0, errors.New("location off")
	}
	return lat, lon, nil
}

func reversePlace(lat, lon float64) string {
	u := fmt.Sprintf("https://nominatim.openstreetmap.org/reverse?format=jsonv2&zoom=14&addressdetails=1&lat=%.4f&lon=%.4f", lat, lon)
	body, err := weatherGet(u)
	if err != nil {
		return ""
	}
	p, _ := parseOSMReverse(body)
	return p
}

func approxLocation() (lat, lon float64, place string, err error) {
	body, err := weatherGet("https://get.geojs.io/v1/ip/geo.json")
	if err != nil {
		return 0, 0, "", err
	}
	return parseGeoJS(body)
}

// geocodeHome turns a home address (without a house number) into a place.
func geocodeHome(street, town, postcode, country string) (lat, lon float64, place string, err error) {
	q := addressQuery(street, town, postcode, country)
	if q == "" {
		return 0, 0, "", errors.New("enter a town or postcode")
	}
	body, err := weatherGet("https://nominatim.openstreetmap.org/search?format=jsonv2&limit=1&addressdetails=1&q=" + urlQuery(q))
	if err != nil {
		return 0, 0, "", err
	}
	lat, lon, place, err = parseOSMSearch(body)
	if err != nil && street != "" { // a street it doesn't know: try the town and postcode alone
		q2 := addressQuery("", town, postcode, country)
		if q2 != "" && q2 != q {
			if body, e := weatherGet("https://nominatim.openstreetmap.org/search?format=jsonv2&limit=1&addressdetails=1&q=" + urlQuery(q2)); e == nil {
				lat, lon, place, err = parseOSMSearch(body)
			}
		}
	}
	return
}

func urlQuery(s string) string {
	r := strings.NewReplacer(" ", "+", "&", "%26", "#", "%23", "?", "%3F", "/", "%2F", "=", "%3D")
	return r.Replace(s)
}

// fahrenheitByRegion: °F where Windows' region uses US measurements.
func fahrenheitByRegion() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\International`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("iMeasure")
	return v == "1"
}

// ---------- fetching ----------

type weatherJob struct {
	home             bool
	homeLat, homeLon float64
	homePlace        string
	fahrenheit       bool
	lastLat, lastLon float64 // where "this PC" was last time (to skip looking up the name)
	lastPlace        string
	lastApprox       bool
	lastFix          time.Time
}

type weatherResult struct {
	w        Weather
	err      error
	note     string // e.g. why the location is approximate
	lat, lon float64
	fix      time.Time
}

var weatherOut struct {
	sync.Mutex
	res weatherResult
}

func runWeatherJob(j weatherJob) (r weatherResult) {
	var lat, lon float64
	var place string
	approx := false
	switch {
	case j.home:
		lat, lon, place = j.homeLat, j.homeLon, j.homePlace
	case !j.lastFix.IsZero() && time.Since(j.lastFix) < 3*time.Hour:
		// this PC rarely moves: look again every few hours, not every half hour
		lat, lon, place, approx = j.lastLat, j.lastLon, j.lastPlace, j.lastApprox
		r.fix = j.lastFix
	default:
		var err error
		lat, lon, err = windowsLocation()
		if err == nil {
			if math.Abs(lat-j.lastLat) < 0.01 && math.Abs(lon-j.lastLon) < 0.01 && j.lastPlace != "" && !j.lastApprox {
				place = j.lastPlace
			} else {
				place = reversePlace(lat, lon)
			}
		} else {
			r.note = "Windows location is off, so this is approximate"
			approx = true
			if lat, lon, place, err = approxLocation(); err != nil {
				r.err = fmt.Errorf("couldn't find your location (%v)", err)
				return
			}
		}
		r.fix = time.Now()
	}
	r.lat, r.lon = lat, lon
	body, err := weatherGet(forecastURL(lat, lon, j.fahrenheit))
	if err != nil {
		r.err = fmt.Errorf("couldn't get the forecast (%v)", err)
		return
	}
	w, err := parseForecast(body, j.fahrenheit, time.Now())
	if err != nil {
		r.err = err
		return
	}
	w.Place, w.Approx = place, approx
	r.w = w
	return
}

func (a *App) weatherOn() bool {
	for _, w := range a.cfg.HiddenWidgets {
		if w == "weather" {
			return false
		}
	}
	return true
}

func (a *App) weatherFahrenheit() bool {
	switch a.cfg.WeatherUnits {
	case "f":
		return true
	case "c":
		return false
	}
	return fahrenheitByRegion()
}

// refreshWeather fetches the weather in the background, when it's due (or now, if forced).
func (a *App) refreshWeather(force bool) {
	if a.wx.busy || !a.weatherOn() {
		return
	}
	if !force && time.Now().Before(a.wx.next) {
		return
	}
	a.wx.busy = true
	j := weatherJob{home: a.cfg.WeatherLoc == "home", homeLat: a.cfg.WeatherHomeLat, homeLon: a.cfg.WeatherHomeLon, homePlace: a.cfg.WeatherHomePlace,
		fahrenheit: a.weatherFahrenheit(), lastLat: a.wx.lat, lastLon: a.wx.lon, lastFix: a.wx.fix}
	if a.wx.w != nil {
		j.lastPlace, j.lastApprox = a.wx.w.Place, a.wx.w.Approx
	}
	if force {
		j.lastFix = time.Time{} // look again
	}
	if j.home && j.homeLat == 0 && j.homeLon == 0 {
		a.wx.busy = false
		a.wx.err = "Add your home address in Settings › Widgets › Weather"
		a.refreshSettings()
		return
	}
	hwnd := a.hwnd
	go func() {
		r := runWeatherJob(j)
		weatherOut.Lock()
		weatherOut.res = r
		weatherOut.Unlock()
		pPostMessageW.Call(hwnd, wmWeatherDone, 0, 0)
	}()
}

func (a *App) weatherDone() {
	weatherOut.Lock()
	r := weatherOut.res
	weatherOut.Unlock()
	a.wx.busy = false
	if r.err != nil {
		a.wx.err = r.err.Error()
		a.wx.next = time.Now().Add(weatherRetry)
	} else {
		w := r.w
		a.wx.w, a.wx.err, a.wx.note = &w, "", r.note
		a.wx.next = time.Now().Add(weatherEvery)
		if a.cfg.WeatherLoc != "home" {
			a.wx.lat, a.wx.lon, a.wx.fix = r.lat, r.lon, r.fix
		}
	}
	a.applyWeatherToWidget()
	a.refreshSettings()
}

func (a *App) applyWeatherToWidget() {
	it := a.items["w:weather"]
	if it == nil {
		return
	}
	a.fillWeatherItem(it)
	a.needRender = true
	a.render()
}

func (a *App) fillWeatherItem(it *Item) {
	if w := a.wx.w; w != nil {
		it.WText1, it.WCode, it.WNight = tempText(w.Temp), w.Code, !w.IsDay
	} else {
		it.WText1, it.WCode = "", -1 // nothing yet (code 0 would be a clear sky)
	}
}

// weatherPop is the widget's card.
func (a *App) weatherPop() PopContent {
	if w := a.wx.w; w != nil {
		return PopContent{Title: "Weather", Mode: PopWeather, Weather: w}
	}
	switch {
	case a.wx.err != "":
		return PopContent{Title: "Weather: " + a.wx.err}
	case a.wx.busy:
		return PopContent{Title: "Weather: finding your location…"}
	}
	return PopContent{Title: "Weather"}
}

// openWeather opens a fuller forecast: Microsoft's Weather app if it's there, or the web.
func (a *App) openWeather() {
	if k, err := registry.OpenKey(registry.CLASSES_ROOT, `bingweather`, registry.QUERY_VALUE); err == nil {
		k.Close()
		shellOpen("bingweather:", "")
		return
	}
	q := "weather"
	if w := a.wx.w; w != nil && w.Place != "" {
		q += " " + w.Place
	}
	shellOpen("https://www.bing.com/search?q="+urlQuery(q), "")
}

// ---------- home address ----------

type weatherHomeResult struct {
	lat, lon                        float64
	place, err                      string
	street, town, postcode, country string
}

var weatherHomeOut struct {
	sync.Mutex
	res weatherHomeResult
}

// saveWeatherHome checks a home address in the background (house numbers removed).
func (a *App) saveWeatherHome(street, town, postcode, country string) {
	street = stripHouseNumber(street)
	hwnd := a.hwnd
	go func() {
		r := weatherHomeResult{street: street, town: strings.TrimSpace(town), postcode: strings.ToUpper(strings.TrimSpace(postcode)), country: strings.TrimSpace(country)}
		lat, lon, place, err := geocodeHome(r.street, r.town, r.postcode, r.country)
		if err != nil {
			r.err = err.Error()
			if strings.Contains(r.err, "not found") {
				r.err = "Couldn't find that address. Check the town and postcode."
			}
		} else {
			r.lat, r.lon, r.place = lat, lon, place
		}
		weatherHomeOut.Lock()
		weatherHomeOut.res = r
		weatherHomeOut.Unlock()
		pPostMessageW.Call(hwnd, wmWeatherHome, 0, 0)
	}()
}

func (a *App) weatherHomeDone() {
	weatherHomeOut.Lock()
	r := weatherHomeOut.res
	weatherHomeOut.Unlock()
	if r.err == "" {
		a.cfg.WeatherStreet, a.cfg.WeatherTown, a.cfg.WeatherPostcode, a.cfg.WeatherCountry = r.street, r.town, r.postcode, r.country
		a.cfg.WeatherHomeLat, a.cfg.WeatherHomeLon, a.cfg.WeatherHomePlace = r.lat, r.lon, r.place
		a.cfg.WeatherLoc = "home"
		a.saveConfig()
		a.wx.w = nil // the old place's weather
		a.refreshWeather(true)
	}
	msg := fmt.Sprintf(`{"ok":%v,"place":%s,"error":%s}`, r.err == "", strconv.Quote(r.place), strconv.Quote(r.err))
	for _, h := range a.settingsHosts() {
		if r.err == "" {
			h.eval("window.mdRefresh && window.mdRefresh()") // shows the saved place
		}
		h.eval("window.mdWeatherSaved && window.mdWeatherSaved(" + msg + ")")
	}
}

// weatherStatus is the line Settings shows under the location.
func (a *App) weatherStatus() string {
	switch {
	case a.wx.busy && a.wx.w == nil:
		return "Finding your location…"
	case a.wx.err != "":
		return a.wx.err
	case a.wx.w != nil:
		w := a.wx.w
		p := w.Place
		if p == "" {
			p = "your location"
		}
		s := fmt.Sprintf("Showing %s, %s in %s · updated %s", tempText(w.Temp), strings.ToLower(wmoText(w.Code, w.IsDay)), p, w.At.Format("15:04"))
		if a.wx.note != "" {
			s += ". " + a.wx.note
		}
		return s
	}
	return ""
}
