// SPDX-License-Identifier: GPL-3.0-or-later

package record

import (
	"bufio"
	"io"
	"iter"
)

// MaxZeile ist die Puffergröße des Zeilenlesers.
//
// Ein aud-Record mit Base64 kann mehrere Kilobyte lang werden; ein Megabyte
// lässt auch dann Luft, wenn asamon-rx größere Chunks schickt. Reißt der Puffer,
// reißt der Strom — und zwar genau im Alarmfall.
const MaxZeile = 1 << 20

// Zaehler hält fest, was beim Lesen auffiel. Alles davon gehört in den
// Datensatz: Ein Verwurf, den niemand meldet, ist ein verlorener Beleg.
type Zaehler struct {
	// Zeilen ist die Zahl der gelesenen Zeilen, gültige wie ungültige.
	Zeilen uint64
	// SeqLuecken ist die Summe der übersprungenen Sequenznummern. Eine Lücke
	// in seq ist genau ein Verwurf in asamon-rx.
	SeqLuecken uint64
	// SeqRueckwaerts zählt Nummern, die kleiner sind als die vorige — das darf
	// im selben Strom nicht vorkommen.
	SeqRueckwaerts uint64
	// KaputteZeilen ist die Zahl der Zeilen, die kein brauchbares JSON waren.
	KaputteZeilen uint64
	// UnbekannteRecords zählt Zeilen mit unbekanntem type.
	UnbekannteRecords uint64
}

// Reader liest einen NDJSON-Strom und verfolgt dabei die Sequenznummern.
type Reader struct {
	sc      *bufio.Scanner
	zaehler Zaehler
	haben   bool
	letzte  uint64
	err     error
}

// NewReader baut einen Leser mit vergrößertem Zeilenpuffer.
func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), MaxZeile)
	return &Reader{sc: sc}
}

// Zaehler gibt den Stand der Zähler.
func (r *Reader) Zaehler() Zaehler { return r.zaehler }

// Alle liefert die Records des Stroms, bis er endet.
//
// Kaputte Zeilen werden gezählt und übersprungen — sie beenden den Strom nicht.
// Nach der Schleife sagt Err, ob der Strom regulär zu Ende war oder abriss:
//
//	for rec := range leser.Alle() {
//	    …
//	}
//	if err := leser.Err(); err != nil { … }
//
// Das ist die Form von bufio.Scanner, nur als Iterator — der Aufrufer sieht die
// Records, und der Fehlerfall steht einmal am Ende statt in jedem Schleifenkopf.
func (r *Reader) Alle() iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for {
			rec, ok := r.naechster()
			if !ok || !yield(rec) {
				return
			}
		}
	}
}

// Err gibt den Lesefehler, der den Strom beendet hat.
//
// Ein regulär geschlossener Strom ergibt nil: io.EOF ist bei einem
// Subprozess, der sich beendet, kein Fehler, sondern die Nachricht.
func (r *Reader) Err() error { return r.err }

// naechster liest bis zum nächsten brauchbaren Record.
func (r *Reader) naechster() (Record, bool) {
	for {
		if !r.sc.Scan() {
			r.err = r.sc.Err()
			return Record{}, false
		}
		line := r.sc.Bytes()
		r.zaehler.Zeilen++
		if len(trimSpace(line)) == 0 {
			continue
		}

		rec, err := ParseLine(line)
		if err != nil {
			r.zaehler.KaputteZeilen++
			continue
		}
		if rec.Kind == KindUnbekannt {
			r.zaehler.UnbekannteRecords++
		}
		r.pruefeSeq(rec.Seq)
		return rec, true
	}
}

// pruefeSeq verfolgt die Sequenznummer. Der erste Record des Stroms setzt den
// Anfangspunkt — nach einem Neustart von asamon-rx beginnt seq wieder bei 0,
// und das ist keine Lücke, sondern ein neuer Strom.
func (r *Reader) pruefeSeq(seq uint64) {
	if !r.haben {
		r.haben = true
		r.letzte = seq
		return
	}
	switch {
	case seq == r.letzte+1:
	case seq > r.letzte+1:
		r.zaehler.SeqLuecken += seq - r.letzte - 1
	default:
		r.zaehler.SeqRueckwaerts++
	}
	r.letzte = seq
}

func trimSpace(b []byte) []byte {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	j := len(b)
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\r' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}
