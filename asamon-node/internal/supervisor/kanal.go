// SPDX-License-Identifier: GPL-3.0-or-later

package supervisor

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/chanstate"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
	"github.com/josch0/asa-monitor/asamon-node/internal/rxproc"
)

// SchnappschussFrist ist die Zeit, die eine Kanal-Goroutine für ihre Antwort
// hat. Wer nicht antwortet, kommt als rx_state: "stalled" in den Datensatz.
//
// Der Reporter wartet nie auf den langsamsten Kanal: Ein hängender Kanal darf
// den Datensatz nicht aufhalten. Sein Hängen ist selbst die Meldung.
const SchnappschussFrist = 500 * time.Millisecond

// FristenTakt ist der Abstand, in dem zeitabhängige Übergänge geprüft werden,
// auch wenn kein Record kommt.
const FristenTakt = time.Second

// anfrage ist eine Schnappschuss-Anfrage an eine Kanal-Goroutine.
type anfrage struct {
	antwort chan report.Channel
}

// kanal ist ein überwachter DAB-Kanal: Subprozess und Zustandsmaschine,
// zusammengehalten von genau einer Goroutine.
type kanal struct {
	name         string
	rx           *rxproc.Prozess
	konfig       chanstate.Konfig
	log          *slog.Logger
	anfragen     chan anfrage
	abschluss    chan anfrage
	bereitschaft chan time.Time

	// panics wird von der Kanal-Goroutine geschrieben und vom Reporter
	// gelesen — deshalb atomar.
	panics atomic.Uint64
	// letzterAbschnitt hält den zuletzt gelieferten Stand für den Fall, dass
	// die Goroutine hängt.
	letzterZustand atomic.Value // string
}

func (k *kanal) run(ctx context.Context, s *Supervisor) {
	// Der Anfragekanal wird **nicht** geschlossen. Ein geschlossener Kanal
	// ließe jeden späteren Schnappschuss in einer Panik enden — und der letzte
	// Datensatz vor dem Herunterfahren ist genau ein solcher später
	// Schnappschuss. Statt zu schließen, läuft eine verwaiste Anfrage in ihre
	// Frist, und der Kanal meldet sich als "stalled".
	for ctx.Err() == nil {
		abgestuerzt := k.schleife(ctx, s)
		if !abgestuerzt {
			return
		}
		// In Go tötet eine ungefangene Panik in irgendeiner Goroutine den
		// ganzen Prozess. Ohne diese Isolation legte ein unerwartetes
		// Bitmuster auf einem Lokalmux den Bundesmux-Kanal mit lahm.
		k.panics.Add(1)
		k.log.Error("Kanalzustand wird nach einer Panik neu aufgesetzt",
			"panics", k.panics.Load())
	}
}

// schleife ist die Kanal-Goroutine. Zurück kommt true, wenn sie durch eine
// Panik endete und neu aufgesetzt werden muss.
func (k *kanal) schleife(ctx context.Context, s *Supervisor) (abgestuerzt bool) {
	defer func() {
		if r := recover(); r != nil {
			k.log.Error("Panik in der Kanal-Zustandsmaschine",
				"panik", r, "stack", string(debug.Stack()))
			abgestuerzt = true
		}
	}()

	zustand := chanstate.Neu(k.konfig, chanstate.Senken{
		Kommando:   k.rx.Sende,
		Wecke:      func(grund string) { s.wecke(k.name + ": " + grund) },
		EidGesehen: func(eid string) { s.merkeEid(eid, k) },
		OeVerweis:  func(eid string) bool { return s.loeseOeAuf(eid, k) },
		Audio:      s.audio,
		Log:        k.log,
	})
	k.letzterZustand.Store(report.RxStarting)

	fristen := time.NewTicker(FristenTakt)
	defer fristen.Stop()

	// Der Record-Strom hat eine eigene Zeitbasis. Im Betrieb deckt sie sich
	// mit der Knotenuhr; im Replay liegt sie Tage oder Monate zurück. Die
	// Zustandsmaschine rechnet ausschließlich in Stromzeit — hier wird die
	// Rechneruhr dorthin umgerechnet.
	var letzterTs, wallBeiRecord time.Time
	stromzeit := func() time.Time {
		if letzterTs.IsZero() {
			return time.Now().UTC()
		}
		return letzterTs.Add(time.Since(wallBeiRecord))
	}

	for {
		select {
		case <-ctx.Done():
			zustand.Beende(stromzeit())
			k.beantworteRest(zustand, stromzeit)
			return false

		case a := <-k.abschluss:
			// Der Abschluss läuft in derselben Goroutine wie alles andere:
			// erst die laufenden Alerts schließen und die Mitschnitte stoppen,
			// dann den letzten Schnappschuss ziehen. Nur so trägt der letzte
			// Datensatz close_reason: "shutdown" statt eines offenen Alerts,
			// der nie wieder auftaucht.
			zustand.Beende(stromzeit())
			a.antwort <- zustand.Schnappschuss(stromzeit())
			k.beantworteRest(zustand, stromzeit)
			return false

		case n, ok := <-k.rx.Nachrichten():
			if !ok {
				// Der Subprozess ist endgültig weg. Die Zustandsmaschine läuft
				// weiter, damit Fristen und Schnappschüsse funktionieren.
				k.rx = nil
				continue
			}
			switch {
			case n.Record != nil:
				zustand.Verarbeite(*n.Record)
				if !n.Record.Zeit.IsZero() {
					letzterTs, wallBeiRecord = n.Record.Zeit, time.Now()
				}
			case n.Zustand != nil:
				zustand.Melde(*n.Zustand, stromzeit())
				k.letzterZustand.Store(n.Zustand.RxZustand)
			}

		case a := <-k.anfragen:
			a.antwort <- zustand.Schnappschuss(stromzeit())

		case bis := <-k.bereitschaft:
			zustand.SetzeBereitschaft(bis)

		case <-fristen.C:
			zustand.Fristen(stromzeit())
		}
	}
}

// beantworteRest bedient noch anstehende Anfragen beim Herunterfahren.
func (k *kanal) beantworteRest(zustand *chanstate.Kanal, stromzeit func() time.Time) {
	for {
		select {
		case a := <-k.anfragen:
			a.antwort <- zustand.Schnappschuss(stromzeit())
		default:
			return
		}
	}
}

// schnappschuss fragt den Kanalzustand ab. Antwortet er nicht innerhalb der
// Frist, kommt ein Ersatzabschnitt mit rx_state: "stalled".
func (k *kanal) schnappschuss() report.Channel {
	return k.frage(k.anfragen, SchnappschussFrist)
}

// abschlussSchnappschuss beendet den Kanalzustand und holt seinen letzten Stand.
//
// Er bekommt mehr Zeit als ein gewöhnlicher Schnappschuss: Hier werden
// Mitschnitte geschlossen und Prüfsummen berechnet, und das ist Plattenarbeit.
func (k *kanal) abschlussSchnappschuss(frist time.Duration) report.Channel {
	return k.frage(k.abschluss, frist)
}

func (k *kanal) frage(ziel chan anfrage, frist time.Duration) report.Channel {
	antwort := make(chan report.Channel, 1)
	select {
	case ziel <- anfrage{antwort: antwort}:
	default:
		return k.haengt("die Kanal-Goroutine nimmt keine Anfragen mehr an")
	}

	select {
	case ch := <-antwort:
		return ch
	case <-time.After(frist):
		return k.haengt("keine Antwort innerhalb von " + frist.String())
	}
}

func (k *kanal) haengt(grund string) report.Channel {
	k.log.Error("Kanal antwortet nicht", "grund", grund)
	return report.Channel{
		Channel:   k.name,
		RxState:   report.RxStalled,
		LastError: grund,
		Asa: report.Asa{
			Records:   []report.AsaRecord{},
			Alerts:    []report.Alert{},
			Heartbeat: report.Heartbeat{MissingSeconds: []string{}},
		},
	}
}

// setzeBereitschaft versetzt den Kanal in Bereitschaft, ohne zu blockieren.
func (k *kanal) setzeBereitschaft(bis time.Time) {
	select {
	case k.bereitschaft <- bis:
	default:
	}
}
