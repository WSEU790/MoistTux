# APT package management (Debian and Ubuntu)
> Updating, installing, removing, holding and repairing packages safely with apt, apt-get and dpkg.
Tags: apt, apt-get, dpkg, packages, updates, upgrade, install, remove, unattended-upgrades, ubuntu, debian

## Concepts

* **Package lists** (`/var/lib/apt/lists`) describe what the configured repositories offer. `apt-get update` refreshes them; it installs nothing.
* **upgrade** installs newer versions of installed packages but never removes packages or installs new dependencies that require removals. **full-upgrade / dist-upgrade** may remove packages to resolve dependencies — treat it as a bigger change.
* **remove** uninstalls a package but keeps its configuration files in `/etc`. **purge** also deletes the configuration.
* **autoremove** removes packages that were installed only as dependencies and are no longer needed (often old kernels).
* `apt` is the interactive front end; `apt-get` and `apt-cache` have a stable interface for scripts. In automation use `apt-get -y` with `DEBIAN_FRONTEND=noninteractive`.
* Security updates on Ubuntu come from the `-security` pocket (e.g. `noble-security`); on Debian from `security.debian.org`.

In NEXUS: `refresh_package_lists`, `check_package_updates`, `apply_security_updates`, `apply_package_updates`, and catalog commands `apt_install`, `apt_remove`, `apt_purge`, `apt_autoremove`, `apt_hold`, `apt_policy`, `apt_show`, `apt_search`, `apt_simulate_install`, `apt_simulate_remove`.

## Safe update procedure

1. Check free space on `/` and `/boot` (`df`). A full `/boot` breaks kernel updates.
2. Refresh lists: `apt-get update`. Read errors: a failing repository (404, expired key) does not block the others but means its packages are stale.
3. Review what would change: `apt list --upgradable`, or simulate with `apt-get -s upgrade`. Look for kernels, libc6, openssl, openssh-server, database servers and web servers.
4. For production: install security updates first; plan full upgrades in a maintenance window and make sure a verified backup exists for databases.
5. Upgrade: `apt-get -y upgrade` (add `-o Dpkg::Options::=--force-confold` to keep your modified config files without prompting).
6. Afterwards check `needrestart -b` for services still running old libraries and `/var/run/reboot-required` for kernel updates. Restart affected services; schedule a reboot for kernel updates.
7. Verify the services you care about (status, logs, endpoint probes).

## Installing and removing packages

* Simulate first: `apt-get -s install PACKAGE` shows every new dependency; `apt-get -s remove PACKAGE` shows everything that would be removed with it. Removing a library can take an application with it.
* Install: `apt-get install -y PACKAGE`. A specific version: `apt-get install PACKAGE=VERSION` (see versions with `apt-cache policy PACKAGE`).
* Find a package: `apt-cache search --names-only WORD`; which package owns a file: `dpkg -S /path/to/file`; files of a package: `dpkg -L PACKAGE`.
* Remove but keep config: `apt-get remove PACKAGE`; delete config too: `apt-get purge PACKAGE` (irreversible without backup).
* Clean up: `apt-get autoremove` then `apt-get clean` to empty `/var/cache/apt/archives`.

## Holding packages

Hold a package to stop upgrades from changing it (e.g. a database whose upgrade you plan separately):

```
apt-mark hold postgresql-16
apt-mark showhold
apt-mark unhold postgresql-16
```

Held packages show as "kept back" during upgrade. Do not leave security-relevant packages held indefinitely; record why a hold exists.

Packages can also be "kept back" without a hold because the new version needs new dependencies or phased updates (Ubuntu rolls some updates out gradually; this is normal and resolves itself).

## Broken packages and dpkg errors

Symptoms: "dpkg was interrupted, you must manually run 'dpkg --configure -a'", "unmet dependencies", "E: Sub-process /usr/bin/dpkg returned an error code (1)".

1. `dpkg --audit` lists half-installed packages.
2. Finish interrupted installs: `dpkg --configure -a`.
3. Repair dependencies: `apt-get install -f -y`.
4. Read the actual error above the summary line; usually a maintainer script failed (a service that fails to start, a full disk, a syntax error in a config file the package validates).
5. Disk full is a frequent cause — check `df -h` and `df -i` first.

## The dpkg/apt lock

"Could not get lock /var/lib/dpkg/lock-frontend" means another package operation runs — usually `unattended-upgrades` or `apt-daily` shortly after boot.

* Check: `ps -eo pid,etime,args | grep -E 'apt|dpkg|unattended'`.
* Wait for it to finish. Do **not** delete lock files or kill dpkg mid-install; that corrupts the package database. If a process is truly hung for a long time, investigate its log (`/var/log/unattended-upgrades/`, `/var/log/apt/term.log`) before acting.

## Repositories and signing keys

* Sources live in `/etc/apt/sources.list` and `/etc/apt/sources.list.d/*.list` or `*.sources` (deb822 format, default on Ubuntu 24.04+).
* Third-party repos should use a key file referenced with `signed-by=/etc/apt/keyrings/NAME.gpg`; `apt-key` is deprecated.
* "NO_PUBKEY" or "EXPKEYSIG": the repository key is missing or expired; get the current key from the vendor's documentation.
* "Release file ... is not valid yet": the server clock is wrong — fix time sync first.
* A repository for a different release (e.g. `jammy` on a `noble` server) causes dependency chaos; remove or correct it.

## Unattended upgrades

Ubuntu installs security updates automatically by default via `unattended-upgrades` (configured in `/etc/apt/apt.conf.d/50unattended-upgrades` and `20auto-upgrades`).

* Logs: `/var/log/unattended-upgrades/unattended-upgrades.log`.
* Dry run: `unattended-upgrade --dry-run -v`.
* Automatic reboot is off by default (`Unattended-Upgrade::Automatic-Reboot "false"`); kernel updates then wait for a reboot (`/var/run/reboot-required`).

## Release upgrades

Moving between releases (e.g. Ubuntu 22.04 → 24.04) with `do-release-upgrade` is a major change: take a full backup or snapshot, read the release notes, ensure console access, and do it in a maintenance window. Never run it automatically.

## Logs

* `/var/log/apt/history.log` — what was installed, upgraded or removed, when, and by which command.
* `/var/log/apt/term.log` — full terminal output of package operations.
* `/var/log/dpkg.log` — low-level dpkg actions.
