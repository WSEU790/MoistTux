# systemd services
> Diagnosing and managing services with systemctl: states, failures, restart loops, overrides and dependencies.
Tags: systemd, systemctl, service, unit, failed, restart, enable, disable, override, drop-in, timers

## Unit states

`systemctl status UNIT` shows:

* **Loaded**: whether the unit file was found, and whether it is `enabled` (starts at boot), `disabled`, `static` (started only as a dependency) or `masked` (cannot be started).
* **Active**: `active (running)`, `active (exited)` (one-shot that finished), `inactive (dead)`, `activating`, `deactivating`, or `failed`.
* **Main PID**, memory, CPU and the latest log lines.

`systemctl --failed` lists failed units. `systemctl list-units --type=service --state=running` lists running services.

In NEXUS: `list_services`, `inspect_service`, `get_service_logs`, `restart_service`, `start_service`, `stop_service`, `reload_service`, and catalog commands `systemctl_status`, `systemctl_cat`, `systemctl_enable`, `systemctl_enable_now`, `systemctl_disable`, `systemctl_mask`, `systemctl_daemon_reload`, `systemctl_reset_failed`, `systemd_analyze_verify`.

## Diagnosing a failed service

1. `systemctl status UNIT` — note the exit code/signal (`status=1/FAILURE`, `code=killed, signal=KILL` often means the OOM killer or a timeout).
2. Logs: `journalctl -u UNIT -n 200 --no-pager` and, for the attempt that failed, `journalctl -u UNIT -b`.
3. Common causes:
   * configuration syntax error — run the program's own config test (`nginx -t`, `apachectl configtest`, `sshd -t`, `named-checkconf`, `postfix check`);
   * port already in use — `ss -tulpn | grep :PORT`;
   * permission denied on a file or directory — check ownership against `User=` in the unit (`systemctl cat UNIT`);
   * missing file or dependency after an upgrade;
   * disk full (`df -h`), out of memory (`dmesg -T | grep -i oom`);
   * `start-limit-hit`: it crashed too often (default 5 times in 10 s) and systemd stopped retrying.
4. Fix the cause first, then `systemctl reset-failed UNIT` and `systemctl start UNIT`.
5. Verify: active state, no new errors in the journal, the port listens, and the service answers (probe its endpoint).

A running process is not proof of health: check that it serves requests.

## Restart versus reload

* `reload` asks the service to re-read its configuration without dropping connections (nginx, Apache, sshd, postfix, haproxy support it). Prefer it after config changes.
* `restart` stops and starts the process; connections drop, in-memory state is lost.
* Always test configuration before reload/restart so a typo does not take the service down.

## Restart loops

`Restart=on-failure` with a crashing program produces a loop. Signs: rising "restart counter" in `systemctl status`, repeated start/stop lines in the journal. Find the error in the first lines after each start. Stopping the loop: fix the cause; `systemctl stop UNIT` temporarily if it consumes resources.

## Overrides (drop-ins)

Never edit files in `/lib/systemd/system` or `/usr/lib/systemd/system`; package upgrades overwrite them. Use a drop-in:

```
/etc/systemd/system/UNIT.service.d/override.conf
[Service]
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
```

`systemctl edit UNIT` creates it interactively. After creating or changing unit files run `systemctl daemon-reload`, then restart the unit. `systemctl cat UNIT` shows the unit plus all drop-ins.

## Enable, disable, mask

* `enable` starts at boot; `enable --now` also starts it now.
* `disable` removes it from boot; it keeps running until stopped (`disable --now` does both).
* `mask` links the unit to /dev/null so nothing can start it, not even dependencies. Use it to stop a conflicting service from coming back; `unmask` reverses it.

## Dependencies and ordering

* `systemctl list-dependencies UNIT` shows what it needs; `--reverse` shows what needs it.
* `After=`/`Before=` control order only; `Requires=`/`Wants=` control whether the other unit is started.
* Services that need the network should use `After=network-online.target` and `Wants=network-online.target`.

## Timers

systemd timers replace cron for many jobs (`apt-daily`, `logrotate`, `certbot`, `fstrim`). `systemctl list-timers --all` shows next and last run; the job's output is in `journalctl -u NAME.service`. A timer that "never ran" may be inactive — `systemctl enable --now NAME.timer`.

## Boot problems

* `systemd-analyze blame` lists slow units; `systemd-analyze critical-chain` shows the chain that delayed boot.
* `journalctl -b -1 -p err` shows errors from the previous boot (needs a persistent journal).
* A unit in `/etc/fstab` that fails to mount can drop the system to emergency mode — use `nofail` for non-critical mounts.
