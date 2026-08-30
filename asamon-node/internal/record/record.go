// SPDX-License-Identifier: GPL-3.0-or-later

// Paket record liest den NDJSON-Strom von asamon-rx.
//
// Verbindliche Beschreibung des Formats: ../asamon-rx/docs/record-format.md.
// Fünf Typen — init, tlm, ens, asa, aud —, alle mit type, seq und ts.
//
// Drei Eigenschaften des Stroms, auf die sich der Entwurf stützt:
//
//  1. seq ist lückenlos, solange nichts verworfen wurde. Eine Lücke ist genau
//     ein Verwurf und muss als solcher in den Datensatz.
//  2. ts ist Knotenzeit, RFC 3339 mit Nanosekunden. Als String zu lesen und als
//     String zu behalten, wo er nur weitergereicht wird — Nanosekunden seit
//     Epoche überschreiten die 2^53 eines float64.
//  3. raw im asa-Record ist der Beleg. Er wird immer unverändert
//     weitergereicht, auch wenn die eigene Deutung scheitert.
//
// Regel für unbekannte Felder und Typen: überlesen, zählen, melden — niemals
// als Fehler behandeln. Das Format darf additiv wachsen.
package record

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"time"
)

// deutung legt fest, wie streng der Strom gelesen wird.
//
// encoding/json/v2 (Go 1.27) ist an drei Stellen strenger als die alte
// Fassung. Zwei davon sind hier erwünscht, eine nicht:
//
//   - **Doppelte Feldnamen werden abgelehnt.** Erwünscht: Der Record-Strom ist
//     ein Beleg. Eine Zeile mit zwei `type`-Feldern ist mehrdeutig, und
//     stillschweigend das letzte zu nehmen wäre genau die Art von leiser
//     Fehldeutung, die dieses Programm sonst überall vermeidet. Sie wird zur
//     gezählten kaputten Zeile.
//   - **Feldnamen werden groß-/kleinschreibungsgenau zugeordnet.** Erwünscht:
//     docs/record-format.md legt die Namen klein fest. Die alte Fassung nahm
//     "Type" für "type" — ein Sender, der das tut, weicht vom Format ab, und
//     das soll auffallen.
//   - **Ungültiges UTF-8 wird abgelehnt.** Nicht erwünscht, deshalb wieder
//     erlaubt: Ein verstümmeltes Ensemble-Label ist ein Symptom schlechten
//     Empfangs, kein Grund, den ganzen ens-Record wegzuwerfen. Ohne ihn gäbe es
//     keinen ens_hash und damit für die nächsten Sekunden auch keinen asa_hash
//     — der Empfangsfehler würde so zum Auswertungsfehler.
//
// Nebenbei liest v2 rund ein Drittel schneller; siehe BenchmarkParseLine.
var deutung = json.JoinOptions(jsontext.AllowInvalidUTF8(true))

// FormatVersion ist die Fassung des Record-Formats, die dieses Programm deutet.
//
// Weicht die Angabe im init-Record davon ab, wird der Kanal nicht gestartet.
// Ein stillschweigend falsch gedeuteter Strom ist schlimmer als ein fehlender
// Kanal.
const FormatVersion = 1

// Kind ist der Record-Typ.
type Kind uint8

const (
	KindUnbekannt Kind = iota
	KindInit
	KindTlm
	KindEns
	KindAsa
	KindAud
)

func (k Kind) String() string {
	switch k {
	case KindInit:
		return "init"
	case KindTlm:
		return "tlm"
	case KindEns:
		return "ens"
	case KindAsa:
		return "asa"
	case KindAud:
		return "aud"
	case KindUnbekannt:
		return "unbekannt"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

func kindAus(s string) Kind {
	switch s {
	case "init":
		return KindInit
	case "tlm":
		return KindTlm
	case "ens":
		return KindEns
	case "asa":
		return KindAsa
	case "aud":
		return KindAud
	default:
		return KindUnbekannt
	}
}

// Record ist eine Zeile des Stroms. Genau eines der Nutzlastfelder ist gesetzt;
// bei KindUnbekannt keines.
type Record struct {
	Kind Kind
	// TypeRaw ist der Text aus dem Feld `type`, auch wenn er unbekannt ist.
	TypeRaw string
	Seq     uint64
	// Ts ist die Knotenzeit als Text, unverändert wie im Strom.
	Ts string
	// Zeit ist Ts, geparst. Bei unlesbarem ts die Nullzeit.
	Zeit time.Time

	Init *Init
	Tlm  *Tlm
	Ens  *Ens
	Asa  *Asa
	Aud  *Aud
}

// Init steht genau einmal am Anfang jedes Stroms und macht jede Aufzeichnung
// für sich allein erklärbar. Deshalb braucht kein anderer Record ein Kanalfeld.
type Init struct {
	FormatVersion int    `json:"format_version"`
	Channel       string `json:"channel"`
	FreqHz        int64  `json:"freq_hz"`
	Device        string `json:"device"`
	DeviceSerial  string `json:"device_serial"`
	RxVersion     string `json:"rx_version"`
	RxCommit      string `json:"rx_commit"`
	WelleCommit   string `json:"welle_commit"`
}

// FreqCorr ist die Frequenzkorrektur aus dem tlm-Record.
type FreqCorr struct {
	Fine   int `json:"fine"`
	Coarse int `json:"coarse"`
}

// Tlm kommt einmal je Sekunde, auch wenn nichts empfangen wurde.
//
// Die CRC-Quote ist die wichtigste Zahl im ganzen Record: Mit ihr lässt sich
// "Ensemble sendet keinen Heartbeat" von "wir empfangen schlecht" trennen — und
// darauf beruht die Abdeckungskarte, das Kernergebnis des Projekts.
type Tlm struct {
	Snr          *float64 `json:"snr"` // null, wenn nicht bezifferbar
	Sync         bool     `json:"sync"`
	Signal       bool     `json:"signal"`
	FreqCorr     FreqCorr `json:"freq_corr"`
	FibTotal     uint64   `json:"fib_total"`    // letzte Sekunde
	FibCrcErr    uint64   `json:"fib_crc_err"`  // davon mit CRC-Fehler
	Dropped      uint64   `json:"dropped"`      // kumulativ
	ParseErrors  uint64   `json:"parse_errors"` // kumulativ
	Eid          string   `json:"eid"`          // "0x10FF"; fehlt ohne Ensemble
	EnsTime      string   `json:"ens_time"`     // FIG 0/10; fehlt, solange keine kam
	EnsOffsetMin *int     `json:"ens_offset_min"`
}

// Komponente ist eine Dienstkomponente mit Subchannel-Eintrag.
type Komponente struct {
	SubChID    int    `json:"subch_id"`
	StartAddr  int    `json:"start_addr"`
	Size       int    `json:"size"` // Capacity Units
	Protection string `json:"protection"`
	Bitrate    int    `json:"bitrate"` // kbit/s
}

// Service ist ein Dienst des Ensembles.
type Service struct {
	Sid         string       `json:"sid"`
	Label       string       `json:"label"`
	Komponenten []Komponente `json:"components"`
}

// Ens geht raus, sobald sich Ensemble, Services, Labels oder die
// Subchannel-Parameter ändern — nicht im Takt. Genau dieser Record ist der
// Grund, warum asamon-node FIG 0/1 und 0/2 nicht selbst parsen muss.
type Ens struct {
	Eid      string    `json:"eid"`
	Ecc      int       `json:"ecc"`
	Label    string    `json:"label"`
	Services []Service `json:"services"`
}

// Asa ist eine FIG-0/15-Instanz, ausgepackt und ungedeutet.
//
// Welche Felder vorhanden sind, hängt von Phase und Flags ab; die Zeiger sind
// deshalb keine Bequemlichkeit, sondern Aussage: nil heißt "stand nicht im
// Strom", nicht "war null".
type Asa struct {
	Heartbeat     bool   `json:"heartbeat"`
	Cn            bool   `json:"cn"`
	Oe            bool   `json:"oe"`
	PdSecondHalf  bool   `json:"pd_second_half"`
	Phase         string `json:"phase"`          // oe:false, kein Heartbeat
	PhaseRaw      *int   `json:"phase_raw"`      // Phase ohne Namen (Normerweiterung)
	SubChID       *int   `json:"subch_id"`       // oe:false, kein Heartbeat
	OtherEid      string `json:"other_eid"`      // oe:true
	Sec           *int   `json:"sec"`            // phase:pre_trigger; 63 ist Sonderwert
	Stage         string `json:"stage"`          // Status-Feld vorhanden
	StageRaw      *int   `json:"stage_raw"`      // Stage ohne Namen
	Iid           *int   `json:"iid"`            // Status-Feld vorhanden
	Last          *bool  `json:"last"`           // Status-Feld vorhanden
	Nff           *int   `json:"nff"`            // Location Codes vorhanden
	LocationCodes string `json:"location_codes"` // Hex, roh und ungedeutet
	Raw           string `json:"raw"`            // immer
}

// AudDatei ist eine Datei, die asamon-rx geschrieben hat.
type AudDatei struct {
	Name   string `json:"name"`  // ohne Verzeichnis
	Codec  string `json:"codec"` // "dabp" (roher Bitstrom) | "mp3"
	Bytes  int64  `json:"bytes"`
	Sha256 string `json:"sha256"` // hex, klein
}

// Aud meldet eine abgeschlossene Aufnahme — genau einer je Mitschnitt, nach
// dem STOP.
//
// Bis zum 30.08.2026 trug dieser Typ den Subchannel-Bitstrom selbst, in
// base64-kodierten Stücken. Seitdem schreibt asamon-rx die Dateien in den
// Ablageordner und nennt sie hier mit Größe und Prüfsumme; der Knoten liest
// sie nur noch zum Hochladen. Drei Dinge werden damit besser: ein Drittel
// weniger Übertragung, kein Base64 auf dem heißesten Pfad dieses Parsers —
// und ein Mitschnitt kann nicht mehr löchrig werden, weil ein Record im
// Warteschlangenüberlauf verworfen wurde.
type Aud struct {
	SubChID   int     `json:"subch_id"`
	AlertUID  string  `json:"alert_uid"` // fehlt, wenn REC ohne uid kam
	Dir       string  `json:"dir"`
	Started   string  `json:"started"`
	Seconds   float64 `json:"seconds"`
	Truncated bool    `json:"truncated"`

	// Erst bekannt, sobald dekodiertes Audio kam.
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	Mode       string `json:"mode"`
	Mp3Bitrate int    `json:"mp3_bitrate"`

	// Summen über die Aufnahme, aus welle.ios Rückrufen. Ohne sie ließe sich
	// eine stockende Aufnahme nicht von einer stillen Meldung unterscheiden.
	FrameErrors int64 `json:"frame_errors"`
	RsErrors    int64 `json:"rs_errors"`
	RsCorrected int64 `json:"rs_corrected"`
	AacErrors   int64 `json:"aac_errors"`

	Files []AudDatei `json:"files"`
	Error string     `json:"error"`
}

// kopf ist der gemeinsame Teil jeder Zeile.
type kopf struct {
	Type string `json:"type"`
	Seq  uint64 `json:"seq"`
	Ts   string `json:"ts"`
}

// ParseLine liest eine Zeile des Stroms.
//
// Ein unbekannter Typ ist kein Fehler: Der Record kommt mit KindUnbekannt
// zurück, damit der Aufrufer ihn zählen kann. Ein Fehler bedeutet, dass die
// Zeile gar kein brauchbares JSON-Objekt mit type/seq war.
func ParseLine(line []byte) (Record, error) {
	var k kopf
	if err := json.Unmarshal(line, &k, deutung); err != nil {
		return Record{}, fmt.Errorf("Zeile ist kein gültiges JSON: %w", err)
	}
	if k.Type == "" {
		return Record{}, fmt.Errorf("Zeile ohne Feld type")
	}

	r := Record{Kind: kindAus(k.Type), TypeRaw: k.Type, Seq: k.Seq, Ts: k.Ts}
	if k.Ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, k.Ts); err == nil {
			r.Zeit = t.UTC()
		}
	}

	var ziel any
	switch r.Kind {
	case KindInit:
		r.Init = &Init{}
		ziel = r.Init
	case KindTlm:
		r.Tlm = &Tlm{}
		ziel = r.Tlm
	case KindEns:
		r.Ens = &Ens{}
		ziel = r.Ens
	case KindAsa:
		r.Asa = &Asa{}
		ziel = r.Asa
	case KindAud:
		r.Aud = &Aud{}
		ziel = r.Aud
	default:
		// Unbekannter Typ: überlesen und zählen. Das Format darf wachsen.
		return r, nil
	}
	if err := json.Unmarshal(line, ziel, deutung); err != nil {
		return Record{}, fmt.Errorf("%s-Record: %w", k.Type, err)
	}
	return r, nil
}
