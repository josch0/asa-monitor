// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse liest den YAML-Text, füllt die Vorgaben auf und prüft alles.
//
// KnownFields(true) heißt: Ein Schlüssel, den das Schema nicht kennt, ist ein
// Fehler. Das ist Absicht — ein vertipptes `report_intervall` bliebe sonst
// stillschweigend wirkungslos, und der Knoten liefe scheinbar richtig.
func Parse(raw []byte) (*Config, []Warnung, error) {
	cfg := Defaults()

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, errors.New("die Konfigurationsdatei ist leer")
		}
		return nil, nil, fmt.Errorf("YAML: %w%s", err, hinweisZuUnbekanntemFeld(err))
	}

	// Eine zweite Auswertung darf es nicht geben: Mehrere YAML-Dokumente in
	// einer Datei wären zwei Konfigurationen, von denen nur die erste wirkt.
	var weiteres yaml.Node
	if err := dec.Decode(&weiteres); err == nil {
		return nil, nil, errors.New("die Datei enthält mehr als ein YAML-Dokument; erwartet wird genau eines")
	} else if !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("YAML: %w", err)
	}

	warnungen, err := cfg.Validate()
	if err != nil {
		return nil, nil, err
	}
	return &cfg, warnungen, nil
}

// hinweisZuUnbekanntemFeld hängt an die Fehlermeldung des YAML-Lesers den Grund
// an, warum unbekannte Schlüssel hier nicht durchgehen.
func hinweisZuUnbekanntemFeld(err error) string {
	if !strings.Contains(err.Error(), "not found in type") {
		return ""
	}
	return "\nUnbekannte Schlüssel werden nicht überlesen: ein Tippfehler bliebe sonst wirkungslos, ohne dass es jemand merkt.\n" +
		"Alle Optionen stehen in docs/node-config.md, ein vollständiges Beispiel in contrib/node-config.example.yaml"
}
