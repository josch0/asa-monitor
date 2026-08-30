// SPDX-License-Identifier: GPL-3.0-or-later
//
// Alles, was sich zwischen Unix und Windows unterscheidet — und nur das.
//
// Der Rest von asamon-rx soll plattformfrei bleiben. Ob das eingehalten ist,
// laesst sich nachsehen: Ausser in platform_posix.cpp und platform_windows.cpp
// darf in src/ kein <poll.h>, <unistd.h>, <sys/*.h> und kein <windows.h>
// stehen.
//
// Seit Patch 3 des welle.io-Forks steht hier nur noch der Weg fuer stdin. Bis
// dahin lag daneben die MSC-Leitung: eine FIFO unter Unix, eine Named Pipe mit
// ueberlappter E/A unter Windows, zusammen ueber 280 Zeilen. Der rohe
// MSC-Strom kommt inzwischen als Rueckruf herein (src/recorder.h).
//
// Was bleibt, bleibt aus einem Grund: Der Empfangsprozess muss sich jederzeit
// in unter einer Sekunde abbrechen lassen. Ein blockierendes read() auf stdin
// wuerde das Herunterfahren an einer Gegenstelle aufhaengen, die vielleicht nie
// wieder etwas schickt.

#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <string>

namespace asamon {

// Rueckgabewerte von readWithTimeout(). Werte > 0 sind gelesene Bytes.
constexpr long kReadTimeout = 0;   // Zeitscheibe abgelaufen, nichts da
constexpr long kReadClosed = -1;   // Gegenstelle weg — regulaeres Ende
constexpr long kReadFailed = -2;   // Fehler; `error` ist gesetzt

// Vorgabe fuer --audio-out.
//
// Sie folgt dem, was asamon-node ohne paths:-Abschnitt annimmt, damit Knoten
// und Kind ohne Absprache denselben Ordner meinen:
//
//   Unix     /var/lib/asamon/audio
//   Windows  %ProgramData%\asamon\state\audio
//
// Wer den Ordner verlegt, gibt ihn beiden Programmen mit — dem Knoten ueber
// paths.state_dir, dem Kind ueber --audio-out.
std::string defaultAudioDir();

// Setzt das Flag, wenn das Betriebssystem den Abbruch verlangt.
//
// Unix: SIGINT und SIGTERM; zusaetzlich wird SIGPIPE ignoriert, damit ein
// weggebrochener Leser den Prozess nicht hart beendet, sondern der Writer den
// Fehler melden kann.
//
// Windows: SetConsoleCtrlHandler fuer CTRL_C, CTRL_CLOSE und CTRL_SHUTDOWN.
// Der Handler laeuft dort in einem eigenen Thread, und bei CTRL_CLOSE gibt
// Windows nur wenige Sekunden — das genuegt, weil der Knoten ohnehin QUIT
// ueber stdin schickt und dieser Weg nur die zweite Verteidigungslinie ist.
void installShutdownHandler(std::atomic<bool>& flag);

// Liest von stdin mit Zeitscheibe.
//
// Blockierend zu lesen ginge nicht: Beim Herunterfahren haengt der Thread sonst
// an einem stdin, das nie schliesst — im Feldtest laeuft asamon-rx haeufig ganz
// ohne Gegenstelle auf stdin.
class StdinReader {
public:
    StdinReader();

    StdinReader(const StdinReader&) = delete;
    StdinReader& operator=(const StdinReader&) = delete;

    long readWithTimeout(void* buffer, std::size_t size, int timeoutMs,
                         std::string& error);

private:
    // Unter Windows haengt der Weg davon ab, was an stdin haengt: eine Pipe
    // (der Regelfall, asamon-node), eine Datei oder eine Konsole.
    int kind_ = 0;
};

}  // namespace asamon
