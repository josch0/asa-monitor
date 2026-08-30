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

// rxBinaryNeben gibt den Empfangsprozess neben der eigenen Binary zurück.
//
// Unter Unix wird nicht danach gesucht: Dort liegt alles an seinem Platz im
// Dateisystem, und ein Programm, das sich sein Gegenstück aus dem eigenen
// Verzeichnis zusammensucht, überrascht mehr, als es hilft.
func rxBinaryNeben() string { return "" }
