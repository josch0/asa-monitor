// SPDX-License-Identifier: GPL-3.0-or-later

// Paket supervisor hält den Knoten zusammen: Kanäle starten und beaufsichtigen,
// Datensätze bauen, senden und im Ausfall sammeln.
//
//	main ── config, identity ──┬── supervisor ──┬── rxproc[5C]  ─┐
//	                           │                ├── rxproc[11D] ─┼─ je eine Lese-Goroutine
//	                           │                └── …            ─┘
//	                           │                       │ chan Nachricht (gepuffert)
//	                           │                       ▼
//	                           │                chanstate[5C], chanstate[11D], …
//	                           │                       │ chan anfrage
//	                           ├── reporter (Ticker) ──┘
//	                           │       │ chan *Report
//	                           ├── uplink ──── spool
//	                           └── audio (je Aufnahme eine Goroutine)
package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/audio"
	"github.com/josch0/asa-monitor/asamon-node/internal/chanstate"
	"github.com/josch0/asa-monitor/asamon-node/internal/config"
	"github.com/josch0/asa-monitor/asamon-node/internal/identity"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
	"github.com/josch0/asa-monitor/asamon-node/internal/rxproc"
	"github.com/josch0/asa-monitor/asamon-node/internal/spool"
	"github.com/josch0/asa-monitor/asamon-node/internal/uplink"
)

const (
	// SofortMindestabstand ist die Untergrenze zwischen zwei sofort gesendeten
	// Datensätzen. Ein Alert hat vier Phasenwechsel, nicht vierhundert — die
	// Grenze ist Vorsorge, keine Erwartung.
	SofortMindestabstand = time.Second

	// BereitschaftsDauer ist die Zeit, die ein Kanal nach einem OE-Verweis in
	// Bereitschaft bleibt.
	BereitschaftsDauer = 30 * time.Second

	// AbschaltFrist ist die Gesamtfrist für das Herunterfahren.
	AbschaltFrist = 20 * time.Second

	// AufraeumTakt ist der Abstand, in dem alte Mitschnitte gelöscht werden.
	AufraeumTakt = time.Hour
)

// Optionen steuern Betriebsarten, die nicht in der Konfigurationsdatei stehen.
type Optionen struct {
	// DryRun: alles außer Uplink; Datensätze als NDJSON nach stdout.
	DryRun bool
	// Einmal: einen Datensatz bauen, senden, beenden.
	Einmal bool
	// ReplayPfad ersetzt asamon-rx durch eine Datei je Kanal.
	ReplayPfad string
	// ReplaySpeed: 1.0 = Echtzeit, 0 = so schnell wie möglich.
	ReplaySpeed float64
	// FakeRxBinary ersetzt paths.rx_binary — nur für Tests.
	FakeRxBinary string
	// Ausgabe nimmt die Datensätze bei DryRun entgegen.
	Ausgabe func(*report.Report)
}

// Supervisor ist der Knoten.
type Supervisor struct {
	cfg   *config.Config
	id    *identity.Identity
	log   *slog.Logger
	opt   Optionen
	spl   *spool.Spool
	up    *uplink.Uplink
	audio *audio.Verwaltung

	kanaele []*kanal

	eidMu sync.Mutex
	eids  map[string]*kanal

	gestartet time.Time
	seq       atomic.Uint64

	wecken    chan string
	berichte  chan *report.Report
	sofortZul time.Time
	// sperreBis ist der Zeitpunkt, ab dem der Uplink wieder versucht wird.
	// Nur die Sender-Goroutine greift darauf zu.
	sperreBis time.Time

	unbekannteRecords atomic.Uint64
	verworfeneReports atomic.Uint64
	abgelehnteReports atomic.Uint64

	// FertigMelder wird nach dem ersten erfolgreichen Datensatz aufgerufen —
	// systemd erfährt so, dass der Knoten wirklich läuft.
	FertigMelder func()
	// Lebenszeichen wird bei jedem Berichtstakt aufgerufen (sd_notify
	// WATCHDOG=1). Der Watchdog wird nur bedient, solange der Reporter läuft;
	// ein hängender Reporter soll einen Neustart auslösen.
	Lebenszeichen func()
}

// Neu baut den Supervisor samt Spool, Uplink und Audio.
func Neu(cfg *config.Config, id *identity.Identity, log *slog.Logger, opt Optionen) (*Supervisor, error) {
	spl, err := spool.Neu(cfg.Paths.StateDir, cfg.Limits.MaxSpoolMB, log)
	if err != nil {
		return nil, err
	}
	aud, err := audio.Neu(cfg.Paths.StateDir, audio.Konfig{
		KeepDays: cfg.Audio.KeepDays,
		Aktiv:    cfg.Audio.Enabled,
	}, log)
	if err != nil {
		return nil, err
	}

	s := &Supervisor{
		cfg:       cfg,
		id:        id,
		log:       log,
		opt:       opt,
		spl:       spl,
		audio:     aud,
		eids:      map[string]*kanal{},
		gestartet: time.Now().UTC(),
		wecken:    make(chan string, 64),
		berichte:  make(chan *report.Report, 8),
	}
	s.up = uplink.Neu(uplink.Konfig{
		BaseURL:            cfg.Server.URL,
		Timeout:            cfg.Server.Timeout.D(),
		InsecureSkipVerify: cfg.Server.InsecureSkipVerify,
		NodeID:             id.NodeID,
	}, log)

	start, err := id.NextSeqStart()
	if err != nil {
		log.Warn("Sequenznummer", "hinweis", err)
	}
	s.seq.Store(start)

	if err := s.legeKanaeleAn(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Supervisor) legeKanaeleAn() error {
	binary := s.cfg.Paths.RxBinary
	if s.opt.FakeRxBinary != "" {
		binary = s.opt.FakeRxBinary
	}

	for _, ch := range s.cfg.Channels {
		k := &kanal{
			name:         ch.Channel,
			log:          s.log.With("channel", ch.Channel),
			anfragen:     make(chan anfrage, 2),
			abschluss:    make(chan anfrage, 1),
			bereitschaft: make(chan time.Time, 4),
			konfig: chanstate.Konfig{
				Channel:          ch.Channel,
				AudioAktiv:       s.cfg.Audio.Enabled,
				PostRoll:         s.cfg.Audio.PostRoll.D(),
				MaxAudioSekunden: s.cfg.Audio.MaxSeconds,
			},
		}
		rxKonfig := rxproc.Konfig{
			Channel:      ch.Channel,
			Binary:       binary,
			Device:       ch.Device,
			DeviceSerial: ch.DeviceSerial,
			Gain:         ch.Gain,
			IQFile:       ch.IQFile,
			LogLevel:     s.cfg.Log.Level,
			QueueSize:    s.cfg.Limits.QueueSize,
			StilleFrist:  time.Duration(s.cfg.Limits.RxSilenceSeconds) * time.Second,
			// Beide Prozesse müssen denselben Ordner meinen. Die Vorgaben
			// stimmen überein, aber nur so lange, wie niemand
			// paths.state_dir verlegt hat — deshalb ausdrücklich mitgeben.
			AudioOut: s.audio.Dir(),
		}
		if s.opt.ReplayPfad != "" {
			pfad, err := replayDatei(s.opt.ReplayPfad, ch.Channel)
			if err != nil {
				return err
			}
			// Im Replay ersetzt fake-rx den Empfangsprozess. Die
			// Zustandsmaschine unterscheidet nicht, woher ihr Strom kommt —
			// das ist die Grundlage aller Regressionstests.
			rxKonfig.VorabArgumente = []string{
				"--serve", "--file", pfad,
				"--speed", fmt.Sprintf("%g", s.opt.ReplaySpeed),
			}
			rxKonfig.Device, rxKonfig.DeviceSerial, rxKonfig.IQFile, rxKonfig.Gain = "", "", "", ""
			// Keine Stilleüberwachung im Replay: Eine Aufzeichnung ist
			// endlich, und ihr Ende ist kein Hänger. fake-rx wartet danach
			// auf QUIT — das ist gewollt und dürfte keinen Neustart auslösen.
			rxKonfig.StilleFrist = 0
		}
		k.rx = rxproc.Neu(rxKonfig, s.log)
		s.kanaele = append(s.kanaele, k)
	}
	return nil
}

// Run startet alles und läuft, bis ctx endet.
func (s *Supervisor) Run(ctx context.Context) error {
	// Zwei Abbruchpunkte, nicht einer: Die Empfangsprozesse bekommen ihr QUIT
	// zuerst, die Zustandsmaschinen leben so lange weiter, bis ihr letzter
	// Stand im Abschluss-Datensatz steht.
	rxCtx, rxBeenden := context.WithCancel(context.Background())
	kanalCtx, kanaeleBeenden := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	for _, k := range s.kanaele {
		wg.Go(func() { k.rx.Run(rxCtx) })
		wg.Go(func() { k.run(kanalCtx, s) })
	}

	senderCtx, senderBeenden := context.WithCancel(context.Background())
	wg.Go(func() { s.senderSchleife(senderCtx) })
	wg.Go(func() { s.aufraeumSchleife(kanalCtx) })

	err := s.reporterSchleife(ctx)

	// Geordnetes Herunterfahren, Gesamtfrist AbschaltFrist: den Kindern QUIT,
	// dann von jedem Kanal seinen Abschluss holen, dann den letzten Datensatz
	// einmal zu senden versuchen — sonst geht er in den Spool.
	abschluss, abschlussEnde := context.WithTimeout(context.Background(), AbschaltFrist)
	defer abschlussEnde()

	s.log.Info("Knoten fährt herunter")
	rxBeenden()

	// Kein SchliesseAlle() mehr: Die Dateien gehören asamon-rx, und dessen
	// QUIT ist oben schon durch. Was es noch geschrieben hat, findet der
	// nächste Start über den Ablageordner wieder.
	letzter := s.baueAbschlussDatensatz()
	s.versendeAbschluss(abschluss, letzter)

	kanaeleBeenden()
	senderBeenden()
	wg.Wait()

	if err := s.id.SaveSeq(s.seq.Load()); err != nil {
		s.log.Warn("Sequenznummer ließ sich nicht festschreiben", "fehler", err)
	}
	return err
}

// wecke meldet, dass sofort ein Datensatz fällig ist.
func (s *Supervisor) wecke(grund string) {
	select {
	case s.wecken <- grund:
	default:
	}
}

// merkeEid pflegt die EId-Tabelle. Über sie werden OE-Verweise lokal aufgelöst.
//
// Ein Kanal liest nie den Zustand eines anderen; die Auflösung läuft über diese
// kleine, mutexgeschützte Tabelle im Supervisor.
func (s *Supervisor) merkeEid(eid string, k *kanal) {
	if eid == "" {
		return
	}
	s.eidMu.Lock()
	defer s.eidMu.Unlock()
	if s.eids[eid] != k {
		s.eids[eid] = k
	}
}

// loeseOeAuf versetzt den Kanal in Bereitschaft, auf dem das warnende Ensemble
// empfangen wird. Zurück kommt, ob es einen solchen Kanal gibt.
//
// Verweist Kanal A auf ein Ensemble, das derselbe Knoten auf Kanal B empfängt,
// geht der Recorder dort sofort scharf — ohne Serverrunde. Das ist der
// Hauptgrund, warum ein Knoten mehrere Kanäle unter einem Prozess führt.
func (s *Supervisor) loeseOeAuf(eid string, quelle *kanal) bool {
	s.eidMu.Lock()
	ziel, ok := s.eids[eid]
	s.eidMu.Unlock()
	if !ok || ziel == quelle {
		return false
	}
	ziel.setzeBereitschaft(time.Now().Add(BereitschaftsDauer))
	s.log.Info("OE-Verweis lokal aufgelöst",
		"von", quelle.name, "nach", ziel.name, "other_eid", eid)
	return true
}

func (s *Supervisor) aufraeumSchleife(ctx context.Context) {
	t := time.NewTicker(AufraeumTakt)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := s.audio.RaeumeAuf(time.Now()); n > 0 {
				s.log.Info("alte Mitschnitte gelöscht", "dateien", n)
			}
		}
	}
}

func replayDatei(pfad, channel string) (string, error) {
	info, err := statPfad(pfad)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return pfad, nil
	}
	kandidat := joinPfad(pfad, channel+".ndjson")
	if _, err := statPfad(kandidat); err != nil {
		return "", fmt.Errorf("--replay: im Verzeichnis %s fehlt %s.ndjson für Kanal %s", pfad, channel, channel)
	}
	return kandidat, nil
}
