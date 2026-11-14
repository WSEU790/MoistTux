# Scheduled jobs: cron and systemd timers
> Finding scheduled jobs, reading their output, and fixing jobs that do not run.
Tags: cron, crontab, schedule, systemd timer, anacron, jobs, periodic

## Where jobs are defined

* User crontabs: `crontab -l -u USER` (stored in `/var/spool/cron/crontabs/`).
* System: `/etc/crontab` and `/etc/cron.d/*` (these have a user field), plus scripts in `/etc/cron.hourly|daily|weekly|monthly/` run by `run-parts` (file names must not contain dots).
* systemd timers: `systemctl list-timers --all`.

In NEXUS: catalog `crontab_system`, `crontab_user`, `systemctl_timers`, `journal_unit`.

## Syntax

```
# m  h  dom mon dow  command
*/5  *  *   *   *    /usr/local/bin/check.sh
30   2  *   *   0    /usr/local/bin/weekly-report.sh >> /var/log/weekly.log 2>&1
```

Cron uses the system time zone. `%` must be escaped as `\%` in crontab lines.

## A job that does not run (or fails silently)

1. Is cron running? `systemctl status cron`.
2. Did it start? `journalctl -u cron --since today` (or `grep CRON /var/log/syslog`) shows each execution.
3. Cron's environment is minimal: `PATH=/usr/bin:/bin`, no profile files. Use absolute paths or set `PATH=` at the top of the crontab.
4. Output is mailed to the user (often nowhere); redirect output to a log file to see errors.
5. Permissions: the script must be executable; in `/etc/cron.d` the file must be owned by root and not group/world writable.
6. Missing newline at the end of the crontab makes the last line be ignored.

## systemd timers

A timer `NAME.timer` starts `NAME.service`. Output goes to `journalctl -u NAME.service`. `Persistent=true` runs missed jobs after downtime. Test the job directly with `systemctl start NAME.service`.

## Good practice

Prevent overlapping runs (`flock -n /run/lock/job.lock COMMAND`), log output, alert on failure, and spread heavy jobs (backups, log compression) away from peak hours.
