// SPDX-License-Identifier: GPL-3.0-or-later

// Paket spool ist der Store-and-Forward-Speicher: Fällt die Verbindung aus,
// sammelt der Knoten weiter und liefert bei nächster Gelegenheit alles nach,
// in Reihenfolge.
//
// **Die wichtigste Regel: Im Normalbetrieb wird nichts auf die Platte
// geschrieben.** Ein Datensatz geht direkt zum Uplink; erst wenn der Versand
// scheitert, landet er hier. Diese Knoten laufen auf Raspberry Pis mit
// SD-Karten — sechs Schreibvorgänge pro Minute, dauerhaft, sind ein reales
// Verschleißproblem und kein theoretisches.
package spool

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/josch0/asa-monitor/asamon-node/internal/identity"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// Endung der abgelegten Datensätze.
const (
	endung     = ".json"
	tempEndung = ".tmp"
)

// Spool verwaltet die abgelegten Datensätze.
type Spool struct {
	dir     string
	maxByte int64
	log     *slog.Logger

	mu        sync.Mutex
	eintraege []eintrag
	bytes     int64
	// Zähler, die in den nächsten Datensatz gehen.
	geschrieben uint64
	geloescht   uint64
}

type eintrag struct {
	seq       uint64
	pfad      string
	bytes     int64
	mitAlerts bool
}

// Neu öffnet den Spool und liest vor, was noch daliegt.
//
// Beim Start wird der Spool zuerst geleert — was von einem früheren Lauf
// übrig ist, ist älter als alles Neue und gehört zuerst zum Server.
func Neu(stateDir string, maxMB int, log *slog.Logger) (*Spool, error) {
	dir := filepath.Join(stateDir, "spool", "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("Spool-Verzeichnis %s: %w", dir, err)
	}
	s := &Spool{dir: dir, maxByte: int64(maxMB) * 1024 * 1024, log: log}
	if err := s.leseEin(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Spool) leseEin() error {
	eintraegeAufPlatte, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("Spool lesen: %w", err)
	}
	for _, e := range eintraegeAufPlatte {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, tempEndung) {
			// Eine halbe Datei von einem Absturz mitten im Schreiben. Sie
			// wurde nie umbenannt und ist damit nie gültig geworden.
			pfad := filepath.Join(s.dir, name)
			if err := os.Remove(pfad); err != nil {
				s.log.Warn("angefangene Spool-Datei ließ sich nicht aufräumen", "datei", pfad, "fehler", err)
			}
			continue
		}
		if !strings.HasSuffix(name, endung) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		seq, err := strconv.ParseUint(strings.TrimSuffix(name, endung), 10, 64)
		if err != nil {
			s.log.Warn("Spool-Datei mit unerwartetem Namen wird übergangen", "datei", name)
			continue
		}
		pfad := filepath.Join(s.dir, name)
		s.eintraege = append(s.eintraege, eintrag{
			seq: seq, pfad: pfad, bytes: info.Size(), mitAlerts: hatAlerts(pfad),
		})
		s.bytes += info.Size()
	}
	slices.SortFunc(s.eintraege, func(a, b eintrag) int { return cmp.Compare(a.seq, b.seq) })
	if len(s.eintraege) > 0 {
		s.log.Info("Spool aus früherem Lauf gefunden", "datensaetze", len(s.eintraege), "bytes", s.bytes)
	}
	return nil
}

// hatAlerts sagt, ob ein abgelegter Datensatz Alerts enthält. Beim Überlauf
// entscheidet das darüber, was zuerst gelöscht wird.
func hatAlerts(pfad string) bool {
	raw, err := os.ReadFile(pfad)
	if err != nil {
		return false
	}
	var r report.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		// Unlesbar heißt: kein belegbarer Alert darin. Wegwerfbar.
		return false
	}
	for _, ch := range r.Channels {
		if len(ch.Asa.Alerts) > 0 {
			return true
		}
	}
	return false
}

// Lege schreibt einen Datensatz in den Spool.
//
// Geschrieben wird in eine Nebendatei, dann fsync, dann rename — halbe Dateien
// darf es nicht geben.
func (s *Spool) Lege(r *report.Report) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("Datensatz %d serialisieren: %w", r.Seq, err)
	}
	pfad := filepath.Join(s.dir, fmt.Sprintf("%010d%s", r.Seq, endung))
	if err := identity.SchreibeAtomar(pfad, raw, 0o600); err != nil {
		return fmt.Errorf("Datensatz %d ablegen: %w", r.Seq, err)
	}

	mitAlerts := false
	for _, ch := range r.Channels {
		if len(ch.Asa.Alerts) > 0 {
			mitAlerts = true
			break
		}
	}

	s.mu.Lock()
	s.eintraege = append(s.eintraege, eintrag{seq: r.Seq, pfad: pfad, bytes: int64(len(raw)), mitAlerts: mitAlerts})
	slices.SortFunc(s.eintraege, func(a, b eintrag) int { return cmp.Compare(a.seq, b.seq) })
	s.bytes += int64(len(raw))
	s.geschrieben++
	s.mu.Unlock()

	s.raeumeAuf()
	return nil
}

// Naechste gibt bis zu n Datensätze in aufsteigender Reihenfolge.
//
// Sie bleiben im Spool, bis Erledige sie freigibt: Ein Datensatz, der beim
// Senden verlorenginge, wäre nicht wiederzubeschaffen.
func (s *Spool) Naechste(n int) ([]*report.Report, []uint64, error) {
	s.mu.Lock()
	auswahl := make([]eintrag, 0, n)
	for _, e := range s.eintraege {
		if len(auswahl) >= n {
			break
		}
		auswahl = append(auswahl, e)
	}
	s.mu.Unlock()

	var (
		berichte []*report.Report
		seqs     []uint64
		kaputt   []uint64
	)
	for _, e := range auswahl {
		raw, err := os.ReadFile(e.pfad)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				kaputt = append(kaputt, e.seq)
				continue
			}
			return nil, nil, fmt.Errorf("Spool-Datei %s: %w", e.pfad, err)
		}
		var r report.Report
		if err := json.Unmarshal(raw, &r); err != nil {
			s.log.Warn("unlesbarer Datensatz im Spool wird verworfen", "seq", e.seq, "fehler", err)
			kaputt = append(kaputt, e.seq)
			continue
		}
		berichte = append(berichte, &r)
		seqs = append(seqs, e.seq)
	}
	if len(kaputt) > 0 {
		s.Erledige(kaputt)
	}
	return berichte, seqs, nil
}

// Erledige entfernt Datensätze, die der Server angenommen hat — oder als
// Duplikat erkannt hat, was dasselbe bedeutet: Er hat sie.
func (s *Spool) Erledige(seqs []uint64) {
	if len(seqs) == 0 {
		return
	}
	weg := make(map[uint64]bool, len(seqs))
	for _, q := range seqs {
		weg[q] = true
	}

	s.mu.Lock()
	behalten := s.eintraege[:0]
	var entfernt []string
	for _, e := range s.eintraege {
		if weg[e.seq] {
			s.bytes -= e.bytes
			entfernt = append(entfernt, e.pfad)
			continue
		}
		behalten = append(behalten, e)
	}
	s.eintraege = behalten
	s.mu.Unlock()

	for _, pfad := range entfernt {
		if err := os.Remove(pfad); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("Spool-Datei ließ sich nicht löschen", "datei", pfad, "fehler", err)
		}
	}
}

// raeumeAuf hält die Obergrenze ein.
//
// Gelöscht wird der älteste Datensatz **ohne Alerts** zuerst. Erst wenn nur
// noch Datensätze mit Alerts da sind, wird auch der älteste davon gelöscht —
// eine verlorene Warnmeldung wiegt schwerer als eine verlorene Telemetrieminute.
func (s *Spool) raeumeAuf() {
	for {
		s.mu.Lock()
		if s.maxByte <= 0 || s.bytes <= s.maxByte || len(s.eintraege) == 0 {
			s.mu.Unlock()
			return
		}
		opfer := -1
		for i, e := range s.eintraege {
			if !e.mitAlerts {
				opfer = i
				break
			}
		}
		if opfer < 0 {
			opfer = 0 // nur noch Datensätze mit Alerts: der älteste muss weichen
		}
		e := s.eintraege[opfer]
		s.eintraege = append(s.eintraege[:opfer], s.eintraege[opfer+1:]...)
		s.bytes -= e.bytes
		s.geloescht++
		geloescht := s.geloescht
		s.mu.Unlock()

		if err := os.Remove(e.pfad); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("Spool-Datei ließ sich nicht löschen", "datei", e.pfad, "fehler", err)
		}
		s.log.Warn("Spool ist voll, ältester Datensatz gelöscht",
			"seq", e.seq, "mit_alerts", e.mitAlerts, "geloescht_gesamt", geloescht)
	}
}

// Stand gibt den Füllstand für den Datensatz.
func (s *Spool) Stand() report.Spool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return report.Spool{Reports: len(s.eintraege), Bytes: s.bytes}
}

// Zaehler gibt die Zähler seit dem Start: abgelegte und wegen Überlauf
// gelöschte Datensätze.
func (s *Spool) Zaehler() (geschrieben, geloescht uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.geschrieben, s.geloescht
}

// Leer sagt, ob nichts mehr nachzuliefern ist.
func (s *Spool) Leer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.eintraege) == 0
}
