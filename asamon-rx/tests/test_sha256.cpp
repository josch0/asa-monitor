// SPDX-License-Identifier: GPL-3.0-or-later
//
// SHA-256 gegen die Testvektoren aus FIPS 180-4 und die ueblichen
// Zusatzfaelle.
//
// Warum das einen eigenen Test verdient: Die Pruefsumme steht im aud-Record
// und ist die einzige Stelle, an der ein Uebertragungsfehler zwischen Platte
// und Server auffallen wuerde. Eine Hashfunktion, die stillschweigend falsch
// rechnet, liefert trotzdem huebsche Hexzeichen — auffallen wuerde es erst
// dort, wo niemand mehr nachsehen kann.

#include "sha256.h"

#include <iostream>
#include <string>
#include <vector>

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

std::string hashOf(const std::string& s)
{
    return sha256Hex(s.data(), s.size());
}

void testVektoren()
{
    check(hashOf("") ==
              "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
          "leere Eingabe");
    check(hashOf("abc") ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "\"abc\"");
    check(hashOf("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq") ==
              "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
          "448-bit-Vektor");
    check(hashOf("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmn"
                 "hijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu") ==
              "cf5b16a778af8380036ce59e7b0492370b249b11e8f07a51afac45037afee9d1",
          "896-bit-Vektor");
}

// Der Fall, auf den es im Betrieb ankommt: Die Datei wird in Stuecken
// geschrieben, nie am Stueck. Das Ergebnis muss dasselbe sein.
void testStueckweise()
{
    const std::string text =
        "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq";
    const std::string erwartet = hashOf(text);

    for (const std::size_t stueck : {std::size_t(1), std::size_t(7),
                                     std::size_t(31), std::size_t(64)}) {
        Sha256 h;
        for (std::size_t i = 0; i < text.size(); i += stueck) {
            const std::size_t n =
                (i + stueck <= text.size()) ? stueck : text.size() - i;
            h.update(text.data() + i, n);
        }
        check(h.hexDigest() == erwartet,
              "stueckweise in " + std::to_string(stueck) + "-Byte-Haeppchen");
    }
}

// Ueber die Blockgrenze hinaus und ueber 2^29 Bit Laengenfeld hinweg waere zu
// teuer; eine Million 'a' ist der uebliche Kompromiss und deckt beides ab, was
// hier schiefgehen kann: die Laengenzaehlung und der Uebergang zwischen
// Puffer und Direktverarbeitung.
void testEineMillion()
{
    Sha256 h;
    const std::vector<char> block(1000, 'a');
    for (int i = 0; i < 1000; ++i) h.update(block.data(), block.size());
    check(h.hexDigest() ==
              "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0",
          "eine Million 'a'");
}

void testReset()
{
    Sha256 h;
    h.update("abc", 3);
    (void)h.hexDigest();
    h.reset();
    h.update("abc", 3);
    check(h.hexDigest() ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "reset() macht das Objekt wieder brauchbar");
}

}  // namespace

int main()
{
    testVektoren();
    testStueckweise();
    testEineMillion();
    testReset();

    if (g_failures > 0) {
        std::cerr << g_failures << " Pruefung(en) fehlgeschlagen\n";
        return 1;
    }
    std::cout << "sha256: alle Pruefungen bestanden\n";
    return 0;
}
