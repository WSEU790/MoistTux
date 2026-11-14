# Suspected compromise: first response
> Signs of intrusion, evidence-preserving triage, containment and when to rebuild.
Tags: security, incident, compromise, hacked, intrusion, malware, cryptominer, backdoor, forensics, rootkit

## Warning signs

* Unknown processes with high CPU (cryptominers), odd names or running from `/tmp`, `/dev/shm`, `/var/tmp`.
* Successful logins from unknown IPs (`last -a`), new users or UID-0 accounts, unknown keys in `authorized_keys`.
* Unexpected cron jobs or systemd units, modified binaries, outbound connections to unusual hosts, disabled logging, the agent or monitoring killed.

In NEXUS: `security_posture`, `inspect_failed_logins`, `inspect_system_users`, `list_processes`, `inspect_process`, `inspect_open_ports`, catalog `last_logins`, `failed_logins`, `who`, `ss_listen`, `crontab_system`, `crontab_user`, `systemctl_timers`, `deleted_open_files`.

## Principles

1. **Do not destroy evidence**: do not reboot, do not delete suspicious files, do not kill processes before recording what they are (unless they are actively causing damage).
2. Assume the attacker may have root: tools on the server can lie. Prefer evidence from outside (provider console, network logs, NEXUS history).
3. Containment beats cleaning: a compromised server should normally be rebuilt from known-good sources, not "cleaned".

## Triage (read-only)

```
who -a; last -a -n 50; lastb -n 50
ps -eo pid,ppid,user,etime,%cpu,args --sort=-%cpu | head -30
ls -l /proc/PID/exe /proc/PID/cwd        # where a process really runs from
ss -tupn                                # established connections with processes
ss -tulpn                               # listeners
awk -F: '$3==0' /etc/passwd
getent group sudo
for u in /root /home/*; do ls -la $u/.ssh 2>/dev/null; done
crontab -l -u root; ls -la /etc/cron.* /var/spool/cron/crontabs
systemctl list-units --type=service --state=running
find /etc/systemd/system -newer /etc/hostname -type f
journalctl -u ssh --since "-2 days" | grep -E 'Accepted|session opened'
debsums -s 2>/dev/null                  # modified package files (package debsums)
```

Record the outputs (NEXUS keeps tool results and the audit log).

## Containment

* Isolate: restrict network access at the provider's firewall (allow only your admin IP), or snapshot the disk and then disconnect.
* Revoke credentials that may be exposed: SSH keys and passwords of users on the server, API keys and database passwords in application configs, cloud credentials. Rotate them from a clean machine.
* Preserve: take a disk snapshot before changes if forensics may be needed.

## Recovery

Rebuild the server from a clean image, deploy applications from version control, restore data from a backup taken **before** the compromise (verify it), harden (key-only SSH, firewall, updates), and monitor closely. Find the entry point (outdated web application, weak password, leaked key) or it will happen again.

## Reporting

Depending on the data involved, there may be legal notification duties (e.g. GDPR: 72 hours). Involve the data owner early.
