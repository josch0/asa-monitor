// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import "fmt"

// Go hat keine erschöpfende Prüfung von Aufzählungen. Der Ersatz dafür ist die
// Hausregel dieses Pakets: benannte Konstanten mit String()-Methode, und in
// jedem switch ein ausdrückliches default, das zählt und meldet statt zu
// verwerfen. `go vet` gehört in die Bauprüfung.

// Phase ist die Signalisierungsphase eines Alerts nach TS 104 089 Kap. 6.3.
type Phase uint8

const (
	// PhaseKeine steht für "kein Alert" — der Ausgangszustand.
	PhaseKeine Phase = iota
	PhasePreTrigger
	PhaseTrigger
	PhaseSustain
	PhaseEnd
	// PhaseUnbekannt steht für einen Phasenwert, den asamon-rx nicht benennen
	// konnte (phase_raw). Heute unerreichbar — Phase ist zwei Bit breit und
	// alle vier Werte sind belegt —, aber die Norm darf wachsen.
	PhaseUnbekannt
)

func (p Phase) String() string {
	switch p {
	case PhaseKeine:
		return ""
	case PhasePreTrigger:
		return "pre_trigger"
	case PhaseTrigger:
		return "trigger"
	case PhaseSustain:
		return "sustain"
	case PhaseEnd:
		return "end"
	case PhaseUnbekannt:
		return "unbekannt"
	default:
		return fmt.Sprintf("Phase(%d)", uint8(p))
	}
}

// PhaseAus liest den Textwert aus dem asa-Record.
func PhaseAus(s string) (Phase, bool) {
	switch s {
	case "":
		return PhaseKeine, true
	case "pre_trigger":
		return PhasePreTrigger, true
	case "trigger":
		return PhaseTrigger, true
	case "sustain":
		return PhaseSustain, true
	case "end":
		return PhaseEnd, true
	default:
		return PhaseUnbekannt, false
	}
}

// Stage ist die Warnstufe samt Entwicklungsstand, TS 104 089 Tabelle in §6.4.3.
type Stage uint8

const (
	StageKeine Stage = iota
	StageLevel1Start
	StageLevel1Update
	StageLevel1Repeat
	StageLevel1Critical
	StageLevel2Start
	StageLevel2Update
	StageLevel2Repeat
	// StageTest ist nur für Testzwecke. Consumer-Empfänger werten solche
	// Alerts nicht aus — ein Monitor gerade doch, und zwar hart getrennt
	// gekennzeichnet.
	StageTest
	StageUnbekannt
)

func (s Stage) String() string {
	switch s {
	case StageKeine:
		return ""
	case StageLevel1Start:
		return "level1_start"
	case StageLevel1Update:
		return "level1_update"
	case StageLevel1Repeat:
		return "level1_repeat"
	case StageLevel1Critical:
		return "level1_critical"
	case StageLevel2Start:
		return "level2_start"
	case StageLevel2Update:
		return "level2_update"
	case StageLevel2Repeat:
		return "level2_repeat"
	case StageTest:
		return "test"
	case StageUnbekannt:
		return "unbekannt"
	default:
		return fmt.Sprintf("Stage(%d)", uint8(s))
	}
}

// StageAus liest den Textwert aus dem asa-Record.
func StageAus(s string) (Stage, bool) {
	switch s {
	case "":
		return StageKeine, true
	case "level1_start":
		return StageLevel1Start, true
	case "level1_update":
		return StageLevel1Update, true
	case "level1_repeat":
		return StageLevel1Repeat, true
	case "level1_critical":
		return StageLevel1Critical, true
	case "level2_start":
		return StageLevel2Start, true
	case "level2_update":
		return StageLevel2Update, true
	case "level2_repeat":
		return StageLevel2Repeat, true
	case "test":
		return StageTest, true
	default:
		return StageUnbekannt, false
	}
}

// Level gibt die Warnstufe. Bei Test und unbekannten Werten ist sie nicht
// definiert — stage bleibt maßgeblich, level ist Bequemlichkeit für den Server.
func (s Stage) Level() (int, bool) {
	switch s {
	case StageLevel1Start, StageLevel1Update, StageLevel1Repeat, StageLevel1Critical:
		return 1, true
	case StageLevel2Start, StageLevel2Update, StageLevel2Repeat:
		return 2, true
	case StageTest, StageKeine, StageUnbekannt:
		return 0, false
	default:
		return 0, false
	}
}

// IstTest sagt, ob dieser Alert hart als Test zu kennzeichnen ist.
func (s Stage) IstTest() bool { return s == StageTest }

// Schliessgrund sagt, warum ein Alert abgeschlossen wurde.
type Schliessgrund uint8

const (
	GrundOffen Schliessgrund = iota
	// GrundEnd ist der reguläre Abschluss über die End-Phase.
	GrundEnd
	// GrundTimeout ist Stille länger als StilleBisAbbruch ohne End.
	GrundTimeout
	// GrundShutdown ist das Herunterfahren des Knotens.
	GrundShutdown
	// GrundStromLuecke ist ein Neustart von asamon-rx mitten im Alert.
	GrundStromLuecke
)

func (g Schliessgrund) String() string {
	switch g {
	case GrundOffen:
		return ""
	case GrundEnd:
		return "end"
	case GrundTimeout:
		return "timeout"
	case GrundShutdown:
		return "shutdown"
	case GrundStromLuecke:
		return "stream_gap"
	default:
		return fmt.Sprintf("Schliessgrund(%d)", uint8(g))
	}
}
