// SPDX-License-Identifier: GPL-3.0-or-later

// Paket audio verwaltet die Mitschnitte: sammeln, schreiben, aufräumen.
//
// Der rohe Subchannel-Bitstrom wird durchgereicht, nicht dekodiert. Kein AAC,
// kein FFmpeg, keine Superframe-Zerlegung — die Datei geht so zum Server, wie
// sie vom Kanal kam.
//
// **Zuschneiden ist Pflicht, nicht Kür.** Der warnende Service kann ein
// reguläres Programm sein, dessen Audio nur für die Dauer der Meldung ersetzt
// wird. Kein Vorlauf, kurzer Nachlauf, harte Obergrenze. Was hier großzügig
// eingestellt wird, landet als fremdes Programm-Audio zentral auf einem Server.
package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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

// Endung der Mitschnitte. Roher Subchannel-Bitstrom, kein Containerformat.
const Endung = ".dabp"

// Konfig ist die Einstellung des Mitschnitts.
type Konfig struct {
	Dir      string
	KeepDays int
	Aktiv    bool
}

// Aufnahme ist ein Mitschnitt, laufend oder abgeschlossen.
type Aufnahme struct {
	AlertUID  string
	Channel   string
	SubChID   int
	Pfad      string
	Start     time.Time
	Bitrate   int // kbit/s, für die Dauerschätzung
	Bytes     int64
	Sha256    string
	Truncated bool
	Gaps      int
	Uploaded  time.Time
	Zustand   string
	Fehler    string

	datei        *os.File
	hash         hash.Hash
	letzterChunk int
	hatChunk     bool
}

// Verwaltung hält alle Aufnahmen des Knotens.
type Verwaltung struct {
	k   Konfig
	log *slog.Logger

	mu    sync.Mutex
	nach  map[string]*Aufnahme // je alert_uid
	dauer time.Duration
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

// leseEin findet Mitschnitte aus einem früheren Lauf wieder. Sie sind noch
// nicht hochgeladen — der Server sagt beim nächsten Datensatz, ob er sie will.
func (v *Verwaltung) leseEin() {
	eintraege, err := os.ReadDir(v.k.Dir)
	if err != nil {
		return
	}
	for _, e := range eintraege {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Endung) {
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
		pfad := filepath.Join(v.k.Dir, e.Name())
		a := &Aufnahme{
			AlertUID: uid, Channel: channel, SubChID: subch, Pfad: pfad,
			Start: info.ModTime().UTC(), Bytes: info.Size(), Zustand: report.AudioGespeicht,
		}
		if summe, err := dateiSumme(pfad); err == nil {
			a.Sha256 = summe
		}
		v.nach[uid] = a
	}
	if len(v.nach) > 0 {
		v.log.Info("Mitschnitte aus früherem Lauf gefunden", "dateien", len(v.nach))
	}
}

// Beginne legt eine Datei an und beginnt den Mitschnitt.
//
// Beim REC wird sofort auf die Platte geschrieben — kein Puffern im
// Arbeitsspeicher: Eine zweiminütige Meldung sind bei 32 kbit/s rund 480 kB,
// und der Knoten läuft auf einem Pi.
func (v *Verwaltung) Beginne(alertUID, channel string, subChID int, start time.Time, bitrate int) {
	if !v.k.Aktiv {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	if vorhanden, ok := v.nach[alertUID]; ok && vorhanden.datei != nil {
		return // läuft bereits
	}
	pfad := filepath.Join(v.k.Dir, dateiName(alertUID, channel, subChID))
	f, err := os.OpenFile(pfad, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		v.log.Error("Mitschnitt lässt sich nicht anlegen", "datei", pfad, "fehler", err)
		v.nach[alertUID] = &Aufnahme{
			AlertUID: alertUID, Channel: channel, SubChID: subChID,
			Zustand: report.AudioFehler, Fehler: err.Error(),
		}
		return
	}
	v.nach[alertUID] = &Aufnahme{
		AlertUID: alertUID, Channel: channel, SubChID: subChID, Pfad: pfad,
		Start: start.UTC(), Bitrate: bitrate, Zustand: report.AudioLaeuft,
		datei: f, hash: sha256.New(),
	}
	v.log.Info("Mitschnitt begonnen", "alert_uid", alertUID, "channel", channel, "subch_id", subChID, "datei", pfad)
}

// Schreibe hängt einen Chunk an.
//
// Lücken in der Chunk-Nummer sind Verluste und gehören gezählt, nicht
// geglättet: Ein Mitschnitt mit stillschweigend fehlenden Sekunden wäre als
// Beleg wertlos.
func (v *Verwaltung) Schreibe(alertUID string, chunk int, daten []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()

	a, ok := v.nach[alertUID]
	if !ok || a.datei == nil {
		return
	}
	if a.hatChunk && chunk != a.letzterChunk+1 {
		luecke := chunk - a.letzterChunk - 1
		if luecke < 0 {
			luecke = 1
		}
		a.Gaps += luecke
		v.log.Warn("Lücke im Mitschnitt", "alert_uid", alertUID,
			"erwartet", a.letzterChunk+1, "erhalten", chunk, "luecken_gesamt", a.Gaps)
	}
	a.letzterChunk, a.hatChunk = chunk, true

	n, err := a.datei.Write(daten)
	if err != nil {
		v.log.Error("Mitschnitt lässt sich nicht schreiben", "alert_uid", alertUID, "fehler", err)
		a.Zustand, a.Fehler = report.AudioFehler, err.Error()
		a.datei.Close()
		a.datei = nil
		return
	}
	a.hash.Write(daten[:n])
	a.Bytes += int64(n)
}

// Beende schließt die Datei und berechnet die Prüfsumme.
func (v *Verwaltung) Beende(alertUID string, abgeschnitten bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	a, ok := v.nach[alertUID]
	if !ok || a.datei == nil {
		return
	}
	a.Truncated = a.Truncated || abgeschnitten
	if err := a.datei.Sync(); err != nil {
		v.log.Warn("Mitschnitt ließ sich nicht synchronisieren", "alert_uid", alertUID, "fehler", err)
	}
	a.datei.Close()
	a.datei = nil
	a.Sha256 = hex.EncodeToString(a.hash.Sum(nil))
	a.Zustand = report.AudioGespeicht
	v.log.Info("Mitschnitt abgeschlossen", "alert_uid", alertUID,
		"bytes", a.Bytes, "sha256", a.Sha256, "truncated", a.Truncated, "luecken", a.Gaps)
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
		State:     a.Zustand,
		SubChID:   a.SubChID,
		Bytes:     a.Bytes,
		StartedAt: report.Zeitpunkt(a.Start),
		Sha256:    a.Sha256,
		Truncated: a.Truncated,
		Gaps:      a.Gaps,
	}
	// Die Dauer wird aus der Bitrate der Komponente geschätzt, nicht gemessen —
	// deshalb heißt das Feld so.
	if a.Bitrate > 0 {
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
		if !ok || a.datei != nil || a.Zustand != report.AudioGespeicht || a.Bytes == 0 {
			continue
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b *Aufnahme) int { return a.Start.Compare(b.Start) })
	return out
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

// RaeumeAuf löscht hochgeladene Dateien nach keep_days.
//
// Nicht hochgeladene bleiben liegen: Sie sind der einzige Beleg, und der
// Server kann sie später noch anfordern.
func (v *Verwaltung) RaeumeAuf(jetzt time.Time) int {
	if v.k.KeepDays <= 0 {
		return 0
	}
	grenze := jetzt.Add(-time.Duration(v.k.KeepDays) * 24 * time.Hour)

	v.mu.Lock()
	var weg []*Aufnahme
	for uid, a := range v.nach {
		if a.datei != nil || a.Uploaded.IsZero() || a.Uploaded.After(grenze) {
			continue
		}
		weg = append(weg, a)
		delete(v.nach, uid)
	}
	v.mu.Unlock()

	for _, a := range weg {
		if err := os.Remove(a.Pfad); err != nil && !errors.Is(err, fs.ErrNotExist) {
			v.log.Warn("Mitschnitt ließ sich nicht löschen", "datei", a.Pfad, "fehler", err)
			continue
		}
		v.log.Info("hochgeladener Mitschnitt gelöscht", "alert_uid", a.AlertUID,
			"alter_tage", int(jetzt.Sub(a.Uploaded).Hours()/24))
	}
	return len(weg)
}

// Dateien gibt die Zahl der vorgehaltenen Mitschnitte für den Datensatz.
func (v *Verwaltung) Dateien() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.nach)
}

// SchliesseAlle beendet laufende Mitschnitte beim Herunterfahren.
func (v *Verwaltung) SchliesseAlle() {
	v.mu.Lock()
	uids := make([]string, 0, len(v.nach))
	for uid, a := range v.nach {
		if a.datei != nil {
			uids = append(uids, uid)
		}
	}
	v.mu.Unlock()
	for _, uid := range uids {
		v.Beende(uid, false)
	}
}

// dateiName baut den Namen: <alert_uid>-<channel>-<subch>.dabp
func dateiName(alertUID, channel string, subChID int) string {
	return fmt.Sprintf("%s-%s-%d%s", alertUID, sicher(channel), subChID, Endung)
}

func zerlegeName(name string) (uid, channel string, subch int, ok bool) {
	rumpf := strings.TrimSuffix(name, Endung)
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
// zu suchen hat.
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

func dateiSumme(pfad string) (string, error) {
	f, err := os.Open(pfad)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
