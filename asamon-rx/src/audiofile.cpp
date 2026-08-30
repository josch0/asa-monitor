// SPDX-License-Identifier: GPL-3.0-or-later

#include "audiofile.h"

#include <cstdio>
#include <filesystem>
#include <system_error>
#include <vector>

namespace asamon {

namespace {

// 64 kB Schreibpuffer. Auf einer SD-Karte ist jeder Kernelaufruf teuer, und
// dieser Weg laeuft auf dem Decoder-Thread von welle.io.
constexpr std::size_t kSchreibPuffer = 64 * 1024;

}  // namespace

DateiSenke::~DateiSenke()
{
    if (datei_ != nullptr) verwirf();
}

bool DateiSenke::oeffne(const std::string& verzeichnis, const std::string& name,
                        std::string& fehler)
{
    std::error_code ec;
    if (!verzeichnis.empty()) {
        std::filesystem::create_directories(verzeichnis, ec);
        if (ec) {
            fehler = "Verzeichnis " + verzeichnis + " nicht anlegbar: " + ec.message();
            return false;
        }
    }

    const std::filesystem::path basis =
        verzeichnis.empty() ? std::filesystem::path(name)
                            : std::filesystem::path(verzeichnis) / name;
    zielPfad_ = basis.string();
    teilPfad_ = zielPfad_ + ".part";

    datei_ = std::fopen(teilPfad_.c_str(), "wb");
    if (datei_ == nullptr) {
        fehler = "kann " + teilPfad_ + " nicht schreiben";
        zielPfad_.clear();
        teilPfad_.clear();
        return false;
    }
    std::setvbuf(datei_, nullptr, _IOFBF, kSchreibPuffer);

    name_ = name;
    bytes_ = 0;
    sha256_.clear();
    hash_.reset();
    schreibFehler_ = false;
    return true;
}

void DateiSenke::schreibe(const void* daten, std::size_t len)
{
    if (datei_ == nullptr || len == 0) return;

    const std::size_t geschrieben = std::fwrite(daten, 1, len, datei_);
    bytes_ += geschrieben;
    hash_.update(daten, geschrieben);
    if (geschrieben != len) schreibFehler_ = true;
}

bool DateiSenke::schliesseUndBenenneUm(std::string& fehler)
{
    if (datei_ == nullptr) {
        fehler = "Datei ist nicht offen";
        return false;
    }

    const bool fehlerBeimSchliessen = (std::fclose(datei_) != 0);
    datei_ = nullptr;
    sha256_ = hash_.hexDigest();

    if (schreibFehler_ || fehlerBeimSchliessen) {
        fehler = "Schreibfehler in " + teilPfad_;
        return false;
    }

    // Unter Windows scheitert rename(), wenn das Ziel existiert. Ein
    // Restbestand aus einem frueheren Lauf ist kein Grund, die frische
    // Aufnahme zu verlieren.
    std::error_code ec;
    std::filesystem::remove(zielPfad_, ec);
    std::filesystem::rename(teilPfad_, zielPfad_, ec);
    if (ec) {
        fehler = "Umbenennen nach " + zielPfad_ + " fehlgeschlagen: " + ec.message();
        return false;
    }
    return true;
}

void DateiSenke::verwirf()
{
    if (datei_ != nullptr) {
        std::fclose(datei_);
        datei_ = nullptr;
    }
    if (!teilPfad_.empty()) {
        std::error_code ec;
        std::filesystem::remove(teilPfad_, ec);
    }
    bytes_ = 0;
    sha256_.clear();
}

}  // namespace asamon
