# Ausrollen

Wie kommt der Knoten auf die Rechner der Freiwilligen? Muss jeder selbst bauen, oder gibt es
fertige Binaries?

**Kurzfassung:** Für `asamon-node` sind fertige Binaries trivial und sollten es geben — ein
Rechner baut alle Plattformen in Sekunden. Für `asamon-rx` sind sie möglich, aber je Plattform
ist eine eigene Bauumgebung nötig, und **für Windows fehlt zuerst ein Port**. Beides sind
GPL-Programme; Binärweitergabe ist erlaubt und verpflichtet zur Quellweitergabe, die dieses
Projekt ohnehin leistet.

---

## Warum die Frage überhaupt zählt

Der ASA-Monitor ist ein **Crowd-Projekt**. Sein Wert steigt mit der Zahl der Knoten, und jeder
Knoten ist ein Freiwilliger, der einen RTL-SDR-Stick übrig hat. Die Hürde, an der solche Leute
abspringen, ist nicht die Konfiguration — die ist eine Datei. Es ist der Satz „übersetze dir
zuerst welle.io".

Auf einem Raspberry Pi Zero 2 W bedeutet das: Abhängigkeiten nachinstallieren, Swap einrichten,
weil sonst der Übersetzer im Speicher stirbt, und dann warten. Wer das verlangt, bekommt Knoten
von Leuten, die ohnehin C++ übersetzen — und verliert alle anderen. Fertige Binaries sind
deshalb kein Komfort, sondern die Bedingung dafür, dass aus dem Projekt ein Netz wird.

---

## Die beiden Programme sind grundverschieden

| | `asamon-node` | `asamon-rx` |
|---|---|---|
| Sprache | Go, `CGO_ENABLED=0` | C++17 |
| Fremdcode | `gopkg.in/yaml.v3` | welle.io (eingebundener Quellbaum), FFTW3f, FAAD2, mpg123, librtlsdr |
| Cross-Build | `GOOS=linux GOARCH=arm64 go build` — von jedem Rechner aus | je Plattform eine native Bauumgebung |
| Ergebnis | eine statische Datei ohne Laufzeitabhängigkeiten | dynamisch gegen die Systembibliotheken gelinkt |
| Windows | läuft, statisch | läuft nativ (MinGW-w64), dynamisch — siehe unten |

Das ist keine Nachlässigkeit, sondern die Folge der Richtungsentscheidung vom 26.08.2026: Der
FIG-0/15-Parser sitzt im welle.io-Backend, und damit ist `asamon-rx` an dessen Bauwelt gebunden.
`asamon-node` wurde bewusst frei davon gehalten — kein cgo, kein SDR-Code — und genau das zahlt
sich hier aus.

---

## Stand: Windows

`asamon-node` läuft unter Windows vollständig: Prozessverwaltung, Deutung, Spool, Uplink, Audio.
Die plattformabhängigen Stellen sind ausgeschrieben (Pfade nach `%ProgramData%`, Job Object
statt Control Group, Uhrenauskunft), und die Tests laufen dort durch.

`asamon-rx` läuft dort seit dem 27.08.2026 ebenfalls — **nativ**, gebaut mit MSYS2/MinGW-w64.
Die drei POSIX-Mechanismen sind ersetzt, und zwar nicht mit `#ifdef` verstreut, sondern hinter
einer Schnittstelle in `src/platform.h` mit je einer Umsetzung pro Plattform:

| Stelle | Linux | Windows |
|---|---|---|
| MSC-Mitschnitt | `mkfifo` + `open(O_RDONLY\|O_NONBLOCK)` | `CreateNamedPipe` auf `\\.\pipe\…`, überlappte E/A |
| Abbruch | `sigaction(SIGINT/SIGTERM)` | `SetConsoleCtrlHandler` |
| Kommandos auf stdin | `poll` + `read` | `PeekNamedPipe` |
| sd_notify | `AF_UNIX`-Socket | entfällt — es gibt kein systemd |

Gebaut wird mit **MinGW**, nicht mit MSVC — denselben Weg geht welle.io für seine eigenen
Windows-Pakete. Der CMake-Pfad, der vorher der offene Punkt war, läuft durch; nötig war eine
einzige Ergänzung: `ws2_32` explizit linken, weil welle.io das unter qmake von Qt geschenkt
bekommt und unter CMake niemand tut.

**Ein Windows-Knoten empfängt damit.** Was noch aussteht, ist der Beleg mit echtem Stick: Der
Rauchtest deckt `--device rawfile` ab, librtlsdr über WinUSB muss ein Feldtest zeigen. Der
Bauweg steht in `../../asamon-rx/README.md`, die Nachlese in `../../asamon-rx/TODO.md`
Abschnitt 17.

---

## Was ein Release enthalten muss

Beide Programme stehen unter **GPL-3.0-or-later**. Binaries weiterzugeben ist ausdrücklich
erlaubt und verpflichtet dazu, den zugehörigen Quelltext verfügbar zu machen. Für ein
GitHub-Release ist das erfüllt, sobald das Repo öffentlich ist und der Release-Tag darauf zeigt.

Bei `asamon-rx` kommt eine zweite Pflicht dazu: Es linkt gegen ein **verändertes** welle.io, und
dessen Quelltext samt Änderungen muss ebenfalls verfügbar sein. Das leistet der öffentliche Fork
[josch0/welle.io](https://github.com/josch0/welle.io), Zweig `asa-fig0-15`, auf den das Submodul
mit einem festgenagelten Commit zeigt.

Damit das nicht von der Erinnerung abhängt, gehört in **jedes Release eine `QUELLEN.txt`**:

```
asamon-node 0.1.0
  Quelle:  https://github.com/<konto>/asa-monitor   Tag v0.1.0, Commit <sha>
  Lizenz:  GPL-3.0-or-later

asamon-rx 0.1.0
  Quelle:  https://github.com/<konto>/asa-monitor   Tag v0.1.0, Commit <sha>
  welle.io: https://github.com/josch0/welle.io      Commit 296e5d30 (Zweig asa-fig0-15)
  Lizenz:  GPL-3.0-or-later; welle.io GPL-2.0-or-later
  Gebaut ohne FDK-AAC (siehe unten)
```

**FDK-AAC muss aus bleiben.** Die Fraunhofer-Lizenz gilt weithin als GPL-unverträglich; ein
damit gebautes Binary dürfte gar nicht weitergegeben werden. `CMakeLists.txt` erzwingt
`FDK_AAC=OFF`, und ein Release-Bau darf das nicht überstimmen.

---

## Empfehlung: vier Stufen

### Stufe 0 — das Repo veröffentlichen

Heute hat dieses Repo **kein Git-Remote**. Ohne veröffentlichtes Repo gibt es keine Releases und
— wegen der GPL — auch keine Grundlage, Binaries weiterzugeben. Das ist der erste Schritt, und
er ist Voraussetzung für alles Weitere.

### Stufe 1 — `asamon-node` als Release

Kostet fast nichts und bringt sofort etwas: Wer den Empfangsprozess selbst baut, muss wenigstens
den Knoten nicht auch noch bauen.

```bash
make dist VERSION=0.1.0
```

erzeugt je Plattform ein `.tar.gz` mit Binary, Beispielkonfiguration, README, Lizenz und (unter
Linux) der systemd-Unit, dazu `SHA256SUMS`. Zielplattformen: `linux/amd64`, `linux/arm64`,
`linux/arm` (armv7), `windows/amd64`.

In CI erledigt das `.github/workflows/release.yml` bei jedem Tag `v*`.

### Stufe 2 — `asamon-rx` für Linux als Release

Das ist die Stufe, die den Freiwilligen wirklich Arbeit abnimmt. Nötig ist je Architektur eine
native Bauumgebung; zwei Wege stehen offen:

- **arm64:** GitHub bietet inzwischen arm64-Linux-Runner an. Nativ gebaut, keine Emulation.
- **armv7:** kein eigener Runner; über QEMU in einem `arm32v7`-Container. Langsam, aber es
  läuft unbeaufsichtigt.

Zu klären ist dabei eine Frage, die dieses Dokument **nicht** beantwortet, weil sie sich nur am
gebauten Binary klären lässt: `asamon-rx` linkt `libwelle` statisch, aber FFTW3f, FAAD2, mpg123
und librtlsdr kommen aus Systempaketen und damit dynamisch. Ein ausgeliefertes Binary braucht
sie also auf dem Zielrechner. Zwei Auswege:

1. **`.deb` bauen** mit `Depends: libfftw3-single3, libfaad2, libmpg123-0, librtlsdr0`. Der
   Paketmanager holt sie dann. Für Raspberry Pi OS und Debian ist das der saubere Weg — und
   nebenbei bekommt der Knoten damit systemd-Unit und Konfiguration am richtigen Ort.
2. **Statisch linken**, soweit die Lizenzen es hergeben. Macht das Binary groß und den Bau
   fragil.

**Empfehlung: `.deb`.** Die Zielgruppe sind Raspberry Pis; ein `apt install ./asamon_0.1.0_arm64.deb`
ist die kürzeste Anleitung, die es gibt.

### Stufe 3 — Windows

Der Port ist gemacht (27.08.2026); was fehlt, ist das Archiv. Es enthält beide Programme
nebeneinander — `asamon-node` findet `asamon-rx.exe` dann von selbst im eigenen Verzeichnis,
und ein ausgepacktes Archiv läuft ohne Installation.

Zu beachten: Anders als `asamon-node` linkt `asamon-rx` dynamisch. Das Windows-Archiv enthält
deshalb nicht eine Datei, sondern einen Ordner — die EXE plus die DLLs, samt deren
Lizenztexten. Beim MinGW-w64-Bau sind das `libstdc++-6.dll`, `libwinpthread-1.dll` und
`libgcc_s_seh-1.dll` sowie FFTW3f, FAAD2, mpg123 und librtlsdr. Dazu kommt der Hinweis auf
**Zadig**: Der RTL-SDR-Stick braucht unter Windows den WinUSB-Treiber, so wie er unter Linux
das Blacklisten von `dvb_usb_rtl28xxu` braucht.

Die CI baut Windows für `asamon-rx` noch nicht — dafür bräuchte der Workflow MSYS2. Das ist
die verbliebene Arbeit dieser Stufe, nicht mehr der Port selbst.

---

## Was dagegen spricht, es beim Selbstbauen zu belassen

Der Vollständigkeit halber, denn es gibt ein Argument dafür: Wer selbst baut, hat den Quelltext
gesehen und weiß, was auf seinem Rechner läuft. Bei einem Programm, das rund um die Uhr Daten
über den eigenen Standort verschickt, ist das kein leeres Argument.

Es trägt trotzdem nicht. Erstens schließt das eine das andere nicht aus — der Bauweg bleibt
dokumentiert und ist der empfohlene für alle, die ihn gehen wollen. Zweitens ist die Prüfbarkeit
besser durch **nachvollziehbare Builds** zu erreichen als durch Handarbeit: Ein Release, das aus
einem öffentlichen CI-Lauf mit sichtbarem Protokoll stammt, ist überprüfbarer als ein Binary,
das jemand auf seinem Laptop gebaut hat. `-trimpath` ist deshalb gesetzt, und die Go-Binaries
tragen ihren Commit über `-ldflags` im `--version`.

---

## Was in ein Release gehört — Prüfliste

- [ ] Archive je Plattform, benannt nach `<programm>-<version>-<os>-<arch>.tar.gz`
- [ ] `SHA256SUMS` daneben
- [ ] `QUELLEN.txt` mit Tag und Commits, für `asamon-rx` auch dem welle.io-Commit
- [ ] `LICENSE` in jedem Archiv
- [ ] Beispielkonfiguration und die passende Startdatei (systemd-Unit bzw. Windows-Hinweis)
- [ ] im Release-Text: die Laufzeitabhängigkeiten von `asamon-rx` und das `apt install` dafür
- [ ] Gegenprobe, dass `--version` den erwarteten Commit nennt

---

## Stand 30.08.2026: Stufe 0 bis 2 sind gebaut

Das Repo ist öffentlich (Stufe 0), `release.yml` baut beides (Stufen 1 und 2), und darüber
liegt ein Installationsskript. Was oben als Empfehlung steht, ist damit umgesetzt — mit drei
Entscheidungen, die erst beim Bauen fielen:

**Je Debian-Fassung ein eigenes Paket.** Mit Debian 13 sind die Bibliotheken auf 64-bit-Zeit
umgestellt und heißen anders — `libmpg123-0` wurde zu `libmpg123-0t64`. Ein Paket für bookworm
und trixie gäbe es nur um den Preis erfundener Abhängigkeiten (`libmpg123-0 | libmpg123-0t64`),
und die glibc-Untergrenze gilt ohnehin nur aufwärts. Deshalb `asamon_<version>_deb12_<arch>.deb`
und `…_deb13_….deb`; `install.sh` liest `VERSION_CODENAME` aus `/etc/os-release` und wählt.

**Die Abhängigkeiten werden nicht aufgeschrieben, sondern ausgelesen.** `dpkg-shlibdeps` nimmt
sie aus dem gebauten Binary. Was `libwelle` nach sich zieht, hängt an den Bauoptionen; eine
gepflegte Liste im Skript wäre schon beim ersten Umlegen einer Option falsch. Der erste Lauf
ergab: `libc6 (>= 2.38), libfaad2 (>= 2.7), libfftw3-single3 (>= 3.3.10), libgcc-s1,
libmp3lame0, libmpg123-0t64, librtlsdr0, libstdc++6 (>= 14)`.

**Das Paket startet den Dienst nicht.** Ohne `node-config.yaml` hätte er weder Standort noch
Server, und ein Dienst, der im Sekundentakt scheitert, ist schlimmer als einer, der wartet.
Gestartet wird er von `install.sh`, nachdem `asamon-node --check` die Konfiguration angenommen
hat — und bei einem Update übernimmt das `postinst` einen laufenden Dienst per `try-restart`,
startet aber keinen, der vorher stand.

### Was der Testlauf in WSL zutage förderte

Gebaut und installiert wurde das Paket unter Debian 13 (amd64), einschließlich Aktualisierung
und `purge`. Zwei Dinge fielen dabei auf, die im Entwurf nicht standen:

- **Die systemd-Unit zeigte auf `/usr/local/bin`.** Richtig für alle, die selbst bauen und
  `make install` benutzen — für ein Paket falsch, das nach `/usr/bin` gehört. Der Dienst
  scheiterte mit `status=203/EXEC`. `build-deb.sh` schreibt den Pfad jetzt um und prüft
  nach, dass es gelungen ist.
- **`asamon-node` fand `asamon-rx` nicht**, weil unter Unix bisher ausschließlich
  `/usr/local/bin/asamon-rx` galt. Jetzt wird `/usr/bin/asamon-rx` als zweiter Ort geprüft —
  aber nur, wenn am ersten nichts liegt und die Konfiguration den Pfad nicht selbst nennt.
  Damit läuft dieselbe Beispielkonfiguration in beiden Fällen.

### Was offen bleibt

- **armhf** (32-bit-Pi-OS): kein nativer Runner, über QEMU dauert ein welle.io-Bau über eine
  Stunde. Raspberry Pi OS ist seit 2022 64-bittig; wer 32 bit fährt, baut selbst.
- **Windows** (Stufe 3): unverändert offen, dafür bräuchte der Workflow MSYS2.
- **Der Workflow ist noch nie gelaufen.** Er ist gegen einen lokalen Nachbau geprüft — der
  Paketbau, die Installation, das Update und der Rückbau —, aber der erste echte Lauf wird
  Kleinigkeiten zeigen. Ein Tag `v0.1.0` ist die Probe.
