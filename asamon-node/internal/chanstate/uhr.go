// SPDX-License-Identifier: GPL-3.0-or-later

package chanstate

import (
	"time"

	"github.com/josch0/asa-monitor/asamon-node/internal/report"
)

// EnsUhrGueltigkeit ist die Frist, innerhalb derer eine ens_time noch nach
// vorn fortgeschrieben werden darf. Danach ist der Versatz zur Knotenuhr nicht
// mehr belegbar — es kam ja seit fünf Sekunden keine Telemetrie mehr.
const EnsUhrGueltigkeit = 5 * time.Second

// EnsUhrRueckblick ist die Spanne, über die zurückgerechnet werden darf.
//
// Sie ist großzügiger als die Vorausschau, und das aus einem Grund: Die
// Fenstergrenzen eines Datensatzes liegen naturgemäß in der Vergangenheit —
// beim Vorgabetakt zehn Sekunden vor der letzten Telemetrie. Zwei Quarzuhren
// laufen in einer Minute weit weniger als eine Sekunde auseinander; die
// Rückrechnung ist damit genauso belastbar wie die Vorausschau, während ein
// enges Fenster hier die Heartbeat-Bilanz um ganze Sekunden verschöbe.
const EnsUhrRueckblick = time.Minute

// ensUhr ist die Ensemble-Uhr: die gemeinsame Zeitbasis aller Empfänger
// desselben Ensembles und damit die Grundlage der Hashes.
//
// Der Unterschied zur Knotenuhr ist der ganze Punkt: Mit Ensemble-Zeit stimmen
// zwei Knoten **exakt** überein, mit Knotenzeit nur, soweit ihre NTP-Uhren
// übereinstimmen — und an einer Sekundengrenze eben nicht.
type ensUhr struct {
	ensZeit   time.Time // aus tlm.ens_time (FIG 0/10), sekundengenau
	beiKnoten time.Time // Knotenzeit des tlm-Records, der sie brachte
	gesetzt   bool
}

// Stelle übernimmt eine neue ens_time.
func (u *ensUhr) Stelle(ensZeit, beiKnoten time.Time) {
	u.ensZeit = ensZeit.UTC()
	u.beiKnoten = beiKnoten.UTC()
	u.gesetzt = true
}

// Gueltig sagt, ob die Uhr zum Zeitpunkt at noch trägt.
func (u *ensUhr) Gueltig(at time.Time) bool {
	if !u.gesetzt {
		return false
	}
	d := at.Sub(u.beiKnoten)
	return d >= -EnsUhrRueckblick && d < EnsUhrGueltigkeit
}

// Sekunde gibt die Ensemble-Sekunde zum Knotenzeitpunkt at und die Quelle.
//
// Fehlt die Ensemble-Zeit, wird auf die Knotenzeit zurückgefallen und das im
// Datensatz mit time_source: "node" kenntlich gemacht. Ein Knoten in diesem
// Zustand kann an einer Sekundengrenze eine Sekunde danebenliegen; sein
// asa_hash weicht dann ab. Deshalb schickt der Datensatz neben dem Hash immer
// auch ens_hash, ens_second und raw mit.
func (u *ensUhr) Sekunde(at time.Time) (time.Time, string) {
	roh, quelle := u.Roh(at)
	return roh.Truncate(time.Second), quelle
}

// Roh gibt die Ensemble-Zeit zum Knotenzeitpunkt at, ohne auf Sekunden
// abzurunden. Gebraucht wird sie für die Fenstergrenzen der
// Heartbeat-Bilanz: Dort entscheidet der Bruchteil darüber, welche Sekunden
// noch ins Fenster fallen, und ein Abrunden auf beiden Seiten verschöbe die
// Bilanz um eine ganze Sekunde.
func (u *ensUhr) Roh(at time.Time) (time.Time, string) {
	if !u.Gueltig(at) {
		return at.UTC(), report.ZeitAusKnoten
	}
	return u.ensZeit.Add(at.Sub(u.beiKnoten)), report.ZeitAusEnsemble
}

// VersatzMs ist der Versatz zwischen Knotenuhr und Ensemble-Zeit in
// Millisekunden, positiv, wenn die Knotenuhr vorgeht. Er ist selbst eine
// Messgröße — und der Grund, warum der Knoten eine synchronisierte Uhr braucht.
//
// Die ens_time ist sekundengenau; bis zu einer Sekunde des Ergebnisses ist
// deshalb Quantisierung, nicht Uhrenfehler.
func (u *ensUhr) VersatzMs() (int64, bool) {
	if !u.gesetzt {
		return 0, false
	}
	return u.beiKnoten.Sub(u.ensZeit).Milliseconds(), true
}
