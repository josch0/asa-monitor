// SPDX-License-Identifier: GPL-3.0-or-later
//
// Implementierung von RadioControllerInterface — die Rueckrufe des
// welle.io-Backends.
//
// Alle Methoden hier laufen auf welle.io-Threads: auf dem OFDM-Thread und je
// einem Thread pro zugeschaltetem Subchannel. Daraus folgt die Regel, die den
// ganzen Entwurf traegt (TODO.md Abschnitt 3):
//
//     Rueckrufe kopieren und stellen ein — sonst nichts.
//
// Kein Datei- oder Netzzugriff, keine Sperre ueber Arbeit hinweg. Wer im
// OFDM-Thread blockiert, verliert Samples.
//
// Und die Falle, die dahinter lauert: onAsaAlert() laeuft unter dem Mutex des
// FIBProcessor, denselben, den getServiceList(), getComponents(),
// getSubchannel() und getEnsembleId() nehmen. Aus einem Rueckruf heraus nie in
// den FIBProcessor zurueckrufen — das waere ein sicherer Selbst-Deadlock beim
// ersten Alert.

#pragma once

#include "options.h"
#include "record.h"
#include "writer.h"

#include "radio-controller.h"

#include <atomic>
#include <mutex>
#include <string>

class RadioReceiver;

// Patch 1 des welle.io-Forks ist Voraussetzung, nicht Kuer: ohne ihn gibt es
// weder asa_alert_t noch onAsaAlert(). Das Bausymbol beantwortet zugleich den
// offenen Punkt aus TODO.md Abschnitt 14 — ein alter asamon-rx darf nicht
// stillschweigend zu einem neuen Fork passen und erst beim Alert auffallen.
#if !defined(WELLE_ASA_ALERT_VERSION)
#  error "welle.io-Patch 1 (FIG 0/15) fehlt im Submodul. Siehe docs/welle-patches.md."
#elif WELLE_ASA_ALERT_VERSION != 1
#  error "welle.io-Patch 1 hat eine andere Version von asa_alert_t als dieser asamon-rx erwartet."
#endif

namespace asamon {

// Momentaufnahme der Zaehler fuer einen tlm-Record. Die Sekundenzaehler
// werden beim Abholen zurueckgesetzt.
struct TelemetrySnapshot {
    float    snr = 0.0f;
    bool     sync = false;
    bool     signalPresent = false;
    int      freqCorrFine = 0;
    int      freqCorrCoarse = 0;
    uint64_t fibTotal = 0;
    uint64_t fibCrcErr = 0;
    uint64_t parseErrors = 0;
    bool     hasEid = false;
    uint16_t eid = 0;
    bool     hasEnsTime = false;
    std::string ensTime;
    int      ensOffsetMin = 0;
};

class Controller : public RadioControllerInterface {
public:
    Controller(Writer& writer, const Options& options);

    // --- RadioControllerInterface ------------------------------------------
    void onSNR(float snr) override;
    void onFrequencyCorrectorChange(int fine, int coarse) override;
    void onSyncChange(char isSync) override;
    void onSignalPresence(bool isSignal) override;
    void onServiceDetected(uint32_t sId) override;
    void onNewEnsemble(uint16_t eId) override;
    void onSetEnsembleLabel(DabLabel& label) override;
    void onDateTimeUpdate(const dab_date_time_t& dateTime) override;
    void onFIBDecodeSuccess(bool crcCheckOk, const uint8_t* fib) override;
    void onNewImpulseResponse(std::vector<float>&& data) override;
    void onConstellationPoints(std::vector<DSPCOMPLEX>&& data) override;
    void onNewNullSymbol(std::vector<DSPCOMPLEX>&& data) override;
    void onTIIMeasurement(tii_measurement_t&& m) override;
    void onMessage(message_level_t level, const std::string& text,
                   const std::string& text2 = std::string()) override;
    void onInputFailure(void) override;
    void onAsaAlert(const asa_alert_t& alert) override;

    // --- fuer den Steuerungsthread -----------------------------------------
    TelemetrySnapshot takeTelemetrySnapshot();

    // true (und zuruecksetzend), wenn sich seit dem letzten Aufruf etwas an
    // Ensemble, Services oder Labels geaendert hat.
    bool takeEnsembleDirty();

    bool inputFailed() const { return inputFailed_.load(std::memory_order_relaxed); }

private:
    Writer& writer_;
    const Options& options_;

    std::atomic<float>    snr_{0.0f};
    std::atomic<bool>     sync_{false};
    std::atomic<bool>     signalPresent_{false};
    std::atomic<int>      freqCorrFine_{0};
    std::atomic<int>      freqCorrCoarse_{0};
    std::atomic<uint64_t> fibTotal_{0};
    std::atomic<uint64_t> fibCrcErr_{0};
    std::atomic<uint64_t> parseErrors_{0};
    std::atomic<bool>     hasEid_{false};
    std::atomic<uint16_t> eid_{0};
    // Erst wenn wirklich etwas hereinkommt — sonst ginge beim Start ein
    // leerer ens-Record raus, noch bevor ein Ensemble empfangen wurde.
    std::atomic<bool>     ensembleDirty_{false};
    std::atomic<bool>     inputFailed_{false};

    mutable std::mutex ensTimeMutex_;
    bool        hasEnsTime_ = false;
    std::string ensTime_;
    int         ensOffsetMin_ = 0;
};

// Uebersetzt einen geparsten Alert in die Nutzlast des asa-Records. Frei
// stehend, damit die Tests denselben Weg gehen wie der Rueckruf: von
// FIBProcessor::processFIB() bis zur fertigen JSON-Zeile, ohne Funk.
// `reportable` wird gesetzt, wenn der Fall in parse_errors gehoert.
AsaPayload asaPayloadFrom(const asa_alert_t& alert, bool& reportable);

// Baut den ens-Record aus dem Empfaenger. Laeuft ausschliesslich auf dem
// Steuerungsthread: die Abfragen nehmen den FIBProcessor-Mutex, aus einem
// Rueckruf heraus waere das ein Deadlock.
EnsPayload buildEnsPayload(RadioReceiver& receiver);

// true, wenn sich der Inhalt so unterscheidet, dass ein neuer ens-Record
// faellig ist. Der Record geht bei Aenderung raus, nicht im Takt.
bool ensPayloadDiffers(const EnsPayload& a, const EnsPayload& b);

}  // namespace asamon
