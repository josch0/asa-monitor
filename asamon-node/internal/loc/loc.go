// SPDX-License-Identifier: GPL-3.0-or-later
//
// Paket loc setzt das DAB Location Coding nach ETSI TS 104 089 um:
// Annex F (Geometrie), Annex A (Präsentationsformat), Annex E (Bitlayout im FIG).
//
// Es dient zwei Zwecken — der Knotenposition aus der Konfiguration und den
// Warngebieten aus den Alerts — und hat zum Rest des Programms keinen Bezug.
// Deshalb ist es vollständig für sich testbar und wird zuerst gebaut: In einem
// Bitlayout verstecken sich Irrtümer am längsten.
package loc

import (
	"errors"
	"fmt"
	"strings"
)

// ErrPolarZoneUnsupported meldet, dass für die Zonen 0 und 41 keine Geometrie
// berechnet wird.
//
// Die Polarzonen rechnen nach Annex F.5 mit Kreisgeometrie statt mit
// sphärischen Rechtecken. Für Deutschland sind sie ohne Belang, und eine
// ungeprüfte Umsetzung wäre schlimmer als gar keine: Stillschweigend falsch
// rechnen ist die einzige unzulässige Variante. Dekodieren und benennen lassen
// sich solche Codes trotzdem — nur ihr Rechteck gibt es nicht.
var ErrPolarZoneUnsupported = errors.New("loc: Polarzone (0 oder 41) hat keine Rechteckgeometrie")

// MaxDigits ist die Zahl der Ziffern eines vollständigen Location Codes.
// Sechs Ziffern à 4 bit sind die 24 bit des Ziffernfeldes.
const MaxDigits = 6

// Code ist ein Location Code: eine Zone und ein bis sechs Ziffern.
//
// Jede weggelassene Ziffer vergröbert die Auflösung um Faktor 4 je Achse. Die
// feinste Stufe (sechs Ziffern) ist rund 1 km groß.
type Code struct {
	Zone   uint8   // 0..41
	Digits []uint8 // 1..6 Ziffern, je 0..15; Digits[0] ist Digit 1
}

// Rect ist ein sphärisches Rechteck in WGS84-Grad.
type Rect struct {
	LatMin float64 `json:"lat_min"`
	LatMax float64 `json:"lat_max"`
	LonMin float64 `json:"lon_min"`
	LonMax float64 `json:"lon_max"`
}

// IsPolar sagt, ob der Code in einer der beiden Polarzonen liegt.
func (c Code) IsPolar() bool { return c.Zone == 0 || c.Zone == 41 }

// Valid prüft Zone und Ziffernzahl.
func (c Code) Valid() error {
	if c.Zone > 41 {
		return fmt.Errorf("loc: Zone %d liegt außerhalb von 0..41", c.Zone)
	}
	if len(c.Digits) == 0 || len(c.Digits) > MaxDigits {
		return fmt.Errorf("loc: %d Ziffern, erlaubt sind 1 bis %d", len(c.Digits), MaxDigits)
	}
	for i, d := range c.Digits {
		if d > 15 {
			return fmt.Errorf("loc: Ziffer %d ist %d, erlaubt sind 0..15", i+1, d)
		}
	}
	return nil
}

// DigitsHex gibt die Ziffern als Hex-Text, wie in der Spec geschrieben
// ("B736BB"). Die Länge ist die Zahl der bekannten Ziffern.
func (c Code) DigitsHex() string {
	var b strings.Builder
	for _, d := range c.Digits {
		b.WriteByte("0123456789ABCDEF"[d&0xF])
	}
	return b.String()
}

// String gibt die Kurzform der Spec, etwa "Z10:B736BB".
func (c Code) String() string { return fmt.Sprintf("Z%d:%s", c.Zone, c.DigitsHex()) }

// digitField packt die Ziffern in die 24 bit des Ziffernfeldes. Fehlende
// Ziffern sind 0 — sie bezeichnen dann die nordwestliche Teilfläche.
func (c Code) digitField() uint32 {
	var v uint32
	for i := range MaxDigits {
		var d uint32
		if i < len(c.Digits) {
			d = uint32(c.Digits[i] & 0xF)
		}
		v = v<<4 | d
	}
	return v
}

// Value ist der 30-bit-Wert aus Zone (6 bit) und Ziffernfeld (24 bit).
func (c Code) Value() uint32 { return uint32(c.Zone&0x3F)<<24 | c.digitField() }
