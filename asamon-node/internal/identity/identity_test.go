// SPDX-License-Identifier: GPL-3.0-or-later

package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"
)

func TestIdentitaetUeberlebtDenNeustart(t *testing.T) {
	dir := t.TempDir()

	erste, err := Load(dir)
	if err != nil {
		t.Fatalf("erster Start: %v", err)
	}
	if _, err := uuid.Parse(erste.NodeID); err != nil {
		t.Errorf("node_id ist keine UUID: %q (%v)", erste.NodeID, err)
	}
	if erste.NodeID[14] != '4' {
		t.Errorf("node_id ist keine UUIDv4: %q", erste.NodeID)
	}
	if v := erste.NodeID[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Errorf("node_id hat nicht die Variant-Bits nach RFC 4122: %q", erste.NodeID)
	}
	if len(erste.PubKey) != ed25519.PublicKeySize {
		t.Errorf("der öffentliche Schlüssel hat %d Byte", len(erste.PubKey))
	}

	zweite, err := Load(dir)
	if err != nil {
		t.Fatalf("zweiter Start: %v", err)
	}
	if zweite.NodeID != erste.NodeID {
		t.Errorf("die node_id wechselte von %q zu %q", erste.NodeID, zweite.NodeID)
	}
	if zweite.PubKeyBase64() != erste.PubKeyBase64() {
		t.Error("das Schlüsselpaar wechselte beim zweiten Start")
	}

	// Zwei verschiedene Verzeichnisse müssen zwei verschiedene Knoten ergeben.
	dritte, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if dritte.NodeID == erste.NodeID {
		t.Error("zwei Knoten bekamen dieselbe node_id")
	}
}

func TestSchluesselIstBrauchbar(t *testing.T) {
	id, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("asamon-node")
	if !ed25519.Verify(id.PubKey, msg, id.Sign(msg)) {
		t.Error("die eigene Signatur wird vom eigenen öffentlichen Schlüssel nicht anerkannt")
	}
}

func TestSequenznummerWiederholtSichNie(t *testing.T) {
	dir := t.TempDir()
	id, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	erste, err := id.NextSeqStart()
	if err != nil {
		t.Fatal(err)
	}
	if erste != seqSprung {
		t.Errorf("der erste Start begann bei %d, erwartet %d", erste, seqSprung)
	}

	// Der Prozess vergibt Nummern und schreibt beim Beenden fest.
	if err := id.SaveSeq(erste + 42); err != nil {
		t.Fatal(err)
	}

	zweite, err := id.NextSeqStart()
	if err != nil {
		t.Fatal(err)
	}
	if zweite <= erste+42 {
		t.Errorf("der zweite Start begann bei %d und würde damit Nummern wiederverwenden (zuletzt vergeben: %d)", zweite, erste+42)
	}

	// Ein Absturz ohne SaveSeq darf ebenfalls keine Nummer wiederverwenden:
	// Der Sprung beim Start deckt bis zu seqSprung Datensätze ab.
	dritte, err := id.NextSeqStart()
	if err != nil {
		t.Fatal(err)
	}
	if dritte <= zweite {
		t.Errorf("der dritte Start begann bei %d, der zweite bei %d", dritte, zweite)
	}
}

func TestKaputteDateienWerdenGemeldet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node_id"), []byte("kein-uuid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("eine node_id ohne UUID wurde angenommen")
	}
	if !strings.Contains(err.Error(), "UUID") {
		t.Errorf("die Meldung nennt den Grund nicht: %v", err)
	}

	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "node_key"), []byte("nicht base64!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir2); err == nil {
		t.Error("ein unlesbarer node_key wurde angenommen")
	}

	// Eine unlesbare seq-Datei darf den Start nicht verhindern, aber keine
	// Nummer wiederverwenden.
	dir3 := t.TempDir()
	id, err := Load(dir3)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir3, "seq"), []byte("keine Zahl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seq, err := id.NextSeqStart()
	if err == nil {
		t.Error("die unlesbare seq-Datei wurde stillschweigend übergangen")
	}
	if seq == 0 {
		t.Error("nach einer unlesbaren seq-Datei wurde bei 0 begonnen")
	}
}

func TestSchreibeAtomarUeberschreibt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "datei")
	if err := SchreibeAtomar(p, []byte("erst"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SchreibeAtomar(p, []byte("dann"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "dann" {
		t.Errorf("Inhalt ist %q", raw)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("die Nebendatei blieb liegen")
	}
}
