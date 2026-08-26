// SPDX-License-Identifier: GPL-3.0-or-later

#include "writer.h"

#include <utility>

namespace asamon {

Writer::Writer(std::FILE* out, size_t capacity)
    : out_(out), capacity_(capacity)
{
}

Writer::~Writer()
{
    stop();
}

int Writer::priorityOf(RecordKind kind)
{
    switch (kind) {
        case RecordKind::Init: return 3;
        case RecordKind::Ens:  return 3;
        case RecordKind::Asa:  return 3;
        case RecordKind::Aud:  return 2;
        case RecordKind::Tlm:  return 1;
    }
    return 1;
}

void Writer::start()
{
    if (thread_.joinable()) return;
    thread_ = std::thread(&Writer::run, this);
}

void Writer::stop()
{
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (stopping_) return;
        stopping_ = true;
    }
    cv_.notify_all();
    if (thread_.joinable()) thread_.join();
}

bool Writer::enqueue(RecordKind kind, RecordPayload payload)
{
    const int priority = priorityOf(kind);

    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (stopping_) return false;

        if (queue_.size() >= capacity_) {
            // Gibt es ueberhaupt ein Opfer geringeren Rangs? Die Zaehler je
            // Rang beantworten das ohne Durchlauf; erst wenn eines existiert,
            // wird die Warteschlange durchsucht.
            bool victimExists = false;
            for (int p = 1; p < priority; ++p) {
                if (countByPriority_[p] > 0) { victimExists = true; break; }
            }
            if (!victimExists) {
                // Der neue Record ist selbst der geringstwertige. Auch dann
                // wird die seq verbraucht: die Luecke im Strom ist der Beleg.
                ++nextSeq_;
                dropped_.fetch_add(1, std::memory_order_relaxed);
                return false;
            }
            // Aeltestes Element geringeren Rangs verdraengen — frische Daten
            // sind wertvoller als alte. Die Reihenfolge des Stroms bleibt
            // dabei erhalten, es entsteht nur eine Luecke in seq.
            for (auto it = queue_.begin(); it != queue_.end(); ++it) {
                const int victimPriority = priorityOf(it->kind);
                if (victimPriority < priority) {
                    --countByPriority_[victimPriority];
                    queue_.erase(it);
                    dropped_.fetch_add(1, std::memory_order_relaxed);
                    break;
                }
            }
        }

        Record rec;
        rec.kind = kind;
        rec.seq = nextSeq_++;
        rec.ts = Clock::now();
        rec.payload = std::move(payload);
        queue_.push_back(std::move(rec));
        ++countByPriority_[priority];
    }

    cv_.notify_one();
    return true;
}

void Writer::run()
{
    for (;;) {
        Record rec;
        {
            std::unique_lock<std::mutex> lock(mutex_);
            cv_.wait(lock, [this] { return stopping_ || !queue_.empty(); });
            if (queue_.empty()) {
                if (stopping_) return;
                continue;
            }
            rec = std::move(queue_.front());
            queue_.pop_front();
            --countByPriority_[priorityOf(rec.kind)];
        }

        const std::string line = serialize(rec);
        if (std::fwrite(line.data(), 1, line.size(), out_) != line.size() ||
            std::fflush(out_) != 0) {
            // In aller Regel EPIPE: die Gegenstelle ist weg. Weiterschreiben
            // hat dann keinen Zweck; die Hauptschleife raeumt auf.
            outputFailed_.store(true, std::memory_order_relaxed);
            return;
        }
    }
}

}  // namespace asamon
