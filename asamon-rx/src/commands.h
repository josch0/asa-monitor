// SPDX-License-Identifier: GPL-3.0-or-later
//
// Zeilenkommandos auf stdin: ASCII, eine Zeile je Kommando, \n-terminiert.
//
//   REC <subChId> [alert_uid]  Subchannel zuschalten und mitschneiden
//   STOP <subChId>             Mitschnitt beenden
//   QUIT                       sauber herunterfahren
//
// Die alert_uid ist freiwillig und wird nicht gedeutet — asamon-rx kennt kein
// ASA. Sie dient allein der Benennung: Gibt der Knoten sie mit, heissen die
// Dateien von vornherein so, wie er sie kennt, und niemand muss hinterher
// umbenennen. Bleibt sie aus, tritt der Startzeitpunkt an ihre Stelle.
//
// Unbekannte Zeilen werden gezaehlt und geloggt, nicht stillschweigend
// verworfen.

#pragma once

#include "options.h"

#include <atomic>
#include <cstdint>
#include <functional>
#include <string>
#include <thread>

namespace asamon {

class CommandReader {
public:
    struct Handlers {
        std::function<void(uint8_t, const std::string&)> onRec;
        std::function<void(uint8_t)> onStop;
        std::function<void()>        onQuit;
    };

    CommandReader(const Options& options, Handlers handlers);
    ~CommandReader();

    CommandReader(const CommandReader&) = delete;
    CommandReader& operator=(const CommandReader&) = delete;

    void start();
    void stop();

    uint64_t unknownCommands() const
    {
        return unknown_.load(std::memory_order_relaxed);
    }

    // Wertet eine einzelne Zeile aus. Oeffentlich, damit die Tests sie ohne
    // Thread und ohne stdin pruefen koennen.
    bool handleLine(const std::string& line);

private:
    void run();

    const Options& options_;
    Handlers handlers_;
    std::atomic<bool> stopping_{false};
    std::atomic<uint64_t> unknown_{0};
    std::thread thread_;
};

}  // namespace asamon
