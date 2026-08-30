// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"testing"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/audio"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// merkendeSenke hält fest, was der Kanalzustand an Audio weitergibt.
type merkendeSenke struct {
	begonnen  []string
	beendet   []string
	uebernahm map[string]audio.Uebernahme
	stand     map[string]*report.Audio
}

func neueSenke() *merkendeSenke {
	return &merkendeSenke{
		uebernahm: map[string]audio.Uebernahme{},
		stand:     map[string]*report.Audio{},
	}
}

func (m *merkendeSenke) Beginne(alertUID, channel string, subChID int, start time.Time, bitrate int) {
	m.begonnen = append(m.begonnen, alertUID)
	m.stand[alertUID] = &report.Audio{State: report.AudioLaeuft, SubChID: subChID}
}

func (m *merkendeSenke) Beende(alertUID string, abgeschnitten bool) {
	m.beendet = append(m.beendet, alertUID)
}

func (m *merkendeSenke) Uebernimm(alertUID string, u audio.Uebernahme) {
	m.uebernahm[alertUID] = u
	m.stand[alertUID] = &report.Audio{
		State: report.AudioGespeicht, SubChID: u.SubChID, DurationS: u.Seconds,
	}
}

func (m *merkendeSenke) Stand(alertUID string) *report.Audio { return m.stand[alertUID] }

// Der aud-Record kommt **nach** dem STOP — ein Alert mit laufendem Mitschnitt
// ist zu diesem Zeitpunkt per Definition nicht mehr zu finden. Trotzdem muss
// die Aufnahme unter der uid des Alerts landen, sonst steht sie in keinem
// Datensatz.
func TestAufnahmeLandetBeimAlert(t *testing.T) {
	senke := neueSenke()
	abschnitte := spieleAb(t, "alert-audio", Konfig{
		AudioAktiv: true, PostRoll: 2 * time.Second, MaxAudioSekunden: 300,
	}, Senken{Audio: senke, Kommando: func(string) {}})

	if len(senke.begonnen) != 1 {
		t.Fatalf("%d Mitschnitte begonnen, erwartet 1: %v", len(senke.begonnen), senke.begonnen)
	}
	uid := senke.begonnen[0]
	if len(senke.beendet) != 1 || senke.beendet[0] != uid {
		t.Errorf("beendet: %v", senke.beendet)
	}

	u, ok := senke.uebernahm[uid]
	if !ok {
		t.Fatalf("die Aufnahme kam nicht beim Alert an, sondern unter: %v", keysOf(senke.uebernahm))
	}
	if len(u.Dateien) != 2 {
		t.Fatalf("%d Dateien übernommen: %+v", len(u.Dateien), u.Dateien)
	}
	if u.Dateien[0].Codec != audio.CodecRoh || u.Dateien[1].Codec != audio.CodecMp3 {
		t.Errorf("Codecs: %+v", u.Dateien)
	}
	if u.Seconds <= 0 || u.SampleRate != 48000 || u.Channels != 2 {
		t.Errorf("Audioangaben fehlen: %+v", u)
	}
	if u.Channel != "5C" || u.SubChID != 7 {
		t.Errorf("Kanal/Subchannel: %q %d", u.Channel, u.SubChID)
	}

	// Und im Datensatz muss der Mitschnitt beim Alert auftauchen.
	sah := false
	for _, ab := range abschnitte {
		for _, al := range ab.Asa.Alerts {
			if al.Audio != nil && al.Audio.State == report.AudioGespeicht {
				sah = true
			}
		}
	}
	if !sah {
		t.Error("kein abgeschlossener Mitschnitt im Datensatz")
	}
}

// Ein Dateiname aus fremdem Prozess darf nie zu einem Pfad werden.
func TestDateinamenMitPfadanteilWerdenAbgelehnt(t *testing.T) {
	for _, name := range []string{"../etc/passwd", `a\b.dabp`, "a/b.dabp", "", ".", ".."} {
		if nameIstHarmlos(name) {
			t.Errorf("%q wurde durchgelassen", name)
		}
	}
	for _, name := range []string{"uid-5C-7.dabp", "20260830T121455Z-5C-13.mp3"} {
		if !nameIstHarmlos(name) {
			t.Errorf("%q wurde abgelehnt", name)
		}
	}
}

func keysOf(m map[string]audio.Uebernahme) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
