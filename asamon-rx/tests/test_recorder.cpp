// SPDX-License-Identifier: GPL-3.0-or-later
//
// Der Datenpfad des Mitschnitts, ohne Empfaenger und ohne Funk: rohe MSC-Bytes
// und dekodiertes PCM hinein, fertige Dateien und ein Abschlussrecord heraus.
//
// Der Test hat schon zwei Umbauten ueberlebt und pruefte jedes Mal dasselbe:
// dass ankommt, was hineingegeben wurde. Bis zum 27.08.2026 lag dazwischen
// eine FIFO — der Test musste sie anlegen, einen Schreiber starten und die
// Reihenfolge abpassen. Seit Patch 3 ist es ein Rueckruf. Seit dem 30.08.2026
// liegt am Ende eine Datei statt einer Kette von aud-Records.
//
// Gerufen wird ueber eine Referenz auf ProgrammeHandlerInterface, also genau
// so, wie welle.io es in decoder_adapter.cpp tut. Damit prueft der Test
// denselben Weg wie der Betrieb — und nebenbei, dass die Signaturen wirklich
// ueberschreiben.

#include "mp3encoder.h"
#include "record.h"
#include "recorder.h"
#include "sha256.h"

#include "radio-controller.h"

#include <cstdio>
#include <filesystem>
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

// Ein eigenes Verzeichnis je Lauf, das am Ende wieder verschwindet.
class TempDir {
public:
    TempDir()
    {
        std::error_code ec;
        pfad_ = std::filesystem::temp_directory_path(ec) /
                ("asamon-rx-test-audio-" + std::to_string(std::rand()));
        std::filesystem::create_directories(pfad_, ec);
    }
    ~TempDir()
    {
        std::error_code ec;
        std::filesystem::remove_all(pfad_, ec);
    }
    std::string str() const { return pfad_.string(); }

private:
    std::filesystem::path pfad_;
};

std::vector<std::uint8_t> dateiInhalt(const std::string& pfad)
{
    std::vector<std::uint8_t> out;
    std::FILE* f = std::fopen(pfad.c_str(), "rb");
    if (f == nullptr) return out;
    std::uint8_t puffer[4096];
    std::size_t n;
    while ((n = std::fread(puffer, 1, sizeof(puffer), f)) > 0) {
        out.insert(out.end(), puffer, puffer + n);
    }
    std::fclose(f);
    return out;
}

bool existiert(const std::string& pfad)
{
    std::error_code ec;
    return std::filesystem::exists(pfad, ec);
}

Options optionenFuer(const std::string& verzeichnis)
{
    Options o;
    o.channel  = "5C";
    o.logLevel = LogLevel::Error;   // Warnungen im Test nicht ausgeben
    o.audioOut = verzeichnis;
    o.mp3Bitrate = 64;
    return o;
}

// --- die Namensgebung -------------------------------------------------------

void testNamen()
{
    check(sicherFuerDateinamen("7c2dabcd-1234") == "7c2dabcd-1234",
          "eine gewoehnliche uid bleibt unveraendert");
    check(sicherFuerDateinamen("../../etc/passwd") == ".._.._etc_passwd",
          "Pfadtrenner koennen nicht durchschlagen");
    check(sicherFuerDateinamen("..").empty(),
          "ein Name aus lauter Punkten wird verworfen");
    check(sicherFuerDateinamen(std::string(200, 'x')).size() == 64,
          "uebermaessig lange uids werden gekuerzt");

    check(aufnahmeBasisName("uid1", "5C", 13, "2026-08-30T12:14:55.123Z") ==
              "uid1-5C-13",
          "mit uid gilt das Schema von asamon-node");
    check(aufnahmeBasisName("", "5C", 13, "2026-08-30T12:14:55.123Z") ==
              "20260830T121455Z-5C-13",
          "ohne uid tritt der Startzeitpunkt an ihre Stelle (bekommen: " +
              aufnahmeBasisName("", "5C", 13, "2026-08-30T12:14:55.123Z") + ")");
}

// --- der rohe Strom ---------------------------------------------------------

void testRohStrom()
{
    TempDir dir;
    const Options options = optionenFuer(dir.str());

    AudioSink sink(options, 13, "uid1", "5C", Clock::now());
    std::string fehler;
    check(sink.oeffne(fehler), "oeffne() gelingt (" + fehler + ")");

    const std::string teilPfad = dir.str() + "/uid1-5C-13.dabp.part";
    check(existiert(teilPfad), "waehrend der Aufnahme heisst die Datei .part");

    // Ueber die Basisklasse rufen — so macht es welle.io auch.
    ProgrammeHandlerInterface& handler = sink;
    std::vector<std::uint8_t> erwartet;
    for (int rahmen = 0; rahmen < 50; ++rahmen) {
        std::vector<std::uint8_t> block(96);
        for (std::size_t i = 0; i < block.size(); ++i) {
            block[i] = static_cast<std::uint8_t>((rahmen * 7 + i) & 0xff);
        }
        handler.onMscData(block.data(), block.size());
        erwartet.insert(erwartet.end(), block.begin(), block.end());
    }

    // Fehlerzaehler, wie welle.io sie liefert: Deltas je Superframe.
    handler.onFrameErrors(2);
    handler.onFrameErrors(1);
    handler.onRsErrors(true, 5);
    handler.onRsErrors(false, 3);
    handler.onAacErrors(4);

    const AudPayload p = sink.abschluss(false, Clock::now());

    const std::string zielPfad = dir.str() + "/uid1-5C-13.dabp";
    check(!existiert(teilPfad), "nach dem Abschluss gibt es keine .part mehr");
    check(existiert(zielPfad), "die fertige Datei traegt den endgueltigen Namen");

    const std::vector<std::uint8_t> inhalt = dateiInhalt(zielPfad);
    check(inhalt == erwartet, "jedes Byte steht unveraendert in der Datei");

    check(p.subChId == 13, "Record nennt den Subchannel");
    check(p.alertUid == "uid1", "Record nennt die alert_uid");
    check(p.dir == dir.str(), "Record nennt den Ablageordner");
    check(!p.truncated, "ohne Notbremse ist truncated falsch");
    check(p.frameErrors == 3, "Rahmenfehler werden aufsummiert");
    check(p.rsErrors == 1, "nur unkorrigierbare RS-Faelle zaehlen als Fehler");
    check(p.rsCorrected == 8, "korrigierte RS-Fehler werden aufsummiert");
    check(p.aacErrors == 4, "AAC-Fehler werden aufsummiert");

    bool rohGefunden = false;
    for (const AudFile& f : p.files) {
        if (f.codec != "dabp") continue;
        rohGefunden = true;
        check(f.name == "uid1-5C-13.dabp", "Dateiname im Record");
        check(f.bytes == erwartet.size(),
              "Groesse im Record stimmt mit der Datei ueberein");
        check(f.sha256 == sha256Hex(erwartet.data(), erwartet.size()),
              "SHA-256 im Record stimmt mit dem Inhalt ueberein");
    }
    check(rohGefunden, "der Record nennt die .dabp-Datei");
}

// --- die MP3 ----------------------------------------------------------------

void testMp3()
{
    TempDir dir;
    const Options options = optionenFuer(dir.str());

    AudioSink sink(options, 13, "uid2", "5C", Clock::now());
    std::string fehler;
    check(sink.oeffne(fehler), "oeffne() gelingt (" + fehler + ")");

    ProgrammeHandlerInterface& handler = sink;
    // welle.io liefert stets zwei Kanaele, auch bei Mono-Programmen
    // (decoder_adapter.cpp, "upmix to stereo"). 20 Bloecke zu 1152 Rahmen
    // sind knapp eine halbe Sekunde bei 48 kHz.
    for (int block = 0; block < 20; ++block) {
        std::vector<std::int16_t> pcm(1152 * 2);
        for (std::size_t i = 0; i < pcm.size() / 2; ++i) {
            const auto wert = static_cast<std::int16_t>(
                8000 * ((block * 1152 + i) % 100 < 50 ? 1 : -1));
            pcm[i * 2]     = wert;
            pcm[i * 2 + 1] = wert;
        }
        handler.onNewAudio(std::move(pcm), 48000, "HE-AACv2");
    }

    const AudPayload p = sink.abschluss(true, Clock::now());
    check(p.truncated, "die Notbremse steht im Record");
    check(p.hasAudio, "Audioparameter sind bekannt, sobald PCM kam");
    check(p.sampleRate == 48000, "Abtastrate im Record");
    check(p.channels == 2, "Kanalzahl im Record");
    check(p.mode == "HE-AACv2", "Formatzusammenfassung im Record");

    if (!Mp3Encoder::verfuegbar()) {
        check(p.files.size() == 1,
              "ohne LAME entsteht nur die .dabp — und der Grund steht im Record");
        check(!p.error.empty(), "der Ausfall bleibt sichtbar");
        std::cerr << "  (ohne LAME gebaut, MP3-Pruefungen uebersprungen)\n";
        return;
    }

    bool mp3Gefunden = false;
    for (const AudFile& f : p.files) {
        if (f.codec != "mp3") continue;
        mp3Gefunden = true;
        check(f.name == "uid2-5C-13.mp3", "MP3-Dateiname im Record");
        check(f.bytes > 0, "die MP3 ist nicht leer");

        const std::vector<std::uint8_t> inhalt = dateiInhalt(dir.str() + "/" + f.name);
        check(inhalt.size() == f.bytes, "Groesse im Record passt zur Datei");
        check(f.sha256 == sha256Hex(inhalt.data(), inhalt.size()),
              "SHA-256 im Record passt zum Inhalt");
        // Ein MPEG-Audio-Rahmen beginnt mit elf gesetzten Bits.
        check(inhalt.size() > 2 && inhalt[0] == 0xff && (inhalt[1] & 0xe0) == 0xe0,
              "die Datei beginnt mit einem MPEG-Rahmenkopf");
    }
    check(mp3Gefunden, "der Record nennt die MP3-Datei");
    check(p.mp3Bitrate == 64, "die Bitrate steht im Record");
    check(p.error.empty(), "kein Fehler gemeldet (" + p.error + ")");
}

// --- der Record, wie er auf die Leitung geht --------------------------------

void testSerialisierung()
{
    AudPayload p;
    p.subChId  = 13;
    p.alertUid = "uid3";
    p.dir      = "/var/lib/asamon/audio";
    p.startedTs = "2026-08-30T12:14:55.000000000Z";
    p.seconds  = 43.75;
    p.truncated = false;
    p.hasAudio = true;
    p.sampleRate = 48000;
    p.channels = 2;
    p.mode = "HE-AACv2";
    p.mp3Bitrate = 64;
    p.files.push_back({"uid3-5C-13.dabp", "dabp", 262144, std::string(64, 'a')});
    p.files.push_back({"uid3-5C-13.mp3", "mp3", 245760, std::string(64, 'b')});

    Record rec;
    rec.kind = RecordKind::Aud;
    rec.seq  = 812;
    rec.ts   = Clock::now();
    rec.payload = p;

    const std::string line = serialize(rec);
    auto enthaelt = [&line](const std::string& s) {
        return line.find(s) != std::string::npos;
    };

    check(enthaelt("\"type\":\"aud\""), "Typ steht im Record");
    check(enthaelt("\"subch_id\":13"), "Subchannel steht im Record");
    check(enthaelt("\"alert_uid\":\"uid3\""), "alert_uid steht im Record");
    check(enthaelt("\"seconds\":43.75"), "Dauer steht im Record");
    check(enthaelt("\"truncated\":false"), "truncated steht im Record");
    check(enthaelt("\"sample_rate\":48000"), "Abtastrate steht im Record");
    check(enthaelt("\"mp3_bitrate\":64"), "Bitrate steht im Record");
    check(enthaelt("\"codec\":\"dabp\""), "die .dabp ist genannt");
    check(enthaelt("\"codec\":\"mp3\""), "die .mp3 ist genannt");
    check(enthaelt("\"bytes\":262144"), "Groesse steht im Record");
    check(!enthaelt("\"data\""),
          "der Strom traegt keine Audiobytes mehr");
    check(line.back() == '\n', "die Zeile endet mit \\n");
}

}  // namespace

int main()
{
    testNamen();
    testRohStrom();
    testMp3();
    testSerialisierung();

    if (g_failures == 0) {
        std::cerr << "test_recorder: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_recorder: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
