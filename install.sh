#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# ASA-Monitor einrichten oder aktualisieren — auf Raspberry Pi OS und Debian.
#
#   curl -fsSL https://raw.githubusercontent.com/josch0/asa-monitor/main/install.sh | sudo sh
#
# Das Skript lädt das neueste Release, prüft es gegen SHA256SUMS, installiert
# das .deb (apt holt die Bibliotheken selbst), fragt die Konfiguration ab und
# startet den Dienst.
#
# Ist der ASA-Monitor schon installiert, wird **aktualisiert**: neues Paket,
# Konfiguration unangetastet, laufender Dienst übernommen.
#
# Zielplattform ist Raspberry Pi OS. Andere Distributionen sind ausdrücklich
# nicht abgedeckt — das Skript bricht dort ab, statt zu raten. Wer sie braucht,
# baut aus den Quellen (asamon-rx/README.md).
#
# Umgebungsvariablen für den unbeaufsichtigten Betrieb (etwa für zehn Pis):
#
#   ASAMON_NAME=…            Anzeigename des Knotens
#   ASAMON_LOCATION_CODE=…   Standort, über https://asa.radio/ zu ermitteln
#   ASAMON_CHANNELS=5C,11D   Kanäle, komma-getrennt (Vorgabe: 5C)
#   ASAMON_SERVER=…          Server-URL; leer lassen, solange es keinen gibt
#   ASAMON_CONTACT=…         optional
#   ASAMON_ANTENNA=…         optional
#   ASAMON_VERSION=0.1.0     bestimmte Fassung statt der neuesten
#   ASAMON_DEB=/pfad/x.deb   fertiges Paket statt Download (für eigene Bauten)
#   ASAMON_UNATTENDED=1      nicht nachfragen; gleichbedeutend mit --unattended

set -eu

REPO="josch0/asa-monitor"
ROH="https://raw.githubusercontent.com/$REPO"
API="https://api.github.com/repos/$REPO"

: "${ASAMON_NAME:=}"
: "${ASAMON_LOCATION_CODE:=}"
: "${ASAMON_CHANNELS:=}"
: "${ASAMON_SERVER:=}"
: "${ASAMON_CONTACT:=}"
: "${ASAMON_ANTENNA:=}"
: "${ASAMON_VERSION:=}"
: "${ASAMON_DEB:=}"
: "${ASAMON_UNATTENDED:=0}"

KONFIG=/etc/asamon/node-config.yaml
arbeit=""

sagen()  { printf '%s\n' "$*"; }
schritt(){ printf '\n\033[1m==> %s\033[0m\n' "$*"; }
warnen() { printf '\033[33mHinweis:\033[0m %s\n' "$*" >&2; }
ende()   { printf '\033[31mAbbruch:\033[0m %s\n' "$*" >&2; exit 1; }

aufraeumen() { [ -n "$arbeit" ] && rm -rf "$arbeit"; }
trap aufraeumen EXIT

# --- Fragen an den Menschen --------------------------------------------------
#
# Bei `curl … | sh` ist stdin die Skriptquelle, nicht die Tastatur. Gefragt wird
# deshalb über /dev/tty. Fehlt auch das, läuft das Skript unbeaufsichtigt und
# braucht seine Werte aus der Umgebung.
tty_da() {
    [ "$ASAMON_UNATTENDED" = 1 ] && return 1
    [ -r /dev/tty ] && [ -w /dev/tty ]
}

frage() { # frage <Text> <Vorgabe>
    _text="$1"; _vorgabe="$2"; _antwort=""
    if ! tty_da; then
        printf '%s' "$_vorgabe"
        return 0
    fi
    if [ -n "$_vorgabe" ]; then
        printf '%s [%s]: ' "$_text" "$_vorgabe" > /dev/tty
    else
        printf '%s: ' "$_text" > /dev/tty
    fi
    IFS= read -r _antwort < /dev/tty || _antwort=""
    [ -z "$_antwort" ] && _antwort="$_vorgabe"
    printf '%s' "$_antwort"
}

# --- Voraussetzungen ---------------------------------------------------------
pruefe_umgebung() {
    [ "$(id -u)" = 0 ] || ende "bitte als root ausführen (sudo sh install.sh)"

    command -v dpkg >/dev/null 2>&1 || \
        ende "kein dpkg gefunden. Dieses Skript ist für Raspberry Pi OS und Debian."
    command -v apt-get >/dev/null 2>&1 || ende "kein apt-get gefunden."

    for werkzeug in curl sha256sum; do
        command -v "$werkzeug" >/dev/null 2>&1 || fehlend="${fehlend:-} $werkzeug"
    done
    if [ -n "${fehlend:-}" ]; then
        schritt "Fehlende Werkzeuge nachinstallieren:$fehlend"
        apt-get update -qq
        # shellcheck disable=SC2086
        apt-get install -y -qq $fehlend coreutils || ende "Nachinstallation fehlgeschlagen"
    fi

    arch=$(dpkg --print-architecture)
    case "$arch" in
        arm64|armhf|amd64) ;;
        *) ende "Architektur $arch wird nicht ausgeliefert. Bauanleitung: asamon-rx/README.md" ;;
    esac

    codename=""
    [ -r /etc/os-release ] && . /etc/os-release && codename="${VERSION_CODENAME:-}"
    case "$codename" in
        trixie)   basis=deb13 ;;
        bookworm) basis=deb12 ;;
        "")       ende "/etc/os-release nennt keine Version — unbekanntes System." ;;
        *)
            # Ein neueres Debian ist wahrscheinlich abwärtskompatibel; das ist
            # eine Annahme, und sie wird als solche gesagt.
            basis=deb13
            warnen "unbekannte Debian-Fassung \"$codename\" — es wird das Paket für trixie versucht."
            ;;
    esac

    if [ ! -d /run/systemd/system ]; then
        ende "kein systemd — der Autostart des Dienstes setzt es voraus."
    fi
}

installierte_version() {
    dpkg-query -W -f='${Version}' asamon 2>/dev/null || true
}

# --- Release holen -----------------------------------------------------------
neueste_version() {
    if [ -n "$ASAMON_VERSION" ]; then
        printf '%s' "$ASAMON_VERSION"
        return 0
    fi
    # Ohne jq: das Feld tag_name aus der API-Antwort schneiden.
    curl -fsSL "$API/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' \
        | head -1
}

hole_paket() {
    version="$1"
    arbeit=$(mktemp -d)

    if [ -n "$ASAMON_DEB" ]; then
        [ -f "$ASAMON_DEB" ] || ende "$ASAMON_DEB nicht gefunden"
        cp "$ASAMON_DEB" "$arbeit/paket.deb"
        sagen "Lokales Paket: $ASAMON_DEB"
        return 0
    fi

    datei="asamon_${version}_${basis}_${arch}.deb"
    url="https://github.com/$REPO/releases/download/v$version/$datei"

    schritt "Paket $datei herunterladen"
    curl -fSL --progress-bar -o "$arbeit/paket.deb" "$url" \
        || ende "Download fehlgeschlagen: $url"

    # Die Prüfsumme ist kein Zierrat: Das Paket wird gleich als root
    # installiert. Fehlt SHA256SUMS im Release, bricht das Skript ab, statt
    # ungeprüft weiterzumachen.
    schritt "Prüfsumme vergleichen"
    curl -fsSL -o "$arbeit/SHA256SUMS" \
        "https://github.com/$REPO/releases/download/v$version/SHA256SUMS" \
        || ende "SHA256SUMS ließ sich nicht laden — Paket wird nicht installiert."

    soll=$(sed -n "s/^\([0-9a-f]\{64\}\)[[:space:]]*[*]\{0,1\}$datei\$/\1/p" "$arbeit/SHA256SUMS" | head -1)
    [ -n "$soll" ] || ende "SHA256SUMS nennt $datei nicht."
    ist=$(sha256sum "$arbeit/paket.deb" | cut -d' ' -f1)
    [ "$soll" = "$ist" ] || ende "Prüfsumme weicht ab. Erwartet $soll, bekommen $ist."
    sagen "Prüfsumme stimmt."
}

installiere_paket() {
    schritt "Paket installieren"
    # apt statt dpkg: Es holt libfftw3, libfaad2, mpg123, librtlsdr und LAME
    # gleich mit. Genau dafür gibt es das Paket.
    DEBIAN_FRONTEND=noninteractive apt-get install -y "$arbeit/paket.deb" \
        || ende "Installation fehlgeschlagen"
}

# --- Konfiguration -----------------------------------------------------------
konfiguration_anlegen() {
    schritt "Konfiguration"

    if ! tty_da && [ -z "$ASAMON_LOCATION_CODE" ]; then
        ende "kein Terminal für Rückfragen und kein ASAMON_LOCATION_CODE gesetzt.
   Entweder das Skript erst herunterladen und dann ausführen:
     curl -fsSL $ROH/main/install.sh -o install.sh && sudo sh install.sh
   oder die Werte über Umgebungsvariablen mitgeben (siehe Kopf des Skripts)."
    fi

    if tty_da; then
        cat > /dev/tty <<'HINWEIS'

Vier Angaben werden gebraucht. Die wichtigste ist der Standort:

  Der ASA Location Code beschreibt ein Rechteck von rund 1 km Kantenlänge —
  bewusst grob, das schützt dich. Zu ermitteln über https://asa.radio/
  Format: 2366-7443-8484

HINWEIS
    fi

    name=$(frage "Name des Knotens" "${ASAMON_NAME:-$(hostname)}")
    lc=$(frage "Location Code (von asa.radio)" "$ASAMON_LOCATION_CODE")
    [ -n "$lc" ] || ende "ohne Location Code kann der Knoten nicht melden."
    kanaele=$(frage "Kanäle, komma-getrennt" "${ASAMON_CHANNELS:-5C}")
    server=$(frage "Server-URL (leer lassen, solange es keinen gibt)" "$ASAMON_SERVER")
    kontakt=$(frage "Kontakt (optional)" "$ASAMON_CONTACT")
    antenne=$(frage "Antenne (optional)" "$ASAMON_ANTENNA")

    # Mehr als ein Kanal setzt einen zweiten Stick voraus, und der wird über
    # seine Seriennummer ausgewählt — dafür fehlt in asamon-rx noch Patch 2
    # (docs/welle-patches.md). Die Konfigurationsprüfung würde es ohnehin
    # ablehnen; hier abzubrechen erspart eine halb geschriebene Einrichtung.
    anzahl=$(printf '%s' "$kanaele" | tr ',' '\n' | grep -c '[^[:space:]]' || true)
    if [ "$anzahl" -gt 1 ]; then
        ende "$anzahl Kanäle angegeben. Mehrkanalbetrieb braucht je Kanal einen
   eigenen Stick, ausgewählt über seine Seriennummer — dieser Patch fehlt in
   asamon-rx noch. Bis dahin ist ein Kanal je Knoten die belastbare Betriebsart."
    fi

    # Ohne Server bekommt die Konfiguration eine Platzhalter-URL: Sie ist
    # Pflichtfeld, und der Dienst wird dann gar nicht erst gestartet.
    ohne_server=0
    if [ -z "$server" ]; then
        server="https://asa.example.org"
        ohne_server=1
    fi

    mkdir -p /etc/asamon
    tmp=$(mktemp)
    {
        printf 'node:\n'
        printf '  name: "%s"\n' "$name"
        printf '  location_code: "%s"\n' "$lc"
        [ -n "$antenne" ] && printf '  antenna: "%s"\n' "$antenne"
        [ -n "$kontakt" ] && printf '  contact: "%s"\n' "$kontakt"
        printf '\nserver:\n'
        printf '  url: "%s"\n' "$server"
        printf '  report_interval: "10s"\n'
        printf '  timeout: "15s"\n'
        printf '\nchannels:\n'
        alt_ifs=$IFS; IFS=,
        for kanal in $kanaele; do
            kanal=$(printf '%s' "$kanal" | tr -d ' ')
            [ -n "$kanal" ] || continue
            printf '  - channel: "%s"\n' "$kanal"
            printf '    device: "rtl_sdr"\n'
            printf '    gain: "auto"\n'
        done
        IFS=$alt_ifs
        printf '\naudio:\n'
        printf '  enabled: true\n'
        printf '  post_roll: "10s"\n'
        printf '  max_seconds: 300\n'
        printf '  keep_days: 7\n'
        printf '\nlog:\n'
        printf '  level: "info"\n'
    } > "$tmp"

    install -m 0640 -o root -g asamon "$tmp" "$KONFIG"
    rm -f "$tmp"
    sagen "Geschrieben: $KONFIG"
}

pruefe_konfiguration() {
    schritt "Konfiguration prüfen"
    asamon-node --check --config "$KONFIG" || \
        ende "die Konfiguration ist nicht gültig. $KONFIG von Hand richtigstellen, dann:
   asamon-node --check --config $KONFIG"
}

dienst_starten() {
    if [ "${ohne_server:-0}" = 1 ]; then
        schritt "Dienst noch nicht gestartet"
        cat <<HINWEIS
Es wurde keine Server-URL angegeben, also gibt es niemanden, dem der Knoten
melden könnte. Die Einrichtung ist trotzdem vollständig.

Für den Feldtest ohne Server — zeigt, was der Knoten melden würde:
  sudo -u asamon asamon-node --config $KONFIG --dry-run

Sobald es einen Server gibt: server.url in $KONFIG eintragen und
  sudo systemctl enable --now asamon-node
HINWEIS
        return 0
    fi

    schritt "Dienst starten"
    systemctl enable --now asamon-node
    sleep 3
    if systemctl is-active --quiet asamon-node; then
        sagen "asamon-node läuft."
    else
        warnen "der Dienst läuft nicht. Was er sagt:"
        journalctl -u asamon-node -n 20 --no-pager || true
    fi
}

stick_hinweis() {
    # Das Blacklisting kommt aus dem Paket und wirkt erst nach einem Neustart
    # des Moduls; das postinst versucht es bereits. Bleibt der Stick belegt,
    # hilft nur Aus- und Einstecken.
    if command -v rtl_test >/dev/null 2>&1; then
        return 0
    fi
    sagen ""
    sagen "Zum Prüfen des Sticks lohnt sich das Paket rtl-sdr:"
    sagen "  sudo apt install rtl-sdr && rtl_test -t"
}

abschluss() {
    cat <<ENDE

Fertig.

  Version:      $(dpkg-query -W -f='${Version}' asamon 2>/dev/null)
  Konfiguration $KONFIG
  Log:          journalctl -u asamon-node -f
  Prüfen:       asamon-node --check --config $KONFIG

ENDE
}

# --- Ablauf ------------------------------------------------------------------
main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --help|-h)
                sed -n '3,32p' "$0" | sed 's/^# \{0,1\}//'
                exit 0 ;;
            --unattended)
                ASAMON_UNATTENDED=1; shift ;;
            --version)
                ASAMON_VERSION="${2:-}"; shift 2 ;;
            --deb)
                ASAMON_DEB="${2:-}"; shift 2 ;;
            *)
                ende "unbekanntes Argument $1 (--help zeigt die Optionen)" ;;
        esac
    done

    pruefe_umgebung

    vorher=$(installierte_version)
    version="$(neueste_version)"
    [ -n "$version" ] || [ -n "$ASAMON_DEB" ] || \
        ende "kein Release gefunden. Gibt es unter github.com/$REPO/releases schon eines?"

    if [ -n "$vorher" ] && [ -f "$KONFIG" ]; then
        schritt "Aktualisierung: installiert ist $vorher, verfügbar ist ${version:-lokales Paket}"
        if [ -n "$version" ] && [ "$vorher" = "$version" ] && [ -z "$ASAMON_DEB" ]; then
            sagen "Bereits aktuell. Nichts zu tun."
            exit 0
        fi
    elif [ -n "$vorher" ]; then
        schritt "Paket $vorher ist installiert, aber $KONFIG fehlt — Einrichtung wird nachgeholt"
    else
        schritt "Neuinstallation von ${version:-lokalem Paket}"
    fi

    hole_paket "$version"
    installiere_paket

    # Ein installiertes Paket ohne Konfiguration ist keine Aktualisierung,
    # sondern eine halbe Installation — etwa nach einem abgebrochenen Lauf oder
    # einem `apt remove` ohne purge. Dann wird gefragt wie beim ersten Mal.
    if [ -n "$vorher" ] && [ -f "$KONFIG" ]; then
        # Update: Die Konfiguration gehört dem Betreiber, nicht diesem Skript.
        sagen ""
        sagen "$KONFIG blieb unverändert."
        if [ -f "$KONFIG" ]; then
            asamon-node --check --config "$KONFIG" >/dev/null 2>&1 || \
                warnen "die vorhandene Konfiguration ist nicht (mehr) gültig — bitte prüfen:
   asamon-node --check --config $KONFIG"
        fi
        systemctl is-active --quiet asamon-node && sagen "Der Dienst läuft weiter."
        abschluss
        exit 0
    fi

    konfiguration_anlegen
    pruefe_konfiguration
    dienst_starten
    stick_hinweis
    abschluss
}

main "$@"
