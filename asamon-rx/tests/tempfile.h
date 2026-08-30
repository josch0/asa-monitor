// SPDX-License-Identifier: GPL-3.0-or-later
//
// Eine temporaere Datei, die auf beiden Plattformen entsteht.
//
// std::tmpfile() ist dafuer nicht zu gebrauchen: Unter Windows legt die
// C-Laufzeit die Datei im **Wurzelverzeichnis des aktuellen Laufwerks** an, und
// das ist ohne erhoehte Rechte nicht beschreibbar — der Aufruf liefert dann
// stillschweigend nullptr. Unter Unix funktioniert er, aber ein zweiter Weg
// nur fuer Windows waere in den Tests eine Fallunterscheidung mehr.

#pragma once

#include <cstdio>
#include <cstdlib>
#include <string>

namespace asamon::test {

// Legt eine Datei zum Lesen und Schreiben im Temporaerverzeichnis an.
// Zurueck kommt nullptr, wenn das nicht gelingt.
class TempFile {
public:
    explicit TempFile(const std::string& name)
    {
        // Durchprobieren statt raten: In einer MSYS2-Shell steht in TMPDIR ein
        // Unix-Pfad ("/tmp"), mit dem die native Windows-Laufzeit nichts
        // anfangen kann. Welches Verzeichnis taugt, zeigt sich erst beim
        // Oeffnen — das aktuelle Verzeichnis ist der letzte Ausweg und
        // funktioniert ueberall.
        for (const char* variable : {"TEMP", "TMP", "TMPDIR"}) {
            const char* wert = std::getenv(variable);
            if (wert == nullptr || wert[0] == '\0') continue;
            if (versuche(std::string(wert) + "/asamon-rx-test-" + name)) return;
        }
        versuche("asamon-rx-test-" + name);
    }

    ~TempFile()
    {
        if (file_ != nullptr) std::fclose(file_);
        if (!path_.empty()) std::remove(path_.c_str());
    }

    TempFile(const TempFile&) = delete;
    TempFile& operator=(const TempFile&) = delete;

    std::FILE* get() const { return file_; }
    const std::string& path() const { return path_; }

private:
    bool versuche(const std::string& pfad)
    {
        std::FILE* f = std::fopen(pfad.c_str(), "w+b");
        if (f == nullptr) return false;
        path_ = pfad;
        file_ = f;
        return true;
    }

    std::string path_;
    std::FILE* file_ = nullptr;
};

}  // namespace asamon::test
