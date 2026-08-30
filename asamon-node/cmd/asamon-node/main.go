// SPDX-License-Identifier: GPL-3.0-or-later

// asamon-node ist der Knotenprozess des ASA-Monitors.
//
// Er startet je überwachtem DAB-Kanal einen asamon-rx-Subprozess, liest deren
// NDJSON-Record-Ströme, deutet sie — Alert-Sets, Phasenverläufe,
// Heartbeat-Lücken, Warngebiete — und schickt das Ergebnis alle zehn Sekunden
// als einen Datensatz an den Server.
//
// Ein Knoten, eine Konfigurationsdatei, ein Log, eine systemd-Unit — gleich wie
// viele Sticks stecken.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/josch0/asa-monitor/asamon-node/internal/buildinfo"
	"github.com/josch0/asa-monitor/asamon-node/internal/config"
	"github.com/josch0/asa-monitor/asamon-node/internal/identity"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
	"github.com/josch0/asa-monitor/asamon-node/internal/supervisor"
)

func main() {
	os.Exit(lauf())
}

func lauf() int {
	var (
		konfigPfad  = flag.String("config", "", "node-config.yaml (Vorgabe: ./node-config.yaml, /etc/asamon/node-config.yaml)")
		pruefen     = flag.Bool("check", false, "Konfiguration prüfen, Standort ausgeben, beenden")
		trocken     = flag.Bool("dry-run", false, "alles außer Uplink; Datensätze als NDJSON nach stdout")
		replay      = flag.String("replay", "", "Record-Strom aus Datei statt asamon-rx (auch Verzeichnis: je Kanal eine Datei)")
		replaySpeed = flag.Float64("replay-speed", 1.0, "1.0 = Echtzeit, 0 = so schnell wie möglich")
		einmal      = flag.Bool("once", false, "einen Datensatz bauen, senden, beenden")
		logStufe    = flag.String("log-level", "", "error|warn|info|debug (überschreibt log.level)")
		version     = flag.Bool("version", false, "Version ausgeben und beenden")
	)
	flag.Usage = nutzung
	flag.Parse()

	if *version {
		fmt.Println(buildinfo.String())
		return 0
	}

	pfad, err := config.Find(*konfigPfad)
	if err != nil {
		fmt.Fprintln(os.Stderr, "asamon-node:", err)
		return 1
	}
	cfg, warnungen, err := config.Load(pfad)
	if err != nil {
		fmt.Fprintln(os.Stderr, "asamon-node:", err)
		return 1
	}
	if *logStufe != "" {
		cfg.Log.Level = *logStufe
	}

	if *pruefen {
		return pruefeUndZeige(cfg, warnungen)
	}

	log := baueLogger(cfg.Log.Level, *trocken)
	for _, w := range warnungen {
		log.Warn(string(w))
	}
	log.Info("asamon-node startet",
		"version", buildinfo.Version, "commit", buildinfo.Commit,
		"platform", buildinfo.Platform(), "config", cfg.Path,
		"kanaele", len(cfg.Channels))

	id, err := identity.Load(cfg.Paths.StateDir)
	if err != nil {
		log.Error("Knotenidentität", "fehler", err)
		return 1
	}
	log.Info("Knotenidentität", "node_id", id.NodeID, "name", cfg.Node.Name,
		"standort", cfg.Location.String())
	if hinweis := identity.RechteHinweis(); hinweis != "" {
		log.Warn(hinweis, "state_dir", cfg.Paths.StateDir)
	}

	opt := supervisor.Optionen{
		DryRun:      *trocken,
		Einmal:      *einmal,
		ReplayPfad:  *replay,
		ReplaySpeed: *replaySpeed,
		Ausgabe:     ndjsonNachStdout,
	}
	if *replay != "" && cfg.Paths.RxBinary == "" {
		log.Error("--replay braucht trotzdem ein rx_binary — dort steht fake-rx")
		return 1
	}

	sup, err := supervisor.Neu(cfg, id, log, opt)
	if err != nil {
		log.Error("Knoten lässt sich nicht aufbauen", "fehler", err)
		return 1
	}
	sup.FertigMelder = func() {
		supervisor.NotifyBereit()
		supervisor.NotifyStatus(fmt.Sprintf("%d Kanäle, node_id %s", len(cfg.Channels), id.NodeID))
	}
	sup.Lebenszeichen = supervisor.NotifyLebenszeichen

	ctx, ende := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer ende()

	if err := sup.Run(ctx); err != nil {
		log.Error("Knoten endete mit Fehler", "fehler", err)
		supervisor.NotifyBeendet()
		return 1
	}
	supervisor.NotifyBeendet()
	log.Info("asamon-node beendet")
	return 0
}

// pruefeUndZeige ist die Abnahme des Gerüsts: Es validiert alles und gibt den
// dekodierten Standort als Rechteck samt Mittelpunkt aus.
func pruefeUndZeige(cfg *config.Config, warnungen []config.Warnung) int {
	fmt.Printf("Konfiguration: %s\n", cfg.Path)
	fmt.Printf("Knoten:        %s\n", cfg.Node.Name)
	fmt.Printf("Standort:      %s  (%s)\n", cfg.Node.LocationCode, cfg.Location.String())

	r, err := cfg.Location.Rect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Standort lässt sich nicht in Geometrie umsetzen: %v\n", err)
		return 1
	}
	lat, lon, _ := cfg.Location.Center()
	fmt.Printf("  Rechteck:    %.6f .. %.6f Breite, %.6f .. %.6f Länge (WGS84)\n",
		r.LatMin, r.LatMax, r.LonMin, r.LonMax)
	fmt.Printf("  Mittelpunkt: %.6f, %.6f\n", lat, lon)
	fmt.Printf("  Kantenlänge: rund %.0f m in Nord-Süd-Richtung\n", (r.LatMax-r.LatMin)*111320)
	fmt.Printf("  URI:         %s\n", cfg.Location.URI())

	fmt.Printf("Server:        %s (Takt %s, Timeout %s)\n",
		cfg.Server.URL, cfg.Server.ReportInterval, cfg.Server.Timeout)
	fmt.Printf("Kanäle:        %d\n", len(cfg.Channels))
	for i, ch := range cfg.Channels {
		serie := ch.DeviceSerial
		if serie == "" {
			serie = "(erstes Gerät)"
		}
		fmt.Printf("  %d. %-4s über %s, Stick %s, Gain %s\n", i+1, ch.Channel, ch.Device, serie, ch.Gain)
	}
	fmt.Printf("Audio:         %v (Nachlauf %s, höchstens %d s, %d Tage aufbewahren)\n",
		cfg.Audio.Enabled, cfg.Audio.PostRoll, cfg.Audio.MaxSeconds, cfg.Audio.KeepDays)
	fmt.Printf("Pfade:         %s, Zustand in %s\n", cfg.Paths.RxBinary, cfg.Paths.StateDir)

	for _, w := range warnungen {
		fmt.Fprintf(os.Stderr, "Warnung: %s\n", w)
	}
	fmt.Println("\nDie Konfiguration ist gültig.")
	return 0
}

func ndjsonNachStdout(r *report.Report) {
	raw, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "asamon-node: Datensatz ließ sich nicht serialisieren:", err)
		return
	}
	os.Stdout.Write(append(raw, '\n'))
}

// baueLogger baut das strukturierte Log.
//
// Nach stdout, damit journald es entgegennimmt. Textformat bei debug — dort
// liest ein Mensch mit —, JSON sonst.
//
// Bei --dry-run geht es nach **stderr**: Dort gehört stdout den Datensätzen,
// genau wie bei asamon-rx die Records stdout gehören und die Logs stderr. Ein
// Log, das sich unter die Nutzdaten mischt, macht beide unbrauchbar.
func baueLogger(stufe string, trocken bool) *slog.Logger {
	var level slog.Level
	switch stufe {
	case "error":
		level = slog.LevelError
	case "warn":
		level = slog.LevelWarn
	case "debug":
		level = slog.LevelDebug
	default:
		level = slog.LevelInfo
	}
	ziel := os.Stdout
	if trocken {
		ziel = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: level}
	if level == slog.LevelDebug {
		return slog.New(slog.NewTextHandler(ziel, opts))
	}
	return slog.New(slog.NewJSONHandler(ziel, opts))
}

func nutzung() {
	fmt.Fprintf(os.Stderr, `asamon-node — Knotenprozess des ASA-Monitors

Aufruf:
  asamon-node [Optionen]

Optionen:
  --config <pfad>     node-config.yaml (Vorgabe: %s)
  --check             Konfiguration prüfen, Standort ausgeben, beenden
  --dry-run           alles außer Uplink; Datensätze als NDJSON nach stdout
  --replay <pfad>     Record-Strom aus Datei statt asamon-rx
                      (auch Verzeichnis: je Kanal eine Datei <kanal>.ndjson)
  --replay-speed <f>  1.0 = Echtzeit, 0 = so schnell wie möglich
  --once              einen Datensatz bauen, senden, beenden
  --log-level <stufe> error|warn|info|debug
  --version

Beispiele:
  asamon-node --check
  asamon-node --replay testdata/streams/alert-einfach.ndjson --replay-speed 0 --dry-run

Vollständige Beschreibung: README.md, docs/node-config.md, docs/uplink-protokoll.md
`, strings.Join(config.DefaultPaths, ", "))
}

func joinKomma(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
