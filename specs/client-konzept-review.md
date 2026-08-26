# Review des vorgeschlagenen Client-Konzepts

Bewertung des Entwurfs „Client monitort einen Kanal, puffert FIG-0/15-Daten, sendet alle 10 s
an den Server, schneidet bei OE=0 das Warn-Audio mit, wechselt bei OE=1 den Kanal nicht".

Stand: 25.08.2026. Grundlage: `asa.md` und die Specs in diesem Ordner.
**Keine Umsetzungsplanung** — nur Prüfung der Tragfähigkeit und Änderungsvorschläge.

> **Nachfolgedokument.** Die hier entwickelten Anforderungen sind in
> [`client-architektur.md`](client-architektur.md) (Stand 26.08.2026) zu einem konkreten
> Zuschnitt geworden: zwei Prozesse (`asa-rx` in C++ gegen die welle.io-Bibliothek,
> `asa-node` in Go), verbunden über einen Record-Strom, der zugleich IPC-Protokoll,
> Archivformat und Roh-Beleg ist. Damit sind die Punkte 3.1 bis 3.4 dieses Reviews strukturell
> beantwortet. Dieses Dokument bleibt als Prüfung der fachlichen Anforderungen gültig.
>
> **Eine Änderung betrifft Punkt 3.4 unmittelbar:** Seit der Richtungsentscheidung vom
> 26.08.2026 wird FIG 0/15 im welle.io-Backend geparst, und über die Prozessgrenze gehen
> geparste Ereignisse statt roher FIBs. Die Forderung „roh vor geparst" bleibt gültig, ihre
> Erfüllung ändert die Form — siehe den Nachtrag am Ende von §3.4.

---

## 1. Gesamturteil

Der Ansatz ist **valide und deckt sich mit dem, was die Specs hergeben**. Vier Punkte sind
sachlich richtig getroffen:

- **Ein Kanal pro Knoten, kein Kanalwechsel bei OE=1.** Mit einem Tuner ist das die einzig
  sinnvolle Politik. Ein Wechsel würde das eigene Ensemble genau in dem Moment blind machen,
  in dem es interessant wird. Die Abdeckung anderer Ensembles ist Aufgabe des Netzes, nicht
  des einzelnen Knotens.
- **Audio-Mitschnitt nur bei OE=0.** Bei OE=1 verweist das FIG nur auf ein fremdes Ensemble
  (16-bit-EId, kein SubChId) — es gibt lokal gar kein Audio, das man mitschneiden könnte.
- **Puffern und gebündelt senden.** Richtig für die Dauerlast.
- **Der Heartbeat selbst als Messgröße.** Genau daraus entsteht die Abdeckungskarte der
  ASA-Einführung, die es sonst nirgends gibt.

Sechs Punkte würde ich ändern oder ergänzen (Abschnitt 3).

---

## 2. Zwei Präzisierungen zur Signalisierung

**Es heißt FIG 0/15** (FIG-Typ 0, Extension 15), nicht 15/0.

**„Sekündliche FIG-0/15-Aussendung" trifft nur den Ruhezustand.** Der 1-Hz-Rhythmus gilt für
den *Heartbeat*. Während eines Alerts wird der Heartbeat **nicht** gesendet, stattdessen laufen
je nach Phase unterschiedliche Formen (TS 104 089, Kap. 6.3):

| Phase | Rhythmus | Inhalt |
|---|---|---|
| Heartbeat | 1×/s | leeres Type-0-Feld (Length = 1) |
| Pre-trigger (00) | 1×/s über 3 s, 1 Instanz je Transmission Frame | Id + Status + Location Codes, `Sec` = Startsekunde |
| Trigger (01) | ≥ 5 s | Id + Status + Location Codes |
| Sustain (10) | 1×/s | nur Id-Feld |
| End (11) | 1×/Transmission Frame über 2 s | nur Id-Feld |

Der Client muss also **fünf Formen** unterscheiden, nicht eine — und das Ausbleiben von FIG 0/15
ist eine eigene, meldenswerte Beobachtung.

Ebenso: FIG 0/15 allein genügt nicht. Ohne **FIG 0/1** (SubChId → Lage und Größe im CIF) kommt
man vom signalisierten SubChId gar nicht zum Audio; ohne **FIG 0/0** (EId) und **FIG 0/10**
(Ensemble-Zeit, Long Form) fehlt der Bezugsrahmen; **FIG 0/2** und **FIG 1/x** liefern Service
und Label. Der Client braucht einen vollständigen FIC-Parser.

---

## 3. Änderungs- und Ergänzungsvorschläge

### 3.1 Alarme nicht in den 10-Sekunden-Takt zwängen

Das 10-s-Batching ist für Heartbeat und Telemetrie richtig, für einen Alert falsch: Es addiert
bis zu 10 s Latenz auf ein Ereignis, dessen einziger Wert Aktualität ist. Vorschlag: **zwei Pfade**.

| Pfad | Auslöser | Inhalt |
|---|---|---|
| Telemetrie-Batch | fester 10-s-Takt | Heartbeat-Aggregat, Empfangsqualität, Ensemble-Kontext |
| Ereignis-Push | sofort bei Phasenwechsel | Pre-trigger / Trigger / Sustain / End, roh + geparst |
| Audio-Upload | nach dem Alert, ggf. gestückelt | Warn-Audio, getrennt vom Ereignis |

### 3.2 Leere Batches immer senden

Wenn ein Knoten schweigt, sobald er nichts empfängt, kann der Server **„Ensemble sendet keinen
Heartbeat" nicht von „Knoten ist tot" unterscheiden** — und damit ist die Abdeckungskarte, das
Kernergebnis des Projekts, wertlos. Der 10-s-Batch muss auch dann rausgehen, wenn nichts
empfangen wurde, und dabei eine **explizite Negativ-Beobachtung** plus Empfangsqualität
(FIC-CRC-Fehlerrate, SNR, Verstärkungseinstellung) tragen.

### 3.3 Heartbeats aggregieren statt einzeln melden

Zehn Einzeldatensätze je 10 s sind reine Redundanz. Aussagekräftiger ist ein Aggregat:
Anzahl empfangener Heartbeats, fehlende Sekunden, Konsistenz des P/D-Flags gegen die
Sekundenhälfte, erster/letzter Zeitstempel. Das Roh-FIB nur bei Auffälligkeit mitschicken.

### 3.4 Roh vor geparst — und zwar dauerhaft

FIG 0/15 ist neu. Es gibt außerhalb der Upstreams genau **zwei** Implementierungen (siehe
[`decoder-optionen.md`](decoder-optionen.md), Abschnitt 0.1): eine partielle, aber
spec-konforme in Qt-DAB-Forks, die weder Heartbeat noch OE-Alerts verarbeitet — und eine
vollständige im welle.io-Fork von WarnBridge, deren **Bitlayout nicht der Norm entspricht**
(Phase und SubChId werden gar nicht gelesen, Status-Feld und Id-Feld sind vertauscht). Genau
das ist die Warnung: Ein falscher Parser liefert plausibel aussehende Werte, und es fällt erst
auf, wenn ein Ereignis bereits verloren ist. Unser Parser wird anfangs ebenfalls Fehler haben.
Deshalb: **rohe FIB-Bytes rund um jeden Alert mit übertragen** (die
Datenmenge ist vernachlässigbar), damit sich die Ereignisse serverseitig neu auswerten lassen,
wenn der Parser korrigiert wird. Ohne das ist jeder Parser-Bug ein unwiederbringlich
verlorenes Ereignis — und bis zum 10.09.2026 sind Ereignisse rar.

**Nachtrag 26.08.2026 — die Forderung wird auf ihren Kern zurückgeschnitten.** Mit dem
FIG-0/15-Patch im welle.io-Backend (`client-architektur.md` Abschnitt 2b) gehen über die
Prozessgrenze nur noch geparste Records. Davon bleibt **eine** Stufe des Roh-Belegs:

> Jedes ASA-Ereignis trägt die rohen **FIG**-Bytes (≤ 31 B, als Hex) mit sich. Damit ist es
> ohne Rückfrage neu auswertbar, falls sich unsere Bitpositionen als falsch erweisen — genau
> der WarnBridge-Fall, vor dem dieser Abschnitt warnt. Kosten: rund 60 Zeichen je Ereignis.

**Rohe FIBs werden nicht mehr übertragen**, weder dauernd noch auf Abruf. Es gibt keinen
FIB-Ring. Was damit wegfällt, ist der Nachweis, dass der FIB-Walk in `processFIB()` nichts
verschluckt hat; die Aussage „Ensemble X sendet keinen Heartbeat" stützt sich fortan allein auf
die **CRC-Quote** aus §3.2 — die bleibt unberührt und trägt den Hauptteil der Beweislast.

Das ist eine bewusste Vereinfachung für die ersten Ausbaustufen: Parserfehler gehen ins Log und
in einen Zähler, eine ausgearbeitete Fehlerbehandlung kommt später. Der Schritt zurück wäre
**additiv** — ein zusätzlicher Record-Typ bricht kein Format.

### 3.5 Audio: beim Trigger zuschalten genügt, und roh übertragen

**Kein Multiplex-Ringpuffer nötig.** Die Warnmeldung beginnt gleichzeitig mit der
Trigger-Phase, nicht danach — TS 104 089 Kap. 6.2 definiert Sustain als „continuation of the
alert message **after the Trigger phase has ended**". Und weil ein Empfänger laut Standard bis
zu fünf Sekunden zum Umschalten haben darf, schreiben die ASA-Guidelines dem Programmanbieter
vor, die ersten Sekunden inhaltlich leer zu halten: *„Denkbar wäre ein mindestens fünf Sekunden
langer Jingle, gefolgt von einer Einleitung wie ‚Dies ist eine amtliche Warnung von …'."*

Ein Knoten, der beim Trigger den signalisierten Subchannel zuschaltet, verliert daher nur die
Anlaufzeit des Decoders: Zeitentschachtelung über 16 CIFs (≈ 384 ms) plus
Superframe-Sync (bis 120 ms) — **rund 0,5 s, mitten im Jingle**. Die Trigger-Phase wiederholt
sich zudem 5 Sekunden lang durchgehend, es gibt also mehrere Chancen, sie zu erwischen. Der
Pre-Trigger ist ein Komfortgewinn, keine Voraussetzung.

Der Vollständigkeit halber die zwei Fälle, in denen doch gepuffert werden sollte:
- **Schlechter Empfang:** Fällt die gesamte Trigger-Phase durch FIC-CRC-Fehler aus, sieht man
  den Alert erst in der Sustain-Phase — die trägt zwar auch Phase und SubChId, aber der
  Mitschnitt beginnt dann mitten in der Meldung. Ein **kurzer, optionaler** Puffer (wenige
  Sekunden) fängt das ab. Architekturgrundlage ist er nicht.
- **Roher FIC:** ~~Ein FIB-Ringpuffer bleibt uneingeschränkt sinnvoll.~~ **Überholt seit dem
  26.08.2026** — siehe den Nachtrag in §3.4. Der Beleg schrumpft auf die rohen FIG-Bytes im
  Ereignis selbst; einen FIB-Ring gibt es nicht.

Ein großer Vorlauf wäre sogar kontraproduktiv: Der warnende Service kann ein reguläres
Programm sein, dessen Audio nur für die Dauer der Meldung ersetzt wird — ein Puffer würde
also genau das reguläre Programm-Audio mitschneiden, das weiter unten vermieden werden soll.

**Format.** Den **rohen DAB+-Bitstrom** übertragen (Superframes bzw. daraus abgeleitete
AAC-Frames), nicht lokal dekodiertes PCM:
- kein AAC-Decoder auf dem Pi nötig (aus dem Superframe ein ADTS/LATM-Frame zu bauen ist reine
  Header-Arbeit, kein Codec) — das hält die Abhängigkeitsliste kurz;
- der Originalbitstrom bleibt als Beleg erhalten;
- bei 32 kbit/s (EEP 2-A, wie für „ASA DE" auf 5C geplant) sind das 4 kB/s — eine zweiminütige
  Meldung ≈ 480 kB. Der Upload ist kein Thema.

**Zuschneiden.** Der warnende Service kann ein reguläres Programm sein, dessen Audio nur für die
Dauer der Meldung überschrieben wird. Der Mitschnitt sollte deshalb eng auf das Alert-Fenster
begrenzt werden (kurzer Vor-/Nachlauf), damit nicht unnötig regulärer Programminhalt
mitgeschnitten und zentral gespeichert wird. Reiner Empfang ist unkritisch; die zentrale
Sammlung und Weiterverbreitung von Programm-Audio ist eine andere Frage. Zugriff auf die
Audiodateien entsprechend beschränken.

**Mitnehmen, weil billig:** DLS/DL+ des warnenden Service, ggf. Journaline — Text zur Meldung
ohne nennenswerten Mehraufwand.

### 3.6 OE-Alerts trotzdem vollständig melden

Kein Kanalwechsel heißt nicht „ignorieren". Ein OE-Alert (EId + Stage + IId + Location Codes)
ist für den Server wertvoll: Er verknüpft „Ensemble B verweist auf A" mit dem Knoten, der A
tatsächlich empfängt, und ist oft das **früheste** Signal im Netz überhaupt. Perspektivisch
könnte der Server daraus Knoten, die das referenzierte Ensemble monitoren, gezielt in einen
Aufzeichnungs-Bereitschaftszustand versetzen.

**Nachtrag 26.08.2026 — auf einem Mehrkanalknoten geht das schon ohne Server.** Überwacht ein
Knoten mehrere Kanäle mit mehreren Sticks, verwaltet **ein** `asa-node` sie alle
(`client-architektur.md` Abschnitt 4a). Kommt dann auf Kanal A ein OE-Alert mit EId X herein
und läuft X auf Kanal B desselben Knotens, kann `asa-node` den Recorder auf B **sofort** scharf
stellen — Mikrosekunden statt einer Serverrunde. Der oben beschriebene Weg über den Server
bleibt für alles nötig, was über den einzelnen Knoten hinausgeht.

---

## 4. Weitere Randbedingungen für den Client

| Thema | Anforderung |
|---|---|
| Zeitbasis | NTP/chrony auf dem Knoten. Jede Beobachtung trägt Knotenzeit **und** Ensemble-Zeit (FIG 0/10) — der Versatz ist selbst eine Messgröße. |
| „Ohne weitere Abhängigkeiten" | In Reinform nicht erreichbar: SDR-Treiber (librtlsdr oder SoapySDR), FFT (FFTW3f oder KissFFT) und Kanaldecodierung sind Pflicht. Vermeidbar sind AAC-Decoder, Qt, GNU Radio, Web-Stack. |
| Zielhardware | Dauerlast ist FIC-Decodierung plus im Alarmfall **ein** Subchannel — deutlich weniger als eine Volldecodierung des Multiplex. Pi 4/5 unkritisch, Pi 3 und Zero 2 W plausibel, aber zu messen. |
| Mehrere Kanäle | Ein **Empfangs**prozess je SDR/Kanal, konfigurationsgesteuert — nicht mehrere Kanäle in einem Empfangsprozess verschachteln. Diese Regel gilt unverändert; sie betrifft `asa-rx`. Darüber verwaltet **ein** `asa-node` alle Kanäle des Knotens: eine Identität, ein Spool, ein 10-s-Batch für alle Kanäle, eine systemd-Unit (`client-architektur.md` Abschnitt 4a). |
| Store-and-Forward | Netzausfälle überbrücken: Batches lokal persistieren, mit Backpressure und Obergrenze; Audio bevorzugt gegenüber Telemetrie nachliefern. |
| Vertrauen | Knotenidentität, signierte Pakete, Replay-Schutz; deklarierte Position und Antennenangaben als Metadaten. |
| Test-Stage | `Stage = 111` (Test) mitschneiden und melden, aber im Datenmodell **hart** von scharfen Warnungen trennen. |
| Warntag-Probewarnungen | Werden als Level 1 mit Warngebiet ausgesendet und sind on air **nicht** von echten Warnungen unterscheidbar — das gehört in die Darstellung, nicht in die Client-Logik. |

---

## 5. Offene Fragen, die das Protokoll bestimmen

1. ~~**Rohdaten-Politik:** Nur bei Alerts rohe FIBs mitliefern, oder dauerhaft?~~
   **Beantwortet (26.08.2026):** gar nicht. Rohe FIBs verlassen den Empfangsprozess nicht; der
   Beleg sind die rohen FIG-Bytes im Ereignis selbst. Siehe den Nachtrag in §3.4.
2. **Audio-Format:** Roher DAB+-Bitstrom (empfohlen) oder serverfreundlich transkodiert?
   Welcher Vor-/Nachlauf um das Alert-Fenster?
3. **Minimale Zielhardware:** Pi 4/5 als Untergrenze — oder muss Pi 3 / Zero 2 W laufen?
   Bestimmt, wie viel Spielraum für Zusatzfunktionen (Journaline, DL+, zweiter Kanal) bleibt.
4. ~~**Sprache/Stack des Clients:** C++ nah an den Decodern, oder Rust/Go mit dem Decoder als
   Subprozess?~~ **Beantwortet** in [`client-architektur.md`](client-architektur.md)
   Abschnitt 4a: beides, entlang der Wissensgrenze getrennt. `asa-rx` ist C++, weil es gegen
   die welle.io-Bibliothek linkt und sonst nichts tut; `asa-node` ist Go und trägt Parser,
   Zustandsmaschine, Spool und Uplink. Die Prozessgrenze ist der Record-Strom.
5. **Ausbaustufen:** Erst Heartbeat-Erfassung (deutlich schlanker, sofort nutzbar für die
   Abdeckungskarte), Audio in Stufe 2 — oder von Beginn an beides?
