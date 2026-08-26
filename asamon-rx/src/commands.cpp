// SPDX-License-Identifier: GPL-3.0-or-later

#include "commands.h"

#include <cerrno>
#include <cstring>
#include <utility>

#include <poll.h>
#include <unistd.h>

namespace asamon {

namespace {

// Schneidet Leerzeichen und ein etwaiges \r ab — Zeilen koennen aus einer
// Datei mit CRLF stammen.
std::string trim(const std::string& in)
{
    size_t begin = 0;
    size_t end = in.size();
    while (begin < end && (in[begin] == ' ' || in[begin] == '\t')) ++begin;
    while (end > begin && (in[end - 1] == ' ' || in[end - 1] == '\t' ||
                           in[end - 1] == '\r' || in[end - 1] == '\n')) {
        --end;
    }
    return in.substr(begin, end - begin);
}

bool parseSubChId(const std::string& text, uint8_t& out)
{
    if (text.empty()) return false;
    for (const char c : text) {
        if (c < '0' || c > '9') return false;
    }
    try {
        const unsigned long value = std::stoul(text);
        if (value > 63) return false;  // SubChId ist ein 6-bit-Feld
        out = static_cast<uint8_t>(value);
        return true;
    } catch (const std::exception&) {
        return false;
    }
}

}  // namespace

CommandReader::CommandReader(const Options& options, Handlers handlers)
    : options_(options), handlers_(std::move(handlers))
{
}

CommandReader::~CommandReader()
{
    stop();
}

void CommandReader::start()
{
    if (thread_.joinable()) return;
    thread_ = std::thread(&CommandReader::run, this);
}

void CommandReader::stop()
{
    stopping_.store(true, std::memory_order_relaxed);
    if (thread_.joinable()) thread_.join();
}

bool CommandReader::handleLine(const std::string& rawLine)
{
    const std::string line = trim(rawLine);
    if (line.empty()) return true;

    const size_t space = line.find(' ');
    const std::string verb = line.substr(0, space);
    const std::string argument =
        space == std::string::npos ? std::string() : trim(line.substr(space + 1));

    if (verb == "QUIT") {
        if (handlers_.onQuit) handlers_.onQuit();
        return true;
    }

    if (verb == "REC" || verb == "STOP") {
        uint8_t subChId = 0;
        if (!parseSubChId(argument, subChId)) {
            unknown_.fetch_add(1, std::memory_order_relaxed);
            logMessage(options_.logLevel, LogLevel::Warn,
                       verb + ": \"" + argument +
                           "\" ist keine SubChId (0-63)");
            return false;
        }
        if (verb == "REC") {
            if (handlers_.onRec) handlers_.onRec(subChId);
        }
        else {
            if (handlers_.onStop) handlers_.onStop(subChId);
        }
        return true;
    }

    unknown_.fetch_add(1, std::memory_order_relaxed);
    logMessage(options_.logLevel, LogLevel::Warn,
               "unbekanntes Kommando: \"" + line + "\"");
    return false;
}

void CommandReader::run()
{
    std::string pending;
    char buffer[512];

    while (!stopping_.load(std::memory_order_relaxed)) {
        pollfd pfd{};
        pfd.fd = STDIN_FILENO;
        pfd.events = POLLIN;

        // Mit Zeitscheibe statt blockierendem Lesen: sonst haenge dieser
        // Thread beim Herunterfahren an einem stdin, das nie schliesst.
        const int ready = ::poll(&pfd, 1, 200);
        if (ready < 0) {
            if (errno == EINTR) continue;
            logMessage(options_.logLevel, LogLevel::Error,
                       "stdin: poll fehlgeschlagen: " +
                           std::string(std::strerror(errno)));
            return;
        }
        if (ready == 0) continue;

        const ssize_t got = ::read(STDIN_FILENO, buffer, sizeof(buffer));
        if (got < 0) {
            if (errno == EINTR || errno == EAGAIN) continue;
            logMessage(options_.logLevel, LogLevel::Error,
                       "stdin: read fehlgeschlagen: " +
                           std::string(std::strerror(errno)));
            return;
        }
        if (got == 0) {
            // stdin geschlossen. Kein Grund zu beenden: im Feldtest laeuft
            // asamon-rx haeufig ohne Gegenstelle auf stdin.
            logMessage(options_.logLevel, LogLevel::Debug,
                       "stdin geschlossen, keine weiteren Kommandos");
            return;
        }

        for (ssize_t i = 0; i < got; ++i) {
            if (buffer[i] == '\n') {
                handleLine(pending);
                pending.clear();
            }
            else {
                pending += buffer[i];
                if (pending.size() > 4096) {   // Schutz gegen endlose Zeilen
                    unknown_.fetch_add(1, std::memory_order_relaxed);
                    logMessage(options_.logLevel, LogLevel::Warn,
                               "stdin: uebermaessig lange Zeile verworfen");
                    pending.clear();
                }
            }
        }
    }
}

}  // namespace asamon
