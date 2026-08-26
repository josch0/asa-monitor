# Der welle.io-Fork: welche Patches er trägt und warum

`asamon-rx` baut gegen einen eingebundenen welle.io-Quellbaum unter `external/welle.io`.
Dieses Dokument hält fest, worauf der Bau festgenagelt ist und was daran verändert wurde.

## Festgenagelter Stand

| | |
|---|---|
| Upstream | `https://github.com/AlbrechtL/welle.io`, Zweig `next` |
| Basis-Commit | `fe06fadf561a954f56580fd9a674316488ae4973` (13.08.2026, `v2.7-102-gfe06fadf`) |
| Zweig im Klon | `asa-fig0-15` |
| Commit mit Patch 1 | `296e5d306008080ad1de735d1ed212b8efae10e5` |
| Patchdatei | [`../patches/0001-add-fig-0-15-ews-asa-decoding.patch`](../patches/0001-add-fig-0-15-ews-asa-decoding.patch) |

**Der Commit wird hart festgenagelt.** Ohne installierte Header ist die welle-API **keine
zugesagte Schnittstelle**; sie kann sich zwischen zwei Commits ändern, ohne dass das irgendwo
als Bruch auftaucht. Ein Wechsel des Basis-Commits ist deshalb eine bewusste Handlung mit
Gegenprobe, kein `git pull`.

> **Noch offen: das öffentliche Fork-Repo.** Der Patch liegt bisher nur als lokaler Zweig und
> als Patchdatei vor. Weil das gebaute Binary ein **verändertes** welle.io enthält, muss dessen
> Quelltext samt Änderungen bei Weitergabe verfügbar sein — ein öffentlicher Fork erledigt das.
> Sobald die URL feststeht, wird das Submodul dorthin umgehängt und dieser Abschnitt ergänzt.
> Solange nur lokal gebaut wird, entsteht keine Weitergabe und damit keine Auflage.

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

## Was **nicht** gepatcht wurde, obwohl es naheläge

Zwei Stellen in welle.io gehen davon aus, das oberste CMake-Projekt zu sein, und greifen auf
`${CMAKE_SOURCE_DIR}` zu — der Suchpfad für `FindFFTW3f.cmake` und die git-Abfrage für
`GITHASH`/`GITDESCRIBE`. Eingebunden über `add_subdirectory()` zeigt das auf **uns**.

Beides wird in unserer `CMakeLists.txt` von außen richtiggestellt (`CMAKE_MODULE_PATH`
voranstellen, `GIT_COMMIT_HASH`/`GIT_DESCRIBE` als Cache-Variablen vorgeben), statt den Fork
dafür anzufassen. Jede Zeile Patch, die nicht sein muss, ist eine Zeile weniger beim nächsten
Wechsel des Basis-Commits.

Ebenfalls bewusst nicht angerührt (aus `TODO.md` Abschnitt 10, „Später, nicht jetzt"):

- Rückruf statt Datei für den MSC-Dump, falls sich die FIFO im Betrieb als zu heikel erweist.
- `decode_audio` durchreichen (`SuperframeFilter(this, true, false)`) — spart bei 32 kbit/s
  kaum Rechenzeit, und FAAD2 bliebe wegen `find_package(Faad REQUIRED)` ohnehin
  Bauabhängigkeit.
- Zugriff auf die Subchannel-Ebene ohne Service-Umweg, falls ein Ensemble den Warn-Subchannel
  ohne Eintrag in FIG 0/2 sendet.

---

## Den Patch auf einen anderen Stand übertragen

```bash
cd external/welle.io
git fetch origin next
git checkout -b asa-fig0-15-neu <neuer-commit>
git am ../../patches/0001-add-fig-0-15-ews-asa-decoding.patch
```

Danach **immer** `ctest --test-dir build` laufen lassen: `tests/fixtures/fig0_15.fixtures`
enthält die erwarteten Records byteweise, ein Abweichen fällt sofort auf. Und diesen Abschnitt
mit den neuen Commit-Kennungen fortschreiben.
