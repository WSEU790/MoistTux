# nginx: configuration and troubleshooting
> Config layout, safe reloads, and fixing 502/504/403/404 errors, upstream problems and TLS issues.
Tags: nginx, web server, reverse proxy, 502, 504, 403, 404, bad gateway, upstream, tls, ssl, php-fpm, proxy_pass

## Layout (Debian/Ubuntu)

* Main config `/etc/nginx/nginx.conf`; sites in `/etc/nginx/sites-available/` enabled by symlinks in `/etc/nginx/sites-enabled/`; snippets in `/etc/nginx/conf.d/` and `/etc/nginx/snippets/`.
* Logs: `/var/log/nginx/access.log` and `error.log` (per-site logs if configured).
* Test configuration: `nginx -t`. Full effective configuration: `nginx -T`.
* Apply: `systemctl reload nginx` (graceful, keeps connections). Restart only if reload is not enough.

In NEXUS: `inspect_service`, `get_service_logs`, `probe_endpoint`, `check_ssl_certificate`, `reload_service`, catalog `nginx_test`, `nginx_dump`, `certbot_list`, `ss_listen`.

## Safe change procedure

1. Back up the file you change (`cp site.conf site.conf.bak-$(date +%F)`).
2. Edit, then `nginx -t`. Fix every error and read warnings ("conflicting server name" means two server blocks claim the same name).
3. `systemctl reload nginx`.
4. Verify with a request (`curl -sSI https://site/`) and watch `error.log` for a minute.

## 502 Bad Gateway

nginx could not get a valid response from the upstream (app server, PHP-FPM, container).

1. `error.log` tells you which: `connect() failed (111: Connection refused)` — upstream not listening; `upstream prematurely closed connection` — upstream crashed mid-request; `no live upstreams`; `(13: Permission denied) while connecting to upstream` — socket permissions or SELinux.
2. Check the upstream service is running and listening where `proxy_pass` / `fastcgi_pass` points (`ss -tulpn`, `ls -l /run/php/`).
3. PHP-FPM socket mismatch after a PHP upgrade (e.g. `php8.1-fpm.sock` vs `php8.3-fpm.sock`) is a classic cause.
4. Upstream out of workers or memory — check its logs and the OOM killer.

## 504 Gateway Timeout

The upstream accepted the connection but did not answer in time (`upstream timed out (110)`). Find out why the backend is slow (database, external API, overloaded workers). Raising `proxy_read_timeout` only hides the problem; do it only for endpoints that are legitimately slow.

## 403 and 404

* 403: file permissions (nginx's user `www-data` needs execute permission on every parent directory and read on files), a `deny` rule, or a directory without an index file and `autoindex off`.
* 404: wrong `root`/`alias`, or the request matched a different `server` block than you think — check `server_name` and which block is `default_server`; `nginx -T | grep -n server_name` helps.

## TLS

* Certificates are usually from Let's Encrypt (`/etc/letsencrypt/live/DOMAIN/fullchain.pem` and `privkey.pem`).
* Check expiry and chain from outside: `check_ssl_certificate` in NEXUS, or `openssl s_client -connect site:443 -servername site`.
* Use `fullchain.pem`, not `cert.pem`, or clients that lack the intermediate fail.
* After renewal nginx must reload to load the new certificate (certbot's nginx plugin or a deploy hook does it).

## Performance and limits

* `worker_processes auto;` and `worker_connections` (default 768 on Ubuntu) bound concurrent connections; "worker_connections are not enough" in the log means raise it (and the open-files limit: `LimitNOFILE` in a systemd drop-in).
* Enable gzip for text, set sensible `client_max_body_size` (413 Request Entity Too Large means it is too small for uploads).

## Typical reverse proxy block

```
location / {
    proxy_pass http://127.0.0.1:3000;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;     # websockets
    proxy_set_header Connection "upgrade";
}
```
