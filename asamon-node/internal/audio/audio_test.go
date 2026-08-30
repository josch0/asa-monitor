// SPDX-License-Identifier: GPL-3.0-or-later

package audio

import (
	"crypto/sha256"
	"encoding/hex"
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

// Der Mitschnitt geht roh auf die Platte, und sein SHA-256 muss dem der Datei
// entsprechen — er ist die Integritätsprüfung, mit der der Server arbeitet.
func TestMitschnittUndPruefsumme(t *testing.T) {
	v, dir := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)

	h := sha256.New()
	for chunk := range 10 {
		daten := []byte{byte(chunk), byte(chunk + 1), byte(chunk + 2), byte(chunk + 3)}
		v.Schreibe("uid1", chunk, daten)
		h.Write(daten)
	}
	v.Beende("uid1", false)

	stand := v.Stand("uid1")
	if stand == nil {
		t.Fatal("kein Stand")
	}
	if stand.State != report.AudioGespeicht {
		t.Errorf("state ist %q", stand.State)
	}
	if stand.Bytes != 40 {
		t.Errorf("bytes ist %d, erwartet 40", stand.Bytes)
	}
	if stand.Gaps != 0 {
		t.Errorf("audio_gaps ist %d", stand.Gaps)
	}
	if want := hex.EncodeToString(h.Sum(nil)); stand.Sha256 != want {
		t.Errorf("sha256 ist %s, erwartet %s", stand.Sha256, want)
	}

	// Und die Datei muss dieselbe Prüfsumme haben.
	pfad := filepath.Join(dir, "audio", "uid1-5C-7"+Endung)
	raw, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	summe := sha256.Sum256(raw)
	if hex.EncodeToString(summe[:]) != stand.Sha256 {
		t.Error("die Prüfsumme der Datei weicht von der gemeldeten ab")
	}
	if len(raw) != 40 {
		t.Errorf("die Datei ist %d Byte groß", len(raw))
	}

	// Die Dauer wird aus der Bitrate geschätzt: 40 Byte bei 32 kbit/s.
	if want := 40.0 * 8 / 32000; stand.DurationSEst != want {
		t.Errorf("duration_s_est ist %v, erwartet %v", stand.DurationSEst, want)
	}
}

// Lücken sind Verluste und gehören gezählt, nicht geglättet.
func TestLueckenWerdenGezaehlt(t *testing.T) {
	v, _ := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)
	for _, chunk := range []int{0, 1, 5, 6, 7} { // 2, 3, 4 fehlen
		v.Schreibe("uid1", chunk, []byte{1, 2, 3, 4})
	}
	v.Beende("uid1", false)

	stand := v.Stand("uid1")
	if stand.Gaps != 3 {
		t.Errorf("audio_gaps ist %d, erwartet 3", stand.Gaps)
	}
	// Die Daten selbst gehen trotzdem vollständig in die Datei.
	if stand.Bytes != 20 {
		t.Errorf("bytes ist %d", stand.Bytes)
	}
}

func TestAbgeschnittenWirdGemeldet(t *testing.T) {
	v, _ := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Schreibe("uid1", 0, []byte("x"))
	v.Beende("uid1", true)
	if !v.Stand("uid1").Truncated {
		t.Error("truncated wurde nicht gemeldet")
	}
}

// Ohne audio_wanted geht nichts raus. Das ist die Crowd-Ersparnis.
func TestNurAngefordertesWirdGeliefert(t *testing.T) {
	v, _ := neu(t, 7)
	for _, uid := range []string{"uid1", "uid2", "uid3"} {
		v.Beginne(uid, "5C", 7, t0, 32)
		v.Schreibe(uid, 0, []byte("daten"))
		v.Beende(uid, false)
	}

	if got := v.Angefordert(nil); len(got) != 0 {
		t.Errorf("ohne Anforderung kamen %d Aufnahmen", len(got))
	}
	got := v.Angefordert([]string{"uid2", "gibtsnicht"})
	if len(got) != 1 || got[0].AlertUID != "uid2" {
		t.Errorf("Angefordert lieferte %v", got)
	}

	// Eine noch laufende Aufnahme wird nicht geliefert — sie ist unvollständig.
	v.Beginne("uid4", "5C", 7, t0, 32)
	v.Schreibe("uid4", 0, []byte("x"))
	if got := v.Angefordert([]string{"uid4"}); len(got) != 0 {
		t.Error("eine laufende Aufnahme wurde zum Upload angeboten")
	}
	v.SchliesseAlle()
}

// Hochgeladene Dateien werden nach keep_days gelöscht; nicht hochgeladene
// bleiben liegen — sie sind der einzige Beleg.
func TestAufraeumenNurNachUpload(t *testing.T) {
	v, dir := neu(t, 7)
	for _, uid := range []string{"alt", "neu", "nie"} {
		v.Beginne(uid, "5C", 7, t0, 32)
		v.Schreibe(uid, 0, []byte("daten"))
		v.Beende(uid, false)
	}
	jetzt := time.Now()
	v.Hochgeladen("alt", jetzt.Add(-10*24*time.Hour))
	v.Hochgeladen("neu", jetzt.Add(-1*24*time.Hour))
	// "nie" bleibt ohne Upload.

	geloescht := v.RaeumeAuf(jetzt)
	if geloescht != 1 {
		t.Errorf("%d Dateien gelöscht, erwartet 1", geloescht)
	}
	if _, err := os.Stat(filepath.Join(dir, "audio", "alt-5C-7"+Endung)); err == nil {
		t.Error("die alte, hochgeladene Datei blieb liegen")
	}
	for _, uid := range []string{"neu", "nie"} {
		if _, err := os.Stat(filepath.Join(dir, "audio", uid+"-5C-7"+Endung)); err != nil {
			t.Errorf("%s wurde gelöscht: %v", uid, err)
		}
	}
	if v.Dateien() != 2 {
		t.Errorf("Dateien() ist %d", v.Dateien())
	}

	// keep_days 0 heißt: nie löschen.
	v2, _ := neu(t, 0)
	v2.Beginne("x", "5C", 7, t0, 32)
	v2.Schreibe("x", 0, []byte("y"))
	v2.Beende("x", false)
	v2.Hochgeladen("x", jetzt.Add(-100*24*time.Hour))
	if n := v2.RaeumeAuf(jetzt); n != 0 {
		t.Errorf("bei keep_days 0 wurden %d Dateien gelöscht", n)
	}
}

// Mitschnitte müssen einen Neustart überleben: Der Server kann sie später noch
// anfordern.
func TestMitschnitteUeberlebenNeustart(t *testing.T) {
	v, dir := neu(t, 7)
	v.Beginne("uid1", "11D", 12, t0, 48)
	v.Schreibe("uid1", 0, []byte("daten"))
	v.Beende("uid1", false)
	summe := v.Stand("uid1").Sha256

	v2, err := Neu(dir, Konfig{KeepDays: 7, Aktiv: true}, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	stand := v2.Stand("uid1")
	if stand == nil {
		t.Fatal("der Mitschnitt wurde nach dem Neustart nicht gefunden")
	}
	if stand.SubChID != 12 {
		t.Errorf("subch_id ist %d, erwartet 12", stand.SubChID)
	}
	if stand.Sha256 != summe {
		t.Errorf("sha256 ist %s, erwartet %s", stand.Sha256, summe)
	}
	if got := v2.Angefordert([]string{"uid1"}); len(got) != 1 {
		t.Error("der wiedergefundene Mitschnitt lässt sich nicht anfordern")
	}
}

func TestAbgeschaltetSchreibtNichts(t *testing.T) {
	dir := t.TempDir()
	v, err := Neu(dir, Konfig{KeepDays: 7, Aktiv: false}, stillesLog())
	if err != nil {
		t.Fatal(err)
	}
	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Schreibe("uid1", 0, []byte("daten"))
	v.Beende("uid1", false)

	if v.Stand("uid1") != nil {
		t.Error("bei abgeschaltetem Audio entstand trotzdem eine Aufnahme")
	}
	eintraege, err := os.ReadDir(filepath.Join(dir, "audio"))
	if err != nil {
		t.Fatal(err)
	}
	if len(eintraege) != 0 {
		t.Errorf("%d Dateien trotz abgeschaltetem Audio", len(eintraege))
	}
}

func TestSchliesseAlle(t *testing.T) {
	v, _ := neu(t, 7)
	v.Beginne("uid1", "5C", 7, t0, 32)
	v.Schreibe("uid1", 0, []byte("daten"))
	if v.Stand("uid1").State != report.AudioLaeuft {
		t.Fatal("die Aufnahme läuft nicht")
	}
	v.SchliesseAlle()
	if got := v.Stand("uid1").State; got != report.AudioGespeicht {
		t.Errorf("state ist %q, erwartet %q", got, report.AudioGespeicht)
	}
	if v.Stand("uid1").Sha256 == "" {
		t.Error("beim Herunterfahren wurde keine Prüfsumme berechnet")
	}
}

func TestDateinameUndZerlegung(t *testing.T) {
	name := dateiName("7c2dabcd", "11D", 12)
	if name != "7c2dabcd-11D-12"+Endung {
		t.Errorf("Dateiname ist %q", name)
	}
	uid, ch, subch, ok := zerlegeName(name)
	if !ok || uid != "7c2dabcd" || ch != "11D" || subch != 12 {
		t.Errorf("zerlegeName gab %q, %q, %d, %v", uid, ch, subch, ok)
	}
	// Ein Kanalname mit Sonderzeichen darf keinen Pfad aufspannen.
	if got := dateiName("uid", "../etc", 1); got != "uid-___etc-1"+Endung {
		t.Errorf("gefährlicher Kanalname ergab %q", got)
	}
}
