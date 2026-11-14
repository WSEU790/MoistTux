# SSH server hardening and lockout-safe changes
> Secure sshd settings, key-only logins, and a procedure for changing SSH without losing access.
Tags: ssh, sshd, sshd_config, hardening, keys, password authentication, root login, lockout, fail2ban, security

## Safe change procedure

1. Keep the current SSH session open the whole time.
2. Change the configuration in a drop-in: `/etc/ssh/sshd_config.d/50-hardening.conf` (Ubuntu 22.04+ includes this directory; files are read in order and the **first** value set wins).
3. Test the syntax: `sshd -t` (no output means OK). Never reload with errors.
4. Reload: `systemctl reload ssh` (Ubuntu service name is `ssh`; on RHEL-like systems `sshd`). Existing sessions survive a reload.
5. From a second terminal, open a **new** session and confirm you can still log in and use sudo.
6. Only then close the old session. If the new login fails, revert the change from the old session.

In NEXUS: `inspect_ssh_configuration` (effective config via `sshd -T`), `inspect_failed_logins`, `security_posture`, catalog `sshd_test`, `failed_logins`, `last_logins`, `fail2ban_status`. NEXUS never changes SSH configuration automatically; changes go through an approved shell command.

## Recommended settings

```
PermitRootLogin prohibit-password      # or "no" when admins use sudo
PasswordAuthentication no              # only after keys work for every admin
KbdInteractiveAuthentication no
PubkeyAuthentication yes
MaxAuthTries 4
LoginGraceTime 30
X11Forwarding no
AllowUsers deploy alice                # optional: explicit allowlist
ClientAliveInterval 300
ClientAliveCountMax 2
```

Check the effective result with `sshd -T | grep -Ei 'permitrootlogin|passwordauthentication|pubkey'` — drop-ins and `Match` blocks can override the main file. Cloud images often ship `/etc/ssh/sshd_config.d/50-cloud-init.conf` with `PasswordAuthentication yes`; it wins over later files.

Before disabling passwords, confirm every administrator (and NEXUS deployment, if it uses a password) has a working key. NEXUS's SSH tunnel uses its own restricted key and is unaffected.

## Keys

* Generate on the client: `ssh-keygen -t ed25519`.
* Install: `ssh-copy-id user@server`, or append the public key to `~/.ssh/authorized_keys`.
* Permissions matter (StrictModes): `~/.ssh` 700, `authorized_keys` 600, home directory not group/world-writable. Wrong permissions make sshd ignore the keys silently; the reason appears in `journalctl -u ssh`.
* Restrict automation keys with options such as `restrict,command="..."` or `from="203.0.113.0/24"`.

## Changing the port

Changing the SSH port reduces log noise but is not real security. If you do it: allow the new port in the firewall first, update `Port` (on Ubuntu 22.10+ SSH may be socket-activated: also check `ssh.socket` and use `systemctl daemon-reload && systemctl restart ssh.socket`), test a new login on the new port, then remove the old rule.

## Brute-force attempts

Internet-facing SSH sees constant login attempts; with key-only authentication they cannot succeed. Look at `lastb`, `journalctl -u ssh` ("Failed password", "Invalid user"). fail2ban reduces noise; key-only authentication is what actually protects you. A successful login from an unknown address is an incident (see the incident response runbook).

## Host keys

Host keys identify the server (`/etc/ssh/ssh_host_*_key`). If they change unexpectedly (clients warn "REMOTE HOST IDENTIFICATION HAS CHANGED"), find out why before connecting: reinstallation, a new server at the same IP, or interception. Regenerate only deliberately: `ssh-keygen -A` after removing old keys, then update clients' known_hosts and NEXUS's saved host key.
