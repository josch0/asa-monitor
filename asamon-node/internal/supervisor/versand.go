// SPDX-License-Identifier: GPL-3.0-or-later

package supervisor

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
	"github.com/josch0/asa-monitor/asamon-node/internal/uplink"
)

// senderSchleife nimmt die Datensätze entgegen und bringt sie zum Server.
//
// Im Normalbetrieb wird dabei **nichts** auf die Platte geschrieben: Der
// Datensatz geht direkt zum Uplink. Erst wenn der Versand scheitert, landet er
// im Spool.
func (s *Supervisor) senderSchleife(ctx context.Context) {
	// Was von einem früheren Lauf übrig ist, geht zuerst raus.
	if !s.spl.Leer() {
		s.liefereNach(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-s.berichte:
			s.versende(ctx, r)
		}
	}
}

func (s *Supervisor) versende(ctx context.Context, r *report.Report) {
	// Solange das Backoff läuft, wird gar nicht erst versucht: Ein Server, der
	// gerade nicht antwortet, soll nicht alle zehn Sekunden ein Timeout kosten.
	if time.Now().Before(s.sperreBis) {
		s.lege(r)
		return
	}
	// Reihenfolge zählt: Liegt noch etwas im Spool, gehört der neue Datensatz
	// hinten dran, nicht davor.
	if !s.spl.Leer() {
		s.lege(r)
		s.liefereNach(ctx)
		return
	}

	antwort, err := s.up.Sende(ctx, []*report.Report{r})
	if err != nil {
		s.behandleFehler(err, r)
		return
	}
	s.verarbeiteAntwort(ctx, antwort)
}

// liefereNach leert den Spool in Reihenfolge.
func (s *Supervisor) liefereNach(ctx context.Context) {
	for ctx.Err() == nil && !s.spl.Leer() {
		if time.Now().Before(s.sperreBis) {
			return
		}
		berichte, seqs, err := s.spl.Naechste(s.cfg.Limits.MaxReportsPerReques)
		if err != nil {
			s.log.Error("Spool lesen", "fehler", err)
			return
		}
		if len(berichte) == 0 {
			return
		}

		antwort, err := s.up.Sende(ctx, berichte)
		if err != nil {
			s.behandleFehler(err, nil)
			return
		}
		// Angenommen und Duplikat bedeuten dasselbe: Der Server hat sie.
		erledigt := antwort.Erledigt()
		// Ein dauerhaft abgelehnter Datensatz darf den Spool nicht füllen.
		for _, abgelehnt := range antwort.Rejected {
			erledigt = append(erledigt, abgelehnt.Seq)
			s.abgelehnteReports.Add(1)
		}
		if len(erledigt) == 0 {
			// Der Server hat geantwortet, aber nichts quittiert. Um eine
			// Endlosschleife zu vermeiden, wird der Stapel als erledigt
			// angesehen und der Fall gemeldet.
			s.log.Error("der Server hat den Stapel weder angenommen noch abgelehnt",
				"datensaetze", len(berichte))
			erledigt = seqs
		}
		s.spl.Erledige(erledigt)
		s.log.Info("Datensätze nachgeliefert",
			"gesendet", len(berichte), "erledigt", len(erledigt), "offen", s.spl.Stand().Reports)
		s.verarbeiteAudio(ctx, antwort)
	}
}

func (s *Supervisor) verarbeiteAntwort(ctx context.Context, a *uplink.Antwort) {
	for _, abgelehnt := range a.Rejected {
		s.abgelehnteReports.Add(1)
		_ = abgelehnt
	}
	s.verarbeiteAudio(ctx, a)
}

// verarbeiteAudio lädt die Mitschnitte hoch, die der Server angefordert hat.
//
// Ohne audio_wanted geht **nichts** raus. Das ist die Crowd-Ersparnis: Zehn
// Knoten, die dieselbe Meldung empfangen, laden sie einmal hoch.
func (s *Supervisor) verarbeiteAudio(ctx context.Context, a *uplink.Antwort) {
	if len(a.AudioWanted) == 0 {
		return
	}
	for _, auf := range s.audio.Angefordert(a.AudioWanted) {
		if ctx.Err() != nil {
			return
		}
		f, err := os.Open(auf.Pfad)
		if err != nil {
			s.log.Error("Mitschnitt lässt sich nicht öffnen", "datei", auf.Pfad, "fehler", err)
			continue
		}
		err = s.up.LadeAudio(ctx, auf.AlertUID, uplink.AudioKopf{
			Channel:   auf.Channel,
			SubChID:   auf.SubChID,
			Started:   report.Zeitpunkt(auf.Start),
			Sha256:    auf.Sha256,
			Truncated: auf.Truncated,
		}, f, auf.Bytes)
		f.Close()

		if err != nil {
			s.log.Warn("Mitschnitt ließ sich nicht hochladen",
				"alert_uid", auf.AlertUID, "bytes", auf.Bytes, "fehler", err)
			continue
		}
		s.audio.Hochgeladen(auf.AlertUID, time.Now())
		s.log.Info("Mitschnitt hochgeladen", "alert_uid", auf.AlertUID, "bytes", auf.Bytes)
	}
}

// behandleFehler entscheidet über Spool und Backoff.
func (s *Supervisor) behandleFehler(err error, r *report.Report) {
	wiederholen := true
	warte := time.Duration(0)
	if f, ok := errors.AsType[*uplink.Fehler](err); ok {
		wiederholen = f.Wiederholen
		warte = f.RetryAfter
	}

	if !wiederholen {
		// 4xx außer 408/429: Der Datensatz wird verworfen und der Fall
		// geloggt. Ein dauerhaft abgelehnter Datensatz darf den Spool nicht
		// füllen.
		s.verworfeneReports.Add(1)
		s.log.Error("Datensatz endgültig abgelehnt und verworfen", "fehler", err)
		return
	}

	if r != nil {
		s.lege(r)
	}
	if warte <= 0 {
		warte = s.up.Backoff()
	}
	s.sperreBis = time.Now().Add(warte)
	s.log.Warn("Uplink gescheitert, nächster Versuch später",
		"fehler", err, "wartezeit", warte.Round(time.Millisecond).String(), "spool", s.spl.Stand().Reports)
}

func (s *Supervisor) lege(r *report.Report) {
	if err := s.spl.Lege(r); err != nil {
		s.log.Error("Datensatz ließ sich nicht ablegen", "seq", r.Seq, "fehler", err)
	}
}

// versendeAbschluss versucht den letzten Datensatz genau einmal; scheitert er,
// geht er in den Spool. Ein Knoten, der herunterfährt, wartet nicht.
func (s *Supervisor) versendeAbschluss(ctx context.Context, r *report.Report) {
	if s.opt.DryRun {
		if s.opt.Ausgabe != nil {
			s.opt.Ausgabe(r)
		}
		return
	}
	if _, err := s.up.Sende(ctx, []*report.Report{r}); err != nil {
		s.log.Warn("der Abschluss-Datensatz ging in den Spool", "fehler", err)
		s.lege(r)
		return
	}
	s.log.Info("Abschluss-Datensatz gesendet", "seq", r.Seq)
}
