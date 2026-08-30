// SPDX-License-Identifier: GPL-3.0-or-later

// Paket audio verwaltet die Mitschnitte: übernehmen, hochladen, aufräumen.
//
// **Geschrieben werden sie nicht mehr hier.** Seit dem 30.08.2026 legt
// asamon-rx die Dateien selbst an — den rohen Subchannel-Bitstrom als Beleg
// (.dabp) und, wenn LAME zur Hand war, eine abspielbare MP3 daneben — und
// meldet sie mit einem einzigen aud-Record am Ende der Aufnahme. Der Knoten
// führt seitdem Buch, lädt hoch und räumt auf; die Bytes gehen nicht mehr
// durch die Prozessgrenze.
//
// Was das besser macht: ein Drittel weniger Übertragung (kein Base64), kein
// heißer Pfad mehr im Record-Leser — und vor allem kann ein Mitschnitt nicht
// mehr löchrig werden, weil ein Record im Warteschlangenüberlauf verworfen
// wurde. Die Lückenzählung, die es dafür brauchte, ist ersatzlos entfallen.
//
// **Zuschneiden bleibt Pflicht, nicht Kür.** Der warnende Service kann ein
// reguläres Programm sein, dessen Audio nur für die Dauer der Meldung ersetzt
// wird. Kein Vorlauf, kurzer Nachlauf, harte Obergrenze — durchgesetzt wird
// das weiterhin vom Kanalzustand über REC und STOP.
package audio

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// Endungen der Mitschnitte.
const (
	// EndungRoh trägt den rohen Subchannel-Bitstrom, kein Containerformat.
	EndungRoh = ".dabp"
	// EndungMp3 trägt dieselbe Aufnahme abspielbar.
	EndungMp3 = ".mp3"
	// EndungTeil hängt an jeder Datei, die asamon-rx gerade schreibt. Eine
	// solche Datei ohne laufende Aufnahme ist eine Waise.
	EndungTeil = ".part"
)

// Codecs, wie asamon-rx sie im aud-Record nennt.
const (
	CodecRoh = "dabp"
	CodecMp3 = "mp3"
)

// Konfig ist die Einstellung des Mitschnitts.
type Konfig struct {
	Dir      string
	KeepDays int
	Aktiv    bool
}

// Datei ist eine einzelne Datei eines Mitschnitts.
type Datei struct {
	Name   string
	Codec  string
	Bytes  int64
	Sha256 string
}

// Uebernahme ist, was asamon-rx am Ende einer Aufnahme meldet. Der
// Kanalzustand baut sie aus dem aud-Record — damit bleibt dieses Paket
// unabhängig vom Record-Format.
type Uebernahme struct {
	Channel   string
	SubChID   int
	Start     time.Time
	Seconds   float64
	Truncated bool

	SampleRate int
	Channels   int
	Mode       string

	FrameErrors int64
	RsErrors    int64
	RsCorrected int64
	AacErrors   int64

	Dateien []Datei
	Fehler  string
}

// Aufnahme ist ein Mitschnitt, angekündigt oder abgeschlossen.
type Aufnahme struct {
	AlertUID  string
	Channel   string
	SubChID   int
	Start     time.Time
	Seconds   float64
	Bitrate   int // kbit/s, für die Dauerschätzung während der Aufnahme
	Bytes     int64
	Truncated bool
	Dateien   []Datei
	Uploaded  time.Time
	Zustand   string
	Fehler    string

	SampleRate  int
	Channels    int
	Mode        string
	FrameErrors int64
	RsErrors    int64
	RsCorrected int64
	AacErrors   int64
}

// RohDatei gibt die Beleg-Datei, oder nil.
func (a *Aufnahme) RohDatei() *Datei {
	for i := range a.Dateien {
		if a.Dateien[i].Codec == CodecRoh {
			return &a.Dateien[i]
		}
	}
	return nil
}

// Verwaltung hält alle Aufnahmen des Knotens.
type Verwaltung struct {
	k   Konfig
	log *slog.Logger

	mu   sync.Mutex
	nach map[string]*Aufnahme // je alert_uid
}

// Neu öffnet das Audio-Verzeichnis und liest ein, was noch daliegt.
func Neu(stateDir string, k Konfig, log *slog.Logger) (*Verwaltung, error) {
	dir := filepath.Join(stateDir, "audio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("Audio-Verzeichnis %s: %w", dir, err)
	}
	k.Dir = dir
	v := &Verwaltung{k: k, log: log, nach: map[string]*Aufnahme{}}
	v.leseEin()
	return v, nil
}

// Dir ist der Ablageordner. Er geht als --audio-out an jeden asamon-rx: Beide
// Prozesse müssen denselben Ordner meinen, und die Vorgabe stimmt nur so
// lange überein, wie niemand paths.state_dir verlegt hat.
func (v *Verwaltung) Dir() string { return v.k.Dir }

// leseEin findet Mitschnitte aus einem früheren Lauf wieder. Sie sind noch
// nicht hochgeladen — der Server sagt beim nächsten Datensatz, ob er sie will.
//
// Angefangene Dateien (.part) sind Waisen: asamon-rx ist gestorben, bevor die
// Aufnahme endete, und einen aud-Record wird es dazu nie geben.
func (v *Verwaltung) leseEin() {
	eintraege, err := os.ReadDir(v.k.Dir)
	if err != nil {
		return
	}
	var waisen int
	for _, e := range eintraege {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), EndungTeil) {
			pfad := filepath.Join(v.k.Dir, e.Name())
			if err := os.Remove(pfad); err == nil {
				waisen++
			}
			continue
		}
		codec := codecVon(e.Name())
		if codec == "" {
			continue
		}
		uid, channel, subch, ok := zerlegeName(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		a, vorhanden := v.nach[uid]
		if !vorhanden {
			a = &Aufnahme{
				AlertUID: uid, Channel: channel, SubChID: subch,
				Start: info.ModTime().UTC(), Zustand: report.AudioGespeicht,
			}
			v.nach[uid] = a
		}
		// Ohne Prüfsumme: Sie stand im aud-Record, den es in diesem Lauf nicht
		// mehr gibt. Der Server bekommt die Datei dann ohne Vorabvergleich —
		// sie neu zu lesen wäre auf einem Pi teurer als der Nutzen.
		a.Dateien = append(a.Dateien, Datei{
			Name: e.Name(), Codec: codec, Bytes: info.Size(),
		})
		a.Bytes += info.Size()
	}
	if len(v.nach) > 0 {
		v.log.Info("Mitschnitte aus früherem Lauf gefunden", "aufnahmen", len(v.nach))
	}
	if waisen > 0 {
		v.log.Info("angefangene Mitschnitte aufgeräumt", "dateien", waisen)
	}
}

// Beginne vermerkt, dass ein Mitschnitt angefordert wurde. Geschrieben wird er
// von asamon-rx; hier entsteht nur der Eintrag, damit der Datensatz schon
// während der Aufnahme "recording" melden kann.
func (v *Verwaltung) Beginne(alertUID, channel string, subChID int, start time.Time, bitrate int) {
	if !v.k.Aktiv {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	if vorhanden, ok := v.nach[alertUID]; ok && vorhanden.Zustand == report.AudioLaeuft {
		return // läuft bereits
	}
	v.nach[alertUID] = &Aufnahme{
		AlertUID: alertUID, Channel: channel, SubChID: subChID,
		Start: start.UTC(), Bitrate: bitrate, Zustand: report.AudioLaeuft,
	}
	v.log.Info("Mitschnitt angefordert", "alert_uid", alertUID,
		"channel", channel, "subch_id", subChID)
}

// Beende vermerkt das STOP. Die Dateien meldet asamon-rx erst danach mit
// seinem aud-Record; bis dahin bleibt die Aufnahme "recording".
func (v *Verwaltung) Beende(alertUID string, abgeschnitten bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if a, ok := v.nach[alertUID]; ok {
		a.Truncated = a.Truncated || abgeschnitten
	}
}

// Uebernimm trägt ein, was asamon-rx geschrieben hat.
//
// Der Eintrag kann fehlen — etwa, wenn der Knoten neu gestartet wurde,
// während asamon-rx weiterlief. Dann entsteht er hier: Eine fertige Datei ist
// wertvoller als ein sauberer Zustandsverlauf.
func (v *Verwaltung) Uebernimm(alertUID string, u Uebernahme) {
	v.mu.Lock()
	defer v.mu.Unlock()

	a, ok := v.nach[alertUID]
	if !ok {
		a = &Aufnahme{AlertUID: alertUID, Start: u.Start.UTC()}
		v.nach[alertUID] = a
	}
	a.Channel = u.Channel
	a.SubChID = u.SubChID
	if !u.Start.IsZero() {
		a.Start = u.Start.UTC()
	}
	a.Seconds = u.Seconds
	a.Truncated = a.Truncated || u.Truncated
	a.Dateien = u.Dateien
	a.SampleRate, a.Channels, a.Mode = u.SampleRate, u.Channels, u.Mode
	a.FrameErrors, a.RsErrors = u.FrameErrors, u.RsErrors
	a.RsCorrected, a.AacErrors = u.RsCorrected, u.AacErrors
	a.Fehler = u.Fehler

	a.Bytes = 0
	for _, d := range u.Dateien {
		a.Bytes += d.Bytes
	}

	switch {
	case len(u.Dateien) == 0:
		a.Zustand = report.AudioFehler
		if a.Fehler == "" {
			a.Fehler = "asamon-rx meldete keine Datei"
		}
	default:
		a.Zustand = report.AudioGespeicht
	}

	v.log.Info("Mitschnitt übernommen", "alert_uid", alertUID,
		"dateien", len(a.Dateien), "bytes", a.Bytes,
		"sekunden", a.Seconds, "truncated", a.Truncated, "fehler", a.Fehler)
}

// Stand gibt den Mitschnitt-Abschnitt eines Alerts für den Datensatz.
func (v *Verwaltung) Stand(alertUID string) *report.Audio {
	v.mu.Lock()
	defer v.mu.Unlock()

	a, ok := v.nach[alertUID]
	if !ok {
		return nil
	}
	aus := &report.Audio{
		State:       a.Zustand,
		SubChID:     a.SubChID,
		Bytes:       a.Bytes,
		StartedAt:   report.Zeitpunkt(a.Start),
		Truncated:   a.Truncated,
		DurationS:   a.Seconds,
		SampleRate:  a.SampleRate,
		Channels:    a.Channels,
		Mode:        a.Mode,
		FrameErrors: a.FrameErrors,
		RsErrors:    a.RsErrors,
		RsCorrected: a.RsCorrected,
		AacErrors:   a.AacErrors,
	}
	if roh := a.RohDatei(); roh != nil {
		aus.Sha256 = roh.Sha256
	}
	for _, d := range a.Dateien {
		aus.Files = append(aus.Files, report.AudioDatei{
			Name: d.Name, Codec: d.Codec, Bytes: d.Bytes, Sha256: d.Sha256,
		})
	}
	// Solange die Aufnahme läuft, kennt niemand ihre Dauer: Dann wird sie aus
	// der Bitrate der Komponente geschätzt — deshalb heißt das Feld so.
	if a.Seconds == 0 && a.Bitrate > 0 {
		aus.DurationSEst = float64(a.Bytes) * 8 / float64(a.Bitrate*1000)
	}
	if !a.Uploaded.IsZero() {
		aus.UploadedAt = report.Zeitpunkt(a.Uploaded)
	}
	return aus
}

// Angefordert gibt die Aufnahmen, die der Server über audio_wanted verlangt
// hat und die auch wirklich da sind.
//
// Ohne diese Liste lädt der Knoten **nichts** hoch: Zehn Knoten, die dieselbe
// Meldung empfangen, laden sie einmal hoch.
func (v *Verwaltung) Angefordert(uids []string) []*Aufnahme {
	v.mu.Lock()
	defer v.mu.Unlock()

	var out []*Aufnahme
	for _, uid := range uids {
		a, ok := v.nach[uid]
		if !ok || a.Zustand != report.AudioGespeicht || len(a.Dateien) == 0 {
			continue
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b *Aufnahme) int { return a.Start.Compare(b.Start) })
	return out
}

// Pfad gibt den vollen Pfad einer Datei im Ablageordner.
func (v *Verwaltung) Pfad(d Datei) string {
	return filepath.Join(v.k.Dir, d.Name)
}

// Hochgeladen vermerkt den erfolgreichen Upload.
func (v *Verwaltung) Hochgeladen(alertUID string, wann time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if a, ok := v.nach[alertUID]; ok {
		a.Uploaded = wann.UTC()
		a.Zustand = report.AudioHochgel
	}
}

// RaeumeAuf löscht hochgeladene Dateien nach keep_days und dazu, was im
// Ablageordner liegengeblieben ist.
//
// Zwei Fälle, ein Weg: Ein hochgeladener Mitschnitt darf nach keep_days
// verschwinden. Und eine Datei, die zu keiner bekannten Aufnahme gehört — weil
// asamon-rx sie schrieb, ohne dass der Record ankam —, ist nach derselben
// Frist ebenfalls fällig. Beides geht nach den Zeitstempeln der Dateien; die
// Verzeichniseinträge sind die Wahrheit, nicht der Speicher des Knotens.
//
// **Nicht hochgeladene, bekannte Aufnahmen bleiben liegen**: Sie sind der
// einzige Beleg, und der Server kann sie später noch anfordern.
func (v *Verwaltung) RaeumeAuf(jetzt time.Time) int {
	if v.k.KeepDays <= 0 {
		return 0
	}
	grenze := jetzt.Add(-time.Duration(v.k.KeepDays) * 24 * time.Hour)

	v.mu.Lock()
	var weg []*Aufnahme
	bekannt := map[string]bool{}
	for uid, a := range v.nach {
		for _, d := range a.Dateien {
			bekannt[d.Name] = true
		}
		if a.Uploaded.IsZero() || a.Uploaded.After(grenze) {
			continue
		}
		weg = append(weg, a)
		delete(v.nach, uid)
	}
	v.mu.Unlock()

	geloescht := 0
	for _, a := range weg {
		for _, d := range a.Dateien {
			pfad := filepath.Join(v.k.Dir, d.Name)
			if err := os.Remove(pfad); err != nil && !errors.Is(err, fs.ErrNotExist) {
				v.log.Warn("Mitschnitt ließ sich nicht löschen", "datei", pfad, "fehler", err)
				continue
			}
			delete(bekannt, d.Name)
			geloescht++
		}
		v.log.Info("hochgeladener Mitschnitt gelöscht", "alert_uid", a.AlertUID,
			"dateien", len(a.Dateien),
			"alter_tage", int(jetzt.Sub(a.Uploaded).Hours()/24))
	}
	geloescht += v.raeumeVerwaisteAuf(bekannt, grenze)
	return geloescht
}

// raeumeVerwaisteAuf entfernt Dateien im Ablageordner, die zu keiner bekannten
// Aufnahme gehören und älter als die Frist sind — samt angefangener .part.
func (v *Verwaltung) raeumeVerwaisteAuf(bekannt map[string]bool, grenze time.Time) int {
	eintraege, err := os.ReadDir(v.k.Dir)
	if err != nil {
		return 0
	}
	geloescht := 0
	for _, e := range eintraege {
		if e.IsDir() || bekannt[e.Name()] {
			continue
		}
		teil := strings.HasSuffix(e.Name(), EndungTeil)
		if !teil && codecVon(e.Name()) == "" {
			continue // fremde Datei: nicht anfassen
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(grenze) {
			continue
		}
		pfad := filepath.Join(v.k.Dir, e.Name())
		if err := os.Remove(pfad); err != nil {
			v.log.Warn("verwaiste Datei ließ sich nicht löschen", "datei", pfad, "fehler", err)
			continue
		}
		v.log.Info("verwaiste Datei gelöscht", "datei", e.Name(), "angefangen", teil)
		geloescht++
	}
	return geloescht
}

// Dateien gibt die Zahl der vorgehaltenen Mitschnitte für den Datensatz.
func (v *Verwaltung) Dateien() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.nach)
}

// DateiName baut den Namen, den auch asamon-rx vergibt:
// <alert_uid>-<channel>-<subch>.<endung>
func DateiName(alertUID, channel string, subChID int, endung string) string {
	return fmt.Sprintf("%s-%s-%d%s", alertUID, sicher(channel), subChID, endung)
}

func codecVon(name string) string {
	switch {
	case strings.HasSuffix(name, EndungRoh):
		return CodecRoh
	case strings.HasSuffix(name, EndungMp3):
		return CodecMp3
	default:
		return ""
	}
}

func zerlegeName(name string) (uid, channel string, subch int, ok bool) {
	rumpf := name
	for _, endung := range []string{EndungRoh, EndungMp3} {
		rumpf = strings.TrimSuffix(rumpf, endung)
	}
	teile := strings.Split(rumpf, "-")
	if len(teile) < 3 {
		return "", "", 0, false
	}
	uid = teile[0]
	channel = teile[len(teile)-2]
	if _, err := fmt.Sscanf(teile[len(teile)-1], "%d", &subch); err != nil {
		return "", "", 0, false
	}
	return uid, channel, subch, true
}

// sicher entfernt aus einem Kanalnamen alles, was in einem Dateinamen nichts
// zu suchen hat. asamon-rx tut in sicherFuerDateinamen() dasselbe.
func sicher(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}
