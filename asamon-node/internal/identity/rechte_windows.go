// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package identity

// RechteHinweis meldet, wenn die Dateirechte im state_dir nicht das leisten,
// was sie sollen.
//
// Unter Windows leisten sie es nicht: Der Modus 0600, mit dem node_key
// angelegt wird, ist dort weitgehend wirkungslos — die tatsächlichen Rechte
// kommen aus den vererbten ACLs des übergeordneten Verzeichnisses. Wer
// %ProgramData%\asamon für alle beschreibbar lässt, hat einen für alle
// lesbaren privaten Schlüssel.
//
// Heute wiegt das wenig: Signiert wird nicht, und der Schlüssel schützt nichts
// (TODO.md Abschnitt 5). Es wiegt an dem Tag, an dem Signieren nachgerüstet
// wird — und genau dann erinnert sich niemand mehr daran. Deshalb steht es im
// Log, statt in einer Fußnote zu verschwinden.
func RechteHinweis() string {
	return "Windows erzwingt den Modus 0600 nicht: Die Rechte an node_key kommen aus den " +
		"vererbten ACLs des state_dir. Wer den Schlüssel schützen will, nimmt dem " +
		"Verzeichnis den Zugriff für andere Konten (icacls <state_dir> /inheritance:r " +
		"/grant:r %USERNAME%:F)."
}
