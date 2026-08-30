// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/record"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// asaFelder ist der asa-Record, wie ihn der Automat sieht.
type asaFelder = record.Asa

// Schnappschuss baut den Kanalabschnitt des Datensatzes und setzt die
// Fensterwerte zurück.
//
// bis ist das Ende des Fensters in der Zeitbasis des Record-Stroms; der Anfang
// ist das Ende des vorigen Fensters. Der Kanal führt diese Grenze selbst —
// niemand sonst kennt seine Zeitbasis, und im Replay weicht sie um Monate von
// der Rechneruhr ab.
//
// Der Aufruf ist der einzige Zugriff von außen auf den Zustand, und er
// geschieht in derselben Goroutine, die auch die Records verarbeitet.
func (c *Kanal) Schnappschuss(bis time.Time) report.Channel {
	von := c.letzteGrenze
	c.ruecke(bis)
	c.pruefeFristen()
	if von.IsZero() {
		von = c.letzteGrenze
	}

	ch := report.Channel{
		Channel:    c.k.Channel,
		RxState:    c.zustand.RxZustand,
		RxRestarts: c.zustand.Neustarts,
		LastError:  c.zustand.LetzterFehler,
	}
	if c.init != nil {
		ch.FreqHz = c.init.FreqHz
		ch.Device = c.init.Device
		ch.DeviceSerial = c.init.DeviceSerial
		ch.RxVersion = c.init.RxVersion
		ch.RxCommit = c.init.RxCommit
		ch.WelleCommit = c.init.WelleCommit
	}
	if c.ens != nil {
		ch.Ensemble = c.ensembleBericht()
	}
	ch.Reception = c.empfangsBericht()
	ch.Asa = c.asaBericht(von, bis)

	c.fenster = neueFensterwerte()
	c.letzteGrenze = c.jetzt
	if bis.After(c.letzteGrenze) {
		c.letzteGrenze = bis.UTC()
	}
	c.raeumeAlertsAuf()
	return ch
}

func (c *Kanal) ensembleBericht() *report.Ensemble {
	e := &report.Ensemble{
		EnsHash:        c.ensHash,
		EnsContentHash: c.ensContentHash,
		Eid:            c.ens.Eid,
		Ecc:            c.ens.Ecc,
		Label:          c.ens.Label,
		FirstSeen:      report.Sekundenzeit(c.ensErst),
		LastSeen:       report.Sekundenzeit(c.ensZuletzt),
		Services:       make([]report.Service, 0, len(c.ens.Services)),
	}
	for _, s := range c.ens.Services {
		rs := report.Service{Sid: s.Sid, Label: s.Label, Components: make([]report.Component, 0, len(s.Komponenten))}
		for _, k := range s.Komponenten {
			rs.Components = append(rs.Components, report.Component{
				SubChID: k.SubChID, StartAddr: k.StartAddr, Size: k.Size,
				Protection: k.Protection, Bitrate: k.Bitrate,
			})
		}
		e.Services = append(e.Services, rs)
	}
	return e
}

func (c *Kanal) empfangsBericht() report.Reception {
	f := c.fenster
	r := report.Reception{
		Samples:     f.tlmAnzahl,
		FibTotal:    f.fibTotal,
		FibCrcErr:   f.fibCrcErr,
		Dropped:     f.dropped,
		NodeDropped: f.nodeDropped,
		ParseErrors: f.parseErrors,
		SeqGaps:     f.seqLuecken,
		BrokenLines: f.kaputt,
	}
	if f.tlmAnzahl > 0 {
		r.SyncRatio = float64(f.syncAnzahl) / float64(f.tlmAnzahl)
	}
	if f.snrAnzahl > 0 {
		avg := f.snrSumme / float64(f.snrAnzahl)
		min, max := f.snrMin, f.snrMax
		r.SnrAvg, r.SnrMin, r.SnrMax = &avg, &min, &max
	}
	if f.fibTotal > 0 {
		r.CrcErrRate = float64(f.fibCrcErr) / float64(f.fibTotal)
	}
	if versatz, ok := c.uhr.VersatzMs(); ok {
		r.EnsTimeOffsetM = &versatz
	}
	return r
}

func (c *Kanal) asaBericht(von, bis time.Time) report.Asa {
	f := c.fenster
	a := report.Asa{
		EverSeen:  c.everSeen,
		Observed:  len(f.asaRecords) > 0,
		Records:   f.asaRecords,
		Alerts:    make([]report.Alert, 0, len(c.alerts)),
		Anomalies: f.auffaelligkeiten,
	}
	if a.Records == nil {
		a.Records = []report.AsaRecord{}
	}
	if f.auffaelligGesamt > len(f.auffaelligkeiten) {
		a.Anomalies = append(a.Anomalies,
			itoa(f.auffaelligGesamt-len(f.auffaelligkeiten))+" weitere Auffälligkeiten nicht aufgeführt")
	}
	a.Heartbeat = c.heartbeatAggregat(von, bis)

	for _, al := range c.alerts {
		// Ein abgeschlossener Alert geht genau einmal mit closed: true raus.
		// Danach bleibt er noch die Nachklangfrist im Zustand, damit die
		// zweite End-Instanz ihn wiederfindet — aber er wiederholt sich nicht
		// im Datensatz.
		if al.geschlossen && al.gemeldet {
			continue
		}
		var audio *report.Audio
		if c.s.Audio != nil {
			audio = c.s.Audio.Stand(al.uid)
		}
		a.Alerts = append(a.Alerts, al.bericht(audio))
		if al.geschlossen {
			al.gemeldet = true
		}
	}
	return a
}

// heartbeatAggregat rechnet die Heartbeat-Bilanz des Fensters aus.
//
// Der Heartbeat ist das, was die Abdeckungskarte trägt — das Kernergebnis des
// Projekts. Deshalb wird je Sekunde des Fensters unterschieden:
//
//   - empfangen,
//   - verdrängt: Solange Alerts signalisiert werden, wird laut Norm kein
//     Heartbeat gesendet. Eine fehlende Sekunde ist dann normal,
//   - fehlend: alles Übrige.
func (c *Kanal) heartbeatAggregat(von, bis time.Time) report.Heartbeat {
	hb := report.Heartbeat{PdMismatch: c.fenster.pdMismatch, MissingSeconds: []string{}}
	if von.IsZero() || !bis.After(von) {
		return hb
	}

	// Das Fenster wird in Ensemble-Zeit umgerechnet: Nur so passen die Grenzen
	// zu den Sekunden, unter denen die Heartbeats vermerkt wurden.
	//
	// Gezählt werden die Sekunden, die **ganz** im Fenster liegen: von der
	// ersten vollen Sekunde ab der Untergrenze bis zur letzten, die vor der
	// Obergrenze beginnt. Beim Vorgabetakt sind das genau zehn.
	ensVon, _ := c.uhr.Roh(von)
	ensBis, _ := c.uhr.Roh(bis)
	erste := ensVon.Truncate(time.Second)
	if erste.Before(ensVon) {
		erste = erste.Add(time.Second)
	}
	if !ensBis.After(erste) {
		return hb
	}

	for s := erste; s.Before(ensBis); s = s.Add(time.Second) {
		hb.Expected++
		u := s.Unix()
		switch {
		case c.fenster.hbSekunden[u]:
			hb.Received++
		case c.fenster.alertSekunden[u]:
			hb.Suppressed++
		default:
			if len(hb.MissingSeconds) < MaxFehlendeSekunden {
				hb.MissingSeconds = append(hb.MissingSeconds, report.Sekundenzeit(s))
			}
		}
	}
	return hb
}

// raeumeAlertsAuf entfernt Alerts, die ein letztes Mal mit closed:true gemeldet
// wurden. Bis dahin bleiben sie stehen — der Server soll den Abschluss sehen.
//
// Zusätzlich bleibt jeder Alert noch die Nachklangfrist liegen: Die End-Phase
// läuft über zwei Sekunden, und weil ein Phasenwechsel den Datensatz sofort
// schließt, fällt die Aufräumgrenze mitten hinein. Ohne diese Frist legte die
// zweite End-Instanz einen zweiten, geisterhaften Alert an, der nur seine
// eigene End-Phase kennt.
func (c *Kanal) raeumeAlertsAuf() {
	behalten := c.alerts[:0]
	for _, al := range c.alerts {
		// Ein laufender Mitschnitt hält den Alert am Leben, auch wenn er längst
		// abgeschlossen ist: Der Nachlauf nach der End-Phase dauert länger als
		// die Nachklangfrist, und ohne den Alert gäbe es niemanden mehr, der
		// das STOP auslöst. Die Aufnahme liefe dann bis zur harten Obergrenze
		// weiter — und schnitte fremdes Programm mit.
		// Und eine angeforderte, aber noch nicht gemeldete Aufnahme hält ihn
		// ebenso: Der aud-Record kommt **nach** dem STOP, also nach der
		// letzten Meldung des Alerts. Ohne diese Ausnahme wäre der Alert weg,
		// bevor seine Dateien bekannt sind — und der Server erführe nie, dass
		// es sie gibt. Die Frist begrenzt den Fall, dass asamon-rx stirbt,
		// bevor es meldet.
		wartetAufAufnahme := !al.audioBegonnen.IsZero() && !al.audioGemeldet &&
			c.jetzt.Sub(al.zuletztStrom) <= AudioMeldefrist
		if al.geschlossen && al.gemeldet && al.audioLaeuft.IsZero() &&
			!wartetAufAufnahme && c.jetzt.Sub(al.zuletztStrom) > Nachklangfrist {
			continue
		}
		behalten = append(behalten, al)
	}
	c.alerts = behalten
}

// OffeneAlerts gibt die Zahl der laufenden Alerts — für Log und Prüfungen.
func (c *Kanal) OffeneAlerts() int {
	n := 0
	for _, al := range c.alerts {
		if !al.geschlossen {
			n++
		}
	}
	return n
}
