// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/hex"
	"fmt"
)

// Dieser Packer baut FIG-0/15-Instanzen nach ETSI TS 104 089, Annex E — die
// Gegenrichtung zu dem, was der welle.io-Patch auspackt.
//
// Er ist der Grund, warum die synthetischen Ströme etwas taugen: Das Feld raw
// darin ist nicht ausgedacht, sondern nach der Norm gepackt, und asamon-node
// bekommt genau das zu sehen, was auch on air stünde. Die Fixtures aus
// asamon-rx dienen als Gegenprobe (siehe szenario_test.go).
//
//	Byte 0      FIG-Header: FIG-Typ (3 bit) + Länge (5 bit)
//	Byte 1      FIG-0-Header: C/N (1) + OE (1) + P/D (1) + Extension (5) = 15
//	ab Byte 2   Type-0-Feld: Id-Feld, Status-Feld, Location Codes

// fig0_15 ist eine zu packende FIG-0/15-Instanz.
type fig0_15 struct {
	heartbeat bool
	cn        bool
	oe        bool
	pd        bool // Sekundenhälfte, nicht Programme/Data

	phase    int // 0 pre_trigger, 1 trigger, 2 sustain, 3 end
	subChID  int
	sec      int // nur bei phase 0; 63 ist Sonderwert
	hatSec   bool
	otherEid int // nur bei oe

	hatStatus bool
	last      bool
	stage     int // 0..7
	iid       int

	locationCodes []byte
}

func (f fig0_15) packe() []byte {
	var feld []byte

	if !f.heartbeat {
		if f.oe {
			feld = append(feld, byte(f.otherEid>>8), byte(f.otherEid))
		} else {
			feld = append(feld, byte(f.phase&0x03)<<6|byte(f.subChID&0x3F))
			if f.hatSec {
				feld = append(feld, byte(f.sec&0x3F)) // Rfa (2 bit) ist 0
			}
		}
		if f.hatStatus {
			var b byte
			if f.last {
				b |= 0x80
			}
			b |= byte(f.stage&0x07) << 4
			b |= byte(f.iid & 0x0F)
			feld = append(feld, b)
		}
		feld = append(feld, f.locationCodes...)
	}

	var kopf byte // C/N, OE, P/D, Extension 15
	if f.cn {
		kopf |= 0x80
	}
	if f.oe {
		kopf |= 0x40
	}
	if f.pd {
		kopf |= 0x20
	}
	kopf |= 15

	laenge := 1 + len(feld) // FIG-0-Header plus Type-0-Feld
	if laenge > 31 {
		panic(fmt.Sprintf("FIG 0/15 mit %d Byte passt nicht in das 5-bit-Längenfeld", laenge))
	}
	out := make([]byte, 0, 1+laenge)
	out = append(out, byte(laenge&0x1F)) // FIG-Typ 0 in den oberen 3 bit
	out = append(out, kopf)
	return append(out, feld...)
}

func (f fig0_15) rawHex() string { return hex.EncodeToString(f.packe()) }
