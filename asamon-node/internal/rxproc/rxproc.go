// SPDX-License-Identifier: GPL-3.0-or-later

// Paket rxproc startet und beaufsichtigt einen asamon-rx als Subprozess.
//
// Je konfiguriertem Kanal genau einer; asamon-node besitzt den Lebenszyklus.
// Eine systemd-Unit für den ganzen Knoten, gleich wie viele Sticks stecken.
//
// **Ein toter Kanal beendet niemals den Knoten.** Die übrigen Kanäle laufen
// unterbrechungsfrei weiter, und ein Kanal in Dauerneustart ist selbst eine
// meldenswerte Beobachtung.
package rxproc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/chanstate"
	"github.com/josch0/asa-monitor/asamon-node/internal/record"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

const (
	// BackoffStart und BackoffMax spannen das exponentielle Backoff auf.
	BackoffStart = time.Second
	BackoffMax   = 60 * time.Second

	// BackoffZuruecksetzenNach ist die Laufzeit, nach der ein Prozess als
	// stabil gilt und das Backoff wieder bei BackoffStart beginnt.
	BackoffZuruecksetzenNach = time.Minute

	// QuitFrist ist die Zeit, die ein Prozess nach QUIT bekommt, bevor
	// SIGTERM folgt; danach noch einmal dieselbe Frist bis SIGKILL.
	QuitFrist = 5 * time.Second

	// ZustandsTakt ist der Abstand, in dem der Zustand gemeldet wird.
	// Häufiger als der Berichtstakt, damit ein verworfener Stand folgenlos
	// bleibt.
	ZustandsTakt = time.Second

	// AnlaufFrist gilt vom Start bis zum ersten Record. Sie ist großzügiger
	// als die laufende Stillefrist, weil asamon-rx erst den Stick öffnet und
	// den Empfänger aufsetzt, bevor der init-Record hinausgeht — ein USB-Reset
	// darf dabei ein paar Sekunden brauchen, ohne dass der Prozess dafür
	// abgeräumt wird.
	AnlaufFrist = 30 * time.Second

	// StillePruefTakt ist der Abstand, in dem die Frist geprüft wird. Feiner
	// als sekundengenau muss es nicht sein: Erkannt wird ein stehender
	// Prozess, kein Jitter.
	StillePruefTakt = time.Second
)

// Konfig ist die Einstellung eines Subprozesses.
type Konfig struct {
	Channel      string
	Binary       string
	Device       string
	DeviceSerial string
	Gain         string
	IQFile       string
	LogLevel     string
	QueueSize    int
	// StilleFrist ist die Zeit ohne einen einzigen Record, nach der der
	// Prozess als steckengeblieben gilt und neu gestartet wird. 0 schaltet die
	// Überwachung ab.
	//
	// Warum das nötig ist: Bis zum 27.08.2026 hat der systemd-Watchdog diesen
	// Fall abgedeckt — asamon-rx tickte ihn aus derselben Sekundenschleife, in
	// der es auch den tlm-Record einreiht. Der Watchdog ist entfallen, weil er
	// asamon-rx an systemd band und unter Windows ohnehin nichts tat. Die
	// Erkennung wandert damit hierher, wo sie auf jeder Plattform greift.
	StilleFrist time.Duration
	// VorabArgumente stehen vor den erzeugten Optionen. Nur für Tests, damit
	// dort fake-rx mit --serve und --file betrieben werden kann.
	VorabArgumente []string
}

// Nachricht ist entweder ein Record aus dem Strom oder eine Zustandsmeldung.
// Beides geht über denselben Kanal, damit die Reihenfolge erhalten bleibt.
type Nachricht struct {
	Record  *record.Record
	Zustand *chanstate.Zustandsmeldung
}

// Prozess beaufsichtigt einen asamon-rx.
type Prozess struct {
	k   Konfig
	log *slog.Logger

	aus       chan Nachricht
	kommandos chan string

	mu            sync.Mutex
	stdin         io.WriteCloser
	rxZustand     string
	letzterFehler string
	neustarts     int
	// stilleNeustarts zählt allein die Neustarts wegen ausbleibender Records.
	// Getrennt von neustarts, weil der Unterschied diagnostisch zählt: Ein
	// Kanal, der abstürzt, hat ein anderes Leiden als einer, der einfriert.
	stilleNeustarts int
	letzterRecord   time.Time
	// ersterRecordGesehen unterscheidet Anlauf von Betrieb: bis zum ersten
	// Record gilt AnlaufFrist, danach die konfigurierte Frist.
	ersterRecordGesehen bool
	nodeVerworfen       uint64
	stromzaehler        record.Zaehler
	// unbrauchbar wird gesetzt, wenn der Strom eine Fassung hat, die dieses
	// Programm nicht deutet. Dann wird nicht neu gestartet.
	unbrauchbar bool

	// gruppe hält die Kindprozesse zusammen, damit sie den Knoten nicht
	// überleben. Unter Linux macht das systemd, unter Windows ein Job Object.
	gruppe *Gruppe
}

// Neu baut die Prozessverwaltung eines Kanals.
func Neu(k Konfig, log *slog.Logger) *Prozess {
	if k.QueueSize < 1 {
		k.QueueSize = 4096
	}
	return &Prozess{
		k:         k,
		log:       log.With("channel", k.Channel),
		aus:       make(chan Nachricht, k.QueueSize),
		kommandos: make(chan string, 64),
		rxZustand: report.RxStarting,
	}
}

// Nachrichten ist der Strom zum Kanalzustand.
func (p *Prozess) Nachrichten() <-chan Nachricht { return p.aus }

// Sende reiht ein Kommando für den Subprozess ein (REC, STOP, QUIT).
//
// Der Aufruf blockiert nie: Ein voller Kommandopuffer bedeutet, dass der
// Subprozess nicht liest — dann ist eine verworfene Aufnahme das kleinere Übel
// als eine blockierte Zustandsmaschine.
func (p *Prozess) Sende(cmd string) {
	select {
	case p.kommandos <- cmd:
	default:
		p.log.Warn("Kommando verworfen, der Subprozess liest nicht", "cmd", cmd)
	}
}

// Argumente baut den Aufruf von asamon-rx.
func (p *Prozess) Argumente() []string {
	args := append([]string{}, p.k.VorabArgumente...)
	args = append(args, "--channel", p.k.Channel)
	if p.k.Device != "" {
		args = append(args, "--device", p.k.Device)
	}
	if p.k.DeviceSerial != "" {
		// Setzt Patch 2 in asamon-rx voraus. Ohne ihn öffnet CRTL_SDR schlicht
		// das erste Gerät, das sich öffnen lässt — für mehrere Sticks also
		// nicht reproduzierbar. Die Konfigurationsprüfung erzwingt deshalb
		// höchstens einen Kanal, solange keine Seriennummern vergeben sind.
		args = append(args, "--device-serial", p.k.DeviceSerial)
	}
	if p.k.Gain != "" {
		args = append(args, "--gain", p.k.Gain)
	}
	if p.k.IQFile != "" {
		args = append(args, "--iq-file", p.k.IQFile)
	}
	if p.k.LogLevel != "" {
		args = append(args, "--log-level", p.k.LogLevel)
	}
	return args
}

// Run startet den Subprozess und hält ihn am Leben, bis ctx endet.
func (p *Prozess) Run(ctx context.Context) {
	defer close(p.aus)

	if g, err := NeueGruppe(); err != nil {
		// Ohne diese Absicherung kann ein hart abgeschossener Knoten einen
		// asamon-rx hinterlassen, der den Stick weiter offen hält. Das ist
		// schlecht — ein Kanal, der deswegen gar nicht erst startet, wäre
		// schlechter.
		p.log.Warn("Prozessgruppe ließ sich nicht anlegen; ein hart beendeter Knoten kann Kindprozesse hinterlassen",
			"fehler", err)
	} else {
		p.gruppe = g
		defer p.gruppe.Schliessen()
	}

	go p.meldeZustandRegelmaessig(ctx)

	backoff := BackoffStart
	for ctx.Err() == nil {
		p.setzeZustand(report.RxStarting, "")
		begonnen := time.Now()
		err := p.einLauf(ctx)
		laufzeit := time.Since(begonnen)

		if ctx.Err() != nil {
			p.setzeZustand(report.RxStopped, "")
			return
		}
		p.zaehleNeustart()

		grund := "der Prozess endete"
		if err != nil {
			grund = err.Error()
		}
		p.setzeZustand(report.RxFailed, p.letzteFehlerzeileOder(grund))

		if p.istUnbrauchbar() {
			p.log.Error("Kanal wird nicht neu gestartet", "grund", p.letzteFehlerzeileOder(grund))
			<-ctx.Done()
			p.setzeZustand(report.RxStopped, "")
			return
		}

		// Nach einer Minute ohne Absturz gilt der Prozess als stabil.
		if laufzeit >= BackoffZuruecksetzenNach {
			backoff = BackoffStart
		}
		p.log.Warn("asamon-rx endete, Neustart mit Backoff",
			"grund", grund,
			"laufzeit", laufzeit.Round(time.Millisecond).String(),
			"backoff", backoff.String())

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			p.setzeZustand(report.RxStopped, "")
			return
		}
		backoff = min(backoff*2, BackoffMax)
	}
	p.setzeZustand(report.RxStopped, "")
}

// einLauf startet den Prozess einmal und wartet auf sein Ende.
func (p *Prozess) einLauf(ctx context.Context) error {
	cmd := exec.Command(p.k.Binary, p.Argumente()...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s lässt sich nicht starten: %w", p.k.Binary, err)
	}
	p.log.Info("asamon-rx gestartet", "pid", cmd.Process.Pid, "binary", p.k.Binary)
	if err := p.gruppe.Aufnehmen(cmd); err != nil {
		p.log.Warn("Kindprozess ließ sich der Prozessgruppe nicht zuordnen", "fehler", err)
	}

	p.mu.Lock()
	p.stdin = stdin
	p.stromzaehler = record.Zaehler{}
	// Die Frist läuft ab dem Start, nicht ab dem ersten Record — sonst bliebe
	// ein Prozess unbemerkt, der gar nichts sagt.
	p.letzterRecord = time.Now()
	p.ersterRecordGesehen = false
	p.mu.Unlock()
	p.setzeZustand(report.RxRunning, "")
	p.meldeNeustart()

	laufCtx, laufEnde := context.WithCancel(context.Background())
	stille := make(chan time.Duration, 1)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); p.leseStdout(stdout) }()
	go func() { defer wg.Done(); p.leseStderr(stderr) }()
	go func() { defer wg.Done(); p.schreibeKommandos(laufCtx, stdin) }()
	go func() { defer wg.Done(); p.ueberwacheStille(laufCtx, stille) }()

	// Auf ctx warten und dann geordnet abbauen.
	fertig := make(chan error, 1)
	go func() { fertig <- cmd.Wait() }()

	select {
	case err = <-fertig:
	case <-ctx.Done():
		p.beendeGeordnet(cmd, stdin, fertig)
		err = nil
	case seit := <-stille:
		// Der Prozess lebt, sagt aber nichts mehr. beendeGeordnet versucht
		// zuerst QUIT — ein wirklich festgefahrener Prozess liest das nicht
		// mehr, dann greift nach QuitFrist der harte Weg.
		p.mu.Lock()
		p.stilleNeustarts++
		anzahl := p.stilleNeustarts
		p.mu.Unlock()
		p.log.Error("asamon-rx sendet keine Records mehr, Neustart",
			"stille", seit.Round(time.Second).String(),
			"stille_neustarts", anzahl)
		p.beendeGeordnet(cmd, stdin, fertig)
		err = fmt.Errorf("keine Records seit %s", seit.Round(time.Second))
	}
	laufEnde()
	stdin.Close()
	wg.Wait()

	p.mu.Lock()
	p.stdin = nil
	p.mu.Unlock()
	return err
}

// beendeGeordnet fährt den Subprozess herunter: erst QUIT, dann SIGTERM, dann
// SIGKILL. Jede Stufe bekommt QuitFrist.
func (p *Prozess) beendeGeordnet(cmd *exec.Cmd, stdin io.WriteCloser, fertig <-chan error) {
	if _, err := io.WriteString(stdin, "QUIT\n"); err != nil {
		p.log.Debug("QUIT ließ sich nicht schreiben", "fehler", err)
	}
	select {
	case <-fertig:
		return
	case <-time.After(QuitFrist):
	}

	p.log.Warn("asamon-rx reagiert nicht auf QUIT, sende SIGTERM")
	if err := signalisiere(cmd, syscall.SIGTERM); err != nil {
		p.log.Debug("SIGTERM ließ sich nicht senden", "fehler", err)
	}
	select {
	case <-fertig:
		return
	case <-time.After(QuitFrist):
	}

	p.log.Error("asamon-rx reagiert nicht auf SIGTERM, wird abgeschossen")
	_ = cmd.Process.Kill()
	<-fertig
}

// signalisiere schickt ein Signal.
//
// Unter Windows gibt es kein SIGTERM: Der Kernel kennt für einen fremden
// Prozess nur das harte Beenden. Die Leiter QUIT → SIGTERM → SIGKILL fällt
// dort also auf QUIT → Kill zusammen, und der Subprozess bekommt genau eine
// Frist statt zwei. Das ist kein Mangel dieses Programms, sondern die
// Plattform — asamon-rx räumt bei QUIT ohnehin selbst auf, und wer darauf
// nicht reagiert, hätte ein SIGTERM auch nicht beachtet.
func signalisiere(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return errors.New("kein Prozess")
	}
	if err := cmd.Process.Signal(sig); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// leseStdout ist die Lese-Goroutine. Sie hält nie an: Ist die
// Kanalwarteschlange voll, wird der Record verworfen und gezählt.
//
// Blockierte sie, liefe der Pipe-Puffer voll, asamon-rx bliebe im write()
// stehen und verlöre Samples — genau das, was der ganze Entwurf verhindern
// soll.
func (p *Prozess) leseStdout(r io.Reader) {
	leser := record.NewReader(r)
	defer func() {
		p.uebernimmZaehler(leser)
		if err := leser.Err(); err != nil {
			p.log.Warn("der Record-Strom riss ab", "fehler", err)
		}
	}()

	for rec := range leser.Alle() {
		if rec.Kind == record.KindInit && rec.Init != nil && rec.Init.FormatVersion != record.FormatVersion {
			// Ein stillschweigend falsch gedeuteter Strom ist schlimmer als
			// ein fehlender Kanal.
			p.log.Error("Kanal wird nicht betrieben: der Strom hat eine andere Fassung",
				"format_version", rec.Init.FormatVersion, "erwartet", record.FormatVersion)
			p.mu.Lock()
			p.unbrauchbar = true
			p.letzterFehler = fmt.Sprintf("format_version %d, dieses Programm deutet %d",
				rec.Init.FormatVersion, record.FormatVersion)
			p.mu.Unlock()
			p.Sende("QUIT")
			return
		}

		p.uebernimmZaehler(leser)
		p.merkeRecord()

		select {
		case p.aus <- Nachricht{Record: &rec}:
		default:
			p.mu.Lock()
			p.nodeVerworfen++
			verworfen := p.nodeVerworfen
			p.mu.Unlock()
			if verworfen%100 == 1 {
				p.log.Warn("Kanalwarteschlange voll, Record verworfen", "verworfen", verworfen)
			}
		}
	}
}

// merkeRecord hält fest, dass der Prozess eben noch gelebt hat.
//
// Gezählt wird **jeder** Record, nicht nur tlm. Der Grund steht in der
// Vorrangregel von asamon-rx: Bei vollem Ausgabepuffer wird tlm als erstes
// verworfen, ausgerechnet im Alarmfall. Eine Frist auf tlm allein würde den
// Prozess also mitten in einer Warnmeldung abräumen — dem einen Moment, in dem
// er auf keinen Fall neu starten darf.
func (p *Prozess) merkeRecord() {
	p.mu.Lock()
	p.letzterRecord = time.Now()
	p.ersterRecordGesehen = true
	p.mu.Unlock()
}

// ueberwacheStille meldet über `stille`, wenn zu lange kein Record kam.
//
// Das ist der Ersatz für den systemd-Watchdog, den asamon-rx bis zum
// 27.08.2026 aus seiner Sekundenschleife heraus bediente. Die Erkennungsgüte
// ist dieselbe — beide Lebenszeichen stammen aus derselben Schleife —, aber
// dieser Weg braucht kein systemd und wirkt deshalb auch unter Windows.
func (p *Prozess) ueberwacheStille(ctx context.Context, stille chan<- time.Duration) {
	if p.k.StilleFrist <= 0 {
		return
	}
	t := time.NewTicker(StillePruefTakt)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case jetzt := <-t.C:
			p.mu.Lock()
			seit := jetzt.Sub(p.letzterRecord)
			frist := p.k.StilleFrist
			if !p.ersterRecordGesehen && AnlaufFrist > frist {
				frist = AnlaufFrist
			}
			p.mu.Unlock()

			if seit >= frist {
				select {
				case stille <- seit:
				default:
				}
				return
			}
		}
	}
}

// StilleNeustarts gibt die Zahl der Neustarts wegen ausbleibender Records —
// für Prüfungen und das Log.
func (p *Prozess) StilleNeustarts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stilleNeustarts
}

// uebernimmZaehler holt den Stand des Zeilenlesers in den gemeinsamen Zustand.
func (p *Prozess) uebernimmZaehler(leser *record.Reader) {
	p.mu.Lock()
	p.stromzaehler = leser.Zaehler()
	p.mu.Unlock()
}

// leseStderr schreibt die Ausgaben des Subprozesses ins eigene Log. Niemals
// verwerfen — dort stehen die Parserfehler.
func (p *Prozess) leseStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8192), 256*1024)
	for sc.Scan() {
		zeile := strings.TrimSpace(sc.Text())
		if zeile == "" {
			continue
		}
		p.log.Warn("rx["+p.k.Channel+"] "+zeile, "quelle", "asamon-rx")
		p.mu.Lock()
		p.letzterFehler = zeile
		p.mu.Unlock()
	}
}

// schreibeKommandos leitet REC/STOP/QUIT an den Subprozess weiter.
func (p *Prozess) schreibeKommandos(ctx context.Context, w io.Writer) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-p.kommandos:
			if _, err := io.WriteString(w, cmd+"\n"); err != nil {
				p.log.Warn("Kommando ließ sich nicht schreiben", "cmd", cmd, "fehler", err)
				return
			}
			p.log.Debug("Kommando gesendet", "cmd", cmd)
		}
	}
}

func (p *Prozess) meldeZustandRegelmaessig(ctx context.Context) {
	t := time.NewTicker(ZustandsTakt)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.meldeZustand(false)
		}
	}
}

func (p *Prozess) meldeNeustart() { p.meldeZustand(true) }

func (p *Prozess) meldeZustand(neustart bool) {
	p.mu.Lock()
	z := chanstate.Zustandsmeldung{
		Neustart:      neustart,
		RxZustand:     p.rxZustand,
		LetzterFehler: p.letzterFehler,
		Neustarts:     p.neustarts,
		NodeVerworfen: p.nodeVerworfen,
		Stromzaehler:  p.stromzaehler,
	}
	p.mu.Unlock()

	select {
	case p.aus <- Nachricht{Zustand: &z}:
	default:
		// Der Zustand wird im Takt wiederholt; ein verworfener Stand ist
		// folgenlos.
	}
}

func (p *Prozess) setzeZustand(zustand, fehler string) {
	p.mu.Lock()
	p.rxZustand = zustand
	if fehler != "" {
		p.letzterFehler = fehler
	}
	p.mu.Unlock()
}

func (p *Prozess) zaehleNeustart() {
	p.mu.Lock()
	p.neustarts++
	p.mu.Unlock()
}

func (p *Prozess) istUnbrauchbar() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unbrauchbar
}

func (p *Prozess) letzteFehlerzeileOder(vorgabe string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.letzterFehler != "" {
		return p.letzterFehler
	}
	return vorgabe
}

// Neustarts gibt die Zahl der bisherigen Neustarts — für Prüfungen.
func (p *Prozess) Neustarts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.neustarts
}
