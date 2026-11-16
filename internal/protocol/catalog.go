package protocol

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The command catalog is the set of everyday administration commands the AI
// (and the operator) may run on a server. Each entry is a fixed argv template
// with typed, validated arguments; nothing is passed through a shell. The
// same definitions are checked by NEXUS before sending, by the agent before
// running, and again by the root helper for entries that need root.

// CatalogParam is one argument of a catalog command.
type CatalogParam struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"` // see validators
	Description string   `json:"description"`
	Optional    bool     `json:"optional,omitempty"`
	Multi       bool     `json:"multi,omitempty"` // a list of values (e.g. packages)
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// CatalogCommand is one runnable command.
type CatalogCommand struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	Description string         `json:"description"`
	Argv        []string       `json:"argv"` // literal tokens, "{name}" inside a token, or a whole "{name...}" token for lists
	Params      []CatalogParam `json:"params,omitempty"`
	Root        bool           `json:"root"`
	Risk        string         `json:"risk"` // read | low | medium | high
	TimeoutSec  int            `json:"timeout_sec"`
	Env         []string       `json:"-"`
	Warning     string         `json:"warning,omitempty"` // shown in the approval
}

const (
	aptEnv = "DEBIAN_FRONTEND=noninteractive"
)

var aptQuiet = []string{"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

func apt(args ...string) []string {
	return append(append([]string{"apt-get"}, args[:len(args)-1]...), append(aptQuiet, args[len(args)-1])...)
}

// Catalog is the full list, in display order.
var Catalog = []CatalogCommand{
	// ---- packages (APT)
	{ID: "apt_install", Category: "packages", Description: "Install packages (apt-get install -y).", Argv: apt("install", "-y", "-q", "{packages...}"), Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Root: true, Risk: "medium", TimeoutSec: 1800, Env: []string{aptEnv, "NEEDRESTART_MODE=l"}},
	{ID: "apt_reinstall", Category: "packages", Description: "Reinstall packages (repairs missing or modified package files).", Argv: apt("install", "--reinstall", "-y", "-q", "{packages...}"), Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Root: true, Risk: "medium", TimeoutSec: 1800, Env: []string{aptEnv, "NEEDRESTART_MODE=l"}},
	{ID: "apt_remove", Category: "packages", Description: "Remove packages, keeping their configuration files (apt-get remove -y).", Argv: []string{"apt-get", "remove", "-y", "-q", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Root: true, Risk: "medium", TimeoutSec: 1200, Env: []string{aptEnv}, Warning: "Removing a package can also remove packages that depend on it; check the list in the output of apt_simulate_remove first."},
	{ID: "apt_simulate_remove", Category: "packages", Description: "Show what removing packages would do, without changing anything (apt-get -s remove).", Argv: []string{"apt-get", "-s", "remove", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Risk: "read", TimeoutSec: 120},
	{ID: "apt_simulate_install", Category: "packages", Description: "Show what installing packages would do, without changing anything (apt-get -s install).", Argv: []string{"apt-get", "-s", "install", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Risk: "read", TimeoutSec: 120},
	{ID: "apt_purge", Category: "packages", Description: "Remove packages AND delete their configuration files (apt-get purge -y).", Argv: []string{"apt-get", "purge", "-y", "-q", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Root: true, Risk: "high", TimeoutSec: 1200, Env: []string{aptEnv}, Warning: "Purging deletes the packages' configuration files; they cannot be recovered without a backup."},
	{ID: "apt_autoremove", Category: "packages", Description: "Remove packages that were installed as dependencies and are no longer needed (apt-get autoremove -y).", Argv: []string{"apt-get", "autoremove", "-y", "-q"}, Root: true, Risk: "medium", TimeoutSec: 1200, Env: []string{aptEnv}},
	{ID: "apt_clean", Category: "packages", Description: "Delete downloaded package files from the APT cache (apt-get clean) to free disk space.", Argv: []string{"apt-get", "clean"}, Root: true, Risk: "low", TimeoutSec: 300},
	{ID: "apt_fix_broken", Category: "packages", Description: "Repair broken dependencies (apt-get install -f -y).", Argv: apt("install", "-f", "-y", "-q"), Root: true, Risk: "medium", TimeoutSec: 1800, Env: []string{aptEnv, "NEEDRESTART_MODE=l"}},
	{ID: "dpkg_configure_pending", Category: "packages", Description: "Finish interrupted package installations (dpkg --configure -a).", Argv: []string{"dpkg", "--configure", "-a"}, Root: true, Risk: "medium", TimeoutSec: 1800, Env: []string{aptEnv}},
	{ID: "dpkg_audit", Category: "packages", Description: "List packages that are only partly installed or broken (dpkg --audit).", Argv: []string{"dpkg", "--audit"}, Risk: "read", TimeoutSec: 60},
	{ID: "apt_hold", Category: "packages", Description: "Hold packages at their current version so upgrades skip them (apt-mark hold).", Argv: []string{"apt-mark", "hold", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Root: true, Risk: "low", TimeoutSec: 60},
	{ID: "apt_unhold", Category: "packages", Description: "Release held packages so they upgrade normally (apt-mark unhold).", Argv: []string{"apt-mark", "unhold", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Root: true, Risk: "low", TimeoutSec: 60},
	{ID: "apt_list_held", Category: "packages", Description: "List held packages (apt-mark showhold).", Argv: []string{"apt-mark", "showhold"}, Risk: "read", TimeoutSec: 60},
	{ID: "apt_search", Category: "packages", Description: "Search the package index by name and description (apt-cache search).", Argv: []string{"apt-cache", "search", "--names-only", "{term}"}, Params: []CatalogParam{{Name: "term", Kind: "word", Description: "Search term"}}, Risk: "read", TimeoutSec: 60},
	{ID: "apt_show", Category: "packages", Description: "Show a package's details: version, size, dependencies, description (apt-cache show).", Argv: []string{"apt-cache", "show", "{package}"}, Params: []CatalogParam{{Name: "package", Kind: "package", Description: "Package name"}}, Risk: "read", TimeoutSec: 60},
	{ID: "apt_policy", Category: "packages", Description: "Show installed and candidate versions and which repository provides them (apt-cache policy).", Argv: []string{"apt-cache", "policy", "{packages...}"}, Params: []CatalogParam{{Name: "packages", Kind: "package", Multi: true, Description: "Package names"}}, Risk: "read", TimeoutSec: 60},
	{ID: "dpkg_files", Category: "packages", Description: "List the files a package installed (dpkg -L).", Argv: []string{"dpkg", "-L", "{package}"}, Params: []CatalogParam{{Name: "package", Kind: "package", Description: "Package name"}}, Risk: "read", TimeoutSec: 60},
	{ID: "dpkg_owner", Category: "packages", Description: "Find which package owns a file (dpkg -S).", Argv: []string{"dpkg", "-S", "{path}"}, Params: []CatalogParam{{Name: "path", Kind: "abspath", Description: "Absolute file path"}}, Risk: "read", TimeoutSec: 60},
	{ID: "apt_upgradable", Category: "packages", Description: "List upgradable packages from the current package lists (apt list --upgradable).", Argv: []string{"apt", "list", "--upgradable"}, Risk: "read", TimeoutSec: 120},
	{ID: "apt_sources", Category: "packages", Description: "Show the configured APT repositories (apt-get indextargets would be too verbose; this lists source entries).", Argv: []string{"grep", "-rhE", "^(deb |Types:|URIs:|Suites:|Components:)", "/etc/apt/sources.list", "/etc/apt/sources.list.d"}, Risk: "read", TimeoutSec: 30},
	{ID: "unattended_upgrades_dry_run", Category: "packages", Description: "Show what unattended-upgrades would install, without installing (unattended-upgrade --dry-run).", Argv: []string{"unattended-upgrade", "--dry-run", "-v"}, Root: true, Risk: "read", TimeoutSec: 600},

	// ---- services (systemd)
	{ID: "systemctl_status", Category: "services", Description: "Show a unit's status with its latest log lines (systemctl status).", Argv: []string{"systemctl", "status", "--no-pager", "--lines=30", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name, e.g. nginx.service"}}, Risk: "read", TimeoutSec: 30},
	{ID: "systemctl_cat", Category: "services", Description: "Show a unit's unit file and drop-ins (systemctl cat).", Argv: []string{"systemctl", "cat", "--no-pager", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Risk: "read", TimeoutSec: 30},
	{ID: "systemctl_failed", Category: "services", Description: "List failed units (systemctl --failed).", Argv: []string{"systemctl", "--failed", "--no-pager", "--no-legend"}, Risk: "read", TimeoutSec: 30},
	{ID: "systemctl_timers", Category: "services", Description: "List timers with their next and last run (systemctl list-timers --all).", Argv: []string{"systemctl", "list-timers", "--all", "--no-pager"}, Risk: "read", TimeoutSec: 30},
	{ID: "systemctl_enable", Category: "services", Description: "Enable a unit so it starts at boot (systemctl enable).", Argv: []string{"systemctl", "enable", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Root: true, Risk: "medium", TimeoutSec: 60},
	{ID: "systemctl_enable_now", Category: "services", Description: "Enable a unit at boot and start it now (systemctl enable --now).", Argv: []string{"systemctl", "enable", "--now", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Root: true, Risk: "medium", TimeoutSec: 120},
	{ID: "systemctl_disable", Category: "services", Description: "Stop a unit from starting at boot; does not stop it now (systemctl disable).", Argv: []string{"systemctl", "disable", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Root: true, Risk: "medium", TimeoutSec: 60},
	{ID: "systemctl_disable_now", Category: "services", Description: "Stop a unit now and stop it from starting at boot (systemctl disable --now).", Argv: []string{"systemctl", "disable", "--now", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Root: true, Risk: "medium", TimeoutSec: 120, Warning: "The service becomes unavailable and stays off after reboots."},
	{ID: "systemctl_mask", Category: "services", Description: "Mask a unit so nothing can start it (systemctl mask).", Argv: []string{"systemctl", "mask", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Root: true, Risk: "high", TimeoutSec: 60, Warning: "A masked unit cannot be started, even by its dependencies, until it is unmasked."},
	{ID: "systemctl_unmask", Category: "services", Description: "Unmask a unit (systemctl unmask).", Argv: []string{"systemctl", "unmask", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Root: true, Risk: "low", TimeoutSec: 60},
	{ID: "systemctl_daemon_reload", Category: "services", Description: "Reload systemd unit files after they changed (systemctl daemon-reload).", Argv: []string{"systemctl", "daemon-reload"}, Root: true, Risk: "low", TimeoutSec: 60},
	{ID: "systemctl_reset_failed", Category: "services", Description: "Clear the failed state of units (systemctl reset-failed).", Argv: []string{"systemctl", "reset-failed"}, Root: true, Risk: "low", TimeoutSec: 60},
	{ID: "systemd_analyze_blame", Category: "services", Description: "Show which units took longest at boot (systemd-analyze blame).", Argv: []string{"systemd-analyze", "blame", "--no-pager"}, Risk: "read", TimeoutSec: 60},
	{ID: "systemd_analyze_verify", Category: "services", Description: "Check a unit file for errors (systemd-analyze verify).", Argv: []string{"systemd-analyze", "verify", "{unit}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}}, Risk: "read", TimeoutSec: 60},

	// ---- logs
	{ID: "journal_unit", Category: "logs", Description: "Show a unit's recent journal (journalctl -u).", Argv: []string{"journalctl", "--no-pager", "-o", "short-iso", "-u", "{unit}", "-n", "{lines}"}, Params: []CatalogParam{{Name: "unit", Kind: "unit", Description: "Unit name"}, {Name: "lines", Kind: "count", Description: "Number of lines (1-2000)", Optional: true, Default: "200"}}, Risk: "read", TimeoutSec: 60},
	{ID: "journal_errors_boot", Category: "logs", Description: "Show error-level messages from the current boot (journalctl -b -p err).", Argv: []string{"journalctl", "--no-pager", "-o", "short-iso", "-b", "-p", "err", "-n", "300"}, Risk: "read", TimeoutSec: 60},
	{ID: "journal_previous_boot", Category: "logs", Description: "Show the last messages of the previous boot, useful after a crash or unexpected reboot (journalctl -b -1).", Argv: []string{"journalctl", "--no-pager", "-o", "short-iso", "-b", "-1", "-n", "200"}, Risk: "read", TimeoutSec: 60},
	{ID: "journal_disk_usage", Category: "logs", Description: "Show how much disk the journal uses (journalctl --disk-usage).", Argv: []string{"journalctl", "--disk-usage"}, Risk: "read", TimeoutSec: 30},
	{ID: "journal_vacuum_size", Category: "logs", Description: "Shrink the journal to a size limit by deleting the oldest archived logs (journalctl --vacuum-size).", Argv: []string{"journalctl", "--vacuum-size={size}"}, Params: []CatalogParam{{Name: "size", Kind: "size", Description: "Size to keep, e.g. 500M or 2G"}}, Root: true, Risk: "medium", TimeoutSec: 300, Warning: "Deleted journal entries cannot be recovered."},
	{ID: "journal_vacuum_time", Category: "logs", Description: "Delete archived journal entries older than an age (journalctl --vacuum-time).", Argv: []string{"journalctl", "--vacuum-time={age}"}, Params: []CatalogParam{{Name: "age", Kind: "age", Description: "Age to keep, e.g. 14d, 4weeks, 3months"}}, Root: true, Risk: "medium", TimeoutSec: 300, Warning: "Deleted journal entries cannot be recovered."},
	{ID: "logrotate_debug", Category: "logs", Description: "Check the logrotate configuration without rotating anything (logrotate -d).", Argv: []string{"logrotate", "-d", "/etc/logrotate.conf"}, Root: true, Risk: "read", TimeoutSec: 120},
	{ID: "logrotate_force", Category: "logs", Description: "Rotate all logs now (logrotate -f).", Argv: []string{"logrotate", "-f", "/etc/logrotate.conf"}, Root: true, Risk: "medium", TimeoutSec: 600},
	{ID: "dmesg_errors", Category: "logs", Description: "Show kernel warnings and errors (dmesg -T --level=err,warn): disk, memory, OOM killer, hardware.", Argv: []string{"dmesg", "-T", "--level=emerg,alert,crit,err,warn"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "auth_log_tail", Category: "logs", Description: "Show the last lines of the authentication log (/var/log/auth.log).", Argv: []string{"tail", "-n", "{lines}", "/var/log/auth.log"}, Params: []CatalogParam{{Name: "lines", Kind: "count", Description: "Number of lines (1-2000)", Optional: true, Default: "200"}}, Root: true, Risk: "read", TimeoutSec: 30},

	// ---- disk and filesystems
	{ID: "df", Category: "disk", Description: "Show disk space per filesystem (df -hT).", Argv: []string{"df", "-hT", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs", "-x", "overlay"}, Risk: "read", TimeoutSec: 30},
	{ID: "df_inodes", Category: "disk", Description: "Show inode usage per filesystem (df -i); full inodes stop file creation even with free space.", Argv: []string{"df", "-i", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs", "-x", "overlay"}, Risk: "read", TimeoutSec: 30},
	{ID: "du_dir", Category: "disk", Description: "Show the size of each entry in a directory, one filesystem only (du -xh --max-depth=1).", Argv: []string{"du", "-xh", "--max-depth=1", "{path}"}, Params: []CatalogParam{{Name: "path", Kind: "abspath", Description: "Absolute directory path"}}, Root: true, Risk: "read", TimeoutSec: 600},
	{ID: "lsblk", Category: "disk", Description: "List block devices, partitions, filesystems and mount points (lsblk -f).", Argv: []string{"lsblk", "-f"}, Risk: "read", TimeoutSec: 30},
	{ID: "findmnt", Category: "disk", Description: "Show mounted filesystems and their options (findmnt).", Argv: []string{"findmnt", "--real"}, Risk: "read", TimeoutSec: 30},
	{ID: "fstab", Category: "disk", Description: "Show /etc/fstab.", Argv: []string{"cat", "/etc/fstab"}, Risk: "read", TimeoutSec: 30},
	{ID: "swap_show", Category: "disk", Description: "Show swap devices and use (swapon --show).", Argv: []string{"swapon", "--show"}, Risk: "read", TimeoutSec: 30},
	{ID: "fstrim", Category: "disk", Description: "Discard unused blocks on all mounted filesystems that support it (fstrim -av), for SSDs and thin disks.", Argv: []string{"fstrim", "-av"}, Root: true, Risk: "low", TimeoutSec: 1800},
	{ID: "deleted_open_files", Category: "disk", Description: "Find deleted files still held open by processes (their space is not freed until the process restarts).", Argv: []string{"find", "/proc", "-mindepth", "3", "-maxdepth", "3", "-path", "/proc/*/fd/*", "-lname", "*(deleted)", "-printf", "%p -> %l\\n"}, Root: true, Risk: "read", TimeoutSec: 120},

	// ---- processes and performance
	{ID: "ps_top_cpu", Category: "processes", Description: "Top 30 processes by CPU (ps aux --sort=-%cpu).", Argv: []string{"ps", "-eo", "pid,ppid,user,%cpu,%mem,rss,etime,stat,args", "--sort=-%cpu"}, Risk: "read", TimeoutSec: 30},
	{ID: "ps_top_mem", Category: "processes", Description: "Processes by memory use (ps aux --sort=-%mem).", Argv: []string{"ps", "-eo", "pid,ppid,user,%cpu,%mem,rss,etime,stat,args", "--sort=-%mem"}, Risk: "read", TimeoutSec: 30},
	{ID: "free", Category: "processes", Description: "Show memory and swap (free -h).", Argv: []string{"free", "-h"}, Risk: "read", TimeoutSec: 30},
	{ID: "uptime", Category: "processes", Description: "Show uptime and load averages.", Argv: []string{"uptime"}, Risk: "read", TimeoutSec: 30},
	{ID: "vmstat", Category: "processes", Description: "Sample CPU, memory, swap and I/O activity five times, one second apart (vmstat 1 5).", Argv: []string{"vmstat", "-w", "1", "5"}, Risk: "read", TimeoutSec: 30},
	{ID: "lscpu", Category: "processes", Description: "Show CPU model, cores and virtualization (lscpu).", Argv: []string{"lscpu"}, Risk: "read", TimeoutSec: 30},
	{ID: "kill_term", Category: "processes", Description: "Ask a process to exit cleanly (kill -TERM).", Argv: []string{"kill", "-TERM", "{pid}"}, Params: []CatalogParam{{Name: "pid", Kind: "pid", Description: "Process ID"}}, Root: true, Risk: "medium", TimeoutSec: 30, Warning: "If the process belongs to a service, prefer restarting the service; systemd may restart or mark it failed."},
	{ID: "kill_kill", Category: "processes", Description: "Force-kill a process that ignores TERM (kill -KILL). Last resort.", Argv: []string{"kill", "-KILL", "{pid}"}, Params: []CatalogParam{{Name: "pid", Kind: "pid", Description: "Process ID"}}, Root: true, Risk: "high", TimeoutSec: 30, Warning: "The process gets no chance to save data or clean up."},

	// ---- network
	{ID: "ip_addr", Category: "network", Description: "Show interfaces and addresses (ip -br addr).", Argv: []string{"ip", "-br", "addr"}, Risk: "read", TimeoutSec: 30},
	{ID: "ip_route", Category: "network", Description: "Show the routing table (ip route).", Argv: []string{"ip", "route"}, Risk: "read", TimeoutSec: 30},
	{ID: "ss_listen", Category: "network", Description: "Show listening TCP/UDP sockets with their processes (ss -tulpn).", Argv: []string{"ss", "-tulpn"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "ss_summary", Category: "network", Description: "Show socket counts by state (ss -s).", Argv: []string{"ss", "-s"}, Risk: "read", TimeoutSec: 30},
	{ID: "resolv_status", Category: "network", Description: "Show DNS resolver configuration (resolvectl status).", Argv: []string{"resolvectl", "status", "--no-pager"}, Risk: "read", TimeoutSec: 30},
	{ID: "resolve_host", Category: "network", Description: "Resolve a host name the way the system does (getent ahosts).", Argv: []string{"getent", "ahosts", "{host}"}, Params: []CatalogParam{{Name: "host", Kind: "host", Description: "Host name or IP"}}, Risk: "read", TimeoutSec: 30},
	{ID: "ping", Category: "network", Description: "Ping a host four times (ping -c 4).", Argv: []string{"ping", "-c", "4", "-W", "2", "{host}"}, Params: []CatalogParam{{Name: "host", Kind: "host", Description: "Host name or IP"}}, Risk: "read", TimeoutSec: 30},
	{ID: "tracepath", Category: "network", Description: "Trace the network path to a host (tracepath -n).", Argv: []string{"tracepath", "-n", "-m", "20", "{host}"}, Params: []CatalogParam{{Name: "host", Kind: "host", Description: "Host name or IP"}}, Risk: "read", TimeoutSec: 90},
	{ID: "http_head", Category: "network", Description: "Fetch only the HTTP response headers of a URL from the server (curl -sSI).", Argv: []string{"curl", "-sS", "-I", "-L", "--max-time", "15", "{url}"}, Params: []CatalogParam{{Name: "url", Kind: "url", Description: "http:// or https:// URL"}}, Risk: "read", TimeoutSec: 30},
	{ID: "ufw_status_numbered", Category: "firewall", Description: "Show ufw rules with their numbers (ufw status numbered).", Argv: []string{"ufw", "status", "numbered"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "ufw_allow_port", Category: "firewall", Description: "Allow incoming connections to a port from anywhere (ufw allow PORT/PROTO).", Argv: []string{"ufw", "allow", "{port}/{proto}"}, Params: []CatalogParam{{Name: "port", Kind: "port", Description: "Port number"}, {Name: "proto", Kind: "enum", Enum: []string{"tcp", "udp"}, Description: "Protocol", Optional: true, Default: "tcp"}}, Root: true, Risk: "high", TimeoutSec: 60, Warning: "Opens the port to the whole internet if the server has a public address."},
	{ID: "ufw_allow_from", Category: "firewall", Description: "Allow incoming connections to a port from one address or network only (ufw allow from SRC to any port PORT proto PROTO).", Argv: []string{"ufw", "allow", "from", "{source}", "to", "any", "port", "{port}", "proto", "{proto}"}, Params: []CatalogParam{{Name: "source", Kind: "cidr", Description: "IP address or CIDR network"}, {Name: "port", Kind: "port", Description: "Port number"}, {Name: "proto", Kind: "enum", Enum: []string{"tcp", "udp"}, Description: "Protocol", Optional: true, Default: "tcp"}}, Root: true, Risk: "high", TimeoutSec: 60},
	{ID: "ufw_deny_port", Category: "firewall", Description: "Deny incoming connections to a port (ufw deny PORT/PROTO).", Argv: []string{"ufw", "deny", "{port}/{proto}"}, Params: []CatalogParam{{Name: "port", Kind: "port", Description: "Port number"}, {Name: "proto", Kind: "enum", Enum: []string{"tcp", "udp"}, Description: "Protocol", Optional: true, Default: "tcp"}}, Root: true, Risk: "high", TimeoutSec: 60, Warning: "Denying the SSH port (usually 22) locks you and NEXUS out of the server."},
	{ID: "ufw_delete_rule", Category: "firewall", Description: "Delete a ufw rule by its number from ufw_status_numbered (ufw --force delete N).", Argv: []string{"ufw", "--force", "delete", "{number}"}, Params: []CatalogParam{{Name: "number", Kind: "count", Description: "Rule number"}}, Root: true, Risk: "high", TimeoutSec: 60, Warning: "Rule numbers shift after each deletion; read them again before deleting another. Deleting the rule that allows SSH can lock you out."},
	{ID: "ufw_enable", Category: "firewall", Description: "Turn the ufw firewall on (ufw --force enable).", Argv: []string{"ufw", "--force", "enable"}, Root: true, Risk: "high", TimeoutSec: 60, Warning: "Make sure a rule allows SSH first (ufw_status_numbered); enabling without one locks you and NEXUS out."},
	{ID: "fail2ban_status", Category: "firewall", Description: "Show fail2ban jails, or one jail's banned addresses (fail2ban-client status [jail]).", Argv: []string{"fail2ban-client", "status", "{jail}"}, Params: []CatalogParam{{Name: "jail", Kind: "word", Description: "Jail name, e.g. sshd", Optional: true}}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "fail2ban_unban", Category: "firewall", Description: "Remove a ban for an address in a fail2ban jail (fail2ban-client set JAIL unbanip IP).", Argv: []string{"fail2ban-client", "set", "{jail}", "unbanip", "{ip}"}, Params: []CatalogParam{{Name: "jail", Kind: "word", Description: "Jail name, e.g. sshd"}, {Name: "ip", Kind: "ip", Description: "IP address"}}, Root: true, Risk: "medium", TimeoutSec: 30},

	// ---- users and access
	{ID: "who", Category: "users", Description: "Show who is logged in now (who -a).", Argv: []string{"who", "-a"}, Risk: "read", TimeoutSec: 30},
	{ID: "last_logins", Category: "users", Description: "Show recent logins and reboots (last -n 50 -a).", Argv: []string{"last", "-n", "50", "-a", "-F"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "failed_logins", Category: "users", Description: "Show recent failed logins (lastb -n 50).", Argv: []string{"lastb", "-n", "50", "-a", "-F"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "sudo_group", Category: "users", Description: "List members of the sudo and admin groups (getent group).", Argv: []string{"getent", "group", "sudo", "admin", "wheel"}, Risk: "read", TimeoutSec: 30},
	{ID: "password_aging", Category: "users", Description: "Show a user's password aging and expiry (chage -l).", Argv: []string{"chage", "-l", "{user}"}, Params: []CatalogParam{{Name: "user", Kind: "user", Description: "User name"}}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "lock_user", Category: "users", Description: "Lock a user's password so it cannot be used to log in (usermod -L). SSH keys still work unless the account is also expired.", Argv: []string{"usermod", "-L", "{user}"}, Params: []CatalogParam{{Name: "user", Kind: "user", Description: "User name"}}, Root: true, Risk: "high", TimeoutSec: 30, Warning: "Do not lock the account NEXUS or you use to log in."},
	{ID: "unlock_user", Category: "users", Description: "Unlock a user's password (usermod -U).", Argv: []string{"usermod", "-U", "{user}"}, Params: []CatalogParam{{Name: "user", Kind: "user", Description: "User name"}}, Root: true, Risk: "medium", TimeoutSec: 30},
	{ID: "sshd_test", Category: "users", Description: "Check the SSH server configuration for errors (sshd -t). Run before reloading ssh.", Argv: []string{"sshd", "-t"}, Root: true, Risk: "read", TimeoutSec: 30},

	// ---- time
	{ID: "timedatectl", Category: "time", Description: "Show time, time zone and NTP sync status (timedatectl).", Argv: []string{"timedatectl", "status"}, Risk: "read", TimeoutSec: 30},
	{ID: "timesync_status", Category: "time", Description: "Show time synchronisation details (timedatectl timesync-status).", Argv: []string{"timedatectl", "timesync-status"}, Risk: "read", TimeoutSec: 30},
	{ID: "chrony_sources", Category: "time", Description: "Show chrony's time sources and offsets (chronyc sources -v).", Argv: []string{"chronyc", "sources", "-v"}, Risk: "read", TimeoutSec: 30},
	{ID: "set_timezone", Category: "time", Description: "Set the system time zone (timedatectl set-timezone).", Argv: []string{"timedatectl", "set-timezone", "{zone}"}, Params: []CatalogParam{{Name: "zone", Kind: "timezone", Description: "Time zone, e.g. Europe/Berlin or UTC"}}, Root: true, Risk: "medium", TimeoutSec: 30},
	{ID: "enable_ntp", Category: "time", Description: "Turn on automatic time synchronisation (timedatectl set-ntp true).", Argv: []string{"timedatectl", "set-ntp", "true"}, Root: true, Risk: "low", TimeoutSec: 30},

	// ---- web servers and certificates
	{ID: "nginx_test", Category: "web", Description: "Check the nginx configuration for errors (nginx -t). Run before reloading nginx.", Argv: []string{"nginx", "-t"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "nginx_dump", Category: "web", Description: "Show the complete effective nginx configuration (nginx -T).", Argv: []string{"nginx", "-T"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "apache_test", Category: "web", Description: "Check the Apache configuration for errors (apachectl configtest).", Argv: []string{"apachectl", "configtest"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "apache_vhosts", Category: "web", Description: "List Apache virtual hosts (apachectl -S).", Argv: []string{"apachectl", "-S"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "certbot_list", Category: "web", Description: "List Let's Encrypt certificates with their domains and expiry (certbot certificates).", Argv: []string{"certbot", "certificates"}, Root: true, Risk: "read", TimeoutSec: 60},
	{ID: "certbot_renew_dry_run", Category: "web", Description: "Test certificate renewal without changing anything (certbot renew --dry-run).", Argv: []string{"certbot", "renew", "--dry-run", "--no-random-sleep-on-renew"}, Root: true, Risk: "low", TimeoutSec: 600},
	{ID: "certbot_renew", Category: "web", Description: "Renew certificates that are due (certbot renew).", Argv: []string{"certbot", "renew", "--no-random-sleep-on-renew"}, Root: true, Risk: "medium", TimeoutSec: 900},

	// ---- containers
	{ID: "docker_ps_all", Category: "containers", Description: "List all containers including stopped ones (docker ps -a).", Argv: []string{"docker", "ps", "-a", "--format", "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"}, Root: true, Risk: "read", TimeoutSec: 30},
	{ID: "docker_df", Category: "containers", Description: "Show Docker disk use: images, containers, volumes, build cache (docker system df).", Argv: []string{"docker", "system", "df"}, Root: true, Risk: "read", TimeoutSec: 60},
	{ID: "docker_prune", Category: "containers", Description: "Remove stopped containers, unused networks, dangling images and build cache (docker system prune -f). Volumes are kept.", Argv: []string{"docker", "system", "prune", "-f"}, Root: true, Risk: "medium", TimeoutSec: 900},
	{ID: "docker_image_prune_all", Category: "containers", Description: "Remove all images not used by any container (docker image prune -af).", Argv: []string{"docker", "image", "prune", "-af"}, Root: true, Risk: "medium", TimeoutSec: 900, Warning: "Images will be downloaded again the next time they are needed."},
	{ID: "docker_pull", Category: "containers", Description: "Download or update an image (docker pull).", Argv: []string{"docker", "pull", "{image}"}, Params: []CatalogParam{{Name: "image", Kind: "image", Description: "Image reference, e.g. nginx:1.27"}}, Root: true, Risk: "low", TimeoutSec: 1800},

	// ---- system
	{ID: "os_release", Category: "system", Description: "Show the distribution and version (/etc/os-release).", Argv: []string{"cat", "/etc/os-release"}, Risk: "read", TimeoutSec: 30},
	{ID: "hostnamectl", Category: "system", Description: "Show host name, OS, kernel and virtualization (hostnamectl).", Argv: []string{"hostnamectl"}, Risk: "read", TimeoutSec: 30},
	{ID: "needrestart", Category: "system", Description: "List services and the kernel that need a restart after updates (needrestart -b).", Argv: []string{"needrestart", "-b", "-r", "l"}, Root: true, Risk: "read", TimeoutSec: 120},
	{ID: "sysctl_get", Category: "system", Description: "Read a kernel parameter (sysctl).", Argv: []string{"sysctl", "{key}"}, Params: []CatalogParam{{Name: "key", Kind: "sysctl", Description: "Parameter, e.g. vm.swappiness"}}, Risk: "read", TimeoutSec: 30},
	{ID: "crontab_system", Category: "system", Description: "Show system cron jobs (/etc/crontab and /etc/cron.d).", Argv: []string{"grep", "-rv", "^#", "/etc/crontab", "/etc/cron.d"}, Risk: "read", TimeoutSec: 30},
	{ID: "crontab_user", Category: "system", Description: "Show a user's crontab (crontab -l -u).", Argv: []string{"crontab", "-l", "-u", "{user}"}, Params: []CatalogParam{{Name: "user", Kind: "user", Description: "User name"}}, Root: true, Risk: "read", TimeoutSec: 30},
}

// FindCommand returns a catalog entry.
func FindCommand(id string) (*CatalogCommand, bool) {
	for i := range Catalog {
		if Catalog[i].ID == id {
			return &Catalog[i], true
		}
	}
	return nil, false
}

// CatalogIDs lists every command ID, sorted.
func CatalogIDs() []string {
	ids := make([]string, 0, len(Catalog))
	for _, c := range Catalog {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

var (
	rePkgName  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]{0,127}(:[a-z0-9]+)?(=[A-Za-z0-9.+~:\-]{1,64})?$`)
	reUnitName = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]{1,200}\.(service|socket|timer|target|path|mount|slice|scope)$`)
	reWord     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._\-]{0,63}$`)
	reHostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9\-]{0,62}))*\.?$`)
	reURL      = regexp.MustCompile(`^https?://[A-Za-z0-9.\-]{1,253}(:[0-9]{1,5})?(/[A-Za-z0-9._~%!$&'()*+,;=:@/\-?]*)?$`)
	reSize     = regexp.MustCompile(`^[1-9][0-9]{0,5}[KMG]$`)
	reAge      = regexp.MustCompile(`^[1-9][0-9]{0,3}(d|days|w|weeks|months|years)$`)
	reZone     = regexp.MustCompile(`^(UTC|[A-Z][A-Za-z_\-]{1,30}(/[A-Z][A-Za-z_\-+0-9]{1,30}){1,2})$`)
	reUser     = regexp.MustCompile(`^[a-z_][a-z0-9_.\-]{0,31}$`)
	reSysctl   = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_\-]+){1,6}$`)
	reImage    = regexp.MustCompile(`^[a-z0-9][a-z0-9._\-/:]{0,200}(@sha256:[a-f0-9]{64})?$`)
	reCount    = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)
	rePid      = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
)

func validateValue(p CatalogParam, v string) error {
	if v == "" || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\x00\n\r") {
		return fmt.Errorf("%s: invalid value %q", p.Name, v)
	}
	ok := false
	switch p.Kind {
	case "package":
		ok = rePkgName.MatchString(v)
	case "unit":
		ok = reUnitName.MatchString(v)
	case "word":
		ok = reWord.MatchString(v)
	case "host":
		ok = net.ParseIP(v) != nil || (len(v) <= 253 && reHostName.MatchString(v))
	case "url":
		ok = len(v) <= 2000 && reURL.MatchString(v)
	case "size":
		ok = reSize.MatchString(v)
	case "age":
		ok = reAge.MatchString(v)
	case "timezone":
		ok = reZone.MatchString(v)
	case "user":
		ok = reUser.MatchString(v)
	case "sysctl":
		ok = reSysctl.MatchString(v)
	case "image":
		ok = reImage.MatchString(v) && !strings.Contains(v, "..")
	case "count":
		ok = reCount.MatchString(v)
		if ok && len(v) == 4 && v > "2000" {
			ok = false
		}
	case "pid":
		ok = rePid.MatchString(v) && v != "1"
	case "port":
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		ok = err == nil && fmt.Sprint(n) == v && n >= 1 && n <= 65535
	case "ip":
		ok = net.ParseIP(v) != nil
	case "cidr":
		if net.ParseIP(v) != nil {
			ok = true
		} else if _, n, err := net.ParseCIDR(v); err == nil {
			ones, _ := n.Mask.Size()
			ok = ones > 0 // never "the whole internet" through this command
		}
	case "abspath":
		ok = filepath.IsAbs(v) && filepath.Clean(v) == v && len(v) < 4096
		for _, bad := range []string{"/proc", "/sys", "/dev"} {
			if v == bad || strings.HasPrefix(v, bad+"/") {
				ok = false
			}
		}
	case "enum":
		for _, e := range p.Enum {
			if v == e {
				ok = true
			}
		}
	}
	if !ok {
		return fmt.Errorf("%s: %q is not a valid %s", p.Name, v, p.Kind)
	}
	return nil
}

// Build validates arguments and returns the argv to execute. Every value is
// checked against its parameter's kind, never starts with "-" (so it cannot
// become an option) and is placed into a fixed position of the template.
func (c *CatalogCommand) Build(args map[string][]string) ([]string, error) {
	vals := map[string][]string{}
	for k := range args {
		known := false
		for _, p := range c.Params {
			if p.Name == k {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("%s does not take argument %q", c.ID, k)
		}
	}
	for _, p := range c.Params {
		v := args[p.Name]
		if len(v) == 0 && p.Default != "" {
			v = []string{p.Default}
		}
		if len(v) == 0 {
			if p.Optional {
				continue
			}
			return nil, fmt.Errorf("%s needs argument %q (%s)", c.ID, p.Name, p.Description)
		}
		if !p.Multi && len(v) != 1 {
			return nil, fmt.Errorf("%s: %q takes a single value", c.ID, p.Name)
		}
		if len(v) > 200 {
			return nil, fmt.Errorf("%s: too many values for %q", c.ID, p.Name)
		}
		for _, x := range v {
			if err := validateValue(p, x); err != nil {
				return nil, fmt.Errorf("%s: %w", c.ID, err)
			}
		}
		vals[p.Name] = v
	}
	var argv []string
	for _, tok := range c.Argv {
		if strings.HasPrefix(tok, "{") && strings.HasSuffix(tok, "...}") {
			argv = append(argv, vals[strings.TrimSuffix(strings.TrimPrefix(tok, "{"), "...}")]...)
			continue
		}
		out, skip := tok, false
		for strings.Contains(out, "{") {
			i := strings.Index(out, "{")
			j := strings.Index(out[i:], "}")
			if j < 0 {
				return nil, fmt.Errorf("%s: malformed template", c.ID)
			}
			name := out[i+1 : i+j]
			v, ok := vals[name]
			if !ok {
				skip = true // optional argument not given: drop the token
				break
			}
			out = out[:i] + v[0] + out[i+j+1:]
		}
		if !skip {
			argv = append(argv, out)
		}
	}
	return argv, nil
}

// EncodeArgs and DecodeArgs carry catalog arguments through the root helper's
// argv as name=value pairs (a list repeats its name).
func EncodeArgs(args map[string][]string) []string {
	var keys []string
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		for _, v := range args[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func DecodeArgs(pairs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("malformed argument %q", p)
		}
		out[k] = append(out[k], v)
	}
	return out, nil
}

// CatalogRunRequest is POST /v1/catalog/run.
type CatalogRunRequest struct {
	ID   string              `json:"id"`
	Args map[string][]string `json:"args"`
}

// ShellRunRequest is POST /v1/shell/run: a command approved by a person in
// NEXUS, signed by the core. Token is the JSON ShellGrant, Sig its signature.
type ShellRunRequest struct {
	Token string `json:"token"` // base64 JSON ShellGrant
	Sig   string `json:"sig"`   // base64 ECDSA-SHA256 (ASN.1) signature over the decoded token
}

// SelfUpgradeRequest is POST /v1/self-upgrade: a new agent binary and a grant
// signed by the core for exactly that binary on this server.
type SelfUpgradeRequest struct {
	Binary string `json:"binary"` // base64
	Grant  string `json:"grant"`  // base64 JSON UpgradeGrant
	Sig    string `json:"sig"`
}

// UpgradeGrant authorizes installing one agent binary on one server.
type UpgradeGrant struct {
	V         int    `json:"v"`
	MachineID string `json:"machine_id"`
	SHA256    string `json:"sha256"`
	Version   string `json:"version"`
	Nonce     string `json:"nonce"`
	Expires   int64  `json:"exp"`
}

// ShellGrant authorizes one shell command on one agent.
type ShellGrant struct {
	V          int    `json:"v"`
	MachineID  string `json:"machine_id"` // /etc/machine-id of the target server
	Command    string `json:"command"`
	Nonce      string `json:"nonce"`
	Expires    int64  `json:"exp"` // unix seconds
	TimeoutSec int    `json:"timeout_sec"`
	ApprovedBy string `json:"approved_by"`
}
