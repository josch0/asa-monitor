// SPDX-License-Identifier: GPL-3.0-or-later

#include "options.h"

#include "platform.h"
#include "version.h"

#include <cstring>
#include <iostream>
#include <mutex>

namespace asamon {

namespace {

const char* kUsage =
    "asamon-rx --channel 5C [Optionen]\n"
    "\n"
    "  --channel <name>       DAB-Kanal, z. B. 5C, 11D, 7B (Pflicht)\n"
    "  --device <name>        rtl_sdr (Vorgabe) | rtl_tcp | airspy | soapysdr | rawfile | auto\n"
    "  --iq-file <pfad>       Quelle fuer --device rawfile\n"
    "  --iq-format <format>   u8 (Vorgabe) | s8 | s16le | s16be | complexf\n"
    "  --gain auto|<index>    Vorgabe: auto\n"
    "  --queue-size <n>       Tiefe der Ausgabe-Warteschlange (Vorgabe: 4096)\n"
    "  --rec-max-seconds <n>  Notbremse fuer REC (Vorgabe: 600, 0 = aus)\n"
    "  --audio-out <pfad>     Ablageordner der Mitschnitte (Vorgabe: der Ort,\n"
    "                         den auch asamon-node ohne paths: annimmt)\n"
    "  --mp3-bitrate <n>      MP3 in kbit/s (Vorgabe: 64, 0 = keine MP3)\n"
    "  --log-level <stufe>    error|warn|info|debug (Vorgabe: info)\n"
    "  --version\n"
    "  --help\n"
    "\n"
    "Records gehen nach stdout, Logs nach stderr. Immer, ohne Ausnahme.\n"
    "Mitschnitte gehen weder noch: REC schreibt sie als <name>.dabp und\n"
    "<name>.mp3 in den Ablageordner; erst der abschliessende aud-Record\n"
    "nennt sie, mit Groesse und SHA-256.\n";

bool parseLogLevel(const std::string& s, LogLevel& out)
{
    if (s == "error") { out = LogLevel::Error; return true; }
    if (s == "warn")  { out = LogLevel::Warn;  return true; }
    if (s == "info")  { out = LogLevel::Info;  return true; }
    if (s == "debug") { out = LogLevel::Debug; return true; }
    return false;
}

const char* levelName(LogLevel l)
{
    switch (l) {
        case LogLevel::Error: return "error";
        case LogLevel::Warn:  return "warn";
        case LogLevel::Info:  return "info";
        case LogLevel::Debug: return "debug";
    }
    return "?";
}

// Ein Argument, das einen Wert braucht, holen. Meldet den Fehler selbst.
bool takeValue(int argc, char** argv, int& i, const char* name, std::string& out)
{
    if (i + 1 >= argc) {
        std::cerr << "asamon-rx: " << name << " braucht einen Wert\n";
        return false;
    }
    out = argv[++i];
    return true;
}

std::mutex logMutex;

}  // namespace

bool parseOptions(int argc, char** argv, Options& out, bool& exitRequested)
{
    exitRequested = false;

    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        std::string value;

        if (arg == "--help" || arg == "-h") {
            std::cerr << kUsage;
            exitRequested = true;
            return true;
        }
        if (arg == "--version") {
            std::cerr << "asamon-rx " ASAMON_RX_VERSION
                         " (commit " ASAMON_RX_COMMIT
                         ", welle.io " ASAMON_RX_WELLE_COMMIT ")\n";
            exitRequested = true;
            return true;
        }
        if (arg == "--channel") {
            if (!takeValue(argc, argv, i, "--channel", out.channel)) return false;
        }
        else if (arg == "--device") {
            if (!takeValue(argc, argv, i, "--device", out.device)) return false;
        }
        else if (arg == "--iq-file") {
            if (!takeValue(argc, argv, i, "--iq-file", out.iqFile)) return false;
        }
        else if (arg == "--iq-format") {
            if (!takeValue(argc, argv, i, "--iq-format", out.iqFormat)) return false;
        }
        else if (arg == "--gain") {
            if (!takeValue(argc, argv, i, "--gain", out.gain)) return false;
        }
        else if (arg == "--queue-size") {
            if (!takeValue(argc, argv, i, "--queue-size", value)) return false;
            try {
                const long n = std::stol(value);
                if (n < 8) throw std::out_of_range("zu klein");
                out.queueSize = static_cast<size_t>(n);
            } catch (const std::exception&) {
                std::cerr << "asamon-rx: --queue-size braucht eine Zahl >= 8\n";
                return false;
            }
        }
        else if (arg == "--rec-max-seconds") {
            if (!takeValue(argc, argv, i, "--rec-max-seconds", value)) return false;
            try {
                out.recMaxSeconds = static_cast<unsigned>(std::stoul(value));
            } catch (const std::exception&) {
                std::cerr << "asamon-rx: --rec-max-seconds braucht eine Zahl\n";
                return false;
            }
        }
        else if (arg == "--audio-out") {
            if (!takeValue(argc, argv, i, "--audio-out", out.audioOut)) return false;
        }
        else if (arg == "--mp3-bitrate") {
            if (!takeValue(argc, argv, i, "--mp3-bitrate", value)) return false;
            try {
                const long n = std::stol(value);
                if (n != 0 && (n < 8 || n > 320)) throw std::out_of_range("Bereich");
                out.mp3Bitrate = static_cast<int>(n);
            } catch (const std::exception&) {
                std::cerr << "asamon-rx: --mp3-bitrate braucht 0 oder 8..320\n";
                return false;
            }
        }
        else if (arg == "--log-level") {
            if (!takeValue(argc, argv, i, "--log-level", value)) return false;
            if (!parseLogLevel(value, out.logLevel)) {
                std::cerr << "asamon-rx: unbekannte Log-Stufe \"" << value
                          << "\" (error|warn|info|debug)\n";
                return false;
            }
        }
        else {
            std::cerr << "asamon-rx: unbekanntes Argument \"" << arg << "\"\n\n" << kUsage;
            return false;
        }
    }

    if (out.channel.empty()) {
        std::cerr << "asamon-rx: --channel fehlt\n\n" << kUsage;
        return false;
    }
    if (out.device == "rawfile" && out.iqFile.empty()) {
        std::cerr << "asamon-rx: --device rawfile braucht --iq-file\n";
        return false;
    }
    // Leer heisst "nicht gesetzt", nicht "aus": Ohne --audio-out gilt der Ort,
    // den auch asamon-node ohne paths:-Abschnitt annimmt. Angelegt wird er
    // erst beim ersten REC — ein Empfangsprozess, der nie aufnimmt, soll
    // nichts hinterlassen.
    if (out.audioOut.empty()) out.audioOut = defaultAudioDir();
    return true;
}

void logMessage(LogLevel configured, LogLevel level, const std::string& text)
{
    if (static_cast<int>(level) > static_cast<int>(configured)) return;
    std::lock_guard<std::mutex> lock(logMutex);
    std::cerr << "[" << levelName(level) << "] " << text << std::endl;
}

}  // namespace asamon
