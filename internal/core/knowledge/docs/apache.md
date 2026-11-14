# Apache HTTP Server
> Managing sites and modules on Debian/Ubuntu, safe reloads, and common errors.
Tags: apache, apache2, httpd, a2ensite, a2enmod, vhost, configtest, 500, 403, mpm, php

## Layout (Debian/Ubuntu)

* `/etc/apache2/apache2.conf`, sites in `sites-available/` enabled with `a2ensite NAME` (disable: `a2dissite`), modules with `a2enmod`/`a2dismod`, configuration snippets with `a2enconf`.
* Logs: `/var/log/apache2/error.log`, `access.log`, plus per-vhost logs.
* Test: `apachectl configtest` ("Syntax OK"). List vhosts and which is default: `apachectl -S`.
* Apply: `systemctl reload apache2` (graceful).

In NEXUS: catalog `apache_test`, `apache_vhosts`, `certbot_list`, plus `reload_service` and `get_service_logs`.

## Common problems

* **AH00558: Could not reliably determine the server's fully qualified domain name** — harmless warning; set `ServerName` in a conf snippet.
* **Address already in use: AH00072** — another program (often nginx) holds port 80/443: `ss -tulpn | grep -E ':80|:443'`.
* **403 Forbidden** — `Require all granted` missing for the directory, file permissions for `www-data`, or `Options -Indexes` with no index file.
* **500 Internal Server Error** — read `error.log`: `.htaccess` errors (a directive for a module that is not enabled, e.g. `RewriteEngine` without `mod_rewrite`), PHP fatal errors, permission problems.
* **Wrong site served** — the first vhost for an IP:port is the default; check `apachectl -S` and `ServerName`/`ServerAlias`.

## MPM and PHP

* `mpm_event` with PHP-FPM (`proxy_fcgi`) is the modern setup; `mpm_prefork` with `mod_php` is legacy and uses much more memory.
* Too many processes / memory exhaustion: tune `MaxRequestWorkers` to fit RAM (memory per worker × workers < available RAM).
* "server reached MaxRequestWorkers setting" in the error log means requests are queueing.

## Safe change procedure

Back up the file, edit, `apachectl configtest`, `systemctl reload apache2`, test with a request and watch `error.log`.
