// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/record"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// Der Ruhezustand ist der Regelfall und trägt die Abdeckungskarte: zehn
// Minuten, 600 Heartbeats, keine Lücke.
func TestZehnMinutenRuhezustand(t *testing.T) {
	abschnitte := spieleAb(t, "heartbeat-10min", Konfig{}, Senken{})

	var erwartet, empfangen, fehlend, pd int
	var fib, crc uint64
	records := 0
	for _, ch := range abschnitte {
		hb := ch.Asa.Heartbeat
		erwartet += hb.Expected
		empfangen += hb.Received
		fehlend += len(hb.MissingSeconds)
		pd += hb.PdMismatch
		fib += ch.Reception.FibTotal
		crc += ch.Reception.FibCrcErr
		records += len(ch.Asa.Records)
		if len(ch.Asa.Alerts) != 0 {
			t.Errorf("im Ruhezustand wurde ein Alert erkannt: %+v", ch.Asa.Alerts)
		}
		if ch.Reception.SeqGaps != 0 || ch.Reception.BrokenLines != 0 {
			t.Errorf("Empfangszähler: %+v", ch.Reception)
		}
	}
	if empfangen != 600 || records != 600 {
		t.Errorf("%d Heartbeats empfangen, %d asa-Records — erwartet je 600", empfangen, records)
	}
	// Im Ruhezustand fehlt keine einzige Sekunde: Das erste Fenster beginnt
	// beim ersten Record, nicht an einer gedachten Sekundengrenze davor.
	if fehlend != 0 {
		t.Errorf("%d fehlende Sekunden, erwartet 0", fehlend)
	}
	if erwartet != 600 {
		t.Errorf("%d erwartete Sekunden, erwartet 600", erwartet)
	}
	// P/D ist bei FIG 0/15 die Sekundenhälfte. Der Erzeuger setzt sie richtig;
	// jede Abweichung wäre ein Fehler in der Ensemble-Uhr.
	if pd != 0 {
		t.Errorf("%d P/D-Abweichungen, erwartet 0", pd)
	}
	if fib == 0 || crc == 0 {
		t.Errorf("Telemetrie wurde nicht aggregiert: fib=%d crc=%d", fib, crc)
	}
	if got := float64(crc) / float64(fib); got > 0.02 {
		t.Errorf("CRC-Quote %.4f, erwartet rund 0.016", got)
	}
}

// Die Kernaussage des ganzen Dedup-Verfahrens: Derselbe Strom ergibt bei zwei
// Knoten mit **verschieden gehenden Uhren** bitgleiche Hashes.
//
// Geprüft wird, indem alle Knotenzeitstempel des Stroms verschoben werden — die
// ens_time aus FIG 0/10 bleibt, wo sie ist. Genau so sieht ein Knoten aus,
// dessen NTP-Uhr um eine halbe Sekunde danebenliegt. Weil die Hashes an der
// Ensemble-Zeit hängen und nicht an der Knotenuhr, darf sich nichts ändern.
func TestHashesHaengenNichtAnDerKnotenuhr(t *testing.T) {
	for _, name := range []string{"alert-einfach", "alert-set-3", "oe-verweis", "heartbeat-luecke"} {
		t.Run(name, func(t *testing.T) {
			basis := hashesAus(t, name, 0)
			if len(basis) == 0 {
				t.Fatal("keine Hashes erzeugt")
			}
			for _, versatz := range []time.Duration{400 * time.Millisecond, -400 * time.Millisecond, 3 * time.Second, -12 * time.Second} {
				got := hashesAus(t, name, versatz)
				if fehlt := differenz(basis, got); len(fehlt) > 0 {
					t.Errorf("Versatz %s: diese Hashes fehlen: %v", versatz, kuerze(fehlt))
				}
				if zuviel := differenz(got, basis); len(zuviel) > 0 {
					t.Errorf("Versatz %s: diese Hashes kamen hinzu: %v", versatz, kuerze(zuviel))
				}
			}
		})
	}
}

// differenz gibt die Werte aus a, die in b fehlen. Verglichen werden Mengen,
// nicht Reihenfolgen: Wie sich die Beobachtungen auf die Berichtsfenster
// verteilen, hängt an der Knotenuhr — die Hashes selbst dürfen es nicht.
func differenz(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, v := range b {
		in[v] = true
	}
	var fehlt []string
	for _, v := range a {
		if !in[v] {
			fehlt = append(fehlt, v)
		}
	}
	return fehlt
}

func kuerze(v []string) []string {
	if len(v) > 5 {
		return append(append([]string{}, v[:5]...), "…")
	}
	return v
}

// hashesAus spielt einen Strom mit verschobener Knotenuhr ab und sammelt alle
// Hashes in Reihenfolge.
func hashesAus(t *testing.T, name string, versatz time.Duration) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "streams", name+".ndjson"))
	if err != nil {
		t.Fatal(err)
	}

	c := Neu(Konfig{Channel: "5C"}, Senken{})
	r := record.NewReader(strings.NewReader(verschiebeTs(t, string(raw), versatz)))

	// Gesammelt wird jeder Wert einmal, in der Reihenfolge des ersten
	// Auftretens. Die Aufteilung in Berichtsfenster darf hier keine Rolle
	// spielen: Sie hängt an der Knotenuhr, und genau deren Einfluss soll der
	// Test ausschließen. Ein Alert erscheint in jedem Fenster erneut, ein
	// asa-Record dagegen genau einmal.
	var hashes []string
	gesehen := map[string]bool{}
	merke := func(v string) {
		if !gesehen[v] {
			gesehen[v] = true
			hashes = append(hashes, v)
		}
	}
	var (
		naechstes time.Time
		letzte    time.Time
	)
	sammle := func(ch report.Channel) {
		if ch.Ensemble != nil {
			merke("ens:" + ch.Ensemble.EnsHash)
			merke("content:" + ch.Ensemble.EnsContentHash)
		}
		for _, rec := range ch.Asa.Records {
			merke("asa:" + rec.AsaHash + "@" + rec.EnsSecond + "/" + rec.TimeSource)
		}
		for _, al := range ch.Asa.Alerts {
			merke("alert:" + al.AlertUID + "/" + al.FirstSeenEns)
		}
	}
	for rec := range r.Alle() {
		if naechstes.IsZero() && !rec.Zeit.IsZero() {
			naechstes = rec.Zeit.Truncate(time.Second).Add(Berichtstakt)
		}
		for !rec.Zeit.IsZero() && !rec.Zeit.Before(naechstes) {
			sammle(c.Schnappschuss(naechstes))
			naechstes = naechstes.Add(Berichtstakt)
		}
		c.Verarbeite(rec)
		if rec.Zeit.After(letzte) {
			letzte = rec.Zeit
		}
	}
	if !letzte.IsZero() {
		c.Fristen(letzte)
		sammle(c.Schnappschuss(letzte))
	}
	return hashes
}

// verschiebeTs rückt alle ts-Felder um versatz, lässt ens_time aber stehen.
func verschiebeTs(t *testing.T, strom string, versatz time.Duration) string {
	t.Helper()
	if versatz == 0 {
		return strom
	}
	var out strings.Builder
	for zeile := range strings.SplitSeq(strom, "\n") {
		if zeile == "" {
			continue
		}
		var felder map[string]json.RawMessage
		if err := json.Unmarshal([]byte(zeile), &felder); err != nil {
			t.Fatal(err)
		}
		var ts string
		if err := json.Unmarshal(felder["ts"], &ts); err != nil {
			t.Fatal(err)
		}
		alt, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			t.Fatal(err)
		}
		neu, err := json.Marshal(alt.Add(versatz).UTC().Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
		felder["ts"] = neu
		raw, err := json.Marshal(felder)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.String()
}

// Fehlt die Ensemble-Zeit, fällt der Knoten auf die eigene Uhr zurück — und das
// muss im Datensatz stehen. Ein Knoten in diesem Zustand kann an einer
// Sekundengrenze danebenliegen; der Server muss das wissen.
func TestOhneEnsembleZeitFaelltDieUhrZurueck(t *testing.T) {
	strom := `{"type":"init","seq":0,"ts":"2026-08-26T14:00:00Z","format_version":1,"channel":"5C"}
{"type":"ens","seq":1,"ts":"2026-08-26T14:00:00Z","eid":"0x10FF","ecc":224,"label":"X"}
{"type":"tlm","seq":2,"ts":"2026-08-26T14:00:01Z","fib_total":125,"eid":"0x10FF"}
{"type":"asa","seq":3,"ts":"2026-08-26T14:00:01.5Z","heartbeat":true,"cn":true,"raw":"018f"}
`
	c := Neu(Konfig{Channel: "5C"}, Senken{})
	for rec := range record.NewReader(strings.NewReader(strom)).Alle() {
		c.Verarbeite(rec)
	}
	ch := c.Schnappschuss(time.Date(2026, 8, 26, 14, 0, 10, 0, time.UTC))
	if len(ch.Asa.Records) != 1 {
		t.Fatalf("%d asa-Records", len(ch.Asa.Records))
	}
	if got := ch.Asa.Records[0].TimeSource; got != report.ZeitAusKnoten {
		t.Errorf("time_source ist %q, erwartet %q", got, report.ZeitAusKnoten)
	}
	if ch.Reception.EnsTimeOffsetM != nil {
		t.Errorf("ens_time_offset_ms ist gesetzt, obwohl keine ens_time kam: %v", *ch.Reception.EnsTimeOffsetM)
	}
	// Ohne Ensemble-Zeit gibt es keine P/D-Prüfung: Sie wäre gegen die eigene
	// Uhr geführt und damit gegenstandslos.
	if ch.Asa.Heartbeat.PdMismatch != 0 {
		t.Errorf("P/D wurde ohne Ensemble-Zeit geprüft: %d Abweichungen", ch.Asa.Heartbeat.PdMismatch)
	}
}

// Ein Strom-Neustart darf laufende Alerts nicht stillschweigend fortführen.
func TestStromNeustartMarkiertLaufendeAlerts(t *testing.T) {
	c := Neu(Konfig{Channel: "5C"}, Senken{})
	t0 := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)

	fuettere(t, c, `{"type":"ens","seq":0,"ts":"2026-08-26T14:00:00Z","eid":"0x10FF","ecc":224,"label":"X"}
{"type":"tlm","seq":1,"ts":"2026-08-26T14:00:00Z","ens_time":"2026-08-26T14:00:00Z","fib_total":125}
{"type":"asa","seq":2,"ts":"2026-08-26T14:00:01Z","phase":"trigger","subch_id":7,"stage":"level1_start","iid":3,"last":true,"raw":"040f4783"}`)

	if c.OffeneAlerts() != 1 {
		t.Fatalf("%d offene Alerts", c.OffeneAlerts())
	}
	c.Melde(Zustandsmeldung{Neustart: true, RxZustand: report.RxRunning, Neustarts: 1}, t0.Add(2*time.Second))

	ch := c.Schnappschuss(t0.Add(10 * time.Second))
	if len(ch.Asa.Alerts) != 1 {
		t.Fatalf("%d Alerts im Datensatz", len(ch.Asa.Alerts))
	}
	if !ch.Asa.Alerts[0].Gap {
		t.Error("der Alert überlebte den Strom-Neustart ohne gap: true")
	}
	if ch.RxRestarts != 1 {
		t.Errorf("rx_restarts ist %d", ch.RxRestarts)
	}
	if len(ch.Asa.Anomalies) == 0 {
		t.Error("der Neustart erzeugte keine Auffälligkeit")
	}
}

// Ein zweiter eigener Alert mit anderem IId ist eine Auffälligkeit, kein
// Programmfehler: melden und beide verfolgen.
func TestZweiterEigenerAlertWirdGemeldetUndVerfolgt(t *testing.T) {
	c := Neu(Konfig{Channel: "5C"}, Senken{})
	t0 := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)
	fuettere(t, c, `{"type":"ens","seq":0,"ts":"2026-08-26T14:00:00Z","eid":"0x10FF","ecc":224,"label":"X"}
{"type":"tlm","seq":1,"ts":"2026-08-26T14:00:00Z","ens_time":"2026-08-26T14:00:00Z","fib_total":125}
{"type":"asa","seq":2,"ts":"2026-08-26T14:00:01Z","phase":"trigger","subch_id":7,"stage":"level1_start","iid":3,"last":true,"raw":"040f4783"}
{"type":"asa","seq":3,"ts":"2026-08-26T14:00:02Z","phase":"trigger","subch_id":7,"stage":"level1_start","iid":9,"last":true,"raw":"040f4789"}`)

	ch := c.Schnappschuss(t0.Add(10 * time.Second))
	if len(ch.Asa.Alerts) != 2 {
		t.Fatalf("%d Alerts, erwartet 2", len(ch.Asa.Alerts))
	}
	if ch.Asa.Alerts[0].AlertUID == ch.Asa.Alerts[1].AlertUID {
		t.Error("beide Alerts bekamen denselben alert_uid")
	}
	gemeldet := strings.Join(ch.Asa.Anomalies, " ")
	if !strings.Contains(gemeldet, "zweiter eigener Alert") {
		t.Errorf("die Auffälligkeit fehlt: %v", ch.Asa.Anomalies)
	}
}

// Ein Test-Alert wird vollwertig verarbeitet, aber hart getrennt gekennzeichnet:
// Consumer-Geräte ignorieren ihn, ein Monitor gerade nicht.
func TestStageTestWirdGekennzeichnet(t *testing.T) {
	abschnitte := spieleAb(t, "stage-test", Konfig{}, Senken{})
	gefunden := false
	for _, ch := range abschnitte {
		for _, al := range ch.Asa.Alerts {
			gefunden = true
			if !al.Test {
				t.Errorf("Stage %q ohne test: true", al.Stage)
			}
			if al.Level != nil {
				t.Errorf("ein Test-Alert bekam level %d — die Warnstufe ist dort nicht definiert", *al.Level)
			}
			if al.Stage != "test" {
				t.Errorf("stage ist %q", al.Stage)
			}
		}
	}
	if !gefunden {
		t.Error("kein Alert erkannt")
	}
}

func fuettere(t *testing.T, c *Kanal, strom string) {
	t.Helper()
	for rec := range record.NewReader(strings.NewReader(strom + "\n")).Alle() {
		c.Verarbeite(rec)
	}
}
