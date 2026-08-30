// SPDX-License-Identifier: GPL-3.0-or-later
#pragma once

#include <string>

namespace asamon {

enum class LogLevel { Error = 0, Warn = 1, Info = 2, Debug = 3 };

struct Options {
    std::string channel;                 // Pflicht, z. B. "5C"
    std::string device      = "rtl_sdr"; // rtl_sdr | rtl_tcp | airspy | soapysdr | rawfile | auto
    std::string iqFile;                  // Quelle fuer --device rawfile
    std::string iqFormat    = "u8";      // u8 | s8 | s16le | s16be | complexf
    std::string gain        = "auto";    // "auto" oder Verstaerkungsindex
    LogLevel    logLevel    = LogLevel::Info;

    // Beschraenkte Warteschlange des Ausgabethreads. Im Ueberlauf wird
    // verworfen, nicht blockiert (TODO.md Abschnitt 8).
    size_t      queueSize   = 4096;

    // Notbremse fuer den Recorder: laeuft REC laenger, wird von selbst gestoppt.
    unsigned    recMaxSeconds = 600;

    // Ablageordner der Mitschnitte. Leer heisst nicht "aus", sondern "noch
    // nicht gesetzt": parseOptions() traegt dann die Vorgabe des Systems ein
    // (defaultAudioDir() in platform.h), und die entspricht dem, was
    // asamon-node ohne paths:-Abschnitt annimmt. Ein Knoten, der den Ordner
    // verlegt, gibt ihn seinem Kind mit --audio-out weiter.
    std::string audioOut;

    // MP3-Bitrate in kbit/s. 0 schaltet die MP3 ab — der Ausweg fuer einen Bau
    // ohne LAME; die .dabp entsteht dann trotzdem.
    int         mp3Bitrate = 64;
};

// Wertet argv aus. Bei --help/--version wird die Ausgabe geschrieben und
// `exitRequested` gesetzt; bei einem Fehler wird eine Meldung nach stderr
// geschrieben und false zurueckgegeben.
bool parseOptions(int argc, char** argv, Options& out, bool& exitRequested);

void logMessage(LogLevel configured, LogLevel level, const std::string& text);

}  // namespace asamon
