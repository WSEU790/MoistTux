package protocol

import (
	"regexp"
	"strings"
)

// Shell commands proposed by the AI run only after a person approves each
// one. These checks run in NEXUS (before asking for approval) and again in
// the root helper (before running). Blocked commands are refused outright;
// warnings are shown prominently in the approval.

type shellRule struct {
	re  *regexp.Regexp
	msg string
}

var critical = `/(root|home|etc|usr|var|boot|bin|lib|lib32|lib64|sbin|opt|srv|snap)`

var shellBlocks = []shellRule{
	{regexp.MustCompile(`--no-preserve-root`), "deleting the root filesystem"},
	{regexp.MustCompile(`\brm\s+(-{1,2}[A-Za-z-]+\s+)*-[A-Za-z]*[rR][A-Za-z]*\s+(-{1,2}[A-Za-z-]+\s+)*(/|/\*|~/?\*?|\$HOME/?\*?|` + critical + `/?\*?)(\s|$|;|&|\|)`), "recursively deleting the root filesystem, a home directory or a system directory"},
	{regexp.MustCompile(`\bmkfs(\.[a-z0-9]+)?\b|\bmke2fs\b|\bmkswap\s+/dev/`), "formatting a disk"},
	{regexp.MustCompile(`\bwipefs\b|\bblkdiscard\b`), "wiping a disk"},
	{regexp.MustCompile(`\bdd\b[^;&|]*\bof=/dev/(sd|vd|xvd|nvme|hd|mmcblk|dm-|md|mapper/)`), "overwriting a disk device"},
	{regexp.MustCompile(`>\s*/dev/(sd|vd|xvd|nvme|hd|mmcblk|dm-|md)`), "overwriting a disk device"},
	{regexp.MustCompile(`\bshred\b[^;&|]*/dev/`), "shredding a disk device"},
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{`), "a fork bomb"},
	{regexp.MustCompile(`(^|[;&|(]\s*|\b(sudo|exec|nohup|then|do)\s+)(/s?bin/|/usr/s?bin/)?(shutdown|poweroff|halt|reboot)(\s|$|;|&)|(^|[;&|(]\s*|\bsudo\s+)(/s?bin/)?init\s+[06]\b|\bsystemctl\s+(poweroff|halt|reboot|kexec)\b`), "shutting down or rebooting (use the reboot tool, which has its own policy and verification)"},
	{regexp.MustCompile(`\bchmod\s+(-[A-Za-z]+\s+)*[0-7]*7[0-7]{2}\s+/(\s|$)|\bchmod\s+-R\s+\S+\s+(/|` + critical + `)(\s|$)`), "changing permissions of the root filesystem or a system directory"},
	{regexp.MustCompile(`\bchown\s+(-[A-Za-z]+\s+)*-R\s+\S+\s+(/|` + critical + `)(\s|$)`), "changing ownership of the root filesystem or a system directory"},
	{regexp.MustCompile(`\b(userdel|deluser)\s+(-[A-Za-z-]+\s+)*root\b`), "deleting the root account"},
	{regexp.MustCompile(`\bmv\s+(-[A-Za-z]+\s+)*(/|` + critical + `)/?\s`), "moving the root filesystem or a system directory"},
	{regexp.MustCompile(`>\s*/etc/(passwd|shadow|group|sudoers)\b`), "overwriting an account database"},
}

var shellWarnings = []shellRule{
	{regexp.MustCompile(`\brm\s+(-[A-Za-z-]+\s+)*-[A-Za-z]*[rR]`), "Deletes files recursively; deleted files cannot be recovered without a backup."},
	{regexp.MustCompile(`\brm\b`), "Deletes files; check the paths."},
	{regexp.MustCompile(`sshd_config|/etc/ssh/|\bsystemctl\s+(stop|restart|reload|disable)\s+(ssh|sshd)(\.service)?\b`), "Changes SSH. A mistake can lock you and NEXUS out; run sshd -t before reloading."},
	{regexp.MustCompile(`\b(ufw|iptables|ip6tables|nft|firewall-cmd)\b`), "Changes the firewall. Blocking the SSH port locks you and NEXUS out."},
	{regexp.MustCompile(`\b(curl|wget)\b[^;&]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`), "Downloads a script from the internet and runs it as root."},
	{regexp.MustCompile(`\b(apt|apt-get|dpkg)\s+(-[A-Za-z]+\s+)*(purge|remove|autoremove|-P|-r)\b`), "Removes packages, possibly with their configuration and dependent packages."},
	{regexp.MustCompile(`\bkill(all)?\s+(-9|-KILL|-SIGKILL)\b|\bpkill\b`), "Kills processes without letting them clean up."},
	{regexp.MustCompile(`>\s*/etc/|\btee\s+(-a\s+)?/etc/`), "Overwrites or appends to a configuration file under /etc."},
	{regexp.MustCompile(`\bsed\s+(-[A-Za-z]*\s+)*-i`), "Edits a file in place; consider making a copy first."},
	{regexp.MustCompile(`\b(chmod|chown|chgrp|setfacl)\b`), "Changes file permissions or ownership."},
	{regexp.MustCompile(`\b(passwd|usermod|useradd|adduser|userdel|deluser|chpasswd|gpasswd|visudo)\b|/etc/sudoers`), "Changes user accounts or sudo rights."},
	{regexp.MustCompile(`\bdocker\s+(rm|rmi|volume\s+rm|volume\s+prune|system\s+prune|network\s+rm)\b`), "Removes Docker containers, images, volumes or networks."},
	{regexp.MustCompile(`\b(fdisk|sfdisk|sgdisk|gdisk|parted|lvremove|vgremove|pvremove|mdadm|cryptsetup)\b`), "Touches disk partitions, volumes or encryption."},
	{regexp.MustCompile(`\b(truncate)\b|>\s*/var/log/`), "Empties files."},
	{regexp.MustCompile(`\bcrontab\s+-r\b`), "Deletes the whole crontab."},
	{regexp.MustCompile(`\bsystemctl\s+(stop|disable|mask)\b`), "Stops or disables services."},
	{regexp.MustCompile(`\b(mount|umount)\b`), "Mounts or unmounts filesystems."},
	{regexp.MustCompile(`\bsysctl\s+(-w\b|\S+=)`), "Changes kernel parameters at runtime."},
}

func normalizeShell(cmd string) string {
	return strings.Join(strings.Fields(cmd), " ")
}

// ShellBlocked returns why a command is refused outright, or "".
func ShellBlocked(cmd string) string {
	n := normalizeShell(cmd)
	if strings.TrimSpace(n) == "" {
		return "empty command"
	}
	if len(cmd) > 8000 {
		return "the command is longer than 8000 characters"
	}
	if strings.ContainsRune(cmd, 0) {
		return "the command contains a NUL byte"
	}
	for _, r := range shellBlocks {
		if r.re.MatchString(n) {
			return r.msg
		}
	}
	return ""
}

// ShellWarnings lists what an operator should know before approving.
func ShellWarnings(cmd string) []string {
	n := normalizeShell(cmd)
	var out []string
	seen := map[string]bool{}
	for _, r := range shellWarnings {
		if r.re.MatchString(n) && !seen[r.msg] {
			// The generic rm warning is redundant next to the recursive one.
			if r.msg == "Deletes files; check the paths." && len(out) > 0 && strings.HasPrefix(out[0], "Deletes files recursively") {
				continue
			}
			seen[r.msg] = true
			out = append(out, r.msg)
		}
	}
	return out
}
