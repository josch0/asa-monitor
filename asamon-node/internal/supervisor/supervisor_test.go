// SPDX-License-Identifier: GPL-3.0-or-later

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/config"
	"github.com/josch0/asa-monitor/asamon-node/internal/identity"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
	"github.com/josch0/asa-monitor/asamon-node/internal/rxproc"
	"github.com/josch0/asa-monitor/asamon-node/internal/uplink"
)

var (
	einmal     sync.Once
	fakeRxPfad string
	fakeRxErr  error
)

func baueFakeRx(t *testing.T) string {
	t.Helper()
	einmal.Do(func() {
		dir, err := os.MkdirTemp("", "asamon-sup-fake-rx-*")
		if err != nil {
			fakeRxErr = err
			return
		}
		name := "fake-rx"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		fakeRxPfad = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", fakeRxPfad, "github.com/josch0/asa-monitor/asamon-node/cmd/fake-rx")
		if aus, err := cmd.CombinedOutput(); err != nil {
			fakeRxErr = fmt.Errorf("%w\n%s", err, aus)
		}
	})
	if fakeRxErr != nil {
		t.Fatalf("fake-rx ließ sich nicht bauen: %v", fakeRxErr)
	}
	return fakeRxPfad
}

func stillesLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func stromPfad(name string) string {
	return filepath.Join("..", "..", "testdata", "streams", name+".ndjson")
}

// umgebung baut Konfiguration und Identität für einen Testknoten.
func umgebung(t *testing.T, serverURL string, kanaele ...string) (*config.Config, *identity.Identity) {
	t.Helper()
	dir := t.TempDir()
	bin := baueFakeRx(t)

	if len(kanaele) == 0 {
		kanaele = []string{"5C"}
	}
	var chs []config.Channel
	for i, name := range kanaele {
		ch := config.Channel{Channel: name, Device: "rtl_sdr", Gain: "auto"}
		if len(kanaele) > 1 {
			ch.DeviceSerial = fmt.Sprintf("0000000%d", i+1)
		}
		chs = append(chs, ch)
	}

	cfg := config.Defaults()
	cfg.Node.Name = "Testknoten"
	cfg.Node.LocationCode = "2366-7443-8484"
	cfg.Server.URL = serverURL
	cfg.Server.ReportInterval = config.Duration(time.Second)
	cfg.Server.Timeout = config.Duration(2 * time.Second)
	cfg.Channels = chs
	cfg.Paths.RxBinary = bin
	cfg.Paths.StateDir = filepath.Join(dir, "state")
	cfg.Limits.MaxReportsPerReques = 10
	cfg.Audio.Enabled = false

	if _, err := cfg.Validate(); err != nil {
		t.Fatalf("Testkonfiguration ist ungültig: %v", err)
	}
	id, err := identity.Load(cfg.Paths.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	return &cfg, id
}

// N3-Abnahme: Drei Kanäle mit fake-rx; einer stürzt wiederholt ab, startet mit
// Backoff neu, die anderen laufen unterbrechungsfrei weiter. SIGTERM beendet in
// unter 20 s ohne Zombie-Prozess.
func TestEinAbstuerzenderKanalReisstDieAnderenNichtMit(t *testing.T) {
	var datensaetze atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u uplink.Umschlag
		json.NewDecoder(r.Body).Decode(&u)
		var seqs []uint64
		for _, rep := range u.Reports {
			seqs = append(seqs, rep.Seq)
			datensaetze.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"accepted": seqs})
	}))
	defer srv.Close()

	cfg, id := umgebung(t, srv.URL, "5C", "11D", "7B")
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	// Alle drei Kanäle spielen dieselbe Aufzeichnung ab; der mittlere stürzt
	// dabei alle paar Records ab.
	for i, k := range sup.kanaele {
		vorab := []string{"--serve", "--file", stromPfad("heartbeat-10min"), "--speed", "1"}
		if i == 1 {
			vorab = append(vorab, "--crash-after", "6")
		}
		k.rx = neuRx(t, k.name, cfg.Paths.RxBinary, vorab)
	}

	ctx, ende := context.WithCancel(context.Background())
	fertig := make(chan error, 1)
	go func() { fertig <- sup.Run(ctx) }()

	// Lange genug laufen lassen, dass mehrere Datensätze und mehrere Neustarts
	// zusammenkommen.
	time.Sleep(12 * time.Second)

	if sup.kanaele[1].rx.Neustarts() < 2 {
		t.Errorf("der abstürzende Kanal startete nur %d mal neu", sup.kanaele[1].rx.Neustarts())
	}
	for _, i := range []int{0, 2} {
		if n := sup.kanaele[i].rx.Neustarts(); n > 1 {
			t.Errorf("Kanal %s startete %d mal neu, obwohl er nicht abstürzt", sup.kanaele[i].name, n)
		}
		if sup.kanaele[i].panics.Load() != 0 {
			t.Errorf("Kanal %s meldete eine Panik", sup.kanaele[i].name)
		}
	}
	if datensaetze.Load() < 5 {
		t.Errorf("nur %d Datensätze in 12 s bei 1-s-Takt", datensaetze.Load())
	}

	begonnen := time.Now()
	ende()
	select {
	case err := <-fertig:
		if err != nil {
			t.Errorf("Run endete mit %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("der Knoten war nach 30 s nicht beendet")
	}
	if dauer := time.Since(begonnen); dauer > AbschaltFrist+5*time.Second {
		t.Errorf("das Herunterfahren dauerte %s", dauer.Round(time.Millisecond))
	}
}

// N6-Abnahme: Gegen einen Server, der eine Weile ausfällt — danach kommen alle
// Datensätze lückenlos und in Reihenfolge an, Duplikate werden als solche
// verbucht, und im Normalbetrieb findet **kein** Schreibvorgang im Spool statt.
func TestServerausfallUndNachlieferung(t *testing.T) {
	var (
		mu        sync.Mutex
		empfangen []uint64
		aus       atomic.Bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if aus.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var u uplink.Umschlag
		json.NewDecoder(r.Body).Decode(&u)
		mu.Lock()
		var seqs []uint64
		for _, rep := range u.Reports {
			empfangen = append(empfangen, rep.Seq)
			seqs = append(seqs, rep.Seq)
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"accepted": seqs})
	}))
	defer srv.Close()

	cfg, id := umgebung(t, srv.URL)
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	sup.kanaele[0].rx = neuRx(t, "5C", cfg.Paths.RxBinary,
		[]string{"--serve", "--file", stromPfad("heartbeat-10min"), "--speed", "1"})

	ctx, ende := context.WithCancel(context.Background())
	fertig := make(chan error, 1)
	go func() { fertig <- sup.Run(ctx) }()

	// Normalbetrieb: nichts darf auf die Platte.
	time.Sleep(4 * time.Second)
	if geschrieben, _ := sup.spl.Zaehler(); geschrieben != 0 {
		t.Errorf("%d Schreibvorgänge im Spool trotz laufendem Server", geschrieben)
	}

	// Server fällt aus.
	aus.Store(true)
	time.Sleep(6 * time.Second)
	geschrieben, _ := sup.spl.Zaehler()
	if geschrieben == 0 {
		t.Error("während des Ausfalls wurde nichts im Spool abgelegt")
	}

	// Server kommt zurück.
	aus.Store(false)
	warten := time.After(30 * time.Second)
	for {
		if sup.spl.Leer() {
			break
		}
		select {
		case <-warten:
			t.Fatalf("der Spool wurde nicht geleert: %+v", sup.spl.Stand())
		case <-time.After(200 * time.Millisecond):
		}
	}

	ende()
	<-fertig

	mu.Lock()
	defer mu.Unlock()
	if len(empfangen) < 8 {
		t.Fatalf("nur %d Datensätze angekommen", len(empfangen))
	}
	// Lückenlos und in Reihenfolge.
	for i := 1; i < len(empfangen); i++ {
		if empfangen[i] <= empfangen[i-1] {
			t.Errorf("Reihenfolge verletzt: %d nach %d", empfangen[i], empfangen[i-1])
		}
		if empfangen[i] != empfangen[i-1]+1 {
			t.Errorf("Lücke zwischen %d und %d", empfangen[i-1], empfangen[i])
		}
	}
	t.Logf("%d Datensätze, seq %d..%d, %d im Spool geschrieben",
		len(empfangen), empfangen[0], empfangen[len(empfangen)-1], geschrieben)
}

// Ein dauerhaft abgelehnter Datensatz darf den Spool nicht füllen.
func TestDauerhaftAbgelehnteWerdenVerworfen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("report_version unsupported"))
	}))
	defer srv.Close()

	cfg, id := umgebung(t, srv.URL)
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	sup.kanaele[0].rx = neuRx(t, "5C", cfg.Paths.RxBinary,
		[]string{"--serve", "--file", stromPfad("heartbeat-10min"), "--speed", "1"})

	ctx, ende := context.WithCancel(context.Background())
	fertig := make(chan error, 1)
	go func() { fertig <- sup.Run(ctx) }()
	time.Sleep(5 * time.Second)
	ende()
	<-fertig

	if sup.verworfeneReports.Load() == 0 {
		t.Error("kein Datensatz wurde verworfen")
	}
	if geschrieben, _ := sup.spl.Zaehler(); geschrieben > 1 {
		t.Errorf("%d abgelehnte Datensätze landeten trotzdem im Spool", geschrieben)
	}
}

// Der erste Datensatz geht sofort beim Start raus — er ist die Anmeldung des
// Knotens. Und er trägt alles, was der Server über den Knoten wissen muss.
func TestAnmeldedatensatz(t *testing.T) {
	empfangen := make(chan *report.Report, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u uplink.Umschlag
		json.NewDecoder(r.Body).Decode(&u)
		for _, rep := range u.Reports {
			select {
			case empfangen <- rep:
			default:
			}
		}
		w.Write([]byte(`{"accepted":[]}`))
	}))
	defer srv.Close()

	cfg, id := umgebung(t, srv.URL)
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	sup.kanaele[0].rx = neuRx(t, "5C", cfg.Paths.RxBinary,
		[]string{"--serve", "--file", stromPfad("heartbeat-10min"), "--speed", "1"})

	ctx, ende := context.WithCancel(context.Background())
	fertig := make(chan error, 1)
	go func() { fertig <- sup.Run(ctx) }()

	var erster *report.Report
	select {
	case erster = <-empfangen:
	case <-time.After(10 * time.Second):
		t.Fatal("kein Datensatz in 10 s")
	}
	ende()
	<-fertig

	if erster.Trigger != report.TriggerStartup {
		t.Errorf("trigger ist %q, erwartet %q", erster.Trigger, report.TriggerStartup)
	}
	if erster.ReportVersion != report.Version {
		t.Errorf("report_version ist %d", erster.ReportVersion)
	}
	if erster.Node.NodeID != id.NodeID {
		t.Errorf("node_id ist %q", erster.Node.NodeID)
	}
	if erster.Node.PubKey == "" {
		t.Error("pubkey fehlt")
	}
	if erster.Node.LocationCode != "2366-7443-8484" {
		t.Errorf("location_code ist %q", erster.Node.LocationCode)
	}
	if erster.Node.Location.Zone != 10 || erster.Node.Location.Digits != "B736BB" {
		t.Errorf("location: %+v", erster.Node.Location)
	}
	if erster.Node.Location.Lat == 0 || erster.Node.Location.LatMin == 0 {
		t.Errorf("die Geometrie fehlt: %+v", erster.Node.Location)
	}
	if erster.Node.Platform == "" || erster.Node.NodeVersion == "" {
		t.Errorf("Version oder Plattform fehlen: %+v", erster.Node)
	}
	if len(erster.Channels) != 1 || erster.Channels[0].Channel != "5C" {
		t.Errorf("channels: %+v", erster.Channels)
	}
	// Auch ein leerer Datensatz geht raus — sonst kann der Server "Ensemble
	// schweigt" nicht von "Knoten ist tot" unterscheiden.
	if erster.Channels[0].Asa.Records == nil {
		t.Error("asa.records ist null statt einer leeren Liste")
	}
}

// Die EId-Tabelle löst OE-Verweise quer über die Kanäle auf.
func TestOeAufloesungUeberKanaele(t *testing.T) {
	cfg, id := umgebung(t, "https://example.invalid", "5C", "11D")
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := sup.kanaele[0], sup.kanaele[1]

	sup.merkeEid("10ff", a)
	sup.merkeEid("20ab", b)

	if sup.loeseOeAuf("20ab", a) != true {
		t.Error("der OE-Verweis auf einen bekannten Kanal wurde nicht aufgelöst")
	}
	select {
	case bis := <-b.bereitschaft:
		if time.Until(bis) < BereitschaftsDauer/2 {
			t.Errorf("die Bereitschaft läuft schon in %s ab", time.Until(bis))
		}
	default:
		t.Error("der Zielkanal wurde nicht in Bereitschaft versetzt")
	}

	// Ein Verweis auf das eigene Ensemble ist keine Auflösung.
	if sup.loeseOeAuf("10ff", a) {
		t.Error("ein Verweis auf den eigenen Kanal wurde aufgelöst")
	}
	// Und ein unbekanntes Ensemble ebenfalls nicht — der Alert wird trotzdem
	// gemeldet, er ist oft das früheste Signal im ganzen Netz.
	if sup.loeseOeAuf("dead", a) {
		t.Error("ein unbekanntes Ensemble wurde aufgelöst")
	}
}

// Ein Kanal, der nicht antwortet, darf den Datensatz nicht aufhalten. Sein
// Hängen ist selbst die Meldung.
func TestHaengenderKanalWirdGemeldet(t *testing.T) {
	cfg, id := umgebung(t, "https://example.invalid")
	sup, err := Neu(cfg, id, stillesLog(), Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	// Die Kanal-Goroutine läuft gar nicht: Niemand liest die Anfragen.
	begonnen := time.Now()
	ch := sup.kanaele[0].schnappschuss()
	dauer := time.Since(begonnen)

	if ch.RxState != report.RxStalled {
		t.Errorf("rx_state ist %q, erwartet %q", ch.RxState, report.RxStalled)
	}
	if ch.LastError == "" {
		t.Error("last_error fehlt")
	}
	if dauer > 2*SchnappschussFrist {
		t.Errorf("die Antwort dauerte %s, die Frist ist %s", dauer, SchnappschussFrist)
	}
	if ch.Asa.Records == nil || ch.Asa.Alerts == nil {
		t.Error("der Ersatzabschnitt hat null-Listen statt leerer")
	}
}

func TestReplayVerzeichnis(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "5C.ndjson"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := replayDatei(dir, "5C")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "5C.ndjson") {
		t.Errorf("replayDatei gab %q", got)
	}
	if _, err := replayDatei(dir, "11D"); err == nil {
		t.Error("ein fehlender Kanalstrom wurde angenommen")
	}
	datei := filepath.Join(dir, "5C.ndjson")
	if got, err := replayDatei(datei, "11D"); err != nil || got != datei {
		t.Errorf("eine einzelne Datei gilt für jeden Kanal: %q, %v", got, err)
	}
}

// neuRx baut eine Prozessverwaltung, die fake-rx mit einer Aufzeichnung
// betreibt — der Ersatz für einen SDR-Stick.
func neuRx(t *testing.T, channel, binary string, vorab []string) *rxproc.Prozess {
	t.Helper()
	return rxproc.Neu(rxproc.Konfig{
		Channel:        channel,
		Binary:         binary,
		QueueSize:      8192,
		VorabArgumente: vorab,
	}, stillesLog())
}

// Die Gesamtkette: Aufzeichnung → Datensätze, ohne Netz und ohne Stick.
//
// Das ist der Lauf, der --replay --dry-run entspricht, und zugleich der
// einzige Weg, ASA-Verkehr zu prüfen, bevor es welchen gibt.
func TestGesamtketteAusAufzeichnung(t *testing.T) {
	cfg, id := umgebung(t, "https://example.invalid")
	cfg.Audio.Enabled = true
	cfg.Audio.PostRoll = config.Duration(2 * time.Second)

	var mu sync.Mutex
	var datensaetze []*report.Report
	sup, err := Neu(cfg, id, stillesLog(), Optionen{
		DryRun: true,
		Ausgabe: func(r *report.Report) {
			mu.Lock()
			datensaetze = append(datensaetze, r)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sup.kanaele[0].rx = neuRx(t, "5C", cfg.Paths.RxBinary,
		[]string{"--serve", "--file", stromPfad("alert-audio"), "--speed", "8"})

	ctx, ende := context.WithCancel(context.Background())
	fertig := make(chan error, 1)
	go func() { fertig <- sup.Run(ctx) }()
	// Der Strom ist 60 s lang und läuft achtfach: rund 8 s.
	time.Sleep(14 * time.Second)
	ende()
	<-fertig

	mu.Lock()
	defer mu.Unlock()
	if len(datensaetze) < 4 {
		t.Fatalf("nur %d Datensätze", len(datensaetze))
	}

	// Jeder Datensatz muss für sich vollständig sein.
	var (
		alerts     = map[string]*report.Alert{}
		asaRecords int
		sahAudio   bool
	)
	for i, r := range datensaetze {
		if r.ReportVersion != report.Version || r.Seq == 0 || r.GeneratedAt == "" {
			t.Errorf("Datensatz %d: Kopf unvollständig: %+v", i, r)
		}
		if i > 0 && r.Seq != datensaetze[i-1].Seq+1 {
			t.Errorf("Sequenzlücke: %d nach %d", r.Seq, datensaetze[i-1].Seq)
		}
		if len(r.Channels) != 1 {
			t.Fatalf("Datensatz %d hat %d Kanäle", i, len(r.Channels))
		}
		ch := r.Channels[0]
		asaRecords += len(ch.Asa.Records)
		for j := range ch.Asa.Alerts {
			al := ch.Asa.Alerts[j]
			alerts[al.AlertUID] = &al
			if al.Audio != nil && al.Audio.Bytes > 0 {
				sahAudio = true
			}
		}
		// raw ist der Beleg und nie optional.
		for _, rec := range ch.Asa.Records {
			if rec.Raw == "" {
				t.Errorf("Datensatz %d: ein asa-Record ohne raw", i)
				break
			}
		}
	}
	if datensaetze[0].Trigger != report.TriggerStartup {
		t.Errorf("der erste Datensatz hat trigger %q", datensaetze[0].Trigger)
	}
	if letzter := datensaetze[len(datensaetze)-1]; letzter.Trigger != report.TriggerShutdown {
		t.Errorf("der letzte Datensatz hat trigger %q", letzter.Trigger)
	}
	if asaRecords < 50 {
		t.Errorf("nur %d asa-Records über alle Datensätze", asaRecords)
	}
	if len(alerts) != 1 {
		t.Errorf("%d verschiedene Alerts, erwartet genau einen: %v", len(alerts), keys(alerts))
	}
	if !sahAudio {
		t.Error("kein Mitschnitt im Datensatz")
	}
	for uid, al := range alerts {
		if !al.Closed {
			t.Errorf("Alert %s wurde nie abgeschlossen", uid[:8])
		}
		if al.Stage != "level1_start" || al.EnteredAtPhase != "pre_trigger" {
			t.Errorf("Alert %s: stage=%q entered=%q", uid[:8], al.Stage, al.EnteredAtPhase)
		}
		if len(al.Area.Codes) == 0 {
			t.Errorf("Alert %s hat kein Warngebiet", uid[:8])
		}
		if al.Area.Raw == "" {
			t.Errorf("Alert %s: area.raw fehlt — es bleibt immer dabei", uid[:8])
		}
	}
}

func keys(m map[string]*report.Alert) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k[:8])
	}
	return out
}
