# NEXUS — implementation status

Status against the master engineering directive, phase by phase.
"Verified" means covered by the automated test suite or exercised end-to-end
against a real Linux host during development.

## Verification environment and its limits

* All development and testing ran in an Ubuntu 24.04 container. Docker, nginx,
  OpenSSH and rsyslog were real; PID 1 was **not** systemd.
* systemd service *management* (start/stop/restart/reload, dependency
  listing, journald queries) is implemented against `systemctl` and
  `journalctl`. It was **not** exercised against a live systemd in this
  environment; the agent correctly reported "systemd is not PID 1".
  Validate it on a real Debian or Ubuntu server first.
* The Windows executable (`NEXUS.exe`) cross-compiles cleanly but was **not
  run on Windows** here. The DPAPI, registry hardware detection, WebView2
  window and single-instance lock code paths have not been executed.
* Ollama was not reachable from the build environment. The Ollama client
  follows the documented REST API and was tested against a protocol-faithful
  scripted server. Validate it with a real local model.

## Phases

| Phase | Item | Status |
|---|---|---|
| 0 | Architecture, stack, modules, security model, protocol, Ollama integration | Done — `docs/ARCHITECTURE.md` |
| 1 | Application shell, navigation, design system, 4 themes, accent and density, configurable dashboard, server inventory, local storage | Done |
| 2 | Agent, secure enrollment, mTLS auth, heartbeats, telemetry, service inventory, system discovery | Done; verified (except live systemd) |
| 2 | Agent install, upgrade and removal | `install` / `uninstall [--purge]` / re-install upgrades the binary in place. Install needs systemd and was not run end-to-end (no systemd PID 1 here); the same steps were applied by hand and the least-privilege runtime was verified. |
| 3 | Processes, services, logs, storage, network, system monitoring, Docker | Done; verified (Docker against a real engine) |
| 4 | Ollama integration, model discovery, capability detection, tool calling, typed tool registry, infrastructure context, conversations, diagnostics | Done; verified against the scripted Ollama server |
| 5 | Monitoring engine, event detection, alert correlation, AI investigations, recommendations, approvals, verification | Done; verified |
| 6 | Docker Compose: discovery and status | Done |
| 6 | Docker Compose Studio: YAML import, editing, validation, backups, deploy, history | **Not started** |
| 6 | Reverse proxies (Nginx, Apache, Traefik, Caddy): config inspection, vhosts, routes, upstream health, routing diagrams | **Not started.** Proxies are detected as listening processes and shown in Topology; no configuration analysis yet. |
| 6 | Databases (PostgreSQL, MySQL, MariaDB, Redis) | **Not started.** Detected by process and port only. |
| 6 | Firewall management | Read-only inspection done; changes intentionally not implemented |
| 6 | Package management (APT) | Done. DNF, YUM, Zypper and Pacman are detected and return an explicit "not supported yet". |
| 6 | SSL monitoring | TLS endpoint certificate probe done; scheduled certificate inventory and expiry rules **not started** |
| 7 | SSH analysis, authentication events, posture, recommendations, audit log | Done. auditd, AIDE and Lynis are detected but not integrated. |
| 8 | Scheduler, Backup Center, restore, restore testing, maintenance windows, automation policies, recovery workflows | Policies and maintenance windows (enforced for autonomous actions) are done. **Backups, restore, scheduled maintenance jobs and update scheduling are not started.** The UI says so explicitly and never shows backup data. |
| 9 | Server groups, tags, environments, fleet monitoring, cross-server views, centralized alerts | Done |
| 9 | Remote agent deployment over SSH | **Not started**; agents are installed manually |
| 10 | Installer (MSI/MSIX), code signing, auto-update, release packaging | **Not started** |
| 10 | Security testing | Unit and integration tests for authentication, policy and validation. No external penetration test. |

## Non-negotiable rules — how they are enforced

| Rule | Mechanism |
|---|---|
| No fabricated telemetry | All values come from agent reads; missing sources are reported as unavailable (for example "no temperature sensors", "no backup center"). Charts break lines at gaps instead of interpolating. |
| No unrestricted AI commands | No shell tool exists. 41 typed tools with schema validation; unknown arguments are rejected; every call passes `ai.Decide`. |
| Credentials never exposed to the model | Tools receive server IDs only. Container environment *values* are never collected, and helper file reads refuse key and credential files. |
| No plaintext passwords | Secrets are sealed with DPAPI on Windows (AES-256-GCM key file elsewhere). |
| No destructive operations without authorization | High risk always needs approval and cannot be configured otherwise; reboots also need a typed hostname. No file-deletion or data-removal tool exists. |
| No automatic SSH or firewall changes | No tools exist for them. |
| No reboot without policy | `reboot_server` is denied unless the server's policy enables reboot requests, and it then still needs approval. |
| No claim of resolution without verification | Action endpoints re-read state, and tools return an explicit `verification` verdict that the model is instructed to report. |
| Service health is more than a running process | The health assessment combines unit state, restart count, result, error-level logs and listening sockets; HTTP and TCP probes are available. |
| Monitoring must not overwhelm servers | 5 s `/proc` sampling, filesystem stats every 15 s, inventory refreshes staggered one per poll, package and posture checks every 6 h, systemd unit resource limits. |
| Errors are not silently discarded | Executions, notification deliveries and helper failures are stored and shown in the UI. |
| No unauthenticated privileged execution | The agent requires a client certificate at the TLS handshake and a pinned fingerprint; the console requires a session token and a loopback Host. |
| AI memory never replaces live data | Memory is injected as dated historical context, and the system prompt requires a live refresh before decisions. |
| Policies never change silently | Every policy and setting change is written to the hash-chained audit log with before and after values. |
