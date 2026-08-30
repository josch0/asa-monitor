// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package supervisor

// uhrSynchronisiert meldet unter Windows immer false — und das ist eine
// Auskunft über dieses Programm, nicht über die Uhr.
//
// Windows hält seine Uhr per Vorgabe über den Dienst W32Time synchron. Das
// **zu bestätigen** ginge nur über die Dienststeuerung oder die Registry, also
// über advapi32-Aufrufe von Hand oder eine zweite Fremdabhängigkeit — für ein
// einziges Telemetriefeld ist das zu viel. Ein Prozessaufruf je Datensatz
// kommt erst recht nicht in Frage.
//
// Für die Serverseite heißt das: `ntp_synchronized: false` von einem
// Windows-Knoten bedeutet **nicht bestätigt**, nicht "Uhr falsch". Die
// belastbare Größe ist `ens_time_offset_ms` im selben Datensatz — sie misst
// den Abstand zwischen Knotenuhr und Ensemble-Zeit und gilt auf jeder
// Plattform gleich. Vermerkt in docs/uplink-protokoll.md.
func uhrSynchronisiert() bool { return false }
