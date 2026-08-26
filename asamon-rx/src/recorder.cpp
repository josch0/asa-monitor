// SPDX-License-Identifier: GPL-3.0-or-later

#include "recorder.h"

#include "radio-receiver.h"

#include <cerrno>
#include <cstring>
#include <utility>

#include <fcntl.h>
#include <poll.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

namespace asamon {

namespace {

// Ein Lesevorgang je Record. Bei 32 kbit/s — der Bitrate, mit der "ASA DE"
// auf 5C geplant ist — sind 4 kB gerade eine Sekunde Warn-Audio.
constexpr size_t kChunkBytes = 4096;

std::string fifoPathFor(const std::string& dir, uint8_t subChId)
{
    return dir + "/asamon-rx-" + std::to_string(::getpid()) + "-" +
           std::to_string(static_cast<int>(subChId)) + ".fifo";
}

}  // namespace

Recorder::Recorder(Writer& writer, const Options& options, RadioReceiver& receiver)
    : writer_(writer), options_(options), receiver_(receiver)
{
}

Recorder::~Recorder()
{
    stopAll();
}

bool Recorder::start(uint8_t subChId)
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

    auto recording = std::unique_ptr<Recording>(new Recording(sid));
    recording->subChId = subChId;
    recording->fifoPath = fifoPathFor(options_.fifoDir, subChId);
    recording->started = Clock::now();
    recording->service = receiver_.getService(sid);

    ::unlink(recording->fifoPath.c_str());
    if (::mkfifo(recording->fifoPath.c_str(), 0600) != 0) {
        logMessage(options_.logLevel, LogLevel::Error,
                   "REC: mkfifo(" + recording->fifoPath +
                       ") fehlgeschlagen: " + std::strerror(errno));
        return false;
    }

    Recording* raw = recording.get();
    raw->reader = std::thread(&Recorder::readLoop, this, raw);

    // Erst jetzt zuschalten: welle.io oeffnet die FIFO mit fopen(..., "wb"),
    // und das blockiert, bis ein Leser da ist.
    if (!receiver_.addServiceToDecode(raw->handler, raw->fifoPath, raw->service)) {
        logMessage(options_.logLevel, LogLevel::Error,
                   "REC: addServiceToDecode fuer Subchannel " +
                       std::to_string(subChId) + " fehlgeschlagen");
        teardown(std::move(recording));
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

void Recorder::stop(uint8_t subChId)
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

    receiver_.removeServiceToDecode(recording->service);
    teardown(std::move(recording));
    logMessage(options_.logLevel, LogLevel::Info,
               "STOP: Subchannel " + std::to_string(subChId) + " abgeschaltet");
}

void Recorder::stopAll()
{
    std::map<uint8_t, std::unique_ptr<Recording>> all;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        all.swap(recordings_);
    }
    for (auto& entry : all) {
        receiver_.removeServiceToDecode(entry.second->service);
        teardown(std::move(entry.second));
    }
}

void Recorder::enforceLimits()
{
    if (options_.recMaxSeconds == 0) return;

    std::vector<uint8_t> expired;
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
    for (const uint8_t subChId : expired) {
        logMessage(options_.logLevel, LogLevel::Warn,
                   "REC: Zeitlimit fuer Subchannel " + std::to_string(subChId) +
                       " erreicht, Notbremse greift");
        stop(subChId);
    }
}

void Recorder::teardown(std::unique_ptr<Recording> recording)
{
    recording->stopping.store(true, std::memory_order_relaxed);
    if (recording->reader.joinable()) recording->reader.join();
    ::unlink(recording->fifoPath.c_str());
}

void Recorder::readLoop(Recording* recording)
{
    pumpFifoToRecords(writer_, options_, recording->fifoPath, recording->subChId,
                      recording->stopping);
}

void pumpFifoToRecords(Writer& writer, const Options& options,
                       const std::string& fifoPath, uint8_t subChId,
                       const std::atomic<bool>& stopping)
{
    // Nichtblockierend oeffnen: sonst haengt dieser Thread bis zum ersten
    // Schreiber und liesse sich nicht mehr abbrechen. Fuer eine FIFO gelingt
    // O_RDONLY | O_NONBLOCK sofort — und genau das entsperrt drueben das
    // fopen(..., "wb") von welle.io.
    const int fd = ::open(fifoPath.c_str(), O_RDONLY | O_NONBLOCK);
    if (fd < 0) {
        logMessage(options.logLevel, LogLevel::Error,
                   "REC: FIFO nicht lesbar: " + std::string(std::strerror(errno)));
        return;
    }

    // Ein eigener Schreib-Deskriptor, der nie schreibt: solange er offen ist,
    // liefert read() kein EOF, wenn welle.io die FIFO zwischendurch schliesst
    // und neu oeffnet. Beim Abbau geht er mit zu.
    const int keepAliveFd = ::open(fifoPath.c_str(), O_WRONLY | O_NONBLOCK);

    std::vector<uint8_t> buffer(kChunkBytes);
    uint64_t chunk = 0;

    while (!stopping.load(std::memory_order_relaxed)) {
        pollfd pfd{};
        pfd.fd = fd;
        pfd.events = POLLIN;

        const int ready = ::poll(&pfd, 1, 200);
        if (ready < 0) {
            if (errno == EINTR) continue;
            logMessage(options.logLevel, LogLevel::Error,
                       "REC: poll fehlgeschlagen: " + std::string(std::strerror(errno)));
            break;
        }
        if (ready == 0) continue;  // Zeitscheibe abgelaufen, Abbruchflag pruefen

        const ssize_t got = ::read(fd, buffer.data(), buffer.size());
        if (got < 0) {
            if (errno == EAGAIN || errno == EINTR) continue;
            logMessage(options.logLevel, LogLevel::Error,
                       "REC: read fehlgeschlagen: " + std::string(std::strerror(errno)));
            break;
        }
        if (got == 0) break;  // alle Schreiber weg

        AudPayload payload;
        payload.subChId = subChId;
        payload.chunk = chunk++;
        payload.data.assign(buffer.begin(), buffer.begin() + got);
        writer.enqueue(RecordKind::Aud, std::move(payload));
    }

    if (keepAliveFd >= 0) ::close(keepAliveFd);
    ::close(fd);
}

}  // namespace asamon
