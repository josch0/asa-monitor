// SPDX-License-Identifier: GPL-3.0-or-later
//
// Zeilenkommandos auf stdin. Der Punkt, auf den es ankommt: unbekannte Zeilen
// werden gezaehlt und geloggt, nicht stillschweigend verworfen.

#include "commands.h"

#include <iostream>
#include <string>
#include <vector>

using namespace asamon;

namespace {

int g_failures = 0;

void check(bool condition, const std::string& what)
{
    if (!condition) {
        std::cerr << "FEHLGESCHLAGEN: " << what << "\n";
        ++g_failures;
    }
}

}  // namespace

int main()
{
    Options options;
    options.logLevel = LogLevel::Error;   // Warnungen im Test nicht ausgeben

    std::vector<int> recorded;
    std::vector<int> stopped;
    int quits = 0;

    CommandReader::Handlers handlers;
    handlers.onRec  = [&](uint8_t id) { recorded.push_back(id); };
    handlers.onStop = [&](uint8_t id) { stopped.push_back(id); };
    handlers.onQuit = [&] { ++quits; };

    CommandReader reader(options, handlers);

    check(reader.handleLine("REC 7"), "REC 7 angenommen");
    check(reader.handleLine("STOP 7"), "STOP 7 angenommen");
    check(reader.handleLine("QUIT"), "QUIT angenommen");
    check(recorded.size() == 1 && recorded[0] == 7, "REC gibt die SubChId weiter");
    check(stopped.size() == 1 && stopped[0] == 7, "STOP gibt die SubChId weiter");
    check(quits == 1, "QUIT einmal ausgeloest");

    // Randwerte: SubChId ist ein 6-bit-Feld.
    check(reader.handleLine("REC 0"), "SubChId 0 ist gueltig");
    check(reader.handleLine("REC 63"), "SubChId 63 ist gueltig");
    check(!reader.handleLine("REC 64"), "SubChId 64 wird abgelehnt");
    check(!reader.handleLine("REC -1"), "negative SubChId wird abgelehnt");
    check(!reader.handleLine("REC abc"), "nicht-numerische SubChId wird abgelehnt");
    check(!reader.handleLine("REC"), "REC ohne Argument wird abgelehnt");

    // Unbekanntes bleibt sichtbar.
    check(!reader.handleLine("FIBDUMP"), "unbekanntes Kommando wird abgelehnt");
    check(!reader.handleLine("rec 7"), "Kleinschreibung ist kein Kommando");
    // Sechs abgelehnte Zeilen: REC 64, REC -1, REC abc, REC, FIBDUMP, rec 7.
    check(reader.unknownCommands() == 6,
          "jede abgelehnte Zeile ist gezaehlt (bekommen: " +
              std::to_string(reader.unknownCommands()) + ")");

    // Leerzeilen und CRLF sind kein Fehler.
    const uint64_t before = reader.unknownCommands();
    check(reader.handleLine(""), "Leerzeile wird uebergangen");
    check(reader.handleLine("  "), "Nur-Leerzeichen wird uebergangen");
    check(reader.handleLine("REC 12\r"), "CRLF-Zeile wird angenommen");
    check(reader.unknownCommands() == before, "nichts davon zaehlt als unbekannt");
    check(recorded.size() == 4 && recorded.back() == 12, "auch die CRLF-Zeile wirkt");

    if (g_failures == 0) {
        std::cerr << "test_commands: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_commands: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
