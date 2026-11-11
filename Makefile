# NEXUS build. Requires Go (see go.mod) and Node 20+. No CGO; every target
# cross-compiles from Linux, macOS or Windows.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/WSEU790/MoistTux/internal/agent.Version=$(VERSION) -X github.com/WSEU790/MoistTux/internal/core/app.Version=$(VERSION)
DIST := dist

.PHONY: all ui agent embed-agents core windows test test-race clean dev

all: ui agent core windows

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

# The Windows desktop app: core + UI in one executable with a WebView2 window.
windows: embed-agents
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(DIST)/windows-amd64/NEXUS.exe ./cmd/nexus
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/nexus-core.exe ./cmd/nexus-core

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
	rm -rf internal/core/agentbin/bin/linux-*
