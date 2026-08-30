// SPDX-License-Identifier: GPL-3.0-or-later
//
// Mitschnitt eines Subchannels: REC <subChId> [alert_uid] schaltet ihn zu, der
// rohe MSC-Strom kommt ueber onMscData() herein, das dekodierte PCM ueber
// onNewAudio(). Beides geht **auf die Platte**, nicht in den Record-Strom; erst
// nach STOP meldet ein einzelner aud-Record, welche Dateien entstanden sind.
//
// Zwei Umbauten liegen dahinter:
//
//   27.08.2026 — Patch 3 des welle.io-Forks (docs/welle-patches.md) loeste die
//   benannte Leitung ab: addServiceToDecode() nimmt einen *Dateinamen*, und wir
//   gaben ihm frueher eine FIFO. Damit verschwanden mkfifo und
//   CreateNamedPipeA, die ueberlappte E/A und die Option --fifo-dir.
//
//   30.08.2026 — der Weg ueber die Platte. Vorher wanderte der Subchannel-
//   Bitstrom base64-kodiert durch den Record-Strom. Das kostete ein Drittel
//   Uebertragung, machte aud zum groessten Record-Typ und setzte den Mitschnitt
//   der Vorrangregel beim Verwerfen aus: ein verworfener aud-Record ist ein
//   Loch mitten in der Aufnahme. Seitdem schreibt asamon-rx die Dateien selbst
//   und meldet sie mit Groesse und SHA-256, damit asamon-node sie nicht noch
//   einmal lesen muss.
//
// Was dabei **nicht** gewandert ist: die Steuerung. Wann aufgenommen wird,
// entscheidet weiterhin allein asamon-node — asamon-rx kennt kein ASA.

#pragma once

#include "audiofile.h"
#include "mp3encoder.h"
#include "options.h"
#include "record.h"
#include "writer.h"

#include "dab-constants.h"
#include "radio-controller.h"

#include <cstddef>
#include <cstdint>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

class RadioReceiver;

// Patch 3 des welle.io-Forks ist Voraussetzung, genau wie Patch 1 es fuer
// controller.h ist: ohne ihn gibt es onMscData() nicht, und der Mitschnitt
// haette keine Quelle. Das Bausymbol verhindert, dass ein alter asamon-rx
// stillschweigend zu einem neuen Fork passt.
#if !defined(WELLE_MSC_DATA_VERSION)
#  error "welle.io-Patch 3 (onMscData) fehlt im Submodul. Siehe docs/welle-patches.md."
#elif WELLE_MSC_DATA_VERSION != 1
#  error "welle.io-Patch 3 hat eine andere Version von onMscData() als dieser asamon-rx erwartet."
#endif

namespace asamon {

// Macht aus fremdem Text einen Dateinamensbestandteil: alles ausser
// [A-Za-z0-9._-] wird zu '_'. Die alert_uid kommt ueber stdin herein und
// darf niemals in einen Pfad durchschlagen.
std::string sicherFuerDateinamen(const std::string& in);

// Basisname ohne Endung. Mit alert_uid ist es genau das Schema, das
// asamon-node bisher selbst vergeben hat (internal/audio: "<uid>-<kanal>-<id>");
// ohne sie tritt der Startzeitpunkt an die Stelle der uid.
std::string aufnahmeBasisName(const std::string& alertUid,
                              const std::string& channel, std::uint8_t subChId,
                              const std::string& startedRfc3339);

// Nimmt den rohen MSC-Strom und das dekodierte PCM eines Subchannels entgegen
// und schreibt beides in je eine Datei.
//
// welle.io verlangt fuer addServiceToDecode() ohnehin einen
// ProgrammeHandlerInterface; seit Patch 3 ist er der Weg selbst. Alle
// Rueckrufe kommen aus demselben Thread (DabAudio::run() treibt den
// DecoderAdapter, der beide ausloest) — deshalb braucht es hier keine Sperre.
//
// Frei stehend und ohne Empfaenger pruefbar (tests/test_recorder.cpp): Bytes
// hinein, fertige Dateien und ein Abschlussrecord heraus.
class AudioSink : public ProgrammeHandlerInterface {
public:
    AudioSink(const Options& options, std::uint8_t subChId,
              std::string alertUid, std::string channel,
              Clock::time_point started);

    AudioSink(const AudioSink&) = delete;
    AudioSink& operator=(const AudioSink&) = delete;

    // Legt den Ablageordner an und oeffnet die .dabp-Datei. Muss vor
    // addServiceToDecode() gelingen: Ohne Ziel gibt es keine Aufnahme.
    bool oeffne(std::string& fehler);

    // --- Rueckrufe, alle auf dem Decoder-Thread ---------------------------
    void onMscData(const uint8_t* data, std::size_t len) override;

    void onNewAudio(std::vector<int16_t>&& audioData, int sampleRate,
                    const std::string& mode) override;

    void onFrameErrors(int frameErrors) override;
    void onRsErrors(bool uncorrectedErrors, int numCorrectedErrors) override;
    void onAacErrors(int aacErrors) override;

    // --- der Rest, absichtlich leer ---------------------------------------
    void onNewDynamicLabel(const std::string& label) override { (void)label; }
    void onMOT(const mot_file_t& motFile) override { (void)motFile; }
    void onPADLengthError(size_t announcedXpadLen, size_t xpadLen) override
    {
        (void)announcedXpadLen; (void)xpadLen;
    }

    // Schliesst alle Dateien, benennt sie um und baut den Abschlussrecord.
    // Nur aufrufen, wenn der Decoder-Thread nachweislich weg ist — also nach
    // removeServiceToDecode(). Warum das reicht: removeSubchannel() loescht
    // den SelectedStream, damit faellt der letzte shared_ptr auf DabAudio,
    // und ~DabAudio joint seinen Thread.
    AudPayload abschluss(bool truncated, Clock::time_point ende);

private:
    const Options& options_;
    std::uint8_t   subChId_;
    std::string    alertUid_;
    std::string    channel_;
    Clock::time_point started_;
    std::string    startedTs_;
    std::string    basisName_;

    DateiSenke  roh_;
    Mp3Encoder  mp3_;
    bool        mp3Versucht_ = false;

    std::string mode_;
    int         sampleRate_ = 0;
    int         channels_ = 0;

    std::uint64_t frameErrors_ = 0;
    std::uint64_t rsErrors_ = 0;
    std::uint64_t rsCorrected_ = 0;
    std::uint64_t aacErrors_ = 0;

    std::string fehler_;
};

class Recorder {
public:
    Recorder(Writer& writer, const Options& options, RadioReceiver& receiver);
    ~Recorder();

    Recorder(const Recorder&) = delete;
    Recorder& operator=(const Recorder&) = delete;

    // Schaltet den Subchannel zu. `alertUid` darf leer sein — dann benennt
    // sich die Aufnahme nach ihrem Startzeitpunkt. Laeuft auf dem
    // Kommando-Thread, nie in einem Rueckruf: die Aufloesung SubChId ->
    // Service nimmt den FIBProcessor-Mutex.
    bool start(std::uint8_t subChId, const std::string& alertUid);

    void stop(std::uint8_t subChId);
    void stopAll();

    // Notbremse: beendet Aufnahmen, die laenger laufen als --rec-max-seconds.
    // Wird vom Steuerungsthread im Sekundentakt gerufen.
    void enforceLimits();

private:
    struct Recording {
        Recording(const Options& options, uint32_t sid, std::uint8_t subChId,
                  const std::string& alertUid, const std::string& channel,
                  Clock::time_point started)
            : service(sid), subChId(subChId), started(started),
              sink(options, subChId, alertUid, channel, started) {}

        Service      service;
        std::uint8_t subChId;
        Clock::time_point started;
        // Muss die Aufnahme ueberleben: welle.io haelt eine Referenz darauf,
        // bis removeServiceToDecode() zurueckkehrt.
        AudioSink    sink;
    };

    // Schaltet ab und schliesst die Dateien. Erst welle.io, dann abschluss() —
    // die Reihenfolge ist die Zusicherung aus AudioSink::abschluss().
    void teardown(Recording& recording, bool truncated);

    Writer& writer_;
    const Options& options_;
    RadioReceiver& receiver_;

    std::mutex mutex_;
    std::map<std::uint8_t, std::unique_ptr<Recording>> recordings_;
};

}  // namespace asamon
