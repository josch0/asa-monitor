// SPDX-License-Identifier: GPL-3.0-or-later
//
// Baut FIBs mit FIG-0/15-Instanzen von Hand.
//
// FIBProcessor::processFIB() ist public — ein Test kann handgebaute FIBs
// direkt einspeisen, ohne Funk, ohne Prozessgrenze, ohne Aufzeichnung. Seit
// rohe FIBs nicht mehr uebertragen werden, ist das die einzige Stelle, an der
// sich der Parser gegen die Norm pruefen laesst (TODO.md Abschnitt 12).
//
// Der Builder setzt die Felder genau so, wie sie ihm gegeben werden, und
// leitet nichts her ausser der Laenge. Das ist Absicht: nur so lassen sich
// auch normwidrige FIGs bauen und pruefen, dass der Parser sie meldet statt
// sie zu verschlucken.

#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace asamon::test {

struct Fig0_15 {
    bool cn = false;
    bool oe = false;
    bool pdSecondHalf = false;

    // Heartbeat: leeres Type-0-Feld, Laengenfeld == 1. Alle uebrigen Felder
    // werden dann nicht geschrieben.
    bool heartbeat = false;

    uint8_t  phase   = 0;   // nur bei oe == false
    uint8_t  subChId = 0;   // nur bei oe == false
    uint16_t otherEid = 0;  // nur bei oe == true

    // Rfa + Sec, das zweite Byte des Id fields. Nur in der Pre-trigger-Phase.
    bool    hasSec = false;
    uint8_t sec    = 0;

    bool    hasStatus = false;
    bool    last      = false;
    uint8_t stage     = 0;
    uint8_t iid       = 0;

    // Rohe Bytes der Location Codes, einschliesslich der zwei NFF-Bits am
    // Anfang des ersten Codes.
    std::vector<uint8_t> locationBytes;

    // Ueberschreibt das Laengenfeld des FIG-Headers. Fuer Negativtests: ein
    // FIG, dessen Laenge nicht zum Inhalt passt.
    bool    overrideLength = false;
    uint8_t length = 0;
};

// Gepackte FIB-Bytes: 30 Byte Nutzlast, mit FIG-Typ 7 (Ende) aufgefuellt.
// Die zwei CRC-Bytes fehlen — processFIB() prueft die CRC nicht mehr.
std::vector<uint8_t> buildFibBytes(const std::vector<Fig0_15>& figs);

// Wandelt gepackte Bytes in die Darstellung um, die das welle.io-Backend
// erwartet: ein Byte je Bit, 256 Byte je FIB.
std::vector<uint8_t> bytesToBits(const std::vector<uint8_t>& bytes);

std::string toHex(const std::vector<uint8_t>& bytes);
std::vector<uint8_t> fromHex(const std::string& hex);

}  // namespace asamon::test
