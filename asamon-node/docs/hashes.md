# Hashes

Mehrere Knoten empfangen dasselbe Signal. Damit der Server ihre Beobachtungen zusammenführen
kann, trägt jede Beobachtung einen Hash, den **jeder Knoten unabhängig zum selben Wert
berechnet**. Dieses Dokument ist die Definition dieser Hashes samt Testvektoren; die Umsetzung
steht in `internal/hashes/`.

Ändert sich eine Definition, **steigt das Präfix** (`-v2`) — nie stillschweigend.

---

## Die Kanonisierungsregel

Gehasht wird **nie serialisiertes JSON**. Feldreihenfolge und Escaping sind dort nicht
garantiert; zwei Knoten mit verschiedenen Programmständen kämen auseinander. Gehasht wird eine
ausgeschriebene Bytefolge:

| Regel | |
|---|---|
| Trennung | Felder mit `\n` (0x0A), **kein** abschließendes `\n` |
| Hex | durchgehend **klein**, ohne Präfix (`10ff`, nicht `0x10FF`), auf feste Breite links mit Nullen aufgefüllt |
| Zeiten | RFC 3339 in **UTC, ohne Bruchteile**, mit `Z`: `2026-08-26T14:03:11Z` |
| Zahlen | Dezimaltext ohne führende Nullen |
| Präfix | je Hashart als **erstes Feld** — es verhindert, dass zwei Hasharten je kollidieren |
| Endstufe | `SHA-256`, davon die **ersten 16 Byte**, als 32 Hexzeichen |

128 bit reichen: Der Server dedupliziert innerhalb eines Zeitfensters, nicht über die Ewigkeit,
und der Hash ist kein Sicherheitsmerkmal — er ist ein Verkettungsschlüssel.

---

## `ens_hash` — Identität eines Kanal-Ensembles

Was zwei Knoten meinen, wenn sie „dasselbe Ensemble" sagen.

```
asamon-ens-v1 \n <channel> \n <eid_hex_4> \n <ecc_hex_2>
```

Bewusst **ohne Label und ohne Services**: Labels kommen bei schlechtem Empfang verstümmelt an,
und eine Umstellung im Multiplex darf die Identität nicht wechseln. Der Kanal steht dagegen
drin — dasselbe Ensemble auf einem anderen Kanal ist ein anderer Empfangsbefund.

### Testvektoren

| Eingabe | `ens_hash` |
|---|---|
| `5C`, `0x10FF`, ECC 224 | `c0a8ceb1d0908a3b1b7610b315e097f8` |
| `11D`, `0x10FF`, ECC 224 | `90587d3818a385eb5b8a47e6cbda4a34` |

Die zu hashende Bytefolge im ersten Fall, mit `·` für `\n`:

```
asamon-ens-v1·5C·10ff·e0
```

---

## `ens_content_hash` — Momentaufnahme des Multiplex-Aufbaus

Erkennt Änderungen und dedupliziert die Ensemble-Datensätze über die Knoten hinweg.

```
asamon-enscontent-v1 \n <ens_hash> \n <label> \n
  je Service, sortiert nach sid, je eine Zeile:
    <sid_hex_8> \t <label> \t <subch_id>,<start_addr>,<size>,<protection>,<bitrate>; …
```

Die Komponenten einer Zeile sind nach `subch_id` sortiert, und **jede** ist mit `;`
abgeschlossen — auch die letzte. Sortiert wird, weil die Reihenfolge im FIC nicht zugesagt ist;
ohne Sortierung liefen zwei Knoten hier auseinander.

### Testvektoren

Ensemble `5C` / `0x10FF` / ECC 224, Label `Bundesmux 1`, ein Service `0x0D3110AB` („ASA DE")
mit einer Komponente (SubChId 7, Start 128, Größe 48, `EEP 2-A`, 32 kbit/s):

```
ens_content_hash = 035c382ff71364293f568a645ae36883
```

Bytefolge, mit `·` für `\n` und `»` für `\t`:

```
asamon-enscontent-v1·c0a8ceb1d0908a3b1b7610b315e097f8·Bundesmux 1·0d3110ab»ASA DE»7,128,48,EEP 2-A,32;
```

Dasselbe Ensemble ohne Label und ohne Services:

```
ens_content_hash = 37ac75a94fe8722c5a65c0870474071b
```

---

## `asa_hash` — eine FIG-0/15-Instanz

Der wichtigste. Er ist der Schlüssel, über den zwei Knoten dieselbe Meldung als dieselbe
erkennen.

```
asamon-asa-v1 \n <ens_hash> \n <ens_second_rfc3339> \n <raw_hex>
```

Drei Entscheidungen darin, jede mit einem Grund:

| Entscheidung | Warum |
|---|---|
| `raw` statt der geparsten Felder | `raw` ist genau das, was auf dem Kanal stand. Zwei Knoten mit verschiedenen Programmständen kämen bei den geparsten Feldern womöglich auseinander, bei `raw` nie |
| **Ensemble-Zeit**, nicht Knotenzeit | Sie kommt aus demselben Sender und ist bei allen Empfängern desselben Ensembles bitgleich. Die lokale NTP-Uhr wäre auf ±1 s genau — und damit an jeder Sekundengrenze uneinig |
| Sekunde, nicht Millisekunde | Ein Heartbeat kommt 1/s; im Alarmfall wiederholt sich dieselbe Instanz innerhalb der Sekunde. Dass diese Wiederholungen **denselben** Hash bekommen, ist erwünscht — sie sind dieselbe Beobachtung |

**Die Grenze offen benannt:** Fehlt einem Knoten die Ensemble-Zeit, fällt er auf die Knotenuhr
zurück (`time_source: "node"`) und kann an einer Sekundengrenze eine Sekunde danebenliegen.
Sein Hash weicht dann ab. Deshalb schickt der Datensatz neben dem Hash **immer auch**
`ens_hash`, `ens_second` und `raw` mit, sodass der Server zweistufig deduplizieren kann:
exakte Hashgleichheit zuerst, danach (`ens_hash`, `raw`, Sekunde ±1). Siehe
[`uplink-protokoll.md`](uplink-protokoll.md).

### Testvektoren

Ensemble wie oben (`c0a8ceb1…`), Heartbeat `018f`:

| Ensemble-Sekunde | `asa_hash` |
|---|---|
| `2026-08-26T14:03:11Z` | `b558307b47c1a469c719bfa623c404c4` |
| `2026-08-26T14:03:12Z` | `65f4d5c86dff70b4e59515c5f41ca1e5` |

Die Nanosekunden der Knotenzeit gehen **nicht** ein: `14:03:11.482913771` und `14:03:11.999`
ergeben denselben Hash.

---

## `alert_uid` — ein Vorfall

Ein **Vorschlag** zur Verkettung, kein Beweis.

```
asamon-alert-v1 \n <eid_hex_4> \n <iid> \n <start_minute_rfc3339>
```

`eid` ist das **warnende** Ensemble (bei `oe: true` also `other_eid`, nicht das empfangene) —
ohne Kanal, denn wer den OE-Verweis sieht, kennt den Kanal des anderen Ensembles nicht.

`start_minute` ist die auf die Minute abgerundete Ensemble-Zeit der **ersten beobachteten**
Instanz dieses Alerts. Weil Alerts laut Norm an der Minutengrenze beginnen, treffen sich Knoten,
die den Beginn gesehen haben, hier zuverlässig.

Wer erst in `sustain` einsteigt, kennt weder die Startminute noch den IId — das Status-Feld gibt
es dort nicht. Dann steht statt der IId-Zahl das Zeichen `-`, und der Datensatz trägt
`alert_uid_confident: false`; der Server verkettet über (`eid`, `iid`, Zeitfenster) selbst.

Der IId ist nur 4 bit breit und wird ensemble-lokal wiederverwendet — eine global eindeutige
Vorfalls-ID existiert on air nicht, und dieses Feld tut nicht so, als gäbe es sie.

### Testvektoren

EId `0x10FF`, erste beobachtete Instanz am `2026-08-26T14:03:11Z` (Minute `14:03`):

| IId | `alert_uid` |
|---|---|
| 3 | `40916a0e9648a82f6427174b46b9663b` |
| unbekannt (`-`) | `d5c56498e3f0458600b62fc6cff3ecc2` |

---

## Audio wird nicht gehasht

Bitfehler machen jeden Mitschnitt knotenspezifisch; zwei Knoten bekämen nie denselben Hash.
Die Datei trägt ihr `sha256` nur als **Integritätsprüfung** und wird über `alert_uid`
zugeordnet.

---

## Was die Tests festhalten

`internal/hashes/hashes_test.go` prüft jeden Testvektor dieses Dokuments. Darüber hinaus hält
`internal/chanstate` die Kernaussage des ganzen Verfahrens fest:

> Derselbe Strom, zweimal mit verschiedener `node_id` und verschiedener Knotenuhr abgespielt,
> ergibt **bitgleiche** `asa_hash`, `ens_hash`, `ens_content_hash` und `alert_uid`.

Ohne diesen Test ist das Dedup-Verfahren eine Behauptung.
