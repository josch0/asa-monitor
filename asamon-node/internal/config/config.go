// SPDX-License-Identifier: GPL-3.0-or-later

// Paket config lädt und prüft node-config.yaml.
//
// Eine Datei, vollständig, ohne Umgebungsvariablen-Magie. Suchreihenfolge:
// --config <pfad>, sonst ./node-config.yaml, sonst /etc/asamon/node-config.yaml.
//
// Geprüft wird beim Start *und* bei --check, und zwar vollständig: Auf einem
// Knoten, den ein Freiwilliger einrichtet und nie wieder anfasst, ist das der
// Unterschied zwischen "läuft" und "läuft scheinbar". Aus demselben Grund läuft
// der YAML-Leser mit KnownFields(true) — ein Tippfehler im Schlüssel ist ein
// Fehler, kein stillschweigend ignoriertes Feld.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/josch0/asa-monitor/asamon-node/internal/loc"
)

// DefaultPaths ist die Suchreihenfolge, wenn --config fehlt. Sie hängt vom
// Betriebssystem ab; siehe pfade_unix.go und pfade_windows.go.
var DefaultPaths = suchpfade()

// Duration ist time.Duration mit YAML-Darstellung als Text ("10s").
type Duration time.Duration

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("Dauerangabe muss als Text in der Syntax von time.ParseDuration stehen, etwa \"10s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q ist keine gültige Dauerangabe (erwartet etwa \"10s\", \"1m30s\"): %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// D gibt die Dauer als time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// Config ist der vollständige Inhalt von node-config.yaml.
type Config struct {
	Node     Node      `yaml:"node"`
	Server   Server    `yaml:"server"`
	Channels []Channel `yaml:"channels"`
	Audio    Audio     `yaml:"audio"`
	Paths    Paths     `yaml:"paths"`
	Limits   Limits    `yaml:"limits"`
	Log      Log       `yaml:"log"`

	// Path ist die Datei, aus der gelesen wurde. Kein YAML-Feld.
	Path string `yaml:"-"`
	// Location ist der dekodierte Standort aus Node.LocationCode.
	Location loc.Code `yaml:"-"`
}

type Node struct {
	Name         string `yaml:"name"`
	LocationCode string `yaml:"location_code"`
	Antenna      string `yaml:"antenna"`
	Contact      string `yaml:"contact"`
}

type Server struct {
	URL                string   `yaml:"url"`
	ReportInterval     Duration `yaml:"report_interval"`
	Timeout            Duration `yaml:"timeout"`
	InsecureSkipVerify bool     `yaml:"insecure_skip_verify"`
}

type Channel struct {
	Channel      string `yaml:"channel"`
	Device       string `yaml:"device"`
	DeviceSerial string `yaml:"device_serial"`
	Gain         string `yaml:"gain"`
	IQFile       string `yaml:"iq_file"`
}

type Audio struct {
	Enabled    bool     `yaml:"enabled"`
	PostRoll   Duration `yaml:"post_roll"`
	MaxSeconds int      `yaml:"max_seconds"`
	KeepDays   int      `yaml:"keep_days"`
}

type Paths struct {
	RxBinary string `yaml:"rx_binary"`
	StateDir string `yaml:"state_dir"`
}

type Limits struct {
	MaxSpoolMB          int `yaml:"max_spool_mb"`
	QueueSize           int `yaml:"queue_size"`
	MaxReportsPerReques int `yaml:"max_reports_per_request"`
	// RxSilenceSeconds ist die Frist, nach der ein asamon-rx als
	// steckengeblieben gilt und neu gestartet wird: So lange darf aus seinem
	// Strom **kein einziger** Record kommen.
	//
	// Der Prozess schickt jede Sekunde einen tlm-Record, auch ohne Empfang —
	// Stille heißt also, dass seine Sekundenschleife steht. Gemessen wird über
	// alle Record-Arten, nicht über tlm allein: tlm wird bei vollem Puffer als
	// erstes verworfen, und wo verworfen wird, fließt per Definition
	// Höherwertiges.
	//
	// 0 schaltet die Überwachung ab. Für --replay sinnvoll, wo ein Strom
	// legitim endet.
	RxSilenceSeconds int `yaml:"rx_silence_seconds"`
}

type Log struct {
	Level string `yaml:"level"`
}

// Warnung ist ein Befund, der den Start nicht verhindert, aber ins Log gehört.
type Warnung string

// Defaults sind die Vorgaben, bevor die Datei gelesen wird. Was in der Datei
// steht, überschreibt sie; was fehlt, bleibt stehen.
func Defaults() Config {
	return Config{
		Server: Server{
			ReportInterval: Duration(10 * time.Second),
			Timeout:        Duration(15 * time.Second),
		},
		Audio: Audio{
			Enabled:    true,
			PostRoll:   Duration(10 * time.Second),
			MaxSeconds: 300,
			KeepDays:   7,
		},
		Paths: Paths{
			RxBinary: vorgabeRxBinary,
			StateDir: vorgabeStateDir,
		},
		Limits: Limits{
			MaxSpoolMB:          512,
			QueueSize:           4096,
			MaxReportsPerReques: 60,
			RxSilenceSeconds:    15,
		},
		Log: Log{Level: "info"},
	}
}

// Find sucht die Konfigurationsdatei. Ist explicit gesetzt, wird nur dort
// gesucht — ein angegebener Pfad, der nicht existiert, ist ein Fehler und darf
// nicht stillschweigend durch die Vorgabe ersetzt werden.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("Konfigurationsdatei %s: %w", explicit, err)
		}
		return explicit, nil
	}
	for _, p := range DefaultPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("keine Konfigurationsdatei gefunden; gesucht wurde in %s — mit --config lässt sich eine andere angeben",
		strings.Join(DefaultPaths, ", "))
}

// Load liest und prüft die Datei. Zurück kommen die geprüfte Konfiguration und
// die Warnungen, die den Start nicht verhindern.
func Load(path string) (*Config, []Warnung, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("Konfigurationsdatei lesen: %w", err)
	}
	cfg, warnungen, err := Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	return cfg, warnungen, nil
}

var kanalname = regexp.MustCompile(`^(([1-9]|1[0-3])[A-D]|LA|LB|LC|LD)$`)

// gueltigeGeraete sind die Gerätenamen, die asamon-rx kennt.
var gueltigeGeraete = []string{"rtl_sdr", "rtl_tcp", "airspy", "soapysdr", "rawfile", "auto"}

// gueltigeLogstufen in aufsteigender Ausführlichkeit.
var gueltigeLogstufen = []string{"error", "warn", "info", "debug"}

// Validate prüft die vollständig geladene Konfiguration.
func (c *Config) Validate() ([]Warnung, error) {
	var warnungen []Warnung

	// --- node.name -------------------------------------------------------
	//
	// Gezählt werden Zeichen, nicht Bytes. Eine NFC-Normalisierung, wie sie
	// TODO.md Abschnitt 4 vorsieht, findet nicht statt: Sie bräuchte
	// golang.org/x/text und damit eine zweite Fremdabhängigkeit. Der Name ist
	// reine Anzeige — Schlüssel ist die node_id —, deshalb wird er unverändert
	// durchgereicht. Vermerkt in TODO.md Abschnitt 21.
	name := c.Node.Name
	if !utf8.ValidString(name) {
		return nil, errors.New("node.name ist kein gültiges UTF-8")
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > 20 {
		return nil, fmt.Errorf("node.name hat %d Zeichen, erlaubt sind 1 bis 20", n)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return nil, fmt.Errorf("node.name enthält ein Steuerzeichen (U+%04X)", r)
		}
	}
	c.Node.Name = name

	// --- node.location_code ----------------------------------------------
	if c.Node.LocationCode == "" {
		return nil, errors.New("node.location_code fehlt; er ist der Standort des Knotens im ASA-Präsentationsformat, etwa \"2366-7443-8484\" (zu ermitteln über https://asa.radio/)")
	}
	code, err := loc.ParsePresentation(c.Node.LocationCode)
	if err != nil {
		return nil, fmt.Errorf("node.location_code: %w", err)
	}
	c.Location = code
	if code.IsPolar() {
		return nil, fmt.Errorf("node.location_code %q liegt in einer Polarzone; dafür gibt es keine Rechteckgeometrie", c.Node.LocationCode)
	}

	// --- server ----------------------------------------------------------
	if c.Server.URL == "" {
		return nil, errors.New("server.url fehlt")
	}
	u, err := url.Parse(c.Server.URL)
	if err != nil {
		return nil, fmt.Errorf("server.url %q ist keine gültige URL: %w", c.Server.URL, err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		warnungen = append(warnungen, Warnung(fmt.Sprintf("server.url %q verwendet http:// — die Datensätze gehen dann unverschlüsselt über das Netz", c.Server.URL)))
	default:
		return nil, fmt.Errorf("server.url %q hat das Schema %q; erwartet werden https oder http", c.Server.URL, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("server.url %q nennt keinen Rechnernamen", c.Server.URL)
	}
	if c.Server.InsecureSkipVerify {
		warnungen = append(warnungen, "server.insecure_skip_verify ist gesetzt — Serverzertifikate werden nicht geprüft. Das gehört in lokale Tests, nicht in den Betrieb")
	}
	if c.Server.ReportInterval.D() < time.Second {
		return nil, fmt.Errorf("server.report_interval ist %s; weniger als 1s ergibt keinen sinnvollen Betrieb", c.Server.ReportInterval)
	}
	if c.Server.ReportInterval.D() > 5*time.Minute {
		return nil, fmt.Errorf("server.report_interval ist %s; mehr als 5m macht die Abdeckungskarte unbrauchbar", c.Server.ReportInterval)
	}
	if c.Server.Timeout.D() <= 0 {
		return nil, fmt.Errorf("server.timeout ist %s; ohne Timeout kann eine hängende Anfrage den Uplink dauerhaft blockieren", c.Server.Timeout)
	}

	// --- channels --------------------------------------------------------
	if len(c.Channels) == 0 {
		return nil, errors.New("channels ist leer; mindestens ein Kanal muss überwacht werden")
	}
	gesehen := map[string]int{}
	serien := map[string]int{}
	for i := range c.Channels {
		ch := &c.Channels[i]
		wo := fmt.Sprintf("channels[%d]", i)

		ch.Channel = strings.ToUpper(strings.TrimSpace(ch.Channel))
		if ch.Channel == "" {
			return nil, fmt.Errorf("%s.channel fehlt", wo)
		}
		if !kanalname.MatchString(ch.Channel) {
			return nil, fmt.Errorf("%s.channel %q ist kein DAB-Kanalname (erwartet etwa 5C, 11D, 7B)", wo, ch.Channel)
		}
		if vorher, doppelt := gesehen[ch.Channel]; doppelt {
			return nil, fmt.Errorf("%s.channel %q steht schon in channels[%d]; jeder Kanal darf nur einmal vorkommen", wo, ch.Channel, vorher)
		}
		gesehen[ch.Channel] = i

		if ch.Device == "" {
			ch.Device = "rtl_sdr"
		}
		if !slices.Contains(gueltigeGeraete, ch.Device) {
			return nil, fmt.Errorf("%s.device %q ist unbekannt; asamon-rx kennt %s", wo, ch.Device, strings.Join(gueltigeGeraete, ", "))
		}
		if ch.Device == "rawfile" {
			if ch.IQFile == "" {
				return nil, fmt.Errorf("%s.device ist rawfile, aber iq_file fehlt", wo)
			}
			if _, err := os.Stat(ch.IQFile); err != nil {
				return nil, fmt.Errorf("%s.iq_file: %w", wo, err)
			}
		} else if ch.IQFile != "" {
			return nil, fmt.Errorf("%s.iq_file ist gesetzt, device ist aber %q; iq_file gilt nur für rawfile", wo, ch.Device)
		}

		if ch.Gain == "" {
			ch.Gain = "auto"
		}
		if ch.DeviceSerial != "" {
			if vorher, doppelt := serien[ch.DeviceSerial]; doppelt {
				return nil, fmt.Errorf("%s.device_serial %q steht schon in channels[%d]; zwei Kanäle können nicht denselben Stick verwenden", wo, ch.DeviceSerial, vorher)
			}
			serien[ch.DeviceSerial] = i
		}
	}
	if len(c.Channels) > 1 {
		for i, ch := range c.Channels {
			if ch.DeviceSerial == "" && ch.Device != "rawfile" {
				return nil, fmt.Errorf("channels[%d] (%s) hat kein device_serial. Ab zwei Kanälen muss jeder Kanal seinen Stick über die Seriennummer benennen — sonst greift sich jeder asamon-rx das erste Gerät, das sich öffnen lässt.\n"+
					"Achtung: die Geräteauswahl über die Seriennummer setzt Patch 2 in asamon-rx voraus (siehe asamon-rx/docs/welle-patches.md). Solange der fehlt, ist ein Stick je Knoten die einzige belastbare Betriebsart", i, ch.Channel)
			}
		}
	}

	// --- audio -----------------------------------------------------------
	if c.Audio.PostRoll.D() < 0 {
		return nil, fmt.Errorf("audio.post_roll ist %s und damit negativ", c.Audio.PostRoll)
	}
	if c.Audio.PostRoll.D() > time.Minute {
		warnungen = append(warnungen, Warnung(fmt.Sprintf("audio.post_roll ist %s — was hier großzügig eingestellt wird, landet als fremdes Programm-Audio auf dem Server", c.Audio.PostRoll)))
	}
	if c.Audio.Enabled && c.Audio.MaxSeconds <= 0 {
		return nil, errors.New("audio.max_seconds muss größer als 0 sein; die harte Obergrenze je Aufnahme ist keine Kür")
	}
	if c.Audio.KeepDays < 0 {
		return nil, fmt.Errorf("audio.keep_days ist %d und damit negativ", c.Audio.KeepDays)
	}

	// --- paths -----------------------------------------------------------
	if c.Paths.RxBinary == "" {
		return nil, errors.New("paths.rx_binary fehlt")
	}
	// Unter Windows liegt der Empfangsprozess in aller Regel neben der eigenen
	// Binary — so kommt ein ausgepacktes Release-Archiv ohne Installation aus.
	//
	// Das greift aber **nur**, solange die Konfiguration den Pfad nicht selbst
	// nennt. Wer ihn setzt, meint ihn: Ein Tippfehler darf nicht dadurch
	// verschwinden, dass sich das Programm stillschweigend etwas anderes sucht.
	if c.Paths.RxBinary == vorgabeRxBinary {
		if neben := rxBinaryNeben(); neben != "" {
			c.Paths.RxBinary = neben
		}
	}
	if err := istAusfuehrbar(c.Paths.RxBinary); err != nil {
		return nil, fmt.Errorf("paths.rx_binary: %w", err)
	}
	if c.Paths.StateDir == "" {
		return nil, errors.New("paths.state_dir fehlt")
	}
	if err := istBeschreibbar(c.Paths.StateDir); err != nil {
		return nil, fmt.Errorf("paths.state_dir: %w", err)
	}

	// --- limits ----------------------------------------------------------
	if c.Limits.MaxSpoolMB <= 0 {
		return nil, fmt.Errorf("limits.max_spool_mb ist %d; ohne Obergrenze kann der Spool die Platte füllen", c.Limits.MaxSpoolMB)
	}
	if c.Limits.QueueSize < 64 {
		return nil, fmt.Errorf("limits.queue_size ist %d; unter 64 gingen schon im Normalbetrieb Records verloren", c.Limits.QueueSize)
	}
	if c.Limits.MaxReportsPerReques < 1 {
		return nil, fmt.Errorf("limits.max_reports_per_request ist %d, erwartet wird mindestens 1", c.Limits.MaxReportsPerReques)
	}
	// Unter fünf Sekunden liefe die Frist gegen jeden Aussetzer, den ein
	// überlasteter Pi ohnehin hat — der Neustart wäre dann die Störung.
	if c.Limits.RxSilenceSeconds != 0 && c.Limits.RxSilenceSeconds < 5 {
		return nil, fmt.Errorf("limits.rx_silence_seconds ist %d; erlaubt sind 0 (aus) oder mindestens 5", c.Limits.RxSilenceSeconds)
	}

	// --- log -------------------------------------------------------------
	if !slices.Contains(gueltigeLogstufen, c.Log.Level) {
		return nil, fmt.Errorf("log.level %q ist unbekannt; erlaubt sind %s", c.Log.Level, strings.Join(gueltigeLogstufen, ", "))
	}

	return warnungen, nil
}

// istAusfuehrbar prüft, dass die Datei existiert und ausführbar ist.
//
// Windows kennt kein Ausführungsbit; dort genügt, dass die Datei existiert und
// kein Verzeichnis ist. Der Zielplattform des Knotens (Linux) hilft die Prüfung
// dagegen sehr: Ein `chmod +x` vergessen zu haben ist der zweithäufigste Fehler
// nach dem falschen Pfad.
func istAusfuehrbar(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s ist ein Verzeichnis", path)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if mode := info.Mode(); mode&0o111 == 0 {
		return fmt.Errorf("%s ist nicht ausführbar (Modus %s) — fehlt ein chmod +x?", path, mode)
	}
	return nil
}

// istBeschreibbar prüft, dass das Verzeichnis existiert oder anlegbar ist und
// beschrieben werden kann. Geprüft wird durch Anlegen und Löschen einer Datei —
// die Rechtebits allein sagen unter fremden Dateisystemen zu wenig.
func istBeschreibbar(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s lässt sich nicht anlegen: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".asamon-schreibprobe-*")
	if err != nil {
		return fmt.Errorf("%s ist nicht beschreibbar: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: Schreibprobe lässt sich nicht aufräumen: %w", dir, err)
	}
	return nil
}
