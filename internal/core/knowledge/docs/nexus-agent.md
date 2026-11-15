# NEXUS agent troubleshooting
> Fixing NEXUS's own agent: privileged helper errors, NoNewPrivileges, sudoers, enrollment, connectivity and the SSH tunnel.
Tags: nexus, nexus-agent, agent, privileged helper, sudo, no new privileges, NoNewPrivileges, sudoers, enrollment, tunnel, repair

## How the agent works

`nexus-agent` runs as the unprivileged system user `nexus` (systemd unit `nexus-agent.service`). Root operations go through `sudo -n /usr/local/lib/nexus/nexus-helper`, allowed by `/etc/sudoers.d/nexus-agent`. NEXUS reaches the agent on port 7443, or through SSH when that port is blocked. These are NEXUS components: their problems are fixed from NEXUS, mainly with **Upgrade/repair over SSH** in the server's settings (gear icon on the server page), which reinstalls the agent and rewrites its unit, sudoers rule and keys while keeping its identity.

In NEXUS: `list_servers` (status and status reason), `inspect_service` with unit `nexus-agent.service`, catalog `systemctl_status`, `systemctl_cat`, `journal_unit`.

## "privileged helper unavailable" and "no new privileges"

Status reason or error: `sudo: The "no new privileges" flag is set, which prevents sudo from running as root`.

* Cause: the `nexus-agent` service runs with systemd's NoNewPrivileges. Agents installed by NEXUS 0.3.7 or earlier used sandboxing options (ProtectKernelModules, LockPersonality, RestrictRealtime) that imply NoNewPrivileges for non-root services on some systems (for example Debian 12), which blocks the sudo helper. It is not a kernel setting of the server, there is no `/proc/sys/kernel/no_new_privs`, and a reboot does not help.
* Effect: every root operation fails (package updates, service restarts, catalog commands that need root, approved shell commands, firewall and SSH reads). Monitoring still works.
* Fix: in NEXUS, open the server's settings and run **Upgrade/repair over SSH** (the saved SSH login can be used if one was saved). The new unit sets `NoNewPrivileges=no` without those options.
* Verify: the server's status reason clears, and a root read such as `inspect_ssh_configuration` succeeds.

## "privileged helper not authorised by sudoers"

The sudoers file `/etc/sudoers.d/nexus-agent` is missing or old (a hand install with `nexus-agent run`, or the file was removed). On Ubuntu 25.10+ (sudo-rs), rules from agents older than NEXUS 0.3.3 are rejected. Fix: Upgrade/repair over SSH.

## Shell commands refused on the server

`refused: shell commands are not enabled on this server` or `not signed by a NEXUS installation trusted`: the server does not yet trust this NEXUS's signing key (`/etc/nexus-agent/shell-signers`). Run Upgrade/repair over SSH once. `the grant was issued for a different server` means the machine ID changed (a cloned server): repair it too.

## Server offline

1. Is the agent running? On the server: `systemctl status nexus-agent` and `journalctl -u nexus-agent -n 100`.
2. Direct connection: port 7443 must be reachable from the NEXUS PC (firewall, cloud security group). If not, run Upgrade/repair and choose a connection through SSH, or allow the port.
3. SSH tunnel: SSH must be reachable and the tunnel key line (`nexus-tunnel:`) must remain in the SSH user's `~/.ssh/authorized_keys`. Repair re-adds it.
4. Certificate errors (fingerprint mismatch) mean the agent's identity changed (its state was deleted). NEXUS will not trust it automatically: remove the server and add it again.

## Enrollment problems

* "the agent port is not reachable from this PC": choose the Automatic connection when installing (it switches to SSH).
* Enrollment codes are single-use and expire after 24 hours; get a new one with `sudo nexus-agent enroll-code` on the server.

## Agent version

`list_servers` and the server page show the agent version. NEXUS updates itself automatically, but agents are updated only by Upgrade/repair over SSH; do that after NEXUS updates that mention agent fixes.
