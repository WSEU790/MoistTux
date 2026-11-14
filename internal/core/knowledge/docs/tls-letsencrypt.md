# TLS certificates and Let's Encrypt (certbot)
> Checking expiry, renewing certificates, and fixing renewal failures.
Tags: tls, ssl, https, certificate, letsencrypt, certbot, renewal, expiry, acme, openssl

## Checking a certificate

* From outside (what clients see): NEXUS `check_ssl_certificate`, or `openssl s_client -connect example.com:443 -servername example.com </dev/null | openssl x509 -noout -dates -subject -issuer`.
* Certificates managed by certbot: `certbot certificates` lists domains, paths and expiry.
* A certificate file: `openssl x509 -in fullchain.pem -noout -dates -ext subjectAltName`.

In NEXUS: `check_ssl_certificate`, catalog `certbot_list`, `certbot_renew_dry_run`, `certbot_renew`, `nginx_test`, `apache_test`.

## How renewal works

Let's Encrypt certificates last 90 days; certbot renews those within 30 days of expiry. Renewal runs from `certbot.timer` (or snap's timer) twice a day: `systemctl list-timers | grep certbot`. Logs: `/var/log/letsencrypt/letsencrypt.log`.

The web server must load the renewed certificate: the nginx/apache plugins reload automatically; for `--standalone` or `--webroot` setups add a deploy hook, e.g. `/etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh` containing `systemctl reload nginx`.

## Test and renew

1. `certbot renew --dry-run` — tests against the staging server without changing anything.
2. If it passes, `certbot renew` (only renews certificates that are due; `--force-renewal` is rarely needed and counts against rate limits).
3. Reload the web server if no hook does it, then check from outside.

## Common failures

* **Port 80 blocked**: HTTP-01 validation needs port 80 reachable from the internet (firewall, cloud security group). Open it, or use DNS-01.
* **DNS points elsewhere**: the domain's A/AAAA record must point to this server. An AAAA record pointing to an old IPv6 address breaks validation even when IPv4 is right.
* **Webroot mismatch**: the `webroot_path` in `/etc/letsencrypt/renewal/DOMAIN.conf` must be served at `/.well-known/acme-challenge/`. A redirect or auth rule in front of it breaks validation.
* **Port 80 in use** with `--standalone`: stop the web server during renewal via pre/post hooks, or switch to the webroot/nginx plugin.
* **Rate limits**: too many failed attempts or duplicate certificates; wait or use the staging server while debugging.
* **Expired already**: renewal still works the same way; clients see errors until the web server reloads the new certificate.

## Good practice

Use `fullchain.pem`, keep private keys readable by root only (`privkey.pem`, mode 600), monitor expiry (NEXUS alerts you before expiry), and test renewal after any change to the web server or firewall.
