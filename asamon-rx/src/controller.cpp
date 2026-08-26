// SPDX-License-Identifier: GPL-3.0-or-later

#include "controller.h"

#include "radio-receiver.h"

#include <cstdio>
#include <cstring>

namespace asamon {

namespace {

std::string protectionToString(const ProtectionSettings& ps)
{
    char buf[16];
    if (ps.shortForm) {
        std::snprintf(buf, sizeof(buf), "UEP-%d", static_cast<int>(ps.uepLevel));
    }
    else {
        std::snprintf(buf, sizeof(buf), "EEP %d-%c", static_cast<int>(ps.eepLevel),
                      ps.eepProfile == EEPProtectionProfile::EEP_A ? 'A' : 'B');
    }
    return buf;
}

}  // namespace

Controller::Controller(Writer& writer, const Options& options)
    : writer_(writer), options_(options)
{
}

void Controller::onSNR(float snr)
{
    snr_.store(snr, std::memory_order_relaxed);
}

void Controller::onFrequencyCorrectorChange(int fine, int coarse)
{
    freqCorrFine_.store(fine, std::memory_order_relaxed);
    freqCorrCoarse_.store(coarse, std::memory_order_relaxed);
}

void Controller::onSyncChange(char isSync)
{
    sync_.store(isSync != 0, std::memory_order_relaxed);
}

void Controller::onSignalPresence(bool isSignal)
{
    signalPresent_.store(isSignal, std::memory_order_relaxed);
}

void Controller::onServiceDetected(uint32_t sId)
{
    (void)sId;
    // Nur merken, dass sich etwas geaendert hat. Die Serviceliste abzufragen
    // wuerde hier den FIBProcessor-Mutex nehmen — das macht der
    // Steuerungsthread.
    ensembleDirty_.store(true, std::memory_order_relaxed);
}

void Controller::onNewEnsemble(uint16_t eId)
{
    eid_.store(eId, std::memory_order_relaxed);
    hasEid_.store(true, std::memory_order_relaxed);
    ensembleDirty_.store(true, std::memory_order_relaxed);
}

void Controller::onSetEnsembleLabel(DabLabel& label)
{
    (void)label;
    ensembleDirty_.store(true, std::memory_order_relaxed);
}

void Controller::onDateTimeUpdate(const dab_date_time_t& dateTime)
{
    // Die Differenz zwischen ts und ens_time ist selbst eine Messgroesse:
    // alle ASA-Alerts sollen an der Minutengrenze beginnen.
    char buf[40];
    std::snprintf(buf, sizeof(buf), "%04d-%02d-%02dT%02d:%02d:%02dZ",
                  dateTime.year, dateTime.month, dateTime.day,
                  dateTime.hour, dateTime.minutes, dateTime.seconds);

    std::lock_guard<std::mutex> lock(ensTimeMutex_);
    hasEnsTime_ = true;
    ensTime_ = buf;
    ensOffsetMin_ = dateTime.hourOffset * 60 + dateTime.minuteOffset;
}

void Controller::onFIBDecodeSuccess(bool crcCheckOk, const uint8_t* fib)
{
    // Der Puffer wird bewusst nicht angefasst. onFIBDecodeSuccess() liefert
    // ein Bit je Byte (256 Byte fuer 256 Bit) — wer hier anfaengt zu packen,
    // loest ein Problem, das wir nicht haben: ueber die Pipe gehen
    // ausschliesslich geparste asa-Records, nie rohe FIBs.
    //
    // Gebraucht wird aus diesem Rueckruf nur die CRC-Quote. Sie ist die
    // Groesse, mit der sich "Ensemble sendet keinen Heartbeat" von "wir
    // empfangen schlecht" trennen laesst — und darauf beruht die
    // Abdeckungskarte, das Kernergebnis des Projekts.
    (void)fib;
    fibTotal_.fetch_add(1, std::memory_order_relaxed);
    if (!crcCheckOk) {
        fibCrcErr_.fetch_add(1, std::memory_order_relaxed);
    }
}

void Controller::onNewImpulseResponse(std::vector<float>&& data)
{
    (void)data;
}

void Controller::onConstellationPoints(std::vector<DSPCOMPLEX>&& data)
{
    (void)data;
}

void Controller::onNewNullSymbol(std::vector<DSPCOMPLEX>&& data)
{
    (void)data;
}

void Controller::onTIIMeasurement(tii_measurement_t&& m)
{
    (void)m;
}

void Controller::onMessage(message_level_t level, const std::string& text,
                           const std::string& text2)
{
    const std::string full = text2.empty() ? text : text + " " + text2;
    logMessage(options_.logLevel,
               level == message_level_t::Error ? LogLevel::Error : LogLevel::Info,
               "welle.io: " + full);
}

void Controller::onInputFailure(void)
{
    logMessage(options_.logLevel, LogLevel::Error, "Eingabegeraet ausgefallen");
    inputFailed_.store(true, std::memory_order_relaxed);
}

AsaPayload asaPayloadFrom(const asa_alert_t& alert, bool& reportable)
{
    AsaPayload payload;
    payload.heartbeat    = alert.heartbeat;
    payload.cn           = alert.cn;
    payload.oe           = alert.oe;
    payload.pdSecondHalf = alert.secondHalfMinute;

    if (!alert.heartbeat) {
        if (alert.oe) {
            payload.hasOtherEid = true;
            payload.otherEid = alert.otherEId;
        }
        else {
            payload.hasPhase = true;
            payload.phase = alert.phase;
            payload.hasSubChId = true;
            payload.subChId = alert.subChId;
        }
        if (alert.hasSec) {
            payload.hasSec = true;
            payload.sec = alert.sec;
        }
        if (alert.hasStatus) {
            payload.hasStatus = true;
            payload.last  = alert.last;
            payload.stage = alert.stage;
            payload.iid   = alert.iid;
        }
        if (alert.hasNff) {
            payload.hasNff = true;
            payload.nff = alert.nff;
        }
    }

    const size_t locationLen =
        alert.locationCodes.size() < kMaxLocationBytes ? alert.locationCodes.size()
                                                       : kMaxLocationBytes;
    if (locationLen > 0) {
        std::memcpy(payload.locationCodes.data(), alert.locationCodes.data(), locationLen);
    }
    payload.locationLen = static_cast<uint8_t>(locationLen);

    const size_t rawLen =
        alert.raw.size() < kMaxRawBytes ? alert.raw.size() : kMaxRawBytes;
    if (rawLen > 0) {
        std::memcpy(payload.raw.data(), alert.raw.data(), rawLen);
    }
    payload.rawLen = static_cast<uint8_t>(rawLen);

    // Ein unbekannter Aufzaehlungswert ist eine meldenswerte Beobachtung, kein
    // Fehler — der Record geht trotzdem raus, nur eben mit *_raw statt Namen.
    // Gezaehlt wird er, damit er im tlm auffaellt.
    const bool unknownEnum =
        (payload.hasPhase && phaseName(payload.phase) == nullptr) ||
        (payload.hasStatus && stageName(payload.stage) == nullptr);
    reportable = unknownEnum || alert.parseError;

    return payload;
}

void Controller::onAsaAlert(const asa_alert_t& alert)
{
    // Laeuft unter dem FIBProcessor-Mutex. Kopieren und einstellen, sonst
    // nichts: keine Abfrage am Empfaenger, keine Datei, kein Warten.
    bool reportable = false;
    AsaPayload payload = asaPayloadFrom(alert, reportable);
    if (reportable) {
        parseErrors_.fetch_add(1, std::memory_order_relaxed);
    }
    writer_.enqueue(RecordKind::Asa, std::move(payload));
}

TelemetrySnapshot Controller::takeTelemetrySnapshot()
{
    TelemetrySnapshot snap;
    snap.snr = snr_.load(std::memory_order_relaxed);
    snap.sync = sync_.load(std::memory_order_relaxed);
    snap.signalPresent = signalPresent_.load(std::memory_order_relaxed);
    snap.freqCorrFine = freqCorrFine_.load(std::memory_order_relaxed);
    snap.freqCorrCoarse = freqCorrCoarse_.load(std::memory_order_relaxed);
    // fib_total und fib_crc_err zaehlen die letzte Sekunde, also holen und
    // zuruecksetzen. parse_errors ist kumulativ.
    snap.fibTotal = fibTotal_.exchange(0, std::memory_order_relaxed);
    snap.fibCrcErr = fibCrcErr_.exchange(0, std::memory_order_relaxed);
    snap.parseErrors = parseErrors_.load(std::memory_order_relaxed);
    snap.hasEid = hasEid_.load(std::memory_order_relaxed);
    snap.eid = eid_.load(std::memory_order_relaxed);
    {
        std::lock_guard<std::mutex> lock(ensTimeMutex_);
        snap.hasEnsTime = hasEnsTime_;
        snap.ensTime = ensTime_;
        snap.ensOffsetMin = ensOffsetMin_;
    }
    return snap;
}

bool Controller::takeEnsembleDirty()
{
    return ensembleDirty_.exchange(false, std::memory_order_relaxed);
}

EnsPayload buildEnsPayload(RadioReceiver& receiver)
{
    EnsPayload payload;
    payload.eid = receiver.getEnsembleId();
    payload.ecc = receiver.getEnsembleEcc();
    payload.label = receiver.getEnsembleLabel().utf8_label();

    for (const auto& service : receiver.getServiceList()) {
        EnsService entry;
        entry.sid = service.serviceId;
        entry.label = service.serviceLabel.utf8_label();

        for (const auto& component : receiver.getComponents(service)) {
            const Subchannel subchannel = receiver.getSubchannel(component);
            if (subchannel.subChId < 0) {
                continue;  // Komponente ohne Subchannel-Eintrag in FIG 0/1
            }
            EnsComponent comp;
            comp.subChId   = subchannel.subChId;
            comp.startAddr = subchannel.startAddr;
            comp.size      = subchannel.length;
            comp.protection = protectionToString(subchannel.protectionSettings);
            comp.bitrate   = subchannel.bitrate();
            entry.components.push_back(std::move(comp));
        }
        payload.services.push_back(std::move(entry));
    }
    return payload;
}

bool ensPayloadDiffers(const EnsPayload& a, const EnsPayload& b)
{
    if (a.eid != b.eid || a.ecc != b.ecc || a.label != b.label) return true;
    if (a.services.size() != b.services.size()) return true;

    for (size_t i = 0; i < a.services.size(); ++i) {
        const auto& sa = a.services[i];
        const auto& sb = b.services[i];
        if (sa.sid != sb.sid || sa.label != sb.label) return true;
        if (sa.components.size() != sb.components.size()) return true;
        for (size_t j = 0; j < sa.components.size(); ++j) {
            const auto& ca = sa.components[j];
            const auto& cb = sb.components[j];
            if (ca.subChId != cb.subChId || ca.startAddr != cb.startAddr ||
                ca.size != cb.size || ca.protection != cb.protection ||
                ca.bitrate != cb.bitrate) {
                return true;
            }
        }
    }
    return false;
}

}  // namespace asamon
