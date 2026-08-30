// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package config

// Die Vorgaben unter Linux und den übrigen unixartigen Systemen folgen dem
// FHS: Konfiguration nach /etc, veränderlicher Zustand nach /var/lib,
// selbstgebaute Programme nach /usr/local/bin.
//
// Die systemd-Unit setzt StateDirectory=asamon und legt damit /var/lib/asamon
// mit den richtigen Rechten an.
const (
	vorgabeStateDir = "/var/lib/asamon"
	vorgabeRxBinary = "/usr/local/bin/asamon-rx"
)

// suchpfade ist die Reihenfolge, in der ohne --config gesucht wird.
func suchpfade() []string {
	return []string{"node-config.yaml", "/etc/asamon/node-config.yaml"}
}

// rxBinaryNeben gibt den Empfangsprozess an seinem zweiten üblichen Ort.
//
// Nicht gesucht wird im eigenen Verzeichnis: Unter Unix liegt alles an seinem
// Platz im Dateisystem, und ein Programm, das sich sein Gegenstück aus dem
// eigenen Verzeichnis zusammensucht, überrascht mehr, als es hilft.
//
// Zwei feste Orte gibt es aber, seit es ein .deb gibt: Wer selbst baut,
// installiert nach /usr/local/bin (die Vorgabe); ein Paket gehört nach
// /usr/bin. Damit dieselbe Beispielkonfiguration in beiden Fällen läuft, wird
// der zweite Ort geprüft, **wenn** am ersten nichts liegt.
//
// Das greift nur, solange die Konfiguration paths.rx_binary nicht selbst
// nennt: Wer den Pfad setzt, meint ihn, und ein Tippfehler darf nicht dadurch
// verschwinden, dass sich das Programm stillschweigend etwas anderes sucht.
func rxBinaryNeben() string {
	if istAusfuehrbar(vorgabeRxBinary) == nil {
		return "" // die Vorgabe liegt da, sie gilt
	}
	const ausPaket = "/usr/bin/asamon-rx"
	if istAusfuehrbar(ausPaket) == nil {
		return ausPaket
	}
	return ""
}
