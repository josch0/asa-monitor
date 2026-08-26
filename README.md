# ASA-Monitor

Crowd-basiertes Monitoring von **ASA/EWS-Warnmeldungen in DAB+** mit verteilten SDR-Empfängern,
zentraler Erfassung und Darstellung auf einer Web-Karte samt Meldungsliste.

> **Status: Der Empfangsknoten steht — der Feldtest fehlt.** `asamon-rx` ist gebaut und
> geprüft (siehe [`asamon-rx/`](asamon-rx/)); was noch aussteht, braucht einen RTL-SDR-Stick
> an einer Antenne. Server, Frontend und `asamon-node` sind noch nicht begonnen.

## Aufgabenstellung (Originalauftrag)

Ein System, das mittels RTL-SDR oder anderer SDR-Lösungen DAB/DAB+ und die darin enthaltenen
ASA/EWS-Meldungen monitort und auf einer Webseite als Karte/Liste anzeigt. Der Aufbau ist
**dezentral und crowd-based**: Empfänger sind über das ganze Land verteilt, monitoren jeweils
einen oder mehrere DAB-Kanäle und schicken ihre Daten an einen zentralen Server.
In diesem Ordner wird alles zum System gesammelt und nach und nach ausgebaut:
Specs, Backend, DAB-Decoder, Frontend.

## Worum es fachlich geht

**ASA (Automatic Safety Alert)** ist die deutsche Ausprägung des international standardisierten
**DAB Emergency Warning System (EWS)** nach **ETSI TS 104 089**. Warnungen der Behörden (BBK/Länder)
laufen über **MoWaS** als CAP-XML zu den Rundfunkveranstaltern, werden dort vertont und im DAB+-Mux
als Audio-Warnmeldung ausgestrahlt. Die Steuerinformation dazu — welcher Subchannel, welche
Warnstufe, welcher Vorfall, welches Warngebiet — steckt in einem einzigen neuen FIC-Element:
**FIG 0/15**. Genau dieses FIG ist der Anknüpfungspunkt des Projekts.

Fachliche Details, Bitlayouts und Abläufe: **[`specs/asa.md`](specs/asa.md)** — das ist das zentrale
Dokument zum Wiedereinstieg.

## Warum ein verteiltes Netz nötig ist

ASA-Signalisierung ist **pro Ensemble** unterschiedlich: Jedes Ensemble signalisiert nur eigene Alerts
sowie OE-Alerts (Verweise) auf Ensembles mit überlappendem Versorgungsgebiet. Es gibt keine zentrale
Stelle, an der man „alle ASA-Meldungen Deutschlands" abgreifen könnte — man muss die einzelnen
Ensembles vor Ort empfangen. Deutschland hat den Bundesmux (Kanal 5C, dort ist der Service „ASA DE"
geplant), einen zweiten Bundesmux sowie zahlreiche Landes- und Lokalmuxe. Ein Crowd-Netz aus
RTL-SDR-Knoten ist damit der naheliegende Weg zu bundesweiter Abdeckung.

## Ordnerstruktur

```
asa-monitor/
├── README.md            # dieses Dokument: Auftrag, Kontext, Stand
├── asamon-rx/           # Empfangsprozess (C++): SDR → FIC → FIG 0/15 auspacken
│   ├── README.md        # bauen, starten, prüfen
│   ├── TODO.md          # der Umsetzungsplan, samt Abschnitt 16: was dabei herauskam
│   ├── docs/            # record-format.md, welle-patches.md
│   ├── src/  tests/  contrib/  patches/
│   └── external/welle.io/       # Submodul, festgenagelter Commit, mit Patch 1
├── asamon-node/         # Deutung, Uplink (Go) — noch leer
└── specs/
    ├── asa.md           # Fachliche Zusammenfassung aller Specs (Hauptdokument)
    ├── decoder-optionen.md      # Analyse der Open-Source-DAB-Decoder als Client-Basis
    ├── client-konzept-review.md # Bewertung des Client-Entwurfs (Puffer, Batch, Audio)
    ├── client-architektur.md    # Architekturskizze des Knotens auf der welle.io-Bibliothek
    ├── QUELLEN.md       # Alle Quellen mit Links + Verfügbarkeitsstatus
    ├── etsi/            # Offizielle ETSI-Deliverables (PDF)
    ├── de/              # Deutsche Umsetzungsdokumente (PDF)
    └── text/            # Textextrakte aller PDFs (durchsuchbar via grep)
```

Die Textextrakte in `specs/text/` sind mit `pdftotext -layout` erzeugt und dienen dazu, in späteren
Sitzungen ohne PDF-Reader gezielt in den Specs suchen zu können:

```bash
grep -n "FIG 0/15" specs/text/ts_104089v010101p.txt
```

Geplante, noch nicht angelegte Bereiche: `backend/` (Server), `frontend/` (Karte und Liste) — und `asamon-node/`, das Gegenstück zu `asamon-rx` auf dem Knoten.

## Aktueller Stand

- [x] Fachliche Grundlagen recherchiert (ASA = EWS nach ETSI TS 104 089, Signalisierung über FIG 0/15)
- [x] Offizielle ETSI-Specs heruntergeladen (`specs/etsi/`)
- [x] Deutsche Umsetzungsdokumente heruntergeladen (`specs/de/`), v.a. ASA-Guidelines V2.0 (Juli 2026)
- [x] Zusammenfassung mit Ablauf und Anforderungen erstellt (`specs/asa.md`)
- [x] Open-Source-DAB-Decoder als mögliche Client-Basis analysiert (`specs/decoder-optionen.md`),
      inkl. aller Branches und aller seit 2025 aktiven Forks — Kernbefund: **im Upstream wertet
      kein Projekt FIG 0/15 aus** (auch die Sendeseite nicht); in Forks existieren zwei
      unvollständige Parser, davon einer mit falschem Bitlayout
- [x] Verwandtes Projekt gefunden: **WarnBridge** (`TogeriX-hub/dab-warnings-meshcore`) —
      DAB+-Warnmeldungen auf dem Pi empfangen und ins LoRa-Mesh einspeisen; welle.io-Fork mit
      ASA-Feldern in `mux.json`
- [x] Client-Entwurf (Kanal-Monitoring, Puffer, 10-s-Batch, Audio-Mitschnitt) bewertet
      (`specs/client-konzept-review.md`) — Audio: beim Trigger zuschalten genügt, kein
      Multiplex-Ringpuffer nötig; daher ist der Knoten deutlich leichter als zunächst angesetzt
- [x] Architekturskizze des CLI-Knotens auf dem welle.io-Backend (`specs/client-architektur.md`)
      — Kernbefund: `onFIBDecodeSuccess()` liefert jeden rohen FIB, der FIG-0/15-Parser braucht
      daher **keinen Fork**; es bleiben drei Berührungspunkte zum Backend. Zuschnitt: zwei
      Prozesse, `asa-rx` (C++, kennt kein ASA) und `asa-node` (Go), verbunden über einen
      Record-Strom, der zugleich IPC-Protokoll, Archivformat und Beleg ist
- [x] Einbindung der welle.io-Bibliothek verifiziert (26.08.2026, Zweig `next`): **`libwelle`
      ist kein eigenständiges Projekt**, sondern das CMake-Ziel `welle` innerhalb von welle.io
      — ohne Header-Installation, pkg-config oder `find_package`-Export. Folge: eingebundener
      Quellbaum auf festgenageltem Commit, **keine Kopie ins eigene Repo**. Damit ist
      `specs/decoder-optionen.md` in den Abschnitten 3.1 und 4 überholt — dort vermerkt
- [x] **Richtungsentscheidung 26.08.2026: FIG 0/15 wird im welle.io-Backend geparst.** Der
      `FIBProcessor` zerlegt jeden CRC-geprüften FIB ohnehin in FIGs und ruft je Extension
      einen Handler auf; FIG 0/15 fällt dort bisher in den `default`-Zweig. Diesen Zweig
      ergänzen wir, samt Rückruf `onAsaAlert()`. Über die Prozessgrenze gehen dann **geparste
      Ereignisse statt roher FIBs**: rund ein Record je Sekunde statt 125, und der
      Gegendruckfall (voller Pipe-Puffer → blockierender `asa-rx` → Samplverlust) entfällt als
      Betriebsrisiko. Preis: ein dauerhafter Patch und damit **doch ein Fork** von welle.io,
      der bei Weitergabe öffentlich sein muss. Details in `specs/client-architektur.md`,
      Abschnitte 1, 2b, 2a und 4a — die dortige frühere Aussage „null Änderungen am Backend"
      ist damit hinfällig
- [x] **Record-Format entschieden (26.08.2026): NDJSON**, ein JSON-Objekt je Zeile. Rohe FIBs
      verlassen den Empfangsprozess gar nicht mehr — kein FIB-Ring, kein Abruf; als Beleg
      genügen die rohen FIG-Bytes im Ereignis selbst, Parserfehler gehen ins Log. Damit
      entfällt das einzige Argument für ein Binärlayout (125 Records je Sekunde), und der Strom
      wird selbsterklärend, werkzeugfähig (`jq`) und ohne Versionssprung erweiterbar.
      Feldliste in `asamon-rx/TODO.md` Abschnitt 7
- [x] **Mehrkanalbetrieb festgelegt (26.08.2026):** Ein Knoten kann mit mehreren RTL-SDR-Sticks
      mehrere Kanäle gleichzeitig überwachen — je Kanal ein `asa-rx`, darüber **ein**
      `asa-node` für alle. Gründe: Die Knotenidentität (Schlüssel, Position, Spool) ist
      knotenweit; eine systemd-Unit statt N ist für ein Netz auf fremden Pis das wichtigere
      Kriterium; ein 10-s-Batch trägt alle Kanäle; und **OE-Verweise werden lokal auflösbar** —
      verweist Kanal A auf ein Ensemble, das derselbe Knoten auf Kanal B empfängt, geht der
      Recorder dort sofort scharf, ohne Serverrunde. Preis: Ein Fehler in `asa-node` trifft
      alle Kanäle; dagegen `recover()` je Kanal-Goroutine, eigene Warteschlange je Kanal und
      Stick-Auswahl über die Seriennummer. Letztere braucht einen weiteren welle.io-Patch —
      `CRTL_SDR` öffnet heute schlicht das erste Gerät, das sich öffnen lässt, und ist damit
      für mehrere Sticks nicht reproduzierbar (`specs/client-architektur.md` Abschnitte 4a
      und 6)
- [x] **`asamon-rx` umgesetzt (26.08.2026), Meilensteine M0 bis M4.** Empfangsprozess in C++
      gegen die welle.io-Bibliothek, mit dem FIG-0/15-Patch. NDJSON-Records (`init`, `tlm`,
      `ens`, `asa`, `aud`), Ausgabethread mit Vorrangregel beim Verwerfen, Recorder über FIFO,
      Zeilenkommandos auf stdin, systemd-Unit mit Watchdog. Sechs Testprogramme, keines braucht
      einen SDR-Stick. Was dabei anders kam als geplant, steht in `asamon-rx/TODO.md`
      Abschnitt 16 — unter anderem: die sieben Testszenarien aus TS 104 090 sind
      *Empfänger*-Konformitätstests und als Bitmuster nicht verwertbar; brauchbar ist dort
      Tabelle A.19 mit den Byte-Längen echter Location-Code-Sätze
- [ ] **Feldtest mit RTL-SDR-Stick** — der eigentliche Zweck: Sendet 5C schon Heartbeats? Und
      stimmt die WarnBridge-Behauptung, dort komme alle 5 Minuten ein Test-Alert?
- [ ] `asamon-node` (Go): Deutung, Alert-Sets, Location-Geometrie, Spool, Uplink
- [ ] Server und Frontend — noch nicht begonnen

## Offene Entscheidungen (noch nicht zu treffen, nur notiert)

- Stack für den Server. Für den Knoten ist die Sprachfrage in der Architekturskizze bereits
  entschieden: C++ für `asa-rx` (weil es gegen die welle.io-Bibliothek linkt), Go für
  `asa-node` (`specs/client-architektur.md` Abschnitt 4a)
- ~~**Record-Format** zwischen `asa-rx` und `asa-node`~~ — **entschieden und umgesetzt**:
  `asamon-rx/docs/record-format.md`
- ~~**Decoder-Ansatz**~~ — **entschieden und umgesetzt**: welle.io-Bibliothek mit dem
  FIG-0/15-Patch (Variante V2c). eti-cmdline bleibt als Werkzeug für Gegenproben
- Datenmodell und Ingest-Protokoll zwischen Knoten und Server
- Umgang mit Vertrauen/Verifikation bei Crowd-Daten (mehrere Knoten melden dasselbe Ereignis)
- ~~Lizenz des Knotens~~ — **entschieden**: `asamon-rx` ist von Anfang an **GPL-3.0-or-later**
  deklariert, mit `LICENSE` und SPDX-Kopf in jeder Quelldatei. Der Server bleibt als
  eigenständiges Programm frei lizenzierbar. Offen bleibt nur die Auflage, die nicht zur Wahl
  steht: Wir geben ein **verändertes** welle.io weiter, dessen Quelltext samt Änderungen
  verfügbar sein muss — dafür braucht es einen **öffentlichen Fork**, der noch fehlt
- Ob der FIG-0/15-Patch welle.io als Pull Request angeboten wird. Upstream gibt es dazu
  **keinen einzigen** PR; angenommen würde er die Fork-Last ganz beseitigen. Der Patch ist
  bereits so geschnitten, dass daraus einer werden kann: ein Commit, englische Kommentare,
  keine Vermischung mit projektspezifischem Code
  (`asamon-rx/patches/0001-add-fig-0-15-ews-asa-decoding.patch`)

## Zeitlicher Kontext

- 09/2024: ETSI TS 104 089 / TS 104 090 veröffentlicht
- 03/2025: ASA-Zertifizierungsprogramm gestartet (DTG Testing)
- 07/2026: ASA-Guidelines V2.0 von Digitalradio Deutschland e.V. veröffentlicht
- **10.09.2026: Start des ASA-Regelbetriebs in Deutschland** (bundesweiter Warntag)

Das heißt: Zum Zeitpunkt dieses Dokuments (August 2026) läuft ASA im Testbetrieb, der Regelbetrieb
steht unmittelbar bevor. Live-Testmeldungen sind also bereits empfangbar.
