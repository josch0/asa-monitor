// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// Unter Windows gibt es kein /etc und kein /var/lib. Der Zustand gehört nach
// ProgramData: Es ist maschinenweit, überlebt Benutzerwechsel und ist der
// vorgesehene Ort für Dienstdaten.
//
// %ProgramData% ist auf jedem unterstützten Windows gesetzt; fehlt es doch,
// bleibt C:\ProgramData als letzter Ausweg — das ist der Vorgabewert seit
// Windows Vista.
func programData() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}

// vorgabeStateDir und vorgabeRxBinary sind Variablen statt Konstanten, weil
// sie unter Windows aus der Umgebung kommen.
var (
	vorgabeStateDir = filepath.Join(programData(), "asamon", "state")
	vorgabeRxBinary = filepath.Join(programData(), "asamon", "asamon-rx.exe")
)

// suchpfade ist die Reihenfolge, in der ohne --config gesucht wird.
//
// Das aktuelle Verzeichnis steht zuerst — eine ausgepackte Release-ZIP soll
// ohne Installation laufen. Danach der maschinenweite Ort.
func suchpfade() []string {
	return []string{
		"node-config.yaml",
		filepath.Join(programData(), "asamon", "node-config.yaml"),
	}
}

// rxBinaryNeben sucht asamon-rx.exe im Verzeichnis der eigenen Binary.
//
// Das ist unter Windows der Regelfall: Wer eine Release-ZIP auspackt, hat
// beide Programme nebeneinander liegen und keinen Paketmanager, der sie nach
// /usr/local/bin legt. Erst wenn dort nichts liegt, greift der feste
// Vorgabepfad.
func rxBinaryNeben() string {
	eigen, err := os.Executable()
	if err != nil {
		return ""
	}
	kandidat := filepath.Join(filepath.Dir(eigen), "asamon-rx.exe")
	if info, err := os.Stat(kandidat); err == nil && !info.IsDir() {
		return kandidat
	}
	return ""
}
