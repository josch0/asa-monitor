// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package rxproc

import (
	"os/exec"
	"testing"
	"time"
)

// Die Probe auf das Job Object: Schließt der Knoten es — und das tut Windows
// auch dann, wenn er hart abgeschossen wird —, muss der Kindprozess sterben.
//
// Ohne diese Absicherung hielte ein überlebender asamon-rx den RTL-SDR-Stick
// offen, und der neu gestartete Knoten fände kein Gerät mehr.
func TestJobObjectNimmtKindprozesseMit(t *testing.T) {
	bin := baueFakeRx(t)

	gruppe, err := NeueGruppe()
	if err != nil {
		t.Fatalf("Job Object ließ sich nicht anlegen: %v", err)
	}

	// fake-rx wartet mit --serve nach dem Strom auf QUIT und läuft damit
	// so lange, bis jemand es beendet.
	cmd := exec.Command(bin, "--serve", "--file", stromPfad("heartbeat-10min"), "--speed", "1")
	// stdin offen halten: fake-rx beendet sich, sobald der Kommandokanal
	// schließt — und ohne Pipe zeigt er auf das Nullgerät, also sofort auf EOF.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatalf("fake-rx ließ sich nicht starten: %v", err)
	}
	fertig := make(chan error, 1)
	go func() { fertig <- cmd.Wait() }()

	if err := gruppe.Aufnehmen(cmd); err != nil {
		cmd.Process.Kill()
		t.Fatalf("Aufnehmen: %v", err)
	}

	// Der Prozess muss laufen, solange das Handle offen ist.
	select {
	case err := <-fertig:
		t.Fatalf("der Kindprozess endete von allein: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := gruppe.Schliessen(); err != nil {
		t.Fatalf("Schliessen: %v", err)
	}

	select {
	case <-fertig:
		// So soll es sein.
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("der Kindprozess überlebte das Schließen des Job Objects")
	}
}

// Ein nil-Zeiger — der Fall, in dem sich kein Job Object anlegen ließ — darf
// nirgends in Panik enden. Der Knoten läuft dann eben ohne die Absicherung.
func TestGruppeOhneJobObjectIstHarmlos(t *testing.T) {
	var g *Gruppe
	if err := g.Aufnehmen(exec.Command("cmd")); err != nil {
		t.Errorf("Aufnehmen auf nil: %v", err)
	}
	if err := g.Schliessen(); err != nil {
		t.Errorf("Schliessen auf nil: %v", err)
	}
}
