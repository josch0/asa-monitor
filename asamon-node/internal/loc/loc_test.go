// SPDX-License-Identifier: GPL-3.0-or-later

package loc

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// Der Testvektor der Spec: BBC Broadcasting House.
// TS 104 089, Annex F.4 und Annex A.
const (
	bbcLat          = 51.5187412
	bbcLon          = -0.1434571
	bbcPresentation = "2366-7443-8484"
	bbcDigits       = "B736BB"
	bbcZone         = 10
)

func TestBBCBeideRichtungen(t *testing.T) {
	c, err := Encode(bbcLat, bbcLon)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if c.Zone != bbcZone || c.DigitsHex() != bbcDigits {
		t.Errorf("Encode ergab %s, erwartet Z%d:%s", c, bbcZone, bbcDigits)
	}
	if got := c.Presentation(); got != bbcPresentation {
		t.Errorf("Presentation ergab %q, erwartet %q", got, bbcPresentation)
	}
	if got := c.URI(); got != "DLI://"+bbcPresentation {
		t.Errorf("URI ergab %q", got)
	}

	back, err := ParsePresentation(bbcPresentation)
	if err != nil {
		t.Fatalf("ParsePresentation: %v", err)
	}
	if back.Zone != bbcZone || back.DigitsHex() != bbcDigits {
		t.Errorf("ParsePresentation ergab %s, erwartet Z%d:%s", back, bbcZone, bbcDigits)
	}

	// Annex F.4 nennt SC = 91A und EC = FEF.
	sc, ec, bits := back.axes()
	if bits != 12 || sc != 0x91A || ec != 0xFEF {
		t.Errorf("SC/EC ergab %03X/%03X bei %d bit, erwartet 91A/FEF bei 12 bit", sc, ec, bits)
	}

	r, err := back.Rect()
	if err != nil {
		t.Fatalf("Rect: %v", err)
	}
	if !r.Contains(bbcLat, bbcLon) {
		t.Errorf("das Rechteck %+v enthält die Ausgangsposition nicht", r)
	}
	// Kantenlänge bei sechs Ziffern: 36°/4096.
	const want = 36.0 / 4096.0
	if math.Abs((r.LatMax-r.LatMin)-want) > 1e-9 || math.Abs((r.LonMax-r.LonMin)-want) > 1e-9 {
		t.Errorf("Kantenlänge %+v, erwartet je %.10f", r, want)
	}
}

func TestPraesentationPruefsumme(t *testing.T) {
	// Ein einzelnes verändertes Symbol muss auffallen.
	bad := "2366-7443-8485"
	if _, err := ParsePresentation(bad); err == nil {
		t.Errorf("%q wurde angenommen, obwohl die Prüfsumme nicht stimmt", bad)
	}
	for _, s := range []string{"", "2366-7443-848", "2366-7443-84840", "2366-7443-8480", "0366-7443-8484", "2366-7443-9484"} {
		if _, err := ParsePresentation(s); err == nil {
			t.Errorf("%q wurde angenommen", s)
		}
	}
	if _, err := ParsePresentation("DLI://" + bbcPresentation); err != nil {
		t.Errorf("URI-Form abgelehnt: %v", err)
	}
}

func TestPraesentationRundlauf(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		c := Code{Zone: uint8(rng.IntN(42)), Digits: make([]uint8, MaxDigits)}
		for d := range c.Digits {
			c.Digits[d] = uint8(rng.IntN(16))
		}
		back, err := ParsePresentation(c.Presentation())
		if err != nil {
			t.Fatalf("%s → %q → %v", c, c.Presentation(), err)
		}
		if back.Zone != c.Zone || back.DigitsHex() != c.DigitsHex() {
			t.Fatalf("%s → %q → %s", c, c.Presentation(), back)
		}
	}
}

// Eigenschaftstest über Zufallspositionen in Deutschland: Wer eine Position
// kodiert, das Präsentationsformat durchläuft und wieder dekodiert, muss ein
// Rechteck erhalten, das die Ausgangsposition enthält.
func TestPositionenInDeutschland(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 1))
	for range 10000 {
		lat := 47.2 + rng.Float64()*(55.1-47.2)
		lon := 5.8 + rng.Float64()*(15.1-5.8)

		c, err := Encode(lat, lon)
		if err != nil {
			t.Fatalf("Encode(%.6f, %.6f): %v", lat, lon, err)
		}
		back, err := ParsePresentation(c.Presentation())
		if err != nil {
			t.Fatalf("ParsePresentation(%q): %v", c.Presentation(), err)
		}
		r, err := back.Rect()
		if err != nil {
			t.Fatalf("Rect: %v", err)
		}
		if !r.Contains(lat, lon) {
			t.Fatalf("%.6f/%.6f → %s → %+v enthält die Position nicht", lat, lon, back, r)
		}
		clat, clon, err := back.Center()
		if err != nil {
			t.Fatalf("Center: %v", err)
		}
		if !r.Contains(clat, clon) {
			t.Fatalf("der Mittelpunkt %.6f/%.6f liegt nicht in %+v", clat, clon, r)
		}
	}
}

func TestGroebereCodesUmfassenFeinere(t *testing.T) {
	fein, err := Encode(52.520008, 13.404954) // Berlin, Brandenburger Tor
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	feinRect, err := fein.Rect()
	if err != nil {
		t.Fatalf("Rect: %v", err)
	}
	for n := 1; n < MaxDigits; n++ {
		grob := Code{Zone: fein.Zone, Digits: fein.Digits[:n]}
		r, err := grob.Rect()
		if err != nil {
			t.Fatalf("Rect(%s): %v", grob, err)
		}
		if r.LatMin > feinRect.LatMin || r.LatMax < feinRect.LatMax ||
			r.LonMin > feinRect.LonMin || r.LonMax < feinRect.LonMax {
			t.Errorf("%s (%+v) umfasst %s (%+v) nicht", grob, r, fein, feinRect)
		}
		want := 36.0 / math.Pow(4, float64(n))
		if math.Abs((r.LatMax-r.LatMin)-want) > 1e-9 {
			t.Errorf("%s hat Kantenlänge %.9f, erwartet %.9f", grob, r.LatMax-r.LatMin, want)
		}
	}
}

func TestPolarzonen(t *testing.T) {
	for _, zone := range []uint8{0, 41} {
		c := Code{Zone: zone, Digits: []uint8{1, 2}}
		if _, err := c.Rect(); err != ErrPolarZoneUnsupported {
			t.Errorf("Zone %d: Rect gab %v, erwartet ErrPolarZoneUnsupported", zone, err)
		}
		if _, _, err := c.Center(); err != ErrPolarZoneUnsupported {
			t.Errorf("Zone %d: Center gab %v", zone, err)
		}
		// Benennen lässt sich ein solcher Code trotzdem.
		if c.String() == "" || c.Presentation() == "" {
			t.Errorf("Zone %d: Benennung fehlgeschlagen", zone)
		}
	}
	if _, err := Encode(89.0, 0); err != ErrPolarZoneUnsupported {
		t.Errorf("Nordpolzone: Encode gab %v", err)
	}
	if _, err := Encode(-89.0, 0); err != ErrPolarZoneUnsupported {
		t.Errorf("Südpolzone: Encode gab %v", err)
	}
}

func TestZonenbestimmung(t *testing.T) {
	cases := []struct {
		lat, lon float64
		zone     uint8
	}{
		{72, 0, 1},   // SE = 18, EE = 0 → erste gebänderte Zone
		{72, -1, 10}, // dieselbe Reihe, östlichste Zone (EE = 359)
		{36, 0, 11},  // SE = 54 → zweite Reihe
		{0, 0, 21},   // Äquator, SE = 90 → dritte Reihe
		{-36, 0, 31}, // vierte Reihe
		{-71.9, 0, 31},
		{51.5187412, -0.1434571, 10},
	}
	for _, c := range cases {
		got, err := Encode(c.lat, c.lon)
		if err != nil {
			t.Fatalf("Encode(%.4f,%.4f): %v", c.lat, c.lon, err)
		}
		if got.Zone != c.zone {
			t.Errorf("Encode(%.4f,%.4f) ergab Zone %d, erwartet %d", c.lat, c.lon, got.Zone, c.zone)
		}
	}
}

func TestGeoJSON(t *testing.T) {
	c, _ := ParsePresentation(bbcPresentation)
	got, err := c.GeoJSON()
	if err != nil {
		t.Fatalf("GeoJSON: %v", err)
	}
	s := string(got)
	if !strings.HasPrefix(s, `{"type":"Polygon","coordinates":[[[`) || !strings.HasSuffix(s, `]]}`) {
		t.Errorf("GeoJSON sieht nicht aus wie ein Polygon: %s", s)
	}
	if strings.Count(s, "],[") != 4 { // fünf Punkte, der letzte schließt den Ring
		t.Errorf("Ring hat nicht fünf Punkte: %s", s)
	}
}
