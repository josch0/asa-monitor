// SPDX-License-Identifier: GPL-3.0-or-later
//
// Die Unix-Seite von platform.h. Das ist der Weg, der von Anfang an gebaut und
// am Geraet gelaufen ist; die Windows-Seite steht daneben in
// platform_windows.cpp.

#include "platform.h"

#if !defined(_WIN32)

#include <cerrno>
#include <csignal>
#include <cstring>

#include <poll.h>
#include <unistd.h>

namespace asamon {

namespace {

std::atomic<bool>* g_shutdownFlag = nullptr;

// Fester Pfad statt XDG-Suche: asamon-rx laeuft als Kindprozess eines
// Systemdienstes, nicht in einer Benutzersitzung. /var/lib/asamon ist die
// StateDirectory der systemd-Unit von asamon-node.
constexpr const char* kDefaultAudioDir = "/var/lib/asamon/audio";

extern "C" void onSignal(int)
{
    // Im Signalhandler nur ein Flag setzen — alles Weitere macht die
    // Hauptschleife.
    if (g_shutdownFlag != nullptr) {
        g_shutdownFlag->store(true, std::memory_order_relaxed);
    }
}

std::string errnoText()
{
    return std::string(std::strerror(errno));
}

// poll() auf einen Deskriptor, mit Zeitscheibe. Seit Patch 3 des
// welle.io-Forks braucht das nur noch stdin — die MSC-Leitung, die frueher
// denselben Weg ging, ist einem Rueckruf gewichen (src/recorder.h).
long readFdWithTimeout(int fd, void* buffer, std::size_t size, int timeoutMs,
                       std::string& error)
{
    pollfd pfd{};
    pfd.fd = fd;
    pfd.events = POLLIN;

    const int ready = ::poll(&pfd, 1, timeoutMs);
    if (ready < 0) {
        if (errno == EINTR) return kReadTimeout;
        error = "poll fehlgeschlagen: " + errnoText();
        return kReadFailed;
    }
    if (ready == 0) return kReadTimeout;

    const ssize_t got = ::read(fd, buffer, size);
    if (got < 0) {
        if (errno == EAGAIN || errno == EINTR) return kReadTimeout;
        error = "read fehlgeschlagen: " + errnoText();
        return kReadFailed;
    }
    if (got == 0) return kReadClosed;
    return static_cast<long>(got);
}

}  // namespace

std::string defaultAudioDir() { return kDefaultAudioDir; }

void installShutdownHandler(std::atomic<bool>& flag)
{
    g_shutdownFlag = &flag;

    struct sigaction action;
    std::memset(&action, 0, sizeof(action));
    action.sa_handler = onSignal;
    sigaction(SIGINT, &action, nullptr);
    sigaction(SIGTERM, &action, nullptr);

    // Ohne das beendet ein EPIPE den Prozess hart, sobald die Gegenstelle den
    // Strom nicht mehr liest. Der Writer meldet den Fehler stattdessen, und
    // wir raeumen geordnet auf.
    std::signal(SIGPIPE, SIG_IGN);
}

StdinReader::StdinReader() = default;

long StdinReader::readWithTimeout(void* buffer, std::size_t size, int timeoutMs,
                                  std::string& error)
{
    return readFdWithTimeout(STDIN_FILENO, buffer, size, timeoutMs, error);
}

}  // namespace asamon

#endif  // !_WIN32
