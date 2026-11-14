# Baseline server hardening checklist
> The essentials for a new or audited Linux server, in priority order.
Tags: hardening, security baseline, checklist, updates, ssh, firewall, users, audit, cis, least privilege

## Priority checklist

1. **Updates**: security updates install automatically (unattended-upgrades); reboots for kernel updates are scheduled. Check with `check_package_updates` and `security_posture`.
2. **SSH**: key-only authentication, root login disabled or key-only, a small set of users (see the SSH runbook).
3. **Firewall**: default deny incoming; only required ports open; admin ports (SSH, databases, 7443) limited to trusted addresses.
4. **Accounts**: no unknown users, no extra UID-0 accounts, sudo limited to admins, no shared accounts, unused accounts locked.
5. **Services**: only what is needed runs and listens; databases and caches listen on localhost or a private network; disable unused services (`systemctl disable --now`).
6. **Logs and monitoring**: persistent journal with size limits, log rotation, NEXUS monitoring with alerts, time sync working.
7. **Backups**: automated, off-site, restore-tested.
8. **Applications**: run as dedicated users, secrets not world-readable (`chmod 600`), web roots not writable by the web server except upload directories.

## Quick audit commands

```
ss -tulpn                                  # listeners
ufw status verbose
sshd -T | grep -Ei 'permitrootlogin|passwordauthentication'
awk -F: '$3==0' /etc/passwd; getent group sudo
find / -xdev -perm -0002 -type f 2>/dev/null | head      # world-writable files
find / -xdev -perm -4000 -type f 2>/dev/null              # setuid binaries (compare with a known list)
systemctl list-units --type=service --state=running
```

In NEXUS: `security_posture` runs most of these checks; `inspect_ssh_configuration`, `inspect_firewall`, `inspect_open_ports`, `inspect_system_users`.

## Kernel parameters worth knowing

Set in `/etc/sysctl.d/99-hardening.conf`, apply with `sysctl --system`:

```
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.tcp_syncookies = 1
kernel.dmesg_restrict = 1
```

Leave `net.ipv4.ip_forward` off unless the server routes traffic (Docker and VPNs turn it on themselves).

## Changes are risky too

Hardening changes can lock you out or break applications. Apply one at a time, test access after each, and keep a rollback path (open session, console access, backup of the config file).
