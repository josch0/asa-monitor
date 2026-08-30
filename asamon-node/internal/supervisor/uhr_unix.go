// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package supervisor

import "os"

// uhrSynchronisiert fragt nicht nach — das ginge nur über D-Bus, und das wäre
// eine Fremdabhängigkeit für eine einzige Auskunft.
//
// Stattdessen wird die Datei geprüft, die systemd-timesyncd nach einer
// erfolgreichen Synchronisation anlegt. Das Feld bedeutet deshalb
// **bestätigt synchronisiert**, nicht "die Uhr geht richtig": Ein Knoten mit
// chrony und ohne timesyncd meldet hier false, obwohl seine Uhr stimmt.
//
// Die belastbarere Größe steht ohnehin daneben im selben Datensatz:
// ens_time_offset_ms misst den Abstand zwischen Knotenuhr und Ensemble-Zeit
// und braucht dafür niemanden zu fragen.
func uhrSynchronisiert() bool {
	for _, pfad := range []string{
		"/run/systemd/timesync/synchronized",
		"/var/lib/systemd/timesync/clock",
	} {
		if _, err := os.Stat(pfad); err == nil {
			return true
		}
	}
	return false
}
