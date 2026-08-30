// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/loc"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

const (
	// StilleBisAbbruch ist die Frist, nach der ein Alert ohne End-Phase als
	// abgebrochen gilt.
	StilleBisAbbruch = 30 * time.Second

	// SatzFrist ist die Frist, nach der ein unvollständiges Alert-Set
	// abgeschlossen wird. Bei Sekundenzähler 59 bricht der Sender die
	// Alert-Group ohnehin ab.
	SatzFrist = 2 * time.Second

	// MaxInstanzen ist die normative Obergrenze eines Alert-Sets.
	MaxInstanzen = 4

	// Nachklangfrist ist die Spanne, in der Instanzen noch einem bereits
	// abgeschlossenen Alert zugeordnet werden.
	//
	// Die End-Phase wird 1x je Transmission Frame über zwei Sekunden gesendet.
	// Die erste Instanz schließt den Alert ab; die folgenden gehören zu
	// demselben Vorfall und dürfen keinen zweiten anlegen. Ohne diese Frist
	// erschiene jeder Alert im Datensatz zweimal — einmal richtig und einmal
	// als Geist, der nur die End-Phase kennt.
	Nachklangfrist = 5 * time.Second

	// AudioMeldefrist ist die Zeit, die ein abgeschlossener Alert auf seinen
	// aud-Record wartet, bevor er trotzdem aufgeräumt wird.
	//
	// Seit dem 30.08.2026 schreibt asamon-rx die Mitschnitte selbst und meldet
	// sie erst nach dem STOP — also nach der letzten Meldung des Alerts. Ohne
	// diese Wartezeit wäre der Alert bereits aufgeräumt, wenn seine Dateien
	// bekannt werden, und der Server erführe nie von ihnen. Großzügig bemessen:
	// Zwischen STOP und Record liegt nur das Schließen zweier Dateien, aber auf
	// einer vollen SD-Karte kann auch das dauern.
	AudioMeldefrist = 60 * time.Second

	// IidUnbekannt steht im Schlüssel, solange kein Status-Feld kam.
	IidUnbekannt = -1
)

// alertSatz sammelt die 1..4 FIG-0/15-Instanzen, die einen Alert samt
// vollständigem Warngebiet beschreiben.
//
// Zusammengehalten werden sie durch identisches Id-Feld, identisches
// Status-Feld außer dem last-Flag, und nff — die Zahl der noch folgenden
// Instanzen. Die letzte hat nff == 0.
type alertSatz struct {
	// begonnen ist in Stromzeit, nicht in Ensemble-Zeit: Fristen werden
	// gegen die Uhr der Zustandsmaschine geprüft, nicht gegen den Sender.
	begonnen       time.Time
	erwartet       int
	instanzen      int
	letztesNff     int
	bytesHex       string
	bytes          []byte
	unvollstaendig bool
	fertig         bool
	codes          []loc.Code
	fehler         string
}

// phasenAbschnitt ist ein Abschnitt im Phasenverlauf.
type phasenAbschnitt struct {
	phase Phase
	von   time.Time
	bis   time.Time
	sec   *int
}

// verfolgterAlert ist ein Vorfall, solange der Knoten ihn beobachtet.
type verfolgterAlert struct {
	uid       string
	uidSicher bool

	oe           bool
	kanalEid     string // EId des empfangenen Ensembles
	warnendeEid  string // bei oe: other_eid — das warnende, nicht das empfangene
	subChID      int
	subChBekannt bool
	iid          int
	iidBekannt   bool

	stage Stage
	test  bool

	phase         Phase
	einstiegPhase Phase
	phasen        []phasenAbschnitt

	// startEns ist der berechnete Beginn des Alerts in Ensemble-Zeit. Aus ihm
	// wird die Startminute des alert_uid. Sie ist der Grund, warum zwei Knoten
	// zum selben uid kommen: Alerts beginnen laut Norm an der Minutengrenze.
	startEns       time.Time
	erstGesehen    time.Time
	zuletztGesehen time.Time

	// zuletztStrom ist derselbe Zeitpunkt in Stromzeit. Beides getrennt zu
	// führen ist kein Luxus: erstGesehen und zuletztGesehen gehen als
	// Zeitstempel in den Datensatz und müssen deshalb Ensemble-Zeit sein,
	// während jede Frist gegen die Uhr der Zustandsmaschine läuft. Wer beides
	// vermischt, bekommt im Replay einen Alert, der eine Sekunde nach seinem
	// Beginn "seit dreißig Sekunden still" ist.
	zuletztStrom time.Time

	geschlossen    bool
	grund          Schliessgrund
	gemeldet       bool // wurde bereits ein letztes Mal mit closed:true gemeldet
	unvollstaendig bool
	luecke         bool // besteht über einen Strom-Neustart hinweg

	// hatStatusInstanz sagt, ob je eine Instanz mit Status-Feld kam — also
	// pre_trigger oder trigger. Nur dann lässt sich über das Warngebiet
	// überhaupt eine Aussage treffen.
	hatStatusInstanz bool
	hatLocationCodes bool
	offenerSatz      *alertSatz
	letzterSatz      *alertSatz

	audioLaeuft        time.Time // Nullzeit = läuft nicht
	audioStopBei       time.Time // Nullzeit = kein Nachlauf geplant
	audioAbgeschnitten bool
	// audioBegonnen bleibt gesetzt, auch nachdem die Aufnahme gestoppt wurde.
	// Der aud-Record von asamon-rx kommt **nach** dem STOP — ein Alert mit
	// laufendem Audio ist zu diesem Zeitpunkt per Definition nicht mehr zu
	// finden.
	audioBegonnen time.Time
	// audioGemeldet sagt, dass asamon-rx die fertigen Dateien genannt hat.
	// Bis dahin bleibt der Alert in der Verfolgung, auch wenn er längst
	// abgeschlossen ist: Sonst käme die Aufnahme in keinem Datensatz vor, und
	// der Server erführe nie, dass es sie gibt.
	audioGemeldet bool
}

// schluessel ist die Kennung, unter der ein Alert verfolgt wird.
//
// Der IId ist nur 4 bit breit und ensemble-lokal wiederverwendet — ein
// Schlüsselwechsel ist deshalb an einen zeitlichen Rahmen gebunden, nicht an
// den Wert allein.
type schluessel struct {
	oe  bool
	id  string // subch_id als Dezimaltext bei oe=false, sonst other_eid
	iid int
}

func (a *verfolgterAlert) schluessel() schluessel {
	return schluessel{oe: a.oe, id: a.id(), iid: a.iid}
}

func (a *verfolgterAlert) id() string {
	if a.oe {
		return a.warnendeEid
	}
	if !a.subChBekannt {
		return "?"
	}
	return itoa(a.subChID)
}

// wechsleZu setzt die Phase und schreibt den Verlauf fort. Zurück kommt, ob es
// ein Wechsel war — jeder Wechsel schließt den laufenden Datensatz sofort ab.
func (a *verfolgterAlert) wechsleZu(p Phase, jetzt time.Time, sec *int) bool {
	if a.phase == p {
		// Der Sekundenzähler des Pre-Triggers kann nachgereicht werden.
		if sec != nil && len(a.phasen) > 0 && a.phasen[len(a.phasen)-1].sec == nil {
			a.phasen[len(a.phasen)-1].sec = sec
		}
		return false
	}
	if n := len(a.phasen); n > 0 {
		a.phasen[n-1].bis = jetzt
	}
	a.phase = p
	a.phasen = append(a.phasen, phasenAbschnitt{phase: p, von: jetzt, sec: sec})
	return true
}

// schliesse beendet den Alert.
func (a *verfolgterAlert) schliesse(grund Schliessgrund, jetzt time.Time) {
	if a.geschlossen {
		return
	}
	a.geschlossen = true
	a.grund = grund
	if n := len(a.phasen); n > 0 && a.phasen[n-1].bis.IsZero() {
		a.phasen[n-1].bis = jetzt
	}
}

// bericht baut den Alert-Abschnitt des Datensatzes.
func (a *verfolgterAlert) bericht(audio *report.Audio) report.Alert {
	al := report.Alert{
		AlertUID:          a.uid,
		AlertUIDConfident: a.uidSicher,
		Oe:                a.oe,
		ChannelEid:        a.kanalEid,
		WarningEid:        a.warnendeEid,
		Stage:             a.stage.String(),
		Test:              a.test,
		Phase:             a.phase.String(),
		EnteredAtPhase:    a.einstiegPhase.String(),
		FirstSeenEns:      report.Sekundenzeit(a.erstGesehen),
		LastSeenEns:       report.Sekundenzeit(a.zuletztGesehen),
		Closed:            a.geschlossen,
		CloseReason:       a.grund.String(),
		Incomplete:        a.unvollstaendig,
		Gap:               a.luecke,
		Phases:            make([]report.Phase, 0, len(a.phasen)),
		Audio:             audio,
	}
	if a.subChBekannt {
		v := a.subChID
		al.SubChID = &v
	}
	if a.iidBekannt {
		v := a.iid
		al.Iid = &v
	}
	if lvl, ok := a.stage.Level(); ok {
		al.Level = &lvl
	}
	for _, p := range a.phasen {
		rp := report.Phase{Phase: p.phase.String(), From: report.Sekundenzeit(p.von), Sec: p.sec}
		if !p.bis.IsZero() {
			rp.To = report.Sekundenzeit(p.bis)
		}
		al.Phases = append(al.Phases, rp)
	}

	satz := a.letzterSatz
	if satz == nil {
		satz = a.offenerSatz
	}
	// whole_ensemble ist dreiwertig: true heißt "keine Location Codes, also
	// das gesamte Versorgungsgebiet", false heißt "es gibt ein Warngebiet",
	// und null heißt "wir haben nie eine Instanz mit Status-Feld gesehen und
	// können deshalb nichts sagen". Ein fehlendes Feld ist hier eine Aussage,
	// ein falsches wäre eine Behauptung.
	al.Area = report.Area{Codes: []report.AreaCode{}}
	if a.hatStatusInstanz || a.hatLocationCodes {
		ganz := !a.hatLocationCodes
		al.Area.WholeEnsemble = &ganz
	}
	if satz != nil {
		al.Instances = satz.instanzen
		al.ExpectedInstances = satz.erwartet
		al.Area.Raw = satz.bytesHex
		al.Area.DecodeError = satz.fehler
		if satz.unvollstaendig {
			al.Incomplete = true
		}
		for _, c := range satz.codes {
			ac := report.AreaCode{Zone: int(c.Zone), Digits: c.DigitsHex(), Presentation: c.Presentation()}
			if r, err := c.Rect(); err == nil {
				ac.Rect = &report.Rect{LatMin: r.LatMin, LatMax: r.LatMax, LonMin: r.LonMin, LonMax: r.LonMax}
			}
			al.Area.Codes = append(al.Area.Codes, ac)
		}
		if len(satz.codes) > 0 {
			if geo, _ := loc.MultiPolygon(satz.codes); geo != nil {
				al.Area.GeoJSON = rohesGeoJSON(geo)
			}
		}
	}
	return al
}
