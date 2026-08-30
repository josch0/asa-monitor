// SPDX-License-Identifier: GPL-3.0-or-later
//
// Mitschnitt eines Subchannels: REC <subChId> schaltet ihn zu, der rohe
// MSC-Strom kommt ueber onMscData() herein und geht als aud-Records hinaus.
//
// Bis zum 27.08.2026 lief das ueber eine benannte Leitung: addServiceToDecode()
// nimmt einen *Dateinamen*, und wir gaben ihm eine FIFO. Patch 3 des
// welle.io-Forks hat das abgeloest (docs/welle-patches.md). Damit verschwunden
// sind: mkfifo und CreateNamedPipeA, die ueberlappte E/A, die Regel "Leser
// steht, bevor zugeschaltet wird" — und die Option --fifo-dir.
//
// Die Bytes sind dieselben geblieben. onMscData() liefert genau das, was
// welle.io in die Dumpdatei geschrieben haette: den Subchannel-Bitstrom, einen
// Aufruf je DAB-Rahmen, vor der Audiodekodierung.

#pragma once

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

// Ein Record je 4096 Byte. Bei 32 kbit/s — der Bitrate, mit der "ASA DE" auf 5C
// geplant ist — ist das gerade eine Sekunde Warn-Audio.
//
// Der Rueckruf kommt viel kleiner herein: ein DAB-Rahmen sind dort 96 Byte alle
// 24 ms. Ungepuffert waeren das ~42 Records je Sekunde, und der Base64-Aufwand
// je Record faende sich im Strom wieder. Deshalb wird gesammelt.
constexpr std::size_t kChunkBytes = 4096;

// Nimmt den rohen MSC-Strom eines Subchannels entgegen und stellt ihn als
// aud-Records ein.
//
// welle.io verlangt fuer addServiceToDecode() ohnehin einen
// ProgrammeHandlerInterface. Frueher war das hier ein leerer Platzhalter, weil
// die Nutzdaten durch die FIFO kamen; seit Patch 3 ist er der Weg selbst. Die
// uebrigen Rueckrufe bleiben leer: dekodiertes Audio, MOT und Dynamic Label
// interessieren asamon-rx nicht.
//
// Frei stehend und ohne Empfaenger pruefbar (tests/test_recorder.cpp): Bytes
// hinein, fertige Records heraus.
class MscSink : public ProgrammeHandlerInterface {
public:
    MscSink(Writer& writer, std::uint8_t subChId);

    MscSink(const MscSink&) = delete;
    MscSink& operator=(const MscSink&) = delete;

    // Laeuft auf dem Decoder-Thread des Subchannels (DabAudio::ourThread), nie
    // auf unserem. Es gilt die Regel aus controller.h: kopieren, einstellen,
    // zurueckkehren. Writer::enqueue() blockiert nie und verwirft im Ueberlauf.
    void onMscData(const uint8_t* data, std::size_t len) override;

    // Reiht ein angefangenes Stueck vorzeitig ein. Nur aufrufen, wenn der
    // Decoder-Thread nachweislich weg ist — also nach removeServiceToDecode().
    // Warum das reicht: removeSubchannel() loescht den SelectedStream, damit
    // faellt der letzte shared_ptr auf DabAudio, und ~DabAudio joint seinen
    // Thread. Nach der Rueckkehr kann onMscData() nicht mehr gerufen werden.
    void flush();

    // --- der Rest von ProgrammeHandlerInterface, absichtlich leer ----------
    void onFrameErrors(int frameErrors) override { (void)frameErrors; }
    void onNewAudio(std::vector<int16_t>&& audioData, int sampleRate,
                    const std::string& mode) override
    {
        (void)audioData; (void)sampleRate; (void)mode;
    }
    void onRsErrors(bool uncorrectedErrors, int numCorrectedErrors) override
    {
        (void)uncorrectedErrors; (void)numCorrectedErrors;
    }
    void onAacErrors(int aacErrors) override { (void)aacErrors; }
    void onNewDynamicLabel(const std::string& label) override { (void)label; }
    void onMOT(const mot_file_t& motFile) override { (void)motFile; }
    void onPADLengthError(size_t announcedXpadLen, size_t xpadLen) override
    {
        (void)announcedXpadLen; (void)xpadLen;
    }

private:
    // Stellt die ersten `count` Bytes des Puffers als aud-Record ein und
    // entfernt sie daraus.
    void emit(std::size_t count);

    Writer&     writer_;
    std::uint8_t subChId_;
    std::uint64_t chunk_ = 0;
    // Nur vom Decoder-Thread beruehrt, und nach dessen Ende von flush().
    // Deshalb ohne Sperre.
    std::vector<std::uint8_t> buffer_;
};

class Recorder {
public:
    Recorder(Writer& writer, const Options& options, RadioReceiver& receiver);
    ~Recorder();

    Recorder(const Recorder&) = delete;
    Recorder& operator=(const Recorder&) = delete;

    // Schaltet den Subchannel zu. Laeuft auf dem Kommando-Thread, nie in
    // einem Rueckruf: die Aufloesung SubChId -> Service nimmt den
    // FIBProcessor-Mutex.
    bool start(std::uint8_t subChId);

    void stop(std::uint8_t subChId);
    void stopAll();

    // Notbremse: beendet Aufnahmen, die laenger laufen als --rec-max-seconds.
    // Wird vom Steuerungsthread im Sekundentakt gerufen.
    void enforceLimits();

private:
    struct Recording {
        Recording(Writer& writer, uint32_t sid, std::uint8_t subChId)
            : service(sid), subChId(subChId), sink(writer, subChId) {}

        Service      service;
        std::uint8_t subChId;
        // Muss die Aufnahme ueberleben: welle.io haelt eine Referenz darauf,
        // bis removeServiceToDecode() zurueckkehrt.
        MscSink      sink;
        Clock::time_point started;
    };

    // Schaltet ab und reiht den Rest ein. Erst welle.io, dann flush() — die
    // Reihenfolge ist die Zusicherung aus MscSink::flush().
    void teardown(Recording& recording);

    Writer& writer_;
    const Options& options_;
    RadioReceiver& receiver_;

    std::mutex mutex_;
    std::map<std::uint8_t, std::unique_ptr<Recording>> recordings_;
};

}  // namespace asamon
