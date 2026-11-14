package main

// The weather widget's data side, free of Win32 so it can be tested: reading the
// Open-Meteo forecast and the OpenStreetMap (Nominatim) and GeoJS location answers,
// the names of the weather conditions, and tidying a home address.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WeatherDay is one day of the forecast.
type WeatherDay struct {
	Name   string // "Today", "Sat"…
	Code   int    // WMO weather code
	Hi, Lo int
}

// Weather is what the widget shows.
type Weather struct {
	Place  string
	Temp   int
	Code   int
	IsDay  bool
	Hi, Lo int
	Days   []WeatherDay
	F      bool // temperatures are in °F
	Approx bool // the location is approximate (from the internet connection)
	At     time.Time
}

// weather kinds, for the glyph
const (
	wkClear = iota
	wkPartly
	wkCloudy
	wkFog
	wkDrizzle
	wkRain
	wkSnow
	wkThunder
)

// wmoKind sorts a WMO weather code (as Open-Meteo gives it) into a kind of glyph.
func wmoKind(code int) int {
	switch {
	case code == 0:
		return wkClear
	case code == 1 || code == 2:
		return wkPartly
	case code == 3:
		return wkCloudy
	case code == 45 || code == 48:
		return wkFog
	case code >= 51 && code <= 57:
		return wkDrizzle
	case code >= 61 && code <= 67, code >= 80 && code <= 82:
		return wkRain
	case code >= 71 && code <= 77, code == 85 || code == 86:
		return wkSnow
	case code >= 95:
		return wkThunder
	}
	return wkCloudy
}

// wmoText names a WMO weather code.
func wmoText(code int, day bool) string {
	switch {
	case code == 0:
		if day {
			return "Sunny"
		}
		return "Clear"
	case code == 1:
		return "Mostly clear"
	case code == 2:
		return "Partly cloudy"
	case code == 3:
		return "Cloudy"
	case code == 45 || code == 48:
		return "Fog"
	case code >= 51 && code <= 57:
		return "Drizzle"
	case code == 61 || code == 80:
		return "Light rain"
	case code == 63 || code == 81:
		return "Rain"
	case code == 65 || code == 82:
		return "Heavy rain"
	case code == 66 || code == 67:
		return "Freezing rain"
	case code == 71 || code == 85:
		return "Light snow"
	case code == 73 || code == 75 || code == 86:
		return "Snow"
	case code == 77:
		return "Snow grains"
	case code >= 95:
		return "Thunderstorm"
	}
	return "Cloudy"
}

// forecastURL is the Open-Meteo request for a place.
func forecastURL(lat, lon float64, fahrenheit bool) string {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(lat, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', 4, 64))
	q.Set("current", "temperature_2m,weather_code,is_day")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min")
	q.Set("timezone", "auto")
	q.Set("forecast_days", "5")
	if fahrenheit {
		q.Set("temperature_unit", "fahrenheit")
	}
	return "https://api.open-meteo.com/v1/forecast?" + q.Encode()
}

// parseForecast reads Open-Meteo's answer. now is used to name the days.
func parseForecast(body []byte, fahrenheit bool, now time.Time) (Weather, error) {
	var r struct {
		Current struct {
			Temp  *float64 `json:"temperature_2m"`
			Code  int      `json:"weather_code"`
			IsDay int      `json:"is_day"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Code []int     `json:"weather_code"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
		Error  bool   `json:"error"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Weather{}, err
	}
	if r.Error {
		return Weather{}, fmt.Errorf("forecast: %s", r.Reason)
	}
	if r.Current.Temp == nil {
		return Weather{}, fmt.Errorf("forecast: no current temperature")
	}
	w := Weather{Temp: roundI(*r.Current.Temp), Code: r.Current.Code, IsDay: r.Current.IsDay != 0, F: fahrenheit, At: now}
	today := now.Format("2006-01-02")
	for i, t := range r.Daily.Time {
		if i >= len(r.Daily.Code) || i >= len(r.Daily.Max) || i >= len(r.Daily.Min) {
			break
		}
		name := t
		if d, err := time.Parse("2006-01-02", t); err == nil {
			name = d.Format("Mon")
		}
		if t == today {
			name = "Today"
			w.Hi, w.Lo = roundI(r.Daily.Max[i]), roundI(r.Daily.Min[i])
		}
		w.Days = append(w.Days, WeatherDay{Name: name, Code: r.Daily.Code[i], Hi: roundI(r.Daily.Max[i]), Lo: roundI(r.Daily.Min[i])})
	}
	if len(w.Days) > 0 && w.Days[0].Name != "Today" { // the API's day boundary differs from ours
		w.Hi, w.Lo = w.Days[0].Hi, w.Days[0].Lo
	}
	return w, nil
}

func roundI(f float64) int { return int(math.Round(f)) }

// jsonFloat reads a number that may come as a string ("51.5") or a number.
type jsonFloat float64

func (f *jsonFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = jsonFloat(v)
	return nil
}

type osmAddress struct {
	Road, Suburb, Village, Town, City, Municipality, County, State, Postcode, Country string
	Hamlet                                                                            string
	CityDistrict                                                                      string `json:"city_district"`
}

// placeName is the short name for a place: the town (or suburb, then town), never
// the street or house.
func (a osmAddress) placeName() string {
	town := firstNonEmpty(a.City, a.Town, a.Village, a.Hamlet, a.Municipality, a.County, a.State)
	local := firstNonEmpty(a.Suburb, a.CityDistrict)
	if local != "" && town != "" && !strings.EqualFold(local, town) {
		return local + ", " + town
	}
	return firstNonEmpty(town, local, a.Country)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// parseOSMSearch reads Nominatim's /search answer: the first match.
func parseOSMSearch(body []byte) (lat, lon float64, place string, err error) {
	var r []struct {
		Lat     jsonFloat  `json:"lat"`
		Lon     jsonFloat  `json:"lon"`
		Address osmAddress `json:"address"`
		Name    string     `json:"name"`
	}
	if err = json.Unmarshal(body, &r); err != nil {
		return
	}
	if len(r) == 0 {
		return 0, 0, "", fmt.Errorf("address not found")
	}
	place = r[0].Address.placeName()
	if place == "" {
		place = r[0].Name
	}
	return float64(r[0].Lat), float64(r[0].Lon), place, nil
}

// parseOSMReverse reads Nominatim's /reverse answer: the place's short name.
func parseOSMReverse(body []byte) (string, error) {
	var r struct {
		Address osmAddress `json:"address"`
		Error   string     `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.Error != "" {
		return "", fmt.Errorf("%s", r.Error)
	}
	return r.Address.placeName(), nil
}

// parseGeoJS reads GeoJS's approximate location (from the internet connection).
func parseGeoJS(body []byte) (lat, lon float64, place string, err error) {
	var r struct {
		Lat    jsonFloat `json:"latitude"`
		Lon    jsonFloat `json:"longitude"`
		City   string    `json:"city"`
		Region string    `json:"region"`
	}
	if err = json.Unmarshal(body, &r); err != nil {
		return
	}
	if r.Lat == 0 && r.Lon == 0 {
		return 0, 0, "", fmt.Errorf("no location")
	}
	return float64(r.Lat), float64(r.Lon), firstNonEmpty(r.City, r.Region), nil
}

var houseNumberRx = regexp.MustCompile(`(?i)^\s*(?:(?:flat|apt|apartment|unit|no\.?)\s*)?\d+[a-z]?(?:\s*[-/]\s*\d+[a-z]?)?\s*,?\s+`)

// stripHouseNumber takes a house or flat number off the front of a street, so it's
// never stored or sent: "12a Baker Street" → "Baker Street".
func stripHouseNumber(street string) string {
	s := strings.TrimSpace(street)
	for i := 0; i < 3; i++ { // "Flat 3, 12 Baker Street"
		t := houseNumberRx.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = strings.TrimSpace(t)
	}
	return s
}

// addressQuery is the search text for a home address (without a house number).
func addressQuery(street, town, postcode, country string) string {
	var parts []string
	for _, p := range []string{stripHouseNumber(street), town, postcode, country} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

// tempText is "14°".
func tempText(t int) string { return strconv.Itoa(t) + "°" }
