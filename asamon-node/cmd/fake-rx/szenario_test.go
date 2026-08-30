// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/josch0/asa-monitor/asamon-node/internal/record"
)

func streamPfad(name string) string {
	return filepath.Join("..", "..", "testdata", "streams", name)
}

// Der wichtigste Test dieses Programms: Der FIG-Packer muss dieselben Bytes
// erzeugen, die asamon-rx auspackt.
//
// Geprüft wird gegen tests/fixtures/fig0_15.fixtures aus asamon-rx — dort steht
// zu jeder handgebauten FIG-0/15-Instanz der asa-Record, den der Empfangsprozess
// daraus schreibt. Wer hier auseinanderläuft, erzeugt Testströme, die es on air
// nie gäbe, und prüft damit nichts.
func TestPackerGegenAsamonRxFixtures(t *testing.T) {
	f, err := os.Open(streamPfad("fig0_15.fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	geprueft := 0
	sc := bufio.NewScanner(f)
	name := ""
	for sc.Scan() {
		zeile := strings.TrimSpace(sc.Text())
		if after, ok := strings.CutPrefix(zeile, "name="); ok {
			name = after
			continue
		}
		if !strings.HasPrefix(zeile, "expect=") {
			continue
		}
		rec, err := record.ParseLine([]byte(strings.TrimPrefix(zeile, "expect=")))
		if err != nil || rec.Asa == nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		fig, err := ausRecord(rec.Asa)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := fig.rawHex(); got != rec.Asa.Raw {
			t.Errorf("%s: gepackt %s, asamon-rx meldet %s", name, got, rec.Asa.Raw)
			continue
		}
		geprueft++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if geprueft < 15 {
		t.Errorf("nur %d Fixtures geprüft; die Datei hat mehr", geprueft)
	}
}

// ausRecord baut die zu packende Instanz aus einem asa-Record zurück.
func ausRecord(a *record.Asa) (fig0_15, error) {
	f := fig0_15{heartbeat: a.Heartbeat, cn: a.Cn, oe: a.Oe, pd: a.PdSecondHalf}
	if a.Heartbeat {
		return f, nil
	}
	if a.Oe {
		v, err := parseEid(a.OtherEid)
		if err != nil {
			return f, err
		}
		f.otherEid = v
	} else {
		switch a.Phase {
		case "pre_trigger":
			f.phase = 0
		case "trigger":
			f.phase = 1
		case "sustain":
			f.phase = 2
		case "end":
			f.phase = 3
		default:
			return f, fmt.Errorf("unbekannte Phase %q", a.Phase)
		}
		if a.SubChID != nil {
			f.subChID = *a.SubChID
		}
		if a.Sec != nil {
			f.sec, f.hatSec = *a.Sec, true
		}
	}
	if a.Stage != "" {
		f.hatStatus = true
		f.stage = stageWert(a.Stage)
		if f.stage < 0 {
			return f, fmt.Errorf("unbekannte Stage %q", a.Stage)
		}
		if a.Iid != nil {
			f.iid = *a.Iid
		}
		if a.Last != nil {
			f.last = *a.Last
		}
	}
	if a.LocationCodes != "" {
		raw, err := hex.DecodeString(a.LocationCodes)
		if err != nil {
			return f, err
		}
		f.locationCodes = raw
	}
	return f, nil
}

func parseEid(s string) (int, error) {
	t := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	raw, err := hex.DecodeString(strings.Repeat("0", (4-len(t))%4) + t)
	if err != nil || len(raw) != 2 {
		return 0, fmt.Errorf("other_eid %q ist nicht lesbar", s)
	}
	return int(raw[0])<<8 | int(raw[1]), nil
}

func stageWert(s string) int {
	for i := range 8 {
		if stageName(i) == s {
			return i
		}
	}
	return -1
}

// Die Ströme in testdata/streams müssen zu dem passen, was der Erzeuger heute
// baut. Sonst prüfen die Golden-Tests gegen eine Aufzeichnung, die sich mit dem
// Code nicht mehr erklären lässt.
//
// Neu erzeugen: make streams
func TestStroemeSindAktuell(t *testing.T) {
	for _, s := range szenarien {
		t.Run(s.name, func(t *testing.T) {
			pfad := streamPfad(s.name + ".ndjson")
			raw, err := os.ReadFile(pfad)
			if err != nil {
				t.Fatalf("%v — neu erzeugen mit: make streams", err)
			}
			soll := strings.Join(s.baue(), "\n") + "\n"
			if string(raw) != soll {
				t.Errorf("%s ist nicht mehr das, was der Erzeuger baut (%d statt %d Byte) — neu erzeugen mit: make streams",
					pfad, len(raw), len(soll))
			}
		})
	}
}

// Jeder erzeugte Strom muss von der Eingangsseite gelesen werden können —
// lückenlos, ohne kaputte Zeilen, mit init als erster Zeile.
func TestStroemeSindLesbar(t *testing.T) {
	for _, s := range szenarien {
		t.Run(s.name, func(t *testing.T) {
			zeilen := s.baue()
			r := record.NewReader(strings.NewReader(strings.Join(zeilen, "\n") + "\n"))
			gelesen := slices.Collect(r.Alle())
			if err := r.Err(); err != nil {
				t.Fatal(err)
			}
			if len(gelesen) == 0 || gelesen[0].Kind != record.KindInit {
				t.Fatalf("die erste Zeile ist nicht init")
			}
			if len(gelesen) != len(zeilen) {
				t.Errorf("%d von %d Zeilen gelesen", len(gelesen), len(zeilen))
			}
			z := r.Zaehler()
			if z.KaputteZeilen != 0 || z.SeqLuecken != 0 || z.SeqRueckwaerts != 0 || z.UnbekannteRecords != 0 {
				t.Errorf("Zähler: %+v", z)
			}
		})
	}
}
