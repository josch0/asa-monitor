// SPDX-License-Identifier: GPL-3.0-or-later

// Paket buildinfo hält fest, welcher Stand gerade läuft.
//
// Version und Commit werden beim Übersetzen über -ldflags gesetzt (siehe
// Makefile). Ohne diese Angaben — etwa bei `go run` — treten die Werte aus
// debug.ReadBuildInfo an ihre Stelle, damit der Datensatz nie leer bleibt.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	// Version ist die Programmversion, gesetzt über -ldflags.
	Version = "0.1.0"
	// Commit ist der Commit dieses Repos zur Bauzeit, gesetzt über -ldflags.
	Commit = ""
)

func init() {
	if Commit != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			Commit = s.Value[:7]
			return
		}
	}
}

// Platform ist "linux/arm64" und dergleichen — im Datensatz die Auskunft
// darüber, auf welcher Art Gerät der Knoten läuft.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// String ist die Zeile für --version.
func String() string {
	s := "asamon-node " + Version
	if Commit != "" {
		s += " (" + Commit + ")"
	}
	return s + " " + Platform() + " " + runtime.Version()
}
