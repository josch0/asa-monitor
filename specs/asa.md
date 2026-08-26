# ASA / DAB-EWS — technische Zusammenfassung

Zusammenfassung der offiziellen Spezifikationen und der deutschen Umsetzungsdokumente aus
diesem Ordner, mit Fokus auf **Ablauf der Signalisierung** und **was daraus für ein
Monitoring-System folgt**. Quellenangaben in Klammern verweisen auf die Dokumente in
`etsi/` bzw. `de/` (Textfassungen in `text/`).

Stand: 25.08.2026.

---

## 1. Einordnung in einem Absatz

**ASA (Automatic Safety Alert)** ist der deutsche Marken- und Betriebsname für das
international standardisierte **DAB Emergency Warning System (EWS)** nach
**ETSI TS 104 089 V1.1.1 (2024-09)**. Die eigentliche Warnmeldung ist **normales DAB+-Audio**
in einem Subchannel des Ensembles — neu ist ausschließlich die **Signalisierung im FIC**
über **FIG 0/15**. Diese sagt einem Empfänger: In welchem Subchannel (bzw. in welchem anderen
Ensemble) läuft gerade eine Warnmeldung, welche Schwere hat sie, zu welchem Vorfall gehört sie
und für welches geografische Gebiet gilt sie. Ein Empfänger im Standby wacht auf, schaltet um,
spielt die Meldung ab und kehrt danach in seinen vorherigen Zustand zurück.

Wichtig für die Abgrenzung: ASA ist **nicht** das ältere Alarm-Announcement-Verfahren
(FIG 0/18 / FIG 0/19, Cluster 0xFF) aus EN 300 401. Es ersetzt es funktional
(ASA-Guidelines, Anhang E: „Vergleich ASA mit bisherigen Warnmöglichkeiten in DAB+").
Wer nach ASA-Meldungen sucht, muss **FIG 0/15** auswerten.

---

## 2. Voraussetzungen an ein teilnehmendes Ensemble

(TS 104 089, Kap. 5)

- Konformität zu ETSI EN 300 401.
- **FIG 0/10 in Long Form** mit **genauer** Zeit (Quelle GNSS oder NTP). Diese „Ensemble-Zeit"
  ist die Bezugsgröße für die gesamte EWS-Signalisierung.
- **FIG 0/15** wird gesendet, solange das Ensemble teilnimmt. Ohne aktive Warnung ist das
  die **Heartbeat-Form**.
- Das **P/D-Flag** im FIG-0-Header von FIG 0/15 dient der Feinsynchronisation schlafender
  Empfänger und ist zweckentfremdet:
  - `0` (Process) bei Sekundenzähler **0–29**
  - `1` (Discard) bei Sekundenzähler **30–59**
  - Alle 12 FIBs eines Transmission Frames gehören zum selben Zeitbezug.
- Ein Ensemble, das die Warnfunktion nicht zuverlässig sicherstellen kann, **darf keinen
  Heartbeat senden** (ASA-Guidelines, „ASA-Ensemble").
- Ein Ensemble führt zu jedem Zeitpunkt **höchstens eine** eigene Warnmeldung, kann aber
  beliebig viele **OE-Alerts** (Verweise auf andere Ensembles) gleichzeitig signalisieren.

---

## 3. FIG 0/15 — Bitlayout

(TS 104 089, Annex E — normativ)

### Header-Flags (FIG Type 0 Header, EN 300 401 Kap. 5.2.2.1)

| Flag | Bedeutung bei FIG 0/15 |
|---|---|
| **C/N** | SIV-Signalisierung. `1` bei Heartbeat sowie bei Sustain/End, wenn die Alert-Group leer ist; `0` bei Trigger-Signalisierung und bei Sustain/End, wenn gleichzeitig OE-Trigger signalisiert werden |
| **OE** | `0` = Alert im eingestellten Ensemble, `1` = Alert in einem anderen Ensemble |
| **P/D** | Sonderdefinition: Sekundenhälfte (siehe oben), **nicht** Programme/Data |

Datenbankschlüssel = **OE-Flag + Id-Feld**. Es gibt **kein CEI**
(die Alert-Datenbank existiert nur während des Alerts).

### Type-0-Feld

```
[ Id field: 8 oder 16 bit ][ Status field: 0 oder 8 bit ][ Location code a ] ... [ Location code l ]
```

**Id field, OE = 0** (Alert im eigenen Ensemble):

| Feld | Bits | Bedeutung |
|---|---|---|
| Phase | 2 | `00` Pre-trigger, `01` Trigger, `10` Sustain, `11` End |
| SubChId | 6 | Subchannel mit dem Warn-Audio |
| Rfa | 2 | nur bei Phase = Pre-trigger, auf 0 gesetzt |
| Sec | 6 | nur bei Phase = Pre-trigger: Sekundenzähler, zu dem die Trigger-Phase startet. Sonderwert **63** = Start bei Sekunde 0 mit 5 s Trigger-Dauer |

**Id field, OE = 1** (Alert in anderem Ensemble): 16 bit **EId** des warnenden Ensembles.

**Status field** (8 bit, nur bei Phase Pre-trigger oder Trigger):

| Feld | Bits | Bedeutung |
|---|---|---|
| Last | 1 (b7) | `1` = letztes FIG 0/15 dieser Alert-Group |
| Stage | 3 (b6–b4) | Warnstufe/Entwicklungsstand, siehe Tabelle unten |
| IId | 4 (b3–b0) | Incident Identifier, 0–15 |

**Stage-Werte:**

| Wert | Stage | Wirkung |
|---|---|---|
| `000` | Level 1 Start | alle Empfänger, weckt aus Standby; setzt Nutzer-Dismiss für diese IId zurück |
| `001` | Level 1 Update | neue Information zum laufenden Vorfall |
| `010` | Level 1 Repeat | Wiederholung bereits gesendeter Information |
| `011` | Level 1 Critical | überschreibt alle Nutzereinstellungen (Dismiss wirkungslos), setzt sie aber nicht zurück |
| `100` | Level 2 Start | nur Empfänger, die bereits Audio ausgeben |
| `101` | Level 2 Update | wie oben |
| `110` | Level 2 Repeat | wie oben |
| `111` | **Test** | nur für Testzwecke; Consumer-Empfänger werten diese Alerts **nicht** aus. IId ist hier bedeutungslos und darf als private Daten des Testanbieters genutzt werden |

**Location code** (je 0–48 bit, additiv; keine Location Codes = gesamtes Versorgungsgebiet):

| Feld | Bits | Bedeutung |
|---|---|---|
| NFF | 2 | Anzahl der **noch folgenden** FIG 0/15 dieses Alert-Sets; letzte Instanz hat NFF = 0. Bei C/N = 0 gilt: (NFF + 1) = Gesamtzahl im Alert-Set. Nicht Teil des Location Codes, nur Integritätsprüfung |
| Zone | 6 | globale Zone 0–41 |
| SCF | 1 | `0` = ein sphärisches Rechteck, `1` = Sub-codes-Feld vorhanden (2–15 Rechtecke) |
| Num digits | 3 | Anzahl der Ziffern im Feld „Other digits", 0–5 (bei SCF = 1 max. 4) |
| Digit 1 | 4 | höchstwertige Ziffer. Wert 0 kann in Polarzonen die gesamte Zone bezeichnen |
| Other digits | 0–20 | restliche Ziffern (bei SCF = 1 ohne die niedrigstwertige) |
| Padding | 0 oder 4 | nur wenn Num digits ungerade, alle Bits 0 |
| Sub-codes | 0 oder 16 | nur bei SCF = 1: Bitmaske, welche der 16 Teilflächen zum Warngebiet gehören |

Grenzen: **max. 25 Byte Location Codes pro FIG-0/15-Instanz**, **max. 4 Instanzen pro Alert-Set**.

### Heartbeat-Form

Leeres Type-0-Feld, erkennbar am **Length-Feld des FIG-Type-0-Headers = 1**, C/N = 1, OE = 0.
Wird **nicht** gesendet, solange Alerts signalisiert werden.

---

## 4. Ablauf einer Warnung (Signalisierungsphasen)

(TS 104 089, Kap. 6.3 und 6.6)

```
  ... Heartbeat (1x/s) ...
        |
        |  -- optional --
        v
  Pre-trigger          5 s vor Trigger-Start; Alert-Set 1x/s über 3 s,
  (Phase 00)           1 Instanz pro Transmission Frame; Sec = Startsekunde
        |              -> nur für andere Ensembles/Monitoring, Consumer ignorieren
        v
  Trigger              mindestens 5 s; Id + Status + Location Codes
  (Phase 01)           -> hier entscheidet der Empfänger, ob er umschaltet
        |
        v
  Sustain              nur Id-Feld, 1x/s; Alert läuft weiter,
  (Phase 10)           wird aber nicht mehr zur Auswahl angeboten
        |
        v
  End                  nur Id-Feld, 1x/Transmission Frame für 2 s
  (Phase 11)
        |
        v
  ... Heartbeat ...
```

**Alert-Group:** Alle Alerts in Trigger-Phase (eigenes Ensemble zuerst, dann OE-Alerts in
beliebiger Reihenfolge) werden zu einer Alert-Group zusammengefasst. Zusammenstellung
zum Minutenbeginn und normalerweise zu jedem Sekundenbeginn; in den ersten 5 Sekunden eines
Alerts kontinuierlich. Bei Sekundenzähler 59 wird die Übertragung der Alert-Group beendet,
auch wenn sie unvollständig ist. Das **Last-Flag** markiert die letzte FIG-0/15-Instanz der
Alert-Group.

**Zeitliche Kopplung:** Schlafende Empfänger prüfen **einmal pro Minute zur Sekunde 0**.
Warnmeldungen sollen deshalb **an der Minutengrenze** beginnen („synchronized alerts") —
das maximiert die Zahl der Geräte, die die Meldung vollständig hören.
Nur **Level-1**-Alerts werden von schlafenden Geräten ausgewertet, Level-2-Alerts nur von
bereits eingeschalteten.

---

## 5. Warngebiet: DAB Location Coding

(TS 104 089, Annex F — normativ; Annex A für die Nutzerdarstellung)

Ein Location Code beschreibt ein **sphärisches Rechteck** in Polarkoordinaten, hierarchisch
verfeinert. Zone (6 bit) + bis zu sechs Ziffern à 4 bit → max. 30 bit.
Maximale Auflösung: **977 m** in Nord-Süd-Richtung; Ost-West 978 m am Äquator, 302 m bei 72° Breite.
Jede weggelassene Ziffer vergröbert die Auflösung um Faktor 4 (Fläche × 16).

### Koordinatentransformation

```
SE (Southerly Extent) = 90 - Breite(WGS84)        # Nordpol 0°, Äquator 90°, Südpol 180°
EE (Easterly Extent)  = Länge(WGS84), bei negativen Werten + 360
```

### Zonenbestimmung (42 Zonen)

```
SE  <  18           -> Zone 0   (Nordpolzone)
18 <= SE < 162      -> Zone = 10 * int((SE - 18)/36) + int(EE/36) + 1     (Zonen 1-40)
SE >= 162           -> Zone 41  (Südpolzone)
```

Die 40 gebänderten Zonen sind 36° × 36° groß, in vier Reihen zu je zehn, von Nord nach Süd
und West nach Ost nummeriert.

### Ziffern in den gebänderten Zonen

```
SC (Southerly Code) = int(frac((SE - 18)/36) * 2^12)   -> 12 bit
EC (Easterly Code)  = int(frac(EE/36)        * 2^12)   -> 12 bit
CC (Combined Code)  = Interleave(SC, EC) je 2 bit, beginnend mit SC -> 24 bit
```

Jede Verfeinerungsstufe teilt die Fläche in 4 × 4 = 16 Teilflächen, nummeriert von
Nordwest nach Ost und Süd (0–15). In der 4-bit-Ziffer zählen die oberen 2 bit Nord→Süd,
die unteren 2 bit West→Ost.

**Beispiel (Spec):** BBC Broadcasting House, WGS84 (51,5187412 / −0,1434571)
→ SE = 38,4812588; EE = 359,8565429
→ Zone = 10 · int(0,5689) + int(9,9960) + 1 = 10
→ SC = 2330 = `91A`, EC = 4079 = `FEF`, CC = `B736BB`
→ **Z10:B736BB**

Die Polarzonen (0 und 41) haben eine abweichende Berechnung der ersten Ziffer
(Kreisgeometrie, Annex F.5) — für Deutschland irrelevant, für einen vollständigen Decoder
aber zu implementieren.

### Präsentationsformat für Nutzer (Annex A)

```
30-bit-Integer (6 bit Zone + 24 bit Ziffern)
  -> Checksumme = Integer mod 61  (6 bit) anhängen  -> 36 bit
  -> 12 Oktalziffern
  -> jede Ziffer + 1  -> Symbole "1".."8"
  -> drei Blöcke à 4 Symbole, mit Bindestrich getrennt
```

Beispiel: `Z10:B736BB` → `2366-7443-8484`.
URI-Form für Smart Devices / QR-Codes: **`DLI://2366-7443-8484`**.
Das ist genau der 12-stellige Code, den https://asa.radio/ zu einer Adresse ausgibt und den
Nutzer an ihrem ASA-Radio eintragen.

### Praktische Konsequenz: Überwarnung

Weil die Fläche aus wenigen (max. vier FIG-Instanzen) Rechtecken zusammengesetzt wird und das
kleinste Rechteck rund 1 km Kantenlänge hat, deckt das signalisierte Warngebiet stets auch
Flächen außerhalb des eigentlichen Gefahrengebiets ab. Die Guidelines nennen das
**Überwarnung** und halten ASA daher für kleinräumige Ereignisse (einzelne Straßenzüge)
für ungeeignet.

---

## 6. Empfängerverhalten (Kurzfassung)

(TS 104 089, Kap. 7; Details und Testfälle in TS 104 090)

- **Betriebsmodi:** Sleep (Prüfung 1×/Minute), Monitor, Audio.
- **Alert-Matching** in drei Stufen: *Receivability* (ist der Alert überhaupt empfangbar?),
  *Stage* (Warnstufe gegen Gerätezustand und Nutzereinstellungen), *Location*
  (liegt die konfigurierte Position im Warngebiet?).
- **Alert ohne Location Codes** → gesamtes Versorgungsgebiet des Ensembles; darauf reagieren
  auch Geräte **ohne** oder mit falsch konfigurierter Position.
- Alert mit Warngebiet → Geräte ohne konfigurierte Position reagieren **nicht**.
- Nach Ende der Meldung kehrt das Gerät in den vorherigen Zustand zurück.
- Reaktion auf Alarme darf **nicht** von einer Nutzereinstellung abhängen (Ausnahme:
  Dismiss-Funktion pro IId, die Level 1 Critical wiederum überschreibt).
- Mobile Empfänger (Fahrzeug) können ihre Position selbst bestimmen (GNSS).

---

## 7. Umsetzung in Deutschland

(ASA-Guidelines V2.0, Juli 2026 — `de/260630-ASA-Guidelines.pdf`)

### Warnkette

```
BBK (Zivilschutz, bundesweit)  |  Innenministerien der Länder (Katastrophenlagen)
             +-----------------+-----------------+
                               v
                            MoWaS         zentrale Erfassung und Verteilung,
                               |          CAP-XML an Warnmultiplikatoren
                               |          (Multiplikatoren-Vereinbarung mit dem BBK;
                               |           technische Anbindung über mecom,
                               |           Satellit / terrestrisch / redundant)
                               v
        Warnmultiplikator / Programmanbieter / Ensemble-Betreiber
                               |   5 Arbeitsschritte:
                               |   1. Auswählen    - betrifft die Meldung eines meiner Ensembles?
                               |   2. Aufbereiten  - DAB-Signalisierung erzeugen (Warnstufe,
                               |                     Warngebiet aus Polygonen/Geocodes, IId)
                               |   3. Einsprechen  - Text vertonen (Sprecher oder Text-to-Speech)
                               |   4. Informieren  - Nachbarensembles versorgen, inkl.
                               |                     alert.identifier gegen Doppelwarnung
                               |   5. Ausspielen   - Audio + Alert-Info an die Multiplexer,
                               |                     Start an der nächsten Minutengrenze
                               v
                    DAB+ Multiplexer  ->  Alert (FIG 0/15) + Warn-Audio im Subchannel
                               |
                               +--> Pre-Trigger -> ASA-Monitoring-Empfänger -> OE-Alert
                               |                                              in anderen Ensembles
                               v
                        ASA-Empfänger
```

Eine koordinierende Einheit („**ASA-Manager**") ist in den Guidelines als noch zu schaffen
beschrieben; einzelne Schritte sind noch offen.

### CAP-Inhalte aus MoWaS

Jede Meldung ist eine CAP-XML-Datei mit u.a.: Identifier, Priorität, Referenz auf
Vorgängermeldungen, Kennzeichnung Erstmeldung/Update/Entwarnung, betroffenes Gebiet als
**Polygone oder Geocodes für Gebietskörperschaften (SHN)**, Freitext mit
Handlungsanweisungen sowie standardisierte **EventCodes** und **InstructionCodes**
(Listen beim BBK). **Für ASA werden derzeit ausschließlich Meldungen der MoWaS-Priorität 1
genutzt; diese lösen Level-1-Warnungen aus.** Bei der Umrechnung Polygon → Location Codes ist
sicherzustellen, dass mindestens das betroffene Gebiet abgedeckt wird.

### Konkrete Ensembles und Dienste

- **Bundesweit:** Ensemble „DR Deutschland" auf **Kanal 5C**. Deutschlandradio richtet dafür
  einen zusätzlichen Service mit **32 kbit/s, EEP 2-A** ein, Servicelabel voraussichtlich
  **„ASA DE"**. Ensemble-Betreiber Media Broadcast empfängt MoWaS (nur Priorität 1),
  synthetisiert das Audio per TTS, wandelt die Polygone in ASA-Warngebiete um und übergibt
  Audio + Metadaten (Warnstufe, Warngebiet, IId) an redundante Multiplexer. Ausspielung an der
  nächstmöglichen Minutengrenze, das reguläre Audio des Services wird für die Dauer der
  Meldung überschrieben. Vor dem Alert wird ein **Pre-Trigger** gesendet.
  (Guidelines, „Studio- und Sendebetrieb", Anhang D)
- **Funkhaus Ingolstadt:** bereits weitgehend vollständige Warnkette, warnender Service
  „Oldie Welle", TTS-basiert, derzeit noch **ohne** Warngebiet. (Anhang C, „EmWaS")
- **Bayern Digital Radio:** Einführungsszenario mit ASA-Monitoring-Empfängern zur
  OE-Alert-Distribution. (Anhänge A und B)

### Warnender Service

Die Warnmeldung wird nie im Alert selbst transportiert — der Alert verweist nur auf einen
Service **im selben Ensemble**. Das kann ein dedizierter ASA-Kanal sein oder ein reguläres
Programm, dessen Audio für die Dauer der Meldung ersetzt wird. Ein warnendes Ensemble hat zu
jedem Zeitpunkt **genau einen** warnenden Service, der aber je nach Gebiet oder Tageszeit
wechseln kann (Modell der ARD wegen föderaler Struktur).

### OE-Alerts und Monitoring-Empfänger

Verweist Ensemble B per OE-Alert auf Ensemble A, muss A **im gesamten relevanten
Versorgungsgebiet von B** empfangbar sein — eine bloße Teilüberlappung ist unzulässig, weil
Empfänger in B sonst zwar den Verweis, aber nicht die Meldung empfangen.
Da zum Start noch kein IP-Verteilnetz für Alerts existiert, werden Alerts **über die
Luftschnittstelle zurückgewonnen**: ASA-Monitoring-Empfänger lesen den Pre-Trigger des
überwachten Ensembles aus und liefern daraus abgeleitete OE-Alerts an nachgereihte Multiplexer.
Betrieb bei der BDR in **heißer Redundanz** (zwei Geräte, keine gegenseitige Überwachung;
der Multiplexer verwirft den unmittelbar folgenden identischen Alert des zweiten Geräts),
eingebunden in ein Polling-basiertes Störungsmeldesystem.

**Für die Zuführung von Alerts an Multiplexer existiert noch kein einheitliches Protokoll**
(Standardisierung läuft in einer WorldDAB-Arbeitsgruppe); derzeit proprietäre Protokolle.

### Warnstufen im deutschen Betrieb

Level 1 (weckt auf), Level 2 (nur eingeschaltete Geräte), Test (von Consumer-Geräten ignoriert).
**Derzeit werden alle ASA-Warnungen als Level 1 ausgesendet.**
Probewarnungen am bundesweiten Warntag werden wie scharfe Warnungen signalisiert
(Level 1, weckt Geräte), weisen aber inhaltlich auf den Probecharakter hin, und werden
**immer mit Warngebiet** ausgesendet (Bundesgebiet oder einzelnes Land).

---

## 8. Was das für den ASA-Monitor bedeutet

Reine Ableitung aus den Specs — **keine Umsetzungsplanung, keine Technologieentscheidung.**

### Was ein Empfangsknoten mindestens auswerten muss

| Element | Wozu |
|---|---|
| **FIG 0/15** | Kern: Heartbeat, Phase, SubChId/EId, Stage, IId, Last, NFF, Location Codes |
| **FIG 0/0** | EId des empfangenen Ensembles (Zuordnung, OE-Auflösung) |
| **FIG 0/10** | Ensemble-Zeit (Long Form); Bezugszeit der gesamten Signalisierung |
| **FIG 0/1** | Subchannel-Organisation — nötig, um vom SubChId zum Audio zu kommen |
| **FIG 0/2** | Service-/Komponentenstruktur (welcher Service nutzt diesen Subchannel) |
| **FIG 1/x** | Ensemble- und Service-Labels für die Anzeige |
| Audio + DLS | Inhalt der Warnmeldung (optional Journaline/SlideShow) |

Daraus folgt: **Zugriff auf den FIC ist zwingend.** Ein reiner Audio-Player genügt nicht;
der Knoten braucht eine Decoder-Ebene, die FIBs/FIGs liefert.

### Fachliche Kernaufgaben

1. **Alert-Set-Rekonstruktion:** Mehrere FIG-0/15-Instanzen bilden ein Alert-Set (NFF,
   identische Id/Status außer Last-Flag). Mehrere Alert-Sets bilden eine Alert-Group.
2. **Deduplizierung:** Datenbankschlüssel ist OE-Flag + Id-Feld; ein Vorfall wird über die
   **IId** über mehrere Meldungen hinweg verkettet — allerdings nur **4 bit, ensemble-lokal**
   (max. 16 gleichzeitig verfolgte Vorfälle, Werte werden wiederverwendet). Eine global
   eindeutige Vorfalls-ID gibt es on air **nicht**; sie muss aus EId + IId + Zeitfenster +
   Warngebiet konstruiert werden.
3. **Location-Decoding:** Location Codes → sphärische Rechtecke → Polygone (z. B. GeoJSON).
   Das ist die Umkehrung von Annex F und die Voraussetzung für jede Kartendarstellung.
   Sub-codes (SCF = 1) liefern bis zu 15 Teilflächen pro Code.
4. **Pre-Trigger-Auswertung** gibt **5 Sekunden Vorlauf** vor der eigentlichen Meldung —
   nützlich, um Audio-Mitschnitt und Erfassung rechtzeitig zu starten.
5. **Heartbeat-Erfassung:** Ob ein Ensemble ASA unterstützt, ist selbst eine wertvolle
   Beobachtung (Abdeckungskarte der ASA-Einführung) und zugleich Lebenszeichen des Knotens.
6. **Crowd-Korrelation:** Dieselbe Warnung erscheint bei vielen Knoten, teils als Alert,
   teils als OE-Alert aus anderen Ensembles. Der Server muss beides zu einem Ereignis
   zusammenführen und zwischen „Ensemble X warnt selbst" und „Ensemble X verweist auf Y"
   unterscheiden.
7. **Test-Stage getrennt behandeln:** `Stage = 111` sind Testalarme, die Consumer-Geräte
   ignorieren — für ein Monitoring-System aber gerade interessant. Sie dürfen nur nicht als
   scharfe Warnung dargestellt werden. Ebenso: Probewarnungen am Warntag sind als
   **Level 1** signalisiert und on air **nicht** von einer echten Warnung unterscheidbar —
   die Unterscheidung steckt nur im gesprochenen Text.

### Nützliche Nebenerkenntnisse

- Unser System ist funktional ein **passiver ASA-Monitoring-Empfänger** im Sinne der
  Guidelines (Kapitel „ASA-Monitoring-Empfänger und der Pre-Trigger von Alerts") — mit dem
  Unterschied, dass wir nichts in einen Multiplexer zurückspeisen, sondern nur beobachten.
  Die dort beschriebenen Betriebsanforderungen (Redundanz, Störungserkennung, regelmäßige
  Bestätigung des Empfangs) sind sinngemäß auf ein Crowd-Netz übertragbar.
- **TS 104 090** enthält sieben Testszenarien für Empfänger (Set-up, Alert-Area-Matching,
  Stages, OE-Alerts, Sleep-Mode-Auswahl, Sleep-Mode-Reaktion, gleichzeitige Alerts).
  Diese eignen sich als Verifikationsszenarien für den eigenen Decoder.
- Eigene Testsignale mit FIG 0/15 lassen sich mit den **ODR-mmbTools** erzeugen, falls die
  Mux-Software das FIG unterstützt oder entsprechend erweitert wird — sonst über selbst
  erzeugte ETI-Frames.
- Da alle ASA-Alerts an der **Minutengrenze** starten sollen, ist eine genaue Zeitbasis im
  Knoten (NTP) sinnvoll, um Erfassungszeitpunkt und Ensemble-Zeit vergleichen zu können.

### Offene Punkte, die nur empirisch zu klären sind

- Welche Ensembles senden bereits einen ASA-Heartbeat? (Nur durch Messen feststellbar —
  und genau das ist ein Ergebnis, das dieses Projekt liefern kann.)
- Wird in Deutschland tatsächlich DL Plus / Journaline begleitend zur Warnmeldung genutzt?
- Wie werden Location Codes praktisch auf Verwaltungsgebiete abgebildet (für lesbare
  Gebietsangaben in der Anzeige)?
- Lässt sich die on air empfangene Warnung mit der öffentlichen MoWaS/NINA-Quelle
  korrelieren? Der `alert.identifier` aus CAP wird on air **nicht** übertragen.

### Einordnung, die im Blick bleiben sollte

Ein solches Monitoring ist reiner Empfang öffentlich ausgestrahlter Rundfunksignale, die
ausdrücklich für die Allgemeinheit bestimmt sind. Ein Crowd-Monitor ist allerdings
**keine offizielle Warnquelle** und darf nicht als solche auftreten oder wirken — er zeigt
verzögert und lückenhaft, was empfangen wurde. Offizieller Warnkanal bleiben ASA-Radios,
NINA und die amtlichen Wege.

---

## 9. Begriffe

| Begriff | Bedeutung |
|---|---|
| **ASA** | Automatic Safety Alert — deutscher Betriebsname des DAB-EWS |
| **EWS** | Emergency Warning System (ETSI TS 104 089) |
| **EWF** | Emergency Warning Functionality — älterer deutscher Systemname, Vorläufer/Oberbegriff |
| **Alert** | Signalisierung einer Warnmeldung im eigenen Ensemble |
| **OE-Alert** | Signalisierung einer Warnmeldung in einem anderen Ensemble (Other Ensemble) |
| **Alert-Set** | 1–4 FIG-0/15-Instanzen, die einen Alert samt vollständigem Warngebiet beschreiben |
| **Alert-Group** | Alle Alert-Sets in Trigger-Phase zu einem Zeitpunkt |
| **Heartbeat** | Leeres FIG 0/15: „Dieses Ensemble nimmt an ASA teil" |
| **IId** | Incident Identifier, 4 bit, verkettet Meldungen zu einem Vorfall |
| **Warnender Service** | Der DAB+-Service, in dem das Warn-Audio läuft |
| **Warnendes Ensemble** | Ensemble, in dem ein warnender Service aktiv ist |
| **Warngebiet** | Durch Location Codes beschriebene Fläche, in der Empfänger reagieren |
| **Überwarnung** | Mitabdeckung von Flächen außerhalb des Gefahrengebiets durch die Rechteck-Approximation |
| **MoWaS** | Modulares Warnsystem des Bundes; liefert CAP-XML an Warnmultiplikatoren |
| **Warnmultiplikator** | Stelle mit MoWaS-Vereinbarung, die Warnungen verbreitet |
