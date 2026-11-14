# Users, groups and sudo
> Creating and removing accounts, sudo rights, locking access and auditing who can do what.
Tags: users, accounts, sudo, sudoers, groups, adduser, usermod, passwd, lock, audit, uid 0

## Accounts

* Create a user with a home directory: `adduser alice` (interactive) or `useradd -m -s /bin/bash alice`.
* Give admin rights: `usermod -aG sudo alice` (Debian/Ubuntu; `wheel` on RHEL). `-a` is essential — without it the user is removed from other groups.
* Lock the password: `usermod -L alice` (SSH keys still work). Disable entirely: `usermod -L -e 1 alice` (expired account) or `chsh -s /usr/sbin/nologin alice`.
* Remove: `deluser --remove-home alice` — check for files, cron jobs and running processes first (`find / -xdev -user alice`, `crontab -l -u alice`, `ps -u alice`).
* Password aging: `chage -l alice`; force a change at next login: `chage -d 0 alice`.

In NEXUS: `inspect_system_users`, `security_posture`, catalog `sudo_group`, `password_aging`, `lock_user`, `unlock_user`, `who`, `last_logins`, `failed_logins`.

## sudo

* Rules live in `/etc/sudoers` and `/etc/sudoers.d/*`. Always edit with `visudo` (or `visudo -f /etc/sudoers.d/FILE`), which validates syntax — a broken sudoers file removes sudo for everyone.
* Check a user's rights: `sudo -l -U alice`.
* Limit a rule to specific commands: `deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart myapp.service`. Wildcards in arguments are risky and not supported by sudo-rs (the default sudo on Ubuntu 25.10+).
* Logging: sudo use appears in `journalctl _COMM=sudo` and `/var/log/auth.log`.

NEXUS's agent uses one sudoers rule, `/etc/sudoers.d/nexus-agent`, that allows only `/usr/local/lib/nexus/nexus-helper`. Do not widen it.

## Auditing access

Check regularly:

* accounts with UID 0 other than root: `awk -F: '$3==0' /etc/passwd`;
* members of sudo/admin/wheel: `getent group sudo admin wheel`;
* accounts with a login shell: `grep -vE 'nologin|false' /etc/passwd`;
* authorized SSH keys: `~/.ssh/authorized_keys` for each user (including root);
* recent logins: `last -a`, failed ones: `lastb`.

Unknown UID-0 accounts, unexpected sudo members or unknown keys are signs of compromise.

## Service accounts

Services should run as dedicated system users without login shells (`useradd --system --no-create-home --shell /usr/sbin/nologin svc`). Never run network services as root unless required; never give service accounts sudo.
