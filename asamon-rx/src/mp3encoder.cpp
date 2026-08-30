// SPDX-License-Identifier: GPL-3.0-or-later

#include "mp3encoder.h"

#ifdef ASAMON_HAVE_LAME
#  include <lame/lame.h>
#endif

namespace asamon {

#ifdef ASAMON_HAVE_LAME

namespace {

// LAME verlangt fuer lame_encode_buffer_interleaved einen Ausgabepuffer von
// mindestens 1,25 * Rahmen + 7200 Byte.
std::size_t ausgabeGroesse(std::size_t frames)
{
    return static_cast<std::size_t>(1.25 * static_cast<double>(frames)) + 7200;
}

}  // namespace

bool Mp3Encoder::verfuegbar() { return true; }

Mp3Encoder::~Mp3Encoder()
{
    if (flags_ != nullptr) {
        lame_close(flags_);
        flags_ = nullptr;
    }
}

bool Mp3Encoder::starte(const std::string& verzeichnis, const std::string& name,
                        int abtastrate, int kanaele, int bitrateKbps,
                        std::string& fehler)
{
    if (gestartet_) {
        fehler = "Encoder laeuft bereits";
        return false;
    }
    if (abtastrate <= 0 || kanaele <= 0 || kanaele > 2) {
        fehler = "unbrauchbare Audioparameter von welle.io";
        return false;
    }

    flags_ = lame_init();
    if (flags_ == nullptr) {
        fehler = "lame_init() fehlgeschlagen";
        return false;
    }

    lame_set_in_samplerate(flags_, abtastrate);
    lame_set_num_channels(flags_, kanaele);
    // Joint Stereo: welle.io hebt Mono-Programme auf zwei gleiche Kanaele an
    // (decoder_adapter.cpp, "upmix to stereo"). Mid/Side kostet dafuer
    // praktisch nichts, und wir sparen uns, den Formatstring zu deuten.
    lame_set_mode(flags_, kanaele == 1 ? MONO : JOINT_STEREO);
    lame_set_brate(flags_, bitrateKbps);
    lame_set_quality(flags_, 2);
    // Kein Xing/LAME-Kopf: Der Strom soll auch dann brauchbar sein, wenn die
    // Datei nach einem Absturz nur bis zur Haelfte kommt und niemand mehr
    // zurueckspringt, um den Kopf nachzutragen.
    lame_set_bWriteVbrTag(flags_, 0);

    if (lame_init_params(flags_) < 0) {
        fehler = "lame_init_params() lehnt die Parameter ab";
        lame_close(flags_);
        flags_ = nullptr;
        return false;
    }

    if (!senke_.oeffne(verzeichnis, name, fehler)) {
        lame_close(flags_);
        flags_ = nullptr;
        return false;
    }

    abtastrate_ = abtastrate;
    kanaele_ = kanaele;
    bitrate_ = bitrateKbps;
    gestartet_ = true;
    return true;
}

void Mp3Encoder::schreibe(const std::int16_t* interleaved, std::size_t frames)
{
    if (!gestartet_ || interleaved == nullptr || frames == 0) return;

    ausgabe_.resize(ausgabeGroesse(frames));
    int n = 0;
    if (kanaele_ == 2) {
        n = lame_encode_buffer_interleaved(
            flags_, const_cast<short*>(reinterpret_cast<const short*>(interleaved)),
            static_cast<int>(frames), ausgabe_.data(),
            static_cast<int>(ausgabe_.size()));
    }
    else {
        n = lame_encode_buffer(
            flags_, reinterpret_cast<const short*>(interleaved), nullptr,
            static_cast<int>(frames), ausgabe_.data(),
            static_cast<int>(ausgabe_.size()));
    }
    if (n > 0) senke_.schreibe(ausgabe_.data(), static_cast<std::size_t>(n));
}

bool Mp3Encoder::schliesse(std::string& fehler)
{
    if (!gestartet_) {
        fehler = "Encoder laeuft nicht";
        return false;
    }

    ausgabe_.resize(7200);
    const int n = lame_encode_flush(flags_, ausgabe_.data(),
                                    static_cast<int>(ausgabe_.size()));
    if (n > 0) senke_.schreibe(ausgabe_.data(), static_cast<std::size_t>(n));

    lame_close(flags_);
    flags_ = nullptr;
    gestartet_ = false;

    return senke_.schliesseUndBenenneUm(fehler);
}

void Mp3Encoder::verwirf()
{
    if (flags_ != nullptr) {
        lame_close(flags_);
        flags_ = nullptr;
    }
    gestartet_ = false;
    senke_.verwirf();
}

#else  // ohne LAME gebaut

bool Mp3Encoder::verfuegbar() { return false; }

Mp3Encoder::~Mp3Encoder() = default;

bool Mp3Encoder::starte(const std::string&, const std::string&, int, int, int,
                        std::string& fehler)
{
    fehler = "ohne LAME gebaut (cmake -DMP3=ON und libmp3lame-dev)";
    return false;
}

void Mp3Encoder::schreibe(const std::int16_t*, std::size_t) {}

bool Mp3Encoder::schliesse(std::string& fehler)
{
    fehler = "ohne LAME gebaut";
    return false;
}

void Mp3Encoder::verwirf() { senke_.verwirf(); }

#endif

}  // namespace asamon
