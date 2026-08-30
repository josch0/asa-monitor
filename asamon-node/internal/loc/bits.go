// SPDX-License-Identifier: GPL-3.0-or-later
//
// Bitlayout der Location Codes im FIG 0/15, nach ETSI TS 104 089, Annex E.
//
//	NFF 2 | Zone 6 | SCF 1 | Num digits 3 | Digit 1 4 | Other digits 4·n
//	      | Padding 0/4 (nur wenn Num digits ungerade)
//	      | Sub-codes 0/16 (nur bei SCF = 1)
//
// Drei Punkte, an denen sich die Umsetzung von asamon-rx bereits die Finger
// verbrannt hat:
//
//  1. NFF steht in *jedem* Location Code, nicht nur im ersten. Das folgt
//     zwingend aus der Padding-Regel — ohne die zwei NFF-Bits stünde die
//     Struktur nicht auf einer Bytegrenze.
//  2. Die Padding-Regel richtet sich nach "Num digits", also nach der Zahl der
//     *Other digits*, nicht nach der Gesamtzahl der Ziffern.
//  3. Bei SCF = 1 fehlt die niedrigstwertige Ziffer, und die 16-bit-Bitmaske
//     sagt, welche der 16 Teilflächen zum Warngebiet gehören.
package loc

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// MaxLocationBytes ist die normative Obergrenze je FIG-0/15-Instanz.
// Ein Sender, der mehr schickt, verletzt TS 104 089 — das soll sichtbar
// werden, nicht abgeschnitten. Der Dekoder erzwingt die Grenze deshalb nicht.
const MaxLocationBytes = 25

// DecodeLocationCodes zerlegt das Location-Code-Feld einer oder mehrerer
// FIG-0/15-Instanzen. Sub-codierte Einträge werden aufgelöst: Aus einem Code
// mit gesetztem SCF werden so viele Codes, wie die Bitmaske Teilflächen nennt.
func DecodeLocationCodes(raw []byte) ([]Code, error) {
	var out []Code
	r := &bitReader{data: raw}

	for r.remaining() > 0 {
		// Ein vollständig aufgefülltes Restbyte ist kein Location Code mehr.
		// Das kürzestmögliche Feld ist 16 bit breit.
		if r.remaining() < 16 {
			return out, fmt.Errorf("loc: %d Restbits nach %d Codes reichen für keinen weiteren Location Code", r.remaining(), len(out))
		}

		start := len(out)
		r.read(2) // NFF — Integritätsprüfung des Alert-Sets, nicht Teil der Geometrie
		zone := uint8(r.read(6))
		// Das Zonenfeld ist sechs Bit breit und trägt damit 0..63, belegt sind
		// aber nur 0..41. Ein Wert darüber ist kein Location Code mehr: Ab hier
		// stimmt die Bitausrichtung nicht, und alles Folgende wäre geraten.
		// Der Alert wird trotzdem gemeldet — mit area.raw als Beleg und
		// decode_error als Begründung.
		if zone > 41 {
			return out, fmt.Errorf("loc: Location Code %d nennt Zone %d, belegt sind 0..41", start+1, zone)
		}
		subCoded := r.read(1) == 1
		numDigits := int(r.read(3))
		if numDigits > 5 {
			return out, fmt.Errorf("loc: Num digits ist %d, erlaubt sind 0..5", numDigits)
		}
		if subCoded && numDigits > 4 {
			return out, fmt.Errorf("loc: bei Sub-codes sind höchstens vier Other digits erlaubt, angegeben sind %d", numDigits)
		}
		if r.remaining() < 4+4*numDigits+padBits(numDigits)+subBits(subCoded) {
			return out, fmt.Errorf("loc: Location Code %d ist abgeschnitten", start+1)
		}

		digits := make([]uint8, 0, numDigits+2)
		digits = append(digits, uint8(r.read(4)))
		for range numDigits {
			digits = append(digits, uint8(r.read(4)))
		}
		if numDigits%2 == 1 {
			r.read(4) // Padding, alle Bits 0
		}

		if !subCoded {
			out = append(out, Code{Zone: zone, Digits: digits})
			continue
		}

		// Bei SCF = 1 fehlt die niedrigstwertige Ziffer; die Maske sagt,
		// welche der 16 Teilflächen dazugehören.
		//
		// Die Bitzuordnung ist die Stelle, an der man sich irren kann: Das
		// Feld wird MSB zuerst übertragen, aber "bi (i = 0 to 15)" meint
		// Bit i *des Wertes*, also das zuletzt übertragene Bit für
		// Teilfläche 0. Nachgeprüft am Cardiff-Beispiel aus Annex C — mit
		// der umgekehrten Zuordnung läge das Warngebiet dort spiegelbildlich
		// am falschen Rand jedes Rechtecks.
		mask := uint16(r.read(16))
		for sub := range 16 {
			if mask&(1<<uint(sub)) == 0 {
				continue
			}
			expanded := make([]uint8, len(digits), len(digits)+1)
			copy(expanded, digits)
			out = append(out, Code{Zone: zone, Digits: append(expanded, uint8(sub))})
		}
	}
	return out, nil
}

// DecodeLocationCodesHex ist die Bequemlichkeitsform für das Feld
// `location_codes` aus dem asa-Record, das dort als Hex-Text steht.
func DecodeLocationCodesHex(s string) ([]Code, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("loc: location_codes %q ist kein gültiges Hex: %w", s, err)
	}
	return DecodeLocationCodes(raw)
}

func padBits(numDigits int) int {
	if numDigits%2 == 1 {
		return 4
	}
	return 0
}

func subBits(subCoded bool) int {
	if subCoded {
		return 16
	}
	return 0
}

// EncodeLocationCodes ist die Rückrichtung — gebraucht wird sie nur zum
// Erzeugen von Prüfdaten (Testströme, Testvektoren gegen TS 104 090 A.19).
//
// subCodes ist je Code die Liste der Teilflächen; ist sie leer, wird SCF = 0
// kodiert. Die Ziffern eines sub-codierten Eintrags sind die *gemeinsamen*
// Ziffern ohne die niedrigstwertige.
func EncodeLocationCodes(codes []Code, nff []uint8, subCodes [][]uint8) ([]byte, error) {
	var w bitWriter
	for i, c := range codes {
		if err := c.Valid(); err != nil {
			return nil, err
		}
		var subs []uint8
		if i < len(subCodes) {
			subs = subCodes[i]
		}
		numDigits := len(c.Digits) - 1
		if numDigits > 5 {
			return nil, fmt.Errorf("loc: Code %d hat %d Other digits, erlaubt sind höchstens 5", i+1, numDigits)
		}
		if len(subs) > 0 && numDigits > 4 {
			return nil, fmt.Errorf("loc: Code %d ist sub-codiert und hat %d Other digits, erlaubt sind höchstens 4", i+1, numDigits)
		}
		var n uint8
		if i < len(nff) {
			n = nff[i]
		}
		w.write(uint32(n), 2)
		w.write(uint32(c.Zone), 6)
		if len(subs) > 0 {
			w.write(1, 1)
		} else {
			w.write(0, 1)
		}
		w.write(uint32(numDigits), 3)
		for _, d := range c.Digits {
			w.write(uint32(d), 4)
		}
		if numDigits%2 == 1 {
			w.write(0, 4)
		}
		if len(subs) > 0 {
			var mask uint32
			for _, s := range subs {
				if s > 15 {
					return nil, fmt.Errorf("loc: Sub-code %d liegt außerhalb von 0..15", s)
				}
				mask |= 1 << uint32(s)
			}
			w.write(mask, 16)
		}
	}
	if w.nbits%8 != 0 {
		return nil, fmt.Errorf("loc: Ergebnis ist mit %d bit nicht byte-ausgerichtet", w.nbits)
	}
	return w.data, nil
}

type bitReader struct {
	data []byte
	pos  int // in Bits
}

func (r *bitReader) remaining() int { return len(r.data)*8 - r.pos }

func (r *bitReader) read(n int) uint32 {
	var v uint32
	for i := range n {
		if r.pos >= len(r.data)*8 {
			return v << uint(n-i)
		}
		bit := (r.data[r.pos/8] >> (7 - uint(r.pos%8))) & 1
		v = v<<1 | uint32(bit)
		r.pos++
	}
	return v
}

type bitWriter struct {
	data  []byte
	nbits int
}

func (w *bitWriter) write(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.nbits%8 == 0 {
			w.data = append(w.data, 0)
		}
		if (v>>uint(i))&1 == 1 {
			w.data[w.nbits/8] |= 1 << (7 - uint(w.nbits%8))
		}
		w.nbits++
	}
}
