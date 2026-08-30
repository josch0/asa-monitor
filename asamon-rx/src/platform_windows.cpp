// SPDX-License-Identifier: GPL-3.0-or-later
//
// Die Windows-Seite von platform.h.
//
// Der Grund, warum es diese Datei gibt: **stdin kennt kein poll().** Was zu tun
// ist, haengt daran, was an stdin haengt — eine Pipe (der Regelfall,
// asamon-node), eine umgeleitete Datei oder eine Konsole.
//
// Bis Patch 3 des welle.io-Forks stand daneben die MSC-Leitung als Named Pipe,
// mit ueberlappter E/A, weil dort der *Leser* auf den Schreiber wartet und
// dieses Warten abbrechbar bleiben musste. Sie ist entfallen; der rohe
// MSC-Strom kommt jetzt als Rueckruf herein (src/recorder.h).

#include "platform.h"

#if defined(_WIN32)

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>

#include <string>

namespace asamon {

namespace {

std::atomic<bool>* g_shutdownFlag = nullptr;

BOOL WINAPI onConsoleCtrl(DWORD type)
{
    switch (type) {
    case CTRL_C_EVENT:
    case CTRL_BREAK_EVENT:
    case CTRL_CLOSE_EVENT:
    case CTRL_LOGOFF_EVENT:
    case CTRL_SHUTDOWN_EVENT:
        if (g_shutdownFlag != nullptr) {
            g_shutdownFlag->store(true, std::memory_order_relaxed);
        }
        return TRUE;
    default:
        return FALSE;
    }
}

// Die Fehlermeldung des Systems, ohne den angehaengten Zeilenumbruch.
std::string lastErrorText(DWORD code)
{
    char* text = nullptr;
    const DWORD len = FormatMessageA(
        FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM |
            FORMAT_MESSAGE_IGNORE_INSERTS,
        nullptr, code, MAKELANGID(LANG_NEUTRAL, SUBLANG_DEFAULT),
        reinterpret_cast<char*>(&text), 0, nullptr);
    std::string out;
    if (len != 0 && text != nullptr) {
        out.assign(text, len);
        while (!out.empty() && (out.back() == '\n' || out.back() == '\r')) {
            out.pop_back();
        }
    }
    else {
        out = "Fehler " + std::to_string(static_cast<unsigned long>(code));
    }
    if (text != nullptr) LocalFree(text);
    return out;
}

std::string lastErrorText()
{
    return lastErrorText(GetLastError());
}

// Was an stdin haengt. Bestimmt einmal beim Anlegen, nicht bei jedem Lesen.
enum StdinKind { kUnknown = 0, kPipe, kFile, kConsole };

}  // namespace

void installShutdownHandler(std::atomic<bool>& flag)
{
    g_shutdownFlag = &flag;
    SetConsoleCtrlHandler(onConsoleCtrl, TRUE);
    // Ein Gegenstueck zu SIGPIPE gibt es nicht: Schreibt der Writer in eine
    // Pipe, deren Leser weg ist, meldet WriteFile schlicht einen Fehler. Der
    // Prozess stirbt daran nicht, und genau das war unter Unix der Zweck des
    // SIG_IGN.
}

StdinReader::StdinReader()
{
    const HANDLE h = GetStdHandle(STD_INPUT_HANDLE);
    if (h == INVALID_HANDLE_VALUE || h == nullptr) {
        kind_ = kUnknown;
        return;
    }
    switch (GetFileType(h)) {
    case FILE_TYPE_PIPE: kind_ = kPipe; break;
    case FILE_TYPE_DISK: kind_ = kFile; break;
    case FILE_TYPE_CHAR: kind_ = kConsole; break;
    default: kind_ = kUnknown; break;
    }
}

long StdinReader::readWithTimeout(void* buffer, std::size_t size, int timeoutMs,
                                  std::string& error)
{
    const HANDLE h = GetStdHandle(STD_INPUT_HANDLE);
    if (h == INVALID_HANDLE_VALUE || h == nullptr || kind_ == kUnknown) {
        // Kein brauchbares stdin — etwa als Dienst ohne Konsole gestartet.
        // Das ist kein Fehler; es kommen nur nie Kommandos.
        Sleep(static_cast<DWORD>(timeoutMs));
        return kReadTimeout;
    }

    if (kind_ == kPipe) {
        // Bei einer Pipe sagt PeekNamedPipe, ob etwas anliegt, ohne zu
        // blockieren. Ein Wartezustand auf das Handle selbst hilft hier nicht:
        // Pipes werden nicht signalisiert, wenn Daten eintreffen.
        DWORD available = 0;
        if (!PeekNamedPipe(h, nullptr, 0, nullptr, &available, nullptr)) {
            const DWORD code = GetLastError();
            if (code == ERROR_BROKEN_PIPE || code == ERROR_PIPE_NOT_CONNECTED) {
                return kReadClosed;
            }
            error = "stdin: PeekNamedPipe fehlgeschlagen: " + lastErrorText(code);
            return kReadFailed;
        }
        if (available == 0) {
            Sleep(static_cast<DWORD>(timeoutMs));
            return kReadTimeout;
        }
    }
    else if (kind_ == kConsole) {
        // Auf einer Konsole ist das Handle wartbar. Es meldet sich auch bei
        // Ereignissen, die keine Eingabe sind (Maus, Fokus); ReadFile wartet
        // dann bis zur naechsten Eingabetaste. Das verzoegert im schlimmsten
        // Fall ein Herunterfahren von Hand — im Betrieb haengt an stdin immer
        // eine Pipe.
        const DWORD waited = WaitForSingleObject(h, static_cast<DWORD>(timeoutMs));
        if (waited == WAIT_TIMEOUT) return kReadTimeout;
        if (waited != WAIT_OBJECT_0) {
            error = "stdin: WaitForSingleObject fehlgeschlagen: " + lastErrorText();
            return kReadFailed;
        }
    }
    // kFile: eine umgeleitete Datei ist immer lesbar; ReadFile liefert am Ende 0.

    DWORD got = 0;
    if (!ReadFile(h, buffer, static_cast<DWORD>(size), &got, nullptr)) {
        const DWORD code = GetLastError();
        if (code == ERROR_BROKEN_PIPE || code == ERROR_HANDLE_EOF) return kReadClosed;
        error = "stdin: ReadFile fehlgeschlagen: " + lastErrorText(code);
        return kReadFailed;
    }
    if (got == 0) return kReadClosed;
    return static_cast<long>(got);
}

}  // namespace asamon

#endif  // _WIN32
