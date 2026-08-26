// SPDX-License-Identifier: GPL-3.0-or-later

#include "notify.h"

#include <cstdlib>
#include <cstring>
#include <string>

#if defined(__unix__)
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#endif

namespace asamon {

namespace {

#if defined(__unix__)

void sendState(const char* state)
{
    const char* socketPath = std::getenv("NOTIFY_SOCKET");
    if (socketPath == nullptr || socketPath[0] == '\0') return;

    const int fd = ::socket(AF_UNIX, SOCK_DGRAM | SOCK_CLOEXEC, 0);
    if (fd < 0) return;

    sockaddr_un addr{};
    addr.sun_family = AF_UNIX;

    const size_t pathLen = std::strlen(socketPath);
    if (pathLen >= sizeof(addr.sun_path)) {
        ::close(fd);
        return;
    }
    std::memcpy(addr.sun_path, socketPath, pathLen);
    // Ein fuehrendes '@' bezeichnet eine abstrakte Adresse; dort steht ein
    // Nullbyte statt des Zeichens.
    if (addr.sun_path[0] == '@') addr.sun_path[0] = '\0';

    const socklen_t addrLen =
        static_cast<socklen_t>(offsetof(sockaddr_un, sun_path) + pathLen);
    ::sendto(fd, state, std::strlen(state), MSG_NOSIGNAL,
             reinterpret_cast<sockaddr*>(&addr), addrLen);
    ::close(fd);
}

#else

void sendState(const char*) {}

#endif

}  // namespace

void notifyReady()    { sendState("READY=1"); }
void notifyWatchdog() { sendState("WATCHDOG=1"); }
void notifyStopping() { sendState("STOPPING=1"); }

unsigned watchdogIntervalSeconds()
{
    const char* usec = std::getenv("WATCHDOG_USEC");
    if (usec == nullptr || usec[0] == '\0') return 0;
    try {
        const unsigned long long micros = std::stoull(usec);
        // systemd erwartet den Tick deutlich vor Ablauf; die Haelfte ist der
        // uebliche Wert.
        const unsigned long long half = micros / 2 / 1000000ULL;
        return half > 0 ? static_cast<unsigned>(half) : 1u;
    } catch (const std::exception&) {
        return 0;
    }
}

}  // namespace asamon
