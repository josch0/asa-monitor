// SPDX-License-Identifier: GPL-3.0-or-later

#include "fib_builder.h"

#include <stdexcept>

namespace asamon::test {

namespace {

class BitWriter {
public:
    void put(uint32_t value, int bits)
    {
        for (int i = bits - 1; i >= 0; --i) {
            bits_.push_back(static_cast<uint8_t>((value >> i) & 1));
        }
    }

    void putByte(uint8_t value) { put(value, 8); }

    size_t bitCount() const { return bits_.size(); }

    std::vector<uint8_t> pack() const
    {
        if (bits_.size() % 8 != 0) {
            throw std::logic_error("BitWriter: Bitfolge ist nicht byte-ausgerichtet");
        }
        std::vector<uint8_t> out(bits_.size() / 8, 0);
        for (size_t i = 0; i < bits_.size(); ++i) {
            out[i / 8] = static_cast<uint8_t>((out[i / 8] << 1) | bits_[i]);
        }
        return out;
    }

private:
    std::vector<uint8_t> bits_;
};

// Alles ausser dem FIG-Header: Type-0-Header und Type-0-Feld.
std::vector<uint8_t> buildFigBody(const Fig0_15& fig)
{
    BitWriter writer;

    // Type-0-Header: C/N, OE, P/D, Extension (5 bit)
    writer.put(fig.cn ? 1 : 0, 1);
    writer.put(fig.oe ? 1 : 0, 1);
    writer.put(fig.pdSecondHalf ? 1 : 0, 1);
    writer.put(15, 5);

    if (!fig.heartbeat) {
        if (fig.oe) {
            writer.put(fig.otherEid, 16);
        }
        else {
            writer.put(fig.phase, 2);
            writer.put(fig.subChId, 6);
            if (fig.hasSec) {
                writer.put(0, 2);          // Rfa
                writer.put(fig.sec, 6);
            }
        }
        if (fig.hasStatus) {
            writer.put(fig.last ? 1 : 0, 1);
            writer.put(fig.stage, 3);
            writer.put(fig.iid, 4);
        }
        for (const uint8_t byte : fig.locationBytes) {
            writer.putByte(byte);
        }
    }

    return writer.pack();
}

}  // namespace

std::vector<uint8_t> buildFibBytes(const std::vector<Fig0_15>& figs)
{
    std::vector<uint8_t> out;

    for (const auto& fig : figs) {
        const std::vector<uint8_t> body = buildFigBody(fig);
        const uint8_t length = fig.overrideLength
                                   ? fig.length
                                   : static_cast<uint8_t>(body.size());
        if (length > 31) {
            throw std::logic_error("FIG-Laenge passt nicht in 5 bit");
        }
        // FIG-Header: Typ 0 (3 bit) + Laenge (5 bit)
        out.push_back(static_cast<uint8_t>((0 << 5) | length));
        out.insert(out.end(), body.begin(), body.end());
    }

    if (out.size() > 30) {
        throw std::logic_error("FIB-Nutzlast ueberschreitet 30 Byte");
    }
    // Auffuellen mit FIG-Typ 7 und Laenge 31 — das Endezeichen, an dem
    // processFIB() den Durchlauf abbricht.
    out.resize(30, 0xFF);
    return out;
}

std::vector<uint8_t> bytesToBits(const std::vector<uint8_t>& bytes)
{
    std::vector<uint8_t> bits(256, 0);
    for (size_t i = 0; i < bytes.size() && i < 32; ++i) {
        for (int bit = 0; bit < 8; ++bit) {
            bits[i * 8 + bit] = static_cast<uint8_t>((bytes[i] >> (7 - bit)) & 1);
        }
    }
    return bits;
}

std::string toHex(const std::vector<uint8_t>& bytes)
{
    static const char digits[] = "0123456789abcdef";
    std::string out;
    out.reserve(bytes.size() * 2);
    for (const uint8_t byte : bytes) {
        out += digits[byte >> 4];
        out += digits[byte & 0x0F];
    }
    return out;
}

std::vector<uint8_t> fromHex(const std::string& hex)
{
    auto nibble = [](char c) -> int {
        if (c >= '0' && c <= '9') return c - '0';
        if (c >= 'a' && c <= 'f') return c - 'a' + 10;
        if (c >= 'A' && c <= 'F') return c - 'A' + 10;
        throw std::logic_error("ungueltiges Hex-Zeichen");
    };

    if (hex.size() % 2 != 0) throw std::logic_error("Hex-Laenge ist ungerade");
    std::vector<uint8_t> out;
    out.reserve(hex.size() / 2);
    for (size_t i = 0; i < hex.size(); i += 2) {
        out.push_back(static_cast<uint8_t>((nibble(hex[i]) << 4) | nibble(hex[i + 1])));
    }
    return out;
}

}  // namespace asamon::test
