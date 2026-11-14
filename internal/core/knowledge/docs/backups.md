# Backups and restores
> Strategy (3-2-1), what to back up, tools (rsync, restic, borg, database dumps), and verifying backups.
Tags: backup, restore, 3-2-1, rsync, restic, borg, snapshot, mysqldump, pg_dump, retention, verification

## Strategy

* **3-2-1**: three copies of the data, on two different kinds of storage, one off-site (another provider or region). Keep at least one copy that the server itself cannot delete (append-only repository, object lock, pull-based backups) so ransomware or an attacker cannot erase it.
* Decide the **RPO** (how much data you can lose: hourly? daily?) and the **RTO** (how fast you must be back). They define frequency and method.
* A backup is not verified until a restore has been tested. NEXUS never assumes a backup is valid without verification.

## What to back up

* Application data and uploads, database dumps (not the live database files), configuration (`/etc`, application configs, crontabs, systemd units), TLS private keys if not re-issuable, Docker volumes and Compose files, and a list of installed packages (`dpkg --get-selections`).
* What can be rebuilt from version control or packages does not need file backups, but the instructions to rebuild do.

## Databases

File copies of a running database are inconsistent. Use the database's tools:

* MySQL/MariaDB: `mysqldump --single-transaction --all-databases | gzip` or mariabackup/XtraBackup.
* PostgreSQL: `pg_dump -Fc DB` per database plus `pg_dumpall --globals-only` for roles; or pg_basebackup/pgBackRest for large databases.
* Run dumps before the file backup picks up the dump directory.

## Tools

* **rsync** to another host: simple mirror, but a mirror copies deletions and corruption too — keep versions (`--link-dest` snapshots).
* **restic** and **borg**: encrypted, deduplicated, versioned repositories (local, SFTP, S3). Keep the repository password somewhere other than the server.
  * `restic backup /etc /var/www /backup/db`, `restic snapshots`, `restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune`, `restic check`.
* **Provider snapshots**: fast whole-disk restores; not off-site by themselves and usually not application-consistent.

## Verification

* Monitor that backup jobs ran and succeeded (exit status, logs, repository snapshot dates).
* Regularly restore into a scratch location or server: check files open, database dumps import, the application starts.
* Check backup size trends: a sudden drop often means something stopped being backed up.

## Before risky changes

Before upgrades, migrations or bulk deletions: take a fresh backup or snapshot, confirm it exists, and note how to roll back.
