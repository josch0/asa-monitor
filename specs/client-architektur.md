# Architekturskizze: CLI-Knoten auf Basis des welle.io-Backends

Zuschnitt für den Empfangsknoten (Architekturvariante **V2c** aus
[`decoder-optionen.md`](decoder-optionen.md): eigenes CLI gegen die welle.io-Bibliothek,
diese mit **einem** Patch). **Keine Umsetzung** — Skizze und Begründung. Wo eine Festlegung
getroffen ist, steht sie ausdrücklich als solche da (FIG-0/15-Parser im Backend, Abschnitt 1;
Sprache für `asa-node`, Abschnitt 4a); alles Übrige ist Vorschlag.

Stand: 26.08.2026. Alle genannten Signaturen sind im Zweig `next` von welle.io geprüft
(`CMakeLists.txt`, `src/backend/radio-controller.h`, `radio-receiver.h`, `dab-constants.h`,
`fib-processor.h`, `fib-processor.cpp`, `fic-handler.cpp`, `decoder_adapter.cpp`,
`dabplus_decoder.h`, `dab-audio.cpp`).

> ### Richtungsentscheidung 26.08.2026 — FIG 0/15 wandert ins Backend
>
> Eine frühere Fassung dieses Dokuments ließ das Backend **unverändert**: rohe FIBs über
> `onFIBDecodeSuccess()` durch die Pipe, FIG-0/15-Parser in `asa-node`. Das ist verworfen.
> Der `FIBProcessor` zerlegt jeden FIB ohnehin schon in FIGs und ruft je Extension einen
> Handler auf — **dort ergänzen wir FIG 0/15 und einen Rückruf**, statt denselben Rohstrom
> hinter der Prozessgrenze ein zweites Mal zu zerlegen.
>
> Der Grund ist die Prozessgrenze: Der Record-Strom schrumpft von **125 FIB/s ≈ 5,6 kB/s auf
> rund einen Record je Sekunde**. Damit verschwindet der Fall, der den Entwurf bisher
> belastete — `asa-node` liest zu langsam, der 64-kB-Pipe-Puffer läuft voll, `asa-rx`
> blockiert im `write()` auf einem welle.io-Thread, Samples gehen verloren.
>
> Betroffen sind die Abschnitte 1, 2, 2a, 3, 4a, 5 und 6; sie sind unten entsprechend gefasst.
> Die Aussage „null Änderungen am Backend" gilt nicht mehr: Es ist genau **ein** Patch, und er
> ist Voraussetzung, kein Kandidat (Abschnitt 2b). Unverändert gültig bleiben die
> Nachprüfungen an `next` — `onFIBDecodeSuccess`, die `RadioReceiver`-API einschließlich
> `dumpFileName`, das Bit-je-Byte-Format des FIB-Puffers, der `SuperframeFilter`-Konstruktor,
> und der Dump-`fwrite` in `decoder_adapter.cpp`, der **nach** `decoder->Feed()` steht und
> denselben Rohpuffer bekommt.

Bildfassung mit Diagrammen: https://claude.ai/code/artifact/ef56f41d-412e-496c-808a-2aa7d2f0098f
— auf diesem Stand, mit einem zusätzlichen Diagramm des FIG-0/15-Bitlayouts nach Annex E.

---

## 1. Die zentrale Entscheidung: FIG 0/15 wird im Backend geparst

`FIBProcessor::processFIB()` bekommt jeden **CRC-geprüften** FIB, zerlegt ihn in FIGs und
verteilt FIG-Typ 0 über eine `switch`-Tabelle auf je einen Handler. Was dort entsteht, geht als
Rückruf an das `RadioControllerInterface` — `onNewEnsemble()`, `onDateTimeUpdate()`,
`onServiceDetected()`. FIG 0/15 fällt heute in den `default`-Zweig:

```cpp
// fib-processor.cpp, FIBProcessor::process_FIG0()
uint8_t extension = getBits_5 (d, 8 + 3);
switch (extension) {
    case 0: FIG0Extension0 (d); break;
    /* ... 1, 2, 3, 5, 8, 9, 10, 13, 14, 17, 18, 19, 21 ... */
    case 22: FIG0Extension22 (d); break;
    default:                       // <- hier fällt FIG 0/15 heute hinein
        break;
}
```

**Wir ergänzen genau diesen einen Zweig** und einen Rückruf `onAsaAlert()`. Damit liefert das
Backend ASA-Ereignisse in derselben Form wie alles andere, und über die Prozessgrenze gehen
**geparste Ereignisse statt eines Rohstroms**.

### Was das trägt

- **Die Prozessgrenze wird unkritisch.** Rund ein Record je Sekunde statt 125. Der
  Gegendruckfall — voller Pipe-Puffer, blockierender `write()` auf einem welle.io-Thread,
  Samplverlust — ist damit kein Betriebsrisiko mehr, sondern nur noch eine Vorsichtsmaßnahme
  (Abschnitt 4a).
- **Kein zweiter FIB-Zerleger.** FIG-Grenzen, Längenfeld, das Bit-je-Byte-Format des Puffers,
  der Abbruch bei FIG-Typ 7 — all das ist im Backend erledigt und erprobt. `asa-node` hätte es
  nachbauen müssen, für dasselbe Ergebnis.
- **Der Parser sitzt neben seinem Kontext.** FIG 0/15 nennt einen SubChId; Subchannels,
  Komponenten und Services liegen im selben Objekt. Das ist ein Vorteil — mit einer
  Einschränkung, die man kennen muss (Abschnitt 2b, „Der Mutex ist die Falle").

### Was es kostet — offen benannt

- **Der Patch ist dauerhaft.** Damit greift das Kriterium aus Abschnitt 2a: Aus einem
  eingebundenen Quellbaum *ohne* Änderungen wird einer *mit* Patch-Serie, und das ist genau
  der Fall, für den dort der Fork vorgesehen war. Konsequenz in Abschnitt 2a.
- **Der Roh-Beleg schrumpft auf das FIG selbst.** `client-konzept-review.md` §3.4 verlangt
  rohe FIBs, weil es keine Referenzimplementierung gibt und ein falscher Parser plausibel
  aussehende Werte liefert. Davon bleibt: Der `asa`-Record trägt die rohen **FIG**-Bytes immer
  mit (≤ 31 B), damit ein Parserfehler nachträglich reparabel ist. Rohe **FIBs** gehen gar
  nicht mehr über die Pipe — kein Ring, kein Abruf (Abschnitt 4a).
- **Der Bitparser ist C++, nicht Go.** Ein Teil der Go-Begründung aus Abschnitt 4a —
  `go test -fuzz` für einen Bitparser ohne Referenzimplementierung — verliert damit sein
  Objekt. Ausgleich in Abschnitt 2b.

### Die drei Berührungspunkte bleiben — einer ändert seine Form

```
┌─ welle.io-Backend, ein Patch ──────┐        ┌─ asa-node, eigener Code ──────────┐
│                                    │        │                                   │
│  SDR-Frontend                      │        │                                   │
│  RTL-SDR · Airspy · SoapySDR       │        │                                   │
│  rtl_tcp · IQ-Datei                │        │                                   │
│        │                           │        │                                   │
│        v                           │        │                                   │
│  OFDM-Demodulator                  │        │                                   │
│        │                           │        │                                   │
│        ├─────────────┐             │        │                                   │
│        v             ┊             │        │                                   │
│  FIC-Handler         ┊             │        │                                   │
│        │ nur CRC ok  ┊             │        │                                   │
│        v             ┊             │        │                                   │
│  FIBProcessor        ┊             │        │                                   │
│   ├─ 0/0 0/1 0/2 0/10┊             │        │                                   │
│   │  1/x (Bestand)   ┊             │        │                                   │
│   └─ FIG 0/15        ┊             │        │                                   │
│        │ ← der Patch ┊             │  (1)   │  ASA-Zustandsmaschine             │
│        └─────────────┊─────────────┼───────>│  Alert-Sets · Phasen · Dedup      │
│                      ┊             │        │  Location-Decoding → GeoJSON      │
│                      ┊             │        │        │                          │
│                      ┊             │        │        ├──────────────────┐       │
│                      v             │  (2)   │        v                  │       │
│  MSC-Handler <─────────────────────┼────────┤  Recorder-Steuerung       │       │
│        │                           │        │                           │       │
│        v                           │  (3)   │                           │       │
│  Subchannel-Decoder ───────────────┼───────>│  Superframe-Packetizer    │       │
│  nur im Alarmfall aktiv            │        │        │                  │       │
│                                    │        │        v                  v       │
│                                    │        │  Puffer · Spool · Signatur        │
└────────────────────────────────────┘        └───────────────────────────────────┘

(1) onAsaAlert(const asa_alert_t&)  — geparstes FIG 0/15 + rohe FIG-Bytes; ~1/s im
                                      Ruhezustand, im Alarmfall bis 12/s
(2) addServiceToDecode(...)         — SubChId → Service, bei Trigger
(3) .msc → FIFO                     — roher Subchannel-Bitstrom
```

Alles an Pfad (2) und (3) läuft **ausschließlich während eines Alerts**. Im Normalbetrieb
dekodiert der Knoten nur den FIC.

Daneben läuft `onFIBDecodeSuccess()` weiter, belastet die Prozessgrenze aber gar nicht mehr:
Gebraucht wird daraus nur noch das Bit `crcCheckOk` für die FIC-CRC-Statistik. Der
bit-je-Byte-Puffer wird nicht einmal angefasst.

---

## 2. Was das Backend hergibt

Bis auf die erste Zeile ist das alles Bestand, unverändert nutzbar. Die erste Zeile ist der
Patch aus Abschnitt 2b.

| Schnittstelle | Verwendung im Knoten |
|---|---|
| `onAsaAlert(const asa_alert_t&)` — **neu, Abschnitt 2b** | **Hauptzugang.** Ein geparstes FIG 0/15 je Rückruf, samt roher FIG-Bytes. Ersetzt den Weg „roher FIB-Strom über die Pipe, Parser in `asa-node`" |
| `onFIBDecodeSuccess(bool, const uint8_t*)` | **Nicht mehr Hauptzugang, aber weiter gebraucht.** Wird für *jeden* FIB gerufen, auch für den mit CRC-Fehler — `processFIB()` dagegen nur für gültige (`fic-handler.cpp`, Z. 215–229). Daraus zieht `asa-rx` die **FIC-CRC-Quote** — mehr nicht; die Nutzlast bleibt unberührt. Die Quote trennt „Ensemble sendet keinen Heartbeat" von „wir empfangen schlecht" — genau die Unterscheidung, die `client-konzept-review.md` §3.2 verlangt, und dafür braucht sie die Nutzlast nicht |
| `onDateTimeUpdate(dab_date_time_t)` | Ensemble-Zeit aus FIG 0/10; Versatz gegen die NTP-Zeit des Knotens ist selbst Messgröße |
| `onNewEnsemble(uint16_t)`, `onSetEnsembleLabel(DabLabel&)` | EId für Zuordnung und OE-Auflösung, Label für die Anzeige |
| `onSNR(float)`, `onSyncChange(char)`, `onFrequencyCorrectorChange(int,int)` | Empfangstelemetrie für den 10-s-Batch |
| `getServiceList()`, `getComponents(Service)`, `getSubchannel(ServiceComponent)` | **Löst die SubChId-Lücke.** `ServiceComponent.subchannelId` gegen den signalisierten SubChId vergleichen → zugehöriger Service, ohne Eingriff ins Backend |
| `addServiceToDecode(handler, dumpFileName, service)` | Schaltet den Warn-Subchannel zu. `dumpFileName` schreibt **den rohen MSC-Strom, nicht dekodiertes Audio**: In `DecoderAdapter` geht derselbe Puffer an `decoder->Feed()` und an `fwrite` — der Dump hängt also nicht am Decoder-Ergebnis |
| `removeServiceToDecode(service)` | Abschalten nach der End-Phase, mit Nachlauf und hartem Zeitlimit |
| `onNewDynamicLabel(string)`, `onMOT(mot_file_t)` | DLS/DL+ und SlideShow zur Warnmeldung — billig, sobald der Subchannel ohnehin läuft |
| `onFrameErrors`, `onRsErrors`, `onAacErrors` | Qualität des Mitschnitts |
| `onNewAudio(...)` | Dekodiertes PCM — **bewusst ignoriert**, übertragen wird der Rohbitstrom |

### Der FIFO-Kniff

> **Überholt seit dem 27.08.2026.** Der Patch-Kandidat, den dieser Abschnitt am Ende benennt,
> ist gebaut: `onMscData()` reicht den rohen Strom als Rückruf heraus, und `asamon-rx` nimmt
> ihn so entgegen. Ausgelöst hat es nicht der Betrieb, sondern der Windows-Port. Der Abschnitt
> bleibt als Entwurfsstand stehen; der Weg von heute steht in
> `asamon-rx/docs/welle-patches.md` (Patch 3) und `asamon-rx/TODO.md` Abschnitt 18.

`dumpFileName` darf auf eine benannte Pipe zeigen. Damit landet der rohe Subchannel-Strom im
eigenen Prozess, ohne dass dieser Pfad Backend-Code braucht — der Patch aus Abschnitt 2b
betrifft nur den FIC, nicht den MSC.

Der Preis: welle.io öffnet mit `fopen(..., "wb")`, das **blockiert, bis ein Leser da ist** —
unser Leser muss also vor dem Zuschalten stehen, und ein hängender Leser blockiert den
Decoder-Thread. Erweist sich das im Betrieb als zu heikel, ist genau hier der erste
Patch-Kandidat fällig: ein Rückruf statt einer Datei (Abschnitt 6).

### Die AAC-Dekodierung abschalten wäre eine Zeile

`addServiceToDecode` dekodiert den Subchannel vollständig, auch wenn wir nur den Rohstrom
wollen. Der Schalter dafür ist aber schon da. `dabplus_decoder.h` deklariert

```cpp
SuperframeFilter(SubchannelSinkObserver* observer, bool decode_audio, bool enable_float32);
```

und `DecoderAdapter` konstruiert ihn als `SuperframeFilter(this, true, false)` — das erste
`true` ist also genau `decode_audio`. Der Dump-`fwrite` bekommt denselben Rohpuffer wie
`Feed()` und läuft unabhängig davon weiter.

Rechenzeit spart das kaum: 32 kbit/s HE-AAC (EEP 2-A, wie für „ASA DE" geplant) sind
vernachlässigbar. Und FAAD2 bliebe wegen `find_package(Faad REQUIRED)` trotzdem
Build-Abhängigkeit, solange man nicht auch das Buildsystem anfasst. Also: möglich und billig,
aber kein Grund für sich allein.

---

## 2a. Wie `libwelle` eingebunden wird

**`libwelle` ist kein eigenständiges Projekt.** Es gibt kein Repository dieses Namens und kein
Distributionspaket. Der Name bezeichnet ein Bauziel *innerhalb* von welle.io: Das
`CMakeLists.txt` enthält genau ein `add_library(welle ${STATIC_OR_SHARED} ...)`, gesteuert
über die Option `LIBWELLE_STATIC` (Vorgabe `ON`). Das Artefakt heißt daher `libwelle.a`
beziehungsweise `libwelle.so`.

**Es gibt keine öffentliche Bauoberfläche.** Geprüft am 26.08.2026 im Zweig `next`:

| gesucht | vorhanden |
|---|---|
| Header-Installation | **nein** |
| pkg-config-Datei | **nein** |
| CMake-Config-Export (`find_package(welle)`) | **nein** |
| Installation der Bibliothek | nur beim dynamischen Bau, nach `${CMAKE_INSTALL_LIBDIR}` — ohne Header also unbrauchbar |

Daraus folgt unmittelbar: Die Bibliothek lässt sich **nicht als externe Abhängigkeit
auflösen**. Wer sie nutzt, braucht den welle.io-Quellbaum im eigenen Bau und richtet die
Include-Pfade direkt auf `src/backend`.

### Der Weg: eingebundener Fork-Quellbaum auf festgenageltem Commit

```
asa-monitor/
└── rx/
    ├── CMakeLists.txt          # asa-rx
    └── external/
        └── welle.io/           # Submodul: eigener Fork, fester Commit
```

- `add_subdirectory(external/welle.io EXCLUDE_FROM_ALL)`
- `-DBUILD_WELLE_IO=OFF -DBUILD_WELLE_CLI=OFF`
- `target_link_libraries(asa-rx PRIVATE welle)`

**Was die beiden Schalter kosten und sparen.** Die `find_package`-Aufrufe stehen im
`CMakeLists.txt` teils bedingt, und zwar zu unseren Gunsten:

| Abhängigkeit | Bedingung | für `asa-rx` |
|---|---|---|
| Qt6 | nur `if(BUILD_WELLE_IO)` | **entfällt** |
| Lame, FLACPP | nur `if(BUILD_WELLE_CLI AND NOT ANDROID)` | **entfällt** |
| FFTW3f *oder* KISS-FFT (`-DKISS_FFT=ON`) | immer | Pflicht |
| FAAD2 (`find_package(Faad REQUIRED)`) | immer, sofern nicht `-DFDK_AAC=ON` | Pflicht — und FDK_AAC bleibt aus Lizenzgründen aus |
| MPG123 | immer | Pflicht (DAB-Legacy-MP2) |
| librtlsdr / libairspy / SoapySDR | je Option `RTLSDR`, `AIRSPY`, `SOAPYSDR` | nach Bedarf |

Der in `decoder-optionen.md` Abschnitt 3.1 genannte „lame/FLAC-Zwang" trifft also nur
`welle-cli`, nicht die Bibliothek. Er verschwindet mit einem Schalter, nicht mit einem Patch.

### Fork, Patch-Datei oder Kopie

| Weg | Aktualisierung | Pflegelast | Weitergabe nach GPL |
|---|---|---|---|
| **Fork auf festem Upstream-Stand** *(der Weg dieser Skizze)* | Rebase auf neues Upstream-Tag | trägt die Patch-Serie sichtbar; Konflikte fallen beim Rebase auf, nicht im Bau | Fork ist selbst die Quelle — und muss bei Weitergabe des Knotens öffentlich sein |
| Submodul auf Upstream + `*.patch` im Bau | Commit hochziehen, Patch neu anlegen | tragbar bei einer Handvoll Zeilen; bricht bei Upstream-Änderungen im Bau statt beim Rebase | Upstream bleibt Upstream, der Patch liegt im eigenen Repo |
| Quellen ins eigene Repo kopieren | praktisch keine | jede Upstream-Korrektur an Demodulation und SDR-Treibern geht verloren | Herkunft und Änderungen von Hand zu belegen |

Die frühere Fassung stellte das Submodul ohne Patch an die Spitze, weil es keine dauerhafte
Patch-Serie gab. Die gibt es jetzt: Der FIG-0/15-Patch aus Abschnitt 2b ist Voraussetzung,
kein Kandidat, und er berührt drei Dateien. Damit fällt die Wahl auf den **Fork** — nicht weil
der Patch groß wäre, sondern weil ein Patch, der nie wieder verschwindet, an einer Stelle
leben soll, an der ein Upstream-Rebase ihn zwingend zur Sprache bringt.

Für den Fork spricht ein zweites Argument: In welle.io existiert **kein einziger Pull Request**
zu EWS/ASA/FIG 0/15 (`decoder-optionen.md` Abschnitt 0.1 C). Ein sauber gegen TS 104 089
Annex E gebauter Parser mit nicht-rein-virtuellem Rückruf ist upstream-tauglich. Wird er
angenommen, verschwindet die Patch-Last ganz — und das ist der einzige Weg, auf dem sie je
verschwindet. Der Fork sollte deshalb von Anfang an so aussehen, dass daraus ein Pull Request
werden kann: ein Commit, keine Vermischung mit projektspezifischem Code.

Der feste Commit bleibt Pflicht, jetzt erst recht: Ein Patch gegen eine nicht zugesagte
Schnittstelle bricht sonst unbemerkt. Und die Kopie ins eigene Repo bleibt aus den Gründen
der Tabelle ausgeschlossen.

**Warum trotzdem hart gepinnt wird.** Ohne installierte Header ist die welle-API **keine
zugesagte Schnittstelle**. Sie kann sich zwischen zwei Commits ändern, ohne dass das irgendwo
als Bruch auftaucht. Ein fester Commit ist deshalb Pflicht, kein Vorsichtsmaß — und
Aktualisierungen sind ein bewusster Schritt mit anschließendem Lauf gegen aufgezeichnete
Ströme (Abschnitt 5, Replay-Modus).

**Eine Reibungsstelle beim Aufsetzen.** welle.io bringt sein eigenes `project()`, seine
`install()`-Regeln und seine Optionen mit in unseren Bau. `EXCLUDE_FROM_ALL` und die beiden
`BUILD_*=OFF`-Schalter fangen das ein; erfahrungsgemäß ist das der Teil, der beim ersten Mal
Arbeit macht.

---

## 2b. Der Patch: FIG 0/15 im `FIBProcessor`

Drei Dateien, geprüft an `next` (26.08.2026):

| Datei | Änderung |
|---|---|
| `src/backend/radio-controller.h` | Struct `asa_alert_t` neben `dab_date_time_t`/`mot_file_t`, ein Rückruf in `RadioControllerInterface` |
| `src/backend/fib-processor.h` | eine Zeile: `void FIG0Extension15(uint8_t *);` im privaten Teil, zwischen `FIG0Extension14` und `FIG0Extension16` |
| `src/backend/fib-processor.cpp` | `case 15: FIG0Extension15 (d); break;` in `process_FIG0()`, dazu der Parser |

### Der Rückruf

```cpp
/* A FIG 0/15 (EWS/ASA, ETSI TS 104 089) was decoded.
 * Deliberately not pure virtual: existing controllers stay unaffected. */
virtual void onAsaAlert(const asa_alert_t& alert) { (void)alert; }
```

**Nicht `= 0`.** Alle übrigen `on...` des `RadioControllerInterface` sind rein virtuell; ein
weiteres rein virtuelles Mitglied würde jeden Implementierer im Baum brechen — `welle-io`,
`welle-cli`, die Tests. Eine leere Vorgabeimplementierung hält den Patch auf drei Dateien und
macht ihn upstream-tauglich. Vorbild im Bestand: `onInputFailure()` und `onRestartService()`
sind aus demselben Grund schon nicht rein virtuell.

### Was der Rückruf trägt — und was nicht

Der Backend-Teil **entpackt Bits, mehr nicht**. Er deutet weder Alert-Sets noch Phasenverläufe
noch Location-Geometrie. Diese Grenze ist die Fortsetzung des Prinzips aus Abschnitt 4a: Das
Volatile bleibt außerhalb des Empfangsteils. Was sich ändert, sobald wir echten ASA-Verkehr
sehen, ist die Deutung — nicht das Bitlayout, das in Annex E normativ festliegt.

```cpp
struct asa_alert_t {
    // FIG-Type-0-Header
    bool     cn = false;               // C/N (SIV)
    bool     oe = false;               // 0 = eigenes Ensemble, 1 = anderes
    bool     secondHalfMinute = false; // P/D, zweckentfremdet: Sekunden 30-59
    bool     heartbeat = false;        // Längenfeld == 1, leeres Type-0-Feld

    // Id-Feld
    uint8_t  phase    = 0;             // 0 Pre-trigger, 1 Trigger, 2 Sustain, 3 End
    uint8_t  subChId  = 0;             // gültig bei oe == false
    uint16_t otherEId = 0;             // gültig bei oe == true
    bool     hasSec   = false;         // nur Phase 0
    uint8_t  sec      = 0;             // Startsekunde; 63 = Sonderfall

    // Status-Feld, nur Phase 0 und 1
    bool     hasStatus = false;
    bool     last      = false;
    uint8_t  stage     = 0;            // 0-7, 7 = Test
    uint8_t  iid       = 0;            // 0-15

    // Location Codes: NFF gedeutet, der Rest roh
    uint8_t  nff = 0;
    std::vector<uint8_t> locationCodes;

    // Roh-Beleg: gepackte FIG-Bytes einschließlich beider Header, <= 31 B
    std::vector<uint8_t> raw;
};
```

`raw` ist der Kern der Antwort auf `client-konzept-review.md` §3.4: Jedes ASA-Ereignis führt
seinen eigenen Beleg mit. Ein Parserfehler lässt sich am gespeicherten Ereignis nachträglich
korrigieren, ohne dass dafür dauerhaft 5,6 kB/s über die Pipe müssen.

### Heartbeat steht im Längenfeld, nicht im Type-0-Feld

Die Heartbeat-Form ist ein FIG 0/15 mit leerem Type-0-Feld, erkennbar am **Längenfeld des
FIG-Headers = 1** (`asa.md` §3). Das Feld liegt im Parser unmittelbar bereit — `processFIB()`
liest es an derselben Stelle, um zum nächsten FIG zu springen:

```cpp
const uint8_t figLength = getBits_5(d, 3);   // Bytes nach dem FIG-Header
alert.heartbeat = (figLength == 1);
```

Damit ist genau der Fall abgedeckt, den der spec-konforme Qt-DAB-Fork-Parser verwirft
(`if (CN_bit == 1) return;`, `decoder-optionen.md` 0.1 A) — und der für die Abdeckungskarte
den größten Wert hat.

### Der Mutex ist die Falle

`processFIB()` hält über die **gesamte** Verteilung `std::lock_guard<std::mutex> lock(mutex)`
— denselben nicht-rekursiven Mutex, den `getServiceList()`, `getComponents()`,
`getSubchannel()` und `getEnsembleId()` nehmen. Der Rückruf läuft also **unter diesem Lock**,
auf dem FIC-Thread. Zwei harte Regeln folgen daraus:

- **Aus `onAsaAlert()` heraus nie in den `FIBProcessor` zurückrufen.** Ein `getServiceList()`
  an dieser Stelle ist ein sofortiger Selbst-Deadlock — kein Wettlaufrisiko, das man
  wegtestet, sondern ein sicherer Stillstand beim ersten Alert. Ausgerechnet die Auflösung
  SubChId → Service, die man dort naheliegenderweise machen möchte, gehört deshalb hinter die
  Warteschlange, auf den Steuerungsthread.
- **Der Rückruf kopiert und stellt ein — sonst nichts.** Dieselbe Regel wie in Abschnitt 4,
  aber mit schärferer Begründung: Hier hängt zusätzlich der Ensemble-Zustand des gesamten
  Backends am selben Lock.

Das ist kein Argument gegen den Patch: Die vorhandenen Rückrufe `onNewEnsemble()`,
`onDateTimeUpdate()`, `onServiceDetected()` und `onRestartService()` stehen genauso unter
diesem Lock. Es ist eine Eigenschaft, die dokumentiert gehört, weil sie sich aus
`radio-controller.h` allein nicht erschließt.

### Erschöpfende Fallbehandlung — jetzt in C++

Die Regel aus Abschnitt 4a bleibt, sie wechselt nur die Sprache: Phase hat 4, Stage 8 mögliche
Werte, und ein unbehandelter Wert darf **nicht** stillschweigend verschwinden. In C++ ist das
billiger als in Go: `enum class` plus `-Wswitch` (in `-Wall` enthalten) erzwingt die
Vollständigkeit zur Bauzeit. Unbekanntes wird gezählt und gemeldet, nicht verworfen — ein
unerwarteter Stage-Wert ist eine meldenswerte Beobachtung, kein Fehler.

### Prüfbarkeit — der Patch verliert nichts

`FIBProcessor::processFIB(uint8_t *p, uint16_t fib)` ist **public**. Ein Unit-Test kann
handgebaute FIBs direkt einspeisen: ohne SDR, ohne Prozessgrenze, ohne Aufzeichnung — und
damit die Bitlayouts aus TS 104 089 Annex E und die Testfälle aus TS 104 090 durchspielen.
Der Wegfall von `go test -fuzz` ist damit ausgeglichen; libFuzzer über denselben Einstiegspunkt
ist der direkte Ersatz. Der Aufbau steht in Abschnitt 5.

---

## 3. Eigene Bausteine

1. **FIG-0/15-Parser im Backend** (C++, Abschnitt 2b) — nach TS 104 089 Annex E, im
   `FIBProcessor`. Zerlegt eine FIG-0/15-Instanz in Felder und reicht sie mitsamt den rohen
   FIG-Bytes hinaus. Der übrige Ensemble-Aufbau (0/0, 0/1, 0/2, 0/10, 1/x) kommt wie bisher
   aus dem Backend-Modell; wir ergänzen nur, was dort fehlt.
2. **CRC-Zähler** (C++, `asa-rx`) — hängt an `onFIBDecodeSuccess()` und wertet **nur das Bit
   `crcCheckOk`** aus: FIBs gesamt und davon fehlerhaft, je Sekunde. Der bit-je-Byte-Puffer
   wird nicht angefasst, es gibt keinen FIB-Ring. Die Quote trennt „Ensemble schweigt" von
   „wir empfangen schlecht" — und dafür braucht sie die Nutzlast nicht.
3. **ASA-Zustandsmaschine** (Go, `asa-node`) — Heartbeat-Überwachung (Sekundenraster, P/D-Konsistenz, Lücken),
   Alert-Set-Rekonstruktion über NFF und Last-Flag, Phasenautomat je Datenbankschlüssel
   (OE-Flag + Id-Feld), OE-Alerts erfassen ohne Kanalwechsel, Test-Stage getrennt halten.
   Gibt **Ereignisse** aus, keinen Zustand.
4. **Recorder-Steuerung** — SubChId → Service auflösen, `addServiceToDecode`, FIFO lesen,
   nach der End-Phase mit Nachlauf abschalten, hartes Zeitlimit als Notbremse.
5. **Superframe-Packetizer** — DAB+-Superframes zu ADTS/LATM verpacken. Reine Header-Arbeit,
   kein Codec.
6. **Puffer & Uplink** — 10-s-Telemetriebatch **für alle Kanäle des Knotens zusammen** (immer,
   auch leer, mit ausdrücklicher Negativ-Beobachtung je Kanal) plus sofortiger Ereignis-Push
   je Kanal, Store-and-Forward auf Platte, Backpressure, signierte Pakete, Audio bevorzugt
   nachliefern.
7. **Kanalverwaltung** — startet und beaufsichtigt je Kanal einen `asa-rx`, wählt den Stick
   über die Seriennummer, hält die Kanalzustände getrennt und löst OE-Verweise quer über die
   Kanäle des Knotens auf (Abschnitt 4a).
8. **Betrieb** — Konfigurationsdatei mit der Kanalliste plus Schalter, strukturiertes Log nach
   stdout (journald), **eine** systemd-Unit mit Watchdog für den ganzen Knoten.

---

## 4. Nebenläufigkeit

Das Backend ruft unsere Rückrufe auf **seinen** Threads auf: dem OFDM-Thread und je einem
Thread pro zugeschaltetem Subchannel (`DabAudio` startet `ourThread`). Daraus folgen drei
Regeln, die den ganzen Entwurf tragen:

- **Rückrufe kopieren und stellen ein — sonst nichts.** Kein Datei- oder Netzzugriff, keine
  Sperre über Arbeit hinweg, keine Allokation außerhalb eines beschränkten Pools. Wer im
  OFDM-Thread blockiert, verliert Samples.
- **Die ASA-Logik läuft auf genau einem eigenen Thread**, gespeist ausschließlich aus
  beschränkten Warteschlangen. Einfädig heißt deterministisch.
- **Die Zustandsmaschine ist eine reine Funktion aus Ereignisstrom und Uhr.** Damit lässt sie
  sich gegen aufgezeichnete Record-Ströme erneut abspielen — die Antwort darauf, dass es keine
  Referenzimplementierung gibt (siehe `decoder-optionen.md` Abschnitt 0.1): Wir prüfen gegen
  konservierte Wirklichkeit und gegen injizierte Testfälle aus TS 104 090. Der Bitparser hat
  seit dem Patch seine eigene, engere Prüfung daneben (Abschnitt 2b und 5).

Mit dem Patch kommt eine vierte Regel dazu: **`onAsaAlert()` läuft unter dem Mutex des
`FIBProcessor`**, und aus ihm heraus darf nichts in den `FIBProcessor` zurückrufen — sonst
Selbst-Deadlock. Begründung in Abschnitt 2b.

---

## 4a. Prozessmodell: ein `asa-node`, je Kanal ein `asa-rx`

**Empfehlung: aufteilen** — aber nicht entlang der Sprachgrenze, sondern entlang der
Wissensgrenze. Die frühere Fassung zog sie bei „der Empfangsteil weiß nichts über ASA".
Mit dem Patch aus Abschnitt 2b liegt sie eine Stufe weiter rechts, und zwar **zwischen
Bitlayout und Deutung**: `asa-rx` kennt das Bitlayout aus TS 104 089 Annex E — das ist
normativ und ändert sich nicht — und deutet nichts. Alert-Sets, Phasenverläufe, Dedup und
Location-Geometrie bleiben in `asa-node`.

**Ein Knoten kann mehrere Kanäle gleichzeitig überwachen** — je RTL-SDR-Stick einen. Dann
läuft **je Kanal ein `asa-rx`**, und **ein** `asa-node` verwaltet sie alle.

```
┌─ asa-rx · Kanal 5C ──────────┐            ┌─ asa-node · Go ──────────────────┐
│ C++ · linkt den Fork         │───────────>│                                  │
│ FIG-0/15-Bitparser           │<───────────│  ┌────────────────────────────┐  │
└──────────────────────────────┘            │  │ Kanalzustand 5C            │  │
                                            │  │ Kanalzustand 11D           │  │
┌─ asa-rx · Kanal 11D ─────────┐            │  │ Kanalzustand 7B            │  │
│ C++ · linkt den Fork         │───────────>│  └─────────────┬──────────────┘  │
│ FIG-0/15-Bitparser           │<───────────│ je Kanal eine Goroutine,         │
└──────────────────────────────┘            │ eigene Queue, eigenes recover    │
                                            │                │                 │
┌─ asa-rx · Kanal 7B ──────────┐            │ OE-Auflösung quer über Kanäle    │
│ C++ · linkt den Fork         │───────────>│                │                 │
│ FIG-0/15-Bitparser           │<───────────│                v                 │
└──────────────────────────────┘            │ Spool · Signatur · Uplink        │
                                            │ ein 10-s-Batch für alle Kanäle   │
  je ein Stick, gewählt                     │ Ereignis-Push sofort, je Kanal   │
  über die Seriennummer                     └──────────────────────────────────┘

  ───>  Record-Strom auf stdout      <───  Kommandos auf stdin
```

Warum die Wissensgrenze trägt: Das Bitlayout steht in einer normativen Anlage und wird sich
nicht ändern; die Deutung wird sich ändern, sobald wir echten ASA-Verkehr sehen. Genau daran
ist der Schnitt ausgerichtet — und nicht daran, welcher Prozess in welcher Sprache geschrieben
ist.

### Warum ein `asa-node` für alle Kanäle

Die frühere Fassung sah **eine systemd-Unit je Kanal** vor, jede mit eigenem `asa-node`. Das
ist verworfen. Vier Gründe, der letzte ist der eigentliche:

- **Die Knotenidentität ist knotenweit, nicht kanalweit.** Signaturschlüssel, deklarierte
  Position, Antennenangaben, Spool — all das gehört dem Knoten. Bei einer Unit je Kanal müsste
  derselbe private Schlüssel in mehreren Prozessen liegen oder man erfände ein
  Sharing-Verfahren. So besitzt genau ein Prozess die Identität.
- **Bedienbarkeit ist hier ein Architekturkriterium.** Das Netz läuft auf fremden Raspberry Pis,
  betrieben von Freiwilligen. Eine Unit, eine Konfigurationsdatei, ein Log wiegt in diesem
  Projekt schwerer als in einem Rechenzentrum.
- **Weniger Serveranrufe.** Ein 10-s-Batch trägt alle Kanäle. Bei drei Kanälen sind das 8.640
  statt 25.920 Anfragen je Tag und Knoten, und eine Signatur je Batch statt drei. Angenehm,
  aber der kleinste Posten.
- **OE-Verweise werden lokal auflösbar.** Kommt auf Kanal A ein OE-Alert mit EId X und
  überwacht derselbe Knoten X auf Kanal B, kann `asa-node` den Recorder auf B **sofort** scharf
  stellen — ohne Serverrunde. `client-konzept-review.md` §3.6 notiert das bisher als etwas, das
  „perspektivisch der Server" leisten könnte. Damit ist ein Mehrkanalknoten nicht nur billiger
  als drei Einkanalknoten, sondern **fachlich besser**. Das ist der Grund, der die Entscheidung
  trägt.

Unberührt bleibt die Regel aus `client-konzept-review.md` §4: **nicht mehrere Kanäle in einem
Empfangsprozess verschachteln.** Sie galt `asa-rx`, und dort gilt sie weiter — ein Stick, ein
Kanal, ein Prozess. Geändert hat sich nur die Ebene darüber.

### Der Preis: die Absturzreichweite — und wie sie bezahlt wird

Bisher blieb ein Absturz kanal-lokal. Jetzt reißt ein Fehler in `asa-node` alle Kanäle mit.
Das ist ein echter Verlust, und er ist nur bewusst bezahlbar:

| Anforderung | Warum |
|---|---|
| **`recover()` je Kanal-Goroutine** — Panik zählen, melden, **nur diese** Zustandsmaschine sauber neu aufsetzen | In Go tötet eine nicht abgefangene Panik in *irgendeiner* Goroutine den ganzen Prozess. Ohne diese Isolation legt ein unerwartetes Bitmuster auf einem Lokalmux den Bundesmux-Kanal mit lahm |
| **Beschränkte Warteschlange je Kanal**, nicht eine gemeinsame | Sonst hungert ein Alert auf Kanal A den Kanal B aus. Der gleichzeitige Alert auf mehreren Kanälen ist nicht exotisch: Eine Bundeswarnung trifft Bundesmux und Landesmux zeitgleich — genau die Lage, auf die alles ankommt |
| **Der 10-s-Takt wartet nie auf den langsamsten Kanal** | Gebatcht wird, was zum Tick vorliegt; fehlende Kanäle kommen als ausdrückliche Negativ-Beobachtung mit. Damit gilt `client-konzept-review.md` §3.2 künftig **je Kanal** statt je Knoten |
| **Batching ist eine reine Telemetrie-Eigenschaft** | Der Ereignis-Push bleibt sofort und kanalweise. Sonst kauft man die Serverersparnis mit Latenz auf dem einen Pfad, der keine verträgt |
| **Sticks über die Seriennummer wählen, nie über den Index** — **braucht einen Patch** | RTL-SDR-Indizes verschieben sich beim Neustart und beim Umstecken. Schlimmer: `CRTL_SDR::open_device()` bietet **gar keine Auswahl** — es nimmt das erste Gerät, das sich öffnen lässt (geprüft 26.08.2026, `rtl_sdr.cpp` Z. 64 ff.). Mit zwei Sticks entscheidet damit die Startreihenfolge, welcher Prozess welchen Kanal bekommt. Das ist Patch-Kandidat 4 in Abschnitt 6 und **Voraussetzung für den Mehrkanalbetrieb**; bis dahin gilt: ein Stick je Knoten |
| **Neustart eines einzelnen Kanals gehört zur Oberfläche von `asa-node`** | `systemctl restart asa@2` fällt weg. Den Ersatz schuldet der Prozess, der jetzt die Lebenszyklen besitzt |

### Der Record-Strom

**NDJSON auf stdout** — ein JSON-Objekt je Zeile, UTF-8 —, Zeilenkommandos auf stdin:

| Record | Inhalt | Rate | Last |
|---|---|---|---|
| `init` | einmalig beim Stromstart: Formatversion, Kanal, Frequenz, Gerät, Fassungen | 1 je Strom | einmalig |
| `asa` | geparstes FIG 0/15 (Abschnitt 2b) **einschließlich der rohen FIG-Bytes als Hex** | 1/s Heartbeat, im Alarmfall bis 12/s | < 1 kB/s |
| `tlm` | SNR, Sync, Frequenzkorrektur, Ensemble-Zeit (FIG 0/10), EId, **FIC-CRC-Quote**, Verwurfs- und Parserfehlerzähler | 1/s | vernachlässigbar |
| `ens` | Ensembleaufbau als Momentaufnahme: Services, Komponenten, Subchannels | bei Änderung | vernachlässigbar |
| `aud` | Chunk des rohen Subchannel-Bitstroms als Base64, SubChId + Sequenznummer | nur im Alarmfall | 5,3 kB/s bei 32 kbit/s |

| Kommando | Wirkung |
|---|---|
| `REC <subChId>` | SubChId → Service auflösen, `addServiceToDecode()` |
| `STOP <subChId>` | `removeServiceToDecode()` |
| `QUIT` | sauber herunterfahren |

**Im Ruhezustand fließen damit einige hundert Byte je Sekunde statt 5,6 kB/s.** Das ist die
Wirkung der Entscheidung aus Abschnitt 1, und sie ist der Grund, warum die Prozessgrenze
aufhört, ein Risiko zu sein.

### Warum JSON

Das einzige Argument für ein festes Binärlayout waren **125 `FIB`-Records je Sekunde**. Rohe
FIBs gehen nicht mehr über die Pipe; übrig bleibt rund **ein Record je Sekunde**. Damit dreht
sich die Abwägung:

- **Selbsterklärend im Archiv.** Ein Mitschnitt ist in drei Jahren ohne Formatdokument lesbar.
  Für ein Projekt, dessen Kernprodukt Belege sind, wiegt das schwer.
- **Schemaentwicklung wird trivial.** Ein neues Feld bricht keinen alten Leser; beim
  Binärlayout war jede Änderung ein Versionssprung.
- **`asa-node` bleibt ohne Fremdabhängigkeit** — `encoding/json` ist Standardbibliothek.
- **Zeilenweise heißt werkzeugfähig:** `grep`, `head`, `tail -f` und `jq` funktionieren sofort.
  Deshalb entfällt der frühere Probe-Modus ersatzlos —
  `asa-rx --channel 5C | jq 'select(.type=="asa")'` beantwortet die Frage, für die es ihn gab.

Drei Festlegungen, die aus JSON folgen und nicht verhandelbar sind:

| Punkt | Warum |
|---|---|
| **Zeitstempel als String**, RFC 3339 mit Nanosekunden | Nanosekunden seit Epoche sind ~1,8·10¹⁸ und überschreiten die 2⁵³ von `float64`. Jeder Leser, der über einen generischen Typ geht, verlöre sonst **stillschweigend** Präzision |
| **`aud` als Base64**, +33 % | 4 kB/s werden 5,3 kB/s, eine zweiminütige Meldung 640 statt 480 kB. Ein Format ist mehr wert als zwei |
| **Aufzählungen als Klartext**, mit Zahlfallback | `"stage":"level1_start"` — und bei Unbekanntem `"stage_raw":5` plus Zähler. So überlebt die Regel „unbekannte Werte melden, nicht verwerfen" den Formatwechsel |

Der `ens`-Record bleibt der Grund, warum `asa-node` FIG 0/1 und 0/2 nicht selbst parsen muss:
Der Ensembleaufbau kommt aus dem erprobten welle.io-Modell. Mit dem Patch gilt dasselbe nun
auch für FIG 0/15 — `asa-node` parst **gar keine** FIGs mehr.

**Warum der `init`-Record und keine Kanalkennung je Record.** Über die Pipe ist der Kanal
implizit: Jeder `asa-rx` hat seine eigene, `asa-node` weiß also immer, von wem er liest. Der
Record-Strom ist aber zugleich Archivformat und Beleg zum Server, und eine archivierte Datei
muss für sich allein erklären können, aus welchem Kanal und von welchem Stick sie stammt. Ein
Kanalfeld in jeder Zeile wäre Redundanz; ein einmaliger Kopfsatz kostet nichts.

### Der Roh-Beleg — was davon bleibt

`client-konzept-review.md` §3.4 verlangt rohe FIBs, weil es keine Referenzimplementierung gibt
und ein falscher Parser plausibel aussehende Werte liefert (WarnBridge ist das Beispiel).
Davon bleibt **eine** Stufe:

> Der `asa`-Record trägt die rohen **FIG**-Bytes (≤ 31 B, als Hex) immer mit. Damit ist jedes
> ASA-Ereignis ohne Rückfrage neu auswertbar, wenn sich unsere Bitpositionen als falsch
> erweisen. Kosten: rund 60 Zeichen je Ereignis.

**Rohe FIBs werden nicht übertragen** — weder dauernd noch auf Anforderung. Es gibt keinen
FIB-Ring, kein `FIBDUMP`, kein `FIBSTREAM`. Damit fällt die Möglichkeit weg, nachträglich zu
belegen, dass der FIB-Walk in `processFIB()` nichts verschluckt hat, und die Aussage „Ensemble
X sendet keinen Heartbeat" stützt sich allein auf die CRC-Quote im `tlm`.

Das ist eine bewusste Vereinfachung für die ersten Ausbaustufen: **Parserfehler gehen ins Log
und in einen Zähler**, eine ausgearbeitete Fehlerbehandlung kommt später. Der Schritt zurück
wäre **additiv** — ein zusätzlicher Record-Typ und ein zusätzliches Kommando brechen kein
Format —, und genau deshalb ist die Vereinfachung risikoarm.

Nebeneffekt, der Arbeit spart: Ohne FIB-Records muss der **bit-je-Byte-Puffer nie gepackt
werden**. Aus `onFIBDecodeSuccess()` wird nur `crcCheckOk` gebraucht. Eine fehleranfällige
Ecke samt Test entfällt ersatzlos. Ohnehin sieht der Parser nie einen kaputten FIB:
`fic-handler.cpp` ruft `processFIB()` nur für gültige.

### Der Uplink

| Ebene | Politik | Größenordnung |
|---|---|---|
| Record-Strom (IPC) | `asa` + `tlm` + `ens`, `aud` im Alarmfall | wenige hundert Byte/s |
| Uplink, Normalbetrieb | Heartbeat-Aggregat und Empfangsqualität | wenige hundert Byte je 10 s |
| Uplink, Alarmfall | Ereignisse einschließlich der rohen FIG-Bytes | wenige kB je Ereignis |

Die frühere Rechnung — 5,6 kB/s sind 480 MB pro Tag und Knoten, bei hundert Knoten 48 GB
täglich — ist damit gegenstandslos. Was den Uplink im Alarmfall noch nennenswert füllt, ist
das Warn-Audio, nicht die Signalisierung.

### Warum dieser Schnitt

**Ein Format für drei Zwecke.** Der Record-Strom ist zugleich IPC-Protokoll, Archivformat für
Wiederholläufe und das, was als Roh-Beleg zum Server geht (siehe `client-konzept-review.md`
§3.4). Damit wird die Aussage aus Abschnitt 4 — die Zustandsmaschine ist eine reine Funktion
aus Ereignisstrom und Uhr — **strukturell** statt bloß konzeptionell: `asa-node` unterscheidet
nicht, ob der Strom aus `asa-rx` oder aus einer Datei kommt.

**Das Volatile liegt außerhalb des Empfangsteils.** Die ASA-Logik wird sich ändern, sobald wir
echten ASA-Verkehr sehen — sie darf sich ändern, ohne den SDR-Teil anzufassen oder die
Synchronisation zu verlieren.

**`asa-rx` bleibt klein und stabil.** Seine gesamte Oberfläche sind fünf Record-Typen und
drei Kommandos, und er weiß nichts davon, dass er einer von mehreren sein könnte. Was daran C++ ist, ist genau das, was C++ sein muss — und der Bitparser gehört
dazu, weil er neben dem FIB-Zerleger sitzt, der ihn ohnehin schon aufruft.

**Ein Fork, keine Kopie.** `asa-rx` bindet einen eigenen welle.io-**Fork** auf einem
festgenagelten Commit ein und linkt das Bauziel `welle`. Der Fork trägt genau einen Patch
(Abschnitt 2b) und sollte so geschnitten sein, dass daraus ein Upstream-Pull-Request werden
kann. Wie das konkret aussieht und warum Vendoring ausscheidet, steht in Abschnitt 2a; die
drei weiteren, nicht vorausgesetzten Patch-Kandidaten in Abschnitt 6.

### Was gegen den Monolithen spricht

Ein einziges C++-Binary spart die Prozessgrenze — und kauft sich dafür ein, dass Uplink,
Spool, Wiederholstrategie, TLS, Signatur, Konfiguration und Serialisierung in C++ entstehen.
Genau dort liegt der Aufwand und liegen die Fehler, und genau dort sind andere Sprachen
besser. Der Monolith wäre die richtige Wahl bei harten Echtzeitschleifen über die Grenze
hinweg — die gibt es hier nicht: Der Weg Trigger erkannt → `REC` → `addServiceToDecode` kostet
über eine Pipe Mikrosekunden, gegenüber 0,4–0,5 s Decoder-Anlauf ist das nicht messbar.

### Sprache für `asa-node`: Go — festgelegt

**Entscheidung: Go.** Die Grenze ist der Record-Strom und damit sprachneutral; die Architektur
bleibt unberührt. Was sich konkret ergibt:

**Was Go hier besser kann als die Alternative.**

- **Auslieferung.** `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build` erzeugt ein statisches
  Binary ohne Cross-Toolchain, von jedem Entwicklungsrechner aus. Für Pi OS 32 bit
  beziehungsweise Pi 3 entsprechend `GOARCH=arm GOARM=7`. Für ein Crowd-Netz auf fremden
  Geräten ist das der wichtigste Punkt überhaupt, und Go liefert ihn ohne Zutun.
- **Nebenläufigkeit.** Die Regeln aus Abschnitt 4 werden idiomatisch statt aufgesetzt: eine
  Goroutine liest den Record-Strom, eine gepufferte Kanalverbindung führt zur
  Zustandsmaschinen-Goroutine, die ihren Zustand allein besitzt. „Einfädig und deterministisch"
  ist damit strukturell erzwungen, nicht bloß vereinbart.
- **Nulldependenz-Uplink.** `net/http` und `crypto/ed25519` sind Standardbibliothek. Zusammen
  und NDJSON liest `encoding/json` ebenfalls aus der Standardbibliothek — `asa-node` kommt
  damit ohne eine einzige Fremdabhängigkeit aus, was zum Anspruch „reine CLI ohne weitere
  Abhängigkeiten" passt.
- **Fuzzing gehört zum Werkzeugkasten.** `go test -fuzz` ohne Zusatzinstallation. Der
  Bitparser ist zwar ins Backend gewandert (Abschnitt 2b), aber die Alert-Set-Rekonstruktion
  über `NFF` und `Last`-Flag und das Einlesen des Record-Stroms bleiben parserartig und
  ebenfalls ohne Referenzimplementierung.

**Was Go nicht mitbringt, und wie wir es ausgleichen.** Es gibt keine Aufzählungstypen mit
erschöpfender Prüfung. Gerade bei FIG 0/15 ist das heikel: Phase hat 4, Stage 8 mögliche Werte,
und ein unbehandelter Wert darf **nicht** stillschweigend verschwinden. Deshalb verbindlich:

- benannte Konstanten plus `String()`-Methode je Feld,
- in jedem `switch` ein ausdrückliches `default`, das den Fall **zählt und meldet** statt ihn
  zu verwerfen — ein unbekannter Stage-Wert ist eine meldenswerte Beobachtung, kein Fehler,
- `go vet` und `exhaustive` als Linter in der Bauprüfung.

**Keine verfrühte Optimierung.** 125 Records je Sekunde sind für den Sammler belanglos;
`sync.Pool` erst, wenn ein Profil es verlangt.

### Gegendruck auf der Pipe — entschärft, nicht abgeschafft

Der Fall, der den Entwurf bisher belastete: `asa-node` liest nicht schnell genug, der
Pipe-Puffer läuft voll (unter Linux 64 kB), `asa-rx` blockiert im `write()`. Passiert das auf
einem welle.io-Callback-Thread, verlieren wir Samples — genau der Fehler, den Abschnitt 4
verbietet. **Bei einigen hundert Byte je Sekunde ist das kein realistischer Betriebszustand
mehr:** 64 kB Puffer fassen dann Minuten statt Sekundenbruchteilen. Das ist der eigentliche
Gewinn der Entscheidung aus Abschnitt 1.

Zwei Fälle bleiben, und für sie bleibt auch die Disziplin:

- **`aud` im Alarmfall** (5,3 kB/s mit Base64) ist der einzige nennenswerte Schub — und er
  kommt ausgerechnet in dem Moment, auf den alles ankommt.
- **Ein hängender `asa-node`** darf den Empfang unter keinen Umständen anhalten.

Deshalb unverändert:

- **`asa-rx`** schreibt aus einem eigenen Thread mit interner beschränkter Warteschlange und
  **verwirft** Records, statt einen welle.io-Thread zu blockieren. Verwürfe werden gezählt und
  im `TLM`-Record gemeldet — eine Lücke im Strom muss sichtbar sein, nicht stillschweigend.
- **`asa-node`** liest ununterbrochen. Ist der Kanal zur Zustandsmaschine voll, geht der
  Record direkt in den Spool, aber die Lese-Goroutine hält nie an.

**Vorrang beim Verwerfen: `asa` vor `aud` vor `tlm`.** Ein verworfener `asa`-Record ist ein
verlorenes Ereignis, eine Lücke im Warn-Audio ist unwiederbringlich, ein verworfener
`tlm`-Record ist eine Zeile Statistik.

Dazu kommt eine Regel, die unmittelbar aus dem Patch folgt: **`onAsaAlert()` läuft unter dem
Mutex des `FIBProcessor`** (Abschnitt 2b). Der Rückruf kopiert und stellt ein — sonst nichts,
und schon gar nichts, was in den `FIBProcessor` zurückführt.

### Beaufsichtigung

`asa-node` startet je konfiguriertem Kanal einen `asa-rx` als Kindprozess und besitzt deren
Lebenszyklus — **eine** systemd-Unit, ein Prozessbaum, gleich wie viele Sticks im Knoten
stecken. Das ist einfacher als N Units mit `Requires=`/`After=` und gibt saubere
Neustartsemantik: Stirbt ein `asa-rx`, startet `asa-node` genau diesen neu und behält Spool
und Zustand — **die übrigen Kanäle laufen unterbrechungsfrei weiter**. Der Preis ist die
Neusynchronisation von ein bis zwei Sekunden auf dem betroffenen Kanal, im Telemetriestrom als
Lücke sichtbar und damit selbst eine Beobachtung.

---

## 5. Betrieb und Prüfbarkeit

**Ein `asa-rx` je Kanal, ein `asa-node` je Knoten.** Ein Stick, ein Kanal, ein
Empfangsprozess — ausgewählt über die Seriennummer, sobald Patch-Kandidat 4 aus Abschnitt 6
das hergibt; bis dahin ist der Mehrkanalbetrieb nicht reproduzierbar. Darüber genau
**eine** systemd-Unit für den ganzen Knoten, gleich wie viele Kanäle er überwacht; weitere
Kanäle sind reine Konfiguration. Stirbt ein `asa-rx`, startet `asa-node` nur diesen neu, und
die übrigen Kanäle laufen weiter (Abschnitt 4a).

**Drei Betriebsarten desselben Binaries:**

| Modus | Quelle | Zweck |
|---|---|---|
| Live | SDR-Frontend | regulärer Knotenbetrieb |
| Replay | IQ-Datei oder aufgezeichneter Record-Strom | dieselbe Logik ohne Funk; Regressionstests |

**Einen eigenen Probe-Modus gibt es nicht mehr.** Er wäre eine zweite Ausgabeform, die man
synchron halten müsste — und NDJSON ist lesbar genug. Die Frage, die vor allen anderen steht,
beantwortet `asa-rx --channel 5C | jq 'select(.type=="asa")'`, ohne Server und ohne Protokoll.
Das ist der erste sinnvolle Feldtest.

**Prüfbarkeit auf drei Ebenen.** Der Patch verschiebt sie, er verkleinert sie nicht:

| Ebene | Was geprüft wird | Wie |
|---|---|---|
| Bitparser (C++) | `FIG0Extension15()` gegen TS 104 089 Annex E | `FIBProcessor::processFIB()` ist **public** — ein Unit-Test speist handgebaute FIBs ein, ohne SDR und ohne Prozessgrenze. Testfälle aus TS 104 090, Fuzzing über denselben Einstiegspunkt |
| Zustandsmaschine (Go) | Alert-Sets, Phasen, Dedup, Location-Decoding | aufgezeichneter `ASA`-Record-Strom; `asa-node` unterscheidet nicht, ob er aus `asa-rx` oder aus einer Datei kommt |
| Gesamtkette | Demodulation bis Ereignis | IQ-Aufzeichnung — welle.io liest Rohdateien über `--device rawfile` |

Damit bleibt die Aussage aus Abschnitt 4 erhalten — die Zustandsmaschine ist eine reine
Funktion aus Ereignisstrom und Uhr —, und der Bitparser bekommt seine eigene, engere Prüfung
dazu. Gegenüber der alten Fassung ist das eher mehr als weniger: Vorher war der Parser nur
über den vollständigen Record-Strom erreichbar.

---

## 6. Offene Punkte

### Der eine Patch, der Voraussetzung ist

**Patch 0 — FIG 0/15 im `FIBProcessor`** (Abschnitt 2b). Drei Dateien: ein `case`, eine
Deklaration, ein Struct plus Rückruf. Er ist der Grund, warum aus dem eingebundenen Quellbaum
ein Fork wird (Abschnitt 2a), und er sollte upstream angeboten werden.

### Patch-Kandidaten — keiner davon Voraussetzung

Drei weitere Stellen könnten später je einen kleinen Patch verlangen. Sie kosten jetzt weniger
als vorher, weil der Fork ohnehin existiert:

| # | Anlass | Eingriff | Auslöser |
|---|---|---|---|
| 1 | ~~`fopen(dumpFileName, "wb")` blockiert auf einer FIFO, bis ein Leser da ist; ein hängender Leser blockiert den Decoder-Thread~~ **gebaut am 27.08.2026 als Patch 3** | Rückruf statt Datei | ~~zeigt sich im Betrieb~~ — gezeigt hat es sich beim Windows-Port |
| 2 | `decode_audio` fest auf `true` | Parameter durchreichen | reine Sparmaßnahme, Nutzen gering (Abschnitt 2) |
| 3 | Warn-Subchannel ohne Service-Eintrag in FIG 0/2 | Zugriff auf die Subchannel-Ebene ohne Service-Umweg | nur falls ein Ensemble so sendet |
| 4 | `CRTL_SDR::open_device()` öffnet das **erste** Gerät, das sich öffnen lässt — keine Auswahl über Index oder Seriennummer | `rtlsdr_get_device_usb_strings()` auswerten, Auswahl über einen neuen `DeviceParam` | **sobald ein Knoten mehr als einen Stick betreibt** — dann keine Kür mehr, sondern Voraussetzung |

- **Warn-Subchannel ohne Service-Eintrag.** Der Weg SubChId → Service setzt voraus, dass der
  signalisierte Subchannel in FIG 0/2 auftaucht. Für „ASA DE" auf 5C ist das der Fall
  (eigener Service, 32 kbit/s EEP 2-A). Ein Monitor sollte den Gegenfall trotzdem behandeln —
  dann bräuchte es direkten Zugriff auf die Subchannel-Ebene, Patch-Kandidat 3.
- **Lizenz — geklärt.** Die Dateiköpfe von welle.io sagen GPL-2.0-**or-later**, und
  `src/backend/dabplus_decoder.cpp` ist dablin-Code unter GPL-3-or-later: Das Binary ist
  effektiv GPL-3.0-or-later. Der Knoten ist damit bei Weitergabe GPL-3.0-or-later — das
  gehört von Anfang an deklariert. Der Server bleibt als eigenständiges Programm frei
  lizenzierbar. Einzige Bauauflage: **nicht** mit `-DFDK_AAC=ON` bauen. Details in
  [`decoder-optionen.md`](decoder-optionen.md) Abschnitt 7.
  **Neu durch den Patch:** Wir geben nicht mehr nur ein Programm weiter, das gegen welle.io
  linkt, sondern ein **verändertes** welle.io. Bei Weitergabe des Knotens muss der Quelltext
  des Forks samt Änderungen verfügbar sein. Mit einem öffentlichen Fork ist das erledigt —
  aber es ist eine Auflage, die vorher nicht bestand.
- **Lastmessung, jetzt mit der Kanalzahl als zweiter Achse.** FIC dauerhaft plus ein
  Subchannel im Alarmfall gegen die Volldecodierung des Multiplex — auf Pi 3 und Zero 2 W
  messen, nicht schätzen. Dazu die Frage, die der Mehrkanalbetrieb aufwirft: **Wie viele
  Sticks trägt ein Pi?** Die Grenze dürfte nicht die Rechenlast sein, sondern USB-Bandbreite
  und Speisestrom — beides misst man nur am Gerät.
- **Gerätewahl und Kanalsteuerung.** Auswahl über die Seriennummer, Verhalten beim Abziehen
  eines Sticks im Betrieb, und die Oberfläche, über die ein einzelner Kanal neu gestartet
  wird, ohne den Knoten anzuhalten (Abschnitt 4a).
- ~~**Record-Format festlegen.**~~ **Entschieden:** NDJSON, ein Objekt je Zeile
  (Abschnitt 4a, „Warum JSON"). Das Mengenargument für ein Binärlayout ist mit dem Wegfall der
  `FIB`-Records entfallen. Die Feldliste steht in `asamon-rx/TODO.md` Abschnitt 7 und gehört
  von dort nach `docs/record-format.md` im Empfänger-Repo.
- **`asa_alert_t` versionieren.** Die Struktur erfinden wir selbst, und sie muss zwischen
  Fork-Stand und `asa-rx` zusammenpassen. Ohne Versionsfeld oder Bausymbol passt eines Tages
  ein alter `asa-rx` zu einem neuen Fork, und es fällt erst beim Alert auf.
- **Umgang mit `asa-rx`-Neustarts.** Der Ensemble-Zustand ist danach leer, bis wieder
  `ENS`-Records kommen. `asa-node` muss ein Trigger-Ereignis in diesem Fenster sauber
  behandeln — mitschneiden, sobald es geht, und die Lücke im Ereignis vermerken.
