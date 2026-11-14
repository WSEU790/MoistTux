# MySQL and MariaDB
> Service health, connection problems, disk and memory, slow queries, backups and safe restarts.
Tags: mysql, mariadb, database, innodb, mysqldump, connections, slow query, replication, backup, my.cnf

## Health checks

* Service: `systemctl status mysql` (or `mariadb`); logs: `journalctl -u mysql`, `/var/log/mysql/error.log`.
* Listening: `ss -tulpn | grep 3306` — `127.0.0.1:3306` means local only (good default).
* Status from the server (as root, socket auth): `mysqladmin status`, `mysqladmin processlist`, `mysql -e "SHOW GLOBAL STATUS LIKE 'Threads_connected'"`.

In NEXUS: `inspect_service`, `get_service_logs`, `inspect_open_ports`, catalog `systemctl_status`, `journal_unit`, `df`, `free`. Database queries go through an approved shell command.

## Will not start

Read the error log first. Common causes:

* disk full (InnoDB cannot write) — free space, see the disk runbook; never delete `ibdata1`, `ib_logfile*`, `#ib_redo*` or database directories;
* bad configuration after an edit (`/etc/mysql/my.cnf`, `/etc/mysql/mysql.conf.d/`, `/etc/mysql/mariadb.conf.d/`) — the log names the option;
* InnoDB crash recovery in progress after an unclean shutdown — wait; large redo logs take time;
* port in use, or AppArmor denying a moved data directory (`dmesg | grep apparmor`).

## Too many connections

"ERROR 1040: Too many connections". Check `Threads_connected` vs `max_connections`. Usually an application leaks connections or a slow query holds them. `SHOW FULL PROCESSLIST` shows what they are doing. Raise `max_connections` only with memory headroom (each connection uses memory).

## Slow queries

Enable the slow query log (`slow_query_log = 1`, `long_query_time = 1`, `slow_query_log_file`) and analyse with `mysqldumpslow` or `pt-query-digest`. Missing indexes are the usual cause (`EXPLAIN SELECT ...`). Locks: `SHOW ENGINE INNODB STATUS\G`.

## Memory

`innodb_buffer_pool_size` is the main memory consumer; on a dedicated database server 50–70% of RAM is typical, much less when sharing the server with applications. OOM kills of mysqld mean the total of buffer pool + per-connection buffers × connections is too high.

## Backups

* Logical: `mysqldump --single-transaction --routines --triggers --all-databases | gzip > /backup/all-$(date +%F).sql.gz` (consistent for InnoDB without locking).
* MariaDB: `mariadb-dump` (same options); physical hot backups with `mariabackup`, MySQL with Percona XtraBackup.
* A backup is only verified when it has been restored somewhere and checked.

## Safe restart and upgrades

Restarting drops connections and empties the buffer pool (performance is lower until it warms up). Take a backup before major version upgrades and read the upgrade notes; run `mysql_upgrade` where required (older versions).
