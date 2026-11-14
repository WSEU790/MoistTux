# Networking and DNS troubleshooting
> Interfaces, routes, DNS with systemd-resolved, netplan, sockets and a step-by-step connectivity ladder.
Tags: network, networking, dns, resolv, systemd-resolved, netplan, ip, route, ss, ports, connectivity, mtu, ping

## The connectivity ladder

Work from the bottom up and stop at the first step that fails:

1. **Link and address**: `ip -br addr` — is the interface UP with the expected IP?
2. **Route**: `ip route` — is there a default route? `ip route get 8.8.8.8` shows which interface and gateway are used.
3. **Gateway**: `ping -c 3 GATEWAY`.
4. **Internet by IP**: `ping -c 3 1.1.1.1`.
5. **DNS**: `getent ahosts example.com` (uses the system resolver like applications do); `resolvectl query example.com`.
6. **TCP to the service**: `curl -sSI --max-time 10 https://example.com`, or `nc -zv HOST PORT`.
7. **Firewalls in between**: local (ufw/nftables), cloud security groups, the remote host's firewall.

ICMP (ping) is often blocked; a failed ping alone does not prove a host is down — test the actual TCP port.

In NEXUS: `get_network_metrics`, `inspect_open_ports`, `probe_endpoint`, catalog `ip_addr`, `ip_route`, `ss_listen`, `ss_summary`, `resolv_status`, `resolve_host`, `ping`, `tracepath`, `http_head`.

## Listening sockets

`ss -tulpn` lists listening TCP/UDP sockets and their processes (root needed for process names).

* `0.0.0.0:80` or `[::]:80` — listens on all interfaces; reachable from outside if the firewall allows it.
* `127.0.0.1:3306` — only from the server itself. Good for databases and admin services.
* A service that "is running" but not reachable: check that it listens on the right address and port, then the firewall.
* "Address already in use" at startup: `ss -tulpn | grep :PORT` shows who holds the port.

## DNS with systemd-resolved (Ubuntu)

`/etc/resolv.conf` usually points to the local stub `127.0.0.53`; the real upstream servers are shown by `resolvectl status`. Configure DNS servers in netplan (or `/etc/systemd/resolved.conf`), not by editing `/etc/resolv.conf` directly (it is a symlink that gets regenerated).

* Flush the cache: `resolvectl flush-caches`.
* A name resolves on your PC but not on the server: compare upstream servers; check `/etc/hosts` overrides; split-horizon DNS.
* Slow DNS makes everything slow (SSH logins, apt, HTTP clients). Time `getent ahosts` to check.

## netplan (Ubuntu)

Network configuration lives in `/etc/netplan/*.yaml`. Apply changes safely with `netplan try` (rolls back after 120 s unless confirmed) — essential on remote servers, since a mistake cuts you off. `netplan apply` applies without rollback. Cloud servers often generate netplan from cloud-init (`/etc/netplan/50-cloud-init.yaml`); persistent changes may need cloud-init configuration instead.

## Connection states and limits

`ss -s` summarises sockets. Many `TIME_WAIT` is normal for busy clients. Many `CLOSE_WAIT` means the application does not close connections (a bug). `SYN_RECV` floods suggest a SYN flood or a slow accept loop. The kernel log line `nf_conntrack: table full` means the connection-tracking table is full.

## MTU problems

Symptoms: small requests work, large ones hang (SSH works, `scp` or HTTPS downloads stall), often over VPNs or tunnels. Test with `ping -M do -s 1472 HOST` (1472 + 28 = 1500 bytes); lower the size until it works and set the interface MTU accordingly.

## Firewalls and cloud security groups

A port can be blocked in three places: the server's firewall (ufw/nftables), the cloud provider's security group or firewall, and NAT on the client side. If `ss` shows the service listening on `0.0.0.0` and the local firewall allows it but outside clients still time out, check the provider's firewall. A timeout usually means a firewall dropped the packets; "connection refused" means the host answered but nothing listens on that port.

## Hostname

`hostnamectl` shows and sets the host name (`hostnamectl set-hostname NAME`); keep `/etc/hosts` consistent (`127.0.1.1 NAME`) or sudo prints "unable to resolve host".
