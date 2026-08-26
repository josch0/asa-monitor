// SPDX-License-Identifier: GPL-3.0-or-later
//
// Der FIFO-Pfad des Recorders, ohne Empfaenger und ohne Funk: eine FIFO, ein
// Schreiber, fertige aud-Records.
//
// Geprueft wird genau das, was am Geraet schwer zu sehen ist — dass der Leser
// steht, bevor der Schreiber kommt, dass ein Abbruch ohne Schreiber nicht
// haengt, und dass die Nutzdaten unveraendert und in der richtigen Reihenfolge
// wieder herauskommen.

#include "recorder.h"
#include "record.h"
#include "writer.h"

#include <atomic>
#include <cstdio>
#include <cstring>
#include <iostream>
#include <string>
#include <thread>
#include <vector>

#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

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

std::vector<std::string> linesWrittenTo(std::FILE* file)
{
    std::vector<std::string> lines;
    std::rewind(file);
    std::string current;
    int ch;
    while ((ch = std::fgetc(file)) != EOF) {
        if (ch == '\n') {
            lines.push_back(current);
            current.clear();
        }
        else {
            current += static_cast<char>(ch);
        }
    }
    if (!current.empty()) lines.push_back(current);
    return lines;
}

bool contains(const std::string& haystack, const std::string& needle)
{
    return haystack.find(needle) != std::string::npos;
}

std::string tempFifoPath()
{
    return "/tmp/asamon-rx-test-" + std::to_string(::getpid()) + ".fifo";
}

void testFifoToRecords()
{
    const std::string path = tempFifoPath();
    ::unlink(path.c_str());
    check(::mkfifo(path.c_str(), 0600) == 0, "FIFO angelegt");

    std::FILE* file = std::tmpfile();
    if (file == nullptr) {
        check(false, "tmpfile angelegt");
        return;
    }

    Options options;
    options.logLevel = LogLevel::Error;
    Writer writer(file, 256);
    writer.start();

    std::atomic<bool> stopping{false};
    std::thread pump([&] {
        pumpFifoToRecords(writer, options, path, 7, stopping);
    });

    // Der Schreiber kommt nach dem Leser — so herum laeuft es auch im
    // Betrieb, wenn welle.io die FIFO oeffnet.
    {
        const int fd = ::open(path.c_str(), O_WRONLY);
        check(fd >= 0, "Schreiber konnte oeffnen");
        if (fd >= 0) {
            const char payload[] = "MSC-Bitstrom";
            check(::write(fd, payload, sizeof(payload) - 1) ==
                      static_cast<ssize_t>(sizeof(payload) - 1),
                  "Nutzdaten geschrieben");
            ::close(fd);
        }
    }

    // Kurz Zeit lassen, dann abbrechen.
    std::this_thread::sleep_for(std::chrono::milliseconds(400));
    stopping.store(true);
    pump.join();
    writer.stop();

    const auto lines = linesWrittenTo(file);
    check(!lines.empty(), "mindestens ein aud-Record");
    if (!lines.empty()) {
        check(contains(lines[0], "\"type\":\"aud\""), "aud-Record erzeugt");
        check(contains(lines[0], "\"subch_id\":7"), "SubChId uebernommen");
        check(contains(lines[0], "\"chunk\":0"), "Stuecknummer beginnt bei 0");
        // Base64 von "MSC-Bitstrom"
        check(contains(lines[0], "\"data\":\"TVNDLUJpdHN0cm9t\""),
              "Nutzdaten unveraendert");
    }

    std::fclose(file);
    ::unlink(path.c_str());
}

void testStopWithoutWriter()
{
    // Kommt nie ein Schreiber, muss sich der Leser trotzdem abbrechen lassen.
    // Genau das leistet das nichtblockierende Oeffnen; mit blockierendem
    // open() haenge dieser Test bis in alle Ewigkeit.
    const std::string path = tempFifoPath() + ".idle";
    ::unlink(path.c_str());
    check(::mkfifo(path.c_str(), 0600) == 0, "FIFO angelegt");

    std::FILE* file = std::tmpfile();
    if (file == nullptr) {
        check(false, "tmpfile angelegt");
        return;
    }

    Options options;
    options.logLevel = LogLevel::Error;
    Writer writer(file, 16);
    writer.start();

    std::atomic<bool> stopping{false};
    const auto begin = Clock::now();
    std::thread pump([&] {
        pumpFifoToRecords(writer, options, path, 3, stopping);
    });

    stopping.store(true);
    pump.join();
    const auto elapsed = std::chrono::duration_cast<std::chrono::milliseconds>(
                             Clock::now() - begin)
                             .count();
    check(elapsed < 2000, "Abbruch ohne Schreiber dauert unter zwei Sekunden");

    writer.stop();
    check(linesWrittenTo(file).empty(), "ohne Daten auch keine Records");

    std::fclose(file);
    ::unlink(path.c_str());
}

}  // namespace

int main()
{
    testFifoToRecords();
    testStopWithoutWriter();

    if (g_failures == 0) {
        std::cerr << "test_recorder: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_recorder: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
