// SPDX-License-Identifier: GPL-3.0-or-later
//
// Geometrie nach ETSI TS 104 089, Annex F.
//
//	SE (Southerly Extent) = 90 - Breite(WGS84)     # Nordpol 0°, Südpol 180°
//	EE (Easterly Extent)  = Länge(WGS84), negativ + 360
//
//	SE  <  18      → Zone 0                                    (Nordpol)
//	18 ≤ SE < 162  → Zone = 10·int((SE-18)/36) + int(EE/36) + 1 (1..40)
//	SE ≥ 162       → Zone 41                                   (Südpol)
//
//	SC = int(frac((SE-18)/36) · 2^12)   EC = int(frac(EE/36) · 2^12)
//	CC = Interleave(SC, EC) je 2 bit, beginnend mit SC → 24 bit
package loc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// zoneSpan ist die Kantenlänge einer gebänderten Zone in Grad.
const zoneSpan = 36.0

// axisSteps ist die Zahl der Schritte je Achse innerhalb einer Zone: sechs
// Ziffern à 2 bit je Achse ergeben 12 bit.
const axisSteps = 1 << 12

// Encode bildet WGS84-Koordinaten auf einen Location Code mit sechs Ziffern ab.
//
// Für die Polarzonen wird ErrPolarZoneUnsupported zurückgegeben — die Zone
// stimmt dann zwar, die Ziffern rechnen dort aber nach Kreisgeometrie.
func Encode(lat, lon float64) (Code, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Code{}, fmt.Errorf("loc: %.6f/%.6f liegt außerhalb von WGS84", lat, lon)
	}
	se := 90 - lat
	ee := lon
	if ee < 0 {
		ee += 360
	}
	if ee >= 360 {
		ee -= 360
	}

	switch {
	case se < 18:
		return Code{Zone: 0}, ErrPolarZoneUnsupported
	case se >= 162:
		return Code{Zone: 41}, ErrPolarZoneUnsupported
	}

	row := int((se - 18) / zoneSpan)
	col := int(ee / zoneSpan)
	if row > 3 {
		row = 3 // se == 162 ist bereits Polarzone; das hier ist der Rundungsrand
	}
	if col > 9 {
		col = 9
	}
	zone := 10*row + col + 1

	sc := int(frac((se-18)/zoneSpan) * axisSteps)
	ec := int(frac(ee/zoneSpan) * axisSteps)
	sc = clampAxis(sc)
	ec = clampAxis(ec)

	c := Code{Zone: uint8(zone), Digits: make([]uint8, MaxDigits)}
	for i := range MaxDigits {
		shift := uint(10 - 2*i)
		hi := (sc >> shift) & 3
		lo := (ec >> shift) & 3
		c.Digits[i] = uint8(hi<<2 | lo)
	}
	return c, nil
}

func frac(v float64) float64 {
	f := v - math.Floor(v)
	if f < 0 {
		f = 0
	}
	return f
}

func clampAxis(v int) int {
	if v < 0 {
		return 0
	}
	if v >= axisSteps {
		return axisSteps - 1
	}
	return v
}

// axes zerlegt die Ziffern in die beiden Achsenanteile. Zurück kommen die
// bekannten oberen Bits von SC und EC sowie die Zahl der bekannten Bits je
// Achse (2 je Ziffer).
func (c Code) axes() (sc, ec, bits int) {
	for _, d := range c.Digits {
		sc = sc<<2 | int(d>>2)&3
		ec = ec<<2 | int(d)&3
		bits += 2
	}
	return sc, ec, bits
}

// Rect gibt die Grenzen des sphärischen Rechtecks in WGS84-Grad.
//
// Bei n Ziffern sind je Achse 2n bit bekannt; die Kantenlänge ist damit
// 36°/2^(2n) in beiden Achsen der Zonenkoordinaten.
func (c Code) Rect() (Rect, error) {
	if err := c.Valid(); err != nil {
		return Rect{}, err
	}
	if c.IsPolar() {
		return Rect{}, ErrPolarZoneUnsupported
	}

	sc, ec, bits := c.axes()
	step := 1 << (12 - bits) // in Einheiten von 1/4096 der Zone
	scMin := sc << (12 - bits)
	ecMin := ec << (12 - bits)

	row := (int(c.Zone) - 1) / 10
	col := (int(c.Zone) - 1) % 10
	seBase := 18 + zoneSpan*float64(row)
	eeBase := zoneSpan * float64(col)

	seMin := seBase + zoneSpan*float64(scMin)/axisSteps
	seMax := seBase + zoneSpan*float64(scMin+step)/axisSteps
	eeMin := eeBase + zoneSpan*float64(ecMin)/axisSteps
	eeMax := eeBase + zoneSpan*float64(ecMin+step)/axisSteps

	// Die Zonengrenzen liegen auf Vielfachen von 36°, also auch auf 180°.
	// Ein Rechteck kann den Antimeridian daher nie überspannen; es genügt,
	// die ganze Zone einheitlich auf [-180,180] zu drehen.
	if eeMin >= 180 {
		eeMin -= 360
		eeMax -= 360
	}

	return Rect{
		LatMin: 90 - seMax,
		LatMax: 90 - seMin,
		LonMin: eeMin,
		LonMax: eeMax,
	}, nil
}

// Center gibt den Mittelpunkt des Rechtecks.
func (c Code) Center() (lat, lon float64, err error) {
	r, err := c.Rect()
	if err != nil {
		return 0, 0, err
	}
	return (r.LatMin + r.LatMax) / 2, (r.LonMin + r.LonMax) / 2, nil
}

// Contains sagt, ob eine Position im Rechteck liegt. Die Nord- und Ostkante
// gehört bereits zur Nachbarfläche — so wie es die Abbildung aus Koordinaten
// vorgibt.
func (r Rect) Contains(lat, lon float64) bool {
	return lat > r.LatMin && lat <= r.LatMax && lon >= r.LonMin && lon < r.LonMax
}

// GeoJSON gibt das Rechteck als GeoJSON-Polygon. Die Ringreihenfolge ist gegen
// den Uhrzeigersinn, wie es RFC 7946 für äußere Ringe vorsieht.
func (c Code) GeoJSON() ([]byte, error) {
	r, err := c.Rect()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(`{"type":"Polygon","coordinates":[`)
	b.WriteString(ringJSON(r))
	b.WriteString(`]}`)
	return []byte(b.String()), nil
}

// MultiPolygon fasst mehrere Codes zu einem GeoJSON-MultiPolygon zusammen.
// Codes ohne Rechteckgeometrie (Polarzonen) werden übersprungen; ihre Zahl
// steht im zweiten Rückgabewert, damit der Aufrufer sie melden kann.
func MultiPolygon(codes []Code) (data []byte, skipped int) {
	var b strings.Builder
	b.WriteString(`{"type":"MultiPolygon","coordinates":[`)
	first := true
	for _, c := range codes {
		r, err := c.Rect()
		if err != nil {
			skipped++
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteByte('[')
		b.WriteString(ringJSON(r))
		b.WriteByte(']')
	}
	b.WriteString(`]}`)
	return []byte(b.String()), skipped
}

func ringJSON(r Rect) string {
	pt := func(lon, lat float64) string {
		return "[" + coord(lon) + "," + coord(lat) + "]"
	}
	return "[" + strings.Join([]string{
		pt(r.LonMin, r.LatMin),
		pt(r.LonMax, r.LatMin),
		pt(r.LonMax, r.LatMax),
		pt(r.LonMin, r.LatMax),
		pt(r.LonMin, r.LatMin),
	}, ",") + "]"
}

// coord schreibt eine Gradzahl kurz und verlustfrei genug: sechs Nachkommastellen
// sind rund 0,1 m und damit weit feiner als die 977 m des feinsten Rechtecks.
func coord(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
