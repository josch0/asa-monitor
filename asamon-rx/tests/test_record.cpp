// SPDX-License-Identifier: GPL-3.0-or-later
//
// Serialisierung: Struktur -> JSON-Zeile.

#include "record.h"

#include <cmath>
#include <iostream>
#include <string>

using namespace asamon;

namespace {

int g_failures = 0;

void check(bool condition, const std::string& what)
{
    if (!condition) {
        std::cerr << "FEHLGESCHLAGEN: " << what << "\n";
        ++g_failures;
    }
}

void checkEqual(const std::string& actual, const std::string& expected,
                const std::string& what)
{
    if (actual != expected) {
        std::cerr << "FEHLGESCHLAGEN: " << what << "\n"
                  << "  erwartet: " << expected << "\n"
                  << "  bekommen: " << actual << "\n";
        ++g_failures;
    }
}

bool contains(const std::string& haystack, const std::string& needle)
{
    return haystack.find(needle) != std::string::npos;
}

Clock::time_point timePoint(long long seconds, long long nanos)
{
    return Clock::time_point{} + std::chrono::seconds(seconds) +
           std::chrono::nanoseconds(nanos);
}

void testTimestamp()
{
    checkEqual(formatRfc3339Nanos(timePoint(0, 0)), "1970-01-01T00:00:00.000000000Z",
               "Epoche");
    checkEqual(formatRfc3339Nanos(timePoint(1787752991, 482913771)),
               "2026-08-26T14:03:11.482913771Z", "Beispiel aus der Formatbeschreibung");

    // Der Grund, warum ts ein String ist: als Zahl waere dieser Wert nicht
    // mehr genau darstellbar. Die Probe darauf ist, dass die letzten Stellen
    // ueberleben.
    const std::string formatted = formatRfc3339Nanos(timePoint(1787752991, 999999999));
    check(contains(formatted, ".999999999Z"), "Nanosekunden bleiben vollstaendig");
}

void testInit()
{
    InitPayload payload;
    payload.channel = "5C";
    payload.freqHz = 178352000;
    payload.device = "rtl_sdr";
    payload.rxVersion = "0.1.0";
    payload.rxCommit = "abc1234";
    payload.welleCommit = "def5678";

    Record record;
    record.kind = RecordKind::Init;
    record.seq = 0;
    record.ts = timePoint(1787752991, 482913771);
    record.payload = payload;

    checkEqual(serialize(record),
               "{\"type\":\"init\",\"seq\":0,\"ts\":\"2026-08-26T14:03:11.482913771Z\","
               "\"format_version\":1,\"channel\":\"5C\",\"freq_hz\":178352000,"
               "\"device\":\"rtl_sdr\",\"device_serial\":\"\",\"rx_version\":\"0.1.0\","
               "\"rx_commit\":\"abc1234\",\"welle_commit\":\"def5678\"}\n",
               "init-Record");
}

void testTelemetry()
{
    TlmPayload payload;
    payload.snr = 12.4f;
    payload.sync = true;
    payload.signalPresent = true;
    payload.freqCorrFine = -3;
    payload.fibTotal = 125;
    payload.fibCrcErr = 2;
    payload.hasEid = true;
    payload.eid = 0x10FF;
    payload.hasEnsTime = true;
    payload.ensTime = "2026-08-26T14:03:11Z";
    payload.ensOffsetMin = 60;

    Record record;
    record.kind = RecordKind::Tlm;
    record.seq = 1;
    record.ts = timePoint(1787752991, 0);
    record.payload = payload;

    const std::string line = serialize(record);
    check(contains(line, "\"snr\":12.4"), "tlm: SNR");
    check(contains(line, "\"freq_corr\":{\"fine\":-3,\"coarse\":0}"), "tlm: Frequenzkorrektur");
    check(contains(line, "\"fib_total\":125"), "tlm: FIB-Zaehler");
    check(contains(line, "\"fib_crc_err\":2"), "tlm: CRC-Fehler");
    check(contains(line, "\"eid\":\"0x10FF\""), "tlm: EId als Kennung");
    check(contains(line, "\"ens_offset_min\":60"), "tlm: Zeitversatz");

    // tlm geht auch dann raus, wenn nichts empfangen wurde — sonst kann der
    // Server "Ensemble schweigt" nicht von "Knoten ist tot" unterscheiden.
    TlmPayload empty;
    Record emptyRecord;
    emptyRecord.kind = RecordKind::Tlm;
    emptyRecord.payload = empty;
    const std::string emptyLine = serialize(emptyRecord);
    check(contains(emptyLine, "\"fib_total\":0"), "tlm: leer, aber vorhanden");
    check(!contains(emptyLine, "\"eid\""), "tlm: ohne EId kein Feld");

    // JSON kennt kein NaN.
    TlmPayload nan;
    nan.snr = std::nan("");
    Record nanRecord;
    nanRecord.kind = RecordKind::Tlm;
    nanRecord.payload = nan;
    check(contains(serialize(nanRecord), "\"snr\":null"), "tlm: NaN wird null");
}

void testEnsemble()
{
    EnsComponent component;
    component.subChId = 7;
    component.startAddr = 128;
    component.size = 48;
    component.protection = "EEP 2-A";
    component.bitrate = 48;

    EnsService service;
    service.sid = 0xD3110AB;
    service.label = "ASA DE";
    service.components.push_back(component);

    EnsPayload payload;
    payload.eid = 0x10FF;
    payload.ecc = 0xE0;
    payload.label = "Bundesmux";
    payload.services.push_back(service);

    Record record;
    record.kind = RecordKind::Ens;
    record.payload = payload;

    const std::string line = serialize(record);
    check(contains(line, "\"label\":\"Bundesmux\""), "ens: Ensemble-Label");
    check(contains(line, "\"sid\":\"0x0D3110AB\""), "ens: SId als Kennung");
    check(contains(line, "\"subch_id\":7"), "ens: SubChId");
    check(contains(line, "\"protection\":\"EEP 2-A\""), "ens: Schutzstufe");
    check(contains(line, "\"bitrate\":48"), "ens: Bitrate");
}

void testAsa()
{
    {   // Heartbeat
        AsaPayload payload;
        payload.heartbeat = true;
        payload.cn = true;
        payload.raw = {0x01, 0x0F};
        payload.rawLen = 2;

        Record record;
        record.kind = RecordKind::Asa;
        record.seq = 42;
        record.ts = timePoint(0, 0);
        record.payload = payload;

        checkEqual(serialize(record),
                   "{\"type\":\"asa\",\"seq\":42,\"ts\":\"1970-01-01T00:00:00.000000000Z\","
                   "\"heartbeat\":true,\"cn\":true,\"oe\":false,\"pd_second_half\":false,"
                   "\"raw\":\"010f\"}\n",
                   "asa: Heartbeat");
    }

    {   // Trigger mit Location Codes
        AsaPayload payload;
        payload.pdSecondHalf = true;
        payload.hasPhase = true;
        payload.phase = 1;
        payload.hasSubChId = true;
        payload.subChId = 7;
        payload.hasStatus = true;
        payload.stage = 0;
        payload.iid = 3;
        payload.last = true;
        payload.hasNff = true;
        payload.nff = 0;
        payload.locationCodes = {0x1a, 0x2b, 0x3c, 0x4d};
        payload.locationLen = 4;
        payload.raw = {0x03, 0x0f, 0x47, 0x03};
        payload.rawLen = 4;

        Record record;
        record.kind = RecordKind::Asa;
        record.payload = payload;

        const std::string line = serialize(record);
        check(contains(line, "\"phase\":\"trigger\""), "asa: Phase benannt");
        check(contains(line, "\"stage\":\"level1_start\""), "asa: Stage benannt");
        check(contains(line, "\"subch_id\":7"), "asa: SubChId");
        check(contains(line, "\"iid\":3"), "asa: IId");
        check(contains(line, "\"last\":true"), "asa: Last");
        check(contains(line, "\"nff\":0"), "asa: NFF");
        check(contains(line, "\"location_codes\":\"1a2b3c4d\""), "asa: Location Codes roh");
        check(contains(line, "\"raw\":\"030f4703\""), "asa: raw immer dabei");
        check(!contains(line, "\"other_eid\""), "asa: kein OE-Feld bei oe=false");
    }

    {   // OE
        AsaPayload payload;
        payload.oe = true;
        payload.hasOtherEid = true;
        payload.otherEid = 0x10FF;
        payload.rawLen = 0;

        Record record;
        record.kind = RecordKind::Asa;
        record.payload = payload;

        const std::string line = serialize(record);
        check(contains(line, "\"other_eid\":\"0x10FF\""), "asa: EId des anderen Ensembles");
        check(!contains(line, "\"subch_id\""), "asa: kein SubChId bei oe=true");
    }

    {   // Unbekannter Stage-Wert: gemeldet, nicht verworfen. Heute nur
        // kuenstlich erreichbar — die Norm belegt alle acht Werte.
        AsaPayload payload;
        payload.hasStatus = true;
        payload.stage = 9;
        payload.rawLen = 0;

        Record record;
        record.kind = RecordKind::Asa;
        record.payload = payload;

        const std::string line = serialize(record);
        check(contains(line, "\"stage_raw\":9"), "asa: unbekannte Stage als stage_raw");
        check(!contains(line, "\"stage\":"), "asa: kein Name fuer unbekannte Stage");
    }
}

void testAudio()
{
    AudPayload payload;
    payload.subChId = 7;
    payload.dir = "/var/lib/asamon/audio";
    payload.startedTs = "2026-08-30T12:14:55.000000000Z";
    payload.seconds = 12.5;
    payload.files.push_back({"20260830T121455Z-5C-7.dabp", "dabp", 76800,
                             "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"});

    Record record;
    record.kind = RecordKind::Aud;
    record.payload = payload;

    const std::string line = serialize(record);
    check(contains(line, "\"subch_id\":7"), "aud: Subchannel");
    check(contains(line, "\"seconds\":12.50"), "aud: Dauer mit zwei Stellen");
    check(contains(line, "\"name\":\"20260830T121455Z-5C-7.dabp\""), "aud: Dateiname");
    check(contains(line, "\"bytes\":76800"), "aud: Groesse");
    // Ohne alert_uid faellt das Feld ganz weg, statt leer dazustehen.
    check(!contains(line, "\"alert_uid\""), "aud: keine leere alert_uid");
}

void testEncoding()
{
    checkEqual(toBase64(nullptr, 0), "", "Base64: leer");

    const uint8_t one[] = {'M'};
    const uint8_t two[] = {'M', 'a'};
    const uint8_t three[] = {'M', 'a', 'n'};
    checkEqual(toBase64(one, 1), "TQ==", "Base64: ein Byte");
    checkEqual(toBase64(two, 2), "TWE=", "Base64: zwei Byte");
    checkEqual(toBase64(three, 3), "TWFu", "Base64: drei Byte");

    const uint8_t bytes[] = {0x00, 0x0f, 0xff};
    checkEqual(toHex(bytes, 3), "000fff", "Hex");

    checkEqual(jsonEscape("a\"b\\c"), "a\\\"b\\\\c", "JSON: Anfuehrungszeichen");
    checkEqual(jsonEscape("Zeile\nUmbruch"), "Zeile\\nUmbruch", "JSON: Umbruch");
    checkEqual(jsonEscape(std::string("\x01")), "\\u0001", "JSON: Steuerzeichen");
    // Ein DAB-Label kann UTF-8 enthalten; das darf nicht zerlegt werden.
    checkEqual(jsonEscape("Bayern 3 Süd"), "Bayern 3 Süd", "JSON: UTF-8 bleibt");
}

}  // namespace

int main()
{
    testTimestamp();
    testInit();
    testTelemetry();
    testEnsemble();
    testAsa();
    testAudio();
    testEncoding();

    if (g_failures == 0) {
        std::cerr << "test_record: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_record: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
