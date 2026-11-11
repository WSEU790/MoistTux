// Command nexus-core runs the NEXUS core and serves the console on the
// loopback interface. It is the headless counterpart of the Windows desktop
// app (nexus.exe) and is used for development and non-Windows workstations.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/WSEU790/MoistTux/internal/core/app"
)

func main() {
	dataDir := flag.String("data", app.DefaultDataDir(), "data directory")
	listen := flag.String("listen", "127.0.0.1:7780", "loopback address for the console")
	dev := flag.String("dev-origin", "", "allow a UI dev server origin (e.g. http://localhost:5173)")
	tokenFile := flag.String("token-file", "", "write the session token to this file (0600) for scripts and tests")
	flag.Parse()

	unlock, err := app.Lock(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-core:", err)
		os.Exit(1)
	}
	defer unlock()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Start(ctx, app.Options{DataDir: *dataDir, Listen: *listen, DevOrigin: *dev})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-core:", err)
		os.Exit(1)
	}
	if *tokenFile != "" {
		if err := os.WriteFile(*tokenFile, []byte(a.Token), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "nexus-core:", err)
		}
	}
	fmt.Printf("\n  NEXUS console: %s\n\n", a.URL)
	<-ctx.Done()
	_ = a.Close()
}
