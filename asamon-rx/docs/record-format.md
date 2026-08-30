# Record-Format

Der Strom, den `asamon-rx` auf stdout schreibt, ist dreierlei zugleich:
**IPC-Protokoll** zu `asamon-node`, **Archivformat** für Wiederholläufe und **Beleg** zum Server.
Deshalb ist er die Festlegung, an der alles Übrige hängt.

**Format: NDJSON — ein JSON-Objekt je Zeile, `\n`-terminiert, UTF-8.**

Neue Felder dürfen jederzeit dazukommen; `format_version` steigt nur, wenn sich die Bedeutung
eines bestehenden Feldes ändert. Ein Leser, der ein Feld nicht kennt, überliest es.

**Aktuelle `format_version`: 1.**

> **Warum JSON und nicht binär.** Das einzige Argument für ein Binärlayout waren 125
> FIB-Records je Sekunde. Rohe FIBs gehen nicht mehr über die Pipe, damit bleibt rund
> **ein Record je Sekunde** übrig — und JSON kauft dafür drei Dinge: Ein Mitschnitt ist in
> drei Jahren ohne Formatdokument lesbar; ein neues Feld bricht keinen alten Leser; und
> `asamon-node` kommt mit `encoding/json` aus der Standardbibliothek aus. Zeilenweise heißt
> außerdem, dass `grep`, `head`, `tail -f` und `jq` sofort funktionieren.

---

## Gemeinsame Felder

Jeder Record trägt diese drei Felder, in dieser Reihenfolge, als erste im Objekt:

| Feld | Typ | Bedeutung |
|---|---|---|
| `type` | String | `init`, `tlm`, `ens`, `asa`, `aud` |
| `seq` | Zahl | Zähler je Strom, ab 0 — Lückenerkennung |
| `ts` | String | Knotenzeit als RFC 3339 mit Nanosekunden, UTC |

### `seq` und was eine Lücke bedeutet

Die Nummer wird beim Einreihen vergeben, unter derselben Sperre wie das Einreihen selbst.
Damit entspricht die Nummernfolge der Reihenfolge im Strom, und **eine Lücke in `seq` ist
genau ein Verwurf** — siehe [Gegendruck](#gegendruck-und-verwurf). Auch ein Record, der
selbst verworfen wird, verbraucht seine Nummer: die Lücke ist der Beleg.

### `ts` ist ein String, keine Zahl

Nanosekunden seit Epoche sind rund 1,8·10¹⁸ und überschreiten die 2⁵³ von `float64`. Jeder
JSON-Leser, der über einen generischen Typ geht (`interface{}` in Go, `object` in Python),
verlöre sonst Präzision — und zwar stillschweigend. RFC 3339 ist eindeutig und nebenbei lesbar.

Voraussetzung ist eine synchronisierte Uhr auf dem Knoten (NTP/chrony): Die Differenz zwischen
`ts` und der Ensemble-Zeit aus FIG 0/10 ist selbst eine Messgröße, und alle ASA-Alerts sollen
an der Minutengrenze beginnen.

---

## `init` — genau einmal, als erste Zeile des Stroms

Macht jede Aufzeichnung für sich allein erklärbar. Deshalb braucht **kein** anderer Record ein
Kanalfeld.

```json
{"type":"init","seq":0,"ts":"2026-08-26T14:03:11.482913771Z","format_version":1,
 "channel":"5C","freq_hz":178352000,"device":"rtl_sdr","device_serial":"",
 "rx_version":"0.1.0","rx_commit":"abc1234","welle_commit":"fe06fad"}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `format_version` | Zahl | Version dieses Formats |
| `channel` | String | DAB-Kanal, z. B. `5C` |
| `freq_hz` | Zahl | Mittenfrequenz |
| `device` | String | `rtl_sdr`, `rawfile`, … |
| `device_serial` | String | Seriennummer des Sticks; **bleibt leer**, solange Patch 2 fehlt |
| `rx_version` | String | Version von `asamon-rx` |
| `rx_commit` | String | Commit dieses Repos zur Bauzeit |
| `welle_commit` | String | Commit des welle.io-Forks zur Bauzeit |

`welle_commit` ist nicht Zierde: Ohne installierte Header ist die welle-API keine zugesagte
Schnittstelle. Welcher Stand den Mitschnitt erzeugt hat, ist im Zweifel die entscheidende Frage.

---

## `tlm` — 1/s

```json
{"type":"tlm","seq":1,"ts":"…","snr":12.4,"sync":true,"signal":true,
 "freq_corr":{"fine":-3,"coarse":0},"fib_total":125,"fib_crc_err":2,
 "dropped":0,"parse_errors":0,"eid":"0x10FF",
 "ens_time":"2026-08-26T14:03:11Z","ens_offset_min":60}
```

| Feld | Typ | Bedeutung |
|---|---|---|
| `snr` | Zahl \| null | Signal-Rausch-Abstand in dB; `null`, wenn nicht bezifferbar |
| `sync`, `signal` | Bool | Synchronisation, Signalpräsenz |
| `freq_corr` | Objekt | `fine`, `coarse` — Frequenzkorrektur |
| `fib_total` | Zahl | FIBs der **letzten Sekunde** |
| `fib_crc_err` | Zahl | davon mit CRC-Fehler |
| `dropped` | Zahl | verworfene Records, **kumulativ** |
| `parse_errors` | Zahl | Parserfehler, **kumulativ** |
| `eid` | String | EId als `"0x10FF"`; fehlt, solange kein Ensemble erkannt ist |
| `ens_time` | String | Ensemble-Zeit aus FIG 0/10; fehlt, solange keine kam |
| `ens_offset_min` | Zahl | lokaler Zeitversatz in Minuten, aus FIG 0/9 |

**Die CRC-Quote ist die wichtigste Zahl im ganzen Record.** Mit ihr lässt sich
„Ensemble sendet keinen Heartbeat" von „wir empfangen schlecht" trennen — und darauf beruht
die Abdeckungskarte, das Kernergebnis des Projekts.

**`tlm` geht auch dann raus, wenn nichts empfangen wurde** — dann mit `fib_total: 0`. Sonst
kann der Server „Ensemble schweigt" nicht von „Knoten ist tot" unterscheiden.

---

## `ens` — bei Änderung

Geht raus, sobald sich Ensemble, Services, Labels oder die Subchannel-Parameter ändern —
nicht im Takt. Genau dieser Record ist der Grund, warum `asamon-node` FIG 0/1 und 0/2
**nicht** selbst parsen muss.

```json
{"type":"ens","seq":2,"ts":"…","eid":"0x10FF","ecc":224,"label":"Bundesmux 1",
 "services":[{"sid":"0x0D3110AB","label":"ASA DE","components":[
   {"subch_id":7,"start_addr":128,"size":48,"protection":"EEP 2-A","bitrate":32}]}]}
```

`size` ist die Größe in Capacity Units, `bitrate` in kbit/s. `protection` ist `EEP <n>-<A|B>`
oder `UEP-<n>`. Komponenten ohne Subchannel-Eintrag in FIG 0/1 werden ausgelassen.

---

## `asa` — 1/s im Ruhezustand, im Alarmfall bis 12/s

Der Kern. Eine FIG-0/15-Instanz, ausgepackt, **ungedeutet**.

```json
{"type":"asa","seq":42,"ts":"…","heartbeat":true,"cn":true,"oe":false,
 "pd_second_half":false,"raw":"018f"}

{"type":"asa","seq":43,"ts":"…","heartbeat":false,"cn":false,"oe":false,
 "pd_second_half":false,"phase":"trigger","subch_id":7,
 "stage":"level1_start","iid":3,"last":true,"nff":0,
 "location_codes":"0a2b3c4d","raw":"070f47830a2b3c4d"}
```

| Feld | Typ | Vorhanden | Bedeutung |
|---|---|---|---|
| `heartbeat` | Bool | immer | Längenfeld des FIG-Headers == 1, leeres Type-0-Feld |
| `cn`, `oe`, `pd_second_half` | Bool | immer | die drei Header-Flags; `pd_second_half` ist das zweckentfremdete P/D (Sekunden 30–59) |
| `phase` | String | `oe: false`, kein Heartbeat | `pre_trigger`, `trigger`, `sustain`, `end` |
| `subch_id` | Zahl | `oe: false`, kein Heartbeat | Subchannel mit dem Warn-Audio |
| `other_eid` | String | `oe: true` | EId des warnenden Ensembles, `"0x10FF"` |
| `sec` | Zahl | `phase: "pre_trigger"` | Sekundenzähler; **63 ist Sonderwert** (Start bei Sekunde 0, 5 s Trigger) |
| `stage` | String | Status-Feld vorhanden | `level1_start`, `level1_update`, `level1_repeat`, `level1_critical`, `level2_start`, `level2_update`, `level2_repeat`, `test` |
| `iid` | Zahl | Status-Feld vorhanden | Incident Identifier, 0–15; bei `stage: "test"` bedeutungslos |
| `last` | Bool | Status-Feld vorhanden | letzte Instanz dieser Alert-Group |
| `nff` | Zahl | Location Codes vorhanden | Anzahl **noch folgender** FIG 0/15 dieses Alert-Sets |
| `location_codes` | String | wenn vorhanden | Hex, **roh und ungedeutet** |
| `raw` | String | **immer** | Hex der gepackten FIG-Bytes einschließlich beider Header |

### Was das Status-Feld hat und was nicht

Nach TS 104 089 §6.4.3 ist es nur bei `pre_trigger` und `trigger` vorhanden. OE-Signalisierung
ist stets Trigger (§6.5.1) — dort folgt es also immer. Bei `sustain` und `end` besteht das
Type-0-Feld nur aus dem Id-Feld: kein `stage`, kein `iid`, kein `last`, keine Location Codes.

### `location_codes` bleiben roh

Die Geometrie — Zone, Sub-codes, sphärische Rechtecke — macht `asamon-node`. `asamon-rx` kennt
nur die Bytegrenzen. Die zwei NFF-Bits am Anfang stehen **doppelt** im Strom: einmal als
Feld `nff`, einmal als erste zwei Bit des Hex-Strings. Das ist Absicht — `location_codes` ist
eine wortgetreue Kopie dessen, was auf dem Kanal stand.

### `raw` ist nicht optional

Es gibt keine Referenzimplementierung von FIG 0/15; die einzige vollständige, die existiert
(WarnBridge), liest Id- und Status-Feld vertauscht und liefert trotzdem plausibel aussehende
Werte. 30 Byte je Ereignis sind der Preis dafür, dass man sich irren darf: Aus `raw` lässt sich
jede Deutung nachträglich zurückrechnen.

### Unbekannte Werte werden gemeldet, nicht verworfen

Trifft der Parser einen Phase- oder Stage-Wert ohne Namen, schreibt er statt des Namens
`"phase_raw": <n>` bzw. `"stage_raw": <n>` und zählt den Fall in `parse_errors`.

> **Heute ist das eine Vorkehrung ohne Anwendungsfall.** Phase ist zwei Bit breit und alle vier
> Werte sind belegt; Stage ist drei Bit breit und alle acht sind belegt. `*_raw` kann erst
> auftreten, wenn die Norm erweitert wird. `tests/test_fig0_15.cpp` hält beides fest, damit die
> Aussage geprüft bleibt und nicht zur Behauptung verkommt.

Ein **fehlerhaftes** FIG dagegen — eines, dessen Längenfeld die vorgeschriebenen Felder nicht
deckt, oder mit mehr als 25 Byte Location Codes — wird ebenfalls gemeldet und in
`parse_errors` gezählt. Der Record geht mit den Feldern raus, die gesichert lesbar waren, und
`raw` trägt den Rest.

---

## `aud` — genau einer je Aufnahme, nach ihrem Ende

```json
{"type":"aud","seq":812,"ts":"…","subch_id":13,"alert_uid":"7c2dabcd",
 "dir":"/var/lib/asamon/audio","started":"2026-08-30T12:14:55.000000000Z",
 "seconds":43.75,"truncated":false,
 "sample_rate":48000,"channels":2,"mode":"HE-AACv2","mp3_bitrate":64,
 "frame_errors":0,"rs_errors":0,"rs_corrected":12,"aac_errors":0,
 "files":[{"name":"7c2dabcd-5C-13.dabp","codec":"dabp","bytes":262144,"sha256":"…"},
          {"name":"7c2dabcd-5C-13.mp3","codec":"mp3","bytes":245760,"sha256":"…"}]}
```

**Die Audiobytes gehen nicht mehr über die Leitung.** `asamon-rx` schreibt sie selbst in den
Ablageordner (`--audio-out`) und meldet mit diesem Record, was entstanden ist. Bis zum
30.08.2026 trug `aud` stattdessen den Subchannel-Bitstrom in Stücken zu 4 kB, base64-kodiert.

Drei Gründe für den Wechsel, der wichtigste zuletzt:

- Base64 kostete ein Drittel Übertragung, und `aud` war der teuerste Pfad im Record-Leser des
  Knotens.
- Ein Mitschnitt braucht keine Zwischenstände: Wer ihn auswerten will, will die ganze Datei.
- **Ein `aud`-Record konnte verworfen werden.** Die Vorrangregel (`asa` vor `aud` vor `tlm`)
  gilt weiter, und im Überlauf bedeutete ein fehlendes Stück ein Loch mitten in der Aufnahme —
  sichtbar nur als `seq`-Lücke. Eine Datei hat dieses Problem nicht.

| Feld | Typ | Bedeutung |
|---|---|---|
| `subch_id` | Zahl | der aufgenommene Subchannel |
| `alert_uid` | String | aus `REC <subChId> <alert_uid>`; **fehlt**, wenn keine mitkam |
| `dir` | String | Ablageordner, wie er auf der Platte steht |
| `started` | String | Beginn der Aufnahme, RFC 3339 |
| `seconds` | Zahl | Dauer mit zwei Nachkommastellen |
| `truncated` | Bool | `true`, wenn `--rec-max-seconds` die Notbremse gezogen hat |
| `sample_rate`, `channels`, `mode` | Zahl, Zahl, String | erst bekannt, sobald dekodiertes Audio kam; `mode` ist welle.ios Formatzusammenfassung |
| `mp3_bitrate` | Zahl | nur, wenn eine MP3 entstand |
| `frame_errors`, `rs_errors`, `rs_corrected`, `aac_errors` | Zahl | Summen über die Aufnahme, aus welle.ios Rückrufen. Ohne sie ließe sich eine stockende Aufnahme nicht von einer stillen Meldung unterscheiden |
| `files` | Liste | je Datei `name` (ohne Verzeichnis), `codec`, `bytes`, `sha256` |
| `error` | String | fehlt, wenn alles glattging |

### Zwei Dateien, zwei Zwecke

`codec: "dabp"` ist der **rohe Subchannel-Bitstrom** — der Beleg, unverändert wie vom Kanal
gekommen. `codec: "mp3"` ist die abspielbare Fassung: welle.io dekodiert den Subchannel ohnehin
und reicht das PCM über `onNewAudio()` heraus, LAME macht daraus MP3. Der Weg über MP3 statt
über einen AAC-Container ist bewusst: DAB+ verwendet die 960er Transformation, und ob der
Decoder eines Browsers die kennt, hängt an der Plattform.

Fehlt LAME beim Bau oder steht `--mp3-bitrate 0`, entsteht nur die `.dabp`; der Grund steht
dann in `error`.

### `.part` und die Frage, wem die Dateien gehören

Während des Schreibens heißt jede Datei `<name>.part`. Erst wenn sie geschlossen und der Hash
gebildet ist, wird umbenannt. Daraus folgt beides:

- Jede Datei **ohne** `.part` ist vollständig und in einem `aud`-Record genannt.
- Jede `.part`-Datei ist erkennbar eine Waise — `asamon-rx` ist gestorben, bevor die Aufnahme
  endete — und darf aufgeräumt werden.

Aufgeräumt wird vom Knoten, nach den Zeitstempeln der Dateien. `asamon-rx` löscht in diesem
Ordner nichts, was es nicht selbst angelegt hat.

### Der Preis

Der Record-Strom ist damit **nicht mehr für sich allein auswertbar**: Ein Replay einer
NDJSON-Datei enthält nur noch Dateinamen. Ein vollständiger Mitschnitt besteht seitdem aus
Strom **und** Ordner.

Zum Abspielen der `.dabp`: `dablin` kann sie **nicht** direkt lesen (es erwartet ETI oder EDI);
`welle-cli -c <kanal> -D` erzeugt zum Vergleich `.msc`-Dateien desselben Formats samt `.wav`.

---

## Gegendruck und Verwurf

Die Prozessgrenze bringt eine Gefahr mit: Liest die Gegenstelle nicht schnell genug, läuft der
Pipe-Puffer voll (unter Linux 64 kB) und `write()` blockiert. Passierte das auf einem
welle.io-Thread, gingen Samples verloren. Deshalb:

- ein **eigener Ausgabethread** mit beschränkter Warteschlange (`--queue-size`, Vorgabe 4096),
- im Überlauf wird **verworfen, nicht blockiert**, und der Verwurf in `dropped` gezählt,
- **Vorrang beim Verwerfen: `asa` vor `aud` vor `tlm`.** `init` und `ens` stehen bei `asa`:
  `init` erklärt die ganze Aufzeichnung, und `ens` ist der Grund, warum `asamon-node` FIG 0/1
  und 0/2 nicht selbst parsen muss.

Verdrängt wird immer das **älteste** Element geringeren Rangs — frische Daten sind wertvoller.
Die Reihenfolge des Stroms bleibt dabei erhalten; es entstehen nur Lücken in `seq`.

Weil im Ruhezustand nur rund ein Record je Sekunde fließt, ist der Überlauf praktisch auf den
Alarmfall beschränkt.

---

## Was **nicht** übertragen wird

**Rohe FIBs — weder dauernd noch auf Anforderung.** Es gibt keinen FIB-Ring, kein `FIBDUMP`,
kein `FIBSTREAM`. Über die Pipe geht ausschließlich der geparste Record.

Damit fällt die Möglichkeit weg, nachträglich zu belegen, dass der FIB-Walk nichts verschluckt
hat; die Aussage „Ensemble X sendet keinen Heartbeat" stützt sich allein auf die CRC-Quote.
Parserfehler gehen nach stderr und in `parse_errors`.

Rückholbar ist das jederzeit ohne Formatbruch — ein zusätzlicher `type` und ein zusätzliches
Kommando wären **additiv**.
