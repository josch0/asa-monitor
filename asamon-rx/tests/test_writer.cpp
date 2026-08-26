// SPDX-License-Identifier: GPL-3.0-or-later
//
// Ausgabethread: Reihenfolge, Nummerierung und die Vorrangregel beim
// Verwerfen. Der Ueberlauf ist der Fall, auf den es ankommt — er darf nie
// blockieren und nie unsichtbar bleiben.

#include "writer.h"

#include <cstdio>
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

// Liest zurueck, was der Writer in eine temporaere Datei geschrieben hat.
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

void testOrderAndSequence()
{
    std::FILE* file = std::tmpfile();
    check(file != nullptr, "tmpfile angelegt");
    if (file == nullptr) return;

    {
        Writer writer(file, 64);
        writer.start();

        InitPayload init;
        init.channel = "5C";
        writer.enqueue(RecordKind::Init, init);
        for (int i = 0; i < 5; ++i) {
            writer.enqueue(RecordKind::Tlm, TlmPayload{});
        }
        writer.stop();
    }

    const auto lines = linesWrittenTo(file);
    check(lines.size() == 6, "alle sechs Records geschrieben");
    if (lines.size() == 6) {
        check(contains(lines[0], "\"type\":\"init\",\"seq\":0"), "init ist die erste Zeile");
        for (size_t i = 1; i < lines.size(); ++i) {
            check(contains(lines[i], "\"seq\":" + std::to_string(i)),
                  "seq laeuft fortlaufend weiter");
        }
    }
    std::fclose(file);
}

void testOverflowDropsByPriority()
{
    std::FILE* file = std::tmpfile();
    if (file == nullptr) {
        check(false, "tmpfile angelegt");
        return;
    }

    std::vector<std::string> lines;
    {
        // Der Writer wird bewusst nicht gestartet: so fuellt sich die
        // Warteschlange, ohne dass nebenher geleert wird.
        Writer writer(file, 8);

        for (int i = 0; i < 8; ++i) {
            check(writer.enqueue(RecordKind::Tlm, TlmPayload{}),
                  "tlm passt in die freie Warteschlange");
        }
        check(writer.dropped() == 0, "noch nichts verworfen");

        // Voll. Ein asa-Record hat Vorrang: ein tlm muss weichen.
        AsaPayload alert;
        alert.heartbeat = true;
        check(writer.enqueue(RecordKind::Asa, alert), "asa verdraengt ein tlm");
        check(writer.dropped() == 1, "der Verwurf wird gezaehlt");

        // Ein weiteres tlm findet kein Opfer geringeren Rangs und faellt
        // selbst heraus — blockieren darf es nie.
        check(!writer.enqueue(RecordKind::Tlm, TlmPayload{}),
              "tlm wird verworfen statt zu blockieren");
        check(writer.dropped() == 2, "auch dieser Verwurf wird gezaehlt");

        // aud steht zwischen beiden: es verdraengt tlm, aber nicht asa.
        AudPayload audio;
        audio.subChId = 7;
        check(writer.enqueue(RecordKind::Aud, audio), "aud verdraengt ein tlm");

        writer.start();
        writer.stop();
        lines = linesWrittenTo(file);
    }

    check(lines.size() == 8, "die Warteschlange bleibt bei ihrer Kapazitaet");

    bool sawAsa = false;
    bool sawAud = false;
    uint64_t lastSeq = 0;
    bool ascending = true;
    for (const auto& line : lines) {
        if (contains(line, "\"type\":\"asa\"")) sawAsa = true;
        if (contains(line, "\"type\":\"aud\"")) sawAud = true;

        const size_t pos = line.find("\"seq\":");
        if (pos != std::string::npos) {
            const uint64_t seq = std::stoull(line.substr(pos + 6));
            if (seq < lastSeq) ascending = false;
            lastSeq = seq;
        }
    }
    check(sawAsa, "der asa-Record hat ueberlebt");
    check(sawAud, "der aud-Record hat ueberlebt");
    // Verworfen wird aus der Mitte, nicht umsortiert: die Reihenfolge bleibt,
    // es entstehen nur Luecken. Genau daran erkennt asamon-node den Verlust.
    check(ascending, "seq bleibt aufsteigend, trotz Luecken");

    std::fclose(file);
}

}  // namespace

int main()
{
    testOrderAndSequence();
    testOverflowDropsByPriority();

    if (g_failures == 0) {
        std::cerr << "test_writer: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_writer: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
