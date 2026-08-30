// SPDX-License-Identifier: GPL-3.0-or-later

package hashes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	ensHash5C = Ens("5C", "0x10FF", 224)
	zeit      = time.Date(2026, 8, 26, 14, 3, 11, 482913771, time.UTC)
	dienste   = []Service{{
		Sid:   "0x0D3110AB",
		Label: "ASA DE",
		Komponenten: []Komponente{
			{SubChID: 7, StartAddr: 128, Size: 48, Protection: "EEP 2-A", Bitrate: 32},
		},
	}}
)

// Die Testvektoren aus docs/hashes.md. Ändert sich eine Definition, muss das
// Präfix steigen — und dann fällt genau dieser Test auf.
func TestTestvektorenAusDokument(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"ens_hash 5C", ensHash5C, "c0a8ceb1d0908a3b1b7610b315e097f8"},
		{"ens_hash 11D", Ens("11D", "0x10FF", 224), "90587d3818a385eb5b8a47e6cbda4a34"},
		{"ens_content_hash", EnsContent(ensHash5C, "Bundesmux 1", dienste), "035c382ff71364293f568a645ae36883"},
		{"ens_content_hash leer", EnsContent(ensHash5C, "", nil), "37ac75a94fe8722c5a65c0870474071b"},
		{"asa_hash 14:03:11", Asa(ensHash5C, zeit, "018f"), "b558307b47c1a469c719bfa623c404c4"},
		{"asa_hash 14:03:12", Asa(ensHash5C, zeit.Add(time.Second), "018f"), "65f4d5c86dff70b4e59515c5f41ca1e5"},
		{"alert_uid iid 3", Alert("0x10FF", 3, true, zeit), "40916a0e9648a82f6427174b46b9663b"},
		{"alert_uid ohne iid", Alert("0x10FF", 0, false, zeit), "d5c56498e3f0458600b62fc6cff3ecc2"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s ist %s, docs/hashes.md nennt %s", c.name, c.got, c.want)
		}
	}
}

// Jeder Testvektor muss auch wirklich im Dokument stehen. Sonst driftet die
// Definition still von ihrer Beschreibung weg.
func TestDokumentNenntDieselbenWerte(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "hashes.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, wert := range []string{
		ensHash5C,
		Ens("11D", "0x10FF", 224),
		EnsContent(ensHash5C, "Bundesmux 1", dienste),
		EnsContent(ensHash5C, "", nil),
		Asa(ensHash5C, zeit, "018f"),
		Asa(ensHash5C, zeit.Add(time.Second), "018f"),
		Alert("0x10FF", 3, true, zeit),
		Alert("0x10FF", 0, false, zeit),
	} {
		if !strings.Contains(text, wert) {
			t.Errorf("docs/hashes.md nennt %s nicht", wert)
		}
	}
	for _, praefix := range []string{PraefixEns, PraefixEnsContent, PraefixAsa, PraefixAlert} {
		if !strings.Contains(text, praefix) {
			t.Errorf("docs/hashes.md nennt das Präfix %s nicht", praefix)
		}
	}
}

func TestHexWirdNormalisiert(t *testing.T) {
	cases := []struct {
		in     string
		breite int
		want   string
	}{
		{"0x10FF", 4, "10ff"},
		{"10FF", 4, "10ff"},
		{"10ff", 4, "10ff"},
		{"0X10ff", 4, "10ff"},
		{" 0x10FF ", 4, "10ff"},
		{"ff", 4, "00ff"},
		{"0x0D3110AB", 8, "0d3110ab"},
		{"", 4, "0000"},
	}
	for _, c := range cases {
		if got := Hex(c.in, c.breite); got != c.want {
			t.Errorf("Hex(%q, %d) = %q, erwartet %q", c.in, c.breite, got, c.want)
		}
	}
	// Verschiedene Schreibweisen derselben EId müssen denselben Hash ergeben —
	// das ist der ganze Zweck der Normalisierung.
	if Ens("5C", "0x10FF", 224) != Ens("5C", "10ff", 224) {
		t.Error("0x10FF und 10ff ergaben verschiedene ens_hash")
	}
}

func TestZeitenWerdenAbgeschnitten(t *testing.T) {
	if Sekunde(zeit) != "2026-08-26T14:03:11Z" {
		t.Errorf("Sekunde: %q", Sekunde(zeit))
	}
	if Minute(zeit) != "2026-08-26T14:03:00Z" {
		t.Errorf("Minute: %q", Minute(zeit))
	}
	// Zonen dürfen keine Rolle spielen.
	ost := time.FixedZone("MESZ", 2*3600)
	gleich := zeit.In(ost)
	if Sekunde(gleich) != Sekunde(zeit) {
		t.Errorf("die Zeitzone schlägt durch: %q vs. %q", Sekunde(gleich), Sekunde(zeit))
	}
	// Bruchteile innerhalb derselben Sekunde ergeben denselben Hash — das ist
	// erwünscht: Wiederholungen innerhalb der Sekunde sind dieselbe Beobachtung.
	if Asa(ensHash5C, zeit, "018f") != Asa(ensHash5C, zeit.Truncate(time.Second).Add(999*time.Millisecond), "018f") {
		t.Error("die Bruchteile der Sekunde schlagen in den asa_hash durch")
	}
}

func TestSortierungMachtUnabhaengigVonDerReihenfolge(t *testing.T) {
	a := []Service{
		{Sid: "0x0D3110AB", Label: "ASA DE", Komponenten: []Komponente{
			{SubChID: 7, StartAddr: 128, Size: 48, Protection: "EEP 2-A", Bitrate: 32},
			{SubChID: 2, StartAddr: 0, Size: 84, Protection: "EEP 3-A", Bitrate: 56},
		}},
		{Sid: "0x0D3110AC", Label: "Zweiter", Komponenten: nil},
	}
	b := []Service{
		{Sid: "0x0D3110AC", Label: "Zweiter", Komponenten: nil},
		{Sid: "0x0D3110AB", Label: "ASA DE", Komponenten: []Komponente{
			{SubChID: 2, StartAddr: 0, Size: 84, Protection: "EEP 3-A", Bitrate: 56},
			{SubChID: 7, StartAddr: 128, Size: 48, Protection: "EEP 2-A", Bitrate: 32},
		}},
	}
	if EnsContent(ensHash5C, "Bundesmux 1", a) != EnsContent(ensHash5C, "Bundesmux 1", b) {
		t.Error("die Reihenfolge von Services und Komponenten schlägt in den Hash durch")
	}
	// Eine echte Änderung muss dagegen auffallen.
	c := append([]Service{}, a...)
	c[1].Label = "Anders"
	if EnsContent(ensHash5C, "Bundesmux 1", a) == EnsContent(ensHash5C, "Bundesmux 1", c) {
		t.Error("ein geändertes Service-Label ändert den Hash nicht")
	}
}

func TestHashartenKollidierenNicht(t *testing.T) {
	// Die Präfixe sind genau dafür da: Selbst wenn zwei Hasharten dieselben
	// Felder bekämen, dürfen sie nie denselben Wert liefern.
	gesehen := map[string]string{}
	for name, wert := range map[string]string{
		"ens":        Ens("5C", "0x0000", 0),
		"enscontent": EnsContent("", "", nil),
		"asa":        Asa("", time.Unix(0, 0), ""),
		"alert":      Alert("0x0000", 0, true, time.Unix(0, 0)),
	} {
		if vorher, doppelt := gesehen[wert]; doppelt {
			t.Errorf("%s und %s ergaben denselben Hash %s", vorher, name, wert)
		}
		gesehen[wert] = name
	}
}

func TestDigestLaenge(t *testing.T) {
	d := Digest("a", "b")
	if len(d) != 32 {
		t.Errorf("Digest hat %d Zeichen, erwartet 32 (16 Byte)", len(d))
	}
	if strings.ToLower(d) != d {
		t.Errorf("Digest ist nicht durchgehend klein: %s", d)
	}
	// Die Trennung mit \n muss echte Trennung sein: "ab" und "a"+"b" dürfen
	// nicht kollidieren.
	if Digest("ab") == Digest("a", "b") {
		t.Error("die Feldtrennung wirkt nicht")
	}
}
