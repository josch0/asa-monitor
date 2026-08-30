// SPDX-License-Identifier: GPL-3.0-or-later

package audio

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

func stillesLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func neu(t *testing.T, keepDays int) (*Verwaltung, string) {
	t.Helper()
	dir := t.TempDir()
	v, err := Neu(dir, Konfig{KeepDays: keepDays, Aktiv: true}, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	return v, dir
}

var t0 = time.Date(2026, 8, 26, 14, 3, 0, 0, time.UTC)

// lege schreibt eine Datei so, wie asamon-rx sie hinterlässt.
func lege(t *testing.T, dir, name string, groesse int, alter time.Duration) string {
	t.Helper()
	pfad := filepath.Join(dir, "audio", name)
	if err := os.WriteFile(pfad, make([]byte, groesse), 0o600); err != nil {
		t.Fatal(err)
	}
	if alter > 0 {
		wann := time.Now().Add(-alter)
		if err := os.Chtimes(pfad, wann, wann); err != nil {
			t.Fatal(err)
		}
	}
	return pfad
}

func uebernahmeMitZweiDateien() Uebernahme {
	return Uebernahme{
		Channel: "5C", SubChID: 7, Start: t0, Seconds: 43.75,
		SampleRate: 48000, Channels: 2, Mode: "HE-AACv2",
		RsCorrected: 12,
		Dateien: []Datei{
			{Name: "uid1-5C-7" + EndungRoh, Codec: CodecRoh, Bytes: 262144, Sha256: "aa"},
			{Name: "uid1-5C-7" + EndungMp3, Codec: CodecMp3, Bytes: 245760, Sha256: "bb"},
		},
	}
}

// Der Knoten schreibt die Dateien nicht mehr — er übernimmt, was asamon-rx
// meldet. Was im Datensatz landet, muss dem Record entsprechen.
func TestUebernahmeLandetImStand(t *testing.T) {
	v, _ := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)

	if stand := v.Stand("uid1"); stand == nil || stand.State != report.AudioLaeuft {
		t.Fatalf("während der Aufnahme: %+v", stand)
	}

	v.Beende("uid1", false)
	v.Uebernimm("uid1", uebernahmeMitZweiDateien())

	stand := v.Stand("uid1")
	if stand == nil {
		t.Fatal("kein Stand")
	}
	if stand.State != report.AudioGespeicht {
		t.Errorf("state ist %q", stand.State)
	}
	if stand.Bytes != 262144+245760 {
		t.Errorf("bytes ist %d — die Summe über alle Dateien fehlt", stand.Bytes)
	}
	if stand.DurationS != 43.75 {
		t.Errorf("duration_s ist %v; die gemessene Dauer kommt von asamon-rx", stand.DurationS)
	}
	if stand.DurationSEst != 0 {
		t.Error("neben der gemessenen Dauer hat die Schätzung nichts zu suchen")
	}
	// Das Feld sha256 gilt dem Beleg, nicht der MP3.
	if stand.Sha256 != "aa" {
		t.Errorf("sha256 ist %q, erwartet die des rohen Bitstroms", stand.Sha256)
	}
	if len(stand.Files) != 2 || stand.Files[1].Codec != CodecMp3 {
		t.Errorf("files: %+v", stand.Files)
	}
	if stand.SampleRate != 48000 || stand.Mode != "HE-AACv2" || stand.RsCorrected != 12 {
		t.Errorf("die Codec- und Fehlerangaben fehlen: %+v", stand)
	}
}

// Meldet asamon-rx keine Datei, ist das ein Fehler und kein leerer Erfolg —
// sonst hielte der Server eine gescheiterte Aufnahme für eine stille Meldung.
func TestUebernahmeOhneDateiIstEinFehler(t *testing.T) {
	v, _ := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Uebernimm("uid1", Uebernahme{Channel: "5C", SubChID: 7, Fehler: "MP3: ohne LAME gebaut"})

	stand := v.Stand("uid1")
	if stand == nil || stand.State != report.AudioFehler {
		t.Fatalf("state: %+v", stand)
	}
}

// Der Knoten kann neu gestartet worden sein, während asamon-rx weiterlief.
// Eine fertige Datei ist wertvoller als ein sauberer Zustandsverlauf.
func TestUebernahmeOhneVorherigesBeginne(t *testing.T) {
	v, _ := neu(t, 7)
	v.Uebernimm("uid9", uebernahmeMitZweiDateien())

	if stand := v.Stand("uid9"); stand == nil || stand.State != report.AudioGespeicht {
		t.Fatalf("die unangekündigte Aufnahme fehlt: %+v", stand)
	}
}

// Angefordert gibt nur, was der Server verlangt hat und was wirklich da ist.
func TestNurAngefordertesGehtRaus(t *testing.T) {
	v, _ := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Uebernimm("uid1", uebernahmeMitZweiDateien())
	v.Beginne("uid2", "5C", 7, t0.Add(time.Minute), 32)

	if got := v.Angefordert([]string{"uid2"}); len(got) != 0 {
		t.Error("eine laufende Aufnahme darf nicht hochgeladen werden")
	}
	if got := v.Angefordert([]string{"unbekannt"}); len(got) != 0 {
		t.Error("eine unbekannte uid liefert nichts")
	}
	got := v.Angefordert([]string{"uid1"})
	if len(got) != 1 || len(got[0].Dateien) != 2 {
		t.Fatalf("angefordert: %+v", got)
	}
	if p := v.Pfad(got[0].Dateien[0]); filepath.Base(p) != "uid1-5C-7"+EndungRoh {
		t.Errorf("Pfad: %s", p)
	}
}

// Ein Neustart findet beide Dateien wieder und wirft angefangene weg.
func TestNeustartFindetDateienUndRaeumtTeileWeg(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "audio"), 0o700); err != nil {
		t.Fatal(err)
	}
	lege(t, dir, "uid1-5C-7"+EndungRoh, 1000, 0)
	lege(t, dir, "uid1-5C-7"+EndungMp3, 800, 0)
	teil := lege(t, dir, "uid2-5C-7"+EndungRoh+EndungTeil, 500, 0)
	lege(t, dir, "liesmich.txt", 10, 0)

	v, err := Neu(dir, Konfig{KeepDays: 7, Aktiv: true}, stillesLog())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(teil); !os.IsNotExist(err) {
		t.Error("eine angefangene Datei ist eine Waise und gehört weggeräumt")
	}
	if v.Dateien() != 1 {
		t.Fatalf("%d Aufnahmen wiedergefunden, erwartet 1", v.Dateien())
	}
	got := v.Angefordert([]string{"uid1"})
	if len(got) != 1 || len(got[0].Dateien) != 2 || got[0].Bytes != 1800 {
		t.Errorf("wiedergefunden: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "audio", "liesmich.txt")); err != nil {
		t.Error("fremde Dateien im Ordner bleiben unangetastet")
	}
}

// Aufgeräumt wird zweierlei: hochgeladene Aufnahmen nach keep_days und alles,
// was liegengeblieben ist. Nicht hochgeladene Belege bleiben.
func TestRaeumeAuf(t *testing.T) {
	v, dir := neu(t, 7)

	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Uebernimm("uid1", uebernahmeMitZweiDateien())
	lege(t, dir, "uid1-5C-7"+EndungRoh, 10, 0)
	lege(t, dir, "uid1-5C-7"+EndungMp3, 10, 0)
	v.Hochgeladen("uid1", time.Now().Add(-10*24*time.Hour))

	// Nicht hochgeladen: bleibt, egal wie alt.
	v.Beginne("uid2", "5C", 7, t0, 32)
	u2 := uebernahmeMitZweiDateien()
	u2.Dateien = []Datei{{Name: "uid2-5C-7" + EndungRoh, Codec: CodecRoh, Bytes: 10}}
	v.Uebernimm("uid2", u2)
	lege(t, dir, "uid2-5C-7"+EndungRoh, 10, 30*24*time.Hour)

	// Verwaist und alt: fällt.
	alt := lege(t, dir, "uid3-5C-7"+EndungRoh, 10, 30*24*time.Hour)
	// Verwaist, aber frisch: bleibt — die Aufnahme kann gerade erst fertig
	// geworden sein, und der aud-Record kommt vielleicht noch.
	frisch := lege(t, dir, "uid4-5C-7"+EndungRoh, 10, 0)
	// Angefangen und alt: fällt.
	teil := lege(t, dir, "uid5-5C-7"+EndungRoh+EndungTeil, 10, 30*24*time.Hour)

	n := v.RaeumeAuf(time.Now())
	if n != 4 {
		t.Errorf("%d Dateien gelöscht, erwartet 4 (zwei hochgeladene, eine verwaiste, eine angefangene)", n)
	}
	for _, weg := range []string{
		filepath.Join(dir, "audio", "uid1-5C-7"+EndungRoh),
		filepath.Join(dir, "audio", "uid1-5C-7"+EndungMp3),
		alt, teil,
	} {
		if _, err := os.Stat(weg); !os.IsNotExist(err) {
			t.Errorf("%s liegt noch da", filepath.Base(weg))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "audio", "uid2-5C-7"+EndungRoh)); err != nil {
		t.Error("ein nicht hochgeladener Beleg wurde gelöscht")
	}
	if _, err := os.Stat(frisch); err != nil {
		t.Error("eine frische verwaiste Datei wurde zu früh gelöscht")
	}
	if v.Dateien() != 1 {
		t.Errorf("%d Aufnahmen übrig, erwartet 1", v.Dateien())
	}
}

// Ohne keep_days wird nichts gelöscht — auch nichts Verwaistes.
func TestOhneKeepDaysBleibtAlles(t *testing.T) {
	v, dir := neu(t, 0)
	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Uebernimm("uid1", uebernahmeMitZweiDateien())
	lege(t, dir, "uid1-5C-7"+EndungRoh, 10, 0)
	v.Hochgeladen("uid1", time.Now().Add(-100*24*time.Hour))

	if n := v.RaeumeAuf(time.Now()); n != 0 {
		t.Errorf("%d Dateien gelöscht, erwartet 0", n)
	}
}

// Der Dateiname ist die Nahtstelle zu asamon-rx: Beide Programme müssen
// denselben bauen, sonst findet der Knoten nach einem Neustart nichts wieder.
func TestDateiName(t *testing.T) {
	if got := DateiName("7c2dabcd", "5C", 13, EndungRoh); got != "7c2dabcd-5C-13.dabp" {
		t.Errorf("DateiName: %s", got)
	}
	if got := DateiName("uid", "../etc", 1, EndungMp3); got != "uid-___etc-1.mp3" {
		t.Errorf("ein Kanalname mit Pfadtrenner: %s", got)
	}
	uid, channel, subch, ok := zerlegeName("7c2dabcd-5C-13.mp3")
	if !ok || uid != "7c2dabcd" || channel != "5C" || subch != 13 {
		t.Errorf("zerlegeName: %q %q %d %v", uid, channel, subch, ok)
	}
	if _, _, _, ok := zerlegeName("kaputt.dabp"); ok {
		t.Error("ein Name ohne die drei Teile darf nicht durchgehen")
	}
}

// Ohne audio.enabled entsteht kein Eintrag.
func TestAusgeschaltet(t *testing.T) {
	dir := t.TempDir()
	v, err := Neu(dir, Konfig{KeepDays: 7, Aktiv: false}, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	v.Beginne("uid1", "5C", 7, t0, 32)
	if v.Dateien() != 0 {
		t.Error("bei ausgeschaltetem Audio wird nichts angelegt")
	}
}
