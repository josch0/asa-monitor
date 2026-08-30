// SPDX-License-Identifier: GPL-3.0-or-later
//
// Präsentationsformat nach ETSI TS 104 089, Annex A.
//
//	30 bit = Zone (6) + Ziffern (24)
//	  → Prüfsumme = Wert mod 61, als 6 bit angehängt → 36 bit
//	  → 12 Oktalziffern
//	  → jede + 1 → Symbole "1".."8"
//	  → drei Blöcke à vier Symbole, mit Bindestrich getrennt
//
// Das ist der Code, den Nutzer an ihrem ASA-Radio eintragen und den
// https://asa.radio/ zu einer Adresse ausgibt.
package loc

import (
	"fmt"
	"strings"
)

// checksumModulus ist der Teiler der Prüfsumme aus Annex A.
const checksumModulus = 61

// ParsePresentation liest "2366-7443-8484" und prüft dabei die Prüfsumme.
//
// Der zurückgegebene Code hat immer sechs Ziffern: Das Präsentationsformat
// überträgt das volle 24-bit-Ziffernfeld und kennt keine gröberen Stufen.
func ParsePresentation(s string) (Code, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "DLI://")
	t = strings.TrimPrefix(t, "dli://")

	var symbols []byte
	for i := 0; i < len(t); i++ {
		switch ch := t[i]; {
		case ch == '-' || ch == ' ':
			continue
		case ch >= '1' && ch <= '8':
			symbols = append(symbols, ch)
		default:
			return Code{}, fmt.Errorf("loc: %q enthält das ungültige Zeichen %q; erlaubt sind nur die Symbole 1..8", s, string(ch))
		}
	}
	if len(symbols) != 12 {
		return Code{}, fmt.Errorf("loc: %q hat %d Symbole, erwartet werden 12 (drei Blöcke à vier)", s, len(symbols))
	}

	var full uint64
	for _, ch := range symbols {
		full = full<<3 | uint64(ch-'1')
	}
	value := uint32(full >> 6)
	want := uint8(full & 0x3F)
	got := uint8(value % checksumModulus)
	if want != got {
		return Code{}, fmt.Errorf("loc: Prüfsumme von %q stimmt nicht (im Code %d, berechnet %d) — vertippt?", s, want, got)
	}

	c := Code{Zone: uint8(value >> 24), Digits: make([]uint8, MaxDigits)}
	for i := range MaxDigits {
		c.Digits[i] = uint8(value>>(20-4*i)) & 0xF
	}
	if c.Zone > 41 {
		return Code{}, fmt.Errorf("loc: %q ergibt Zone %d, erlaubt sind 0..41", s, c.Zone)
	}
	return c, nil
}

// Presentation gibt die Rückrichtung: drei Blöcke à vier Symbole.
//
// Codes mit weniger als sechs Ziffern werden mit Nullziffern aufgefüllt — das
// Präsentationsformat kennt keine gröberen Stufen. Maßgeblich bleibt in einem
// solchen Fall das Ziffernfeld selbst, nicht diese Zeichenkette.
func (c Code) Presentation() string {
	value := c.Value()
	full := uint64(value)<<6 | uint64(value%checksumModulus)

	var b strings.Builder
	for i := 11; i >= 0; i-- {
		if i == 7 || i == 3 {
			b.WriteByte('-')
		}
		b.WriteByte(byte('1' + (full>>(uint(i)*3))&7))
	}
	return b.String()
}

// URI gibt die Form für QR-Codes und Smart Devices: "DLI://2366-7443-8484".
func (c Code) URI() string { return "DLI://" + c.Presentation() }
