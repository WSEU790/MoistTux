# Agent protocol v1

Transport: HTTPS, TLS 1.3 only. The agent requires a client certificate during
the handshake. All endpoints except `POST /v1/enroll` additionally require the
certificate's SHA-256 fingerprint to be enrolled. Request and response bodies
are JSON; wire types are defined in `internal/protocol/protocol.go`. Errors
use `{"error": "...", "code": "..."}`.

The core pins the agent certificate fingerprint. During enrollment it accepts
only a certificate whose fingerprint starts with the 128-bit prefix embedded in
the enrollment code.

## Enrollment and credentials

| Method | Path | Notes |
|---|---|---|
| POST | `/v1/enroll` | `{token, client_cert, label}`. The token is single-use, expires after 24 h and is stored only as a hash. `client_cert` must equal the TLS client certificate. Rate limited: 10 failures per 10 min. |
| POST | `/v1/credentials/rotate-client` | Replace the caller's pinned certificate with `new_client_cert`. |
| POST | `/v1/credentials/rotate-agent` | Stage a new agent key and certificate; returns `new_fingerprint`. |
| POST | `/v1/credentials/activate-agent` | Switch the agent to the staged certificate. The core re-pins first. |
| POST | `/v1/unenroll` | Remove the caller's certificate from the trust list. |

## Read endpoints

| Path | Returns |
|---|---|
| `/v1/health` | Agent identity, version, boot ID, sampler and helper status, capabilities, agent RSS |
| `/v1/info` | `SystemInfo`: OS, kernel, hardware, interfaces, block devices, package manager, reboot-required |
| `/v1/metrics?since=SEQ&max=N` | Buffered `MetricsSample`s with `seq > since` (5 s interval, 20 min buffer) |
| `/v1/processes?sort=cpu\|mem&limit=N`, `/v1/processes/{pid}` | Process list with windowed CPU% / process detail |
| `/v1/services`, `/v1/services/{unit}`, `/v1/services/{unit}/dependencies` | systemd units / detail with health assessment / dependency lists |
| `/v1/logs?unit&since&until&priority&grep&limit&file`, `/v1/logs/files` | journald or allowlisted `/var/log` entries |
| `/v1/docker`, `/v1/docker/{containers,images,volumes,networks,compose}`, `/v1/docker/containers/{id}[/logs]` | Docker Engine data. Environment values are never returned. |
| `/v1/storage`, `/v1/storage/usage?path&depth&privileged`, `/v1/storage/large-files?path&min_mb&limit&privileged` | Filesystems and devices / directory sizes / largest files (report only) |
| `/v1/network`, `/v1/network/firewall` | Interfaces, routes, DNS, listening sockets with bind scope / firewall listing |
| `/v1/packages?q`, `/v1/packages/updates`, `/v1/packages/history` | Installed packages / simulated upgrade / APT history |
| `/v1/security/{ssh,auth?hours,users,posture}` | `sshd -T` with findings / auth events / accounts / posture |

## Action endpoints

Every action endpoint re-reads the affected state after acting and returns it
as `after`, so the core can verify the outcome.

| Method | Path | Privilege |
|---|---|---|
| POST | `/v1/services/{unit}/{start\|stop\|restart\|reload\|try-reload-or-restart\|enable\|disable\|reset-failed}` | helper `systemctl` |
| POST | `/v1/docker/containers/{id}/{start\|stop\|restart\|pause\|unpause}` | Docker socket |
| POST | `/v1/packages/refresh` | helper `apt-update` |
| POST | `/v1/packages/upgrade` | `{packages[], security_only, dry_run}` → helper `apt-upgrade` (never `dist-upgrade`, never removes packages) |
| POST | `/v1/probe` | `{kind: tcp\|tls\|http, target, expect_status}`. Active check from the server itself. |
| POST | `/v1/system/reboot` | `{confirm: <hostname>}` → helper `reboot` |

## Privileged helper

The privileged helper is the only way the agent gains root. `install` places
it at `/usr/local/lib/nexus/nexus-helper`, a root-owned hard link to the agent
binary that, under that name, runs nothing but the helper. The agent calls
`sudo -n /usr/local/lib/nexus/nexus-helper <op> [args]`, and the sudoers rule
is a single line with no `Defaults` and no argument wildcards:

```
nexus ALL=(root) NOPASSWD: /usr/local/lib/nexus/nexus-helper
```

The classic sudo and sudo-rs (the default `sudo` from Ubuntu 25.10) both
accept this rule; sudo-rs rejects argument wildcards such as `helper *`. Agents
installed before this change keep working through `nexus-agent helper <op>`
until they are upgraded. The helper validates arguments against fixed
patterns both before calling sudo and again inside the root process. Operations: `ping`, `systemctl <verb> <unit>`, `apt-update`,
`apt-upgrade all|security|packages <names…>`, `ufw-status`, `nft-list`,
`iptables-save`, `sshd-config`, `fail2ban-status [jail]`,
`read-file <path> <max>`, `sockets`, `du <path> <depth>`,
`largefiles <path> <min> <limit>` and `reboot`.

`read-file` accepts only paths under `/var/log` and selected configuration
directories. It refuses keys, shadow files and credential files, including
when reached through a symlink.

## Transport

The agent speaks HTTPS with mutual TLS (pinned certificates) on its listen
address, `:7443` by default. When the NEXUS PC cannot reach that port, the
agent listens on `127.0.0.1:7443` and each TLS connection is carried by an
SSH session whose forced command is `nexus-agent bridge 7443`, which pipes
stdin/stdout to the local port (see `internal/core/tunnel`). The TLS
handshake, pinning and every endpoint are identical over both transports.

