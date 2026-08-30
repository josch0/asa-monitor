# asamon-node — Umsetzungsplan

Dieses Dokument ist der vollständige Auftrag für die Umsetzung von `asamon-node`. Es legt
Struktur, Datenmodell, Protokoll und Meilensteine fest. Wer danach implementiert, soll keine
Architekturentscheidung mehr selbst treffen müssen — und wo doch eine nötig wird, steht in
Abschnitt 19, wie damit zu verfahren ist.

Stand: 26.08.2026. Voraussetzung: `asamon-rx` ist gebaut und geprüft (M0–M4).

---

## 1. Worum es geht — in zehn Zeilen

`asamon-node` ist der **Knotenprozess** des [ASA-Monitors](../README.md). Er startet je
überwachtem DAB-Kanal einen `asamon-rx`-Subprozess, liest deren NDJSON-Record-Ströme,
**deutet** sie — Alert-Sets, Phasenverläufe, Heartbeat-Lücken, Warngebiete — und schickt das
Ergebnis alle zehn Sekunden als einen Datensatz an den Server. Fällt der Server aus, sammelt er
weiter und liefert später alles nach. Weil andere Knoten dieselben Signale empfangen, trägt
jeder Datensatz **Hashes**, über die der Server Duplikate erkennt und zusammenführt.

Ein Knoten, eine Konfigurationsdatei, ein Log, eine systemd-Unit — gleich wie viele Sticks
stecken.

```
┌─ asamon-rx · 5C ──┐                ┌─ asamon-node · Go ───────────────┐
│ C++ · welle.io    │──NDJSON──────▶ │ Kanalzustand · ASA-Automat       │
│ FIG-0/15-Bitparser│◀──REC/STOP──── │ Location-Geometrie · Hashes      │──HTTPS──▶ Server
└───────────────────┘                │ Spool · Uplink · Audio           │
┌─ asamon-rx · 11D ─┐                │ ein 10-s-Datensatz für alle      │
│ …                 │──NDJSON──────▶ │ Kanäle des Knotens               │
└───────────────────┘                └──────────────────────────────────┘
```

### Die Specs — vor dem Anfangen lesen

| Datei | Warum |
|---|---|
| `../specs/asa.md` | **Zuerst.** Fachliche Grundlage: FIG-0/15-Bitlayout (Abschnitt 3), Phasenablauf (4), Location Coding (5), was ein Monitor auswerten muss (8) |
| `../asamon-rx/docs/record-format.md` | **Verbindlich.** Die Eingangsseite von `asamon-node`, Feld für Feld |
| `../specs/client-architektur.md` | Abschnitte 3, 4, 4a, 5 — Bausteine, Nebenläufigkeitsregeln, Prozessmodell, Go-Festlegung |
| `../specs/client-konzept-review.md` | Abschnitte 3.1–3.6 und 4 — warum zwei Uplink-Pfade, warum leere Datensätze, warum roher Audio-Bitstrom |
| `../asamon-rx/README.md` | Kommandozeile und Kommandos des Subprozesses |
| `../asamon-rx/TODO.md` Abschnitt 16 | Was bei der `asamon-rx`-Umsetzung anders kam als geplant — inklusive der Location-Code-Byte-Längen aus TS 104 090 Tabelle A.19, die hier als Testvektoren wiederverwendet werden |

---

## 2. Was `asamon-node` ist — und was ausdrücklich nicht

**Ist:** Prozessverwaltung, Deutung, Zustand, Geometrie, Identität, Spool, Uplink.

**Ist ausdrücklich nicht:**

- **Kein Bitparser für FIG 0/15.** Das macht der welle.io-Fork, und `asamon-rx` reicht die
  Felder fertig heraus. `asamon-node` liest `location_codes` als Hex und deutet **nur diese**.
- **Kein FIC-Parser.** FIG 0/0, 0/1, 0/2, 0/9, 0/10 und 1/x kommen als `ens`- und
  `tlm`-Records fertig an. Genau dafür gibt es diese Record-Typen.
- **Kein Audio-Decoder.** Der rohe Subchannel-Bitstrom wird durchgereicht, nicht dekodiert.
  Kein AAC, kein FFmpeg, keine Superframe-Zerlegung — die Datei geht so zum Server, wie sie
  vom Kanal kam.
- **Kein Server.** Datenhaltung, Karte, Korrelation über Knoten hinweg gehören ins Backend.
  `asamon-node` liefert Beobachtungen samt Hashes; die Zusammenführung ist Serversache.
- **Kein SDR-Code.** Kein librtlsdr, kein cgo. `CGO_ENABLED=0` ist verbindlich.

---

## 3. Die Eingangsseite steht bereits fest

`asamon-rx` schreibt **NDJSON auf stdout, Logs auf stderr**. Fünf Record-Typen, alle mit
`type`, `seq`, `ts`:

| Typ | Rate | Was `asamon-node` daraus macht |
|---|---|---|
| `init` | 1 je Strom | Kanal, Frequenz, Gerät, Fassungen — Kopf jedes Kanalabschnitts im Datensatz |
| `tlm` | 1/s | Empfangsqualität aggregieren, **Ensemble-Uhr** stellen, Verwurf- und Parserzähler übernehmen |
| `ens` | bei Änderung | Ensembleaufbau; Quelle für `ens_hash` und `ens_content_hash` |
| `asa` | 1/s, im Alarmfall bis 12/s | der Kern: Heartbeat-Überwachung, Alert-Sets, Phasenautomat |
| `aud` | nur im Alarmfall | Chunks des rohen Subchannel-Bitstroms, Base64 |

Kommandos zurück auf stdin: `REC <subChId>`, `STOP <subChId>`, `QUIT`.

**Drei Eigenschaften des Stroms, auf die sich der Entwurf stützt:**

1. **`seq` ist lückenlos, solange nichts verworfen wurde.** Eine Lücke ist genau ein Verwurf und
   muss als solcher in den Datensatz — nicht stillschweigend geschluckt werden.
2. **`ts` ist Knotenzeit**, RFC 3339 mit Nanosekunden. Als String zu lesen und als String zu
   behalten, wo er nur weitergereicht wird.
3. **`raw` im `asa`-Record ist der Beleg.** Er wird **immer** unverändert weitergereicht, auch
   wenn die eigene Deutung scheitert. Er ist außerdem Bestandteil des Hashes.

**Regel für unbekannte Felder und Typen:** überlesen, zählen, im Datensatz melden
(`unknown_records`) — niemals als Fehler behandeln. Das Format darf additiv wachsen.

**Regel für `format_version`:** Weicht sie von der erwarteten (1) ab, wird der Kanal mit einer
deutlichen Fehlermeldung **nicht** gestartet. Ein stillschweigend falsch gedeuteter Strom ist
schlimmer als ein fehlender Kanal.

---

## 4. Konfiguration: `node-config.yaml`

Eine Datei, vollständig, ohne Umgebungsvariablen-Magie. Suchreihenfolge: `--config <pfad>`,
sonst `./node-config.yaml`, sonst `/etc/asamon/node-config.yaml`.

```yaml
node:
  # Frei wählbarer Anzeigename. 1..20 Zeichen (Unicode-Zeichen, nicht Bytes).
  # Keine Steuerzeichen. Nicht netzweit eindeutig — die Identität ist node_id.
  name: "Berlin-Mitte-01"

  # Standort des Knotens als ASA Location Code im Präsentationsformat
  # (TS 104 089 Annex A): drei Blöcke à vier Symbole "1".."8".
  # Auflösung rund 1 km — bewusst grob, das schützt die Freiwilligen.
  location_code: "2366-7443-8484"

  # Rein beschreibend, beides optional.
  antenna: "Dachantenne Omni, 10 m ü. Grund"
  contact: "mail@example.org"

server:
  url: "https://asa.example.org"
  report_interval: "10s"        # Vorgabe 10s
  timeout: "15s"                # HTTP-Timeout je Anfrage
  insecure_skip_verify: false   # nur für lokale Tests

# 1..n Kanäle. Reihenfolge ist die Anzeigereihenfolge.
channels:
  - channel: "5C"               # Pflicht, DAB-Kanalname
    device: "rtl_sdr"           # Vorgabe rtl_sdr; rawfile nur für Tests
    device_serial: ""           # leer = erstes Gerät. Pflicht ab zwei Sticks (Patch 2)
    gain: "auto"                # auto oder Index
    iq_file: ""                 # nur bei device: rawfile
  - channel: "11D"
    device: "rtl_sdr"
    device_serial: "00000002"

audio:
  enabled: true
  post_roll: "10s"              # Nachlauf nach der End-Phase
  max_seconds: 300              # harte Notbremse je Aufnahme
  keep_days: 7                  # hochgeladene Dateien so lange behalten, dann löschen

paths:
  rx_binary: "/usr/local/bin/asamon-rx"
  state_dir: "/var/lib/asamon"  # node_id, node_key, seq, spool/, audio/

limits:
  max_spool_mb: 512
  queue_size: 4096              # je Kanal, Records
  max_reports_per_request: 60   # beim Nachliefern

log:
  level: "info"                 # error|warn|info|debug
```

### Validierung — vollständig, beim Start und bei `--check`

| Feld | Regel | Bei Verstoß |
|---|---|---|
| `node.name` | 1–20 Zeichen, keine Steuerzeichen, nach NFC normalisiert | Abbruch |
| `node.location_code` | Format `dddd-dddd-dddd` mit Symbolen `1`–`8`, **Prüfsumme muss stimmen** (Abschnitt 9) | Abbruch |
| `channels` | mindestens einer; gültiger DAB-Kanalname; **kein Kanalname doppelt** | Abbruch |
| `channels[].device_serial` | bei mehr als einem Kanal für **jeden** gesetzt und paarweise verschieden | Abbruch, mit Hinweis auf Patch 2 |
| `server.url` | gültige URL; `http://` erlaubt, aber Warnung im Log | Warnung |
| `paths.rx_binary` | existiert und ist ausführbar | Abbruch |
| `paths.state_dir` | existiert oder ist anlegbar, beschreibbar | Abbruch |
| Dauerangaben | `time.ParseDuration`-Syntax, als String im YAML | Abbruch |

`asamon-node --check` prüft alles Obige, gibt den dekodierten Standort als Rechteck samt
Mittelpunkt aus und beendet sich mit 0 oder 1. Das ist die Abnahme für Meilenstein N0.

### Die eine erlaubte Fremdabhängigkeit

YAML ist nicht in der Standardbibliothek. **`gopkg.in/yaml.v3` ist die einzige zugelassene
Fremdabhängigkeit** des ganzen Programms. Alles Weitere — HTTP, JSON, Kryptografie, Logging,
Prozessverwaltung — kommt aus der Standardbibliothek. Wer eine zweite Abhängigkeit für nötig
hält, notiert das in Abschnitt 19, statt sie hinzuzufügen.

`yaml.v3` wird mit `KnownFields(true)` betrieben: **ein Tippfehler im Schlüssel ist ein
Fehler**, kein stillschweigend ignoriertes Feld. Auf einem Knoten, den ein Freiwilliger
einrichtet und nie wieder anfasst, ist das der Unterschied zwischen „läuft" und „läuft
scheinbar".

---

## 5. Knotenidentität

Drei Dinge, sauber getrennt:

| | Was | Wo | Wechselt |
|---|---|---|---|
| `node_id` | UUIDv4, beim ersten Start erzeugt | `<state_dir>/node_id`, Modus 0600 | nie |
| `name` | Anzeigename, max. 20 Zeichen | `node-config.yaml` | jederzeit |
| `location_code` | Standort, ASA-Präsentationsformat | `node-config.yaml` | beim Umzug |

**Warum beides.** Der Name ist frei wählbar und damit netzweit weder eindeutig noch stabil —
zwei Freiwillige nennen ihren Knoten „Zuhause". Die `node_id` ist der Schlüssel, unter dem der
Server Beobachtungen verkettet; der Name ist Anzeige. Wird der Name geändert, bleibt die
Historie erhalten.

UUIDv4 ohne Fremdabhängigkeit: 16 Byte aus `crypto/rand`, Version- und Variant-Bits nach
RFC 4122 setzen, als `8-4-4-4-12` formatieren. Zwanzig Zeilen im Paket `identity`.

### Schlüsselpaar jetzt anlegen, noch nicht verwenden

Eine Authentifizierung an der API ist **nicht** vorgesehen — das bleibt so. Trotzdem erzeugt
der Knoten beim ersten Start ein **Ed25519-Schlüsselpaar** (`crypto/ed25519`, Standardbibliothek)
in `<state_dir>/node_key` (0600) und schickt den **öffentlichen** Teil in jedem Datensatz mit
(`node.pubkey`, base64).

Begründung: Der Tag, an dem eine offene API in einem Crowd-Netz missbraucht wird, kommt
erfahrungsgemäß. Wird dann Signieren nachgerüstet, sind die Schlüssel bereits da, seit dem
ersten Tag mit den Daten verknüpft, und **kein Knoten muss neu identifiziert werden**. Kosten
heute: 32 Byte je Datensatz und rund dreißig Zeilen Code. Signiert wird vorerst nicht.

---

## 6. Repo-Layout

```
asamon-node/
├── README.md                  # bauen, konfigurieren, betreiben (am Ende zu schreiben)
├── TODO.md                    # dieses Dokument
├── LICENSE                    # GPL-3.0-or-later, wie asamon-rx
├── go.mod  go.sum
├── Makefile                   # build, test, vet, cross (arm64/armv7)
├── cmd/
│   ├── asamon-node/main.go    # Flags, Start, Signale — sonst nichts
│   └── fake-rx/main.go        # Testhilfe: spielt eine NDJSON-Datei als asamon-rx nach,
│                              # samt Kommandos auf stdin, wahlweise mit Absturz
├── internal/
│   ├── config/                # node-config.yaml: laden, validieren, Vorgaben
│   ├── identity/              # node_id, Ed25519-Schlüssel, state_dir
│   ├── record/                # NDJSON-Records von asamon-rx: Typen, Leser, Zähler
│   ├── loc/                   # ASA Location Coding, beide Richtungen (Abschnitt 9)
│   ├── rxproc/                # asamon-rx starten, beaufsichtigen, Kommandos senden
│   ├── chanstate/             # Kanalzustand + ASA-Zustandsmaschine (Abschnitt 8)
│   ├── hashes/                # kanonische Hashes, mit Testvektoren (Abschnitt 10)
│   ├── report/                # Datensatzmodell und Bau (Abschnitt 11)
│   ├── spool/                 # Store-and-Forward (Abschnitt 13)
│   ├── uplink/                # HTTP, Retry, Audio-Upload (Abschnitt 12)
│   ├── audio/                 # Mitschnitte: sammeln, schreiben, aufräumen (Abschnitt 14)
│   └── buildinfo/             # Version, Commit, gesetzt über -ldflags
├── contrib/
│   ├── asamon-node.service    # eine Unit für den ganzen Knoten, mit Watchdog
│   └── node-config.example.yaml
├── docs/
│   ├── uplink-protokoll.md    # das Vertragsdokument zum Server
│   ├── node-config.md         # jede Option, ausgeschrieben
│   └── hashes.md              # die Kanonisierungsregeln, mit Testvektoren
└── testdata/
    ├── streams/               # aufgezeichnete NDJSON-Ströme (auch synthetische)
    ├── locations/             # Testvektoren fürs loc-Paket, u. a. aus TS 104 090 A.19
    └── golden/                # erwartete Datensätze zu den Strömen
```

**`internal/` ist Absicht:** Nichts davon ist öffentliche API. Was der Server braucht, steht in
`docs/uplink-protokoll.md`, nicht in einem importierbaren Go-Paket.

Modulpfad: `github.com/josch0/asa-monitor/asamon-node`. Go **≥ 1.22** (wegen `log/slog` und
`math/rand/v2`).

---

## 7. Kanal-Lebenszyklus: `asamon-rx` als Subprozess

Je konfiguriertem Kanal genau ein `asamon-rx`. `asamon-node` besitzt den Lebenszyklus —
**eine** systemd-Unit für den ganzen Knoten.

### Start

```
<rx_binary> --channel <channel> --device <device> [--device-serial <s>]
            [--gain <g>] [--iq-file <p>] --log-level <level>
```

`--device-serial` erst mitgeben, wenn `asamon-rx` es kennt (Patch 2). Bis dahin: Abbruch bei
mehr als einem Kanal, mit einer Fehlermeldung, die genau das erklärt.

- **stdout**: `bufio.Scanner` mit vergrößertem Puffer — `aud`-Records mit Base64 können
  mehrere Kilobyte lang werden. Puffer **1 MB**, sonst reißt der Strom im Alarmfall ab.
- **stderr**: zeilenweise ins eigene Log, mit Präfix `rx[<channel>]`, Stufe `warn`. Niemals
  verwerfen — dort stehen die Parserfehler.
- **stdin**: bleibt offen, dient den Kommandos. Nach `QUIT` schließen.

### Beaufsichtigung

| Lage | Verhalten |
|---|---|
| Prozess endet (gleich mit welchem Code) | Neustart mit **exponentiellem Backoff**: 1 s, 2 s, 4 s … max 60 s; nach einer Minute ohne Absturz wird das Backoff zurückgesetzt |
| Neustart | `rx_restarts` je Kanal zählen, im Datensatz melden. **Kanalzustand bleibt erhalten**, nur die Sequenzverfolgung beginnt neu (jeder Strom hat sein eigenes `seq`) |
| Prozess reagiert nicht auf `QUIT` | nach 5 s `SIGTERM`, nach weiteren 5 s `SIGKILL` |
| `asamon-node` bekommt `SIGTERM`/`SIGINT` | allen Kindern `QUIT`, letzten Datensatz bauen und **einmal** zu senden versuchen (Frist: `server.timeout`), sonst in den Spool; dann beenden. Gesamtfrist 20 s |
| Stick fehlt beim Start | `asamon-rx` endet mit Fehler → normales Backoff; der Kanal meldet `rx_state: "failed"` mit der letzten stderr-Zeile als `last_error` |

**Ein toter Kanal beendet niemals den Knoten.** Die übrigen Kanäle laufen unterbrechungsfrei
weiter, und ein Kanal in Dauerneustart ist selbst eine meldenswerte Beobachtung.

### Was ein Strom-Neustart für die Deutung bedeutet

Nach einem Neustart kommt wieder ein `init`, und `seq` beginnt bei 0. Der Kanalzustand darf
laufende Alerts **nicht** stillschweigend fortführen: Jeder Alert, der über einen
Strom-Neustart hinweg besteht, bekommt `gap: true` und wird beim Server als möglicherweise
unvollständig kenntlich. Die ein bis zwei Sekunden Neusynchronisation sind im Telemetriestrom
ohnehin als Lücke sichtbar.

---

## 8. Der Kanalzustand und die ASA-Zustandsmaschine

Das fachliche Herzstück. Je Kanal eine Goroutine, die ihren Zustand **allein besitzt** —
Zugriff von außen ausschließlich über Kanäle (`chan`). Keine Mutexe auf dem Zustand.

Die Zustandsmaschine ist eine **reine Funktion aus Record-Strom und Uhr**. Damit ist sie gegen
aufgezeichnete Ströme wiederholbar abspielbar, und genau darauf beruht ihre Prüfbarkeit: Eine
Referenzimplementierung von FIG 0/15 gibt es nicht.

### 8.1 Die Ensemble-Uhr

Aus `tlm.ens_time` (FIG 0/10) plus der seither verstrichenen **monotonen** Zeit. Sie ist die
gemeinsame Zeitbasis aller Empfänger desselben Ensembles und damit die Grundlage der Hashes
(Abschnitt 10).

```go
// gültig, solange die letzte ens_time weniger als 5 s alt ist
func (c *Channel) ensSecond(at time.Time) (time.Time, string)  // Zeit, "ens" | "node"
```

Fehlt sie, wird auf die **Knotenzeit** zurückgefallen und das im Datensatz mit
`time_source: "node"` kenntlich gemacht. Der Unterschied ist wichtig: Mit Ensemble-Zeit stimmen
zwei Knoten **exakt** überein, mit Knotenzeit nur, soweit ihre NTP-Uhren übereinstimmen.

### 8.2 Heartbeat-Überwachung

Der Heartbeat ist das, was die Abdeckungskarte trägt — das Kernergebnis des Projekts.

Je Sekunde des Berichtsfensters wird festgehalten:

- **empfangen / fehlt** (`heartbeat: true` in einem `asa`-Record dieser Sekunde),
- **P/D-Konsistenz**: `pd_second_half` muss `true` sein für Sekunden 30–59 und `false` für
  0–29, bezogen auf die **Ensemble-Zeit**. Abweichungen zählen (`pd_mismatch`),
- **verdrängt durch Alerts**: Solange Alerts signalisiert werden, wird laut Norm **kein**
  Heartbeat gesendet. Eine fehlende Heartbeat-Sekunde während eines laufenden Alerts ist
  daher **normal** und wird als `suppressed` gezählt, nicht als `missing`.

Ein Ensemble, von dem noch nie ein Heartbeat kam, ist ein anderer Befund als eines, dessen
Heartbeat aussetzt. Beides muss unterscheidbar bleiben: `asa.ever_seen` und `asa.observed`.

### 8.3 Alert-Sets rekonstruieren

Ein Alert-Set sind 1–4 FIG-0/15-Instanzen, die einen Alert samt vollständigem Warngebiet
beschreiben. Zusammengehalten werden sie durch:

- **identisches Id-Feld** — bei `oe: false` also (`phase`, `subch_id`), bei `oe: true`
  (`other_eid`),
- **identisches Status-Feld** außer dem `last`-Flag — also (`stage`, `iid`),
- **`nff`**: Anzahl der noch folgenden Instanzen. Die letzte hat `nff == 0`.

Regeln:

| Regel | Verhalten |
|---|---|
| `nff` zählt herunter (3,2,1,0) | vollständiges Set, Location Codes in Empfangsreihenfolge aneinanderhängen |
| `nff` springt oder fehlt | Set als `incomplete: true` abschließen, alle empfangenen Instanzen dennoch melden |
| Ein Set bleibt > 2 s offen | abschließen mit `incomplete: true`. Bei Sekundenzähler 59 bricht der Sender die Alert-Group ohnehin ab |
| Mehr als 4 Instanzen | melden, zählen, Set trotzdem abschließen — die Norm erlaubt höchstens 4 |
| `stage == "test"` | vollwertig verarbeiten, aber **hart** getrennt kennzeichnen (`test: true`). Consumer-Geräte ignorieren Test-Alerts; ein Monitor gerade nicht |

### 8.4 Der Phasenautomat

Schlüssel je verfolgtem Alert: (`oe`, `other_eid` **oder** `subch_id`, `iid`). Der `iid` ist nur
4 bit breit und ensemble-lokal wiederverwendet — ein Schlüsselwechsel ist deshalb an einen
zeitlichen Rahmen gebunden.

```
   (kein Alert)
        │ pre_trigger              ┌───────────────────────────────┐
        ▼                          │                               │
  ┌───────────┐   trigger    ┌──────────┐   sustain   ┌─────────┐  │ end
  │ PRETRIGGER├─────────────▶│ TRIGGER  ├────────────▶│ SUSTAIN ├──┘
  └───────────┘              └──────────┘             └─────────┘
        │                          │                       │
        └──── Stille > 30 s ───────┴───────────────────────┴──────▶ ABGEBROCHEN
```

- **Einstieg in jeder Phase möglich.** Bei schlechtem Empfang kann die gesamte Trigger-Phase
  durch CRC-Fehler ausfallen; der Alert wird dann erst in `sustain` sichtbar. Das ist ein
  gültiger, aber unvollständiger Befund: `entered_at_phase` festhalten.
- **`end`** schließt den Alert ab. Danach läuft der Nachlauf für den Audiomitschnitt.
- **Stille länger als 30 s** ohne `end` → Alert als `aborted` abschließen. Der Grund gehört in
  den Datensatz (`close_reason`: `end` | `timeout` | `shutdown` | `stream_gap`).
- **Ein Ensemble führt höchstens eine eigene Warnung** (`oe: false`), aber beliebig viele
  OE-Alerts gleichzeitig. Erscheint ein zweiter eigener Alert mit anderem `iid`, ist das eine
  meldenswerte Auffälligkeit, kein Programmfehler — melden und beide verfolgen.
- **`sec == 63` im Pre-Trigger** ist Sonderwert: Start bei Sekunde 0, 5 s Triggerdauer.

Für jeden Zustands- und Phasenwechsel gilt: **benannte Konstanten mit `String()`-Methode, in
jedem `switch` ein ausdrückliches `default`, das zählt und meldet statt zu verwerfen.** Go hat
keine erschöpfende Prüfung von Aufzählungen; das ist der Ersatz. `go vet` gehört in die
Bauprüfung.

### 8.5 Audio-Auslösung

| Ereignis | Wirkung |
|---|---|
| `phase == "trigger"`, `oe == false`, Audio aktiviert | sofort `REC <subch_id>` an diesen Kanal |
| `phase == "pre_trigger"` | nichts. Der Vorlauf ist ein Komfortgewinn, keine Voraussetzung — und ein früher Start schneidet reguläres Programm mit |
| `phase == "end"` | nach `audio.post_roll` ein `STOP <subch_id>` |
| `audio.max_seconds` erreicht | `STOP`, `truncated: true` im Datensatz |
| Alert `aborted` | `STOP` |

### 8.6 OE-Auflösung quer über die Kanäle

Kommt auf Kanal A ein Alert mit `oe: true` und `other_eid: 0x10FF` herein, und empfängt
derselbe Knoten dieses Ensemble auf Kanal B, dann:

1. sucht `asamon-node` in seinen Kanalzuständen nach `eid == other_eid`,
2. findet er ihn, wird Kanal B sofort in **Bereitschaft** versetzt: Der dortige Kanalzustand
   erwartet in den nächsten Sekunden einen eigenen Alert und startet den Mitschnitt bereits
   beim ersten `asa`-Record mit passender Phase — ohne auf den vollständigen Alert-Set zu
   warten,
3. der OE-Alert wird **in jedem Fall vollständig gemeldet**, auch wenn er lokal nicht
   auflösbar ist. Er ist oft das früheste Signal im ganzen Netz.

Die Auflösung läuft über eine kleine, mutexgeschützte EId-Tabelle im Supervisor — nicht über
den Kanalzustand hinweg. Ein Kanal liest nie den Zustand eines anderen.

---

## 9. Location Coding — Paket `loc`

Ein eigenständiges, vollständig testbares Paket ohne Bezug zum Rest. Es dient **zwei** Zwecken:
der Knotenposition aus der Konfiguration und den Warngebieten aus den Alerts. Deshalb lohnt es
sich, es sauber zu bauen.

Normative Quellen: TS 104 089 **Annex F** (Geometrie), **Annex A** (Präsentationsformat),
**Annex E** (Bitlayout im FIG). Zusammenfassung in `../specs/asa.md` Abschnitt 5.

### 9.1 Präsentationsformat ↔ 30-bit-Code

```
30 bit = Zone (6) + Digits (24)
  → Prüfsumme = Integer mod 61, als 6 bit angehängt → 36 bit
  → 12 Oktalziffern → jede + 1 → Symbole "1".."8"
  → drei Blöcke à vier Symbole, Bindestrich getrennt
```

```go
func ParsePresentation(s string) (Code, error)   // "2366-7443-8484" → Code, prüft die Prüfsumme
func (c Code) Presentation() string              // Rückrichtung
func (c Code) URI() string                       // "DLI://2366-7443-8484"
```

**Testvektor aus der Spec:** BBC Broadcasting House, WGS84 (51,5187412 / −0,1434571) →
`Z10:B736BB` → `2366-7443-8484`. Beide Richtungen müssen ihn treffen.

### 9.2 Koordinaten ↔ Zone und Ziffern

```
SE = 90 - Breite            EE = Länge (negativ: +360)
SE  <  18      → Zone 0                                   (Nordpol)
18 ≤ SE < 162  → Zone = 10·int((SE-18)/36) + int(EE/36) + 1   (1..40)
SE ≥ 162       → Zone 41                                  (Südpol)

SC = int(frac((SE-18)/36) · 2^12)      EC = int(frac(EE/36) · 2^12)
CC = Interleave(SC, EC) je 2 bit, beginnend mit SC  → 24 bit
```

Rückrichtung, die eigentlich gebrauchte: aus Zone und *n* Ziffern das **sphärische Rechteck**.
Bei *n* Ziffern sind je Achse 2·*n* Bit bekannt, die Kantenlänge ist also 36°/2^(2n) in beiden
Achsen der Zonenkoordinaten.

```go
func (c Code) Rect() Rect        // Lat/Lon-Grenzen, WGS84
func (c Code) Center() (lat, lon float64)
func (c Code) GeoJSON() []byte   // Polygon, für das Frontend
```

**Die Polarzonen 0 und 41 rechnen anders** (Kreisgeometrie, Annex F.5). Für Deutschland
irrelevant — trotzdem implementieren oder mit einem klaren `ErrPolarZoneUnsupported`
zurückweisen. Stillschweigend falsch rechnen ist die einzige unzulässige Variante.

### 9.3 Bitlayout im FIG — `location_codes` dekodieren

Aus dem `asa`-Record kommt ein Hex-String. Sein Aufbau, je Location Code:

```
NFF 2 | Zone 6 | SCF 1 | Num digits 3 | Digit 1 4 | Other digits 4·n
       | Padding 0/4 (nur wenn Num digits ungerade) | Sub-codes 0/16 (nur bei SCF=1)
```

Drei Punkte, an denen sich `asamon-rx` bereits die Finger verbrannt hat und die hier
gelten:

1. **NFF steht in *jedem* Location Code**, nicht nur im ersten. Das folgt zwingend aus der
   Padding-Regel — ohne die zwei NFF-Bits stünde die Struktur nicht auf einer Bytegrenze.
2. **Die Padding-Regel richtet sich nach `Num digits`**, nicht nach der Gesamtzahl der Ziffern.
3. **Bei SCF = 1** fehlt die niedrigstwertige Ziffer, und die 16-bit-Bitmaske sagt, welche der
   16 Teilflächen zum Warngebiet gehören — bis zu 15 Rechtecke aus einem Code.

```go
func DecodeLocationCodes(raw []byte) ([]Code, error)  // eine oder mehrere, mit Sub-codes aufgelöst
```

**Testvektoren:** `../asamon-rx/tests/fixtures/` enthält die Sätze **LC3** und **LC5** aus
TS 104 090 Tabelle A.19 mitsamt den normativen Byte-Längen. Diese Dateien werden nach
`testdata/locations/` übernommen und hier **rückwärts** geprüft: Wer LC5 aus 19 Byte in
4 Codes (5+5+4+5) zerlegt, hat Annex E richtig gelesen. Das ist eine zweite, unabhängige
normative Probe — und der Grund, warum dieses Paket zuerst gebaut wird.

### 9.4 Überwarnung ist normal

Das kleinste Rechteck hat rund 1 km Kantenlänge, ein Warngebiet besteht aus höchstens vier
FIG-Instanzen. Das signalisierte Gebiet ist deshalb regelmäßig größer als das tatsächliche
Gefahrengebiet. Das ist keine Ungenauigkeit unseres Decoders und darf nicht als solche
dargestellt werden.

---

## 10. Hashes — die Grundlage der Duplikaterkennung

Mehrere Knoten empfangen dasselbe Signal. Damit der Server Beobachtungen zusammenführen kann,
trägt jede Beobachtung einen Hash, den **jeder Knoten unabhängig zum selben Wert berechnet**.

### 10.1 Kanonisierung — die Regel, an der alles hängt

Gehasht wird **nie serialisiertes JSON** (Feldreihenfolge und Escaping sind nicht garantiert),
sondern eine ausgeschriebene Bytefolge:

- Felder mit `\n` (0x0A) getrennt, kein abschließendes `\n`,
- Hex durchgehend **klein**, ohne Präfix (`10ff`, nicht `0x10FF`),
- Zeiten als RFC 3339 in **UTC, ohne Bruchteile**, mit `Z` (`2026-08-26T14:03:11Z`),
- Zahlen als Dezimaltext ohne führende Nullen,
- ein Präfix je Hashart als erstes Feld — es verhindert, dass zwei Hasharten je kollidieren,
- `SHA-256`, davon die **ersten 16 Byte**, als 32 Hexzeichen.

Jede Hashart bekommt in `docs/hashes.md` ihre Definition **und mindestens zwei Testvektoren**.
Ändert sich eine Definition, steigt das Präfix (`-v2`) — nie stillschweigend.

### 10.2 Die vier Hashes

**`ens_hash` — Identität eines Kanals-Ensembles.** Was zwei Knoten meinen, wenn sie „dasselbe
Ensemble" sagen.

```
asamon-ens-v1 \n <channel> \n <eid_hex_4> \n <ecc_hex_2>
```

Bewusst **ohne Label und ohne Services**: Labels kommen bei schlechtem Empfang verstümmelt an,
und eine Umstellung im Multiplex darf die Identität nicht wechseln.

**`ens_content_hash` — Momentaufnahme des Multiplex-Aufbaus.** Erkennt Änderungen und
dedupliziert die Ensemble-Datensätze über die Knoten hinweg.

```
asamon-enscontent-v1 \n <ens_hash> \n <label> \n
  je Service, sortiert nach sid:   <sid_hex_8> \t <label> \t
      je Komponente, sortiert nach subch_id:  <subch_id> , <start_addr> , <size> , <protection> , <bitrate> ;
```

**`asa_hash` — eine FIG-0/15-Instanz.** Der wichtigste. Er ist der Schlüssel, über den zwei
Knoten dieselbe Meldung als dieselbe erkennen.

```
asamon-asa-v1 \n <ens_hash> \n <ens_second_rfc3339> \n <raw_hex>
```

Drei Entscheidungen darin, jede mit einem Grund:

| Entscheidung | Warum |
|---|---|
| `raw` statt der geparsten Felder | `raw` ist genau das, was auf dem Kanal stand. Zwei Knoten mit verschiedenen Programmständen kämen bei den geparsten Feldern womöglich auseinander, bei `raw` nie |
| **Ensemble-Zeit**, nicht Knotenzeit | Sie kommt aus demselben Sender und ist bei allen Empfängern desselben Ensembles bitgleich. Die lokale NTP-Uhr wäre auf ±1 s genau — und damit an jeder Sekundengrenze uneinig |
| Sekunde, nicht Millisekunde | Ein Heartbeat kommt 1/s; im Alarmfall wiederholt sich dieselbe Instanz innerhalb der Sekunde. Dass diese Wiederholungen **denselben** Hash bekommen, ist erwünscht — sie sind dieselbe Beobachtung |

**Die Grenze offen benennen:** Fehlt einem Knoten die Ensemble-Zeit, fällt er auf die
Knotenuhr zurück (`time_source: "node"`) und kann an einer Sekundengrenze eine Sekunde
danebenliegen. Sein Hash weicht dann ab. Deshalb schickt der Datensatz neben dem Hash **immer
auch** `ens_hash`, `ens_second` und `raw` mit, sodass der Server zweistufig deduplizieren kann:
exakte Hashgleichheit zuerst, danach (`ens_hash`, `raw`, Sekunde ±1). Das gehört in
`docs/uplink-protokoll.md`, damit die Serverseite es nicht neu erfinden muss.

**`alert_uid` — ein Vorfall.** Ein *Vorschlag* zur Verkettung, kein Beweis.

```
asamon-alert-v1 \n <eid_hex_4> \n <iid> \n <start_minute_rfc3339>
```

`eid` ist das **warnende** Ensemble (bei `oe: true` also `other_eid`, nicht das empfangene) —
ohne Kanal, denn wer den OE-Verweis sieht, kennt den Kanal des anderen Ensembles nicht.
`start_minute` ist die auf die Minute abgerundete Ensemble-Zeit der **ersten beobachteten**
Instanz dieses Alerts. Weil Alerts laut Norm an der Minutengrenze beginnen, treffen sich
Knoten, die den Beginn gesehen haben, hier zuverlässig.

Wer erst in `sustain` einsteigt, kennt die Startminute nicht. Dann gilt:
`alert_uid_confident: false`, und der Server verkettet über (`eid`, `iid`, Zeitfenster) selbst.
Der `iid` ist nur 4 bit breit und wird wiederverwendet — eine global eindeutige Vorfalls-ID
existiert on air nicht, und dieses Feld tut nicht so, als gäbe es sie.

**Audio** wird **nicht** über einen Hash dedupliziert: Bitfehler machen jeden Mitschnitt
knotenspezifisch. Die Datei trägt ihr `sha256` nur als Integritätsprüfung und wird über
`alert_uid` zugeordnet.

---

## 11. Der Datensatz zum Server

Ein Datensatz je `report_interval` (Vorgabe 10 s), **immer**, auch wenn nichts empfangen wurde.
Sonst kann der Server „Ensemble sendet keinen Heartbeat" nicht von „Knoten ist tot"
unterscheiden — und damit wäre die Abdeckungskarte wertlos.

Der **erste** Datensatz geht sofort beim Start raus (Fenster = seit Prozessstart, meist unter
einer Sekunde). Er ist die Anmeldung des Knotens.

### 11.1 Schema

```json
{
  "report_version": 1,
  "seq": 4711,
  "generated_at": "2026-08-26T14:03:20.004Z",
  "window": { "from": "2026-08-26T14:03:10.002Z", "to": "2026-08-26T14:03:20.002Z" },
  "trigger": "interval",

  "node": {
    "node_id": "8f14e45f-ceea-467a-9c9b-2f0f5a4b1c33",
    "name": "Berlin-Mitte-01",
    "pubkey": "MCowBQYDK2VwAyEA…",
    "location_code": "2366-7443-8484",
    "location": {
      "zone": 10, "digits": "B736BB",
      "lat_min": 51.5140, "lat_max": 51.5228,
      "lon_min": -0.1494, "lon_max": -0.1406,
      "lat": 51.5184, "lon": -0.1450
    },
    "antenna": "Dachantenne Omni, 10 m ü. Grund",
    "contact": "mail@example.org",
    "node_version": "0.1.0",
    "node_commit": "abc1234",
    "platform": "linux/arm64",
    "started_at": "2026-08-26T09:12:00Z",
    "uptime_s": 17240,
    "clock": { "ntp_synchronized": true, "ens_offset_ms": 42 },
    "spool": { "reports": 0, "bytes": 0, "audio_files": 1 }
  },

  "channels": [
    {
      "channel": "5C",
      "freq_hz": 178352000,
      "device": "rtl_sdr",
      "device_serial": "",
      "rx_state": "running",
      "rx_version": "0.1.0",
      "rx_commit": "abc1234",
      "welle_commit": "fe06fad",
      "rx_restarts": 0,
      "last_error": "",

      "ensemble": {
        "ens_hash": "5b1e…",
        "ens_content_hash": "9c04…",
        "eid": "0x10FF", "ecc": 224, "label": "Bundesmux 1",
        "first_seen": "2026-08-26T09:12:04Z",
        "last_seen": "2026-08-26T14:03:19Z",
        "services": [
          { "sid": "0x0D3110AB", "label": "ASA DE",
            "components": [ { "subch_id": 7, "start_addr": 128, "size": 48,
                              "protection": "EEP 2-A", "bitrate": 32 } ] }
        ]
      },

      "reception": {
        "samples": 10,
        "snr_avg": 12.4, "snr_min": 11.8, "snr_max": 13.1,
        "sync_ratio": 1.0,
        "fib_total": 1250, "fib_crc_err": 18, "crc_err_rate": 0.0144,
        "dropped": 0, "parse_errors": 0, "seq_gaps": 0,
        "ens_time_offset_ms": 42
      },

      "asa": {
        "ever_seen": true,
        "observed": true,
        "heartbeat": {
          "expected": 10, "received": 10, "suppressed": 0,
          "missing_seconds": [], "pd_mismatch": 0
        },
        "records": [
          { "asa_hash": "1f9a…", "ens_second": "2026-08-26T14:03:11Z",
            "time_source": "ens", "ts": "2026-08-26T14:03:11.482913771Z",
            "heartbeat": true, "cn": true, "oe": false, "pd_second_half": false,
            "raw": "018f" }
        ],
        "alerts": []
      }
    }
  ],

  "counters": { "panics": 0, "unknown_records": 0, "reports_spooled": 0 }
}
```

**`trigger`** ist `"interval"`, `"startup"`, `"alert"` oder `"shutdown"`. Das Feld sagt dem
Server, warum dieser Datensatz kam.

### 11.2 Die Liste der ASA-Status

`asa.records` ist die **vollständige, verlustfreie** Liste aller `asa`-Records des Fensters —
im Ruhezustand zehn Heartbeats je Kanal, im Alarmfall bis zu 120. Sie ist bewusst nicht
aggregiert:

- Jeder Eintrag trägt seinen **eigenen Hash**; nur so kann der Server einzeln deduplizieren.
- `raw` ist der Beleg, aus dem sich jede Deutung nachträglich zurückrechnen lässt — es gibt
  keine Referenzimplementierung von FIG 0/15, und unser Parser wird Fehler haben.
- Das Volumen ist unerheblich: ein Heartbeat-Eintrag wiegt rund 200 Byte, zehn Kanalsekunden
  also 2 kB.

Zusätzlich steht in `asa.heartbeat` das **Aggregat** (erwartet, empfangen, unterdrückt,
fehlende Sekunden, P/D-Abweichungen). Der Server kann damit rechnen, ohne die Liste
auszuwerten. Beides zusammen, nicht eines statt des anderen.

### 11.3 Ein Alert im Datensatz

```json
{
  "alert_uid": "7c2d…", "alert_uid_confident": true,
  "oe": false, "channel_eid": "0x10FF", "warning_eid": "0x10FF",
  "subch_id": 7, "iid": 3,
  "stage": "level1_start", "level": 1, "test": false,
  "phase": "trigger", "entered_at_phase": "trigger",
  "first_seen_ens": "2026-08-26T14:03:00Z",
  "last_seen_ens":  "2026-08-26T14:04:12Z",
  "closed": false, "close_reason": "", "incomplete": false, "gap": false,
  "instances": 3, "expected_instances": 3,
  "phases": [
    { "phase": "pre_trigger", "from": "…", "to": "…", "sec": 0 },
    { "phase": "trigger",     "from": "…", "to": "…" }
  ],
  "area": {
    "whole_ensemble": false,
    "codes": [ { "zone": 10, "digits": "B736", "presentation": "2366-7443-8484",
                 "rect": { "lat_min": 51.4, "lat_max": 51.6,
                           "lon_min": -0.2, "lon_max": 0.0 } } ],
    "geojson": { "type": "MultiPolygon", "coordinates": [ [ [ [ -0.2, 51.4 ] ] ] ] },
    "raw": "0a2b3c4d"
  },
  "audio": { "state": "recording", "subch_id": 7, "bytes": 41984,
             "started_at": "…", "sha256": "", "truncated": false }
}
```

- **`area.whole_ensemble: true`** heißt: keine Location Codes — der Alert gilt für das gesamte
  Versorgungsgebiet. Das ist kein fehlendes Feld, sondern eine Aussage.
- **`area.raw`** bleibt immer dabei, auch wenn die Geometrie gelingt.
- **`level`** ist aus `stage` abgeleitet (1, 2 oder `null` bei Test) — Bequemlichkeit für die
  Serverseite, `stage` bleibt maßgeblich.
- Ein Alert erscheint in **jedem** Datensatz, solange er läuft, und ein letztes Mal mit
  `closed: true`. Der Server sieht ihn also mehrfach; `alert_uid` hält ihn zusammen.

### 11.4 Sofortiges Senden bei Alerts

Der 10-Sekunden-Takt ist für Heartbeat und Telemetrie richtig und für einen Alert falsch: Er
addiert bis zu zehn Sekunden auf ein Ereignis, dessen einziger Wert Aktualität ist.

**Regel:** Bei jedem Phasenwechsel eines Alerts (`pre_trigger` → `trigger` → `sustain` → `end`)
und beim Auftauchen eines neuen Alerts wird der laufende Datensatz **sofort** geschlossen und
gesendet, mit `trigger: "alert"`. Der Takt beginnt danach neu.

Das ändert am Format nichts — nur am Zeitpunkt. Ein Rückstau ist nicht zu befürchten: Ein Alert
hat vier Phasenwechsel, nicht vierhundert. Zur Sicherheit dennoch eine Untergrenze von **1 s**
zwischen zwei sofortigen Datensätzen; was in dieser Sekunde anfällt, geht mit dem nächsten.

---

## 12. Uplink-Protokoll

`docs/uplink-protokoll.md` ist das Vertragsdokument zur Serverseite und wird zusammen mit dem
Code geschrieben. Keine Authentifizierung — bewusst, siehe Abschnitt 5.

### `POST /api/v1/reports`

`Content-Type: application/json`, Body **immer** ein Umschlag mit Liste, auch bei einem
Datensatz. Beim Nachliefern gehen bis zu `limits.max_reports_per_request` Stück in eine
Anfrage.

```json
{ "reports": [ { … }, { … } ] }
```

Antwort `200`:

```json
{
  "accepted":    [4711, 4712],
  "duplicates":  [4710],
  "rejected":    [ { "seq": 4709, "reason": "report_version unsupported" } ],
  "audio_wanted": ["7c2d…"],
  "server_time": "2026-08-26T14:03:20.180Z"
}
```

- **Idempotenz** über (`node_id`, `seq`). Ein zweites Mal geliefert heißt `duplicates` — das
  ist ein Erfolg, kein Fehler, und der Datensatz verlässt den Spool.
- **`audio_wanted`** ist der Kern der Crowd-Ersparnis: Der Server nennt nur die `alert_uid`s,
  zu denen er noch **kein** Audio hat. Hat ein anderer Knoten schneller hochgeladen, spart
  dieser Knoten den Upload. Ohne diese Liste lädt der Knoten **nichts** hoch.
- **`server_time`** dient der Uhrenkontrolle; Abweichungen über 5 s werden geloggt.

### `POST /api/v1/alerts/{alert_uid}/audio`

`Content-Type: application/octet-stream`, Body = roher Subchannel-Bitstrom, unverändert wie
empfangen. Metadaten in Kopfzeilen:

| Kopfzeile | Inhalt |
|---|---|
| `X-Asamon-Node` | `node_id` |
| `X-Asamon-Channel` | DAB-Kanal |
| `X-Asamon-SubChId` | Subchannel |
| `X-Asamon-Started` | erste Aufnahmezeit, RFC 3339 |
| `X-Asamon-Sha256` | SHA-256 der Datei, hex |
| `X-Asamon-Truncated` | `true`, wenn `max_seconds` zuschlug |

Antwort `201` (angenommen), `200` (hatten wir schon) oder `413` (zu groß). In allen drei Fällen
gilt die Datei als erledigt.

### Wiederholung und Backoff

| | |
|---|---|
| HTTP-Timeout | `server.timeout` (Vorgabe 15 s) |
| Wiederholung bei `5xx`, Netzfehler, Timeout | exponentiell 1 s → 2 → 4 … max **300 s**, mit ±20 % Jitter |
| `4xx` außer `408`/`429` | **nicht** wiederholen. Der Datensatz wird verworfen und der Fall geloggt — ein dauerhaft abgelehnter Datensatz darf den Spool nicht füllen |
| `429` mit `Retry-After` | die Angabe beachten |
| Erfolg | Backoff zurücksetzen, Spool in Reihenfolge leeren |

Ein `http.Client` mit `Transport`-Wiederverwendung, `MaxIdleConnsPerHost: 2`. Kein
`http.DefaultClient` — der hat kein Timeout.

---

## 13. Spool — Store-and-Forward

Fällt die Verbindung aus, sammelt der Knoten weiter und liefert bei nächster Gelegenheit
**alles** nach, in Reihenfolge.

**Die wichtigste Regel: Im Normalbetrieb wird nichts auf die Platte geschrieben.** Ein
Datensatz geht direkt zum Uplink; erst wenn der Versand scheitert, landet er im Spool. Diese
Knoten laufen auf Raspberry Pis mit SD-Karten — sechs Schreibvorgänge pro Minute, dauerhaft,
sind ein reales Verschleißproblem und kein theoretisches. Ausnahme: **Audio geht immer sofort
auf die Platte**, es ist für den Arbeitsspeicher zu groß.

| | |
|---|---|
| Ablage | `<state_dir>/spool/reports/<seq zehnstellig>.json`, ein Datensatz je Datei |
| Schreiben | in `.tmp` schreiben, `fsync`, dann `rename` — halbe Dateien darf es nicht geben |
| Reihenfolge | aufsteigend nach `seq`. Beim Start wird der Spool eingelesen und zuerst geleert |
| Obergrenze | `limits.max_spool_mb` (Vorgabe 512) |
| Bei Überlauf | ältesten Datensatz **ohne Alerts** zuerst löschen. Erst wenn nur noch Datensätze mit Alerts da sind, wird auch der älteste davon gelöscht. Jede Löschung wird gezählt (`reports_dropped`) und im nächsten Datensatz gemeldet |
| Audio | `<state_dir>/audio/<alert_uid>-<channel>-<subch>.dabp`, getrennte Obergrenze, nach `audio.keep_days` aufgeräumt |

`seq` wird in `<state_dir>/seq` fortgeschrieben — beim Start plus 1000 aufschlagen und die
Lücke in Kauf nehmen, statt bei jedem Datensatz zu schreiben. Ein Neustart darf keine `seq`
wiederverwenden; Lücken sind unschädlich.

---

## 14. Audiomitschnitt

`aud`-Records tragen `subch_id`, `chunk` und Base64-Daten. Der Weg:

1. Beim `REC` eine Datei im Spool anlegen, Base64 dekodieren, **anhängen** — kein Puffern im
   Arbeitsspeicher, keine Umkodierung, kein Decoder.
2. `chunk` prüfen: Lücken sind Verluste und gehören gezählt (`audio_gaps`), nicht geglättet.
3. Beim `STOP` Datei schließen, SHA-256 berechnen, Größe und Dauer festhalten. Dauer wird aus
   der Bitrate der Komponente geschätzt und als `duration_s_est` gekennzeichnet.
4. Erst wenn der Server die `alert_uid` in `audio_wanted` nennt, hochladen.
5. Nach erfolgreichem Upload `uploaded_at` vermerken, Datei nach `audio.keep_days` löschen.

**Zuschneiden ist Pflicht, nicht Kür.** Der warnende Service kann ein reguläres Programm sein,
dessen Audio nur für die Dauer der Meldung ersetzt wird. Kein Vorlauf, kurzer Nachlauf, harte
Obergrenze. Was hier großzügig eingestellt wird, landet als fremdes Programm-Audio zentral auf
einem Server.

---

## 15. Nebenläufigkeit und Fehlerisolierung

Die Regeln aus `../specs/client-architektur.md` Abschnitt 4, hier verbindlich:

```
main ── config, identity ──┬── supervisor ──┬── rxproc[5C]  ─┐
                           │                ├── rxproc[11D] ─┼─ je eine Lese-Goroutine
                           │                └── …            ─┘
                           │                       │ chan Record (gepuffert, queue_size)
                           │                       ▼
                           │                chanstate[5C], chanstate[11D], …   je eine Goroutine
                           │                       │ chan snapshotRequest
                           ├── reporter (Ticker) ──┘
                           │       │ chan *Report
                           ├── uplink ──── spool
                           └── audio (je Aufnahme eine Goroutine)
```

| Regel | Begründung |
|---|---|
| **Die Lese-Goroutine hält nie an.** Ist die Kanalwarteschlange voll, wird der Record verworfen und gezählt (`node_dropped`) | Blockiert sie, läuft der Pipe-Puffer voll und `asamon-rx` verliert Samples — genau das, was der ganze Entwurf verhindern soll |
| **Je Kanal eine eigene Warteschlange**, nicht eine gemeinsame | Sonst hungert ein Alert auf Kanal A den Kanal B aus. Eine Bundeswarnung trifft Bundesmux und Landesmux gleichzeitig — das ist die Lage, auf die es ankommt |
| **`recover()` in jeder Kanal-Goroutine.** Panik zählen, mit vollem Stack loggen, **nur diese** Zustandsmaschine neu aufsetzen | In Go tötet eine ungefangene Panik in irgendeiner Goroutine den ganzen Prozess. Ohne diese Isolation legt ein unerwartetes Bitmuster auf einem Lokalmux den Bundesmux-Kanal mit lahm |
| **Der Reporter wartet nie auf den langsamsten Kanal.** Momentaufnahme per Anfrage-Kanal mit **500 ms** Frist; wer nicht antwortet, kommt als `rx_state: "stalled"` in den Datensatz | Ein hängender Kanal darf den Datensatz nicht aufhalten. Sein Hängen ist selbst die Meldung |
| **Der Kanalzustand gehört genau einer Goroutine.** Keine Mutexe darauf, kein Lesen von außen | Einfädig heißt deterministisch, und deterministisch heißt gegen Aufzeichnungen prüfbar |
| **`context.Context` überall**, ein Abbruch reicht durch bis zu den Kindprozessen | Sauberes Herunterfahren in 20 s statt `SIGKILL` |

---

## 16. Betrieb

### Kommandozeile

```
asamon-node [Optionen]

  --config <pfad>     node-config.yaml (Vorgabe: ./node-config.yaml, /etc/asamon/…)
  --check             Konfiguration prüfen, Standort ausgeben, beenden
  --dry-run           alles außer Uplink; Datensätze als NDJSON nach stdout
  --replay <pfad>     Record-Strom aus Datei statt asamon-rx (auch Verzeichnis: je Kanal eine Datei)
  --replay-speed <f>  1.0 = Echtzeit, 0 = so schnell wie möglich
  --once              einen Datensatz bauen, senden, beenden (für cron-artige Prüfungen)
  --log-level <stufe> error|warn|info|debug
  --version
```

**`--replay` ist kein Nebenschauplatz.** Die Zustandsmaschine unterscheidet nicht, ob ihr Strom
aus `asamon-rx` oder aus einer Datei kommt — das ist die Grundlage aller Regressionstests und
der einzige Weg, ASA-Verkehr zu prüfen, bevor es welchen gibt.

### Logging

`log/slog`, **strukturiert nach stdout** (journald nimmt es entgegen), Textformat bei `debug`,
JSON sonst. Feste Felder: `channel`, `eid`, `alert_uid`, wo sinnvoll. **Nie** einen ganzen
Record ins Log auf `info` — im Alarmfall wären das 120 Zeilen je Sekunde.

### systemd

`contrib/asamon-node.service`: **eine** Unit für den ganzen Knoten.

```ini
[Service]
Type=notify
WatchdogSec=60
Restart=always
RestartSec=5
User=asamon
StateDirectory=asamon
NoNewPrivileges=yes
ProtectSystem=strict
PrivateTmp=yes
```

`Type=notify` und `WatchdogSec` brauchen `sd_notify` — das sind zwei Dutzend Zeilen über
`net.Dial("unixgram", os.Getenv("NOTIFY_SOCKET"))`, **keine Fremdabhängigkeit**. Der Watchdog
wird nur bedient, solange der Reporter läuft; ein hängender Reporter soll einen Neustart
auslösen.

`After=network-online.target chronyd.service` — die Uhr ist Voraussetzung, nicht Zubehör.

---

## 17. Testen

`go test ./...`, `go vet ./...`, `gofmt -l`. Alles ohne SDR-Stick, alles ohne Netz.

| Ebene | Was geprüft wird | Wie |
|---|---|---|
| `loc` | Präsentationsformat beide Richtungen, Prüfsumme, Zone/Ziffern, Rechtecke, Sub-codes | BBC-Beispiel aus Annex F; LC3/LC5 aus TS 104 090 A.19; Eigenschaftstest: `Rect(ParsePresentation(x)).Center()` liegt im Rechteck |
| `record` | NDJSON lesen, unbekannte Felder, `seq`-Lücken, kaputte Zeilen | Tabellentests plus **`go test -fuzz`** auf dem Zeilenparser |
| `chanstate` | Alert-Sets, Phasen, Heartbeat-Lücken, P/D, Test-Stage, OE | synthetische Ströme in `testdata/streams/`, erwartete Datensätze in `testdata/golden/`. **Golden-Files, keine handgeschriebenen Zusicherungen** — eine Parseränderung wird als Diff sichtbar |
| `hashes` | Kanonisierung | Testvektoren aus `docs/hashes.md`; zusätzlich: **derselbe Strom, zweimal mit verschiedener `node_id` abgespielt, ergibt identische `asa_hash`/`ens_hash`** |
| `rxproc` | Start, Neustart, Backoff, `QUIT`, `SIGTERM` | `cmd/fake-rx` — spielt eine Datei nach, versteht `REC`/`STOP`/`QUIT` und kann auf Kommando abstürzen |
| `uplink` | Retry, Backoff, Idempotenz, `audio_wanted` | `httptest.Server`, der `5xx`, Timeouts und `429` nachstellt |
| `spool` | Reihenfolge, Überlauf, Alert-Vorrang, halbe Dateien | temporäres Verzeichnis, Prozessabbruch mitten im Schreiben nachstellen |
| Gesamtkette | Aufzeichnung → Datensätze | `--replay --dry-run` gegen `testdata/golden/` |

**Testdaten, die es noch nicht gibt:** Echten ASA-Verkehr hat niemand aufgezeichnet. Also
werden die Ströme in `testdata/streams/` synthetisch erzeugt — ein kleines Go-Programm, das
`asa`-Records nach TS 104 089 Annex E baut, samt Pre-Trigger, Trigger über 5 s, Sustain, End,
mehrteiligem Alert-Set mit NFF und OE-Verweis. Diese Erzeugung gehört ins Repo
(`cmd/fake-rx` mit einem `--scenario`-Schalter), nicht in einen Einmalskript-Ordner. Sobald es
echte Mitschnitte gibt, treten sie **daneben**, nicht an ihre Stelle.

---

## 18. Meilensteine

Jeder Meilenstein endet mit einem Abnahmekriterium, das ohne SDR-Stick prüfbar ist.

### N0 — Gerüst
`go.mod`, `Makefile`, `buildinfo`, `config`, `identity`, `log/slog`, `--check`, `--version`.
**Abnahme:** `asamon-node --check` validiert `contrib/node-config.example.yaml` vollständig,
gibt den dekodierten Standort aus, meldet jeden eingebauten Konfigurationsfehler mit
brauchbarer Meldung. Ein unbekannter YAML-Schlüssel ist ein Fehler.

### N1 — Location Coding
Paket `loc` vollständig, beide Richtungen, mit Sub-codes und Polarzonen-Entscheidung.
**Abnahme:** BBC-Beispiel beide Richtungen; LC3 und LC5 aus TS 104 090 A.19 mit korrekten
Byte-Längen zerlegt; Eigenschaftstest über 10 000 Zufallspositionen in Deutschland.

*Dieser Meilenstein steht bewusst vorn: Er ist unabhängig, vollständig prüfbar und deckt den
Teil ab, in dem sich Bitlayout-Irrtümer am längsten verstecken.*

### N2 — Record-Strom und Kanalzustand
`record`, `chanstate` ohne Alerts: Telemetrie aggregieren, Ensemble-Uhr, `ens`-Verarbeitung,
Heartbeat-Überwachung.
**Abnahme:** `--replay testdata/streams/heartbeat-10min.ndjson --dry-run` erzeugt Datensätze
mit korrekten Empfangswerten und Heartbeat-Aggregaten; `seq`-Lücken und Verwürfe erscheinen im
Datensatz.

### N3 — Subprozess-Verwaltung
`rxproc`, `cmd/fake-rx`, Supervisor, Signalbehandlung.
**Abnahme:** Drei Kanäle mit `fake-rx`; einer stürzt wiederholt ab, startet mit Backoff neu,
die anderen laufen unterbrechungsfrei; `SIGTERM` beendet in unter 20 s ohne Zombie-Prozess.

### N4 — ASA-Zustandsmaschine
Alert-Sets über NFF/Last, Phasenautomat, OE-Auflösung quer über Kanäle, Test-Stage.
**Abnahme:** Die synthetischen Szenarien (einfacher Alert; Alert-Set über 3 Instanzen; Einstieg
erst in Sustain; abgebrochener Alert; OE-Verweis, der lokal aufgelöst wird; Test-Stage) erzeugen
genau die Golden-Datensätze. Fuzzing auf dem Record-Leser läuft eine Minute ohne Panik.

### N5 — Hashes und Datensatz
`hashes` mit `docs/hashes.md`, `report` vollständig.
**Abnahme:** Derselbe Strom, zweimal mit verschiedenen `node_id`s und verschiedenen
Knotenuhren abgespielt, ergibt **bitgleiche** `asa_hash`, `ens_hash`, `ens_content_hash` und
`alert_uid`. Das ist die Kernaussage des ganzen Dedup-Verfahrens und muss ein Test sein.

### N6 — Uplink und Spool
`uplink`, `spool`, Idempotenz, Backoff, sofortiges Senden bei Alerts.
**Abnahme:** Gegen einen `httptest.Server`, der 60 s ausfällt: Danach kommen **alle**
Datensätze lückenlos und in Reihenfolge an, Duplikate werden als solche verbucht, und im
Normalbetrieb findet **kein** Schreibvorgang im Spool statt (Zähler prüfen).

### N7 — Audio
`audio`, REC/STOP-Steuerung aus dem Phasenautomaten, Upload-Aushandlung, Aufräumen.
**Abnahme:** Ein synthetischer Alert mit `aud`-Records ergibt eine Datei, deren SHA-256 mit dem
gemeldeten übereinstimmt; ohne `audio_wanted` wird nichts hochgeladen; `max_seconds` und
`keep_days` greifen.

### N8 — Härtung und Auslieferung
`recover()` überall, systemd-Unit mit `sd_notify`, Cross-Build arm64/armv7, `README.md`,
`docs/uplink-protokoll.md`, `docs/node-config.md`.
**Abnahme:** 24-Stunden-Lauf im Replay ohne Speicherwachstum (RSS-Verlauf festhalten), ohne
`seq`-Lücken im Uplink, ohne Goroutine-Leck (`runtime.NumGoroutine()` protokollieren). Statisch
gebaute Binaries für arm64 und armv7 liegen vor.

---

## 19. Was dieses Repo nicht entscheidet

Serverseitige Datenhaltung, Korrelation über Knoten hinweg, Karte und Frontend, Vertrauens- und
Verifikationsmodell für Crowd-Daten. `asamon-node` liefert Beobachtungen samt Hashes und
Belegen; was daraus wird, entscheidet die Serverseite.

**Wenn beim Arbeiten hier eine Festlegung nötig scheint, die über den Knoten hinausgeht: nicht
hier treffen**, sondern in `../specs/` notieren — so wie es `asamon-rx/TODO.md` Abschnitt 15
für die Empfangsseite vorgibt. Das Protokoll aus Abschnitt 12 ist der einzige Ort, an dem
dieses Repo etwas festlegt, das die Serverseite bindet; deshalb steht es als eigenes Dokument
in `docs/uplink-protokoll.md`.

---

## 20. Annahmen, die bei der Umsetzung gelten

Diese Punkte waren im Auftrag nicht ausdrücklich entschieden. Sie sind hier so festgelegt, dass
gearbeitet werden kann; jede Änderung daran ist billig, solange sie früh kommt.

| # | Annahme | Warum so |
|---|---|---|
| 1 | Neben dem 10-s-Takt gibt es **sofortiges Senden bei Alert-Phasenwechseln** (Abschnitt 11.4) | Der Takt kostet einen Alert bis zu 10 s Latenz. Format und Endpunkt bleiben gleich, nur der Zeitpunkt ändert sich |
| 2 | `asa.records` bleibt die **vollständige Liste** und bekommt zusätzlich ein Aggregat (Abschnitt 11.2) | Die Hashes brauchen die Einzelmeldung; das Aggregat spart dem Server das Rechnen. 2 kB je Datensatz sind kein Argument |
| 3 | **Audio geht über einen eigenen Endpunkt**, nie im 10-s-Datensatz (Abschnitt 12) | Eine zweiminütige Meldung sind 480 kB. In einem 10-s-JSON hätte das nichts zu suchen |
| 4 | Der Server sagt über **`audio_wanted`**, ob er das Audio überhaupt braucht | Das ist die Crowd-Ersparnis: Zehn Knoten, die dieselbe Meldung empfangen, laden sie einmal hoch |
| 5 | Der Knoten legt ein **Ed25519-Schlüsselpaar** an und schickt den öffentlichen Teil mit, signiert aber nicht (Abschnitt 5) | Keine Authentifizierung heute, aber Nachrüstbarkeit ohne Identitätswechsel. Kosten: 32 Byte |
| 6 | Neben dem Namen gibt es eine **`node_id` (UUIDv4)** (Abschnitt 5) | Namen sind frei wählbar, nicht eindeutig und änderbar. Ohne stabile Kennung verliert der Server bei jeder Umbenennung die Historie |
| 7 | Die Hashes stützen sich auf die **Ensemble-Zeit**, nicht auf die Knotenuhr (Abschnitt 10.2) | Nur so stimmen zwei Knoten exakt überein. Der Rückfall auf die Knotenuhr wird im Datensatz kenntlich gemacht |
| 8 | **`gopkg.in/yaml.v3`** ist die einzige Fremdabhängigkeit (Abschnitt 4) | YAML ist gefordert und nicht in der Standardbibliothek. Alles andere bleibt stdlib |
| 9 | Im Normalbetrieb wird **nicht auf die Platte geschrieben** (Abschnitt 13) | SD-Karten in fremden Pis. Der Spool ist für den Ausfall da, nicht für den Alltag |
| 10 | Der Knotenstandort wird als **ASA Location Code** konfiguriert, nicht als Lat/Lon | Es ist das geforderte Format, es zwingt das `loc`-Paket früh zur Reife, und seine Grobheit (~1 km) schützt die Freiwilligen ganz nebenbei |

---

## 21. Was die Umsetzung ergeben hat (27.08.2026)

N0 bis N8 sind gebaut. Dieser Abschnitt hält fest, wo die Wirklichkeit vom Plan abwich und was
offen bleibt — der Plan selbst bleibt oben unverändert stehen, damit die Begründungen nachlesbar
sind.

### Ein zweiter normativer Testvektor, den der Plan nicht kannte

Abschnitt 9.3 nennt die Byte-Längen aus TS 104 090 Tabelle A.19 als Probe auf das Bitlayout. Die
gehen auf — sie prüfen aber nur **Längen**, nicht **Bitreihenfolgen**. Die entscheidende Lücke
schließt **TS 104 089 Annex C**, das Cardiff-Beispiel:

> Zone 10, vier Location Codes, 22 Byte, **17 sphärische Rechtecke**, mit den Sub-codes `CC00`,
> `F730`, (keine) und `0007`.

Daran ließ sich klären, was Annex E offenlässt: `bi (i = 0 to 15)` meint **Bit i des Wertes**,
nicht das i-te übertragene Bit. Das Feld geht MSB zuerst über den Kanal, Teilfläche 0 ist also
das **zuletzt** übertragene Bit. Die erste Fassung des Dekoders hatte es andersherum — und
lieferte spiegelbildliche Warngebiete, die trotzdem plausibel aussahen.

Die Probe darauf ist geometrisch, nicht arithmetisch: Das Warngebiet liegt um den gemeinsamen
Eckpunkt der vier 4-stelligen Rechtecke B624/B625/B628/B629. In B624 — dem nordwestlichen —
müssen die Teilflächen deshalb am **Südost**-Rand liegen. Ein reiner Rundlauftest hätte den
Fehler nie gefunden, weil Encoder und Dekoder symmetrisch falsch gewesen wären.

`internal/loc/bits_test.go` hält beides fest: einmal über den eigenen Encoder, einmal über
handgepackte Bytes (`0abb6240cc00`).

### Drei Uhren, nicht zwei

Der Plan unterscheidet Ensemble-Zeit und Knotenzeit (Abschnitte 8.1 und 10.2). In der Umsetzung
kam eine dritte dazu, und ihr Fehlen war der hartnäckigste Fehler des ganzen Projekts:

| Uhr | Wofür | Warum getrennt |
|---|---|---|
| **Ensemble-Zeit** | jeder Zeitstempel im Datensatz, jeder Hash | nur sie ist bei zwei Knoten bitgleich |
| **Stromzeit** | jede Frist: 30 s Stille, 2 s Alert-Set, Audio-Nachlauf, Fenstergrenzen | im Replay liegt sie Tage zurück |
| **Knotenzeit** | `generated_at`, `window`, `uptime_s`, Uplink-Backoff | die Uhr des Rechners |

Die erste Fassung prüfte Fristen in Knotenzeit gegen Zeitstempel in Ensemble-Zeit. Im Betrieb
fällt das nicht auf, weil beide fast gleich laufen. Im Replay eines Mitschnitts von gestern lief
jeder Alert eine Sekunde nach seinem Beginn in die 30-Sekunden-Abbruchfrist. Folgen:

- `verfolgterAlert` führt `zuletztGesehen` (Ensemble) **und** `zuletztStrom` (Strom) getrennt.
- Der erste Record eines Stroms setzt die Uhr der Zustandsmaschine **hart**, nicht nur vorwärts
  (`uebernimmStromzeit`). Vorher vergiftete der Schnappschuss beim Start — der vor jedem Record
  kommt — die Zeitbasis mit der Rechneruhr, und danach passte kein Fenster mehr.
- Der Supervisor rechnet die Rechneruhr in Stromzeit um
  (`letzterRecordTs + time.Since(wallBeiRecord)`), statt sie durchzureichen.

### Das Berichtsfenster gehört dem Kanal

Abschnitt 15 sieht vor, dass der Reporter `von` und `bis` an die Kanal-Goroutinen gibt. Das geht
nicht: Beide Grenzen müssen in der Zeitbasis des jeweiligen Stroms liegen, und die kennt nur der
Kanal. `Schnappschuss` nimmt deshalb nur noch **eine** Grenze; der Anfang ist das Ende des
vorigen Fensters, das der Kanal selbst führt.

Nebenwirkung, die dem Plan widerspricht: Das erste Fenster ist kürzer als `report_interval`, weil
es beim ersten Record beginnt statt an einer gedachten Sekundengrenze davor. Das ist richtiger —
sonst meldete jeder Knotenstart eine fehlende Heartbeat-Sekunde, die es nie gab.

Die Heartbeat-Bilanz zählt außerdem nur Sekunden, die **ganz** im Fenster liegen (aufgerundete
Untergrenze, ungerundete Obergrenze). Mit beidseitigem Abrunden verschob sie sich um eine ganze
Sekunde, sobald `ts` und `ens_time` Bruchteile auseinanderlagen — und das tun sie immer.

### Herunterfahren in der falschen Reihenfolge

Abschnitt 7 gibt vor: "allen Kindern QUIT, letzten Datensatz bauen". Wörtlich umgesetzt hieß das,
die Kanal-Goroutinen zu beenden und danach von ihnen einen Schnappschuss zu verlangen — was in
einer Panik endete (*send on closed channel*) und den Knoten bei **jedem** Herunterfahren
abgeschossen hätte.

Jetzt gibt es zwei Abbruchpunkte statt einem: Die Empfangsprozesse bekommen ihr QUIT zuerst, die
Zustandsmaschinen leben weiter, bis ihr Abschluss im letzten Datensatz steht. Dafür hat jeder
Kanal einen eigenen `abschluss`-Kanal: Die Goroutine schließt darin ihre Alerts
(`close_reason: "shutdown"`), stoppt die Mitschnitte, liefert den Schnappschuss und endet dann.
Der Anfragekanal wird **nie** geschlossen — eine verwaiste Anfrage läuft in ihre Frist, und der
Kanal meldet sich als `stalled`.

### Was die End-Phase mit dem Aufräumen macht

Die End-Phase wird 1× je Transmission Frame über zwei Sekunden gesendet. Weil jeder Phasenwechsel
den Datensatz sofort schließt, fällt die Aufräumgrenze mitten hinein: Die erste End-Instanz
schloss den Alert, er wurde gemeldet und entfernt, und die zweite legte einen **zweiten,
geisterhaften Alert** an, der nur seine eigene End-Phase kannte. Im Datensatz erschien jede
Warnung doppelt.

Dagegen die `Nachklangfrist` (5 s): Ein abgeschlossener Alert geht genau einmal mit
`closed: true` raus, bleibt danach aber noch im Zustand, damit Nachzügler ihn wiederfinden. Und
solange sein Mitschnitt läuft, bleibt er in jedem Fall — sonst löste niemand mehr das STOP aus,
und die Aufnahme liefe bis zur harten Obergrenze weiter.

### Kleinere Abweichungen vom Plan

- **`whole_ensemble` ist dreiwertig.** Abschnitt 11.3 sieht `true`/`false` vor. Wer erst in
  `sustain` einsteigt, hat aber nie eine Instanz mit Status-Feld gesehen und kann über das
  Warngebiet **nichts** sagen. `false` wäre dort eine Behauptung; das Feld ist deshalb `null`.
- **`asa_hash` kann leer sein.** Er braucht `ens_hash`, und der braucht einen `ens`-Record. In den
  ersten Sekunden nach dem Start gibt es ihn nicht. Ein Hash über einen leeren `ens_hash` würde
  zwei Ensembles zusammenwerfen, die zufällig dasselbe `raw` in derselben Sekunde tragen.
- **Die Startminute des `alert_uid` kommt aus dem Pre-Trigger.** Sein Sekundenzähler nennt den
  Beginn der Trigger-Phase; daraus wird die Minute berechnet (`triggerStart`). Ohne das käme ein
  Knoten, der den Pre-Trigger bei Sekunde 55 sah, auf die vorige Minute — und ein anderer, der
  erst den Trigger sah, auf die richtige. Beide hätten verschiedene `alert_uid` für denselben
  Vorfall.
- **`internal/supervisor` ist im Layout aus Abschnitt 6 nicht vorgesehen.** Abschnitt 15 verlangt
  aber einen Supervisor samt EId-Tabelle, und `cmd/asamon-node/main.go` soll "Flags, Start,
  Signale — sonst nichts" enthalten. Das Paket ist der Ort dafür.
- **`cmd/fake-rx` hat ein `--serve` bekommen.** Ohne den Schalter ist es nur der Erzeuger und
  schreibt nach stdout; erst mit ihm wird es zum Gesprächspartner, der REC/STOP/QUIT liest und
  danach wartet. Sonst bliebe jedes `fake-rx --scenario x > datei` hängen.
- **Bei `--dry-run` geht das Log nach stderr.** Abschnitt 16 verlangt das Log auf stdout; dort
  gehört stdout aber den Datensätzen — genau wie bei `asamon-rx` die Records stdout gehören und
  die Logs stderr. Ein Log, das sich unter die Nutzdaten mischt, macht beide unbrauchbar.
- **Zu `heartbeat-10min` gibt es keine Golden-Datei.** 61 Fenster à zehn gleiche Heartbeats
  ergäben 300 kB, die nichts prüfen. Der Strom wird stattdessen über seine Summen geprüft
  (600 empfangen, 0 fehlend, 0 P/D-Abweichungen).

### Die NFC-Normalisierung entfällt

Abschnitt 4 verlangt `node.name` "nach NFC normalisiert". Das braucht `golang.org/x/text` und
damit eine **zweite Fremdabhängigkeit**, die derselbe Abschnitt verbietet. Nach der dort
vorgesehenen Regel steht der Fall hier statt im Code.

Geprüft werden gültiges UTF-8, 1–20 Zeichen und keine Steuerzeichen; der Name geht unverändert
durch. Der Preis ist gering: Der Name ist reine Anzeige, Schlüssel ist die `node_id`. Zwei
Schreibweisen desselben Namens sind für den Server zwei Namen — aber schon zwei *verschiedene*
Namen desselben Knotens sind das, und dafür gibt es die `node_id`.

### Ein Fehler in einem Beispiel des Plans

Abschnitt 11.1 gibt für den BBC-Testvektor `lat_min: 51.5140, lat_max: 51.5228` an. Richtig sind
**51.512695 .. 51.521484** — eine Zelle daneben. Die Längengrade des Beispiels (−0.1494 ..
−0.1406) stimmen. Nachgerechnet aus Annex F.4: SE = 38,4812588 und SC = 2330 = `91A`, daraus
SE ∈ [38,478516; 38,487305] und damit lat ∈ [51,512695; 51,521484]. Der Punkt selbst
(51,5187412) liegt in beiden Rechtecken, weshalb es beim Abtippen nicht auffiel.

### Was **nicht** geprüft werden konnte

- **Alles am Gerät.** Kein RTL-SDR, keine Antenne. Der gesamte Empfangspfad lief über
  synthetische Ströme. Offen bleibt damit die Frage, um die es geht: **Kommen auf 5C
  Heartbeats?** — und der Prüfpunkt aus `asamon-rx/TODO.md` Abschnitt 16: WarnBridge behauptet,
  alle 5 Minuten für 30 s einen Test-Alert. **Nachmessen, nicht glauben.**
- **`make race`.** Der Race-Detector braucht cgo und damit einen C-Compiler; auf dem
  Entwicklungsrechner (Windows, kein gcc) stand er nicht zur Verfügung. Das Ziel ist im Makefile
  vorbereitet und gehört unter Linux nachgeholt — die Nebenläufigkeit ist mit mehreren
  Goroutinen je Kanal der Teil, in dem sich ein Fehler am längsten versteckt.
- **Der 24-Stunden-Lauf aus N8.** Läufe von rund einer Minute unter Replay zeigen kein
  Speicherwachstum und kein Goroutine-Leck, ersetzen die 24 Stunden auf dem Pi aber nicht.
  Festzuhalten wären dabei RSS-Verlauf, `runtime.NumGoroutine()` und die Lückenfreiheit der
  `seq` im Uplink.
- **Der Betrieb unter systemd.** Die Unit und `sd_notify` sind geschrieben, aber nie gegen einen
  echten systemd gelaufen — `NOTIFY_SOCKET` gibt es unter Windows nicht.
- **Das Makefile selbst.** Auf dem Entwicklungsrechner ist `make` nicht installiert; die Syntax
  ist geprüft, und jedes Ziel wickelt nur `go build` oder `go test` ein, was einzeln gelaufen
  ist. Auf einem Debian oder Raspberry Pi OS ist `make` ohnehin vorhanden.
- **Mehrkanalbetrieb an echten Sticks.** Er setzt Patch 2 in `asamon-rx` voraus (Geräteauswahl
  über die Seriennummer). Die Konfigurationsprüfung erzwingt deshalb höchstens einen Kanal,
  solange keine Seriennummern vergeben sind, und erklärt in der Fehlermeldung, warum.

### Offene Punkte für die Serverseite

Sie stehen in [`docs/uplink-protokoll.md`](docs/uplink-protokoll.md) und binden das Backend:
zweistufige Duplikaterkennung, Idempotenz über (`node_id`, `seq`), und `audio_wanted` als
einziger Auslöser für Mitschnitt-Uploads. Ein Server, der `audio_wanted` nie setzt, bekommt nie
Audio — das ist beabsichtigt.

---

## 22. Umstellung auf Go 1.27 (27.08.2026)

Abschnitt 6 legt Go **≥ 1.22** fest, begründet mit `log/slog` und `math/rand/v2`. Die
Untergrenze ist auf **1.27** gestiegen (erschienen am 18.08.2026). Drei Dinge aus der neuen
Standardbibliothek sind es wert, und der Rest ist bewusst liegengeblieben.

### Was übernommen wurde und warum

**`uuid` — die `node_id` braucht keinen Eigenbau mehr.**
Abschnitt 5 sah rund zwanzig eigene Zeilen für UUIDv4 vor, ausdrücklich begründet mit „ohne
Fremdabhängigkeit". Mit der Aufnahme in die Standardbibliothek ist dieser Grund entfallen:
`uuid.NewV4()` erzeugt sie, `uuid.Parse` prüft eine vorhandene Datei gründlicher als die
handgeschriebene Zeichenprüfung. Rund 35 Zeilen weniger, darunter eine, die die Version- und
Variant-Bits von Hand setzte — genau die Sorte Code, die man nie wieder ansieht und die genau
deshalb falsch sein darf.

**`encoding/json/v2` im Record-Leser — strenger, wo es zählt, und schneller.**
Der Strom ist ein Beleg, und v2 macht zwei Mehrdeutigkeiten zu Fehlern, die die alte Fassung
still weggedeutet hat:

| Fall | alt | jetzt |
|---|---|---|
| zwei `type`-Felder in einer Zeile | letzter Wert gewinnt | gezählte kaputte Zeile |
| `"Type"` statt `"type"` | wird als `type` genommen | gezählte kaputte Zeile |
| ungültiges UTF-8 im Label | ersetzt durch U+FFFD | **weiterhin ersetzt** (siehe unten) |

Die dritte Zeile ist die interessante: v2 lehnt ungültiges UTF-8 per Vorgabe ab, und das wäre
hier **falsch**. Ein verstümmeltes Ensemble-Label ist ein Symptom schlechten Empfangs, kein
Formatfehler; würde deswegen der ganze `ens`-Record wegfallen, gäbe es keinen `ens_hash` und
damit für die nächsten Sekunden auch keinen `asa_hash` — der Empfangsfehler würde zum
Auswertungsfehler. Deshalb steht dort ausdrücklich `jsontext.AllowInvalidUTF8(true)`.
`TestStrengeDerDeutung` hält alle vier Fälle fest.

Dazu kommt Tempo, und zwar an der Stelle, an der es zählt. Gemessen mit `BenchmarkParseLine*`
auf einem i7-1355U:

| | alt (v1) | jetzt (v2) |
|---|---|---|
| `aud`-Record, 4 kB Base64 | 6815 ns | 4411 ns |
| `asa`-Record | 1387 ns | 1170 ns |

Rund ein Drittel auf dem Audio-Pfad, bei gleicher Zahl Allokationen. Im Alarmfall fallen je
Kanal ein `aud`-Record je Sekunde und bis zu zwölf `asa`-Records an — auf einem Pi Zero ist das
kein Nichts.

**Ein Preis, der genannt gehört:** Wer mit `GOEXPERIMENT=nojsonv2` baut, kann `asamon-node`
nicht mehr übersetzen — `encoding/json/v2` ist dann ausgeschlossen. Der Schalter ist ein
Ausstieg auf Zeit und soll mit Go 1.28 verschwinden; bis dahin ist das die einzige
Bauumgebung, die scheitert.

**`goroutineleak` — das Profil sieht, was Zählen übersieht.**
`TestKeinGoroutineLeckImDauerlauf` zählte bisher Goroutinen vor und nach dem Lauf. Das findet,
was noch läuft, aber nicht, was für immer auf eine Primitive wartet, die niemand mehr erreichen
kann — und genau diese Bauart droht bei sechs Kanälen mit je eigener Goroutine und mehreren
`chan`-Übergaben. Das Profil beantwortet die Frage direkt und schreibt im Fehlerfall die Stacks
ins Testprotokoll.

**Iteratoren im Record-Leser.**
`Reader.Next()` ist `Reader.Alle() iter.Seq[Record]` plus `Err()` gewichen — die Form von
`bufio.Scanner`, nur als Iterator. Aus

```go
for {
    rec, err := leser.Next()
    if err != nil { break }
    …
}
```

wird `for rec := range leser.Alle()`, und der Fehlerfall steht einmal am Ende statt in jedem
Schleifenkopf. Acht Aufrufstellen sind dadurch kürzer geworden; in `rxproc` entfiel nebenbei
die Kopie `kopie := rec`, die es nur wegen der alten Schleifenvariablen-Semantik gab.

**Kleinkram, angewandt mit `go fix`.** Die Toolchain bringt in 1.27 Modernisierer mit, die den
größten Teil selbst erledigen: `for i := range n` statt Drei-Klausel-Schleifen, `slices.Contains`,
`maps.Copy`, `strings.SplitSeq`, `strings.CutPrefix`, `errors.AsType[T]`, `sync.WaitGroup.Go`
statt `Add(1)`/`defer Done()`. Dazu von Hand: `slices.SortFunc` statt `sort.Slice` (drei
Stellen) und `min` statt der Backoff-Deckelung von Hand.

### Was geprüft und **nicht** übernommen wurde

- **`httptest.NewTestServer`** (In-Memory-Netz, kein echter Port). Es setzt voraus, dass der
  Prüfling seinen `http.Client` von `Server.Client()` bezieht. Der Uplink baut seinen Transport
  aber bewusst selbst — eigene Timeouts, `MaxIdleConnsPerHost`, kein `http.DefaultClient` —,
  und genau diese Einstellungen sollen die Tests mitdurchlaufen. Ein untergeschobener Client
  prüfte sie nicht mehr. Der Grund steht im Testcode, damit ihn niemand ein zweites Mal
  herausfinden muss.
- **`testing/synctest`.** Es würde die zeitabhängigen Tests deterministisch machen, verlangt
  aber ausdrücklich, dass eine Blase weder Netz noch externe Prozesse berührt. Die
  zeitabhängigen Tests dieses Repos tun beides — `fake-rx` ist ein Kindprozess. Für den
  Kanalzustand wäre es entbehrlich: Der ist bereits deterministisch, weil er in der Zeitbasis
  des Record-Stroms rechnet (Abschnitt 21).
- **`encoding/json/v2` im Uplink.** Dort wäre die Strenge falsch herum: Ein Server, der
  doppelte Feldnamen schickt, hat einen Fehler — aber die Antwort deswegen zu verwerfen hieße,
  den Stapel unbegrenzt zu wiederholen. Beim Record-Leser kostet eine strenge Ablehnung eine
  gezählte Zeile, beim Uplink kostete sie den Fortschritt.
- **`os.Root`** für Spool- und Audioverzeichnis. Die Pfade baut das Programm selbst aus einem
  konfigurierten Verzeichnis, und der einzige Wert von außen — der Kanalname im Dateinamen —
  geht schon durch `sicher()`. Der Umbau wäre groß und der Gewinn keiner.
- **Generische Methoden**, `crypto/mldsa`, `simd`, `math/big.Int.Divide`, `hash/maphash.Hasher`:
  kein Anwendungsfall in diesem Programm.
- **`omitzero` statt `omitempty`** im Datensatzmodell. Geprüft, aber keine Stelle profitiert:
  Wo Felder wegfallen sollen, sind es Strings und Listen, und dort tut `omitempty` genau das
  Richtige. Die einzige dreiwertige Angabe (`whole_ensemble`) braucht ausdrücklich `null` im
  Datensatz, also gerade **keine** Auslassung.

### Was sich am Verhalten nicht geändert hat

Die Release Notes zu 1.27 lesen sich so, als würde `encoding/json` selbst strenger — „rejects
invalid UTF-8, rejects duplicate names". **Das stimmt für die v1-Schnittstelle nicht.** Nachgemessen:
Doppelte Feldnamen gewinnen dort weiterhin mit dem letzten Wert, ungültiges UTF-8 wird weiterhin
ersetzt. Die strengen Vorgaben gehören zum neuen Paket `encoding/json/v2`, nicht zur alten
Schnittstelle, die v2 nur noch als Unterbau benutzt.

Für dieses Repo heißt das: Report, Spool, Konfiguration und Uplink haben sich nicht bewegt, und
der einzige Ort mit geändertem Verhalten ist der, an dem es Absicht war.

### Was der Fuzzer bei der Gelegenheit gefunden hat

Beim Wiederholungslauf nach der Umstellung fand `FuzzDecodeLocationCodes` einen Fehler, der seit
N1 im Code stand und mit Go 1.27 nichts zu tun hat: **Das Zonenfeld wurde nicht geprüft.** Es ist
sechs Bit breit und trägt damit 0..63, belegt sind nach Annex F aber nur 0..41. Der Dekoder gab
Codes mit Zone 48 heraus — Werte, die die eigene `Valid()`-Prüfung ablehnt.

Schlimm war das nicht: `Rect()` scheiterte bei solchen Codes ohnehin, das Feld `rect` blieb also
leer. Falsch war es trotzdem, denn ab einer unbelegten Zone stimmt die Bitausrichtung nicht mehr,
und alles Folgende im selben Feld wäre geraten. Jetzt bricht der Dekoder dort ab; der Alert wird
weiterhin vollständig gemeldet, mit `area.raw` als Beleg und `area.decode_error` als Begründung.

Die auslösende Eingabe liegt als `internal/loc/testdata/fuzz/FuzzDecodeLocationCodes/…` im Repo
und läuft ab jetzt bei jedem `go test` mit. Der Fall ist eine Erinnerung daran, dass ein
Fuzz-Lauf von einer Minute nicht dasselbe ist wie zwanzig Sekunden: Gefunden wurde er erst, als
das Korpus über mehrere Läufe gewachsen war.

### Was offen bleibt

`make race` ist weiterhin nicht gelaufen — der Race-Detector braucht cgo und damit einen
C-Compiler, den der Entwicklungsrechner nicht hat (Abschnitt 21). Daran hat 1.27 nichts
geändert.

---

## 23. Windows und Auslieferung (27.08.2026)

Zwei Fragen, die zusammenhängen: Läuft der Knoten auch unter Windows, und muss jeder Freiwillige
sich sein Binary selbst bauen? Die Antwort auf die zweite steht in
[`docs/ausrollen.md`](docs/ausrollen.md); hier steht, was sich am Code geändert hat.

### Was Windows am Knoten geändert hat

`asamon-node` lief unter Windows von Anfang an — die gesamte Entwicklung fand dort statt, und
die Tests liefen durch. „Läuft" hieß aber nur „übersetzt und besteht die Tests". Vier Stellen
waren fachlich falsch, und alle vier fallen erst im Betrieb auf:

**Die Vorgabepfade waren Unix-Pfade.** `/etc/asamon`, `/var/lib/asamon`,
`/usr/local/bin/asamon-rx` gibt es unter Windows nicht. Jetzt gilt je Plattform (`pfade_unix.go`,
`pfade_windows.go`): unter Windows `%ProgramData%\asamon\…`, und `asamon-rx.exe` wird **neben der
eigenen Binary** gesucht. Damit läuft ein ausgepacktes Release-Archiv ohne Installation.

Dabei fiel ein eigener Fehler auf: Die erste Fassung ersetzte den konfigurierten Pfad **immer**
durch den Nachbarn, sobald er nicht existierte. Damit hätte ein Tippfehler in `rx_binary`
stillschweigend funktioniert — genau das, was `KnownFields(true)` und die Prüfsumme im
Standortcode überall sonst verhindern. Jetzt greift die Suche nur, solange die Konfiguration
schweigt. `TestGesetzterRxPfadWirdNichtErsetzt` hält das fest.

**Kindprozesse überlebten den Knoten.** Unter Linux räumt systemd sie über die Control Group ab;
Windows kennt nichts dergleichen. Ein überlebender `asamon-rx` hält den RTL-SDR-Stick offen, und
der neu gestartete Knoten findet dann kein Gerät mehr — auf einem Rechner, den ein Freiwilliger
einmal einrichtet und nie wieder anfasst, ist das kein Randfall, sondern ein Totalausfall bis
zum nächsten Blick.

Das Gegenstück zur Control Group ist ein **Job Object** mit
`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`: Windows schließt beim Prozessende alle Handles, und damit
sterben die Kinder mit — gleich, ob der Knoten sauber endet oder abgeschossen wird. Die drei
nötigen kernel32-Aufrufe sind von Hand gebunden (`gruppe_windows.go`), derselbe Handel wie beim
sd_notify: achtzig Zeilen gegen eine Fremdabhängigkeit. `TestJobObjectNimmtKindprozesseMit`
prüft es direkt — Kind starten, zuweisen, Handle schließen, Kind muss sterben.

**`SIGTERM` gibt es dort nicht.** Die Leiter `QUIT` → `SIGTERM` → `SIGKILL` fällt unter Windows
auf `QUIT` → hartes Beenden zusammen; der Subprozess bekommt eine Frist statt zwei. Das ist die
Plattform, kein Mangel: `asamon-rx` räumt bei `QUIT` selbst auf, und wer darauf nicht reagiert,
hätte auch ein `SIGTERM` nicht beachtet. Der Code sagt das jetzt, statt es zu verschweigen.

**`ntp_synchronized` war eine Behauptung.** Die Prüfung sah nur nach den Dateien von
systemd-timesyncd; unter Windows meldete sie stumm `false`. Das Feld ist jetzt plattformgeteilt
und heißt ausdrücklich **bestätigt synchronisiert**: Ein Windows-Knoten kann es nicht
bestätigen, und ein Linux-Knoten mit chrony statt timesyncd genauso wenig. Damit die Serverseite
das nicht als „Uhr kaputt" liest, steht es in `docs/uplink-protokoll.md` — und daneben der
Hinweis auf `ens_time_offset_ms`, die belastbare Größe, die auf jeder Plattform gleich gilt.

Dazu eine Warnung beim Start: **Windows erzwingt den Modus 0600 nicht.** Die Rechte an
`node_key` kommen aus den vererbten ACLs des `state_dir`. Heute schützt der Schlüssel nichts —
signiert wird nicht (Abschnitt 5) —, aber an dem Tag, an dem Signieren nachgerüstet wird,
erinnert sich niemand mehr daran. Deshalb steht es im Log statt in einer Fußnote.

### Was sich am Bau geändert hat

`ZIELE` im Makefile umfasst jetzt `linux/amd64`, `linux/arm64`, `linux/arm` und `windows/amd64`;
alle vier baut ein einziger Rechner, weil kein cgo im Spiel ist. Neu ist `make dist`: fertige
Archive mit Binary, Beispielkonfiguration, README, Lizenz und — unter Linux — der systemd-Unit,
dazu `SHA256SUMS`.

Bewusst überall `tar.gz`, auch für Windows: Das spart eine Abhängigkeit auf `zip`, und Windows
entpackt `.tar.gz` seit Jahren mit Bordmitteln.

Die Beispielkonfiguration nennt **keinen `paths:`-Abschnitt mehr**. Er war der einzige Grund,
warum sie nur auf einer Plattform lief; ohne ihn gelten die Vorgaben des Betriebssystems, und
dasselbe Archiv läuft überall. Ein Test hält fest, dass er nicht zurückkommt.

Zwei GitHub-Workflows sind dazugekommen (`.github/workflows/`): einer prüft `asamon-node` auf
Linux **und** Windows — mit Race-Detector und Fuzzing auf Linux, wo ein C-Compiler da ist —, der
andere baut bei einem Tag `v*` die Release-Archive samt `QUELLEN.txt`. **Beide sind nie
gelaufen**: Dieses Repo hat kein Remote (siehe unten).

### Die Auslieferungsfrage

Ausführlich in [`docs/ausrollen.md`](docs/ausrollen.md). Der Kern:

- **`asamon-node`**: fertige Binaries sind trivial. Ein Rechner baut alle Plattformen, das
  Ergebnis ist statisch und ohne Laufzeitabhängigkeiten. Das sollte es geben.
- **`asamon-rx`**: möglich, aber je Plattform ist eine native Bauumgebung nötig, und das Binary
  braucht FFTW3f, FAAD2, mpg123 und librtlsdr zur Laufzeit. Empfehlung dort: **`.deb`** mit
  `Depends`, weil die Zielgruppe Raspberry Pis sind.
- **GPL**: Binärweitergabe ist erlaubt und verpflichtet zur Quellweitergabe. Erfüllt durch das
  öffentliche Repo plus den öffentlichen welle.io-Fork mit festgenageltem Commit — jedes Release
  bekommt eine `QUELLEN.txt`, die beides ausdrücklich nennt. `FDK_AAC` muss aus bleiben.
- **Voraussetzung**: Dieses Repo hat **kein Git-Remote**. Ohne Veröffentlichung gibt es weder
  Releases noch die Grundlage, Binaries weiterzugeben. Das ist Schritt null.

### Warum `asamon-rx` unter Windows nicht mitkam

Der Empfangsprozess hängt an drei POSIX-Mechanismen (FIFO, `sigaction`, `AF_UNIX`). Der Port ist
in `../asamon-rx/TODO.md` Abschnitt 17 aufgeschrieben — mit dem, was der Blick in welle.io
ergeben hat: Dessen Windows-Bau läuft über **MinGW**, die Bibliotheken kommen fertig aus
`welle.io-win-libs`, und das Backend ist damit unter Windows bereits erprobt. Offen bleibt der
CMake-Pfad; welle.io baut dort über qmake.

**Geschrieben wurde davon nichts.** Auf diesem Rechner gibt es weder cmake noch einen
C++-Übersetzer; ungeprüfter Win32-Code mit Handles wäre das Gegenteil dessen, was dieses Projekt
sonst verlangt. Bis der Port da ist, heißt „Windows-Knoten": entwickeln, `--replay`,
`--dry-run` — **empfangen kann er nicht**.

---

## 24. Stilleüberwachung statt Watchdog in `asamon-rx` (umgesetzt am 27.08.2026)

`asamon-rx` hatte bis dahin eine eigene sd_notify-Anbindung und eine systemd-Unit für den
Einzelbetrieb. Beides ist entfernt (`asamon-rx/TODO.md` Abschnitt 19) — es gibt keinen
Einzelbetrieb, und der Watchdog band den Empfangsprozess an ein Init-System, das es unter
Windows nicht gibt.

Die Aufgabe, die der Watchdog erfüllte, wandert damit hierher: **einen Prozess erkennen, der
lebt, aber nichts mehr tut.**

### Warum der Record-Strom dafür reicht

`asamon-rx` reiht den `tlm`-Record aus derselben Sekundenschleife ein, aus der es zuvor auch den
Watchdog tickte — zwei Zeilen auseinander. Der Record geht unbedingt hinaus, auch ohne Empfang
(sonst ließe sich „Ensemble schweigt" nicht von „Knoten ist tot" unterscheiden). Bleiben Records
aus, steht die Schleife. Die Erkennungsgüte ist deshalb dieselbe wie vorher, nur ohne systemd.

### Was gebaut wurde

| Ort | Änderung |
|---|---|
| `internal/config` | `limits.rx_silence_seconds`, Vorgabe 15, `0` = aus, sonst mindestens 5 |
| `internal/rxproc` | `Konfig.StilleFrist`, `ueberwacheStille()`, `merkeRecord()`, `StilleNeustarts()`, dritter Fall im `select` von `einLauf` |
| `internal/supervisor` | Frist durchreichen; im **Replay** auf 0 setzen |
| `cmd/fake-rx` | `--go-silent <n>`: nach n Records verstummen, aber weiterlaufen |

Der erkannte Hänger läuft durch denselben Pfad wie ein Absturz: `beendeGeordnet` (QUIT → nach
`QuitFrist` der harte Weg), `RxFailed` mit Grund, Neustartzähler, Backoff. Ein Protokollfeld kam
nicht dazu — der Neustart erscheint als `rx_restarts`, der Grund in `last_error`. `StilleNeustarts`
wird getrennt geführt, aber nur fürs Log und die Tests: Ein Kanal, der abstürzt, hat ein anderes
Leiden als einer, der einfriert.

### Die zwei Entscheidungen, die nicht offensichtlich waren

1. **Gemessen wird über alle Record-Arten, nicht über `tlm`.** `asamon-rx` verwirft bei vollem
   Ausgabepuffer zuerst `tlm` — die Vorrangregel lautet `asa` vor `aud` vor `tlm`. Eine Frist auf
   `tlm` allein hätte den Prozess **mitten in einer Warnmeldung** neu gestartet, im einen Moment,
   in dem er das auf keinen Fall darf. Wo verworfen wird, fließt per Definition Höherwertiges.
2. **Eine eigene Anlauffrist von 30 s bis zum ersten Record.** `asamon-rx` öffnet erst den Stick
   und setzt den Empfänger auf, bevor der `init`-Record hinausgeht. Ohne diese Unterscheidung
   liefe ein langsam anlaufendes Gerät in eine Neustartschleife. Die Frist beginnt trotzdem beim
   Prozessstart, nicht beim ersten Record — sonst bliebe ein Prozess unbemerkt, der nie etwas
   sagt.

### Wie es geprüft ist

Drei Tests in `internal/rxproc`, keiner braucht einen Stick:

| Test | Was er zeigt |
|---|---|
| `TestSteckengebliebenerProzessWirdNeuGestartet` | `--go-silent 3 --ignore-quit`: der Hänger wird erkannt und **wiederholt** neu gestartet (18,7 s) |
| `TestLaufenderProzessWirdNichtNeuGestartet` | **der wichtigere:** ein Strom in Echtzeit löst über die dreifache Frist hinweg keinen Neustart aus |
| `TestStilleUeberwachungAbschaltbar` | `StilleFrist: 0` — der Replay-Fall |

Ein Fehlalarm wäre schlimmer als eine spät erkannte Störung: Er beendet einen gesunden Kanal,
und im Zweifel während eines Alerts. Deshalb ist der zweite Test der, auf den es ankommt.

Die volle Testsuite läuft weiterhin durch (`go test ./...`, einschließlich `supervisor` mit
103 s).

### Offen geblieben

- **Am Gerät ungeprüft.** Dass die Frist einen echten festgefahrenen `asamon-rx` fängt, ist nur
  gegen `fake-rx` belegt. Ein echter Hänger im welle.io-Backend sieht von außen genauso aus —
  gesehen hat ihn aber noch niemand.
- **Der Wert 15 s ist geschätzt**, nicht gemessen. Er stammt aus dem Verhältnis von einem
  Record je Sekunde zu einer Frist, die einen ausgelasteten Pi nicht stört. Der Feldtest kann
  ihn korrigieren.
