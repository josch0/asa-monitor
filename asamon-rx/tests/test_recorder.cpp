// SPDX-License-Identifier: GPL-3.0-or-later
//
// Der Datenpfad des Recorders, ohne Empfaenger und ohne Funk: rohe MSC-Bytes
// hinein, fertige aud-Records heraus.
//
// Bis zum 27.08.2026 lief dieser Weg ueber eine benannte Leitung, und der Test
// musste eine FIFO anlegen, einen Schreiber starten und die Reihenfolge
// beider abpassen. Seit Patch 3 des welle.io-Forks ist es ein Rueckruf — der
// Test ruft ihn jetzt einfach auf.
//
// Gerufen wird ueber eine Referenz auf ProgrammeHandlerInterface, also genau
// so, wie welle.io es in decoder_adapter.cpp tut (`myInterface.onMscData(...)`).
// Damit prueft der Test denselben Weg wie der Betrieb — und nebenbei, dass die
// Signatur wirklich ueberschreibt.

#include "record.h"
#include "recorder.h"
#include "tempfile.h"
#include "writer.h"

#include "radio-controller.h"

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

// Der Inhalt des data-Feldes einer Recordzeile, ohne Anfuehrungszeichen.
std::string dataFieldOf(const std::string& line)
{
    const std::string key = "\"data\":\"";
    const auto begin = line.find(key);
    if (begin == std::string::npos) return std::string();
    const auto from = begin + key.size();
    const auto end = line.find('"', from);
    if (end == std::string::npos) return std::string();
    return line.substr(from, end - from);
}

// Ein DAB-Rahmen bei 32 kbit/s: 24 * 32 / 8 Byte. Genau diese Groesse liefert
// welle.io je Aufruf fuer den Subchannel, mit dem "ASA DE" auf 5C geplant ist.
constexpr std::size_t kFrameBytes = 96;

// Eine Aufnahme aufsetzen: Writer auf eine temporaere Datei, Senke daneben.
struct Fixture {
    asamon::test::TempFile temp;
    Writer writer;
    MscSink sink;

    explicit Fixture(const char* name, std::uint8_t subChId = 7)
        : temp(name), writer(temp.get(), 256), sink(writer, subChId)
    {
        writer.start();
    }

    // Der Weg, den welle.io geht: ueber die Basisklasse.
    void feed(const std::vector<std::uint8_t>& bytes)
    {
        ProgrammeHandlerInterface& handler = sink;
        handler.onMscData(bytes.data(), bytes.size());
    }

    // Was Recorder::teardown() im Betrieb tut: erst den Rest einreihen, dann
    // den Ausgabethread beenden. Ohne das flush() bliebe ein angefangenes
    // Stueck im Puffer liegen.
    std::vector<std::string> finish()
    {
        sink.flush();
        writer.stop();
        return linesWrittenTo(temp.get());
    }
};

// Ein Rahmen voller wiedererkennbarer Bytes.
std::vector<std::uint8_t> frame(std::uint8_t fill)
{
    return std::vector<std::uint8_t>(kFrameBytes, fill);
}

// Unter der Stueckgroesse entsteht noch kein Record: der Rueckruf kommt je
// Rahmen herein, ein Record je Rahmen waere ein Vielfaches an Aufwand im
// Strom. Gesammelt wird bis kChunkBytes.
void testShortDataIsBuffered()
{
    Fixture f("test_recorder-1");
    if (f.temp.get() == nullptr) { check(false, "temporaere Datei angelegt"); return; }

    for (int i = 0; i < 10; ++i) f.feed(frame(0xA5));  // 960 Byte
    check(linesWrittenTo(f.temp.get()).empty(), "unter 4096 Byte noch kein Record");

    const auto lines = f.finish();
    check(lines.size() == 1, "flush() gibt den Rest heraus");
}

// Ab kChunkBytes wird eingereiht, und zwar in Stuecken genau dieser Groesse.
// 43 Rahmen sind 4128 Byte: ein voller Record, 32 Byte bleiben liegen.
void testChunkBoundary()
{
    Fixture f("test_recorder-2");
    if (f.temp.get() == nullptr) { check(false, "temporaere Datei angelegt"); return; }

    for (int i = 0; i < 43; ++i) f.feed(frame(0x5A));

    // Der Writer laeuft nebenher; erst nach stop() steht die Datei fest.
    const auto lines = f.finish();
    check(lines.size() == 2, "ein volles Stueck und der Rest aus flush()");
    if (lines.size() == 2) {
        check(contains(lines[0], "\"type\":\"aud\""), "aud-Record erzeugt");
        check(contains(lines[0], "\"subch_id\":7"), "SubChId uebernommen");
        check(contains(lines[0], "\"chunk\":0"), "Stuecknummer beginnt bei 0");
        check(contains(lines[1], "\"chunk\":1"), "Stuecknummer zaehlt hoch");

        // Base64 von 4096 Byte sind 5464 Zeichen, von 32 Byte 44.
        check(dataFieldOf(lines[0]).size() == 5464, "erstes Stueck ist 4096 Byte");
        check(dataFieldOf(lines[1]).size() == 44, "zweites Stueck ist der Rest");
    }
}

// Die Nutzdaten muessen unveraendert durchgehen — das ist der Kern des
// Mitschnitts. "MSC-Bitstrom" ist derselbe Probetext, den schon der Test
// gegen die FIFO benutzt hat.
void testPayloadUnchanged()
{
    Fixture f("test_recorder-3", 12);
    if (f.temp.get() == nullptr) { check(false, "temporaere Datei angelegt"); return; }

    const std::string text = "MSC-Bitstrom";
    f.feed(std::vector<std::uint8_t>(text.begin(), text.end()));

    const auto lines = f.finish();
    check(lines.size() == 1, "ein Record");
    if (!lines.empty()) {
        check(contains(lines[0], "\"subch_id\":12"), "SubChId uebernommen");
        check(dataFieldOf(lines[0]) == "TVNDLUJpdHN0cm9t", "Nutzdaten unveraendert");
    }
}

// Ohne Daten kein Record: ein flush() auf leerem Puffer muss still bleiben.
// Sonst stuende nach jedem STOP ein leerer aud-Record im Strom.
void testFlushWithoutDataIsSilent()
{
    Fixture f("test_recorder-4");
    if (f.temp.get() == nullptr) { check(false, "temporaere Datei angelegt"); return; }

    const auto lines = f.finish();
    check(lines.empty(), "ohne Daten auch keine Records");
}

// Ein Rueckruf ohne Nutzlast darf nichts anrichten. welle.io ruft so nicht,
// aber die Senke ist oeffentlich und der Fall billig abzusichern.
void testEmptyCallIsHarmless()
{
    Fixture f("test_recorder-5");
    if (f.temp.get() == nullptr) { check(false, "temporaere Datei angelegt"); return; }

    ProgrammeHandlerInterface& handler = f.sink;
    handler.onMscData(nullptr, 0);
    const std::uint8_t byte = 0x00;
    handler.onMscData(&byte, 0);

    const auto lines = f.finish();
    check(lines.empty(), "leerer Rueckruf erzeugt keinen Record");
}

}  // namespace

int main()
{
    testShortDataIsBuffered();
    testChunkBoundary();
    testPayloadUnchanged();
    testFlushWithoutDataIsSilent();
    testEmptyCallIsHarmless();

    if (g_failures == 0) {
        std::cerr << "test_recorder: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_recorder: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
