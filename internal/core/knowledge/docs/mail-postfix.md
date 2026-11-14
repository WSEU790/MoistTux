# Mail: Postfix basics and outgoing mail problems
> Queue inspection, delivery errors, relays and why mail from servers ends up in spam.
Tags: mail, email, postfix, smtp, queue, mailq, relay, spf, dkim, dmarc, port 25

## Inspecting

* Queue: `mailq` (or `postqueue -p`); flush: `postqueue -f`; delete one message: `postsuper -d QUEUEID`; delete all deferred: `postsuper -d ALL deferred` (destroys mail — only for spam floods).
* Logs: `journalctl -u postfix@-` or `/var/log/mail.log`. Each message has a queue ID; follow it through the log.
* Config check: `postfix check`; effective settings: `postconf -n`.

## Common delivery errors

* `Connection timed out` to port 25 — many cloud providers block outgoing port 25; use an authenticated relay (submission port 587) such as your mail provider or a transactional mail service (`relayhost = [smtp.example.com]:587` with SASL).
* `550 ... SPF`, `DMARC policy` rejections — the sending domain's DNS does not authorise this server. Publish SPF (`v=spf1 ip4:SERVER_IP include:... -all`), sign with DKIM (opendkim), set a DMARC record.
* `Relay access denied` — the server does not relay for that client/domain (correct for an open-relay-safe setup).
* Messages in spam: missing SPF/DKIM/DMARC, no reverse DNS (PTR) matching the host name, IP on a blocklist.

## Disk and spam floods

A compromised web application or open relay can fill the queue with thousands of messages (inode or disk exhaustion, blocklisting). Stop the source first (the script or account sending), then clean the queue.

## Local-only mail

Servers often only need to send system mail (cron, alerts). Configure Postfix as "satellite"/null client with a relay, listening on localhost only (`inet_interfaces = loopback-only`).
