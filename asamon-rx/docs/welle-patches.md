# Der welle.io-Fork: welche Patches er trägt und warum

`asamon-rx` baut gegen einen eingebundenen welle.io-Quellbaum unter `external/welle.io`.
Dieses Dokument hält fest, worauf der Bau festgenagelt ist und was daran verändert wurde.

## Festgenagelter Stand

| | |
|---|---|
| Fork (Submodul, `origin`) | `https://github.com/josch0/welle.io`, Zweig **`asa-fig0-15`** |
| Upstream (`upstream`) | `https://github.com/AlbrechtL/welle.io`, Zweig `next` |
| Basis-Commit | `fe06fadf561a954f56580fd9a674316488ae4973` (13.08.2026, `v2.7-102-gfe06fadf`) |
| Commit mit Patch 1 | `296e5d306008080ad1de735d1ed212b8efae10e5` |
| Commit mit Patch 3 | `c29f198effcf2e564ebe52d7552f0190d57ec607` (27.08.2026) |
| Patchdateien | [`0001-add-fig-0-15-ews-asa-decoding.patch`](../patches/0001-add-fig-0-15-ews-asa-decoding.patch), [`0003-add-onmscdata-callback.patch`](../patches/0003-add-onmscdata-callback.patch) |

Der Fork trägt **zwei** Commits über dem Upstream-Stand. Das ist Absicht: Je weniger zwischen
`next` und uns liegt, desto schmerzloser der nächste Wechsel des Basis-Commits — und desto eher
lassen sich die Patches als Pull Requests anbieten.

**Die Nummern der Patchdateien folgen der Bezeichnung, nicht der Reihenfolge im Fork.** Patch 2
(Geräteauswahl über die Seriennummer) ist noch nicht gebaut; seine Nummer bleibt reserviert,
damit die Bezeichnungen im ganzen Projekt stabil bleiben. Deshalb gibt es `0001` und `0003`,
aber kein `0002`.

Ein frischer Klon holt den Patch damit von allein:

```bash
git clone --recurse-submodules <asa-monitor>
cd asamon-rx && cmake -B build -DCMAKE_BUILD_TYPE=Release && cmake --build build
```

Am 27.08.2026 so geprüft — auf beiden Plattformen gebaut ohne Warnung im eigenen Code,
`ctest` je 6/6: Windows 11 mit MSYS2/MinGW-w64 (GCC 16.2, Ninja) und Debian in WSL
(GCC 14.2, Unix Makefiles).

Im Submodul sind beide Fernarchive eingerichtet:

```bash
cd external/welle.io
git remote -v
# origin    https://github.com/josch0/welle.io.git
# upstream  https://github.com/AlbrechtL/welle.io.git
```

**Der Commit wird hart festgenagelt.** Ohne installierte Header ist die welle-API **keine
zugesagte Schnittstelle**; sie kann sich zwischen zwei Commits ändern, ohne dass das irgendwo
als Bruch auftaucht. Ein Wechsel des Basis-Commits ist deshalb eine bewusste Handlung mit
Gegenprobe, kein `git pull`.

**Damit ist die GPL-Auflage erfüllt.** Das gebaute Binary enthält ein **verändertes**
welle.io; dessen Quelltext samt Änderungen muss bei Weitergabe verfügbar sein. Der öffentliche
Fork erledigt das — jeder, der ein asamon-rx-Binary bekommt, kann sich den Quelltext dazu
holen, aus dem es entstanden ist.

---

## Patch 1 — FIG 0/15 im `FIBProcessor` (**Voraussetzung**)

Ohne diesen Patch baut `asamon-rx` nicht; `src/controller.h` bricht mit einer ausdrücklichen
Meldung ab. Das ist Absicht — der Patch ist der Zweck des ganzen Programms.

Drei Dateien:

| Datei | Änderung |
|---|---|
| `src/backend/radio-controller.h` | `asa_alert_t`, `WELLE_ASA_ALERT_VERSION`, Rückruf `onAsaAlert()` |
| `src/backend/fib-processor.h` | eine Zeile: `void FIG0Extension15(uint8_t *);` |
| `src/backend/fib-processor.cpp` | `case 15:` in `process_FIG0()` und der Parser |

### Warum `onAsaAlert()` nicht rein virtuell ist

Alle übrigen `on…` im `RadioControllerInterface` sind es; ein weiteres würde jeden
Implementierer im Baum brechen (`welle-io`, `welle-cli`, Tests). Vorbild im Bestand:
`onInputFailure()` und `onRestartService()` sind aus demselben Grund schon nicht rein virtuell.
Das hält den Patch bei drei Dateien — und macht ihn upstream-tauglich.

### Was der Parser tut und was nicht

Er packt das Bitlayout aus TS 104 089 Annex E aus und **deutet nichts**: keine Alert-Sets,
keine Phasenverläufe, keine Location-Geometrie. Fehlerhafte FIGs werden mit gesetztem
`parseError` gemeldet statt verworfen.

Die Bitpositionen stammen aus der Norm, nicht aus vorhandenen Fork-Parsern: Der eine
(Qt-DAB) ist unvollständig und verwirft ausgerechnet den Heartbeat (`if (CN_bit == 1) return;`),
der andere (WarnBridge) hat Id- und Status-Feld vertauscht und liefert trotzdem plausibel
aussehende Werte.

### Abweichungen von der Skizze in `TODO.md`

Zwei Felder sind in `asa_alert_t` hinzugekommen, die dort nicht standen:

- **`hasNff`** — NFF existiert nur, wenn Location Codes vorhanden sind. Ohne dieses Flag wäre
  `nff == 0` nicht von „nicht vorhanden" zu unterscheiden, und beides bedeutet etwas anderes:
  `nff == 0` heißt „letzte Instanz dieses Alert-Sets".
- **`parseError`** — trägt die Beobachtung über die Prozessgrenze, dass das FIG nicht zur Norm
  passte. Ohne dieses Feld müsste `asamon-rx` die Längenarithmetik ein zweites Mal nachrechnen,
  um zu wissen, was der Parser bereits weiß.

### Versionierung — der offene Punkt aus `TODO.md` Abschnitt 14

> „Ohne Versionsfeld oder Bausymbol passt eines Tages ein alter `asamon-rx` zu einem neuen
> Fork — und es fällt erst beim Alert auf."

Dagegen steht `#define WELLE_ASA_ALERT_VERSION 1` in `radio-controller.h`. `src/controller.h`
prüft es beim Übersetzen:

```cpp
#if !defined(WELLE_ASA_ALERT_VERSION)
#  error "welle.io-Patch 1 (FIG 0/15) fehlt im Submodul."
#elif WELLE_ASA_ALERT_VERSION != 1
#  error "welle.io-Patch 1 hat eine andere Version von asa_alert_t als dieser asamon-rx erwartet."
#endif
```

**Ändert sich die Bedeutung eines Feldes in `asa_alert_t`, wird die Zahl erhöht — an beiden
Stellen.** Ein zusätzliches Feld allein ist kein Grund dazu.

### Als Pull Request

Der Patch ist so geschnitten, dass daraus einer werden kann: ein Commit, keine Vermischung mit
projektspezifischem Code, englische Kommentare, Stil des umgebenden Codes. In welle.io gibt es
zu EWS/ASA/FIG 0/15 bislang **keinen einzigen** PR. Wird er angenommen, verschwindet die
Fork-Last ganz — das ist der einzige Weg, auf dem sie je verschwindet.

---

## Patch 2 — Geräteauswahl über die Seriennummer (**fehlt noch**)

`CRTL_SDR::open_device()` nimmt das erste Gerät, das sich öffnen lässt (`rtl_sdr.cpp`:
„Found N devices. Uses the first working one"). Es gibt **keine** Auswahl über Index oder
Seriennummer.

Für einen Knoten mit **einem** Stick ist das gleichgültig. Mit zwei Sticks hängt es von der
Startreihenfolge ab, welcher Prozess welchen Kanal bekommt — nicht reproduzierbar, und nach
einem Neustart womöglich vertauscht.

**Bis der Patch da ist: nur ein Stick je Knoten.** Solange bleibt `device_serial` im
`init`-Record leer.

Der kleinste Eingriff wäre ein neuer `DeviceParam::RtlSdrSerial`:
`CVirtualInput::setDeviceParam(DeviceParam, const std::string&)` existiert bereits als
Überladung mit Vorgabeimplementierung, und `rtlsdr_get_device_usb_strings()` liefert die
Seriennummern.

---

## Patch 3 — `onMscData()` für den rohen MSC-Strom (**Voraussetzung**)

Ohne diesen Patch baut `asamon-rx` nicht; `src/recorder.h` bricht mit einer ausdrücklichen
Meldung ab — genau wie `src/controller.h` es für Patch 1 tut.

Zwei Dateien, zusammen 20 Zeilen:

| Datei | Änderung |
|---|---|
| `src/backend/radio-controller.h` | `WELLE_MSC_DATA_VERSION`, Rückruf `onMscData()` im `ProgrammeHandlerInterface` |
| `src/backend/decoder_adapter.cpp` | eine Zeile in `addtoFrame()`, neben dem bestehenden `fwrite()` |

### Was er löst

Der rohe Subchannel-Bitstrom war bis dahin **nur über eine Datei** erreichbar:
`addServiceToDecode()` nimmt einen Dateinamen, und `DecoderAdapter` öffnet ihn mit
`fopen(..., "wb")`. Wer den Strom will und keine Datei, muss eine benannte Leitung hinhalten und
am anderen Ende mitlesen. Das kostete in `asamon-rx`:

- eine FIFO unter Unix (`mkfifo`, `O_RDONLY | O_NONBLOCK`, ein Schreib-Deskriptor als
  Lebenszeichen), eine Named Pipe mit **überlappter E/A** unter Windows — zusammen über
  280 Zeilen in `src/platform_*.cpp`, mit den drei Fallen aus `TODO.md` Abschnitt 17;
- eine unsichtbare **Reihenfolgeregel**: Der Leser musste stehen, bevor zugeschaltet wurde —
  unter Unix, weil das `fopen` des Schreibers sonst blockiert, unter Windows, weil `CreateFile`
  die Leitung sonst nicht findet. Zwei verschiedene Gründe für dieselbe Regel;
- einen Lesethread je Aufnahme samt Zeitscheibe, nur damit sich eine Aufnahme abbrechen ließ,
  zu der nie Daten kamen;
- bis zu 4 kB Verlust am Rand: Beim Abschalten stand im stdio-Puffer von welle.io noch, was
  niemand mehr abholte — bei 32 kbit/s knapp eine Sekunde Warn-Audio.

Alles davon ist ersatzlos entfallen. Was blieb, ist eine Klasse: `asamon::MscSink` in
`src/recorder.h`, die den Rückruf entgegennimmt und Records einstellt.

### Warum `onMscData()` nicht rein virtuell ist

Dieselbe Begründung wie bei `onAsaAlert()`, nur ein Interface weiter unten: Alle übrigen `on…`
im `ProgrammeHandlerInterface` sind rein virtuell, und ein weiteres würde jeden Implementierer
im Baum brechen — `welle-gui`, drei Handler in `welle-cli`, zwei Testklassen. Mit
Vorgabeimplementierung bleibt der Patch bei zwei Dateien.

### Was der Rückruf liefert

Byteweise dasselbe, was in die Dumpdatei ginge: den Subchannel-Bitstrom nach Deinterleaving und
Fehlerkorrektur, **vor** der Audiodekodierung, ein Aufruf je DAB-Rahmen. Bei 32 kbit/s sind das
96 Byte alle 24 ms. `MscSink` sammelt bis 4096 Byte, bevor ein `aud`-Record entsteht — sonst
stünden ~42 Records je Sekunde im Strom, jeder mit eigenem Base64-Rahmen.

**Das Record-Format hat sich dadurch nicht geändert.** `docs/record-format.md` gilt unverändert,
und die Fixtures in `tests/fixtures/` ebenso.

### Der Thread und die Zusicherung beim Abschalten

`onMscData()` läuft auf dem Decoder-Thread des Subchannels (`DabAudio::ourThread`), nicht auf
unserem. Es gilt dieselbe Regel wie für alle welle.io-Rückrufe: kopieren, einstellen,
zurückkehren. `Writer::enqueue()` blockiert nie und verwirft im Überlauf — der Entwurf war von
Anfang an darauf ausgelegt (`TODO.md` Abschnitt 8), es kommt nur ein weiterer Rückruf hinzu.

Beim Abschalten trägt eine Zusicherung aus welle.io selbst: `removeSubchannel()` löscht den
`SelectedStream`, damit fällt der letzte `shared_ptr` auf `DabAudio`, und `~DabAudio` **joint
seinen Thread**. Nach der Rückkehr von `removeServiceToDecode()` kann `onMscData()` nicht mehr
gerufen werden — erst dann räumt `Recorder::teardown()` den Restpuffer mit `MscSink::flush()`
ab. Die Reihenfolge ist zwingend und im Code an beiden Stellen begründet.

### Versionierung

`#define WELLE_MSC_DATA_VERSION 1` in `radio-controller.h`, geprüft in `src/recorder.h` —
dieselbe Mechanik wie bei Patch 1 und aus demselben Grund. Ändert sich die Bedeutung der
übergebenen Daten, wird die Zahl an beiden Stellen erhöht.

### Als Pull Request

Auch dieser Patch ist so geschnitten, dass einer daraus werden kann, und er ist **unabhängig von
Patch 1** — zwei getrennte PRs. Der Nutzen reicht über uns hinaus: `welle-cli --dump-msc`
schreibt heute selbst nur Dateien, und jeder, der den rohen Strom weiterverarbeiten will
(XPADxpert, Datenrundfunk), geht bisher denselben Umweg über eine Leitung, den wir gegangen
sind.

---

## Was **nicht** gepatcht wurde, obwohl es naheläge

Zwei Stellen in welle.io gehen davon aus, das oberste CMake-Projekt zu sein, und greifen auf
`${CMAKE_SOURCE_DIR}` zu — der Suchpfad für `FindFFTW3f.cmake` und die git-Abfrage für
`GITHASH`/`GITDESCRIBE`. Eingebunden über `add_subdirectory()` zeigt das auf **uns**.

Beides wird in unserer `CMakeLists.txt` von außen richtiggestellt (`CMAKE_MODULE_PATH`
voranstellen, `GIT_COMMIT_HASH`/`GIT_DESCRIBE` als Cache-Variablen vorgeben), statt den Fork
dafür anzufassen. Jede Zeile Patch, die nicht sein muss, ist eine Zeile weniger beim nächsten
Wechsel des Basis-Commits.

Ebenfalls bewusst nicht angerührt (aus `TODO.md` Abschnitt 10, „Später, nicht jetzt"):

- `decode_audio` durchreichen (`SuperframeFilter(this, true, false)`) — spart bei 32 kbit/s
  kaum Rechenzeit, und FAAD2 bliebe wegen `find_package(Faad REQUIRED)` ohnehin
  Bauabhängigkeit.
- Zugriff auf die Subchannel-Ebene ohne Service-Umweg, falls ein Ensemble den Warn-Subchannel
  ohne Eintrag in FIG 0/2 sendet.

---

## Den Patch auf einen anderen Stand übertragen

```bash
cd external/welle.io
git fetch upstream next
git checkout -b asa-fig0-15-neu <neuer-commit>
git am ../../patches/0001-add-fig-0-15-ews-asa-decoding.patch
git am ../../patches/0003-add-onmscdata-callback.patch
git push origin asa-fig0-15-neu
```

Die Reihenfolge ist die der Bezeichnungen, nicht die der Dateinamen — `0002` fehlt, siehe oben.

Danach im Hauptrepo den neuen Stand festhalten — `git add asamon-rx/external/welle.io` — und
`.gitmodules` auf den neuen Zweig zeigen lassen.

Danach **immer** `ctest --test-dir build` laufen lassen: `tests/fixtures/fig0_15.fixtures`
enthält die erwarteten Records byteweise, ein Abweichen fällt sofort auf. Und diesen Abschnitt
mit den neuen Commit-Kennungen fortschreiben.
