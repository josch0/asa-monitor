// SPDX-License-Identifier: GPL-3.0-or-later
//
// Ausgabethread: nimmt Records entgegen, serialisiert sie und schreibt sie
// zeilenweise nach stdout.
//
// Der Grund fuer den eigenen Thread steht in TODO.md Abschnitt 8: Liest die
// Gegenstelle nicht schnell genug, laeuft der Pipe-Puffer voll und write()
// blockiert. Passierte das auf einem welle.io-Thread, gingen Samples verloren.
// Deshalb reihen die Rueckrufe nur ein und kehren sofort zurueck; im Ueberlauf
// wird verworfen, nicht blockiert — und der Verwurf gezaehlt und im naechsten
// tlm gemeldet. Eine Luecke im Strom muss sichtbar sein, nicht stillschweigend.

#pragma once

#include "record.h"

#include <atomic>
#include <condition_variable>
#include <cstdio>
#include <deque>
#include <mutex>
#include <thread>

namespace asamon {

class Writer {
public:
    // `out` wird nicht uebernommen und muss den Writer ueberleben.
    Writer(std::FILE* out, size_t capacity);
    ~Writer();

    Writer(const Writer&) = delete;
    Writer& operator=(const Writer&) = delete;

    void start();

    // Reiht die restlichen Records aus und beendet den Thread.
    void stop();

    // Reiht einen Record ein. Vergibt seq und Zeitstempel unter der Sperre,
    // damit die Nummerierung der Reihenfolge im Strom entspricht: eine
    // Luecke in seq ist dann genau ein Verwurf.
    // Kehrt sofort zurueck und blockiert nie. Liefert false, wenn der Record
    // (oder ein aelterer, geringerwertiger) verworfen wurde.
    bool enqueue(RecordKind kind, RecordPayload payload);

    uint64_t dropped() const { return dropped_.load(std::memory_order_relaxed); }

    // true, sobald ein Schreibfehler auftrat — in aller Regel EPIPE, weil die
    // Gegenstelle weg ist. Die Hauptschleife nimmt das als Abbruchgrund.
    bool outputFailed() const { return outputFailed_.load(std::memory_order_relaxed); }

private:
    // Vorrang beim Verwerfen: asa vor aud vor tlm (TODO.md Abschnitt 8).
    // init und ens stehen bei asa: init erklaert die ganze Aufzeichnung,
    // und ens ist der Grund, warum asamon-node FIG 0/1 und 0/2 nicht selbst
    // parsen muss — beide sind unwiederbringlich, wenn sie fehlen.
    static int priorityOf(RecordKind kind);

    void run();

    std::FILE* out_;
    const size_t capacity_;

    mutable std::mutex mutex_;
    std::condition_variable cv_;
    std::deque<Record> queue_;
    size_t countByPriority_[4] = {0, 0, 0, 0};
    uint64_t nextSeq_ = 0;
    bool stopping_ = false;

    std::atomic<uint64_t> dropped_{0};
    std::atomic<bool> outputFailed_{false};
    std::thread thread_;
};

}  // namespace asamon
