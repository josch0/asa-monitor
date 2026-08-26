// SPDX-License-Identifier: GPL-3.0-or-later
//
// Prueft das Bitlayout der Location Codes gegen ETSI TS 104 090, Tabelle A.19.
//
// Dort stehen die Location-Code-Saetze der offiziellen Teststroeme EWS1..EWS9
// samt ihrer **Byte-Laengen**. Diese Laengen sind eine zweite, unabhaengige
// normative Probe: Wer sie nachrechnen kann, hat Annex E richtig gelesen.
//
// Geprueft wird damit nicht der Produktcode — `asamon-rx` deutet Location
// Codes nicht — sondern das Verstaendnis, auf dem der ganze Parser steht.
// Insbesondere faellt hier auf, wenn NFF an der falschen Stelle sitzt: nur
// mit NFF in **jedem** Location Code geht die Rechnung auf.

#include "fib_builder.h"
#include "location_codes.h"

#include <iostream>
#include <string>
#include <vector>

using namespace asamon::test;

namespace {

int g_failures = 0;

void checkLength(const std::string& name, const std::vector<LocationCode>& codes,
                 size_t expectedBytes)
{
    size_t actual = 0;
    try {
        actual = encodeLocationCodes(codes).size();
    } catch (const std::exception& error) {
        std::cerr << "FEHLGESCHLAGEN: " << name << ": " << error.what() << "\n";
        ++g_failures;
        return;
    }
    if (actual != expectedBytes) {
        std::cerr << "FEHLGESCHLAGEN: " << name << ": " << expectedBytes
                  << " Byte nach Tabelle A.19, kodiert wurden " << actual << "\n";
        ++g_failures;
    }
}

void check(bool condition, const std::string& what)
{
    if (!condition) {
        std::cerr << "FEHLGESCHLAGEN: " << what << "\n";
        ++g_failures;
    }
}

}  // namespace

int main()
{
    // LC1 — die Empfaengerposition selbst: ein L6-Code, 5 Byte.
    checkLength("LC1", {{0, 1, "91BB82", {}}}, 5);

    // LC2 — acht L6-Codes um die Empfaengerposition, sub-codiert zu zwei
    // Location Codes: Z1:91BB8[76531], Z1:91BB4[FED]. 12 Byte.
    checkLength("LC2",
                {{0, 1, "91BB8", {7, 6, 5, 3, 1}},
                 {0, 1, "91BB4", {0xF, 0xE, 0xD}}},
                12);

    // LC3 — dieselben Ziffern in acht verschiedenen Zonen. Braucht zwei
    // FIG-0/15-Instanzen: fuenf Codes (25 Byte, das normative Maximum) mit
    // NFF = 1, dann drei Codes (15 Byte) mit NFF = 0.
    checkLength("LC3 erste Instanz",
                {{1, 0, "91BB82", {}},
                 {1, 10, "91BB82", {}},
                 {1, 2, "91BB82", {}},
                 {1, 41, "91BB82", {}},
                 {1, 19, "91BB82", {}}},
                25);
    checkLength("LC3 zweite Instanz",
                {{0, 20, "91BB82", {}},
                 {0, 11, "91BB82", {}},
                 {0, 12, "91BB82", {}}},
                15);

    // LC4 — acht L3-Codes, sub-codiert: Z1:91[FEA76], Z1:92[C84]. 10 Byte.
    checkLength("LC4",
                {{0, 1, "91", {0xF, 0xE, 0xA, 7, 6}},
                 {0, 1, "92", {0xC, 8, 4}}},
                10);

    // LC5 — neun L4-Codes: Z1:928[DC98], Z1:92C[10], Z1:91F3, Z1:91B[FB].
    // 19 Byte. Der lehrreichste Satz: er mischt sub-codierte und einfache
    // Rechtecke, und nur mit richtiger Padding-Regel geht die Summe auf.
    checkLength("LC5",
                {{0, 1, "928", {0xD, 0xC, 9, 8}},
                 {0, 1, "92C", {1, 0}},
                 {0, 1, "91F3", {}},
                 {0, 1, "91B", {0xF, 0xB}}},
                19);

    // Die Padding-Regel im Einzelnen: erst mit NFF ergibt sie Bytegrenzen.
    // 2 + 6 + 1 + 3 + 4 = 16 bit Grundgeruest, dazu 4 bit je Other digit.
    checkLength("null Other digits", {{0, 1, "9", {}}}, 2);
    checkLength("eine Other digit (mit Padding)", {{0, 1, "91", {}}}, 3);
    checkLength("zwei Other digits", {{0, 1, "91B", {}}}, 3);
    checkLength("fuenf Other digits (mit Padding)", {{0, 1, "91BB82", {}}}, 5);
    checkLength("vier Other digits mit Sub-codes", {{0, 1, "91BB8", {1}}}, 6);

    // Sechs Other digits gibt es nicht — Num digits ist drei Bit breit und
    // auf 0 bis 5 begrenzt.
    bool threw = false;
    try {
        encodeLocationCodes({{0, 1, "91BB821", {}}});
    } catch (const std::exception&) {
        threw = true;
    }
    check(threw, "mehr als fuenf Other digits werden abgelehnt");

    // Und bei SCF = 1 sind es hoechstens vier: die niedrigstwertige Ziffer
    // steckt dann in den Sub-codes.
    threw = false;
    try {
        encodeLocationCodes({{0, 1, "91BB82", {1}}});
    } catch (const std::exception&) {
        threw = true;
    }
    check(threw, "bei Sub-codes hoechstens vier Other digits");

    // Ein voller Satz muss auch als FIG durchs Nadeloehr passen: 25 Byte
    // Location Codes plus beide Header, Id und Status sind 29 Byte — gerade
    // noch innerhalb der 30 Byte Nutzlast eines FIB.
    Fig0_15 fig;
    fig.phase = 1;
    fig.subChId = 7;
    fig.hasStatus = true;
    fig.locationBytes = encodeLocationCodes({{1, 0, "91BB82", {}},
                                             {1, 10, "91BB82", {}},
                                             {1, 2, "91BB82", {}},
                                             {1, 41, "91BB82", {}},
                                             {1, 19, "91BB82", {}}});
    bool built = true;
    try {
        buildFibBytes({fig});
    } catch (const std::exception& error) {
        built = false;
        std::cerr << "  " << error.what() << "\n";
    }
    check(built, "LC3 passt als Trigger-FIG in einen FIB");

    if (g_failures == 0) {
        std::cerr << "test_location_codes: alle Pruefungen bestanden\n";
        return 0;
    }
    std::cerr << "test_location_codes: " << g_failures
              << " Pruefung(en) fehlgeschlagen\n";
    return 1;
}
