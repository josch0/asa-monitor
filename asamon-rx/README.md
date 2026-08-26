# asamon-rx

Empfangsprozess des [ASA-Monitors](../README.md): **SDR → FIC → FIG 0/15 auspacken.**

Beantwortet die Frage, die vor allen anderen steht und bisher unbeantwortet ist:
**Sendet dieses Ensemble schon einen ASA-Heartbeat?**

```bash
asamon-rx --channel 5C | jq -c 'select(.type=="asa")'
```

Braucht dafür weder Server noch Protokoll noch `asamon-node`.

---

## Was es tut — und was ausdrücklich nicht

**ASA** (Automatic Safety Alert) ist die deutsche Ausprägung des DAB-Warnsystems nach
**ETSI TS 104 089**. Die Warnmeldung selbst ist normales DAB+-Audio; neu ist allein die
Signalisierung im FIC über ein einziges Element: **FIG 0/15**. Es sagt, in welchem Subchannel
gerade eine Warnung läuft, welche Warnstufe sie hat und für welches Gebiet sie gilt.

`asamon-rx` kennt das Bitlayout aus Annex E — das ist normativ und ändert sich nicht — und
**deutet nichts**. Keine Alert-Sets, keine Phasenverläufe, kein Dedup, keine
Location-Geometrie, kein Uplink, keine Signatur. Das alles ist Sache von `asamon-node`.

Ein Stick, ein Kanal, ein Prozess. Records gehen nach **stdout**, Logs nach **stderr** — immer,
ohne Ausnahme.

---

## Bauen

### Abhängigkeiten (Raspberry Pi OS / Debian, 64 bit)

```bash
sudo apt update
sudo apt install -y build-essential cmake git pkg-config \
    libfftw3-dev libfaad-dev libmpg123-dev librtlsdr-dev
```

### Quellen holen

```bash
git clone --recurse-submodules <dieses-repo>
```

Wurde ohne `--recurse-submodules` geklont:

```bash
git submodule update --init --recursive
```

Der welle.io-Quellbaum unter `external/welle.io` steht auf einem **festgenagelten Commit** und
trägt einen Patch — ohne ihn baut `asamon-rx` nicht. Was, warum und wie er sich auf einen
anderen Stand übertragen lässt: [`docs/welle-patches.md`](docs/welle-patches.md).

### Übersetzen

```bash
cmake -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j$(nproc)
ctest --test-dir build --output-on-failure
```

Auf dem Pi 3 / Zero 2 W kann der Speicher knapp werden: dann `-j2` und gegebenenfalls Swap.

> **Niemals mit `-DFDK_AAC=ON` bauen.** Die Fraunhofer-FDK-AAC-Lizenz gilt weithin als
> GPL-unverträglich. Die Vorgabe ist FAAD2, und `CMakeLists.txt` erzwingt sie.

### RTL-SDR nutzbar machen

Der Kernel greift sich den Stick sonst als DVB-T-Empfänger:

```bash
echo 'blacklist dvb_usb_rtl28xxu' | sudo tee /etc/modprobe.d/blacklist-rtl.conf
sudo modprobe -r dvb_usb_rtl28xxu

sudo cp contrib/99-asamon-rtlsdr.rules /etc/udev/rules.d/
sudo udevadm control --reload && sudo udevadm trigger
```

Probe: `rtl_test -t`.

---

## Starten

```
asamon-rx --channel 5C [Optionen]

  --channel <name>       DAB-Kanal, z. B. 5C, 11D, 7B (Pflicht)
  --device <name>        rtl_sdr (Vorgabe) | rtl_tcp | airspy | soapysdr | rawfile | auto
  --iq-file <pfad>       Quelle für --device rawfile
  --iq-format <format>   u8 (Vorgabe) | s8 | s16le | s16be | complexf
  --gain auto|<index>    Vorgabe: auto
  --queue-size <n>       Tiefe der Ausgabe-Warteschlange (Vorgabe: 4096)
  --rec-max-seconds <n>  Notbremse für REC (Vorgabe: 600, 0 = aus)
  --fifo-dir <pfad>      Ablage der MSC-FIFOs (Vorgabe: /tmp)
  --log-level <stufe>    error|warn|info|debug (Vorgabe: info)
  --version
```

### Was man damit sieht

```bash
# Kommen Heartbeats?
asamon-rx --channel 5C | jq -c 'select(.type=="asa")'

# Wie gut ist der Empfang? Das ist die Zahl, die "Ensemble schweigt" von
# "wir empfangen schlecht" trennt.
asamon-rx --channel 5C | jq -r 'select(.type=="tlm") |
    "\(.snr) dB  \(.fib_crc_err)/\(.fib_total) CRC-Fehler"'

# Was liegt überhaupt im Multiplex?
asamon-rx --channel 5C | jq -r 'select(.type=="ens") |
    .services[] | "\(.label): \(.components[].subch_id)"'
```

Es gibt **keinen Probe-Modus**. Er wäre eine zweite Ausgabeform, die man synchron halten
müsste — NDJSON ist bereits lesbar genug.

### Replay statt Funk

```bash
asamon-rx --channel 5C --device rawfile --iq-file mitschnitt.iq
```

Dieselbe Kette ohne Antenne. **Jeden Mitschnitt aufheben** — bis zum Regelbetrieb sind
Ereignisse rar, und ein IQ-Mitschnitt konserviert echte Wirklichkeit.

---

## Kommandos auf stdin

ASCII, eine Zeile je Kommando. Unbekannte Zeilen werden gezählt und geloggt, nicht
stillschweigend verworfen.

| Kommando | Wirkung |
|---|---|
| `REC <subChId>` | Subchannel zuschalten, roher MSC-Strom geht als `aud`-Records raus |
| `STOP <subChId>` | Mitschnitt beenden |
| `QUIT` | sauber herunterfahren |

```bash
# Subchannel 7 zwei Minuten mitschneiden
{ echo "REC 7"; sleep 120; echo "STOP 7"; echo "QUIT"; } | asamon-rx --channel 5C > lauf.ndjson

# und wieder zu Audio zusammensetzen
jq -r 'select(.type=="aud") | .data' lauf.ndjson | base64 -d > subch7.dabplus
```

---

## Der Record-Strom

NDJSON, ein JSON-Objekt je Zeile. Fünf Typen: `init`, `tlm`, `ens`, `asa`, `aud`.
Vollständige Beschreibung: **[`docs/record-format.md`](docs/record-format.md)**.

```json
{"type":"asa","seq":42,"ts":"2026-08-26T14:03:11.482913771Z","heartbeat":true,
 "cn":true,"oe":false,"pd_second_half":false,"raw":"018f"}
```

Das `raw`-Feld ist nicht optional: Es gibt keine Referenzimplementierung von FIG 0/15, und die
einzige vollständige, die existiert, liest Id- und Status-Feld vertauscht. 30 Byte je Ereignis
sind der Preis dafür, dass man sich irren darf.

---

## Betrieb unter systemd

`contrib/asamon-rx.service` ist eine Vorlage für den **Einzelbetrieb**, mit Watchdog. Im
regulären Knotenbetrieb startet `asamon-node` die `asamon-rx`-Prozesse selbst — dann braucht
ein Knoten eine Unit statt N.

Voraussetzung ist eine synchronisierte Uhr (`chrony` oder `systemd-timesyncd`): Die Differenz
zwischen `ts` und der Ensemble-Zeit aus FIG 0/10 ist eine Messgröße, und alle ASA-Alerts sollen
an der Minutengrenze beginnen.

---

## Testen

```bash
ctest --test-dir build --output-on-failure
```

Sechs Testprogramme, keines braucht einen SDR-Stick:

| Test | Was er prüft |
|---|---|
| `fig0_15` | **der wichtigste.** Handgebaute FIBs direkt in `FIBProcessor::processFIB()`, gegen die Fälle aus Annex E |
| `location_codes` | das Bitlayout der Location Codes gegen die Byte-Längen in TS 104 090, Tabelle A.19 — eine zweite normative Quelle |
| `record` | Serialisierung: Struktur → JSON-Zeile |
| `writer` | Reihenfolge, Nummerierung, Vorrangregel beim Verwerfen |
| `recorder` | der FIFO-Pfad, ohne Empfänger |
| `commands` | Zeilenkommandos, samt der abgelehnten |

`tests/fixtures/fig0_15.fixtures` hält die erwarteten Records byteweise fest. Ändert sich der
Parser, wird das dort als Diff sichtbar — nicht nur als rote Zusicherung. Neu erzeugen:

```bash
./build/test_fig0_15 --write-fixtures tests/fixtures/fig0_15.fixtures
```

### Was am Gerät geprüft werden muss

Die Tests kommen ohne Stick aus — der Empfang nicht. Was bisher **nur unter Replay** geprüft
ist und am echten Multiplex nachgezogen gehört: Ensemble-Label und Serviceliste, `REC` auf
einen echten Subchannel (Gegenprobe mit `dablin`), und die Frage, um die es geht — kommen auf
5C Heartbeats?

Geprüft ist dagegen bereits, unter Debian mit `--device rawfile`:

| | |
|---|---|
| `SIGTERM` | beendet in **41 ms**, keine verwaiste FIFO |
| Gegenstelle bricht weg | EPIPE erkannt, geordneter Abbau statt hartem Tod |
| fehlende IQ-Datei | Rückgabewert 1 und klare Meldung, statt wortlos nach einer Sekunde aufzuhören |
| **12 min Dauerlauf** | RSS **konstant 25 728 kB**, 6 Threads, 722 Records, **0** Verwürfe, **0** `seq`-Lücken, jede Zeile gültiges JSON |

Der Dauerlauf zeigt kein Speicherwachstum, ersetzt aber die **24 Stunden auf dem Pi** aus M4
nicht — das steht noch aus.

Fuzzing über denselben Einstiegspunkt:

```bash
cmake -B build-fuzz -DFUZZING=ON -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++
cmake --build build-fuzz --target fuzz_fig0_15
./build-fuzz/fuzz_fig0_15 -max_len=32
```

---

## Lizenz

**GPL-3.0-or-later.** `asamon-rx` linkt statisch gegen `libwelle` aus welle.io (GPL-2.0-or-later),
das in `src/backend/dabplus_decoder.cpp` dablin-Code unter GPL-3-or-later enthält.

Weil ein **verändertes** welle.io mit ausgeliefert wird, muss dessen Quelltext samt Änderungen
bei Weitergabe verfügbar sein. Siehe [`docs/welle-patches.md`](docs/welle-patches.md).
