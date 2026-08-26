# asamon-rx — Umsetzungsplan

Dieses Dokument ist die vollständige Arbeitsanweisung für die erste Fassung dieses Repos.
Es ist so geschrieben, dass es **ohne Vorwissen** ausreicht: Wer hier anfängt, braucht nichts
weiter als diese Datei, die verlinkten Specs und einen Raspberry Pi mit RTL-SDR-Stick.

**Stand: 26.08.2026. Es ist noch nichts implementiert.**

---

## 1. Worum es geht — in zehn Zeilen

**ASA (Automatic Safety Alert)** ist die deutsche Ausprägung des DAB-Warnsystems nach
**ETSI TS 104 089**. Die Warnmeldung selbst ist normales DAB+-Audio; neu ist allein die
Signalisierung im FIC über ein einziges Element: **FIG 0/15**. Es sagt, in welchem Subchannel
gerade eine Warnung läuft, welche Warnstufe sie hat und für welches Gebiet sie gilt.

Das Projekt **asa-monitor** baut ein Crowd-Netz aus verteilten SDR-Empfängern, das diese
Signalisierung bundesweit beobachtet und auf einer Karte darstellt. Ein Knoten besteht aus
zwei Programmen:

| Programm | Repo | Sprache | Aufgabe |
|---|---|---|---|
| **`asamon-rx`** | dieses hier | C++ | Empfang: SDR → FIC → **Bitlayout** von FIG 0/15 auspacken. Deutet nichts. |
| `asamon-node` | `../asamon-node` | Go | Deutung: Alert-Sets, Phasen, Dedup, Location-Decoding, Spool, Uplink zum Server. |

Verbunden sind sie über einen **Record-Strom** auf stdout und **Zeilenkommandos** auf stdin.
`asamon-node` startet je überwachtem Kanal einen `asamon-rx` als Kindprozess.

> **Namen.** Die Specs nennen die Programme `asa-rx` und `asa-node`. Gemeint ist dasselbe;
> Repo- und Binärname sind `asamon-rx` bzw. `asamon-node`.

### Die Specs — vor dem Anfangen lesen

Alle unter `T:\dev\asa-monitor\specs\`:

| Datei | Was daraus gebraucht wird |
|---|---|
| **`client-architektur.md`** | **Das Hauptdokument.** Abschnitt 1 (warum der Patch), 2 (welle.io-API), 2a (Einbindung), **2b (der Patch, im Detail)**, 4 (Nebenläufigkeit), 4a (Prozessmodell, Record-Strom), 5 (Betrieb), 6 (offene Punkte) |
| **`asa.md`** | Abschnitt 3: **Bitlayout von FIG 0/15**, normativ nach TS 104 089 Annex E. Das ist die Vorlage für den Parser. Abschnitt 4: Phasenablauf |
| `client-konzept-review.md` | §3.2 (Negativ-Beobachtung), §3.4 (Roh-Beleg), §3.5 (Audio), §4 (Randbedingungen) |
| `decoder-optionen.md` | Abschnitt 0.1 (die zwei existierenden Fork-Parser und ihre Fehler), Abschnitt 7 (Lizenz) |
| `text/ts_104089v010101p.txt` | Textextrakt der Norm. `grep -n "FIG 0/15" specs/text/ts_104089v010101p.txt` |

---

## 2. Ziel dieser ersten Fassung

**Eine Binary, die auf einem Raspberry Pi mit einem RTL-SDR-Stick läuft und beantwortet:
Sendet dieses Ensemble schon einen ASA-Heartbeat?**

Das ist die Frage, die vor allen anderen steht — sie ist bisher unbeantwortet, und sie braucht
weder Server noch Protokoll noch `asamon-node`.

### Ausdrücklich **nicht** Aufgabe von `asamon-rx`

- Keine Deutung von ASA: keine Alert-Sets, keine Phasenverläufe, kein Dedup, keine
  Location-Geometrie. Das alles ist `asamon-node`.
- Kein Uplink, kein HTTPS, kein Spool, keine Signatur.
- Keine Verwaltung mehrerer Kanäle in einem Prozess. **Ein Stick, ein Kanal, ein Prozess.**
- Kein Audio-Dekodieren. Übertragen wird der rohe DAB+-Bitstrom.

Die Grenze lautet: **`asamon-rx` kennt das Bitlayout aus Annex E — das ist normativ und ändert
sich nicht — und deutet nichts.** Was sich ändern wird, sobald echter ASA-Verkehr zu sehen ist,
gehört auf die andere Seite der Pipe.

---

## 3. Verifizierte Fakten über welle.io

Alle am **26.08.2026** im Zweig `next` von `https://github.com/AlbrechtL/welle.io` geprüft.
**Nicht neu herleiten, nicht raten** — hier stehen genau die Punkte, an denen man sonst
Stunden verliert.

### Die Bibliothek

- **`libwelle` ist kein eigenständiges Projekt.** Es gibt kein Repo, kein Paket, keine
  installierten Header, keine pkg-config-Datei, kein `find_package`-Export. Der Name bezeichnet
  das CMake-Ziel `welle` *innerhalb* von welle.io. Einbindung nur über den Quellbaum.
- `add_library(welle STATIC ...)` — Vorgabe ist statisch (`LIBWELLE_STATIC=ON`).
- welle.io baut mit **C++14**. Unser eigener Code darf neuer sein.
- **`RTLSDR` ist per Vorgabe `OFF`.** Ohne `-DRTLSDR=ON` gibt es keinen RTL-SDR-Support, und
  `CInputFactory` liefert stillschweigend ein Null-Device. Häufigste Startfalle.
- welle.io nutzt **globales `include_directories()`** statt
  `target_include_directories(welle PUBLIC ...)`. Das propagiert **nach unten**, nicht nach
  oben: `target_link_libraries(asamon-rx PRIVATE welle)` allein gibt uns **keine Header**.
  Die Pfade müssen im eigenen Ziel gesetzt werden (siehe Abschnitt 6).

### Die Schnittstellen

Aus `src/backend/radio-controller.h` (`RadioControllerInterface`) und
`src/backend/radio-receiver.h` (`RadioReceiver`):

```cpp
// Rückrufe, die wir implementieren (alle rein virtuell, also alle nötig):
virtual void onSNR(float snr) = 0;
virtual void onFrequencyCorrectorChange(int fine, int coarse) = 0;
virtual void onSyncChange(char isSync) = 0;
virtual void onSignalPresence(bool isSignal) = 0;
virtual void onServiceDetected(uint32_t sId) = 0;
virtual void onNewEnsemble(uint16_t eId) = 0;
virtual void onSetEnsembleLabel(DabLabel& label) = 0;
virtual void onDateTimeUpdate(const dab_date_time_t& dateTime) = 0;
virtual void onFIBDecodeSuccess(bool crcCheckOk, const uint8_t* fib) = 0;
virtual void onNewImpulseResponse(std::vector<float>&& data) = 0;
virtual void onConstellationPoints(std::vector<DSPCOMPLEX>&& data) = 0;
virtual void onNewNullSymbol(std::vector<DSPCOMPLEX>&& data) = 0;
virtual void onTIIMeasurement(tii_measurement_t&& m) = 0;
virtual void onMessage(message_level_t level, const std::string& text,
                       const std::string& text2 = std::string()) = 0;
// nicht rein virtuell, Vorgabe leer:
virtual void onInputFailure(void) { }
virtual void onRestartService(void) { }

// RadioReceiver:
RadioReceiver(RadioControllerInterface& rci, InputInterface& input,
              RadioReceiverOptions rro, int transmission_mode = 1);
void restart(bool doScan);           // false = empfangen, nicht scannen
void stop();
bool addServiceToDecode(ProgrammeHandlerInterface& h,
                        const std::string& dumpFileName, const Service& s);
bool removeServiceToDecode(const Service& s);
uint16_t getEnsembleId() const;
DabLabel getEnsembleLabel() const;
std::vector<Service> getServiceList() const;
std::list<ServiceComponent> getComponents(const Service& s) const;
Subchannel getSubchannel(const ServiceComponent& sc) const;   // subch == -1 = Fehler
```

Gerät erzeugen (`src/input/input_factory.h`):

```cpp
CVirtualInput* CInputFactory::GetDevice(RadioControllerInterface&, const std::string& device);
// gültige Namen: "auto", "rtl_sdr", "rtl_tcp", "airspy", "soapysdr", "rawfile"
```

Kanal → Frequenz (`src/various/channels.h`, Teil von `backend_sources`, also in `libwelle`):

```cpp
Channels ch;
int freqHz = ch.getFrequency("5C");
```

### Die fünf Fallen

1. **`onFIBDecodeSuccess()` liefert ein Bit je Byte** — `bitBuffer_out` ist 768 Byte groß, der
   Rückruf bekommt `&bitBuffer_out[(i % 3) * 256]`, also **256 Byte für 256 Bit**. Für uns ist
   das eine Falle, die wir umgehen, indem wir den Puffer **gar nicht anfassen**: Über die Pipe
   gehen ausschließlich geparste `asa`-Records, nie rohe FIBs. Gebraucht wird aus diesem
   Rückruf nur das Bit `crcCheckOk` für die Zählung. Wer hier anfängt zu packen, löst ein
   Problem, das wir nicht haben.

2. **`onFIBDecodeSuccess()` wird für *jeden* FIB gerufen, `processFIB()` nur für gültige.**
   `fic-handler.cpp`, Z. 215–229: `onFIBDecodeSuccess(crcvalid, p)` immer, danach
   `if (crcvalid) fibProcessor.processFIB(...)`. Daraus folgt die CRC-Quote, mit der sich
   „Ensemble sendet keinen Heartbeat" von „wir empfangen schlecht" trennen lässt — und der
   FIG-0/15-Parser sieht nie einen kaputten FIB.

3. **`onAsaAlert()` läuft unter dem Mutex des `FIBProcessor`.** `processFIB()` hält über die
   *gesamte* FIG-Verteilung `std::lock_guard<std::mutex> lock(mutex)` — denselben
   nicht-rekursiven Mutex, den `getServiceList()`, `getComponents()`, `getSubchannel()` und
   `getEnsembleId()` nehmen. **Aus dem Rückruf heraus nie in den `FIBProcessor` zurückrufen.**
   Ein `getServiceList()` an dieser Stelle ist ein sicherer Selbst-Deadlock beim ersten Alert.
   Die Auflösung SubChId → Service gehört hinter die Warteschlange, auf den Steuerungsthread.

4. **`dumpFileName` in `addServiceToDecode()` schreibt den rohen MSC-Strom**, nicht dekodiertes
   Audio: In `decoder_adapter.cpp` geht derselbe Puffer an `decoder->Feed()` und an `fwrite`.
   Der Name darf auf eine **benannte Pipe** zeigen. Preis: welle.io öffnet mit
   `fopen(..., "wb")`, das **blockiert, bis ein Leser da ist** — unser Leser muss also stehen,
   *bevor* zugeschaltet wird, und ein hängender Leser blockiert den Decoder-Thread.

5. **`CRTL_SDR::open_device()` öffnet das erste Gerät, das sich öffnen lässt.** Es gibt
   **keine** Auswahl über Index oder Seriennummer (`rtl_sdr.cpp`, Z. 64 ff.:
   „Found N devices. Uses the first working one"). Für einen Knoten mit **einem** Stick ist das
   egal. Für mehrere Sticks ist es ein Blocker — siehe Abschnitt 10, Patch 2. Bis der da ist,
   gilt: **nur ein Stick je Knoten testen.**

### Rückrufe laufen auf welle.io-Threads

Auf dem OFDM-Thread und je einem Thread pro zugeschaltetem Subchannel. Daraus folgt die
Regel, die den ganzen Entwurf trägt:

> **Rückrufe kopieren und stellen ein — sonst nichts.** Kein Datei- oder Netzzugriff, keine
> Sperre über Arbeit hinweg, keine Allokation außerhalb eines beschränkten Pools. Wer im
> OFDM-Thread blockiert, verliert Samples.

Alles Schreiben nach stdout läuft deshalb auf einem **eigenen Ausgabethread**, gespeist aus
einer beschränkten Warteschlange, die im Überlauf **verwirft und zählt** statt zu blockieren.

---

## 4. Lizenz — vor dem ersten Commit klären

`asamon-rx` linkt gegen `libwelle`, statisch. welle.io ist GPL-2.0-**or-later**, enthält aber
in `src/backend/dabplus_decoder.cpp` dablin-Code unter GPL-3-or-later. Das gebaute Binary ist
damit effektiv **GPL-3.0-or-later**, und dieses Repo ist es auch.

- `LICENSE` mit dem GPL-3.0-Text **im ersten Commit** anlegen.
- SPDX-Kopf in jede Quelldatei: `// SPDX-License-Identifier: GPL-3.0-or-later`
- **Niemals mit `-DFDK_AAC=ON` bauen** — die Fraunhofer-FDK-AAC-Lizenz gilt weithin als
  GPL-unverträglich. Mit FAAD2 bauen.
- Weil wir ein **verändertes** welle.io ausliefern (der Patch aus Abschnitt 10), muss der
  Quelltext des Forks samt Änderungen bei Weitergabe verfügbar sein. Ein öffentlicher Fork
  erledigt das.

---

## 5. Repo-Layout

```
asamon-rx/
├── TODO.md                     # dieses Dokument
├── README.md                   # kurz: was es ist, wie man es baut, wie man es startet
├── LICENSE                     # GPL-3.0
├── CMakeLists.txt
├── .gitignore                  # build/, .cache/, compile_commands.json
├── .gitmodules
├── docs/
│   ├── record-format.md        # Abschnitt 7 dieses Dokuments, ausgelagert und versioniert
│   └── welle-patches.md        # welche Patches der Fork trägt und warum
├── external/
│   └── welle.io/               # Submodul: eigener Fork, fester Commit
├── src/
│   ├── main.cpp                # CLI, Aufbau, Signale, Hauptschleife
│   ├── options.h/.cpp          # Kommandozeile und Konfiguration
│   ├── controller.h/.cpp       # RadioControllerInterface-Implementierung (die Rückrufe)
│   ├── record.h/.cpp           # Record-Rahmen, Serialisierung der Nutzlasten
│   ├── writer.h/.cpp           # Ausgabethread, beschränkte Warteschlange, Verwurfszähler
│   ├── commands.h/.cpp         # stdin-Zeilenkommandos einlesen und einreihen
│   └── recorder.h/.cpp         # SubChId → Service, FIFO, aud-Records
├── tests/
│   ├── test_record.cpp         # Serialisierung: Struktur → JSON-Zeile
│   └── fixtures/               # handgebaute FIBs mit FIG 0/15, siehe Abschnitt 12
└── contrib/
    ├── asamon-rx.service       # systemd-Unit (nur für Einzelbetrieb/Test)
    └── 99-asamon-rtlsdr.rules  # udev-Regel für Zugriff ohne root
```

---

## 6. Bauen

### Abhängigkeiten (Raspberry Pi OS / Debian, 64 bit)

```bash
sudo apt update
sudo apt install -y build-essential cmake git pkg-config \
    libfftw3-dev libfaad-dev libmpg123-dev librtlsdr-dev
```

### RTL-SDR nutzbar machen

Der Kernel greift sich den Stick sonst als DVB-T-Empfänger:

```bash
echo 'blacklist dvb_usb_rtl28xxu' | sudo tee /etc/modprobe.d/blacklist-rtl.conf
sudo modprobe -r dvb_usb_rtl28xxu 2>/dev/null || true
```

udev-Regel nach `/etc/udev/rules.d/99-asamon-rtlsdr.rules` (Vorlage in `contrib/`), damit der
Stick ohne root nutzbar ist; danach `sudo udevadm control --reload && sudo udevadm trigger`.
Prüfen mit `rtl_test -t` (aus `rtl-sdr` bzw. `librtlsdr-dev`).

### Submodul

```bash
git submodule add https://github.com/<uns>/welle.io external/welle.io
cd external/welle.io && git checkout <fester-commit> && cd -
```

Bis der Fork existiert (Meilenstein M0/M1 brauchen ihn nicht), reicht der Upstream
`AlbrechtL/welle.io` auf einem festen Commit von `next`.

**Der Commit wird hart festgenagelt** und in `docs/welle-patches.md` notiert. Ohne installierte
Header ist die welle-API **keine zugesagte Schnittstelle**; sie kann sich zwischen zwei Commits
ändern, ohne dass das irgendwo als Bruch auftaucht.

### CMakeLists.txt — die Punkte, auf die es ankommt

```cmake
cmake_minimum_required(VERSION 3.16)
project(asamon-rx LANGUAGES C CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# welle.io bringt sein eigenes project(), seine Optionen und install()-Regeln mit.
set(BUILD_WELLE_IO  OFF CACHE BOOL "" FORCE)
set(BUILD_WELLE_CLI OFF CACHE BOOL "" FORCE)
set(RTLSDR          ON  CACHE BOOL "" FORCE)   # Vorgabe ist OFF!
set(FDK_AAC         OFF CACHE BOOL "" FORCE)   # Lizenz, siehe Abschnitt 4
add_subdirectory(external/welle.io EXCLUDE_FROM_ALL)

add_executable(asamon-rx src/main.cpp src/controller.cpp src/record.cpp
                         src/writer.cpp src/commands.cpp src/recorder.cpp
                         src/options.cpp)

# PFLICHT: welle.io setzt keine target_include_directories, die Pfade kommen
# aus einem globalen include_directories() und propagieren nicht zu uns.
target_include_directories(asamon-rx PRIVATE
    external/welle.io/src
    external/welle.io/src/backend
    external/welle.io/src/input
    external/welle.io/src/various
    external/welle.io/src/libs/fec)

target_link_libraries(asamon-rx PRIVATE welle Threads::Threads)
target_compile_options(asamon-rx PRIVATE -Wall -Wextra -Wswitch)
```

`-Wswitch` (in `-Wall` enthalten) ist kein Beiwerk: In Verbindung mit `enum class` erzwingt es,
dass jeder Phase- und Stage-Wert behandelt wird. Ein unbehandelter Wert darf **nicht**
stillschweigend verschwinden.

**Erwartbare Reibung beim ersten Mal:** welle.io mischt seine `install()`-Regeln und Optionen
in unseren Bau. `EXCLUDE_FROM_ALL` und die `CACHE ... FORCE`-Schalter fangen das ein; wenn
Header fehlen, gehört auch das Build-Verzeichnis von welle.io in die Include-Pfade.

### Bauen

```bash
cmake -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j$(nproc)
```

Auf dem Pi 3 / Zero 2 W kann der Speicher beim Übersetzen knapp werden: dann `-j2` und
gegebenenfalls Swap. Auf dem Pi 4/5 nativ bauen ist der einfachste Weg; eine
Cross-Toolchain lohnt erst, wenn es weh tut.

---

## 7. Record-Format — hier festgelegt

Der Record-Strom ist zugleich **IPC-Protokoll**, **Archivformat** für Wiederholläufe und
**Beleg** zum Server. Deshalb ist er die Festlegung, an der alles Übrige hängt.

**Format: NDJSON — ein JSON-Objekt je Zeile, `\n`-terminiert, UTF-8.**

Diese Spezifikation gehört nach `docs/record-format.md` und wird ab dem ersten Commit
versioniert. Neue Felder dürfen jederzeit dazukommen; `format_version` steigt nur, wenn sich
die Bedeutung eines bestehenden Feldes ändert.

> **Warum JSON und nicht binär.** Das einzige Argument für ein Binärlayout waren 125
> `FIB`-Records je Sekunde. Rohe FIBs gehen nicht mehr über die Pipe (siehe unten), damit
> bleibt rund **ein Record je Sekunde** übrig — und JSON kauft dafür drei Dinge: Ein Mitschnitt
> ist in drei Jahren ohne Formatdokument lesbar; ein neues Feld bricht keinen alten Leser; und
> `asamon-node` kommt mit `encoding/json` aus der Standardbibliothek aus. Zeilenweise heißt
> außerdem, dass `grep`, `head`, `tail -f` und `jq` sofort funktionieren — deshalb gibt es
> **keinen Probe-Modus**: Wer wissen will, ob ein Ensemble einen Heartbeat sendet, schreibt
> `asamon-rx --channel 5C | jq 'select(.type=="asa")'`.

### Gemeinsame Felder in jedem Record

| Feld | Typ | Bedeutung |
|---|---|---|
| `type` | String | `init`, `tlm`, `ens`, `asa`, `aud` |
| `seq` | Zahl | Zähler je Strom, ab 0 — Lückenerkennung |
| `ts` | String | Knotenzeit als **RFC 3339 mit Nanosekunden**, z. B. `"2026-08-26T14:03:11.482913771Z"` |

> **`ts` ist ein String, keine Zahl.** Nanosekunden seit Epoche sind rund 1,8·10¹⁸ und
> überschreiten die 2⁵³ von `float64`. Jeder JSON-Leser, der über einen generischen Typ geht
> (`interface{}` in Go, `object` in Python), verlöre sonst Präzision — und zwar
> stillschweigend. RFC 3339 ist eindeutig und nebenbei lesbar.

### `init` — genau einmal, als erste Zeile des Stroms

Macht jede Aufzeichnung für sich allein erklärbar. Deshalb braucht **kein** anderer Record ein
Kanalfeld.

```json
{"type":"init","seq":0,"ts":"2026-08-26T14:03:11.482913771Z","format_version":1,
 "channel":"5C","freq_hz":178352000,"device":"rtl_sdr","device_serial":"",
 "rx_version":"0.1.0","rx_commit":"abc1234","welle_commit":"def5678"}
```

`device_serial` bleibt leer, solange Patch 2 (Abschnitt 10) fehlt.

### `tlm` — 1/s

```json
{"type":"tlm","seq":1,"ts":"…","snr":12.4,"sync":true,"signal":true,
 "freq_corr":{"fine":-3,"coarse":0},"fib_total":125,"fib_crc_err":2,
 "dropped":0,"parse_errors":0,"eid":"0x10FF",
 "ens_time":"2026-08-26T14:03:11Z","ens_offset_min":60}
```

`fib_total` und `fib_crc_err` zählen die letzte Sekunde. Die Quote ist die Größe, mit der sich
**„Ensemble sendet keinen Heartbeat" von „wir empfangen schlecht"** trennen lässt — und darauf
beruht die Abdeckungskarte, das Kernergebnis des Projekts. Die Differenz zwischen `ts` und
`ens_time` (aus FIG 0/10) ist selbst eine Messgröße.

**`tlm` geht auch dann raus, wenn nichts empfangen wurde** — dann mit `fib_total: 0`. Sonst
kann der Server „Ensemble schweigt" nicht von „Knoten ist tot" unterscheiden.

### `ens` — bei Änderung

Ensemble-Label, EId, ECC, dazu je Service SId und Label und je Komponente `subch_id`,
`start_addr`, `size`, `protection`, `bitrate`. Genau dieser Record ist der Grund, warum
`asamon-node` FIG 0/1 und 0/2 **nicht** selbst parsen muss.

### `asa` — 1/s im Ruhezustand, im Alarmfall bis 12/s

Der Kern. Eine FIG-0/15-Instanz, ausgepackt, **ungedeutet**.

```json
{"type":"asa","seq":42,"ts":"…","heartbeat":true,"cn":true,"oe":false,
 "pd_second_half":false,"raw":"000f"}

{"type":"asa","seq":43,"ts":"…","heartbeat":false,"cn":false,"oe":false,
 "pd_second_half":true,"phase":"trigger","subch_id":7,
 "stage":"level1_start","iid":3,"last":true,"nff":0,
 "location_codes":"1a2b3c4d","raw":"030f4703a1…"}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `heartbeat` | Bool | Längenfeld des FIG-Headers == 1, leeres Type-0-Feld |
| `cn`, `oe`, `pd_second_half` | Bool | die drei Header-Flags; `pd_second_half` ist das zweckentfremdete P/D |
| `phase` | String | `pre_trigger`, `trigger`, `sustain`, `end` — fehlt beim Heartbeat |
| `subch_id` | Zahl | nur bei `oe: false` |
| `other_eid` | String | nur bei `oe: true`, die 16-bit-EId als `"0x10FF"` |
| `sec` | Zahl | nur bei `phase: "pre_trigger"`; 63 ist Sonderwert |
| `stage` | String | `level1_start`, `level1_update`, `level1_repeat`, `level1_critical`, `level2_start`, `level2_update`, `level2_repeat`, `test` |
| `iid`, `last`, `nff` | Zahl/Bool | Incident Identifier, Last-Flag, Anzahl folgender Instanzen |
| `location_codes` | String | Hex, **roh und ungedeutet** — die Geometrie macht `asamon-node` |
| `raw` | String | Hex der gepackten FIG-Bytes einschließlich beider Header, ≤ 31 B |

**Unbekannte Aufzählungswerte werden gemeldet, nicht verworfen.** Trifft der Parser einen
Stage- oder Phase-Wert, für den es keinen Namen gibt, schreibt er statt des Namens ein
`"stage_raw": 5` und zählt den Fall in `parse_errors`. Ein unerwarteter Wert ist eine
meldenswerte Beobachtung, kein Fehler.

**`raw` ist nicht optional.** Es gibt keine Referenzimplementierung von FIG 0/15; die einzige
vollständige, die existiert (WarnBridge), liest Id- und Status-Feld vertauscht und liefert
trotzdem plausibel aussehende Werte. 30 Byte je Ereignis sind der Preis dafür, dass man sich
irren darf.

### `aud` — nur im Alarmfall

```json
{"type":"aud","seq":812,"ts":"…","subch_id":7,"chunk":12,"data":"<base64>"}
```

Der rohe Subchannel-Bitstrom, **nicht** dekodiertes Audio. Bei 32 kbit/s (EEP 2-A, wie für
„ASA DE" auf 5C geplant) sind das 4 kB/s, mit Base64 rund 5,3 kB/s — eine zweiminütige Meldung
wächst von 480 kB auf 640 kB. Das ist der Preis dafür, ein Format zu haben statt zwei, und er
ist hier vertretbar.

### Was **nicht** übertragen wird

**Rohe FIBs — weder dauernd noch auf Anforderung.** Es gibt keinen FIB-Ring, kein `FIBDUMP`,
kein `FIBSTREAM`. Über die Pipe geht ausschließlich der geparste Record.

Damit fällt die Möglichkeit weg, nachträglich zu belegen, dass der FIB-Walk nichts verschluckt
hat. Für die ersten Meilensteine ist das eine Frage der Fehlersuche, und dafür ist ein Log das
richtige Werkzeug: **Parserfehler gehen nach stderr und in den `parse_errors`-Zähler im `tlm`.**
Eine ausgearbeitete Fehlerbehandlung ist Sache späterer Meilensteine.

Rückholbar ist das jederzeit ohne Formatbruch — ein zusätzlicher `type` und ein zusätzliches
Kommando sind additiv. Die Dauerlast auf der Pipe liegt damit bei einigen hundert Byte je
Sekunde.

---

## 8. Kommandos auf stdin

ASCII, eine Zeile je Kommando, `\n`-terminiert. Unbekannte Zeilen werden **gezählt und
geloggt**, nicht stillschweigend verworfen.

| Kommando | Wirkung |
|---|---|
| `REC <subChId>` | SubChId → Service auflösen, `addServiceToDecode()` mit FIFO-Ziel |
| `STOP <subChId>` | `removeServiceToDecode()` |
| `QUIT` | sauber herunterfahren |

**Die Auflösung SubChId → Service passiert auf dem Kommando-Thread, nie in einem Rückruf**
(Abschnitt 3, Falle 3).

### Gegendruck

Die Prozessgrenze bringt eine Gefahr mit: Liest die Gegenstelle nicht schnell genug, läuft der
Pipe-Puffer voll (unter Linux 64 kB) und `write()` blockiert. Passiert das auf einem
welle.io-Thread, verlieren wir Samples. Deshalb:

- Ein **eigener Ausgabethread** mit beschränkter Warteschlange.
- Im Überlauf wird **verworfen, nicht blockiert** — und der Verwurf gezählt und im nächsten
  `TLM` gemeldet. Eine Lücke im Strom muss sichtbar sein, nicht stillschweigend.
- **Vorrang beim Verwerfen: `asa` vor `aud` vor `tlm`.** Ein verworfener `asa`-Record ist ein
  verlorenes Ereignis; eine Lücke im Warn-Audio ist unwiederbringlich; ein verworfener
  `tlm`-Record ist eine Zeile Statistik.

Weil im Ruhezustand nur rund ein Record je Sekunde fließt, ist der Überlauf praktisch auf den
Alarmfall beschränkt — `aud` mit 5,3 kB/s ist das Einzige, was den 64-kB-Pipe-Puffer überhaupt
noch spürbar füllt.

---

## 9. Kommandozeile

```
asamon-rx --channel 5C [Optionen]

  --channel <name>       DAB-Kanal, z. B. 5C, 11D, 7B (Pflicht)
  --device <name>        rtl_sdr (Vorgabe) | rtl_tcp | airspy | soapysdr | rawfile | auto
  --iq-file <pfad>       Quelle für --device rawfile
  --gain auto|<index>    Vorgabe: auto
  --log-level <stufe>    error|warn|info|debug (Vorgabe: info)
  --version
```

**Records gehen nach stdout, Logs nach stderr.** Immer, ohne Ausnahme — sonst zerschießt die
erste Logzeile den Record-Strom.

### Betriebsarten

| Modus | Quelle | Zweck |
|---|---|---|
| Live | SDR | regulärer Knotenbetrieb |
| Replay | `--device rawfile --iq-file …` | dieselbe Kette ohne Funk; Regressionstests. Klasse `CRAWFile`, Schnittstelle in `src/input/raw_file.h` prüfen |

**Es gibt keinen Probe-Modus.** Er wäre eine zweite Ausgabeform, die man synchron halten
müsste — und NDJSON ist bereits lesbar genug. Die Frage, für die es ihn gab, beantwortet:

```bash
asamon-rx --channel 5C | jq -c 'select(.type=="asa")'
asamon-rx --channel 5C | jq -r 'select(.type=="tlm") | "\(.snr) dB  \(.fib_crc_err)/\(.fib_total) CRC-Fehler"'
```

Das ist zugleich der erste sinnvolle Feldtest und braucht weder Server noch Protokoll noch
`asamon-node`.

---

## 10. Der welle.io-Fork

Für den Normalbetrieb wären **null** Änderungen am Backend nötig — mit einer Ausnahme, und die
ist der Kern des Ganzen.

### Patch 1 — FIG 0/15 im `FIBProcessor` (**Voraussetzung**)

Drei Dateien. Vollständige Begründung in `client-architektur.md` Abschnitt 2b, Bitlayout in
`asa.md` Abschnitt 3.

**`src/backend/radio-controller.h`** — Struct neben `dab_date_time_t`/`mot_file_t`, dazu ein
Rückruf im `RadioControllerInterface`:

```cpp
struct asa_alert_t {
    bool     cn = false;               // C/N (SIV)
    bool     oe = false;               // 0 = eigenes Ensemble, 1 = anderes
    bool     secondHalfMinute = false; // P/D, zweckentfremdet: Sekunden 30-59
    bool     heartbeat = false;        // Längenfeld == 1, leeres Type-0-Feld
    uint8_t  phase    = 0;             // 0 Pre-trigger, 1 Trigger, 2 Sustain, 3 End
    uint8_t  subChId  = 0;             // gültig bei oe == false
    uint16_t otherEId = 0;             // gültig bei oe == true
    bool     hasSec   = false;
    uint8_t  sec      = 0;
    bool     hasStatus = false;
    bool     last      = false;
    uint8_t  stage     = 0;            // 0-7, 7 = Test
    uint8_t  iid       = 0;            // 0-15
    uint8_t  nff       = 0;
    std::vector<uint8_t> locationCodes;  // roh, ungedeutet
    std::vector<uint8_t> raw;            // gepackte FIG-Bytes inkl. beider Header, <= 31 B
};

/* A FIG 0/15 (EWS/ASA, ETSI TS 104 089) was decoded.
 * Deliberately not pure virtual: existing controllers stay unaffected. */
virtual void onAsaAlert(const asa_alert_t& alert) { (void)alert; }
```

**Nicht `= 0`.** Alle übrigen `on…` sind rein virtuell; ein weiteres würde jeden Implementierer
im Baum brechen (`welle-io`, `welle-cli`, Tests). Vorbild im Bestand: `onInputFailure()` und
`onRestartService()` sind aus demselben Grund schon nicht rein virtuell. Das hält den Patch bei
drei Dateien — und macht ihn upstream-tauglich.

**`src/backend/fib-processor.h`** — eine Zeile im privaten Teil, zwischen `FIG0Extension14`
und `FIG0Extension16`:

```cpp
void FIG0Extension15(uint8_t *);
```

**`src/backend/fib-processor.cpp`** — der `case` in `process_FIG0()` plus der Parser:

```cpp
case 15: FIG0Extension15 (d); break;
```

Für den Parser gilt:

- Heartbeat steht im **Längenfeld des FIG-Headers**, nicht im Type-0-Feld:
  `const uint8_t figLength = getBits_5(d, 3); alert.heartbeat = (figLength == 1);`
  Genau diesen Fall verwirft der einzige spec-konforme Fork-Parser, den es sonst gibt
  (Qt-DAB, `if (CN_bit == 1) return;`) — und er ist für die Abdeckungskarte der wertvollste.
- Bitpositionen strikt nach `asa.md` Abschnitt 3. **Nicht** aus vorhandenen Fork-Parsern
  abschreiben: Der eine ist unvollständig, der andere hat Id- und Status-Feld vertauscht.
- `enum class` für Phase und Stage, `switch` mit ausdrücklichem `default`, das den Fall
  **zählt und meldet** statt ihn zu verwerfen. Ein unbekannter Stage-Wert ist eine
  meldenswerte Beobachtung, kein Fehler.
- Der Parser **deutet nicht**: keine Alert-Sets, keine Phasenverläufe, keine
  Location-Geometrie.

Der Fork sollte so geschnitten sein, dass daraus ein **Pull Request** werden kann: ein Commit,
keine Vermischung mit projektspezifischem Code. In welle.io gibt es zu EWS/ASA/FIG 0/15 bislang
**keinen einzigen** PR. Wird der Patch angenommen, verschwindet die Fork-Last ganz — das ist
der einzige Weg, auf dem sie je verschwindet.

### Patch 2 — Geräteauswahl über die Seriennummer (**nur für Mehrkanalbetrieb**)

`CRTL_SDR::open_device()` nimmt das erste Gerät, das sich öffnen lässt. Mit zwei Sticks im
Knoten hängt es damit von der Startreihenfolge ab, welcher Prozess welchen Kanal bekommt —
nicht reproduzierbar, und nach einem Neustart womöglich vertauscht.

Nötig ist ein Weg, `rtlsdr_get_device_usb_strings()` auszuwerten und gezielt zu öffnen.
`CVirtualInput::setDeviceParam(DeviceParam, const std::string&)` existiert bereits als
Überladung mit Vorgabeimplementierung — ein neuer `DeviceParam::RtlSdrSerial` wäre der
kleinste Eingriff.

**Bis dahin: nur ein Stick je Knoten.** In `docs/welle-patches.md` festhalten.

### Später, nicht jetzt

- Rückruf statt Datei für den MSC-Dump, falls sich die FIFO im Betrieb als zu heikel erweist.
- `decode_audio` durchreichen (`SuperframeFilter(this, true, false)`, erstes Argument) — spart
  bei 32 kbit/s kaum Rechenzeit, und FAAD2 bliebe wegen `find_package(Faad REQUIRED)` ohnehin
  Bauabhängigkeit.
- Zugriff auf die Subchannel-Ebene ohne Service-Umweg, falls ein Ensemble den Warn-Subchannel
  ohne Eintrag in FIG 0/2 sendet.

---

## 11. Meilensteine

Jeder Meilenstein endet mit etwas, das **auf dem Pi vorführbar** ist.

### M0 — Bauen und tunen  ·  *die erste Binary*

- Repo-Gerüst, `LICENSE`, `.gitignore`, CMake, welle.io als Submodul (**Upstream genügt**).
- `Controller : RadioControllerInterface` mit allen Rückrufen; die meisten leer.
- `main.cpp`: Kanal → Frequenz über `Channels`, Gerät über `CInputFactory::GetDevice`,
  `RadioReceiver`, `restart(false)`.
- Ausgabe nach stderr: SNR, Sync-Zustand, Ensemble-Label, gefundene Services.

**Fertig, wenn:** `./asamon-rx --channel 5C` auf dem Pi das Ensemble-Label und eine
Serviceliste nach stderr zeigt, mit plausiblem SNR. Damit sind Toolchain, Stick, Antenne und Bau bewiesen —
und die häufigsten Startfallen (`RTLSDR=ON`, udev, Kernelmodul) sind abgeräumt.

### M1 — Record-Strom

- `record.cpp`: Rahmen und Serialisierung nach Abschnitt 7.
- `writer.cpp`: Ausgabethread, beschränkte Warteschlange, Verwurfszähler.
- `init` beim Start, `tlm` im Sekundentakt (**auch leer**), `ens` bei Änderung.
- Logs strikt nach stderr — die erste Logzeile auf stdout zerschießt den Strom.

**Fertig, wenn:** `./asamon-rx --channel 5C | jq -c .` eine Zeile `init`, danach im
Sekundentakt `tlm` und bei Bedarf `ens` zeigt — und `jq` keine Zeile als ungültig ablehnt. Und
wenn das bei **abgezogener Antenne** ebenfalls funktioniert, mit `fib_total: 0`.

### M2 — FIG 0/15  ·  *der eigentliche Zweck*

- welle.io-Fork anlegen, Patch 1 anwenden, Submodul umhängen, Commit festnageln.
- `onAsaAlert()` im Controller, `asa`-Records.
- Unbekannte Phase- und Stage-Werte als `*_raw` melden und in `parse_errors` zählen.

**Fertig, wenn:** `./asamon-rx --channel 5C | jq -c 'select(.type=="asa")'` auf dem Pi
entweder im Sekundentakt Heartbeats zeigt — oder belegt, dass keine kommen, unterscheidbar von
schlechtem Empfang über die CRC-Quote im `tlm`. **Beides ist ein Ergebnis.**

> **Konkreter Prüfpunkt:** Das Schwesterprojekt WarnBridge behauptet in `asa_watch.py`, auf
> **5C (Bundesmux)** komme alle 5 Minuten für 30 s ein **Test-Alert**. Das ist in einer halben
> Stunde nachgemessen und wäre sofort ein reproduzierbarer Testfall. Nachmessen, nicht glauben —
> der dortige Parser hat ein falsches Bitlayout.

### M3 — Recorder

- `REC <subChId>` → SubChId gegen `ServiceComponent.subchannelId` aus `getComponents()`
  auflösen, `addServiceToDecode()` mit FIFO-Ziel. **Leser steht vor dem Zuschalten**
  (Abschnitt 3, Falle 4).
- FIFO lesen, in `aud`-Records stückeln (Base64), `STOP` sauber abbauen, hartes Zeitlimit als
  Notbremse.

**Fertig, wenn:** `REC` auf einen beliebigen Audio-Subchannel eines empfangbaren Ensembles
einen `aud`-Strom liefert, der sich — Base64 dekodiert und aneinandergehängt — zu hörbarem
Audio zusammensetzen lässt (Gegenprobe mit `dablin` oder `welle-cli`). Ein echter Alert ist
dafür nicht nötig.

### M4 — Härtung

- `SIGINT`/`SIGTERM` sauber, `stop()` auf dem `RadioReceiver`, FIFO abbauen.
- Verwurfszähler, CRC-Quote und `parse_errors` im `tlm`; Vorrangregel beim Verwerfen.
- `systemd`-Unit in `contrib/` mit Watchdog; `README.md` mit Bau- und Startanleitung.
- Dauerlauf über 24 h auf dem Pi, Speicher- und CPU-Verlauf mitschreiben.

**Fertig, wenn:** 24 h ohne Speicherwachstum, ohne Verwürfe im Normalbetrieb, und ein
`kill -TERM` beendet den Prozess in unter einer Sekunde ohne verwaiste FIFO.

---

## 12. Testen

- **Einheitstests ohne SDR.** `FIBProcessor::processFIB(uint8_t *p, uint16_t fib)` ist
  **public**. Ein Test kann handgebaute FIBs direkt einspeisen — ohne Funk, ohne Prozessgrenze,
  ohne Aufzeichnung. Das ist der wichtigste Testpfad des ganzen Repos, denn der Parser hat
  keine Referenzimplementierung — und seit rohe FIBs nicht mehr übertragen werden, ist es
  zugleich die **einzige** Stelle, an der sich der Parser gegen die Norm prüfen lässt. Nicht
  sparen.
- **Testfälle** aus **ETSI TS 104 090** (`specs/etsi/ts_104090v010102p.pdf`): sieben Szenarien
  — Set-up, Alert-Area-Matching, Stages, OE-Alerts, Sleep-Mode-Auswahl und -Reaktion,
  gleichzeitige Alerts. Als Fixtures nach `tests/fixtures/`.
- **Vergessene Fälle, die getestet gehören:** Heartbeat (Länge 1), OE = 1 (Id-Feld ist die
  16-bit-EId), Pre-trigger mit `sec == 63`, Stage 7 (Test), `nff > 0` mit mehreren Instanzen,
  Location Codes maximaler Länge (25 Byte).
- **Fuzzing** über denselben Einstiegspunkt (libFuzzer). Ein Bitparser ohne
  Referenzimplementierung ist genau der Fall, für den sich das lohnt.
- **Replay** über `--device rawfile`: IQ-Mitschnitte konservieren echte Wirklichkeit und machen
  Regressionen prüfbar, sobald einmal ein Alert empfangen wurde. **Jeden Mitschnitt aufheben** —
  bis zum Regelbetrieb sind Ereignisse rar.
- **Gegenprobe** mit `eti-cmdline` (aus `JvanKatwijk/eti-stuff`) und `etisnoop`: unabhängiger
  Decodierpfad für dasselbe Signal. Als Werkzeug auf dem Entwicklungsrechner gesetzt, nicht auf
  dem Knoten.

---

## 13. Zeitlicher Kontext

**Am 10.09.2026 startet der ASA-Regelbetrieb in Deutschland** (bundesweiter Warntag). Bis dahin
läuft Testbetrieb, echte Aussendungen sind rar. Das hat zwei Folgen für die Reihenfolge:

- **M2 ist der wichtigste Meilenstein**, nicht M4 oder M5. Was jetzt zählt, ist die Antwort auf
  „sendet dieses Ensemble überhaupt?" — sie ist bisher niemandem bekannt.
- **Der Testdatenpfad gehört früh gesichert.** Wer erst nach dem 10.09. anfängt, Mitschnitte zu
  sammeln, hat die Einführungsphase verpasst und kann sie nicht nachholen.

---

## 14. Offene Punkte

- **Wie viele Sticks trägt ein Pi?** Die Grenze dürfte nicht die Rechenlast sein, sondern
  USB-Bandbreite und Speisestrom. Nur am Gerät messbar. Hängt an Patch 2.
- **Lastprofil messen, nicht schätzen:** FIC dauerhaft plus ein Subchannel im Alarmfall, gegen
  die Volldecodierung des Multiplex (`eti-cmdline-rtlsdr -C 5C`). Auf Pi 3 und Zero 2 W.
- **Verhalten beim Abziehen eines Sticks im Betrieb.** `onInputFailure()` ist der Haken;
  `rtlsdrUnplugged` existiert in `CRTL_SDR`. Was `asamon-rx` dann tut, ist offen.
- **Warn-Subchannel ohne Service-Eintrag in FIG 0/2.** Für „ASA DE" auf 5C ist der Fall nicht
  zu erwarten (eigener Service, 32 kbit/s EEP 2-A). Ein Monitor sollte ihn trotzdem behandeln —
  dann bräuchte es direkten Zugriff auf die Subchannel-Ebene.
- **`asa_alert_t` versionieren.** Die Struktur erfinden wir selbst, und sie muss zwischen
  Fork-Stand und `asamon-rx` zusammenpassen. Ohne Versionsfeld oder Bausymbol passt eines Tages
  ein alter `asamon-rx` zu einem neuen Fork — und es fällt erst beim Alert auf.
- **Belegbarkeit über den Parser hinaus.** Rohe FIBs werden bewusst nicht übertragen. Damit
  lässt sich nicht nachweisen, dass der FIB-Walk nichts verschluckt hat, und die Aussage
  „Ensemble X sendet keinen Heartbeat" stützt sich allein auf die CRC-Quote. Für die ersten
  Meilensteine reicht das; ob später ein Record-Typ für rohe FIBs dazukommt, ist offen. Der
  Schritt wäre **additiv** und bräche kein Format.
- **Zeitbasis.** NTP/chrony auf dem Knoten ist Voraussetzung: `ts_ns` gegen die Ensemble-Zeit
  aus FIG 0/10 ist eine Messgröße, und alle ASA-Alerts sollen an der **Minutengrenze**
  beginnen.

---

## 15. Was dieses Repo nicht entscheidet

Ingest-Protokoll und Datenmodell zwischen Knoten und Server, Vertrauens- und Verifikationsmodell
für Crowd-Daten, Server-Stack. Das gehört nach `asamon-node` beziehungsweise auf die
Serverseite. Wenn beim Arbeiten hier eine Festlegung nötig scheint, die über den Record-Strom
hinausgeht: **nicht hier treffen**, sondern in `T:\dev\asa-monitor\specs\` notieren.
