# `node-config.yaml`

Eine Datei, vollständig, ohne Umgebungsvariablen-Magie. Ein vollständiges Beispiel liegt in
[`../contrib/node-config.example.yaml`](../contrib/node-config.example.yaml).

**Suchreihenfolge:** `--config <pfad>` → `./node-config.yaml` → `/etc/asamon/node-config.yaml`
(unter Windows `%ProgramData%\asamon\node-config.yaml`).
Ein mit `--config` angegebener Pfad, den es nicht gibt, ist ein Fehler und wird **nicht**
stillschweigend durch die Vorgabe ersetzt.

Prüfen, ohne den Knoten zu starten:

```bash
asamon-node --check
```

Das validiert alles, gibt den dekodierten Standort als Rechteck samt Mittelpunkt aus und endet
mit 0 oder 1.

---

## Ein unbekannter Schlüssel ist ein Fehler

Der YAML-Leser läuft mit `KnownFields(true)`. Ein vertipptes `report_intervall` bliebe sonst
wirkungslos, ohne dass es jemand merkt — auf einem Knoten, den ein Freiwilliger einrichtet und
nie wieder anfasst, ist das der Unterschied zwischen „läuft" und „läuft scheinbar".

Aus demselben Grund wird die Datei **vollständig** geprüft, beim Start wie bei `--check`, und
nicht erst dann, wenn ein Feld zum ersten Mal gebraucht wird.

---

## `node`

```yaml
node:
  name: "Berlin-Mitte-01"
  location_code: "2366-7443-8484"
  antenna: "Dachantenne Omni, 10 m ü. Grund"
  contact: "mail@example.org"
```

| Feld | Pflicht | Regel |
|---|---|---|
| `name` | ja | 1–20 Zeichen (Unicode-Zeichen, nicht Bytes), keine Steuerzeichen |
| `location_code` | ja | `dddd-dddd-dddd` mit Symbolen `1`–`8`, **Prüfsumme muss stimmen** |
| `antenna` | nein | rein beschreibend |
| `contact` | nein | rein beschreibend |

**Warum es neben dem Namen eine `node_id` gibt.** Der Name ist frei wählbar und damit netzweit
weder eindeutig noch stabil — zwei Freiwillige nennen ihren Knoten „Zuhause". Die `node_id` ist
eine UUIDv4, die beim ersten Start in `<state_dir>/node_id` entsteht und **nie** wechselt; unter
ihr verkettet der Server die Beobachtungen. Wird der Name geändert, bleibt die Historie
erhalten.

**Der Standort ist ein ASA Location Code, kein Lat/Lon-Paar.** Drei Gründe: Es ist das geforderte
Format (TS 104 089 Annex A), es zwingt das Geometriepaket früh zur Reife, und seine Grobheit von
rund 1 km schützt die Freiwilligen ganz nebenbei. Den eigenen Code liefert
[asa.radio](https://asa.radio/) zu einer Adresse.

Die Prüfsumme ist der Grund, warum ein Tippfehler auffällt statt einen Knoten 300 km weit zu
versetzen:

```
$ asamon-node --check
asamon-node: node-config.yaml: node.location_code: loc: Prüfsumme von "2366-7443-8485"
stimmt nicht (im Code 4, berechnet 3) — vertippt?
```

---

## `server`

```yaml
server:
  url: "https://asa.example.org"
  report_interval: "10s"
  timeout: "15s"
  insecure_skip_verify: false
```

| Feld | Vorgabe | Regel |
|---|---|---|
| `url` | — (Pflicht) | gültige URL mit Rechnernamen; `http://` erlaubt, aber **Warnung im Log** |
| `report_interval` | `10s` | 1 s bis 5 min |
| `timeout` | `15s` | HTTP-Timeout je Anfrage, größer als 0 |
| `insecure_skip_verify` | `false` | prüft Serverzertifikate nicht — **nur für lokale Tests**, erzeugt eine Warnung |

Dauerangaben stehen als **Text** in der Syntax von `time.ParseDuration` (`"10s"`, `"1m30s"`).
Eine nackte Zahl ist ein Fehler: `report_interval: 10` wäre mehrdeutig.

Warum die Obergrenze von 5 min: Der Datensatz geht **immer** raus, auch wenn nichts empfangen
wurde. Nur so kann der Server „Ensemble sendet keinen Heartbeat" von „Knoten ist tot"
unterscheiden. Ein Takt von einer halben Stunde machte die Abdeckungskarte unbrauchbar.

---

## `channels`

```yaml
channels:
  - channel: "5C"
    device: "rtl_sdr"
    device_serial: ""
    gain: "auto"
    iq_file: ""
```

Mindestens einer. Die Reihenfolge ist die Anzeigereihenfolge im Datensatz.

| Feld | Vorgabe | Regel |
|---|---|---|
| `channel` | — (Pflicht) | DAB-Kanalname: `5C`, `11D`, `7B`, `LA`… Jeder Name **höchstens einmal** |
| `device` | `rtl_sdr` | `rtl_sdr`, `rtl_tcp`, `airspy`, `soapysdr`, `rawfile`, `auto` |
| `device_serial` | leer | Seriennummer des Sticks. Leer = erstes Gerät |
| `gain` | `auto` | `auto` oder ein Verstärkungsindex |
| `iq_file` | leer | nur bei `device: rawfile`, dann Pflicht |

### Ab zwei Kanälen ist `device_serial` Pflicht

```
channels[1] (11D) hat kein device_serial. Ab zwei Kanälen muss jeder Kanal seinen Stick
über die Seriennummer benennen — sonst greift sich jeder asamon-rx das erste Gerät, das
sich öffnen lässt.
Achtung: die Geräteauswahl über die Seriennummer setzt Patch 2 in asamon-rx voraus
(siehe asamon-rx/docs/welle-patches.md). Solange der fehlt, ist ein Stick je Knoten die
einzige belastbare Betriebsart.
```

Das ist keine Schikane: `CRTL_SDR` in welle.io öffnet heute schlicht das erste Gerät, das sich
öffnen lässt. Mit zwei Sticks wäre nach jedem Neustart offen, welcher Kanal auf welchem Gerät
landet — und die Zuordnung von Beobachtung zu Antenne wäre wertlos.

**Ein Knoten, mehrere Kanäle, ein Prozess.** Der Hauptgrund dafür sind nicht die eingesparten
systemd-Units, sondern die **OE-Verweise**: Signalisiert Kanal A eine Warnung in einem Ensemble,
das derselbe Knoten auf Kanal B empfängt, geht der Recorder dort sofort scharf — ohne Serverrunde.

---

## `audio`

```yaml
audio:
  enabled: true
  post_roll: "10s"
  max_seconds: 300
  keep_days: 7
```

| Feld | Vorgabe | Regel |
|---|---|---|
| `enabled` | `true` | Mitschnitt überhaupt |
| `post_roll` | `10s` | Nachlauf nach der End-Phase; über 1 min erzeugt eine Warnung |
| `max_seconds` | `300` | harte Notbremse je Aufnahme; bei aktivem Audio größer als 0 |
| `keep_days` | `7` | hochgeladene Dateien so lange behalten. `0` = nie löschen |

**Zuschneiden ist Pflicht, nicht Kür.** Der warnende Service kann ein reguläres Programm sein,
dessen Audio nur für die Dauer der Meldung ersetzt wird. Kein Vorlauf, kurzer Nachlauf, harte
Obergrenze — was hier großzügig eingestellt wird, landet als fremdes Programm-Audio zentral auf
einem Server.

Der Mitschnitt startet erst in der **Trigger**-Phase, nicht im Pre-Trigger: Der Vorlauf wäre ein
Komfortgewinn, keine Voraussetzung, und ein früher Start schnitte reguläres Programm mit. Die
einzige Ausnahme ist ein OE-Verweis eines anderen Kanals desselben Knotens — dann geht der
Recorder schon beim Pre-Trigger scharf, weil dort die Warnung bereits belegt ist.

Nicht hochgeladene Dateien bleiben liegen, auch über `keep_days` hinaus: Sie sind der einzige
Beleg, und der Server kann sie später noch anfordern.

---

## `paths`

```yaml
paths:
  rx_binary: "/usr/local/bin/asamon-rx"
  state_dir: "/var/lib/asamon"
```

Der ganze Abschnitt ist **entbehrlich**: Ohne ihn gelten die Vorgaben des Betriebssystems, und
ein ausgepacktes Release-Archiv läuft damit auf beiden Plattformen ohne Änderung.

| Feld | Vorgabe Linux | Vorgabe Windows | Regel |
|---|---|---|---|
| `rx_binary` | `/usr/local/bin/asamon-rx` | `asamon-rx.exe` neben `asamon-node.exe`, sonst `%ProgramData%\asamon\asamon-rx.exe` | muss existieren und ausführbar sein |
| `state_dir` | `/var/lib/asamon` | `%ProgramData%\asamon\state` | wird angelegt, wenn es fehlt; muss beschreibbar sein |

**Wer `rx_binary` setzt, meint es.** Die Suche neben der eigenen Binary greift nur, solange die
Konfiguration schweigt. Ein Tippfehler im Pfad bleibt ein Fehler und verschwindet nicht dadurch,
dass sich das Programm stillschweigend etwas anderes sucht.

Unter Windows ist der Modus 0600, mit dem `node_key` angelegt wird, weitgehend wirkungslos: Die
tatsächlichen Rechte kommen aus den vererbten ACLs des `state_dir`. Der Knoten weist beim Start
darauf hin. Solange nicht signiert wird, schützt der Schlüssel nichts — aber das ändert sich an
dem Tag, an dem Signieren nachgerüstet wird.

Im `state_dir` liegt alles, was einen Neustart überlebt:

| | |
|---|---|
| `node_id` | UUIDv4, Modus 0600, wechselt nie |
| `node_key` | Ed25519-Schlüsselpaar, Modus 0600 |
| `seq` | zuletzt vergebene Sequenznummer |
| `spool/reports/` | Datensätze, die noch nicht beim Server sind |
| `audio/` | Mitschnitte |

Die Schreibprobe bei `--check` legt eine Datei an und löscht sie wieder — die Rechtebits allein
sagen unter fremden Dateisystemen zu wenig.

---

## `limits`

```yaml
limits:
  max_spool_mb: 512
  queue_size: 4096
  max_reports_per_request: 60
  rx_silence_seconds: 15
```

| Feld | Vorgabe | Regel |
|---|---|---|
| `max_spool_mb` | `512` | Obergrenze des Spools, größer als 0 |
| `queue_size` | `4096` | Records je Kanal in der Warteschlange, mindestens 64 |
| `max_reports_per_request` | `60` | Datensätze je Anfrage beim Nachliefern, mindestens 1 |
| `rx_silence_seconds` | `15` | Stille im Record-Strom bis zum Neustart; `0` = aus, sonst mindestens 5 |

**Im Normalbetrieb wird nichts auf die Platte geschrieben.** Ein Datensatz geht direkt zum
Uplink; erst wenn der Versand scheitert, landet er im Spool. Diese Knoten laufen auf Raspberry
Pis mit SD-Karten — sechs Schreibvorgänge pro Minute, dauerhaft, sind ein reales
Verschleißproblem und kein theoretisches. Einzige Ausnahme: **Audio geht immer sofort auf die
Platte**, es ist für den Arbeitsspeicher zu groß.

Läuft der Spool über, weicht der **älteste Datensatz ohne Alerts** zuerst. Erst wenn nur noch
Datensätze mit Alerts da sind, wird auch der älteste davon gelöscht. Jede Löschung erscheint als
`counters.reports_dropped` im nächsten Datensatz.

`queue_size` ist die Warteschlange **je Kanal**, nicht knotenweit. Sonst hungerte ein Alert auf
Kanal A den Kanal B aus — und eine Bundeswarnung trifft Bundesmux und Landesmux gleichzeitig.
Läuft sie über, wird verworfen und gezählt (`reception.node_dropped`), niemals blockiert:
Blockierte die Lese-Goroutine, liefe der Pipe-Puffer voll und `asamon-rx` verlöre Samples.

### `rx_silence_seconds` — der Ersatz für den Watchdog

`asamon-rx` schickt **jede Sekunde** einen `tlm`-Record, auch wenn nichts empfangen wird. Bleiben
Records aus, ist seine Sekundenschleife stehengeblieben — der Prozess lebt dann noch, tut aber
nichts mehr. `Restart` allein deckt diesen Fall nicht ab: Es gibt keinen Absturz, den man
bemerken könnte.

Bis zum 27.08.2026 hat der systemd-Watchdog das erkannt, den `asamon-rx` aus derselben
Sekundenschleife heraus bediente. Er ist entfallen, weil er `asamon-rx` an systemd band und
unter Windows ohnehin nichts tat. Seitdem misst `asamon-node` die Stille selbst — dieselbe
Erkennungsgüte, auf jeder Plattform, ohne Init-System.

Zwei Feinheiten stecken darin:

- **Gemessen wird über alle Record-Arten, nicht über `tlm` allein.** `asamon-rx` verwirft bei
  vollem Ausgabepuffer zuerst `tlm` (Vorrang: `asa` vor `aud` vor `tlm`) — ausgerechnet im
  Alarmfall. Eine Frist auf `tlm` würde den Prozess also mitten in einer Warnmeldung abräumen.
  Wo verworfen wird, fließt per Definition Höherwertiges: Der Prozess lebt nachweislich.
- **Bis zum ersten Record gilt eine eigene Anlauffrist von 30 s.** `asamon-rx` öffnet erst den
  Stick und setzt den Empfänger auf, bevor der `init`-Record hinausgeht; ein USB-Reset darf
  dabei ein paar Sekunden brauchen.

Erkannt wird der Hänger, dann geht `QUIT` hinaus; ein festgefahrener Prozess liest das nicht
mehr, und nach der Frist folgt der harte Weg. Danach greift dasselbe Backoff wie nach einem
Absturz. Im **Replay** ist die Überwachung immer aus — eine Aufzeichnung ist endlich, und ihr
Ende ist kein Hänger.

---

## `log`

```yaml
log:
  level: "info"
```

`error`, `warn`, `info` oder `debug`. Das Log ist strukturiert und geht nach **stdout**, damit
journald es entgegennimmt: Textformat bei `debug` — dort liest ein Mensch mit —, JSON sonst.

Bei `--dry-run` geht es stattdessen nach **stderr**: Dort gehört stdout den Datensätzen, genau
wie bei `asamon-rx` die Records stdout gehören und die Logs stderr.

`--log-level` auf der Kommandozeile überschreibt die Datei.

---

## Kommandozeile

```
asamon-node [Optionen]

  --config <pfad>     node-config.yaml
  --check             Konfiguration prüfen, Standort ausgeben, beenden
  --dry-run           alles außer Uplink; Datensätze als NDJSON nach stdout
  --replay <pfad>     Record-Strom aus Datei statt asamon-rx
                      (auch Verzeichnis: je Kanal eine Datei <kanal>.ndjson)
  --replay-speed <f>  1.0 = Echtzeit, 0 = so schnell wie möglich
  --once              einen Datensatz bauen, senden, beenden
  --log-level <stufe> error|warn|info|debug
  --version
```

**`--replay` ist kein Nebenschauplatz.** Die Zustandsmaschine unterscheidet nicht, ob ihr Strom
aus `asamon-rx` oder aus einer Datei kommt — das ist die Grundlage aller Regressionstests und
der einzige Weg, ASA-Verkehr zu prüfen, bevor es welchen gibt.

Zu beachten: `--replay-speed` steuert auch, wie die Berichtsfenster liegen. Bei `0` landet ein
ganzer Mitschnitt in ein bis zwei Datensätzen; für einen realistischen Ablauf ist `1` richtig.
Die Zustandsmaschine rechnet dabei durchgehend in der **Zeitbasis des Mitschnitts**, nicht in
der des Rechners — ein Mitschnitt von gestern läuft deshalb so ab, wie er aufgezeichnet wurde,
und nicht sofort in jede Abbruchfrist.
