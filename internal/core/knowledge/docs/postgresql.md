# PostgreSQL
> Clusters on Debian/Ubuntu, logs, connections, vacuum, disk, backups and upgrades.
Tags: postgresql, postgres, database, pg_dump, vacuum, autovacuum, connections, pg_hba, wal, replication, backup

## Clusters on Debian/Ubuntu

Debian packages manage clusters by version and name:

* `pg_lsclusters` — version, name, port, status, data directory and log file.
* Service: `systemctl status postgresql@16-main` (the `postgresql` unit is only a wrapper).
* Config: `/etc/postgresql/16/main/postgresql.conf` and `pg_hba.conf`; data: `/var/lib/postgresql/16/main`.
* Logs: `/var/log/postgresql/postgresql-16-main.log`.
* Admin shell: `sudo -u postgres psql`.

In NEXUS: `inspect_service`, `get_service_logs`, catalog `systemctl_status`, `journal_unit`, `df`, `ss_listen`. SQL goes through an approved shell command (`sudo -u postgres psql -c '...'`).

## Will not start

Check the log file from `pg_lsclusters`. Common causes: disk full (WAL cannot be written — never delete files in `pg_wal`), a syntax error in `postgresql.conf`, wrong permissions on the data directory (must be owned by postgres, mode 700), port in use, or recovery in progress.

## Connections

* "FATAL: no pg_hba.conf entry for host" — add a line to `pg_hba.conf` for that user/database/address with `scram-sha-256`, then `SELECT pg_reload_conf();` or `systemctl reload postgresql@16-main`.
* "too many clients already" — compare `SELECT count(*) FROM pg_stat_activity;` with `max_connections`. Use a pooler (PgBouncer) instead of raising the limit far.
* Listen address: `listen_addresses = 'localhost'` by default; change and restart to accept remote connections (and restrict them in the firewall).

## Vacuum and bloat

Autovacuum removes dead rows and prevents transaction ID wraparound. Check `SELECT relname, n_dead_tup, last_autovacuum FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10;`. Long-running or idle-in-transaction sessions block vacuum: `SELECT pid, state, xact_start, query FROM pg_stat_activity WHERE state <> 'idle' ORDER BY xact_start;`. Warnings about wraparound need prompt attention.

## Disk

* Database sizes: `SELECT datname, pg_size_pretty(pg_database_size(datname)) FROM pg_database;`.
* WAL growth: an inactive replication slot keeps WAL forever — `SELECT slot_name, active FROM pg_replication_slots;`. Drop slots that are no longer used.

## Backups

* Logical: `sudo -u postgres pg_dump -Fc DBNAME > /backup/DBNAME-$(date +%F).dump`, restore with `pg_restore`. All databases and roles: `pg_dumpall`.
* Physical/point-in-time: `pg_basebackup` with WAL archiving, or tools like pgBackRest.
* Test restores regularly into a scratch database.

## Upgrades

Minor updates (16.x) come through apt and need a restart. Major upgrades (15 → 16) need `pg_upgradecluster` (Debian) or pg_upgrade/dump-restore, with a backup first and in a maintenance window.
