# NEXUS developer handbook

Everything you need to work on NEXUS: how the pieces fit, how to build, run and
test them, where to add things, and the rules every change must keep. Read
this first, then [`ARCHITECTURE.md`](ARCHITECTURE.md) (design and security
model), [`PROTOCOL.md`](PROTOCOL.md) (agent wire protocol) and
[`STATUS.md`](STATUS.md) (what is built, verified and still missing).

---

## 1. The product in one minute

NEXUS is a Windows desktop app that monitors and operates Linux servers.

* **`nexus-agent`** runs on each Linux server: one static Go binary that reads
  `/proc` and `/sys`, talks to systemd, journald, Docker and APT, and serves
  a typed HTTPS API (mutual TLS, pinned certificates). It runs as the
  unprivileged `nexus` user. Anything that needs root goes through a separate,
  allowlisted helper via `sudo`.
* **The core** runs on the Windows PC: it polls the agents, stores telemetry
  in SQLite, runs the deterministic monitoring rules, hosts the AI engine
  (local Ollama), and serves the UI and a loopback-only JSON API.
* **`NEXUS.exe`** is the core plus the React UI in a WebView2 window, built
  as one executable. `nexus-core` is the same core without a window, used for
  development on Linux or macOS and for tests.
* **The AI** never runs shell commands. It can only call 41 typed tools, and
  every call passes a policy engine; changes need approval, show an impact
  preview first, and are verified afterwards.

```
NEXUS.exe (Windows)                                      Linux server
┌────────────────────────────────────────────┐          ┌───────────────────────────┐
│ WebView2 ── React UI (web/)                │          │ nexus-agent run  (user nexus)
│              │ same-origin, session cookie │  mTLS    │   collectors /proc /sys   │
│ api ─ loopback 127.0.0.1, token, Host check├─────────►│   systemd journald docker │
│  │                                         │  pinned  │   apt sshd ufw nft        │
│ app ─ fleet poller ─ agentclient           │  certs   │        │ sudo -n          │
│  │    monitor (rules, incidents)           │          │ nexus-helper <op> (root,  │
│  │    ai engine ─ policy ─ tools ─ ollama  │          │   re-validates arguments) │
│ store (SQLite) secrets (DPAPI) update      │          └───────────────────────────┘
└────────────────────────────────────────────┘
```

## 2. Non-negotiable engineering rules

These come from the project directive. Code review should reject a change
that breaks any of them.

1. **No fake data.** Never fabricate telemetry, fill gaps with invented
   numbers or ship mock-ups. If something is unavailable, say so in the UI
   ("unavailable: …", with the reason).
2. **No unrestricted execution.** There is no "run this command" path
   anywhere, for the AI or for people. Every operation is a typed tool with
   validated arguments, and root operations go through the helper allowlist,
   which validates again inside the root process.
3. **Secrets are never plaintext at rest and never sent to the model.**
   Secrets are sealed with DPAPI on Windows (an AES-GCM key file elsewhere).
   SSH passwords are kept only if the user opts in. Tool results must not
   include credentials.
4. **No destructive or risky action without authorization.** That covers
   reboots, upgrades, stops, and firewall and SSH changes. Changes go through
   approvals with an impact preview. High-risk operations need the operator
   to type the server name or hostname. Never modify SSH or firewall
   configuration automatically.
5. **Verify, don't assume.** After a change, re-check the real state and
   report it (`verifyService`, `verifyContainer`, update re-checks). Never
   call a service healthy just because its process exists, or call an issue
   resolved without re-checking it.
6. **Never discard errors silently.** Surface them to the user, the audit log
   or the AI as real tool results.
7. **Stay light on servers.** The agent is limited to MemoryMax 512M and
   CPUWeight 50 and samples every 5 s. Expensive scans (du, large files) are
   on demand and bounded.
8. **Every UI control works.** No placeholder buttons; if a feature is not
   available, disable the control and say why.
9. **The AI stays optional and local.** NEXUS must work without Ollama, and
   no external AI service may ever be required.
10. **Live data beats memory.** AI memory is context only; tools always fetch
    the current state.

## 3. Repository layout

```
cmd/
  nexus/            Windows desktop app: core + WebView2 window (main_windows.go);
                    main_other.go explains it is Windows-only
  nexus-core/       Headless core + console for development and tests
  nexus-agent/      Linux agent CLI: run, install, uninstall, enroll-code,
                    clients, revoke, version, helper (and nexus-helper mode)
  nexus-release/    Release tooling: keygen, signed update manifest, verify
  nexus-winres/     Writes the Windows icon, version info and manifest (.syso)
internal/
  protocol/         Agent ↔ core wire types (v1); shared by both sides
  pki/              Keys, certificates, fingerprints, enrollment codes
  agent/            Agent HTTP server (server.go), sampler, services, logs,
                    docker, packages, storage, security, probe, the root
                    helper (helper.go), sudo runner (runner.go), install.go
  agent/collect/    /proc and /sys collectors (Linux)
  core/
    app/            Wiring: Start() builds every component; Version; data dir
    store/          SQLite (modernc, no CGO): migrations and all queries
    secrets/        DPAPI (Windows) / AES-GCM key file (other) sealing
    identity/       The core's own key and certificate
    agentclient/    mTLS client for one agent (pinned fingerprint)
    fleet/          Poller: keeps a client per server, ingests telemetry
    monitor/        Deterministic alert rules, incidents, cooldowns
    notify/         Desktop, Discord, Telegram, SMTP notifications
    ollama/         Ollama REST client (chat, tools, show, pull)
    ai/             AI engine: registry.go (tool type), tools.go (all tools),
                    policy.go (modes, Decide), engine.go (conversations,
                    approvals, investigations), prompt.go, models.go, hardware
    api/            Loopback HTTP API: server.go (auth, Host/Origin checks),
                    routes.go (all routes), topology.go
    deploy/         Agent install, upgrade and repair over SSH
    agentbin/       Embedded gzip Linux agents for SSH deployment (bin/ is
                    filled by `make embed-agents`; gitignored)
    update/         Signed self-update (GitHub Releases) and installer launch
    webui/          Embeds the built UI (dist/ is filled by `make ui`)
    events/         In-process event bus feeding the UI's SSE stream
  integration/      End-to-end tests: real agent + real core in one process
web/                React 18 + TypeScript + Vite UI
  src/App.tsx       Shell, navigation and routing (hash routes)
  src/pages/        One file per workspace (Fleet, Server, AICenter, …)
  src/components/   UI kit (UI.tsx), Charts, Markdown, Icon
  src/lib/          api.ts (fetch + SSE), state.tsx (app state, routing,
                    useFetch), types.ts, format.ts
installer/          NSIS script (nexus.nsi) and assets (icon, README.txt)
release/            update-public-key.txt (the public half of the signing key)
.github/workflows/  release.yml: test, build, Windows test, signed release
VERSION             Major.minor ("0.3"); CI appends the run number
```

## 4. Toolchain and setup

| Tool | Version | Used for |
|---|---|---|
| Go | 1.26 (see `go.mod`) | everything except the UI; no CGO anywhere |
| Node.js | 20+ (CI uses 22) | the UI build (`web/`) |
| NSIS | 3.x (`apt install nsis`) | only for `make installer` |
| Ollama | any recent | optional, for trying the AI locally |

```bash
git clone https://github.com/WSEU790/MoistTux && cd MoistTux
make ui            # npm ci + vite build → internal/core/webui/dist
go build ./...     # builds without the UI or embedded agents too
make test          # go vet, all Go tests, UI type-check
```

All targets cross-compile: you can build `NEXUS.exe` and the installer on
Linux or macOS. Development so far has mostly been on Linux.

Without `make ui`, the binaries serve a "UI not built" page while the API
still runs. Without `make embed-agents`, *Install over SSH* finds an agent
only in `linux-<arch>/nexus-agent` next to the executable. Both embed
directories are gitignored build outputs.

## 5. Build targets

| Command | Output |
|---|---|
| `make ui` | `internal/core/webui/dist` (embedded into the core and NEXUS.exe) |
| `make agent` | `dist/linux-{amd64,arm64}/nexus-agent` |
| `make embed-agents` | gzip copies in `internal/core/agentbin/bin/` |
| `make core` | `dist/nexus-core` (with embedded agents) |
| `make windows` | `dist/windows-amd64/NEXUS.exe` (GUI) and `nexus-core.exe` |
| `make installer` | `dist/NEXUS-Setup-<version>.exe` |
| `make all` | everything above |
| `make release-manifest` | signed `nexus-update.json` + `.sig` (needs the key) |
| `make clean` | removes all build outputs |

Versioning: local builds are `<VERSION>.0-dev` (for example `0.3.0-dev`).
Dev builds never update themselves. CI builds are `<VERSION>.<run number>`.
Override with `make … VERSION=0.3.42`. The version is set at link time in
`internal/agent.Version` and `internal/core/app.Version`; the update public
key goes into `internal/core/update.PublicKey`.

## 6. Running it locally

### Core and UI with hot reload

```bash
make dev                         # core on 127.0.0.1:7780, allows the vite origin
npm --prefix web run dev         # vite on http://localhost:5173 (proxies /api)
```

The core prints `NEXUS console: http://127.0.0.1:7780/?token=…`; open that URL
once to get the session cookie. Data goes to `~/.local/share/nexus` (or `$XDG_DATA_HOME/nexus`;
`%LOCALAPPDATA%\NEXUS` on Windows). Use `--data DIR` for a throwaway
instance.

For scripts and tests, `nexus-core --token-file FILE` writes the session
token, which you send as `Authorization: Bearer <token>`:

```bash
./dist/nexus-core --data /tmp/nx --listen 127.0.0.1:7781 --token-file /tmp/nx.tok
curl -H "Authorization: Bearer $(cat /tmp/nx.tok)" http://127.0.0.1:7781/api/servers
```

The API accepts only loopback `Host` headers and same-origin requests: this
blocks DNS-rebinding and cross-site requests. Use `127.0.0.1:<port>` or
`localhost:<port>`.

### An agent to talk to

On any Linux machine (the current one is fine):

```bash
./dist/linux-amd64/nexus-agent run --state-dir /tmp/agent --listen 127.0.0.1:7443
```

It prints an enrollment code (`NX1-…`). In the UI: Fleet → Add server →
*Agent already installed*, then enter the address and the code. Run as
non-root without the sudoers rule, privileged operations report "privileged
helper not authorised by sudoers", which is the correct and expected
behavior. For the real least-privilege setup, use
`sudo nexus-agent install` on a disposable server, or *Install over SSH*
(section 10).

### The AI

Run `ollama serve`, pull a model with tool support (for example
`ollama pull qwen2.5:7b`), and pick it in Settings → AI models. The engine
reads each model's capabilities from `/api/show`. Models without native tool
calling use a JSON fallback.

### The Windows app

`NEXUS.exe` needs Windows 10/11 and the WebView2 Runtime. Flags:
`--data DIR` and `--headless` (no window; opens the console in the default
browser). Only one instance can run per data directory (a lock file).

## 7. How a request flows (where to look)

* **Telemetry:** the `agent/sampler.go` ring buffer → `GET /v1/metrics` →
  `fleet` poller → `store/telemetry.go` (raw kept 48 h, 5-minute rollups kept
  30 days) → `monitor` rules → alerts and incidents → `events` bus →
  `GET /api/events` (SSE) → `web/src/lib/api.ts` `events`.
* **A read in the UI:** `GET /api/servers/{id}/agent/<path>` is a read-only
  proxy to the agent, limited by the `proxyAllow` regexp in `api/routes.go`.
  New read endpoints must be added there.
* **An action in the UI** (for example restarting a service):
  `POST /api/servers/{id}/actions/preview`, then `POST …/actions` with a tool
  name. This uses the **same tool registry** as the AI, so validation,
  previews, audit and verification are identical. High-risk tools require
  `confirm` set to the server name or hostname.
* **An AI turn:** `ai/engine.go` builds the prompt (`prompt.go`: fleet
  context, focused server, memory) → Ollama chat with tool schemas → each
  tool call goes to `policy.Decide(mode, tool, args, server, policy)`. That
  returns allow, deny, or approval-needed (an approval row with the `Preview`
  impact). Once approved, `afterDecision` runs the tool, records the
  execution, and resumes the conversation with the real result.
* **Root operations on the server:** the agent handler → `Privileged.Do` →
  `sudo -n /usr/local/lib/nexus/nexus-helper <op> <args>` →
  `HelperMain` → `ValidateHelperArgs` (again) → `ExecHelper`. In tests (and
  agents installed before the dedicated helper existed), the call is
  `<agent binary> helper <op>`.

## 8. Recipes for common changes

### Add a read-only metric or inventory to the agent

1. Add the wire type to `internal/protocol/protocol.go`.
2. Collect it in `internal/agent/` (or `agent/collect/` for /proc and /sys).
   Use `runCmd(ctx, timeout, env, name, args…)` for external tools; it uses a
   fixed PATH and timeout and limits output. Report "unavailable" with a
   reason rather than empty data.
3. Register the route in `agent/server.go` `routes()` with `auth("GET /v1/…", h)`
   (the client certificate is required).
4. Allow the path in `proxyAllow` (`core/api/routes.go`) if the UI should
   read it, and/or add a read-only AI tool (next recipe).
5. Add tests: collector parsing in `agent/collect/*_test.go`, and an assertion
   in `integration/e2e_test.go` if it is important.

### Add an AI tool (and, with it, a UI action)

Tools live in `internal/core/ai/tools.go`; the type is in `registry.go`:

```go
r.Add(&Tool{
    Name: "get_widget_status", Risk: RiskRead, Capability: "widgets", Timeout: 30 * time.Second,
    Description: "…what it returns, written for the model…",
    Params: []Param{{Name: "name", Type: "string", Pattern: `^[a-z0-9-]{1,64}$`, Description: "Widget"}},
    Exec: func(ctx context.Context, env *Env, args map[string]any) (any, error) {
        var out protocol.WidgetStatus
        return out, env.Client.Get(ctx, "/v1/widgets/"+args["name"].(string), nil, &out)
    },
})
```

* Choose `Risk` honestly: `RiskRead`, `RiskLow` (harmless changes such as
  refreshing package lists), `RiskMedium` (service and container actions,
  package installs) or `RiskHigh` (reboot). Reads are allowed in every mode;
  changes need Administrator mode (or Autonomous with a per-server allowlist)
  and usually an approval.
* Every changing tool needs a `Preview` that returns an `Impact` (summary,
  effects, rollback, evidence) and a `Describe`. Its result must contain a
  verification of the new state.
* Constrain every argument (`Enum`, `Pattern`, min/max). Validation errors go
  back to the model and never reach the agent; `integration` tests this.
* Add policy tests in `ai/policy_test.go` for any new decision logic.

### Add a privileged (root) operation

Only if there is no unprivileged way. In `internal/agent/helper.go`:

1. Add the op to `ValidateHelperArgs` with strict argument patterns. It runs
   both before sudo and inside the root process.
2. Implement it in `ExecHelper` with a fixed command line (no shell, no
   user-controlled flags).
3. Call it from the agent with `a.priv.Do(ctx, timeout, &out, "op", args…)`.
4. Document it in `docs/PROTOCOL.md` (privileged helper list).

The sudoers rule never changes: it allows only `/usr/local/lib/nexus/nexus-helper`
with no wildcards, which keeps it compatible with sudo-rs (the default sudo
from Ubuntu 25.10).

### Change the database

Append a new string to `migrations` in `internal/core/store/store.go`. Never
edit an existing migration: installed copies have already run it. Migrations
run in a transaction at start-up, and a database newer than the build is
refused (no silent downgrade). Use `ON DELETE CASCADE` for data that belongs
to a server.

### Add a setting

Settings are JSON documents in the `settings` table, keyed by section and
served by `GET/PUT /api/settings/{section}` (`api/routes.go`), with every
change audited. Add the section there and a panel in
`web/src/pages/Settings.tsx`.

### Add a UI page

Create `web/src/pages/Foo.tsx`, add a case to the router and an item to the
navigation in `web/src/App.tsx`. Fetch with `useFetch<T>(url, deps, intervalMs)`
from `lib/state.tsx` or `api.get/post` from `lib/api.ts`, and subscribe to live
updates with `events`. Use the components in `components/UI.tsx` and the CSS
variables in `styles/` so all four themes keep working. Run
`npm --prefix web run typecheck`.

### Add an alert rule

Rules are in `internal/core/monitor/monitor.go`. They are deterministic: a
threshold, a sustain window and a cooldown, grouped into incidents. Add a
test in `monitor_test.go` with real sample sequences.

## 9. Tests

```bash
make test                 # go vet + go test ./... + UI type-check
make test-race            # race detector
go test ./internal/integration -run TestEndToEnd -v     # the big one, with logs
```

* **Unit tests** sit next to the code: `pki`, `agent` (sudoers rule, helper),
  `agent/collect`, `ai` (policy), `monitor`, `deploy` (saved logins),
  `update` (signature and downgrade rejection, tampering).
* **`internal/integration/e2e_test.go`** starts a real agent and a real core
  in one process and drives the HTTP API. It covers enrollment, mTLS
  rejection, telemetry, Docker, AI tool calls through a scripted Ollama,
  policy denials, the approval workflow (a real `apt-get update` through
  sudo), disconnect and recovery, core restart, certificate rotation and
  unenrollment. As root, the helper runs in-process; otherwise through
  `sudo -n <test binary> helper …` (`TestMain` handles `helper`).
* **`TestSSHDeployment`** runs only with a disposable server:

  ```bash
  NEXUS_SSH_TEST_HOST=10.0.0.5 NEXUS_SSH_TEST_USER=deploy NEXUS_SSH_TEST_PASSWORD=… \
  NEXUS_SSH_TEST_FINGERPRINT=SHA256:… NEXUS_SSH_TEST_UNIT=nginx.service \
    go test ./internal/integration -run TestSSHDeployment -v
  ```

  It installs the agent, rejects a forged host key, restarts a unit through
  the helper, and uninstalls. **Never point it at a real server.**
* **Fakes are only for things outside NEXUS:** a scripted Ollama in the
  tests, and (for update testing) a local HTTP server that imitates the
  GitHub Releases API. Nothing simulated ships in the product.

### Disposable Linux servers with systemd

Real systemd behavior needs a server with systemd as PID 1. A quick lab
(Docker, privileged, on the same machine):

```bash
sudo debootstrap --variant=minbase noble ./noble http://archive.ubuntu.com/ubuntu
sudo chroot ./noble sh -c 'apt-get update && apt-get install -y --no-install-recommends \
  systemd systemd-sysv dbus openssh-server sudo ca-certificates iproute2 procps ufw nginx'
sudo chroot ./noble sh -c 'useradd -m -s /bin/bash -G sudo deploy && echo "deploy:Lab-Passw0rd!" | chpasswd; \
  systemctl enable ssh; systemctl mask getty@tty1.service console-getty.service systemd-logind.service'
(cd noble && sudo tar -c --one-file-system . | docker import -c 'CMD ["/sbin/init"]' -c 'ENV container=docker' - lab/noble)
docker run -d --name lab1 --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  --tmpfs /run --tmpfs /run/lock lab/noble
```

For **Ubuntu 26.04** (codename `resolute`; add `sudo-rs` to the package list
to match a stock server), debootstrap may need `ln -s gutsy
/usr/share/debootstrap/scripts/resolute`. Its systemd 259 refuses to boot on a
cgroup v1 host; if `/sys/fs/cgroup` is not cgroup2, mount the unified
hierarchy instead: `-v /sys/fs/cgroup/unified:/sys/fs/cgroup:rw`.

### Testing the Windows build without Windows

`NEXUS.exe`, the installer and the updater have been exercised under Wine
(`PATH=/usr/lib/wine:$PATH wine …`) and are tested on a real Windows runner in
CI (section 11). DPAPI, the registry and WebView2 behave fully only on real
Windows, so check UI changes there before a release when you can.

## 10. SSH deployment (Fleet → Add server → Install over SSH)

`internal/core/deploy` connects with `golang.org/x/crypto/ssh`:

1. The host key must match the fingerprint the operator confirmed, and NEXUS
   also checks `~/.ssh/known_hosts` (a mismatch is refused).
2. It inspects the server: architecture, systemd, sudo, and the address the
   server sees for this PC (`$SSH_CONNECTION`, which is what a firewall rule
   would have to allow).
3. It uploads the embedded agent for that architecture and checks its
   SHA-256.
4. It runs `sudo nexus-agent install`. The sudo password goes on stdin, never
   on a command line.
5. It chooses the connection (`Request.Connection`):
   * `auto` (default): direct if this PC reaches the agent port; otherwise
     the agent is reinstalled with `--listen 127.0.0.1:<port>` and NEXUS uses
     an SSH tunnel.
   * `ssh`: always the tunnel. `direct`: never (a blocked port then fails
     with *Retry enrollment* and the corrected firewall hint).
6. It enrolls with the agent's fingerprint pinned, directly or through the
   tunnel.

*Upgrade/repair* reuses the same path. For a tunneled server it keeps the
agent on 127.0.0.1 and re-adds the tunnel key if it was removed. Saved SSH
logins are opt-in and sealed with DPAPI in `ssh_credentials` (deleted with
the server); the API never returns them.

### SSH tunnel (`internal/core/tunnel`)

For servers behind firewalls, cloud security groups or NAT. NEXUS generates
an ED25519 key per server and adds one line to the SSH user's
`~/.ssh/authorized_keys`. The line replaces an earlier line from the same
NEXUS (matched by the `nexus-tunnel:<core fingerprint>` comment) and leaves
every other line alone:

```
restrict,command="/usr/local/lib/nexus/nexus-agent bridge 7443" ssh-ed25519 AAAA… nexus-tunnel:20192bd48fa21a8c
```

* `restrict` disables local and remote port forwarding, PTYs, agent and X11
  forwarding and `~/.ssh/rc`. The forced command replaces whatever the
  client asks to run with `nexus-agent bridge 7443`
  (`internal/agent/bridge.go`), which only pipes the session to
  `127.0.0.1:7443`. Port forwarding is deliberately not used: in
  `authorized_keys`, `port-forwarding` would re-enable remote (`-R`)
  forwarding as well, and many hardened servers set `AllowTcpForwarding no`.
* Each agent connection is one SSH session over a single kept-alive SSH
  connection, which redials after drops (sleep, network change). The agent's
  pinned mutual TLS runs inside, so the key alone grants nothing.
* The private key is sealed with DPAPI in `ssh_tunnels` (migration v3,
  deleted with the server). `GET /api/servers/{id}/ssh` returns the tunnel
  settings, never the key.
* Verified against OpenSSH 9.6 and Ubuntu 24.04 behind ufw dropping the agent
  port. The key cannot run commands, open a shell, or forward locally,
  remotely or with `-W`; it works with `AllowTcpForwarding no`, and survives
  restarts and dropped connections. Unit tests use an in-process SSH server
  (`tunnel_test.go`).
* Removing a server from NEXUS leaves that one line in `authorized_keys`. It
  can only reach an agent that no longer trusts this NEXUS; delete the line
  marked `nexus-tunnel:` to tidy up.

## 11. CI, releases and self-update

`.github/workflows/release.yml` runs on every push to `main` and
`nexus-foundation`, and on manual *Run workflow*:

1. **test-linux** (ubuntu-24.04): `make ui`, `go vet`, `go test`, UI typecheck.
2. **build** (ubuntu-24.04): builds the UI, the agents and `NEXUS.exe` /
   `nexus-core.exe` (`make windows`) with version `<VERSION>.<run number>`,
   and uploads them as an artifact.
3. **test-windows** (windows-latest): Windows unit tests (DPAPI, updater,
   saved logins). Builds `NEXUS-Setup-<version>.exe` with NSIS (preinstalled
   on the runner, or installed with Chocolatey). Runs a core smoke test
   (DPAPI-sealed identity, updater enabled), then installer tests: silent
   install, update over a running app with relaunch, and uninstall. The
   tested installer is uploaded as its own artifact.
4. **release** (ubuntu-24.04): signs `nexus-update.json` with the
   `NEXUS_UPDATE_SIGNING_KEY` secret and publishes GitHub Release
   `v<version>` with that installer, the manifest, the signature and the agents.

No CI job uses apt. In November 2026, GitHub runners (ubuntu-24.04 and
ubuntu-26.04) could not resolve Canonical's archive hosts, which broke
`apt-get install nsis`, so the installer moved to the Windows job. For the
same reason, the end-to-end test's real `apt-get update` can take minutes on
CI; the test allows for that. `make installer` still works locally with
Linux NSIS.

**How installed copies update** (`internal/core/update`): at start-up (after
45 s) and every 6 hours, NEXUS fetches `releases/latest` from GitHub. It
downloads `nexus-update.json` and `.sig` and verifies the ed25519 signature
against the public key compiled into the app. It requires the version to be
newer and to match the tag, then downloads the installer and checks its size
and SHA-256. A verified installer is kept as `pending.json` and runs on the
next start, or immediately via *Restart & update*, as
`NEXUS-Setup.exe /S /UPDATE /RELAUNCH`. The installer is per-user
(`%LOCALAPPDATA%\Programs\NEXUS`), so updates need no administrator rights.
User data in `%LOCALAPPDATA%\NEXUS` is never touched by updates.

**The signing key.** The private key exists only as the repository secret
and in the maintainer's password manager. **Never commit it.** If it is lost,
existing installs cannot verify new releases and must be reinstalled by hand
with a build that has a new public key. To rotate it:
`go run ./cmd/nexus-release keygen -out key.txt -pub release/update-public-key.txt`,
store the new secret, and ship a release. Only builds that already contain
the new public key accept releases signed with it.

**Testing updates locally:** set `NEXUS_UPDATE_API=http://127.0.0.1:<port>`
(only loopback HTTP or HTTPS is accepted) to point the updater at a local
server that serves `/repos/WSEU790/MoistTux/releases/latest` and the assets.
`NEXUS_UPDATE_FIRST_CHECK` shortens the first-check delay (for example `2s`).
Sign test manifests with a throwaway key and build with that public key; see
`update_test.go` for the exact formats.

**Making a release:** merge to `main` (or push to `nexus-foundation`). Bump
`VERSION` for a new minor line (for example `0.4`). The run number keeps
counting, so versions only go up. Releases are not code-signed (no
Authenticode certificate yet), so SmartScreen warns on first install.

## 12. Data and files

| Where | What |
|---|---|
| `%LOCALAPPDATA%\NEXUS\nexus.db` | SQLite: servers, telemetry, alerts, incidents, AI conversations, approvals, executions, memory, settings, audit (hash-chained), ssh_credentials |
| `%LOCALAPPDATA%\NEXUS\core.key.sealed`, `core.crt` | the core's identity (DPAPI-sealed key) |
| `%LOCALAPPDATA%\NEXUS\nexus.log`, `nexus.lock` | log, single-instance lock |
| `%LOCALAPPDATA%\NEXUS\updates\` | downloaded, verified installers and `pending.json` |
| `/usr/local/lib/nexus/nexus-agent`, `nexus-helper` | agent and its root helper (hard link), root-owned |
| `/var/lib/nexus-agent/` | agent identity, enrolled cores, pending codes (0700, user `nexus`) |
| `/etc/sudoers.d/nexus-agent` | `nexus ALL=(root) NOPASSWD: /usr/local/lib/nexus/nexus-helper` |
| `/etc/systemd/system/nexus-agent.service` | hardened unit (resource limits, protections) |
| `~<ssh user>/.ssh/authorized_keys` | only for tunneled servers: one `restrict,command="… bridge 7443"` line marked `nexus-tunnel:` |

Ports: agent 7443 (HTTPS, mTLS); dev core 7780; vite 5173; Ollama 11434.

## 13. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| UI shows "UI not built" | run `make ui`, then rebuild the binary |
| Install over SSH cannot find an agent for the server | run `make embed-agents` (or `make core`/`make windows`, which include it) |
| Privileged operations: "privileged helper not authorised by sudoers" | the agent was installed by hand without `install`, or the sudoers file was removed; re-run `sudo nexus-agent install` or *Upgrade/repair over SSH* |
| `go run github.com/…@latest` refused in automation | pinned library code is used instead (`cmd/nexus-winres` uses `tc-hib/winres` from `go.mod`) |
| Windows build lacks an icon | `make windows` runs `cmd/nexus-winres` first; a plain `go build` skips the `.syso` |
| Update never offered on a local build | expected: `-dev` builds and builds without `update.PublicKey` never update |
| CI release job fails "Add the NEXUS_UPDATE_SIGNING_KEY…" | add the repository secret (Settings → Secrets and variables → Actions) |
| Install over SSH: "port 7443 is not reachable from this PC" with *Direct only* | expected behind firewalls; use *Automatic* (SSH tunnel) or open the port for the address the server sees (shown in the hint) |
| SSH tunnel setup fails: "unable to authenticate" | sshd does not accept public keys for that user (`PubkeyAuthentication no`, or a non-default `AuthorizedKeysFile`), or a `ForceCommand` overrides the key's command |
| Agent reports Docker unavailable: permission denied | the `nexus` user is not in the `docker` group; install with `--docker` (root-equivalent, so a deliberate choice) |

## 14. Conventions

* Go: standard library first; dependencies stay minimal and pinned in
  `go.mod`. No CGO. Wrap errors with context (`fmt.Errorf("…: %w", err)`)
  and return them; never `panic` in request paths.
* Run `gofmt` on files you touch. TypeScript must pass `tsc -b --noEmit`.
* Every user-visible change: update `README.md` (user-facing features),
  `docs/STATUS.md` (what is verified and how) and, for protocol or security
  changes, `PROTOCOL.md` / `ARCHITECTURE.md`.
* Commits are small and explain *why*. Pushing to `main` or
  `nexus-foundation` publishes a release to every installed copy, so only
  push code that passed `make test`.

## 15. Known gaps and good next tasks

See [`STATUS.md`](STATUS.md) for the authoritative list. At the time of
writing, the main missing parts are:

* Backup Center (scheduling, verification, restore drills);
* Docker Compose editing, reverse-proxy and database integrations;
* Authenticode signing of NEXUS.exe and the installer (removes the
  SmartScreen prompt);
* Debian, RHEL-family and other distributions (only Ubuntu 24.04 and 26.04
  are verified);
* more coverage on real Windows hardware: WebView2 UI, notifications, DPAPI
  edge cases.
