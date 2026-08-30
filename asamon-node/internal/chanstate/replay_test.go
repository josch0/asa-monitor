// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/record"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

var aktualisiere = flag.Bool("update", false, "Golden-Dateien neu schreiben")

// Berichtstakt ist der Takt, in dem die Testläufe Schnappschüsse ziehen —
// derselbe wie die Vorgabe im Betrieb.
const Berichtstakt = 10 * time.Second

// spieleAb liest einen Strom aus testdata/streams und gibt die Kanalabschnitte
// aller Berichtsfenster.
//
// Der Lauf ist die reine Funktion, um die es geht: Was herauskommt, hängt
// allein am Strom und an den Zeitstempeln darin — nicht an der Uhr des
// Rechners, auf dem der Test läuft.
func spieleAb(t *testing.T, name string, k Konfig, s Senken) []report.Channel {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "streams", name+".ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if k.Channel == "" {
		k.Channel = "5C"
	}
	c := Neu(k, s)
	r := record.NewReader(f)

	var (
		abschnitte []report.Channel
		naechstes  time.Time
		letzte     time.Time
	)
	for rec := range r.Alle() {
		if naechstes.IsZero() && !rec.Zeit.IsZero() {
			naechstes = rec.Zeit.Truncate(time.Second).Add(Berichtstakt)
		}
		for !rec.Zeit.IsZero() && !rec.Zeit.Before(naechstes) {
			abschnitte = append(abschnitte, c.Schnappschuss(naechstes))
			naechstes = naechstes.Add(Berichtstakt)
		}
		c.Verarbeite(rec)
		if rec.Zeit.After(letzte) {
			letzte = rec.Zeit
		}
	}
	if !letzte.IsZero() {
		c.Fristen(letzte)
		abschnitte = append(abschnitte, c.Schnappschuss(letzte))
	}
	return abschnitte
}

// vergleicheMitGolden prüft das Ergebnis gegen testdata/golden.
//
// Golden-Files, keine handgeschriebenen Zusicherungen: Eine Parseränderung wird
// so als Diff sichtbar und nicht nur als rote Zusicherung.
//
// Neu schreiben: go test ./internal/chanstate -update
func vergleicheMitGolden(t *testing.T, name string, wert any) {
	t.Helper()
	pfad := filepath.Join("..", "..", "testdata", "golden", name+".json")

	raw, err := json.MarshalIndent(wert, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')

	if *aktualisiere {
		if err := os.WriteFile(pfad, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s neu geschrieben (%d Byte)", pfad, len(raw))
		return
	}

	soll, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatalf("%v — neu schreiben mit: go test ./internal/chanstate -update", err)
	}
	if string(raw) == string(strings.ReplaceAll(string(soll), "\r\n", "\n")) {
		return
	}
	t.Errorf("%s weicht ab. Erste Abweichung:\n%s", pfad, ersteAbweichung(string(soll), string(raw)))
}

func ersteAbweichung(soll, ist string) string {
	sz := strings.Split(strings.ReplaceAll(soll, "\r\n", "\n"), "\n")
	iz := strings.Split(ist, "\n")
	for i := 0; i < len(sz) || i < len(iz); i++ {
		s, j := "", ""
		if i < len(sz) {
			s = sz[i]
		}
		if i < len(iz) {
			j = iz[i]
		}
		if s != j {
			return "Zeile " + itoa(i+1) + "\n  golden: " + s + "\n  jetzt:  " + j
		}
	}
	return "(kein Zeilenunterschied — vermutlich am Zeilenende)"
}

func TestGoldenSzenarien(t *testing.T) {
	// heartbeat-10min fehlt hier mit Absicht: 61 Fenster à zehn gleiche
	// Heartbeats ergäben 300 kB Golden-Datei ohne Aussagekraft. Der Strom wird
	// stattdessen in TestZehnMinutenRuhezustand über seine Summen geprüft.
	szenarien := []string{
		"heartbeat-luecke",
		"alert-einfach",
		"alert-set-3",
		"einstieg-sustain",
		"alert-abgebrochen",
		"oe-verweis",
		"stage-test",
	}
	for _, name := range szenarien {
		t.Run(name, func(t *testing.T) {
			abschnitte := spieleAb(t, name, Konfig{}, Senken{})
			if len(abschnitte) == 0 {
				t.Fatal("keine Datensätze erzeugt")
			}
			vergleicheMitGolden(t, name, abschnitte)
		})
	}
}
