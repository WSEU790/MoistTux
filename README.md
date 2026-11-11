# NEXUS — AI Infrastructure Command Center

NEXUS is a Windows desktop command center for Linux servers. A lightweight,
authenticated agent on each server streams real telemetry and exposes a typed,
policy-controlled set of operations. A local AI (Ollama) investigates problems
with those operations, explains what it found, and carries out approved
maintenance.

```
 Windows 10/11                                         Linux servers
┌───────────────────────────────────────────┐        ┌──────────────────────┐
│ NEXUS.exe                                 │  mTLS  │ nexus-agent          │
│  WebView2 UI ── loopback API ── AI core ──┼───────►│  /proc, /sys sampler │
│                     │            │        │ pinned │  systemd, journald   │
│                  SQLite      Ollama       │  certs │  Docker, APT, sshd   │
│                (DPAPI keys)  (local)      │        │  sudo-only helper    │
└───────────────────────────────────────────┘        └──────────────────────┘
```

## What works today

| Area | Status |
|---|---|
| **Linux agent** | Single static binary (amd64/arm64, ~8 MB, ~15 MB RSS). Samples CPU (per core, iowait, steal), memory, swap, load, filesystems and inodes, disk I/O, network rates and errors, process counts, temperatures and uptime every 5 s from `/proc` and `/sys`, buffering 20 minutes. Discovers OS, kernel, hardware, block devices, interfaces, routes, DNS and listening sockets with their owning processes. |
| **Security model** | TLS 1.3 with certificate pinning in both directions; single-use enrollment codes bound to the agent fingerprint; rate-limited enrollment; certificate rotation and revocation. The agent runs as an unprivileged `nexus` user; root operations go only through `nexus-agent helper`, which sudoers allows and which re-validates every argument. |
| **Server operations** | Processes (live CPU%, details, sockets); systemd services (state, health assessment, dependencies, logs, start/stop/restart/reload with verification); logs (journald or allowlisted `/var/log` files); Docker (containers, health, stats, logs, actions, images, volumes, networks, Compose projects); storage (filesystems, block devices, directory analysis, large-file report); network (interfaces, routes, DNS, sockets with bind scope, ufw/nftables/iptables listing); APT (installed, simulated updates with security classification, refresh, upgrade, history). |
| **Security center** | Effective `sshd -T` configuration with findings, SSH/sudo authentication activity, local accounts, sensitive-file permissions, UID 0 accounts, firewall state, pending security updates and reboot-required status. |
| **Monitoring** | Deterministic rules with sustain windows for CPU, memory, swap, load, disk, inodes and temperature; rules for failed units, unhealthy, exited and restarting containers, security updates, reboot-required, security findings, unexpected reboots and offline agents. De-duplication, cooldowns, storm suppression, and incidents that group related alerts. |
| **AI core** | Ollama detection, model discovery, capability detection (`/api/show`), tool calling with streaming, a JSON-schema fallback for models without tools, model roles with automatic selection, hardware-aware recommendations, explicit model downloads, 41 typed tools (29 read-only, 3 low, 8 medium and 1 high risk), four operational modes, a per-server policy engine, approvals with impact previews, verification after every change, persistent operational memory, and rate-limited automatic incident investigations. |
| **Desktop UI** | Command Center (customisable layout), Fleet, server workspace (overview with historical charts and nine operational tabs), AI Command Center, Incidents and Alerts, Approvals, interactive Topology, Policies editor, Memory, Activity and hash-chained Audit, and Settings. Four themes (Graphite, Midnight, Tactical, Light) plus custom accent and density. |
| **Persistence** | SQLite (WAL) with migrations. Raw telemetry is kept 48 h and 5-minute rollups 30 days, separately from configuration and audit data. |
| **Notifications** | In-app and native desktop notifications, plus opt-in Discord, Telegram and SMTP with secrets sealed by DPAPI. |

| **SSH deployment** | Fleet → Add server → Install over SSH. NEXUS confirms the server's host key, signs in with a password or private key, uploads its built-in agent (SHA-256 verified), installs it with sudo, checks the firewall, and enrolls it. Agents can also be upgraded or repaired over SSH. The SSH login is not stored unless you tick *Remember this SSH login*; it is then encrypted with your Windows account (DPAPI) and can be forgotten at any time. Day-to-day monitoring never uses SSH. |

See [`docs/STATUS.md`](docs/STATUS.md) for phase-by-phase status against the
product directive, including what is **not** built yet (Backup Center, Compose
editing, reverse-proxy and database integrations, installer and auto-update).

## Quick start

### 1. Install NEXUS on Windows

Download `NEXUS-Setup-<version>.exe` from the
[latest release](https://github.com/WSEU790/MoistTux/releases/latest) and run it.
It installs for your Windows account only, with no administrator rights
needed. It adds Start-menu and desktop shortcuts and an *Apps & features*
entry, and checks for the Microsoft Edge WebView2 Runtime (included with
Windows 11 and current Windows 10). The installer is not code-signed yet, so
SmartScreen asks you to confirm: choose *More info → Run anyway*.

NEXUS then **updates itself**. It checks GitHub at start-up and every 6 hours,
downloads new releases in the background, and installs them the next time it
starts, or right away from *Update ready · Restart*. See
[Releases and updates](#releases-and-updates).

### Building from source

```bash
make ui          # React/TypeScript UI → embedded into the binaries
make all         # linux agents (also embedded for SSH deployment), nexus-core,
                 # NEXUS.exe and the NSIS installer → ./dist
```

Requires Go 1.26+, Node 20+ and NSIS (`apt install nsis`) for the installer.
Every target cross-compiles without CGO, so the Windows executable and
installer can be built on Linux or macOS. Local builds are versioned
`<VERSION>.0-dev` and never update themselves.

### 2. Add a Linux server

**Over SSH (recommended):** in NEXUS go to Fleet → Add server → Install over
SSH. Enter the address, click *Check server identity*, confirm the host-key
fingerprint, then enter an SSH user (root, or a user with sudo) and a password
or private key. NEXUS does the rest.

**Manually:**

```bash
scp dist/linux-amd64/nexus-agent admin@server:/tmp/
ssh admin@server 'sudo /tmp/nexus-agent install --listen :7443'
```

`install` creates the `nexus` service account, a sudoers rule that allows only
the validated helper, and a hardened systemd unit. It then prints a single-use
enrollment code such as `NX1-…`. Add `--docker` to grant Docker access (the
`docker` group is equivalent to root). Restrict port 7443 to your workstation
in the server firewall.

### 3. Headless core (Linux/macOS)

`./dist/nexus-core`, then open the printed URL. Self-update is available in the
Windows app only.

### 4. Local AI

Install [Ollama](https://ollama.com) and run `ollama serve`. NEXUS detects it on
`127.0.0.1:11434`. Settings → AI models shows hardware-sized recommendations,
for example `qwen2.5:7b` on a GPU with 8 GB. Models download only when you
click Download.

## Releases and updates

Every push to `main` or `nexus-foundation` runs
[`.github/workflows/release.yml`](.github/workflows/release.yml):

1. **test-linux** runs `go vet`, the Go tests (including the end-to-end agent
   and core test) and the UI type-check.
2. **build** creates the UI, the agents, `NEXUS.exe` (with icon, version
   information and a per-monitor DPI manifest) and `NEXUS-Setup-<version>.exe`.
   The version is `<VERSION file>.<run number>`.
3. **test-windows** runs on a real Windows runner. It runs the unit tests
   (DPAPI, policy, updater, saved logins) and a smoke test of the core
   (identity sealed with DPAPI, updater enabled). It then installs silently,
   updates over a running NEXUS with relaunch, and uninstalls.
4. **release** signs `nexus-update.json` with the `NEXUS_UPDATE_SIGNING_KEY`
   secret and publishes GitHub Release `v<version>`: the installer, the
   signed manifest and the Linux agents.

Installed copies trust an update only if the manifest's ed25519 signature
verifies against the public key built into them
(`release/update-public-key.txt`), the version is newer and matches the tag,
and the installer matches the signed size and SHA-256. Keep the private key
safe: losing it means existing installations cannot verify newer releases, so
they would have to be reinstalled by hand.

## Testing

```bash
make test        # go vet, unit and integration tests, UI type-check
make test-race   # the same suite under the race detector
```

The integration test (`internal/integration`) runs the real agent and core
in-process on the test host and checks:

* enrollment, including rejection of a mismatched fingerprint and of a reused code;
* rejection of clients without certificates and of unenrolled certificates;
* the API's token check and its Host-header (DNS-rebinding) check;
* real telemetry ingestion and the Docker inventory;
* AI tool calling against live data;
* blocking in read-only modes, argument validation, and denial of high-risk operations;
* the full approval workflow, with a real `apt-get update` executed after approval and then verified;
* agent disconnect and recovery alerts;
* crash recovery after a core restart;
* certificate rotation and unenrollment.

`TestSSHDeployment` runs only when `NEXUS_SSH_TEST_HOST`, `_USER` and
`_PASSWORD` point at a disposable server. It installs the agent over SSH,
rejects a forged host key, restarts a systemd unit through the helper, and
uninstalls the agent again.

The only simulated component is Ollama, replaced by a scripted server that
speaks the documented `/api/chat` protocol. It exists only in the test suite.

## Repository layout

```
cmd/nexus-agent     Linux agent (run, install, enroll-code, revoke, helper)
cmd/nexus-core      Headless core + console (dev, non-Windows)
cmd/nexus           Windows desktop app (WebView2)
internal/agent      Agent server, collectors, privileged helper, installers
internal/core       store · secrets · identity · agentclient · fleet · monitor ·
                    notify · ollama · ai (tools, policy, engine) · api · app
internal/protocol   Agent ↔ core wire types (v1)
internal/pki        Keys, fingerprints, enrollment codes
web/                Desktop UI (React + TypeScript + Vite)
docs/               Architecture, security model, protocol, status
```
