// SPDX-License-Identifier: GPL-3.0-or-later
//
// Minimale sd_notify-Anbindung fuer den Betrieb unter systemd.
//
// Bewusst ohne libsystemd: das Protokoll ist eine Zeile in ein
// AF_UNIX-Datagramm, und eine Bauabhaengigkeit fuer drei Zeilen waere auf
// einem Knoten in fremder Hand der schlechtere Tausch. Ohne NOTIFY_SOCKET in
// der Umgebung tun alle Funktionen nichts.

#pragma once

namespace asamon {

void notifyReady();
void notifyWatchdog();
void notifyStopping();

// Watchdog-Intervall aus WATCHDOG_USEC, in Sekunden; 0, wenn nicht gesetzt.
// Getickt wird mit der halben Zeit, so will es systemd.
unsigned watchdogIntervalSeconds();

}  // namespace asamon
