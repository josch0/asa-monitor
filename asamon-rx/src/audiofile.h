// SPDX-License-Identifier: GPL-3.0-or-later
//
// Eine Datei, die waehrend des Schreibens `.part` heisst.
//
// Der Grund ist die Prozessgrenze: asamon-node erfaehrt von einer Aufnahme
// erst durch den abschliessenden aud-Record. Stirbt asamon-rx mittendrin,
// bleibt eine Datei liegen, von der niemand weiss — deshalb traegt sie bis zum
// Abschluss die Endung `.part`. Damit gilt beides:
//
//   * jede Datei ohne `.part` ist vollstaendig und in einem aud-Record genannt,
//   * jede `.part`-Datei ist erkennbar eine Waise und darf aufgeraeumt werden.
//
// Groesse und SHA-256 entstehen beim Schreiben. Die Datei wird nie ein zweites
// Mal gelesen — weder hier noch im Knoten.

#pragma once

#include "sha256.h"

#include <cstdint>
#include <cstdio>
#include <string>

namespace asamon {

class DateiSenke {
public:
    DateiSenke() = default;
    ~DateiSenke();

    DateiSenke(const DateiSenke&) = delete;
    DateiSenke& operator=(const DateiSenke&) = delete;

    // Legt `verzeichnis` an, falls noetig, und oeffnet `verzeichnis/name.part`.
    bool oeffne(const std::string& verzeichnis, const std::string& name,
                std::string& fehler);

    bool offen() const { return datei_ != nullptr; }

    // Laeuft auf dem Decoder-Thread von welle.io. Blockierende E/A ist hier
    // bewusst in Kauf genommen: Ein voller Schreibpuffer verzoegert allenfalls
    // die Audiodekodierung dieses einen Subchannels, waehrend FIC und damit der
    // ASA-Pfad auf einem anderen Thread laufen. Gepuffert wird mit 64 kB, damit
    // der Kernel selten gerufen wird.
    void schreibe(const void* daten, std::size_t len);

    // Schliesst und benennt `.part` auf den endgueltigen Namen um. Danach ist
    // die Senke verbraucht.
    bool schliesseUndBenenneUm(std::string& fehler);

    // Schliesst und loescht die `.part`-Datei — fuer den Fall, dass gar nichts
    // geschrieben wurde.
    void verwirf();

    std::uint64_t bytes() const { return bytes_; }

    // Gueltig ab dem ersten schliesseUndBenenneUm(); vorher leer.
    const std::string& sha256() const { return sha256_; }
    const std::string& name() const { return name_; }

private:
    std::FILE*    datei_ = nullptr;
    std::string   name_;         // endgueltiger Name, ohne Verzeichnis
    std::string   zielPfad_;
    std::string   teilPfad_;
    std::uint64_t bytes_ = 0;
    Sha256        hash_;
    std::string   sha256_;
    bool          schreibFehler_ = false;
};

}  // namespace asamon
