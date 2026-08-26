# Quellen und Links

Stand: 25.08.2026. Alle als „lokal" markierten Dokumente liegen in diesem Ordner,
Textextrakte jeweils unter `text/<dateiname>.txt`.

## 1. Offizielle ETSI-Standards

Alle ETSI-Deliverables sind frei abrufbar unter `https://www.etsi.org/deliver/...`.
Hinweis: Der Abruf per Kommandozeile erfordert einen Browser-User-Agent, sonst antwortet
Cloudflare mit HTTP 403.

### Kern (ASA/EWS unmittelbar)

| Spec | Titel | Datei | Relevanz |
|---|---|---|---|
| **ETSI TS 104 089 V1.1.1 (2024-09)** | DAB; Emergency Warning System (EWS); Definition and rules of behaviour | `etsi/ts_104089v010101p.pdf` | **Zentral.** Definiert FIG 0/15, Alert-Phasen, Location-Coding, Empfängerverhalten |
| **ETSI TS 104 090 V1.1.2 (2025-02)** | DAB; EWS; Minimum requirements and test specifications for receivers | `etsi/ts_104090v010102p.pdf` | Testfälle 1–7; brauchbar als Prüfszenarien für den eigenen Decoder |
| **ETSI EN 300 401 V2.2.1 (2017-05)** | DAB to mobile, portable and fixed receivers | `etsi/en_300401v020201p.pdf` | DAB-Basisnorm: FIC/FIB/FIG-Struktur, FIG 0/0, 0/1, 0/2, 0/10, Announcements |
| **ETSI TS 103 176 V2.6.1** | DAB; Rules of implementation; Service information features | `etsi/ts_103176v020601p.pdf` | Implementierungsregeln, u.a. EWF-Kapitel und Announcement-Handhabung |

Direkt-URLs:
- TS 104 089: https://www.etsi.org/deliver/etsi_ts/104000_104099/104089/01.01.01_60/ts_104089v010101p.pdf
- TS 104 090: https://www.etsi.org/deliver/etsi_ts/104000_104099/104090/01.01.02_60/ts_104090v010102p.pdf
- EN 300 401: https://www.etsi.org/deliver/etsi_en/300400_300499/300401/02.02.01_60/en_300401v020201p.pdf
- TS 103 176: https://www.etsi.org/deliver/etsi_ts/103100_103199/103176/02.06.01_60/ts_103176v020601p.pdf

### Ergänzend (Begleitdienste, Transport, Tabellen)

| Spec | Titel | Datei | Relevanz |
|---|---|---|---|
| ETSI TS 102 979 V1.1.1 | DAB; Journaline; Journaline specification | `etsi/ts_102979v010101p.pdf` | Textbegleitinformation bei EWF/EWFplus, mehrsprachig |
| ETSI TS 102 980 V2.1.2 | DAB; Dynamic Label Plus (DL Plus) | `etsi/ts_102980v020102p.pdf` | Begleittext zur Warnmeldung |
| ETSI TS 101 756 V2.5.1 | DAB; Registered tables | `etsi/ts_101756v020501p.pdf` | Country IDs, Programme Types, Announcement Types, Language Codes |
| ETSI EN 301 234 V2.1.1 | DAB; Multimedia Object Transfer (MOT) protocol | `etsi/en_301234v020101p.pdf` | Transport für SlideShow/Journaline |
| ETSI TS 101 499 V3.2.1 | DAB; MOT SlideShow | `etsi/ts_101499v030201p.pdf` | Optionale Bildinhalte zur Warnmeldung |
| ETSI TS 102 818 V3.5.1 | DAB/DRM/RadioDNS; SPI (Service and Programme Information) | `etsi/ts_102818v030501p.pdf` | Dienstmetadaten, evtl. zur Anreicherung |
| ETSI EN 300 797 V1.3.1 | DAB; Distribution interfaces; STI | `etsi/en_300797v010301p.pdf` | Zuführungsschnittstelle; Kontext zur Sendeseite |

Nicht existent: „ETSI TS 103 923" — unter dieser Nummer gibt es kein EWF/EWS-Dokument.
Maßgeblich sind TS 104 089 und TS 104 090.

Nicht geladen (bei Bedarf nachziehen): ETSI EN 300 799 (ETI) liegt nicht unter dem
üblichen `etsi_en/300700_300799/300799/`-Pfad; relevant nur, falls ETI-Frames selbst
erzeugt/interpretiert werden sollen.

## 2. Deutsche Umsetzung

| Dokument | Datei | Inhalt |
|---|---|---|
| **ASA-Guidelines V2.0, Juli 2026** — Digitalradio Deutschland e.V. | `de/260630-ASA-Guidelines.pdf` | **Wichtigstes deutsches Dokument.** Begriffe, Warnkette MoWaS→DAB+, Sendeseite, ASA-Monitoring-Empfänger, Anhänge zu BDR/Ingolstadt/Bundesmux |
| ASA-Handout: Anleitung zum Funktionstest (02/2026) | `de/ASA-Handout-Anleitung-zum-Funktionstest.pdf` | Ablauf des Funktionstests am Gerät |
| ASA Fact Sheet (07/2025, EN) | `de/ASA-Fact-Sheet.pdf` | Einseitige Übersicht |
| EWF-Präsentation Lokalrundfunktage 2022 | `de/220706-EWF-Lokalrundfunktage-Praesentation.pdf` | Historischer Kontext, Systemvorstellung (über FragDenStaat) |
| PM 08/2024: ASA-Weltpremiere / neue ETSI-Norm | `de/PM-2024-08-ASA-Weltpremiere.pdf` | Einordnung |
| PM 03/2025: ASA-Zertifizierungssystem gestartet | `de/PM-2025-03-ASA-Zertifizierungssystem.pdf` | Zertifizierung/Tick Mark |

Quell-URLs:
- ASA-Guidelines: https://www.dabplus.de/wp-content/uploads/sites/5/2026/07/260630-ASA-Guidelines.pdf
- Funktionstest: https://www.dabplus.de/wp-content/uploads/sites/5/2026/02/ASA-Handout-Anleitung-zum-Funktionstest.pdf
- Fact Sheet: https://www.dabplus.de/wp-content/uploads/sites/5/2025/07/ASA-Fact-Sheet.pdf
- EWF-Präsentation: https://media.frag-den-staat.de/files/foi/744439/220706-ewf-lokalrundfunktage_geschwaerzt.pdf

### Nicht (mehr) verfügbar

- **„Systemkonzept Warnmeldungen über DAB+" V1.3 (2023)** und die zugehörigen Management Summaries
  (DE/EN) waren über Google-Drive-Links in der Pressemitteilung vom 07.09.2023 verlinkt.
  Alle drei Dateien liefern inzwischen HTTP 404, auch über `drive.usercontent.google.com`.
  Inhaltlich weitgehend abgelöst durch die ASA-Guidelines V2.0.
  Referenz-Pressemitteilung: https://www.dabplus.de/2023/09/07/warnmeldungen-ueber-dab-beteiligung-am-warntag-am-14-september-neue-internationale-standards-systemkonzept-faqs-und-betraege-abrufbar/
  → Bei Bedarf direkt bei Digitalradio Deutschland e.V. anfragen (buero@dabplus.de).

## 3. Webseiten und Portale

| Seite | URL | Zweck |
|---|---|---|
| ASA-Portal Digitalradio Deutschland | https://www.dabplus.de/asa/ | Zentrale deutsche ASA-Seite inkl. Downloads |
| ASA Location Codes | https://www.asa.radio/ | Adresse → 12-stelliger Positionscode (Präsentationsformat nach TS 104 089 Annex A) |
| WorldDAB ASA/EWS-Specs | https://www.worlddab.org/dab/asa-emergency-warnings/asa-ews-etsi-specifications | Internationale Einordnung der ETSI-Specs |
| DTG Testing: zertifizierte Geräte | https://www.dtgtesting.com/asa-approved-products/ | Liste ASA-zertifizierter Empfänger |
| EWF-Informationsseite | https://www.ewf.digital/ | Hintergrund zur Emergency Warning Functionality |
| BBK MoWaS | https://www.bbk.bund.de/DE/Warnung-Vorsorge/Warnung-in-Deutschland/MoWaS/mowas_node.html | MoWaS, CAP-Profil, Event- und Instruction-Codes |
| Wikipedia EWF | https://de.wikipedia.org/wiki/Emergency_Warning_Functionality | Überblick |

## 4. Open-Source-Werkzeuge (für die spätere Empfangsseite)

> Ausführliche Analyse aller Kandidaten mit Aktualität, Lizenz, FIC-Zugriff und Bewertung:
> **[`decoder-optionen.md`](decoder-optionen.md)** (Stand 25.08.2026) — für welle.io in den
> Abschnitten 3.1 und 4 überholt durch
> **[`client-architektur.md`](client-architektur.md)** (Stand 26.08.2026).

| Projekt | URL | Anmerkung |
|---|---|---|
| welle.io | https://github.com/AlbrechtL/welle.io | Ausgereifter DAB/DAB+-SDR-Empfänger (C++/Qt), nutzt teilweise Code aus dablin. Das Qt-freie Backend wird als Bauziel `welle` gebaut (`libwelle.a`) — **kein eigenes Repository, kein Paket, keine installierten Header**; Einbindung nur über den Quellbaum |
| eti-stuff / eti-cmdline | https://github.com/JvanKatwijk/eti-stuff | Erzeugt ETI-Frames aus dem SDR-Stream, headless |
| dablin | https://github.com/Opendigitalradio/dablin | DAB/DAB+-Player, liest ETI-Bitstreams |
| ODR-mmbTools | http://www.opendigitalradio.org/software | Vollständige Sendekette (ODR-DabMux/DabMod), nützlich zum Erzeugen eigener Testsignale mit FIG 0/15 |

### Bekannte FIG-0/15-Implementierungen (nur in Forks, Stand 25.08.2026)

| Fundstelle | URL | Anmerkung |
|---|---|---|
| **WarnBridge** (Schwesterprojekt) | https://github.com/TogeriX-hub/dab-warnings-meshcore | DAB+-Warnmeldungen (ASA/Journaline) auf dem Pi empfangen und ins MeshCore-LoRa-Mesh einspeisen, NINA als Fallback |
| WarnBridge welle.io-Fork | https://github.com/TogeriX-hub/welle.io | `FIG0Extension15()` + ASA-Felder in `mux.json`; **Bitlayout weicht von TS 104 089 Annex E ab** |
| Qt-DAB-Forks mit Id-Feld-Parser | https://github.com/gkusmierz/qt-dab · https://github.com/mnhauke/qt-dab | Phase + SubChId korrekt nach Annex E; verwirft aber Heartbeat (C/N=1) und OE-Alerts. Im Qt-DAB-Upstream nur noch als Stub „to be researched" |

## 5. Wie diese Sammlung aktualisiert wird

ETSI-Dokumente mit Browser-User-Agent laden, sonst 403:

```bash
UA="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
curl -sL -A "$UA" -o specs/etsi/<datei>.pdf "https://www.etsi.org/deliver/<pfad>"
pdftotext -layout specs/etsi/<datei>.pdf specs/text/<datei>.txt
```

Neuere Versionen finden: Verzeichnis `https://www.etsi.org/deliver/etsi_ts/<band>/<nummer>/`
mit demselben User-Agent abrufen und die Versionsordner (`NN.NN.NN_60`) auslesen.
