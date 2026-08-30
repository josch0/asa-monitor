#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Baut ein .deb aus bereits übersetzten Binaries.
#
# Warum ein Paket und kein Archiv: `asamon-rx` linkt dynamisch gegen FFTW3f,
# FAAD2, mpg123, librtlsdr und LAME. Ein Archiv müsste dem Freiwilligen sagen,
# welche fünf Pakete er vorher installieren soll; `apt` weiß es selbst. Nebenbei
# landen systemd-Unit, udev-Regel und Beispielkonfiguration an ihrem richtigen
# Platz, und die Deinstallation ist gelöst, statt in einer Anleitung zu stehen.
#
# Warum ohne debhelper: dpkg-deb genügt für ein Paket, das nichts übersetzt und
# keine Debian-Richtlinien für den Upload erfüllen muss. Der Bau bleibt damit
# ein Skript, das man ganz liest, statt eines Regelwerks aus sechs Dateien.
#
# Aufruf:
#   packaging/build-deb.sh --version 0.1.0 --arch arm64 \
#       --rx build/asamon-rx --node asamon-node/build/asamon-node \
#       --out dist
#
# Die Architektur ist die von dpkg (arm64, armhf, amd64) und muss zu den
# übergebenen Binaries passen — geprüft wird das hier nicht, das tut die CI
# durch ihren Bauort.

set -eu

version=""
arch=""
rx=""
node=""
out="dist"
quellen=""
basis=""

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="$2"; shift 2 ;;
        --arch)    arch="$2";    shift 2 ;;
        --rx)      rx="$2";      shift 2 ;;
        --node)    node="$2";    shift 2 ;;
        --out)     out="$2";     shift 2 ;;
        --quellen) quellen="$2"; shift 2 ;;
        # --basis benennt die Debian-Fassung, gegen die gebaut wurde (deb12,
        # deb13). Sie steht **nur im Dateinamen**, nicht in der Paketversion:
        # Ein Binary gilt nur aufwärts, und die Bibliotheksnamen unterscheiden
        # sich seit der t64-Umstellung — install.sh sucht deshalb gezielt das
        # Paket zur eigenen Fassung.
        --basis)   basis="$2";   shift 2 ;;
        *) echo "build-deb: unbekanntes Argument $1" >&2; exit 1 ;;
    esac
done

for pflicht in version arch rx node; do
    eval "wert=\$$pflicht"
    [ -n "$wert" ] || { echo "build-deb: --$pflicht fehlt" >&2; exit 1; }
done
[ -f "$rx" ]   || { echo "build-deb: $rx nicht gefunden" >&2; exit 1; }
[ -f "$node" ] || { echo "build-deb: $node nicht gefunden" >&2; exit 1; }

hier=$(cd "$(dirname "$0")" && pwd)
wurzel=$(cd "$hier/.." && pwd)
bau=$(mktemp -d)
trap 'rm -rf "$bau"' EXIT

if [ -n "$basis" ]; then
    paket="asamon_${version}_${basis}_${arch}"
else
    paket="asamon_${version}_${arch}"
fi
wz="$bau/$paket"

# --- Dateibaum ---------------------------------------------------------------
mkdir -p "$wz/DEBIAN" \
         "$wz/usr/bin" \
         "$wz/lib/systemd/system" \
         "$wz/lib/udev/rules.d" \
         "$wz/etc/asamon" \
         "$wz/etc/modprobe.d" \
         "$wz/usr/share/doc/asamon"

install -m 0755 "$rx"   "$wz/usr/bin/asamon-rx"
install -m 0755 "$node" "$wz/usr/bin/asamon-node"
# Die Unit in contrib/ zeigt auf /usr/local/bin — richtig für alle, die selbst
# bauen und mit `make install` installieren. Ein Paket gehört nach /usr/bin,
# also wird der Pfad hier umgeschrieben statt in contrib/ zweideutig zu werden.
sed 's|/usr/local/bin/asamon-node|/usr/bin/asamon-node|' \
    "$wurzel/asamon-node/contrib/asamon-node.service" \
    > "$wz/lib/systemd/system/asamon-node.service"
chmod 0644 "$wz/lib/systemd/system/asamon-node.service"
grep -q 'ExecStart=/usr/bin/asamon-node' "$wz/lib/systemd/system/asamon-node.service" || {
    echo "build-deb: ExecStart in der Unit ließ sich nicht umschreiben" >&2
    exit 1
}
install -m 0644 "$wurzel/asamon-rx/contrib/99-asamon-rtlsdr.rules" \
                "$wz/lib/udev/rules.d/99-asamon-rtlsdr.rules"
install -m 0644 "$wurzel/asamon-node/contrib/node-config.example.yaml" \
                "$wz/etc/asamon/node-config.example.yaml"
install -m 0644 "$wurzel/asamon-node/LICENSE" "$wz/usr/share/doc/asamon/copyright"
[ -n "$quellen" ] && install -m 0644 "$quellen" "$wz/usr/share/doc/asamon/QUELLEN.txt"

# Der Kernel greift sich den Stick sonst als DVB-T-Empfänger. Das ist eine
# conffile: Wer sie ändert, soll seine Fassung behalten dürfen.
cat > "$wz/etc/modprobe.d/asamon-blacklist-rtl.conf" <<'EOF'
# Von asamon: Ohne dieses Blacklisting belegt der DVB-T-Treiber den RTL-SDR-Stick,
# und librtlsdr kommt nicht mehr an ihn heran.
blacklist dvb_usb_rtl28xxu
EOF
chmod 0644 "$wz/etc/modprobe.d/asamon-blacklist-rtl.conf"

# --- Abhängigkeiten ----------------------------------------------------------
# dpkg-shlibdeps liest sie aus dem Binary statt sie zu raten: Welche Pakete
# libwelle nach sich zieht, hängt an den Bauoptionen und ändert sich, sobald
# jemand eine Option umlegt.
deps="libc6"
if command -v dpkg-shlibdeps >/dev/null 2>&1; then
    mkdir -p "$bau/shlibs/debian"
    : > "$bau/shlibs/debian/control"
    if ( cd "$bau/shlibs" && dpkg-shlibdeps -O --ignore-missing-info \
            "$wz/usr/bin/asamon-rx" 2>/dev/null ) > "$bau/deps.txt"; then
        ermittelt=$(sed -n 's/^shlibs:Depends=//p' "$bau/deps.txt")
        [ -n "$ermittelt" ] && deps="$ermittelt"
    fi
fi
# adduser für den Systemnutzer im postinst; die udev-Regel braucht plugdev,
# das aus base-passwd kommt und ohnehin da ist.
deps="$deps, adduser"

groesse=$(du -ks "$wz" | cut -f1)

cat > "$wz/DEBIAN/control" <<EOF
Package: asamon
Version: $version
Section: hamradio
Priority: optional
Architecture: $arch
Depends: $deps
Recommends: chrony | systemd-timesyncd
Suggests: rtl-sdr, jq
Installed-Size: $groesse
Maintainer: josch0 <root@josch0.dev>
Homepage: https://github.com/josch0/asa-monitor
Description: ASA/EWS-Monitor für DAB+ (Empfangsprozess und Knoten)
 Empfängt DAB+ mit einem RTL-SDR-Stick, wertet die ASA/EWS-Signalisierung
 nach ETSI TS 104 089 (FIG 0/15) aus und meldet die Beobachtungen an einen
 zentralen Server.
 .
 Das Paket enthält beide Programme: asamon-rx (Empfang und FIG-0/15-Parser,
 gebaut gegen einen welle.io-Fork) und asamon-node (Deutung, Zustand, Uplink).
 Gestartet wird nur asamon-node; die Empfangsprozesse startet es selbst, je
 überwachtem Kanal einen.
 .
 Nach der Installation muss /etc/asamon/node-config.yaml angelegt werden;
 asamon-node --check prüft sie. Der Dienst wird erst danach gestartet.
EOF

cat > "$wz/DEBIAN/conffiles" <<'EOF'
/etc/modprobe.d/asamon-blacklist-rtl.conf
EOF

# --- postinst ----------------------------------------------------------------
# Was hier **nicht** passiert: den Dienst starten. Ohne node-config.yaml hätte
# er keinen Standort und keinen Server, und ein Dienst, der im Sekundentakt
# scheitert, ist schlimmer als einer, der wartet. install.sh startet ihn, wenn
# die Konfiguration steht; bei einem Update läuft er von selbst weiter.
cat > "$wz/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e

case "$1" in
    configure)
        if ! getent passwd asamon >/dev/null; then
            adduser --system --group --home /var/lib/asamon \
                    --shell /usr/sbin/nologin --quiet asamon
        fi
        # Zugriff auf den Stick: Die udev-Regel vergibt die Gruppe plugdev.
        if getent group plugdev >/dev/null; then
            adduser --quiet asamon plugdev || true
        fi

        mkdir -p /var/lib/asamon
        chown asamon:asamon /var/lib/asamon
        chmod 0700 /var/lib/asamon

        # Der DVB-T-Treiber hält den Stick, solange er geladen ist. Das
        # Blacklisting wirkt erst beim nächsten Start — also hier einmal
        # nachhelfen. Schlägt es fehl, weil der Stick gerade benutzt wird, ist
        # das kein Grund, die Installation abzubrechen.
        modprobe -r dvb_usb_rtl28xxu 2>/dev/null || true

        if command -v udevadm >/dev/null 2>&1; then
            udevadm control --reload-rules 2>/dev/null || true
            udevadm trigger --subsystem-match=usb 2>/dev/null || true
        fi
        ;;
esac

#DEBHELPER#

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
    # Bei einem Update den laufenden Dienst übernehmen — aber keinen starten,
    # der vorher nicht lief.
    if systemctl is-active --quiet asamon-node.service; then
        systemctl try-restart asamon-node.service >/dev/null 2>&1 || true
    fi
fi

exit 0
EOF
chmod 0755 "$wz/DEBIAN/postinst"

# --- prerm / postrm ----------------------------------------------------------
cat > "$wz/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e

if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
    systemctl stop asamon-node.service >/dev/null 2>&1 || true
    systemctl disable asamon-node.service >/dev/null 2>&1 || true
fi

exit 0
EOF
chmod 0755 "$wz/DEBIAN/prerm"

# purge löscht auch /var/lib/asamon — und damit den Ed25519-Schlüssel, also die
# Identität des Knotens. Beim bloßen remove bleibt er liegen: Wer neu
# installiert, soll derselbe Knoten bleiben.
cat > "$wz/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e

case "$1" in
    purge)
        rm -rf /var/lib/asamon
        rm -f /etc/asamon/node-config.yaml
        rmdir /etc/asamon 2>/dev/null || true
        if getent passwd asamon >/dev/null; then
            deluser --quiet --system asamon || true
        fi
        ;;
esac

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi

exit 0
EOF
chmod 0755 "$wz/DEBIAN/postrm"

# --- bauen -------------------------------------------------------------------
mkdir -p "$out"
# Root als Eigentümer, damit das Paket unabhängig vom Bauwirt gleich aussieht.
dpkg-deb --root-owner-group --build "$wz" "$out/$paket.deb" >/dev/null
echo "$out/$paket.deb"
