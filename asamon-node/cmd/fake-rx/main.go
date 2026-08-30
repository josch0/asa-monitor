// SPDX-License-Identifier: GPL-3.0-or-later

// fake-rx spielt asamon-rx nach: Es schreibt einen NDJSON-Strom nach stdout,
// versteht REC/STOP/QUIT auf stdin und kann auf Kommando abstürzen.
//
// Zwei Aufgaben in einem Programm:
//
//   - Testhilfe für rxproc und den Supervisor: Ein Kanal, der wiederholt
//     abstürzt, darf die anderen nicht mitreißen — das lässt sich ohne
//     SDR-Stick nur so prüfen.
//   - Erzeuger der synthetischen Ströme in testdata/streams. Echten
//     ASA-Verkehr hat niemand aufgezeichnet; die Ströme müssen also gebaut
//     werden, und zwar nachvollziehbar aus dem Repo heraus.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		szenarioName  = flag.String("scenario", "", "Szenario nach stdout schreiben")
		datei         = flag.String("file", "", "NDJSON-Datei als Strom abspielen")
		liste         = flag.Bool("list", false, "Szenarien auflisten")
		speed         = flag.Float64("speed", 0, "1.0 = Echtzeit, 0 = so schnell wie möglich")
		crashNach     = flag.Int("crash-after", 0, "nach n Records mit Rückgabewert 1 enden (0 = nie)")
		stillNach     = flag.Int("go-silent", 0, "nach n Records verstummen, aber weiterlaufen (0 = nie)")
		ignoriereQuit = flag.Bool("ignore-quit", false, "QUIT nicht beachten — prüft den SIGTERM-Pfad")
		serve         = flag.Bool("serve", false, "als asamon-rx auftreten: Kommandos lesen und nach dem Strom auf QUIT warten")
		// Die Optionen von asamon-rx werden angenommen und ignoriert, damit
		// rxproc denselben Aufruf bauen kann wie im Betrieb.
		_ = flag.String("channel", "", "DAB-Kanal (wird angenommen und ignoriert)")
		_ = flag.String("device", "", "Gerät (wird angenommen und ignoriert)")
		_ = flag.String("device-serial", "", "Seriennummer (wird angenommen und ignoriert)")
		_ = flag.String("gain", "", "Verstärkung (wird angenommen und ignoriert)")
		_ = flag.String("iq-file", "", "IQ-Datei (wird angenommen und ignoriert)")
		_ = flag.String("log-level", "", "Logstufe (wird angenommen und ignoriert)")
	)
	flag.Parse()

	if *liste {
		for _, s := range szenarien {
			fmt.Printf("%-20s %s\n", s.name, s.beschreibung)
		}
		return
	}

	zeilen, err := hoteZeilen(*szenarioName, *datei)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-rx:", err)
		os.Exit(1)
	}

	// Ohne --serve ist fake-rx nur der Erzeuger: ausgeben und fertig. Erst
	// --serve macht daraus einen Gesprächspartner, der auf QUIT wartet — sonst
	// bliebe jedes `fake-rx --scenario x > datei` hängen.
	if !*serve {
		schreibeAlles(zeilen)
		return
	}

	beende := make(chan struct{})
	go leseKommandos(beende, *ignoriereQuit)
	spiele(zeilen, *speed, *crashNach, *stillNach, beende)
}

func hoteZeilen(szenarioName, datei string) ([]string, error) {
	switch {
	case szenarioName != "" && datei != "":
		return nil, fmt.Errorf("--scenario und --file schließen einander aus")
	case szenarioName != "":
		return baueSzenario(szenarioName)
	case datei != "":
		raw, err := os.ReadFile(datei)
		if err != nil {
			return nil, err
		}
		var out []string
		for z := range strings.SplitSeq(string(raw), "\n") {
			if z = strings.TrimRight(z, "\r"); z != "" {
				out = append(out, z)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("weder --scenario noch --file angegeben (--list zeigt die Szenarien)")
	}
}

func schreibeAlles(zeilen []string) {
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, z := range zeilen {
		w.WriteString(z)
		w.WriteByte('\n')
	}
}

// spiele gibt den Strom aus — wahlweise in Echtzeit nach den Zeitstempeln.
//
// --go-silent stellt den Fall nach, für den asamon-node eine Stillefrist hat:
// ein Prozess, der lebt, aber nichts mehr sagt. Zusammen mit --ignore-quit
// wird daraus ein wirklich festgefahrener Prozess, den nur der harte Weg
// beendet — genau der Fall, den früher der systemd-Watchdog abgedeckt hat.
func spiele(zeilen []string, speed float64, crashNach, stillNach int, beende <-chan struct{}) {
	w := bufio.NewWriter(os.Stdout)
	begonnen := time.Now()
	var erstesTs time.Time

	for i, z := range zeilen {
		select {
		case <-beende:
			w.Flush()
			fmt.Fprintln(os.Stderr, "fake-rx: QUIT empfangen, Ende")
			return
		default:
		}

		if speed > 0 {
			if ts, ok := zeitAus(z); ok {
				if erstesTs.IsZero() {
					erstesTs = ts
				}
				soll := time.Duration(float64(ts.Sub(erstesTs)) / speed)
				if warten := soll - time.Since(begonnen); warten > 0 {
					select {
					case <-time.After(warten):
					case <-beende:
						w.Flush()
						return
					}
				}
			}
		}

		w.WriteString(z)
		w.WriteByte('\n')
		w.Flush()

		if crashNach > 0 && i+1 >= crashNach {
			fmt.Fprintf(os.Stderr, "fake-rx: Absturz nach %d Records (--crash-after)\n", crashNach)
			os.Exit(1)
		}
		if stillNach > 0 && i+1 >= stillNach {
			fmt.Fprintf(os.Stderr, "fake-rx: verstummt nach %d Records (--go-silent)\n", stillNach)
			<-beende
			return
		}
	}
	w.Flush()

	// Der Strom ist zu Ende. Ein echter asamon-rx liefe weiter; damit sich
	// Neustart und Backoff prüfen lassen, wird hier auf QUIT gewartet.
	<-beende
}

// zeitAus liest das ts-Feld einer Zeile, ohne sie ganz zu parsen.
func zeitAus(zeile string) (time.Time, bool) {
	const marke = `"ts":"`
	i := strings.Index(zeile, marke)
	if i < 0 {
		return time.Time{}, false
	}
	rest := zeile[i+len(marke):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, rest[:j])
	return t, err == nil
}

// leseKommandos beantwortet die Zeilenkommandos auf stdin — dieselben, die
// asamon-rx versteht.
func leseKommandos(beende chan struct{}, ignoriereQuit bool) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		zeile := strings.TrimSpace(sc.Text())
		switch {
		case zeile == "":
		case zeile == "QUIT":
			fmt.Fprintln(os.Stderr, "fake-rx: QUIT")
			if ignoriereQuit {
				fmt.Fprintln(os.Stderr, "fake-rx: QUIT wird auf Wunsch nicht beachtet")
				continue
			}
			close(beende)
			return
		case strings.HasPrefix(zeile, "REC "), strings.HasPrefix(zeile, "STOP "):
			fmt.Fprintln(os.Stderr, "fake-rx:", zeile)
		default:
			fmt.Fprintf(os.Stderr, "fake-rx: unbekanntes Kommando %q\n", zeile)
		}
	}
	// stdin geschlossen: Die Gegenstelle ist weg.
	select {
	case <-beende:
	default:
		close(beende)
	}
}
