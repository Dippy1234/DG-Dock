package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseForecast(t *testing.T) {
	body := []byte(`{"latitude":51.5,"longitude":-0.12,"current":{"time":"2026-11-13T17:00","temperature_2m":9.6,"weather_code":3,"is_day":0},
	"daily":{"time":["2026-11-13","2026-11-14","2026-11-15","2026-11-16","2026-11-17"],"weather_code":[3,61,2,0,95],
	"temperature_2m_max":[11.2,10.4,9.5,8.1,12.9],"temperature_2m_min":[5.4,6.6,3.2,1.9,7.4]}}`)
	now := time.Date(2026, 11, 13, 17, 5, 0, 0, time.Local)
	w, err := parseForecast(body, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if w.Temp != 10 || w.Code != 3 || w.IsDay || w.Hi != 11 || w.Lo != 5 {
		t.Fatalf("current wrong: %+v", w)
	}
	if len(w.Days) != 5 || w.Days[0].Name != "Today" || w.Days[1].Name != "Sat" || w.Days[1].Code != 61 || w.Days[4].Hi != 13 {
		t.Fatalf("days wrong: %+v", w.Days)
	}
	if _, err := parseForecast([]byte(`{"error":true,"reason":"Latitude must be in range"}`), false, now); err == nil || !strings.Contains(err.Error(), "Latitude") {
		t.Fatal("API error not reported", err)
	}
	if _, err := parseForecast([]byte(`{}`), false, now); err == nil {
		t.Fatal("empty answer accepted")
	}
}

func TestForecastURL(t *testing.T) {
	u := forecastURL(51.50735, -0.12776, true)
	for _, want := range []string{"latitude=51.5074", "longitude=-0.1278", "temperature_unit=fahrenheit", "forecast_days=5", "current=temperature_2m%2Cweather_code%2Cis_day"} {
		if !strings.Contains(u, want) {
			t.Fatalf("%s missing %s", u, want)
		}
	}
	if strings.Contains(forecastURL(1, 2, false), "fahrenheit") {
		t.Fatal("celsius should be the API default")
	}
}

func TestOSM(t *testing.T) {
	lat, lon, place, err := parseOSMSearch([]byte(`[{"lat":"51.5237","lon":"-0.1585","name":"Baker Street",
		"address":{"road":"Baker Street","suburb":"Marylebone","city":"London","postcode":"NW1 6XE","country":"United Kingdom"}}]`))
	if err != nil || lat < 51.5 || lon > -0.15 || place != "Marylebone, London" {
		t.Fatal(lat, lon, place, err)
	}
	if _, _, _, err := parseOSMSearch([]byte(`[]`)); err == nil {
		t.Fatal("no match should be an error")
	}
	p, err := parseOSMReverse([]byte(`{"address":{"town":"Guildford","county":"Surrey","country":"United Kingdom"}}`))
	if err != nil || p != "Guildford" {
		t.Fatal(p, err)
	}
	if _, err := parseOSMReverse([]byte(`{"error":"Unable to geocode"}`)); err == nil {
		t.Fatal("error not reported")
	}
}

func TestGeoJS(t *testing.T) {
	lat, lon, place, err := parseGeoJS([]byte(`{"latitude":"53.4794","longitude":"-2.2453","city":"Manchester","region":"England"}`))
	if err != nil || lat < 53 || lon > -2 || place != "Manchester" {
		t.Fatal(lat, lon, place, err)
	}
}

func TestStripHouseNumber(t *testing.T) {
	cases := map[string]string{
		"12 Baker Street":         "Baker Street",
		"12a Baker Street":        "Baker Street",
		"  221B, Baker Street ":   "Baker Street",
		"Flat 3, 12 Baker Street": "Baker Street",
		"10-12 High Road":         "High Road",
		"Baker Street":            "Baker Street",
		"Station Road 5":          "Station Road 5", // only a number at the start is a house number
		"1st Avenue":              "1st Avenue",
	}
	for in, want := range cases {
		if got := stripHouseNumber(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if q := addressQuery("12 Baker Street", "London", "NW1", ""); q != "Baker Street, London, NW1" {
		t.Fatal(q)
	}
}

func TestWMO(t *testing.T) {
	if wmoKind(0) != wkClear || wmoKind(2) != wkPartly || wmoKind(63) != wkRain || wmoKind(75) != wkSnow || wmoKind(96) != wkThunder || wmoKind(45) != wkFog {
		t.Fatal("kinds")
	}
	if wmoText(0, true) != "Sunny" || wmoText(0, false) != "Clear" || wmoText(65, true) != "Heavy rain" {
		t.Fatal("texts")
	}
}
