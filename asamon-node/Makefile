# SPDX-License-Identifier: GPL-3.0-or-later
#
# asamon-node — bauen, prüfen, ausliefern.
#
# CGO_ENABLED=0 ist verbindlich: Der Knoten enthält keinen SDR-Code, kein
# librtlsdr, kein cgo. Damit sind die Binaries statisch und laufen auf jedem
# Pi, ohne dass die passende libc mitgeliefert werden muss.
#
# Gebraucht wird Go >= 1.27 (siehe go.mod und TODO.md Abschnitt 22). Mit der
# Vorgabe GOTOOLCHAIN=auto holt sich die vorhandene Go-Installation die passende
# Toolchain selbst; wer das abgeschaltet hat, braucht 1.27 im Pfad.

VERSION ?= 0.1.0
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unbekannt)
PKG     := github.com/josch0/asa-monitor/asamon-node
LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT)

GO      ?= go
BUILD   := $(GO) build -trimpath -ldflags "$(LDFLAGS)"
BIN     := build
DIST    := dist

export CGO_ENABLED = 0

.PHONY: all build test vet fmt check race fuzz cross dist clean streams golden install

all: check build

## build — beide Programme für die eigene Plattform
build:
	@mkdir -p $(BIN)
	$(BUILD) -o $(BIN)/ ./cmd/asamon-node
	$(BUILD) -o $(BIN)/ ./cmd/fake-rx

## check — alles, was vor einem Commit laufen sollte
check: fmt vet test

## test — die vollständige Prüfung, ohne SDR-Stick und ohne Netz
test:
	$(GO) test ./... -timeout 600s

## vet — go vet ist die Krücke, die Gos fehlende Aufzählungsprüfung ersetzt
vet:
	$(GO) vet ./...

## fmt — meldet ungeformte Dateien, statt sie stillschweigend zu ändern
fmt:
	@ungeformt=$$(gofmt -l cmd internal); \
	if [ -n "$$ungeformt" ]; then \
		echo "nicht mit gofmt geformt:"; echo "$$ungeformt"; exit 1; \
	fi

## race — Nebenläufigkeitsprüfung. Braucht cgo und damit einen C-Compiler;
## unter Windows ohne gcc steht sie nicht zur Verfügung.
race:
	CGO_ENABLED=1 $(GO) test -race ./... -timeout 900s

## fuzz — die beiden Parser, die fremde Bytes sehen
fuzz:
	$(GO) test ./internal/record -run FuzzParseLine -fuzz FuzzParseLine -fuzztime 60s
	$(GO) test ./internal/loc -run FuzzDecodeLocationCodes -fuzz FuzzDecodeLocationCodes -fuzztime 60s

## Zielplattformen. asamon-node ist reines Go ohne cgo und lässt sich deshalb
## für alle davon von einem einzigen Rechner aus bauen — asamon-rx nicht, das
## braucht je Plattform eine eigene Bauumgebung (siehe docs/ausrollen.md).
ZIELE := linux/amd64 linux/arm64 linux/arm windows/amd64

## cross — statische Binaries für alle Zielplattformen
cross:
	@mkdir -p $(BIN)
	@for ziel in $(ZIELE); do \
		os=$${ziel%%/*}; arch=$${ziel##*/}; \
		name=asamon-node-$$os-$$arch; \
		[ "$$os" = windows ] && name=$$name.exe; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch GOARM=7 $(BUILD) -o $(BIN)/$$name ./cmd/asamon-node || exit 1; \
	done
	@echo; ls -l $(BIN)/asamon-node-*

## dist — fertige Archive, so wie sie an ein GitHub-Release gehen
##
## Je Plattform ein Archiv mit Binary, Beispielkonfiguration, README und Lizenz;
## unter Linux zusätzlich die systemd-Unit. Dazu SHA256SUMS, damit sich ein
## heruntergeladenes Archiv prüfen lässt.
##
## Bewusst überall tar.gz, auch für Windows: Das spart eine Abhängigkeit auf
## `zip`, und Windows entpackt .tar.gz seit Jahren mit Bordmitteln (tar.exe).
dist: cross
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@for ziel in $(ZIELE); do \
		os=$${ziel%%/*}; arch=$${ziel##*/}; \
		paket=asamon-node-$(VERSION)-$$os-$$arch; \
		verz=$(DIST)/$$paket; \
		mkdir -p $$verz || exit 1; \
		if [ "$$os" = windows ]; then \
			cp $(BIN)/asamon-node-$$os-$$arch.exe $$verz/asamon-node.exe || exit 1; \
		else \
			cp $(BIN)/asamon-node-$$os-$$arch $$verz/asamon-node || exit 1; \
			cp contrib/asamon-node.service $$verz/ || exit 1; \
		fi; \
		cp contrib/node-config.example.yaml README.md LICENSE $$verz/ || exit 1; \
		cp docs/ausrollen.md $$verz/AUSROLLEN.md || exit 1; \
		( cd $(DIST) && tar czf $$paket.tar.gz $$paket ) || exit 1; \
		rm -rf $$verz; \
	done
	@cd $(DIST) && sha256sum *.tar.gz > SHA256SUMS
	@echo; ls -l $(DIST)

## streams — die synthetischen Testströme neu erzeugen
##
## Echten ASA-Verkehr hat niemand aufgezeichnet; die Ströme werden gebaut.
## Sobald es echte Mitschnitte gibt, treten sie daneben, nicht an ihre Stelle.
streams: build
	@for s in $$(./$(BIN)/fake-rx --list | awk '{print $$1}'); do \
		echo "  $$s"; ./$(BIN)/fake-rx --scenario $$s > testdata/streams/$$s.ndjson; \
	done

## golden — die erwarteten Datensätze neu schreiben
##
## Erst ansehen, was sich ändert: Ein Golden-File, das man ohne Blick auf den
## Diff neu schreibt, prüft nichts mehr.
golden:
	$(GO) test ./internal/chanstate -update

## install — nach /usr/local/bin, samt Beispielkonfiguration
install: build
	install -m 0755 $(BIN)/asamon-node /usr/local/bin/asamon-node
	install -d /etc/asamon
	@test -f /etc/asamon/node-config.yaml || \
		install -m 0644 contrib/node-config.example.yaml /etc/asamon/node-config.yaml
	install -m 0644 contrib/asamon-node.service /etc/systemd/system/asamon-node.service
	@echo
	@echo "Jetzt /etc/asamon/node-config.yaml anpassen, dann:"
	@echo "  asamon-node --check --config /etc/asamon/node-config.yaml"
	@echo "  systemctl daemon-reload && systemctl enable --now asamon-node"

clean:
	rm -rf $(BIN) $(DIST)
