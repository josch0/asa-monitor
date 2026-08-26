// SPDX-License-Identifier: GPL-3.0-or-later

#include "location_codes.h"

#include <stdexcept>

namespace asamon::test {

namespace {

uint8_t digitValue(char c)
{
    if (c >= '0' && c <= '9') return static_cast<uint8_t>(c - '0');
    if (c >= 'a' && c <= 'f') return static_cast<uint8_t>(c - 'a' + 10);
    if (c >= 'A' && c <= 'F') return static_cast<uint8_t>(c - 'A' + 10);
    throw std::logic_error("Location code: ungueltige Ziffer");
}

}  // namespace

std::vector<uint8_t> encodeLocationCodes(const std::vector<LocationCode>& codes)
{
    std::vector<uint8_t> bits;

    auto put = [&bits](uint32_t value, int width) {
        for (int i = width - 1; i >= 0; --i) {
            bits.push_back(static_cast<uint8_t>((value >> i) & 1));
        }
    };

    for (const auto& code : codes) {
        if (code.digits.empty()) {
            throw std::logic_error("Location code ohne Ziffern");
        }
        // Num digits zählt die "Other digits", also alles ausser Digit 1.
        const size_t otherCount = code.digits.size() - 1;
        if (otherCount > 5) {
            throw std::logic_error("Location code: mehr als fuenf Other digits");
        }
        const bool subCoded = !code.subCodes.empty();
        if (subCoded && otherCount > 4) {
            throw std::logic_error("Location code: bei SCF=1 hoechstens vier Other digits");
        }

        put(code.nff, 2);
        put(code.zone, 6);
        put(subCoded ? 1 : 0, 1);
        put(static_cast<uint32_t>(otherCount), 3);
        put(digitValue(code.digits[0]), 4);
        for (size_t i = 1; i < code.digits.size(); ++i) {
            put(digitValue(code.digits[i]), 4);
        }
        // Padding nur bei ungerader Anzahl Other digits — dann und nur dann
        // steht die Struktur wieder auf einer Bytegrenze.
        if (otherCount % 2 == 1) {
            put(0, 4);
        }
        if (subCoded) {
            uint16_t mask = 0;
            for (const uint8_t index : code.subCodes) {
                if (index > 15) throw std::logic_error("Sub-code ausserhalb 0-15");
                mask = static_cast<uint16_t>(mask | (1u << index));
            }
            put(mask, 16);
        }
    }

    if (bits.size() % 8 != 0) {
        throw std::logic_error("Location codes sind nicht byte-ausgerichtet");
    }
    std::vector<uint8_t> out(bits.size() / 8, 0);
    for (size_t i = 0; i < bits.size(); ++i) {
        out[i / 8] = static_cast<uint8_t>((out[i / 8] << 1) | bits[i]);
    }
    return out;
}

}  // namespace asamon::test
