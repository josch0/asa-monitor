// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/hashes"
	"github.com/josch0/asa-monitor/asamon-node/internal/loc"
	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// verarbeiteAlertInstanz ist der Phasenautomat.
//
//	 (kein Alert)
//	      │ pre_trigger              ┌───────────────────────────────┐
//	      ▼                          │                               │
//	┌───────────┐   trigger    ┌──────────┐   sustain   ┌─────────┐  │ end
//	│ PRETRIGGER├─────────────▶│ TRIGGER  ├────────────▶│ SUSTAIN ├──┘
//	└───────────┘              └──────────┘             └─────────┘
//	      │                          │                       │
//	      └──── Stille > 30 s ───────┴───────────────────────┴──────▶ ABGEBROCHEN
//
// Der Einstieg ist in jeder Phase möglich: Bei schlechtem Empfang kann die
// gesamte Trigger-Phase durch CRC-Fehler ausfallen, und der Alert wird erst in
// sustain sichtbar. Das ist ein gültiger, aber unvollständiger Befund —
// entered_at_phase hält ihn fest.
func (c *Kanal) verarbeiteAlertInstanz(a *asaFelder, ensSek time.Time) {
	phase := c.phaseVon(a)
	stage := c.stageVon(a)

	al := c.findeOderLege(a, phase, ensSek)
	if al == nil {
		return
	}

	al.zuletztGesehen = ensSek
	al.zuletztStrom = c.jetzt
	if stage != StageKeine {
		al.stage = stage
		al.test = stage.IstTest()
	}
	if a.Iid != nil && !al.iidBekannt {
		al.iid = *a.Iid
		al.iidBekannt = true
	}
	if a.SubChID != nil {
		al.subChID = *a.SubChID
		al.subChBekannt = true
	}
	if a.Stage != "" || a.Last != nil || a.Iid != nil {
		al.hatStatusInstanz = true
	}

	// Der Pre-Trigger nennt die Sekunde, zu der die Trigger-Phase startet.
	// Aus ihr wird die Startminute des alert_uid — und nur deshalb kommen
	// zwei Knoten, von denen einer den Pre-Trigger sah und der andere nicht,
	// zum selben Wert.
	if phase == PhasePreTrigger && a.Sec != nil && al.einstiegPhase == PhasePreTrigger {
		al.startEns = triggerStart(ensSek, *a.Sec)
	}
	c.setzeUID(al)

	if al.wechsleZu(phase, ensSek, a.Sec) {
		c.wecke("Phasenwechsel nach " + phase.String())
	}

	c.satzInstanz(al, a, ensSek)
	c.steuereAudio(al, phase, ensSek)

	if phase == PhaseEnd {
		al.schliesse(GrundEnd, ensSek)
		c.planeAudioStop(al)
		c.wecke("Alert beendet")
	}
}

// phaseVon bestimmt die Phase einer Instanz.
//
// OE-Signalisierung ist nach TS 104 089 §6.5.1 stets Trigger und trägt kein
// Phasenfeld. Ein OE-Alert endet deshalb nie mit einer End-Phase, sondern
// läuft in die Stille-Frist — das ist der Norm geschuldet, kein Fehler.
func (c *Kanal) phaseVon(a *asaFelder) Phase {
	if a.Oe {
		return PhaseTrigger
	}
	p, ok := PhaseAus(a.Phase)
	if !ok {
		c.melde("unbekannter Phasenwert %q (phase_raw %v) — die Norm wurde erweitert", a.Phase, a.PhaseRaw)
	}
	return p
}

func (c *Kanal) stageVon(a *asaFelder) Stage {
	s, ok := StageAus(a.Stage)
	if !ok {
		c.melde("unbekannter Stage-Wert %q (stage_raw %v) — die Norm wurde erweitert", a.Stage, a.StageRaw)
	}
	return s
}

// findeOderLege sucht den verfolgten Alert zu dieser Instanz oder legt ihn an.
//
// Der Schlüssel ist (oe, id, iid). Bei sustain und end gibt es kein
// Status-Feld und damit keinen IId — solche Instanzen werden dem offenen Alert
// mit passender (oe, id) zugeordnet.
func (c *Kanal) findeOderLege(a *asaFelder, phase Phase, ensSek time.Time) *verfolgterAlert {
	oe := a.Oe
	var id, warnendeEid string
	kanalEid := c.kanalEid()

	if oe {
		if a.OtherEid == "" {
			c.melde("OE-Instanz ohne other_eid — sie ist nicht zuzuordnen")
			return nil
		}
		warnendeEid = hashes.Hex(a.OtherEid, 4)
		id = warnendeEid
	} else {
		warnendeEid = kanalEid
		if a.SubChID == nil {
			c.melde("Instanz ohne subch_id in Phase %s — sie ist nicht zuzuordnen", phase)
			return nil
		}
		id = itoa(*a.SubChID)
	}

	iid := IidUnbekannt
	if a.Iid != nil {
		iid = *a.Iid
	}

	// Erste Wahl: genaue Übereinstimmung unter den laufenden Alerts.
	var ohneIid, nachklang *verfolgterAlert
	var offene []*verfolgterAlert
	for _, al := range c.alerts {
		if al.oe != oe || al.id() != id {
			continue
		}
		if al.geschlossen {
			// Die End-Phase läuft über zwei Sekunden; ihre zweite Instanz
			// gehört noch zu diesem Alert und darf keinen neuen anlegen.
			if ensSek.Sub(al.zuletztGesehen) <= Nachklangfrist &&
				(iid == IidUnbekannt || al.iid == iid) {
				nachklang = al
			}
			continue
		}
		offene = append(offene, al)
		if iid != IidUnbekannt && al.iid == iid {
			return al
		}
		if al.iid == IidUnbekannt {
			ohneIid = al
		}
	}
	if len(offene) == 0 && nachklang != nil {
		return nachklang
	}

	if iid == IidUnbekannt {
		switch len(offene) {
		case 0:
			// Einstieg ohne Status-Feld: sustain oder end.
		case 1:
			return offene[0]
		default:
			c.melde("%d gleichzeitige Alerts auf %s — die Instanz ohne Status-Feld wird dem zuletzt gesehenen zugeordnet", len(offene), id)
			neuester := offene[0]
			for _, al := range offene[1:] {
				if al.zuletztGesehen.After(neuester.zuletztGesehen) {
					neuester = al
				}
			}
			return neuester
		}
	} else if ohneIid != nil {
		// Ein in sustain begonnener Alert bekommt seinen IId nachgereicht.
		ohneIid.iid = iid
		ohneIid.iidBekannt = true
		return ohneIid
	} else if len(offene) > 0 && !oe {
		// Ein Ensemble führt höchstens eine eigene Warnung. Ein zweiter
		// eigener Alert mit anderem IId ist eine meldenswerte Auffälligkeit,
		// kein Programmfehler — beide werden verfolgt.
		c.melde("zweiter eigener Alert auf Subchannel %s mit IId %d, während IId %d noch läuft", id, iid, offene[0].iid)
	}

	al := &verfolgterAlert{
		oe:             oe,
		kanalEid:       kanalEid,
		warnendeEid:    warnendeEid,
		iid:            iid,
		iidBekannt:     iid != IidUnbekannt,
		phase:          PhaseKeine,
		einstiegPhase:  phase,
		startEns:       ensSek,
		erstGesehen:    ensSek,
		zuletztGesehen: ensSek,
		zuletztStrom:   c.jetzt,
	}
	if a.SubChID != nil {
		al.subChID = *a.SubChID
		al.subChBekannt = true
	}
	c.alerts = append(c.alerts, al)
	c.setzeUID(al)
	c.wecke("neuer Alert")

	if oe {
		// Der OE-Alert wird in jedem Fall vollständig gemeldet, auch wenn er
		// lokal nicht auflösbar ist. Er ist oft das früheste Signal im ganzen
		// Netz. Ist das warnende Ensemble auf einem anderen Kanal dieses
		// Knotens zu empfangen, geht der dortige Recorder sofort scharf —
		// ohne Serverrunde.
		if c.s.OeVerweis != nil && c.s.OeVerweis(warnendeEid) {
			c.s.Log.Info("OE-Verweis lokal aufgelöst", "channel", c.k.Channel, "other_eid", warnendeEid)
		}
	}
	return al
}

// setzeUID berechnet den alert_uid neu. Er hängt an der warnenden EId, am IId
// und an der Startminute — alles drei kann sich im Verlauf noch schärfen.
func (c *Kanal) setzeUID(al *verfolgterAlert) {
	al.uid = hashes.Alert(al.warnendeEid, al.iid, al.iidBekannt, al.startEns)
	// Sicher ist der uid nur, wenn der Knoten den Beginn gesehen hat. Wer erst
	// in sustain einsteigt, kennt weder Startminute noch IId.
	al.uidSicher = al.iidBekannt &&
		(al.einstiegPhase == PhasePreTrigger || al.einstiegPhase == PhaseTrigger)
}

// triggerStart rechnet aus dem Sekundenzähler des Pre-Triggers den Beginn der
// Trigger-Phase aus.
//
// sec == 63 ist Sonderwert: Start bei Sekunde 0, 5 s Triggerdauer.
func triggerStart(ensSek time.Time, sec int) time.Time {
	ziel := sec
	if sec == 63 {
		ziel = 0
	}
	if ziel < 0 || ziel > 59 {
		return ensSek
	}
	kandidat := ensSek.Truncate(time.Minute).Add(time.Duration(ziel) * time.Second)
	for !kandidat.After(ensSek) {
		kandidat = kandidat.Add(time.Minute)
	}
	// Der Pre-Trigger läuft 5 s vor dem Trigger-Start. Liegt der Kandidat
	// weiter weg, ist der Sekundenzähler nicht plausibel — dann bleibt es beim
	// beobachteten Zeitpunkt.
	if kandidat.Sub(ensSek) > time.Minute {
		return ensSek
	}
	return kandidat
}

// satzInstanz führt die Alert-Sets: 1..4 Instanzen, die zusammen das
// vollständige Warngebiet beschreiben.
func (c *Kanal) satzInstanz(al *verfolgterAlert, a *asaFelder, ensSek time.Time) {
	if a.LocationCodes == "" {
		if a.Nff != nil {
			c.melde("Instanz mit nff %d, aber ohne location_codes", *a.Nff)
		}
		return
	}
	al.hatLocationCodes = true

	nff := 0
	if a.Nff != nil {
		nff = *a.Nff
	} else {
		c.melde("location_codes ohne nff — das Set lässt sich nicht auf Vollständigkeit prüfen")
	}

	satz := al.offenerSatz
	neuAnfangen := satz == nil || satz.fertig || nff != satz.letztesNff-1
	if satz != nil && neuAnfangen && !satz.fertig {
		// Das vorige Set wurde nie vollständig. Es wird trotzdem gemeldet —
		// alle empfangenen Instanzen sind ein Befund.
		satz.unvollstaendig = true
		c.schliesseSatz(al, satz)
		c.melde("Alert-Set abgebrochen: nff sprang von %d auf %d", satz.letztesNff, nff)
	}
	if neuAnfangen {
		satz = &alertSatz{begonnen: c.jetzt, erwartet: nff + 1, letztesNff: nff + 1}
		al.offenerSatz = satz
	}

	satz.instanzen++
	satz.letztesNff = nff
	satz.bytesHex += strings.ToLower(a.LocationCodes)
	if satz.instanzen > MaxInstanzen {
		c.melde("Alert-Set hat %d Instanzen, die Norm erlaubt höchstens %d", satz.instanzen, MaxInstanzen)
		satz.unvollstaendig = true
		c.schliesseSatz(al, satz)
		return
	}
	if nff == 0 {
		satz.fertig = true
		c.schliesseSatz(al, satz)
	}
}

// schliesseSatz dekodiert die gesammelten Location Codes und macht das Set zum
// aktuellen Warngebiet.
func (c *Kanal) schliesseSatz(al *verfolgterAlert, satz *alertSatz) {
	if al.offenerSatz == satz {
		al.offenerSatz = nil
	}
	raw, err := hex.DecodeString(satz.bytesHex)
	if err != nil {
		satz.fehler = "location_codes sind kein gültiges Hex: " + err.Error()
		c.melde("%s", satz.fehler)
		al.letzterSatz = satz
		return
	}
	satz.bytes = raw
	if len(raw) > loc.MaxLocationBytes && satz.erwartet == 1 {
		c.melde("%d Byte Location Codes in einer Instanz, die Norm erlaubt %d", len(raw), loc.MaxLocationBytes)
	}
	codes, err := loc.DecodeLocationCodes(raw)
	if err != nil {
		// Der Alert wird trotzdem gemeldet: raw bleibt der Beleg, aus dem sich
		// jede Deutung nachträglich zurückrechnen lässt.
		satz.fehler = err.Error()
		c.melde("Warngebiet nicht deutbar: %v", err)
	}
	satz.codes = codes
	al.letzterSatz = satz
}

// steuereAudio schaltet den Mitschnitt zu.
//
// Beim Pre-Trigger geschieht nichts: Der Vorlauf ist ein Komfortgewinn, keine
// Voraussetzung — und ein früher Start schneidet reguläres Programm mit. Nur
// wenn ein OE-Verweis eines anderen Kanals diesen Kanal in Bereitschaft
// versetzt hat, geht der Recorder schon im Pre-Trigger scharf.
func (c *Kanal) steuereAudio(al *verfolgterAlert, phase Phase, jetzt time.Time) {
	if !c.k.AudioAktiv || al.oe || !al.subChBekannt {
		return
	}
	if !al.audioLaeuft.IsZero() {
		return
	}
	bereit := jetzt.Before(c.bereitschaftBis)
	switch phase {
	case PhaseTrigger:
	case PhasePreTrigger:
		if !bereit {
			return
		}
	case PhaseKeine, PhaseSustain, PhaseEnd, PhaseUnbekannt:
		return
	default:
		return
	}

	al.audioLaeuft = c.jetzt
	al.audioBegonnen = c.jetzt
	al.audioStopBei = time.Time{}
	// Die alert_uid geht mit: Dann benennt asamon-rx die Dateien von
	// vornherein so, wie der Knoten sie kennt, und niemand muss hinterher
	// umbenennen. Gedeutet wird sie dort nicht — asamon-rx kennt kein ASA.
	c.sende("REC " + itoa(al.subChID) + " " + al.uid)
	if c.s.Audio != nil {
		c.s.Audio.Beginne(al.uid, c.k.Channel, al.subChID, jetzt, c.bitrateVon(al.subChID))
	}
	c.s.Log.Debug("REC gesendet", "subch_id", al.subChID, "alert_uid", al.uid)
}

// planeAudioStop setzt den Nachlauf. Die Frist läuft in Stromzeit — wie jede
// andere auch.
func (c *Kanal) planeAudioStop(al *verfolgterAlert) {
	if al.audioLaeuft.IsZero() {
		return
	}
	al.audioStopBei = c.jetzt.Add(c.k.PostRoll)
}

func (c *Kanal) stoppeAudio(al *verfolgterAlert, abgeschnitten bool) {
	if al.audioLaeuft.IsZero() {
		return
	}
	al.audioLaeuft = time.Time{}
	al.audioStopBei = time.Time{}
	al.audioAbgeschnitten = al.audioAbgeschnitten || abgeschnitten
	if al.subChBekannt {
		c.sende("STOP " + itoa(al.subChID))
	}
	if c.s.Audio != nil {
		c.s.Audio.Beende(al.uid, al.audioAbgeschnitten)
	}
	c.s.Log.Debug("STOP gesendet", "alert_uid", al.uid, "truncated", al.audioAbgeschnitten)
}

// pruefeFristen treibt alles voran, was von der Zeit abhängt.
func (c *Kanal) pruefeFristen() {
	jetzt := c.jetzt
	if jetzt.IsZero() {
		return
	}
	for _, al := range c.alerts {
		// Ein Alert-Set, das länger als SatzFrist offen ist, wird
		// abgeschlossen. Bei Sekundenzähler 59 bricht der Sender die
		// Alert-Group ohnehin ab.
		if s := al.offenerSatz; s != nil && jetzt.Sub(s.begonnen) > SatzFrist {
			s.unvollstaendig = true
			c.schliesseSatz(al, s)
			c.melde("Alert-Set nach %s unvollständig abgeschlossen (%d von %d Instanzen)",
				SatzFrist, s.instanzen, s.erwartet)
		}

		if !al.geschlossen && jetzt.Sub(al.zuletztStrom) > StilleBisAbbruch {
			// Der gemeldete Zeitpunkt bleibt Ensemble-Zeit; geprüft wird in
			// Stromzeit.
			al.schliesse(GrundTimeout, al.zuletztGesehen.Add(StilleBisAbbruch))
			c.stoppeAudio(al, false)
			c.melde("Alert %s nach %s Stille als abgebrochen abgeschlossen", kurz(al.uid), StilleBisAbbruch)
			c.wecke("Alert abgebrochen")
			continue
		}

		if !al.audioLaeuft.IsZero() {
			if c.k.MaxAudioSekunden > 0 &&
				jetzt.Sub(al.audioLaeuft) >= time.Duration(c.k.MaxAudioSekunden)*time.Second {
				c.melde("Mitschnitt nach %d s abgebrochen (audio.max_seconds)", c.k.MaxAudioSekunden)
				c.stoppeAudio(al, true)
			} else if !al.audioStopBei.IsZero() && !jetzt.Before(al.audioStopBei) {
				c.stoppeAudio(al, false)
			}
		}
	}
}

// SetzeBereitschaft versetzt den Kanal in Bereitschaft: Ein anderer Kanal
// dieses Knotens hat einen OE-Verweis auf das hier empfangene Ensemble
// gesehen. Der Recorder geht dann bereits beim Pre-Trigger scharf, ohne auf
// den vollständigen Alert-Set zu warten.
func (c *Kanal) SetzeBereitschaft(bis time.Time) {
	if bis.After(c.bereitschaftBis) {
		c.bereitschaftBis = bis
	}
}

func (c *Kanal) sende(cmd string) {
	if c.s.Kommando != nil {
		c.s.Kommando(cmd)
	}
}

func (c *Kanal) wecke(grund string) {
	if c.s.Wecke != nil {
		c.s.Wecke(grund)
	}
}

// kanalEid gibt die EId des empfangenen Ensembles, normalisiert.
func (c *Kanal) kanalEid() string {
	if c.ens != nil && c.ens.Eid != "" {
		return hashes.Hex(c.ens.Eid, 4)
	}
	if c.eidAusTlm != "" {
		return hashes.Hex(c.eidAusTlm, 4)
	}
	return ""
}

// bitrateVon sucht die Bitrate einer Komponente — sie dient nur der Schätzung
// der Aufnahmedauer und ist deshalb entbehrlich.
func (c *Kanal) bitrateVon(subChID int) int {
	if c.ens == nil {
		return 0
	}
	for _, s := range c.ens.Services {
		for _, k := range s.Komponenten {
			if k.SubChID == subChID {
				return k.Bitrate
			}
		}
	}
	return 0
}

var _ = report.AudioKeins
