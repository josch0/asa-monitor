// SPDX-License-Identifier: GPL-3.0-or-later

#include "recorder.h"

#include "radio-receiver.h"

#include <algorithm>
#include <chrono>
#include <utility>

namespace asamon {

std::string sicherFuerDateinamen(const std::string& in)
{
    std::string out;
    out.reserve(in.size());
    for (const char c : in) {
        const bool erlaubt = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
                             (c >= '0' && c <= '9') || c == '.' || c == '_' ||
                             c == '-';
        out += erlaubt ? c : '_';
    }
    // Ein Name aus lauter Punkten waere "." oder ".." — beides zeigt auf ein
    // Verzeichnis, nicht auf eine Datei.
    if (out.find_first_not_of('.') == std::string::npos) out.clear();
    if (out.size() > 64) out.resize(64);
    return out;
}

std::string aufnahmeBasisName(const std::string& alertUid,
                              const std::string& channel, std::uint8_t subChId,
                              const std::string& startedRfc3339)
{
    std::string kennung = sicherFuerDateinamen(alertUid);
    if (kennung.empty()) {
        // Ohne alert_uid tritt der Startzeitpunkt an ihre Stelle:
        // "2026-08-30T12:14:55.123Z" wird zu "20260830T121455Z".
        for (std::size_t i = 0; i < startedRfc3339.size() && i < 19; ++i) {
            const char c = startedRfc3339[i];
            if (c == '-' || c == ':') continue;
            kennung += c;
        }
        kennung += 'Z';
    }
    return kennung + "-" + sicherFuerDateinamen(channel) + "-" +
           std::to_string(static_cast<unsigned>(subChId));
}

AudioSink::AudioSink(const Options& options, std::uint8_t subChId,
                     std::string alertUid, std::string channel,
                     Clock::time_point started)
    : options_(options), subChId_(subChId), alertUid_(std::move(alertUid)),
      channel_(std::move(channel)), started_(started),
      startedTs_(formatRfc3339Nanos(started))
{
    basisName_ = aufnahmeBasisName(alertUid_, channel_, subChId_, startedTs_);
}

bool AudioSink::oeffne(std::string& fehler)
{
    return roh_.oeffne(options_.audioOut, basisName_ + ".dabp", fehler);
}

void AudioSink::onMscData(const uint8_t* data, std::size_t len)
{
    if (data == nullptr || len == 0) return;
    roh_.schreibe(data, len);
}

void AudioSink::onNewAudio(std::vector<int16_t>&& audioData, int sampleRate,
                           const std::string& mode)
{
    if (audioData.empty() || sampleRate <= 0) return;

    // welle.io hebt Mono-Programme auf zwei gleiche Kanaele an
    // (decoder_adapter.cpp, "upmix to stereo"), und die Kanalzahl steht nicht
    // im Rueckruf. Zwei ist deshalb keine Annahme, sondern die Zusicherung
    // von PutAudio().
    constexpr int kKanaele = 2;

    if (!mp3Versucht_) {
        mp3Versucht_ = true;
        sampleRate_ = sampleRate;
        channels_ = kKanaele;
        mode_ = mode;

        if (options_.mp3Bitrate > 0) {
            std::string fehler;
            if (!mp3_.starte(options_.audioOut, basisName_ + ".mp3", sampleRate,
                             kKanaele, options_.mp3Bitrate, fehler)) {
                // Kein Abbruch: Die .dabp ist der Beleg, die MP3 die
                // Bequemlichkeit. Der Grund geht in den Abschlussrecord und
                // ins Log, damit der Ausfall nicht stillschweigend bleibt.
                fehler_ = "MP3: " + fehler;
                logMessage(options_.logLevel, LogLevel::Warn,
                           "REC: kein MP3 fuer Subchannel " +
                               std::to_string(subChId_) + " — " + fehler);
            }
        }
    }

    mp3_.schreibe(audioData.data(), audioData.size() / kKanaele);
}

void AudioSink::onFrameErrors(int frameErrors)
{
    // Delta je Superframe: welle.io setzt seinen Zaehler nach dem Rueckruf
    // zurueck (decoder_adapter.cpp:81).
    if (frameErrors > 0) frameErrors_ += static_cast<std::uint64_t>(frameErrors);
}

void AudioSink::onRsErrors(bool uncorrectedErrors, int numCorrectedErrors)
{
    if (uncorrectedErrors) ++rsErrors_;
    if (numCorrectedErrors > 0) {
        rsCorrected_ += static_cast<std::uint64_t>(numCorrectedErrors);
    }
}

void AudioSink::onAacErrors(int aacErrors)
{
    if (aacErrors > 0) aacErrors_ += static_cast<std::uint64_t>(aacErrors);
}

AudPayload AudioSink::abschluss(bool truncated, Clock::time_point ende)
{
    AudPayload p;
    p.subChId   = subChId_;
    p.alertUid  = alertUid_;
    p.dir       = options_.audioOut;
    p.startedTs = startedTs_;
    p.seconds   = std::chrono::duration<double>(ende - started_).count();
    p.truncated = truncated;

    p.hasAudio   = (sampleRate_ > 0);
    p.sampleRate = sampleRate_;
    p.channels   = channels_;
    p.mode       = mode_;

    p.frameErrors = frameErrors_;
    p.rsErrors    = rsErrors_;
    p.rsCorrected = rsCorrected_;
    p.aacErrors   = aacErrors_;

    if (roh_.offen()) {
        std::string fehler;
        if (roh_.schliesseUndBenenneUm(fehler)) {
            AudFile f;
            f.name   = roh_.name();
            f.codec  = "dabp";
            f.bytes  = roh_.bytes();
            f.sha256 = roh_.sha256();
            p.files.push_back(std::move(f));
        }
        else {
            if (!fehler_.empty()) fehler_ += "; ";
            fehler_ += fehler;
            logMessage(options_.logLevel, LogLevel::Error, "REC: " + fehler);
        }
    }

    if (mp3_.laeuft()) {
        std::string fehler;
        if (mp3_.schliesse(fehler)) {
            AudFile f;
            f.name   = mp3_.datei().name();
            f.codec  = "mp3";
            f.bytes  = mp3_.datei().bytes();
            f.sha256 = mp3_.datei().sha256();
            p.files.push_back(std::move(f));
            p.mp3Bitrate = mp3_.bitrate();
        }
        else {
            if (!fehler_.empty()) fehler_ += "; ";
            fehler_ += "MP3: " + fehler;
            logMessage(options_.logLevel, LogLevel::Error, "REC: MP3 " + fehler);
        }
    }

    p.error = fehler_;
    return p;
}

Recorder::Recorder(Writer& writer, const Options& options, RadioReceiver& receiver)
    : writer_(writer), options_(options), receiver_(receiver)
{
}

Recorder::~Recorder()
{
    stopAll();
}

bool Recorder::start(std::uint8_t subChId, const std::string& alertUid)
{
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (recordings_.count(subChId) != 0) {
            logMessage(options_.logLevel, LogLevel::Warn,
                       "REC: Subchannel " + std::to_string(subChId) +
                           " laeuft bereits");
            return false;
        }
    }

    // SubChId gegen die Komponenten aller Services aufloesen. Diese Abfragen
    // nehmen den FIBProcessor-Mutex — deshalb gehoeren sie auf diesen Thread
    // und niemals in einen Rueckruf.
    bool found = false;
    uint32_t sid = 0;
    for (const auto& service : receiver_.getServiceList()) {
        for (const auto& component : receiver_.getComponents(service)) {
            if (component.subchannelId == static_cast<int16_t>(subChId)) {
                sid = service.serviceId;
                found = true;
                break;
            }
        }
        if (found) break;
    }
    if (!found) {
        logMessage(options_.logLevel, LogLevel::Warn,
                   "REC: kein Service mit Subchannel " + std::to_string(subChId) +
                       " in diesem Ensemble");
        return false;
    }

    auto recording = std::unique_ptr<Recording>(new Recording(
        options_, sid, subChId, alertUid, options_.channel, Clock::now()));
    recording->service = receiver_.getService(sid);

    // Erst das Ziel, dann der Empfang: Eine Aufnahme ohne beschreibbare Datei
    // waere eine, von der niemand etwas hat.
    std::string fehler;
    if (!recording->sink.oeffne(fehler)) {
        logMessage(options_.logLevel, LogLevel::Error, "REC: " + fehler);
        return false;
    }

    // Der leere Dateiname schaltet den Dump in welle.io ab: die Nutzdaten
    // kommen ueber onMscData() herein, eine Datei legt asamon-rx selbst an.
    if (!receiver_.addServiceToDecode(recording->sink, "", recording->service)) {
        logMessage(options_.logLevel, LogLevel::Error,
                   "REC: addServiceToDecode fuer Subchannel " +
                       std::to_string(subChId) + " fehlgeschlagen");
        return false;
    }

    {
        std::lock_guard<std::mutex> lock(mutex_);
        recordings_.emplace(subChId, std::move(recording));
    }
    logMessage(options_.logLevel, LogLevel::Info,
               "REC: Subchannel " + std::to_string(subChId) + " zugeschaltet" +
                   (alertUid.empty() ? std::string() : " (" + alertUid + ")"));
    return true;
}

void Recorder::stop(std::uint8_t subChId)
{
    std::unique_ptr<Recording> recording;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        auto it = recordings_.find(subChId);
        if (it == recordings_.end()) {
            logMessage(options_.logLevel, LogLevel::Warn,
                       "STOP: Subchannel " + std::to_string(subChId) +
                           " laeuft nicht");
            return;
        }
        recording = std::move(it->second);
        recordings_.erase(it);
    }

    teardown(*recording, false);
    logMessage(options_.logLevel, LogLevel::Info,
               "STOP: Subchannel " + std::to_string(subChId) + " abgeschaltet");
}

void Recorder::stopAll()
{
    std::map<std::uint8_t, std::unique_ptr<Recording>> all;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        all.swap(recordings_);
    }
    for (auto& entry : all) {
        teardown(*entry.second, false);
    }
}

void Recorder::enforceLimits()
{
    if (options_.recMaxSeconds == 0) return;

    std::vector<std::uint8_t> expired;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        const auto now = Clock::now();
        for (const auto& entry : recordings_) {
            const auto age = std::chrono::duration_cast<std::chrono::seconds>(
                                 now - entry.second->started)
                                 .count();
            if (age >= static_cast<long long>(options_.recMaxSeconds)) {
                expired.push_back(entry.first);
            }
        }
    }
    for (const std::uint8_t subChId : expired) {
        logMessage(options_.logLevel, LogLevel::Warn,
                   "REC: Zeitlimit fuer Subchannel " + std::to_string(subChId) +
                       " erreicht, Notbremse greift");

        std::unique_ptr<Recording> recording;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            auto it = recordings_.find(subChId);
            if (it == recordings_.end()) continue;
            recording = std::move(it->second);
            recordings_.erase(it);
        }
        // truncated = true: Der Knoten soll die Aufnahme als abgeschnitten
        // kennzeichnen koennen, statt aus der Dauer zu raten.
        teardown(*recording, true);
    }
}

void Recorder::teardown(Recording& recording, bool truncated)
{
    // Erst abschalten: danach laeuft der Decoder-Thread nicht mehr, und die
    // Dateien gehoeren uns allein (Begruendung in AudioSink::abschluss()).
    receiver_.removeServiceToDecode(recording.service);
    AudPayload payload = recording.sink.abschluss(truncated, Clock::now());

    logMessage(options_.logLevel, LogLevel::Info,
               "REC: Subchannel " + std::to_string(recording.subChId) + " ergab " +
                   std::to_string(payload.files.size()) + " Datei(en)");
    writer_.enqueue(RecordKind::Aud, std::move(payload));
}

}  // namespace asamon
