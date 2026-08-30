# Uplink-Protokoll

Dies ist das **Vertragsdokument zwischen Knoten und Server**. Es ist der einzige Ort, an dem
`asamon-node` etwas festlegt, das die Serverseite bindet.

Alles Übrige — Datenhaltung, Korrelation über Knoten hinweg, Karte, Frontend, Vertrauensmodell
für Crowd-Daten — entscheidet die Serverseite. `asamon-node` liefert Beobachtungen samt Hashes
und Belegen.

**Keine Authentifizierung.** Das ist eine bewusste Festlegung, keine Auslassung. Der Knoten
führt trotzdem seit dem ersten Tag ein Ed25519-Schlüsselpaar mit und schickt den öffentlichen
Teil in jedem Datensatz (`node.pubkey`); wird Signieren später nachgerüstet, sind die Schlüssel
bereits mit den Daten verknüpft und **kein Knoten muss neu identifiziert werden**.

---

## Überblick

| | |
|---|---|
| `POST /api/v1/reports` | ein oder mehrere Datensätze, JSON |
| `POST /api/v1/alerts/{alert_uid}/audio` | ein Mitschnitt, roher Bitstrom |

Der zweite Endpunkt wird **nur** benutzt, wenn der Server im ersten die passende `alert_uid`
unter `audio_wanted` genannt hat.

---

## `POST /api/v1/reports`

`Content-Type: application/json`. Der Rumpf ist **immer** ein Umschlag mit Liste, auch bei einem
einzigen Datensatz — ein Format statt zwei.

```json
{ "reports": [ { … }, { … } ] }
```

Beim Nachliefern aus dem Spool gehen bis zu `limits.max_reports_per_request` (Vorgabe 60) Stück
in eine Anfrage, in aufsteigender `seq`.

### Kopfzeilen

| Kopfzeile | Inhalt |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `asamon-node` |
| `X-Asamon-Node` | die `node_id`, doppelt zum Rumpf — erleichtert Zuordnung im Zugriffslog |

### Antwort `200`

```json
{
  "accepted":    [4711, 4712],
  "duplicates":  [4710],
  "rejected":    [ { "seq": 4709, "reason": "report_version unsupported" } ],
  "audio_wanted": ["7c2d…"],
  "server_time": "2026-08-26T14:03:20.180Z"
}
```

| Feld | Bedeutung |
|---|---|
| `accepted` | neu übernommen |
| `duplicates` | hatte der Server schon. **Das ist ein Erfolg, kein Fehler** — der Datensatz verlässt den Spool |
| `rejected` | dauerhaft abgelehnt, mit Grund. Der Knoten verwirft ihn und loggt den Fall; ein dauerhaft abgelehnter Datensatz darf den Spool nicht füllen |
| `audio_wanted` | `alert_uid`s, zu denen der Server noch **kein** Audio hat |
| `server_time` | Uhrenkontrolle; der Knoten loggt Abweichungen über 5 s |

**`node.clock.ntp_synchronized` heißt „bestätigt synchronisiert", nicht „Uhr geht richtig".**
Bestätigen kann das nur ein Knoten unter systemd-timesyncd. Ein Windows-Knoten meldet dort
immer `false`, obwohl Windows seine Uhr per Vorgabe synchron hält; dasselbe gilt für einen
Linux-Knoten mit chrony statt timesyncd. Wer daraus eine Aussage über die Uhr ableiten will,
nimmt `reception.ens_time_offset_ms` — die Differenz zwischen Knotenuhr und Ensemble-Zeit gilt
auf jeder Plattform gleich und braucht niemanden zu fragen.

**Idempotenz über (`node_id`, `seq`).** Der Knoten kann denselben Datensatz mehrfach schicken —
etwa wenn eine Antwort unterwegs verlorenging. Der Server muss das erkennen und `duplicates`
melden.

Quittiert der Server einen Stapel weder als angenommen noch als abgelehnt, betrachtet der Knoten
ihn als erledigt und meldet den Fall im Log: Ein Datensatz, der endlos wiederkäme, blockierte
den Spool.

### `audio_wanted` — die Crowd-Ersparnis

Zehn Knoten empfangen dieselbe Warnmeldung. Ohne Aushandlung lüden zehn Knoten dieselben 480 kB
hoch. Der Server nennt deshalb nur die `alert_uid`s, zu denen er noch kein Audio hat; hat ein
anderer Knoten schneller hochgeladen, spart dieser den Upload.

**Ohne diese Liste lädt der Knoten nichts hoch.** Ein Server, der das Feld nie setzt, bekommt
nie Audio — das ist beabsichtigt und kein Fehler.

### Wiederholung und Backoff

| Lage | Verhalten |
|---|---|
| HTTP-Timeout | `server.timeout` (Vorgabe 15 s) |
| `5xx`, Netzfehler, Timeout, unlesbare Antwort | wiederholen: exponentiell 1 s → 2 → 4 … max **300 s**, mit ±20 % Streuung |
| `4xx` außer `408`/`429` | **nicht** wiederholen; Datensatz verwerfen und loggen |
| `429` mit `Retry-After` | die Angabe beachten (Sekunden oder HTTP-Datum) |
| Erfolg | Backoff zurücksetzen, Spool in Reihenfolge leeren |

Die Streuung ist nicht Zierde: Ohne sie kämen nach einem Serverausfall alle Knoten des Netzes im
Gleichtakt wieder und legten ihn erneut lahm.

---

## `POST /api/v1/alerts/{alert_uid}/audio`

`Content-Type: application/octet-stream`.

**Zu einer `alert_uid` gehören seit dem 30.08.2026 zwei Dateien**, und der Endpunkt wird je
Aufnahme zweimal aufgerufen — unterschieden allein durch `X-Asamon-Codec`:

| Codec | Rumpf |
|---|---|
| `dabp` | der **rohe Subchannel-Bitstrom**, unverändert wie empfangen. Der Beleg. `dablin` liest ihn **nicht** (es erwartet ETI oder EDI); `welle-cli -c <kanal> -D` erzeugt Dateien desselben Formats |
| `mp3` | dieselbe Aufnahme, abspielbar. `asamon-rx` kodiert sie aus dem PCM, das welle.io beim Dekodieren ohnehin erzeugt — kein zweiter Decoder, kein Transkodieren aus dem Rohstrom |

Ein Server, der nur eines von beidem will, quittiert das andere mit `200`; der Knoten hakt es
dann ab. Bleibt **eine** der beiden Dateien hängen, gilt die Aufnahme als nicht hochgeladen und
wird beim nächsten `audio_wanted` erneut angeboten — sonst verschwände der Beleg nach
`audio.keep_days`, obwohl der Server ihn nie bekommen hat.

Fehlt LAME beim Bau von `asamon-rx` oder steht `--mp3-bitrate 0`, kommt nur die `dabp`-Datei.

| Kopfzeile | Inhalt |
|---|---|
| `X-Asamon-Node` | `node_id` |
| `X-Asamon-Channel` | DAB-Kanal, etwa `5C` |
| `X-Asamon-SubChId` | Subchannel, dezimal |
| `X-Asamon-Started` | erste Aufnahmezeit, RFC 3339 |
| `X-Asamon-Sha256` | SHA-256 **dieser** Datei, hex |
| `X-Asamon-Truncated` | `true`, wenn `audio.max_seconds` zuschlug |
| `X-Asamon-Codec` | `dabp` oder `mp3` |
| `X-Asamon-Filename` | Dateiname beim Knoten: `<alert_uid>-<kanal>-<subchid>.<endung>` |

| Antwort | Bedeutung |
|---|---|
| `201` | angenommen |
| `200` | hatten wir schon |
| `413` | zu groß |

**In allen drei Fällen gilt die Datei als erledigt** und wird nach `audio.keep_days` gelöscht.
Bei jedem anderen Status bleibt sie liegen und wird beim nächsten `audio_wanted` erneut
angeboten.

Audio wird **nicht** über einen Hash dedupliziert: Bitfehler machen jeden Mitschnitt
knotenspezifisch. Das `sha256` ist reine Integritätsprüfung.

---

## Der Datensatz

Ein Datensatz je `report_interval` (Vorgabe 10 s), **immer**, auch wenn nichts empfangen wurde.
Sonst kann der Server „Ensemble sendet keinen Heartbeat" nicht von „Knoten ist tot"
unterscheiden — und damit wäre die Abdeckungskarte wertlos.

Der **erste** Datensatz geht sofort beim Start raus (`trigger: "startup"`). Er ist die Anmeldung
des Knotens.

### `trigger` — warum dieser Datensatz kam

| Wert | Anlass |
|---|---|
| `startup` | der erste Datensatz nach dem Start |
| `interval` | der reguläre Takt |
| `alert` | ein Alert tauchte auf oder wechselte die Phase — der Datensatz wurde **sofort** geschlossen |
| `shutdown` | der letzte Datensatz vor dem Beenden |

Bei `alert` liegt zwischen zwei Datensätzen mindestens 1 s; was in dieser Sekunde anfällt, geht
mit dem nächsten.

### Schema

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
      "lat_min": 51.512695, "lat_max": 51.521484,
      "lon_min": -0.149414, "lon_max": -0.140625,
      "lat": 51.517090, "lon": -0.145020
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

  "channels": [ … ],
  "counters": {
    "panics": 0, "unknown_records": 0,
    "reports_spooled": 0, "reports_dropped": 0, "reports_rejected": 0
  }
}
```

`seq` ist je Knoten aufsteigend, aber **nicht lückenlos**: Beim Start schlägt der Knoten 1000
auf die zuletzt gespeicherte Nummer auf, statt bei jedem Datensatz auf die SD-Karte zu
schreiben. Lücken sind unschädlich; eine wiederverwendete Nummer wäre ein Fehler.

### Kanalabschnitt

```json
{
  "channel": "5C",
  "freq_hz": 178352000,
  "device": "rtl_sdr",
  "device_serial": "",
  "rx_state": "running",
  "rx_version": "0.1.0", "rx_commit": "abc1234", "welle_commit": "fe06fad",
  "rx_restarts": 0,
  "last_error": "",

  "ensemble": {
    "ens_hash": "c0a8ceb1d0908a3b1b7610b315e097f8",
    "ens_content_hash": "035c382ff71364293f568a645ae36883",
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
    "dropped": 0, "node_dropped": 0, "parse_errors": 0,
    "seq_gaps": 0, "broken_lines": 0,
    "ens_time_offset_ms": 42
  },

  "asa": { … }
}
```

`rx_state` ist `starting`, `running`, `failed`, `stopped` oder **`stalled`** — Letzteres heißt,
dass die Zustandsmaschine dieses Kanals nicht innerhalb von 500 ms geantwortet hat. Der Reporter
wartet nie auf den langsamsten Kanal; ein hängender Kanal ist selbst die Meldung.

**`crc_err_rate` ist die wichtigste Zahl im ganzen Datensatz.** Mit ihr lässt sich „Ensemble
sendet keinen Heartbeat" von „wir empfangen schlecht" trennen — und darauf beruht die
Abdeckungskarte.

Die Zähler des Abschnitts sind **Fensterwerte**, keine Summen seit dem Start:

| Feld | Bedeutung |
|---|---|
| `dropped` | Records, die `asamon-rx` beim Gegendruck verwarf |
| `node_dropped` | Records, die `asamon-node` verwarf, weil die Kanalwarteschlange voll war |
| `parse_errors` | Parserfehler in `asamon-rx` |
| `seq_gaps` | Lücken in der Sequenznummer des Stroms — jede ist genau ein Verwurf |
| `broken_lines` | Zeilen, die kein gültiges JSON waren |

### ASA-Abschnitt

```json
{
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
  "alerts": [],
  "anomalies": []
}
```

- **`ever_seen`** vs. **`observed`**: Ein Ensemble, von dem noch nie ein FIG 0/15 kam, ist ein
  anderer Befund als eines, dessen Heartbeat gerade aussetzt. Beides bleibt unterscheidbar.
- **`heartbeat.suppressed`**: Solange Alerts signalisiert werden, wird laut Norm **kein**
  Heartbeat gesendet. Diese Sekunden fehlen nicht — sie entfallen.
- **`records`** ist die vollständige, verlustfreie Liste aller `asa`-Records des Fensters: im
  Ruhezustand zehn, im Alarmfall bis zu 120. Sie ist bewusst nicht aggregiert, weil jeder
  Eintrag seinen eigenen Hash trägt und nur so einzeln dedupliziert werden kann. Das Aggregat
  steht **daneben**, nicht an ihrer Stelle.
- **`anomalies`** sind Beobachtungen, keine Fehler: ein `nff`, das springt; ein zweiter eigener
  Alert; ein unbekannter Stage-Wert. Sie gehören gemeldet, nicht verworfen.

**`asa_hash` kann leer sein.** Er braucht die Ensemble-Identität; in den ersten Sekunden nach
dem Start, bevor ein `ens`-Record kam, gibt es sie noch nicht. Ein Hash ohne `ens_hash` würde
zwei Ensembles zusammenwerfen, die zufällig dasselbe `raw` in derselben Sekunde tragen — deshalb
bleibt das Feld dann leer statt falsch.

### Alert

```json
{
  "alert_uid": "7c2d…", "alert_uid_confident": true,
  "oe": false, "channel_eid": "10ff", "warning_eid": "10ff",
  "subch_id": 7, "iid": 3,
  "stage": "level1_start", "level": 1, "test": false,
  "phase": "trigger", "entered_at_phase": "trigger",
  "first_seen_ens": "2026-08-26T14:03:00Z",
  "last_seen_ens":  "2026-08-26T14:04:12Z",
  "closed": false, "close_reason": "", "incomplete": false, "gap": false,
  "instances": 3, "expected_instances": 3,
  "phases": [
    { "phase": "pre_trigger", "from": "…", "to": "…", "sec": 0 },
    { "phase": "trigger",     "from": "…" }
  ],
  "area": {
    "whole_ensemble": false,
    "codes": [ { "zone": 10, "digits": "B736", "presentation": "2366-7443-8484",
                 "rect": { "lat_min": 51.4, "lat_max": 51.6,
                           "lon_min": -0.2, "lon_max": 0.0 } } ],
    "geojson": { "type": "MultiPolygon", "coordinates": [ … ] },
    "raw": "0a2b3c4d"
  },
  "audio": { "state": "stored", "subch_id": 7, "bytes": 507904,
             "started_at": "…", "sha256": "…", "truncated": false,
             "duration_s": 43.75, "duration_s_est": 0, "audio_gaps": 0,
             "sample_rate": 48000, "channels": 2, "mode": "HE-AACv2",
             "rs_corrected": 12,
             "files": [ { "name": "7c2d…-5C-7.dabp", "codec": "dabp",
                          "bytes": 262144, "sha256": "…" },
                        { "name": "7c2d…-5C-7.mp3", "codec": "mp3",
                          "bytes": 245760, "sha256": "…" } ] }
}
```

| Feld | Anmerkung |
|---|---|
| `alert_uid_confident` | `false` heißt: Der Knoten stieg erst in `sustain` ein und kennt weder Startminute noch IId. Der Server verkettet dann über (`warning_eid`, `iid`, Zeitfenster) selbst |
| `warning_eid` | das **warnende** Ensemble. Bei `oe: true` ist das `other_eid`, nicht das empfangene |
| `level` | aus `stage` abgeleitet (1, 2 oder `null` bei Test). Bequemlichkeit; `stage` bleibt maßgeblich |
| `test` | Consumer-Geräte ignorieren Test-Alerts. Ein Monitor gerade nicht — deshalb werden sie vollwertig verarbeitet und hart getrennt gekennzeichnet |
| `entered_at_phase` | Bei schlechtem Empfang kann die gesamte Trigger-Phase durch CRC-Fehler ausfallen. Der Alert ist dann ein gültiger, aber unvollständiger Befund |
| `close_reason` | `end`, `timeout` (30 s Stille), `shutdown` oder `stream_gap` |
| `gap` | Der Alert bestand über einen Neustart von `asamon-rx` hinweg und ist möglicherweise unvollständig |
| `whole_ensemble` | **dreiwertig.** `true`: keine Location Codes, der Alert gilt für das gesamte Versorgungsgebiet. `false`: es gibt ein Warngebiet. `null`: der Knoten sah nie eine Instanz mit Status-Feld und kann nichts sagen |
| `area.raw` | bleibt **immer** dabei, auch wenn die Geometrie gelingt |
| `area.decode_error` | steht dort, wenn die Location Codes nicht zu deuten waren. Der Alert wird trotzdem gemeldet |
| `audio.files` | je Datei Name, Codec, Größe und Prüfsumme. Über genau diese Namen läuft der Upload |
| `audio.sha256` | die Prüfsumme des **rohen** Bitstroms, also des Belegs; die der MP3 steht in `files` |
| `audio.duration_s` | die gemessene Dauer, sobald `asamon-rx` die Aufnahme gemeldet hat. Solange sie läuft, steht stattdessen `duration_s_est` — aus der Bitrate der Komponente geschätzt |
| `audio.rs_corrected`, `rs_errors`, `frame_errors`, `aac_errors` | was welle.io während der Aufnahme meldete. Ohne diese Zahlen ließe sich eine stockende Aufnahme nicht von einer stillen Meldung unterscheiden |
| `audio.audio_gaps` | bleibt seit dem 30.08.2026 **immer 0**: Der Mitschnitt geht nicht mehr durch den Record-Strom und kann keine Lücken durch verworfene Records bekommen. Das Feld bleibt, damit ältere Server nichts vermissen |

Ein Alert erscheint in **jedem** Datensatz, solange er läuft, und **genau einmal** mit
`closed: true`. Danach taucht er nicht mehr auf.

**Eine Ausnahme gibt es seit dem 30.08.2026:** Ein Alert mit Mitschnitt erscheint noch ein
zweites Mal mit `closed: true` — dann mit gefülltem `audio.files`. Grund ist die Reihenfolge:
`asamon-rx` meldet die fertigen Dateien erst nach dem STOP, also nach der letzten regulären
Meldung des Alerts. Ohne diesen Nachzügler erführe der Server nie, dass es die Dateien gibt,
und könnte sie folglich nie über `audio_wanted` anfordern.

**OE-Alerts enden nie mit `close_reason: "end"`.** OE-Signalisierung ist nach TS 104 089 §6.5.1
stets Trigger und trägt kein Phasenfeld; ein OE-Alert läuft daher immer in die 30-Sekunden-Frist
und schließt mit `timeout`. Das ist der Norm geschuldet, kein Befund.

### Überwarnung ist normal

Das kleinste sphärische Rechteck hat rund 1 km Kantenlänge, und ein Warngebiet besteht aus
höchstens vier FIG-Instanzen. Das signalisierte Gebiet ist deshalb regelmäßig größer als das
tatsächliche Gefahrengebiet. Das ist **keine Ungenauigkeit des Decoders** und darf auf der Karte
nicht als solche dargestellt werden.

---

## Duplikaterkennung — was der Server damit tun soll

Die Definitionen der Hashes und ihre Testvektoren stehen in [`hashes.md`](hashes.md). Für die
Serverseite zählt vor allem eines:

**Deduplizieren in zwei Stufen.**

1. **Exakte Hashgleichheit.** Zwei `asa_hash` sind gleich → dieselbe Beobachtung. Das ist der
   Regelfall: Beide Knoten hatten die Ensemble-Zeit aus FIG 0/10, und die ist bei allen
   Empfängern desselben Ensembles bitgleich.

2. **(`ens_hash`, `raw`, Sekunde ±1).** Fehlt einem Knoten die Ensemble-Zeit, fällt er auf seine
   eigene Uhr zurück und markiert das mit `time_source: "node"`. Er kann dann an einer
   Sekundengrenze eine Sekunde danebenliegen, und sein Hash weicht ab. Deshalb schickt der
   Datensatz neben dem Hash **immer auch** `ens_hash`, `ens_second` und `raw` mit.

Für Vorfälle gilt dasselbe eine Stufe höher: `alert_uid` verkettet, wo `alert_uid_confident`
gesetzt ist; sonst über (`warning_eid`, `iid`, Zeitfenster). Der IId ist nur 4 bit breit und
wird ensemble-lokal wiederverwendet — **eine global eindeutige Vorfalls-ID existiert on air
nicht**, und `alert_uid` tut nicht so, als gäbe es sie.

---

## Was dieses Dokument nicht festlegt

Serverseitige Datenhaltung, Korrelation über Knoten hinweg, Karte und Frontend, Vertrauens- und
Verifikationsmodell für Crowd-Daten. Wird beim Bau des Servers eine Festlegung nötig, die über
diese Schnittstelle hinausgeht, gehört sie nach `../../specs/`, nicht hierher.
