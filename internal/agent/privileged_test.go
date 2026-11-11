//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sudo-rs (the default sudo from Ubuntu 25.10) rejects argument wildcards and
// does not know requiretty; a rule using either denies the helper entirely.
func TestSudoersRuleWorksWithSudoRs(t *testing.T) {
	rule := SudoersRule()
	for _, bad := range []string{"*", "Defaults", "requiretty", "ALL=(ALL)", InstallBin + " "} {
		if strings.Contains(rule, bad) {
			t.Errorf("sudoers rule contains %q:\n%s", bad, rule)
		}
	}
	want := ServiceUser + " ALL=(root) NOPASSWD: " + HelperBin + "\n"
	if !strings.HasSuffix(rule, want) {
		t.Errorf("sudoers rule = %q, want it to end with %q", rule, want)
	}
}

func TestNewPrivilegedPrefersDedicatedHelper(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "nexus-agent")
	if err := os.WriteFile(self, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p := NewPrivileged(self); p.Helper != "" {
		t.Fatalf("helper %q chosen although none is installed", p.Helper)
	}
	helper := filepath.Join(dir, filepath.Base(HelperBin))
	if err := os.WriteFile(helper, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p := NewPrivileged(self); p.Helper != helper {
		t.Fatalf("helper = %q, want %q", p.Helper, helper)
	}
}

func TestSudoDeniedRecognisesBothSudos(t *testing.T) {
	for _, msg := range []string{
		"sudo: a password is required",
		"sudo-rs: authentication failed: I'm sorry nexus. I'm afraid I can't do that",
		"sudo: interactive authentication is required",
		"nexus is not in the sudoers file.",
	} {
		if !sudoDenied(msg) {
			t.Errorf("not recognised as a sudo refusal: %q", msg)
		}
	}
	if sudoDenied("E: Could not get lock /var/lib/apt/lists/lock") {
		t.Error("an apt error was treated as a sudo refusal")
	}
}

func TestHelperExecutableName(t *testing.T) {
	if IsHelperExecutable() {
		t.Fatal("the test binary was detected as the privileged helper")
	}
	if filepath.Base(HelperBin) != "nexus-helper" || filepath.Dir(HelperBin) != filepath.Dir(InstallBin) {
		t.Fatalf("helper %s must sit next to %s", HelperBin, InstallBin)
	}
}
