// SPDX-License-Identifier: GPL-3.0-or-later

// Paket hashes berechnet die Hashes, über die der Server Beobachtungen
// mehrerer Knoten zusammenführt.
//
// Mehrere Knoten empfangen dasselbe Signal. Damit der Server sie
// zusammenführen kann, trägt jede Beobachtung einen Hash, den jeder Knoten
// unabhängig zum selben Wert berechnet.
//
// # Kanonisierung — die Regel, an der alles hängt
//
// Gehasht wird nie serialisiertes JSON: Feldreihenfolge und Escaping sind dort
// nicht garantiert, und zwei Knoten mit verschiedenen Programmständen kämen
// auseinander. Gehasht wird eine ausgeschriebene Bytefolge:
//
//   - Felder mit \n (0x0A) getrennt, kein abschließendes \n,
//   - Hex durchgehend klein, ohne Präfix (10ff, nicht 0x10FF),
//   - Zeiten als RFC 3339 in UTC, ohne Bruchteile, mit Z,
//   - Zahlen als Dezimaltext ohne führende Nullen,
//   - ein Präfix je Hashart als erstes Feld — es verhindert, dass zwei
//     Hasharten je kollidieren,
//   - SHA-256, davon die ersten 16 Byte, als 32 Hexzeichen.
//
// Ändert sich eine Definition, steigt das Präfix (-v2) — nie stillschweigend.
// Die Definitionen und ihre Testvektoren stehen in docs/hashes.md.
package hashes

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Die Präfixe. Jede Hashart hat ihres, und es steht immer als erstes Feld.
const (
	PraefixEns        = "asamon-ens-v1"
	PraefixEnsContent = "asamon-enscontent-v1"
	PraefixAsa        = "asamon-asa-v1"
	PraefixAlert      = "asamon-alert-v1"
)

// IidUnbekannt steht im alert_uid, wenn der Knoten erst in der Sustain-Phase
// eingestiegen ist und das Status-Feld — und damit den IId — nie gesehen hat.
const IidUnbekannt = "-"

// Digest ist die gemeinsame Endstufe: SHA-256, davon die ersten 16 Byte.
//
// 128 bit reichen: Der Server dedupliziert innerhalb eines Zeitfensters, nicht
// über die Ewigkeit, und der Hash ist kein Sicherheitsmerkmal — er ist ein
// Verkettungsschlüssel.
func Digest(felder ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(felder, "\n")))
	return hex.EncodeToString(sum[:16])
}

// Sekunde schreibt eine Zeit als RFC 3339 in UTC, ohne Bruchteile.
func Sekunde(t time.Time) string { return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z") }

// Minute schreibt eine Zeit als RFC 3339 in UTC, auf die Minute abgerundet.
func Minute(t time.Time) string { return t.UTC().Truncate(time.Minute).Format("2006-01-02T15:04:05Z") }

// Hex normalisiert einen Hexwert auf kleine Ziffern und feste Breite.
//
// Angenommen werden "0x10FF", "10FF" und "10ff"; heraus kommt immer "10ff".
// Ein Wert, der breiter ist als breite, kommt ungekürzt zurück — Abschneiden
// wäre stillschweigender Datenverlust.
func Hex(s string, breite int) string {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(strings.TrimPrefix(t, "0x"), "0X")
	t = strings.ToLower(t)
	for len(t) < breite {
		t = "0" + t
	}
	return t
}

// HexZahl schreibt eine Zahl als Hex mit fester Breite.
func HexZahl(v int, breite int) string { return fmt.Sprintf("%0*x", breite, v) }

// Zahl schreibt eine Zahl als Dezimaltext ohne führende Nullen.
func Zahl(v int) string { return strconv.Itoa(v) }

// Ens berechnet den ens_hash — die Identität eines Kanal-Ensembles.
//
//	asamon-ens-v1 \n <channel> \n <eid_hex_4> \n <ecc_hex_2>
//
// Bewusst ohne Label und ohne Services: Labels kommen bei schlechtem Empfang
// verstümmelt an, und eine Umstellung im Multiplex darf die Identität nicht
// wechseln.
func Ens(channel, eid string, ecc int) string {
	return Digest(PraefixEns, channel, Hex(eid, 4), HexZahl(ecc, 2))
}

// Komponente ist eine Dienstkomponente, so wie sie in den Inhaltshash eingeht.
type Komponente struct {
	SubChID    int
	StartAddr  int
	Size       int
	Protection string
	Bitrate    int
}

// Service ist ein Dienst, so wie er in den Inhaltshash eingeht.
type Service struct {
	Sid         string
	Label       string
	Komponenten []Komponente
}

// EnsContent berechnet den ens_content_hash — die Momentaufnahme des
// Multiplex-Aufbaus. Er erkennt Änderungen und dedupliziert die
// Ensemble-Datensätze über die Knoten hinweg.
//
//	asamon-enscontent-v1 \n <ens_hash> \n <label> \n
//	  je Service, sortiert nach sid:
//	    <sid_hex_8> \t <label> \t <subch_id>,<start_addr>,<size>,<protection>,<bitrate>; …
//
// Je Service eine Zeile; die Komponenten sind nach subch_id sortiert und jede
// mit ';' abgeschlossen. Sortiert wird, weil die Reihenfolge im FIC nicht
// zugesagt ist — zwei Knoten dürfen hier nicht auseinanderlaufen.
func EnsContent(ensHash, label string, services []Service) string {
	felder := []string{PraefixEnsContent, ensHash, label}

	sortiert := slices.Clone(services)
	slices.SortStableFunc(sortiert, func(a, b Service) int {
		return strings.Compare(Hex(a.Sid, 8), Hex(b.Sid, 8))
	})

	for _, s := range sortiert {
		var b strings.Builder
		b.WriteString(Hex(s.Sid, 8))
		b.WriteByte('\t')
		b.WriteString(s.Label)
		b.WriteByte('\t')

		komponenten := slices.Clone(s.Komponenten)
		slices.SortStableFunc(komponenten, func(a, b Komponente) int {
			return cmp.Compare(a.SubChID, b.SubChID)
		})
		for _, k := range komponenten {
			b.WriteString(Zahl(k.SubChID))
			b.WriteByte(',')
			b.WriteString(Zahl(k.StartAddr))
			b.WriteByte(',')
			b.WriteString(Zahl(k.Size))
			b.WriteByte(',')
			b.WriteString(k.Protection)
			b.WriteByte(',')
			b.WriteString(Zahl(k.Bitrate))
			b.WriteByte(';')
		}
		felder = append(felder, b.String())
	}
	return Digest(felder...)
}

// Asa berechnet den asa_hash — den Schlüssel einer einzelnen
// FIG-0/15-Instanz. Er ist der wichtigste: Über ihn erkennen zwei Knoten
// dieselbe Meldung als dieselbe.
//
//	asamon-asa-v1 \n <ens_hash> \n <ens_second_rfc3339> \n <raw_hex>
//
// Drei Entscheidungen darin, jede mit einem Grund:
//
//   - raw statt der geparsten Felder: raw ist genau das, was auf dem Kanal
//     stand. Zwei Knoten mit verschiedenen Programmständen kämen bei den
//     geparsten Feldern womöglich auseinander, bei raw nie.
//   - Ensemble-Zeit, nicht Knotenzeit: Sie kommt aus demselben Sender und ist
//     bei allen Empfängern desselben Ensembles bitgleich. Die lokale NTP-Uhr
//     wäre auf ±1 s genau — und damit an jeder Sekundengrenze uneinig.
//   - Sekunde, nicht Millisekunde: Im Alarmfall wiederholt sich dieselbe
//     Instanz innerhalb der Sekunde. Dass diese Wiederholungen denselben Hash
//     bekommen, ist erwünscht — sie sind dieselbe Beobachtung.
//
// Die Grenze offen benannt: Fehlt einem Knoten die Ensemble-Zeit, fällt er auf
// die Knotenuhr zurück und kann an einer Sekundengrenze eine Sekunde
// danebenliegen. Deshalb schickt der Datensatz neben dem Hash immer auch
// ens_hash, ens_second und raw mit — der Server kann dann zweistufig
// deduplizieren.
func Asa(ensHash string, ensSekunde time.Time, raw string) string {
	return Digest(PraefixAsa, ensHash, Sekunde(ensSekunde), strings.ToLower(raw))
}

// Alert berechnet den alert_uid — einen Vorfall. Er ist ein Vorschlag zur
// Verkettung, kein Beweis.
//
//	asamon-alert-v1 \n <eid_hex_4> \n <iid> \n <start_minute_rfc3339>
//
// eid ist das warnende Ensemble (bei oe:true also other_eid, nicht das
// empfangene) — ohne Kanal, denn wer den OE-Verweis sieht, kennt den Kanal des
// anderen Ensembles nicht.
//
// startMinute ist die auf die Minute abgerundete Ensemble-Zeit der ersten
// beobachteten Instanz. Weil Alerts laut Norm an der Minutengrenze beginnen,
// treffen sich Knoten, die den Beginn gesehen haben, hier zuverlässig. Wer erst
// in sustain einsteigt, kennt die Startminute nicht — dann ist
// alert_uid_confident false, und der Server verkettet selbst.
//
// Ist der IId unbekannt (Einstieg in sustain, kein Status-Feld gesehen), steht
// statt der Zahl IidUnbekannt. Der IId ist nur 4 bit breit und wird
// wiederverwendet; eine global eindeutige Vorfalls-ID existiert on air nicht,
// und dieses Feld tut nicht so, als gäbe es sie.
func Alert(warnendeEid string, iid int, iidBekannt bool, startMinute time.Time) string {
	iidFeld := IidUnbekannt
	if iidBekannt {
		iidFeld = Zahl(iid)
	}
	return Digest(PraefixAlert, Hex(warnendeEid, 4), iidFeld, Minute(startMinute))
}
