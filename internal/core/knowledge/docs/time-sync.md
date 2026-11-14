# Time synchronisation and time zones
> Checking clock sync with timedatectl, systemd-timesyncd and chrony, and why clock drift breaks things.
Tags: time, ntp, chrony, timesyncd, timedatectl, clock, drift, timezone, utc

## Why it matters

A wrong clock breaks TLS ("certificate not yet valid"), apt ("Release file is not valid yet"), Kerberos and TOTP logins, log correlation, cron timing and distributed databases.

In NEXUS: catalog `timedatectl`, `timesync_status`, `chrony_sources`, `set_timezone`, `enable_ntp`.

## Check

`timedatectl` shows local time, UTC, time zone, "System clock synchronized: yes/no" and "NTP service: active".

* Ubuntu uses `systemd-timesyncd` by default (Ubuntu 25.10+ may use chrony): `timedatectl timesync-status`.
* chrony: `chronyc tracking` (offset), `chronyc sources -v` (servers; `*` marks the selected one).

## Fix

* NTP off: `timedatectl set-ntp true`.
* No sync: outbound UDP 123 blocked by a firewall, or no reachable servers configured (`/etc/systemd/timesyncd.conf` `NTP=`, or `/etc/chrony/chrony.conf`). Cloud providers offer internal NTP servers (e.g. `169.254.169.123` on AWS).
* Large offset: chrony steps the clock only at startup by default (`makestep`); restart chronyd or run `chronyc makestep`.
* Only one time daemon should run (timesyncd or chrony, not both).

## Time zones

Servers are commonly kept in UTC (`timedatectl set-timezone UTC`) to simplify logs across regions; applications convert for display. Changing the zone affects cron schedules (cron uses local time).
