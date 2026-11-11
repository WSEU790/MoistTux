//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "nexus (desktop) targets Windows 10/11. On this platform run nexus-core and open the printed URL.")
	os.Exit(2)
}
