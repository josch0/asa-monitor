# asamon-node

Knotenprozess des [ASA-Monitors](../README.md): **Record-Ströme deuten, Datensätze bauen, zum
Server schicken.**

```
┌─ asamon-rx · 5C ──┐                ┌─ asamon-node · Go ───────────────┐
│ C++ · welle.io    │──NDJSON──────▶ │ Kanalzustand · ASA-Automat       │
│ FIG-0/15-Bitparser│◀──REC/STOP──── │ Location-Geometrie · Hashes      │──HTTPS──▶ Server
└───────────────────┘                │ Spool · Uplink · Audio           │
┌─ asamon-rx · 11D ─┐                │ ein 10-s-Datensatz für alle      │
│ …                 │──NDJSON──────▶ │ Kanäle des Knotens               │
└───────────────────┘                └──────────────────────────────────┘
```

Ein Knoten, eine Konfigurationsdatei, ein Log, eine systemd-Unit — gleich wie viele Sticks
stecken.

---

## Was es tut — und was ausdrücklich nicht

**Ist:** Prozessverwaltung, Deutung, Zustand, Geometrie, Identität, Spool, Uplink.

**Ist ausdrücklich nicht:**

- **Kein Bitparser für FIG 0/15.** Das macht der welle.io-Fork, und `asamon-rx` reicht die
  Felder fertig heraus. `asamon-node` liest `location_codes` als Hex und deutet **nur diese**.
- **Kein FIC-Parser.** FIG 0/0, 0/1, 0/2, 0/9, 0/10 und 1/x kommen als `ens`- und `tlm`-Records
  fertig an.
- **Kein Audio-Decoder.** Der rohe Subchannel-Bitstrom wird durchgereicht, nicht dekodiert. Die
  Datei geht so zum Server, wie sie vom Kanal kam.
- **Kein Server.** Datenhaltung, Karte und Korrelation über Knoten hinweg gehören ins Backend.
- **Kein SDR-Code.** Kein librtlsdr, kein cgo. `CGO_ENABLED=0` ist verbindlich — die Binaries
  sind statisch.

---

## Bauen

Go **≥ 1.27**. Eine einzige Fremdabhängigkeit: `gopkg.in/yaml.v3`. Alles Weitere — HTTP, JSON,
Kryptografie, UUIDs, Logging, Prozessverwaltung — kommt aus der Standardbibliothek.

Die Untergrenze ist nicht willkürlich. Aus 1.27 werden drei Dinge gebraucht, die den Knoten
messbar besser machen: das Paket `uuid` (die `node_id` braucht keinen Eigenbau mehr),
`encoding/json/v2` für den Record-Leser (strenger **und** rund ein Drittel schneller auf dem
Audio-Pfad, siehe `BenchmarkParseLineAud`) und das Profil `goroutineleak`, das im Dauerlauf
findet, was bloßes Zählen von Goroutinen übersieht. Wer eine ältere Toolchain hat, bekommt die
passende über `GOTOOLCHAIN=auto` automatisch.

```bash
make            # prüfen und bauen
make test       # alles, ohne SDR-Stick und ohne Netz
make cross      # statische Binaries für alle Zielplattformen
make dist       # dieselben als fertige Archive, so wie sie ins Release gehen
```

Unter Windows ohne `make` genügt `go build ./cmd/asamon-node`; `go test ./...` läuft dort
ebenso. Zielplattformen sind `linux/amd64`, `linux/arm64`, `linux/arm` (armv7) und
`windows/amd64` — alle vier baut ein einziger Rechner, weil kein cgo im Spiel ist.

---

## Einrichten

```bash
sudo make install                      # nach /usr/local/bin, samt Unit und Beispiel
sudoedit /etc/asamon/node-config.yaml
asamon-node --check --config /etc/asamon/node-config.yaml
```

`--check` prüft die Konfiguration vollständig und gibt den dekodierten Standort aus:

```
Konfiguration: /etc/asamon/node-config.yaml
Knoten:        Berlin-Mitte-01
Standort:      2366-7443-8484  (Z10:B736BB)
  Rechteck:    51.512695 .. 51.521484 Breite, -0.149414 .. -0.140625 Länge (WGS84)
  Mittelpunkt: 51.517090, -0.145020
  Kantenlänge: rund 978 m in Nord-Süd-Richtung
  URI:         DLI://2366-7443-8484
Server:        https://asa.example.org (Takt 10s, Timeout 15s)
Kanäle:        1
  1. 5C   über rtl_sdr, Stick (erstes Gerät), Gain auto
Audio:         true (Nachlauf 10s, höchstens 300 s, 7 Tage aufbewahren)
Pfade:         /usr/local/bin/asamon-rx, Zustand in /var/lib/asamon

Die Konfiguration ist gültig.
```

**Den Standort liefert [asa.radio](https://asa.radio/) zu einer Adresse.** Er wird als ASA
Location Code eingetragen, nicht als Lat/Lon: Es ist das geforderte Format, und seine Grobheit
von rund 1 km schützt die Freiwilligen ganz nebenbei. Ein Tippfehler fällt an der Prüfsumme auf.

Jede Option ist in [`docs/node-config.md`](docs/node-config.md) ausgeschrieben.

---

## Betrieb unter Linux

```bash
sudo systemctl enable --now asamon-node
journalctl -u asamon-node -f
```

Eine Unit für den ganzen Knoten. Die `asamon-rx`-Prozesse startet `asamon-node` selbst und hält
sie am Leben — mit exponentiellem Backoff von 1 s bis 60 s. **Ein toter Kanal beendet niemals
den Knoten**; die übrigen laufen unterbrechungsfrei weiter, und ein Kanal in Dauerneustart ist
selbst eine meldenswerte Beobachtung (`rx_state`, `rx_restarts`, `last_error` im Datensatz).

Nicht nur ein abgestürzter Kanal wird neu gestartet, sondern auch ein **festgefahrener**:
`asamon-rx` schickt jede Sekunde einen `tlm`-Record, auch ohne Empfang; bleiben Records länger
als `limits.rx_silence_seconds` (Vorgabe 15 s) ganz aus, steht seine Sekundenschleife. Diesen
Fall deckte früher der systemd-Watchdog von `asamon-rx` ab — er ist entfallen, weil er den
Empfangsprozess an systemd band und unter Windows nichts tat. Einzelheiten in
[`docs/node-config.md`](docs/node-config.md#rx_silence_seconds--der-ersatz-für-den-watchdog).

`Type=notify` mit `WatchdogSec=60`: Der Watchdog wird nur bedient, solange der Reporter läuft.
Ein hängender Reporter löst einen Neustart aus — genau dafür ist er da.

Voraussetzung ist eine **synchronisierte Uhr** (chrony oder systemd-timesyncd). Sie ist nicht
Zubehör: Alle ASA-Alerts sollen an der Minutengrenze beginnen, und die Differenz zwischen
Knotenzeit und Ensemble-Zeit ist selbst eine Messgröße (`ens_time_offset_ms`).

---

## Betrieb unter Windows

Beide Programme laufen dort. `asamon-rx` ist seit dem 27.08.2026 nach Windows portiert und
baut nativ mit MSYS2/MinGW-w64 — kein WSL, kein Docker (`../asamon-rx/README.md`,
Abschnitt „Windows: Übersetzen"). Ein Windows-Rechner ist damit ein vollwertiger Knoten.

Einschränkung, solange sie gilt: Belegt ist der Empfangspfad bis einschließlich
`--device rawfile`. Der Betrieb mit echtem RTL-SDR-Stick über WinUSB steht noch aus.

Archiv auspacken, Konfiguration danebenlegen, prüfen:

```powershell
tar -xzf asamon-node-0.1.0-windows-amd64.tar.gz
cd asamon-node-0.1.0-windows-amd64
copy node-config.example.yaml node-config.yaml
notepad node-config.yaml
.\asamon-node.exe --check
```

Ohne `paths:`-Abschnitt gelten die Windows-Vorgaben: `asamon-rx.exe` **neben** `asamon-node.exe`,
Zustand unter `%ProgramData%\asamon\state`. Ein ausgepacktes Archiv läuft damit ohne
Installation.

Als Dienst gibt es keine systemd-Unit; die Aufgabenplanung tut dasselbe:

```powershell
schtasks /create /tn asamon-node /sc onstart /ru SYSTEM /rl HIGHEST ^
  /tr "\"C:\asamon\asamon-node.exe\" --config C:\asamon\node-config.yaml"
```

Drei Unterschiede zu Linux, die man kennen sollte:

| | Linux | Windows |
|---|---|---|
| Kindprozesse aufräumen | Control Group der systemd-Unit | **Job Object** mit `KILL_ON_JOB_CLOSE` — ein hart beendeter Knoten nimmt `asamon-rx` mit, damit der Stick frei wird |
| Herunterfahren des Kindes | `QUIT` → `SIGTERM` → `SIGKILL` | `QUIT` → hartes Beenden; Windows kennt für fremde Prozesse kein SIGTERM |
| `ntp_synchronized` | aus systemd-timesyncd bestätigt | immer `false` — **nicht bestätigt**, nicht „Uhr falsch". Maßgeblich ist `ens_time_offset_ms` |

Und eine Warnung, die der Knoten beim Start auch selbst ausgibt: **Windows erzwingt den Modus
0600 nicht.** Die Rechte an `node_key` kommen aus den vererbten ACLs des `state_dir`. Heute
schützt der Schlüssel nichts — signiert wird nicht —, aber an dem Tag, an dem Signieren
nachgerüstet wird, zählt es:

```powershell
icacls C:\ProgramData\asamon\state /inheritance:r /grant:r "%USERNAME%:F"
```

---

## Was ohne Server geht

```bash
# Was würde dieser Knoten melden? Ohne Uplink, Datensätze nach stdout.
asamon-node --dry-run | jq -c '.channels[] | {ch:.channel, hb:.asa.heartbeat}'

# Eine Aufzeichnung abspielen — die Zustandsmaschine merkt keinen Unterschied.
asamon-node --replay testdata/streams/alert-einfach.ndjson --dry-run

# Alle Kanäle aus einem Verzeichnis, je Kanal eine Datei <kanal>.ndjson
asamon-node --replay /pfad/zu/mitschnitten --replay-speed 1 --dry-run
```

**`--replay` ist kein Nebenschauplatz.** Die Zustandsmaschine unterscheidet nicht, ob ihr Strom
aus `asamon-rx` oder aus einer Datei kommt — das ist die Grundlage aller Regressionstests und
der einzige Weg, ASA-Verkehr zu prüfen, bevor es welchen gibt.

---

## Der Datensatz

Ein Datensatz je `report_interval` (Vorgabe 10 s), **immer**, auch wenn nichts empfangen wurde.
Sonst kann der Server „Ensemble sendet keinen Heartbeat" nicht von „Knoten ist tot"
unterscheiden — und damit wäre die Abdeckungskarte wertlos, das Kernergebnis des Projekts.

Zusätzlich geht bei **jedem Phasenwechsel eines Alerts** sofort einer raus: Der 10-Sekunden-Takt
ist für Heartbeat und Telemetrie richtig und für einen Alert falsch.

Vollständige Beschreibung: **[`docs/uplink-protokoll.md`](docs/uplink-protokoll.md)** — das ist
das Vertragsdokument zur Serverseite.

### Warum jeder Datensatz Hashes trägt

Mehrere Knoten empfangen dasselbe Signal. Damit der Server sie zusammenführen kann, trägt jede
Beobachtung einen Hash, den **jeder Knoten unabhängig zum selben Wert berechnet**. Die Hashes
stützen sich dabei auf die **Ensemble-Zeit** aus FIG 0/10, nicht auf die Knotenuhr: Sie kommt
aus demselben Sender und ist bei allen Empfängern desselben Ensembles bitgleich.

Definitionen und Testvektoren: [`docs/hashes.md`](docs/hashes.md).

---

## Testen

```bash
make test       # alles
make fuzz       # die beiden Parser, die fremde Bytes sehen
make race       # Nebenläufigkeit (braucht cgo, also einen C-Compiler)
```

Keiner der Tests braucht einen SDR-Stick oder Netz.

| Paket | Was geprüft wird |
|---|---|
| `loc` | Präsentationsformat beide Richtungen samt Prüfsumme; das **Cardiff-Beispiel aus TS 104 089 Annex C** (vier Location Codes, 22 Byte, 17 Rechtecke); die **Byte-Längen aus TS 104 090 Tabelle A.19**; 10 000 Zufallspositionen in Deutschland |
| `record` | NDJSON lesen, unbekannte Felder und Typen, `seq`-Lücken, kaputte Zeilen — plus Fuzzing auf dem Zeilenparser |
| `hashes` | jeder Testvektor aus `docs/hashes.md`, und dass das Dokument dieselben Werte nennt |
| `chanstate` | Alert-Sets, Phasen, Heartbeat-Bilanz, P/D, Test-Stage, OE — gegen **Golden-Datensätze** in `testdata/golden/` |
| `rxproc` | Start, Absturz, Backoff, `QUIT` → `SIGTERM` → `SIGKILL` |
| `spool` | Reihenfolge, Überlauf mit Alert-Vorrang, halbe Dateien nach einem Absturz |
| `uplink` | Wiederholung, Backoff mit Streuung, Idempotenz, `audio_wanted` |
| `audio` | Prüfsummen, Lücken, Aufräumen |
| `supervisor` | drei Kanäle mit einem abstürzenden; Serverausfall und Nachlieferung; die Gesamtkette |

### Die zwei Proben, auf denen alles ruht

Eine Referenzimplementierung von FIG 0/15 gibt es nicht — die einzige vollständige, die
existiert, liest Id- und Status-Feld vertauscht und liefert trotzdem plausibel aussehende Werte.
Deshalb zwei unabhängige normative Proben:

1. **`cmd/fake-rx` packt FIG 0/15 selbst** und wird gegen `asamon-rx/tests/fixtures/` geprüft:
   Alle 18 Fixtures müssen byteweise stimmen. Wer hier auseinanderläuft, erzeugt Testströme, die
   es on air nie gäbe, und prüft damit nichts.
2. **Das Cardiff-Beispiel aus Annex C** legt die Bitreihenfolge des Sub-codes-Feldes fest: 22
   Byte, 17 sphärische Rechtecke, und das Warngebiet muss um den gemeinsamen Eckpunkt der vier
   4-stelligen Rechtecke liegen. Mit vertauschter Bitreihenfolge läge es spiegelbildlich am
   falschen Rand.

### Die Kernaussage des Dedup-Verfahrens

> Derselbe Strom, abgespielt mit **verschieden gehenden Knotenuhren**, ergibt bitgleiche
> `asa_hash`, `ens_hash`, `ens_content_hash` und `alert_uid`.

Das ist ein Test (`TestHashesHaengenNichtAnDerKnotenuhr`), keine Behauptung. Ohne ihn wäre das
ganze Verfahren eine Hoffnung.

### Testdaten

Echten ASA-Verkehr hat niemand aufgezeichnet. Die Ströme in `testdata/streams/` werden deshalb
erzeugt — nach TS 104 089 Annex E gepackt, mit Pre-Trigger, Trigger über 5 s, Sustain, End,
mehrteiligem Alert-Set und OE-Verweis:

```bash
./build/fake-rx --list
make streams          # neu erzeugen
```

Sobald es echte Mitschnitte gibt, treten sie **daneben**, nicht an ihre Stelle.

---

## Was am Gerät noch aussteht

Die Tests kommen ohne Stick aus — der Empfang nicht. Offen bleibt alles, wofür ein RTL-SDR und
eine Antenne nötig sind, allen voran die Frage, um die es geht: **Sendet 5C schon Heartbeats?**
Siehe `TODO.md` Abschnitt 21.

---

## Lizenz

**GPL-3.0-or-later**, wie `asamon-rx`. Der Server bleibt als eigenständiges Programm frei
lizenzierbar — zwischen ihm und dem Knoten steht ein HTTP-Protokoll, kein gemeinsamer
Adressraum.
