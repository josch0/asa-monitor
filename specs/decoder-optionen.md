# Open-Source-DAB-Decoder als Basis für den ASA-Monitor-Client

Bestandsaufnahme möglicher Grundlagen für die Empfangssoftware (Fork oder eingebundene Lib).
**Keine Entscheidung, keine Implementierung** — reine Optionsanalyse.

Stand: 25.08.2026. Alle Aktualitätsangaben sind der GitHub-API entnommen (`pushed_at`),
alle Aussagen über den Funktionsumfang wurden im Quellcode geprüft, nicht aus README-Texten
übernommen.

> ### Teilweise überholt — bitte zuerst lesen
>
> Dieses Dokument ist die **Bestandsaufnahme der Kandidaten**. Für welle.io ist es in zwei
> Schritten überholt worden, beide in
> [`client-architektur.md`](client-architektur.md) (Stand 26.08.2026):
>
> 1. **Der Zugang ist billiger als hier angenommen.** `onFIBDecodeSuccess()` liefert jeden
>    rohen FIB, `getServiceList()`/`getComponents()`/`getSubchannel()` lösen SubChId → Service,
>    und `addServiceToDecode(..., dumpFileName, ...)` liefert den rohen MSC-Strom. Der
>    Fork-Zwang, den dieses Dokument durchgängig unterstellt, folgt daraus **nicht**.
> 2. **Trotzdem wird gepatcht — aus einem anderen Grund.** Die Richtungsentscheidung vom
>    26.08.2026 setzt den FIG-0/15-Parser in den `FIBProcessor` und gibt geparste Ereignisse
>    statt roher FIBs über die Prozessgrenze. Das drückt den IPC-Strom von 5,6 kB/s auf rund
>    einen Record je Sekunde und nimmt dem Gegendruckfall die Grundlage. Damit ist es doch ein
>    Fork — aber einer mit **einem** Patch, und nicht, weil die Schnittstellen es erzwingen.
>
> Betroffen sind **Abschnitt 3.1** (Contra) und **Abschnitt 4** (Varianten V2/V2b/V2c); beide
> tragen unten einen entsprechenden Hinweis. Die Bewertung der übrigen Kandidaten, der Befund
> aus Abschnitt 0 und die Kriterien aus Abschnitt 1 sind unberührt; die Lizenzlage aus
> Abschnitt 7 bekommt durch den Patch eine zusätzliche Auflage.

---

## 0. Der Befund, der alles andere überlagert

**In keinem Upstream-Zweig wertet ein Open-Source-DAB-Projekt FIG 0/15 vollständig aus.**
Direkt im Code geprüft (`master` *und* `next`/`develop`):

| Projekt | Datei | Unterstützte FIG-0-Extensions | 0/15 |
|---|---|---|---|
| welle.io | `src/backend/fib-processor.cpp` | 0, 1, 2, 3, 5, 8, 9, 10, 13, 14, 17, 18, 19, 21, 22 | **nein** |
| dablin | `src/fic_decoder.cpp` | 0, 1, 2, 5, 8, 9, 10, 13, 17, 18, 19 | **nein** |
| etisnoop | `src/fig0_*.cpp` | 0,1,2,3,5,6,7,8,9,10,11,13,14,16,17,18,19,21,22,24,25,26,27,28,31 | **nein** (keine `fig0_15.cpp`) |
| Qt-DAB | `sources/frontend/fic-handling/fib-decoder.cpp` | vollständige Dispatch-Tabelle 0–31 | **Stub** (s. u.) |
| dab-cmdline | `library/src/ofdm/fib-decoder.cpp` | — | **nein** („case 15: Reserved") |
| ODR-DabMux (Sendeseite) | `src/fig/FIG0_*.cpp` | 0,1,2,3,5,6,7,8,9,10,13,14,17,18,19,20,21,24 | **nein** |

**Außerhalb des Upstreams sieht es anders aus** — siehe Abschnitt 0.1. Es existieren zwei
unabhängige Implementierungen in Forks, beide unvollständig, aber beide lehrreich.

Die strategische Aussage bleibt davon unberührt:

> Der ASA-Teil ist in **jedem** Szenario im Wesentlichen Eigenentwicklung. Die Wahl des
> Decoders entscheidet nicht darüber, *ob* wir FIG 0/15 selbst schreiben, sondern nur darüber,
> **wie wir an die rohen FIB-Bytes und an das Audio des Warn-Subchannels herankommen** — und
> wie teuer die Pflege dieser Anbindung auf Dauer ist.

**Nachtrag 26.08.2026.** Aus dieser Lücke ist eine Entscheidung geworden: Der Parser entsteht
nicht neben welle.io, sondern **in** welle.io — als `case 15:` im `FIBProcessor`, in einem
eigenen Fork (`client-architektur.md` Abschnitt 2b). Da es upstream zu FIG 0/15 nicht einmal
einen Pull Request gibt, ist dieser Parser zugleich der naheliegendste Beitrag zurück.

Zweite Konsequenz: Auch die **Sendeseite** kann kein FIG 0/15 — weder ODR-DabMux `master`/`next`
noch einer der zehn aktiven Forks (geprüft inkl. `jacob-hofman/ODR-DabMux-NonStandard` und
`nickpiggott/ODR-DabMux`) enthält eine `FIG0_15.cpp`. Für eigene Testsignale müsste ein
`FIG0_15`-Generator ergänzt werden (überschaubar — das FIG ist klein und in TS 104 089 Annex E
normativ ausformuliert) oder man patcht FIBs direkt in aufgezeichnete ETI-Frames. Das ist
relevant, weil vor dem 10.09.2026 kaum echte Aussendungen zu erwarten sind.

---

## 0.1 Was es in Forks und Feature-Branches bereits gibt

Geprüft wurden alle Branches der Hauptprojekte sowie sämtliche seit 2025 aktiven Forks:
40 welle.io-Forks (inkl. deren Nicht-Default-Branches), 8 Qt-DAB-, 3 DABstar-, 6 dablin-,
3 etisnoop-, 10 ODR-DabMux-, 4 dab-cmdline-, 2 eti-stuff-, 5 DAB-Radio- und 12
AbracaDABra-Forks. Zwei echte Fundstellen:

### A. Qt-DAB-Linie — partieller, aber spec-konformer Parser (nicht im Upstream)

Upstream `master` und `develop` enthalten nur:

```cpp
//	8.1.7
//	CN_bit SIV, OE_bit Rfu and PD_bit special use
void	fibDecoder::FIG0Extension15 (uint8_t *d) {
	(void)d;
//	to be researched
}
```

In **Forks eines älteren Qt-DAB-Standes** steht an derselben Stelle ein tatsächlicher Parser —
identisch in `gkusmierz/qt-dab`, `steveyoon77/qt-dab`, `Nanker59/qt-dab` und `mnhauke/qt-dab`
(letzterer ist das openSUSE-Paket). Auszug:

```cpp
const uint8_t CN_bit = getBits_1 (d, 8 + 0);
const uint8_t OE_bit = getBits_1 (d, 8 + 1);
const uint8_t PD_bit = getBits_1 (d, 8 + 2);
...
if (CN_bit == 1) return;   // Next config, not implemented yet
if (PD_bit == 1) return;   // discard
if (OE_bit == 1) return;   // other ensemble, not implemented
//	Handling the Id field
uint8_t phase   = getBits_2 (d, bitOffset);
uint8_t subChId = getBits_6 (d, bitOffset + 2);
bitOffset += 8;
if (phase == 00) { secondsCount = getBits_6 (d, bitOffset + 2); bitOffset += 8; }
//	Handling the Statusfield
bitOffset += 8;
//	Handling the location codes
//	to be researched
```

**Bewertung.** Das Id-Feld ist **korrekt nach TS 104 089 Annex E** gelesen: Phase (2 bit) +
SubChId (6 bit), und bei Pre-trigger der Sekundenzähler an der richtigen Position (nach 2 bit
Rfa). Auch die Sonderbedeutung des P/D-Flags ist erkannt. Das ist die einzige gefundene
Implementierung, die sich an der Norm ausrichtet — sie **bestätigt unsere Lesart von Annex E**.

Drei Einschränkungen, die sie für uns direkt unbrauchbar machen:
- `if (CN_bit == 1) return;` verwirft den **Heartbeat** — der hat laut Spec C/N = 1. Damit
  fällt genau das weg, was für die ASA-Abdeckungskarte den größten Wert hat. Ebenso Sustain/End
  bei leerer Alert-Group.
- `OE_bit == 1` → `return`: OE-Alerts werden verworfen.
- Status-Feld wird übersprungen, Location Codes gar nicht angefasst, alle Werte per `(void)`
  verworfen — es wird nichts gespeichert oder ausgegeben.

Praktischer Nutzen: **Verifikation unserer Bitinterpretation**, nicht Code-Wiederverwendung
(es sind rund 30 Zeilen).

Denselben leeren Stub trägt auch `dab-cmdline` in der Altdatei
`library/src/ofdm/fib-processor.cpp`; der aktive `fib-decoder.cpp` führt 15 weiterhin als
„Reserved".

### B. WarnBridge — ein komplettes Schwesterprojekt mit eigenem FIG-0/15-Parser

[`TogeriX-hub/dab-warnings-meshcore`](https://github.com/TogeriX-hub/dab-warnings-meshcore)
(„WarnBridge", Push 2026-07-01) empfängt DAB+-Warnmeldungen auf einem Raspberry Pi 4 und speist
sie in ein **MeshCore-LoRa-Mesh** ein — NINA-API als Fallback. Der Empfangsteil ist unserem
Vorhaben sehr nah:

- Python, Raspberry Pi 4 (2 GB), RTL-SDR, systemd-Service mit Watchdog und automatischem
  welle-cli-Neustart
- eigener **welle.io-Fork** ([`TogeriX-hub/welle.io`](https://github.com/TogeriX-hub/welle.io))
  mit `FIG0Extension15()` im `FIBProcessor`
- ASA-Zustand wird über die **bestehende welle-cli-Webschnittstelle** als `/mux.json`
  exponiert, Objekt `asa` mit `ews_ensemble`, `active`, `is_test`, `status`, `level`, `iid`
- Journaline-Dekodierung für die Warntexte, CAP-Normalisierung, Deduplizierung (ID +
  Inhalts-Hash), SQLite, Web-Dashboard, Simulator-Modus für Entwicklung ohne Hardware

**Bewertung des Parsers.** Er ist funktionsfähig als Ja/Nein-Signal, aber das Bitlayout weicht
deutlich von TS 104 089 Annex E ab:

| Feld laut Spec | Position laut Spec | im Fork |
|---|---|---|
| Phase (2 bit), SubChId (6 bit) | Id-Feld, OE = 0 | **wird gar nicht gelesen** |
| EId (16 bit) | Id-Feld, OE = 1 | OE-Flag wird nicht ausgewertet |
| Last (b7), Stage (b6–b4), IId (b3–b0) | **Status-Feld** | als IId/Last/Level/Test im **Id-Feld** gelesen |
| NFF (2 bit) | Beginn des Location Codes | als 1 bit im Status-Feld kommentiert |
| — | — | erfindet „Service ID (5 bit)" und ein „Alert-Flag" |

Folgen: Ohne SubChId kann der Fork **das Warn-Audio nicht auffinden**; ohne OE-Auswertung nicht
zwischen eigenem und fremdem Ensemble unterscheiden; die Warnstufe (`level`) beruht auf einer
falschen Bitposition. Die Heartbeat-Erkennung über das Length-Feld funktioniert dagegen.

**Was wir daraus mitnehmen — mehr als der Code selbst wert ist:**
1. Der **welle.io-Weg ist praktisch begehbar**, und zwar billiger als in Variante V2 angenommen:
   man muss kein eigenes CLI schreiben, sondern hängt die ASA-Felder an die vorhandene
   `mux.json` an (siehe neue Variante **V2b** in Abschnitt 4).
2. Eine empirische Feldbeobachtung aus `asa_watch.py`, die wir selbst nachmessen sollten:
   auf **5C (Bundesmux)** kommt dort laut Kommentar **alle 5 Minuten für 30 s ein Test-Alert**.
   Das wäre ein reproduzierbarer Testfall lange vor dem 10.09.2026.
3. Journaline wird begleitend zur Warnmeldung offenbar tatsächlich genutzt — das war in
   `asa.md` noch als offene Frage notiert.
4. Ein Negativbeispiel für Punkt 3.4 des Client-Reviews: Ein Parser mit falschem Bitlayout
   liefert plausibel aussehende Werte. Ohne mitgeführte **Roh-FIBs** fällt so etwas erst auf,
   wenn ein echtes Ereignis bereits verloren ist.

### C. Wo nichts gefunden wurde

welle.io (alle Branches, 39 der 40 aktiven Forks inkl. deren Feature-Branches — geprüft u. a.
`mpbraendli`, `DABodr`, `andimik`, `seife`, `EmergReanimator`), dablin (`master`, `next`, alle
6 Forks), etisnoop, ODR-DabMux, DABstar (auch `old-dab/DABstar`), DAB-Radio (`master`, `dev`,
`experimental`, alle Forks), eti-stuff. In welle.io gibt es zudem **keinen einzigen Pull
Request** zu EWS/ASA/FIG 0/15.

**AbracaDABra** bleibt ausgeschlossen: Der Decoder-Kern ist binär (Abschnitt 3.6), und die
Release-Notes bis v4.2.1 (2026-08-14) erwähnen kein ASA/EWS. Die 12 aktiven Forks können daran
nichts ändern.

---

## 1. Bewertungskriterien

1. **FIC-Zugriff** — kommt der Anwendungscode an rohe FIBs/FIGs, oder nur an vorverdaute Objekte?
2. **Subchannel-Zugriff über SubChId** — ASA nennt einen SubChId, keinen Service. Kann man
   *diesen* Subchannel mitschneiden, ohne den Umweg über eine Service-Auswahl?
3. **Anlaufzeit beim Zuschalten eines Subchannels** — der SubChId ist erst zur Trigger-Phase
   bekannt. Ein kalt gestarteter Subchannel-Decoder braucht Zeitentschachtelung über 16 CIFs
   (≈ 384 ms) plus DAB+-Superframe-Sync (bis 120 ms), liefert also erst nach **rund 0,4–0,5 s**
   gültiges Audio. Entscheidend ist, ob das relevant ist — siehe Kasten unten. Es ist es nicht.
4. **Headless / Abhängigkeitsgewicht** — Ziel ist ein CLI-Prozess auf einem Raspberry Pi.
5. **Aktualität und Bus-Faktor** — wer pflegt das in drei Jahren?
6. **Lizenz** — GPL-2 und GPL-3 lassen sich nicht beliebig mischen.

> ### Kein Multiplex-Ringpuffer nötig — die Zeitrechnung
>
> Die Warnmeldung beginnt **gleichzeitig** mit der Trigger-Phase, nicht danach. TS 104 089
> Kap. 6.2 definiert Sustain als „continuation of the alert message **after the Trigger phase
> has ended**" — das Audio läuft also während des Triggers. Die ASA-Guidelines bestätigen es
> von der anderen Seite: „Nach dem ASA-Standard muss ein Empfänger **spätestens fünf Sekunden
> nach Beginn der Warnmeldung** das entsprechende Audio wiedergeben."
>
> Genau deshalb schreiben die Guidelines dem Programmanbieter vor, die Meldung so zu bauen,
> dass die ersten fünf Sekunden nichts Wesentliches enthalten: *„Denkbar wäre ein mindestens
> fünf Sekunden langer Jingle, gefolgt von einer Einleitung wie ‚Dies ist eine amtliche
> Warnung von …' und anschließend der eigentlichen Warnmeldung."*
>
> Daraus folgt die Zeitrechnung für einen Monitoring-Knoten:
>
> | Schritt | Dauer |
> |---|---|
> | FIG 0/15 wird in den ersten Transmission Frame der Sekunde eingefügt (Empfehlung TS 104 089 Kap. 6.6.1) | ≤ 24 ms |
> | Trigger-Erkennung im FIC, SubChId aus dem Id-Feld, Lage des Subchannels aus dem bereits gecachten FIG 0/1 | ~0 |
> | Subchannel-Decoder anlaufen lassen (Zeitentschachtelung 16 CIF + Superframe-Sync) | ~0,4–0,5 s |
> | **Verlust am Anfang der Meldung** | **~0,5 s — mitten im Jingle** |
>
> Die Trigger-Phase wiederholt sich zudem **5 Sekunden lang durchgehend** (Kap. 7.2.2.3), es
> gibt also mehrere Chancen, sie zu erwischen. Ein Ringpuffer über den **gesamten Multiplex**
> würde permanent die Kanaldecodierung aller Subchannels bezahlen, um eine halbe Sekunde
> Jingle zu retten. Das lohnt nicht.
>
> **Zwei Einschränkungen, die bestehen bleiben:**
> - Bei schlechtem Empfang kann der Trigger komplett durch FIC-CRC-Fehler fallen. Dann sieht
>   man den Alert erst in der **Sustain**-Phase — die trägt zwar ebenfalls Phase + SubChId im
>   Id-Feld, aber die Trigger-Phase ist dann schon vorbei, der Mitschnitt beginnt mitten in der
>   Meldung. Nur für diesen Degradationsfall hätte ein Puffer Wert; er sollte deshalb
>   **optional und kurz** sein, nicht Architekturgrundlage.
> - Ein **FIC**-Ringpuffer (rohe FIBs, wenige kB/s) bleibt uneingeschränkt sinnvoll — aber das
>   ist etwas völlig anderes als ein Multiplex-Ringpuffer und kostet praktisch nichts.
>
> Nebeneffekt, der in dieselbe Richtung zeigt: Der warnende Service kann ein reguläres
> Programm sein, dessen Audio nur für die Dauer der Meldung ersetzt wird. Ein großer Vorlauf
> im Puffer würde also **reguläres Programm-Audio** mitschneiden — genau das, was
> `client-konzept-review.md` §3.5 vermeiden will.

---

## 2. Kandidaten im Überblick

| Projekt | Lizenz | Sprache | Letzter Push | ★ | Rolle |
|---|---|---|---|---|---|
| [welle.io](https://github.com/AlbrechtL/welle.io) | GPL-2.0 | C++ | 2026-08-13 | 739 | Voll-Decoder SDR→Audio, Lib + headless CLI |
| [Qt-DAB](https://github.com/JvanKatwijk/Qt-DAB) | GPL-2.0 | C++ | 2026-08-23 | 357 | Voll-Decoder, GUI-zentriert |
| [AbracaDABra](https://github.com/KejPi/AbracaDABra) | MIT (App) | C++ | 2026-08-14 | 169 | Voll-Decoder, **Kern binär-only** |
| [dablin](https://github.com/Opendigitalradio/dablin) | GPL-3.0 | C++ | 2026-01-20 | 144 | ETI/EDI → Audio, sehr sauberer FIC-Parser |
| [dab-cmdline](https://github.com/JvanKatwijk/dab-cmdline) | GPL-2.0 | C++ | 2026-06-18 | 68 | Decoder als Bibliothek mit C-API |
| [DAB-Radio](https://github.com/williamyang98/DAB-Radio) | **MIT** | C++ | 2025-08-30 | 49 | Voll-Decoder, sauber modularisiert |
| [ODR-DabMux](https://github.com/Opendigitalradio/ODR-DabMux) | GPL-3.0 | C++ | 2026-06-17 | 49 | Sendeseite, Testsignalerzeugung |
| [eti-stuff / eti-cmdline](https://github.com/JvanKatwijk/eti-stuff) | GPL-2.0 | C++ | 2026-07-14 | 26 | **SDR → ETI**, headless, Qt-frei |
| [DABstar](https://github.com/tomneda/DABstar) | GPL-2.0 | C++ | 2026-08-24 | 25 | Qt-DAB-Fork, GUI |
| [etisnoop](https://github.com/Opendigitalradio/etisnoop) | GPL-3.0 | C++ | 2026-01-12 | 14 | ETI-Analysewerkzeug, FIG-Referenzparser |
| [dabtools](https://github.com/linuxstb/dabtools) | GPL-3.0 | C | 2017-01-26 | 41 | historisch: Ursprung des ETI-Codes |
| [rtl-dab](https://github.com/maydavid/rtl-dab) | (keine) | C | 2020-04-22 | 28 | historisch, minimal |
| [sdrdab](https://github.com/kwanty/sdrdab) | — | C++ | 2017-11-30 | — | tot |
| [libdabdecode](https://github.com/Opendigitalradio/libdabdecode) | — | C++ | 2017-10-21 | — | **archiviert** |
| [gr-dab](https://github.com/andrmuel/gr-dab) / [Fork bkerler](https://github.com/bkerler/gr-dab) | GPL-3.0 | Py/C++ | 2024-12 / 2025-06 | — | GNU-Radio-Modul |
| [dab_decoder](https://github.com/F4JTV/dab_decoder) | ? | C++ | 2026-05-26 | 0 | SDR++-Plugin, sehr jung |

---

## 3. Die Kandidaten im Detail

### 3.1 welle.io — die größte gepflegte Codebasis

**Aufbau.** CMake-Optionen trennen sauber: `BUILD_WELLE_IO` (Qt6-GUI) und `BUILD_WELLE_CLI`,
dazu `LIBWELLE_STATIC` — der Backend-Code wird als eigenständige Bibliothek `welle` gebaut.
**Qt6 wird nur für die GUI benötigt**, das Backend ist Qt-frei. Das ist für uns der
entscheidende Punkt: Man kann die Bibliothek nutzen, ohne Qt anzufassen.

**Aber: keine öffentliche Bauoberfläche.** `libwelle` ist kein eigenständiges Projekt und kein
Paket, sondern nur dieses Bauziel. welle.io installiert **weder Header noch pkg-config noch
einen CMake-Config-Export**; die Bibliothek wird lediglich beim dynamischen Bau nach
`${CMAKE_INSTALL_LIBDIR}` gelegt, ohne Header also unbrauchbar. Ein `find_package(welle)` gibt
es nicht. Wer die Bibliothek nutzen will, bindet den Quellbaum in den eigenen Bau ein —
Einzelheiten in [`client-architektur.md`](client-architektur.md) Abschnitt 2a.

**Abhängigkeiten.** FFTW3f *oder* KISS-FFT (`-DKISS_FFT=ON` reduziert die Deps), faad2 oder
fdk-aac, mpg123, dazu je nach Frontend librtlsdr / libairspy / SoapySDR. `welle-cli` verlangt
zusätzlich **lame und FLAC** (nur fürs Web-Streaming — für uns wegzupatchen).

**FIC-Zugriff.** `FIBProcessor` parst die oben genannten Extensions; FIG 0/15 fällt in den
`default`-Zweig und wird verworfen. Ergänzen heißt: ein `FIG0Extension15()` schreiben und in
den `switch` eintragen — überschaubar. **Genau das ist seit dem 26.08.2026 der beschlossene
Weg** (`client-architektur.md` Abschnitt 2b).

**Mitschnitt.** `welle-cli -D` schreibt den **rohen FIC nach `dump.fic`** und pro Programm eine
`.msc`-Datei. Das ist funktional genau das, was wir brauchen, aber die Auswahl erfolgt über
Service-Label/SId, nicht über SubChId, und `-D` dekodiert *alle* Programme — auf einem Pi zu teuer.

**Pro**
- Mit Abstand aktivste größere Codebasis (739 ★, Push 2026-08-13, Release v2.7 03/2025).
- Backend als Bibliothek gebaut und Qt-frei; headless CLI existiert bereits.
- Breite Hardware-Unterstützung inkl. SoapySDR → deckt „optional andere SDR" ab.
- Gute Demodulations-Qualitätsmetriken vorhanden (SNR, FIC-CRC, TII) — wertvoll, um
  „kein Heartbeat" von „schlechter Empfang" zu unterscheiden.
- Auf dem Pi weit verbreitet und erprobt.

**Contra**
- ~~`welle-cli` ist auf *Anhören* und Web-Streaming ausgelegt, nicht auf Multiplex-Mitschnitt.
  Für Kriterium 2 und 3 müssten wir eigenen Code ins Backend legen.~~ **Überholt.** Der erste
  Halbsatz stimmt — nur ist `welle-cli` gar nicht der Weg. Gegen die Bibliothek gerechnet sind
  Kriterium 2 und 3 aus der vorhandenen API bedienbar, ohne Backend-Code
  ([`client-architektur.md`](client-architektur.md) Abschnitt 1 und 2).
- ~~Fork = dauerhafte Merge-Last gegen ein aktives Upstream.~~ **Teils überholt, teils
  bestätigt.** Erzwungen wird der Fork von den Schnittstellen nicht. Er kommt trotzdem, aber
  aus freier Entscheidung und mit genau **einem** Patch: FIG 0/15 im `FIBProcessor`, damit
  über die Prozessgrenze geparste Ereignisse statt roher FIBs gehen
  ([`client-architektur.md`](client-architektur.md) Abschnitte 1 und 2b). Merge-Last für einen
  `case`-Zweig, eine Deklaration und einen Rückruf — und ein Kandidat für einen
  Upstream-Pull-Request, der sie ganz beseitigen würde.
- Die Bibliotheks-API ist **nicht zugesagt** — keine installierten Header, kein
  Versionsversprechen. Sie kann sich zwischen zwei Commits ändern, ohne dass das als Bruch
  sichtbar wird. Deshalb harter Commit-Pin und Regressionslauf nach jeder Aktualisierung.
- Kein ETI-Ausgang, also kein natürlicher Ringpuffer auf Multiplexebene.
- Lizenz: unkritisch, siehe Abschnitt 7 — die Dateiköpfe sagen GPL-2.0-**or-later**, und
  welle.io enthält ohnehin schon GPL-3-Code aus dablin.

### 3.2 eti-cmdline (aus eti-stuff) — SDR → ETI, headless

**Was es tut.** Ein reduzierter DAB-Decoder, der die Aussendung in eine Folge von
**ETI-NI-Frames** (EN 300 799) übersetzt und auf stdout ausgibt. Basiert auf dab-cmdline,
der ETI-Teil stammt aus dabtools. Vorgesehener Betrieb: `eti-cmdline-rtlsdr -C 5C | dablin`.

**Abhängigkeiten.** Im CMakeLists geprüft: FFTW3f, libsndfile, libsamplerate plus den
SDR-Treiber. **Kein Qt** (das ist nur `eti-backend`, ein separates Testprogramm).
Geräte: RTLSDR, Airspy, SDRplay (2.13 und 3.x), Pluto, HackRF, LimeSDR, rtl_tcp, Raw-/WAV-/XML-Dateien.

**Was ETI liefert.** Ein ETI-NI-Frame enthält **den gesamten Multiplex**: FIC *und* die Daten
aller Subchannels, 6144 Byte je 24 ms = 2,048 Mbit/s = **256 kB/s**. Alle Subchannels sind
also immer schon dekodiert; die Anlaufzeit aus Kriterium 3 entfällt vollständig, und ein
Ringpuffer wäre mit 15,4 MB für 60 s trivial billig.

**Das ist aber kein Kaufargument mehr.** Wie der Kasten in Abschnitt 1 zeigt, kostet die
Anlaufzeit nur rund eine halbe Sekunde Jingle. ETI zahlt dafür **permanent** die
Kanaldecodierung aller Subchannels statt nur des einen benötigten. Der Ringpuffer war in einer
früheren Fassung dieses Dokuments das Hauptargument für eti-cmdline — er trägt nicht.

Was bleibt: Adressierung über SubChId statt über Service (Kriterium 2) fällt bei ETI von
selbst an, und **ETI-Aufzeichnungen sind reproduzierbare Testdaten** — man kann FIG 0/15
künstlich in aufgezeichnete FIBs einsetzen und die sieben Testszenarien aus TS 104 090
durchspielen, ohne auf eine echte Aussendung zu warten. Dafür braucht es eti-cmdline aber nur
auf dem Entwicklungsrechner, nicht auf jedem Knoten.

**Pro**
- Saubere Prozessgrenze zwischen DSP und Anwendungslogik; unser Code enthält **keine Signalverarbeitung**.
- Schlankste Abhängigkeitsliste aller Voll-Decoder, Qt-frei, für genau diesen Einsatz gebaut.
- ETI ist ein offen dokumentiertes, stabiles Format — kein privates Interface, das sich verschiebt.
- Als eigener Prozess: keine Fork-Pflege, und die GPL-Frage entschärft sich
  (getrennte Prozesse, Pipe statt Linken).
- Breiteste Geräteliste im Feld.
- **Als Testdaten- und Analysewerkzeug erste Wahl**, unabhängig von der Knoten-Architektur.

**Contra**
- **CPU:** ETI erzwingt die Kanaldecodierung des *kompletten* Multiplex (Deinterleaving +
  Viterbi für alle Subchannels), nicht nur für einen — dauerhaft, für einen Nutzen, der sich
  auf eine halbe Sekunde Anlaufzeit beschränkt. Das ist der teuerste Weg zum Ziel.
- Kleines Projekt, Bus-Faktor 1 (Jan van Katwijk), Doku dünn.
- ETI-Framing (LIDATA/FC/STC/EOF, CRC) müssen wir selbst parsen — gut dokumentiert, aber Arbeit.
- Liefert kaum Empfangsqualitätsmetriken nach außen; die müssten wir nachrüsten oder aus
  FIC-CRC-Fehlerraten selbst gewinnen.

### 3.3 dablin — der beste FIC-Parser als Vorlage

Kein SDR-Frontend: dablin liest ETI-NI oder EDI-AF. Der FIC-Decoder ist der **mit Abstand am
saubersten geschriebene** der geprüften Projekte — klare `ProcessFIG0_x()`-Methoden, lesbare
Bitextraktion, Announcement-Behandlung inklusive Alarm-Flag, TS-101-756-Tabellen. Audio über
FAAD2 oder FDK-AAC, DL/DL+, MOT-SlideShow. Konsolenvariante `dablin` ohne GTK.

**Rolle für uns:** nicht als Laufzeitkomponente, sondern als **Referenz und Strukturvorlage**
für den eigenen FIC-Parser — und als Gegenprobe („hört dablin denselben Service wie wir?").
Wenn Code übernommen wird: **GPL-3.0** beachten.

### 3.4 etisnoop — die Vorlage für den FIG-0/15-Parser und für Konformitätsmessungen

ODR-Analysewerkzeug für ETI-Ströme. Je FIG-Extension eine eigene Datei (`fig0_0.cpp` …
`fig0_31.cpp`) — genau das Muster, in das sich ein `fig0_15.cpp` einfügen würde. Enthält
zusätzlich `repetitionrate.cpp` (Wiederholraten-Statistik) und `ensembledatabase.cpp`.

Die Wiederholraten-Auswertung ist inhaltlich interessant: Damit lässt sich messen, ob ein
Ensemble den Heartbeat wirklich 1×/s sendet und ob das P/D-Flag korrekt zwischen den
Sekundenhälften wechselt — **das sind eigenständige Monitoring-Ergebnisse**, die das Projekt
liefern kann. GPL-3.0.

### 3.5 dab-cmdline — Decoder als Bibliothek

Explizit als Bibliothek konzipiert (`dab-api.h`: `dabInit`, `dabStartProcessing`, `dabStop`,
`set_audioChannel`, `set_dataChannel`) mit Callbacks für Ensemble-Name, Service-Namen, Zeit,
Audio-PCM, Datenbytes, MOT-Slides, TII und `fib_quality_t`.

**Der entscheidende Haken:** Die API liefert **`fib_quality` — aber keine FIB-Inhalte**. Es gibt
keinen Weg, rohe FIGs aus der Bibliothek herauszubekommen, ohne sie zu ändern. Damit ist der
Vorteil „saubere Lib-Grenze" für unseren Anwendungsfall wieder aufgehoben; wir wären doch im
Bibliothekscode. Für einen reinen **Heartbeat-Monitor ohne Audio** wäre es dennoch die
schlankste Basis. Aktiv (2026-06-18), GPL-2.0, viele Geräte, klein und lesbar.

### 3.6 AbracaDABra — funktional stark, aber als Basis ausgeschlossen

Auf dem Papier der attraktivste Kandidat: MIT-Lizenz, sehr aktiv (2026-08-14), Qt6, offizielle
AArch64-AppImages **für Raspberry Pi 4/5**, sehr breiter Funktionsumfang (Announcements
einschließlich Alarm-Tests, DL+, SLS/CatSLS, SPI, RadioDNS, TII mit Kartenansicht,
Rohdaten-Dump, Audioaufnahme).

**Ausschlusskriterium — im Repository verifiziert:** Der eigentliche DAB-Decoder ist die
Bibliothek `dabsdr`, und die liegt **nur als vorkompiliertes Binary** im Baum:

```
lib/linux_x86_64/   dabsdr.h   libdabsdr.so.4.0.1 (158 kB)   ← Header + .so, kein Quellcode
lib/linux_aarch64/ …   lib/darwin_arm64/ …   lib/windows_amd64/ …
```

Die MIT-Lizenz gilt für die Anwendung, nicht für den Decoder-Kern. FIG 0/15 ist dort **nicht
nachrüstbar**. Für uns damit **kein Fork-Kandidat** — wohl aber ein sehr nützlicher
**Referenzempfänger** zum Gegentesten am selben Standort.

### 3.7 Qt-DAB und DABstar — als Fork ungeeignet, als Referenz gut

Beide extrem aktiv (2026-08-23 bzw. 2026-08-24), beide GPL-2.0, beide Qt-GUI-zentriert.
DABstar (Fork von Qt-DAB, Tom Neda) ist technisch das modernere der beiden: VOLK/SIMD,
FDK-AAC, und bemerkenswert für uns ein **„FIB content window"** — ein eingebauter FIB-Inspektor,
mit dem man live sehen kann, ob ein Ensemble überhaupt FIG 0/15 sendet.

Als Grundlage für einen headless-Client sind beide zu tief an die GUI gekoppelt. Als
**Diagnosewerkzeug am Schreibtisch** sind sie erste Wahl — insbesondere DABstars FIB-Fenster,
um vor jeder Zeile Code empirisch zu klären, welche deutschen Ensembles den Heartbeat bereits
senden.

### 3.8 DAB-Radio (williamyang98) — die lizenzfreundliche Außenseiteroption

**MIT-Lizenz** — als einziges vollständiges Voll-Decoder-Projekt mit offenem Kern. Sehr sauber
modularisiert (`src/ofdm`, `src/dab`, `src/basic_radio`, `src/basic_scraper` — letzteres
schreibt Audio/Slideshow/MOT auf Platte), CI für Windows/Linux/macOS, gut lesbarer Code.

**Contra:** letzter Push 2025-08-30 (rund ein Jahr Stillstand), kleine Community (49 ★),
Beispiel-Apps an imgui gekoppelt, offene TODOs (TII fehlt, Firecode-Fehlerkorrektur im
AAC-Superframe ungelöst), keine Pi-Optimierung nachgewiesen.

Interessant, falls die GPL an irgendeiner Stelle stört, oder als drittes Vergleichs-Backend.

### 3.9 Ausgeschieden

- **dabtools** (2017/2018, „currently unmaintained") — historisch relevant, weil eti-cmdline
  seinen ETI-Code von hier hat, aber nichts, worauf man 2026 aufsetzt.
- **rtl-dab** (2020, keine Lizenzdatei), **sdrdab** (2017), **libdabdecode** (archiviert 2017) — tot.
- **gr-dab** — funktioniert, zieht aber die komplette GNU-Radio-Laufzeit nach. Widerspricht
  „schlanke CLI ohne weitere Abhängigkeiten" fundamental.
- **F4JTV/dab_decoder** — SDR++-Plugin, Mai 2026, 0 ★, an die SDR++-GUI gebunden. Zu jung.

---

## 4. Drei Architekturvarianten

> **Hinweis zur Neubewertung.** Eine frühere Fassung dieses Dokuments stellte V1 an die Spitze,
> weil ein Multiplex-Ringpuffer nötig schien, um den Anfang der Warnmeldung zu retten. Das war
> falsch (siehe Kasten in Abschnitt 1). Damit verliert V1 sein Hauptargument, und die
> Reihenfolge dreht sich.
>
> **Zweite Neubewertung (26.08.2026).** Die Varianten V2 und V2b sind hier noch als *Fork*
> beschrieben. Das ist überholt: Die Schnittstellenprüfung in
> [`client-architektur.md`](client-architektur.md) hat ergeben, dass für den Normalbetrieb
> keine Änderung am Backend nötig ist. Aus V2 wird damit **V2c** — ein eigenes CLI gegen die
> Bibliothek —, und das ist die Variante, die die Architekturskizze ausarbeitet
> (`asa-rx` + `asa-node`). V2b und V2 bleiben als Zwischenstände stehen, weil ihre Last- und
> Zeitrechnung weiter gilt.
>
> **Dritte Neubewertung (26.08.2026, später am Tag).** V2c bekommt **einen** Patch: den
> FIG-0/15-Parser im `FIBProcessor`. Nicht weil die Schnittstellen ihn erzwingen, sondern
> weil er die Prozessgrenze von 5,6 kB/s auf rund einen Record je Sekunde entlastet und damit
> den Gegendruckfall beseitigt. V2c unten ist entsprechend gefasst.

### V2c — eigenes CLI gegen die Bibliothek, ein Patch *(die ausgearbeitete Variante)*

```
RTL-SDR ──> welle.io-Bibliothek (Fork, fester Commit, ein Patch: FIG 0/15)
              │  onAsaAlert() · onFIBDecodeSuccess() · getServiceList() · addServiceToDecode()
              v
            asa-rx (C++: Bitparser + FIB-Ring, deutet nichts)
              │  Record-Strom über die Prozessgrenze: ASA · TLM · ENS · AUD (FIB auf Abruf)
              v
            asa-node (Go) ──> Zustandsmaschine · Location-Decoding · Spool ──HTTPS──> Server
```

Statt `welle-cli` ein eigenes Kommandozeilenprogramm gegen das Bauziel `welle`. Genau das ist
der Zuschnitt, den [`client-architektur.md`](client-architektur.md) ausarbeitet: `asa-rx`
(C++, klein, kennt das Bitlayout aus Annex E und deutet nichts) und `asa-node` (Go, alles
Übrige), verbunden über einen Record-Strom.

Der einzige Eingriff ins Backend ist `case 15:` in `FIBProcessor::process_FIG0()` samt Rückruf
`onAsaAlert()` — drei Dateien, nicht rein virtuell, also ohne Bruch für die übrigen
Implementierer. Er ist Voraussetzung, kein Kandidat.

Die Einwände gegen die `welle-cli`-Struktur (Web-Server, lame/FLAC-Zwang, Service-orientierte
Auswahl) treffen `welle-cli`, nicht die Bibliothek: Lame und FLACPP werden nur unter
`if(BUILD_WELLE_CLI ...)` gesucht, Qt6 nur unter `if(BUILD_WELLE_IO)`. Beides entfällt mit
einem CMake-Schalter.

### V2b — nur der signalisierte Subchannel *(überholt durch V2c, Lastrechnung gilt weiter)*

```
RTL-SDR ──> welle.io-Backend ──> asa-monitor-client (eigener Prozess)
              ├─ FIC dauerhaft: FIG 0/15, 0/0, 0/1, 0/2, 0/10, 1/x
              ├─ roher FIB-Ringpuffer (wenige kB/s)
              ├─ bei Trigger: SubChId → genau diesen Subchannel zuschalten
              │    (~0,4–0,5 s Anlauf, fällt in den Jingle)
              └─ Ereignisse + Roh-FIBs + DAB+-Superframes ──> HTTPS ──> Server
```

**Pro:** Es wird nur dekodiert, was gebraucht wird — FIC dauerhaft (billig), ein Subchannel
im Alarmfall. Das ist die mit Abstand geringste Dauerlast und macht kleinere Pi-Modelle
realistisch. welle.ios Qualitätsmetriken (SNR, FIC-CRC) fallen mit ab. Der Weg ist durch
WarnBridge praktisch erprobt (Abschnitt 0.1 B).
**Contra (in dieser Form überholt):** ~~Fork-Pflege~~ — entfällt, siehe Hinweis oben.
~~welle.io wählt intern über Service statt SubChId, das ist zu ergänzen~~ — `getComponents()`
liefert `ServiceComponent.subchannelId`, der Weg SubChId → Service ist damit von außen
gangbar. ~~GPLv2-Frage klären~~ — geklärt, Abschnitt 7. Es bleibt: Die Schnittstelle zum
Client muss **Push** sein, nicht Polling.

### V1 — eti-cmdline als Subprozess + eigener ASA-Client

```
RTL-SDR ──> eti-cmdline (unverändert) ──ETI-NI──> asa-monitor-client
                                                   ├─ FIC → eigener FIG-Parser
                                                   ├─ Subchannel jederzeit ohne Anlaufzeit
                                                   └─ HTTPS → Server
```

**Pro:** kein Fork, keine Merge-Last; klare Lizenzgrenze durch Prozesstrennung; unser Code
enthält keine DSP und ist damit in beliebiger Sprache schreibbar; ETI-Mitschnitte als Testdaten
und als Beleg; keine Anlaufzeit beim Zuschalten.
**Contra:** dauerhafte Volldecodierung des Multiplex, ohne dass dem ein entsprechender Nutzen
gegenübersteht; Abhängigkeit von einem Ein-Personen-Projekt (aber austauschbar — ETI ist
genormt).

**Wofür V1 trotzdem gesetzt ist:** als Werkzeug auf dem Entwicklungsrechner — ETI aufzeichnen,
FIG 0/15 injizieren, TS-104-090-Testfälle fahren, Referenzdecodierung gegen den eigenen Parser.
Nur eben nicht zwingend auf jedem Knoten.

### V2 — welle.io-Fork mit eigenem CLI *(überholt, siehe V2c)*

Der Zwischenstand, aus dem V2c hervorging: wie V2b, aber mit eigenem CLI **und** Fork. Die
Fork-Oberfläche, die hier als Preis verbucht war, ist entfallen.

**Zur Schnittstelle zwischen Backend und Anwendungscode.** WarnBridge hängt die ASA-Felder an
die vorhandene `mux.json` und fragt sie 1×/s per HTTP ab. Das ist der billigste Einstieg, aber
Polling: Bei einer Trigger-Phase von 5 s ist das knapp, und schnelle Phasenwechsel
(Pre-trigger → Trigger) können verschluckt werden. Für uns muss es ein Push sein. In V2c
erledigt sich die Frage von selbst: Die Rückrufe des Backends *sind* der Push, und nach außen
tritt der Record-Strom auf stdout — siehe
[`client-architektur.md`](client-architektur.md) Abschnitt 4a.

### V3 — dab-cmdline als Bibliothek

Schlankste Variante, aber die API gibt keine FIBs heraus → Änderungen in der Bibliothek
unvermeidlich. Sinnvoll, falls die erste Ausbaustufe **nur den Heartbeat** erfassen soll und
Audio-Mitschnitt später kommt.

### Nicht empfohlen
AbracaDABra (Kern binär), Qt-DAB/DABstar (GUI-gekoppelt), gr-dab (Abhängigkeitsgewicht),
dabtools/rtl-dab/sdrdab/libdabdecode (unmaintained).

---

## 5. Was in jeder Variante Eigenentwicklung bleibt

1. FIG-0/15-Parser inkl. Alert-Set-Rekonstruktion (NFF) und Alert-Group-Logik (Last-Flag).
   Der Qt-DAB-Fork-Code (0.1 A) deckt davon das Id-Feld ab und taugt zur Gegenprobe;
   Status-Feld, Location Codes, Heartbeat und OE-Fall fehlen dort vollständig.
2. Location-Code-Dekodierung (Umkehrung TS 104 089 Annex F) → GeoJSON.
3. Subchannel-Extraktion nach SubChId und Verpackung der DAB+-Superframes.
4. Puffer-, Batch- und Übertragungslogik zum Server.
5. FIG-0/15-Generator für Testsignale (ODR-DabMux-Ergänzung oder ETI-Patching).

---

## 6. Empfohlene nächste Schritte (vor jeder Entscheidung)

1. **Messen statt raten:** auf der Zielhardware beide Lastprofile protokollieren —
   `eti-cmdline-rtlsdr -C 5C` (Volldecodierung) gegen `welle-cli -c 5C -p <ein Service>`
   (FIC + ein Subchannel). Die Differenz beziffert, was V1 dauerhaft mehr kostet als V2b.
2. **Empirisch klären, ob überhaupt schon etwas zu sehen ist:** mit DABstars FIB-Fenster oder
   einem 60-Sekunden-`dump.fic` aus `welle-cli -c 5C -D` prüfen, ob deutsche Ensembles den
   ASA-Heartbeat bereits senden. Ein einziger Mitschnitt mit einem echten FIG 0/15 ist mehr
   wert als jede weitere Spec-Lektüre. **Konkreter Ansatzpunkt:** WarnBridge behauptet, auf
   5C komme alle 5 Minuten für 30 s ein Test-Alert — das ist in einer halben Stunde
   nachgemessen und wäre sofort ein reproduzierbarer Testfall.
3. **Testdatenpfad sichern:** ETI-Aufzeichnung + FIG-0/15-Injektion aufbauen, bevor
   Anwendungslogik entsteht — sonst ist der Parser bis zum 10.09.2026 unprüfbar.
4. **Lizenz:** geklärt, siehe Abschnitt 7. Offen bleibt nur die Bauentscheidung gegen
   `-DFDK_AAC=ON`.

---

## 7. Lizenzlage (geprüft 25.08.2026)

Maßgeblich sind die **Dateiköpfe**, nicht `COPYING` — `COPYING` enthält nur den Lizenztext,
die Rechteeinräumung steht in den Quelldateien.

### welle.io ist GPL-2.0-**or-later**

Stichprobe über `radio-receiver.cpp`, `fib-processor.cpp`, `dab-constants.h`,
`ofdm-processor.cpp`, `decoder_adapter.cpp`, `dab-audio.cpp`, `charsets.cpp`,
`input/rtl_sdr.cpp`, `various/Socket.cpp`, `welle-cli/welle-cli.cpp` — durchgängig:

> „either version 2 of the License, or (at your option) any later version"

Die Sorge „v2 only, also unvereinbar mit GPL-3" war damit unbegründet.

### welle.io enthält bereits GPL-3-Code

`src/backend/dabplus_decoder.cpp` trägt den Kopf:

```
    DABlin - capital DAB experience
    Copyright (C) 2015-2018 Stefan Pöschel
    ... either version 3 of the License, or (at your option) any later version.
```

Der DAB+-Superframe- und AAC-Pfad von welle.io **ist** dablin-Code unter GPL-3-or-later. Das
gebaute welle.io-Binary ist damit effektiv **GPL-3.0-or-later**, nicht GPL-2. Eine Übernahme
weiterer Teile aus dablin oder etisnoop (beide GPL-3-or-later) ändert an der Lizenzlage
folglich nichts.

### Was daraus folgt

| Punkt | Folge |
|---|---|
| Unser Knoten (`asa-rx`) linkt gegen die welle.io-Bibliothek — bei `LIBWELLE_STATIC=ON` (Vorgabe) sogar statisch | Er ist bei Weitergabe **GPL-3.0-or-later**. Das sollte von Anfang an so deklariert werden, statt es später zu entdecken. Dass wir das Backend nicht ändern, ändert daran nichts: Linken genügt |
| `asa-node` ist ein eigener Prozess, der über eine Pipe spricht | Kein abgeleitetes Werk der Bibliothek. Trotzdem ist der Knoten als Ganzes nur sinnvoll gemeinsam zu verteilen — die Lizenzentscheidung sollte einheitlich getroffen werden |
| Der Server ist ein eigenständiges Programm, das über HTTPS spricht | Kein abgeleitetes Werk; frei lizenzierbar |
| dablin-/etisnoop-Code als Vorlage | Zulässig. Trotzdem sollte der FIG-0/15-Parser aus TS 104 089 Annex E entstehen — das ist die normative Quelle, und die vorhandenen Fork-Implementierungen sind unvollständig oder falsch (Abschnitt 0.1) |
| **`-DFDK_AAC=ON` vermeiden** | Die Fraunhofer-FDK-AAC-Lizenz gilt weithin als GPL-unverträglich (Debian führt `fdk-aac` in non-free). Mit FAAD2 bauen — oder den Decoder ganz umgehen |
| **Wir geben ein *verändertes* welle.io weiter** (FIG-0/15-Patch, `client-architektur.md` Abschnitt 2b) | Der Quelltext des Forks samt Änderungen muss bei Weitergabe des Knotens verfügbar sein. Mit einem öffentlichen Fork erledigt — aber eine Auflage, die ohne den Patch nicht bestand |
| Fertige Pi-Images verteilen | Zieht die Quelltext-Pflicht für alles im Image nach sich |
