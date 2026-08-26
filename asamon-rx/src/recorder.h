// SPDX-License-Identifier: GPL-3.0-or-later
//
// Mitschnitt eines Subchannels: REC <subChId> schaltet ihn zu, der rohe
// MSC-Strom laeuft ueber eine FIFO herein und geht als aud-Records hinaus.
//
// Der Kniff und seine Falle (TODO.md Abschnitt 3, Falle 4): dumpFileName in
// addServiceToDecode() schreibt den rohen MSC-Strom, nicht dekodiertes Audio,
// und der Name darf auf eine benannte Pipe zeigen. Nur oeffnet welle.io sie
// mit fopen(..., "wb") — das blockiert, bis ein Leser da ist. Der Leser muss
// also stehen, bevor zugeschaltet wird.

#pragma once

#include "options.h"
#include "record.h"
#include "writer.h"

#include "dab-constants.h"
#include "radio-controller.h"

#include <atomic>
#include <cstdint>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <thread>

class RadioReceiver;

namespace asamon {

// welle.io verlangt fuer addServiceToDecode() einen ProgrammeHandler. Wir
// brauchen ihn nicht: uebertragen wird der rohe Subchannel-Bitstrom aus der
// FIFO, nicht dekodiertes Audio. Der Handler bleibt deshalb leer.
class NullProgrammeHandler : public ProgrammeHandlerInterface {
public:
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
};

// Liest den rohen MSC-Strom aus einer FIFO und stellt ihn als aud-Records
// ein, bis `stopping` gesetzt wird oder alle Schreiber weg sind.
//
// Frei stehend, damit sich dieser Teil ohne Empfaenger und ohne Funk pruefen
// laesst: eine FIFO, ein Schreiber, fertige Records.
void pumpFifoToRecords(Writer& writer, const Options& options,
                       const std::string& fifoPath, uint8_t subChId,
                       const std::atomic<bool>& stopping);

class Recorder {
public:
    Recorder(Writer& writer, const Options& options, RadioReceiver& receiver);
    ~Recorder();

    Recorder(const Recorder&) = delete;
    Recorder& operator=(const Recorder&) = delete;

    // Schaltet den Subchannel zu. Laeuft auf dem Kommando-Thread, nie in
    // einem Rueckruf: die Aufloesung SubChId -> Service nimmt den
    // FIBProcessor-Mutex.
    bool start(uint8_t subChId);

    void stop(uint8_t subChId);
    void stopAll();

    // Notbremse: beendet Aufnahmen, die laenger laufen als --rec-max-seconds.
    // Wird vom Steuerungsthread im Sekundentakt gerufen.
    void enforceLimits();

private:
    struct Recording {
        Recording(uint32_t sid) : service(sid) {}

        Service     service;
        uint8_t     subChId = 0;
        std::string fifoPath;
        std::thread reader;
        std::atomic<bool> stopping{false};
        Clock::time_point started;
        NullProgrammeHandler handler;
    };

    void readLoop(Recording* recording);
    void teardown(std::unique_ptr<Recording> recording);

    Writer& writer_;
    const Options& options_;
    RadioReceiver& receiver_;

    std::mutex mutex_;
    std::map<uint8_t, std::unique_ptr<Recording>> recordings_;
};

}  // namespace asamon
