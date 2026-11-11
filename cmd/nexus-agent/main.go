//go:build linux

// Command nexus-agent is the lightweight NEXUS service for managed Linux hosts.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent"
)

func usage() {
	fmt.Fprintf(os.Stderr, `nexus-agent %s — NEXUS infrastructure agent

Usage:
  nexus-agent install [--listen :7443] [--docker] [--interval 5s]   install as a systemd service (root)
  nexus-agent enroll-code [--state-dir DIR]                         issue a new single-use enrollment code
  nexus-agent clients [--state-dir DIR]                             list enrolled NEXUS cores
  nexus-agent revoke --all [--state-dir DIR]                        revoke every enrolled core
  nexus-agent run [--listen :7443] [--state-dir DIR] [--interval 5s] run in the foreground
  nexus-agent uninstall [--purge]                                   remove the service (root)
  nexus-agent version
`, agent.Version)
}

func main() {
	// Started as /usr/local/lib/nexus/nexus-helper (normally through sudo):
	// run the validated privileged helper and nothing else.
	if agent.IsHelperExecutable() {
		os.Exit(agent.HelperMain(os.Args[1:]))
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "helper":
		os.Exit(agent.HelperMain(args))
	case "version", "--version":
		fmt.Println(agent.Version)
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		listen := fs.String("listen", agent.DefaultListen, "address to listen on")
		dir := fs.String("state-dir", agent.DefaultState, "state directory")
		interval := fs.Duration("interval", 5*time.Second, "telemetry sampling interval")
		docker := fs.String("docker-socket", "/var/run/docker.sock", "Docker Engine socket")
		_ = fs.Parse(args)
		a, err := agent.New(agent.Config{Listen: *listen, StateDir: *dir, Interval: *interval, DockerSocket: *docker})
		if err != nil {
			fatal(err)
		}
		if len(a.State().Clients()) == 0 {
			fmt.Fprintln(os.Stderr, "no NEXUS core is enrolled yet; issuing an enrollment code:")
			if err := agent.PrintEnrollment(os.Stderr, *dir, *listen); err != nil {
				fatal(err)
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := a.Serve(ctx); err != nil {
			fatal(err)
		}
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		listen := fs.String("listen", agent.DefaultListen, "address to listen on")
		docker := fs.Bool("docker", false, "grant Docker access (adds the service user to the docker group, which is root-equivalent)")
		interval := fs.Duration("interval", 5*time.Second, "telemetry sampling interval")
		_ = fs.Parse(args)
		if err := agent.Install(agent.InstallOptions{Listen: *listen, Docker: *docker, Interval: *interval, Out: os.Stdout}); err != nil {
			fatal(err)
		}
	case "enroll-code":
		fs := flag.NewFlagSet("enroll-code", flag.ExitOnError)
		dir := fs.String("state-dir", agent.DefaultState, "state directory")
		listen := fs.String("listen", agent.DefaultListen, "listen address (display only)")
		_ = fs.Parse(args)
		if err := agent.PrintEnrollment(os.Stdout, *dir, *listen); err != nil {
			fatal(err)
		}
	case "clients":
		fs := flag.NewFlagSet("clients", flag.ExitOnError)
		dir := fs.String("state-dir", agent.DefaultState, "state directory")
		_ = fs.Parse(args)
		st, err := agent.LoadOrCreateState(*dir)
		if err != nil {
			fatal(err)
		}
		for _, c := range st.Clients() {
			fmt.Printf("%s…  %-40s enrolled %s  last seen %s\n", c.Fingerprint[:16], c.Label, c.EnrolledAt.Format(time.RFC3339), c.LastSeen.Format(time.RFC3339))
		}
	case "revoke":
		fs := flag.NewFlagSet("revoke", flag.ExitOnError)
		dir := fs.String("state-dir", agent.DefaultState, "state directory")
		all := fs.Bool("all", false, "revoke every enrolled core and pending code")
		_ = fs.Parse(args)
		if !*all {
			fatal(fmt.Errorf("specify --all"))
		}
		st, err := agent.LoadOrCreateState(*dir)
		if err != nil {
			fatal(err)
		}
		if err := st.RevokeAll(); err != nil {
			fatal(err)
		}
		fmt.Println("all clients revoked; restart is not required")
	case "uninstall":
		fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
		purge := fs.Bool("purge", false, "also delete identity, state and the service user")
		_ = fs.Parse(args)
		if err := agent.Uninstall(os.Stdout, *purge); err != nil {
			fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "nexus-agent:", err)
	os.Exit(1)
}
