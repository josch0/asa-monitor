// SPDX-License-Identifier: GPL-3.0-or-later

package supervisor

import (
	"net"
	"os"
	"strings"
	"time"
)

// sd_notify ohne Fremdabhängigkeit.
//
// Type=notify und WatchdogSec brauchen nichts weiter als einen Datagramm-Socket
// im Unix-Namensraum. Zwei Dutzend Zeilen sind billiger als eine Abhängigkeit,
// die den Rest ihres Ökosystems mitbringt.

// Notify schickt eine Zustandsmeldung an systemd. Ohne NOTIFY_SOCKET — also
// beim Start von Hand — geschieht nichts.
func Notify(zustand string) {
	pfad := os.Getenv("NOTIFY_SOCKET")
	if pfad == "" {
		return
	}
	// Ein führendes '@' bezeichnet den abstrakten Namensraum; Go erwartet
	// dafür einen Nullbyte-Präfix.
	if strings.HasPrefix(pfad, "@") {
		pfad = "\x00" + pfad[1:]
	}
	conn, err := net.DialTimeout("unixgram", pfad, time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(zustand))
}

// NotifyBereit meldet, dass der Knoten läuft. Erst danach gilt die Unit als
// gestartet.
func NotifyBereit() { Notify("READY=1") }

// NotifyLebenszeichen bedient den Watchdog.
//
// Er wird nur bedient, solange der Reporter läuft: Ein hängender Reporter soll
// einen Neustart auslösen — genau dafür ist der Watchdog da.
func NotifyLebenszeichen() { Notify("WATCHDOG=1") }

// NotifyBeendet meldet das Herunterfahren.
func NotifyBeendet() { Notify("STOPPING=1") }

// NotifyStatus setzt die Statuszeile, die `systemctl status` anzeigt.
func NotifyStatus(text string) { Notify("STATUS=" + text) }

// WatchdogTakt gibt den Abstand, in dem der Watchdog bedient werden sollte:
// die halbe WatchdogSec aus der Umgebung. Ohne Angabe kommt 0 zurück.
func WatchdogTakt() time.Duration {
	v := os.Getenv("WATCHDOG_USEC")
	if v == "" {
		return 0
	}
	var usec int64
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0
		}
		usec = usec*10 + int64(v[i]-'0')
	}
	if usec <= 0 {
		return 0
	}
	return time.Duration(usec) * time.Microsecond / 2
}
