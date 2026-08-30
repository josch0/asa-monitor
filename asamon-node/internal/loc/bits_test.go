// SPDX-License-Identifier: GPL-3.0-or-later

package loc

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// digits liest "91BB82" als Ziffernfolge.
func digits(s string) []uint8 {
	out := make([]uint8, 0, len(s))
	for i := 0; i < len(s); i++ {
		v := strings.IndexByte("0123456789ABCDEF", s[i])
		if v < 0 {
			panic("ungueltige Ziffer im Testcode: " + s)
		}
		out = append(out, uint8(v))
	}
	return out
}

func code(zone uint8, d string) Code { return Code{Zone: zone, Digits: digits(d)} }

// Die Byte-Längen der offiziellen Testströme EWS1..EWS9 aus ETSI TS 104 090,
// Tabelle A.19. Sie sind eine zweite, unabhängige normative Probe auf das
// Bitlayout: Wer sie nachrechnen kann, hat Annex E richtig gelesen.
//
// Insbesondere geht die Rechnung nur auf, wenn NFF in *jedem* Location Code
// steht — sonst stünde die Struktur nicht auf einer Bytegrenze.
func TestByteLaengenAusTS104090(t *testing.T) {
	var datei struct {
		Saetze []struct {
			Name         string `json:"name"`
			Beschreibung string `json:"beschreibung"`
			Bytes        int    `json:"bytes"`
			Codes        []struct {
				Zone     uint8   `json:"zone"`
				Digits   string  `json:"digits"`
				Nff      uint8   `json:"nff"`
				SubCodes []uint8 `json:"sub_codes"`
			} `json:"codes"`
		} `json:"saetze"`
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "locations", "ts104090-a19.json"))
	if err != nil {
		t.Fatalf("Testvektoren: %v", err)
	}
	if err := json.Unmarshal(raw, &datei); err != nil {
		t.Fatalf("Testvektoren: %v", err)
	}
	if len(datei.Saetze) == 0 {
		t.Fatal("Testvektoren: keine Sätze in der Datei")
	}

	for _, satz := range datei.Saetze {
		var (
			codes []Code
			nff   []uint8
			subs  [][]uint8
		)
		for _, c := range satz.Codes {
			codes = append(codes, code(c.Zone, c.Digits))
			nff = append(nff, c.Nff)
			subs = append(subs, c.SubCodes)
		}

		packed, err := EncodeLocationCodes(codes, nff, subs)
		if err != nil {
			t.Errorf("%s: %v", satz.Name, err)
			continue
		}
		if len(packed) != satz.Bytes {
			t.Errorf("%s: %d Byte nach Tabelle A.19, kodiert wurden %d (%s)",
				satz.Name, satz.Bytes, len(packed), hex.EncodeToString(packed))
			continue
		}
		// Rückwärts: dieselben Bytes müssen wieder dieselben Rechtecke ergeben.
		back, err := DecodeLocationCodes(packed)
		if err != nil {
			t.Errorf("%s: Rückweg: %v", satz.Name, err)
			continue
		}
		want := expand(codes, subs)
		if len(back) != len(want) {
			t.Errorf("%s: Rückweg ergab %d Codes, erwartet %d", satz.Name, len(back), len(want))
			continue
		}
		for i := range want {
			if back[i].Zone != want[i].Zone || back[i].DigitsHex() != want[i].DigitsHex() {
				t.Errorf("%s: Code %d ist %s, erwartet %s", satz.Name, i+1, back[i], want[i])
			}
		}
	}
}

// expand bildet die Sub-codes so ab, wie es der Dekoder tut: je gesetzter
// Teilfläche ein eigener Code, aufsteigend nach Teilflächennummer.
func expand(codes []Code, subs [][]uint8) []Code {
	var out []Code
	for i, c := range codes {
		var s []uint8
		if i < len(subs) {
			s = subs[i]
		}
		if len(s) == 0 {
			out = append(out, c)
			continue
		}
		for sub := range 16 {
			for _, have := range s {
				if int(have) == sub {
					d := append(append([]uint8{}, c.Digits...), uint8(sub))
					out = append(out, Code{Zone: c.Zone, Digits: d})
				}
			}
		}
	}
	return out
}

// Das Cardiff-Beispiel aus TS 104 089, Annex C — der aussagekräftigste
// Testvektor, den die Spec hergibt: vier Location Codes, 22 Byte,
// 17 sphärische Rechtecke. Er legt zugleich die Bitreihenfolge des
// Sub-codes-Feldes fest.
func TestCardiffAnnexC(t *testing.T) {
	codes := []Code{code(10, "B624"), code(10, "B625"), code(10, "B6283"), code(10, "B629")}
	subs := [][]uint8{maskBits(0xCC00), maskBits(0xF730), nil, maskBits(0x0007)}

	raw, err := EncodeLocationCodes(codes, nil, subs)
	if err != nil {
		t.Fatalf("EncodeLocationCodes: %v", err)
	}
	if len(raw) != 22 {
		t.Errorf("Annex C nennt 22 Byte (3 × 6 + 4), kodiert wurden %d", len(raw))
	}

	got, err := DecodeLocationCodes(raw)
	if err != nil {
		t.Fatalf("DecodeLocationCodes: %v", err)
	}
	if len(got) != 17 {
		t.Fatalf("Annex C nennt 17 sphärische Rechtecke, dekodiert wurden %d", len(got))
	}
	for _, c := range got {
		if len(c.Digits) != 5 {
			t.Errorf("%s hat %d Ziffern, Annex C spricht von 5-stelligen Codes", c, len(c.Digits))
		}
	}

	// Die Probe auf die Bitreihenfolge: Das Warngebiet liegt um den
	// gemeinsamen Eckpunkt der vier 4-stelligen Rechtecke B624/B625/B628/B629.
	// In B624 — dem nordwestlichen der vier — müssen die Teilflächen deshalb
	// am *Südost*-Rand liegen. Mit vertauschter Bitreihenfolge lägen sie
	// spiegelbildlich am Nordwest-Rand.
	b624 := filterPrefix(got, "B624")
	if len(b624) != 4 {
		t.Fatalf("B624 hat %d Teilflächen, erwartet 4 (Sub-codes CC00)", len(b624))
	}
	for _, c := range b624 {
		last := c.Digits[4]
		if last>>2 < 2 || last&3 < 2 {
			t.Errorf("Teilfläche %X von B624 liegt nicht im Südosten — Bitreihenfolge der Sub-codes vertauscht?", last)
		}
	}

	// Umgekehrt in B629, dem südöstlichen: dort liegen die Teilflächen am
	// Nordrand (Sub-codes 0007 → Teilflächen 0, 1, 2).
	b629 := filterPrefix(got, "B629")
	if len(b629) != 3 {
		t.Fatalf("B629 hat %d Teilflächen, erwartet 3 (Sub-codes 0007)", len(b629))
	}
	for _, c := range b629 {
		if c.Digits[4]>>2 != 0 {
			t.Errorf("Teilfläche %X von B629 liegt nicht am Nordrand", c.Digits[4])
		}
	}

	// Und die Geometrie muss zusammenhängend um Cardiff liegen.
	const cardiffLat, cardiffLon = 51.4816, -3.1791
	near := 0
	for _, c := range got {
		r, err := c.Rect()
		if err != nil {
			t.Fatalf("Rect(%s): %v", c, err)
		}
		if r.LatMin < cardiffLat+0.2 && r.LatMax > cardiffLat-0.2 &&
			r.LonMin < cardiffLon+0.2 && r.LonMax > cardiffLon-0.2 {
			near++
		}
	}
	if near != 17 {
		t.Errorf("%d von 17 Rechtecken liegen in der Nähe von Cardiff", near)
	}
}

// Dieselbe Probe ohne den eigenen Encoder: LC1 aus Annex C, Bit für Bit von
// Hand aus den Feldangaben der Spec gepackt.
//
//	NFF 00 | Zone 001010 | SCF 1 | Num digits 011 | Digit 1 1011
//	       | Other digits 0110 0010 0100 | Padding 0000
//	       | Sub-codes 1100 1100 0000 0000
//
// ergibt 0a bb 62 40 cc 00 — sechs Byte, und das Sub-codes-Feld steht
// unverändert als "cc00" am Ende. Wer hier die Bitreihenfolge dreht, bekommt
// die spiegelbildlichen Teilflächen.
func TestCardiffLC1AusHandgepacktenBytes(t *testing.T) {
	got, err := DecodeLocationCodesHex("0abb6240cc00")
	if err != nil {
		t.Fatalf("DecodeLocationCodesHex: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("%d Rechtecke, erwartet 4", len(got))
	}
	want := []string{"B624A", "B624B", "B624E", "B624F"}
	for i, c := range got {
		if c.Zone != 10 || c.DigitsHex() != want[i] {
			t.Errorf("Rechteck %d ist %s, erwartet Z10:%s", i+1, c, want[i])
		}
	}
}

func maskBits(mask uint16) []uint8 {
	var out []uint8
	for i := range 16 {
		if mask&(1<<uint(i)) != 0 {
			out = append(out, uint8(i))
		}
	}
	return out
}

func filterPrefix(codes []Code, prefix string) []Code {
	var out []Code
	for _, c := range codes {
		if strings.HasPrefix(c.DigitsHex(), prefix) {
			out = append(out, c)
		}
	}
	return out
}

// Die Fixtures aus asamon-rx tragen echte location_codes-Felder. Sie werden
// hier rückwärts geprüft — das ist die Nahtstelle zwischen den beiden
// Programmen.
func TestFixtureFelder(t *testing.T) {
	cases := []struct {
		name  string
		hex   string
		codes int
	}{
		{"oe_trigger", "0102", 1},
		{"lc3_first_instance", "40591bb8204a591bb82042591bb82069591bb82053591bb820", 5},
		{"lc3_second_instance", "14591bb8200b591bb8200c591bb820", 3},
	}
	for _, c := range cases {
		got, err := DecodeLocationCodesHex(c.hex)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(got) != c.codes {
			t.Errorf("%s: %d Codes dekodiert, erwartet %d: %v", c.name, len(got), c.codes, got)
		}
	}

	// lc5_mixed_subcoding ist der lehrreichste Satz: er mischt sub-codierte
	// und einfache Rechtecke, und nur mit richtiger Padding-Regel geht die
	// Summe auf. 19 Byte, neun L4-Codes.
	got, err := DecodeLocationCodesHex("01a928330001a92c000301391f3001a91b8800")
	if err != nil {
		t.Fatalf("lc5_mixed_subcoding: %v", err)
	}
	if len(got) != 9 {
		t.Errorf("lc5_mixed_subcoding ergab %d Rechtecke, erwartet 9: %v", len(got), got)
	}
	for _, c := range got {
		if len(c.Digits) != 4 {
			t.Errorf("lc5_mixed_subcoding: %s hat %d Ziffern, erwartet 4", c, len(c.Digits))
		}
	}
}

func TestDekoderLehntKaputtesAb(t *testing.T) {
	cases := []struct{ name, hex string }{
		{"abgeschnitten", "40"},
		{"Ziffern fehlen", "4059"},
		{"Sub-codes fehlen", "c159"},
	}
	for _, c := range cases {
		if _, err := DecodeLocationCodesHex(c.hex); err == nil {
			t.Errorf("%s (%s) wurde angenommen", c.name, c.hex)
		}
	}
	if _, err := DecodeLocationCodesHex("xyz"); err == nil {
		t.Errorf("ungültiges Hex wurde angenommen")
	}
	if got, err := DecodeLocationCodesHex(""); err != nil || got != nil {
		t.Errorf("leeres Feld ergab %v, %v", got, err)
	}
}

func FuzzDecodeLocationCodes(f *testing.F) {
	f.Add([]byte{0x40, 0x59, 0x1b, 0xb8, 0x20})
	f.Add([]byte{0x01, 0xa9, 0x28, 0x33, 0x00})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := DecodeLocationCodes(raw)
		if err != nil {
			return
		}
		for _, c := range got {
			if err := c.Valid(); err != nil {
				t.Fatalf("Dekoder lieferte einen ungültigen Code %v: %v", c, err)
			}
			_, _ = c.Rect() // darf fehlschlagen, aber nicht in Panik enden
			_ = c.Presentation()
		}
	})
}
