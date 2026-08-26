// SPDX-License-Identifier: GPL-3.0-or-later
//
// Prueft den FIG-0/15-Parser gegen ETSI TS 104 089, Annex E.
//
// Der Weg ist derselbe wie im Betrieb: handgebauter FIB ->
// FIBProcessor::processFIB() -> onAsaAlert() -> asa-Record -> JSON-Zeile.
// Nur der Funk fehlt.
//
// Aufruf ohne Argumente: pruefen. Mit --write-fixtures <datei>: die
// Fixture-Datei neu erzeugen. Sie ist eingecheckt, damit eine Aenderung am
// Parser als Diff sichtbar wird und nicht nur als rote Zusicherung.

#include "controller.h"
#include "record.h"

#include "fib_builder.h"
#include "location_codes.h"

#include "fib-processor.h"
#include "radio-controller.h"

#include <cstdio>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

using namespace asamon;
using namespace asamon::test;

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

// Nimmt die Alerts entgegen. Alle uebrigen Rueckrufe sind leer — sie muessen
// nur existieren, weil das Interface rein virtuell ist.
class CollectingController : public RadioControllerInterface {
public:
    std::vector<asa_alert_t> alerts;

    void onSNR(float) override {}
    void onFrequencyCorrectorChange(int, int) override {}
    void onSyncChange(char) override {}
    void onSignalPresence(bool) override {}
    void onServiceDetected(uint32_t) override {}
    void onNewEnsemble(uint16_t) override {}
    void onSetEnsembleLabel(DabLabel&) override {}
    void onDateTimeUpdate(const dab_date_time_t&) override {}
    void onFIBDecodeSuccess(bool, const uint8_t*) override {}
    void onNewImpulseResponse(std::vector<float>&&) override {}
    void onConstellationPoints(std::vector<DSPCOMPLEX>&&) override {}
    void onNewNullSymbol(std::vector<DSPCOMPLEX>&&) override {}
    void onTIIMeasurement(tii_measurement_t&&) override {}
    void onMessage(message_level_t, const std::string&, const std::string&) override {}
    void onAsaAlert(const asa_alert_t& alert) override { alerts.push_back(alert); }
};

std::vector<asa_alert_t> feed(const std::vector<uint8_t>& fibBytes)
{
    CollectingController controller;
    FIBProcessor processor(controller);
    std::vector<uint8_t> bits = bytesToBits(fibBytes);
    processor.processFIB(bits.data(), 0);
    return controller.alerts;
}

std::vector<asa_alert_t> feed(const std::vector<Fig0_15>& figs)
{
    return feed(buildFibBytes(figs));
}

// Serialisiert wie im Betrieb, aber mit festem seq und Zeitstempel, damit die
// Zeile vergleichbar ist.
std::string serializeAsa(const asa_alert_t& alert)
{
    bool reportable = false;
    Record record;
    record.kind = RecordKind::Asa;
    record.seq = 0;
    record.ts = Clock::time_point{};
    record.payload = asaPayloadFrom(alert, reportable);
    std::string line = serialize(record);
    if (!line.empty() && line.back() == '\n') line.pop_back();
    return line;
}

struct Case {
    std::string name;
    std::vector<Fig0_15> figs;
};

// Die Faelle aus TODO.md Abschnitt 12, samt der dort ausdruecklich genannten
// "vergessenen Faelle".
std::vector<Case> allCases()
{
    std::vector<Case> cases;

    {   // Heartbeat: leeres Type-0-Feld, Laengenfeld == 1, C/N = 1, OE = 0.
        // Der wertvollste Fall fuer die Abdeckungskarte — und genau der, den
        // der einzige spec-konforme Fork-Parser (Qt-DAB) verwirft.
        Fig0_15 fig;
        fig.cn = true;
        fig.heartbeat = true;
        cases.push_back({"heartbeat", {fig}});
    }
    {   // Heartbeat in der zweiten Minutenhaelfte: P/D gesetzt.
        Fig0_15 fig;
        fig.cn = true;
        fig.heartbeat = true;
        fig.pdSecondHalf = true;
        cases.push_back({"heartbeat_second_half", {fig}});
    }
    {   // Trigger im eigenen Ensemble, mit Status und einem Location Code.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.last = true;
        fig.stage = 0;      // Level 1 Start
        fig.iid = 3;
        fig.locationBytes = {0x0A, 0x2B, 0x3C, 0x4D};
        cases.push_back({"trigger_level1_start", {fig}});
    }
    {   // Pre-trigger mit dem Sonderwert 63: Start bei Sekunde 0, 5 s Trigger.
        Fig0_15 fig;
        fig.phase = 0;
        fig.subChId = 12;
        fig.hasSec = true;
        fig.sec = 63;
        fig.hasStatus = true;
        fig.stage = 4;      // Level 2 Start
        fig.iid = 9;
        cases.push_back({"pre_trigger_sec63", {fig}});
    }
    {   // Pre-trigger mit gewoehnlichem Sekundenwert.
        Fig0_15 fig;
        fig.phase = 0;
        fig.subChId = 12;
        fig.hasSec = true;
        fig.sec = 30;
        fig.hasStatus = true;
        fig.stage = 1;
        fig.iid = 1;
        cases.push_back({"pre_trigger_sec30", {fig}});
    }
    {   // OE = 1: das Id-Feld ist die 16-bit-EId des anderen Ensembles, und es
        // wird ausschliesslich Trigger signalisiert (Norm 6.5.1) — das Status-
        // Feld folgt also immer. Genau hier irrt der WarnBridge-Parser.
        Fig0_15 fig;
        fig.oe = true;
        fig.otherEid = 0x10FF;
        fig.hasStatus = true;
        fig.last = true;
        fig.stage = 2;
        fig.iid = 5;
        fig.locationBytes = {0x01, 0x02};
        cases.push_back({"oe_trigger", {fig}});
    }
    {   // Stage 7 = Test. Consumer-Empfaenger werten das nicht aus; fuer einen
        // Monitor ist es das Interessanteste, was vor dem Regelbetrieb kommt.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 4;
        fig.hasStatus = true;
        fig.stage = 7;
        fig.iid = 0;
        cases.push_back({"stage_test", {fig}});
    }
    {   // NFF > 0: es folgen weitere Instanzen dieses Alert-Sets.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.stage = 0;
        fig.iid = 3;
        fig.locationBytes = {0x80, 0x11, 0x22, 0x33};   // NFF = 2 in den ersten zwei Bit
        cases.push_back({"nff_two_following", {fig}});
    }
    {   // Sustain: nur das Id-Feld, kein Status, keine Location Codes.
        Fig0_15 fig;
        fig.cn = true;
        fig.phase = 2;
        fig.subChId = 7;
        cases.push_back({"sustain", {fig}});
    }
    {   // End, ebenso.
        Fig0_15 fig;
        fig.cn = true;
        fig.phase = 3;
        fig.subChId = 7;
        cases.push_back({"end", {fig}});
    }
    {   // Location Codes in voller Laenge: 25 Byte, das normative Maximum.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 21;
        fig.hasStatus = true;
        fig.stage = 3;      // Level 1 Critical
        fig.iid = 15;
        for (int i = 0; i < 25; ++i) {
            fig.locationBytes.push_back(static_cast<uint8_t>(0x40 + i));
        }
        cases.push_back({"location_codes_max", {fig}});
    }
    {   // Der Location-Code-Satz LC3 aus ETSI TS 104 090, Tabelle A.19, erste
        // Instanz: fuenf Codes, 25 Byte — das normative Maximum — mit NFF = 1,
        // also folgt genau eine weitere Instanz. Ein echter Satz aus den
        // offiziellen Teststroemen, kein ausgedachter.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.stage = 0;
        fig.iid = 1;
        fig.locationBytes = encodeLocationCodes({{1, 0, "91BB82", {}},
                                                 {1, 10, "91BB82", {}},
                                                 {1, 2, "91BB82", {}},
                                                 {1, 41, "91BB82", {}},
                                                 {1, 19, "91BB82", {}}});
        cases.push_back({"lc3_first_instance", {fig}});
    }
    {   // Und die zweite Instanz desselben Satzes: drei Codes, 15 Byte,
        // NFF = 0 — die letzte des Alert-Sets.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.last = true;
        fig.stage = 0;
        fig.iid = 1;
        fig.locationBytes = encodeLocationCodes({{0, 20, "91BB82", {}},
                                                 {0, 11, "91BB82", {}},
                                                 {0, 12, "91BB82", {}}});
        cases.push_back({"lc3_second_instance", {fig}});
    }
    {   // LC5: neun L4-Codes, teils sub-codiert, 19 Byte.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 21;
        fig.hasStatus = true;
        fig.stage = 4;
        fig.iid = 2;
        fig.locationBytes = encodeLocationCodes({{0, 1, "928", {0xD, 0xC, 9, 8}},
                                                 {0, 1, "92C", {1, 0}},
                                                 {0, 1, "91F3", {}},
                                                 {0, 1, "91B", {0xF, 0xB}}});
        cases.push_back({"lc5_mixed_subcoding", {fig}});
    }
    {   // Zwei Instanzen in einem FIB — der FIB-Walk muss beide finden.
        Fig0_15 first;
        first.phase = 1;
        first.subChId = 7;
        first.hasStatus = true;
        first.stage = 0;
        first.iid = 3;
        first.locationBytes = {0x40, 0x11};

        Fig0_15 second;
        second.oe = true;
        second.otherEid = 0x1234;
        second.hasStatus = true;
        second.last = true;
        second.stage = 0;
        second.iid = 3;
        cases.push_back({"two_instances", {first, second}});
    }
    {   // Laengenfeld zu klein fuer das Status-Feld: gemeldet, nicht verworfen.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.stage = 0;
        fig.iid = 3;
        fig.overrideLength = true;
        fig.length = 2;     // deckt nur Type-0-Header und Id-Feld
        cases.push_back({"truncated_status", {fig}});
    }
    {   // Location Codes hinter einem Sustain-Id-Feld: normwidrig (6.4.4),
        // wird gemeldet und trotzdem uebergeben.
        Fig0_15 fig;
        fig.phase = 2;
        fig.subChId = 7;
        fig.locationBytes = {0x00, 0x11};
        cases.push_back({"location_after_sustain", {fig}});
    }
    {   // Mehr als die zulaessigen 25 Byte Location Codes: eine
        // Normverletzung, die sichtbar werden soll statt abgeschnitten zu
        // werden. 26 ist dabei das Aeusserste, was ein Trigger-FIG ueberhaupt
        // tragen kann — 30 Byte FIB minus beide Header, Id und Status.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.stage = 0;
        fig.iid = 3;
        for (int i = 0; i < 26; ++i) {
            fig.locationBytes.push_back(static_cast<uint8_t>(i));
        }
        cases.push_back({"location_codes_over_limit", {fig}});
    }

    return cases;
}

// Prueft die Bedeutung, nicht nur die Zeichenkette: dass die Bits an den
// Stellen landen, die Annex E vorschreibt.
void checkSemantics()
{
    {
        const auto cases = allCases();
        for (const auto& testCase : cases) {
            const auto alerts = feed(testCase.figs);
            check(!alerts.empty(), testCase.name + ": kein Alert gemeldet");
        }
    }

    {   // Heartbeat
        Fig0_15 fig;
        fig.cn = true;
        fig.heartbeat = true;
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Heartbeat: genau ein Alert");
        check(alerts[0].heartbeat, "Heartbeat: erkannt");
        check(alerts[0].cn, "Heartbeat: C/N gesetzt");
        check(!alerts[0].oe, "Heartbeat: OE nicht gesetzt");
        check(!alerts[0].hasStatus, "Heartbeat: kein Status-Feld");
        check(alerts[0].locationCodes.empty(), "Heartbeat: keine Location Codes");
        check(!alerts[0].parseError, "Heartbeat: kein Parserfehler");
        check(alerts[0].raw.size() == 2, "Heartbeat: raw ist zwei Byte");
    }

    {   // Trigger, eigenes Ensemble
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.last = true;
        fig.stage = 3;
        fig.iid = 9;
        fig.locationBytes = {0xC0, 0x11, 0x22, 0x33};
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Trigger: genau ein Alert");
        check(!alerts[0].heartbeat, "Trigger: kein Heartbeat");
        check(alerts[0].phase == 1, "Trigger: Phase == 1");
        check(alerts[0].subChId == 7, "Trigger: SubChId == 7");
        check(alerts[0].hasStatus, "Trigger: Status vorhanden");
        check(alerts[0].last, "Trigger: Last gesetzt");
        check(alerts[0].stage == 3, "Trigger: Stage == 3");
        check(alerts[0].iid == 9, "Trigger: IId == 9");
        check(alerts[0].hasNff, "Trigger: NFF vorhanden");
        check(alerts[0].nff == 3, "Trigger: NFF == 3 (obere zwei Bit von 0xC0)");
        check(alerts[0].locationCodes.size() == 4, "Trigger: vier Location-Byte");
        check(!alerts[0].parseError, "Trigger: kein Parserfehler");
    }

    {   // Pre-trigger: Sec sitzt in den unteren sechs Bit des zweiten Bytes
        Fig0_15 fig;
        fig.phase = 0;
        fig.subChId = 12;
        fig.hasSec = true;
        fig.sec = 63;
        fig.hasStatus = true;
        fig.stage = 0;
        fig.iid = 1;
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Pre-trigger: genau ein Alert");
        check(alerts[0].phase == 0, "Pre-trigger: Phase == 0");
        check(alerts[0].subChId == 12, "Pre-trigger: SubChId == 12");
        check(alerts[0].hasSec, "Pre-trigger: Sec vorhanden");
        check(alerts[0].sec == 63, "Pre-trigger: Sec == 63");
        check(alerts[0].hasStatus, "Pre-trigger: Status vorhanden");
        check(alerts[0].iid == 1, "Pre-trigger: IId == 1");
    }

    {   // OE: die 16 Bit sind die EId, nicht Phase und SubChId
        Fig0_15 fig;
        fig.oe = true;
        fig.otherEid = 0x10FF;
        fig.hasStatus = true;
        fig.stage = 2;
        fig.iid = 5;
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "OE: genau ein Alert");
        check(alerts[0].oe, "OE: Flag gesetzt");
        check(alerts[0].otherEId == 0x10FF, "OE: EId == 0x10FF");
        check(alerts[0].hasStatus, "OE: Status vorhanden");
        check(alerts[0].stage == 2, "OE: Stage == 2");
        check(alerts[0].iid == 5, "OE: IId == 5");
        check(!alerts[0].parseError, "OE: kein Parserfehler");
    }

    {   // Sustain: nur das Id-Feld
        Fig0_15 fig;
        fig.cn = true;
        fig.phase = 2;
        fig.subChId = 7;
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Sustain: genau ein Alert");
        check(alerts[0].phase == 2, "Sustain: Phase == 2");
        check(!alerts[0].hasStatus, "Sustain: kein Status-Feld");
        check(!alerts[0].hasNff, "Sustain: kein NFF");
        check(!alerts[0].parseError, "Sustain: kein Parserfehler");
    }

    {   // Zwei Instanzen in einem FIB
        Fig0_15 first;
        first.phase = 1;
        first.subChId = 7;
        first.hasStatus = true;
        Fig0_15 second;
        second.oe = true;
        second.otherEid = 0x1234;
        second.hasStatus = true;
        const auto alerts = feed({first, second});
        check(alerts.size() == 2, "Zwei Instanzen: beide gemeldet");
        if (alerts.size() == 2) {
            check(!alerts[0].oe, "Zwei Instanzen: erste ist eigenes Ensemble");
            check(alerts[1].otherEId == 0x1234, "Zwei Instanzen: zweite traegt die EId");
        }
    }

    {   // Zu kurzes Laengenfeld: gemeldet, nicht verworfen
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.overrideLength = true;
        fig.length = 2;
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Abgeschnitten: trotzdem gemeldet");
        check(alerts[0].parseError, "Abgeschnitten: parseError gesetzt");
        check(alerts[0].phase == 1, "Abgeschnitten: Id-Feld bleibt lesbar");
    }

    {   // Mehr als 25 Byte Location Codes
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        for (int i = 0; i < 26; ++i) {
            fig.locationBytes.push_back(static_cast<uint8_t>(i));
        }
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Ueberlange Location Codes: gemeldet");
        check(alerts[0].parseError, "Ueberlange Location Codes: parseError gesetzt");
        check(alerts[0].locationCodes.size() == 26,
              "Ueberlange Location Codes: nichts abgeschnitten");
    }

    {   // Location Codes hinter Sustain: normwidrig
        Fig0_15 fig;
        fig.phase = 3;
        fig.subChId = 7;
        fig.locationBytes = {0x00, 0x11};
        const auto alerts = feed({fig});
        check(alerts.size() == 1, "Location nach End: gemeldet");
        check(alerts[0].parseError, "Location nach End: parseError gesetzt");
    }

    {   // Alle acht Stage-Werte und alle vier Phasen haben einen Namen. Damit
        // ist stage_raw/phase_raw heute unerreichbar — die Vorkehrung greift
        // erst, wenn die Norm erweitert wird. Genau das haelt dieser Test fest.
        for (uint8_t stage = 0; stage < 8; ++stage) {
            check(stageName(stage) != nullptr,
                  "Stage " + std::to_string(stage) + " hat einen Namen");
        }
        for (uint8_t phase = 0; phase < 4; ++phase) {
            check(phaseName(phase) != nullptr,
                  "Phase " + std::to_string(phase) + " hat einen Namen");
        }
        check(stageName(8) == nullptr, "Stage 8 hat keinen Namen");
        check(phaseName(4) == nullptr, "Phase 4 hat keinen Namen");
    }

    {   // Das raw-Feld traegt beide Header und den gesamten FIG-Inhalt.
        Fig0_15 fig;
        fig.phase = 1;
        fig.subChId = 7;
        fig.hasStatus = true;
        fig.stage = 0;
        fig.iid = 3;
        const auto fibBytes = buildFibBytes({fig});
        const auto alerts = feed(fibBytes);
        check(alerts.size() == 1, "raw: ein Alert");
        if (alerts.size() == 1) {
            const auto& raw = alerts[0].raw;
            check(raw.size() == 4, "raw: FIG-Header + drei Byte");
            bool identical = raw.size() <= fibBytes.size();
            for (size_t i = 0; identical && i < raw.size(); ++i) {
                identical = raw[i] == fibBytes[i];
            }
            check(identical, "raw: Bytes stimmen mit dem FIB ueberein");
        }
    }
}

std::string fixturePath(int argc, char** argv, int index)
{
    return index < argc ? argv[index] : std::string();
}

void writeFixtures(const std::string& path)
{
    std::ofstream out(path);
    if (!out) {
        std::cerr << "kann " << path << " nicht schreiben\n";
        ++g_failures;
        return;
    }
    out << "# Handgebaute FIG-0/15-Instanzen nach ETSI TS 104 089, Annex E.\n"
        << "# Erzeugt von tests/test_fig0_15.cpp --write-fixtures.\n"
        << "# fib    = gepackte FIB-Nutzlast, 30 Byte, hex\n"
        << "# expect = asa-Record, wie asamon-rx ihn schreibt (seq 0, Zeit 0)\n";

    for (const auto& testCase : allCases()) {
        const auto fibBytes = buildFibBytes(testCase.figs);
        const auto alerts = feed(fibBytes);
        out << "\nname=" << testCase.name << "\n";
        out << "fib=" << asamon::test::toHex(fibBytes) << "\n";
        for (const auto& alert : alerts) {
            out << "expect=" << serializeAsa(alert) << "\n";
        }
    }
}

// Prueft die eingecheckten Fixtures. Ein Diff hier ist eine Aenderung am
// Parser oder am Record-Format — beides soll auffallen.
void checkFixtures(const std::string& path)
{
    std::ifstream in(path);
    if (!in) {
        std::cerr << "Fixture-Datei " << path << " nicht lesbar\n";
        ++g_failures;
        return;
    }

    std::string line;
    std::string name;
    std::vector<uint8_t> fibBytes;
    std::vector<std::string> expected;

    auto verify = [&] {
        if (name.empty()) return;
        const auto alerts = feed(fibBytes);
        if (alerts.size() != expected.size()) {
            std::cerr << "FEHLGESCHLAGEN: " << name << ": " << expected.size()
                      << " Records erwartet, " << alerts.size() << " bekommen\n";
            ++g_failures;
            return;
        }
        for (size_t i = 0; i < alerts.size(); ++i) {
            checkEqual(serializeAsa(alerts[i]), expected[i], name + " [" +
                                                                 std::to_string(i) + "]");
        }
    };

    while (std::getline(in, line)) {
        if (!line.empty() && line.back() == '\r') line.pop_back();
        if (line.empty() || line[0] == '#') continue;

        if (line.rfind("name=", 0) == 0) {
            verify();
            name = line.substr(5);
            fibBytes.clear();
            expected.clear();
        }
        else if (line.rfind("fib=", 0) == 0) {
            fibBytes = asamon::test::fromHex(line.substr(4));
        }
        else if (line.rfind("expect=", 0) == 0) {
            expected.push_back(line.substr(7));
        }
    }
    verify();
}

}  // namespace

int main(int argc, char** argv)
{
    const std::string mode = argc > 1 ? argv[1] : std::string();

    if (mode == "--write-fixtures") {
        const std::string path = fixturePath(argc, argv, 2);
        if (path.empty()) {
            std::cerr << "--write-fixtures braucht einen Dateinamen\n";
            return 2;
        }
        writeFixtures(path);
        std::cerr << "Fixtures geschrieben nach " << path << "\n";
        return g_failures == 0 ? 0 : 1;
    }

    checkSemantics();

    const std::string path =
        mode.empty() ? std::string(ASAMON_FIXTURE_DIR) + "/fig0_15.fixtures" : mode;
    checkFixtures(path);

    if (g_failures == 0) {
        std::cerr << "test_fig0_15: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_fig0_15: " << g_failures << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
