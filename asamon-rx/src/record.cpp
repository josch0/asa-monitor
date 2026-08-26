// SPDX-License-Identifier: GPL-3.0-or-later

#include "record.h"

#include <cmath>
#include <cstdio>
#include <ctime>

namespace asamon {

namespace {

const char kHexDigits[] = "0123456789abcdef";
const char kBase64Alphabet[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

// 0x10FF-Schreibweise fuer 16-bit-Kennungen. Als String, damit die fuehrende
// Null nicht verlorengeht und der Wert als Kennung erkennbar bleibt.
std::string hex16(uint16_t v)
{
    char buf[8];
    std::snprintf(buf, sizeof(buf), "0x%04X", v);
    return buf;
}

std::string hex32(uint32_t v)
{
    char buf[16];
    std::snprintf(buf, sizeof(buf), "0x%08X", v);
    return buf;
}

// JSON kennt weder NaN noch Infinity — beides wird zu null.
void appendFloat(std::string& out, float v)
{
    if (!std::isfinite(v)) {
        out += "null";
        return;
    }
    char buf[32];
    std::snprintf(buf, sizeof(buf), "%.1f", static_cast<double>(v));
    out += buf;
}

void appendUint(std::string& out, uint64_t v)
{
    char buf[24];
    std::snprintf(buf, sizeof(buf), "%llu", static_cast<unsigned long long>(v));
    out += buf;
}

void appendInt(std::string& out, long long v)
{
    char buf[24];
    std::snprintf(buf, sizeof(buf), "%lld", v);
    out += buf;
}

void appendKeyString(std::string& out, const char* key, const std::string& value)
{
    out += ",\"";
    out += key;
    out += "\":\"";
    out += jsonEscape(value);
    out += '"';
}

void appendKeyBool(std::string& out, const char* key, bool value)
{
    out += ",\"";
    out += key;
    out += "\":";
    out += value ? "true" : "false";
}

void appendKeyUint(std::string& out, const char* key, uint64_t value)
{
    out += ",\"";
    out += key;
    out += "\":";
    appendUint(out, value);
}

void appendKeyInt(std::string& out, const char* key, long long value)
{
    out += ",\"";
    out += key;
    out += "\":";
    appendInt(out, value);
}

const char* kindName(RecordKind kind)
{
    switch (kind) {
        case RecordKind::Init: return "init";
        case RecordKind::Tlm:  return "tlm";
        case RecordKind::Ens:  return "ens";
        case RecordKind::Asa:  return "asa";
        case RecordKind::Aud:  return "aud";
    }
    return "unknown";
}

void serializeInit(std::string& out, const InitPayload& p)
{
    appendKeyInt(out, "format_version", kFormatVersion);
    appendKeyString(out, "channel", p.channel);
    appendKeyInt(out, "freq_hz", p.freqHz);
    appendKeyString(out, "device", p.device);
    appendKeyString(out, "device_serial", p.deviceSerial);
    appendKeyString(out, "rx_version", p.rxVersion);
    appendKeyString(out, "rx_commit", p.rxCommit);
    appendKeyString(out, "welle_commit", p.welleCommit);
}

void serializeTlm(std::string& out, const TlmPayload& p)
{
    out += ",\"snr\":";
    appendFloat(out, p.snr);
    appendKeyBool(out, "sync", p.sync);
    appendKeyBool(out, "signal", p.signalPresent);
    out += ",\"freq_corr\":{\"fine\":";
    appendInt(out, p.freqCorrFine);
    out += ",\"coarse\":";
    appendInt(out, p.freqCorrCoarse);
    out += '}';
    appendKeyUint(out, "fib_total", p.fibTotal);
    appendKeyUint(out, "fib_crc_err", p.fibCrcErr);
    appendKeyUint(out, "dropped", p.dropped);
    appendKeyUint(out, "parse_errors", p.parseErrors);
    if (p.hasEid) {
        appendKeyString(out, "eid", hex16(p.eid));
    }
    if (p.hasEnsTime) {
        appendKeyString(out, "ens_time", p.ensTime);
        appendKeyInt(out, "ens_offset_min", p.ensOffsetMin);
    }
}

void serializeEns(std::string& out, const EnsPayload& p)
{
    appendKeyString(out, "eid", hex16(p.eid));
    appendKeyInt(out, "ecc", p.ecc);
    appendKeyString(out, "label", p.label);
    out += ",\"services\":[";
    bool firstService = true;
    for (const auto& service : p.services) {
        if (!firstService) out += ',';
        firstService = false;
        out += "{\"sid\":\"";
        out += hex32(service.sid);
        out += "\",\"label\":\"";
        out += jsonEscape(service.label);
        out += "\",\"components\":[";
        bool firstComponent = true;
        for (const auto& component : service.components) {
            if (!firstComponent) out += ',';
            firstComponent = false;
            out += "{\"subch_id\":";
            appendInt(out, component.subChId);
            out += ",\"start_addr\":";
            appendInt(out, component.startAddr);
            out += ",\"size\":";
            appendInt(out, component.size);
            out += ",\"protection\":\"";
            out += jsonEscape(component.protection);
            out += "\",\"bitrate\":";
            appendInt(out, component.bitrate);
            out += '}';
        }
        out += "]}";
    }
    out += ']';
}

void serializeAsa(std::string& out, const AsaPayload& p)
{
    appendKeyBool(out, "heartbeat", p.heartbeat);
    appendKeyBool(out, "cn", p.cn);
    appendKeyBool(out, "oe", p.oe);
    appendKeyBool(out, "pd_second_half", p.pdSecondHalf);

    if (p.hasPhase) {
        // Ein unbekannter Aufzaehlungswert ist eine meldenswerte Beobachtung,
        // kein Fehler: er wird als *_raw gemeldet, nie verworfen.
        if (const char* name = phaseName(p.phase)) {
            appendKeyString(out, "phase", name);
        }
        else {
            appendKeyUint(out, "phase_raw", p.phase);
        }
    }
    if (p.hasSubChId)  appendKeyUint(out, "subch_id", p.subChId);
    if (p.hasOtherEid) appendKeyString(out, "other_eid", hex16(p.otherEid));
    if (p.hasSec)      appendKeyUint(out, "sec", p.sec);

    if (p.hasStatus) {
        if (const char* name = stageName(p.stage)) {
            appendKeyString(out, "stage", name);
        }
        else {
            appendKeyUint(out, "stage_raw", p.stage);
        }
        appendKeyUint(out, "iid", p.iid);
        appendKeyBool(out, "last", p.last);
    }
    if (p.hasNff) appendKeyUint(out, "nff", p.nff);

    if (p.locationLen > 0) {
        appendKeyString(out, "location_codes",
                        toHex(p.locationCodes.data(), p.locationLen));
    }
    // raw ist nicht optional: es gibt keine Referenzimplementierung von
    // FIG 0/15, und die einzige vollstaendige, die existiert, liest Id- und
    // Status-Feld vertauscht. 30 Byte je Ereignis sind der Preis dafuer,
    // dass man sich irren darf.
    appendKeyString(out, "raw", toHex(p.raw.data(), p.rawLen));
}

void serializeAud(std::string& out, const AudPayload& p)
{
    appendKeyUint(out, "subch_id", p.subChId);
    appendKeyUint(out, "chunk", p.chunk);
    appendKeyString(out, "data", toBase64(p.data.data(), p.data.size()));
}

}  // namespace

std::string formatRfc3339Nanos(Clock::time_point tp)
{
    using namespace std::chrono;
    const auto sinceEpoch = tp.time_since_epoch();
    const auto secs = duration_cast<seconds>(sinceEpoch);
    auto nanos = duration_cast<nanoseconds>(sinceEpoch - secs).count();

    std::time_t tt = static_cast<std::time_t>(secs.count());
    if (nanos < 0) {          // vor der Epoche: auf positive Nanosekunden normieren
        nanos += 1000000000;
        tt -= 1;
    }

    std::tm tmUtc{};
#if defined(_WIN32)
    gmtime_s(&tmUtc, &tt);
#else
    gmtime_r(&tt, &tmUtc);
#endif

    char buf[48];
    std::snprintf(buf, sizeof(buf), "%04d-%02d-%02dT%02d:%02d:%02d.%09lldZ",
                  tmUtc.tm_year + 1900, tmUtc.tm_mon + 1, tmUtc.tm_mday,
                  tmUtc.tm_hour, tmUtc.tm_min, tmUtc.tm_sec,
                  static_cast<long long>(nanos));
    return buf;
}

std::string toHex(const uint8_t* data, size_t len)
{
    std::string out;
    out.reserve(len * 2);
    for (size_t i = 0; i < len; ++i) {
        out += kHexDigits[data[i] >> 4];
        out += kHexDigits[data[i] & 0x0F];
    }
    return out;
}

std::string toBase64(const uint8_t* data, size_t len)
{
    std::string out;
    out.reserve(((len + 2) / 3) * 4);
    size_t i = 0;
    for (; i + 2 < len; i += 3) {
        const uint32_t triple = (static_cast<uint32_t>(data[i]) << 16) |
                                (static_cast<uint32_t>(data[i + 1]) << 8) |
                                 static_cast<uint32_t>(data[i + 2]);
        out += kBase64Alphabet[(triple >> 18) & 0x3F];
        out += kBase64Alphabet[(triple >> 12) & 0x3F];
        out += kBase64Alphabet[(triple >> 6) & 0x3F];
        out += kBase64Alphabet[triple & 0x3F];
    }
    if (i < len) {
        const size_t rest = len - i;
        uint32_t triple = static_cast<uint32_t>(data[i]) << 16;
        if (rest == 2) triple |= static_cast<uint32_t>(data[i + 1]) << 8;
        out += kBase64Alphabet[(triple >> 18) & 0x3F];
        out += kBase64Alphabet[(triple >> 12) & 0x3F];
        out += (rest == 2) ? kBase64Alphabet[(triple >> 6) & 0x3F] : '=';
        out += '=';
    }
    return out;
}

std::string jsonEscape(const std::string& in)
{
    std::string out;
    out.reserve(in.size() + 8);
    for (const char ch : in) {
        const unsigned char c = static_cast<unsigned char>(ch);
        switch (c) {
            case '"':  out += "\\\"";  break;
            case '\\': out += "\\\\";  break;
            case '\b': out += "\\b";   break;
            case '\f': out += "\\f";   break;
            case '\n': out += "\\n";   break;
            case '\r': out += "\\r";   break;
            case '\t': out += "\\t";   break;
            default:
                if (c < 0x20) {
                    char buf[8];
                    std::snprintf(buf, sizeof(buf), "\\u%04x", c);
                    out += buf;
                }
                else {
                    out += ch;  // UTF-8 wird unveraendert durchgereicht
                }
                break;
        }
    }
    return out;
}

const char* phaseName(uint8_t phase)
{
    switch (phase) {
        case 0: return "pre_trigger";
        case 1: return "trigger";
        case 2: return "sustain";
        case 3: return "end";
        default: return nullptr;
    }
}

const char* stageName(uint8_t stage)
{
    switch (stage) {
        case 0: return "level1_start";
        case 1: return "level1_update";
        case 2: return "level1_repeat";
        case 3: return "level1_critical";
        case 4: return "level2_start";
        case 5: return "level2_update";
        case 6: return "level2_repeat";
        case 7: return "test";
        default: return nullptr;
    }
}

std::string serialize(const Record& rec)
{
    std::string out;
    out.reserve(256);
    out += "{\"type\":\"";
    out += kindName(rec.kind);
    out += "\",\"seq\":";
    appendUint(out, rec.seq);
    out += ",\"ts\":\"";
    out += formatRfc3339Nanos(rec.ts);
    out += '"';

    switch (rec.kind) {
        case RecordKind::Init: serializeInit(out, std::get<InitPayload>(rec.payload)); break;
        case RecordKind::Tlm:  serializeTlm(out, std::get<TlmPayload>(rec.payload));   break;
        case RecordKind::Ens:  serializeEns(out, std::get<EnsPayload>(rec.payload));   break;
        case RecordKind::Asa:  serializeAsa(out, std::get<AsaPayload>(rec.payload));   break;
        case RecordKind::Aud:  serializeAud(out, std::get<AudPayload>(rec.payload));   break;
    }

    out += "}\n";
    return out;
}

}  // namespace asamon
