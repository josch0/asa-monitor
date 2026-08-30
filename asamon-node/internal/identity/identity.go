// SPDX-License-Identifier: GPL-3.0-or-later

// Paket identity hält die drei Dinge fest, die einen Knoten ausmachen und den
// Neustart überleben: node_id, Schlüsselpaar und Sequenznummer.
//
// Der Anzeigename aus der Konfiguration gehört ausdrücklich nicht dazu. Er ist
// frei wählbar und damit netzweit weder eindeutig noch stabil — zwei
// Freiwillige nennen ihren Knoten "Zuhause". Die node_id ist der Schlüssel,
// unter dem der Server Beobachtungen verkettet; wird der Name geändert, bleibt
// die Historie erhalten.
//
// Die node_id ist eine UUIDv4 aus dem Paket uuid der Standardbibliothek
// (Go 1.27). TODO.md Abschnitt 5 sah dafür rund zwanzig eigene Zeilen vor, weil
// eine Fremdabhängigkeit nicht in Frage kam — mit der Aufnahme in die
// Standardbibliothek ist dieser Grund entfallen.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"uuid"
)

// seqSprung ist der Aufschlag auf die gespeicherte Sequenznummer beim Start.
//
// Statt bei jedem Datensatz auf die Platte zu schreiben, wird beim Start eine
// Lücke gerissen und die Nummer erst wieder beim Beenden festgeschrieben. Diese
// Knoten laufen auf Raspberry Pis mit SD-Karten; sechs Schreibvorgänge pro
// Minute, dauerhaft, sind ein reales Verschleißproblem. Eine wiederverwendete
// seq wäre ein Fehler, eine Lücke ist unschädlich — der Server erkennt
// Duplikate über (node_id, seq).
const seqSprung = 1000

// Identity ist die dauerhafte Kennung eines Knotens.
type Identity struct {
	NodeID   string
	PubKey   ed25519.PublicKey
	privKey  ed25519.PrivateKey
	stateDir string
}

// Load lädt node_id und Schlüsselpaar aus dem state_dir und legt beides an,
// wenn es noch nicht da ist.
func Load(stateDir string) (*Identity, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("state_dir %s: %w", stateDir, err)
	}
	id := &Identity{stateDir: stateDir}

	nodeID, err := ladeOderErzeuge(filepath.Join(stateDir, "node_id"), func() ([]byte, error) {
		return []byte(uuid.NewV4().String() + "\n"), nil
	})
	if err != nil {
		return nil, err
	}
	id.NodeID = strings.TrimSpace(string(nodeID))
	if _, err := uuid.Parse(id.NodeID); err != nil {
		return nil, fmt.Errorf("%s enthält keine UUID: %q — die Datei von Hand zu löschen erzeugt eine neue Identität und trennt damit die Historie beim Server ab",
			filepath.Join(stateDir, "node_id"), id.NodeID)
	}

	key, err := ladeOderErzeuge(filepath.Join(stateDir, "node_key"), func() ([]byte, error) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return []byte(base64.StdEncoding.EncodeToString(priv) + "\n"), nil
	})
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(key)))
	if err != nil {
		return nil, fmt.Errorf("%s ist nicht lesbar: %w", filepath.Join(stateDir, "node_key"), err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s hat %d Byte, erwartet werden %d", filepath.Join(stateDir, "node_key"), len(raw), ed25519.PrivateKeySize)
	}
	id.privKey = ed25519.PrivateKey(raw)
	id.PubKey = id.privKey.Public().(ed25519.PublicKey)
	return id, nil
}

// PubKeyBase64 ist der öffentliche Schlüssel, wie er in jeden Datensatz geht.
//
// Signiert wird vorerst nicht — eine Authentifizierung an der API ist nicht
// vorgesehen. Der Schlüssel entsteht trotzdem am ersten Tag und ist ab dem
// ersten Datensatz mit den Daten verknüpft: Wird Signieren später nachgerüstet,
// muss kein Knoten neu identifiziert werden. Kosten heute: 32 Byte je Datensatz.
func (i *Identity) PubKeyBase64() string {
	return base64.StdEncoding.EncodeToString(i.PubKey)
}

// Sign ist vorbereitet, wird aber heute nicht aufgerufen. Siehe PubKeyBase64.
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.privKey, msg) }

// StateDir gibt das Verzeichnis, in dem alles Dauerhafte liegt.
func (i *Identity) StateDir() string { return i.stateDir }

// NextSeqStart liest die zuletzt vergebene Sequenznummer, schlägt seqSprung
// auf und schreibt das Ergebnis zurück. Der Rückgabewert ist die erste Nummer,
// die dieser Prozess vergeben darf.
func (i *Identity) NextSeqStart() (uint64, error) {
	path := filepath.Join(i.stateDir, "seq")
	var letzte uint64
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		v, perr := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if perr != nil {
			// Eine unlesbare seq-Datei darf den Start nicht verhindern; sie
			// darf aber auch keine Nummer wiederverwenden. Deshalb wird von
			// vorn begonnen und der Fall dem Aufrufer gemeldet.
			return seqSprung, fmt.Errorf("%s ist unlesbar (%q); die Zählung beginnt bei %d neu", path, strings.TrimSpace(string(raw)), seqSprung)
		}
		letzte = v
	case errors.Is(err, fs.ErrNotExist):
		letzte = 0
	default:
		return 0, fmt.Errorf("%s: %w", path, err)
	}

	start := letzte + seqSprung
	if err := SchreibeAtomar(path, []byte(strconv.FormatUint(start, 10)+"\n"), 0o600); err != nil {
		return start, fmt.Errorf("%s fortschreiben: %w", path, err)
	}
	return start, nil
}

// SaveSeq schreibt die zuletzt vergebene Nummer fest. Aufgerufen wird das beim
// geordneten Beenden — im Betrieb bleibt die Platte in Ruhe.
func (i *Identity) SaveSeq(seq uint64) error {
	return SchreibeAtomar(filepath.Join(i.stateDir, "seq"), []byte(strconv.FormatUint(seq, 10)+"\n"), 0o600)
}

// SchreibeAtomar schreibt in eine Nebendatei, ruft fsync und benennt um.
// Halbe Dateien darf es nicht geben — weder im Spool noch bei der Identität.
func SchreibeAtomar(path string, data []byte, perm fs.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// Windows benennt nicht über eine bestehende Datei hinweg um.
	if err := os.Rename(tmp, path); err != nil {
		if rmErr := os.Remove(path); rmErr == nil {
			if err2 := os.Rename(tmp, path); err2 == nil {
				return nil
			}
		}
		os.Remove(tmp)
		return err
	}
	return nil
}

// ladeOderErzeuge liest die Datei oder legt sie mit erzeuge() an, mit Modus 0600.
func ladeOderErzeuge(path string, erzeuge func() ([]byte, error)) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, fmt.Errorf("%s ist leer", path)
		}
		return raw, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	data, err := erzeuge()
	if err != nil {
		return nil, fmt.Errorf("%s erzeugen: %w", path, err)
	}
	if err := SchreibeAtomar(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("%s schreiben: %w", path, err)
	}
	return data, nil
}
