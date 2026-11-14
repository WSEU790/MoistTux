# Logs: journald, syslog and logrotate
> Finding the right log, filtering the journal, keeping logs from filling the disk.
Tags: logs, journalctl, journald, syslog, rsyslog, logrotate, auth.log, dmesg, kernel

## Where logs are

* **journald** collects logs from systemd services, the kernel and syslog: `journalctl`.
* **rsyslog** (if installed) writes text files in `/var/log`: `syslog`, `auth.log` (logins, sudo, SSH), `kern.log`.
* Applications often keep their own: `/var/log/nginx/`, `/var/log/apache2/`, `/var/log/mysql/`, `/var/log/postgresql/`, `/var/log/apt/`, `/var/log/unattended-upgrades/`.
* Kernel ring buffer: `dmesg -T` (needs root on most systems).

In NEXUS: `search_system_logs`, `get_service_logs`, catalog commands `journal_unit`, `journal_errors_boot`, `journal_previous_boot`, `journal_disk_usage`, `journal_vacuum_size`, `journal_vacuum_time`, `dmesg_errors`, `auth_log_tail`, `logrotate_debug`, `logrotate_force`.

## Filtering the journal

```
journalctl -u nginx.service -n 100 --no-pager      # one unit, last 100 lines
journalctl -u nginx -S "2026-11-14 09:00" -U "2026-11-14 10:00"
journalctl -p err -b                               # errors since boot
journalctl -b -1 -n 200                            # end of the previous boot
journalctl -k                                      # kernel messages
journalctl _COMM=sshd -S today                     # by executable
journalctl -o short-iso                            # ISO timestamps
journalctl --list-boots                            # boots kept in the journal
```

Priorities: 0 emerg, 1 alert, 2 crit, 3 err, 4 warning, 5 notice, 6 info, 7 debug.

## Investigating with logs

1. Establish the time of the problem (alert time, user report).
2. Read the service's own log around that time, then the system journal for the same window (OOM killer, disk errors, network changes, restarts).
3. Look for the **first** error, not the last; later errors are often consequences.
4. Correlate with deployments and package changes (`/var/log/apt/history.log`).
5. Quote exact log lines as evidence; do not paraphrase error messages.

## Persistent journal and size

On Ubuntu the journal is persistent (`/var/log/journal`). Limits are set in `/etc/systemd/journald.conf` (`SystemMaxUse=`, default 10% of the filesystem, capped at 4 GiB).

* Usage: `journalctl --disk-usage`.
* Shrink now: `journalctl --vacuum-size=500M` or `--vacuum-time=14d` (deletes archived journal files; irreversible).
* For a permanent limit set `SystemMaxUse=1G` in a drop-in under `/etc/systemd/journald.conf.d/` and `systemctl restart systemd-journald`.

## logrotate

`/etc/logrotate.conf` and `/etc/logrotate.d/*` rotate text logs daily or weekly (run by `logrotate.timer`).

* Test without changes: `logrotate -d /etc/logrotate.conf`.
* Force rotation: `logrotate -f /etc/logrotate.conf`.
* A log that grows without rotation: check that a `/etc/logrotate.d/` rule covers its path and that `postrotate` reopens the file (otherwise the program keeps writing to the rotated, deleted file — space is not freed).
* Typical rule:

```
/var/log/myapp/*.log {
    daily
    rotate 14
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
}
```

`copytruncate` works for programs that cannot reopen logs, at the risk of losing a few lines.

## Kernel messages worth recognising

* `Out of memory: Killed process 1234 (java)` — the OOM killer; see the memory runbook.
* `I/O error, dev sda, sector ...`, `EXT4-fs error`, `Buffer I/O error` — disk or filesystem problem; check SMART and back up.
* `EXT4-fs ... remounting filesystem read-only` — the filesystem detected corruption; services writing to it fail.
* `nf_conntrack: table full, dropping packet` — too many tracked connections; raise `net.netfilter.nf_conntrack_max` or reduce connection churn.
* `task ... blocked for more than 120 seconds` — I/O stalls (slow storage, NFS).
* `segfault at ...` — a program crashed; check its own logs and version.
