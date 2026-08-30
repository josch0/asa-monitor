// SPDX-License-Identifier: GPL-3.0-or-later

package rxproc

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/record"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

var (
	einmal     sync.Once
	fakeRxPfad string
	fakeRxErr  error
)

// baueFakeRx übersetzt cmd/fake-rx einmal je Testlauf. Kein Test dieses Pakets
// braucht einen SDR-Stick.
func baueFakeRx(t *testing.T) string {
	t.Helper()
	einmal.Do(func() {
		dir, err := os.MkdirTemp("", "asamon-fake-rx-*")
		if err != nil {
			fakeRxErr = err
			return
		}
		name := "fake-rx"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		fakeRxPfad = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", fakeRxPfad, "github.com/josch0/asa-monitor/asamon-node/cmd/fake-rx")
		if aus, err := cmd.CombinedOutput(); err != nil {
			fakeRxErr = err
			fakeRxPfad = string(aus)
		}
	})
	if fakeRxErr != nil {
		t.Fatalf("fake-rx ließ sich nicht bauen: %v\n%s", fakeRxErr, fakeRxPfad)
	}
	return fakeRxPfad
}

func stillesLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func stromPfad(name string) string {
	return filepath.Join("..", "..", "testdata", "streams", name+".ndjson")
}

func TestArgumente(t *testing.T) {
	p := Neu(Konfig{
		Channel: "5C", Binary: "/usr/local/bin/asamon-rx", Device: "rtl_sdr",
		DeviceSerial: "00000002", Gain: "auto", LogLevel: "info",
	}, stillesLog())
	got := strings.Join(p.Argumente(), " ")
	want := "--channel 5C --device rtl_sdr --device-serial 00000002 --gain auto --log-level info"
	if got != want {
		t.Errorf("Argumente:\n  %s\nerwartet:\n  %s", got, want)
	}

	// Ohne Seriennummer darf die Option nicht mitgehen: asamon-rx kennt sie
	// erst mit Patch 2.
	p = Neu(Konfig{Channel: "5C", Binary: "x", Device: "rawfile", IQFile: "m.iq"}, stillesLog())
	if strings.Contains(strings.Join(p.Argumente(), " "), "--device-serial") {
		t.Errorf("--device-serial wurde ohne Seriennummer mitgegeben: %v", p.Argumente())
	}
}

// Der Regelfall: Der Subprozess liefert einen Strom, und er kommt vollständig
// beim Kanalzustand an.
func TestStromKommtAn(t *testing.T) {
	bin := baueFakeRx(t)
	ctx, ende := context.WithTimeout(context.Background(), 30*time.Second)
	defer ende()

	p := Neu(Konfig{
		Channel: "5C", Binary: bin, QueueSize: 8192,
		VorabArgumente: []string{"--serve", "--file", stromPfad("alert-einfach")},
	}, stillesLog())
	go p.Run(ctx)

	var records, zustaende int
	var sahInit, sahAsa bool
	warten := time.After(20 * time.Second)
	for records < 100 {
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete zu früh")
			}
			switch {
			case n.Record != nil:
				records++
				if n.Record.Kind == record.KindInit {
					sahInit = true
				}
				if n.Record.Kind == record.KindAsa {
					sahAsa = true
				}
			case n.Zustand != nil:
				zustaende++
			}
		case <-warten:
			t.Fatalf("nur %d Records in 20 s", records)
		}
	}
	if !sahInit || !sahAsa {
		t.Errorf("init=%v asa=%v", sahInit, sahAsa)
	}
	if zustaende == 0 {
		t.Error("keine Zustandsmeldung erhalten")
	}
	ende()
	warteAufEnde(t, p)
}

// Ein Kanal in Dauerneustart ist selbst eine meldenswerte Beobachtung — und
// darf den Knoten nicht mitreißen.
func TestAbsturzFuehrtTZuNeustartMitBackoff(t *testing.T) {
	bin := baueFakeRx(t)
	ctx, ende := context.WithCancel(context.Background())
	defer ende()

	p := Neu(Konfig{
		Channel: "5C", Binary: bin,
		VorabArgumente: []string{"--serve", "--crash-after", "5", "--file", stromPfad("alert-einfach")},
	}, stillesLog())
	go p.Run(ctx)

	// Zwei Neustarts abwarten: der erste kommt sofort, der zweite nach 1 s
	// Backoff, der dritte nach 2 s.
	neustarts := 0
	warten := time.After(25 * time.Second)
	for neustarts < 3 {
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete")
			}
			if n.Zustand != nil && n.Zustand.Neustart {
				neustarts++
			}
		case <-warten:
			t.Fatalf("nur %d Neustarts in 25 s (Backoff zu langsam oder kein Neustart)", neustarts)
		}
	}
	if p.Neustarts() < 2 {
		t.Errorf("rx_restarts ist %d", p.Neustarts())
	}
	ende()
	warteAufEnde(t, p)
}

// Nach QUIT muss der Prozess in unter 20 s weg sein — auch wenn er QUIT
// beharrlich ignoriert. Dann greift SIGTERM, dann SIGKILL.
func TestHartnaeckigerProzessWirdBeendet(t *testing.T) {
	bin := baueFakeRx(t)
	ctx, ende := context.WithCancel(context.Background())

	p := Neu(Konfig{
		Channel: "5C", Binary: bin,
		VorabArgumente: []string{"--serve", "--ignore-quit", "--file", stromPfad("heartbeat-10min")},
	}, stillesLog())

	fertig := make(chan struct{})
	go func() { p.Run(ctx); close(fertig) }()

	// Warten, bis der Prozess wirklich läuft.
	warteAufRunning(t, p)

	begonnen := time.Now()
	ende()
	select {
	case <-fertig:
	case <-time.After(25 * time.Second):
		t.Fatal("der Prozess war nach 25 s nicht beendet")
	}
	dauer := time.Since(begonnen)
	if dauer > 20*time.Second {
		t.Errorf("das Herunterfahren dauerte %s, erlaubt sind 20 s", dauer.Round(time.Millisecond))
	}
	if dauer < QuitFrist {
		t.Errorf("das Herunterfahren dauerte nur %s — QUIT wurde offenbar gar nicht abgewartet", dauer)
	}
}

// Ein fehlendes Binary ist der häufigste Fehler beim Einrichten. Er darf den
// Knoten nicht beenden, sondern muss als rx_state: failed mit brauchbarer
// Meldung im Datensatz landen.
func TestFehlendesBinaryWirdGemeldet(t *testing.T) {
	ctx, ende := context.WithCancel(context.Background())
	defer ende()

	p := Neu(Konfig{Channel: "5C", Binary: filepath.Join(t.TempDir(), "gibtsnicht")}, stillesLog())
	go p.Run(ctx)

	warten := time.After(10 * time.Second)
	for {
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete")
			}
			if n.Zustand != nil && n.Zustand.RxZustand == report.RxFailed {
				if n.Zustand.LetzterFehler == "" {
					t.Error("rx_state failed ohne last_error")
				}
				ende()
				warteAufEnde(t, p)
				return
			}
		case <-warten:
			t.Fatal("kein rx_state failed in 10 s")
		}
	}
}

// Kommandos gehen an den Subprozess. fake-rx bestätigt sie auf stderr, und von
// dort landen sie in unserem Log — geprüft wird hier, dass der Schreibweg
// überhaupt steht und nichts blockiert.
func TestKommandosBlockierenNie(t *testing.T) {
	p := Neu(Konfig{Channel: "5C", Binary: "x"}, stillesLog())
	// Ohne laufenden Prozess laufen die Kommandos in den Puffer und danach ins
	// Leere. Blockieren darf keines davon.
	fertig := make(chan struct{})
	go func() {
		for range 1000 {
			p.Sende("REC 7")
		}
		close(fertig)
	}()
	select {
	case <-fertig:
	case <-time.After(5 * time.Second):
		t.Fatal("Sende() blockierte")
	}
}

func warteAufRunning(t *testing.T, p *Prozess) {
	t.Helper()
	warten := time.After(15 * time.Second)
	for {
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete")
			}
			if n.Zustand != nil && n.Zustand.RxZustand == report.RxRunning {
				return
			}
			if n.Record != nil {
				return
			}
		case <-warten:
			t.Fatal("der Prozess lief nach 15 s nicht")
		}
	}
}

// warteAufEnde leert den Nachrichtenstrom, bis er geschlossen wird. Bleibt er
// offen, hängt eine Goroutine — und das ist ein Befund.
func warteAufEnde(t *testing.T, p *Prozess) {
	t.Helper()
	warten := time.After(25 * time.Second)
	for {
		select {
		case _, ok := <-p.Nachrichten():
			if !ok {
				return
			}
		case <-warten:
			t.Fatal("der Nachrichtenstrom wurde nicht geschlossen — Run() hängt")
		}
	}
}

// Ein Prozess, der lebt, aber nichts mehr sagt, ist der Fall, für den es bis
// zum 27.08.2026 den systemd-Watchdog gab. asamon-rx tickte ihn aus derselben
// Sekundenschleife, in der es den tlm-Record einreiht; seit der Watchdog
// entfallen ist, misst asamon-node die Stille im Record-Strom selbst.
//
// fake-rx stellt den Fall mit --go-silent nach; --ignore-quit macht daraus
// einen wirklich festgefahrenen Prozess, den erst der harte Weg beendet.
func TestSteckengebliebenerProzessWirdNeuGestartet(t *testing.T) {
	bin := baueFakeRx(t)
	ctx, ende := context.WithCancel(context.Background())
	defer ende()

	p := Neu(Konfig{
		Channel: "5C", Binary: bin,
		StilleFrist:    2 * time.Second,
		VorabArgumente: []string{"--serve", "--go-silent", "3", "--ignore-quit", "--file", stromPfad("heartbeat-10min")},
	}, stillesLog())
	go p.Run(ctx)

	// Ein Zyklus ist: 2 s Stille erkennen, dann QUIT (wird ignoriert), dann
	// nach QuitFrist der harte Weg. Zwei Neustarts sind der Beleg, dass der
	// Weg wiederholbar ist und nicht bloß einmal zufällig griff.
	neustarts := 0
	warten := time.After(60 * time.Second)
	for neustarts < 2 {
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete")
			}
			if n.Zustand != nil && n.Zustand.Neustart {
				neustarts++
			}
		case <-warten:
			t.Fatalf("nur %d Neustarts in 60 s; StilleNeustarts=%d", neustarts, p.StilleNeustarts())
		}
	}
	if p.StilleNeustarts() < 1 {
		t.Errorf("StilleNeustarts ist %d, erwartet mindestens 1", p.StilleNeustarts())
	}
	ende()
	warteAufEnde(t, p)
}

// Der wichtigere Test von beiden: **kein Fehlalarm.** Ein Prozess, der
// regelmäßig Records schickt, darf nicht neu gestartet werden — ein Neustart
// mitten in einer Warnmeldung wäre schlimmer als eine spät erkannte Störung.
func TestLaufenderProzessWirdNichtNeuGestartet(t *testing.T) {
	bin := baueFakeRx(t)
	ctx, ende := context.WithCancel(context.Background())
	defer ende()

	p := Neu(Konfig{
		Channel: "5C", Binary: bin,
		StilleFrist: 3 * time.Second,
		// In Echtzeit abgespielt kommt etwa ein Record je Sekunde — der
		// Ruhezustand eines echten Kanals.
		VorabArgumente: []string{"--serve", "--speed", "1", "--file", stromPfad("heartbeat-10min")},
	}, stillesLog())
	go p.Run(ctx)

	// Deutlich länger als die Frist mitlaufen.
	beobachtung := time.After(10 * time.Second)
	records := 0
	for {
		fertig := false
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete")
			}
			if n.Record != nil {
				records++
			}
			if n.Zustand != nil && n.Zustand.Neustart && records > 0 {
				t.Fatal("laufender Prozess wurde neu gestartet")
			}
		case <-beobachtung:
			fertig = true
		}
		if fertig {
			break
		}
	}
	if records < 3 {
		t.Fatalf("nur %d Records in 10 s — der Strom floss nicht", records)
	}
	if p.StilleNeustarts() != 0 {
		t.Errorf("StilleNeustarts ist %d, erwartet 0", p.StilleNeustarts())
	}
	ende()
	warteAufEnde(t, p)
}

// StilleFrist 0 schaltet die Überwachung ab. Das ist der Replay-Fall: Eine
// Aufzeichnung ist endlich, und ihr Ende ist kein Hänger.
func TestStilleUeberwachungAbschaltbar(t *testing.T) {
	bin := baueFakeRx(t)
	ctx, ende := context.WithCancel(context.Background())
	defer ende()

	p := Neu(Konfig{
		Channel: "5C", Binary: bin,
		StilleFrist:    0,
		VorabArgumente: []string{"--serve", "--go-silent", "3", "--file", stromPfad("heartbeat-10min")},
	}, stillesLog())
	go p.Run(ctx)

	beobachtung := time.After(6 * time.Second)
	for {
		fertig := false
		select {
		case n, ok := <-p.Nachrichten():
			if !ok {
				t.Fatal("der Nachrichtenstrom endete")
			}
			_ = n
		case <-beobachtung:
			fertig = true
		}
		if fertig {
			break
		}
	}
	if p.StilleNeustarts() != 0 {
		t.Errorf("StilleNeustarts ist %d, obwohl die Überwachung aus ist", p.StilleNeustarts())
	}
	ende()
	warteAufEnde(t, p)
}
