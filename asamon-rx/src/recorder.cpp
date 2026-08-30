// SPDX-License-Identifier: GPL-3.0-or-later

#include "recorder.h"

#include "radio-receiver.h"

#include <algorithm>
#include <utility>

namespace asamon {

MscSink::MscSink(Writer& writer, std::uint8_t subChId)
    : writer_(writer), subChId_(subChId)
{
    buffer_.reserve(kChunkBytes * 2);
}

void MscSink::onMscData(const uint8_t* data, std::size_t len)
{
    if (data == nullptr || len == 0) return;

    buffer_.insert(buffer_.end(), data, data + len);
    while (buffer_.size() >= kChunkBytes) {
        emit(kChunkBytes);
    }
}

void MscSink::flush()
{
    if (!buffer_.empty()) emit(buffer_.size());
}

void MscSink::emit(std::size_t count)
{
    AudPayload payload;
    payload.subChId = subChId_;
    payload.chunk = chunk_++;
    payload.data.assign(buffer_.begin(),
                        buffer_.begin() + static_cast<std::ptrdiff_t>(count));
    writer_.enqueue(RecordKind::Aud, std::move(payload));

    buffer_.erase(buffer_.begin(),
                  buffer_.begin() + static_cast<std::ptrdiff_t>(count));
}

Recorder::Recorder(Writer& writer, const Options& options, RadioReceiver& receiver)
    : writer_(writer), options_(options), receiver_(receiver)
{
}

Recorder::~Recorder()
{
    stopAll();
}

bool Recorder::start(std::uint8_t subChId)
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

    auto recording = std::unique_ptr<Recording>(new Recording(writer_, sid, subChId));
    recording->started = Clock::now();
    recording->service = receiver_.getService(sid);

    // Der leere Dateiname schaltet den Dump in welle.io ab: die Nutzdaten
    // kommen ueber onMscData() herein, eine Datei braucht dafuer niemand.
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
               "REC: Subchannel " + std::to_string(subChId) + " zugeschaltet");
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

    teardown(*recording);
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
        teardown(*entry.second);
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
        stop(subChId);
    }
}

void Recorder::teardown(Recording& recording)
{
    // Erst abschalten: danach laeuft der Decoder-Thread nicht mehr, und der
    // Puffer gehoert uns allein (Begruendung in MscSink::flush()).
    receiver_.removeServiceToDecode(recording.service);
    recording.sink.flush();
}

}  // namespace asamon
