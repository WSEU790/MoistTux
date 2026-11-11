# NEXUS — Architecture

This document is the Phase 0 deliverable: the technical discovery that was performed,
the decisions that follow from it, and the module boundaries every later phase builds on.

## 1. Discovery findings (2026-11-11)

| Item | Finding | Consequence |
|---|---|---|
| Repository | `WSEU790/MoistTux` contained one commit with a one-line README. No existing code, framework, backend, database, auth or Linux connectivity to preserve. | Clean modular architecture, no migration constraints. |
| Build host | Ubuntu 24.04 x86_64 container. Go 1.24, Node 22, Rust 1.97, Docker 29, systemd 255 binaries (PID 1 is not systemd). No .NET SDK, no Windows host, no Wine. | The Windows desktop must be buildable from Linux without a Windows SDK. Native WPF/WinUI cannot be built or tested here. |
| Ollama | Not installed. `ollama.com` and `registry.ollama.ai` are blocked by the build environment's network policy. | Ollama integration is implemented strictly against Ollama's documented REST API. Automated tests use a protocol-faithful fake Ollama server (tests only; never in production paths). Live-model validation must happen on the target Windows machine. |
| Linux connectivity | None existed. | NEXUS ships its own authenticated agent (§4). SSH is used only for initial agent deployment. |
| Credentials | None existed. | Secret store with Windows DPAPI (§5). |

## 2. Technology stack

| Layer | Choice | Why |
|---|---|---|
| Linux agent | Go, single static binary (`CGO_ENABLED=0`), amd64 + arm64 | Small footprint (~10 MB RSS), no runtime dependencies, reads `/proc` and `/sys` directly instead of spawning shells for telemetry. |
| AI core + backend | Go, in-process with the desktop | One language for agent and core; shared protocol types; trivially cross-compiles to Windows. |
| Persistence | SQLite via `modernc.org/sqlite` (pure Go) with versioned migrations, WAL mode | Embedded, zero-admin, no CGO so the Windows build stays reproducible from Linux. |
| Desktop UI | React 18 + TypeScript + Vite, custom design system, no UI kit | Full control over the visual identity; charts and topology are hand-built (canvas/SVG) so nothing looks like a template. |
| Windows shell | `nexus.exe`: Go + Microsoft Edge WebView2 (`github.com/jchv/go-webview2`, pure Go) | Native window with the system Chromium runtime that ships with Windows 10/11; core runs in the same process. |
| Dev/headless | `nexus-core`: same core, serves the UI on `127.0.0.1` | Lets the full product run and be tested on Linux/macOS; also a headless mode. |

## 3. Subsystems and modules

```
 ┌──────────────────────────── Windows 10/11 ─────────────────────────────┐
 │  nexus.exe                                                             │
 │  ┌──────────────┐  HTTP+SSE (loopback, session token)  ┌────────────┐  │
 │  │ WebView2 UI  │ ◄──────────────────────────────────► │ NEXUS Core │  │
 │  │ (React/TS)   │                                      │  (Go)      │  │
 │  └──────────────┘                                      └─────┬──────┘  │
 │                                    Ollama REST (localhost)   │         │
 │                          ┌──────────────────┐ ◄──────────────┤         │
 │                          │ Ollama (local)   │                │         │
 │                          └──────────────────┘                │         │
 └──────────────────────────────────────────────────────────────┼─────────┘
                       HTTPS, TLS 1.3, mutual cert pinning      │
            ┌───────────────────────┬───────────────────────────┤
     ┌──────▼──────┐         ┌──────▼──────┐             ┌──────▼──────┐
     │ nexus-agent │         │ nexus-agent │     ...     │ nexus-agent │
     │ (Debian)    │         │ (Ubuntu)    │             │ (other)     │
     └─────────────┘         └─────────────┘             └─────────────┘
```

### Go packages

| Package | Responsibility |
|---|---|
| `internal/pki` | Key/cert generation, SHA-256 fingerprints, enrollment codes. |
| `internal/protocol` | Wire types shared by agent and core (versioned `v1`). |
| `internal/agent` | Agent HTTPS server, auth middleware, enrollment, sampler, collectors, distro adapters, privileged helper, install/uninstall. |
| `internal/agent/collect` | Pure readers of `/proc`, `/sys`, `/etc` — unit-testable against fixture trees. |
| `internal/core/store` | SQLite schema, migrations, repositories, retention. |
| `internal/core/secrets` | DPAPI-backed (Windows) / key-file AES-GCM (other OS) secret store. |
| `internal/core/agentclient` | Pinned mTLS client for the agent API. |
| `internal/core/fleet` | Server inventory, polling workers, connection state, health scoring. |
| `internal/core/monitor` | Deterministic rules, alert lifecycle, dedup, cooldown, incident grouping. |
| `internal/core/ollama` | Ollama REST client: version, tags, show/capabilities, chat (streaming + tools), pull, ps, delete. |
| `internal/core/ai` | Model roles and selection, hardware profile, tool registry, policy engine, approvals, orchestrator loop, operational memory. |
| `internal/core/api` | Loopback HTTP API, SSE event bus, UI asset serving. |

## 4. Agent protocol (v1)

* Transport: HTTPS, TLS 1.3 only, default port `7443`.
* **Identity, both directions, by certificate pinning** (no shared CA to compromise):
  * The agent generates an ECDSA P-256 key and self-signed certificate at install time.
  * The core generates its own client identity on first run.
* **Enrollment** (one time, per core):
  1. `nexus-agent install` (or `nexus-agent enroll-code`) prints an enrollment code
     `NX1-<token>-<agent-fingerprint-prefix>`; the token is 128 random bits, stored only as a SHA-256 hash, single use, expires after 24 h.
  2. The operator enters host, port and code in NEXUS. The core connects and **refuses to proceed unless the agent's certificate fingerprint matches the code** (no trust-on-first-use window).
  3. The core sends its client certificate and the token to `POST /v1/enroll`. The agent verifies the token in constant time, burns it, and pins the core's certificate fingerprint.
  4. All other endpoints require a client certificate whose fingerprint is pinned. Failed enrollment attempts are rate limited.
* **Rotation**: `POST /v1/credentials/rotate-client` (core presents a new cert over the existing authenticated channel) and `POST /v1/credentials/rotate-agent` (agent generates a new cert and returns the new fingerprint; the core re-pins). **Revocation**: `POST /v1/unenroll` from the core, or `nexus-agent revoke --all` on the server.
* **Heartbeat**: `GET /v1/health` returns agent version, uptime, sampler status and boot id; the core marks a server offline after 3 missed polls.
* Endpoints are typed (no generic shell). See `docs/PROTOCOL.md`.

## 5. Security model

* **No exposed agent port when it is not needed.** When the agent port is not reachable from the NEXUS PC (firewall, cloud security group, NAT), NEXUS reaches the agent through SSH: the agent listens on 127.0.0.1 only, and NEXUS signs in with a per-server key that `authorized_keys` restricts with `restrict` and a forced `nexus-agent bridge <port>` command, so the key cannot run commands or forward ports. The SSH host key is pinned, the key is sealed with DPAPI, and mutual TLS with pinned certificates still runs end to end inside the tunnel. Because adding a key changes SSH access, the operator chooses this in the install form (*Automatic* is the default, *Direct only* turns it off).
* **Least privilege on Linux.** The agent runs as the system user `nexus` with read access via the `systemd-journal` and `adm` groups. Operations that need root go through the dedicated helper `/usr/local/lib/nexus/nexus-helper <op>` invoked with `sudo -n`; the sudoers entry allows **only** that root-owned binary (no argument wildcards, so it works with both the classic sudo and sudo-rs), and the helper re-validates every argument against a fixed allowlist (`systemctl` verbs on a validated unit name, `apt-get` with validated package names, read-only firewall listings, `sshd -T`, …). There is no "run this string" operation anywhere in the system.
* **Docker access** requires the `docker` group, which is root-equivalent; it is opt-in at install (`--docker`) and documented as such.
* **Desktop API** binds to `127.0.0.1` on a random port, requires a per-launch 256-bit session token, and rejects non-loopback `Host`/`Origin` headers.
* **Secrets** (agent client key, SSH credentials used for deployment, database passwords, webhook URLs) are encrypted with DPAPI (`CurrentUser` scope) on Windows. They are never placed in AI prompts or logs; tools receive only the IDs of credentials.
* **AI execution policy** (§6) sits between the model and every tool. The model cannot reach the agent directly.
* **Audit**: every tool invocation, approval decision, policy change and enrollment is written to an append-only audit table.

## 6. AI core

* **Ollama first-class**: auto-detect on `http://127.0.0.1:11434`, version check, model discovery (`/api/tags`), capability detection via `/api/show` (`capabilities` field; template inspection as fallback), streaming chat, pull with progress, delete, loaded-model monitoring (`/api/ps`).
* **Models without tool calling** are still usable: the orchestrator switches to a JSON-schema constrained protocol (`format` parameter) in which the model emits `{"action":"tool", ...}` or `{"action":"answer", ...}`.
* **Roles**: general reasoning, command generation, log analysis, security analysis, lightweight classification, deep diagnostics. Each role has an explicit assignment or an automatic choice scored on tool support, parameter size and the host's RAM/VRAM.
* **Tools**: every tool declares a JSON schema, validation, risk class (`read`, `low`, `medium`, `high`), timeout and audit behaviour, and is implemented against a typed agent endpoint.
* **Policy**: operational mode (Assistant, Analyst, Administrator, Autonomous) × per-server risk matrix (`allow` / `approve` / `deny`). Assistant and Analyst modes are read-only regardless of the matrix. High-risk operations always require explicit approval with an impact preview; no policy can make them silent.
* **Approvals**: a gated tool call is persisted as a pending approval with an impact preview gathered by read-only tools; the conversation pauses and resumes with the real result (or the denial) once the operator decides.
* **Memory**: structured infrastructure facts (role, important services, constraints, maintenance windows, incidents) are stored separately from chat history, are editable and deletable, are stamped with when/how they were learned, and are injected into prompts as *historical context* — the system prompt requires a live refresh before any operational decision.

## 7. Monitoring pipeline

`agent sampler (5 s, /proc)` → `core poller (10 s, fetches samples since last sequence)` → `telemetry store` → `rule engine (thresholds with sustain windows)` → `alert lifecycle (dedup by fingerprint, cooldown, auto-resolve)` → `incident grouping (per server, time-correlated)` → optional `AI investigation (read-only, rate limited)` → `notifications`.

Telemetry retention is separate from configuration and audit: raw samples 48 h, 5-minute rollups 30 days, then deleted.

## 8. Distribution compatibility

Distro-specific behaviour sits behind adapters: package manager (`apt` implemented; `dnf`, `yum`, `zypper`, `pacman` return an explicit *unsupported* error until implemented), init system (`systemd`), firewall (`ufw`, `nftables`, `iptables`, read-only listings), log source (journald, falling back to `/var/log` files).
