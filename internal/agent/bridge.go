//go:build linux

package agent

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

// Bridge connects stdin/stdout to the agent's own port on 127.0.0.1. It is
// the forced command of the SSH key NEXUS uses when the agent port is not
// reachable from the NEXUS PC:
//
//	restrict,command="/usr/local/lib/nexus/nexus-agent bridge 7443" ssh-ed25519 …
//
// "restrict" disables every kind of forwarding, PTYs and ~/.ssh/rc, and the
// forced command means the key can run nothing but this byte pipe. The agent
// on the other end still requires NEXUS's pinned client certificate, so the
// bridge grants no access by itself.
func Bridge(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: nexus-agent bridge <agent port>")
		return 2
	}
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintln(os.Stderr, "nexus-agent bridge: invalid port")
		return 2
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexus-agent bridge: agent not reachable on 127.0.0.1:%d: %v\n", port, err)
		return 1
	}
	defer c.Close()
	go func() {
		_, _ = io.Copy(c, os.Stdin)
		// NEXUS closed its side: let the agent finish and close the connection.
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	_, _ = io.Copy(os.Stdout, c)
	return 0
}
