// SPDX-License-Identifier: GPL-3.0-or-later
//
// Record-Format des Stroms zwischen asamon-rx und asamon-node.
// Verbindliche Beschreibung: docs/record-format.md.
//
// NDJSON, ein JSON-Objekt je Zeile, \n-terminiert, UTF-8. Der Strom ist
// zugleich IPC-Protokoll, Archivformat und Beleg zum Server.

#pragma once

#include <array>
#include <chrono>
#include <cstdint>
#include <string>
#include <variant>
#include <vector>

namespace asamon {

constexpr int kFormatVersion = 1;

// Feste Puffer statt vector — im OFDM-Thread wird nicht allokiert.
//
// Die Groessen sind die physikalischen Grenzen, nicht die normativen: ein FIG
// belegt hoechstens die 30 Byte Nutzlast eines FIB, und was davon nach beiden
// Headern und dem kuerzestmoeglichen Id-Feld uebrigbleibt, sind 27 Byte.
// ETSI TS 104 089 erlaubt nur 25 — ein Sender, der mehr schickt, verletzt die
// Norm. Genau das soll sichtbar werden statt abgeschnitten: der Parser meldet
// den Fall, der Puffer nimmt ihn auf.
constexpr size_t kMaxRawBytes = 30;
constexpr size_t kMaxLocationBytes = 27;

using Clock = std::chrono::system_clock;

// RFC 3339 mit Nanosekunden, UTC. Als String, nicht als Zahl: Nanosekunden
// seit Epoche ueberschreiten die 2^53 eines float64, und jeder JSON-Leser
// ueber einen generischen Typ verloere sonst stillschweigend Praezision.
std::string formatRfc3339Nanos(Clock::time_point tp);

enum class RecordKind { Init, Tlm, Ens, Asa, Aud };

struct InitPayload {
    std::string channel;
    int         freqHz = 0;
    std::string device;
    std::string deviceSerial;   // leer, solange Patch 2 fehlt (TODO.md Abschnitt 10)
    std::string rxVersion;
    std::string rxCommit;
    std::string welleCommit;
};

struct TlmPayload {
    float    snr        = 0.0f;
    bool     sync       = false;
    bool     signalPresent = false;
    int      freqCorrFine   = 0;
    int      freqCorrCoarse = 0;
    uint64_t fibTotal   = 0;    // letzte Sekunde
    uint64_t fibCrcErr  = 0;    // letzte Sekunde
    uint64_t dropped    = 0;    // Verwuerfe der Ausgabe-Warteschlange, kumulativ
    uint64_t parseErrors = 0;   // kumulativ
    bool     hasEid     = false;
    uint16_t eid        = 0;
    bool     hasEnsTime = false;
    std::string ensTime;        // aus FIG 0/10, RFC 3339
    int      ensOffsetMin = 0;
};

struct EnsComponent {
    int      subChId   = 0;
    int      startAddr = 0;
    int      size      = 0;     // Capacity Units
    std::string protection;     // "EEP 2-A", "UEP-3", …
    int      bitrate   = 0;     // kbit/s
};

struct EnsService {
    uint32_t sid = 0;
    std::string label;
    std::vector<EnsComponent> components;
};

struct EnsPayload {
    uint16_t eid = 0;
    uint8_t  ecc = 0;
    std::string label;
    std::vector<EnsService> services;
};

// Eine FIG-0/15-Instanz, ausgepackt und ungedeutet. Alles, was Deutung waere —
// Alert-Sets, Phasenverlaeufe, Location-Geometrie — ist Sache von asamon-node.
struct AsaPayload {
    bool     heartbeat = false;
    bool     cn        = false;
    bool     oe        = false;
    bool     pdSecondHalf = false;

    bool     hasPhase  = false;
    uint8_t  phase     = 0;     // 0 Pre-trigger, 1 Trigger, 2 Sustain, 3 End

    bool     hasSubChId = false;
    uint8_t  subChId    = 0;

    bool     hasOtherEid = false;
    uint16_t otherEid    = 0;

    bool     hasSec    = false;
    uint8_t  sec       = 0;     // 63 ist Sonderwert

    bool     hasStatus = false;
    bool     last      = false;
    uint8_t  stage     = 0;     // 0-7, 7 = Test
    uint8_t  iid       = 0;

    bool     hasNff    = false;
    uint8_t  nff       = 0;

    std::array<uint8_t, kMaxLocationBytes> locationCodes{};
    uint8_t  locationLen = 0;

    std::array<uint8_t, kMaxRawBytes> raw{};
    uint8_t  rawLen = 0;
};

struct AudPayload {
    uint8_t  subChId = 0;
    uint64_t chunk   = 0;
    std::vector<uint8_t> data;  // roher Subchannel-Bitstrom, nicht dekodiertes Audio
};

using RecordPayload =
    std::variant<InitPayload, TlmPayload, EnsPayload, AsaPayload, AudPayload>;

struct Record {
    RecordKind kind = RecordKind::Tlm;
    uint64_t   seq  = 0;
    Clock::time_point ts;
    RecordPayload payload;
};

// Erzeugt genau eine NDJSON-Zeile, einschliesslich abschliessendem \n.
std::string serialize(const Record& rec);

// Hilfsmittel, auch von den Tests genutzt.
std::string toHex(const uint8_t* data, size_t len);
std::string toBase64(const uint8_t* data, size_t len);
std::string jsonEscape(const std::string& in);

// Namen der Aufzaehlungen. Liefern nullptr, wenn der Wert keinen Namen hat —
// dann wird er als *_raw gemeldet und in parse_errors gezaehlt.
const char* phaseName(uint8_t phase);
const char* stageName(uint8_t stage);

}  // namespace asamon
