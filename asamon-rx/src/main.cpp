// SPDX-License-Identifier: GPL-3.0-or-later
//
// asamon-rx — Empfang: SDR -> FIC -> Bitlayout von FIG 0/15 auspacken.
// Deutet nichts. Alles, was Deutung waere, gehoert auf die andere Seite der
// Pipe, nach asamon-node.
//
// Records gehen nach stdout, Logs nach stderr. Immer, ohne Ausnahme — die
// erste Logzeile auf stdout zerschiesst den Strom.

#include "commands.h"
#include "controller.h"
#include "options.h"
#include "platform.h"
#include "record.h"
#include "recorder.h"
#include "version.h"
#include "writer.h"

#include "channels.h"
#include "input_factory.h"
#include "radio-receiver.h"
#include "raw_file.h"
#include "virtual_input.h"

#include <atomic>
#include <cstdio>
#include <cstring>
#include <memory>
#include <string>
#include <thread>

namespace {

std::atomic<bool> g_shutdownRequested{false};

}  // namespace

int main(int argc, char** argv)
{
    using namespace asamon;

    Options options;
    bool exitRequested = false;
    if (!parseOptions(argc, argv, options, exitRequested)) return 2;
    if (exitRequested) return 0;

    asamon::installShutdownHandler(g_shutdownRequested);

    Writer writer(stdout, options.queueSize);
    writer.start();

    Controller controller(writer, options);

    Channels channels;
    const int frequency = channels.getFrequency(options.channel);
    if (frequency <= 0) {
        logMessage(options.logLevel, LogLevel::Error,
                   "unbekannter Kanal \"" + options.channel + "\"");
        return 2;
    }

    std::unique_ptr<CVirtualInput> input;
    if (options.device == "rawfile") {
        // Replay: dieselbe Kette ohne Funk. Der Umweg ueber die Factory faellt
        // aus, weil der Dateiname gesetzt werden muss.
        auto file = std::unique_ptr<CRAWFile>(new CRAWFile(controller));
        file->setFileName(options.iqFile, options.iqFormat);
        input = std::move(file);
    }
    else {
        input.reset(CInputFactory::GetDevice(controller, options.device));
    }
    if (!input) {
        logMessage(options.logLevel, LogLevel::Error,
                   "Eingabegeraet \"" + options.device + "\" nicht verfuegbar. "
                   "Ist mit -DRTLSDR=ON gebaut worden?");
        return 1;
    }
    // Eine fehlende IQ-Datei meldet CRAWFile nur ueber is_ok(). Ohne diese
    // Probe liefe asamon-rx an, schriebe ein init und endete eine Sekunde
    // spaeter wortlos — der unangenehmste aller Fehlerfaelle.
    if (!input->is_ok()) {
        logMessage(options.logLevel, LogLevel::Error,
                   "Eingabegeraet \"" + options.device + "\" liess sich nicht oeffnen" +
                       (options.device == "rawfile" ? " (--iq-file \"" + options.iqFile +
                                                          "\" lesbar?)"
                                                    : ""));
        return 1;
    }

    if (options.gain == "auto") {
        input->setAgc(true);
    }
    else {
        try {
            const int gainIndex = std::stoi(options.gain);
            input->setAgc(false);
            input->setGain(gainIndex);
        } catch (const std::exception&) {
            logMessage(options.logLevel, LogLevel::Error,
                       "--gain braucht \"auto\" oder einen Verstaerkungsindex");
            return 2;
        }
    }
    input->setFrequency(frequency);

    RadioReceiverOptions receiverOptions;
    RadioReceiver receiver(controller, *input, receiverOptions);
    Recorder recorder(writer, options, receiver);

    // init als erste Zeile des Stroms: sie macht jede Aufzeichnung fuer sich
    // allein erklaerbar. Deshalb braucht kein anderer Record ein Kanalfeld.
    {
        InitPayload payload;
        payload.channel = options.channel;
        payload.freqHz = frequency;
        payload.device = options.device;
        // device_serial bleibt leer, solange Patch 2 fehlt: CRTL_SDR oeffnet
        // schlicht das erste Geraet, das sich oeffnen laesst.
        payload.rxVersion = ASAMON_RX_VERSION;
        payload.rxCommit = ASAMON_RX_COMMIT;
        payload.welleCommit = ASAMON_RX_WELLE_COMMIT;
        writer.enqueue(RecordKind::Init, std::move(payload));
    }

    receiver.restart(false);   // empfangen, nicht scannen

    CommandReader::Handlers handlers;
    handlers.onRec  = [&recorder](uint8_t subChId) { recorder.start(subChId); };
    handlers.onStop = [&recorder](uint8_t subChId) { recorder.stop(subChId); };
    handlers.onQuit = [] {
        g_shutdownRequested.store(true, std::memory_order_relaxed);
    };
    CommandReader commands(options, handlers);
    commands.start();

    logMessage(options.logLevel, LogLevel::Info,
               "asamon-rx " ASAMON_RX_VERSION " laeuft auf Kanal " +
                   options.channel + " (" + std::to_string(frequency) + " Hz)");

    EnsPayload lastEnsemble;
    bool haveEnsemble = false;
    bool inputBroke = false;
    auto nextTick = Clock::now();

    while (!g_shutdownRequested.load(std::memory_order_relaxed)) {
        nextTick += std::chrono::seconds(1);
        std::this_thread::sleep_until(nextTick);

        if (writer.outputFailed()) {
            logMessage(options.logLevel, LogLevel::Error,
                       "Ausgabe abgebrochen (Gegenstelle weg), beende");
            break;
        }
        if (controller.inputFailed()) {
            inputBroke = true;
            break;
        }

        // tlm geht auch dann raus, wenn nichts empfangen wurde — sonst kann
        // der Server "Ensemble schweigt" nicht von "Knoten ist tot"
        // unterscheiden.
        //
        // **Und es ist das Lebenszeichen des Prozesses.** asamon-node erkennt
        // an ausbleibenden Records, dass diese Schleife steht, und startet den
        // Prozess neu. Bis zum 27.08.2026 tat das der systemd-Watchdog, der
        // zwei Zeilen weiter unten aus derselben Schleife getickt wurde; der
        // Weg ueber den Record-Strom leistet dasselbe und funktioniert auch
        // unter Windows, wo es keinen Watchdog gibt (TODO.md Abschnitt 19).
        // Diese Zeile darf deshalb nie an eine Bedingung geraten.
        const TelemetrySnapshot snapshot = controller.takeTelemetrySnapshot();
        TlmPayload tlm;
        tlm.snr = snapshot.snr;
        tlm.sync = snapshot.sync;
        tlm.signalPresent = snapshot.signalPresent;
        tlm.freqCorrFine = snapshot.freqCorrFine;
        tlm.freqCorrCoarse = snapshot.freqCorrCoarse;
        tlm.fibTotal = snapshot.fibTotal;
        tlm.fibCrcErr = snapshot.fibCrcErr;
        tlm.dropped = writer.dropped();
        tlm.parseErrors = snapshot.parseErrors;
        tlm.hasEid = snapshot.hasEid;
        tlm.eid = snapshot.eid;
        tlm.hasEnsTime = snapshot.hasEnsTime;
        tlm.ensTime = snapshot.ensTime;
        tlm.ensOffsetMin = snapshot.ensOffsetMin;
        writer.enqueue(RecordKind::Tlm, std::move(tlm));

        // Die Abfragen fuer den ens-Record nehmen den FIBProcessor-Mutex —
        // deshalb stehen sie hier auf dem Steuerungsthread und nicht in einem
        // Rueckruf.
        if (controller.takeEnsembleDirty()) {
            EnsPayload ensemble = buildEnsPayload(receiver);
            if (!haveEnsemble || ensPayloadDiffers(ensemble, lastEnsemble)) {
                lastEnsemble = ensemble;
                haveEnsemble = true;
                writer.enqueue(RecordKind::Ens, std::move(ensemble));
            }
        }

        recorder.enforceLimits();
    }

    logMessage(options.logLevel, LogLevel::Info, "beende");

    commands.stop();
    recorder.stopAll();
    receiver.stop();
    input->stop();
    writer.stop();

    // Ein weggebrochenes Eingabegeraet ist ein Fehlschlag, kein Feierabend:
    // asamon-node soll den Prozess neu starten, statt ihn als erledigt zu
    // betrachten.
    return inputBroke ? 1 : 0;
}
