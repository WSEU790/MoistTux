//go:build linux

package integration

import (
	"os"
	"testing"

	"github.com/WSEU790/MoistTux/internal/agent"
)

// When the in-process agent runs without root it invokes its own binary as
// `sudo -n <binary> helper …`. Under `go test` that binary is this test
// executable, so it must behave like the agent's helper instead of running
// the test suite again.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "helper" {
		os.Exit(agent.HelperMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}
