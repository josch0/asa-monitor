// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pruefumgebung legt rx_binary und state_dir an und gibt die YAML-Zeilen
// zurück, die auf sie zeigen. Ohne sie schlüge jede Prüfung schon an den
// Pfaden fehl.
func pruefumgebung(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rx := filepath.Join(dir, "asamon-rx")
	if err := os.WriteFile(rx, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return "paths:\n  rx_binary: " + quote(rx) + "\n  state_dir: " + quote(filepath.Join(dir, "state")) + "\n"
}

// quote macht aus einem Windows-Pfad einen YAML-Text, in dem die Rückstriche
// nicht als Escape gelesen werden.
func quote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

// minimal enthält nur die Pflichtangaben — an ihm werden die Vorgaben geprüft.
const minimal = `
node:
  name: "Berlin-Mitte-01"
  location_code: "2366-7443-8484"
server:
  url: "https://asa.example.org"
channels:
  - channel: "5C"
`

// vollstaendig nennt jeden Abschnitt genau einmal, damit die Fehlerfälle über
// strings.Replace gebaut werden können, ohne einen Schlüssel zu verdoppeln.
const vollstaendig = `
node:
  name: "Berlin-Mitte-01"
  location_code: "2366-7443-8484"
server:
  url: "https://asa.example.org"
  report_interval: "10s"
  timeout: "15s"
channels:
  - channel: "5C"
audio:
  enabled: true
  post_roll: "10s"
  max_seconds: 300
  keep_days: 7
limits:
  max_spool_mb: 512
  queue_size: 4096
  max_reports_per_request: 60
log:
  level: "info"
`

func TestBeispielkonfigurationIstGueltig(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contrib", "node-config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Das Beispiel lässt paths bewusst offen, damit ein ausgepacktes
	// Release-Archiv auf jeder Plattform ohne Änderung läuft. Für die Prüfung
	// kommt ein Abschnitt dazu, der auf ein temporäres Verzeichnis zeigt —
	// sonst hinge der Test daran, ob auf diesem Rechner zufällig ein
	// asamon-rx installiert ist.
	if strings.Contains(string(raw), "\npaths:") {
		t.Error("das Beispiel nennt paths wieder ausdrücklich; dann läuft es nicht mehr auf beiden Plattformen")
	}
	text := string(raw) + "\n" + pruefumgebung(t)

	cfg, warnungen, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("contrib/node-config.example.yaml wird nicht angenommen: %v", err)
	}
	if len(warnungen) != 0 {
		t.Errorf("das Beispiel erzeugt Warnungen: %v", warnungen)
	}
	if cfg.Node.Name != "Berlin-Mitte-01" {
		t.Errorf("node.name ist %q", cfg.Node.Name)
	}
	if cfg.Location.String() != "Z10:B736BB" {
		t.Errorf("der Standort wurde als %s dekodiert", cfg.Location)
	}
	if cfg.Server.ReportInterval.String() != "10s" {
		t.Errorf("report_interval ist %s", cfg.Server.ReportInterval)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].Channel != "5C" {
		t.Errorf("channels: %+v", cfg.Channels)
	}
}

func TestVorgabenGreifen(t *testing.T) {
	cfg, _, err := Parse([]byte(minimal + pruefumgebung(t)))
	if err != nil {
		t.Fatalf("minimale Konfiguration abgelehnt: %v", err)
	}
	if cfg.Server.ReportInterval.String() != "10s" || cfg.Server.Timeout.String() != "15s" {
		t.Errorf("Server-Vorgaben fehlen: %+v", cfg.Server)
	}
	if cfg.Limits.QueueSize != 4096 || cfg.Limits.MaxSpoolMB != 512 || cfg.Limits.MaxReportsPerReques != 60 {
		t.Errorf("Limit-Vorgaben fehlen: %+v", cfg.Limits)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("log.level ist %q", cfg.Log.Level)
	}
	if cfg.Channels[0].Device != "rtl_sdr" || cfg.Channels[0].Gain != "auto" {
		t.Errorf("Kanal-Vorgaben fehlen: %+v", cfg.Channels[0])
	}
	if !cfg.Audio.Enabled || cfg.Audio.MaxSeconds != 300 || cfg.Audio.KeepDays != 7 {
		t.Errorf("Audio-Vorgaben fehlen: %+v", cfg.Audio)
	}

	// audio.enabled ist eine Vorgabe, die ein ausdrückliches false überschreiben
	// können muss — sonst ließe sich der Mitschnitt nie abstellen.
	cfg, _, err = Parse([]byte(minimal + "audio:\n  enabled: false\n" + pruefumgebung(t)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audio.Enabled {
		t.Error("audio.enabled: false wurde von der Vorgabe überschrieben")
	}
}

func TestUnbekannterSchluesselIstFehler(t *testing.T) {
	kaputt := strings.Replace(vollstaendig, "report_interval:", "report_intervall:", 1)
	_, _, err := Parse([]byte(kaputt + pruefumgebung(t)))
	if err == nil {
		t.Fatal("der vertippte Schlüssel report_intervall wurde angenommen")
	}
	if !strings.Contains(err.Error(), "docs/node-config.md") {
		t.Errorf("die Meldung hilft nicht weiter: %v", err)
	}
}

func TestJederEingebauteFehlerWirdGemeldet(t *testing.T) {
	umgebung := pruefumgebung(t)
	ersetze := func(alt, neu string) string { return strings.Replace(vollstaendig, alt, neu, 1) }

	cases := []struct {
		name    string
		yaml    string
		enthalt string
	}{
		{"Name fehlt", ersetze(`name: "Berlin-Mitte-01"`, `name: ""`), "node.name"},
		{"Name zu lang", ersetze("Berlin-Mitte-01", strings.Repeat("x", 21)), "node.name"},
		{"Steuerzeichen im Namen", ersetze(`"Berlin-Mitte-01"`, `"Berlin\a01"`), "Steuerzeichen"},
		{"Standort fehlt", ersetze(`location_code: "2366-7443-8484"`, `location_code: ""`), "node.location_code"},
		{"Standort mit falscher Prüfsumme", ersetze("2366-7443-8484", "2366-7443-8485"), "Prüfsumme"},
		{"Standort mit falschem Symbol", ersetze("2366-7443-8484", "2366-7443-8489"), "ungültige Zeichen"},
		{"Standort zu kurz", ersetze("2366-7443-8484", "2366-7443-848"), "Symbole"},
		{"URL fehlt", ersetze(`url: "https://asa.example.org"`, `url: ""`), "server.url"},
		{"URL mit fremdem Schema", ersetze("https://asa.example.org", "ftp://asa.example.org"), "Schema"},
		{"URL ohne Rechnernamen", ersetze("https://asa.example.org", "https://"), "Rechnernamen"},
		{"kein Kanal", ersetze("channels:\n  - channel: \"5C\"\n", "channels: []\n"), "channels ist leer"},
		{"Kanalname ungültig", ersetze(`channel: "5C"`, `channel: "42Z"`), "kein DAB-Kanalname"},
		{"Kanal doppelt", ersetze("  - channel: \"5C\"\n", "  - channel: \"5C\"\n  - channel: \"5C\"\n"), "nur einmal"},
		{"Gerät unbekannt", ersetze(`channel: "5C"`, "channel: \"5C\"\n    device: \"usrp\""), "unbekannt"},
		{"rawfile ohne iq_file", ersetze(`channel: "5C"`, "channel: \"5C\"\n    device: \"rawfile\""), "iq_file fehlt"},
		{"iq_file ohne rawfile", ersetze(`channel: "5C"`, "channel: \"5C\"\n    iq_file: \"x.iq\""), "gilt nur für rawfile"},
		{"Dauerangabe kaputt", ersetze(`report_interval: "10s"`, `report_interval: "zehn Sekunden"`), "Dauerangabe"},
		{"Dauerangabe als Zahl", ersetze(`report_interval: "10s"`, `report_interval: 10`), "Dauerangabe"},
		{"report_interval zu klein", ersetze(`report_interval: "10s"`, `report_interval: "200ms"`), "report_interval"},
		{"report_interval zu groß", ersetze(`report_interval: "10s"`, `report_interval: "10m"`), "report_interval"},
		{"Timeout null", ersetze(`timeout: "15s"`, `timeout: "0s"`), "server.timeout"},
		{"Log-Stufe unbekannt", ersetze(`level: "info"`, `level: "trace"`), "log.level"},
		{"queue_size zu klein", ersetze("queue_size: 4096", "queue_size: 8"), "queue_size"},
		{"max_spool_mb null", ersetze("max_spool_mb: 512", "max_spool_mb: 0"), "max_spool_mb"},
		{"max_reports_per_request null", ersetze("max_reports_per_request: 60", "max_reports_per_request: 0"), "max_reports_per_request"},
		{"max_seconds null bei aktivem Audio", ersetze("max_seconds: 300", "max_seconds: 0"), "max_seconds"},
		{"keep_days negativ", ersetze("keep_days: 7", "keep_days: -1"), "keep_days"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Parse([]byte(c.yaml + umgebung))
			if err == nil {
				t.Fatalf("wurde angenommen, erwartet wurde ein Fehler über %q", c.enthalt)
			}
			if !strings.Contains(err.Error(), c.enthalt) {
				t.Errorf("die Meldung nennt %q nicht: %v", c.enthalt, err)
			}
		})
	}
}

func TestPfadeWerdenGeprueft(t *testing.T) {
	dir := t.TempDir()
	fehlt := "paths:\n  rx_binary: " + quote(filepath.Join(dir, "gibtsnicht")) + "\n  state_dir: " + quote(dir) + "\n"
	if _, _, err := Parse([]byte(minimal + fehlt)); err == nil {
		t.Error("ein fehlendes rx_binary wurde angenommen")
	}

	// Ein Verzeichnis als rx_binary ist der Fehler, den man beim Abtippen macht.
	alsVerzeichnis := "paths:\n  rx_binary: " + quote(dir) + "\n  state_dir: " + quote(dir) + "\n"
	if _, _, err := Parse([]byte(minimal + alsVerzeichnis)); err == nil {
		t.Error("ein Verzeichnis als rx_binary wurde angenommen")
	}

	// state_dir wird angelegt, wenn es fehlt — der Freiwillige soll nicht
	// erst mkdir tippen müssen.
	neu := filepath.Join(dir, "tief", "state")
	rx := filepath.Join(dir, "asamon-rx")
	if err := os.WriteFile(rx, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok := "paths:\n  rx_binary: " + quote(rx) + "\n  state_dir: " + quote(neu) + "\n"
	if _, _, err := Parse([]byte(minimal + ok)); err != nil {
		t.Errorf("state_dir wurde nicht angelegt: %v", err)
	}
	if _, err := os.Stat(neu); err != nil {
		t.Errorf("state_dir fehlt nach der Prüfung: %v", err)
	}
}

func TestZweiKanaeleBrauchenSeriennummern(t *testing.T) {
	umgebung := pruefumgebung(t)
	zwei := `
node:
  name: "Zwei-Sticks"
  location_code: "2366-7443-8484"
server:
  url: "https://asa.example.org"
channels:
  - channel: "5C"
  - channel: "11D"
`
	_, _, err := Parse([]byte(zwei + umgebung))
	if err == nil {
		t.Fatal("zwei Kanäle ohne device_serial wurden angenommen")
	}
	if !strings.Contains(err.Error(), "Patch 2") {
		t.Errorf("die Meldung erklärt den Grund nicht: %v", err)
	}

	mitSerien := strings.Replace(zwei, `- channel: "5C"`, "- channel: \"5C\"\n    device_serial: \"00000001\"", 1)
	mitSerien = strings.Replace(mitSerien, `- channel: "11D"`, "- channel: \"11D\"\n    device_serial: \"00000002\"", 1)
	if _, _, err := Parse([]byte(mitSerien + umgebung)); err != nil {
		t.Errorf("zwei Kanäle mit verschiedenen Seriennummern wurden abgelehnt: %v", err)
	}

	gleich := strings.Replace(mitSerien, "00000002", "00000001", 1)
	if _, _, err := Parse([]byte(gleich + umgebung)); err == nil {
		t.Error("zwei Kanäle mit derselben Seriennummer wurden angenommen")
	}
}

func TestWarnungenHaltenNichtAuf(t *testing.T) {
	umgebung := pruefumgebung(t)
	cfg, warnungen, err := Parse([]byte(strings.Replace(vollstaendig, "https://", "http://", 1) + umgebung))
	if err != nil {
		t.Fatalf("http:// hat den Start verhindert: %v", err)
	}
	if len(warnungen) == 0 {
		t.Error("http:// hat keine Warnung erzeugt")
	}
	if cfg.Server.URL != "http://asa.example.org" {
		t.Errorf("server.url ist %q", cfg.Server.URL)
	}

	_, warnungen, err = Parse([]byte(strings.Replace(vollstaendig, "channels:", "  insecure_skip_verify: true\nchannels:", 1) + umgebung))
	if err != nil {
		t.Fatalf("insecure_skip_verify hat den Start verhindert: %v", err)
	}
	if len(warnungen) == 0 {
		t.Error("insecure_skip_verify hat keine Warnung erzeugt")
	}
}

func TestMehrereDokumenteWerdenAbgelehnt(t *testing.T) {
	umgebung := pruefumgebung(t)
	if _, _, err := Parse([]byte(minimal + umgebung + "\n---\nnode:\n  name: \"zweiter\"\n")); err == nil {
		t.Error("zwei YAML-Dokumente in einer Datei wurden angenommen")
	}
	if _, _, err := Parse([]byte("")); err == nil {
		t.Error("eine leere Datei wurde angenommen")
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node-config.yaml")
	if err := os.WriteFile(p, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Find(p); err != nil || got != p {
		t.Errorf("Find(%q) = %q, %v", p, got, err)
	}
	if _, err := Find(filepath.Join(dir, "gibtsnicht.yaml")); err == nil {
		t.Error("ein angegebener, nicht vorhandener Pfad wurde angenommen")
	}
}

// Ein ausdrücklich gesetzter rx_binary-Pfad wird nicht ersetzt.
//
// Unter Windows sucht die Prüfung den Empfangsprozess neben der eigenen Binary,
// damit ein ausgepacktes Release-Archiv ohne Installation läuft. Das darf aber
// nur greifen, solange die Konfiguration schweigt: Ein Tippfehler im Pfad muss
// ein Fehler bleiben und darf nicht dadurch verschwinden, dass sich das
// Programm stillschweigend etwas anderes sucht.
func TestGesetzterRxPfadWirdNichtErsetzt(t *testing.T) {
	dir := t.TempDir()
	falsch := filepath.Join(dir, "gibt-es-nicht", "asamon-rx")
	umgebung := "paths:\n  rx_binary: " + quote(falsch) + "\n  state_dir: " + quote(dir) + "\n"

	_, _, err := Parse([]byte(minimal + umgebung))
	if err == nil {
		t.Fatal("ein falsch gesetzter rx_binary-Pfad wurde angenommen")
	}
	if !strings.Contains(err.Error(), "rx_binary") {
		t.Errorf("die Meldung nennt das Feld nicht: %v", err)
	}
}
