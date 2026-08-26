// SPDX-License-Identifier: GPL-3.0-or-later
//
// Kodiert Location Codes nach ETSI TS 104 089, Annex E — nur fuer die Tests.
//
// `asamon-rx` selbst deutet Location Codes nicht; es reicht die Bytes roh
// durch, und die Geometrie macht `asamon-node`. Dieser Encoder dient einem
// anderen Zweck: ETSI TS 104 090, Tabelle A.19 nennt fuer die offiziellen
// Testströme EWS1..EWS9 die **Byte-Längen** der dort verwendeten Location-
// Code-Sätze. Wer sie nachrechnen kann, hat das Bitlayout richtig verstanden —
// und das ist eine zweite, unabhängige normative Probe neben Annex E selbst.
//
// Aufbau eines Location Codes:
//
//   NFF 2 | Zone 6 | SCF 1 | Num digits 3 | Digit 1 4 |
//   Other digits 4*n | Padding 0/4 | Sub-codes 0/16
//
// Die Padding-Regel ("nur wenn Num digits ungerade") ergibt genau dann eine
// Byte-Ausrichtung, wenn NFF mitgezählt wird — womit nebenbei feststeht, dass
// NFF in **jedem** Location Code steht, nicht nur im ersten.

#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace asamon::test {

struct LocationCode {
    uint8_t  nff = 0;
    uint8_t  zone = 0;

    // Ziffern des Location Codes, hexadezimal als Text, z. B. "91BB82".
    // Die erste ist Digit 1, der Rest sind die "Other digits".
    std::string digits;

    // Sub-codes: welche der 16 Teilflächen zum Warngebiet gehören. Leer
    // bedeutet SCF = 0, also ein einzelnes sphärisches Rechteck.
    std::vector<uint8_t> subCodes;
};

// Kodiert eine Folge von Location Codes zu den Bytes, die im Type-0-Feld
// stehen. Wirft std::logic_error, wenn das Ergebnis nicht byte-ausgerichtet
// wäre — das darf nach Annex E nicht vorkommen.
std::vector<uint8_t> encodeLocationCodes(const std::vector<LocationCode>& codes);

}  // namespace asamon::test
