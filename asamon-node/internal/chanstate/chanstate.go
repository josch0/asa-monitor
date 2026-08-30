// SPDX-License-Identifier: GPL-3.0-or-later

// Paket chanstate hält den Zustand eines überwachten DAB-Kanals und deutet
// den Record-Strom von asamon-rx: Telemetrie, Ensemble, Heartbeat-Überwachung,
// Alert-Sets und Phasenverläufe.
//
// Der Zustand gehört genau einer Goroutine. Zugriff von außen ausschließlich
// über Kanäle, keine Mutexe darauf — einfädig heißt deterministisch, und
// deterministisch heißt gegen Aufzeichnungen prüfbar.
//
// Die Zustandsmaschine ist eine **reine Funktion aus Record-Strom und Uhr**.
// Damit ist sie gegen aufgezeichnete Ströme wiederholbar abspielbar, und genau
// darauf beruht ihre Prüfbarkeit: Eine Referenzimplementierung von FIG 0/15
// gibt es nicht.
package chanstate

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/audio"
	"github.com/josch0/asa-monitor/asamon-node/internal/hashes"
	"github.com/josch0/asa-monitor/asamon-node/internal/loc"
	"github.com/josch0/asa-monitor/asamon-node/internal/record"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// MaxFehlendeSekunden begrenzt die Liste der fehlenden Heartbeat-Sekunden im
// Datensatz. Die Zahl selbst steht immer vollständig im Aggregat.
const MaxFehlendeSekunden = 120

// MaxAuffaelligkeiten begrenzt die Liste der gemeldeten Auffälligkeiten je
// Fenster. Ein Kanal, der dauerhaft Unsinn liefert, soll den Datensatz nicht
// sprengen.
const MaxAuffaelligkeiten = 20

// Konfig ist die Einstellung eines Kanals.
type Konfig struct {
	Channel          string
	AudioAktiv       bool
	PostRoll         time.Duration
	MaxAudioSekunden int
}

// Audiosenke nimmt die Mitschnitte entgegen. In Tests darf sie fehlen.
//
// Geschrieben werden die Dateien seit dem 30.08.2026 von asamon-rx; hierher
// kommt nur noch, was es am Ende meldet. Deshalb Uebernimm() statt Schreibe():
// keine Bytes, keine Stücknummern, keine Lücken.
type Audiosenke interface {
	Beginne(alertUID, channel string, subChID int, start time.Time, bitrate int)
	Beende(alertUID string, abgeschnitten bool)
	Uebernimm(alertUID string, u audio.Uebernahme)
	Stand(alertUID string) *report.Audio
}

// Senken sind die Ausgänge des Kanalzustands. Alle sind einzeln entbehrlich —
// ein nil-Feld heißt schlicht "wird nicht gebraucht", und die Zustandsmaschine
// bleibt für sich allein prüfbar.
type Senken struct {
	// Kommando schickt REC/STOP an den asamon-rx dieses Kanals.
	Kommando func(cmd string)
	// Wecke meldet dem Reporter, dass sofort ein Datensatz fällig ist.
	Wecke func(grund string)
	// EidGesehen pflegt die EId-Tabelle des Supervisors.
	EidGesehen func(eid string)
	// OeVerweis bittet den Supervisor, den Kanal mit dieser EId in
	// Bereitschaft zu versetzen. Zurück kommt, ob ein solcher Kanal existiert.
	OeVerweis func(eid string) bool
	Audio     Audiosenke
	Log       *slog.Logger
}

// Zustandsmeldung kommt von der Prozessverwaltung, nicht aus dem Record-Strom.
type Zustandsmeldung struct {
	// Neustart sagt, dass ein neuer Strom begonnen hat. Der Kanalzustand
	// bleibt erhalten, nur die Sequenzverfolgung beginnt neu.
	Neustart      bool
	RxZustand     string
	LetzterFehler string
	Neustarts     int
	// NodeVerworfen sind Records, die die Lese-Goroutine verwerfen musste,
	// weil die Warteschlange voll war. Kumulativ.
	NodeVerworfen uint64
	// Stromzaehler sind die Zähler des Zeilenlesers, kumulativ je Strom.
	Stromzaehler record.Zaehler
}

// Kanal ist der Zustand eines überwachten DAB-Kanals.
type Kanal struct {
	k Konfig
	s Senken

	init    *record.Init
	zustand Zustandsmeldung

	// Ensemble
	ens            *record.Ens
	ensHash        string
	ensContentHash string
	ensErst        time.Time
	ensZuletzt     time.Time
	eidAusTlm      string

	uhr ensUhr

	// jetzt ist die Uhr der Zustandsmaschine. Sie läuft in der **Zeitbasis des
	// Record-Stroms**, nicht in der des Rechners.
	//
	// Das ist keine Spitzfindigkeit: Im Replay liegen die Zeitstempel eines
	// Mitschnitts Tage oder Monate zurück. Würden Fristen gegen die
	// Rechneruhr geprüft, liefe jeder Alert sofort in seine Abbruchfrist. Wer
	// diese Uhr vorantreibt, ist der Aufrufer — im Betrieb aus der Knotenuhr,
	// im Replay aus den Records selbst.
	jetzt time.Time

	// letzteGrenze ist das Ende des zuletzt gemeldeten Fensters, ebenfalls in
	// Stromzeit. Der Kanal führt sie selbst: Nur er kennt seine Zeitbasis.
	letzteGrenze time.Time

	// stromzeitGesetzt sagt, ob die Zeitbasis schon aus einem Record stammt.
	//
	// Vor dem ersten Record vertritt die Rechneruhr sie — der Reporter zieht
	// seinen ersten Schnappschuss, bevor irgendein Kanal synchron ist. Kommt
	// dann ein Mitschnitt von gestern, läge die Uhr einen Tag in der Zukunft
	// und keine Frist und kein Fenster ergäbe je wieder Sinn. Deshalb setzt
	// der erste Record die Uhr **hart**, nicht nur vorwärts.
	stromzeitGesetzt bool

	// dauerhaft
	everSeen bool
	alerts   []*verfolgterAlert

	// Bereitschaft aus einem OE-Verweis eines anderen Kanals
	bereitschaftBis time.Time

	// Fenster: alles hier wird beim Schnappschuss zurückgesetzt
	fenster fensterwerte

	// Basiswerte für die kumulativen Zähler des Stroms
	letzteDropped     uint64
	letzteParseErrors uint64
	letzteNodeDropped uint64
	letzteSeqLuecken  uint64
	letzteKaputt      uint64
	letzteUnbekannt   uint64
}

type fensterwerte struct {
	tlmAnzahl  int
	snrSumme   float64
	snrAnzahl  int
	snrMin     float64
	snrMax     float64
	syncAnzahl int

	fibTotal    uint64
	fibCrcErr   uint64
	dropped     uint64
	nodeDropped uint64
	parseErrors uint64
	seqLuecken  uint64
	kaputt      uint64
	unbekannt   uint64

	asaRecords    []report.AsaRecord
	hbSekunden    map[int64]bool
	alertSekunden map[int64]bool
	pdMismatch    int

	auffaelligkeiten []string
	auffaelligGesamt int
}

func neueFensterwerte() fensterwerte {
	return fensterwerte{
		hbSekunden:    map[int64]bool{},
		alertSekunden: map[int64]bool{},
	}
}

// Neu baut einen Kanalzustand.
func Neu(k Konfig, s Senken) *Kanal {
	if s.Log == nil {
		s.Log = slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return &Kanal{
		k:       k,
		s:       s,
		zustand: Zustandsmeldung{RxZustand: report.RxStarting},
		fenster: neueFensterwerte(),
	}
}

// Name gibt den DAB-Kanalnamen.
func (c *Kanal) Name() string { return c.k.Channel }

// Jetzt gibt die Uhr der Zustandsmaschine.
func (c *Kanal) Jetzt() time.Time { return c.jetzt }

// UnbekannteRecords gibt die Zahl der Records mit unbekanntem Typ im Fenster.
func (c *Kanal) UnbekannteRecords() uint64 { return c.fenster.unbekannt }

// Melde nimmt eine Zustandsmeldung der Prozessverwaltung entgegen.
func (c *Kanal) Melde(z Zustandsmeldung, jetzt time.Time) {
	c.ruecke(jetzt)
	if z.Neustart {
		c.stromNeustart()
	}
	// Die Zähler des Stroms sind kumulativ; nach einem Neustart beginnen sie
	// wieder bei 0. Das darf keine negativen Deltas geben.
	c.fenster.seqLuecken += delta(&c.letzteSeqLuecken, z.Stromzaehler.SeqLuecken)
	c.fenster.kaputt += delta(&c.letzteKaputt, z.Stromzaehler.KaputteZeilen)
	c.fenster.unbekannt += delta(&c.letzteUnbekannt, z.Stromzaehler.UnbekannteRecords)
	c.fenster.nodeDropped += delta(&c.letzteNodeDropped, z.NodeVerworfen)

	z.Stromzaehler = record.Zaehler{}
	z.NodeVerworfen = 0
	c.zustand = z
}

// stromNeustart hält fest, was ein Neustart von asamon-rx für die Deutung
// bedeutet: Der Kanalzustand bleibt erhalten, aber jeder Alert, der über den
// Neustart hinweg besteht, ist möglicherweise unvollständig.
func (c *Kanal) stromNeustart() {
	c.letzteDropped, c.letzteParseErrors = 0, 0
	c.letzteSeqLuecken, c.letzteKaputt, c.letzteUnbekannt = 0, 0, 0
	c.uhr = ensUhr{}
	for _, a := range c.alerts {
		if !a.geschlossen {
			a.luecke = true
			c.melde("Alert %s besteht über einen Strom-Neustart hinweg und ist damit möglicherweise unvollständig", kurz(a.uid))
		}
	}
}

// Verarbeite deutet einen Record.
func (c *Kanal) Verarbeite(rec record.Record) {
	c.uebernimmStromzeit(rec.Zeit)
	c.ruecke(rec.Zeit)

	switch rec.Kind {
	case record.KindInit:
		c.verarbeiteInit(rec.Init)
	case record.KindTlm:
		c.verarbeiteTlm(rec.Tlm)
	case record.KindEns:
		c.verarbeiteEns(rec.Ens)
	case record.KindAsa:
		c.verarbeiteAsa(rec)
	case record.KindAud:
		c.verarbeiteAud(rec.Aud)
	case record.KindUnbekannt:
		c.fenster.unbekannt++
	default:
		c.melde("Record-Typ %s ohne Behandlung", rec.Kind)
	}
	c.pruefeFristen()
}

// Fristen treibt die zeitabhängigen Übergänge voran, auch wenn kein Record
// kommt. Ein toter asamon-rx darf keinen Alert für immer offen halten.
//
// bis ist die Zeit in der Zeitbasis des Record-Stroms.
func (c *Kanal) Fristen(bis time.Time) {
	c.ruecke(bis)
	c.pruefeFristen()
}

// Beende schließt alle laufenden Alerts und stoppt die Mitschnitte.
func (c *Kanal) Beende(jetzt time.Time) {
	c.ruecke(jetzt)
	for _, a := range c.alerts {
		if !a.geschlossen {
			a.schliesse(GrundShutdown, c.jetzt)
		}
		c.stoppeAudio(a, false)
	}
}

// uebernimmStromzeit setzt die Zeitbasis auf den ersten Record des Stroms.
func (c *Kanal) uebernimmStromzeit(t time.Time) {
	if t.IsZero() || c.stromzeitGesetzt {
		return
	}
	c.stromzeitGesetzt = true
	c.jetzt = t.UTC()
	c.letzteGrenze = t.UTC()
}

func (c *Kanal) ruecke(t time.Time) {
	if t.IsZero() {
		return
	}
	t = t.UTC()
	if t.After(c.jetzt) {
		c.jetzt = t
	}
	if c.letzteGrenze.IsZero() {
		c.letzteGrenze = t
	}
}

func (c *Kanal) verarbeiteInit(in *record.Init) {
	c.init = in
	if in.FormatVersion != 0 && in.FormatVersion != record.FormatVersion {
		c.melde("format_version %d, erwartet %d — der Strom wird möglicherweise falsch gedeutet", in.FormatVersion, record.FormatVersion)
	}
	if in.Channel != "" && in.Channel != c.k.Channel {
		c.melde("asamon-rx meldet Kanal %q, konfiguriert ist %q", in.Channel, c.k.Channel)
	}
}

func (c *Kanal) verarbeiteTlm(t *record.Tlm) {
	f := &c.fenster
	f.tlmAnzahl++
	if t.Snr != nil {
		v := *t.Snr
		if f.snrAnzahl == 0 || v < f.snrMin {
			f.snrMin = v
		}
		if f.snrAnzahl == 0 || v > f.snrMax {
			f.snrMax = v
		}
		f.snrSumme += v
		f.snrAnzahl++
	}
	if t.Sync {
		f.syncAnzahl++
	}
	f.fibTotal += t.FibTotal
	f.fibCrcErr += t.FibCrcErr
	f.dropped += delta(&c.letzteDropped, t.Dropped)
	f.parseErrors += delta(&c.letzteParseErrors, t.ParseErrors)

	if t.Eid != "" {
		c.eidAusTlm = t.Eid
		if c.s.EidGesehen != nil {
			c.s.EidGesehen(hashes.Hex(t.Eid, 4))
		}
	}
	if t.EnsTime != "" {
		if ez, err := time.Parse(time.RFC3339, t.EnsTime); err == nil {
			c.uhr.Stelle(ez, c.jetzt)
		} else {
			c.melde("ens_time %q ist nicht lesbar: %v", t.EnsTime, err)
		}
	}
}

func (c *Kanal) verarbeiteEns(e *record.Ens) {
	c.ens = e
	if c.ensErst.IsZero() {
		c.ensErst = c.jetzt
	}
	c.ensZuletzt = c.jetzt

	c.ensHash = hashes.Ens(c.k.Channel, e.Eid, e.Ecc)
	dienste := make([]hashes.Service, 0, len(e.Services))
	for _, s := range e.Services {
		hs := hashes.Service{Sid: s.Sid, Label: s.Label}
		for _, k := range s.Komponenten {
			hs.Komponenten = append(hs.Komponenten, hashes.Komponente{
				SubChID: k.SubChID, StartAddr: k.StartAddr, Size: k.Size,
				Protection: k.Protection, Bitrate: k.Bitrate,
			})
		}
		dienste = append(dienste, hs)
	}
	c.ensContentHash = hashes.EnsContent(c.ensHash, e.Label, dienste)

	if e.Eid != "" && c.s.EidGesehen != nil {
		c.s.EidGesehen(hashes.Hex(e.Eid, 4))
	}
}

func (c *Kanal) verarbeiteAsa(rec record.Record) {
	a := rec.Asa
	c.everSeen = true
	ensSek, quelle := c.uhr.Sekunde(c.jetzt)

	// Der Hash braucht die Ensemble-Identität. Solange sie fehlt — die ersten
	// Sekunden nach dem Start —, bleibt das Feld leer statt falsch: Ein Hash
	// ohne ens_hash würde zwei Ensembles zusammenwerfen, die zufällig
	// dasselbe raw in derselben Sekunde tragen.
	asaHash := ""
	if c.ensHash != "" {
		asaHash = hashes.Asa(c.ensHash, ensSek, a.Raw)
	}
	c.fenster.asaRecords = append(c.fenster.asaRecords, report.AsaRecord{
		AsaHash:      asaHash,
		EnsSecond:    report.Sekundenzeit(ensSek),
		TimeSource:   quelle,
		Ts:           rec.Ts,
		Heartbeat:    a.Heartbeat,
		Cn:           a.Cn,
		Oe:           a.Oe,
		PdSecondHalf: a.PdSecondHalf,
		Raw:          a.Raw,
	})

	// P/D ist bei FIG 0/15 zweckentfremdet: Es sagt die Sekundenhälfte an.
	// Eine Abweichung ist ein Hinweis darauf, dass Ensemble-Uhr und Sender
	// auseinanderlaufen — oder dass wir die falsche Sekunde annehmen.
	if quelle == report.ZeitAusEnsemble {
		if a.PdSecondHalf != (ensSek.Second() >= 30) {
			c.fenster.pdMismatch++
		}
	}

	if a.Heartbeat {
		c.fenster.hbSekunden[ensSek.Unix()] = true
		return
	}
	c.fenster.alertSekunden[ensSek.Unix()] = true
	c.verarbeiteAlertInstanz(a, ensSek)
}

// verarbeiteAud übernimmt eine abgeschlossene Aufnahme.
//
// Der Record trägt die alert_uid selbst, weil der Knoten sie beim REC
// mitgegeben hat — sie ist die verlässliche Zuordnung. Fehlt sie (ein
// asamon-rx, das ohne uid gestartet wurde), bleibt der Subchannel als Notnagel.
func (c *Kanal) verarbeiteAud(a *record.Aud) {
	// Den verfolgten Alert suchen — über die uid, die der Knoten beim REC
	// mitgegeben hat, sonst über den Subchannel.
	var ziel *verfolgterAlert
	for _, al := range c.alerts {
		if a.AlertUID != "" && al.uid == a.AlertUID {
			ziel = al
			break
		}
	}
	if ziel == nil {
		ziel = c.alertMitAufnahme(a.SubChID)
	}
	if ziel != nil {
		ziel.audioGemeldet = true
		// Der Alert war zu diesem Zeitpunkt längst mit closed: true gemeldet.
		// Damit die Dateien überhaupt in einen Datensatz kommen, muss er noch
		// einmal hinaus — danach räumt ihn raeumeAlertsAuf() ab.
		if ziel.gemeldet {
			ziel.gemeldet = false
			if c.s.Wecke != nil {
				c.s.Wecke("audio")
			}
		}
	}

	uid := a.AlertUID
	if uid == "" {
		// Der Record kommt nach dem STOP, ein *laufendes* Audio gibt es zu
		// diesem Zeitpunkt also nie — gesucht wurde oben der jüngste Alert,
		// der auf diesem Subchannel aufgenommen hat.
		if ziel != nil {
			uid = ziel.uid
		} else if len(a.Files) > 0 {
			// Auch das kann fehlschlagen — etwa, wenn der Alert längst
			// abgeschlossen und aus der Verfolgung genommen wurde. Dann ist
			// der Dateiname die beste Kennung, die es gibt: Eine fertige
			// Aufnahme wegzuwerfen wäre der schlechtere Tausch.
			uid = strings.TrimSuffix(a.Files[0].Name, filepath.Ext(a.Files[0].Name))
			c.melde("aud-Record für Subchannel %d ohne alert_uid und ohne verfolgte Aufnahme; abgelegt als %q", a.SubChID, uid)
		} else {
			c.melde("aud-Record für Subchannel %d ohne alert_uid und ohne Datei", a.SubChID)
			return
		}
	}
	if a.Error != "" {
		c.melde("asamon-rx meldet zum Mitschnitt %s: %s", uid, a.Error)
	}
	if c.s.Audio == nil {
		return
	}

	u := audio.Uebernahme{
		Channel:     c.k.Channel,
		SubChID:     a.SubChID,
		Start:       zeitAus(a.Started),
		Seconds:     a.Seconds,
		Truncated:   a.Truncated,
		SampleRate:  a.SampleRate,
		Channels:    a.Channels,
		Mode:        a.Mode,
		FrameErrors: a.FrameErrors,
		RsErrors:    a.RsErrors,
		RsCorrected: a.RsCorrected,
		AacErrors:   a.AacErrors,
		Fehler:      a.Error,
	}
	for _, f := range a.Files {
		// Ein Dateiname aus fremdem Prozess gehört geprüft, bevor er zu einem
		// Pfad wird: Der Knoten öffnet die Datei später zum Hochladen.
		if !nameIstHarmlos(f.Name) {
			c.melde("aud-Record nennt einen unbrauchbaren Dateinamen %q", f.Name)
			continue
		}
		u.Dateien = append(u.Dateien, audio.Datei{
			Name: f.Name, Codec: f.Codec, Bytes: f.Bytes, Sha256: f.Sha256,
		})
	}
	c.s.Audio.Uebernimm(uid, u)
}

// zeitAus liest den Zeitstempel aus dem Record. Eine unlesbare Angabe ist kein
// Grund, die Aufnahme zu verwerfen — dann gilt eben, was der Knoten selbst
// weiß.
func zeitAus(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// nameIstHarmlos lässt nur einfache Dateinamen durch — kein Verzeichnisanteil,
// kein Aufstieg, nichts Leeres.
func nameIstHarmlos(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

func (c *Kanal) alertMitLaufendemAudio(subChID int) *verfolgterAlert {
	for _, a := range c.alerts {
		if !a.audioLaeuft.IsZero() && a.subChBekannt && a.subChID == subChID {
			return a
		}
	}
	return nil
}

// alertMitAufnahme findet den jüngsten Alert, der auf diesem Subchannel
// aufgenommen hat — laufend oder abgeschlossen. Nur für den Fall gedacht, dass
// der aud-Record keine alert_uid trägt.
func (c *Kanal) alertMitAufnahme(subChID int) *verfolgterAlert {
	var jung *verfolgterAlert
	for _, a := range c.alerts {
		if a.audioBegonnen.IsZero() || !a.subChBekannt || a.subChID != subChID {
			continue
		}
		if jung == nil || a.audioBegonnen.After(jung.audioBegonnen) {
			jung = a
		}
	}
	return jung
}

// melde hält eine Auffälligkeit fest. Auffälligkeiten sind keine Fehler: Ein
// unerwartetes Bitmuster auf einem Lokalmux ist eine Beobachtung und gehört
// gemeldet, nicht verworfen.
func (c *Kanal) melde(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	c.fenster.auffaelligGesamt++
	if len(c.fenster.auffaelligkeiten) < MaxAuffaelligkeiten {
		c.fenster.auffaelligkeiten = append(c.fenster.auffaelligkeiten, text)
	}
	// Der Kanalname steht bereits im Logger; ihn hier zu wiederholen ergäbe
	// zwei gleiche Schlüssel in derselben Zeile.
	c.s.Log.Warn(text)
}

// delta bildet die Zunahme eines kumulativen Zählers und merkt sich den neuen
// Stand. Ein Rückschritt bedeutet einen neuen Strom — dann ist der neue Wert
// selbst das Delta.
func delta(letzte *uint64, neu uint64) uint64 {
	if neu < *letzte {
		*letzte = neu
		return neu
	}
	d := neu - *letzte
	*letzte = neu
	return d
}

func itoa(v int) string { return strconv.Itoa(v) }

func kurz(uid string) string { return uid[:min(len(uid), 8)] }

// rohesGeoJSON reicht die von loc erzeugten Bytes unverändert in den Datensatz.
func rohesGeoJSON(b []byte) json.RawMessage { return json.RawMessage(b) }

// discard schluckt Logausgaben, wenn kein Logger gesetzt ist.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

var _ = hex.EncodeToString
var _ = loc.MaxLocationBytes
