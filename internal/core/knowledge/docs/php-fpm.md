# PHP-FPM
> Pools, sockets, worker limits and the errors they cause behind nginx or Apache.
Tags: php, php-fpm, fpm, pool, pm.max_children, 502, socket, wordpress, laravel, opcache

## Basics

* Service per version: `php8.3-fpm` (`systemctl status php8.3-fpm`). Config: `/etc/php/8.3/fpm/php-fpm.conf`, pools in `/etc/php/8.3/fpm/pool.d/www.conf`, PHP settings in `/etc/php/8.3/fpm/php.ini`.
* Socket: `/run/php/php8.3-fpm.sock` (must match `fastcgi_pass` in nginx or `SetHandler "proxy:unix:..."` in Apache).
* Test config: `php-fpm8.3 -t`. Apply: `systemctl reload php8.3-fpm`.
* Logs: `/var/log/php8.3-fpm.log`; PHP errors go to the web server error log or the pool's `php_admin_value[error_log]`.

In NEXUS: `inspect_service`, `get_service_logs`, `reload_service`, `restart_service`, catalog `systemctl_status`, `journal_unit`, `ps_top_mem`.

## "server reached pm.max_children setting"

All workers are busy; new requests wait and nginx may return 502/504. Causes: slow requests (database, external APIs), traffic spikes, or too few workers.

Sizing: `pm.max_children ≈ (RAM available for PHP) / (average memory per worker)`. Measure worker memory with `ps -o rss= -C php-fpm8.3 | awk '{s+=$1;n++} END {print s/n/1024 " MB avg"}'`. Raising the limit beyond memory causes swapping or OOM kills.

`pm = dynamic` (default) with `pm.start_servers`, `pm.min_spare_servers`, `pm.max_spare_servers`; `pm = ondemand` for low-traffic sites saves memory. `pm.max_requests = 500` recycles workers to contain leaks.

## 502 after a PHP upgrade

The new PHP version uses a new socket path (`php8.3-fpm.sock`) and service name; the web server still points to the old one, or the old FPM service was removed. Update `fastcgi_pass` and reload the web server; make sure the new FPM service is enabled and running.

## Slow requests

Enable the slow log in the pool: `request_slowlog_timeout = 5s`, `slowlog = /var/log/php-fpm-slow.log` — it records stack traces of slow requests. Enable OPcache in production (`opcache.enable=1`).

## Upload and memory limits

`upload_max_filesize`, `post_max_size` (php.ini) and nginx `client_max_body_size` must all allow the upload size. `memory_limit` errors ("Allowed memory size exhausted") appear in the error log.
