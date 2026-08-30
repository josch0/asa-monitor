// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

func stillesLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func neu(t *testing.T, maxMB int) (*Spool, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Neu(dir, maxMB, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

// datensatz baut einen Datensatz. fuellung bläht ihn auf, damit sich die
// Obergrenze prüfen lässt, ohne tausende Dateien zu schreiben.
func datensatz(seq uint64, mitAlert bool, fuellung int) *report.Report {
	ch := report.Channel{Channel: "5C", RxState: report.RxRunning}
	if mitAlert {
		ch.Asa.Alerts = []report.Alert{{AlertUID: "abc", Phase: "trigger"}}
	}
	if fuellung > 0 {
		raw := make([]byte, fuellung)
		for i := range raw {
			raw[i] = 'a'
		}
		ch.Asa.Records = []report.AsaRecord{{Raw: string(raw)}}
	}
	return &report.Report{ReportVersion: 1, Seq: seq, Channels: []report.Channel{ch}}
}

func TestReihenfolgeBleibtErhalten(t *testing.T) {
	s, _ := neu(t, 512)
	// Absichtlich durcheinander abgelegt.
	for _, seq := range []uint64{5, 1, 3, 2, 4} {
		if err := s.Lege(datensatz(seq, false, 0)); err != nil {
			t.Fatal(err)
		}
	}
	berichte, seqs, err := s.Naechste(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(berichte) != 5 {
		t.Fatalf("%d Datensätze", len(berichte))
	}
	for i, want := range []uint64{1, 2, 3, 4, 5} {
		if berichte[i].Seq != want || seqs[i] != want {
			t.Errorf("Position %d: seq %d, erwartet %d", i, berichte[i].Seq, want)
		}
	}

	// Erst Erledige gibt sie frei: Ein Datensatz, der beim Senden verlorenginge,
	// wäre nicht wiederzubeschaffen.
	if s.Leer() {
		t.Error("der Spool gilt nach Naechste bereits als leer")
	}
	s.Erledige([]uint64{1, 2, 3})
	rest, _, _ := s.Naechste(10)
	if len(rest) != 2 || rest[0].Seq != 4 {
		t.Errorf("nach Erledige: %d Datensätze, erster %d", len(rest), rest[0].Seq)
	}
	s.Erledige([]uint64{4, 5})
	if !s.Leer() {
		t.Error("der Spool ist nach dem Erledigen aller Datensätze nicht leer")
	}
}

func TestBegrenzungAufNStueck(t *testing.T) {
	s, _ := neu(t, 512)
	for seq := uint64(1); seq <= 10; seq++ {
		if err := s.Lege(datensatz(seq, false, 0)); err != nil {
			t.Fatal(err)
		}
	}
	berichte, _, err := s.Naechste(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(berichte) != 3 || berichte[0].Seq != 1 || berichte[2].Seq != 3 {
		t.Errorf("Naechste(3) gab %d Datensätze", len(berichte))
	}
}

// Beim Überlauf wird der älteste Datensatz **ohne Alerts** zuerst gelöscht.
// Eine verlorene Warnmeldung wiegt schwerer als eine verlorene Telemetrieminute.
func TestUeberlaufLoeschtAlertloseZuerst(t *testing.T) {
	s, _ := neu(t, 1) // 1 MB

	// Abwechselnd mit und ohne Alert, je 100 kB.
	for seq := uint64(1); seq <= 20; seq++ {
		if err := s.Lege(datensatz(seq, seq%2 == 0, 100*1024)); err != nil {
			t.Fatal(err)
		}
	}

	berichte, _, err := s.Naechste(100)
	if err != nil {
		t.Fatal(err)
	}
	_, geloescht := s.Zaehler()
	if geloescht == 0 {
		t.Fatal("nichts wurde gelöscht, obwohl die Obergrenze überschritten war")
	}
	if s.Stand().Bytes > 1024*1024 {
		t.Errorf("der Spool ist mit %d Byte über der Grenze", s.Stand().Bytes)
	}

	mitAlert, ohneAlert := 0, 0
	for _, r := range berichte {
		if len(r.Channels[0].Asa.Alerts) > 0 {
			mitAlert++
		} else {
			ohneAlert++
		}
	}
	if mitAlert != 10 {
		t.Errorf("%d von 10 Datensätzen mit Alert überlebten", mitAlert)
	}
	if ohneAlert >= 10 {
		t.Errorf("%d alertlose Datensätze überlebten — sie hätten zuerst weichen müssen", ohneAlert)
	}
	t.Logf("überlebt: %d mit Alert, %d ohne; gelöscht: %d", mitAlert, ohneAlert, geloescht)
}

// Nur wenn ausschließlich Datensätze mit Alerts da sind, weicht auch der
// älteste davon.
func TestUeberlaufLoeschtNotfallsAuchAlerts(t *testing.T) {
	s, _ := neu(t, 1)
	for seq := uint64(1); seq <= 20; seq++ {
		if err := s.Lege(datensatz(seq, true, 100*1024)); err != nil {
			t.Fatal(err)
		}
	}
	berichte, _, err := s.Naechste(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(berichte) == 20 {
		t.Error("nichts wurde gelöscht, obwohl nur Alert-Datensätze da waren")
	}
	if len(berichte) == 0 {
		t.Fatal("alles wurde gelöscht")
	}
	// Der älteste muss gegangen sein, nicht der neueste.
	if berichte[len(berichte)-1].Seq != 20 {
		t.Errorf("der neueste Datensatz fehlt; letzter ist %d", berichte[len(berichte)-1].Seq)
	}
}

// Ein Absturz mitten im Schreiben darf keine halbe Datei hinterlassen, die
// beim nächsten Start als gültiger Datensatz gelesen wird.
func TestHalbeDateienWerdenAufgeraeumt(t *testing.T) {
	s, dir := neu(t, 512)
	if err := s.Lege(datensatz(7, false, 0)); err != nil {
		t.Fatal(err)
	}
	// So sieht ein Abbruch mitten im Schreiben aus.
	halb := filepath.Join(dir, "spool", "reports", "0000000008.json.tmp")
	if err := os.WriteFile(halb, []byte(`{"report_version":1,"seq":8,"chan`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Und so eine Datei, die es zwar bis zum Umbenennen schaffte, aber Unsinn
	// enthält.
	kaputt := filepath.Join(dir, "spool", "reports", "0000000009.json")
	if err := os.WriteFile(kaputt, []byte(`kein JSON`), 0o600); err != nil {
		t.Fatal(err)
	}

	zweiter, err := Neu(dir, 512, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(halb); err == nil {
		t.Error("die angefangene Datei blieb liegen")
	}
	berichte, _, err := zweiter.Naechste(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(berichte) != 1 || berichte[0].Seq != 7 {
		t.Errorf("%d Datensätze überlebten den Neustart: %v", len(berichte), berichte)
	}
	// Der unlesbare Datensatz ist beim Lesen aussortiert worden; übrig bleibt
	// allein der gültige.
	if n := zweiter.Stand().Reports; n != 1 {
		t.Errorf("%d Datensätze im Spool, erwartet 1 (der unlesbare hätte weichen müssen)", n)
	}
	if _, err := os.Stat(kaputt); err == nil {
		t.Error("die unlesbare Datei blieb liegen")
	}
}

func TestSpoolUeberlebtNeustart(t *testing.T) {
	s, dir := neu(t, 512)
	for seq := uint64(100); seq < 105; seq++ {
		if err := s.Lege(datensatz(seq, seq == 102, 0)); err != nil {
			t.Fatal(err)
		}
	}
	zweiter, err := Neu(dir, 512, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	berichte, _, err := zweiter.Naechste(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(berichte) != 5 || berichte[0].Seq != 100 {
		t.Fatalf("%d Datensätze nach dem Neustart", len(berichte))
	}
	if zweiter.Stand().Reports != 5 {
		t.Errorf("Stand: %+v", zweiter.Stand())
	}
}

// Im Normalbetrieb darf **nichts** auf die Platte gehen. Der Test hält fest,
// dass der Spool ohne Lege() keine Datei anlegt — SD-Karten in fremden Pis.
func TestOhneAblageKeineDatei(t *testing.T) {
	s, dir := neu(t, 512)
	if !s.Leer() {
		t.Error("ein frischer Spool ist nicht leer")
	}
	if geschrieben, _ := s.Zaehler(); geschrieben != 0 {
		t.Errorf("%d Schreibvorgänge ohne Lege()", geschrieben)
	}
	eintraege, err := os.ReadDir(filepath.Join(dir, "spool", "reports"))
	if err != nil {
		t.Fatal(err)
	}
	if len(eintraege) != 0 {
		t.Errorf("%d Dateien im Spool, obwohl nichts abgelegt wurde", len(eintraege))
	}
}
