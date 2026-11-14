# Kernel updates, restarts and reboots
> When a reboot is really needed, restarting services after library updates, and rebooting safely.
Tags: kernel, reboot, reboot-required, needrestart, livepatch, uptime, restart services, maintenance window

## Is a reboot needed?

* `/var/run/reboot-required` exists (and `/var/run/reboot-required.pkgs` lists why) after kernel, libc or microcode updates on Ubuntu/Debian.
* `needrestart -b` lists the running kernel versus the newest installed one, and services still using old libraries.
* Running kernel: `uname -r`; installed: `dpkg -l 'linux-image-*' | grep ^ii`.

Library updates (openssl, libc) usually need only the affected **services** restarted; kernel updates need a reboot (or a livepatch for critical fixes: `canonical-livepatch status` with Ubuntu Pro).

In NEXUS: `security_posture` (shows reboot-required), `check_package_updates`, catalog `needrestart`, `uptime`, `last_logins`; reboot with `reboot_server`, which is high risk, disabled by default in policy, and always needs approval with the hostname typed.

## Rebooting safely

1. Choose a maintenance window; tell users.
2. Make sure you can get back in: SSH works, console access exists (provider), firewall rules persist (ufw is persistent; ad-hoc iptables rules may not be).
3. Check that critical services are **enabled** so they start at boot (`systemctl is-enabled`), and that `/etc/fstab` mounts are correct (a bad entry can stop the boot; use `nofail` for non-critical mounts).
4. For databases, stop the application first or let the database shut down cleanly (systemd does this on reboot).
5. Reboot: `systemctl reboot`.
6. After it returns: kernel version (`uname -r`), failed units (`systemctl --failed`), services and endpoints, disk mounts, NEXUS agent online.

## Unexpected reboots

`last -x reboot shutdown | head` shows reboot times. Find the cause in the previous boot's log: `journalctl -b -1 -n 200` (kernel panic, OOM, power loss, watchdog, provider maintenance, unattended-upgrades with automatic reboot enabled). A gap in the log without a clean shutdown sequence points to a crash or power/hypervisor event.

## Old kernels

`apt-get autoremove` removes old kernels while keeping the running and newest ones. Do this when `/boot` fills up.
