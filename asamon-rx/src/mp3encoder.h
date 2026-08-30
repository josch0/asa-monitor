// SPDX-License-Identifier: GPL-3.0-or-later
//
// PCM aus welle.io nach MP3, direkt in eine Datei.
//
// Warum ueberhaupt hier und nicht im Knoten: Der teure Teil — HE-AAC v2 nach
// PCM — laeuft in asamon-rx ohnehin. welle.io dekodiert jeden zugeschalteten
// Subchannel und reicht das Ergebnis ueber ProgrammeHandlerInterface::
// onNewAudio() heraus; bis zum 30.08.2026 war dieser Rueckruf leer, das fertige
// PCM wurde also weggeworfen. Zu einer abspielbaren Datei fehlt damit nur der
// Encoder, und LAME reiht sich in die vier C-Bibliotheken ein, gegen die
// asamon-rx ohnehin linkt. asamon-node bleibt frei davon und damit statisch
// und cross-baubar (CGO_ENABLED=0).
//
// Warum MP3 und nicht das Naheliegende: Die AAC-Rahmen liessen sich ohne
// Codec in einen MP4-Container umpacken — aber DAB+ verwendet die 960er
// Transformation ("the only way to select 960 transform here", sagt welle.ios
// dabplus_decoder.cpp dazu), und ob der Decoder eines Browsers die kennt, ist
// von der Plattform abhaengig. MP3 kennt jeder, ist bei diesen Bitraten
// gleich gross und macht die Frage gegenstandslos.

#pragma once

#include "audiofile.h"

#include <cstdint>
#include <string>
#include <vector>

// LAME wird nur in der .cpp eingebunden.
struct lame_global_struct;

namespace asamon {

class Mp3Encoder {
public:
    Mp3Encoder() = default;
    ~Mp3Encoder();

    Mp3Encoder(const Mp3Encoder&) = delete;
    Mp3Encoder& operator=(const Mp3Encoder&) = delete;

    // true, wenn dieser Bau ueberhaupt MP3 kann (CMake: ASAMON_HAVE_LAME).
    static bool verfuegbar();

    // Wird beim ersten onNewAudio() gerufen: Erst dann stehen Abtastrate und
    // Kanalzahl fest — sie kommen aus dem Superframe-Kopf, nicht aus dem FIC.
    bool starte(const std::string& verzeichnis, const std::string& name,
                int abtastrate, int kanaele, int bitrateKbps, std::string& fehler);

    bool laeuft() const { return gestartet_; }

    // `interleaved` enthaelt `frames` Rahmen zu `kanaele` Abtastwerten.
    void schreibe(const std::int16_t* interleaved, std::size_t frames);

    // Spuelt den Encoder und benennt die Datei um.
    bool schliesse(std::string& fehler);

    // Bricht ab und loescht die angefangene Datei.
    void verwirf();

    const DateiSenke& datei() const { return senke_; }
    int abtastrate() const { return abtastrate_; }
    int kanaele() const { return kanaele_; }
    int bitrate() const { return bitrate_; }

private:
    lame_global_struct* flags_ = nullptr;
    DateiSenke senke_;
    std::vector<unsigned char> ausgabe_;
    bool gestartet_ = false;
    int  abtastrate_ = 0;
    int  kanaele_ = 0;
    int  bitrate_ = 0;
};

}  // namespace asamon
