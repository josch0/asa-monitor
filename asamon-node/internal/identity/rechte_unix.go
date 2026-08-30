// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package identity

// RechteHinweis meldet, wenn die Dateirechte im state_dir nicht das leisten,
// was sie sollen.
//
// Unter Unix leisten sie es: node_id, node_key und seq werden mit Modus 0600
// angelegt, das state_dir selbst mit 0700. Der private Schlüssel ist damit nur
// für den Benutzer lesbar, unter dem der Knoten läuft.
func RechteHinweis() string { return "" }
