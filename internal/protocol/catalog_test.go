package protocol

import (
	"strings"
	"testing"
)

var sample = map[string]string{
	"package": "nginx", "unit": "nginx.service", "word": "sshd", "host": "example.com", "url": "https://example.com/health",
	"size": "500M", "age": "14d", "timezone": "Europe/Berlin", "user": "deploy", "sysctl": "vm.swappiness", "image": "nginx:1.27",
	"count": "50", "pid": "1234", "port": "443", "ip": "203.0.113.7", "cidr": "203.0.113.0/24", "abspath": "/var/log",
}

func TestEveryCatalogCommandBuilds(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Catalog {
		if seen[c.ID] {
			t.Fatalf("duplicate id %s", c.ID)
		}
		seen[c.ID] = true
		switch c.Risk {
		case "read", "low", "medium", "high":
		default:
			t.Fatalf("%s: bad risk %q", c.ID, c.Risk)
		}
		if c.TimeoutSec <= 0 || c.Description == "" || c.Category == "" {
			t.Fatalf("%s: missing timeout, description or category", c.ID)
		}
		args := map[string][]string{}
		for _, p := range c.Params {
			v := sample[p.Kind]
			if p.Kind == "enum" {
				v = p.Enum[0]
			}
			if v == "" {
				t.Fatalf("%s: no sample for kind %q", c.ID, p.Kind)
			}
			args[p.Name] = []string{v}
		}
		argv, err := c.Build(args)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		for _, a := range argv {
			if strings.Contains(a, "{") && !strings.Contains(c.ID, "docker_ps") {
				t.Fatalf("%s: unexpanded placeholder in %q", c.ID, argv)
			}
		}
	}
	if len(Catalog) < 100 {
		t.Fatalf("catalog has only %d commands", len(Catalog))
	}
}

func TestCatalogRejectsInjection(t *testing.T) {
	c, _ := FindCommand("apt_install")
	for _, bad := range [][]string{{"-o", "APT::Update::Pre-Invoke::=sh"}, {"nginx;rm"}, {"$(id)"}, {"nginx curl"}, {"../x"}, {""}, {"NGINX"}} {
		if _, err := c.Build(map[string][]string{"packages": bad}); err == nil {
			t.Errorf("apt_install accepted %q", bad)
		}
	}
	argv, err := c.Build(map[string][]string{"packages": {"nginx", "certbot", "python3-certbot-nginx"}})
	if err != nil || argv[len(argv)-1] != "python3-certbot-nginx" || argv[0] != "apt-get" {
		t.Fatalf("apt_install argv %q %v", argv, err)
	}
	if _, err := c.Build(map[string][]string{"packages": {"nginx"}, "extra": {"x"}}); err == nil {
		t.Error("unknown argument accepted")
	}
	j, _ := FindCommand("journal_vacuum_size")
	if argv, err := j.Build(map[string][]string{"size": {"500M"}}); err != nil || argv[1] != "--vacuum-size=500M" {
		t.Fatalf("inline placeholder: %q %v", argv, err)
	}
	if _, err := j.Build(map[string][]string{"size": {"500M --rotate"}}); err == nil {
		t.Error("size accepted extra text")
	}
	u, _ := FindCommand("ufw_allow_from")
	if _, err := u.Build(map[string][]string{"source": {"0.0.0.0/0"}, "port": {"22"}}); err == nil {
		t.Error("ufw_allow_from accepted the whole internet as a source")
	}
	if argv, err := u.Build(map[string][]string{"source": {"203.0.113.9"}, "port": {"7443"}}); err != nil || strings.Join(argv, " ") != "ufw allow from 203.0.113.9 to any port 7443 proto tcp" {
		t.Fatalf("ufw_allow_from: %q %v", argv, err)
	}
	k, _ := FindCommand("kill_kill")
	if _, err := k.Build(map[string][]string{"pid": {"1"}}); err == nil {
		t.Error("kill accepted PID 1")
	}
	f, _ := FindCommand("fail2ban_status")
	if argv, _ := f.Build(nil); strings.Join(argv, " ") != "fail2ban-client status" {
		t.Errorf("optional argument not dropped: %q", argv)
	}
	d, _ := FindCommand("du_dir")
	for _, bad := range []string{"/proc", "/sys/kernel", "relative/path", "/var/../etc"} {
		if _, err := d.Build(map[string][]string{"path": {bad}}); err == nil {
			t.Errorf("du_dir accepted %q", bad)
		}
	}
}

func TestEncodeDecodeArgs(t *testing.T) {
	in := map[string][]string{"packages": {"a", "b"}, "unit": {"x.service"}}
	out, err := DecodeArgs(EncodeArgs(in))
	if err != nil || len(out["packages"]) != 2 || out["unit"][0] != "x.service" {
		t.Fatalf("%v %v", out, err)
	}
}

func TestShellBlocks(t *testing.T) {
	blocked := []string{
		"rm -rf /", "rm -rf /*", "sudo rm -fr / --no-preserve-root", "rm -r -f /etc", "rm -rf ~", "rm -Rf /usr/",
		"mkfs.ext4 /dev/sdb1", "dd if=/dev/zero of=/dev/sda bs=1M", "echo x > /dev/nvme0n1", ":(){ :|:& };:",
		"shutdown -h now", "reboot", "systemctl poweroff", "init 0", "chmod -R 777 /", "chown -R nobody /",
		"userdel -r root", "wipefs -a /dev/sdb", "cat x > /etc/shadow", "mv /etc /tmp/etc",
		"apt-get -y upgrade && reboot", "sudo /sbin/shutdown -r +1", "sleep 5; poweroff",
	}
	for _, c := range blocked {
		if ShellBlocked(c) == "" {
			t.Errorf("not blocked: %s", c)
		}
	}
	allowed := []string{
		"apt-get update && apt-get -y install htop", "rm -rf /var/cache/apt/archives/*.deb", "rm -rf /tmp/build",
		"systemctl restart nginx", "df -h / && du -sh /var/log", "journalctl -u nginx -n 50 --no-pager",
		"chmod 640 /etc/nginx/conf.d/site.conf", "dd if=/dev/sda of=/root/mbr.bin bs=512 count=1",
		"find /var/log -name '*.gz' -mtime +30 -delete", "grep -c reboot /var/log/syslog",
		"last -x reboot | head", "journalctl --list-boots | grep shutdown",
	}
	for _, c := range allowed {
		if why := ShellBlocked(c); why != "" {
			t.Errorf("wrongly blocked (%s): %s", why, c)
		}
	}
}

func TestShellWarnings(t *testing.T) {
	cases := map[string]string{
		"rm -rf /tmp/build": "recursively",
		"sed -i 's/^#Port 22/Port 2222/' /etc/ssh/sshd_config": "SSH",
		"ufw deny 22": "firewall",
		"curl -fsSL https://get.example.com | sh": "internet",
		"apt-get purge -y apache2":                "Removes packages",
		"echo 'x' > /etc/hosts":                   "/etc",
	}
	for cmd, want := range cases {
		w := strings.Join(ShellWarnings(cmd), " | ")
		if !strings.Contains(w, want) {
			t.Errorf("%q: warnings %q lack %q", cmd, w, want)
		}
	}
	if w := ShellWarnings("df -h"); len(w) != 0 {
		t.Errorf("df -h warned: %v", w)
	}
}
