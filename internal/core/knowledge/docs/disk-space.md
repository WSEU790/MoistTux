# Disk full: finding and freeing space safely
> A step-by-step triage for full filesystems and full inodes, with safe clean-up actions.
Tags: disk, space, full, df, du, inodes, cleanup, journal, docker, apt cache, deleted files, no space left on device

## Symptoms

"No space left on device", services failing to start or write, databases refusing writes, package installs failing, logins slow. Check both bytes and inodes: a filesystem can be full of tiny files while `df -h` shows free space.

In NEXUS: `get_disk_metrics`, `inspect_disk_usage`, `find_large_files`, catalog `df`, `df_inodes`, `du_dir`, `deleted_open_files`, `journal_disk_usage`, `journal_vacuum_size`, `apt_clean`, `apt_autoremove`, `docker_df`, `docker_prune`, `logrotate_force`.

## Triage

1. `df -hT` — which filesystem is full? (`/`, `/var`, `/boot`, a data volume.)
2. `df -i` — inodes exhausted? (Typical cause: millions of session files, mail queue, cache files.)
3. Find where the space is, one level at a time, staying on one filesystem:
   `du -xh --max-depth=1 / | sort -h`, then descend into the largest directory (`/var`, then `/var/log`, …).
4. Check for **deleted files still held open**: their space is not freed until the process closes them. `find /proc/*/fd -lname '*(deleted)'` or `lsof +L1`. Restarting (or reloading) the owning service frees the space.
5. Identify what the large items are before deleting anything: logs, caches, backups, database files, container data, core dumps.

## Usual suspects and safe fixes

| Location | Cause | Safe action |
|---|---|---|
| `/var/log/journal` | journal grew large | `journalctl --vacuum-size=500M` |
| `/var/log/*.log`, `*.1`, `*.gz` | missing or failing rotation | fix logrotate; `logrotate -f`; delete only old rotated archives |
| `/var/cache/apt/archives` | downloaded packages | `apt-get clean` |
| old kernels in `/boot` and `/usr/lib/modules` | kernels not removed | `apt-get autoremove` (keeps the running and newest kernels) |
| `/var/lib/docker` | images, stopped containers, build cache, logs | `docker system df`; `docker system prune` (volumes are kept); cap container logs with `max-size` |
| `/var/lib/snapd` | old snap revisions | `snap set system refresh.retain=2` |
| `/tmp`, `/var/tmp` | leftover files | delete files older than a few days after checking owners |
| `/var/crash`, `core` files | crash dumps | inspect, then delete |
| application data, uploads, backups | growth | not a clean-up job: extend the disk or move data |

Never delete database files, files under `/var/lib/mysql` or `/var/lib/postgresql`, or anything you cannot identify. Never truncate a file a database has open. Large is not the same as unneeded.

## Docker container logs

The default `json-file` logging driver keeps logs forever. Find large ones: `du -sh /var/lib/docker/containers/*/*-json.log`. Cap them in `/etc/docker/daemon.json`:

```
{ "log-driver": "json-file", "log-opts": { "max-size": "50m", "max-file": "3" } }
```

then restart Docker (affects running containers; plan it). Existing containers keep their old settings until recreated.

## /boot is full

Kernel updates fail with "No space left on device" in `/boot`. Run `apt-get autoremove` to remove old kernels. If apt itself is stuck, list kernels with `dpkg -l 'linux-image-*'`, keep the running kernel (`uname -r`) and the newest one, and remove older ones with `apt-get remove linux-image-VERSION`.

## Inodes exhausted

Find directories with the most files: `find /var -xdev -type f | cut -d/ -f2-4 | sort | uniq -c | sort -n | tail`. Typical culprits: PHP session directories, mail queues (`/var/spool/postfix`), cache directories. Clean the files with their application's tools or by age, and fix the cause (missing session garbage collection, a stuck queue).

## Growing the disk (cloud servers)

After enlarging the volume in the provider's console:

```
lsblk                                  # see the new size
growpart /dev/sda 1                    # grow partition 1 (package cloud-guest-utils)
resize2fs /dev/sda1                    # ext4
xfs_growfs /                           # xfs (by mount point)
```

With LVM: `pvresize /dev/sda3`, `lvextend -r -l +100%FREE /dev/VG/LV`. Take a snapshot first.

## Prevent recurrence

Set journal limits, make sure logrotate covers every application log, cap Docker logs, monitor disk and inode usage with alerts at 80–90%, and plan capacity for data growth.
