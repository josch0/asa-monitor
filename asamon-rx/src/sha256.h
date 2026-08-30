// SPDX-License-Identifier: GPL-3.0-or-later
//
// SHA-256 nach FIPS 180-4, gerade so viel wie asamon-rx braucht.
//
// Warum eigener Code und keine Bibliothek: Die Pruefsumme wandert in den
// aud-Record, damit asamon-node die geschriebene Datei nicht selbst lesen muss,
// um zu melden, was er heute nebenbei mitrechnet. Dafuer eine Abhaengigkeit auf
// OpenSSL aufzunehmen — mit ihrer Bauwelt auf vier Zielplattformen — steht in
// keinem Verhaeltnis zu 150 Zeilen, die sich gegen die Testvektoren des
// Standards pruefen lassen (tests/test_sha256.cpp).

#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

namespace asamon {

// Fortschreibende Berechnung: Die Datei wird beim Schreiben gehasht, nicht
// hinterher noch einmal gelesen.
class Sha256 {
public:
    Sha256() { reset(); }

    void reset();
    void update(const void* data, std::size_t len);

    // Schliesst ab und liefert die 64 Zeichen lange Kleinbuchstaben-Hexfassung.
    // Nach hexDigest() ist das Objekt verbraucht; reset() macht es wieder
    // benutzbar.
    std::string hexDigest();

private:
    void transform(const std::uint8_t* block);

    std::uint32_t state_[8]{};
    std::uint64_t bitCount_ = 0;
    std::uint8_t  buffer_[64]{};
    std::size_t   bufferLen_ = 0;
};

// Bequemlichkeit fuer die Tests.
std::string sha256Hex(const void* data, std::size_t len);

}  // namespace asamon
