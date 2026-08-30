// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/loc"
)

// musterSumme ist die Prüfsumme über `bytes` Nullbytes — genau der Inhalt, mit
// dem fake-rx die Datei beim Abspielen anlegt (legeDateienAn in main.go). Sie
// stimmt also mit der Datei überein, statt eine Zahl zu erfinden, und ist bei
// jedem Lauf dieselbe: Die Ströme sollen byteweise wiederholbar entstehen.
func musterSumme(bytes int64) string {
	summe := sha256.Sum256(make([]byte, bytes))
	return hex.EncodeToString(summe[:])
}

// Echten ASA-Verkehr hat niemand aufgezeichnet. Die Ströme in testdata/streams
// werden deshalb hier erzeugt — nach TS 104 089 Annex E gepackt, mit
// Pre-Trigger, Trigger über 5 s, Sustain, End, mehrteiligem Alert-Set und
// OE-Verweis. Sobald es echte Mitschnitte gibt, treten sie **daneben**, nicht
// an ihre Stelle.
//
// Die Erzeugung gehört ins Repo und nicht in einen Einmalskript-Ordner: Ein
// Testfall, den niemand nachbauen kann, ist kein Testfall.

// startzeit ist der Nullpunkt aller Szenarien. Fest gewählt, damit die Ströme
// byteweise wiederholbar entstehen.
var startzeit = time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)

const (
	testEid       = "0x10FF"
	testEcc       = 224
	testEnsLabel  = "Bundesmux 1"
	testChannel   = "5C"
	testSubChID   = 7
	testBitrate   = 32
	fremdEid      = "0x20AB"
	tlmVersatzNs  = 123456789
	asaVersatzMs  = 200
	fibProSekunde = 125
)

// strom baut einen NDJSON-Strom auf.
type strom struct {
	zeilen []string
	seq    uint64
}

func (s *strom) schreibe(typ string, ts time.Time, felder map[string]any) {
	// Die drei gemeinsamen Felder stehen zuerst; danach folgt der Rest in
	// stabiler Reihenfolge, damit der Strom byteweise wiederholbar entsteht.
	var b strings.Builder
	b.WriteString(`{"type":`)
	b.WriteString(jsonWert(typ))
	b.WriteString(`,"seq":`)
	fmt.Fprintf(&b, "%d", s.seq)
	b.WriteString(`,"ts":`)
	b.WriteString(jsonWert(ts.UTC().Format(time.RFC3339Nano)))

	schluessel := make([]string, 0, len(felder))
	for k := range felder {
		schluessel = append(schluessel, k)
	}
	slices.Sort(schluessel)
	for _, k := range schluessel {
		b.WriteString(",")
		b.WriteString(jsonWert(k))
		b.WriteString(":")
		b.WriteString(jsonWert(felder[k]))
	}
	b.WriteString("}")

	s.zeilen = append(s.zeilen, b.String())
	s.seq++
}

func jsonWert(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func (s *strom) init(ts time.Time) {
	s.schreibe("init", ts, map[string]any{
		"format_version": 1,
		"channel":        "5C",
		"freq_hz":        178352000,
		"device":         "rawfile",
		"device_serial":  "",
		"rx_version":     "0.1.0",
		"rx_commit":      "abc1234",
		"welle_commit":   "fe06fad",
	})
}

func (s *strom) ens(ts time.Time) {
	s.schreibe("ens", ts, map[string]any{
		"eid":   testEid,
		"ecc":   testEcc,
		"label": testEnsLabel,
		"services": []map[string]any{{
			"sid":   "0x0D3110AB",
			"label": "ASA DE",
			"components": []map[string]any{{
				"subch_id": testSubChID, "start_addr": 128, "size": 48,
				"protection": "EEP 2-A", "bitrate": testBitrate,
			}},
		}},
	})
}

// tlm schreibt die Telemetrie einer Sekunde. crcFehler steuert, wie schlecht
// der Empfang aussieht — die CRC-Quote ist die Zahl, die "Ensemble schweigt"
// von "wir empfangen schlecht" trennt.
func (s *strom) tlm(ts, ensZeit time.Time, crcFehler int) {
	s.schreibe("tlm", ts, map[string]any{
		"snr":            12.4,
		"sync":           true,
		"signal":         true,
		"freq_corr":      map[string]any{"fine": -3, "coarse": 0},
		"fib_total":      fibProSekunde,
		"fib_crc_err":    crcFehler,
		"dropped":        0,
		"parse_errors":   0,
		"eid":            testEid,
		"ens_time":       ensZeit.UTC().Format(time.RFC3339),
		"ens_offset_min": 120,
	})
}

func (s *strom) asa(ts time.Time, f fig0_15, extra map[string]any) {
	felder := map[string]any{
		"heartbeat":      f.heartbeat,
		"cn":             f.cn,
		"oe":             f.oe,
		"pd_second_half": f.pd,
		"raw":            f.rawHex(),
	}
	maps.Copy(felder, extra)
	s.schreibe("asa", ts, felder)
}

// heartbeat schreibt die Ruheform: leeres Type-0-Feld, C/N = 1, OE = 0.
func (s *strom) heartbeat(ts, ensSek time.Time) {
	s.asa(ts, fig0_15{heartbeat: true, cn: true, pd: ensSek.Second() >= 30}, nil)
}

// takt gibt die Zeitstempel der i-ten Sekunde: Ensemble-Sekunde, tlm-Zeit,
// asa-Zeit.
func takt(i int) (ensSek, tlmTs, asaTs time.Time) {
	ensSek = startzeit.Add(time.Duration(i) * time.Second)
	tlmTs = ensSek.Add(tlmVersatzNs)
	asaTs = tlmTs.Add(asaVersatzMs * time.Millisecond)
	return
}

// warngebiet packt ein Warngebiet aus Location Codes: zwei Rechtecke über
// Berlin, damit die Geometrie im Datensatz nachprüfbar ist.
func warngebiet(nff uint8) []byte {
	berlin, err := loc.Encode(52.520008, 13.404954)
	if err != nil {
		panic(err)
	}
	raw, err := loc.EncodeLocationCodes([]loc.Code{berlin}, []uint8{nff}, nil)
	if err != nil {
		panic(err)
	}
	return raw
}

// szenario ist ein benannter Strom.
type szenario struct {
	name         string
	beschreibung string
	baue         func() []string
}

var szenarien = []szenario{
	{"heartbeat-10min", "zehn Minuten Ruhezustand: ein Heartbeat je Sekunde", baueHeartbeat},
	{"alert-einfach", "Pre-Trigger, Trigger über 5 s, Sustain, End — der Regelfall", baueAlertEinfach},
	{"alert-set-3", "Trigger mit einem Alert-Set über drei Instanzen (NFF 2,1,0)", baueAlertSet3},
	{"einstieg-sustain", "der Alert wird erst in der Sustain-Phase sichtbar", baueEinstiegSustain},
	{"alert-abgebrochen", "Trigger, dann Stille — der Alert läuft in die 30-s-Frist", baueAlertAbgebrochen},
	{"oe-verweis", "ein OE-Alert auf ein anderes Ensemble", baueOeVerweis},
	{"stage-test", "ein Test-Alert (Stage 7), den Consumer-Geräte ignorieren", baueStageTest},
	{"alert-audio", "Alert mit Mitschnitt: aud-Records während der Trigger- und Sustain-Phase", baueAlertAudio},
	{"heartbeat-luecke", "Heartbeats mit einer Lücke von fünf Sekunden und schlechtem Empfang", baueHeartbeatLuecke},
}

func szenarioNamen() []string {
	out := make([]string, 0, len(szenarien))
	for _, s := range szenarien {
		out = append(out, s.name)
	}
	return out
}

func baueSzenario(name string) ([]string, error) {
	for _, s := range szenarien {
		if s.name == name {
			return s.baue(), nil
		}
	}
	return nil, fmt.Errorf("unbekanntes Szenario %q; bekannt sind: %s", name, strings.Join(szenarioNamen(), ", "))
}

// kopf schreibt init und ens und gibt den vorbereiteten Strom.
func kopf() *strom {
	s := &strom{}
	s.init(startzeit.Add(-500 * time.Millisecond))
	s.ens(startzeit.Add(-100 * time.Millisecond))
	return s
}

func baueHeartbeat() []string {
	s := kopf()
	for i := range 600 {
		ensSek, tlmTs, asaTs := takt(i)
		s.tlm(tlmTs, ensSek, 2)
		s.heartbeat(asaTs, ensSek)
	}
	return s.zeilen
}

func baueHeartbeatLuecke() []string {
	s := kopf()
	for i := range 40 {
		ensSek, tlmTs, asaTs := takt(i)
		// In den Sekunden 20 bis 24 bricht der Empfang ein: viele CRC-Fehler,
		// kein Heartbeat. Genau diese Unterscheidung trägt die Abdeckungskarte.
		schlecht := i >= 20 && i < 25
		crc := 2
		if schlecht {
			crc = 118
		}
		s.tlm(tlmTs, ensSek, crc)
		if !schlecht {
			s.heartbeat(asaTs, ensSek)
		}
	}
	return s.zeilen
}

// alertPlan beschreibt den zeitlichen Ablauf eines Alerts in Sekunden ab dem
// Beginn des Stroms.
type alertPlan struct {
	dauer         int // Gesamtlänge des Stroms in Sekunden
	preTriggerVon int
	preTriggerBis int
	triggerVon    int
	triggerBis    int
	sustainBis    int
	endBis        int
	stage         int
	iid           int
	instanzen     int // Instanzen je Trigger-Sekunde (Alert-Set)
	mitAudio      bool
	abbruch       bool // kein Sustain, kein End — nur Stille
}

func baueAlert(p alertPlan) []string {
	s := kopf()
	audioChunk := 0

	for i := 0; i < p.dauer; i++ {
		ensSek, tlmTs, asaTs := takt(i)
		s.tlm(tlmTs, ensSek, 2)
		pd := ensSek.Second() >= 30

		switch {
		case i >= p.preTriggerVon && i < p.preTriggerBis:
			s.asa(asaTs, fig0_15{
				pd: pd, phase: 0, subChID: testSubChID,
				sec: p.triggerVon % 60, hatSec: true,
				hatStatus: true, last: true, stage: p.stage, iid: p.iid,
			}, map[string]any{
				"phase": "pre_trigger", "subch_id": testSubChID,
				"sec": p.triggerVon % 60, "stage": stageName(p.stage), "iid": p.iid, "last": true,
			})

		case i >= p.triggerVon && i < p.triggerBis:
			for n := 0; n < p.instanzen; n++ {
				nff := p.instanzen - 1 - n
				codes := warngebiet(uint8(nff))
				letzte := nff == 0
				ts := asaTs.Add(time.Duration(n*50) * time.Millisecond)
				s.asa(ts, fig0_15{
					pd: pd, phase: 1, subChID: testSubChID,
					hatStatus: true, last: letzte, stage: p.stage, iid: p.iid,
					locationCodes: codes,
				}, map[string]any{
					"phase": "trigger", "subch_id": testSubChID,
					"stage": stageName(p.stage), "iid": p.iid, "last": letzte,
					"nff": nff, "location_codes": hexOf(codes),
				})
			}

		case !p.abbruch && i >= p.triggerBis && i < p.sustainBis:
			s.asa(asaTs, fig0_15{cn: true, pd: pd, phase: 2, subChID: testSubChID}, map[string]any{
				"phase": "sustain", "subch_id": testSubChID,
			})

		case !p.abbruch && i >= p.sustainBis && i < p.endBis:
			s.asa(asaTs, fig0_15{cn: true, pd: pd, phase: 3, subChID: testSubChID}, map[string]any{
				"phase": "end", "subch_id": testSubChID,
			})

		default:
			s.heartbeat(asaTs, ensSek)
		}

		// Ein einziger aud-Record am Ende der Aufnahme — so, wie asamon-rx es
		// seit dem 30.08.2026 hält: Die Bytes liegen als Datei im
		// Ablageordner, der Strom nennt sie nur noch.
		//
		// Ohne alert_uid, weil fake-rx keine kennt: Es spielt eine
		// Aufzeichnung ab und hat kein REC gesehen. Damit prüft dieser Strom
		// zugleich den Notnagel im Knoten, der die Aufnahme dann über den
		// Subchannel dem laufenden Alert zuordnet.
		if p.mitAudio && i == p.sustainBis {
			sekunden := float64(p.sustainBis + 1 - p.triggerVon)
			roh := int64(sekunden) * testBitrate * 1000 / 8
			start := asaTs.Add(-time.Duration(sekunden) * time.Second)
			basis := start.UTC().Format("20060102T150405Z") +
				"-" + testChannel + "-" + strconv.Itoa(testSubChID)
			s.schreibe("aud", asaTs.Add(300*time.Millisecond), map[string]any{
				"subch_id":    testSubChID,
				"dir":         "/var/lib/asamon/audio",
				"started":     start.UTC().Format(time.RFC3339Nano),
				"seconds":     sekunden,
				"truncated":   false,
				"sample_rate": 48000,
				"channels":    2,
				"mode":        "HE-AACv2",
				"mp3_bitrate": 64,
				"files": []map[string]any{
					{"name": basis + ".dabp", "codec": "dabp", "bytes": roh,
						"sha256": musterSumme(roh)},
					{"name": basis + ".mp3", "codec": "mp3", "bytes": roh * 3 / 4,
						"sha256": musterSumme(roh * 3 / 4)},
				},
			})
			audioChunk++
		}
	}
	return s.zeilen
}

func baueAlertEinfach() []string {
	return baueAlert(alertPlan{
		dauer: 50, preTriggerVon: 15, preTriggerBis: 18,
		triggerVon: 20, triggerBis: 25, sustainBis: 35, endBis: 37,
		stage: 0, iid: 3, instanzen: 1,
	})
}

func baueAlertSet3() []string {
	return baueAlert(alertPlan{
		dauer: 50, preTriggerVon: 15, preTriggerBis: 18,
		triggerVon: 20, triggerBis: 25, sustainBis: 35, endBis: 37,
		stage: 0, iid: 4, instanzen: 3,
	})
}

func baueStageTest() []string {
	return baueAlert(alertPlan{
		dauer: 50, preTriggerVon: 15, preTriggerBis: 18,
		triggerVon: 20, triggerBis: 25, sustainBis: 35, endBis: 37,
		stage: 7, iid: 0, instanzen: 1,
	})
}

func baueAlertAudio() []string {
	return baueAlert(alertPlan{
		dauer: 60, preTriggerVon: 15, preTriggerBis: 18,
		triggerVon: 20, triggerBis: 25, sustainBis: 35, endBis: 37,
		stage: 0, iid: 5, instanzen: 1, mitAudio: true,
	})
}

func baueAlertAbgebrochen() []string {
	// Trigger über 5 s, danach nur noch Telemetrie: kein End, keine
	// Heartbeats. Nach StilleBisAbbruch (30 s) muss der Alert als abgebrochen
	// gelten.
	s := kopf()
	for i := range 60 {
		ensSek, tlmTs, asaTs := takt(i)
		s.tlm(tlmTs, ensSek, 2)
		pd := ensSek.Second() >= 30
		switch {
		case i < 10:
			s.heartbeat(asaTs, ensSek)
		case i >= 10 && i < 15:
			codes := warngebiet(0)
			s.asa(asaTs, fig0_15{
				pd: pd, phase: 1, subChID: testSubChID,
				hatStatus: true, last: true, stage: 1, iid: 6,
				locationCodes: codes,
			}, map[string]any{
				"phase": "trigger", "subch_id": testSubChID,
				"stage": "level1_update", "iid": 6, "last": true,
				"nff": 0, "location_codes": hexOf(codes),
			})
		}
	}
	return s.zeilen
}

func baueEinstiegSustain() []string {
	// Die gesamte Trigger-Phase fällt durch CRC-Fehler aus; der Alert wird
	// erst in sustain sichtbar. Ein gültiger, aber unvollständiger Befund.
	s := kopf()
	for i := range 45 {
		ensSek, tlmTs, asaTs := takt(i)
		schlecht := i >= 18 && i < 25
		crc := 2
		if schlecht {
			crc = 110
		}
		s.tlm(tlmTs, ensSek, crc)
		pd := ensSek.Second() >= 30
		switch {
		case i < 18:
			s.heartbeat(asaTs, ensSek)
		case schlecht:
			// nichts: die Trigger-Phase geht im Rauschen unter
		case i >= 25 && i < 35:
			s.asa(asaTs, fig0_15{cn: true, pd: pd, phase: 2, subChID: testSubChID}, map[string]any{
				"phase": "sustain", "subch_id": testSubChID,
			})
		case i >= 35 && i < 37:
			s.asa(asaTs, fig0_15{cn: true, pd: pd, phase: 3, subChID: testSubChID}, map[string]any{
				"phase": "end", "subch_id": testSubChID,
			})
		default:
			s.heartbeat(asaTs, ensSek)
		}
	}
	return s.zeilen
}

func baueOeVerweis() []string {
	// OE-Signalisierung ist nach TS 104 089 §6.5.1 stets Trigger und trägt
	// kein Phasenfeld. Sie ist oft das früheste Signal im ganzen Netz.
	s := kopf()
	eidWert := 0x20AB
	for i := range 45 {
		ensSek, tlmTs, asaTs := takt(i)
		s.tlm(tlmTs, ensSek, 2)
		pd := ensSek.Second() >= 30
		if i >= 20 && i < 30 {
			codes := warngebiet(0)
			s.asa(asaTs, fig0_15{
				oe: true, pd: pd, otherEid: eidWert,
				hatStatus: true, last: true, stage: 0, iid: 2,
				locationCodes: codes,
			}, map[string]any{
				"other_eid": fremdEid, "stage": "level1_start", "iid": 2, "last": true,
				"nff": 0, "location_codes": hexOf(codes),
			})
			continue
		}
		s.heartbeat(asaTs, ensSek)
	}
	return s.zeilen
}

func stageName(v int) string {
	namen := []string{"level1_start", "level1_update", "level1_repeat", "level1_critical",
		"level2_start", "level2_update", "level2_repeat", "test"}
	if v < 0 || v >= len(namen) {
		return ""
	}
	return namen[v]
}

func hexOf(b []byte) string {
	const ziffern = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, ziffern[v>>4], ziffern[v&0x0F])
	}
	return string(out)
}
