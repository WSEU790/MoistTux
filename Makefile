# NEXUS build. Requires Go (see go.mod), Node 20+ and, for the installer, NSIS.
# No CGO: every target cross-compiles from Linux, macOS or Windows.
#
# Local builds are versioned <VERSION>.0-dev and never update themselves.
# CI sets VERSION=<VERSION>.<run number> and signs the release
# (see .github/workflows/release.yml).
VERSION ?= $(shell cat VERSION).0-dev
PUBKEY := $(shell cat release/update-public-key.txt)
MOD := github.com/WSEU790/MoistTux
LDFLAGS := -s -w -X $(MOD)/internal/agent.Version=$(VERSION) -X $(MOD)/internal/core/app.Version=$(VERSION) -X $(MOD)/internal/core/update.PublicKey=$(PUBKEY)
NUMVERSION := $(shell echo "$(VERSION)" | awk -F'[.-]' '{printf "%d.%d.%d.0", $$1, $$2, $$3}')
DIST := dist

.PHONY: all ui agent embed-agents core windows installer release-manifest test test-race clean dev

all: ui agent core windows installer

ui:
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web run build

agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/nexus-agent ./cmd/nexus-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-arm64/nexus-agent ./cmd/nexus-agent

# Embed the Linux agents (gzip) so NEXUS can install them over SSH.
embed-agents: agent
	mkdir -p internal/core/agentbin/bin/linux-amd64 internal/core/agentbin/bin/linux-arm64
	gzip -9 -c $(DIST)/linux-amd64/nexus-agent > internal/core/agentbin/bin/linux-amd64/nexus-agent.gz
	gzip -9 -c $(DIST)/linux-arm64/nexus-agent > internal/core/agentbin/bin/linux-arm64/nexus-agent.gz

core: embed-agents
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/nexus-core ./cmd/nexus-core

# The Windows desktop app: core + UI in one executable with a WebView2 window,
# plus its icon, version information and application manifest.
windows: embed-agents
	go run ./cmd/nexus-winres -version $(VERSION) -out cmd/nexus/rsrc_windows_amd64.syso
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(DIST)/windows-amd64/NEXUS.exe ./cmd/nexus
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/nexus-core.exe ./cmd/nexus-core

# Per-user Windows installer (NSIS): dist/NEXUS-Setup-<version>.exe
installer: windows
	makensis -V2 -DVERSION=$(VERSION) -DNUMVERSION=$(NUMVERSION) -DSRCDIR=../$(DIST)/windows-amd64 -DOUTFILE=../$(DIST)/NEXUS-Setup-$(VERSION).exe installer/nexus.nsi

# Signed update manifest; requires the NEXUS_UPDATE_SIGNING_KEY secret.
release-manifest:
	go run ./cmd/nexus-release manifest -version $(VERSION) -commit "$$(git rev-parse HEAD)" -notes-file $(DIST)/notes.md -installer $(DIST)/NEXUS-Setup-$(VERSION).exe -pub release/update-public-key.txt -out $(DIST)

test:
	go vet ./...
	go test ./...
	npm --prefix web run typecheck

test-race:
	go test -race ./...

# Run the core against a UI dev server (vite on :5173 proxies /api to :7780).
dev:
	go run ./cmd/nexus-core --listen 127.0.0.1:7780 --dev-origin http://localhost:5173

clean:
	rm -rf $(DIST)
	find internal/core/webui/dist -mindepth 1 ! -name README.txt -delete
	rm -rf internal/core/agentbin/bin/linux-amd64 internal/core/agentbin/bin/linux-arm64
	rm -f cmd/nexus/rsrc_windows_amd64.syso
