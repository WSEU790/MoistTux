# Website down: a triage playbook
> A fast, ordered checklist from "the site is down" to the failing layer.
Tags: website down, outage, http, 500, 502, 503, 504, triage, dns, tls, reverse proxy, playbook

## 1. Confirm and characterise (from outside)

* Probe the URL (NEXUS `probe_endpoint`, or `curl -sSI https://site/`): timeout, connection refused, TLS error, or an HTTP status?
* All paths or one? All sites on the server or one? Other servers too (network or provider problem)?
* When did it start? What changed (deploy, update, certificate expiry, DNS change)?

## 2. Name and certificate

* DNS resolves to the right IP (`getent ahosts site` from the server and from outside).
* TLS: `check_ssl_certificate` — expired or wrong certificate gives browser errors while the server is fine.

## 3. Reachability

* Timeout: firewall (ufw, cloud security group) or the server is down/overloaded. Connection refused: nothing listens on 80/443 — the web server is stopped or failed.
* `ss -tulpn | grep -E ':80|:443'` on the server.

## 4. Web server

* `systemctl status nginx` / `apache2`; config test (`nginx -t`); error log.
* Status codes: **502** upstream down or crashing (see nginx runbook); **504** upstream too slow; **503** maintenance mode, rate limiting, or no healthy upstreams; **500** application error (application log); **403/404** permissions or routing.

## 5. Application and dependencies

* App service or container running and healthy (`inspect_service`, `list_docker_containers`).
* Application logs: the first error.
* Database reachable and not overloaded (connections, disk full, slow queries).
* Workers exhausted (PHP-FPM `max_children`, app server thread pools).

## 6. Resources

Disk full, memory exhausted (OOM kills), CPU saturated, inodes, file descriptors ("Too many open files").

## 7. Fix, then verify

Apply the smallest fix (restart a crashed service, free disk space, roll back a deploy, renew a certificate). Verify from outside with the same probe as step 1, watch error logs for a few minutes, and confirm the root cause so it does not repeat.

## Communicate

Note start time, impact, cause, fix and follow-ups. NEXUS's incident timeline and audit log help reconstruct what happened.
