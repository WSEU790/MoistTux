# Firewall: ufw, nftables and iptables
> Changing firewall rules without locking yourself out, common ufw tasks, and how Docker bypasses ufw.
Tags: firewall, ufw, iptables, nftables, ports, allow, deny, lockout, docker, fail2ban, security group

## The golden rule

Before enabling a firewall or changing rules on a remote server, make sure SSH stays allowed — and keep your current session open until you have verified that a **new** SSH login works. Locking yourself out of a cloud server means console access or a rescue system.

NEXUS itself reaches the agent either on port 7443 or through SSH (port 22): blocking the one in use disconnects NEXUS too.

In NEXUS: `inspect_firewall`, catalog `ufw_status_numbered`, `ufw_allow_port`, `ufw_allow_from`, `ufw_deny_port`, `ufw_delete_rule`, `ufw_enable`, `fail2ban_status`, `fail2ban_unban`. Firewall changes are high risk and always need approval.

## ufw basics

```
ufw status verbose            # on/off, defaults, rules
ufw status numbered           # rules with numbers (for delete)
ufw allow OpenSSH             # or: ufw allow 22/tcp
ufw allow 80,443/tcp
ufw allow from 203.0.113.0/24 to any port 5432 proto tcp
ufw deny 3306/tcp
ufw delete 3                  # by number; numbers shift after each delete
ufw --force enable
ufw reload
```

Safe enable sequence on a fresh server: `ufw default deny incoming`, `ufw default allow outgoing`, `ufw allow OpenSSH` (or your SSH port), then `ufw --force enable`, then test a new SSH login.

Prefer `allow from <your IP or network>` over opening admin ports (SSH, databases, 7443) to the whole internet. Application profiles: `ufw app list` (e.g. "Nginx Full", "OpenSSH").

Logging: `ufw logging low` writes blocked packets to the kernel log (`[UFW BLOCK]` lines) — useful to see what is being dropped.

## Docker bypasses ufw

Ports published by Docker (`-p 8080:80`) are added to iptables `DOCKER` chains **before** ufw's rules, so they are reachable from the internet even when ufw denies them. Options:

* publish only on localhost (`-p 127.0.0.1:8080:80`) and put a reverse proxy in front;
* use the `DOCKER-USER` chain to filter;
* or use the cloud provider's firewall.

Always verify exposure from outside the server, not only with `ufw status`.

## nftables and iptables

Ubuntu 22.04+ uses nftables underneath; ufw and iptables commands are front ends.

* List everything: `nft list ruleset` or `iptables-save`.
* Rules added directly with `iptables` are not persistent unless saved (`netfilter-persistent save`) and may conflict with ufw — pick one tool per server.
* Debugging drops: counters in `nft list ruleset` / `iptables -L -v -n` show which rules match.

## fail2ban

fail2ban bans IPs that fail logins repeatedly (sshd jail by default on many setups).

* Status: `fail2ban-client status`, `fail2ban-client status sshd`.
* Unban yourself: `fail2ban-client set sshd unbanip 203.0.113.7`.
* Whitelist trusted networks with `ignoreip` in `/etc/fail2ban/jail.local`.

## Cloud firewalls

AWS security groups, Azure NSGs, GCP firewall rules, Hetzner/OVH/DigitalOcean firewalls filter traffic before it reaches the server. When a port is open in ufw and the service listens but outside clients time out, check the provider's firewall.

## Verify

After any change: `ufw status numbered`, test the affected service from outside (or from NEXUS with `probe_endpoint`), and open a fresh SSH session.
