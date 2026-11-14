package knowledge

import (
	"strings"
	"testing"
)

func TestLibraryLoads(t *testing.T) {
	lib, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	docs, sections := lib.Stats()
	if docs < 20 || sections < 120 {
		t.Fatalf("library has %d runbooks and %d sections", docs, sections)
	}
	for _, d := range lib.Docs() {
		full, _ := lib.Get(d.ID)
		if d.Summary == "" || len(d.Tags) < 3 || len(full.Sections) < 3 {
			t.Errorf("%s: summary, tags or sections missing", d.ID)
		}
	}
}

func TestSearchFindsTheRightRunbook(t *testing.T) {
	lib, _ := Load()
	cases := map[string]string{
		"nginx 502 bad gateway":                          "nginx",
		"no space left on device":                        "disk-space",
		"journal using too much disk":                    "logs-journald",
		"Could not get lock /var/lib/dpkg/lock-frontend": "apt-packages",
		"process killed out of memory":                   "memory-oom",
		"locked out after enabling ufw":                  "firewall-ufw",
		"disable password authentication ssh":            "ssh-hardening",
		"certbot renewal fails port 80":                  "tls-letsencrypt",
		"container keeps restarting exit code 137":       "docker",
		"server reached pm.max_children":                 "php-fpm",
		"too many connections mysql":                     "mysql-mariadb",
		"pg_hba.conf entry":                              "postgresql",
		"reboot required after kernel update":            "kernel-reboots",
		"cryptominer unknown process high cpu":           "security-incident",
		"high iowait load average":                       "cpu-load-performance",
		"cron job does not run":                          "cron-scheduling",
		"clock not synchronized":                         "time-sync",
		"filesystem remounted read-only":                 "storage-filesystems",
		"website down 503":                               "website-down",
		"systemd service failed start-limit-hit":         "systemd-services",
	}
	for q, want := range cases {
		hits := lib.Search(q, 3)
		if len(hits) == 0 {
			t.Errorf("%q: no results", q)
			continue
		}
		found := false
		for _, h := range hits {
			if h.DocID == want {
				found = true
			}
		}
		if !found {
			var got []string
			for _, h := range hits {
				got = append(got, h.DocID+"/"+h.Section)
			}
			t.Errorf("%q: want %s in top 3, got %v", q, want, got)
		}
	}
}

func TestRead(t *testing.T) {
	lib, _ := Load()
	text, err := lib.Read("nginx", "502", 0)
	if err != nil || !strings.Contains(text, "502 Bad Gateway") {
		t.Fatalf("read section: %v %.80s", err, text)
	}
	if _, err := lib.Read("nginx", "no such section", 0); err == nil || !strings.Contains(err.Error(), "sections:") {
		t.Fatalf("missing section error: %v", err)
	}
	if _, err := lib.Read("nope", "", 0); err == nil {
		t.Fatal("unknown runbook")
	}
	if text, _ := lib.Read("disk-space", "", 500); len(text) > 600 {
		t.Fatal("max length not applied")
	}
}
