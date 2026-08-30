// SPDX-License-Identifier: GPL-3.0-or-later

package supervisor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/buildinfo"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// reporterSchleife baut die Datensätze — im Takt und sofort bei Alerts.
//
// Ein Datensatz je report_interval, **immer**, auch wenn nichts empfangen
// wurde. Sonst kann der Server "Ensemble sendet keinen Heartbeat" nicht von
// "Knoten ist tot" unterscheiden.
func (s *Supervisor) reporterSchleife(ctx context.Context) error {
	takt := s.cfg.Server.ReportInterval.D()

	// Der erste Datensatz geht sofort beim Start raus. Er ist die Anmeldung
	// des Knotens.
	von := s.gestartet
	jetzt := time.Now().UTC()
	s.reiche(s.baueDatensatz(report.TriggerStartup, von, jetzt))
	von = jetzt
	if s.FertigMelder != nil {
		s.FertigMelder()
	}
	if s.opt.Einmal {
		return nil
	}

	t := time.NewTicker(takt)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-t.C:
			jetzt = time.Now().UTC()
			s.reiche(s.baueDatensatz(report.TriggerInterval, von, jetzt))
			von = jetzt
			if s.Lebenszeichen != nil {
				s.Lebenszeichen()
			}

		case grund := <-s.wecken:
			// Der 10-Sekunden-Takt ist für Heartbeat und Telemetrie richtig
			// und für einen Alert falsch: Er addiert bis zu zehn Sekunden auf
			// ein Ereignis, dessen einziger Wert Aktualität ist.
			jetzt = time.Now().UTC()
			if jetzt.Before(s.sofortZul) {
				continue
			}
			s.sofortZul = jetzt.Add(SofortMindestabstand)
			s.log.Info("Datensatz wird sofort geschlossen", "grund", grund)
			s.reiche(s.baueDatensatz(report.TriggerAlert, von, jetzt))
			von = jetzt
			t.Reset(takt)
		}
	}
}

// baueAbschlussDatensatz holt von jedem Kanal seinen letzten Stand — samt
// geschlossenen Alerts und beendeten Mitschnitten — und baut daraus den
// Abschluss-Datensatz.
func (s *Supervisor) baueAbschlussDatensatz() *report.Report {
	frist := AbschaltFrist / 4
	kanaele := make([]report.Channel, 0, len(s.kanaele))
	var panics uint64
	for _, k := range s.kanaele {
		kanaele = append(kanaele, k.abschlussSchnappschuss(frist))
		panics += k.panics.Load()
	}
	jetzt := time.Now().UTC()
	geschrieben, geloescht := s.spl.Zaehler()
	stand := s.spl.Stand()
	stand.AudioFiles = s.audio.Dateien()

	return &report.Report{
		ReportVersion: report.Version,
		Seq:           s.seq.Add(1),
		GeneratedAt:   report.Zeitpunkt(jetzt),
		Window: report.Fenster{
			From: report.Zeitpunkt(jetzt.Add(-s.cfg.Server.ReportInterval.D())),
			To:   report.Zeitpunkt(jetzt),
		},
		Trigger:  report.TriggerShutdown,
		Node:     s.knoten(jetzt),
		Channels: kanaele,
		Counters: report.Counters{
			Panics:          panics,
			UnknownRecords:  s.unbekannteRecords.Load(),
			ReportsSpooled:  geschrieben,
			ReportsDropped:  geloescht,
			ReportsRejected: s.abgelehnteReports.Load(),
		},
	}
}

// reiche gibt den Datensatz an den Versand — ohne je zu blockieren.
func (s *Supervisor) reiche(r *report.Report) {
	if s.opt.DryRun {
		if s.opt.Ausgabe != nil {
			s.opt.Ausgabe(r)
		}
		return
	}
	select {
	case s.berichte <- r:
	default:
		// Der Versand kommt nicht nach. Statt den Reporter aufzuhalten, geht
		// der Datensatz sofort in den Spool.
		if err := s.spl.Lege(r); err != nil {
			s.log.Error("Datensatz ließ sich nicht ablegen", "seq", r.Seq, "fehler", err)
		}
	}
}

// baueDatensatz zieht von jedem Kanal einen Schnappschuss und setzt den
// Datensatz zusammen.
func (s *Supervisor) baueDatensatz(trigger string, von, bis time.Time) *report.Report {
	seq := s.seq.Add(1)

	kanaele := make([]report.Channel, 0, len(s.kanaele))
	var panics uint64
	var unbekannt uint64
	for _, k := range s.kanaele {
		ch := k.schnappschuss()
		kanaele = append(kanaele, ch)
		panics += k.panics.Load()
	}
	unbekannt = s.unbekannteRecords.Load()

	geschrieben, geloescht := s.spl.Zaehler()
	stand := s.spl.Stand()
	stand.AudioFiles = s.audio.Dateien()

	return &report.Report{
		ReportVersion: report.Version,
		Seq:           seq,
		GeneratedAt:   report.Zeitpunkt(time.Now()),
		Window:        report.Fenster{From: report.Zeitpunkt(von), To: report.Zeitpunkt(bis)},
		Trigger:       trigger,
		Node:          s.knoten(bis),
		Channels:      kanaele,
		Counters: report.Counters{
			Panics:          panics,
			UnknownRecords:  unbekannt,
			ReportsSpooled:  geschrieben,
			ReportsDropped:  geloescht,
			ReportsRejected: s.abgelehnteReports.Load(),
		},
	}
}

func (s *Supervisor) knoten(jetzt time.Time) report.Node {
	code := s.cfg.Location
	n := report.Node{
		NodeID:       s.id.NodeID,
		Name:         s.cfg.Node.Name,
		PubKey:       s.id.PubKeyBase64(),
		LocationCode: code.Presentation(),
		Location: report.Location{
			Zone:   int(code.Zone),
			Digits: code.DigitsHex(),
		},
		Antenna:     s.cfg.Node.Antenna,
		Contact:     s.cfg.Node.Contact,
		NodeVersion: buildinfo.Version,
		NodeCommit:  buildinfo.Commit,
		Platform:    buildinfo.Platform(),
		StartedAt:   report.Sekundenzeit(s.gestartet),
		UptimeS:     int64(jetzt.Sub(s.gestartet).Seconds()),
		Clock:       report.Clock{NtpSynchronized: uhrSynchronisiert()},
		Spool:       s.spl.Stand(),
	}
	n.Spool.AudioFiles = s.audio.Dateien()
	if r, err := code.Rect(); err == nil {
		n.Location.LatMin, n.Location.LatMax = r.LatMin, r.LatMax
		n.Location.LonMin, n.Location.LonMax = r.LonMin, r.LonMax
	}
	if lat, lon, err := code.Center(); err == nil {
		n.Location.Lat, n.Location.Lon = lat, lon
	}
	return n
}

func statPfad(pfad string) (fs.FileInfo, error) { return os.Stat(pfad) }

func joinPfad(teile ...string) string { return filepath.Join(teile...) }
