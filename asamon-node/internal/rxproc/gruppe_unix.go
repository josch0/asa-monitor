// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package rxproc

import "os/exec"

// Unter Linux ist die Prozessgruppe Sache von systemd.
//
// Die Unit läuft in einer eigenen Control Group, und `KillMode=control-group`
// — die Vorgabe — räumt beim Beenden alles darin ab. Stirbt asamon-node hart,
// bleibt kein asamon-rx zurück. Etwas Eigenes daneben zu bauen brächte nichts
// und könnte systemd nur in die Quere kommen.
//
// Wer den Knoten ohne systemd betreibt, sollte wissen: Dann greift diese
// Absicherung nicht, und ein hart abgeschossener Knoten kann einen asamon-rx
// hinterlassen, der den Stick weiter offen hält. Unter Windows, wo es kein
// systemd gibt, übernimmt ein Job Object diese Aufgabe — siehe
// gruppe_windows.go.
type Gruppe struct{}

// NeueGruppe gibt es hier nur, damit der Aufrufer plattformfrei bleibt.
func NeueGruppe() (*Gruppe, error) { return &Gruppe{}, nil }

// Aufnehmen tut nichts.
func (g *Gruppe) Aufnehmen(cmd *exec.Cmd) error { return nil }

// Schliessen tut nichts.
func (g *Gruppe) Schliessen() error { return nil }
